package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursortab/buffer"
	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
	"cursortab/utils"
)

// Buffer defines the interface for buffer operations.
// Implemented by buffer.NvimBuffer for Neovim integration.
type Buffer interface {
	Sync(workspacePath string) (*buffer.SyncResult, error)
	Lines() []string
	Row() int
	Col() int
	Path() string
	Version() int
	ViewportBounds() (top, bottom int)
	PreviousLines() []string
	OriginalLines() []string
	DiffHistories() []*types.DiffEntry
	SetFileContext(prev, orig []string, diffs []*types.DiffEntry)
	HasChanges(startLine, endLineInc int, lines []string) bool
	PrepareCompletion(startLine, endLineInc int, lines []string, groups []*text.Group) buffer.Batch
	CommitPending()
	CommitUserEdits() bool // Returns true if changes were committed
	ShowCursorTarget(line int) error
	ClearUI() error
	MoveCursor(line int, center, mark bool) error
	LinterErrors() *types.LinterErrors
	RegisterEventHandler(handler func(event string)) error
}

// Provider defines the interface that all AI providers must implement.
// Implemented by inline.Provider, sweep.Provider, zeta.Provider.
type Provider interface {
	GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error)
}

// LineStreamProvider extends Provider with line-by-line streaming capabilities.
// For providers like sweep, zeta, fim that stream by lines.
type LineStreamProvider interface {
	Provider
	// GetStreamingType returns: 0=none, 1=lines, 2=tokens
	GetStreamingType() int
	// PrepareLineStream prepares the stream and returns it along with provider context
	PrepareLineStream(ctx context.Context, req *types.CompletionRequest) (LineStream, any, error)
	// ValidateFirstLine validates the first line (called after first line received)
	ValidateFirstLine(providerCtx any, firstLine string) error
	// FinishLineStream runs postprocessors on the final accumulated result
	FinishLineStream(providerCtx any, text string, finishReason string, stoppedEarly bool) (*types.CompletionResponse, error)
}

// TokenStreamProvider extends Provider with token-by-token streaming capabilities.
// For providers like inline that stream individual tokens for ghost text.
type TokenStreamProvider interface {
	Provider
	// GetStreamingType returns: 0=none, 1=lines, 2=tokens
	GetStreamingType() int
	// PrepareTokenStream prepares the stream and returns it along with provider context.
	// The stream emits cumulative text (not deltas) for idempotent UI updates.
	PrepareTokenStream(ctx context.Context, req *types.CompletionRequest) (LineStream, any, error)
	// FinishTokenStream runs postprocessors on the final accumulated result
	FinishTokenStream(providerCtx any, text string) (*types.CompletionResponse, error)
}

// Streaming type constants
const (
	StreamingTypeNone   = 0 // Batch mode
	StreamingTypeLines  = 1 // Line-by-line (sweep, zeta, fim)
	StreamingTypeTokens = 2 // Token-by-token (inline)
)

// LineStream provides incremental line-by-line streaming
type LineStream interface {
	LinesChan() <-chan string // Channel for complete lines
	Cancel()                  // Cancel the stream early
}

// TrimmedContext provides access to trim info from the provider.
// Implemented by provider.Context to allow engine to extract window offset.
type TrimmedContext interface {
	GetWindowStart() int      // 0-indexed start offset of trimmed window
	GetTrimmedLines() []string // Lines sent to the model (nil if no trimming)
}

// StreamingState holds state during incremental line streaming
type StreamingState struct {
	// Stage building
	StageBuilder *text.IncrementalStageBuilder

	// Buffering for truncation safety
	PendingLine    string // Buffer for last line (drop if truncated)
	HasPendingLine bool

	// Accumulated text for postprocessing
	AccumulatedText strings.Builder

	// Provider context for postprocessing
	ProviderContext any
	Validated       bool

	// Request data needed for finalization
	Request *types.CompletionRequest

	// Track if we've rendered the first stage during streaming
	// Only render one stage during streaming; rest handled at completion
	FirstStageRendered bool
}

// TokenStreamingState holds state during token-by-token streaming
type TokenStreamingState struct {
	// Accumulated text (cumulative, not deltas)
	AccumulatedText string

	// Provider context for postprocessing
	ProviderContext any

	// Request data needed for finalization
	Request *types.CompletionRequest

	// Line prefix: text before cursor on current line (for rendering full line)
	LinePrefix string

	// Line number where ghost text is shown (1-indexed)
	LineNum int
}

type state int

const (
	stateIdle state = iota
	statePendingCompletion
	stateHasCompletion
	stateHasCursorTarget
	stateStreamingCompletion // New state for incremental streaming
)

type CursorPredictionConfig struct {
	Enabled       bool // Show jump indicators (default: true)
	AutoAdvance   bool // On no-op, jump to last line + retrigger (default: true)
	ProximityThreshold int // Lines apart to trigger staging (default: 3)
}

// FileState holds per-file context that persists across file switches
type FileState struct {
	PreviousLines []string           // Content before user started editing this file
	DiffHistories []*types.DiffEntry // Cumulative diffs for this file
	OriginalLines []string           // Snapshot when editing session began
	LastAccessNs  int64              // Monotonic timestamp for LRU eviction
	Version       int                // Buffer version when last active
}

type EngineConfig struct {
	NsID                int
	CompletionTimeout   time.Duration
	IdleCompletionDelay time.Duration
	TextChangeDebounce  time.Duration
	CursorPrediction    CursorPredictionConfig
	MaxDiffTokens       int // Maximum tokens for diff history per file (0 = no limit)
}

