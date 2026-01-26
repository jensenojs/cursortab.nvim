package autocomplete

import (
	"bytes"
	"context"
	"cursortab/logger"
	"cursortab/types"
	"cursortab/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider implements the types.Provider interface for autocomplete
type Provider struct {
	config      *types.ProviderConfig
	httpClient  *http.Client
	url         string
	model       string
	temperature float64
	maxTokens   int
	topK        int
}

// completionRequest matches the OpenAI Completion API format used by serve.py
type completionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Temperature float64  `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`
	TopK        int      `json:"top_k"`
	Stop        []string `json:"stop,omitempty"`
	N           int      `json:"n"`
	Echo        bool     `json:"echo"`
}

// completionResponse matches the OpenAI Completion API response format
type completionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		Text         string `json:"text"`
		Logprobs     any    `json:"logprobs"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// NewProvider creates a new autocomplete provider instance
func NewProvider(config *types.ProviderConfig) (*Provider, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	return &Provider{
		config:      config,
		httpClient:  &http.Client{},
		url:         config.ProviderURL,
		model:       config.ProviderModel,
		temperature: config.ProviderTemperature,
		maxTokens:   config.ProviderMaxTokens,
		topK:        config.ProviderTopK,
	}, nil
}

// GetCompletion implements types.Provider.GetCompletion for autocomplete
// This provider only does end-of-line completion without cursor predictions
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the prompt from the file content up to the cursor position
	prompt := p.buildPrompt(req)

	// Create the completion request
	completionReq := completionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.maxTokens,
		TopK:        p.topK,
		Stop:        []string{"\n"}, // Stop at newline for end-of-line completion
		N:           1,
		Echo:        false,
	}

	// Marshal the request
	reqBody, err := json.Marshal(completionReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.url+"/v1/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var completionResp completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check if we got any completions
	if len(completionResp.Choices) == 0 {
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil, // No cursor predictions for end-of-line completion
		}, nil
	}

	// Extract the completion text and finish reason
	completionText := completionResp.Choices[0].Text
	finishReason := completionResp.Choices[0].FinishReason

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
		logger.Info("autocomplete completion truncated: rejected (finish_reason=length, output_len=%d chars)", len(completionText))
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
		CursorTarget: nil, // No cursor predictions - this is key difference from cursor provider
	}, nil
}

// buildPrompt constructs the prompt from the file content up to the cursor position
// Uses max_tokens to limit context size (80% headroom for output)
func (p *Provider) buildPrompt(req *types.CompletionRequest) string {
	if len(req.Lines) == 0 {
		return ""
	}

	// Trim content around cursor (80% of max_tokens to leave headroom)
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	inputTokenBudget := p.config.ProviderMaxTokens * 80 / 100
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
