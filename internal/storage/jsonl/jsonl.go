package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cuken/overseer/pkg/types"
)

// Read loads tasks from a JSONL file
func Read(path string) ([]*types.Task, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []*types.Task{}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var tasks []*types.Task
	scanner := bufio.NewScanner(file)
	// Increase buffer size for large lines if needed
	const maxCapacity = 1024 * 1024 // 1MB per line
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var task types.Task
		if err := json.Unmarshal(line, &task); err != nil {
			return nil, fmt.Errorf("failed to parse line %d: %w", lineNum, err)
		}
		tasks = append(tasks, &task)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return tasks, nil
}

// Write dumps tasks to a JSONL file
func Write(path string, tasks []*types.Task) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temp file first
	tmpPath := path + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)

	for _, task := range tasks {
		if err := encoder.Encode(task); err != nil {
			file.Close()
			return fmt.Errorf("failed to encode task %s: %w", task.ID, err)
		}
	}

	if err := writer.Flush(); err != nil {
		file.Close()
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
