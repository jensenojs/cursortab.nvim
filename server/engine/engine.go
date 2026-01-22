package engine

import (
	"context"
	"cursortab/text"
	"cursortab/types"
	"cursortab/utils"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/neovim/go-client/nvim"
)

type state int

const (
	stateIdle state = iota
	statePendingCompletion
	stateHasCompletion
	stateHasCursorTarget
)

type EngineConfig struct {
	NsID                int
	CompletionTimeout   time.Duration
	IdleCompletionDelay time.Duration
	TextChangeDebounce  time.Duration
	MaxDiffTokens       int // Maximum tokens for diff history per file (0 = no limit)
}

type Engine struct {
	WorkspacePath string
	WorkspaceID   string

	provider        types.Provider
	n               *nvim.Nvim
	buffer          *text.Buffer
	state           state
	ctx             context.Context
	currentCancel   context.CancelFunc
	prefetchCancel  context.CancelFunc
	idleTimer       *time.Timer
	textChangeTimer *time.Timer
	mu              sync.RWMutex
	eventChan       chan Event

	// Main context and cancel for the engine lifecycle
	mainCtx    context.Context
	mainCancel context.CancelFunc
	stopped    bool
	stopOnce   sync.Once

	// Completion state
	completions  []*types.Completion
	applyBatch   *nvim.Batch
	cursorTarget *types.CursorPredictionTarget

	// Original buffer lines when completion was shown (for partial typing optimization)
	completionOriginalLines []string

	// Prefetch state
	prefetchedCompletions   []*types.Completion
	prefetchedCursorTarget  *types.CursorPredictionTarget
	prefetchInProgress      bool
	waitingForPrefetchOnTab bool

	// Config options
	config EngineConfig

	// Per-file cumulative diff histories within the current workspace
	fileDiffStore map[string][]*types.DiffEntry
}

func NewEngine(provider types.Provider, config EngineConfig) (*Engine, error) {
	workspacePath, err := os.Getwd()
	if err != nil {
		log.Printf("error getting current directory, using home: %v", err)
		workspacePath = "~"
	}
	workspaceID := fmt.Sprintf("%s-%d", workspacePath, os.Getpid())

	buffer, err := text.NewBuffer(text.BufferConfig{
		NsID: config.NsID,
	})
	if err != nil {
		return nil, err
	}

	return &Engine{
		WorkspacePath:           workspacePath,
		WorkspaceID:             workspaceID,
		provider:                provider,
		n:                       nil, // Will be set later via SetNvim
		buffer:                  buffer,
		state:                   stateIdle,
		ctx:                     nil,
		eventChan:               make(chan Event, 100),
		config:                  config,
		idleTimer:               nil,
		textChangeTimer:         nil,
		mu:                      sync.RWMutex{},
		completions:             nil,
		cursorTarget:            nil,
		prefetchedCompletions:   nil,
		prefetchedCursorTarget:  nil,
		prefetchInProgress:      false,
		waitingForPrefetchOnTab: false,
		stopped:                 false,
		fileDiffStore:           make(map[string][]*types.DiffEntry),
	}, nil
}

func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}

	// Create main context for engine lifecycle
	e.mainCtx, e.mainCancel = context.WithCancel(ctx)
	e.mu.Unlock()

	go e.eventLoop(e.mainCtx)
	log.Printf("engine started")
}

// Stop gracefully shuts down the engine and cleans up all resources
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		log.Printf("stopping engine...")

		// Mark as stopped to prevent new operations
		e.stopped = true
		// Cancel main context to stop event loop
		if e.mainCancel != nil {
			e.mainCancel()
		}
		// Cancel any pending operations
		if e.currentCancel != nil {
			e.currentCancel()
			e.currentCancel = nil
		}
		if e.prefetchCancel != nil {
			e.prefetchCancel()
			e.prefetchCancel = nil
		}
		// Stop idle timer
		e.stopIdleTimer()
		// Stop text change timer
		e.stopTextChangeTimer()
		// Clear any pending completions/predictions
		e.clearStateUnsafe()
		// Close event channel (this will cause eventLoop to exit if it hasn't already)
		close(e.eventChan)

		log.Printf("engine stopped")
	})
}

