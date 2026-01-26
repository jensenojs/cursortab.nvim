package text

import (
	"cursortab/assert"
	"fmt"
	"sort"
	"testing"
)

func TestClusterDistanceFromCursor(t *testing.T) {
	// Cluster with relative coordinates 1-6, baseLineOffset=10 means buffer lines 10-15
	cluster := &ChangeCluster{
		StartLine: 1,
		EndLine:   6,
		Changes: map[int]LineChange{
			1: {Type: ChangeModification, OldLineNum: 1, NewLineNum: 1},
			6: {Type: ChangeModification, OldLineNum: 6, NewLineNum: 6},
		},
	}
	baseLineOffset := 10
	diff := &DiffResult{
		Changes:      cluster.Changes,
		OldLineCount: 6,
		NewLineCount: 6,
	}

	tests := []struct {
		cursorRow int // buffer coordinates
		expected  int
	}{
		{5, 5},  // cursor before cluster (buffer line 5, cluster starts at buffer 10)
		{10, 0}, // cursor at start (buffer line 10)
		{12, 0}, // cursor inside (buffer line 12)
		{15, 0}, // cursor at end (buffer line 15)
		{20, 5}, // cursor after cluster (buffer line 20, cluster ends at buffer 15)
	}

	for _, tt := range tests {
		result := clusterDistanceFromCursor(cluster, tt.cursorRow, baseLineOffset, diff)
		assert.Equal(t, tt.expected, result, fmt.Sprintf("distance for cursor at %d", tt.cursorRow))
	}
}

func TestClusterDistanceFromCursor_NoOffset(t *testing.T) {
	// When baseLineOffset=1, cluster coordinates match buffer coordinates
	cluster := &ChangeCluster{
		StartLine: 10,
		EndLine:   15,
		Changes: map[int]LineChange{
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10},
			15: {Type: ChangeModification, OldLineNum: 15, NewLineNum: 15},
		},
	}
	baseLineOffset := 1
	diff := &DiffResult{
		Changes:      cluster.Changes,
		OldLineCount: 15,
		NewLineCount: 15,
	}

	tests := []struct {
		cursorRow int
		expected  int
	}{
		{5, 5},  // cursor before cluster
		{10, 0}, // cursor at start
		{12, 0}, // cursor inside
		{15, 0}, // cursor at end
		{20, 5}, // cursor after cluster
	}

	for _, tt := range tests {
		result := clusterDistanceFromCursor(cluster, tt.cursorRow, baseLineOffset, diff)
		assert.Equal(t, tt.expected, result, fmt.Sprintf("distance for cursor at %d", tt.cursorRow))
	}
}

func TestJoinLines(t *testing.T) {
	lines := []string{"line1", "line2", "line3"}
	result := JoinLines(lines)
	// JoinLines adds \n after each line for diffmatchpatch compatibility
	expected := "line1\nline2\nline3\n"

	assert.Equal(t, expected, result, "JoinLines result")
}

func TestCreateStages_PureAdditionsPreservesEmptyLines(t *testing.T) {
	// Reproduces bug: empty lines between additions were being lost in staging
	oldLines := []string{"import numpy as np", ""}
	newLines := []string{"import numpy as np", "", "def f1():", "    pass", "", "def f2():", "    pass"}

	text1 := JoinLines(oldLines)
	text2 := JoinLines(newLines)
	diff := ComputeDiff(text1, text2)

	t.Logf("Diff changes: %d", len(diff.Changes))
	for lineNum, change := range diff.Changes {
		t.Logf("  Line %d: Type=%v, Content=%q", lineNum, change.Type, change.Content)
	}

	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.py", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.True(t, len(result.Stages) >= 1, "should have at least 1 stage")

	stage := result.Stages[0]
	t.Logf("Stage: BufferStart=%d, BufferEnd=%d, Lines=%d", stage.BufferStart, stage.BufferEnd, len(stage.Lines))
	t.Logf("Stage Changes: %d", len(stage.Changes))
	for lineNum, change := range stage.Changes {
		t.Logf("  Stage Line %d: Type=%v, Content=%q", lineNum, change.Type, change.Content)
	}
	t.Logf("Stage Groups: %d", len(stage.Groups))
	for i, g := range stage.Groups {
		t.Logf("  Group %d: Type=%s, StartLine=%d, EndLine=%d, Lines=%v", i, g.Type, g.StartLine, g.EndLine, g.Lines)
	}

	// Stage should have 5 changes (lines 3-7 of new text = stage lines 1-5)
	assert.Equal(t, 5, len(stage.Changes), "stage should have 5 changes")

	// Stage line 3 should be the empty line (new line 5)
	line3Change, exists := stage.Changes[3]
	assert.True(t, exists, "should have change at stage line 3")
	if exists {
		assert.Equal(t, ChangeAddition, line3Change.Type, "stage line 3 should be addition")
		assert.Equal(t, "", line3Change.Content, "stage line 3 should be empty string")
	}

	// Groups should cover all 5 stage lines with no gaps
	totalLinesInGroups := 0
	for _, g := range stage.Groups {
		totalLinesInGroups += g.EndLine - g.StartLine + 1
	}
	assert.Equal(t, 5, totalLinesInGroups, "groups should cover all 5 lines")
}

