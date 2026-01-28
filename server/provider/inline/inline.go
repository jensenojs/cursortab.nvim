package inline

import (
	"context"
	"cursortab/client/openai"
	"cursortab/logger"
	"cursortab/types"
	"cursortab/utils"
	"fmt"
	"strings"
)

// Provider implements the engine.Provider interface for inline completion
type Provider struct {
	config      *types.ProviderConfig
	client      *openai.Client
	model       string
	temperature float64
	maxTokens   int
	topK        int
}

// NewProvider creates a new inline provider instance
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

// GetCompletion implements engine.Provider.GetCompletion for inline completion
// This provider only does end-of-line completion without cursor predictions
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Skip completion if there's text after the cursor
	if req.CursorRow >= 1 && req.CursorRow <= len(req.Lines) {
		currentLine := req.Lines[req.CursorRow-1]
		if req.CursorCol < len(currentLine) {
			logger.Debug("inline: skipping completion, text after cursor")
			return &types.CompletionResponse{
				Completions:  []*types.Completion{},
				CursorTarget: nil,
			}, nil
		}
	}

	// Build the prompt from the file content up to the cursor position
	prompt := p.buildPrompt(req)

	// Create the completion request
	completionReq := &openai.CompletionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.maxTokens,
		TopK:        p.topK,
		Stop:        []string{"\n"}, // Stop at newline for end-of-line completion
		N:           1,
		Echo:        false,
	}

	// Debug logging for request
	logger.Debug("inline provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
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
	logger.Debug("inline provider response:\n  Text length: %d chars\n  FinishReason: %s\n  Text: %q",
		len(completionText), finishReason, completionText)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(completionText) == "" {
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// For single-line completions, if we hit max_tokens (finish_reason == "length"),
	// it means the completion was truncated - we should reject it as incomplete
	if finishReason == "length" {
		logger.Info("inline completion truncated: rejected (finish_reason=length, output_len=%d chars)", len(completionText))
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Build the completion result
	// For end-of-line completion, we replace from cursor position to end of current line
	currentLine := req.Lines[req.CursorRow-1]
	cursorCol := min(req.CursorCol, len(currentLine))
	beforeCursor := currentLine[:cursorCol]
	afterCursor := currentLine[cursorCol:]

	// If the completion matches what's already after the cursor, no change needed
	if completionText == afterCursor {
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	newLine := beforeCursor + completionText

	completion := &types.Completion{
		StartLine:  req.CursorRow,
		EndLineInc: req.CursorRow,
		Lines:      []string{newLine},
	}

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, nil
}

// buildPrompt constructs the prompt from the file content up to the cursor position
// Uses max_tokens to limit context size
func (p *Provider) buildPrompt(req *types.CompletionRequest) string {
	if len(req.Lines) == 0 {
		return ""
	}

	// Trim content around cursor
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	inputTokenBudget := p.config.ProviderMaxTokens
	trimmedLines, newCursorRow, _, _, _ := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, req.CursorCol, inputTokenBudget)

	var promptBuilder strings.Builder

	// Add lines before the cursor (within trimmed window)
	for i := range newCursorRow {
		promptBuilder.WriteString(trimmedLines[i])
		promptBuilder.WriteString("\n")
	}

	// Add the current line up to the cursor position
	if newCursorRow < len(trimmedLines) {
		currentLine := trimmedLines[newCursorRow]
		if req.CursorCol <= len(currentLine) {
			promptBuilder.WriteString(currentLine[:req.CursorCol])
		} else {
			promptBuilder.WriteString(currentLine)
		}
	}

	return promptBuilder.String()
}
