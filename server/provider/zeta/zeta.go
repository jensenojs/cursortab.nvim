package zeta

import (
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"fmt"
	"strings"
)

// NewProvider creates a new Zeta provider (Zed's native model)
func NewProvider(config *types.ProviderConfig) *provider.Provider {
	return &provider.Provider{
		Name:      "zeta",
		Config:    config,
		Client:    openai.NewClient(config.ProviderURL, config.CompletionPath),
		Streaming: true,
		Preprocessors: []provider.Preprocessor{
			provider.TrimContent(),
		},
		PromptBuilder: buildPrompt,
		Postprocessors: []provider.Postprocessor{
			provider.RejectEmpty(),
			provider.ValidateAnchorPosition(0.25),
			provider.AnchorTruncation(0.75),
			parseCompletion,
		},
	}
}

func buildPrompt(p *provider.Provider, ctx *provider.Context) *openai.CompletionRequest {
	req := ctx.Request

	userExcerpt := buildUserExcerpt(req, ctx)
	userEdits := buildUserEditsFromDiffHistory(req)
	diagnosticsText := formatDiagnosticsForPrompt(req)
	prompt := buildInstructionPrompt(userEdits, diagnosticsText, userExcerpt)

	return &openai.CompletionRequest{
		Model:       p.Config.ProviderModel,
		Prompt:      prompt,
		Temperature: p.Config.ProviderTemperature,
		MaxTokens:   p.Config.ProviderMaxTokens,
		TopK:        p.Config.ProviderTopK,
		Stop:        []string{"\n<|editable_region_end|>"},
		N:           1,
		Echo:        false,
	}
}

func buildUserExcerpt(req *types.CompletionRequest, ctx *provider.Context) string {
	var promptBuilder strings.Builder

	if len(req.Lines) == 0 {
		promptBuilder.WriteString("```")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n<|start_of_file|>\n<|editable_region_start|>\n<|user_cursor_is_here|>\n<|editable_region_end|>\n```")
		return promptBuilder.String()
	}

	cursorRow := req.CursorRow
	cursorCol := req.CursorCol
	cursorLine := cursorRow - 1

	editableStart := ctx.WindowStart
	editableEnd := ctx.WindowEnd

	contextLinesBefore := 5
	contextLinesAfter := 5

	contextStart := max(0, editableStart-contextLinesBefore)
	contextEnd := min(len(req.Lines), editableEnd+contextLinesAfter)

	promptBuilder.WriteString("```")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	if contextStart == 0 {
		promptBuilder.WriteString("<|start_of_file|>\n")
	}

	for i := contextStart; i < editableStart; i++ {
		promptBuilder.WriteString(req.Lines[i])
		promptBuilder.WriteString("\n")
	}

	promptBuilder.WriteString("<|editable_region_start|>\n")

	for i := editableStart; i < cursorLine; i++ {
		promptBuilder.WriteString(req.Lines[i])
		promptBuilder.WriteString("\n")
	}

	if cursorLine < len(req.Lines) {
		currentLine := req.Lines[cursorLine]
		if cursorCol <= len(currentLine) {
			beforeCursor := currentLine[:cursorCol]
			afterCursor := currentLine[cursorCol:]

			promptBuilder.WriteString(beforeCursor)
			promptBuilder.WriteString("<|user_cursor_is_here|>")
			promptBuilder.WriteString(afterCursor)
		} else {
			promptBuilder.WriteString(currentLine)
			promptBuilder.WriteString("<|user_cursor_is_here|>")
		}
	} else {
		promptBuilder.WriteString("<|user_cursor_is_here|>")
	}

	for i := cursorLine + 1; i < editableEnd; i++ {
		promptBuilder.WriteString("\n")
		promptBuilder.WriteString(req.Lines[i])
	}

	promptBuilder.WriteString("\n<|editable_region_end|>")

	for i := editableEnd; i < contextEnd; i++ {
		promptBuilder.WriteString("\n")
		promptBuilder.WriteString(req.Lines[i])
	}

	promptBuilder.WriteString("\n```")

	return promptBuilder.String()
}

func buildUserEditsFromDiffHistory(req *types.CompletionRequest) string {
	if len(req.FileDiffHistories) == 0 {
		return ""
	}

	var editsBuilder strings.Builder
	firstEdit := true

	for _, fileHistory := range req.FileDiffHistories {
		if len(fileHistory.DiffHistory) == 0 {
			continue
		}

		for _, diffEntry := range fileHistory.DiffHistory {
			unifiedDiff := diffEntryToUnifiedDiff(diffEntry)
			if unifiedDiff == "" {
				continue
			}

			if !firstEdit {
				editsBuilder.WriteString("\n\n")
			}
			firstEdit = false

			editsBuilder.WriteString("User edited \"")
			editsBuilder.WriteString(fileHistory.FileName)
			editsBuilder.WriteString("\":\n")
			editsBuilder.WriteString("```diff\n")
			editsBuilder.WriteString(unifiedDiff)
			editsBuilder.WriteString("\n```")
		}
	}

	return editsBuilder.String()
}

