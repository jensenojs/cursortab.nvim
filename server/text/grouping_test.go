package text

import (
	"cursortab/assert"
	"testing"
)

func TestGroupChanges_SingleModification(t *testing.T) {
	changes := map[int]LineChange{
		1: {
			Type:       ChangeReplaceChars,
			Content:    "Hello there",
			OldContent: "Hello world",
			ColStart:   6,
			ColEnd:     11,
		},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "number of groups")
	assert.Equal(t, "modification", groups[0].Type, "group type")
	assert.Equal(t, 1, groups[0].StartLine, "start line")
	assert.Equal(t, 1, groups[0].EndLine, "end line")
	assert.Equal(t, "replace_chars", groups[0].RenderHint, "render hint")
	assert.Equal(t, 6, groups[0].ColStart, "col start")
	assert.Equal(t, 11, groups[0].ColEnd, "col end")
}

func TestGroupChanges_SingleAddition(t *testing.T) {
	changes := map[int]LineChange{
		2: {
			Type:    ChangeAddition,
			Content: "new line",
		},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "number of groups")
	assert.Equal(t, "addition", groups[0].Type, "group type")
	assert.Equal(t, 2, groups[0].StartLine, "start line")
	assert.Equal(t, 2, groups[0].EndLine, "end line")
	assert.Equal(t, "", groups[0].RenderHint, "render hint for addition")
}

func TestGroupChanges_ConsecutiveAdditions(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeAddition, Content: "line a"},
		3: {Type: ChangeAddition, Content: "line b"},
		4: {Type: ChangeAddition, Content: "line c"},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "should be grouped into one")
	assert.Equal(t, "addition", groups[0].Type, "group type")
	assert.Equal(t, 2, groups[0].StartLine, "start line")
	assert.Equal(t, 4, groups[0].EndLine, "end line")
	assert.Equal(t, 3, len(groups[0].Lines), "number of lines")
}

func TestGroupChanges_ConsecutiveModifications(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeModification, Content: "new 2", OldContent: "old 2"},
		3: {Type: ChangeModification, Content: "new 3", OldContent: "old 3"},
		4: {Type: ChangeModification, Content: "new 4", OldContent: "old 4"},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "should be grouped into one")
	assert.Equal(t, "modification", groups[0].Type, "group type")
	assert.Equal(t, 2, groups[0].StartLine, "start line")
	assert.Equal(t, 4, groups[0].EndLine, "end line")
	assert.Equal(t, 3, len(groups[0].Lines), "number of lines")
	assert.Equal(t, 3, len(groups[0].OldLines), "number of old lines")
	assert.Equal(t, "", groups[0].RenderHint, "no render hint for multi-line")
}

func TestGroupChanges_NonConsecutive(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeModification, Content: "new 2", OldContent: "old 2"},
		4: {Type: ChangeModification, Content: "new 4", OldContent: "old 4"},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 2, len(groups), "should be two groups")
	assert.Equal(t, 2, groups[0].StartLine, "first group start")
	assert.Equal(t, 4, groups[1].StartLine, "second group start")
}

func TestGroupChanges_MixedTypes(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeModification, Content: "mod", OldContent: "old"},
		3: {Type: ChangeAddition, Content: "add"},
	}

	groups := GroupChanges(changes)

	// Modification and addition are different types, so they should be separate groups
	assert.Equal(t, 2, len(groups), "should be two groups for different types")
}

func TestGroupChanges_DeletionsExcluded(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeDeletion, Content: "deleted"},
		3: {Type: ChangeAddition, Content: "added"},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "deletion should be excluded")
	assert.Equal(t, "addition", groups[0].Type, "only addition group")
}

func TestGroupChanges_OnlyDeletions(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeDeletion, Content: "deleted 1"},
		3: {Type: ChangeDeletion, Content: "deleted 2"},
	}

	groups := GroupChanges(changes)

	assert.True(t, groups == nil || len(groups) == 0, "no groups for pure deletions")
}

func TestGroupChanges_Empty(t *testing.T) {
	groups := GroupChanges(nil)
	assert.True(t, groups == nil || len(groups) == 0, "no groups for empty changes")

	groups = GroupChanges(map[int]LineChange{})
	assert.True(t, groups == nil || len(groups) == 0, "no groups for empty map")
}

func TestGroupChanges_AppendCharsHint(t *testing.T) {
	changes := map[int]LineChange{
		1: {
			Type:       ChangeAppendChars,
			Content:    "hello world",
			OldContent: "hello",
			ColStart:   5,
			ColEnd:     11,
		},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 1, len(groups), "one group")
	assert.Equal(t, "append_chars", groups[0].RenderHint, "render hint")
	assert.Equal(t, 5, groups[0].ColStart, "col start")
	assert.Equal(t, 11, groups[0].ColEnd, "col end")
}

