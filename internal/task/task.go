package task

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cuken/overseer/pkg/types"
)

// NewTask creates a new task from a request
func NewTask(title, description string) *types.Task {
	createdAt := time.Now()
	id := generateID(title, description, createdAt)
	slug := slugify(title)
	branch := fmt.Sprintf("feature/task-%s-%s", id[3:], slug)

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
		CreatedAt:        createdAt,
		UpdatedAt:        createdAt,
		Handoffs:         0,
	}
}

// NewConflictResolutionTask creates a task to resolve merge conflicts
func NewConflictResolutionTask(parentTask *types.Task, conflictFiles []string) *types.Task {
	createdAt := time.Now()
	title := fmt.Sprintf("Resolve merge conflicts for: %s", parentTask.Title)
	description := fmt.Sprintf("Merge conflicts detected when merging %s into %s.\n\nConflicting files:\n",
		parentTask.Branch, parentTask.MergeTarget)
	for _, f := range conflictFiles {
		description += fmt.Sprintf("- %s\n", f)
	}
	id := generateID(title, description, createdAt)

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

// generateID creates a new hash-based task ID (e.g. bd-a1b2c3d4)
func generateID(title, description string, createdAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte(description))
	h.Write([]byte(createdAt.Format(time.RFC3339Nano)))
	return "bd-" + hex.EncodeToString(h.Sum(nil))[:8]
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

// TaskRequest represents the parsed contents of a request file
type TaskRequest struct {
	Title       string
	Description string
	Metadata    map[string]string
}

// ParseRequestFile extracts title, description and metadata from a markdown request file
func ParseRequestFile(content string) *TaskRequest {
	req := &TaskRequest{
		Metadata: make(map[string]string),
	}

	lines := strings.Split(content, "\n")
	startLine := 0

	// Check for YAML frontmatter
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "---" {
				startLine = i + 1
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.ToLower(strings.TrimSpace(parts[0]))
				val := strings.TrimSpace(parts[1])
				req.Metadata[key] = val
			}
		}
	}

	trimmedLines := lines[startLine:]
	// Look for a markdown header as title
	for i, line := range trimmedLines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			req.Title = strings.TrimPrefix(line, "# ")
			// Rest is description
			if i+1 < len(trimmedLines) {
				req.Description = strings.TrimSpace(strings.Join(trimmedLines[i+1:], "\n"))
			}
			return req
		}
	}

	// No header found, use first non-empty line as title
	for i, line := range trimmedLines {
		line = strings.TrimSpace(line)
		if line != "" {
			req.Title = line
			if i+1 < len(trimmedLines) {
				req.Description = strings.TrimSpace(strings.Join(trimmedLines[i+1:], "\n"))
			}
			break
		}
	}

	return req
}

// ParseRelativeDate parses a date string like "+2d", "2026-01-15T12:00:00Z", or "+5h"
func ParseRelativeDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}

	if strings.HasPrefix(s, "+") {
		durationStr := s[1:]
		// Handle days suffix 'd' which is not supported by time.ParseDuration
		if strings.HasSuffix(durationStr, "d") {
			daysStr := strings.TrimSuffix(durationStr, "d")
			var days int
			if _, err := fmt.Sscanf(daysStr, "%d", &days); err == nil {
				return time.Now().Add(time.Duration(days) * 24 * time.Hour), nil
			}
		}

		dur, err := time.ParseDuration(durationStr)
		if err == nil {
			return time.Now().Add(dur), nil
		}
	}

	// Try common layouts
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date format: %s", s)
}

// Summary returns a brief summary of the task
func Summary(task *types.Task) string {
	return fmt.Sprintf("[%s] %s (%s/%s) - Priority: %d",
		task.ID, task.Title, task.State, task.Phase, task.Priority)
}
