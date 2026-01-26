package sweep

import (
	"context"
	"cursortab/client/openai"
	"cursortab/logger"
	"cursortab/types"
	"cursortab/utils"
	"fmt"
	"strings"
)

// Provider implements the types.Provider interface for Sweep Next-Edit model
// Uses the Qwen2.5-Coder pretrained format with <|file_sep|> tokens
// Input trimming uses 80% of max_tokens to ensure output fits within generation limit
type Provider struct {
	config      *types.ProviderConfig
	client      *openai.Client
	model       string
	temperature float64
}

// NewProvider creates a new Sweep provider instance
func NewProvider(config *types.ProviderConfig) (*Provider, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	return &Provider{
		config:      config,
		client:      openai.NewClient(config.ProviderURL),
		model:       config.ProviderModel,
		temperature: config.ProviderTemperature,
	}, nil
}

// GetCompletion implements types.Provider.GetCompletion for Sweep
// Uses the Sweep Next-Edit format with <|file_sep|> tokens
// Uses streaming to enable early cancellation when enough lines are received
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the prompt in Sweep's format (also returns window bounds and maxLines for streaming)
	prompt, windowStart, windowEnd, maxLines := p.buildPrompt(req)

	// Create the completion request with streaming enabled
	completionReq := &openai.CompletionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.config.ProviderMaxTokens,
		Stop:        []string{"<|file_sep|>", "</s>"},
		N:           1,
		Echo:        false,
	}

	// Debug logging for request
	logger.Debug("sweep provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  MaxLines: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
		p.client.URL+"/v1/completions",
		completionReq.Model,
		completionReq.Temperature,
		completionReq.MaxTokens,
		maxLines,
		len(prompt),
		prompt)

	// Send streaming request with line limit
	result, err := p.client.DoStreamingCompletion(ctx, completionReq, maxLines)
	if err != nil {
		return nil, err
	}

	// Debug logging for response
	logger.Debug("sweep provider streaming response:\n  Text length: %d chars\n  FinishReason: %s\n  StoppedEarly: %v",
		len(result.Text), result.FinishReason, result.StoppedEarly)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(result.Text) == "" {
		logger.Debug("sweep completion text is empty after trimming")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	logger.Debug("sweep completion text (%d chars):\n%s", len(result.Text), result.Text)

	// If we stopped early due to line limit, treat as "length" finish reason for truncation handling
	finishReason := result.FinishReason
	if result.StoppedEarly {
		finishReason = "length"
	}

	// Parse the completion into a result (pass window bounds from buildPrompt)
	completion := p.parseCompletion(req, result.Text, finishReason, windowStart, windowEnd)
	if completion == nil {
		logger.Debug("sweep parseCompletion returned nil (no changes detected)")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	logger.Debug("sweep parsed completion: StartLine=%d, EndLineInc=%d, Lines=%d", completion.StartLine, completion.EndLineInc, len(completion.Lines))

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, nil
}

// buildPrompt constructs the prompt in Sweep Next-Edit format:
//
//	<|file_sep|>{changed_file}.diff
//	original:
//	{before_changes}
//	updated:
//	{after_changes}
//	<|file_sep|>original/{file_path}
//	{contents_prior_to_most_recent_change}
//	<|file_sep|>current/{file_path}
//	{current_state_of_contents}
//	<|file_sep|>updated/{file_path}
//
// The model generates the updated content after the last marker.
// Uses token-based context limiting (max_context_tokens) around the cursor.
// Returns: (prompt, windowStart, windowEnd, maxLines) where window bounds are 0-indexed line numbers.
// maxLines is 0 if no trimming occurred (no limit needed), otherwise it's the window size.
func (p *Provider) buildPrompt(req *types.CompletionRequest) (string, int, int, int) {
	var promptBuilder strings.Builder

	// Handle empty file edge case
	if len(req.Lines) == 0 {
		promptBuilder.WriteString("<|file_sep|>original/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n\n")
		promptBuilder.WriteString("<|file_sep|>current/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n\n")
		promptBuilder.WriteString("<|file_sep|>updated/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n")
		return promptBuilder.String(), 0, 0, 0
	}

	// 1. Build diff history section (already trimmed by engine)
	diffSection := p.buildDiffSection(req)

	// 2. Trim content around cursor (80% of max_tokens to leave headroom for output)
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	inputTokenBudget := p.config.ProviderMaxTokens * 80 / 100
	trimmedLines, _, _, trimOffset, didTrim := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, req.CursorCol, inputTokenBudget)

	// Calculate window bounds for parseCompletion
	windowStart := trimOffset
	windowEnd := trimOffset + len(trimmedLines)

	// Calculate max lines for streaming limit:
	// - If trimming occurred, limit to window size (content beyond window lacks context)
	// - If viewport height is provided, also limit to viewport (prevents overflow when staging disabled)
	// - If neither, no limit (model has full context, will stop at stop tokens)
	maxLines := 0
	if didTrim {
		maxLines = len(trimmedLines)
	}
	// Apply viewport constraint if provided (prevents overflow when cursor prediction is disabled)
	if req.ViewportHeight > 0 {
		if maxLines == 0 || req.ViewportHeight < maxLines {
			maxLines = req.ViewportHeight
		}
	}

	// 3. Get original content with same trim window
	originalLines := p.getTrimmedOriginalContent(req, trimOffset, len(trimmedLines))

	// Write diff section if present
	if diffSection != "" {
		promptBuilder.WriteString(diffSection)
	}

	// Add original/{file_path} section
	promptBuilder.WriteString("<|file_sep|>original/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(originalLines, "\n"))
	promptBuilder.WriteString("\n")

	// Add current/{file_path} section
	promptBuilder.WriteString("<|file_sep|>current/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(trimmedLines, "\n"))
	promptBuilder.WriteString("\n")

	// Add updated/{file_path} marker - model generates from here
	promptBuilder.WriteString("<|file_sep|>updated/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	return promptBuilder.String(), windowStart, windowEnd, maxLines
}

// buildDiffSection builds the diff history section in Sweep's format.
// Diff entries are already trimmed by the engine using max_diff_history_tokens.
func (p *Provider) buildDiffSection(req *types.CompletionRequest) string {
	if len(req.FileDiffHistories) == 0 {
		return ""
	}

	var builder strings.Builder

	for _, fileHistory := range req.FileDiffHistories {
		for _, diffEntry := range fileHistory.DiffHistory {
			if diffEntry.Original == "" && diffEntry.Updated == "" {
				continue
			}

			builder.WriteString("<|file_sep|>")
			builder.WriteString(fileHistory.FileName)
			builder.WriteString(".diff\n")
			builder.WriteString("original:\n")
			builder.WriteString(diffEntry.Original)
			builder.WriteString("\nupdated:\n")
			builder.WriteString(diffEntry.Updated)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

// getTrimmedOriginalContent returns the original content (before most recent change)
// trimmed to match the same window as the current content
func (p *Provider) getTrimmedOriginalContent(req *types.CompletionRequest, trimOffset, lineCount int) []string {
	// Use PreviousLines if available, otherwise fall back to current Lines
	sourceLines := req.PreviousLines
	if len(sourceLines) == 0 {
		sourceLines = req.Lines
	}

	// Calculate window bounds
	windowStart := trimOffset
	windowEnd := trimOffset + lineCount

	// Clamp to available lines
	if windowStart >= len(sourceLines) {
		return []string{}
	}
	if windowEnd > len(sourceLines) {
		windowEnd = len(sourceLines)
	}

	return sourceLines[windowStart:windowEnd]
}

// parseCompletion parses the model's completion (the predicted updated content)
// finishReason indicates why the model stopped: "stop" (hit stop token) or "length" (hit max_tokens)
// windowStart and windowEnd are 0-indexed line bounds from buildPrompt
func (p *Provider) parseCompletion(req *types.CompletionRequest, completionText string, finishReason string, windowStart, windowEnd int) *types.Completion {
	// The model outputs the updated window content directly
	// Strip any trailing <|file_sep|> or </s> tokens that might have leaked
	completionText = strings.TrimSuffix(completionText, "<|file_sep|>")
	completionText = strings.TrimSuffix(completionText, "</s>")
	// Trim only leading newlines (preserve indentation) and trailing whitespace
	completionText = strings.TrimLeft(completionText, "\n")
	completionText = strings.TrimRight(completionText, " \t\n\r")

	// Bounds check for window (handles empty file and edge cases)
	if windowStart < 0 {
		windowStart = 0
	}
	if windowEnd > len(req.Lines) {
		windowEnd = len(req.Lines)
	}
	if windowStart >= windowEnd || windowStart >= len(req.Lines) {
		return nil
	}

	// Get the original window content for comparison
	oldLines := req.Lines[windowStart:windowEnd]
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")

	// If the new text equals old text, no completion needed
	if completionText == oldText {
		return nil
	}

	// Split new text into lines
	newLines := strings.Split(completionText, "\n")

	// Validation 1: Anchor check - first line of output should match near start of window
	// If it matches far into the window, the model skipped content or malfunctioned
	if len(newLines) > 0 && len(oldLines) > 10 {
		firstLineAnchor := utils.FindAnchorLineFullSearch(newLines[0], oldLines)
		// Allow some tolerance (first 25% of window) for minor structural changes
		maxAllowedAnchor := len(oldLines) / 4
		if firstLineAnchor > maxAllowedAnchor {
			logger.Debug("sweep completion rejected: first line anchors at %d (max allowed %d), model likely skipped content",
				firstLineAnchor, maxAllowedAnchor)
			return nil
		}
	}

	// Validation 2: Line removal check - reject if removing more than 10% of lines
	// This catches cases where model outputs dramatically fewer lines than expected
	if len(oldLines) > 10 {
		minAllowedLines := len(oldLines) * 9 / 10 // 90% of original
		if len(newLines) < minAllowedLines {
			logger.Debug("sweep completion rejected: output has %d lines, minimum allowed %d (90%% of %d)",
				len(newLines), minAllowedLines, len(oldLines))
			return nil
		}
	}

	// Handle truncated output (finish_reason == "length") with anchor matching
	processedLines, endLineInc, shouldReject := utils.HandleTruncatedCompletionWithAnchor(newLines, oldLines, finishReason, windowStart, windowEnd)
	if shouldReject {
		logger.Debug("sweep completion rejected: only truncated content")
		return nil
	}
	newLines = processedLines

	// Log if we had to adjust for truncation
	if finishReason == "length" {
		originalLineCount := len(strings.Split(completionText, "\n"))
		logger.Info("sweep completion truncated: dropped last line, replacing lines %d-%d only (window was %d-%d, original_lines=%d, kept_lines=%d)",
			windowStart+1, endLineInc, windowStart+1, windowEnd, originalLineCount, len(newLines))
	}

	// Final check: if after processing the text equals the portion we're replacing, no completion needed
	replaceEnd := min(endLineInc-windowStart, len(oldLines))
	if utils.IsNoOpReplacement(newLines, oldLines[:replaceEnd]) {
		return nil
	}

	return &types.Completion{
		StartLine:  windowStart + 1, // Convert back to 1-indexed
		EndLineInc: endLineInc,
		Lines:      newLines,
	}
}