type Engine struct {
	WorkspacePath string
	WorkspaceID   string

	provider        Provider
	buffer          Buffer
	clock           Clock
	state           state
	ctx             context.Context
	currentCancel   context.CancelFunc
	prefetchCancel  context.CancelFunc
	idleTimer       Timer
	textChangeTimer Timer
	mu              sync.RWMutex
	eventChan       chan Event

	// Main context and cancel for the engine lifecycle
	mainCtx    context.Context
	mainCancel context.CancelFunc
	stopped    bool
	stopOnce   sync.Once

	// Completion state
	completions  []*types.Completion
	applyBatch   buffer.Batch
	cursorTarget *types.CursorPredictionTarget

	// Staged completion state (for multi-stage completions)
	stagedCompletion *types.StagedCompletion

	// Original buffer lines when completion was shown (for partial typing optimization)
	completionOriginalLines []string

	// Prefetch state
	prefetchedCompletions  []*types.Completion
	prefetchedCursorTarget *types.CursorPredictionTarget
	prefetchState          prefetchState

	// Streaming state (line-by-line)
	streamingState   *StreamingState
	streamingCancel  context.CancelFunc
	streamLinesChan  <-chan string // Lines channel (nil when not streaming)
	streamLineNum    int           // Line counter for current stream

	// Token streaming state (token-by-token for inline)
	tokenStreamingState *TokenStreamingState
	tokenStreamChan     <-chan string // Token stream channel (nil when not streaming)

	// Config options
	config EngineConfig

	// Per-file state that persists across file switches (for context restoration)
	fileStateStore map[string]*FileState
}

func NewEngine(provider Provider, buf Buffer, config EngineConfig, clock Clock) (*Engine, error) {
	workspacePath, err := os.Getwd()
	if err != nil {
		logger.Warn("error getting current directory, using home: %v", err)
		workspacePath = "~"
	}
	workspaceID := fmt.Sprintf("%s-%d", workspacePath, os.Getpid())

	return &Engine{
		WorkspacePath:          workspacePath,
		WorkspaceID:            workspaceID,
		provider:               provider,
		buffer:                 buf,
		clock:                  clock,
		state:                  stateIdle,
		ctx:                    nil,
		eventChan:              make(chan Event, 100),
		config:                 config,
		idleTimer:              nil,
		textChangeTimer:        nil,
		mu:                     sync.RWMutex{},
		completions:            nil,
		cursorTarget:           nil,
		prefetchedCompletions:  nil,
		prefetchedCursorTarget: nil,
		prefetchState:          prefetchNone,
		stopped:                false,
		fileStateStore:         make(map[string]*FileState),
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
	logger.Info("engine started")
}

// Stop gracefully shuts down the engine and cleans up all resources
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		logger.Info("stopping engine...")

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
		// Clear any pending completions/predictions (without calling OnReject since we're stopping)
		e.state = stateIdle
		e.cursorTarget = nil
		e.completions = nil
		e.applyBatch = nil
		e.stagedCompletion = nil
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone
		e.completionOriginalLines = nil
		// Close event channel (this will cause eventLoop to exit if it hasn't already)
		close(e.eventChan)

		logger.Info("engine stopped")
	})
}

// ClearOptions configures what to clear in clearState
type ClearOptions struct {
	CancelCurrent     bool
	CancelPrefetch    bool
	ClearStaged       bool // Clear staged completion state (set false when advancing stages)
	ClearCursorTarget bool
	CallOnReject      bool
}

// clearState consolidates all state clearing into one method with configurable options
func (e *Engine) clearState(opts ClearOptions) {
	if opts.CancelCurrent && e.currentCancel != nil {
		e.currentCancel()
		e.currentCancel = nil
	}
	if opts.CancelPrefetch && e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchState = prefetchNone
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
	}
	if opts.ClearCursorTarget {
		e.cursorTarget = nil
	}
	if opts.CallOnReject {
		e.buffer.ClearUI()
	}
	e.completions = nil
	e.applyBatch = nil
	if opts.ClearStaged {
		e.stagedCompletion = nil
	}
	e.completionOriginalLines = nil
}

// clearAll clears everything including prefetch and staged completions
func (e *Engine) clearAll() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: true, ClearStaged: true, ClearCursorTarget: true, CallOnReject: true})
}

// clearKeepPrefetch clears current completion but keeps prefetch data, staged completion state, and cursor target
func (e *Engine) clearKeepPrefetch() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: false, ClearStaged: false, ClearCursorTarget: false, CallOnReject: true})
}

// eventLoopRestarts tracks the number of event loop restarts for panic recovery
var eventLoopRestarts atomic.Int32

const maxEventLoopRestarts = 3