func TestGroupChanges_MultiLineWithDifferentColumns(t *testing.T) {
	// Changes with render hints are never merged - each gets its own group
	changes := map[int]LineChange{
		1: {Type: ChangeReplaceChars, Content: "a", OldContent: "b", ColStart: 0, ColEnd: 1},
		2: {Type: ChangeReplaceChars, Content: "c", OldContent: "d", ColStart: 5, ColEnd: 6},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 2, len(groups), "hinted changes stay separate")
	assert.Equal(t, "replace_chars", groups[0].RenderHint, "first group has hint")
	assert.Equal(t, 0, groups[0].ColStart, "first group col start")
	assert.Equal(t, 1, groups[0].ColEnd, "first group col end")
	assert.Equal(t, "replace_chars", groups[1].RenderHint, "second group has hint")
	assert.Equal(t, 5, groups[1].ColStart, "second group col start")
	assert.Equal(t, 6, groups[1].ColEnd, "second group col end")
}

func TestGroupChanges_MultiLineWithIdenticalColumns(t *testing.T) {
	// Changes with render hints are never merged - each gets its own single-line group
	// This allows the Lua renderer to apply char-level rendering to each line individually
	changes := map[int]LineChange{
		1: {Type: ChangeReplaceChars, Content: "application.route()", OldContent: "app.route()", ColStart: 3, ColEnd: 11},
		2: {Type: ChangeReplaceChars, Content: "application.route()", OldContent: "app.route()", ColStart: 3, ColEnd: 11},
		3: {Type: ChangeReplaceChars, Content: "application.route()", OldContent: "app.route()", ColStart: 3, ColEnd: 11},
	}

	groups := GroupChanges(changes)

	assert.Equal(t, 3, len(groups), "each hinted change gets its own group")
	for i, group := range groups {
		assert.Equal(t, "replace_chars", group.RenderHint, "group has hint")
		assert.Equal(t, 3, group.ColStart, "col start")
		assert.Equal(t, 11, group.ColEnd, "col end")
		assert.Equal(t, i+1, group.StartLine, "start line")
		assert.Equal(t, i+1, group.EndLine, "end line")
	}
}

func TestGroupChanges_MultiLineMixedHints(t *testing.T) {
	// Multi-line groups with different change types should clear RenderHint
	changes := map[int]LineChange{
		1: {Type: ChangeReplaceChars, Content: "a", OldContent: "b", ColStart: 0, ColEnd: 1},
		2: {Type: ChangeAppendChars, Content: "cd", OldContent: "c", ColStart: 1, ColEnd: 2},
	}

	groups := GroupChanges(changes)

	// Different change types = different group types = separate groups
	assert.Equal(t, 2, len(groups), "different types = separate groups")
}

func TestCalculateCursorPosition_Modification(t *testing.T) {
	changes := map[int]LineChange{
		1: {Type: ChangeModification, Content: "modified line"},
	}
	newLines := []string{"modified line"}

	line, col := CalculateCursorPosition(changes, newLines)

	assert.Equal(t, 1, line, "cursor line")
	assert.Equal(t, 13, col, "cursor col at end of line")
}

func TestCalculateCursorPosition_Addition(t *testing.T) {
	changes := map[int]LineChange{
		2: {Type: ChangeAddition, Content: "new line"},
	}
	newLines := []string{"line 1", "new line", "line 3"}

	line, col := CalculateCursorPosition(changes, newLines)

	assert.Equal(t, 2, line, "cursor line")
	assert.Equal(t, 8, col, "cursor col at end of new line")
}

func TestCalculateCursorPosition_ModificationPriority(t *testing.T) {
	// Modification should take priority over addition
	changes := map[int]LineChange{
		1: {Type: ChangeModification, Content: "mod"},
		3: {Type: ChangeAddition, Content: "add"},
	}
	newLines := []string{"mod", "line 2", "add"}

	line, _ := CalculateCursorPosition(changes, newLines)

	assert.Equal(t, 1, line, "cursor at modification, not addition")
}

func TestCalculateCursorPosition_OnlyDeletions(t *testing.T) {
	changes := map[int]LineChange{
		1: {Type: ChangeDeletion, Content: "deleted"},
	}
	newLines := []string{}

	line, col := CalculateCursorPosition(changes, newLines)

	assert.Equal(t, -1, line, "no cursor for deletions")
	assert.Equal(t, -1, col, "no cursor col for deletions")
}

func TestCalculateCursorPosition_Empty(t *testing.T) {
	line, col := CalculateCursorPosition(nil, nil)
	assert.Equal(t, -1, line, "no cursor for empty")
	assert.Equal(t, -1, col, "no cursor col for empty")

	line, col = CalculateCursorPosition(map[int]LineChange{}, nil)
	assert.Equal(t, -1, line, "no cursor for empty map")
	assert.Equal(t, -1, col, "no cursor col for empty map")
}

func TestCalculateCursorPosition_ClampToBuffer(t *testing.T) {
	// Cursor line should be clamped to buffer size
	changes := map[int]LineChange{
		10: {Type: ChangeAddition, Content: "line"},
	}
	newLines := []string{"only", "two", "lines"}

	line, _ := CalculateCursorPosition(changes, newLines)

	assert.True(t, line <= len(newLines), "cursor clamped to buffer size")
}
