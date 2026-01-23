package text

import (
	"testing"
)

func TestClusterChanges_SingleCluster(t *testing.T) {
	// Changes at lines 10, 11, 12 should form one cluster
	diff := &DiffResult{
		Changes: map[int]LineDiff{
			10: {Type: LineModification, LineNumber: 10, Content: "line 10"},
			11: {Type: LineModification, LineNumber: 11, Content: "line 11"},
			12: {Type: LineModification, LineNumber: 12, Content: "line 12"},
		},
	}

	clusters := ClusterChanges(diff, 3)

	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(clusters))
	}

	if clusters[0].StartLine != 10 {
		t.Errorf("expected StartLine 10, got %d", clusters[0].StartLine)
	}

	if clusters[0].EndLine != 12 {
		t.Errorf("expected EndLine 12, got %d", clusters[0].EndLine)
	}

	if len(clusters[0].Changes) != 3 {
		t.Errorf("expected 3 changes, got %d", len(clusters[0].Changes))
	}
}

func TestClusterChanges_MultipleClusters(t *testing.T) {
	// Changes at lines 10, 12 and 25, 27 should form two clusters (threshold=3)
	diff := &DiffResult{
		Changes: map[int]LineDiff{
			10: {Type: LineModification, LineNumber: 10, Content: "line 10"},
			12: {Type: LineModification, LineNumber: 12, Content: "line 12"},
			25: {Type: LineModification, LineNumber: 25, Content: "line 25"},
			27: {Type: LineModification, LineNumber: 27, Content: "line 27"},
		},
	}

	clusters := ClusterChanges(diff, 3)

	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}

	// First cluster: lines 10-12
	if clusters[0].StartLine != 10 || clusters[0].EndLine != 12 {
		t.Errorf("first cluster: expected lines 10-12, got %d-%d", clusters[0].StartLine, clusters[0].EndLine)
	}

	// Second cluster: lines 25-27
	if clusters[1].StartLine != 25 || clusters[1].EndLine != 27 {
		t.Errorf("second cluster: expected lines 25-27, got %d-%d", clusters[1].StartLine, clusters[1].EndLine)
	}
}

func TestClusterChanges_ThreeClusters(t *testing.T) {
	// Changes at lines 10, 25, 40 should form three clusters (threshold=3)
	diff := &DiffResult{
		Changes: map[int]LineDiff{
			10: {Type: LineModification, LineNumber: 10, Content: "line 10"},
			25: {Type: LineModification, LineNumber: 25, Content: "line 25"},
			40: {Type: LineModification, LineNumber: 40, Content: "line 40"},
		},
	}

	clusters := ClusterChanges(diff, 3)

	if len(clusters) != 3 {
		t.Errorf("expected 3 clusters, got %d", len(clusters))
	}
}

func TestClusterChanges_EmptyDiff(t *testing.T) {
	diff := &DiffResult{
		Changes: map[int]LineDiff{},
	}

	clusters := ClusterChanges(diff, 3)

	if clusters != nil {
		t.Errorf("expected nil clusters for empty diff, got %v", clusters)
	}
}

func TestClusterChanges_WithGroupTypes(t *testing.T) {
	// A modification group spanning lines 10-15 should use EndLine for cluster boundary
	diff := &DiffResult{
		Changes: map[int]LineDiff{
			10: {
				Type:       LineModificationGroup,
				LineNumber: 10,
				StartLine:  10,
				EndLine:    15,
				Content:    "group content",
			},
			20: {Type: LineModification, LineNumber: 20, Content: "line 20"},
		},
	}

	clusters := ClusterChanges(diff, 3)

	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}

	// First cluster should end at line 15 (group end)
	if clusters[0].EndLine != 15 {
		t.Errorf("first cluster: expected EndLine 15, got %d", clusters[0].EndLine)
	}
}

