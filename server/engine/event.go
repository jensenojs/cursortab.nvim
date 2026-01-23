package engine

import (
	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
)

type EventType string

// Event type constants
const (
	EventEsc               EventType = "esc"
	EventTextChanged       EventType = "text_changed"
	EventTextChangeTimeout EventType = "trigger_completion"
	EventCursorMovedNormal EventType = "cursor_moved_normal"
	EventInsertEnter       EventType = "insert_enter"
	EventInsertLeave       EventType = "insert_leave"
	EventTab               EventType = "tab"
	EventIdleTimeout       EventType = "idle_timeout"
	EventCompletionReady   EventType = "completion_ready"
	EventCompletionError   EventType = "completion_error"
	EventPrefetchReady     EventType = "prefetch_ready"
	EventPrefetchError     EventType = "prefetch_error"
)

var eventTypeMap map[string]EventType

func init() {
	eventTypeMap = buildEventTypeMap()
}

func buildEventTypeMap() map[string]EventType {
	eventMap := make(map[string]EventType)

	// Create a slice of all known EventType values
	allEventTypes := []EventType{
		EventEsc,
		EventTextChanged,
		EventTextChangeTimeout,
		EventCursorMovedNormal,
		EventInsertEnter,
		EventInsertLeave,
		EventTab,
		EventIdleTimeout,
		EventCompletionReady,
		EventCompletionError,
		EventPrefetchReady,
		EventPrefetchError,
	}

	// Build the map from EventType value to string
	for _, eventType := range allEventTypes {
		eventMap[string(eventType)] = eventType
	}

	return eventMap
}

func EventTypeFromString(s string) EventType {
	if eventType, exists := eventTypeMap[s]; exists {
		return eventType
	}
	return "" // or return a default EventType
}

type Event struct {
	Type EventType
	Data any
}

func (e *Engine) handleEsc() {
	e.reject()
	e.stopIdleTimer()
}

func (e *Engine) handleTextChange() {
	// If we have an active completion, check if the user typed content that matches the prediction
	// Lua handles the visual update locally; we just need to detect match vs mismatch here
	if e.state == stateHasCompletion && len(e.completions) > 0 && e.n != nil {
		e.buffer.SyncIn(e.n, e.WorkspacePath)

		matches, hasRemaining := e.checkTypingMatchesPrediction()
		if matches {
			if hasRemaining {
				// Typing matches - Lua already updated the visual, just keep completion state
				logger.Debug("typing matches prediction, keeping completion state")
				return
			}
			// User typed everything - completion fully typed
			logger.Debug("typing matches prediction, completion fully typed")
			e.clearCompletionState()
			e.state = stateIdle
			e.startTextChangeTimer()
			return
		}
		logger.Debug("typing does not match prediction, rejecting")
	}

	e.reject()
	e.startTextChangeTimer()
}

// checkTypingMatchesPrediction checks if the current buffer state (after user typed)
// matches the prediction, meaning the user typed content consistent with the completion.
// Returns (matches, hasRemaining) where:
// - matches: true if the current buffer is a valid prefix of the target
// - hasRemaining: true if there's still content left to predict
func (e *Engine) checkTypingMatchesPrediction() (bool, bool) {
	if len(e.completions) == 0 || len(e.completionOriginalLines) == 0 {
		return false, false
	}

	completion := e.completions[0]
	targetLines := completion.Lines
	originalLines := e.completionOriginalLines

	if len(targetLines) == 0 {
		return false, false
	}

	startIdx := completion.StartLine - 1 // Convert to 0-indexed
	if startIdx < 0 || startIdx >= len(e.buffer.Lines) {
		return false, false
	}

	// For simplicity, only optimize when completion doesn't delete lines
	if len(targetLines) < len(originalLines) {
		return false, false
	}

	// Get current buffer lines in the target range
	targetEndIdx := startIdx + len(targetLines) - 1
	var currentLines []string
	for i := startIdx; i <= targetEndIdx && i < len(e.buffer.Lines); i++ {
		currentLines = append(currentLines, e.buffer.Lines[i])
	}

	if len(currentLines) == 0 {
		return false, false
	}

	// Check each current line is a prefix of the corresponding target line
	// and track if user made progress
	madeProgress := false
	for i, currentLine := range currentLines {
		if i >= len(targetLines) {
			return false, false
		}

		targetLine := targetLines[i]

		// Current line must be a prefix of target line
		if len(currentLine) > len(targetLine) {
			return false, false
		}
		if currentLine != targetLine[:len(currentLine)] {
			return false, false
		}

		// Check if user made progress compared to original
		if i < len(originalLines) {
			if currentLine != originalLines[i] && len(currentLine) > len(originalLines[i]) {
				madeProgress = true
			}
		} else if currentLine != "" {
			madeProgress = true
		}
	}

	if !madeProgress {
		return false, false
	}

	// Check if there's remaining content to predict
	hasRemaining := len(currentLines) < len(targetLines)
	if !hasRemaining {
		for i := range currentLines {
			if i < len(targetLines) && len(currentLines[i]) < len(targetLines[i]) {
				hasRemaining = true
				break
			}
		}
	}

	return true, hasRemaining
}

