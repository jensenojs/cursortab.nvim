package types

import "context"

// Completion represents a code completion with line range and content
type Completion struct {
	StartLine  int // 1-indexed
	EndLineInc int // 1-indexed, inclusive
	Lines      []string
}

type CompletionSource int

const (
	CompletionSourceTyping CompletionSource = iota
	CompletionSourceIdle
)

// CursorPredictionTarget represents the target for cursor jump with additional metadata
type CursorPredictionTarget struct {
	RelativePath    string
	LineNumber      int32 // 1-indexed
	ExpectedContent string
	ShouldRetrigger bool
}

// CompletionStage represents one stage of a multi-stage completion
type CompletionStage struct {
	Completion   *Completion
	CursorTarget *CursorPredictionTarget
	IsLastStage  bool
	VisualGroups []*VisualGroup // Visual groups for UI alignment
}

// VisualGroup represents consecutive changes for UI alignment
type VisualGroup struct {
	Type      string   `json:"type"`      // "modification" or "addition"
	StartLine int      `json:"startLine"` // 1-indexed relative to diff
	EndLine   int      `json:"endLine"`
	Lines     []string `json:"lines"`     // New content
	OldLines  []string `json:"oldLines"`  // Old content (for modifications)
}

// StagedCompletion holds the queue of pending stages
type StagedCompletion struct {
	Stages           []*CompletionStage
	CurrentIdx       int
	SourcePath       string
	CumulativeOffset int // Tracks line count drift after each stage accept (for unequal line counts)
}

// CompletionRequest contains all the context needed for unified completion requests
type CompletionRequest struct {
	Source        CompletionSource
	WorkspacePath string
	WorkspaceID   string
	// File context
	FilePath string
	Lines    []string
	Version  int
	// PreviousLines is the file content before the most recent edit
	PreviousLines []string
	// Multi-file diff histories in the same workspace
	FileDiffHistories []*FileDiffHistory
	// Cursor position
	CursorRow int // 1-indexed
	CursorCol int // 0-indexed
	// Viewport constraint: only set when staging is disabled (0 = no limit)
	ViewportHeight int
	// Linter errors if LSP is active
	LinterErrors *LinterErrors
}

// CompletionResponse contains both completions and cursor prediction target
type CompletionResponse struct {
	Completions  []*Completion
	CursorTarget *CursorPredictionTarget // Optional, from cursor_prediction_target
}

// LinterErrors represents linter error information for the current file
type LinterErrors struct {
	RelativeWorkspacePath string
	Errors                []*LinterError
	FileContents          string
}

// LinterError represents a single linter error
type LinterError struct {
	Message  string
	Source   string
	Severity string
	Range    *CursorRange
}

// FileDiffHistory represents cumulative diffs for a specific file in the workspace
type FileDiffHistory struct {
	FileName    string
	DiffHistory []*DiffEntry
}

// DiffEntry represents a single diff operation with structured before/after content
// This allows providers to format the diff in their required format
type DiffEntry struct {
	// Original is the content before the change (the text that was replaced/deleted)
	Original string
	// Updated is the content after the change (the new text)
	Updated string
}

// GetOriginal returns the original content (implements utils.DiffEntry interface)
func (d *DiffEntry) GetOriginal() string { return d.Original }

// GetUpdated returns the updated content (implements utils.DiffEntry interface)
func (d *DiffEntry) GetUpdated() string { return d.Updated }

// CursorRange represents a range in the file (follows LSP conventions)
type CursorRange struct {
	StartLine      int // 1-indexed
	StartCharacter int // 0-indexed
	EndLine        int // 1-indexed
	EndCharacter   int // 0-indexed
}

// Provider defines the interface that all AI providers must implement
type Provider interface {
	// GetCompletion returns code completions and optional cursor prediction target in a single response
	GetCompletion(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}

// ProviderType represents the type of provider
type ProviderType string

const (
	ProviderTypeAutoComplete ProviderType = "autocomplete"
	ProviderTypeSweep        ProviderType = "sweep"
	ProviderTypeZeta         ProviderType = "zeta"
)

// ProviderConfig holds configuration for providers
type ProviderConfig struct {
	MaxTokens int // Maximum tokens to send in a request (0 = no limit)
	// Generic provider configuration (used by Zeta, autocomplete, etc.)
	ProviderURL         string  // URL of the provider server (e.g., "http://localhost:8000")
	ProviderModel       string  // Model name
	ProviderTemperature float64 // Sampling temperature
	ProviderMaxTokens   int     // Max tokens to generate
	ProviderTopK        int     // Top-k sampling (used by some providers)
	ProviderAPIKey      string  // API key for cloud services (optional)
}
