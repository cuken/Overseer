package task

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuken/overseer/internal/storage/jsonl"
	"github.com/cuken/overseer/pkg/types"
)

func TestStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	task := &types.Task{
		ID:        "test-1",
		Title:     "Test Task",
		State:     types.StatePending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := store.Save(task); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load("test-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Title != task.Title {
		t.Errorf("Expected title %s, got %s", task.Title, loaded.Title)
	}
}

func TestStore_Sync(t *testing.T) {
	dir := t.TempDir()

	// Create a JSONL file first
	task := &types.Task{
		ID:        "sync-1",
		Title:     "Synced Task",
		State:     types.StatePending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	jsonlPath := filepath.Join(dir, "tasks.jsonl")
	if err := jsonl.Write(jsonlPath, []*types.Task{task}); err != nil {
		t.Fatalf("Failed to write initial JSONL: %v", err)
	}

	// Create store, should import from JSONL
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	loaded, err := store.Load("sync-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Title != "Synced Task" {
		t.Errorf("Sync failed, title mismatch")
	}

	// Now update in store, check JSONL
	loaded.State = types.StateImplementing
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read JSONL directly
	tasks, err := jsonl.Read(jsonlPath)
	if err != nil {
		t.Fatalf("Failed to read JSONL: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task in JSONL, got %d", len(tasks))
	}
	if tasks[0].State != types.StateImplementing {
		t.Errorf("JSONL not updated, state is %s", tasks[0].State)
	}
}