func (e *Engine) eventLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			restarts := eventLoopRestarts.Add(1)
			logger.Error("event loop panic [%d/%d]: %v\n%s",
				restarts, maxEventLoopRestarts, r, debug.Stack())

			if int(restarts) < maxEventLoopRestarts {
				e.eventLoop(e.mainCtx) // Restart the event loop
			} else {
				logger.Error("max event loop restarts reached, stopping engine")
				go e.Stop() // async to avoid deadlock
			}
		}
	}()

	for {
		// Get current stream channels (nil when not streaming)
		e.mu.RLock()
		linesChan := e.streamLinesChan
		tokenChan := e.tokenStreamChan
		e.mu.RUnlock()

		select {
		case <-ctx.Done():
			// Clean shutdown when context is cancelled
			return

		case line, ok := <-linesChan:
			// Direct stream line handling - no intermediate buffer
			// When linesChan is nil, this case is never selected
			e.mu.Lock()
			if e.stopped {
				e.mu.Unlock()
				return
			}
			if e.streamLinesChan != linesChan {
				// Stream changed while we were waiting, ignore stale data
				e.mu.Unlock()
				continue
			}
			if !ok {
				// Channel closed - stream complete
				logger.Debug("stream complete after %d lines", e.streamLineNum)
				e.handleStreamCompleteSimple()
				e.mu.Unlock()
				continue
			}
			e.streamLineNum++
			if e.streamLineNum <= 3 {
				logger.Debug("stream line %d: %q", e.streamLineNum, line)
			}
			e.handleStreamLine(line)
			e.mu.Unlock()

		case text, ok := <-tokenChan:
			// Token stream handling (cumulative text for inline completion)
			// When tokenChan is nil, this case is never selected
			e.mu.Lock()
			if e.stopped {
				e.mu.Unlock()
				return
			}
			if e.tokenStreamChan != tokenChan {
				// Stream changed while we were waiting, ignore stale data
				e.mu.Unlock()
				continue
			}
			if !ok {
				// Channel closed - token stream complete
				logger.Debug("token stream complete")
				e.handleTokenStreamComplete()
				e.mu.Unlock()
				continue
			}
			e.handleTokenChunk(text)
			e.mu.Unlock()

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
						logger.Error("event handler panic recovered for event %v: %v", event.Type, r)
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

	logger.Debug("handle event: %v (state=%s)", event.Type, e.state)
	defer func() {
		logger.Debug("after event: %v (state=%s)", event.Type, e.state)
	}()

	// Layer 1: Background/async results
	if e.handleBackgroundEvent(event) {
		return
	}

	// Layer 2: Dispatch table for user/timer events
	e.dispatch(event)
}

// handleBackgroundEvent handles async completion and prefetch results.
// Returns true if the event was handled, false if it should be dispatched.
func (e *Engine) handleBackgroundEvent(event Event) bool {
	switch event.Type {
	case EventCompletionReady:
		if e.state != statePendingCompletion {
			logger.Debug("ignoring stale completion: state=%v", e.state)
			return true
		}
		e.handleCompletionReadyImpl(event.Data.(*types.CompletionResponse))
		return true

	case EventCompletionError:
		if err, ok := event.Data.(error); ok && errors.Is(err, context.Canceled) {
			logger.Debug("completion canceled: %v", err)
		} else {
			logger.Error("completion error: %v", event.Data)
		}
		return true

	case EventPrefetchReady:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			logger.Debug("ignoring stale prefetch: prefetchState=%v", e.prefetchState)
			return true
		}
		e.handlePrefetchReady(event.Data.(*types.CompletionResponse))
		return true

	case EventPrefetchError:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			logger.Debug("ignoring stale prefetch error: prefetchState=%v", e.prefetchState)
			return true
		}
		// Nil-safe error handling
		if err, ok := event.Data.(error); ok {
			e.handlePrefetchError(err)
		} else {
			e.handlePrefetchError(nil)
		}
		return true

	// Note: EventStreamLine, EventStreamComplete, EventStreamError are now handled
	// directly in the event loop via channel selection, not through eventChan
	}
	return false
}

func (e *Engine) reject() {
	e.clearState(ClearOptions{
		CancelCurrent:     true,
		CancelPrefetch:    true,
		ClearStaged:       true,
		ClearCursorTarget: true,
		CallOnReject:      true,
	})
	e.state = stateIdle
}

// syncBuffer syncs the buffer state and handles file switching.
// This should be called instead of buffer.Sync directly to ensure
// file context is properly saved/restored when switching files.
func (e *Engine) syncBuffer() {
	result, err := e.buffer.Sync(e.WorkspacePath)
	if err != nil {
		logger.Debug("sync error: %v", err)
		return
	}

	if result != nil && result.BufferChanged {
		e.handleFileSwitch(result.OldPath, result.NewPath, e.buffer.Lines())
	}
}

func (e *Engine) requestCompletion(source types.CompletionSource) {
	// Check if stopped before making request
	if e.stopped {
		return
	}

	e.syncBuffer()

	req := &types.CompletionRequest{
		Source:            source,
		WorkspacePath:     e.WorkspacePath,
		WorkspaceID:       e.WorkspaceID,
		FilePath:          e.buffer.Path(),
		Lines:             e.buffer.Lines(),
		Version:           e.buffer.Version(),
		PreviousLines:     e.buffer.PreviousLines(),
		FileDiffHistories: e.getAllFileDiffHistories(),
		CursorRow:         e.buffer.Row(),
		CursorCol:         e.buffer.Col(),
		ViewportHeight:    e.getViewportHeightConstraint(),
		LinterErrors:      e.buffer.LinterErrors(),
	}

	// Check if provider supports streaming
	if streamProvider, ok := e.provider.(LineStreamProvider); ok {
		switch streamProvider.GetStreamingType() {
		case StreamingTypeLines:
			e.requestStreamingCompletion(streamProvider, req)
			return
		case StreamingTypeTokens:
			if tokenProvider, ok := e.provider.(TokenStreamProvider); ok {
				e.requestTokenStreamingCompletion(tokenProvider, req)
				return
			}
		}
	}

	// Fallback to batch mode
	e.state = statePendingCompletion

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.currentCancel = cancel

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, req)

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

// requestStreamingCompletion handles line-by-line streaming completions
func (e *Engine) requestStreamingCompletion(provider LineStreamProvider, req *types.CompletionRequest) {
	e.state = stateStreamingCompletion

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.streamingCancel = cancel

	// Prepare the stream
	stream, providerCtx, err := provider.PrepareLineStream(ctx, req)
	if err != nil {
		cancel()
		logger.Debug("streaming: PrepareLineStream failed: %v", err)
		e.state = stateIdle
		return
	}

	// Get old lines for incremental diff building.
	// Extract trim info from provider context if available.
	windowStart := 0
	var oldLines []string
	if tc, ok := providerCtx.(TrimmedContext); ok && len(tc.GetTrimmedLines()) > 0 {
		// Provider trimmed the content - use trimmed lines and offset
		windowStart = tc.GetWindowStart()
		oldLines = tc.GetTrimmedLines()
		logger.Debug("streaming: using trimmed window start=%d, lines=%d", windowStart, len(oldLines))
	} else {
		// No trimming - use full buffer
		oldLines = req.Lines
		logger.Debug("streaming: using full buffer, lines=%d", len(oldLines))
	}

	viewportTop, viewportBottom := e.buffer.ViewportBounds()

	// Initialize streaming state
	e.streamingState = &StreamingState{
		StageBuilder: text.NewIncrementalStageBuilder(
			oldLines,
			windowStart+1, // baseLineOffset (1-indexed)
			e.config.CursorPrediction.ProximityThreshold,
			viewportTop,
			viewportBottom,
			e.buffer.Row(),
			req.FilePath,
		),
		ProviderContext: providerCtx,
		Request:         req,
	}

	logger.Debug("stream starting")

	// Set stream channel directly - event loop will select on it
	e.streamLinesChan = stream.LinesChan()
	e.streamLineNum = 0
}

