package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cuken/overseer/pkg/types"
	"gopkg.in/yaml.v3"
)

// Store manages task persistence using YAML files
type Store struct {
	baseDir string
	mu      sync.RWMutex
}

// NewStore creates a new task store
func NewStore(tasksDir string) *Store {
	return &Store{
		baseDir: tasksDir,
	}
}

// Save persists a task to the appropriate state directory
func (s *Store) Save(task *types.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.dirForState(task.State)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	path := filepath.Join(dir, task.ID+".yaml")
	data, err := yaml.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write task file: %w", err)
	}

	return nil
}

// Load reads a task by ID, searching all state directories
func (s *Store) Load(id string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := []string{"active", "pending", "review", "completed"}
	for _, state := range states {
		path := filepath.Join(s.baseDir, state, id+".yaml")
		if _, err := os.Stat(path); err == nil {
			return s.loadFromPath(path)
		}
	}

	return nil, fmt.Errorf("task not found: %s", id)
}

// LoadByPrefix loads a task by ID prefix (for CLI convenience)
func (s *Store) LoadByPrefix(prefix string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []*types.Task
	states := []string{"active", "pending", "review", "completed"}

	for _, state := range states {
		dir := filepath.Join(s.baseDir, state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".yaml") {
				task, err := s.loadFromPath(filepath.Join(dir, entry.Name()))
				if err == nil {
					matches = append(matches, task)
				}
			}
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no task found with prefix: %s", prefix)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous prefix %s: matches %d tasks", prefix, len(matches))
	}

	return matches[0], nil
}

// Delete removes a task file
func (s *Store) Delete(task *types.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Search all directories for the task
	states := []string{"active", "pending", "review", "completed"}
	for _, state := range states {
		path := filepath.Join(s.baseDir, state, task.ID+".yaml")
		if _, err := os.Stat(path); err == nil {
			return os.Remove(path)
		}
	}

	return nil
}

// Move transitions a task to a new state directory
func (s *Store) Move(task *types.Task, oldState, newState types.TaskState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldDir := s.dirForState(oldState)
	newDir := s.dirForState(newState)

	oldPath := filepath.Join(oldDir, task.ID+".yaml")
	newPath := filepath.Join(newDir, task.ID+".yaml")

	// Ensure new directory exists
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", newDir, err)
	}

	// Write to new location first
	data, err := yaml.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	if err := os.WriteFile(newPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write task file: %w", err)
	}

	// Remove from old location
	os.Remove(oldPath)

	return nil
}

// ListByState returns all tasks in a given state
func (s *Store) ListByState(state types.TaskState) ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.dirForState(state)
	return s.loadFromDir(dir)
}

// ListAll returns all tasks from all state directories
func (s *Store) ListAll() ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var allTasks []*types.Task
	states := []string{"active", "pending", "review", "completed"}

	for _, state := range states {
		dir := filepath.Join(s.baseDir, state)
		tasks, err := s.loadFromDir(dir)
		if err != nil {
			continue
		}
		allTasks = append(allTasks, tasks...)
	}

	return allTasks, nil
}

// ListActive returns all tasks that are currently being worked on
func (s *Store) ListActive() ([]*types.Task, error) {
	tasks, err := s.loadFromDir(filepath.Join(s.baseDir, "active"))
	if err != nil {
		return nil, err
	}

	// Filter for actually active states
	var active []*types.Task
	for _, t := range tasks {
		if t.State.IsActive() {
			active = append(active, t)
		}
	}

	return active, nil
}

// ListPending returns all pending tasks
func (s *Store) ListPending() ([]*types.Task, error) {
	return s.loadFromDir(filepath.Join(s.baseDir, "pending"))
}

// ListReview returns all tasks awaiting human review
func (s *Store) ListReview() ([]*types.Task, error) {
	return s.loadFromDir(filepath.Join(s.baseDir, "review"))
}

func (s *Store) loadFromPath(path string) (*types.Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read task file: %w", err)
	}

	var task types.Task
	if err := yaml.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	return &task, nil
}

func (s *Store) loadFromDir(dir string) ([]*types.Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var tasks []*types.Task
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			task, err := s.loadFromPath(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}

func (s *Store) dirForState(state types.TaskState) string {
	switch state {
	case types.StateReview:
		return filepath.Join(s.baseDir, "review")
	case types.StateCompleted:
		return filepath.Join(s.baseDir, "completed")
	case types.StatePending:
		return filepath.Join(s.baseDir, "pending")
	default:
		return filepath.Join(s.baseDir, "active")
	}
}
