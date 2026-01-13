package git

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cuken/overseer/pkg/types"
)

// BranchManager handles branch operations for tasks
type BranchManager struct {
	git          *Git
	branchPrefix string
	mergeTarget  string
}

// NewBranchManager creates a new branch manager
func NewBranchManager(git *Git, cfg types.GitConfig) *BranchManager {
	return &BranchManager{
		git:          git,
		branchPrefix: cfg.BranchPrefix,
		mergeTarget:  cfg.MergeTarget,
	}
}

// CreateTaskBranch creates a new branch for a task
func (b *BranchManager) CreateTaskBranch(task *types.Task) error {
	// Ensure we're on the merge target first
	if err := b.git.Checkout(b.mergeTarget); err != nil {
		return fmt.Errorf("failed to checkout %s: %w", b.mergeTarget, err)
	}

	// Pull latest changes
	if b.git.HasRemote() {
		if err := b.git.Pull(); err != nil {
			// Ignore pull errors for now (might not have upstream)
		}
	}

	// Create the task branch
	branchName := b.BranchName(task)
	if err := b.git.CheckoutNew(branchName); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}

	// Update task with branch name
	task.Branch = branchName

	return nil
}

// SwitchToTaskBranch switches to an existing task branch
func (b *BranchManager) SwitchToTaskBranch(task *types.Task) error {
	if task.Branch == "" {
		return fmt.Errorf("task has no branch assigned")
	}

	if !b.git.BranchExists(task.Branch) {
		return fmt.Errorf("branch %s does not exist", task.Branch)
	}

	return b.git.Checkout(task.Branch)
}

// CommitTaskProgress commits current changes with a task-related message
func (b *BranchManager) CommitTaskProgress(task *types.Task, message string) error {
	hasChanges, err := b.git.HasChanges()
	if err != nil {
		return err
	}
	if !hasChanges {
		return nil // Nothing to commit
	}

	if err := b.git.AddAll(); err != nil {
		return err
	}

	fullMessage := fmt.Sprintf("[%s] %s\n\nTask: %s\nPhase: %s",
		task.ID[:8], message, task.Title, task.Phase)

	return b.git.Commit(fullMessage)
}

// PushTaskBranch pushes the task branch to origin
func (b *BranchManager) PushTaskBranch(task *types.Task) error {
	if !b.git.HasRemote() {
		return nil // No remote, skip push
	}

	return b.git.PushSetUpstream(task.Branch)
}

// CleanupTaskBranch removes a completed task's branch
func (b *BranchManager) CleanupTaskBranch(task *types.Task) error {
	if task.Branch == "" {
		return nil
	}

	// Switch to merge target first
	if err := b.git.Checkout(b.mergeTarget); err != nil {
		return err
	}

	// Delete local branch
	if err := b.git.DeleteBranch(task.Branch); err != nil {
		return err
	}

	// Delete remote branch if exists
	if b.git.HasRemote() {
		b.git.DeleteRemoteBranch(task.Branch)
	}

	return nil
}

// BranchName generates a branch name for a task
func (b *BranchManager) BranchName(task *types.Task) string {
	if task.Branch != "" {
		return task.Branch
	}

	slug := slugify(task.Title)
	return fmt.Sprintf("%s/task-%s-%s", b.branchPrefix, task.ID[:8], slug)
}

// ListTaskBranches returns all branches matching the task pattern
func (b *BranchManager) ListTaskBranches() ([]string, error) {
	out, err := b.git.run("branch", "--list", fmt.Sprintf("%s/task-*", b.branchPrefix))
	if err != nil {
		return nil, err
	}

	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "* ") // Remove current branch marker
		if line != "" {
			branches = append(branches, line)
		}
	}

	return branches, nil
}

// GetMergeTarget returns the configured merge target branch
func (b *BranchManager) GetMergeTarget() string {
	return b.mergeTarget
}

// slugify creates a URL-safe slug from a title
func slugify(title string) string {
	slug := strings.ToLower(title)
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	slug = reg.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 30 {
		slug = slug[:30]
	}
	return slug
}
