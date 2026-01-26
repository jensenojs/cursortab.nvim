package engine

import (
	"cursortab/logger"
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

// handleTextChangeImpl handles text change when we have an active completion.
// It checks if the user typed content that matches the prediction.
func (e *Engine) handleTextChangeImpl() {
	if len(e.completions) == 0 {
		e.reject()
		e.startTextChangeTimer()
		return
	}

	e.syncBuffer()

	matches, hasRemaining := e.checkTypingMatchesPrediction()
	if matches {
		if hasRemaining {
			// Typing matches - Lua already updated the visual, just keep completion state
			logger.Debug("typing matches prediction, keeping completion state")
			return
		}
		// User typed everything - completion fully typed
		logger.Debug("typing matches prediction, completion fully typed")
		e.clearAll()
		e.state = stateIdle
		e.startTextChangeTimer()
		return
	}

	logger.Debug("typing does not match prediction, rejecting")
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
	bufferLines := e.buffer.Lines()

	if len(targetLines) == 0 {
		return false, false
	}

	startIdx := completion.StartLine - 1 // Convert to 0-indexed
	if startIdx < 0 || startIdx >= len(bufferLines) {
		return false, false
	}

	// For simplicity, only optimize when completion doesn't delete lines
	if len(targetLines) < len(originalLines) {
		return false, false
	}

	// Get current buffer lines in the target range
	targetEndIdx := startIdx + len(targetLines) - 1
	var currentLines []string
	for i := startIdx; i <= targetEndIdx && i < len(bufferLines); i++ {
		currentLines = append(currentLines, bufferLines[i])
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

// handleCompletionReadyImpl contains the actual completion handling logic
func (e *Engine) handleCompletionReadyImpl(response *types.CompletionResponse) {
	// Sync buffer to get current cursor position - the user may have moved
	// the cursor while we were waiting for the completion
	e.syncBuffer()

	if len(response.Completions) == 0 {
		e.handleCursorTarget()
		return
	}

	completion := response.Completions[0]

	// Use unified processCompletion for all completion handling
	if e.processCompletion(completion, response.CursorTarget) {
		if len(response.Completions) > 1 {
			logger.Debug("multiple completions: %v", response.Completions)
		}
		return
	}

	// No changes - handle no-op case
	logger.Debug("no changes to completion")
	if e.config.CursorPrediction.AutoAdvance && e.config.CursorPrediction.Enabled {
		e.cursorTarget = &types.CursorPredictionTarget{
			LineNumber:      int32(completion.EndLineInc),
			ShouldRetrigger: true,
		}
	}
	e.handleCursorTarget()
}