// requestTokenStreamingCompletion handles token-by-token streaming completions (inline)
func (e *Engine) requestTokenStreamingCompletion(provider TokenStreamProvider, req *types.CompletionRequest) {
	e.state = stateStreamingCompletion

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.streamingCancel = cancel

	// Prepare the stream
	stream, providerCtx, err := provider.PrepareTokenStream(ctx, req)
	if err != nil {
		cancel()
		logger.Debug("token streaming: PrepareTokenStream failed: %v", err)
		e.state = stateIdle
		return
	}

	// Get line prefix (text before cursor on current line)
	linePrefix := ""
	if req.CursorRow >= 1 && req.CursorRow <= len(req.Lines) {
		currentLine := req.Lines[req.CursorRow-1]
		cursorCol := min(req.CursorCol, len(currentLine))
		linePrefix = currentLine[:cursorCol]
	}

	// Initialize token streaming state
	e.tokenStreamingState = &TokenStreamingState{
		AccumulatedText: "",
		ProviderContext: providerCtx,
		Request:         req,
		LinePrefix:      linePrefix,
		LineNum:         req.CursorRow,
	}

	logger.Debug("token stream starting at line %d, prefix=%q", req.CursorRow, linePrefix)

	// Set token stream channel - event loop will select on it
	e.tokenStreamChan = stream.LinesChan()
}

func (e *Engine) handleCursorTarget() {
	if !e.config.CursorPrediction.Enabled {
		e.clearCompletionUIOnly()
		return
	}

	if e.cursorTarget == nil || e.cursorTarget.LineNumber < 1 {
		e.clearCompletionUIOnly()
		return
	}

	distance := abs(int(e.cursorTarget.LineNumber) - e.buffer.Row())
	if distance <= e.config.CursorPrediction.ProximityThreshold {
		// Close enough - don't show cursor prediction

		// If we have remaining staged completions, check if next stage is still close
		if e.stagedCompletion != nil && e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
			nextStage := e.getStage(e.stagedCompletion.CurrentIdx)
			if nextStage == nil {
				return
			}
			stageStart := nextStage.BufferStart
			stageEnd := nextStage.BufferEnd

			// Calculate distance from cursor to next stage
			var stageDistance int
			if e.buffer.Row() < stageStart {
				stageDistance = stageStart - e.buffer.Row()
			} else if e.buffer.Row() > stageEnd {
				stageDistance = e.buffer.Row() - stageEnd
			} else {
				stageDistance = 0 // Cursor is within the stage range
			}

			if stageDistance <= e.config.CursorPrediction.ProximityThreshold {
				// Stage is close enough - show it directly
				e.showCurrentStage()
				return
			}
			// Stage is far - show cursor prediction to it instead
			e.cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    e.buffer.Path(),
				LineNumber:      int32(stageStart),
				ShouldRetrigger: false,
			}
			e.state = stateHasCursorTarget
			e.buffer.ShowCursorTarget(stageStart)
			return
		}

		// If prefetch is ready, show completion immediately
		if e.prefetchState == prefetchReady && e.tryShowPrefetchedCompletion() {
			return
		}
		// If prefetch is in flight, wait for it
		if e.prefetchState == prefetchInFlight {
			e.prefetchState = prefetchWaitingForCursorPrediction
		}
		// Go idle (no cursor prediction needed when close)
		e.clearCompletionUIOnly()
		return
	}

	// Far away - show cursor prediction to the target line
	e.state = stateHasCursorTarget
	e.buffer.ShowCursorTarget(int(e.cursorTarget.LineNumber))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// clearCompletionUIOnly clears completion state but preserves prefetch.
// This is used when transitioning out of cursor target state without
// wanting to cancel an in-flight prefetch.
// NOTE: This does NOT call OnReject because it's expected that
// clearKeepPrefetch() was already called before this, which handles the UI clearing.
func (e *Engine) clearCompletionUIOnly() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: false, ClearStaged: true, CallOnReject: false})
	e.state = stateIdle
	e.cursorTarget = nil
}

