package text

import (
	"cursortab/logger"
	"cursortab/types"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/neovim/go-client/nvim"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type BufferConfig struct {
	NsID int
}

type Buffer struct {
	Lines         []string
	Row           int // 1-indexed
	Col           int // 0-indexed
	Path          string
	Version       int
	DiffHistories []*types.DiffEntry // Structured diff history for provider consumption
	PreviousLines []string           // Buffer content before the most recent edit (for sweep provider)

	originalLines    []string // Original file content when editing session started
	lastModifiedLine int      // Track which line was last modified
	id               nvim.Buffer
	scrollOffsetX    int // Horizontal scroll offset (leftcol)

	// Viewport bounds (1-indexed line numbers)
	ViewportTop    int // First visible line (1-indexed)
	ViewportBottom int // Last visible line (1-indexed)

	config BufferConfig

	// Pending completion state (committed only on accept)
	pendingStartLine        int
	pendingEndLineInclusive int
	pendingLines            []string
	hasPending              bool
}

func NewBuffer(config BufferConfig) (*Buffer, error) {
	return &Buffer{
		Lines:                   []string{},
		Row:                     1,
		Col:                     0,
		Path:                    "",
		Version:                 0,
		DiffHistories:           []*types.DiffEntry{},
		PreviousLines:           []string{},
		originalLines:           []string{},
		lastModifiedLine:        -1,
		id:                      nvim.Buffer(0),
		scrollOffsetX:           0,
		config:                  config,
		pendingStartLine:        0,
		pendingEndLineInclusive: 0,
		pendingLines:            nil,
		hasPending:              false,
	}, nil
}

func (b *Buffer) MoveCursorToStartOfLine(n *nvim.Nvim, line int, center bool, mark bool) error {
	batch := n.NewBatch()
	applyCursorMove(batch, line, 0, center, mark)
	batch.ExecLua("vim.cmd('normal! ^')", nil, nil) // Move cursor to start of line
	return batch.Execute()
}

// SetFileContext restores file-specific state when switching back to a previously edited file.
// This is called by the engine after detecting a file switch.
func (b *Buffer) SetFileContext(previousLines []string, diffHistories []*types.DiffEntry, originalLines []string) {
	if previousLines != nil {
		b.PreviousLines = make([]string, len(previousLines))
		copy(b.PreviousLines, previousLines)
	} else if originalLines != nil {
		// For new files without previous state, initialize PreviousLines to the original
		// file content. This ensures providers (like sweep) have a valid "before" state
		// to compare against, rather than falling back to current content.
		b.PreviousLines = make([]string, len(originalLines))
		copy(b.PreviousLines, originalLines)
	} else {
		b.PreviousLines = nil
	}

	if diffHistories != nil {
		b.DiffHistories = make([]*types.DiffEntry, len(diffHistories))
		copy(b.DiffHistories, diffHistories)
	} else {
		b.DiffHistories = []*types.DiffEntry{}
	}

	if originalLines != nil {
		b.originalLines = make([]string, len(originalLines))
		copy(b.originalLines, originalLines)
	}
}

// GetOriginalLines returns the original file content when editing session started
func (b *Buffer) GetOriginalLines() []string {
	return b.originalLines
}

func applyCursorMove(batch *nvim.Batch, line, col int, center bool, mark bool) {
	if mark {
		batch.ExecLua("vim.cmd('normal! m\\'')", nil, nil)
	}
	batch.SetWindowCursor(0, [2]int{line, col})
	if center {
		batch.ExecLua("vim.cmd('normal! zz')", nil, nil)
	}
}

func (b *Buffer) executeLuaFunction(n *nvim.Nvim, luaCode string, args ...any) {
	batch := n.NewBatch()
	if len(args) > 0 {
		batch.ExecLua(luaCode, nil, args...)
	} else {
		batch.ExecLua(luaCode, nil, nil)
	}
	if err := batch.Execute(); err != nil {
		logger.Error("error executing lua function: %v", err)
	}
}

// SyncInResult contains information about the sync operation
type SyncInResult struct {
	BufferChanged bool   // True if we switched to a different buffer
	OldPath       string // Path of the previous buffer (empty if no previous)
	NewPath       string // Path of the current buffer
}

