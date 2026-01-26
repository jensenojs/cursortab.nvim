package text

import (
	"cursortab/assert"
	"fmt"
	"testing"
)

// assertChangesEqual compares two changes maps
func assertChangesEqual(t *testing.T, expected, actual map[int]LineChange) {
	t.Helper()

	for lineNum, expectedChange := range expected {
		actualChange, exists := actual[lineNum]
		assert.True(t, exists, fmt.Sprintf("change at line %d exists", lineNum))
		if !exists {
			continue
		}
		assertLineChangeEqual(t, expectedChange, actualChange)
	}

	for lineNum := range actual {
		_, exists := expected[lineNum]
		assert.True(t, exists, fmt.Sprintf("no unexpected change at line %d", lineNum))
	}
}

// assertLineChangeEqual compares two LineChange objects
func assertLineChangeEqual(t *testing.T, expected, actual LineChange) {
	t.Helper()

	assert.Equal(t, expected.Type, actual.Type, "Type")
	assert.Equal(t, expected.Content, actual.Content, "Content")
	assert.Equal(t, expected.OldContent, actual.OldContent, "OldContent")
	assert.Equal(t, expected.ColStart, actual.ColStart, "ColStart")
	assert.Equal(t, expected.ColEnd, actual.ColEnd, "ColEnd")
}