func (e *Engine) acceptCompletion() {
	if e.applyBatch != nil {
		if err := e.applyBatch.Execute(); err != nil {
			logger.Error("error applying completion: %v", err)
		}
	}

	// Commit pending file changes only after successful apply
	e.buffer.CommitPending()

	// After commit, save file state for context persistence across file switches
	e.saveCurrentFileState()

	e.clearKeepPrefetch()

	// Handle staged completions: if there are more stages, show cursor target to next stage
	if e.stagedCompletion != nil {
		// Track cumulative offset for unequal line count stages
		currentStage := e.getStage(e.stagedCompletion.CurrentIdx)
		if currentStage != nil {
			oldLineCount := currentStage.BufferEnd - currentStage.BufferStart + 1
			newLineCount := len(currentStage.Lines)
			e.stagedCompletion.CumulativeOffset += newLineCount - oldLineCount
		}

		e.stagedCompletion.CurrentIdx++
		if e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
			// More stages remaining - sync buffer and show cursor target to next stage
			e.syncBuffer()

			// Apply cumulative offset to remaining stages if line counts changed
			if e.stagedCompletion.CumulativeOffset != 0 {
				for i := e.stagedCompletion.CurrentIdx; i < len(e.stagedCompletion.Stages); i++ {
					stage := e.getStage(i)
					if stage != nil {
						stage.BufferStart += e.stagedCompletion.CumulativeOffset
						stage.BufferEnd += e.stagedCompletion.CumulativeOffset
					}
					if stage != nil && stage.CursorTarget != nil {
						stage.CursorTarget.LineNumber += int32(e.stagedCompletion.CumulativeOffset)
					}
				}
				// Reset cumulative offset after applying (already factored in)
				e.stagedCompletion.CumulativeOffset = 0
			}

			// At n-1 stage (one stage remaining), trigger prefetch early so it has
			// fresh context and time to complete before the last stage is accepted.
			// Use stage n's BufferStart as context position (where the user will be looking).
			if e.stagedCompletion.CurrentIdx == len(e.stagedCompletion.Stages)-1 {
				lastStage := e.getStage(len(e.stagedCompletion.Stages) - 1)
				if lastStage != nil && lastStage.CursorTarget != nil && lastStage.CursorTarget.ShouldRetrigger {
					overrideRow := max(1, lastStage.BufferStart)
					e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
				}
			}

			e.handleCursorTarget() // Shows jump indicator to next stage
			return
		}
		// All stages complete - clear staged completion
		// (cursorTarget already has ShouldRetrigger from last stage if applicable)
		e.stagedCompletion = nil
	}

	// Sync buffer to get the updated state after applying completion
	e.syncBuffer()

	// Prefetch next completion if cursor target requests retrigger (after applying current completion)
	// Skip if prefetch is already in flight (e.g., triggered at n-1 stage)
	if e.cursorTarget != nil && e.cursorTarget.ShouldRetrigger && e.prefetchState == prefetchNone {
		// Prefetch targeting the predicted cursor line
		overrideRow := max(1, int(e.cursorTarget.LineNumber))
		e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
	}

	e.handleCursorTarget()
}

// saveCurrentFileState saves the current buffer state to the file state store
func (e *Engine) saveCurrentFileState() {
	if e.buffer.Path() == "" {
		return
	}

	state := &FileState{
		PreviousLines: copyLines(e.buffer.PreviousLines()),
		DiffHistories: copyDiffs(e.buffer.DiffHistories()),
		OriginalLines: copyLines(e.buffer.OriginalLines()),
		LastAccessNs:  e.clock.Now().UnixNano(),
		Version:       e.buffer.Version(),
	}

	e.fileStateStore[e.buffer.Path()] = state
	e.trimFileStateStore(2) // Keep at most 2 files
}

// handleFileSwitch manages file state when switching between files.
// Called after Sync detects a buffer change. Returns true if state was restored.
func (e *Engine) handleFileSwitch(oldPath, newPath string, currentLines []string) bool {
	if oldPath == newPath {
		return false
	}

	// Save state of the file we're leaving
	if oldPath != "" {
		state := &FileState{
			PreviousLines: copyLines(e.buffer.PreviousLines()),
			DiffHistories: copyDiffs(e.buffer.DiffHistories()),
			OriginalLines: copyLines(e.buffer.OriginalLines()),
			LastAccessNs:  e.clock.Now().UnixNano(),
			Version:       e.buffer.Version(),
		}
		e.fileStateStore[oldPath] = state
	}

	// Try to restore state for the new file
	if state, exists := e.fileStateStore[newPath]; exists {
		if e.isFileStateValid(state, currentLines) {
			// Restore the saved state
			e.buffer.SetFileContext(state.PreviousLines, state.OriginalLines, state.DiffHistories)
			state.LastAccessNs = e.clock.Now().UnixNano()
			logger.Debug("restored file state for %s (version=%d, diffs=%d)", newPath, state.Version, len(state.DiffHistories))
			return true
		}
		// State is stale (file changed externally) - discard it
		delete(e.fileStateStore, newPath)
		logger.Debug("discarded stale file state for %s", newPath)
	}

	// New file or stale state - initialize fresh (PreviousLines stays nil for new files)
	e.buffer.SetFileContext(nil, copyLines(currentLines), nil)
	return false
}

// isFileStateValid checks if saved state is still valid for the current file content.
// Returns false if the file appears to have changed externally.
func (e *Engine) isFileStateValid(state *FileState, currentLines []string) bool {
	if len(state.OriginalLines) == 0 {
		return false
	}

	// Simple heuristic: if line count changed significantly, state is stale
	origLen := len(state.OriginalLines)
	currLen := len(currentLines)
	if origLen != currLen {
		// Allow some tolerance for small changes
		diff := origLen - currLen
		if diff < 0 {
			diff = -diff
		}
		// If more than 10% difference or more than 10 lines, consider stale
		threshold := max(origLen/10, 10)
		if diff > threshold {
			return false
		}
	}

	// Check anchor lines (first, middle, last) for major content drift
	checkIndices := []int{0}
	if currLen > 2 {
		checkIndices = append(checkIndices, currLen/2, currLen-1)
	}

	mismatches := 0
	for _, i := range checkIndices {
		if i < len(state.OriginalLines) && i < len(currentLines) {
			if state.OriginalLines[i] != currentLines[i] {
				mismatches++
			}
		}
	}

	// If more than half of anchor lines changed, consider stale
	return mismatches <= len(checkIndices)/2
}

// trimFileStateStore keeps only the most recently accessed maxFiles files
func (e *Engine) trimFileStateStore(maxFiles int) {
	if len(e.fileStateStore) <= maxFiles {
		return
	}

	type entry struct {
		path  string
		state *FileState
	}

	entries := make([]entry, 0, len(e.fileStateStore))
	for path, state := range e.fileStateStore {
		entries = append(entries, entry{path, state})
	}

	// Sort by LastAccessNs descending (most recent first)
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].state.LastAccessNs < entries[j].state.LastAccessNs {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Keep only maxFiles most recent entries
	e.fileStateStore = make(map[string]*FileState)
	for i := 0; i < maxFiles && i < len(entries); i++ {
		e.fileStateStore[entries[i].path] = entries[i].state
	}
}

