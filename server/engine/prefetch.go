package engine

import (
	"context"
	"errors"

	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
)

// prefetchState represents the state of prefetch operations
type prefetchState int

const (
	prefetchNone prefetchState = iota
	prefetchInFlight
	prefetchWaitingForTab
	prefetchWaitingForCursorPrediction
	prefetchReady
)

// String returns a human-readable name for the prefetch state
func (s prefetchState) String() string {
	switch s {
	case prefetchNone:
		return "None"
	case prefetchInFlight:
		return "InFlight"
	case prefetchWaitingForTab:
		return "WaitingForTab"
	case prefetchWaitingForCursorPrediction:
		return "WaitingForCursorPrediction"
	case prefetchReady:
		return "Ready"
	default:
		return "Unknown"
	}
}

// requestPrefetch requests a completion for a specific cursor position without changing the engine state
func (e *Engine) requestPrefetch(source types.CompletionSource, overrideRow int, overrideCol int) {
	if e.stopped {
		return
	}

	// Cancel existing prefetch if any
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchState = prefetchNone
	}

	// Sync buffer to ensure latest context
	e.syncBuffer()

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.prefetchCancel = cancel
	e.prefetchState = prefetchInFlight

	// Snapshot required values to avoid races with buffer mutation
	lines := append([]string{}, e.buffer.Lines()...)
	previousLines := append([]string{}, e.buffer.PreviousLines()...)
	version := e.buffer.Version()
	filePath := e.buffer.Path()
	linterErrors := e.buffer.LinterErrors()
	viewportHeight := e.getViewportHeightConstraint()

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
			ViewportHeight:    viewportHeight,
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

// handlePrefetchReady processes a successful prefetch response
func (e *Engine) handlePrefetchReady(resp *types.CompletionResponse) {
	e.prefetchedCompletions = resp.Completions
	e.prefetchedCursorTarget = resp.CursorTarget
	previousPrefetchState := e.prefetchState
	e.prefetchState = prefetchReady

	// If we were waiting for prefetch due to tab press, continue with cursor target logic
	if previousPrefetchState == prefetchWaitingForTab {
		e.handleDeferredCursorTarget()
	}

	// If we were waiting for prefetch to show cursor prediction (last stage case),
	// check if first change is close enough to show completion, otherwise show cursor prediction
	if previousPrefetchState == prefetchWaitingForCursorPrediction {
		if len(e.prefetchedCompletions) > 0 {
			comp := e.prefetchedCompletions[0]
			// Extract old lines from buffer for the completion range
			bufferLines := e.buffer.Lines()
			var oldLines []string
			for i := comp.StartLine; i <= comp.EndLineInc && i-1 < len(bufferLines); i++ {
				oldLines = append(oldLines, bufferLines[i-1])
			}
			// Find the first line that actually differs
			targetLine := text.FindFirstChangedLine(oldLines, comp.Lines, comp.StartLine-1)

			if targetLine > 0 {
				distance := abs(targetLine - e.buffer.Row())
				if distance <= e.config.CursorPrediction.DistThreshold {
					// Close enough - show completion immediately
					e.tryShowPrefetchedCompletion()
				} else {
					// Far away - show cursor prediction to that line
					e.cursorTarget = &types.CursorPredictionTarget{
						RelativePath:    e.buffer.Path(),
						LineNumber:      int32(targetLine),
						ShouldRetrigger: false, // Will use prefetched data
					}
					e.state = stateHasCursorTarget
					e.buffer.ShowCursorTarget(targetLine)
				}
			}
		}
	}
}

// tryShowPrefetchedCompletion attempts to show prefetched completion immediately.
// Returns true if completion was shown, false otherwise.
func (e *Engine) tryShowPrefetchedCompletion() bool {
	if len(e.prefetchedCompletions) == 0 {
		return false
	}

	// Sync buffer to get current cursor position
	e.syncBuffer()

	comp := e.prefetchedCompletions[0]
	cursorTarget := e.prefetchedCursorTarget

	// Clear prefetch state before processing
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchState = prefetchNone

	// Use unified processCompletion for all completion handling (including staging)
	return e.processCompletion(comp, cursorTarget)
}

// handlePrefetchError processes a prefetch error
func (e *Engine) handlePrefetchError(err error) {
	if err != nil && errors.Is(err, context.Canceled) {
		logger.Debug("prefetch canceled: %v", err)
	} else if err != nil {
		logger.Error("prefetch error: %v", err)
	} else {
		logger.Debug("prefetch error: nil")
	}
	previousPrefetchState := e.prefetchState
	e.prefetchState = prefetchNone

	// If we were waiting for prefetch due to tab press, fall back to original logic
	if previousPrefetchState == prefetchWaitingForTab {
		e.handleDeferredCursorTarget()
	}
}

// handleDeferredCursorTarget handles cursor target logic that was deferred due to prefetch in progress
func (e *Engine) handleDeferredCursorTarget() {
	if e.cursorTarget == nil {
		return
	}

	// Check if we now have prefetched completions
	if len(e.prefetchedCompletions) > 0 {
		// Sync buffer to get updated cursor position
		e.syncBuffer()

		comp := e.prefetchedCompletions[0]
		cursorTarget := e.prefetchedCursorTarget

		// Clear prefetch state before processing
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone

		// Use unified processCompletion for all completion handling (including staging)
		if e.processCompletion(comp, cursorTarget) {
			return
		}

		// No changes
		logger.Debug("no changes to completion (deferred prefetched)")
		e.handleCursorTarget()
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

// usePrefetchedCompletion attempts to use prefetched data when accepting a cursor target.
// Returns true if prefetched data was used, false if caller should handle normally.
func (e *Engine) usePrefetchedCompletion() bool {
	if len(e.prefetchedCompletions) == 0 {
		return false
	}

	// Sync buffer to get updated cursor position after move
	e.syncBuffer()

	comp := e.prefetchedCompletions[0]
	cursorTarget := e.prefetchedCursorTarget

	// Clear prefetch state before processing
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchState = prefetchNone

	// Use unified processCompletion for all completion handling (including staging)
	if e.processCompletion(comp, cursorTarget) {
		return true
	}

	// No changes - handle cursor target
	logger.Debug("no changes to completion (prefetched)")
	e.handleCursorTarget()
	return true
}
