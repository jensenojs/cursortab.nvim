package inline

import (
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"strings"
)

// NewProvider creates a new inline completion provider
func NewProvider(config *types.ProviderConfig) *provider.Provider {
	return &provider.Provider{
		Name:      "inline",
		Config:    config,
		Client:    openai.NewClient(config.ProviderURL, config.CompletionPath),
		Streaming: false,
		Preprocessors: []provider.Preprocessor{
			provider.SkipIfTextAfterCursor(),
			provider.TrimContent(),
		},
		PromptBuilder: buildPrompt,
		Postprocessors: []provider.Postprocessor{
			provider.RejectEmpty(),
			provider.RejectTruncated(),
			parseCompletion,
		},
	}
}

func buildPrompt(p *provider.Provider, ctx *provider.Context) *openai.CompletionRequest {
	var promptBuilder strings.Builder

	if len(ctx.TrimmedLines) == 0 {
		return &openai.CompletionRequest{
			Model:       p.Config.ProviderModel,
			Prompt:      "",
			Temperature: p.Config.ProviderTemperature,
			MaxTokens:   p.Config.ProviderMaxTokens,
			TopK:        p.Config.ProviderTopK,
			Stop:        []string{"\n"},
			N:           1,
			Echo:        false,
		}
	}

	for i := range ctx.CursorLine {
		promptBuilder.WriteString(ctx.TrimmedLines[i])
		promptBuilder.WriteString("\n")
	}

	if ctx.CursorLine < len(ctx.TrimmedLines) {
		currentLine := ctx.TrimmedLines[ctx.CursorLine]
		cursorCol := ctx.Request.CursorCol
		if cursorCol <= len(currentLine) {
			promptBuilder.WriteString(currentLine[:cursorCol])
		} else {
			promptBuilder.WriteString(currentLine)
		}
	}

	return &openai.CompletionRequest{
		Model:       p.Config.ProviderModel,
		Prompt:      promptBuilder.String(),
		Temperature: p.Config.ProviderTemperature,
		MaxTokens:   p.Config.ProviderMaxTokens,
		TopK:        p.Config.ProviderTopK,
		Stop:        []string{"\n"},
		N:           1,
		Echo:        false,
	}
}

func parseCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	currentLine := req.Lines[req.CursorRow-1]
	cursorCol := min(req.CursorCol, len(currentLine))
	beforeCursor := currentLine[:cursorCol]

	newLine := beforeCursor + completionText
	return p.BuildCompletion(ctx, req.CursorRow, req.CursorRow, []string{newLine})
}
