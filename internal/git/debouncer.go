package git

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cuken/overseer/internal/logger"
)

// Debouncer manages delayed git commits to prevent spam
type Debouncer struct {
	gitClient *Git
	interval  time.Duration
	branch    string
	message   string
	autoPush  bool

	mu     sync.Mutex
	timer  *time.Timer
	dirty  bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	log    *logger.Logger
}

// NewDebouncer creates a new git operation debouncer
func NewDebouncer(gitClient *Git, interval time.Duration, branch string, autoPush bool, log *logger.Logger) *Debouncer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Debouncer{
		gitClient: gitClient,
		interval:  interval,
		branch:    branch,
		autoPush:  autoPush,
		ctx:       ctx,
		cancel:    cancel,
		log:       log,
	}
}

// MarkDirty signals that changes have been made and schedules a commit
func (d *Debouncer) MarkDirty(commitMessage string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.message = commitMessage
	d.dirty = true

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.interval, func() {
		d.Flush()
	})
}

// Flush forces an immediate commit of pending changes
func (d *Debouncer) Flush() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.dirty {
		return nil
	}

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}

	d.log.Debug("Flushing git changes for branch %s", d.branch)

	// Verify we are on the right branch
	current, err := d.gitClient.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	if current != d.branch {
		return fmt.Errorf("wrong branch: expected %s, got %s", d.branch, current)
	}

	// Check if there are actual changes
	hasChanges, err := d.gitClient.HasChanges()
	if err != nil {
		return fmt.Errorf("failed to check changes: %w", err)
	}

	if !hasChanges {
		d.dirty = false
		return nil
	}

	// Add all changes
	if err := d.gitClient.AddAll(); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	// Commit
	msg := d.message
	if msg == "" {
		msg = "Auto-save: Work in progress"
	}
	if err := d.gitClient.Commit(msg); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Push if enabled
	// Push if enabled
	if d.autoPush && d.gitClient.HasRemote() {
		if err := d.gitClient.Push(); err != nil {
			// Try setting upstream if push fails
			if err := d.gitClient.PushSetUpstream(d.branch); err != nil {
				d.log.Warn("Failed to push branch %s: %v", d.branch, err)
			}
		}
	}

	d.dirty = false
	d.message = ""
	d.log.Success("Debounced commit successful")
	return nil
}

// Stop stops the debouncer and performs a final flush
func (d *Debouncer) Stop() error {
	d.cancel()
	return d.Flush()
}
