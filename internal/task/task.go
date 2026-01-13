package task

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cuken/overseer/pkg/types"
	"github.com/google/uuid"
)

// NewTask creates a new task from a request
func NewTask(title, description string) *types.Task {
	id := generateID()
	slug := slugify(title)
	branch := fmt.Sprintf("feature/task-%s-%s", id[:8], slug)

	return &types.Task{
		ID:               id,
		Title:            title,
		Description:      description,
		Branch:           branch,
		State:            types.StatePending,
		Phase:            types.PhasePlan,
		Priority:         0,
		RequiresApproval: false,
		Dependencies:     nil,
		MergeTarget:      "main",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		Handoffs:         0,
	}
}

// NewConflictResolutionTask creates a task to resolve merge conflicts
func NewConflictResolutionTask(parentTask *types.Task, conflictFiles []string) *types.Task {
	id := generateID()
	title := fmt.Sprintf("Resolve merge conflicts for: %s", parentTask.Title)
	description := fmt.Sprintf("Merge conflicts detected when merging %s into %s.\n\nConflicting files:\n",
		parentTask.Branch, parentTask.MergeTarget)
	for _, f := range conflictFiles {
		description += fmt.Sprintf("- %s\n", f)
	}

	return &types.Task{
		ID:               id,
		Title:            title,
		Description:      description,
		Branch:           parentTask.Branch,
		State:            types.StatePending,
		Phase:            types.PhasePlan,
		Priority:         parentTask.Priority + 1, // Higher priority than parent
		RequiresApproval: true,                    // Conflicts require human review
		Dependencies:     nil,
		MergeTarget:      parentTask.MergeTarget,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		Handoffs:         0,
		ConflictFiles:    conflictFiles,
		ParentTaskID:     parentTask.ID,
	}
}

// TransitionTo attempts to transition a task to a new state
func TransitionTo(task *types.Task, newState types.TaskState) error {
	if !task.State.CanTransitionTo(newState) {
		return fmt.Errorf("invalid transition from %s to %s", task.State, newState)
	}
	task.State = newState
	task.UpdatedAt = time.Now()
	return nil
}

// SetPhase sets the task phase and updates timestamp
func SetPhase(task *types.Task, phase types.TaskPhase) {
	task.Phase = phase
	task.UpdatedAt = time.Now()
}

// IncrementHandoff increments the handoff counter
func IncrementHandoff(task *types.Task) {
	task.Handoffs++
	task.UpdatedAt = time.Now()
}

// MarkRequiresApproval flags the task for human review
func MarkRequiresApproval(task *types.Task, reason string) {
	task.RequiresApproval = true
	task.Description += fmt.Sprintf("\n\n---\n**Approval Required**: %s", reason)
	task.UpdatedAt = time.Now()
}

// generateID creates a new unique task ID
func generateID() string {
	return uuid.New().String()
}

// slugify creates a URL-safe slug from a title
func slugify(title string) string {
	// Convert to lowercase
	slug := strings.ToLower(title)

	// Replace spaces and special chars with hyphens
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	slug = reg.ReplaceAllString(slug, "-")

	// Trim leading/trailing hyphens
	slug = strings.Trim(slug, "-")

	// Limit length
	if len(slug) > 30 {
		slug = slug[:30]
	}

	return slug
}

// ParseRequestFile extracts title and description from a markdown request file
func ParseRequestFile(content string) (title, description string) {
	lines := strings.Split(content, "\n")

	// Look for a markdown header as title
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimPrefix(line, "# ")
			// Rest is description
			if i+1 < len(lines) {
				description = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			}
			return
		}
	}

	// No header found, use first line as title
	if len(lines) > 0 {
		title = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			description = strings.TrimSpace(strings.Join(lines[1:], "\n"))
		}
	}

	return
}

// Summary returns a brief summary of the task
func Summary(task *types.Task) string {
	return fmt.Sprintf("[%s] %s (%s/%s) - Priority: %d",
		task.ID[:8], task.Title, task.State, task.Phase, task.Priority)
}