// clearStateUnsafe clears engine state without locking (internal use)
func (e *Engine) clearStateUnsafe() {
	e.state = stateIdle
	e.completions = nil
	e.applyBatch = nil
	e.cursorTarget = nil
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchInProgress = false
	e.waitingForPrefetchOnTab = false
	e.completionOriginalLines = nil
}

func (e *Engine) eventLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("event loop panic recovered: %v", r)
			e.eventLoop(e.mainCtx) // Restart the event loop
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Clean shutdown when context is cancelled
			return
		case event, ok := <-e.eventChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}

			// Check if we're stopped before processing
			e.mu.RLock()
			stopped := e.stopped
			e.mu.RUnlock()

			if stopped {
				return
			}

			// Wrap event handling in its own recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("event handler panic recovered for event %v: %v", event.Type, r)
					}
				}()
				e.handleEvent(event)
			}()
		}
	}
}

func (e *Engine) handleEvent(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check we're not stopped while holding the lock
	if e.stopped {
		return
	}

	log.Printf("handle event: %v", event)

	switch event.Type {
	case EventEsc:
		e.handleEsc()
	case EventTextChanged:
		e.handleTextChange()
	case EventTextChangeTimeout:
		e.handleTextChangeTimeout()
	case EventCursorMovedNormal:
		e.handleCursorMoveNormal()
	case EventInsertEnter:
		e.handleInsertEnter()
	case EventInsertLeave:
		e.handleInsertLeave()
	case EventTab:
		e.handleTab()
	case EventIdleTimeout:
		e.handleIdleTimeout()
	case EventCompletionReady:
		e.handleCompletionReady(event.Data.(*types.CompletionResponse))
	case EventCompletionError:
		log.Printf("completion error: %v", event.Data)
	case EventPrefetchReady:
		resp := event.Data.(*types.CompletionResponse)
		e.prefetchedCompletions = resp.Completions
		e.prefetchedCursorTarget = resp.CursorTarget
		e.prefetchInProgress = false

		// If we were waiting for prefetch due to tab press, continue with cursor target logic
		if e.waitingForPrefetchOnTab {
			e.waitingForPrefetchOnTab = false
			e.handleDeferredCursorTarget()
		}
	case EventPrefetchError:
		log.Printf("prefetch error: %v", event.Data)
		e.prefetchInProgress = false

		// If we were waiting for prefetch due to tab press, fall back to original logic
		if e.waitingForPrefetchOnTab {
			e.waitingForPrefetchOnTab = false
			e.handleDeferredCursorTarget()
		}
	}
}

func (e *Engine) clearCompletionState() {
	if e.currentCancel != nil {
		e.currentCancel()
		e.currentCancel = nil
	}
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
	}
	if e.n != nil {
		e.buffer.OnReject(e.n)
	}
	e.completions = nil
	e.applyBatch = nil
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchInProgress = false
	e.waitingForPrefetchOnTab = false
	e.completionOriginalLines = nil
}

// clearCompletionStateExceptPrefetch clears the currently completion without affecting prefetched data
func (e *Engine) clearCompletionStateExceptPrefetch() {
	if e.currentCancel != nil {
		e.currentCancel()
		e.currentCancel = nil
	}
	if e.n != nil {
		e.buffer.OnReject(e.n)
	}
	e.completions = nil
	e.applyBatch = nil
	e.completionOriginalLines = nil
}

func (e *Engine) reject() {
	e.clearCompletionState()
	e.state = stateIdle
	e.cursorTarget = nil
}

func (e *Engine) requestCompletion(source types.CompletionSource) {
	// Check if stopped before making request
	if e.stopped || e.n == nil {
		return
	}

	e.state = statePendingCompletion
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.currentCancel = cancel

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, &types.CompletionRequest{
			Source:            source,
			WorkspacePath:     e.WorkspacePath,
			WorkspaceID:       e.WorkspaceID,
			FilePath:          e.buffer.Path,
			Lines:             e.buffer.Lines,
			Version:           e.buffer.Version,
			PreviousLines:     e.buffer.PreviousLines,
			FileDiffHistories: e.getAllFileDiffHistories(),
			CursorRow:         e.buffer.Row,
			CursorCol:         e.buffer.Col,
			LinterErrors:      e.buffer.GetProviderLinterErrors(e.n),
		})

		if err != nil {
			select {
			case e.eventChan <- Event{Type: EventCompletionError, Data: err}:
			case <-e.mainCtx.Done():
			}
			return
		}

		select {
		case e.eventChan <- Event{Type: EventCompletionReady, Data: result}:
		case <-e.mainCtx.Done():
		}
	}()
}