func TestCreateStages_ExactProductionScenario(t *testing.T) {
	// Exact reproduction of the bug from production log
	// Buffer: ["import numpy as np", ""] (2 lines)
	// Completion: 10 lines with 3 functions separated by empty lines
	oldLines := []string{"import numpy as np", ""}
	newLines := []string{
		"import numpy as np",
		"",
		"def calculate_distance(x1, y1, x2, y2):",
		"    return np.sqrt((x2 - x1) ** 2 + (y2 - y1) ** 2)",
		"",
		"def calculate_angle(x1, y1, x2, y2):",
		"    return np.arctan2(y2 - y1, x2 - x1)",
		"",
		"def calculate_distance_and_angle(x1, y1, x2, y2):",
		"    distance = np.sqrt((x2 - x1) ** 2 + (y2 - y1) ** 2)",
	}

	text1 := JoinLines(oldLines)
	text2 := JoinLines(newLines)
	t.Logf("text1 (len=%d): %q", len(text1), text1)
	t.Logf("text2 (len=%d): %q", len(text2), text2)

	diff := ComputeDiff(text1, text2)

	t.Logf("Diff: OldLineCount=%d, NewLineCount=%d, Changes=%d", diff.OldLineCount, diff.NewLineCount, len(diff.Changes))

	// Print changes sorted by key
	keys := make([]int, 0, len(diff.Changes))
	for k := range diff.Changes {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, lineNum := range keys {
		change := diff.Changes[lineNum]
		t.Logf("  Change[%d]: Type=%v, NewLineNum=%d, OldLineNum=%d, Content=%q",
			lineNum, change.Type, change.NewLineNum, change.OldLineNum, change.Content)
	}

	// Should have 8 additions (lines 3-10), lines 1-2 are equal
	assert.Equal(t, 8, len(diff.Changes), "should have 8 additions")

	// Create stages as production does
	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.py", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.True(t, len(result.Stages) >= 1, "should have at least 1 stage")

	stage := result.Stages[0]
	t.Logf("Stage: BufferStart=%d, BufferEnd=%d, Lines=%d, Changes=%d, Groups=%d",
		stage.BufferStart, stage.BufferEnd, len(stage.Lines), len(stage.Changes), len(stage.Groups))
	for lineNum, change := range stage.Changes {
		t.Logf("  Stage Line %d: Type=%v, Content=%q", lineNum, change.Type, change.Content)
	}
	for i, g := range stage.Groups {
		t.Logf("  Group %d: Type=%s, StartLine=%d, EndLine=%d, LinesCount=%d", i, g.Type, g.StartLine, g.EndLine, len(g.Lines))
	}

	// Stage should have 8 changes (additions at lines 3-10)
	assert.Equal(t, 8, len(stage.Changes), "stage should have 8 changes")

	// Stage should have 8 lines of content
	assert.Equal(t, 8, len(stage.Lines), "stage should have 8 lines")

	// Empty lines at positions 3 and 6 of stage content (new lines 5 and 8)
	line3Change, exists := stage.Changes[3]
	assert.True(t, exists, "should have change at stage line 3")
	if exists {
		assert.Equal(t, "", line3Change.Content, "stage line 3 should be empty")
	}

	line6Change, exists := stage.Changes[6]
	assert.True(t, exists, "should have change at stage line 6")
	if exists {
		assert.Equal(t, "", line6Change.Content, "stage line 6 should be empty")
	}

	// Groups should cover all 8 lines with no gaps
	totalLinesInGroups := 0
	for _, g := range stage.Groups {
		totalLinesInGroups += g.EndLine - g.StartLine + 1
	}
	assert.Equal(t, 8, totalLinesInGroups, "groups should cover all 8 lines")
}

func TestCreateStages_EmptyDiff(t *testing.T) {
	diff := &DiffResult{Changes: map[int]LineChange{}}
	stages := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", []string{}, []string{})

	assert.Nil(t, stages, "stages for empty diff")
}

func TestCreateStages_SingleCluster(t *testing.T) {
	// All changes within proximity threshold - should still return stages (always return stages now)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
			11: {Type: ChangeModification, OldLineNum: 11, NewLineNum: 11, Content: "new11", OldContent: "old11"},
			12: {Type: ChangeModification, OldLineNum: 12, NewLineNum: 12, Content: "new12", OldContent: "old12"},
		},
	}
	newLines := make([]string, 20)
	oldLines := make([]string, 20)
	for i := range newLines {
		newLines[i] = "line"
		oldLines[i] = "line"
	}

	result := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", newLines, oldLines)

	// Single cluster still returns a result with 1 stage
	assert.NotNil(t, result, "result for single cluster")
	assert.Len(t, 1, result.Stages, "stages")
}

func TestCreateStages_TwoClusters(t *testing.T) {
	// Changes at lines 10-11 and 25-26 (gap > threshold of 3)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
			11: {Type: ChangeModification, OldLineNum: 11, NewLineNum: 11, Content: "new11", OldContent: "old11"},
			25: {Type: ChangeModification, OldLineNum: 25, NewLineNum: 25, Content: "new25", OldContent: "old25"},
			26: {Type: ChangeModification, OldLineNum: 26, NewLineNum: 26, Content: "new26", OldContent: "old26"},
		},
	}
	newLines := make([]string, 30)
	oldLines := make([]string, 30)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	// Cursor at line 15, baseLineOffset=1, so cluster 10-11 is closer
	result := CreateStages(diff, 15, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages")

	// First stage should be the closer cluster (10-11)
	assert.Equal(t, 10, result.Stages[0].BufferStart, "first stage buffer start")
	assert.Equal(t, 11, result.Stages[0].BufferEnd, "first stage buffer end")

	// Second stage should be 25-26
	assert.Equal(t, 25, result.Stages[1].BufferStart, "second stage buffer start")

	// First stage cursor target should point to next stage
	assert.Equal(t, int32(25), result.Stages[0].CursorTarget.LineNumber, "first stage cursor target line")
	assert.False(t, result.Stages[0].CursorTarget.ShouldRetrigger, "first stage ShouldRetrigger")

	// Last stage should have ShouldRetrigger=true
	assert.True(t, result.Stages[1].CursorTarget.ShouldRetrigger, "last stage ShouldRetrigger")
	assert.True(t, result.Stages[1].IsLastStage, "second stage IsLastStage")
}

func TestCreateStages_CursorDistanceSorting(t *testing.T) {
	// Three clusters: 5-6, 20-21, 35-36
	// Cursor at 22 - closest to cluster 20-21
	diff := &DiffResult{
		Changes: map[int]LineChange{
			5:  {Type: ChangeModification, OldLineNum: 5, NewLineNum: 5, Content: "new5", OldContent: "old5"},
			6:  {Type: ChangeModification, OldLineNum: 6, NewLineNum: 6, Content: "new6", OldContent: "old6"},
			20: {Type: ChangeModification, OldLineNum: 20, NewLineNum: 20, Content: "new20", OldContent: "old20"},
			21: {Type: ChangeModification, OldLineNum: 21, NewLineNum: 21, Content: "new21", OldContent: "old21"},
			35: {Type: ChangeModification, OldLineNum: 35, NewLineNum: 35, Content: "new35", OldContent: "old35"},
			36: {Type: ChangeModification, OldLineNum: 36, NewLineNum: 36, Content: "new36", OldContent: "old36"},
		},
	}
	newLines := make([]string, 40)
	oldLines := make([]string, 40)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	result := CreateStages(diff, 22, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 3, result.Stages, "stages")

	// First stage should be closest to cursor (20-21)
	assert.Equal(t, 20, result.Stages[0].BufferStart, "first stage should be closest cluster (20-21)")
}