func (b *Buffer) SyncIn(n *nvim.Nvim, workspacePath string) *SyncInResult {
	// Use batch API to make all calls in a single round-trip
	batch := n.NewBatch()

	var currentBuf nvim.Buffer
	var path string
	var lines [][]byte
	var window nvim.Window
	var cursor [2]int
	var scrollOffset int
	var viewportBounds [2]int

	batch.CurrentBuffer(&currentBuf)
	batch.BufferName(nvim.Buffer(0), &path) // Use 0 for current buffer
	batch.BufferLines(nvim.Buffer(0), 0, -1, false, &lines)
	batch.CurrentWindow(&window)
	batch.WindowCursor(nvim.Window(0), &cursor) // Use 0 for current window

	// Get horizontal scroll offset (leftcol) from current window
	batch.ExecLua(`
		local view = vim.fn.winsaveview()
		return view.leftcol or 0
	`, &scrollOffset, nil)

	// Get vertical viewport bounds (first and last visible line numbers, 1-indexed)
	batch.ExecLua(`
		return {vim.fn.line("w0"), vim.fn.line("w$")}
	`, &viewportBounds, nil)

	if err := batch.Execute(); err != nil {
		logger.Error("error executing sync batch: %v", err)
		return nil
	}

	linesStr := make([]string, len(lines))
	for i, line := range lines {
		linesStr[i] = string(line[:])
	}

	// Store old path before updating
	oldPath := b.Path

	// Update buffer state
	b.Lines = linesStr
	b.Row = cursor[0]              // Line (vertical position, 1-based in nvim cursor)
	b.Col = cursor[1]              // Column (horizontal position, 0-based in nvim cursor)
	b.scrollOffsetX = scrollOffset // Horizontal scroll offset

	// Update viewport bounds (1-indexed)
	b.ViewportTop = viewportBounds[0]
	b.ViewportBottom = viewportBounds[1]

	// Convert absolute path to relative workspace path
	relativePath := makeRelativeToWorkspace(path, workspacePath)
	b.Path = relativePath

	// Handle buffer change
	if b.id != currentBuf {
		// New buffer - update buffer ID and reset basic state
		// Note: PreviousLines, DiffHistories, and originalLines are managed by the engine
		// to enable proper context restoration when switching back to this file
		b.id = currentBuf
		b.lastModifiedLine = -1
		b.Version = 0

		return &SyncInResult{
			BufferChanged: true,
			OldPath:       oldPath,
			NewPath:       relativePath,
		}
	}

	// Same buffer - no change
	return &SyncInResult{
		BufferChanged: false,
		OldPath:       oldPath,
		NewPath:       relativePath,
	}
}

// Helper function to convert absolute path to relative workspace path
func makeRelativeToWorkspace(absolutePath, workspacePath string) string {
	absolutePath = filepath.Clean(absolutePath)
	workspacePath = filepath.Clean(workspacePath)

	// If the file is within the workspace, make it relative
	if relativePath, found := strings.CutPrefix(absolutePath, workspacePath); found {
		relativePath = strings.TrimPrefix(relativePath, string(filepath.Separator))
		return relativePath
	}

	return absolutePath
}

func (b *Buffer) getDiffResult(startLine, endLineInclusive int, lines []string) *DiffResult {
	originalLines := []string{}
	for i := startLine; i <= endLineInclusive && i-1 < len(b.Lines); i++ {
		originalLines = append(originalLines, b.Lines[i-1])
	}
	oldText := JoinLines(originalLines)
	newText := JoinLines(lines)
	return ComputeDiff(oldText, newText)
}

func (b *Buffer) getApplyBatch(n *nvim.Nvim, startLine, endLineInclusive int, lines []string, diffResult *DiffResult) *nvim.Batch {
	// Create apply batch for the completion
	applyBatch := n.NewBatch()

	b.clearNamespace(applyBatch, b.config.NsID)

	placeBytes := make([][]byte, len(lines))
	for i, line := range lines {
		placeBytes[i] = []byte(line)
	}

	// execute lua to actually clear the lines within the range beforehand
	applyBatch.ExecLua(fmt.Sprintf("vim.cmd('normal! %v,%vd')", startLine, endLineInclusive), nil, nil)

	applyBatch.SetBufferLines(b.id, startLine-1, endLineInclusive, false, placeBytes)

	// Apply cursor positioning from diff changes
	cursorLine, cursorCol := CalculateCursorPosition(diffResult.Changes, lines)
	if cursorLine >= 0 && cursorCol >= 0 {
		// Convert from diff line numbers (relative to new text) to buffer line numbers
		bufferLine := startLine + cursorLine - 1
		applyCursorMove(applyBatch, bufferLine, cursorCol, false, true)
	}

	// Mark as pending; actual commit happens on accept
	b.pendingStartLine = startLine
	b.pendingEndLineInclusive = endLineInclusive
	b.pendingLines = append([]string{}, lines...)
	b.hasPending = true

	return applyBatch
}