// getAllFileDiffHistories returns diff history for the current file only.
// This prevents context pollution from other files' diffs.
func (e *Engine) getAllFileDiffHistories() []*types.FileDiffHistory {
	// Only return diffs for the current file
	if e.buffer.Path() == "" || len(e.buffer.DiffHistories()) == 0 {
		return nil
	}

	// Copy to ensure immutability
	diffs := copyDiffs(e.buffer.DiffHistories())

	// Apply token limiting if configured
	if e.config.MaxDiffTokens > 0 {
		diffs = utils.TrimDiffEntries(diffs, e.config.MaxDiffTokens)
	}

	if len(diffs) == 0 {
		return nil
	}

	return []*types.FileDiffHistory{{
		FileName:    e.buffer.Path(),
		DiffHistory: diffs,
	}}
}

// copyLines creates a deep copy of a string slice
func copyLines(lines []string) []string {
	if lines == nil {
		return nil
	}
	result := make([]string, len(lines))
	copy(result, lines)
	return result
}

// copyDiffs creates a deep copy of a DiffEntry slice
func copyDiffs(diffs []*types.DiffEntry) []*types.DiffEntry {
	if diffs == nil {
		return nil
	}
	result := make([]*types.DiffEntry, len(diffs))
	copy(result, diffs)
	return result
}

func (e *Engine) acceptCursorTarget() {
	if e.cursorTarget == nil {
		return
	}

	err := e.buffer.MoveCursor(int(e.cursorTarget.LineNumber), true, true)
	if err != nil {
		logger.Error("error moving cursor: %v", err)
	}

	e.buffer.ClearUI()

	// Handle staged completions: if there are more stages, show the next stage
	if e.stagedCompletion != nil && e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
		e.syncBuffer()
		e.showCurrentStage()
		return
	}

	// Try to use prefetched completion
	if e.usePrefetchedCompletion() {
		return
	}

	// If prefetch is in progress, wait for it to complete instead of triggering new request
	if e.prefetchState == prefetchInFlight {
		e.prefetchState = prefetchWaitingForTab
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

// showCurrentStage displays the current stage of a multi-stage completion
func (e *Engine) showCurrentStage() {
	if e.stagedCompletion == nil || e.stagedCompletion.CurrentIdx >= len(e.stagedCompletion.Stages) {
		return
	}

	stage := e.getStage(e.stagedCompletion.CurrentIdx)
	if stage == nil {
		return
	}

	e.completions = []*types.Completion{{
		StartLine:  stage.BufferStart,
		EndLineInc: stage.BufferEnd,
		Lines:      stage.Lines,
	}}
	e.cursorTarget = stage.CursorTarget
	e.state = stateHasCompletion

	// Use PrepareCompletion with pre-computed groups from stage
	e.applyBatch = e.buffer.PrepareCompletion(
		stage.BufferStart,
		stage.BufferEnd,
		stage.Lines,
		stage.Groups,
	)

	// Store original buffer lines for partial typing optimization
	bufferLines := e.buffer.Lines()
	e.completionOriginalLines = nil
	for i := stage.BufferStart; i <= stage.BufferEnd && i-1 < len(bufferLines); i++ {
		e.completionOriginalLines = append(e.completionOriginalLines, bufferLines[i-1])
	}
}

// getStage returns the stage at the given index with type assertion
func (e *Engine) getStage(idx int) *text.Stage {
	if e.stagedCompletion == nil || idx < 0 || idx >= len(e.stagedCompletion.Stages) {
		return nil
	}
	stage, ok := e.stagedCompletion.Stages[idx].(*text.Stage)
	if !ok {
		return nil
	}
	return stage
}

// processCompletion is the SINGLE ENTRY POINT for processing all completions.
// It handles diff analysis, staging decisions, and showing completions.
// Called from both fresh completion responses and prefetch paths.
// Returns true if completion was processed successfully, false if no changes.
func (e *Engine) processCompletion(completion *types.Completion) bool {
	defer logger.Trace("engine.processCompletion")()
	if completion == nil {
		return false
	}

	// Check for actual changes
	if !e.buffer.HasChanges(completion.StartLine, completion.EndLineInc, completion.Lines) {
		logger.Debug("processCompletion: no changes detected")
		return false
	}

	// Extract original lines from buffer
	bufferLines := e.buffer.Lines()
	var originalLines []string
	for i := completion.StartLine; i <= completion.EndLineInc && i-1 < len(bufferLines); i++ {
		originalLines = append(originalLines, bufferLines[i-1])
	}

	// Analyze diff with viewport awareness
	viewportTop, viewportBottom := e.buffer.ViewportBounds()
	originalText := text.JoinLines(originalLines)
	newText := text.JoinLines(completion.Lines)
	diffResult := text.AnalyzeDiffForStagingWithViewport(
		originalText, newText,
		viewportTop, viewportBottom,
		completion.StartLine,
	)

	// Create stages - CreateStages handles all viewport/distance logic and returns:
	// - nil: no staging needed (single visible+close cluster or no changes)
	// - StagingResult with FirstNeedsNavigation: whether to show cursor prediction UI
	stagingResult := text.CreateStages(
		diffResult,
		e.buffer.Row(),
		viewportTop, viewportBottom,
		completion.StartLine,
		e.config.CursorPrediction.ProximityThreshold,
		e.buffer.Path(),
		completion.Lines,
		originalLines, // oldLines parameter
	)

	if stagingResult != nil && len(stagingResult.Stages) > 0 {
		// Convert stages to any slice for storage
		stagesAny := make([]any, len(stagingResult.Stages))
		for i, s := range stagingResult.Stages {
			stagesAny[i] = s
		}
		e.stagedCompletion = &types.StagedCompletion{
			Stages:     stagesAny,
			CurrentIdx: 0,
			SourcePath: e.buffer.Path(),
		}

		if stagingResult.FirstNeedsNavigation {
			// First stage is outside viewport or far from cursor - show cursor prediction
			firstStage := stagingResult.Stages[0]
			e.cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    e.buffer.Path(),
				LineNumber:      int32(firstStage.BufferStart),
				ShouldRetrigger: false,
			}
			e.state = stateHasCursorTarget
			e.buffer.ShowCursorTarget(firstStage.BufferStart)
			return true
		}

		// Show first stage directly
		e.showCurrentStage()
		return true
	}

	// No staging means no changes to show
	return false
}