func TestChangeDeletion(t *testing.T) {
	text1 := "line 1\nline 2\nline 3\nline 4"
	text2 := "line 1\nline 3\nline 4"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:    ChangeDeletion,
			Content: "line 2",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeAddition(t *testing.T) {
	text1 := "line 1\nline 3\nline 4"
	text2 := "line 1\nline 2\nline 3\nline 4"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:    ChangeAddition,
			Content: "line 2",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeAppendChars(t *testing.T) {
	text1 := "Hello world"
	text2 := "Hello world!"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeAppendChars,
			Content:    "Hello world!",
			OldContent: "Hello world",
			ColStart:   11,
			ColEnd:     12,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeDeleteChars(t *testing.T) {
	text1 := "Hello world!"
	text2 := "Hello world"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeDeleteChars,
			Content:    "Hello world",
			OldContent: "Hello world!",
			ColStart:   11,
			ColEnd:     12,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeDeleteCharsMiddle(t *testing.T) {
	text1 := "Hello world John"
	text2 := "Hello John"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeDeleteChars,
			Content:    "Hello John",
			OldContent: "Hello world John",
			ColStart:   6,
			ColEnd:     12,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeReplaceChars(t *testing.T) {
	text1 := "Hello world"
	text2 := "Hello there"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeReplaceChars,
			Content:    "Hello there",
			OldContent: "Hello world",
			ColStart:   6,
			ColEnd:     11,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeReplaceCharsMiddle(t *testing.T) {
	text1 := "Hello world John"
	text2 := "Hello there John"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeReplaceChars,
			Content:    "Hello there John",
			OldContent: "Hello world John",
			ColStart:   6,
			ColEnd:     11,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeModificationAndAddition(t *testing.T) {
	text1 := `function hello() {
    console.log("old message");
    return true;
}`

	text2 := `function hello() {
    console.log("new message");
    return true;
    console.log("added line");
}`

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:       ChangeReplaceChars,
			Content:    `    console.log("new message");`,
			OldContent: `    console.log("old message");`,
			ColStart:   17,
			ColEnd:     20,
		},
		4: {
			Type:    ChangeAddition,
			Content: `    console.log("added line");`,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestMultipleDeletions(t *testing.T) {
	text1 := "line 1\nline 2\nline 3\nline 4\nline 5"
	text2 := "line 1\nline 3\nline 5"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:    ChangeDeletion,
			Content: "line 2",
		},
		4: {
			Type:    ChangeDeletion,
			Content: "line 4",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestMultipleAdditions(t *testing.T) {
	text1 := "line 1\nline 3\nline 5"
	text2 := "line 1\nline 2\nline 3\nline 4\nline 5"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:    ChangeAddition,
			Content: "line 2",
		},
		4: {
			Type:    ChangeAddition,
			Content: "line 4",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestMultipleCharacterChanges(t *testing.T) {
	text1 := "Hello world\nGoodbye world\nWelcome world"
	text2 := "Hello there\nGoodbye there\nWelcome there"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeReplaceChars,
			Content:    "Hello there",
			OldContent: "Hello world",
			ColStart:   6,
			ColEnd:     11,
		},
		2: {
			Type:       ChangeReplaceChars,
			Content:    "Goodbye there",
			OldContent: "Goodbye world",
			ColStart:   8,
			ColEnd:     13,
		},
		3: {
			Type:       ChangeReplaceChars,
			Content:    "Welcome there",
			OldContent: "Welcome world",
			ColStart:   8,
			ColEnd:     13,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestMixedCharacterOperations(t *testing.T) {
	text1 := "Hello world\nGoodbye world!\nWelcome world"
	text2 := "Hello there\nGoodbye world\nWelcome there!"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeReplaceChars,
			Content:    "Hello there",
			OldContent: "Hello world",
			ColStart:   6,
			ColEnd:     11,
		},
		2: {
			Type:       ChangeDeleteChars,
			Content:    "Goodbye world",
			OldContent: "Goodbye world!",
			ColStart:   13,
			ColEnd:     14,
		},
		3: {
			Type:       ChangeReplaceChars,
			Content:    "Welcome there!",
			OldContent: "Welcome world",
			ColStart:   8,
			ColEnd:     14,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestChangeModification(t *testing.T) {
	text1 := "start middle end"
	text2 := "beginning middle finish extra"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeModification,
			Content:    "beginning middle finish extra",
			OldContent: "start middle end",
			ColStart:   0,
			ColEnd:     0,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestNoChanges(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 0, len(actual.Changes), "no changes")
}

func TestConsecutiveModifications(t *testing.T) {
	text1 := `function test() {
    start middle end
    start middle end
    start middle end
}`

	text2 := `function test() {
    beginning middle finish extra
    beginning middle finish extra
    beginning middle finish extra
}`

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:       ChangeModification,
			Content:    "    beginning middle finish extra",
			OldContent: "    start middle end",
		},
		3: {
			Type:       ChangeModification,
			Content:    "    beginning middle finish extra",
			OldContent: "    start middle end",
		},
		4: {
			Type:       ChangeModification,
			Content:    "    beginning middle finish extra",
			OldContent: "    start middle end",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestConsecutiveAdditions(t *testing.T) {
	text1 := `function test() {
    return true;
}`

	text2 := `function test() {
    let x = 1;
    let y = 2;
    let z = 3;
    return true;
}`

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		2: {
			Type:    ChangeAddition,
			Content: "    let x = 1;",
		},
		3: {
			Type:    ChangeAddition,
			Content: "    let y = 2;",
		},
		4: {
			Type:    ChangeAddition,
			Content: "    let z = 3;",
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestMixedChangesNoGrouping(t *testing.T) {
	text1 := `function test() {
    let x = 1;
    console.log("test");
    let y = 2;
}`

	text2 := `function test() {
    let x = 10;
    console.log("test");
    let y = 20;
}`

	actual := ComputeDiff(text1, text2)

	// Should have changes for lines 2 and 4 (not consecutive)
	assert.True(t, len(actual.Changes) > 0, "changes detected")
}

func TestLineChangeClassification(t *testing.T) {
	tests := []struct {
		name     string
		oldLine  string
		newLine  string
		expected ChangeType
	}{
		{
			name:     "Simple word replacement - should be replace_chars",
			oldLine:  "Hello world",
			newLine:  "Hello there",
			expected: ChangeReplaceChars,
		},
		{
			name:     "Multiple changes - should be modification",
			oldLine:  "start middle end",
			newLine:  "beginning middle finish extra",
			expected: ChangeModification,
		},
		{
			name:     "Single word change - should be replace_chars",
			oldLine:  "let x = 1;",
			newLine:  "let x = 10;",
			expected: ChangeReplaceChars,
		},
		{
			name:     "Complex restructuring - should be modification",
			oldLine:  "function hello() { return true; }",
			newLine:  "async function hello() { const result = await process(); return result; }",
			expected: ChangeModification,
		},
		{
			name:     "Append at end - should be append_chars",
			oldLine:  "Hello world",
			newLine:  "Hello world!",
			expected: ChangeAppendChars,
		},
		{
			name:     "App to server replacement - should be replace_chars",
			oldLine:  `app.route("/health", health);`,
			newLine:  `server.route("/health", health);`,
			expected: ChangeReplaceChars,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diffType, _, _ := categorizeLineChangeWithColumns(test.oldLine, test.newLine)
			assert.Equal(t, test.expected, diffType, "change classification")
		})
	}
}

func TestEmptyOldText(t *testing.T) {
	text1 := ""
	text2 := "line 1\nline 2\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.True(t, len(actual.Changes) > 0, "changes when adding content to empty file")
}

func TestEmptyNewText(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := ""

	actual := ComputeDiff(text1, text2)

	assert.True(t, len(actual.Changes) > 0, "changes when deleting all content")

	// Should all be deletions
	for _, change := range actual.Changes {
		assert.Equal(t, ChangeDeletion, change.Type, "all deletions")
	}
}

func TestSingleLineFile(t *testing.T) {
	text1 := "hello"
	text2 := "hello world"

	actual := ComputeDiff(text1, text2)

	expected := map[int]LineChange{
		1: {
			Type:       ChangeAppendChars,
			Content:    "hello world",
			OldContent: "hello",
			ColStart:   5,
			ColEnd:     11,
		},
	}

	assertChangesEqual(t, expected, actual.Changes)
}

func TestAdditionAtFirstLine(t *testing.T) {
	text1 := "line 2\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[1]
	assert.True(t, exists, "addition at line 1 exists")
	assert.Equal(t, ChangeAddition, change.Type, "addition type")
}

func TestMultipleAdditionsAtBeginning(t *testing.T) {
	text1 := "original line"
	text2 := "new line 1\nnew line 2\nnew line 3\noriginal line"

	actual := ComputeDiff(text1, text2)

	assert.True(t, len(actual.Changes) > 0, "changes for additions at beginning")
}

func TestModificationAtFirstLine(t *testing.T) {
	text1 := "old content\nline 2"
	text2 := "new content here\nline 2"

	actual := ComputeDiff(text1, text2)

	_, exists := actual.Changes[1]
	assert.True(t, exists, "change at line 1")
}

func TestAdditionAtEndOfFile(t *testing.T) {
	text1 := "line 1\nline 2\n"
	text2 := "line 1\nline 2\nline 3\nline 4\n"

	actual := ComputeDiff(text1, text2)

	hasAddition := false
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition {
			hasAddition = true
			break
		}
	}
	assert.True(t, hasAddition, "at least one addition")
}

func TestDeletionAtFirstLine(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 2\nline 3"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[1]
	assert.True(t, exists, "deletion at line 1 exists")
	assert.Equal(t, ChangeDeletion, change.Type, "deletion type")
}

func TestDeletionAtLastLine(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 2"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[3]
	assert.True(t, exists, "deletion at line 3 exists")
	assert.Equal(t, ChangeDeletion, change.Type, "deletion type")
}

func TestIdenticalLineMarkedAsModification(t *testing.T) {
	oldText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr


if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]`

	newText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr


if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]
    print(bubble_sort(arr))`

	actual := ComputeDiff(oldText, newText)

	// Check that line 11 is NOT in changes (it's identical in both)
	if change, exists := actual.Changes[11]; exists {
		assert.False(t, change.Content == change.OldContent,
			"line 11 should not be marked as change when content == oldContent")
	}

	// Line 12 should be an addition
	change, exists := actual.Changes[12]
	assert.True(t, exists, "line 12 exists")
	assert.Equal(t, ChangeAddition, change.Type, "line 12 is addition")
}

func TestIfCompletionBug(t *testing.T) {
	oldText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr

if `

	newText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr

if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]
    sorted_arr = bubble_sort(arr)
    print(sorted_arr)`

	actual := ComputeDiff(oldText, newText)

	// Line 9 ("if " -> "if __name__ == "__main__":") should be append_chars, not deletion
	change9, exists := actual.Changes[9]
	assert.True(t, exists, "change at line 9 exists")
	assert.False(t, change9.Type == ChangeDeletion, "line 9 not categorized as deletion")
	assert.Equal(t, ChangeAppendChars, change9.Type, "line 9 is append_chars")
	assert.Equal(t, "if ", change9.OldContent, "oldContent")
}

func TestSingleLineToMultipleLinesWithSpacesReproduceBug(t *testing.T) {
	oldText := "def test"
	newText := `def test():
    print("test")

test()



`

	actual := ComputeDiff(oldText, newText)

	assert.True(t, len(actual.Changes) >= 2, "at least 2 changes detected")
}

// =============================================================================
// Tests for unequal line count scenarios (insertions/deletions)
// =============================================================================

func TestLineMapping_EqualLineCounts(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nmodified\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.NotNil(t, actual.LineMapping, "LineMapping")
	assert.Equal(t, 3, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 3, actual.NewLineCount, "NewLineCount")

	assert.Equal(t, 1, actual.LineMapping.NewToOld[0], "new line 1 maps to old line 1")
	assert.Equal(t, 3, actual.LineMapping.NewToOld[2], "new line 3 maps to old line 3")
	assert.Equal(t, 2, actual.LineMapping.NewToOld[1], "new line 2 maps to old line 2")
}

func TestLineMapping_PureInsertion(t *testing.T) {
	text1 := "line 1\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 2, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 3, actual.NewLineCount, "NewLineCount")
	assert.NotNil(t, actual.LineMapping, "LineMapping")

	assert.Equal(t, 1, actual.LineMapping.NewToOld[0], "new line 1 maps to old line 1")
	assert.Equal(t, -1, actual.LineMapping.NewToOld[1], "new line 2 (inserted) maps to -1")
	assert.Equal(t, 2, actual.LineMapping.NewToOld[2], "new line 3 maps to old line 2")

	change, exists := actual.Changes[2]
	assert.True(t, exists, "change at line 2 exists")
	assert.Equal(t, ChangeAddition, change.Type, "change type")
	assert.Equal(t, 2, change.NewLineNum, "NewLineNum")
}

func TestLineMapping_PureDeletion(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 3, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 2, actual.NewLineCount, "NewLineCount")

	change, exists := actual.Changes[2]
	assert.True(t, exists, "change at line 2 (deletion) exists")
	assert.Equal(t, ChangeDeletion, change.Type, "change type")
	assert.Equal(t, 2, change.OldLineNum, "OldLineNum")
	assert.Equal(t, -1, actual.LineMapping.OldToNew[1], "old line 2 (deleted) maps to -1")
}

func TestLineMapping_MultipleInsertions(t *testing.T) {
	text1 := "start\nend"
	text2 := "start\nnew 1\nnew 2\nnew 3\nend"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 2, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 5, actual.NewLineCount, "NewLineCount")

	additionCount := 0
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition {
			additionCount++
			assert.True(t, change.NewLineNum > 0, "positive NewLineNum for insertion")
		}
	}
	assert.Equal(t, 3, additionCount, "addition count")
}

func TestLineMapping_MultipleDeletions(t *testing.T) {
	text1 := "start\ndel 1\ndel 2\ndel 3\nend"
	text2 := "start\nend"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 5, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 2, actual.NewLineCount, "NewLineCount")

	deletionCount := 0
	for _, change := range actual.Changes {
		if change.Type == ChangeDeletion {
			deletionCount++
			assert.True(t, change.OldLineNum > 0, "positive OldLineNum for deletion")
		}
	}
	assert.Equal(t, 3, deletionCount, "deletion count")
}

func TestLineMapping_MixedInsertionDeletion(t *testing.T) {
	text1 := "line 1\nold line 2\nline 3"
	text2 := "line 1\nnew line 2a\nnew line 2b\nline 3"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 3, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 4, actual.NewLineCount, "NewLineCount")
	assert.True(t, len(actual.Changes) > 0, "changes detected")
}

func TestLineMapping_NetLineIncrease(t *testing.T) {
	text1 := `func hello() {
}`
	text2 := `func hello() {
    fmt.Println("Hello")
    fmt.Println("World")
}`

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 2, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 4, actual.NewLineCount, "NewLineCount")
	assert.Equal(t, 1, actual.LineMapping.NewToOld[0], "new line 1 maps to old line 1")
}

func TestLineMapping_NetLineDecrease(t *testing.T) {
	text1 := `func hello() {
    fmt.Println("Hello")
    fmt.Println("World")
    fmt.Println("!")
}`
	text2 := `func hello() {
    fmt.Println("Hello World!")
}`

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 5, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 3, actual.NewLineCount, "NewLineCount")
	assert.True(t, len(actual.Changes) > 0, "changes detected")
}

func TestLineChangeCoordinates_Modification(t *testing.T) {
	text1 := "Hello world"
	text2 := "Hello there"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[1]
	assert.True(t, exists, "change at line 1 exists")
	assert.Equal(t, 1, change.OldLineNum, "OldLineNum")
	assert.Equal(t, 1, change.NewLineNum, "NewLineNum")
}

func TestLineChangeCoordinates_Addition(t *testing.T) {
	text1 := "line 1\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[2]
	assert.True(t, exists, "change at line 2 exists")
	assert.Equal(t, ChangeAddition, change.Type, "change type")
	assert.Equal(t, 2, change.NewLineNum, "NewLineNum")
}

func TestLineChangeCoordinates_Deletion(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 3"

	actual := ComputeDiff(text1, text2)

	change, exists := actual.Changes[2]
	assert.True(t, exists, "change at line 2 exists")
	assert.Equal(t, ChangeDeletion, change.Type, "change type")
	assert.Equal(t, 2, change.OldLineNum, "OldLineNum")
}

func TestDeletionAtLine1(t *testing.T) {
	text1 := "first line\nsecond line\nthird line"
	text2 := "second line\nthird line"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 3, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 2, actual.NewLineCount, "NewLineCount")

	change, exists := actual.Changes[1]
	assert.True(t, exists, "deletion at line 1 exists")
	assert.Equal(t, ChangeDeletion, change.Type, "change type")
	assert.Equal(t, 1, change.OldLineNum, "OldLineNum")
	assert.True(t, change.NewLineNum <= 0, "no valid new line anchor for first line deletion")

	assert.Equal(t, -1, actual.LineMapping.OldToNew[0], "old line 1 deleted")
	assert.Equal(t, 1, actual.LineMapping.OldToNew[1], "old line 2 maps to new line 1")
}

func TestMultipleConsecutiveInsertionsThenDeletions(t *testing.T) {
	text1 := "line A\nline B\nline C\nline D\nline E"
	text2 := "line A\nnew 1\nnew 2\nline C\nline E"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 5, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 5, actual.NewLineCount, "NewLineCount")
	assert.True(t, len(actual.Changes) > 0, "changes detected")

	assert.NotNil(t, actual.LineMapping, "LineMapping exists")
	assert.Equal(t, 5, len(actual.LineMapping.NewToOld), "NewToOld length")
	assert.Equal(t, 5, len(actual.LineMapping.OldToNew), "OldToNew length")
}

func TestInsertionAtLine1(t *testing.T) {
	text1 := "existing line"
	text2 := "new first line\nexisting line"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 1, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 2, actual.NewLineCount, "NewLineCount")

	assert.Equal(t, -1, actual.LineMapping.NewToOld[0], "new line 1 is insertion")
	assert.Equal(t, 1, actual.LineMapping.NewToOld[1], "new line 2 maps to old line 1")
}

func TestLargeLineCountDrift(t *testing.T) {
	text1 := "line 1\nline 2"
	text2 := "line 1\nnew a\nnew b\nnew c\nnew d\nnew e\nline 2"

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 2, actual.OldLineCount, "OldLineCount")
	assert.Equal(t, 7, actual.NewLineCount, "NewLineCount")

	insertionCount := 0
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition {
			insertionCount++
			assert.True(t, change.NewLineNum > 0, "insertion has valid NewLineNum")
		}
	}
	assert.Equal(t, 5, insertionCount, "5 insertions detected")

	assert.Equal(t, 1, actual.LineMapping.NewToOld[0], "new line 1 maps to old line 1")
	assert.Equal(t, 2, actual.LineMapping.NewToOld[6], "new line 7 maps to old line 2")
}

