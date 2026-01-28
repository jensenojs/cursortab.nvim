package fim

import (
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"strings"
)

// Default FIM tokens (Qwen/DeepSeek-Coder style)
const (
	defaultPrefixToken = "<|fim_prefix|>"
	defaultSuffixToken = "<|fim_suffix|>"
	defaultMiddleToken = "<|fim_middle|>"
)

// NewProvider creates a new fill-in-the-middle completion provider
func NewProvider(config *types.ProviderConfig) *provider.Provider {
	return &provider.Provider{
		Name:      "fim",
		Config:    config,
		Client:    openai.NewClient(config.ProviderURL, config.CompletionPath),
		Streaming: false,
		Preprocessors: []provider.Preprocessor{
			provider.TrimContent(),
		},
		PromptBuilder: buildPrompt,
		Postprocessors: []provider.Postprocessor{
			provider.RejectEmpty(),
			provider.DropLastLineIfTruncated(),
			parseCompletion,
		},
	}
}

// getFIMTokens returns the FIM tokens from config or defaults
func getFIMTokens(config *types.ProviderConfig) (prefix, suffix, middle string) {
	if config.FIMTokens != nil {
		prefix = config.FIMTokens.Prefix
		suffix = config.FIMTokens.Suffix
		middle = config.FIMTokens.Middle
	}
	if prefix == "" {
		prefix = defaultPrefixToken
	}
	if suffix == "" {
		suffix = defaultSuffixToken
	}
	if middle == "" {
		middle = defaultMiddleToken
	}
	return
}

func buildPrompt(p *provider.Provider, ctx *provider.Context) *openai.CompletionRequest {
	prefixToken, suffixToken, middleToken := getFIMTokens(p.Config)
	var prompt string

	if len(ctx.TrimmedLines) == 0 {
		prompt = prefixToken + suffixToken + middleToken
	} else {
		var prefixBuilder strings.Builder
		var suffixBuilder strings.Builder

		for i := range ctx.CursorLine {
			prefixBuilder.WriteString(ctx.TrimmedLines[i])
			prefixBuilder.WriteString("\n")
		}

		if ctx.CursorLine < len(ctx.TrimmedLines) {
			currentLine := ctx.TrimmedLines[ctx.CursorLine]
			cursorCol := min(ctx.Request.CursorCol, len(currentLine))
			prefixBuilder.WriteString(currentLine[:cursorCol])
			suffixBuilder.WriteString(currentLine[cursorCol:])
		}

		for i := ctx.CursorLine + 1; i < len(ctx.TrimmedLines); i++ {
			suffixBuilder.WriteString("\n")
			suffixBuilder.WriteString(ctx.TrimmedLines[i])
		}

		prompt = prefixToken + prefixBuilder.String() + suffixToken + suffixBuilder.String() + middleToken
	}

	return &openai.CompletionRequest{
		Model:       p.Config.ProviderModel,
		Prompt:      prompt,
		Temperature: p.Config.ProviderTemperature,
		MaxTokens:   p.Config.ProviderMaxTokens,
		TopK:        p.Config.ProviderTopK,
		N:           1,
		Echo:        false,
	}
}

func parseCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	currentLine := ""
	if req.CursorRow >= 1 && req.CursorRow <= len(req.Lines) {
		currentLine = req.Lines[req.CursorRow-1]
	}
	cursorCol := min(req.CursorCol, len(currentLine))

	completionLines := strings.Split(completionText, "\n")

	beforeCursor := currentLine[:cursorCol]
	afterCursor := currentLine[cursorCol:]

	resultLines := make([]string, len(completionLines))
	resultLines[0] = beforeCursor + completionLines[0]

	for i := 1; i < len(completionLines); i++ {
		resultLines[i] = completionLines[i]
	}

	resultLines[len(resultLines)-1] += afterCursor

	// FIM inserts content at cursor position - always replace only the current line
	return p.BuildCompletion(ctx, req.CursorRow, req.CursorRow, resultLines)
}
