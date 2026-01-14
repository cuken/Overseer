package storage

import (
	"context"

	"github.com/cuken/overseer/pkg/types"
)

// Store defines the interface for task storage
type Store interface {
	// Task operations
	CreateTask(ctx context.Context, task *types.Task) error
	GetTask(ctx context.Context, id string) (*types.Task, error)
	UpdateTask(ctx context.Context, task *types.Task) error
	DeleteTask(ctx context.Context, id string) error
	ListTasks(ctx context.Context, state types.TaskState) ([]*types.Task, error)
	ListAllTasks(ctx context.Context) ([]*types.Task, error)

	// Persistence
	Close() error
}
