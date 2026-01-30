package engine

import (
	"cursortab/types"
)

// String returns a human-readable name for the state
func (s state) String() string {
	switch s {
	case stateIdle:
		return "Idle"
	case statePendingCompletion:
		return "PendingCompletion"
	case stateHasCompletion:
		return "HasCompletion"
	case stateHasCursorTarget:
		return "HasCursorTarget"
	case stateStreamingCompletion:
		return "StreamingCompletion"
	default:
		return "Unknown"
	}
}

// Transition represents a valid state transition in the engine's state machine
type Transition struct {
	From   state
	Event  EventType
	Action func(*Engine, Event)
}

// transitions defines all valid state transitions in the engine.
// This table serves as documentation and enables validation of state changes.
//
// State Machine:
//
//	                    TextChangeTimeout / IdleTimeout
//	  +-------+              +----------+            +-----------+
//	  | Idle  |------------->| Pending  |----------->| Streaming |
//	  +-------+              +----------+            +-----------+
//	      ^                       |                       |
//	      |                       | CompletionReady       | StreamComplete
//	      |                       v                       |
//	      |                  +-----------+                |
//	      |                  | HasCompl. |<---------------+
//	      |                  +-----------+
//	      |                       | Tab
//	      |         +-------------+-------------+
//	      |         | no cursor target          | has cursor target
//	      |         v                           v
//	      |    (prefetch?)               +--------------+
//	      |         |                    | HasCursorTgt |
//	      +<--------+                    +--------------+
//	                                          | Tab
//	                                          v
//	                                     (prefetch?) --> HasCompl. or Pending
//
//	Rejection (any -> Idle): Esc, InsertLeave, TextChanged mismatch
//	CursorMovedNormal: resets idle timer (any state)
var transitions = []Transition{
	// From stateIdle
	{stateIdle, EventTextChangeTimeout, (*Engine).doRequestCompletion},
	{stateIdle, EventIdleTimeout, (*Engine).doRequestIdleCompletion},
	{stateIdle, EventCursorMovedNormal, (*Engine).doResetIdleTimer},
	{stateIdle, EventInsertEnter, (*Engine).doStopIdleTimer},
	{stateIdle, EventInsertLeave, (*Engine).doStartIdleTimer},
	{stateIdle, EventEsc, (*Engine).doStopIdleTimer},
	{stateIdle, EventTextChanged, (*Engine).doStartTextChangeTimer},

	// From statePendingCompletion
	{statePendingCompletion, EventTextChanged, (*Engine).doTextChangePending},
	{statePendingCompletion, EventEsc, (*Engine).doReject},
	{statePendingCompletion, EventInsertLeave, (*Engine).doRejectAndStartIdleTimer},
	{statePendingCompletion, EventCursorMovedNormal, (*Engine).doResetIdleTimer},

	// From stateHasCompletion
	{stateHasCompletion, EventTab, (*Engine).doAcceptCompletion},
	{stateHasCompletion, EventEsc, (*Engine).doReject},
	{stateHasCompletion, EventTextChanged, (*Engine).doTextChangeWithCompletion},
	{stateHasCompletion, EventInsertLeave, (*Engine).doRejectAndStartIdleTimer},
	{stateHasCompletion, EventCursorMovedNormal, (*Engine).doResetIdleTimer},

	// From stateHasCursorTarget
	{stateHasCursorTarget, EventTab, (*Engine).doAcceptCursorTarget},
	{stateHasCursorTarget, EventEsc, (*Engine).doReject},
	{stateHasCursorTarget, EventTextChanged, (*Engine).doRejectAndDebounce},
	{stateHasCursorTarget, EventInsertLeave, (*Engine).doRejectAndStartIdleTimer},
	{stateHasCursorTarget, EventCursorMovedNormal, (*Engine).doResetIdleTimer},

	// From stateStreamingCompletion
	{stateStreamingCompletion, EventEsc, (*Engine).doRejectStreaming},
	{stateStreamingCompletion, EventTextChanged, (*Engine).doRejectStreamingAndDebounce},
	{stateStreamingCompletion, EventInsertLeave, (*Engine).doRejectStreamingAndStartIdleTimer},
	{stateStreamingCompletion, EventCursorMovedNormal, (*Engine).doResetIdleTimer},
}

// transitionMap provides O(1) lookup for transitions by (state, event) pair
var transitionMap map[transitionKey]*Transition

type transitionKey struct {
	from  state
	event EventType
}

func init() {
	transitionMap = make(map[transitionKey]*Transition)
	for i := range transitions {
		t := &transitions[i]
		key := transitionKey{from: t.From, event: t.Event}
		transitionMap[key] = t
	}
}