func (e *Engine) handleTextChangeTimeout() {
	e.requestCompletion(types.CompletionSourceTyping)
}

func (e *Engine) handleCursorMoveNormal() {
	e.reject()
	e.resetIdleTimer()
}

func (e *Engine) handleInsertEnter() {
	e.stopIdleTimer()
}

func (e *Engine) handleInsertLeave() {
	e.reject()
	e.startIdleTimer()
}

func (e *Engine) handleTab() {
	if e.n == nil {
		return
	}

	switch e.state {
	case stateHasCompletion:
		e.acceptCompletion()
	case stateHasCursorTarget:
		e.acceptCursorTarget()
	}
}

func (e *Engine) handleIdleTimeout() {
	if e.state == stateIdle {
		e.requestCompletion(types.CompletionSourceIdle)
	}
}

func (e *Engine) handleCompletionReady(response *types.CompletionResponse) {
	if e.n == nil {
		return
	}

	e.state = stateHasCompletion
	e.completions = response.Completions
	e.cursorTarget = response.CursorTarget

	if len(response.Completions) > 0 {
		completion := response.Completions[0]

		if e.buffer.HasChanges(completion.StartLine, completion.EndLineInc, completion.Lines) {
			// Has changes - check if we should split into stages
			if e.config.CursorPrediction.Enabled && e.config.CursorPrediction.DistThreshold > 0 && len(completion.Lines) > 0 {
				// Extract the original lines in the completion range
				var originalLines []string
				for i := completion.StartLine; i <= completion.EndLineInc && i-1 < len(e.buffer.Lines); i++ {
					originalLines = append(originalLines, e.buffer.Lines[i-1])
				}

				// Analyze the diff
				originalText := text.JoinLines(originalLines)
				newText := text.JoinLines(completion.Lines)
				diffResult := text.AnalyzeDiffForStaging(originalText, newText)

				// Check if we should split
				if text.ShouldSplitCompletion(diffResult, e.config.CursorPrediction.DistThreshold) {
					clusters := text.ClusterChanges(diffResult, e.config.CursorPrediction.DistThreshold)
					if len(clusters) > 1 {
						// Create staged completion
						stages := text.CreateStagesFromClusters(
							clusters,
							originalLines,
							completion.Lines,
							e.buffer.Row,
							e.buffer.Path,
							completion.StartLine, // base offset for coordinate mapping
						)

						if len(stages) > 0 {
							e.stagedCompletion = &types.StagedCompletion{
								Stages:     stages,
								CurrentIdx: 0,
								SourcePath: e.buffer.Path,
							}
							e.showCurrentStage()
							return
						}
					}
				}
			}

			// Normal single-completion flow
			e.applyBatch = e.buffer.OnCompletionReady(e.n, completion.StartLine, completion.EndLineInc, completion.Lines)

			// Store original buffer lines for partial typing optimization
			e.completionOriginalLines = nil
			for i := completion.StartLine; i <= completion.EndLineInc && i-1 < len(e.buffer.Lines); i++ {
				e.completionOriginalLines = append(e.completionOriginalLines, e.buffer.Lines[i-1])
			}
		} else {
			// NO CHANGES - handle no-op case
			logger.Debug("no changes to completion")

			if e.config.CursorPrediction.AutoAdvance && e.config.CursorPrediction.Enabled {
				// Create cursor target to last line with retrigger
				e.cursorTarget = &types.CursorPredictionTarget{
					LineNumber:      int32(completion.EndLineInc),
					ShouldRetrigger: true,
				}
				e.handleCursorTarget() // Shows jump indicator
			} else {
				e.handleCursorTarget() // Existing behavior (likely reject)
			}
			return
		}

		if len(response.Completions) > 1 {
			logger.Debug("multiple completions: %v", response.Completions)
		}

	} else {
		e.handleCursorTarget()
	}
}
