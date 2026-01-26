package text

import (
	"cursortab/types"
	"sort"
	"strings"
)

// Stage represents a single stage of changes to apply
type Stage struct {
	BufferStart  int                          // 1-indexed buffer coordinate
	BufferEnd    int                          // 1-indexed, inclusive
	Lines        []string                     // New content for this stage
	Changes      map[int]LineChange           // Changes keyed by line num relative to stage
	Groups       []*Group                     // Pre-computed groups for rendering
	CursorLine   int                          // Cursor position (1-indexed, relative to stage)
	CursorCol    int                          // Cursor column (0-indexed)
	CursorTarget *types.CursorPredictionTarget // Navigation target
	IsLastStage  bool
}

// StagingResult contains the result of CreateStages
type StagingResult struct {
	Stages               []*Stage
	FirstNeedsNavigation bool
}

// ChangeCluster represents a group of nearby changes (within threshold lines)
type ChangeCluster struct {
	StartLine int                // First line with changes (1-indexed)
	EndLine   int                // Last line with changes (1-indexed)
	Changes   map[int]LineChange // Map of line number to change operation
}

// CreateStages is the main entry point for creating stages from a diff result.
// Always returns stages (at least 1 stage for non-empty changes).
//
// Parameters:
//   - diff: The diff result from ComputeDiff
//   - cursorRow: Current cursor position (1-indexed buffer coordinate)
//   - viewportTop, viewportBottom: Visible viewport (1-indexed buffer coordinates)
//   - baseLineOffset: Where the diff range starts in the buffer (1-indexed)
//   - proximityThreshold: Max gap between changes to be in same stage
//   - filePath: File path for cursor targets
//   - newLines: New content lines for extracting stage content
//   - oldLines: Old content lines for extracting old content in groups
func CreateStages(
	diff *DiffResult,
	cursorRow int,
	viewportTop, viewportBottom int,
	baseLineOffset int,
	proximityThreshold int,
	filePath string,
	newLines []string,
	oldLines []string,
) *StagingResult {
	if len(diff.Changes) == 0 {
		return nil
	}

	// Step 1: Partition changes by viewport visibility
	var inViewChanges, outViewChanges []int
	for lineNum, change := range diff.Changes {
		bufferLine := getBufferLineForChange(change, lineNum, baseLineOffset, diff.LineMapping)

		isVisible := viewportTop == 0 && viewportBottom == 0 ||
			(bufferLine >= viewportTop && bufferLine <= viewportBottom)

		if isVisible {
			inViewChanges = append(inViewChanges, lineNum)
		} else {
			outViewChanges = append(outViewChanges, lineNum)
		}
	}

	sort.Ints(inViewChanges)
	sort.Ints(outViewChanges)

	// Step 2: Group changes by proximity within each partition
	inViewClusters := groupChangesByProximity(diff, inViewChanges, proximityThreshold)
	outViewClusters := groupChangesByProximity(diff, outViewChanges, proximityThreshold)

	allClusters := append(inViewClusters, outViewClusters...)

	if len(allClusters) == 0 {
		return nil
	}

	// Step 3: Sort clusters by cursor distance
	sort.SliceStable(allClusters, func(i, j int) bool {
		distI := clusterDistanceFromCursor(allClusters[i], cursorRow, baseLineOffset, diff)
		distJ := clusterDistanceFromCursor(allClusters[j], cursorRow, baseLineOffset, diff)
		if distI != distJ {
			return distI < distJ
		}
		return allClusters[i].StartLine < allClusters[j].StartLine
	})

	// Step 4: Create stages from clusters
	stages := buildStagesFromClusters(allClusters, newLines, filePath, baseLineOffset, diff)

	// Step 5: Check if first stage needs navigation UI
	firstNeedsNav := clusterNeedsNavigation(
		allClusters[0], cursorRow, viewportTop, viewportBottom,
		baseLineOffset, diff, proximityThreshold,
	)

	return &StagingResult{
		Stages:               stages,
		FirstNeedsNavigation: firstNeedsNav,
	}
}

// getBufferLineForChange calculates the buffer line for a change using the appropriate coordinate.
func getBufferLineForChange(change LineChange, mapKey int, baseLineOffset int, mapping *LineMapping) int {
	if change.OldLineNum > 0 {
		return change.OldLineNum + baseLineOffset - 1
	}

	if mapping != nil && change.NewLineNum > 0 && change.NewLineNum <= len(mapping.NewToOld) {
		oldLine := mapping.NewToOld[change.NewLineNum-1]
		if oldLine > 0 {
			return oldLine + baseLineOffset - 1
		}
		for i := change.NewLineNum - 2; i >= 0; i-- {
			if mapping.NewToOld[i] > 0 {
				return mapping.NewToOld[i] + baseLineOffset - 1
			}
		}
	}

	return mapKey + baseLineOffset - 1
}