func TestEmptyLineAddition(t *testing.T) {
	// Reproduces bug: empty line additions were being dropped
	// because addChange incorrectly rejected them when oldContent == newContent == ""
	text1 := "import numpy as np"
	text2 := "import numpy as np\n\ndef test_numpy():\n    print('test')"

	actual := ComputeDiff(text1, text2)

	// Line 1 is unchanged (import numpy as np)
	// Line 2 is empty line addition
	// Lines 3-4 are function additions
	// Total: 3 additions
	assert.Equal(t, 3, len(actual.Changes), "should have 3 additions (empty line + 2 function lines)")

	// Verify empty line is included
	hasEmptyLineAddition := false
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition && change.Content == "" {
			hasEmptyLineAddition = true
			break
		}
	}
	assert.True(t, hasEmptyLineAddition, "empty line addition should be included")
}

func TestEmptyLineAdditionMiddle(t *testing.T) {
	// Empty line added in the middle of content
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 2\n\nline 3"

	actual := ComputeDiff(text1, text2)

	// Should detect the empty line addition
	hasEmptyLineAddition := false
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition && change.Content == "" {
			hasEmptyLineAddition = true
			break
		}
	}
	assert.True(t, hasEmptyLineAddition, "empty line addition in middle should be detected")
}

