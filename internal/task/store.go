package task

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/cuken/overseer/internal/storage/jsonl"
	"github.com/cuken/overseer/internal/storage/sqlite"
	"github.com/cuken/overseer/pkg/types"
)

// Store manages task persistence using SQLite and JSONL
type Store struct {
	db        *sqlite.SQLiteStore
	jsonlPath string
	mu        sync.RWMutex
}

// NewStore creates a new task store and synchronizes with JSONL
func NewStore(tasksDir string) (*Store, error) {
	dbPath := filepath.Join(tasksDir, "tasks.db")
	jsonlPath := filepath.Join(tasksDir, "tasks.jsonl")

	db, err := sqlite.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	s := &Store{
		db:        db,
		jsonlPath: jsonlPath,
	}

	// Initial sync: Import JSONL to DB
	if err := s.syncFromJSONL(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to sync from JSONL: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) syncFromJSONL() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := jsonl.Read(s.jsonlPath)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}

	ctx := context.Background()
	return s.db.Import(ctx, tasks)
}

func (s *Store) syncToJSONL() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tasks, err := s.db.Export(ctx)
	if err != nil {
		return err
	}

	return jsonl.Write(s.jsonlPath, tasks)
}

// Save persists a task and syncs to JSONL
func (s *Store) Save(task *types.Task) error {
	// Update timestamp
	task.UpdatedAt = time.Now()

	ctx := context.Background()

	// Check if exists
	existing, err := s.db.GetTask(ctx, task.ID)
	if err != nil && err.Error() != "sql: no rows in result set" {
		// Ignore not found error, treat as new
	}

	if existing == nil {
		if err := s.db.CreateTask(ctx, task); err != nil {
			return err
		}
	} else {
		if err := s.db.UpdateTask(ctx, task); err != nil {
			return err
		}
	}

	return s.syncToJSONL()
}

// Load reads a task by ID
func (s *Store) Load(id string) (*types.Task, error) {
	ctx := context.Background()
	return s.db.GetTask(ctx, id)
}

// LoadByPrefix loads a task by ID prefix
func (s *Store) LoadByPrefix(prefix string) (*types.Task, error) {
	// inefficient but simple: list all and filter.
	// Optimally, SQLite 'LIKE' query.
	// But `GetTask` is by ID.
	// Let's list all active/pending/etc which covers most.
	// Actually, just query DB.

	// Add ListByPrefix to SQLiteStore?
	// For now, load all is safer if I don't want to change SQLiteStore struct in this file.
	// But I defined SQLiteStore in internal/storage/sqlite.

	ctx := context.Background()
	all, err := s.db.ListAllTasks(ctx)
	if err != nil {
		return nil, err
	}

	var matches []*types.Task
	for _, t := range all {
		if len(t.ID) >= len(prefix) && t.ID[:len(prefix)] == prefix {
			matches = append(matches, t)
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

// Delete removes a task
func (s *Store) Delete(task *types.Task) error {
	ctx := context.Background()
	if err := s.db.DeleteTask(ctx, task.ID); err != nil {
		return err
	}
	return s.syncToJSONL()
}

// Move transitions a task (State change is just an update in SQLite)
func (s *Store) Move(task *types.Task, oldState, newState types.TaskState) error {
	task.State = newState
	return s.Save(task)
}

// ListByState returns all tasks in a given state
func (s *Store) ListByState(state types.TaskState) ([]*types.Task, error) {
	ctx := context.Background()
	return s.db.ListTasks(ctx, state)
}

// ListAll returns all tasks
func (s *Store) ListAll() ([]*types.Task, error) {
	ctx := context.Background()
	return s.db.ListAllTasks(ctx)
}

// ListActive returns all tasks that are currently being worked on
func (s *Store) ListActive() ([]*types.Task, error) {
	ctx := context.Background()
	// In file store, "active" was a directory containing generic active states.
	// In types.go: StatePlanning, StateImplementing, etc. are active.
	// We need to query for all active states.

	// Helper to fetch all and filter
	all, err := s.db.ListAllTasks(ctx)
	if err != nil {
		return nil, err
	}

	var active []*types.Task
	for _, t := range all {
		if t.State.IsActive() {
			active = append(active, t)
		}
	}
	return active, nil
}

// ListPending returns all pending tasks
func (s *Store) ListPending() ([]*types.Task, error) {
	return s.ListByState(types.StatePending)
}

// ListReview returns all tasks awaiting human review
func (s *Store) ListReview() ([]*types.Task, error) {
	return s.ListByState(types.StateReview)
}
