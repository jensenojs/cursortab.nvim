package utils

import (
	"cursortab/text"
	"strings"
)

// Token estimation constants
const (
	AvgCharsPerToken = 2 // Conservative estimate for mixed content (code + JSON)
)

// EstimateTokenCount estimates the number of tokens in a slice of strings
func EstimateTokenCount(lines []string) int {
	if len(lines) == 0 {
		return 0
	}

	totalChars := 0
	for _, line := range lines {
		totalChars += len(line) + 1 // +1 for newline
	}

	return (totalChars + AvgCharsPerToken - 1) / AvgCharsPerToken // Ceiling division
}

// EstimateCharsFromTokens estimates the number of characters for a given token count
func EstimateCharsFromTokens(tokens int) int {
	return tokens * AvgCharsPerToken
}

// TrimContentAroundCursor trims the content to fit within maxTokens while preserving
// context around the cursor position. Returns the trimmed lines, adjusted cursor position,
// trim offset, and whether trimming occurred.
func TrimContentAroundCursor(lines []string, cursorRow, cursorCol, maxTokens int) ([]string, int, int, int, bool) {
	// Handle empty file
	if len(lines) == 0 {
		return lines, 0, cursorCol, 0, false
	}

	// Clamp cursor to valid range
	if cursorRow < 0 {
		cursorRow = 0
	}
	if cursorRow >= len(lines) {
		cursorRow = len(lines) - 1
	}

	if maxTokens <= 0 {
		return lines, cursorRow, cursorCol, 0, false
	}

	maxChars := EstimateCharsFromTokens(maxTokens)

	// Calculate total content size
	totalChars := 0
	for _, line := range lines {
		totalChars += len(line) + 1 // +1 for newline
	}

	// If content is already within limits, return as-is
	if totalChars <= maxChars {
		return lines, cursorRow, cursorCol, 0, false
	}

	// Balanced approach: allocate half budget before cursor, half after
	// This ensures we see context both above AND below the cursor
	cursorLineChars := len(lines[cursorRow]) + 1
	remainingBudget := maxChars - cursorLineChars
	halfBudget := remainingBudget / 2

	// Expand BEFORE cursor (up to half budget)
	startLine := cursorRow
	charsBefore := 0
	for startLine > 0 && charsBefore < halfBudget {
		newChars := len(lines[startLine-1]) + 1
		if charsBefore+newChars <= halfBudget {
			startLine--
			charsBefore += newChars
		} else {
			break
		}
	}

	// Expand AFTER cursor (up to half budget + any unused from before)
	unusedBefore := halfBudget - charsBefore
	budgetAfter := halfBudget + unusedBefore
	endLine := cursorRow
	charsAfter := 0
	for endLine < len(lines)-1 && charsAfter < budgetAfter {
		newChars := len(lines[endLine+1]) + 1
		if charsAfter+newChars <= budgetAfter {
			endLine++
			charsAfter += newChars
		} else {
			break
		}
	}

	// If we have unused budget after expanding down, try expanding up more
	unusedAfter := budgetAfter - charsAfter
	if unusedAfter > 0 {
		for startLine > 0 {
			newChars := len(lines[startLine-1]) + 1
			if charsBefore+newChars <= halfBudget+unusedAfter {
				startLine--
				charsBefore += newChars
			} else {
				break
			}
		}
	}

	// Extract the trimmed lines
	trimmedLines := make([]string, endLine-startLine+1)
	copy(trimmedLines, lines[startLine:endLine+1])

	// Adjust cursor position
	newCursorRow := cursorRow - startLine

	// Return trim offset (how many lines were removed from the start)
	trimOffset := startLine

	return trimmedLines, newCursorRow, cursorCol, trimOffset, true
}

// DiffEntry interface for token limiting - matches types.DiffEntry
type DiffEntry interface {
	GetOriginal() string
	GetUpdated() string
}

// TrimDiffEntries trims diff entries to fit within maxTokens.
// Keeps the most recent entries and removes older ones if over limit.
func TrimDiffEntries[T DiffEntry](diffs []T, maxTokens int) []T {
	if len(diffs) == 0 || maxTokens <= 0 {
		return diffs
	}

	maxChars := EstimateCharsFromTokens(maxTokens)

	// Iterate from newest (end) to oldest (start), keeping entries within limit
	totalChars := 0
	cutoffIndex := 0

	for i := len(diffs) - 1; i >= 0; i-- {
		entryChars := len(diffs[i].GetOriginal()) + len(diffs[i].GetUpdated())
		if totalChars+entryChars > maxChars && i < len(diffs)-1 {
			cutoffIndex = i + 1
			break
		}
		totalChars += entryChars
	}

	if cutoffIndex > 0 {
		return diffs[cutoffIndex:]
	}
	return diffs
}