func TestMultipleEmptyLineAdditions(t *testing.T) {
	// Multiple empty lines added
	text1 := "start\nend"
	text2 := "start\n\n\n\nend"

	actual := ComputeDiff(text1, text2)

	// Count empty line additions
	emptyLineCount := 0
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition && change.Content == "" {
			emptyLineCount++
		}
	}
	assert.Equal(t, 3, emptyLineCount, "should detect 3 empty line additions")
}

func TestTrailingEmptyLinePreserved(t *testing.T) {
	// Reproduces bug: trailing empty line in original was being lost
	// Buffer has: ["import numpy as np", ""] (2 lines, line 2 is empty)
	// Completion: ["import numpy as np", "", "def test():", "    pass"] (4 lines)
	// Expected: lines 1-2 are EQUAL, lines 3-4 are additions

	// Use JoinLines to create proper text representation
	text1 := JoinLines([]string{"import numpy as np", ""}) // 2 lines
	text2 := JoinLines([]string{"import numpy as np", "", "def test():", "    pass"})

	actual := ComputeDiff(text1, text2)

	// Debug: print what we got
	t.Logf("OldLineCount: %d, NewLineCount: %d", actual.OldLineCount, actual.NewLineCount)
	t.Logf("Changes count: %d", len(actual.Changes))
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: Type=%v, Content=%q, NewLineNum=%d", lineNum, change.Type, change.Content, change.NewLineNum)
	}

	// Should only have 2 additions (def test and pass), NOT 3 (with spurious empty line)
	assert.Equal(t, 2, len(actual.Changes), "should only have 2 additions (trailing empty line preserved)")

	// Verify no empty line addition (the empty line already exists in original)
	for _, change := range actual.Changes {
		if change.Type == ChangeAddition && change.Content == "" {
			t.Errorf("should not have empty line addition - the empty line already exists in original")
		}
	}
}

