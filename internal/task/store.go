package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	db         *sqlite.SQLiteStore
	jsonlPath  string
	mu         sync.RWMutex
	dirty      bool
	flushTimer *time.Timer
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
	s.Flush() // Ensure final changes are persisted
	return s.db.Close()
}

// Flush immediately persists any pending changes to JSONL
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	s.dirty = false
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}

	ctx := context.Background()
	tasks, err := s.db.Export(ctx)
	if err != nil {
		return err
	}

	return jsonl.Write(s.jsonlPath, tasks)
}

func (s *Store) scheduleFlushLocked() {
	s.dirty = true
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}

	s.flushTimer = time.AfterFunc(5*time.Second, func() {
		s.Flush()
	})
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

// CalculateContentHash computes a stable hash of the task's content
func CalculateContentHash(t *types.Task) string {
	h := sha256.New()

	// Hash critical fields that define the task content
	h.Write([]byte(t.Title))
	h.Write([]byte(t.Description))
	h.Write([]byte(t.MergeTarget))
	h.Write([]byte(t.ParentTaskID))

	for _, dep := range t.Dependencies {
		h.Write([]byte(dep))
	}

	// Note: We deliberately exclude dynamic state fields like:
	// - State, Phase, Handoffs, RequiresApproval, UpdatedAt
	// - Branch (generated), ID (generated)
	// - ConflictFiles (runtime state)

	return hex.EncodeToString(h.Sum(nil))
}

// Save persists a task and syncs to JSONL
func (s *Store) Save(task *types.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Calculate new hash
	newHash := CalculateContentHash(task)
	task.ContentHash = newHash

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

	s.scheduleFlushLocked()
	return nil
}

// Load reads a task by ID
func (s *Store) Load(id string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	return s.db.GetTask(ctx, id)
}

// LoadByPrefix loads a task by ID prefix
func (s *Store) LoadByPrefix(prefix string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	matches, err := s.db.GetTaskByPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no task found with prefix: %s", prefix)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous prefix %s: multiple matches found", prefix)
	}

	return matches[0], nil
}

// Delete removes a task
func (s *Store) Delete(task *types.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	if err := s.db.DeleteTask(ctx, task.ID); err != nil {
		return err
	}
	s.scheduleFlushLocked()
	return nil
}

// Move transitions a task (State change is just an update in SQLite)
func (s *Store) Move(task *types.Task, oldState, newState types.TaskState) error {
	task.State = newState
	return s.Save(task)
}

// ListByState returns all tasks in a given state
func (s *Store) ListByState(state types.TaskState) ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	return s.db.ListTasks(ctx, state)
}

// ListAll returns all tasks
func (s *Store) ListAll() ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	return s.db.ListAllTasks(ctx)
}

// ListActive returns all tasks that are currently being worked on
func (s *Store) ListActive() ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	return s.db.ListActiveTasks(ctx)
}

// ListPending returns all pending tasks
func (s *Store) ListPending() ([]*types.Task, error) {
	return s.ListByState(types.StatePending)
}

// ListReview returns all tasks awaiting human review
func (s *Store) ListReview() ([]*types.Task, error) {
	return s.ListByState(types.StateReview)
}

// Worker operations

func (s *Store) UpdateWorkerStatus(status *types.WorkerStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	return s.db.UpdateWorkerStatus(ctx, status)
}

func (s *Store) ListWorkers() ([]*types.WorkerStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	return s.db.ListWorkers(ctx)
}

func (s *Store) PruneStaleWorkers(threshold time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	return s.db.PruneStaleWorkers(ctx, threshold)
}
