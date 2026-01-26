package engine

import (
	"context"

	"cursortab/buffer"
	"cursortab/text"
	"cursortab/types"
)

// Buffer defines the interface for buffer operations.
// Implemented by buffer.NvimBuffer for Neovim integration.
type Buffer interface {
	// Sync reads current state from editor
	Sync(workspacePath string) (*buffer.SyncResult, error)

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
	PrepareCompletion(startLine, endLineInc int, lines []string, groups []*text.Group) buffer.Batch
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

// Provider defines the interface that all AI providers must implement.
// Implemented by autocomplete.Provider, sweep.Provider, zeta.Provider.
type Provider interface {
	// GetCompletion returns code completions and optional cursor prediction target
	GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error)
}