// findTransition looks up a valid transition for the given state and event.
// Returns nil if no valid transition exists.
func findTransition(from state, event EventType) *Transition {
	return transitionMap[transitionKey{from: from, event: event}]
}

// dispatch finds and executes the appropriate transition for an event.
// Returns true if a transition was found and executed, false otherwise.
// Note: The actual state change is performed by the action function,
// which allows for conditional transitions based on runtime state.
func (e *Engine) dispatch(event Event) bool {
	t := findTransition(e.state, event.Type)
	if t == nil {
		return false
	}
	if t.Action != nil {
		t.Action(e, event)
	}

	// Post-dispatch hook: InsertLeave always commits uncommitted user edits
	if event.Type == EventInsertLeave {
		e.syncBuffer()
		if e.buffer.CommitUserEdits() {
			e.saveCurrentFileState()
		}
	}

	return true
}

// Action functions for state transitions.
// These wrap the existing handler logic and manage state transitions explicitly.

func (e *Engine) doRequestCompletion(event Event) {
	e.requestCompletion(types.CompletionSourceTyping)
	// Note: requestCompletion sets state = statePendingCompletion
}

func (e *Engine) doRequestIdleCompletion(event Event) {
	if e.state == stateIdle {
		e.requestCompletion(types.CompletionSourceIdle)
	}
}

func (e *Engine) doResetIdleTimer(event Event) {
	e.reject()
	e.resetIdleTimer()
}

func (e *Engine) doStopIdleTimer(event Event) {
	e.stopIdleTimer()
}

func (e *Engine) doStartIdleTimer(event Event) {
	e.startIdleTimer()
}

func (e *Engine) doStartTextChangeTimer(event Event) {
	e.startTextChangeTimer()
}

func (e *Engine) doTextChangePending(event Event) {
	// Cancel in-flight request and restart debounce
	if e.currentCancel != nil {
		e.currentCancel()
		e.currentCancel = nil
	}
	e.state = stateIdle
	e.startTextChangeTimer()
}

func (e *Engine) doReject(event Event) {
	e.reject()
	e.stopIdleTimer()
}

func (e *Engine) doRejectAndDebounce(event Event) {
	e.reject()
	e.startTextChangeTimer()
}

func (e *Engine) doRejectAndStartIdleTimer(event Event) {
	e.reject()
	e.startIdleTimer()
}

func (e *Engine) doAcceptCompletion(event Event) {
	e.acceptCompletion()
	// Note: acceptCompletion handles state transitions internally
}

func (e *Engine) doAcceptCursorTarget(event Event) {
	e.acceptCursorTarget()
	// Note: acceptCursorTarget handles state transitions internally
}

func (e *Engine) doTextChangeWithCompletion(event Event) {
	e.handleTextChangeImpl()
	// Note: handleTextChangeImpl handles state transitions internally
}

// Streaming state action functions

func (e *Engine) doRejectStreaming(event Event) {
	e.cancelStreaming()
	e.reject()
	e.stopIdleTimer()
}

func (e *Engine) doRejectStreamingAndDebounce(event Event) {
	// For token streaming with partial results, check if typing matches
	if e.tokenStreamingState != nil && len(e.completions) > 0 {
		// Cancel the stream but preserve the partial completion state
		e.cancelTokenStreamingKeepPartial()

		// Check if the typed text matches the partial completion
		e.syncBuffer()
		matches, hasRemaining := e.checkTypingMatchesPrediction()
		if matches {
			if hasRemaining {
				// Typing matches - keep completion state
				e.state = stateHasCompletion
				return
			}
			// User typed everything
			e.clearAll()
			e.state = stateIdle
			e.startTextChangeTimer()
			return
		}
		// Doesn't match - reject
		e.reject()
		e.startTextChangeTimer()
		return
	}

	// For line streaming with partial results (first stage rendered), check if typing matches
	if e.streamingState != nil && len(e.completions) > 0 {
		// Cancel the stream but preserve the partial completion state
		e.cancelLineStreamingKeepPartial()

		// Check if the typed text matches the partial completion
		e.syncBuffer()
		matches, hasRemaining := e.checkTypingMatchesPrediction()
		if matches {
			if hasRemaining {
				// Typing matches - keep completion state
				e.state = stateHasCompletion
				return
			}
			// User typed everything
			e.clearAll()
			e.state = stateIdle
			e.startTextChangeTimer()
			return
		}
		// Doesn't match - reject
		e.reject()
		e.startTextChangeTimer()
		return
	}

	// No partial results - reject everything
	e.cancelStreaming()
	e.reject()
	e.startTextChangeTimer()
}

func (e *Engine) doRejectStreamingAndStartIdleTimer(event Event) {
	e.cancelStreaming()
	e.reject()
	e.startIdleTimer()
}
