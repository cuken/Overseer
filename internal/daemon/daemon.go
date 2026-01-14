package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cuken/overseer/internal/agent"
	"github.com/cuken/overseer/internal/config"
	"github.com/cuken/overseer/internal/logger"
	"github.com/cuken/overseer/internal/mcp"
	"github.com/cuken/overseer/internal/task"
	"github.com/cuken/overseer/internal/worker"
	"github.com/cuken/overseer/pkg/types"
)

// Daemon is the main overseer daemon
type Daemon struct {
	projectDir string
	cfg        *types.Config
	store      *task.Store
	queue      *task.Queue
	watcher    *task.Watcher
	pool       *worker.Pool
	mcpClient  *mcp.Client
	signals    *SignalHandler
	pidFile    string
	verbose    bool
	log        *logger.Logger
}

// SetVerbose enables verbose logging
func (d *Daemon) SetVerbose(v bool) {
	d.verbose = v
	if d.log != nil {
		d.log.SetVerbose(v)
	}
}

// New creates a new daemon instance
func New(projectDir string) (*Daemon, error) {
	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirectories(projectDir, cfg); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	// Initialize logging
	logsDir := filepath.Join(projectDir, cfg.Paths.Logs)
	if err := logger.Setup(logsDir, false); err != nil {
		return nil, fmt.Errorf("failed to setup logging: %w", err)
	}

	log := logger.New("Daemon", logsDir)

	tasksDir := filepath.Join(projectDir, cfg.Paths.Tasks)
	store, err := task.NewStore(tasksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}
	queue := task.NewQueue(store)

	requestsDir := filepath.Join(projectDir, cfg.Paths.Requests)
	watcher, err := task.NewWatcher(requestsDir, store, queue)
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	return &Daemon{
		projectDir: projectDir,
		cfg:        cfg,
		store:      store,
		queue:      queue,
		watcher:    watcher,
		mcpClient:  mcp.NewClient(),
		signals:    NewSignalHandler(),
		pidFile:    filepath.Join(projectDir, ".overseer", "daemon.pid"),
		log:        log,
	}, nil
}

// Run starts the daemon
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file
	if err := d.writePIDFile(); err != nil {
		d.log.Warn("Failed to write PID file: %v", err)
	}
	defer d.removePIDFile()

	// Setup signal handling
	ctx = d.signals.Setup(ctx)
	defer d.signals.Stop()

	d.log.Info("Starting in %s", d.projectDir)
	d.log.Info("Config: llama=%s, workers=%d",
		d.cfg.Llama.ServerURL, d.cfg.Workers.Count)

	// Check LLM server connection
	d.log.Info("Checking LLM server at %s...", d.cfg.Llama.ServerURL)
	// Create a temporary client just for the health check
	// We can't use the worker's client here since workers aren't started yet
	healthClient := agent.NewLlamaClient(d.cfg.Llama)
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := healthClient.Health(healthCtx); err != nil {
		d.log.Warn("LLM server check failed: %v", err)
		d.log.Warn("Ensure llama.cpp server is running and accessible at %s", d.cfg.Llama.ServerURL)
	} else {
		d.log.Success("LLM server connected successfully")
	}
	cancel()

	// Connect to MCP servers (run from project directory so relative paths work)
	d.log.Info("Connecting to MCP servers...")
	if err := d.mcpClient.Connect(ctx, d.cfg.MCP.Servers, d.projectDir); err != nil {
		d.log.Warn("MCP connection failed: %v", err)
	}
	defer d.mcpClient.Close()

	// Log connected MCP servers
	status := d.mcpClient.ServerStatus()
	for name, connected := range status {
		if connected {
			d.log.Success("MCP server connected: %s", name)
		}
	}

	// Load existing pending tasks
	d.log.Info("Loading pending tasks...")
	if err := d.queue.LoadPending(); err != nil {
		d.log.Warn("Failed to load pending tasks: %v", err)
	}

	// Load existing active tasks (to resume them)
	d.log.Info("Resuming active tasks...")
	activeTasks, err := d.store.ListActive()
	if err != nil {
		d.log.Warn("Failed to load active tasks: %v", err)
	} else {
		for _, t := range activeTasks {
			d.log.Info("Resuming active task: %s", t.ID)
			d.queue.Enqueue(t)
		}
	}

	// Check for blocked tasks that can be cleared
	d.log.Info("Checking gates for blocked tasks...")
	d.checkGates()

	d.log.Info("Queue has %d tasks", d.queue.Len())

	// Start file watcher
	d.log.Info("Starting file watcher on %s", d.cfg.Paths.Requests)
	d.watcher.Start(ctx)
	defer d.watcher.Stop()

	// Create and start worker pool
	logsDir := filepath.Join(d.projectDir, d.cfg.Paths.Logs)
	d.pool = worker.NewPool(d.projectDir, d.cfg, d.store, d.queue, d.mcpClient, logsDir)
	d.pool.SetVerbose(d.verbose)
	d.pool.Start(ctx)
	defer d.pool.Stop()

	d.log.Success("Ready. Watching for tasks...")

	// Main loop
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Info("Shutting down...")
			return nil

		case newTask := <-d.watcher.NewTasks():
			d.log.Success("New task created: %s - %s",
				newTask.ID, newTask.Title)

		case err := <-d.watcher.Errors():
			d.log.Error("Watcher error: %v", err)

		case <-ticker.C:
			d.checkGates()
			d.printStatus()
		}
	}
}

