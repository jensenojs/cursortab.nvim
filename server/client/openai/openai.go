package openai

import (
	"bufio"
	"bytes"
	"context"
	"cursortab/logger"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CompletionRequest matches the OpenAI Completion API format
type CompletionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Temperature float64  `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`
	TopK        int      `json:"top_k,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	N           int      `json:"n"`
	Echo        bool     `json:"echo"`
	Stream      bool     `json:"stream"`
}

// CompletionResponse matches the OpenAI Completion API response format
type CompletionResponse struct {
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

// StreamChunk represents a single SSE chunk from streaming response
type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamResult contains the result of a streaming completion
type StreamResult struct {
	Text         string
	FinishReason string
	StoppedEarly bool
}

// DefaultCompletionPath is the default API endpoint path
const DefaultCompletionPath = "/v1/completions"

// Client is a reusable OpenAI-compatible API client
type Client struct {
	HTTPClient     *http.Client
	URL            string
	CompletionPath string
}

// NewClient creates a new OpenAI-compatible client
func NewClient(url, completionPath string) *Client {
	return &Client{
		HTTPClient:     &http.Client{},
		URL:            url,
		CompletionPath: completionPath,
	}
}

// DoCompletion sends a non-streaming completion request
func (c *Client) DoCompletion(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	req.Stream = false

	body, err := c.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var resp CompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// DoStreamingCompletion sends a streaming completion request with line-count early cancellation
// maxLines: stop after receiving this many newlines (0 = no limit)
func (c *Client) DoStreamingCompletion(ctx context.Context, req *CompletionRequest, maxLines int) (*StreamResult, error) {
	req.Stream = true

	// Marshal the request without HTML escaping
	var reqBodyBuf bytes.Buffer
	encoder := json.NewEncoder(&reqBodyBuf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.URL+c.CompletionPath, &reqBodyBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// Send the request
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read streaming response with line-count early cancellation
	return c.readStreamWithLineLimit(resp.Body, maxLines), nil
}

// readStreamWithLineLimit reads SSE stream and stops after maxLines newlines
func (c *Client) readStreamWithLineLimit(body io.Reader, maxLines int) *StreamResult {
	var textBuilder strings.Builder
	var finishReason string
	lineCount := 0
	stoppedEarly := false

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Check for end of stream
		if line == "data: [DONE]" {
			break
		}

		// Parse SSE data line
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := strings.TrimPrefix(line, "data: ")
		var chunk StreamChunk
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			logger.Debug("openai stream: failed to parse chunk: %v", err)
			continue
		}

		// Extract text from chunk
		if len(chunk.Choices) > 0 {
			text := chunk.Choices[0].Text
			textBuilder.WriteString(text)

			// Count newlines in this chunk
			lineCount += strings.Count(text, "\n")

			// Check if we should stop early (maxLines > 0 means limit is enabled)
			if maxLines > 0 && lineCount >= maxLines {
				stoppedEarly = true
				logger.Debug("openai stream: stopping early at %d lines (max: %d)", lineCount, maxLines)
				break
			}

			// Capture finish reason if present
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Debug("openai stream: scanner error: %v", err)
	}

	return &StreamResult{
		Text:         textBuilder.String(),
		FinishReason: finishReason,
		StoppedEarly: stoppedEarly,
	}
}

// doRequest sends an HTTP request and returns the response body
func (c *Client) doRequest(ctx context.Context, req *CompletionRequest) ([]byte, error) {
	// Marshal the request without HTML escaping
	var reqBodyBuf bytes.Buffer
	encoder := json.NewEncoder(&reqBodyBuf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.URL+c.CompletionPath, &reqBodyBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.HTTPClient.Do(httpReq)
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

	return body, nil
}
