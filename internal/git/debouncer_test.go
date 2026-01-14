package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuken/overseer/internal/logger"
)

func TestDebouncer(t *testing.T) {
	// Setup git repo
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	execGit(t, dir, "config", "user.email", "test@example.com")
	execGit(t, dir, "config", "user.name", "Test User")

	// Create initial commit and ignore logs
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("logs/\n"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("Initial"), 0644)
	g.AddAll()
	g.Commit("root")

	// Create feature branch
	branch := "feature/test"
	if err := g.CheckoutNew(branch); err != nil {
		t.Fatalf("CheckoutNew failed: %v", err)
	}

	logsDir := filepath.Join(dir, "logs")
	os.Mkdir(logsDir, 0755)
	log := logger.New("Test", logsDir)

	// Test: Fast inputs should be debounced
	debouncer := NewDebouncer(g, 100*time.Millisecond, branch, false, log)

	// Make a change
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644)
	debouncer.MarkDirty("update 1")

	// Verify no commit immediately
	if !hasUncommittedChanges(t, g) {
		t.Error("Expected uncommitted changes given immediate check")
	}

	// Make another change quickly
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content2"), 0644)
	debouncer.MarkDirty("update 2")

	// Wait for debounce
	time.Sleep(200 * time.Millisecond)

	// Verify committed
	if hasUncommittedChanges(t, g) {
		status, _ := g.Status()
		t.Errorf("Expected changes to be committed after debounce. Status:\n%s", status)
	}

	logMsg, _ := g.Log(1)
	if logMsg == "" {
		t.Error("Expected commit log")
	}

	// Test: Flush
	os.WriteFile(filepath.Join(dir, "file3.txt"), []byte("content3"), 0644)
	debouncer.MarkDirty("update 3")
	debouncer.Flush()

	if hasUncommittedChanges(t, g) {
		t.Error("Expected changes to be committed after Flush")
	}
}

func hasUncommittedChanges(t *testing.T, g *Git) bool {
	clean, err := g.HasChanges()
	if err != nil {
		t.Fatalf("HasChanges failed: %v", err)
	}
	return clean
}

func execGit(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
}
