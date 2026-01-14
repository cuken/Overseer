package sqlite

import (
	"context"
	"fmt"

	"github.com/cuken/overseer/pkg/types"
)

// Import bulk inserts/updates tasks from a slice
func (s *SQLiteStore) Import(ctx context.Context, tasks []*types.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `
	INSERT INTO tasks (
		id, title, description, branch, state, phase, priority,
		requires_approval, dependencies, merge_target, created_at,
		updated_at, handoffs, conflict_files, parent_task_id, content_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		description=excluded.description,
		branch=excluded.branch,
		state=excluded.state,
		phase=excluded.phase,
		priority=excluded.priority,
		requires_approval=excluded.requires_approval,
		dependencies=excluded.dependencies,
		merge_target=excluded.merge_target,
		created_at=excluded.created_at,
		updated_at=excluded.updated_at,
		handoffs=excluded.handoffs,
		conflict_files=excluded.conflict_files,
		parent_task_id=excluded.parent_task_id,
		content_hash=excluded.content_hash`

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, t := range tasks {
		_, err := stmt.ExecContext(ctx,
			t.ID, t.Title, t.Description, t.Branch, t.State, t.Phase, t.Priority,
			t.RequiresApproval, jsonString(t.Dependencies), t.MergeTarget, t.CreatedAt,
			t.UpdatedAt, t.Handoffs, jsonString(t.ConflictFiles), t.ParentTaskID, t.ContentHash,
		)
		if err != nil {
			return fmt.Errorf("failed to upsert task %s: %w", t.ID, err)
		}
	}

	return tx.Commit()
}

// Export returns all tasks for serialization
func (s *SQLiteStore) Export(ctx context.Context) ([]*types.Task, error) {
	return s.ListAllTasks(ctx)
}
