package buffer

import (
	"cursortab/text"
	"cursortab/types"
)

// Client defines the interface for buffer operations.
// Engine depends on this interface, allowing test mocks.
type Client interface {
	// Sync reads current state from editor
	Sync(workspacePath string) (*SyncResult, error)

	// State accessors
	Lines() []string
	Row() int
	Col() int
	Path() string
	Version() int
	ViewportBounds() (top, bottom int)

	// File context
	PreviousLines() []string
	OriginalLines() []string
	DiffHistories() []*types.DiffEntry
	SetFileContext(prev, orig []string, diffs []*types.DiffEntry)

	// Completion lifecycle
	HasChanges(startLine, endLineInc int, lines []string) bool
	PrepareCompletion(startLine, endLineInc int, lines []string, groups []*text.Group) Batch
	CommitPending()

	// UI operations
	ShowCursorTarget(line int) error
	ClearUI() error
	MoveCursor(line int, center, mark bool) error

	// LSP
	LinterErrors() *types.LinterErrors

	// Event registration (for nvim RPC handler)
	RegisterEventHandler(handler func(event string)) error
}

// Batch represents deferred editor operations
type Batch interface {
	Execute() error
}

// SyncResult contains state after syncing with editor
type SyncResult struct {
	BufferChanged bool
	OldPath       string
	NewPath       string
}
