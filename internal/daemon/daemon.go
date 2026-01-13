package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/cuken/overseer/internal/agent"
	"github.com/cuken/overseer/internal/config"
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
}

// SetVerbose enables verbose logging
func (d *Daemon) SetVerbose(v bool) {
	d.verbose = v
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

	tasksDir := filepath.Join(projectDir, cfg.Paths.Tasks)
	store := task.NewStore(tasksDir)
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
	}, nil
}

// Run starts the daemon
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file
	if err := d.writePIDFile(); err != nil {
		log.Printf("[Daemon] Warning: failed to write PID file: %v", err)
	}
	defer d.removePIDFile()

	// Setup signal handling
	ctx = d.signals.Setup(ctx)
	defer d.signals.Stop()

	log.Printf("[Daemon] Starting in %s", d.projectDir)
	log.Printf("[Daemon] Config: llama=%s, workers=%d",
		d.cfg.Llama.ServerURL, d.cfg.Workers.Count)

	// Check LLM server connection
	log.Printf("[Daemon] Checking LLM server at %s...", d.cfg.Llama.ServerURL)
	// Create a temporary client just for the health check
	// We can't use the worker's client here since workers aren't started yet
	healthClient := agent.NewLlamaClient(d.cfg.Llama)
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := healthClient.Health(healthCtx); err != nil {
		log.Printf("[Daemon] WARNING: LLM server check failed: %v", err)
		log.Printf("[Daemon] Ensure llama.cpp server is running and accessible at %s", d.cfg.Llama.ServerURL)
	} else {
		log.Printf("[Daemon] LLM server connected successfully")
	}
	cancel()

	// Connect to MCP servers
	log.Printf("[Daemon] Connecting to MCP servers...")
	if err := d.mcpClient.Connect(ctx, d.cfg.MCP.Servers); err != nil {
		log.Printf("[Daemon] Warning: MCP connection failed: %v", err)
	}
	defer d.mcpClient.Close()

	// Log connected MCP servers
	status := d.mcpClient.ServerStatus()
	for name, connected := range status {
		if connected {
			log.Printf("[Daemon] MCP server connected: %s", name)
		}
	}

	// Load existing pending tasks
	log.Printf("[Daemon] Loading pending tasks...")
	if err := d.queue.LoadPending(); err != nil {
		log.Printf("[Daemon] Warning: failed to load pending tasks: %v", err)
	}

	// Load existing active tasks (to resume them)
	log.Printf("[Daemon] Resuming active tasks...")
	activeTasks, err := d.store.ListActive()
	if err != nil {
		log.Printf("[Daemon] Warning: failed to load active tasks: %v", err)
	} else {
		for _, t := range activeTasks {
			log.Printf("[Daemon] Resuming active task: %s", t.ID)
			d.queue.Enqueue(t)
		}
	}

	log.Printf("[Daemon] Queue has %d tasks", d.queue.Len())

	// Start file watcher
	log.Printf("[Daemon] Starting file watcher on %s", d.cfg.Paths.Requests)
	d.watcher.Start(ctx)
	defer d.watcher.Stop()

	// Create and start worker pool
	d.pool = worker.NewPool(d.projectDir, d.cfg, d.store, d.queue, d.mcpClient)
	d.pool.SetVerbose(d.verbose)
	d.pool.Start(ctx)
	defer d.pool.Stop()

	log.Printf("[Daemon] Ready. Watching for tasks...")

	// Main loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Daemon] Shutting down...")
			return nil

		case newTask := <-d.watcher.NewTasks():
			log.Printf("[Daemon] New task created: %s - %s",
				newTask.ID[:8], newTask.Title)

		case err := <-d.watcher.Errors():
			log.Printf("[Daemon] Watcher error: %v", err)

		case <-ticker.C:
			d.printStatus()
		}
	}
}

func (d *Daemon) printStatus() {
	pending, _ := d.store.ListPending()
	active, _ := d.store.ListActive()
	review, _ := d.store.ListReview()

	log.Printf("[Daemon] Status: %d pending, %d active, %d review, queue=%d",
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