func TestCreateStages_ViewportPartitioning(t *testing.T) {
	// Changes at lines 10 (in viewport) and 100 (out of viewport)
	// Viewport is 1-50
	diff := &DiffResult{
		Changes: map[int]LineChange{
			10:  {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
			100: {Type: ChangeModification, OldLineNum: 100, NewLineNum: 100, Content: "new100", OldContent: "old100"},
		},
	}
	newLines := make([]string, 110)
	oldLines := make([]string, 110)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	// Cursor at 10, viewport 1-50
	result := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages")

	// In-view changes should come first (line 10)
	assert.Equal(t, 10, result.Stages[0].BufferStart, "first stage should be in-viewport change")

	// Out-of-view change should be second (line 100)
	assert.Equal(t, 100, result.Stages[1].BufferStart, "second stage should be out-of-viewport change")
}

func TestCreateStages_ProximityGrouping(t *testing.T) {
	// Changes at lines 10, 12, 14 (all within threshold of 3)
	// Should form single cluster
	diff := &DiffResult{
		Changes: map[int]LineChange{
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
			12: {Type: ChangeModification, OldLineNum: 12, NewLineNum: 12, Content: "new12", OldContent: "old12"},
			14: {Type: ChangeModification, OldLineNum: 14, NewLineNum: 14, Content: "new14", OldContent: "old14"},
		},
	}
	newLines := make([]string, 20)
	oldLines := make([]string, 20)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	result := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", newLines, oldLines)

	// Gap between 10->12 is 2, 12->14 is 2 - all within threshold
	// Now returns 1 stage (always returns stages)
	assert.NotNil(t, result, "result for single cluster")
	assert.Len(t, 1, result.Stages, "stages for single cluster (all within threshold)")
}

func TestCreateStages_ProximityGrouping_SplitByGap(t *testing.T) {
	// Changes at lines 10, 12 (gap=2) and 20 (gap=8 from 12)
	// With threshold=3, should split into two clusters
	diff := &DiffResult{
		Changes: map[int]LineChange{
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
			12: {Type: ChangeModification, OldLineNum: 12, NewLineNum: 12, Content: "new12", OldContent: "old12"},
			20: {Type: ChangeModification, OldLineNum: 20, NewLineNum: 20, Content: "new20", OldContent: "old20"},
		},
	}
	newLines := make([]string, 25)
	oldLines := make([]string, 25)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	result := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages (gap > threshold)")

	// First cluster should be 10-12
	assert.Equal(t, 10, result.Stages[0].BufferStart, "first stage start line")
	assert.Equal(t, 12, result.Stages[0].BufferEnd, "first stage end line")

	// Second cluster should be 20
	assert.Equal(t, 20, result.Stages[1].BufferStart, "second stage start line")
}

func TestCreateStages_WithBaseLineOffset(t *testing.T) {
	// Diff coordinates are relative (1-indexed from start of extraction)
	// baseLineOffset=50 means diff line 1 = buffer line 50
	diff := &DiffResult{
		Changes: map[int]LineChange{
			1:  {Type: ChangeModification, OldLineNum: 1, NewLineNum: 1, Content: "new1", OldContent: "old1"},
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
		},
	}
	newLines := make([]string, 15)
	oldLines := make([]string, 15)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	// baseLineOffset=50, so diff line 1 = buffer line 50, diff line 10 = buffer line 59
	result := CreateStages(diff, 55, 1, 100, 50, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages")

	// Verify buffer coordinates in stage
	assert.True(t, result.Stages[0].BufferStart == 50 || result.Stages[0].BufferStart == 59,
		fmt.Sprintf("stage should have buffer coordinates (50 or 59), got %d", result.Stages[0].BufferStart))
}

func TestCreateStages_GroupsComputed(t *testing.T) {
	// Verify that groups are computed for stages
	diff := &DiffResult{
		Changes: map[int]LineChange{
			1: {Type: ChangeModification, OldLineNum: 1, NewLineNum: 1, Content: "new1", OldContent: "old1"},
			2: {Type: ChangeModification, OldLineNum: 2, NewLineNum: 2, Content: "new2", OldContent: "old2"},
			// Gap
			10: {Type: ChangeModification, OldLineNum: 10, NewLineNum: 10, Content: "new10", OldContent: "old10"},
		},
	}
	newLines := []string{"new1", "new2", "", "", "", "", "", "", "", "new10"}
	oldLines := []string{"old1", "old2", "", "", "", "", "", "", "", "old10"}

	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages")

	// First stage (lines 1-2) should have groups
	assert.NotNil(t, result.Stages[0].Groups, "first stage groups")
}

func TestGroupChangesByProximity(t *testing.T) {
	diff := &DiffResult{
		Changes: map[int]LineChange{
			5:  {Type: ChangeModification, OldLineNum: 5, NewLineNum: 5},
			6:  {Type: ChangeModification, OldLineNum: 6, NewLineNum: 6},
			7:  {Type: ChangeModification, OldLineNum: 7, NewLineNum: 7},
			20: {Type: ChangeModification, OldLineNum: 20, NewLineNum: 20},
			21: {Type: ChangeModification, OldLineNum: 21, NewLineNum: 21},
		},
	}

	lineNumbers := []int{5, 6, 7, 20, 21}
	clusters := groupChangesByProximity(diff, lineNumbers, 3)

	assert.Len(t, 2, clusters, "clusters")

	// First cluster: 5-7
	assert.Equal(t, 5, clusters[0].StartLine, "first cluster start line")
	assert.Equal(t, 7, clusters[0].EndLine, "first cluster end line")
	assert.Equal(t, 3, len(clusters[0].Changes), "first cluster change count")

	// Second cluster: 20-21
	assert.Equal(t, 20, clusters[1].StartLine, "second cluster start line")
	assert.Equal(t, 21, clusters[1].EndLine, "second cluster end line")
}

func TestGroupChangesByProximity_EmptyInput(t *testing.T) {
	diff := &DiffResult{Changes: map[int]LineChange{}}
	clusters := groupChangesByProximity(diff, []int{}, 3)

	assert.Nil(t, clusters, "clusters for empty input")
}

// =============================================================================
// Tests for staging with unequal line counts
// =============================================================================

func TestCreateStages_WithInsertions(t *testing.T) {
	// Test staging when completion adds lines (net line increase)
	// Old: 3 lines, New: 5 lines (2 insertions at different locations)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2: {Type: ChangeAddition, NewLineNum: 2, OldLineNum: -1, Content: "inserted1"},
			5: {Type: ChangeAddition, NewLineNum: 5, OldLineNum: -1, Content: "inserted2"},
		},
		OldLineCount: 3,
		NewLineCount: 5,
		LineMapping: &LineMapping{
			NewToOld: []int{1, -1, 2, 3, -1}, // line 2 and 5 are insertions
			OldToNew: []int{1, 3, 4},         // old lines map to new positions
		},
	}
	newLines := []string{"line1", "inserted1", "line2", "line3", "inserted2"}
	oldLines := []string{"line1", "line2", "line3"}

	// Gap between line 2 and line 5 is 3, with threshold 2 they should be separate
	result := CreateStages(diff, 1, 1, 50, 1, 2, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages for separated insertions")

	// Verify stages have valid content
	for i, stage := range result.Stages {
		assert.True(t, len(stage.Lines) > 0, fmt.Sprintf("stage %d has lines", i))
	}
}

func TestCreateStages_WithDeletions(t *testing.T) {
	// Test staging when completion removes lines (net line decrease)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2:  {Type: ChangeDeletion, OldLineNum: 2, NewLineNum: -1, Content: "deleted1"},
			10: {Type: ChangeDeletion, OldLineNum: 10, NewLineNum: -1, Content: "deleted2"},
		},
		OldLineCount: 12,
		NewLineCount: 10,
		LineMapping: &LineMapping{
			NewToOld: []int{1, 3, 4, 5, 6, 7, 8, 9, 11, 12},
			OldToNew: []int{1, -1, 2, 3, 4, 5, 6, 7, 8, -1, 9, 10},
		},
	}
	newLines := make([]string, 10)
	oldLines := make([]string, 12)
	for i := range newLines {
		newLines[i] = "content"
	}
	for i := range oldLines {
		oldLines[i] = "content"
	}

	// Gap is large (10-2=8), should create 2 stages
	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages for separated deletions")
}

