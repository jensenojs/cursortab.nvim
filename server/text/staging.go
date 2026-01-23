package text

import (
	"cursortab/types"
	"sort"
	"strings"
)

// ChangeCluster represents a group of nearby changes (within threshold lines)
type ChangeCluster struct {
	StartLine int             // First line with changes (1-indexed)
	EndLine   int             // Last line with changes (1-indexed)
	Changes   map[int]LineDiff // Map of line number to diff operation
}

// ClusterChanges groups nearby changes (within threshold lines) into clusters
func ClusterChanges(diff *DiffResult, threshold int) []*ChangeCluster {
	if len(diff.Changes) == 0 {
		return nil
	}

	// Get sorted line numbers
	var lineNumbers []int
	for lineNum := range diff.Changes {
		lineNumbers = append(lineNumbers, lineNum)
	}
	sort.Ints(lineNumbers)

	var clusters []*ChangeCluster
	var currentCluster *ChangeCluster

	for _, lineNum := range lineNumbers {
		change := diff.Changes[lineNum]

		// Get the actual end line for group types
		endLine := lineNum
		if change.Type == LineModificationGroup || change.Type == LineAdditionGroup {
			endLine = change.EndLine
		}

		if currentCluster == nil {
			// Start new cluster
			currentCluster = &ChangeCluster{
				StartLine: lineNum,
				EndLine:   endLine,
				Changes:   make(map[int]LineDiff),
			}
			currentCluster.Changes[lineNum] = change
		} else {
			// Check if this change is within threshold of the current cluster
			gap := lineNum - currentCluster.EndLine
			if gap <= threshold {
				// Add to current cluster
				currentCluster.Changes[lineNum] = change
				if endLine > currentCluster.EndLine {
					currentCluster.EndLine = endLine
				}
			} else {
				// Gap too large - finalize current cluster and start new one
				clusters = append(clusters, currentCluster)
				currentCluster = &ChangeCluster{
					StartLine: lineNum,
					EndLine:   endLine,
					Changes:   make(map[int]LineDiff),
				}
				currentCluster.Changes[lineNum] = change
			}
		}
	}

	// Don't forget the last cluster
	if currentCluster != nil {
		clusters = append(clusters, currentCluster)
	}

	return clusters
}

// ShouldSplitCompletion returns true if there are gaps > threshold between changes
func ShouldSplitCompletion(diff *DiffResult, threshold int) bool {
	clusters := ClusterChanges(diff, threshold)
	return len(clusters) > 1
}

// CreateStagesFromClusters builds stages sorted by cursor distance
// Each stage contains changes for one cluster, with a cursor target pointing to the next cluster
// baseLineOffset is the 1-indexed line number where the completion range starts in the buffer
func CreateStagesFromClusters(
	clusters []*ChangeCluster,
	originalLines []string,
	newLines []string,
	cursorRow int,
	filePath string,
	baseLineOffset int,
) []*types.CompletionStage {
	if len(clusters) == 0 {
		return nil
	}

	// Sort clusters by distance from cursor (closest first), with line number as tiebreaker
	sortedClusters := make([]*ChangeCluster, len(clusters))
	copy(sortedClusters, clusters)
	sort.SliceStable(sortedClusters, func(i, j int) bool {
		// Convert cluster coordinates to buffer coordinates for distance calculation
		distI := clusterDistanceFromCursor(sortedClusters[i], cursorRow, baseLineOffset)
		distJ := clusterDistanceFromCursor(sortedClusters[j], cursorRow, baseLineOffset)
		if distI != distJ {
			return distI < distJ
		}
		// Tiebreaker: sort by line number for deterministic ordering
		return sortedClusters[i].StartLine < sortedClusters[j].StartLine
	})

	var stages []*types.CompletionStage

	for i, cluster := range sortedClusters {
		isLastStage := i == len(sortedClusters)-1

		// Create completion for this cluster (with coordinate offset)
		completion := createCompletionFromCluster(cluster, newLines, baseLineOffset)

		// Create cursor target (convert to buffer coordinates)
		var cursorTarget *types.CursorPredictionTarget
		if isLastStage {
			// Last stage: cursor target to end of this cluster with retrigger
			bufferEndLine := cluster.EndLine + baseLineOffset - 1
			cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    filePath,
				LineNumber:      int32(bufferEndLine),
				ShouldRetrigger: true,
			}
		} else {
			// Not last stage: cursor target to the start of the next cluster
			nextCluster := sortedClusters[i+1]
			bufferStartLine := nextCluster.StartLine + baseLineOffset - 1
			cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    filePath,
				LineNumber:      int32(bufferStartLine),
				ShouldRetrigger: false,
			}
		}

		stages = append(stages, &types.CompletionStage{
			Completion:   completion,
			CursorTarget: cursorTarget,
			IsLastStage:  isLastStage,
		})
	}

	return stages
}

// clusterDistanceFromCursor calculates the minimum distance from cursor to a cluster
// baseLineOffset converts cluster-relative coordinates to buffer coordinates
func clusterDistanceFromCursor(cluster *ChangeCluster, cursorRow int, baseLineOffset int) int {
	// Convert cluster coordinates to buffer coordinates
	bufferStartLine := cluster.StartLine + baseLineOffset - 1
	bufferEndLine := cluster.EndLine + baseLineOffset - 1

	if cursorRow >= bufferStartLine && cursorRow <= bufferEndLine {
		return 0 // Cursor is within the cluster
	}
	if cursorRow < bufferStartLine {
		return bufferStartLine - cursorRow
	}
	return cursorRow - bufferEndLine
}

// createCompletionFromCluster creates a Completion from a cluster of changes
// It determines the minimal line range that needs to be replaced
// baseLineOffset converts cluster-relative coordinates to buffer coordinates
func createCompletionFromCluster(cluster *ChangeCluster, newLines []string, baseLineOffset int) *types.Completion {
	// Cluster coordinates are relative to the extracted text (1-indexed)
	// We need to map them back to buffer coordinates
	startLine := cluster.StartLine
	endLine := cluster.EndLine

	// Convert to buffer coordinates
	bufferStartLine := startLine + baseLineOffset - 1
	bufferEndLine := endLine + baseLineOffset - 1

	// Extract the new content for this range (using cluster-relative indices)
	var lines []string
	for i := startLine; i <= endLine && i-1 < len(newLines); i++ {
		lines = append(lines, newLines[i-1])
	}

	// Ensure we have at least the lines from startLine to endLine
	// If newLines is shorter, use empty strings (shouldn't happen in normal cases)
	for len(lines) < endLine-startLine+1 {
		lines = append(lines, "")
	}

	return &types.Completion{
		StartLine:  bufferStartLine,
		EndLineInc: bufferEndLine,
		Lines:      lines,
	}
}

// AnalyzeDiffForStaging analyzes the diff between original and new text
// and returns the DiffResult. This is a wrapper for analyzeDiff that's exported
// for use by the staging logic.
func AnalyzeDiffForStaging(originalText, newText string) *DiffResult {
	return analyzeDiff(originalText, newText)
}

// JoinLines joins a slice of strings with newlines
func JoinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