// RegisterEventHandler registers the event handler for nvim RPC callbacks.
// This should be called after buffer.SetClient has been called with a valid nvim connection.
func (e *Engine) RegisterEventHandler() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Don't change if stopped
	if e.stopped {
		return
	}

	// Register the event handler for the new connection
	if err := e.buffer.RegisterEventHandler(func(event string) {
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
		logger.Error("error registering event handler for new connection: %v", err)
	}
}

// getViewportHeightConstraint returns the viewport height constraint for completion requests.
// When staging is enabled, returns 0 (no limit) to allow multi-line completions.
// When staging is disabled, limits to distance from cursor to viewport bottom to prevent overflow.
func (e *Engine) getViewportHeightConstraint() int {
	if e.config.CursorPrediction.Enabled {
		return 0
	}
	// Distance from cursor to end of viewport
	_, viewportBottom := e.buffer.ViewportBounds()
	if viewportBottom > 0 && e.buffer.Row() > 0 {
		if constraint := viewportBottom - e.buffer.Row(); constraint > 0 {
			return constraint
		}
	}
	return 0
}

// cancelStreaming cancels an in-progress streaming completion (both line and token streaming)
func (e *Engine) cancelStreaming() {
	if e.streamLinesChan != nil {
		logger.Debug("stream cancelling after %d lines", e.streamLineNum)
	}
	if e.tokenStreamChan != nil {
		logger.Debug("token stream cancelling")
	}
	// Clear channels first - this immediately stops event loop from reading
	e.streamLinesChan = nil
	e.streamLineNum = 0
	e.tokenStreamChan = nil
	// Then cancel the HTTP request
	if e.streamingCancel != nil {
		e.streamingCancel()
		e.streamingCancel = nil
	}
	e.streamingState = nil
	e.tokenStreamingState = nil
}

// cancelTokenStreamingKeepPartial cancels token streaming but preserves the partial
// completion state (completions and completionOriginalLines) for typing match validation.
// Used when user types during token streaming to check if typing matches partial result.
func (e *Engine) cancelTokenStreamingKeepPartial() {
	if e.tokenStreamChan != nil {
		logger.Debug("token stream cancelling (keeping partial)")
	}
	// Clear channel first - stops event loop from reading
	e.tokenStreamChan = nil
	// Cancel the HTTP request
	if e.streamingCancel != nil {
		e.streamingCancel()
		e.streamingCancel = nil
	}
	// Clear streaming state but keep completions and completionOriginalLines
	// These were populated by handleTokenChunk and are needed for checkTypingMatchesPrediction
	e.tokenStreamingState = nil
}

// handleStreamLine processes a line received from the streaming provider.
// Caller must verify stream ID matches before calling.
func (e *Engine) handleStreamLine(line string) {
	ss := e.streamingState
	if ss == nil {
		return
	}

	// Accumulate text for postprocessing
	ss.AccumulatedText.WriteString(line)
	ss.AccumulatedText.WriteString("\n")

	// First line validation
	if !ss.Validated {
		if sp, ok := e.provider.(LineStreamProvider); ok {
			if err := sp.ValidateFirstLine(ss.ProviderContext, line); err != nil {
				logger.Debug("stream validation failed: %v", err)
				e.cancelStreaming()
				e.state = stateIdle
				return
			}
		}
		ss.Validated = true
	}

	// Process pending line through stage builder (if any)
	if ss.HasPendingLine {
		finalized := ss.StageBuilder.AddLine(ss.PendingLine)
		if finalized != nil && !ss.FirstStageRendered {
			// Check if this stage is close enough to render immediately
			viewportTop, viewportBottom := e.buffer.ViewportBounds()
			needsNav := text.StageNeedsNavigation(
				finalized,
				e.buffer.Row(),
				viewportTop, viewportBottom,
				e.config.CursorPrediction.ProximityThreshold,
			)
			if !needsNav {
				// Stage is close to cursor - render it immediately
				e.renderStreamedStage(finalized)
				ss.FirstStageRendered = true
			}
			// If needsNav, don't render - let Finalize() handle it with cursor prediction
		}
	}

	// Buffer current line (will be processed on next line or completion)
	ss.PendingLine = line
	ss.HasPendingLine = true
}

// handleStreamCompleteSimple processes stream completion when lines channel closes.
// Called directly from event loop.
func (e *Engine) handleStreamCompleteSimple() {
	// Clear stream channel first
	e.streamLinesChan = nil
	e.streamLineNum = 0

	if e.streamingState == nil {
		return
	}

	ss := e.streamingState
	firstStageRendered := ss.FirstStageRendered

	// Process pending line if not truncated
	if ss.HasPendingLine {
		ss.StageBuilder.AddLine(ss.PendingLine)
		ss.HasPendingLine = false
	}

	// Finalize remaining stages
	stagingResult := ss.StageBuilder.Finalize()

	// Clear streaming state
	e.streamingState = nil
	e.streamingCancel = nil

	if stagingResult == nil || len(stagingResult.Stages) == 0 {
		logger.Debug("stream complete: no stages to show")
		e.state = stateIdle
		return
	}

	// Convert to staged completion format
	stagesAny := make([]any, len(stagingResult.Stages))
	for i, s := range stagingResult.Stages {
		stagesAny[i] = s
	}
	e.stagedCompletion = &types.StagedCompletion{
		Stages:     stagesAny,
		CurrentIdx: 0,
		SourcePath: e.buffer.Path(),
	}

	// If we already rendered the first stage during streaming, don't re-render it
	if firstStageRendered {
		// Stage 0 is already showing - just update cursor target from finalized data
		firstStage := stagingResult.Stages[0]
		e.cursorTarget = firstStage.CursorTarget
		e.state = stateHasCompletion
		logger.Debug("stream complete: first stage already rendered, skipping re-render")
		return
	}

	// Clear any UI (nothing was rendered during streaming)
	e.buffer.ClearUI()

	// Transition to appropriate state
	if stagingResult.FirstNeedsNavigation {
		firstStage := stagingResult.Stages[0]
		e.cursorTarget = &types.CursorPredictionTarget{
			RelativePath:    e.buffer.Path(),
			LineNumber:      int32(firstStage.BufferStart),
			ShouldRetrigger: false,
		}
		e.state = stateHasCursorTarget
		e.buffer.ShowCursorTarget(firstStage.BufferStart)
	} else {
		e.showCurrentStage()
	}
}

