package main

import (
	"context"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"cursortab/engine"
	"cursortab/logger"
	"cursortab/provider"
	"cursortab/types"

	"github.com/neovim/go-client/nvim"
)

type Daemon struct {
	config      Config
	provider    types.Provider
	engine      *engine.Engine
	listener    net.Listener
	socketPath  string
	pidPath     string
	clientCount int64
	shutdown    chan bool
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewDaemon(config Config) (*Daemon, error) {
	provider, err := provider.NewProvider(
		types.ProviderType(config.Provider.Type),
		&types.ProviderConfig{
			MaxTokens:           config.Provider.MaxContextTokens,
			ProviderURL:         config.Provider.URL,
			ProviderModel:       config.Provider.Model,
			ProviderTemperature: config.Provider.Temperature,
			ProviderMaxTokens:   config.Provider.MaxTokens,
			ProviderTopK:        config.Provider.TopK,
			ProviderAPIKey:      config.Provider.APIKey,
		},
	)
	if err != nil {
		return nil, err
	}

	eng, err := engine.NewEngine(provider, engine.EngineConfig{
		NsID:                config.NsID,
		CompletionTimeout:   time.Duration(config.Provider.CompletionTimeout) * time.Millisecond,
		IdleCompletionDelay: time.Duration(config.Behavior.IdleCompletionDelay) * time.Millisecond,
		TextChangeDebounce:  time.Duration(config.Behavior.TextChangeDebounce) * time.Millisecond,
		CursorPrediction: engine.CursorPredictionConfig{
			Enabled:       config.Behavior.CursorPrediction.Enabled,
			AutoAdvance:   config.Behavior.CursorPrediction.AutoAdvance,
			DistThreshold: config.Behavior.CursorPrediction.DistThreshold,
		},
		MaxDiffTokens: config.Provider.MaxDiffHistoryTokens,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Daemon{
		config:     config,
		provider:   provider,
		engine:     eng,
		socketPath: getSocketPath(),
		pidPath:    getPidPath(),
		shutdown:   make(chan bool, 1),
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

func (d *Daemon) Start() error {
	// Setup logging and PID management
	d.writePidFile()
	defer d.removePidFile()

	// Setup socket
	if err := d.setupSocket(); err != nil {
		return err
	}
	defer d.cleanup()

	logger.Info("daemon listening on socket: %s", d.socketPath)

	// Start engine
	d.engine.Start(d.ctx)

	// Setup shutdown handling
	d.setupShutdownHandling()

	// Start connection handling
	go d.acceptConnections()

	// Start idle monitoring
	go d.monitorIdleShutdown()

	// Wait for shutdown
	<-d.ctx.Done()
	logger.Info("daemon shutting down...")
	return nil
}

func (d *Daemon) setupSocket() error {
	// Remove existing socket
	os.Remove(d.socketPath)

	// Listen on Unix socket
	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	return nil
}

func (d *Daemon) setupShutdownHandling() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("received shutdown signal")
		d.Stop()
	}()
}

func (d *Daemon) acceptConnections() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return // Server is shutting down
			default:
				logger.Error("error accepting connection: %v", err)
				continue
			}
		}

		atomic.AddInt64(&d.clientCount, 1)
		logger.Info("new client connected, total clients: %d", atomic.LoadInt64(&d.clientCount))
		go d.handleConnection(conn)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	defer func() {
		atomic.AddInt64(&d.clientCount, -1)
		logger.Info("client disconnected, remaining clients: %d", atomic.LoadInt64(&d.clientCount))
	}()

	// Create Neovim client from the connection
	n, err := nvim.New(conn, conn, conn, logger.Debug)
	if err != nil {
		logger.Error("error creating nvim client: %v", err)
		return
	}

	// Set nvim instance for this connection
	d.engine.SetNvim(n)

	// Serve this connection until it closes or context is done
	select {
	case <-d.ctx.Done():
		return
	default:
		if err := n.Serve(); err != nil && err != io.EOF {
			logger.Error("error serving connection: %v", err)
		}
	}
}

func (d *Daemon) monitorIdleShutdown() {
	// In debug mode, shut down immediately when no clients are connected
	if d.config.Debug.ImmediateShutdown {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
				if atomic.LoadInt64(&d.clientCount) == 0 {
					logger.Debug("debug mode: no clients connected, shutting down daemon immediately")
					d.Stop()
					return
				}
			}
		}
	} else {
		// Normal mode: wait for timeout period before shutting down
		idleTimer := time.NewTimer(30 * time.Second)
		defer idleTimer.Stop()

		for {
			select {
			case <-d.ctx.Done():
				return
			case <-idleTimer.C:
				if atomic.LoadInt64(&d.clientCount) == 0 {
					logger.Info("no clients connected for timeout period, shutting down daemon")
					d.Stop()
					return
				}
			}

			// Reset timer when no clients
			if atomic.LoadInt64(&d.clientCount) == 0 {
				idleTimer.Reset(5 * time.Second)
			} else {
				idleTimer.Reset(30 * time.Second)
			}
		}
	}
}

func (d *Daemon) Stop() {
	d.engine.Stop()
	if d.listener != nil {
		d.listener.Close()
	}
	d.cancel()
}

func (d *Daemon) cleanup() {
	os.Remove(d.socketPath)
}

func (d *Daemon) writePidFile() {
	pid := os.Getpid()
	err := os.WriteFile(d.pidPath, []byte(strconv.Itoa(pid)), 0644)
	if err != nil {
		logger.Warn("could not write PID file: %v", err)
	}
	logger.Info("server started with PID %d", pid)
}

func (d *Daemon) removePidFile() {
	if err := os.Remove(d.pidPath); err != nil && !os.IsNotExist(err) {
		logger.Warn("could not remove PID file: %v", err)
	}
}