func TestTrailingEmptyLineInBothTexts(t *testing.T) {
	// Both texts end with empty line - should be detected as equal
	text1 := JoinLines([]string{"line1", ""}) // 2 lines
	text2 := JoinLines([]string{"line1", ""}) // Same

	actual := ComputeDiff(text1, text2)

	assert.Equal(t, 0, len(actual.Changes), "identical texts should have no changes")
}

func TestJoinLinesSplitLinesRoundTrip(t *testing.T) {
	// Test round-trip consistency
	cases := [][]string{
		{"a"},
		{"a", "b"},
		{"a", ""},        // trailing empty line
		{"a", "", "b"},   // empty line in middle
		{"", "a"},        // empty line at start
		{"a", "b", "c"},
	}

	for _, lines := range cases {
		text := JoinLines(lines)
		result := splitLines(text)
		if len(lines) != len(result) {
			t.Errorf("round-trip failed for %v: got %v", lines, result)
			continue
		}
		for i := range lines {
			if lines[i] != result[i] {
				t.Errorf("round-trip element mismatch at %d for %v: got %v", i, lines, result)
			}
		}
	}
}

func TestPureAdditionsAtEndOfFile(t *testing.T) {
	// Reproduces bug: empty lines between additions were being lost
	// Buffer has: ["import numpy as np", ""] (2 lines)
	// Completion: ["import numpy as np", "", "def f1():", "    pass", "", "def f2():", "    pass"]
	// Expected: lines 1-2 EQUAL, lines 3-7 ADDITIONS (5 additions, including empty line at 5)

	oldLines := []string{"import numpy as np", ""}
	newLines := []string{"import numpy as np", "", "def f1():", "    pass", "", "def f2():", "    pass"}

	text1 := JoinLines(oldLines)
	text2 := JoinLines(newLines)

	actual := ComputeDiff(text1, text2)

	t.Logf("OldLineCount: %d, NewLineCount: %d", actual.OldLineCount, actual.NewLineCount)
	t.Logf("Changes count: %d", len(actual.Changes))
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: Type=%v, Content=%q", lineNum, change.Type, change.Content)
	}

	// Should have 5 additions (lines 3-7)
	assert.Equal(t, 5, len(actual.Changes), "should have 5 additions")

	// Line 5 should be an empty line addition
	line5Change, exists := actual.Changes[5]
	assert.True(t, exists, "should have change at line 5")
	if exists {
		assert.Equal(t, ChangeAddition, line5Change.Type, "line 5 should be addition")
		assert.Equal(t, "", line5Change.Content, "line 5 should be empty string")
	}
}
