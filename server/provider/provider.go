package provider

import (
	"context"
	"cursortab/client/openai"
	"cursortab/engine"
	"cursortab/logger"
	"cursortab/types"
	"errors"
	"fmt"
)

// Compile-time check that Provider implements engine.Provider
var _ engine.Provider = (*Provider)(nil)

// Client interface for API calls (enables mocking in tests)
type Client interface {
	DoCompletion(ctx context.Context, req *openai.CompletionRequest) (*openai.CompletionResponse, error)
	DoStreamingCompletion(ctx context.Context, req *openai.CompletionRequest, maxLines int) (*openai.StreamResult, error)
}

// Context carries data through the completion pipeline
type Context struct {
	Request      *types.CompletionRequest
	TrimmedLines []string
	WindowStart  int // 0-indexed
	WindowEnd    int // 0-indexed, exclusive
	CursorLine   int // 0-indexed within trimmed lines
	MaxLines     int // for streaming limit (0 = no limit)
	EndLineInc   int // 1-indexed inclusive end line, set by AnchorTruncation (0 = not set)
	Result       *openai.StreamResult
}

// Provider implements engine.Provider with a configurable pipeline
type Provider struct {
	Name           string
	Config         *types.ProviderConfig
	Client         Client
	Streaming      bool
	Preprocessors  []Preprocessor
	PromptBuilder  PromptBuilder
	Postprocessors []Postprocessor
}

// GetCompletion implements engine.Provider
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	defer logger.Trace("Provider.GetCompletion")()
	pctx := &Context{Request: req}

	for _, pre := range p.Preprocessors {
		if err := pre(p, pctx); err != nil {
			if errors.Is(err, ErrSkipCompletion) {
				return p.EmptyResponse(), nil
			}
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
	}

	completionReq := p.PromptBuilder(p, pctx)
	p.logRequest(completionReq, pctx.MaxLines)

	var result *openai.StreamResult
	var err error
	if p.Streaming {
		result, err = p.Client.DoStreamingCompletion(ctx, completionReq, pctx.MaxLines)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
	} else {
		var resp *openai.CompletionResponse
		resp, err = p.Client.DoCompletion(ctx, completionReq)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		result = &openai.StreamResult{}
		if len(resp.Choices) > 0 {
			result.Text = resp.Choices[0].Text
			result.FinishReason = resp.Choices[0].FinishReason
		}
	}
	pctx.Result = result
	p.logResponse(result)

	for _, post := range p.Postprocessors {
		if resp, done := post(p, pctx); done {
			return resp, nil
		}
	}

	return p.EmptyResponse(), nil
}

// EmptyResponse returns an empty completion response
func (p *Provider) EmptyResponse() *types.CompletionResponse {
	return &types.CompletionResponse{
		Completions:  []*types.Completion{},
		CursorTarget: nil,
	}
}

// BuildCompletion creates a completion response, returning empty if it's a no-op.
// startLine and endLineInc are 1-indexed.
func (p *Provider) BuildCompletion(ctx *Context, startLine, endLineInc int, lines []string) (*types.CompletionResponse, bool) {
	req := ctx.Request
	if endLineInc <= len(req.Lines) && IsNoOpReplacement(lines, req.Lines[startLine-1:endLineInc]) {
		return p.EmptyResponse(), true
	}

	completion := &types.Completion{
		StartLine:  startLine,
		EndLineInc: endLineInc,
		Lines:      lines,
	}

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, true
}

func (p *Provider) logRequest(req *openai.CompletionRequest, maxLines int) {
	logger.Debug("%s provider request:\n  URL: %s%s\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  MaxLines: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
		p.Name,
		p.Config.ProviderURL,
		p.Config.CompletionPath,
		req.Model,
		req.Temperature,
		req.MaxTokens,
		maxLines,
		len(req.Prompt),
		req.Prompt)
}

func (p *Provider) logResponse(result *openai.StreamResult) {
	logger.Debug("%s provider response:\n  Text length: %d chars\n  FinishReason: %s\n  StoppedEarly: %v\n  Text: %q",
		p.Name,
		len(result.Text),
		result.FinishReason,
		result.StoppedEarly,
		result.Text)
}