func (d *Daemon) printStatus() {
	pending, _ := d.store.ListPending()
	active, _ := d.store.ListActive()
	review, _ := d.store.ListReview()

	d.log.Debug("Status: %d pending, %d active, %d review, queue=%d",
		len(pending), len(active), len(review), d.queue.Len())
}

func (d *Daemon) writePIDFile() error {
	pid := os.Getpid()
	return os.WriteFile(d.pidFile, []byte(fmt.Sprintf("%d", pid)), 0644)
}

func (d *Daemon) removePIDFile() {
	os.Remove(d.pidFile)
}

// Stop gracefully stops the daemon
func (d *Daemon) Stop() {
	if d.pool != nil {
		d.pool.Stop()
	}
	if d.watcher != nil {
		d.watcher.Stop()
	}
	if d.mcpClient != nil {
		d.mcpClient.Close()
	}
}

func (d *Daemon) checkGates() {
	blocked, err := d.store.ListBlocked()
	if err != nil {
		d.log.Warn("Failed to list blocked tasks for gate check: %v", err)
		return
	}

	for _, t := range blocked {
		if t.Gate == nil {
			continue
		}

		// Check for timeout
		if !t.Gate.Timeout.IsZero() && time.Now().After(t.Gate.Timeout) {
			d.log.Warn("Gate %s for task %s expired", t.Gate.ID, t.ID)
			t.Gate.Status = types.GateStatusExpired
			t.Gate.Message = "Gate timed out"
			// Keep it blocked but mark as expired for human intervention or auto-failure
			d.store.Save(t)
			continue
		}

		// Automatic clearing logic
		shouldClear := false
		switch t.Gate.Type {
		case types.GateTimer:
			// Timer handled by the timeout check above, but if it was just a "wait 5m" gate
			// with no specific timeout message, we clear it when time is up.
			shouldClear = true

		case types.GateGitHubRun:
			// TODO: Implement actual GitHub API check
			// We skip for now until we have GitHub integration
		}

		if shouldClear {
			d.log.Success("Gate %s cleared for task %s", t.Gate.ID, t.ID)
			t.Gate.Status = types.GateStatusCleared
			now := time.Now()
			t.Gate.ClearedAt = &now
			t.State = types.StatePending // Move back to pending to be picked up
			d.store.Save(t)
			d.queue.Enqueue(t)
		}
	}
}