func TestCreateStages_MixedInsertionDeletion(t *testing.T) {
	// Test with both insertions and deletions in different regions
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2:  {Type: ChangeAddition, NewLineNum: 2, OldLineNum: 1, Content: "inserted"},
			15: {Type: ChangeDeletion, OldLineNum: 15, NewLineNum: 14, Content: "deleted"},
		},
		OldLineCount: 20,
		NewLineCount: 20, // net zero change
		LineMapping: &LineMapping{
			NewToOld: make([]int, 20),
			OldToNew: make([]int, 20),
		},
	}
	// Initialize mappings
	for i := range diff.LineMapping.NewToOld {
		diff.LineMapping.NewToOld[i] = i + 1
	}
	for i := range diff.LineMapping.OldToNew {
		diff.LineMapping.OldToNew[i] = i + 1
	}
	diff.LineMapping.NewToOld[1] = -1 // line 2 is insertion

	newLines := make([]string, 20)
	oldLines := make([]string, 20)
	for i := range newLines {
		newLines[i] = "content"
		oldLines[i] = "content"
	}

	// Large gap (15-2=13), should create 2 stages
	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages")
}

func TestGetBufferLineForChange_Insertion(t *testing.T) {
	// Test that insertions correctly calculate buffer position
	mapping := &LineMapping{
		NewToOld: []int{1, -1, -1, 2}, // lines 2,3 are insertions after line 1
		OldToNew: []int{1, 4},
	}

	// Pure insertion at new line 2, should anchor to old line 1
	change := LineChange{
		Type:       ChangeAddition,
		NewLineNum: 2,
		OldLineNum: -1, // pure insertion
	}

	bufferLine := getBufferLineForChange(change, 2, 1, mapping)

	// Should find old line 1 as anchor (the mapped line before insertion)
	assert.Equal(t, 1, bufferLine, "buffer line for insertion (anchor)")
}

func TestGetBufferLineForChange_Modification(t *testing.T) {
	// Test that modifications use OldLineNum directly
	mapping := &LineMapping{
		NewToOld: []int{1, 2, 3},
		OldToNew: []int{1, 2, 3},
	}

	change := LineChange{
		Type:       ChangeModification,
		NewLineNum: 2,
		OldLineNum: 2,
	}

	bufferLine := getBufferLineForChange(change, 2, 10, mapping)

	// baseLineOffset=10, oldLineNum=2: 2 + 10 - 1 = 11
	assert.Equal(t, 11, bufferLine, "buffer line for modification")
}

func TestGetClusterBufferRange_WithInsertions(t *testing.T) {
	// Cluster containing insertions should still compute valid buffer range
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2: {Type: ChangeAddition, NewLineNum: 2, OldLineNum: -1},
			3: {Type: ChangeAddition, NewLineNum: 3, OldLineNum: -1},
		},
		LineMapping: &LineMapping{
			NewToOld: []int{1, -1, -1, 2}, // insertions at lines 2,3
			OldToNew: []int{1, 4},
		},
	}

	cluster := &ChangeCluster{
		StartLine: 2,
		EndLine:   3,
		Changes:   diff.Changes,
	}

	start, end := getClusterBufferRange(cluster, 1, diff)

	// Both insertions anchor to line 1, so range should be 1-1
	assert.True(t, start <= end, fmt.Sprintf("invalid range: start=%d > end=%d", start, end))
}

// =============================================================================
// Edge case tests for staging with extreme coordinate scenarios
// =============================================================================

func TestGetBufferLineForChange_DeletionAtLine1(t *testing.T) {
	// Edge case: deletion at line 1 with no preceding anchor
	mapping := &LineMapping{
		NewToOld: []int{2, 3}, // old line 1 deleted
		OldToNew: []int{-1, 1, 2},
	}

	change := LineChange{
		Type:       ChangeDeletion,
		OldLineNum: 1,
		NewLineNum: -1, // no new line for deletions
	}

	bufferLine := getBufferLineForChange(change, 1, 1, mapping)

	// Should use OldLineNum directly: 1 + 1 - 1 = 1
	assert.Equal(t, 1, bufferLine, "buffer line for deletion at line 1")
}

func TestGetBufferLineForChange_InsertionWithNoAnchor(t *testing.T) {
	// Edge case: insertion at line 1 with no preceding mapped line
	mapping := &LineMapping{
		NewToOld: []int{-1, 1}, // new line 1 is insertion, new line 2 maps to old line 1
		OldToNew: []int{2},
	}

	change := LineChange{
		Type:       ChangeAddition,
		NewLineNum: 1,
		OldLineNum: -1, // pure insertion
	}

	bufferLine := getBufferLineForChange(change, 1, 1, mapping)

	// No anchor found, should fallback to mapKey: 1 + 1 - 1 = 1
	assert.Equal(t, 1, bufferLine, "buffer line for insertion at line 1 with no anchor")
}

