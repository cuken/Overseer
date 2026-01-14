package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuken/overseer/internal/config"
	"github.com/cuken/overseer/pkg/types"
	"github.com/spf13/cobra"
)

func executeCommand(root *cobra.Command, args ...string) (string, error) {
	root.SetArgs(args)

	// Reset flags between runs (important for persistent flags)
	jsonOutput = false
	if f := root.PersistentFlags().Lookup("json"); f != nil {
		f.Changed = false
		f.Value.Set("false")
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := root.Execute()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String(), err
}

func TestJSONOutput(t *testing.T) {
	// Create temp directory for project
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tmpDir)

	// Test Init with JSON
	output, err := executeCommand(rootCmd, "init", "--json")
	if err != nil {
		t.Fatalf("init command failed: %v\nOutput: %s", err, output)
	}

	var initResp InitResponse
	if err := json.Unmarshal([]byte(output), &initResp); err != nil {
		t.Errorf("Failed to parse init JSON: %v. Output:\n%s", err, output)
	}
	if initResp.Message == "" {
		t.Error("Expected message in init response")
	}

	// Create a dummy task file
	taskFile := filepath.Join(tmpDir, "task.md")
	os.WriteFile(taskFile, []byte("Title: Test Task\n\nDescription."), 0644)

	// Test Add with JSON
	output, err = executeCommand(rootCmd, "add", "task.md", "--json")
	if err != nil {
		t.Fatalf("add command failed: %v\nOutput: %s", err, output)
	}

	var addResp AddResponse
	if err := json.Unmarshal([]byte(output), &addResp); err != nil {
		t.Errorf("Failed to parse add JSON: %v. Output:\n%s", err, output)
	}
	if addResp.Filename != "task.md" {
		t.Errorf("Expected filename task.md, got %s", addResp.Filename)
	}

	// Test List with JSON
	output, err = executeCommand(rootCmd, "list", "--json")
	if err != nil {
		t.Fatalf("list command failed: %v\nOutput: %s", err, output)
	}

	var listResp ListResponse
	if err := json.Unmarshal([]byte(output), &listResp); err != nil {
		t.Errorf("Failed to parse list JSON: %v. Output:\n%s", err, output)
	}
	// We expect pending tasks to contain our new task
	// Wait, daemon is not running, so it stays in valid requests? The 'add' command moves it to requests.
	// The store only lists tasks in tasks directory. 'add' puts it in 'requests'.
	// So list might be empty for Active/Pending if daemon hasn't processed it.
	// But ListResponse should parse correctly.

	// Test Status with JSON (empty)
	output, err = executeCommand(rootCmd, "status", "--json")
	if err != nil {
		t.Fatalf("status command failed: %v", err)
	}
	var statusResp StatusResponse
	if err := json.Unmarshal([]byte(output), &statusResp); err != nil {
		t.Errorf("Failed to parse status JSON: %v", err)
	}

	// Test Clean with JSON
	// We need to fake a task in the store to clean it, or just clean all (which finds nothing)
	output, err = executeCommand(rootCmd, "clean", "--force", "--json")
	if err != nil {
		t.Fatalf("clean command failed: %v", err)
	}
	var cleanResp CleanResponse
	if err := json.Unmarshal([]byte(output), &cleanResp); err != nil {
		t.Errorf("Failed to parse clean JSON: %v", err)
	}
}

// Ensure the config setup mock works if needed
func setupConfig(t *testing.T, dir string) {
	cfg := types.DefaultConfig()
	config.EnsureDirectories(dir, cfg)
}
