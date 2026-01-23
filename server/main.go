package main

import (
	"cursortab/logger"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type CursorPredictionConfig struct {
	Enabled       bool `json:"enabled"`
	AutoAdvance   bool `json:"auto_advance"`
	DistThreshold int  `json:"dist_threshold"`
}

type Config struct {
	NsID                   int                    `json:"ns_id"`
	Provider               string                 `json:"provider"`
	IdleCompletionDelay    int                    `json:"idle_completion_delay"` // in milliseconds
	TextChangeDebounce     int                    `json:"text_change_debounce"`  // in milliseconds
	CompletionTimeout      int                    `json:"completion_timeout"`    // in milliseconds
	CursorPrediction       CursorPredictionConfig `json:"cursor_prediction"`
	ProviderURL            string                 `json:"provider_url"`
	ProviderModel          string                 `json:"provider_model"`
	ProviderTemperature    float64                `json:"provider_temperature"`
	ProviderMaxTokens      int                    `json:"provider_max_tokens"`
	ProviderTopK           int                    `json:"provider_top_k"`
	MaxContextTokens       int                    `json:"max_context_tokens"` // max tokens for context trimming
	LogLevel               string                 `json:"log_level"`          // debug, info, warn, error
	DebugImmediateShutdown bool                   `json:"debug_immediate_shutdown"`
}

type ServerMode string

const (
	ModeDaemon ServerMode = "daemon"
	ModeClient ServerMode = "client"
)

// Setup logger to log to a file in the same directory as the executable
// Caller must defer logger.Close()
func setupLogger(logLevel string) *logger.LimitedLogger {
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("error getting executable path: %v", err)
	}
	execDir := filepath.Dir(execPath)
	logPath := filepath.Join(execDir, "cursortab.log")

	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		logger.Fatal("error opening file: %v", err)
	}

	level := logger.ParseLogLevel(logLevel)
	return logger.NewLimitedLogger(f, level)
}

func getSocketPath() string {
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("error getting executable path: %v", err)
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, "cursortab.sock")
}

func getPidPath() string {
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("error getting executable path: %v", err)
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, "cursortab.pid")
}

func isDaemonRunning() (bool, int) {
	pidPath := getPidPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false, 0
	}

	// Check if process is still running
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// On Unix, Signal(0) checks if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil, pid
}

func loadConfig() Config {
	var config Config
	if err := json.Unmarshal([]byte(os.Getenv("CURSORTAB_CONFIG")), &config); err != nil {
		logger.Fatal("invalid config: %v", err)
	}

	logger.Info("config: %+v", config)
	return config
}

func runDaemon() {
	// Setup logger early with default level
	ll := setupLogger("info")
	defer ll.Close()

	config := loadConfig()

	// Update log level based on config
	if config.LogLevel != "" {
		logger.SetGlobalLevel(logger.ParseLogLevel(config.LogLevel))
	}

	daemon, err := NewDaemon(config)
	if err != nil {
		logger.Fatal("error creating daemon: %v", err)
	}

	if err := daemon.Start(); err != nil {
		logger.Fatal("error starting daemon: %v", err)
	}
}

func runClient() {
	client := NewClient()

	if err := client.EnsureDaemonRunning(); err != nil {
		logger.Fatal("error ensuring daemon is running: %v", err)
	}

	if err := client.Connect(); err != nil {
		logger.Fatal("error connecting to daemon: %v", err)
	}
}

func main() {
	var mode ServerMode = ModeClient

	// Check command line arguments
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		mode = ModeDaemon
	}

	switch mode {
	case ModeDaemon:
		runDaemon()
	case ModeClient:
		runClient()
	}
}