func TestCreateStages_CumulativeOffsetScenario(t *testing.T) {
	// Scenario: first stage increases line count, affecting second stage coordinates
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2:  {Type: ChangeAddition, NewLineNum: 2, OldLineNum: 1, Content: "insert1"},
			3:  {Type: ChangeAddition, NewLineNum: 3, OldLineNum: 1, Content: "insert2"},
			20: {Type: ChangeModification, NewLineNum: 22, OldLineNum: 20, Content: "modified"},
		},
		OldLineCount: 25,
		NewLineCount: 27, // +2 from insertions
		LineMapping: &LineMapping{
			NewToOld: make([]int, 27),
			OldToNew: make([]int, 25),
		},
	}
	// Initialize mappings
	for i := range diff.LineMapping.OldToNew {
		diff.LineMapping.OldToNew[i] = i + 3 // offset by 2 after line 1
	}
	diff.LineMapping.OldToNew[0] = 1
	for i := range diff.LineMapping.NewToOld {
		switch i {
		case 0:
			diff.LineMapping.NewToOld[i] = 1
		case 1, 2:
			diff.LineMapping.NewToOld[i] = -1 // insertions
		default:
			diff.LineMapping.NewToOld[i] = i - 1
		}
	}

	newLines := make([]string, 27)
	oldLines := make([]string, 25)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
	}
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// Large gap (20-3=17), should create 2 stages
	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.Len(t, 2, result.Stages, "stages for insertions + distant modification")

	// Verify both stages have valid content
	for i, stage := range result.Stages {
		assert.True(t, stage.BufferStart > 0, fmt.Sprintf("stage %d has valid BufferStart", i))
	}
}

func TestCreateStages_AllDeletions(t *testing.T) {
	// Edge case: completion that only contains deletions
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2: {Type: ChangeDeletion, OldLineNum: 2, NewLineNum: -1, Content: "deleted1"},
			3: {Type: ChangeDeletion, OldLineNum: 3, NewLineNum: -1, Content: "deleted2"},
		},
		OldLineCount: 5,
		NewLineCount: 3,
		LineMapping: &LineMapping{
			NewToOld: []int{1, 4, 5},
			OldToNew: []int{1, -1, -1, 2, 3},
		},
	}
	newLines := []string{"line1", "line4", "line5"}
	oldLines := []string{"line1", "deleted1", "deleted2", "line4", "line5"}

	// Deletions are at same location, should form single cluster
	result := CreateStages(diff, 1, 1, 50, 1, 3, "test.go", newLines, oldLines)

	// Single cluster = 1 stage
	assert.NotNil(t, result, "result for single cluster of deletions")
	assert.Len(t, 1, result.Stages, "single cluster of deletions")
}

func TestGetClusterBufferRange_AllInsertions(t *testing.T) {
	// Cluster containing only insertions (all OldLineNum = -1 but have mapping)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			2: {Type: ChangeAddition, NewLineNum: 2, OldLineNum: -1},
			3: {Type: ChangeAddition, NewLineNum: 3, OldLineNum: -1},
			4: {Type: ChangeAddition, NewLineNum: 4, OldLineNum: -1},
		},
		LineMapping: &LineMapping{
			NewToOld: []int{1, -1, -1, -1, 2}, // lines 2,3,4 are insertions
			OldToNew: []int{1, 5},
		},
	}

	cluster := &ChangeCluster{
		StartLine: 2,
		EndLine:   4,
		Changes:   diff.Changes,
	}

	start, end := getClusterBufferRange(cluster, 1, diff)

	// Pure additions with valid anchors (from mapping) use insertion point (anchor + 1).
	// The mapping shows these insertions are anchored to old line 1, so insertion point is 2.
	assert.True(t, start <= end, fmt.Sprintf("valid range: start=%d end=%d", start, end))
	assert.Equal(t, 2, start, "pure additions with mapping anchor: insertion point is anchor + 1")
}

// =============================================================================
// Tests for group bounds validation
// =============================================================================

func TestStageGroups_ShouldNotExceedStageContent(t *testing.T) {
	// Stage's groups should only reference lines within the stage's content.
	changes := make(map[int]LineChange)

	// Add changes at low line numbers (will be in first cluster)
	for i := 1; i <= 17; i++ {
		changes[i] = LineChange{
			Type:       ChangeAddition,
			NewLineNum: i,
			OldLineNum: -1,
			Content:    fmt.Sprintf("line%d", i),
		}
	}

	// Add changes at high line numbers (should be in separate cluster)
	for i := 41; i <= 54; i++ {
		changes[i] = LineChange{
			Type:       ChangeAddition,
			NewLineNum: i,
			OldLineNum: -1,
			Content:    fmt.Sprintf("line%d", i),
		}
	}

	diff := &DiffResult{
		Changes:      changes,
		OldLineCount: 3,
		NewLineCount: 54,
	}

	// Create newLines for full completion
	newLines := make([]string, 54)
	oldLines := make([]string, 3)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("content%d", i+1)
	}
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("old%d", i+1)
	}

	// Create stages with proximity threshold of 3
	// Gap between line 17 and 41 is 24, so they should be in separate clusters
	result := CreateStages(diff, 1, 1, 100, 1, 3, "test.go", newLines, oldLines)

	// Should have at least 2 stages (may have more due to viewport partitioning)
	assert.NotNil(t, result, "result")
	assert.True(t, len(result.Stages) >= 2, fmt.Sprintf("should have at least 2 stages, got %d", len(result.Stages)))

	// CRITICAL: For ALL stages, Groups should NOT reference lines
	// beyond the stage's content line count
	for i, stage := range result.Stages {
		stageLineCount := len(stage.Lines)

		for _, g := range stage.Groups {
			assert.True(t, g.StartLine <= stageLineCount,
				fmt.Sprintf("Stage %d: Group StartLine (%d) exceeds stage content line count (%d)",
					i, g.StartLine, stageLineCount))
			assert.True(t, g.EndLine <= stageLineCount,
				fmt.Sprintf("Stage %d: Group EndLine (%d) exceeds stage content line count (%d)",
					i, g.EndLine, stageLineCount))
		}
	}
}

// =============================================================================
// Tests for additions at end of file: buffer range should extend to end of original
// =============================================================================