// requestPrefetch requests a completion for a specific cursor position without changing the engine state
func (e *Engine) requestPrefetch(source types.CompletionSource, overrideRow int, overrideCol int) {
	if e.stopped || e.n == nil {
		return
	}

	// Cancel existing prefetch if any
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchInProgress = false
	}

	// Sync buffer to ensure latest context
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.prefetchCancel = cancel
	e.prefetchInProgress = true

	// Snapshot required values to avoid races with buffer mutation
	lines := append([]string{}, e.buffer.Lines...)
	previousLines := append([]string{}, e.buffer.PreviousLines...)
	version := e.buffer.Version
	// legacy per-file diff history removed in favor of multi-file store
	filePath := e.buffer.Path
	linterErrors := e.buffer.GetProviderLinterErrors(e.n)

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, &types.CompletionRequest{
			Source:            source,
			WorkspacePath:     e.WorkspacePath,
			WorkspaceID:       e.WorkspaceID,
			FilePath:          filePath,
			Lines:             lines,
			Version:           version,
			PreviousLines:     previousLines,
			FileDiffHistories: e.getAllFileDiffHistories(),
			CursorRow:         overrideRow,
			CursorCol:         overrideCol,
			LinterErrors:      linterErrors,
		})

		if err != nil {
			select {
			case e.eventChan <- Event{Type: EventPrefetchError, Data: err}:
			case <-e.mainCtx.Done():
			}
			return
		}

		select {
		case e.eventChan <- Event{Type: EventPrefetchReady, Data: result}:
		case <-e.mainCtx.Done():
		}
	}()
}

func (e *Engine) handleCursorTarget() {
	if e.cursorTarget != nil {
		e.state = stateHasCursorTarget
		lineNumber := e.cursorTarget.LineNumber
		if lineNumber >= 1 {
			e.buffer.OnCursorPredictionReady(e.n, int(lineNumber))
		} else {
			e.reject()
		}
	} else {
		e.reject()
	}
}

func (e *Engine) acceptCompletion() {
	if e.applyBatch != nil {
		if err := e.applyBatch.Execute(); err != nil {
			log.Printf("error applying completion: %v", err)
		}
	}

	// Commit pending file changes only after successful apply
	e.buffer.CommitPendingEdit()

	// After commit, update per-file diff store for the current file
	if e.buffer.Path != "" && len(e.buffer.DiffHistories) > 0 {
		// Copy slice to avoid aliasing
		diffs := make([]*types.DiffEntry, len(e.buffer.DiffHistories))
		copy(diffs, e.buffer.DiffHistories)
		e.fileDiffStore[e.buffer.Path] = diffs

		// Keep at most two files in file histories
		e.trimFileDiffStoreToMaxFiles(2)
	}

	e.clearCompletionStateExceptPrefetch()

	// Prefetch next completion if cursor target requests retrigger (after applying current completion)
	if e.cursorTarget != nil && e.cursorTarget.ShouldRetrigger {
		// Sync buffer to get the updated state after applying completion
		e.buffer.SyncIn(e.n, e.WorkspacePath)

		// Prefetch targeting the predicted cursor line
		overrideRow := max(1, int(e.cursorTarget.LineNumber))
		e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
	}

	e.handleCursorTarget()
}

// trimFileDiffStoreToMaxFiles keeps only the most recent maxFiles files in the diff store
func (e *Engine) trimFileDiffStoreToMaxFiles(maxFiles int) {
	if len(e.fileDiffStore) <= maxFiles {
		return
	}

	// Convert to slice for sorting by some criteria (e.g., file name for deterministic behavior)
	type fileEntry struct {
		fileName string
		diffs    []*types.DiffEntry
	}

	var entries []fileEntry
	for fileName, diffs := range e.fileDiffStore {
		entries = append(entries, fileEntry{fileName, diffs})
	}

	// Sort by file name to ensure deterministic behavior
	// In a real implementation, you might want to sort by last access time
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].fileName > entries[j].fileName {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Keep only the first maxFiles entries
	entriesToKeep := entries
	if len(entries) > maxFiles {
		entriesToKeep = entries[:maxFiles]
	}

	// Rebuild the map with only the kept entries
	newFileDiffStore := make(map[string][]*types.DiffEntry)
	for _, entry := range entriesToKeep {
		newFileDiffStore[entry.fileName] = entry.diffs
	}

	e.fileDiffStore = newFileDiffStore
}

