package worker

import (
	"context"
	"log"
	"sync"

	"github.com/cuken/overseer/internal/mcp"
	"github.com/cuken/overseer/internal/task"
	"github.com/cuken/overseer/pkg/types"
)

// Pool manages a pool of workers
type Pool struct {
	workers    []*Worker
	projectDir string
	cfg        *types.Config
	store      *task.Store
	queue      *task.Queue
	mcpClient  *mcp.Client
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	verbose    bool
}

// SetVerbose enables verbose logging
func (p *Pool) SetVerbose(v bool) {
	p.verbose = v
}

// NewPool creates a new worker pool
func NewPool(projectDir string, cfg *types.Config, store *task.Store, queue *task.Queue, mcpClient *mcp.Client) *Pool {
	return &Pool{
		projectDir: projectDir,
		cfg:        cfg,
		store:      store,
		queue:      queue,
		mcpClient:  mcpClient,
	}
}

// Start launches the worker pool
func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	workerCount := p.cfg.Workers.Count
	if workerCount < 1 {
		workerCount = 1
	}

	log.Printf("[Pool] Starting %d workers", workerCount)

	p.workers = make([]*Worker, workerCount)
	for i := 0; i < workerCount; i++ {
		p.workers[i] = NewWorker(i, p.projectDir, p.cfg, p.store, p.queue, p.mcpClient)
		p.workers[i].SetVerbose(p.verbose)
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.Run(ctx)
		}(p.workers[i])
	}
}

// Stop gracefully stops all workers
func (p *Pool) Stop() {
	log.Printf("[Pool] Stopping workers...")
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	log.Printf("[Pool] All workers stopped")
}

// WorkerCount returns the number of active workers
func (p *Pool) WorkerCount() int {
	return len(p.workers)
}
