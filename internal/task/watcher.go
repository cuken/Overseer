package task

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cuken/overseer/pkg/types"
	"github.com/fsnotify/fsnotify"
)

// Watcher monitors the requests directory for new task files
type Watcher struct {
	requestsDir string
	store       *Store
	queue       *Queue
	fsWatcher   *fsnotify.Watcher
	newTasks    chan *types.Task
	errors      chan error
}

// NewWatcher creates a new request watcher
func NewWatcher(requestsDir string, store *Store, queue *Queue) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	if err := os.MkdirAll(requestsDir, 0755); err != nil {
		fsWatcher.Close()
		return nil, err
	}

	if err := fsWatcher.Add(requestsDir); err != nil {
		fsWatcher.Close()
		return nil, err
	}

	return &Watcher{
		requestsDir: requestsDir,
		store:       store,
		queue:       queue,
		fsWatcher:   fsWatcher,
		newTasks:    make(chan *types.Task, 10),
		errors:      make(chan error, 10),
	}, nil
}

// Start begins watching for new request files
func (w *Watcher) Start(ctx context.Context) {
	// First, process any existing files
	w.processExistingFiles()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.fsWatcher.Events:
				if !ok {
					return
				}
				w.handleEvent(event)
			case err, ok := <-w.fsWatcher.Errors:
				if !ok {
					return
				}
				select {
				case w.errors <- err:
				default:
				}
			}
		}
	}()
}

// Stop stops the watcher
func (w *Watcher) Stop() error {
	return w.fsWatcher.Close()
}

// NewTasks returns a channel of newly created tasks
func (w *Watcher) NewTasks() <-chan *types.Task {
	return w.newTasks
}

// Errors returns a channel of watcher errors
func (w *Watcher) Errors() <-chan error {
	return w.errors
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Only process new/modified markdown files
	if !strings.HasSuffix(event.Name, ".md") {
		return
	}

	if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
		// Small delay to ensure file is fully written
		time.Sleep(100 * time.Millisecond)
		w.processFile(event.Name)
	}
}

func (w *Watcher) processExistingFiles() {
	entries, err := os.ReadDir(w.requestsDir)
	if err != nil {
		log.Printf("Error reading requests directory: %v", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			w.processFile(filepath.Join(w.requestsDir, entry.Name()))
		}
	}
}

func (w *Watcher) processFile(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Error reading request file %s: %v", path, err)
		return
	}

	title, description := ParseRequestFile(string(content))
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), ".md")
	}

	task := NewTask(title, description)

	// Save to pending
	if err := w.store.Save(task); err != nil {
		log.Printf("Error saving task: %v", err)
		return
	}

	// Add to queue
	w.queue.Enqueue(task)

	// Move processed file to archive (or delete)
	archiveDir := filepath.Join(filepath.Dir(w.requestsDir), "requests_processed")
	if err := os.MkdirAll(archiveDir, 0755); err == nil {
		archivePath := filepath.Join(archiveDir, filepath.Base(path))
		os.Rename(path, archivePath)
	} else {
		os.Remove(path)
	}

	// Notify listeners
	select {
	case w.newTasks <- task:
	default:
	}

	log.Printf("Created task from request: %s", Summary(task))
}