// getAllFileDiffHistories returns all known file diff histories in provider format
func (e *Engine) getAllFileDiffHistories() []*types.FileDiffHistory {
	if len(e.fileDiffStore) == 0 {
		return nil
	}
	histories := make([]*types.FileDiffHistory, 0, len(e.fileDiffStore))
	for fileName, diffs := range e.fileDiffStore {
		if len(diffs) == 0 {
			continue
		}
		// Copy to ensure immutability
		copyDiffs := make([]*types.DiffEntry, len(diffs))
		copy(copyDiffs, diffs)

		// Apply token limiting if configured
		if e.config.MaxDiffTokens > 0 {
			copyDiffs = utils.TrimDiffEntries(copyDiffs, e.config.MaxDiffTokens)
		}

		if len(copyDiffs) == 0 {
			continue
		}

		histories = append(histories, &types.FileDiffHistory{
			FileName:    fileName,
			DiffHistory: copyDiffs,
		})
	}
	if len(histories) == 0 {
		return nil
	}
	return histories
}

func (e *Engine) acceptCursorTarget() {
	if e.n == nil || e.cursorTarget == nil {
		return
	}

	err := e.buffer.MoveCursorToStartOfLine(e.n, int(e.cursorTarget.LineNumber), true, true)
	if err != nil {
		log.Printf("error moving cursor: %v", err)
	}

	if e.n != nil {
		e.buffer.OnReject(e.n)
	}

	if len(e.prefetchedCompletions) > 0 {
		e.state = stateHasCompletion
		e.completions = e.prefetchedCompletions
		e.cursorTarget = e.prefetchedCursorTarget
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil

		if e.buffer.HasChanges(e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines) {
			e.applyBatch = e.buffer.OnCompletionReady(e.n, e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines)
		} else {
			log.Printf("no changes to completion (prefetched)")
			e.handleCursorTarget()
		}

		return
	}

	// If prefetch is in progress, wait for it to complete instead of triggering new request
	if e.prefetchInProgress {
		e.waitingForPrefetchOnTab = true
		return
	}

	if e.cursorTarget.ShouldRetrigger {
		e.requestCompletion(types.CompletionSourceTyping)
		e.state = stateIdle
		e.cursorTarget = nil
		return
	}

	e.state = stateIdle
	e.cursorTarget = nil
}

// handleDeferredCursorTarget handles cursor target logic that was deferred due to prefetch in progress
func (e *Engine) handleDeferredCursorTarget() {
	if e.n == nil || e.cursorTarget == nil {
		return
	}

	// Check if we now have prefetched completions
	if len(e.prefetchedCompletions) > 0 {
		e.state = stateHasCompletion
		e.completions = e.prefetchedCompletions
		e.cursorTarget = e.prefetchedCursorTarget
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil

		if e.buffer.HasChanges(e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines) {
			e.applyBatch = e.buffer.OnCompletionReady(e.n, e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines)
		} else {
			log.Printf("no changes to completion (deferred prefetched)")
			e.handleCursorTarget()
		}

		return
	}

	// Fall back to original behavior - trigger new completion if needed
	if e.cursorTarget.ShouldRetrigger {
		e.requestCompletion(types.CompletionSourceTyping)
		e.state = stateIdle
		e.cursorTarget = nil
		return
	}

	e.state = stateIdle
	e.cursorTarget = nil
}

// SetNvim sets a new nvim instance for the engine (used for socket connections)
func (e *Engine) SetNvim(n *nvim.Nvim) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Don't change if stopped
	if e.stopped {
		return
	}

	e.n = n

	// Re-register the event handler for the new connection
	if err := e.n.RegisterHandler("cursortab_event", func(n *nvim.Nvim, event string) {
		e.mu.RLock()
		stopped := e.stopped
		e.mu.RUnlock()

		if stopped {
			return
		}

		eventType := EventTypeFromString(event)
		if eventType != "" {
			select {
			case e.eventChan <- Event{Type: eventType, Data: nil}:
			case <-e.mainCtx.Done():
				return
			}
		}
	}); err != nil {
		log.Printf("error registering event handler for new connection: %v", err)
	}
}