func (b *Buffer) OnCompletionReady(n *nvim.Nvim, startLine, endLineInclusive int, lines []string) *nvim.Batch {
	return b.OnCompletionReadyWithGroups(n, startLine, endLineInclusive, lines, nil)
}

// OnCompletionReadyWithGroups shows a completion with optional pre-computed groups
func (b *Buffer) OnCompletionReadyWithGroups(n *nvim.Nvim, startLine, endLineInclusive int, lines []string, groups []*Group) *nvim.Batch {
	// Compute diff
	diffResult := b.getDiffResult(startLine, endLineInclusive, lines)

	// Get original lines for grouping
	var originalLines []string
	for i := startLine; i <= endLineInclusive && i-1 < len(b.Lines); i++ {
		originalLines = append(originalLines, b.Lines[i-1])
	}

	// Use pre-computed groups or compute fresh ones
	if groups == nil && len(diffResult.Changes) > 0 {
		groups = GroupChanges(diffResult.Changes)
	}

	applyBatch := b.getApplyBatch(n, startLine, endLineInclusive, lines, diffResult)

	// Convert to Lua format
	luaDiffResult := diffResultToLuaFormat(diffResult, groups, lines, startLine, endLineInclusive)

	// Debug logging for data sent to Lua
	if jsonData, err := json.Marshal(luaDiffResult); err == nil {
		logger.Debug("sending to lua on_completion_ready:\n  startLine: %d\n  endLineInclusive: %d\n  lines: %d\n  diffResult: %s",
			startLine, endLineInclusive, len(lines), string(jsonData))
	}

	b.executeLuaFunction(n, "require('cursortab').on_completion_ready(...)", luaDiffResult)

	return applyBatch
}

// diffResultToLuaFormat converts diff result and groups to a format suitable for Lua rendering
func diffResultToLuaFormat(diffResult *DiffResult, groups []*Group, newLines []string, startLine, endLineInclusive int) map[string]any {
	// Compute cursor position
	cursorLine, cursorCol := CalculateCursorPosition(diffResult.Changes, newLines)

	// Build groups array for Lua
	var luaGroups []map[string]any
	for _, g := range groups {
		luaGroup := map[string]any{
			"type":       g.Type,
			"start_line": g.StartLine,
			"end_line":   g.EndLine,
			"lines":      g.Lines,
			"old_lines":  g.OldLines,
		}

		// Add render hint for character-level optimizations
		if g.RenderHint != "" {
			luaGroup["render_hint"] = g.RenderHint
			luaGroup["col_start"] = g.ColStart
			luaGroup["col_end"] = g.ColEnd
		}

		luaGroups = append(luaGroups, luaGroup)
	}

	return map[string]any{
		"startLine":        startLine,
		"endLineInclusive": endLineInclusive,
		"groups":           luaGroups,
		"cursor_line":      cursorLine,
		"cursor_col":       cursorCol,
	}
}

// extractGranularDiffs analyzes old and new lines and returns DiffEntry records
// for each contiguous region that changed. This avoids storing unchanged context
// when a provider (like sweep) replaces a large window but only modifies a few lines.
func extractGranularDiffs(oldLines, newLines []string) []*types.DiffEntry {
	oldText := JoinLines(oldLines)
	newText := JoinLines(newLines)

	if oldText == newText {
		return nil
	}

	dmp := diffmatchpatch.New()
	chars1, chars2, lineArray := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(chars1, chars2, false)
	lineDiffs := dmp.DiffCharsToLines(diffs, lineArray)

	var entries []*types.DiffEntry

	for i := 0; i < len(lineDiffs); i++ {
		diff := lineDiffs[i]

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			// No change, skip
			continue

		case diffmatchpatch.DiffDelete:
			// Get the deleted content (trim trailing newline from diff output)
			deletedText := strings.TrimSuffix(diff.Text, "\n")
			insertedText := ""

			// Check if followed by an insert (modification pattern)
			if i+1 < len(lineDiffs) && lineDiffs[i+1].Type == diffmatchpatch.DiffInsert {
				insertedText = strings.TrimSuffix(lineDiffs[i+1].Text, "\n")
				i++ // Skip the insert in next iteration
			}

			entries = append(entries, &types.DiffEntry{
				Original: deletedText,
				Updated:  insertedText,
			})

		case diffmatchpatch.DiffInsert:
			// Pure insertion (no preceding delete)
			insertedText := strings.TrimSuffix(diff.Text, "\n")
			entries = append(entries, &types.DiffEntry{
				Original: "",
				Updated:  insertedText,
			})
		}
	}

	return entries
}