// groupChangesByProximity groups sorted line numbers into clusters based on proximity.
func groupChangesByProximity(diff *DiffResult, lineNumbers []int, proximityThreshold int) []*ChangeCluster {
	if len(lineNumbers) == 0 {
		return nil
	}

	var clusters []*ChangeCluster
	var currentCluster *ChangeCluster

	for _, lineNum := range lineNumbers {
		change := diff.Changes[lineNum]
		endLine := lineNum

		if currentCluster == nil {
			currentCluster = &ChangeCluster{
				StartLine: lineNum,
				EndLine:   endLine,
				Changes:   make(map[int]LineChange),
			}
			currentCluster.Changes[lineNum] = change
		} else {
			gap := lineNum - currentCluster.EndLine
			if gap <= proximityThreshold {
				currentCluster.Changes[lineNum] = change
				if endLine > currentCluster.EndLine {
					currentCluster.EndLine = endLine
				}
			} else {
				clusters = append(clusters, currentCluster)
				currentCluster = &ChangeCluster{
					StartLine: lineNum,
					EndLine:   endLine,
					Changes:   make(map[int]LineChange),
				}
				currentCluster.Changes[lineNum] = change
			}
		}
	}

	if currentCluster != nil {
		clusters = append(clusters, currentCluster)
	}

	return clusters
}

// clusterNeedsNavigation determines if a cluster requires cursor prediction UI.
func clusterNeedsNavigation(cluster *ChangeCluster, cursorRow, viewportTop, viewportBottom, baseLineOffset int, diff *DiffResult, distThreshold int) bool {
	bufferStart, bufferEnd := getClusterBufferRange(cluster, baseLineOffset, diff)

	if viewportTop > 0 && viewportBottom > 0 {
		entirelyOutside := bufferEnd < viewportTop || bufferStart > viewportBottom
		if entirelyOutside {
			return true
		}
	}

	distance := clusterDistanceFromCursor(cluster, cursorRow, baseLineOffset, diff)
	return distance > distThreshold
}

// clusterDistanceFromCursor calculates the minimum distance from cursor to a cluster.
func clusterDistanceFromCursor(cluster *ChangeCluster, cursorRow int, baseLineOffset int, diff *DiffResult) int {
	bufferStartLine, bufferEndLine := getClusterBufferRange(cluster, baseLineOffset, diff)

	if cursorRow >= bufferStartLine && cursorRow <= bufferEndLine {
		return 0
	}
	if cursorRow < bufferStartLine {
		return bufferStartLine - cursorRow
	}
	return cursorRow - bufferEndLine
}

// getClusterBufferRange determines the buffer line range for a cluster using coordinate mapping.
func getClusterBufferRange(cluster *ChangeCluster, baseLineOffset int, diff *DiffResult) (int, int) {
	minOldLine := -1
	maxOldLine := -1
	minOldLineFromNonAdditions := -1
	hasAdditions := false
	hasNonAdditions := false
	maxNewLineNum := 0

	for lineNum, change := range cluster.Changes {
		bufferLine := getBufferLineForChange(change, lineNum, baseLineOffset, diff.LineMapping)

		isAddition := change.Type == ChangeAddition

		if isAddition {
			hasAdditions = true
			if change.NewLineNum > maxNewLineNum {
				maxNewLineNum = change.NewLineNum
			}
			hasValidAnchor := change.OldLineNum > 0 ||
				(diff.LineMapping != nil && change.NewLineNum > 0)
			if hasValidAnchor {
				if minOldLine == -1 || bufferLine < minOldLine {
					minOldLine = bufferLine
				}
			}
		} else {
			hasNonAdditions = true
			if minOldLineFromNonAdditions == -1 || bufferLine < minOldLineFromNonAdditions {
				minOldLineFromNonAdditions = bufferLine
			}
			if minOldLine == -1 || bufferLine < minOldLine {
				minOldLine = bufferLine
			}
			if bufferLine > maxOldLine {
				maxOldLine = bufferLine
			}
		}
	}

	if hasAdditions && hasNonAdditions && minOldLineFromNonAdditions > 0 {
		minOldLine = minOldLineFromNonAdditions
	}

	if hasAdditions && diff.OldLineCount > 0 {
		lastOldLineInRange := baseLineOffset + diff.OldLineCount - 1
		if maxNewLineNum > diff.OldLineCount && maxOldLine < lastOldLineInRange {
			maxOldLine = lastOldLineInRange
		}
	}

	// Track if minOldLine was set from a valid anchor (not fallback)
	// Pure additions with valid anchors need to use insertion point (anchor + 1)
	hadValidAnchor := minOldLine != -1

	if minOldLine == -1 {
		minOldLine = cluster.StartLine + baseLineOffset - 1
	}
	if maxOldLine == -1 {
		if hasAdditions && !hasNonAdditions {
			maxOldLine = minOldLine
		} else {
			maxOldLine = cluster.EndLine + baseLineOffset - 1
		}
	}

	// For pure additions WITH a valid anchor, the buffer range represents
	// where the new content will be INSERTED, not the anchor. Additions are inserted
	// AFTER the anchor line, so we need to add 1 to get the insertion point.
	// E.g., if anchor is old line 2, new content appears starting at buffer line 3.
	// Note: We only do this when there was a valid anchor (not fallback to cluster.StartLine)
	if hasAdditions && !hasNonAdditions && hadValidAnchor {
		minOldLine++
		maxOldLine = minOldLine // For pure additions, start == end (insertion point)
	}

	return minOldLine, maxOldLine
}

