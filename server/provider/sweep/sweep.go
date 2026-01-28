package sweep

import (
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"strings"
)

// NewProvider creates a new Sweep Next-Edit model provider
func NewProvider(config *types.ProviderConfig) *provider.Provider {
	return &provider.Provider{
		Name:      "sweep",
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
	var promptBuilder strings.Builder

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

		return &openai.CompletionRequest{
			Model:       p.Config.ProviderModel,
			Prompt:      promptBuilder.String(),
			Temperature: p.Config.ProviderTemperature,
			MaxTokens:   p.Config.ProviderMaxTokens,
			TopK:        p.Config.ProviderTopK,
			Stop:        []string{"<|file_sep|>", "</s>"},
			N:           1,
			Echo:        false,
		}
	}

	diffSection := buildDiffSection(req)
	originalLines := getTrimmedOriginalContent(req, ctx.WindowStart, len(ctx.TrimmedLines))

	if diffSection != "" {
		promptBuilder.WriteString(diffSection)
	}

	promptBuilder.WriteString("<|file_sep|>original/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(originalLines, "\n"))
	promptBuilder.WriteString("\n")

	promptBuilder.WriteString("<|file_sep|>current/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(ctx.TrimmedLines, "\n"))
	promptBuilder.WriteString("\n")

	promptBuilder.WriteString("<|file_sep|>updated/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	return &openai.CompletionRequest{
		Model:       p.Config.ProviderModel,
		Prompt:      promptBuilder.String(),
		Temperature: p.Config.ProviderTemperature,
		MaxTokens:   p.Config.ProviderMaxTokens,
		TopK:        p.Config.ProviderTopK,
		Stop:        []string{"<|file_sep|>", "</s>"},
		N:           1,
		Echo:        false,
	}
}

func buildDiffSection(req *types.CompletionRequest) string {
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

func getTrimmedOriginalContent(req *types.CompletionRequest, trimOffset, lineCount int) []string {
	sourceLines := req.PreviousLines
	if len(sourceLines) == 0 {
		sourceLines = req.Lines
	}

	windowStart := trimOffset
	windowEnd := trimOffset + lineCount

	if windowStart >= len(sourceLines) {
		return []string{}
	}
	if windowEnd > len(sourceLines) {
		windowEnd = len(sourceLines)
	}

	return sourceLines[windowStart:windowEnd]
}

func parseCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	completionText = strings.TrimSuffix(completionText, "<|file_sep|>")
	completionText = strings.TrimSuffix(completionText, "</s>")
	completionText = strings.TrimRight(completionText, " \t\n\r")

	windowStart := ctx.WindowStart
	windowEnd := ctx.WindowEnd
	if windowStart < 0 {
		windowStart = 0
	}
	if windowEnd > len(req.Lines) {
		windowEnd = len(req.Lines)
	}
	if windowStart >= windowEnd || windowStart >= len(req.Lines) {
		return p.EmptyResponse(), true
	}

	oldLines := req.Lines[windowStart:windowEnd]
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")

	if completionText == oldText {
		return p.EmptyResponse(), true
	}

	newLines := strings.Split(completionText, "\n")

	endLineInc := ctx.EndLineInc
	if endLineInc == 0 {
		endLineInc = min(windowStart+len(newLines), windowEnd)
	}

	return p.BuildCompletion(ctx, windowStart+1, endLineInc, newLines)
}