// CommitPendingEdit applies the pending edit to buffer state, increments version,
// and appends structured diff entries showing before/after content. No-op if no pending edit.
//
// Uses granular diff extraction to create separate DiffEntry records for each
// contiguous changed region, rather than one large entry for the entire replaced range.
// This ensures diff history accurately reflects what actually changed.
func (b *Buffer) CommitPendingEdit() {
	if !b.hasPending {
		return
	}

	startLine := b.pendingStartLine
	endLineInclusive := b.pendingEndLineInclusive
	lines := b.pendingLines

	// Extract only the affected original lines (the range being replaced)
	var originalRangeLines []string
	for i := startLine; i <= endLineInclusive && i-1 < len(b.originalLines); i++ {
		originalRangeLines = append(originalRangeLines, b.originalLines[i-1])
	}

	// Extract granular diffs - one DiffEntry per contiguous changed region
	diffEntries := extractGranularDiffs(originalRangeLines, lines)
	b.DiffHistories = append(b.DiffHistories, diffEntries...)

	// Compute the final buffer state after applying the completion
	newLines := make([]string, 0, len(b.Lines)-((endLineInclusive-startLine)+1)+len(lines))
	if startLine-1 > 0 && startLine-1 <= len(b.Lines) {
		newLines = append(newLines, b.Lines[:startLine-1]...)
	}
	newLines = append(newLines, lines...)
	if endLineInclusive < len(b.Lines) {
		newLines = append(newLines, b.Lines[endLineInclusive:]...)
	}

	// Reset checkpoint to current state for next working diff
	b.originalLines = make([]string, len(newLines))
	copy(b.originalLines, newLines)

	// Save current lines as previous state BEFORE updating (for sweep provider)
	b.PreviousLines = make([]string, len(b.Lines))
	copy(b.PreviousLines, b.Lines)

	// Commit the new content and bump version
	b.Lines = make([]string, len(newLines))
	copy(b.Lines, newLines)
	b.Version++

	// Clear pending
	b.pendingStartLine = 0
	b.pendingEndLineInclusive = 0
	b.pendingLines = nil
	b.hasPending = false
}

func (b *Buffer) OnCursorPredictionReady(n *nvim.Nvim, line int) {
	logger.Debug("sending to lua on_cursor_prediction_ready: line=%d", line)
	b.executeLuaFunction(n, "require('cursortab').on_cursor_prediction_ready(...)", line)
}

func (b *Buffer) OnReject(n *nvim.Nvim) {
	// Clear pending state to prevent stale data from being committed
	b.hasPending = false
	b.pendingLines = nil

	logger.Debug("sending to lua on_reject")
	b.executeLuaFunction(n, `require('cursortab').on_reject()`)
}

func (b *Buffer) clearNamespace(batch *nvim.Batch, nsID int) {
	batch.ClearBufferNamespace(b.id, nsID, 0, -1)
}

// hasChanges checks if the proposed completion would introduce actual changes
func (b *Buffer) HasChanges(startLine, endLineInclusive int, lines []string) bool {
	// Check the original replacement range for changes
	for i := startLine; i <= endLineInclusive; i++ {
		relativeLineIdx := i - startLine

		var l *string
		var realL *string

		if i-1 >= 0 && i-1 < len(b.Lines) {
			realL = &b.Lines[i-1]
		}

		if relativeLineIdx < len(lines) {
			l = &lines[relativeLineIdx]
		}

		if (l != nil && realL != nil && *l != *realL) ||
			(l != nil && realL == nil) ||
			(l == nil && realL != nil) {
			return true
		}
	}

	// Check if there are additional lines beyond the replacement range (insertions)
	if startLine+len(lines)-1 > endLineInclusive {
		return true
	}

	return false
}