func TestShouldSplitCompletion(t *testing.T) {
	tests := []struct {
		name      string
		changes   map[int]LineDiff
		threshold int
		expected  bool
	}{
		{
			name: "single cluster - no split",
			changes: map[int]LineDiff{
				10: {Type: LineModification, LineNumber: 10},
				11: {Type: LineModification, LineNumber: 11},
			},
			threshold: 3,
			expected:  false,
		},
		{
			name: "two clusters - should split",
			changes: map[int]LineDiff{
				10: {Type: LineModification, LineNumber: 10},
				25: {Type: LineModification, LineNumber: 25},
			},
			threshold: 3,
			expected:  true,
		},
		{
			name:      "empty - no split",
			changes:   map[int]LineDiff{},
			threshold: 3,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := &DiffResult{Changes: tt.changes}
			result := ShouldSplitCompletion(diff, tt.threshold)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestCreateStagesFromClusters(t *testing.T) {
	// Clusters with relative coordinates (1-indexed within the completion range)
	clusters := []*ChangeCluster{
		{StartLine: 1, EndLine: 3, Changes: map[int]LineDiff{1: {}, 3: {}}},   // Buffer lines 10-12
		{StartLine: 16, EndLine: 18, Changes: map[int]LineDiff{16: {}, 18: {}}}, // Buffer lines 25-27
		{StartLine: 31, EndLine: 31, Changes: map[int]LineDiff{31: {}}},        // Buffer line 40
	}

	originalLines := make([]string, 50)
	for i := range originalLines {
		originalLines[i] = ""
	}

	newLines := make([]string, 50)
	for i := range newLines {
		newLines[i] = "new content"
	}

	cursorRow := 15       // Cursor at buffer line 15
	filePath := "test.go"
	baseLineOffset := 10  // Completion starts at buffer line 10

	stages := CreateStagesFromClusters(clusters, originalLines, newLines, cursorRow, filePath, baseLineOffset)

	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}

	// Verify stages are sorted by distance from cursor (closest first)
	// Cursor at 15: cluster 10-12 is 3 away, cluster 25-27 is 10 away, cluster 40 is 25 away
	// First stage should map to buffer lines 10-12
	if stages[0].Completion.StartLine != 10 {
		t.Errorf("first stage should be closest cluster (buffer 10-12), got StartLine %d", stages[0].Completion.StartLine)
	}

	// Verify last stage has ShouldRetrigger=true
	if !stages[2].CursorTarget.ShouldRetrigger {
		t.Error("last stage should have ShouldRetrigger=true")
	}

	// Verify non-last stages have ShouldRetrigger=false
	if stages[0].CursorTarget.ShouldRetrigger {
		t.Error("first stage should have ShouldRetrigger=false")
	}

	// Verify IsLastStage flags
	if stages[0].IsLastStage || stages[1].IsLastStage {
		t.Error("non-last stages should have IsLastStage=false")
	}
	if !stages[2].IsLastStage {
		t.Error("last stage should have IsLastStage=true")
	}

	// Verify cursor targets point to correct buffer lines
	// First stage cursor target should point to next cluster's start (buffer line 25)
	if stages[0].CursorTarget.LineNumber != 25 {
		t.Errorf("first stage cursor target should be 25, got %d", stages[0].CursorTarget.LineNumber)
	}
}

func TestClusterDistanceFromCursor(t *testing.T) {
	// Cluster with relative coordinates 1-6, baseLineOffset=10 means buffer lines 10-15
	cluster := &ChangeCluster{StartLine: 1, EndLine: 6}
	baseLineOffset := 10

	tests := []struct {
		cursorRow int // buffer coordinates
		expected  int
	}{
		{5, 5},   // cursor before cluster (buffer line 5, cluster starts at buffer 10)
		{10, 0},  // cursor at start (buffer line 10)
		{12, 0},  // cursor inside (buffer line 12)
		{15, 0},  // cursor at end (buffer line 15)
		{20, 5},  // cursor after cluster (buffer line 20, cluster ends at buffer 15)
	}

	for _, tt := range tests {
		result := clusterDistanceFromCursor(cluster, tt.cursorRow, baseLineOffset)
		if result != tt.expected {
			t.Errorf("cursor at %d: expected distance %d, got %d", tt.cursorRow, tt.expected, result)
		}
	}
}

func TestClusterDistanceFromCursor_NoOffset(t *testing.T) {
	// When baseLineOffset=1, cluster coordinates match buffer coordinates
	cluster := &ChangeCluster{StartLine: 10, EndLine: 15}
	baseLineOffset := 1

	tests := []struct {
		cursorRow int
		expected  int
	}{
		{5, 5},   // cursor before cluster
		{10, 0},  // cursor at start
		{12, 0},  // cursor inside
		{15, 0},  // cursor at end
		{20, 5},  // cursor after cluster
	}

	for _, tt := range tests {
		result := clusterDistanceFromCursor(cluster, tt.cursorRow, baseLineOffset)
		if result != tt.expected {
			t.Errorf("cursor at %d: expected distance %d, got %d", tt.cursorRow, tt.expected, result)
		}
	}
}

func TestJoinLines(t *testing.T) {
	lines := []string{"line1", "line2", "line3"}
	result := JoinLines(lines)
	expected := "line1\nline2\nline3"

	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