// FindAnchorLine searches for the best matching line in oldLines for the given needle.
// Searches in a window around expectedPos to handle structural changes (adds/removes).
// Returns the index in oldLines or -1 if no good match found.
func FindAnchorLine(needle string, oldLines []string, expectedPos int) int {
	if len(oldLines) == 0 {
		return -1
	}

	bestIdx := -1
	bestSimilarity := 0.7 // Similarity threshold

	// Search in a window around expected position
	// Look 2 lines before and 5 lines after to handle adds/removes
	searchStart := max(0, expectedPos-2)
	searchEnd := min(len(oldLines), expectedPos+5)

	for i := searchStart; i < searchEnd; i++ {
		similarity := text.LineSimilarity(needle, oldLines[i])
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	return bestIdx
}

// FindAnchorLineFullSearch searches the entire oldLines array for the best matching line.
// Used for validation to detect if model output is misaligned with expected window.
// Returns the index in oldLines or -1 if no good match found.
func FindAnchorLineFullSearch(needle string, oldLines []string) int {
	if len(oldLines) == 0 {
		return -1
	}

	bestIdx := -1
	bestSimilarity := 0.7 // Similarity threshold

	for i, line := range oldLines {
		similarity := text.LineSimilarity(needle, line)
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	return bestIdx
}

// HandleTruncatedCompletion processes completion lines when the model hits max_tokens.
// When finishReason is "length", the output is incomplete:
// 1. Drop the last line (likely truncated mid-content)
// 2. Only replace lines we have content for, leave the rest untouched
// Returns: (processedLines, adjustedEndLineInc, shouldReject)
func HandleTruncatedCompletion(
	newLines []string,
	finishReason string,
	windowStart, windowEnd int,
) ([]string, int, bool) {
	endLineInc := windowEnd

	// Handle truncated output (finish_reason == "length")
	if finishReason == "length" && len(newLines) > 0 {
		// Drop the last line (truncated)
		newLines = newLines[:len(newLines)-1]

		// If nothing left after dropping, reject the completion
		if len(newLines) == 0 {
			return nil, 0, true
		}

		// Only replace lines we have content for, leave the rest untouched
		endLineInc = windowStart + len(newLines)
	}

	return newLines, endLineInc, false
}

// HandleTruncatedCompletionWithAnchor processes completion lines when the model hits max_tokens,
// using anchor matching to find the correct replacement range.
// When finishReason is "length", the output is incomplete:
// 1. Drop the last line (likely truncated mid-content)
// 2. Find where the last model line maps to in the original (handles adds/removes)
// 3. Replace up to the anchor line, preserving content beyond
// Returns: (processedLines, adjustedEndLineInc, shouldReject)
func HandleTruncatedCompletionWithAnchor(
	newLines []string,
	oldLines []string,
	finishReason string,
	windowStart, windowEnd int,
) ([]string, int, bool) {
	endLineInc := windowEnd

	// Handle truncated output (finish_reason == "length")
	if finishReason == "length" && len(newLines) > 0 {
		// Drop the last line (truncated)
		newLines = newLines[:len(newLines)-1]

		// If nothing left after dropping, reject the completion
		if len(newLines) == 0 {
			return nil, 0, true
		}

		// Find where the last model line maps to in the original
		lastModelLine := newLines[len(newLines)-1]
		expectedPos := len(newLines) - 1
		anchorIdx := FindAnchorLine(lastModelLine, oldLines, expectedPos)

		if anchorIdx != -1 {
			// Found anchor - replace from start to anchor line (inclusive)
			endLineInc = windowStart + anchorIdx + 1
		} else {
			// No anchor found - use conservative approach (simple length-based)
			endLineInc = windowStart + len(newLines)
		}
	}

	return newLines, endLineInc, false
}

// IsNoOpReplacement checks if replacing oldLines with newLines would result in no change.
// Compares joined and whitespace-trimmed content.
func IsNoOpReplacement(newLines, oldLines []string) bool {
	newText := strings.TrimRight(strings.Join(newLines, "\n"), " \t\n\r")
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")
	return newText == oldText
}