// GetLinterErrors retrieves Neovim diagnostics for the current buffer and returns them in a simple format
func (b *Buffer) GetLinterErrors(n *nvim.Nvim) *LinterErrors {
	// First check if LSP is available for this buffer
	batch := n.NewBatch()
	var hasLsp bool

	// Check if there are any active LSP clients attached to this buffer
	batch.ExecLua(fmt.Sprintf(`
		local clients = vim.lsp.get_clients and vim.lsp.get_clients({bufnr = %d}) or vim.lsp.get_active_clients({bufnr = %d})
		return #clients > 0
	`, int(b.id), int(b.id)), &hasLsp, nil)

	if err := batch.Execute(); err != nil {
		logger.Error("error checking LSP availability: %v", err)
		return nil
	}

	// If no LSP is available, return nil
	if !hasLsp {
		return nil
	}

	// Use batch API to get diagnostics for the current buffer
	batch = n.NewBatch()

	var diagnostics []map[string]any
	var bufferContents [][]byte

	// Get diagnostics using vim.diagnostic.get() for this specific buffer
	batch.ExecLua(fmt.Sprintf(`
		local diagnostics = vim.diagnostic.get(%d)
		return diagnostics
	`, int(b.id)), &diagnostics, nil)

	// Get buffer contents for the linter errors
	batch.BufferLines(b.id, 0, -1, false, &bufferContents)

	if err := batch.Execute(); err != nil {
		logger.Error("error getting linter errors: %v", err)
		return nil
	}

	if len(diagnostics) == 0 {
		return nil
	}

	// Convert buffer contents to string
	contentLines := make([]string, len(bufferContents))
	for i, line := range bufferContents {
		contentLines[i] = string(line)
	}
	fileContents := strings.Join(contentLines, "\n")

	// Convert Neovim diagnostics to LinterError format
	linterErrors := make([]*LinterError, 0, len(diagnostics))

	for _, diag := range diagnostics {
		linterError := &LinterError{
			Message: getString(diag, "message"),
			Source:  getString(diag, "source"),
		}

		// Convert severity
		if severity, ok := diag["severity"].(float64); ok {
			switch int(severity) {
			case 1:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
			case 2:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_WARNING"
			case 3:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_INFORMATION"
			case 4:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_HINT"
			default:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
			}
		} else {
			linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
		}

		// Convert range information
		if lnum := getNumber(diag, "lnum"); lnum != -1 {
			if col := getNumber(diag, "col"); col != -1 {
				endLnum := lnum
				endCol := col

				if endL := getNumber(diag, "end_lnum"); endL != -1 {
					endLnum = endL
				}
				if endC := getNumber(diag, "end_col"); endC != -1 {
					endCol = endC
				}

				linterError.Range = &CursorRange{
					StartLine:      lnum,
					StartCharacter: col,
					EndLine:        endLnum,
					EndCharacter:   endCol,
				}
			}
		}

		linterErrors = append(linterErrors, linterError)
	}

	return &LinterErrors{
		RelativeWorkspacePath: b.Path,
		Errors:                linterErrors,
		FileContents:          fileContents,
	}
}

// GetProviderLinterErrors retrieves Neovim diagnostics and converts them to provider.LinterErrors format
func (b *Buffer) GetProviderLinterErrors(n *nvim.Nvim) *types.LinterErrors {
	linterErrors := b.GetLinterErrors(n)

	if linterErrors == nil {
		return nil
	}

	// Convert to provider format
	providerErrors := make([]*types.LinterError, len(linterErrors.Errors))
	for i, err := range linterErrors.Errors {
		var providerRange *types.CursorRange
		if err.Range != nil {
			providerRange = &types.CursorRange{
				StartLine:      err.Range.StartLine,
				StartCharacter: err.Range.StartCharacter,
				EndLine:        err.Range.EndLine,
				EndCharacter:   err.Range.EndCharacter,
			}
		}

		providerErrors[i] = &types.LinterError{
			Message:  err.Message,
			Source:   err.Source,
			Severity: err.Severity,
			Range:    providerRange,
		}
	}

	return &types.LinterErrors{
		RelativeWorkspacePath: linterErrors.RelativeWorkspacePath,
		Errors:                providerErrors,
		FileContents:          linterErrors.FileContents,
	}
}

// Helper types for internal linter error handling
type LinterErrors struct {
	RelativeWorkspacePath string
	Errors                []*LinterError
	FileContents          string
}

type LinterError struct {
	Message  string
	Source   string
	Severity string
	Range    *CursorRange
}

type CursorRange struct {
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
}

// Helper function to safely get string from map
func getString(m map[string]any, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// Helper function to safely get number from map, handling both int and float64
func getNumber(m map[string]any, key string) int {
	if val, ok := m[key].(int); ok {
		return val
	}
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	if val, ok := m[key].(int32); ok {
		return int(val)
	}
	if val, ok := m[key].(int64); ok {
		return int(val)
	}
	return -1
}