func TestGetClusterBufferRange_AdditionsAtEndOfFile(t *testing.T) {
	// When a cluster contains additions that extend beyond the original buffer,
	// the buffer range should extend to the end of the original buffer.

	// Scenario: 10-line file, modification at line 8, additions at lines 9-12
	diff := &DiffResult{
		Changes: map[int]LineChange{
			8:  {Type: ChangeModification, NewLineNum: 8, OldLineNum: 8, Content: "modified", OldContent: "original"},
			9:  {Type: ChangeAddition, NewLineNum: 9, OldLineNum: 8, Content: "added1"},
			10: {Type: ChangeAddition, NewLineNum: 10, OldLineNum: 8, Content: "added2"},
			11: {Type: ChangeAddition, NewLineNum: 11, OldLineNum: 8, Content: "added3"},
			12: {Type: ChangeAddition, NewLineNum: 12, OldLineNum: 8, Content: "added4"},
		},
		OldLineCount: 10,
		NewLineCount: 14,
	}

	cluster := &ChangeCluster{
		StartLine: 8,
		EndLine:   12,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	assert.Equal(t, 8, startLine, "buffer start line")
	assert.Equal(t, 10, endLine, "buffer end line should extend to end of original buffer")
}

func TestGetClusterBufferRange_AdditionsWithinBuffer(t *testing.T) {
	// When additions don't extend beyond the original buffer,
	// the buffer range should not be artificially extended.

	// Scenario: 20-line file, additions at lines 5-7 (well within buffer)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			5: {Type: ChangeAddition, NewLineNum: 5, OldLineNum: 4, Content: "added1"},
			6: {Type: ChangeAddition, NewLineNum: 6, OldLineNum: 4, Content: "added2"},
			7: {Type: ChangeAddition, NewLineNum: 7, OldLineNum: 4, Content: "added3"},
		},
		OldLineCount: 20,
		NewLineCount: 23,
		LineMapping: &LineMapping{
			NewToOld: []int{1, 2, 3, 4, -1, -1, -1, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
			OldToNew: []int{1, 2, 3, 4, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23},
		},
	}

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   7,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	// Pure additions with anchor at line 4: insertion point is anchor + 1 = 5
	assert.Equal(t, 5, startLine, "buffer start line is insertion point (anchor + 1)")
	assert.Equal(t, 5, endLine, "buffer end line equals start for pure additions")
}

func TestCreateStages_AdditionsAtEndOfFile(t *testing.T) {
	// End-to-end test: stage should have correct buffer range when
	// additions extend beyond the original buffer.

	// Scenario: 15-line file with changes at lines 12-18 (modification + 5 additions)
	diff := &DiffResult{
		Changes: map[int]LineChange{
			12: {Type: ChangeModification, NewLineNum: 12, OldLineNum: 12, Content: "modified", OldContent: "original"},
			13: {Type: ChangeAddition, NewLineNum: 13, OldLineNum: 12, Content: "added1"},
			14: {Type: ChangeAddition, NewLineNum: 14, OldLineNum: 12, Content: "added2"},
			15: {Type: ChangeAddition, NewLineNum: 15, OldLineNum: 12, Content: "added3"},
			16: {Type: ChangeAddition, NewLineNum: 16, OldLineNum: 12, Content: "added4"},
			17: {Type: ChangeAddition, NewLineNum: 17, OldLineNum: 12, Content: "added5"},
			18: {Type: ChangeAddition, NewLineNum: 18, OldLineNum: 12, Content: "added6"},
		},
		OldLineCount: 15,
		NewLineCount: 21,
	}

	newLines := make([]string, 21)
	oldLines := make([]string, 15)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
	}
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// Cursor at line 1 (far from changes), viewport covers all
	result := CreateStages(diff, 1, 1, 30, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result")
	assert.True(t, len(result.Stages) >= 1, "should have at least 1 stage")

	// Find stage with changes at line 12+
	var stage *struct{ BufferStart, BufferEnd, Lines int }
	for _, s := range result.Stages {
		if s.BufferStart >= 12 {
			stage = &struct{ BufferStart, BufferEnd, Lines int }{
				s.BufferStart, s.BufferEnd, len(s.Lines),
			}
			break
		}
	}

	assert.NotNil(t, stage, "should have stage at line 12+")
	if stage != nil {
		assert.Equal(t, 12, stage.BufferStart, "stage BufferStart")
		assert.Equal(t, 15, stage.BufferEnd, "stage BufferEnd should extend to end of original buffer")
		assert.Equal(t, 7, stage.Lines, "stage should have 7 lines (new lines 12-18)")
	}
}

func TestGetClusterBufferRange_AdditionsAnchoredBeforeModifications(t *testing.T) {
	// When modifications exist at line N and additions are anchored to line N-1,
	// the buffer range should start at the first modification, not the anchor.

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// Modifications at lines 43-44 (have OldLineNum)
			43: {Type: ChangeModification, NewLineNum: 43, OldLineNum: 43, Content: "mod1", OldContent: "old1"},
			44: {Type: ChangeModification, NewLineNum: 44, OldLineNum: 44, Content: "mod2", OldContent: "old2"},
			// Additions at lines 45-50, anchored to line 42 (line before modification region)
			45: {Type: ChangeAddition, NewLineNum: 45, OldLineNum: 42, Content: "added1"},
			46: {Type: ChangeAddition, NewLineNum: 46, OldLineNum: 42, Content: "added2"},
			47: {Type: ChangeAddition, NewLineNum: 47, OldLineNum: 42, Content: "added3"},
			48: {Type: ChangeAddition, NewLineNum: 48, OldLineNum: 42, Content: "added4"},
			49: {Type: ChangeAddition, NewLineNum: 49, OldLineNum: 42, Content: "added5"},
			50: {Type: ChangeAddition, NewLineNum: 50, OldLineNum: 42, Content: "added6"},
		},
		OldLineCount: 44,
		NewLineCount: 50,
	}

	cluster := &ChangeCluster{
		StartLine: 43,
		EndLine:   50,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	// StartLine should be 43 (first modification), NOT 42 (anchor of additions)
	assert.Equal(t, 43, startLine,
		"buffer start should be 43 (first modification), not 42 (addition anchor)")

	// EndLine should be 44 (end of original buffer)
	assert.Equal(t, 44, endLine, "buffer end should be 44")
}