func diffEntryToUnifiedDiff(entry *types.DiffEntry) string {
	if entry.Original == entry.Updated {
		return ""
	}

	originalLines := strings.Split(entry.Original, "\n")
	updatedLines := strings.Split(entry.Updated, "\n")

	var diffBuilder strings.Builder

	fmt.Fprintf(&diffBuilder, "@@ -%d,%d +%d,%d @@\n",
		1, len(originalLines), 1, len(updatedLines))

	for _, line := range originalLines {
		diffBuilder.WriteString("-")
		diffBuilder.WriteString(line)
		diffBuilder.WriteString("\n")
	}

	for _, line := range updatedLines {
		diffBuilder.WriteString("+")
		diffBuilder.WriteString(line)
		diffBuilder.WriteString("\n")
	}

	return strings.TrimSuffix(diffBuilder.String(), "\n")
}

func formatDiagnosticsForPrompt(req *types.CompletionRequest) string {
	if req.LinterErrors == nil || len(req.LinterErrors.Errors) == 0 {
		return ""
	}

	var diagBuilder strings.Builder

	diagBuilder.WriteString("Diagnostics in \"")
	diagBuilder.WriteString(req.LinterErrors.RelativeWorkspacePath)
	diagBuilder.WriteString("\":\n")
	diagBuilder.WriteString("```diagnostics\n")

	for _, err := range req.LinterErrors.Errors {
		if err.Range != nil {
			fmt.Fprintf(&diagBuilder, "line %d: ", err.Range.StartLine)
		}

		fmt.Fprintf(&diagBuilder, "[%s] %s", err.Severity, err.Message)

		if err.Source != "" {
			fmt.Fprintf(&diagBuilder, " (source: %s)", err.Source)
		}
		diagBuilder.WriteString("\n")
	}

	diagBuilder.WriteString("```")
	return diagBuilder.String()
}

func buildInstructionPrompt(userEdits, diagnostics, userExcerpt string) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("### Instruction:\n")
	promptBuilder.WriteString("You are a code completion assistant and your task is to analyze user edits and then rewrite an excerpt that the user provides, suggesting the appropriate edits within the excerpt, taking into account the cursor location.\n\n")

	promptBuilder.WriteString("### User Edits:\n\n")
	promptBuilder.WriteString(userEdits)
	promptBuilder.WriteString("\n\n")

	if diagnostics != "" {
		promptBuilder.WriteString("### Diagnostics:\n\n")
		promptBuilder.WriteString(diagnostics)
		promptBuilder.WriteString("\n\n")
	}

	promptBuilder.WriteString("### User Excerpt:\n\n")
	promptBuilder.WriteString(userExcerpt)
	promptBuilder.WriteString("\n\n")

	promptBuilder.WriteString("### Response:\n")

	return promptBuilder.String()
}

func parseCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	content := strings.ReplaceAll(completionText, "<|user_cursor_is_here|>", "")

	startMarker := "<|editable_region_start|>"
	endMarker := "<|editable_region_end|>"

	startIdx := strings.Index(content, startMarker)
	if startIdx == -1 {
		return parseSimpleCompletion(p, ctx)
	}

	content = content[startIdx:]

	newlineIdx := strings.Index(content, "\n")
	if newlineIdx == -1 {
		return p.EmptyResponse(), true
	}
	content = content[newlineIdx+1:]

	endIdx := strings.Index(content, "\n"+endMarker)
	var newText string
	if endIdx == -1 {
		newText = content
	} else {
		newText = content[:endIdx]
	}

	editableStart := ctx.WindowStart
	editableEnd := ctx.WindowEnd
	oldLines := req.Lines[editableStart:editableEnd]
	oldText := strings.Join(oldLines, "\n")

	if newText == oldText {
		return p.EmptyResponse(), true
	}

	newLines := strings.Split(newText, "\n")

	endLineInc := ctx.EndLineInc
	if endLineInc == 0 {
		endLineInc = min(editableStart+len(newLines), editableEnd)
	}

	return p.BuildCompletion(ctx, editableStart+1, endLineInc, newLines)
}

func parseSimpleCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	completionLines := strings.Split(completionText, "\n")
	if len(completionLines) == 0 {
		return p.EmptyResponse(), true
	}

	cursorRow := req.CursorRow
	cursorCol := req.CursorCol

	var resultLines []string

	if cursorRow <= len(req.Lines) {
		currentLine := req.Lines[cursorRow-1]
		beforeCursor := ""
		if cursorCol <= len(currentLine) {
			beforeCursor = currentLine[:cursorCol]
		} else {
			beforeCursor = currentLine
		}
		resultLines = append(resultLines, beforeCursor+completionLines[0])
	} else {
		resultLines = append(resultLines, completionLines[0])
	}

	resultLines = append(resultLines, completionLines[1:]...)

	endLine := cursorRow + len(completionLines) - 1
	if ctx.EndLineInc > 0 {
		endLine = ctx.EndLineInc
	}

	return p.BuildCompletion(ctx, cursorRow, endLine, resultLines)
}
