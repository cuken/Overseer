package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/cuken/overseer/pkg/types"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type SQLiteStore struct {
	db *sql.DB
}

func New(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set pragmas for performance/safety
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set synchronous mode: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		title TEXT,
		description TEXT,
		branch TEXT,
		state TEXT,
		phase TEXT,
		priority INTEGER,
		requires_approval BOOLEAN,
		dependencies TEXT,
		merge_target TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		handoffs INTEGER,
		conflict_files TEXT,
		parent_task_id TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks(state);
	CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_task_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Helper to serialize JSON fields
func jsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Helper to deserialize JSON fields
func fromJSON(data string, v interface{}) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), v)
}

func (s *SQLiteStore) CreateTask(ctx context.Context, t *types.Task) error {
	query := `
	INSERT INTO tasks (
		id, title, description, branch, state, phase, priority,
		requires_approval, dependencies, merge_target, created_at,
		updated_at, handoffs, conflict_files, parent_task_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query,
		t.ID, t.Title, t.Description, t.Branch, t.State, t.Phase, t.Priority,
		t.RequiresApproval, jsonString(t.Dependencies), t.MergeTarget, t.CreatedAt,
		t.UpdatedAt, t.Handoffs, jsonString(t.ConflictFiles), t.ParentTaskID,
	)
	return err
}

func (s *SQLiteStore) GetTask(ctx context.Context, id string) (*types.Task, error) {
	query := `SELECT * FROM tasks WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)
	return s.scanTask(row)
}

func (s *SQLiteStore) UpdateTask(ctx context.Context, t *types.Task) error {
	query := `
	UPDATE tasks SET
		title=?, description=?, branch=?, state=?, phase=?, priority=?,
		requires_approval=?, dependencies=?, merge_target=?, created_at=?,
		updated_at=?, handoffs=?, conflict_files=?, parent_task_id=?
	WHERE id=?`

	_, err := s.db.ExecContext(ctx, query,
		t.Title, t.Description, t.Branch, t.State, t.Phase, t.Priority,
		t.RequiresApproval, jsonString(t.Dependencies), t.MergeTarget, t.CreatedAt,
		t.UpdatedAt, t.Handoffs, jsonString(t.ConflictFiles), t.ParentTaskID,
		t.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteTask(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) ListTasks(ctx context.Context, state types.TaskState) ([]*types.Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT * FROM tasks WHERE state = ?", state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*types.Task
	for rows.Next() {
		t, err := s.scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *SQLiteStore) ListAllTasks(ctx context.Context) ([]*types.Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT * FROM tasks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*types.Task
	for rows.Next() {
		t, err := s.scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// Scannable interface to handle Row and Rows
type scannable interface {
	Scan(dest ...interface{}) error
}

func (s *SQLiteStore) scanTask(row scannable) (*types.Task, error) {
	var t types.Task
	var deps, conflicts string

	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.Branch, &t.State, &t.Phase,
		&t.Priority, &t.RequiresApproval, &deps, &t.MergeTarget,
		&t.CreatedAt, &t.UpdatedAt, &t.Handoffs, &conflicts, &t.ParentTaskID,
	)
	if err != nil {
		return nil, err
	}

	if err := fromJSON(deps, &t.Dependencies); err != nil {
		return nil, fmt.Errorf("failed to parse dependencies: %w", err)
	}
	if err := fromJSON(conflicts, &t.ConflictFiles); err != nil {
		return nil, fmt.Errorf("failed to parse conflict_files: %w", err)
	}

	return &t, nil
}