// getClusterNewLineRange determines the new line range for content extraction.
func getClusterNewLineRange(cluster *ChangeCluster) (int, int) {
	minNewLine := -1
	maxNewLine := -1

	for _, change := range cluster.Changes {
		if change.NewLineNum > 0 {
			if minNewLine == -1 || change.NewLineNum < minNewLine {
				minNewLine = change.NewLineNum
			}
			if change.NewLineNum > maxNewLine {
				maxNewLine = change.NewLineNum
			}
		}
	}

	if minNewLine == -1 {
		minNewLine = cluster.StartLine
	}
	if maxNewLine == -1 {
		maxNewLine = cluster.EndLine
	}

	return minNewLine, maxNewLine
}

// buildStagesFromClusters creates Stages from clusters.
func buildStagesFromClusters(clusters []*ChangeCluster, newLines []string, filePath string, baseLineOffset int, diff *DiffResult) []*Stage {
	var stages []*Stage

	for i, cluster := range clusters {
		isLastStage := i == len(clusters)-1

		// Get buffer range for this cluster
		bufferStart, bufferEnd := getClusterBufferRange(cluster, baseLineOffset, diff)

		// Get new line range for content extraction
		newStartLine, newEndLine := getClusterNewLineRange(cluster)

		// Extract the new content using new coordinates
		var stageLines []string
		for j := newStartLine; j <= newEndLine && j-1 < len(newLines); j++ {
			if j > 0 {
				stageLines = append(stageLines, newLines[j-1])
			}
		}

		// Extract old content for modifications
		stageOldLines := make([]string, len(stageLines))
		for lineNum, change := range cluster.Changes {
			newLineNum := lineNum
			if change.NewLineNum > 0 {
				newLineNum = change.NewLineNum
			}
			relativeIdx := newLineNum - newStartLine
			if relativeIdx >= 0 && relativeIdx < len(stageOldLines) {
				stageOldLines[relativeIdx] = change.OldContent
			}
		}

		// Create remapped changes with line numbers relative to stage content
		remappedChanges := make(map[int]LineChange)
		for lineNum, change := range cluster.Changes {
			newLineNum := lineNum
			if change.NewLineNum > 0 {
				newLineNum = change.NewLineNum
			}
			relativeLine := newLineNum - newStartLine + 1
			if relativeLine > 0 && relativeLine <= len(stageLines) {
				remappedChange := change
				remappedChange.NewLineNum = relativeLine
				remappedChanges[relativeLine] = remappedChange
			}
		}

		// Compute groups and cursor position using the grouping module
		groups := GroupChanges(remappedChanges)
		cursorLine, cursorCol := CalculateCursorPosition(remappedChanges, stageLines)

		// Create cursor target
		var cursorTarget *types.CursorPredictionTarget
		if isLastStage {
			_, bufferEndLine := getClusterBufferRange(cluster, baseLineOffset, diff)
			cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    filePath,
				LineNumber:      int32(bufferEndLine),
				ShouldRetrigger: true,
			}
		} else {
			nextCluster := clusters[i+1]
			nextBufferStart, _ := getClusterBufferRange(nextCluster, baseLineOffset, diff)
			cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    filePath,
				LineNumber:      int32(nextBufferStart),
				ShouldRetrigger: false,
			}
		}

		stages = append(stages, &Stage{
			BufferStart:  bufferStart,
			BufferEnd:    bufferEnd,
			Lines:        stageLines,
			Changes:      remappedChanges,
			Groups:       groups,
			CursorLine:   cursorLine,
			CursorCol:    cursorCol,
			CursorTarget: cursorTarget,
			IsLastStage:  isLastStage,
		})
	}

	return stages
}

// AnalyzeDiffForStagingWithViewport analyzes the diff with viewport-aware grouping
func AnalyzeDiffForStagingWithViewport(originalText, newText string, viewportTop, viewportBottom, baseLineOffset int) *DiffResult {
	return ComputeDiff(originalText, newText)
}

// JoinLines joins a slice of strings with newlines.
// Each line gets a trailing \n, which is the standard line terminator format
// that diffmatchpatch expects. This ensures proper line counting:
// - ["a", "b"] → "a\nb\n" (2 lines)
// - ["a", ""] → "a\n\n" (2 lines, second is empty)
func JoinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
