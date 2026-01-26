package text

import "sort"

// Group represents consecutive changes of the same type for rendering
type Group struct {
	Type      string   // "modification", "addition", "deletion"
	StartLine int      // 1-indexed, relative to stage content
	EndLine   int      // 1-indexed, inclusive
	Lines     []string // New content
	OldLines  []string // Old content (modifications only)

	// Character-level rendering hints (single-line only)
	RenderHint string // "", "append_chars", "replace_chars", "delete_chars"
	ColStart   int    // For character-level changes
	ColEnd     int    // For character-level changes
}

// GroupChanges groups consecutive same-type changes for efficient rendering.
// Returns groups sorted by StartLine. Deletions are not grouped (they don't render as content).
// Group content is populated from change.Content and change.OldContent fields.
func GroupChanges(changes map[int]LineChange) []*Group {
	if len(changes) == 0 {
		return nil
	}

	// Get sorted line numbers, excluding deletions
	var lineNums []int
	for lineNum, change := range changes {
		if change.Type != ChangeDeletion {
			lineNums = append(lineNums, lineNum)
		}
	}

	if len(lineNums) == 0 {
		return nil
	}

	sort.Ints(lineNums)

	var groups []*Group
	var currentGroup *Group

	for _, lineNum := range lineNums {
		change := changes[lineNum]
		groupType := changeTypeToGroupType(change.Type)
		changeHasHint := getChangeRenderHint(change.Type) != ""

		// Determine if we should extend current group or start new one
		// Never extend a group if:
		// - Group types differ
		// - Lines are not consecutive
		// - Current group has a render hint (keep hinted groups single-line)
		// - New change has a render hint (each hinted change gets its own group)
		shouldStartNew := currentGroup == nil ||
			currentGroup.Type != groupType ||
			lineNum != currentGroup.EndLine+1 ||
			currentGroup.RenderHint != "" ||
			changeHasHint

		if shouldStartNew {
			// Flush current group and start new
			if currentGroup != nil {
				groups = append(groups, currentGroup)
			}
			currentGroup = &Group{
				Type:      groupType,
				StartLine: lineNum,
				EndLine:   lineNum,
				Lines:     []string{change.Content},
			}
			if groupType == "modification" {
				currentGroup.OldLines = []string{change.OldContent}
			}
			// Set RenderHint for this single-line group
			setRenderHint(currentGroup, change)
		} else {
			// Extend current group (only happens when neither has a hint)
			currentGroup.EndLine = lineNum
			currentGroup.Lines = append(currentGroup.Lines, change.Content)
			if groupType == "modification" {
				currentGroup.OldLines = append(currentGroup.OldLines, change.OldContent)
			}
		}
	}

	// Flush final group
	if currentGroup != nil {
		groups = append(groups, currentGroup)
	}

	return groups
}

// getChangeRenderHint returns the render hint for a change type
func getChangeRenderHint(ct ChangeType) string {
	switch ct {
	case ChangeAppendChars:
		return "append_chars"
	case ChangeDeleteChars:
		return "delete_chars"
	case ChangeReplaceChars:
		return "replace_chars"
	default:
		return ""
	}
}

// changeTypeToGroupType converts a ChangeType to a group type string
func changeTypeToGroupType(ct ChangeType) string {
	switch ct {
	case ChangeAddition:
		return "addition"
	case ChangeModification, ChangeAppendChars, ChangeDeleteChars, ChangeReplaceChars:
		return "modification"
	case ChangeDeletion:
		return "deletion"
	default:
		return "modification"
	}
}

// setRenderHint sets the render hint for character-level optimizations
func setRenderHint(group *Group, change LineChange) {
	switch change.Type {
	case ChangeAppendChars:
		group.RenderHint = "append_chars"
		group.ColStart = change.ColStart
		group.ColEnd = change.ColEnd
	case ChangeDeleteChars:
		group.RenderHint = "delete_chars"
		group.ColStart = change.ColStart
		group.ColEnd = change.ColEnd
	case ChangeReplaceChars:
		group.RenderHint = "replace_chars"
		group.ColStart = change.ColStart
		group.ColEnd = change.ColEnd
	default:
		group.RenderHint = ""
		group.ColStart = 0
		group.ColEnd = 0
	}
}

// CalculateCursorPosition computes optimal cursor position from changes
// Priority: modifications > additions > char-level > deletions
// Returns (line, col) where line is 1-indexed and col is 0-indexed
// Returns (-1, -1) if no cursor positioning is needed
func CalculateCursorPosition(changes map[int]LineChange, newLines []string) (int, int) {
	if len(changes) == 0 {
		return -1, -1
	}

	// Check if all changes are deletions (no cursor positioning)
	allDeletions := true
	for _, change := range changes {
		if change.Type != ChangeDeletion {
			allDeletions = false
			break
		}
	}
	if allDeletions {
		return -1, -1
	}

	// Find the best target line based on priority
	var lastModification, lastAddition, lastAppendChars, lastReplaceChars, lastDeleteChars int = -1, -1, -1, -1, -1

	for lineNum, change := range changes {
		switch change.Type {
		case ChangeModification:
			if lineNum > lastModification {
				lastModification = lineNum
			}
		case ChangeAddition:
			if lineNum > lastAddition {
				lastAddition = lineNum
			}
		case ChangeAppendChars:
			if lineNum > lastAppendChars {
				lastAppendChars = lineNum
			}
		case ChangeReplaceChars:
			if lineNum > lastReplaceChars {
				lastReplaceChars = lineNum
			}
		case ChangeDeleteChars:
			if lineNum > lastDeleteChars {
				lastDeleteChars = lineNum
			}
		}
	}

	// Priority order: modifications > additions > append/replace/delete chars
	var targetLine int = -1
	if lastModification != -1 {
		targetLine = lastModification
	} else if lastAddition != -1 {
		targetLine = lastAddition
	} else if lastAppendChars != -1 {
		targetLine = lastAppendChars
	} else if lastReplaceChars != -1 {
		targetLine = lastReplaceChars
	} else if lastDeleteChars != -1 {
		targetLine = lastDeleteChars
	}

	if targetLine <= 0 {
		return -1, -1
	}

	// Clamp to valid range
	if targetLine > len(newLines) {
		targetLine = len(newLines)
	}

	if targetLine <= 0 {
		return -1, -1
	}

	// Position at end of target line
	col := len(newLines[targetLine-1])
	return targetLine, col
}
