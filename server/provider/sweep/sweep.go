package sweep

import (
	"bytes"
	"context"
	"cursortab/logger"
	"cursortab/types"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider implements the types.Provider interface for Sweep Next-Edit model
// Uses the Qwen2.5-Coder pretrained format with <|file_sep|> tokens
type Provider struct {
	config      *types.ProviderConfig
	httpClient  *http.Client
	url         string
	model       string
	temperature float64
	maxTokens   int
}

// completionRequest matches the OpenAI Completion API format
type completionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Temperature float64  `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`
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

// NewProvider creates a new Sweep provider instance
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
	}, nil
}

// GetCompletion implements types.Provider.GetCompletion for Sweep
// Uses the Sweep Next-Edit format with <|file_sep|> tokens
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the prompt in Sweep's format
	prompt := p.buildPrompt(req)

	// Create the completion request
	completionReq := completionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.maxTokens,
		Stop:        []string{"<|file_sep|>", "</s>"},
		N:           1,
		Echo:        false,
	}

	// Marshal the request without HTML escaping
	var reqBodyBuf bytes.Buffer
	encoder := json.NewEncoder(&reqBodyBuf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(completionReq); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	reqBody := reqBodyBuf.Bytes()

	// Debug logging for request
	logger.Debug("Sweep provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
		p.url+"/v1/completions",
		completionReq.Model,
		completionReq.Temperature,
		completionReq.MaxTokens,
		len(prompt),
		prompt)

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

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the response
	var completionResp completionResponse
	if err := json.Unmarshal(body, &completionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Debug logging for response
	logger.Debug("Sweep provider response:\n  ID: %s\n  Model: %s\n  Choices: %d\n  Usage: prompt=%d, completion=%d, total=%d",
		completionResp.ID,
		completionResp.Model,
		len(completionResp.Choices),
		completionResp.Usage.PromptTokens,
		completionResp.Usage.CompletionTokens,
		completionResp.Usage.TotalTokens)

	// Check if we got any completions
	if len(completionResp.Choices) == 0 {
		logger.Debug("Sweep provider returned no completions")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Extract the completion text (this is the predicted updated content)
	completionText := completionResp.Choices[0].Text
	logger.Debug("Sweep completion text (%d chars):\n%s", len(completionText), completionText)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(completionText) == "" {
		logger.Debug("Sweep completion text is empty after trimming")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Parse the completion into a result
	completion := p.parseCompletion(req, completionText)
	if completion == nil {
		logger.Debug("Sweep parseCompletion returned nil (no changes detected)")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	logger.Debug("Sweep parsed completion: StartLine=%d, EndLineInc=%d, Lines=%d", completion.StartLine, completion.EndLineInc, len(completion.Lines))

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
func (p *Provider) buildPrompt(req *types.CompletionRequest) string {
	var promptBuilder strings.Builder

	// Add recent diffs in Sweep's original/updated format
	p.addRecentDiffs(&promptBuilder, req)

	// Calculate the 21-line window (10 above + cursor line + 10 below)
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	windowStart := max(0, cursorLine-10)
	windowEnd := min(len(req.Lines), cursorLine+10+1)

	// Get the current window content
	currentWindowLines := req.Lines[windowStart:windowEnd]
	currentWindowContent := strings.Join(currentWindowLines, "\n")

	// Get the original window content (before most recent change)
	// We reconstruct this from the diff history if available
	originalWindowContent := p.getOriginalWindowContent(req, windowStart, windowEnd)

	// Add original/{file_path} section
	promptBuilder.WriteString("<|file_sep|>original/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(originalWindowContent)
	promptBuilder.WriteString("\n")

	// Add current/{file_path} section
	promptBuilder.WriteString("<|file_sep|>current/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(currentWindowContent)
	promptBuilder.WriteString("\n")

	// Add updated/{file_path} marker - model generates from here
	promptBuilder.WriteString("<|file_sep|>updated/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	return promptBuilder.String()
}

// addRecentDiffs adds the recent diffs in Sweep's original/updated format
// Uses the structured DiffEntry which contains actual before/after content
func (p *Provider) addRecentDiffs(builder *strings.Builder, req *types.CompletionRequest) {
	if len(req.FileDiffHistories) == 0 {
		return
	}

	for _, fileHistory := range req.FileDiffHistories {
		if len(fileHistory.DiffHistory) == 0 {
			continue
		}

		// Each DiffEntry contains the actual original and updated content
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
}

// getOriginalWindowContent returns the content to use for the "original/" section.
// This is the file state before the most recent change, as expected by the sweep model.
func (p *Provider) getOriginalWindowContent(req *types.CompletionRequest, windowStart, windowEnd int) string {
	// Use PreviousLines if available, otherwise fall back to current Lines
	sourceLines := req.PreviousLines
	if len(sourceLines) == 0 {
		sourceLines = req.Lines
	}

	// Clamp window bounds to available lines
	if windowStart >= len(sourceLines) {
		return ""
	}
	if windowEnd > len(sourceLines) {
		windowEnd = len(sourceLines)
	}

	windowLines := sourceLines[windowStart:windowEnd]
	return strings.Join(windowLines, "\n")
}

// parseCompletion parses the model's completion (the predicted updated content)
func (p *Provider) parseCompletion(req *types.CompletionRequest, completionText string) *types.Completion {
	// The model outputs the updated window content directly
	// Strip any trailing <|file_sep|> or </s> tokens that might have leaked
	completionText = strings.TrimSuffix(completionText, "<|file_sep|>")
	completionText = strings.TrimSuffix(completionText, "</s>")
	// Trim only leading newlines (preserve indentation) and trailing whitespace
	completionText = strings.TrimLeft(completionText, "\n")
	completionText = strings.TrimRight(completionText, " \t\n\r")

	// Calculate the window that was sent in the prompt
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	windowStart := max(0, cursorLine-10)
	windowEnd := min(len(req.Lines), cursorLine+10+1)

	// Get the original window content for comparison
	oldLines := req.Lines[windowStart:windowEnd]
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")

	// If the new text equals old text, no completion needed
	if completionText == oldText {
		return nil
	}

	// Split new text into lines
	newLines := strings.Split(completionText, "\n")

	return &types.Completion{
		StartLine:  windowStart + 1, // Convert back to 1-indexed
		EndLineInc: windowEnd,       // windowEnd is 0-indexed exclusive = 1-indexed inclusive
		Lines:      newLines,
	}
}