func TestGetClusterBufferRange_OnlyAdditionsWithAnchor(t *testing.T) {
	// When a cluster has ONLY additions (no modifications), the insertion point
	// (anchor + 1) determines the buffer range.

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// Only additions, all anchored to line 10
			11: {Type: ChangeAddition, NewLineNum: 11, OldLineNum: 10, Content: "added1"},
			12: {Type: ChangeAddition, NewLineNum: 12, OldLineNum: 10, Content: "added2"},
			13: {Type: ChangeAddition, NewLineNum: 13, OldLineNum: 10, Content: "added3"},
		},
		OldLineCount: 15,
		NewLineCount: 18,
	}

	cluster := &ChangeCluster{
		StartLine: 11,
		EndLine:   13,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	// Pure additions with anchor at line 10: insertion point is anchor + 1 = 11
	assert.Equal(t, 11, startLine, "buffer start should be insertion point (anchor + 1)")
	assert.Equal(t, 11, endLine, "buffer end equals start for pure additions")
}

func TestGetClusterBufferRange_OnlyAdditionsBeyondBuffer(t *testing.T) {
	// When additions extend beyond the original buffer, the insertion point is still anchor + 1

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// Additions beyond original buffer (OldLineCount=10, additions at new lines 11-15)
			11: {Type: ChangeAddition, NewLineNum: 11, OldLineNum: 10, Content: "added1"},
			12: {Type: ChangeAddition, NewLineNum: 12, OldLineNum: 10, Content: "added2"},
			13: {Type: ChangeAddition, NewLineNum: 13, OldLineNum: 10, Content: "added3"},
			14: {Type: ChangeAddition, NewLineNum: 14, OldLineNum: 10, Content: "added4"},
			15: {Type: ChangeAddition, NewLineNum: 15, OldLineNum: 10, Content: "added5"},
		},
		OldLineCount: 10,
		NewLineCount: 15,
	}

	cluster := &ChangeCluster{
		StartLine: 11,
		EndLine:   15,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	// Pure additions with anchor at line 10: insertion point is anchor + 1 = 11
	assert.Equal(t, 11, startLine, "buffer start should be insertion point (anchor + 1)")
	assert.Equal(t, 11, endLine, "buffer end equals start for pure additions")
}

// =============================================================================
// Edge case tests from code review
// =============================================================================

func TestCreateStages_EmptyNewLines(t *testing.T) {
	// Edge case: newLines slice is empty but diff has changes

	diff := &DiffResult{
		Changes: map[int]LineChange{
			1: {Type: ChangeModification, NewLineNum: 1, OldLineNum: 1, Content: "mod", OldContent: "old"},
			5: {Type: ChangeModification, NewLineNum: 5, OldLineNum: 5, Content: "mod2", OldContent: "old2"},
		},
		OldLineCount: 10,
		NewLineCount: 10,
	}

	// Empty newLines slice
	newLines := []string{}
	oldLines := []string{}

	// Should not panic, should return result with empty Lines in stages
	result := CreateStages(diff, 3, 1, 20, 1, 2, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "result should not be nil")
	assert.True(t, len(result.Stages) >= 1, "should have stages")

	// Stages should have empty Lines arrays
	for i, stage := range result.Stages {
		// Lines will be empty since newLines is empty
		assert.Equal(t, 0, len(stage.Lines), fmt.Sprintf("stage %d should have empty lines", i))
	}
}

func TestClusterDistanceFromCursor_CursorAtZero(t *testing.T) {
	// Edge case: cursor at line 0 (invalid but should handle gracefully)

	diff := &DiffResult{
		Changes: map[int]LineChange{
			5: {Type: ChangeModification, NewLineNum: 5, OldLineNum: 5, Content: "mod", OldContent: "old"},
		},
		OldLineCount: 10,
		NewLineCount: 10,
	}

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   5,
		Changes:   diff.Changes,
	}

	// Cursor at line 0
	distance := clusterDistanceFromCursor(cluster, 0, 1, diff)

	// Buffer line for change is 5, cursor is 0
	// Distance should be 5 - 0 = 5
	assert.Equal(t, 5, distance, "distance from cursor 0 to line 5")
}

func TestGetClusterBufferRange_AllAdditionsNoValidAnchor(t *testing.T) {
	// Edge case: all additions with OldLineNum=0 and no LineMapping

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// All additions with no valid anchor (OldLineNum=0, no mapping)
			5: {Type: ChangeAddition, NewLineNum: 5, OldLineNum: 0, Content: "added1"},
			6: {Type: ChangeAddition, NewLineNum: 6, OldLineNum: 0, Content: "added2"},
			7: {Type: ChangeAddition, NewLineNum: 7, OldLineNum: 0, Content: "added3"},
		},
		OldLineCount: 10,
		NewLineCount: 13,
		LineMapping:  nil, // No mapping available
	}

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   7,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 1, diff)

	// Should fall back to cluster.StartLine + baseLineOffset - 1 = 5 + 1 - 1 = 5
	assert.Equal(t, 5, startLine, "should fallback to cluster.StartLine")
	// For pure additions, maxOldLine = minOldLine
	assert.Equal(t, 5, endLine, "should equal startLine for pure additions")
}

func TestGetClusterBufferRange_BaseLineOffsetZero(t *testing.T) {
	// Edge case: baseLineOffset = 0

	diff := &DiffResult{
		Changes: map[int]LineChange{
			5: {Type: ChangeModification, NewLineNum: 5, OldLineNum: 5, Content: "mod", OldContent: "old"},
		},
		OldLineCount: 10,
		NewLineCount: 10,
	}

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   5,
		Changes:   diff.Changes,
	}

	startLine, endLine := getClusterBufferRange(cluster, 0, diff)

	// With baseLineOffset=0: bufferLine = 5 + 0 - 1 = 4
	assert.Equal(t, 4, startLine, "buffer start with offset 0")
	assert.Equal(t, 4, endLine, "buffer end with offset 0")
}

func TestCreateStages_PartiallyVisibleSingleCluster_FarFromCursor(t *testing.T) {
	// Edge case: single cluster that is partially visible but far from cursor

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// Cluster spans lines 45-55, viewport is 1-50, so partially visible
			45: {Type: ChangeModification, NewLineNum: 45, OldLineNum: 45, Content: "mod1", OldContent: "old1"},
			50: {Type: ChangeModification, NewLineNum: 50, OldLineNum: 50, Content: "mod2", OldContent: "old2"},
			55: {Type: ChangeModification, NewLineNum: 55, OldLineNum: 55, Content: "mod3", OldContent: "old3"},
		},
		OldLineCount: 60,
		NewLineCount: 60,
	}

	newLines := make([]string, 60)
	oldLines := make([]string, 60)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// Cursor at line 5 (far from cluster at 45-55), viewport 1-50 (cluster partially visible)
	result := CreateStages(diff, 5, 1, 50, 1, 3, "test.go", newLines, oldLines)

	// Single cluster that's partially visible but far from cursor
	assert.NotNil(t, result, "should create staging result")
	assert.True(t, len(result.Stages) >= 1, "should have at least 1 stage")

	// Distance from cursor (5) to cluster start (45) = 40 > threshold (3)
	assert.True(t, result.FirstNeedsNavigation,
		"FirstNeedsNavigation should be true when cluster is far from cursor")
}

