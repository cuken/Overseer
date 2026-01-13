package git

import (
	"fmt"
	"strings"

	"github.com/cuken/overseer/pkg/types"
)

// MergeResult represents the outcome of a merge attempt
type MergeResult struct {
	Success       bool
	ConflictFiles []string
	Message       string
}

// Merger handles merge operations for tasks
type Merger struct {
	git          *Git
	branchMgr    *BranchManager
	autoPush     bool
}

// NewMerger creates a new merger
func NewMerger(git *Git, branchMgr *BranchManager, cfg types.GitConfig) *Merger {
	return &Merger{
		git:       git,
		branchMgr: branchMgr,
		autoPush:  cfg.AutoPush,
	}
}

// AttemptMerge tries to merge a task branch into the merge target
func (m *Merger) AttemptMerge(task *types.Task) (*MergeResult, error) {
	if task.Branch == "" {
		return nil, fmt.Errorf("task has no branch")
	}

	mergeTarget := m.branchMgr.GetMergeTarget()

	// Stash any uncommitted changes
	hasChanges, _ := m.git.HasChanges()
	if hasChanges {
		if err := m.git.Stash(); err != nil {
			return nil, fmt.Errorf("failed to stash changes: %w", err)
		}
		defer m.git.StashPop()
	}

	// Checkout merge target
	if err := m.git.Checkout(mergeTarget); err != nil {
		return nil, fmt.Errorf("failed to checkout %s: %w", mergeTarget, err)
	}

	// Pull latest
	if m.git.HasRemote() {
		if err := m.git.Pull(); err != nil {
			// Ignore pull errors, might not have upstream
		}
	}

	// Attempt merge with no-commit to check for conflicts first
	_, err := m.git.run("merge", "--no-commit", "--no-ff", task.Branch)
	if err != nil {
		// Check for conflicts
		conflicts, conflictErr := m.getConflictFiles()
		if conflictErr != nil || len(conflicts) == 0 {
			// Abort and return error
			m.git.run("merge", "--abort")
			return nil, fmt.Errorf("merge failed: %w", err)
		}

		// Abort the failed merge
		m.git.run("merge", "--abort")

		return &MergeResult{
			Success:       false,
			ConflictFiles: conflicts,
			Message:       fmt.Sprintf("Merge conflicts in %d files", len(conflicts)),
		}, nil
	}

	// Merge succeeded, commit it
	commitMsg := fmt.Sprintf("Merge %s: %s\n\nTask ID: %s\nMerged by: Overseer",
		task.Branch, task.Title, task.ID)

	if err := m.git.Commit(commitMsg); err != nil {
		// Might be nothing to commit if fast-forward
		m.git.run("merge", "--abort")
		return nil, fmt.Errorf("failed to commit merge: %w", err)
	}

	// Push if configured
	if m.autoPush && m.git.HasRemote() {
		if err := m.git.Push(); err != nil {
			return &MergeResult{
				Success: true,
				Message: "Merge succeeded but push failed: " + err.Error(),
			}, nil
		}
	}

	return &MergeResult{
		Success: true,
		Message: fmt.Sprintf("Successfully merged %s into %s", task.Branch, mergeTarget),
	}, nil
}

// getConflictFiles returns a list of files with merge conflicts
func (m *Merger) getConflictFiles() ([]string, error) {
	out, err := m.git.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}

	return files, nil
}

// ResolveConflicts applies conflict resolution for a set of files
// This is called after manual resolution or by an agent
func (m *Merger) ResolveConflicts(task *types.Task, resolvedFiles []string) error {
	// Add resolved files
	if err := m.git.Add(resolvedFiles...); err != nil {
		return fmt.Errorf("failed to stage resolved files: %w", err)
	}

	// Check if any conflicts remain
	conflicts, err := m.getConflictFiles()
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("conflicts remain in %d files: %v", len(conflicts), conflicts)
	}

	// Commit the resolution
	commitMsg := fmt.Sprintf("Resolve merge conflicts for: %s\n\nTask ID: %s\nResolved by: Overseer",
		task.Title, task.ID)

	return m.git.Commit(commitMsg)
}

// AbortMerge aborts an in-progress merge
func (m *Merger) AbortMerge() error {
	_, err := m.git.run("merge", "--abort")
	return err
}

// IsMerging checks if there's a merge in progress
func (m *Merger) IsMerging() bool {
	// Check for MERGE_HEAD file
	_, err := m.git.run("rev-parse", "--verify", "MERGE_HEAD")
	return err == nil
}

// CanMerge checks if a task branch can be merged (no conflicts predicted)
func (m *Merger) CanMerge(task *types.Task) (bool, []string, error) {
	if task.Branch == "" {
		return false, nil, fmt.Errorf("task has no branch")
	}

	mergeTarget := m.branchMgr.GetMergeTarget()

	// Use merge-tree to check for conflicts without actually merging
	// Note: This is available in git 2.38+
	out, err := m.git.run("merge-tree", "--write-tree", mergeTarget, task.Branch)
	if err != nil {
		// Fallback: just try the merge
		return true, nil, nil
	}

	// Check output for "CONFLICT" markers
	if strings.Contains(out, "CONFLICT") {
		// Extract conflict files (simplified)
		lines := strings.Split(out, "\n")
		var conflicts []string
		for _, line := range lines {
			if strings.HasPrefix(line, "CONFLICT") {
				// Parse filename from conflict line
				parts := strings.Fields(line)
				if len(parts) > 0 {
					conflicts = append(conflicts, parts[len(parts)-1])
				}
			}
		}
		return false, conflicts, nil
	}

	return true, nil, nil
}
