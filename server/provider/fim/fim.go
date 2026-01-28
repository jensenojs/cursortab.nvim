package fim

import (
	"context"
	"cursortab/client/openai"
	"cursortab/logger"
	"cursortab/types"
	"cursortab/utils"
	"fmt"
	"strings"
)

// FIM tokens for different model formats
const (
	FIMPrefixToken = "<|fim_prefix|>"
	FIMSuffixToken = "<|fim_suffix|>"
	FIMMiddleToken = "<|fim_middle|>"
)

// Provider implements the engine.Provider interface for fill-in-the-middle completion
type Provider struct {
	config      *types.ProviderConfig
	client      *openai.Client
	model       string
	temperature float64
	maxTokens   int
	topK        int
}

// NewProvider creates a new FIM provider instance
func NewProvider(config *types.ProviderConfig) (*Provider, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	return &Provider{
		config:      config,
		client:      openai.NewClient(config.ProviderURL),
		model:       config.ProviderModel,
		temperature: config.ProviderTemperature,
		maxTokens:   config.ProviderMaxTokens,
		topK:        config.ProviderTopK,
	}, nil
}

// GetCompletion implements engine.Provider.GetCompletion for fill-in-the-middle completion
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the FIM prompt with prefix and suffix
	prompt := p.buildPrompt(req)

	// Create the completion request - no stop tokens to allow multi-line output
	completionReq := &openai.CompletionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.maxTokens,
		TopK:        p.topK,
		N:           1,
		Echo:        false,
	}

	// Debug logging for request
	logger.Debug("fim provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
		p.client.URL+"/v1/completions",
		completionReq.Model,
		completionReq.Temperature,
		completionReq.MaxTokens,
		len(prompt),
		prompt)

	// Send the request
	completionResp, err := p.client.DoCompletion(ctx, completionReq)
	if err != nil {
		return nil, err
	}

	// Check if we got any completions
	if len(completionResp.Choices) == 0 {
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Extract the completion text and finish reason
	completionText := completionResp.Choices[0].Text
	finishReason := completionResp.Choices[0].FinishReason

	// Debug logging for response
	logger.Debug("fim provider response:\n  Text length: %d chars\n  FinishReason: %s\n  Text: %q",
		len(completionText), finishReason, completionText)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(completionText) == "" {
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Handle truncation - if we hit max_tokens, drop the last line as it's likely incomplete
	completionLines := strings.Split(completionText, "\n")
	if finishReason == "length" && len(completionLines) > 1 {
		logger.Debug("fim completion truncated, dropping last line")
		completionLines = completionLines[:len(completionLines)-1]
		completionText = strings.Join(completionLines, "\n")

		// If nothing left after dropping, reject
		if strings.TrimSpace(completionText) == "" {
			logger.Info("fim completion truncated: rejected after dropping incomplete line")
			return &types.CompletionResponse{
				Completions:  []*types.Completion{},
				CursorTarget: nil,
			}, nil
		}
	} else if finishReason == "length" && len(completionLines) == 1 {
		// Single line truncated - reject as incomplete
		logger.Info("fim completion truncated: rejected (finish_reason=length, single line)")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Build the completion result
	return p.buildCompletionResponse(req, completionText)
}

// buildPrompt constructs the FIM prompt with prefix and suffix context
func (p *Provider) buildPrompt(req *types.CompletionRequest) string {
	if len(req.Lines) == 0 {
		return FIMPrefixToken + FIMSuffixToken + FIMMiddleToken
	}

	// Trim content around cursor
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	inputTokenBudget := p.config.ProviderMaxTokens
	trimmedLines, newCursorRow, _, _, _ := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, req.CursorCol, inputTokenBudget)

	var prefixBuilder strings.Builder
	var suffixBuilder strings.Builder

	// Build prefix: lines before cursor + current line up to cursor
	for i := range newCursorRow {
		prefixBuilder.WriteString(trimmedLines[i])
		prefixBuilder.WriteString("\n")
	}

	// Add the current line up to cursor position
	if newCursorRow < len(trimmedLines) {
		currentLine := trimmedLines[newCursorRow]
		cursorCol := min(req.CursorCol, len(currentLine))
		prefixBuilder.WriteString(currentLine[:cursorCol])

		// Build suffix: rest of current line + remaining lines
		suffixBuilder.WriteString(currentLine[cursorCol:])
	}

	// Add remaining lines to suffix
	for i := newCursorRow + 1; i < len(trimmedLines); i++ {
		suffixBuilder.WriteString("\n")
		suffixBuilder.WriteString(trimmedLines[i])
	}

	// Construct FIM prompt: <|fim_prefix|>{prefix}<|fim_suffix|>{suffix}<|fim_middle|>
	return FIMPrefixToken + prefixBuilder.String() + FIMSuffixToken + suffixBuilder.String() + FIMMiddleToken
}

// buildCompletionResponse creates the completion response from the generated text
func (p *Provider) buildCompletionResponse(req *types.CompletionRequest, completionText string) (*types.CompletionResponse, error) {
	// Get the current line and cursor position
	currentLine := ""
	if req.CursorRow >= 1 && req.CursorRow <= len(req.Lines) {
		currentLine = req.Lines[req.CursorRow-1]
	}
	cursorCol := min(req.CursorCol, len(currentLine))

	// Split completion into lines
	completionLines := strings.Split(completionText, "\n")

	// Build the result lines
	// First line: prefix from current line + first completion line
	beforeCursor := currentLine[:cursorCol]
	afterCursor := currentLine[cursorCol:]

	resultLines := make([]string, len(completionLines))
	resultLines[0] = beforeCursor + completionLines[0]

	// Middle lines are taken directly from completion
	for i := 1; i < len(completionLines); i++ {
		resultLines[i] = completionLines[i]
	}

	// Last line needs the suffix from after cursor appended
	resultLines[len(resultLines)-1] += afterCursor

	// Calculate end line - completion replaces from cursor row
	// For single-line completion, it's just the current row
	// For multi-line, it extends to cursor row + (completion lines - 1)
	endLine := req.CursorRow
	if len(completionLines) > 1 {
		endLine = req.CursorRow + len(completionLines) - 1
	}

	// Check if result would be identical to original (no-op)
	if len(resultLines) == 1 && endLine == req.CursorRow {
		if resultLines[0] == currentLine {
			return &types.CompletionResponse{
				Completions:  []*types.Completion{},
				CursorTarget: nil,
			}, nil
		}
	}

	completion := &types.Completion{
		StartLine:  req.CursorRow,
		EndLineInc: req.CursorRow, // FIM replaces only the current line range, inserts new content
		Lines:      resultLines,
	}

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, nil
}