func TestCreateStages_PartiallyVisibleSingleCluster_CloseToCursor(t *testing.T) {
	// Edge case: single cluster that is partially visible but close to cursor

	diff := &DiffResult{
		Changes: map[int]LineChange{
			// Cluster spans lines 48-55, viewport is 1-50
			// Cursor at 47 is within threshold of 48
			48: {Type: ChangeModification, NewLineNum: 48, OldLineNum: 48, Content: "mod1", OldContent: "old1"},
			55: {Type: ChangeModification, NewLineNum: 55, OldLineNum: 55, Content: "mod2", OldContent: "old2"},
		},
		OldLineCount: 60,
		NewLineCount: 60,
	}

	newLines := make([]string, 60)
	oldLines := make([]string, 60)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// Cursor at line 47, viewport 1-50
	result := CreateStages(diff, 47, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "should create staging result for partially visible cluster")
}

func TestCreateStages_SingleClusterEntirelyOutsideViewport(t *testing.T) {
	// Edge case: single cluster entirely outside viewport

	diff := &DiffResult{
		Changes: map[int]LineChange{
			100: {Type: ChangeModification, NewLineNum: 100, OldLineNum: 100, Content: "mod", OldContent: "old"},
		},
		OldLineCount: 150,
		NewLineCount: 150,
	}

	newLines := make([]string, 150)
	oldLines := make([]string, 150)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// Cursor at line 10, viewport 1-50, cluster at 100 (entirely outside)
	result := CreateStages(diff, 10, 1, 50, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "should create staging result")
	assert.Equal(t, 1, len(result.Stages), "should have 1 stage")
	assert.True(t, result.FirstNeedsNavigation,
		"FirstNeedsNavigation should be true when cluster is outside viewport")
}

func TestClusterNeedsNavigation_PartiallyVisible(t *testing.T) {
	// Test clusterNeedsNavigation for partially visible cluster

	diff := &DiffResult{
		Changes: map[int]LineChange{
			45: {Type: ChangeModification, NewLineNum: 45, OldLineNum: 45, Content: "mod1", OldContent: "old1"},
			55: {Type: ChangeModification, NewLineNum: 55, OldLineNum: 55, Content: "mod2", OldContent: "old2"},
		},
		OldLineCount: 60,
		NewLineCount: 60,
	}

	cluster := &ChangeCluster{
		StartLine: 45,
		EndLine:   55,
		Changes:   diff.Changes,
	}

	// Viewport 1-50: cluster 45-55 is partially visible
	// Cursor at 47: distance to 45-55 is 0 (cursor within range)
	needsNav := clusterNeedsNavigation(cluster, 47, 1, 50, 1, diff, 3)

	// Not entirely outside viewport, cursor within cluster
	assert.False(t, needsNav, "should not need navigation when cursor is within cluster")

	// Cursor at 10: distance to 45-55 is 35
	needsNav = clusterNeedsNavigation(cluster, 10, 1, 50, 1, diff, 3)

	// Not entirely outside viewport, but far from cursor
	assert.True(t, needsNav, "should need navigation when far from cursor")
}

func TestCreateStages_NoViewportInfo(t *testing.T) {
	// Edge case: viewportTop=0 and viewportBottom=0 (no viewport info)

	diff := &DiffResult{
		Changes: map[int]LineChange{
			10:  {Type: ChangeModification, NewLineNum: 10, OldLineNum: 10, Content: "mod1", OldContent: "old1"},
			100: {Type: ChangeModification, NewLineNum: 100, OldLineNum: 100, Content: "mod2", OldContent: "old2"},
		},
		OldLineCount: 150,
		NewLineCount: 150,
	}

	newLines := make([]string, 150)
	oldLines := make([]string, 150)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("line%d", i+1)
		oldLines[i] = fmt.Sprintf("line%d", i+1)
	}

	// No viewport info (0, 0), cursor at 50
	result := CreateStages(diff, 50, 0, 0, 1, 3, "test.go", newLines, oldLines)

	assert.NotNil(t, result, "should create staging result")
	assert.Equal(t, 2, len(result.Stages), "should have 2 stages (large gap between 10 and 100)")

	// Both should be treated as in-view, sorted by distance to cursor
	// Cursor at 50: distance to 10 is 40, distance to 100 is 50
	// So line 10 should be first
	assert.Equal(t, 10, result.Stages[0].BufferStart, "closer stage first")
}

func TestGetClusterNewLineRange_NoNewLineNum(t *testing.T) {
	// Edge case: changes have NewLineNum=0, should fallback to cluster coordinates

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   10,
		Changes: map[int]LineChange{
			5: {Type: ChangeModification, NewLineNum: 0, OldLineNum: 5, Content: "mod"},
			7: {Type: ChangeModification, NewLineNum: 0, OldLineNum: 7, Content: "mod"},
		},
	}

	startLine, endLine := getClusterNewLineRange(cluster)

	// Should fallback to cluster.StartLine and cluster.EndLine
	assert.Equal(t, 5, startLine, "should fallback to cluster.StartLine")
	assert.Equal(t, 10, endLine, "should fallback to cluster.EndLine")
}

func TestBuildStagesFromClusters_SingleDeletion(t *testing.T) {
	// Edge case: cluster with only deletions (no new content)

	diff := &DiffResult{
		Changes: map[int]LineChange{
			5: {Type: ChangeDeletion, NewLineNum: 0, OldLineNum: 5, OldContent: "deleted"},
		},
		OldLineCount: 10,
		NewLineCount: 9,
	}

	cluster := &ChangeCluster{
		StartLine: 5,
		EndLine:   5,
		Changes:   diff.Changes,
	}

	newLines := []string{"1", "2", "3", "4", "6", "7", "8", "9", "10"} // Line 5 deleted

	stages := buildStagesFromClusters([]*ChangeCluster{cluster}, newLines, "test.go", 1, diff)

	assert.Equal(t, 1, len(stages), "should have 1 stage")
	// For deletions, lines might be empty or contain surrounding context
	// The important thing is it doesn't panic
	assert.NotNil(t, stages[0], "stage should not be nil")
}