// renderStreamedStage renders a finalized stage during streaming
func (e *Engine) renderStreamedStage(stage *text.Stage) {
	if stage == nil || len(stage.Groups) == 0 {
		return
	}

	// Prepare completion for this stage and render it
	e.applyBatch = e.buffer.PrepareCompletion(
		stage.BufferStart,
		stage.BufferEnd,
		stage.Lines,
		stage.Groups,
	)

	// Store for partial typing optimization
	bufferLines := e.buffer.Lines()
	e.completionOriginalLines = nil
	for i := stage.BufferStart; i <= stage.BufferEnd && i-1 < len(bufferLines); i++ {
		e.completionOriginalLines = append(e.completionOriginalLines, bufferLines[i-1])
	}

	e.completions = []*types.Completion{{
		StartLine:  stage.BufferStart,
		EndLineInc: stage.BufferEnd,
		Lines:      stage.Lines,
	}}
	e.cursorTarget = stage.CursorTarget

	logger.Debug("rendered streamed stage: BufferStart=%d, BufferEnd=%d, groups=%d",
		stage.BufferStart, stage.BufferEnd, len(stage.Groups))
}

// handleTokenChunk processes a cumulative text chunk from token streaming.
// The text parameter contains the full accumulated text so far (not a delta).
func (e *Engine) handleTokenChunk(accumulatedText string) {
	ts := e.tokenStreamingState
	if ts == nil {
		return
	}

	// Update accumulated text
	ts.AccumulatedText = accumulatedText

	// Build the full line content
	fullLineText := ts.LinePrefix + accumulatedText
	lineNum := ts.LineNum
	colStart := len(ts.LinePrefix)

	// Get original line content
	bufferLines := e.buffer.Lines()
	var oldLine string
	if lineNum >= 1 && lineNum <= len(bufferLines) {
		oldLine = bufferLines[lineNum-1]
	}

	// Create a group with append_chars render hint
	group := &text.Group{
		Type:       "modification",
		StartLine:  1,
		EndLine:    1,
		BufferLine: lineNum,
		Lines:      []string{fullLineText},
		OldLines:   []string{oldLine},
		RenderHint: "append_chars",
		ColStart:   colStart,
		ColEnd:     len(fullLineText),
	}

	// Call PrepareCompletion to render the ghost text
	e.applyBatch = e.buffer.PrepareCompletion(lineNum, lineNum, []string{fullLineText}, []*text.Group{group})

	// Store completion state for partial typing optimization
	e.completions = []*types.Completion{{
		StartLine:  lineNum,
		EndLineInc: lineNum,
		Lines:      []string{fullLineText},
	}}
	e.completionOriginalLines = []string{oldLine}

	logger.Debug("token chunk: len=%d, fullLine=%q", len(accumulatedText), fullLineText)
}

// handleTokenStreamComplete processes token stream completion when channel closes.
// Called directly from event loop.
func (e *Engine) handleTokenStreamComplete() {
	// Clear token stream channel first
	e.tokenStreamChan = nil

	if e.tokenStreamingState == nil {
		e.state = stateIdle
		return
	}

	ts := e.tokenStreamingState
	finalText := ts.AccumulatedText
	providerCtx := ts.ProviderContext
	req := ts.Request

	// Clear token streaming state
	e.tokenStreamingState = nil
	e.streamingCancel = nil

	// If empty, go idle
	if finalText == "" {
		logger.Debug("token stream complete: empty result")
		e.buffer.ClearUI()
		e.state = stateIdle
		return
	}

	// Run postprocessors through provider
	tokenProvider, ok := e.provider.(TokenStreamProvider)
	if !ok {
		logger.Debug("token stream complete: provider not TokenStreamProvider")
		e.buffer.ClearUI()
		e.state = stateIdle
		return
	}

	resp, err := tokenProvider.FinishTokenStream(providerCtx, finalText)
	if err != nil {
		logger.Debug("token stream complete: FinishTokenStream error: %v", err)
		e.buffer.ClearUI()
		e.state = stateIdle
		return
	}

	// Process the response like a normal completion
	if resp == nil || len(resp.Completions) == 0 {
		logger.Debug("token stream complete: no completions")
		e.buffer.ClearUI()
		e.state = stateIdle
		return
	}

	// For inline provider, there's always just one completion
	completion := resp.Completions[0]

	// Validate completion is for current buffer state
	if completion.StartLine < 1 || completion.StartLine > len(req.Lines) {
		logger.Debug("token stream complete: invalid completion range")
		e.buffer.ClearUI()
		e.state = stateIdle
		return
	}

	// Process through normal completion flow (handles staging etc.)
	if e.processCompletion(completion) {
		e.state = stateHasCompletion
	} else {
		logger.Debug("token stream complete: processCompletion returned false")
		e.buffer.ClearUI()
		e.state = stateIdle
	}
}
