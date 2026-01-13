package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cuken/overseer/internal/agent"
	"github.com/cuken/overseer/internal/git"
	"github.com/cuken/overseer/internal/mcp"
	"github.com/cuken/overseer/internal/task"
	"github.com/cuken/overseer/pkg/types"
)

// Worker processes tasks from the queue
type Worker struct {
	id          int
	projectDir  string
	cfg         *types.Config
	store       *task.Store
	queue       *task.Queue
	mcpClient   *mcp.Client
	gitClient   *git.Git
	branchMgr   *git.BranchManager
	merger      *git.Merger
	llamaClient *agent.LlamaClient
	verbose     bool
}

// SetVerbose enables verbose logging
func (w *Worker) SetVerbose(v bool) {
	w.verbose = v
	if w.llamaClient != nil {
		w.llamaClient.SetDebug(v)
	}
}

// NewWorker creates a new worker
func NewWorker(id int, projectDir string, cfg *types.Config, store *task.Store, queue *task.Queue, mcpClient *mcp.Client) *Worker {
	gitClient := git.New(projectDir)
	branchMgr := git.NewBranchManager(gitClient, cfg.Git)
	merger := git.NewMerger(gitClient, branchMgr, cfg.Git)
	llamaClient := agent.NewLlamaClient(cfg.Llama)

	return &Worker{
		id:          id,
		projectDir:  projectDir,
		cfg:         cfg,
		store:       store,
		queue:       queue,
		mcpClient:   mcpClient,
		gitClient:   gitClient,
		branchMgr:   branchMgr,
		merger:      merger,
		llamaClient: llamaClient,
	}
}

// Run starts the worker loop
func (w *Worker) Run(ctx context.Context) {
	log.Printf("[Worker %d] Started", w.id)
	defer log.Printf("[Worker %d] Stopped", w.id)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Get next task from queue
		t := w.queue.Dequeue()
		if t == nil {
			// No tasks available, wait a bit
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		log.Printf("[Worker %d] Processing task %s: %s", w.id, t.ID[:8], t.Title)

		// Process the task
		if err := w.processTask(ctx, t); err != nil {
			log.Printf("[Worker %d] Task %s failed: %v", w.id, t.ID[:8], err)
			// Re-queue if it's a recoverable error
			if t.State != types.StateBlocked && t.State != types.StateCompleted {
				w.queue.Enqueue(t)
			}
		}
	}
}

func (w *Worker) processTask(ctx context.Context, t *types.Task) error {
	// Transition from pending to planning
	if t.State == types.StatePending {
		if err := task.TransitionTo(t, types.StatePlanning); err != nil {
			return err
		}
		if err := w.store.Move(t, types.StatePending, types.StatePlanning); err != nil {
			return err
		}
	}

	// Create branch if needed
	if t.Branch == "" || !w.gitClient.BranchExists(t.Branch) {
		if err := w.branchMgr.CreateTaskBranch(t); err != nil {
			log.Printf("[Worker %d] Failed to create branch: %v", w.id, err)
			// Continue without branch for now
		} else {
			log.Printf("[Worker %d] Created branch %s", w.id, t.Branch)
		}
	} else {
		if err := w.branchMgr.SwitchToTaskBranch(t); err != nil {
			log.Printf("[Worker %d] Failed to switch to branch: %v", w.id, err)
		}
	}

	// Save task state
	if err := w.store.Save(t); err != nil {
		return err
	}

	// Run agent loop
	for t.State.IsActive() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if approval is required
		if t.RequiresApproval {
			log.Printf("[Worker %d] Task %s requires approval", w.id, t.ID[:8])
			if err := task.TransitionTo(t, types.StateReview); err == nil {
				w.store.Move(t, t.State, types.StateReview)
			}
			return nil
		}

		// Run agent
		agentResult := w.runAgent(ctx, t)

		// Handle agent result
		switch agentResult {
		case AgentResultComplete:
			// Agent completed its work, check phase
			if t.Phase == types.PhaseDebug || t.Phase == types.PhaseTest {
				// Move to merging if not already there
				if t.State != types.StateMerging {
					if err := task.TransitionTo(t, types.StateMerging); err == nil {
						w.store.Save(t)
					}
				}

				// If ready to merge, do it
				if t.State == types.StateMerging {
					return w.attemptMerge(ctx, t)
				}
			}
		case AgentResultHandoff:
			// Agent needs handoff, continue with fresh agent
			log.Printf("[Worker %d] Agent handoff for task %s (generation %d)",
				w.id, t.ID[:8], t.Handoffs)
			w.store.Save(t)
			continue
		case AgentResultBlocked:
			// Agent is blocked
			if err := task.TransitionTo(t, types.StateBlocked); err == nil {
				w.store.Move(t, t.State, types.StateBlocked)
			}
			return nil
		case AgentResultError:
			// Agent encountered an error
			log.Printf("[Worker %d] Agent error for task %s", w.id, t.ID[:8])
			return fmt.Errorf("agent error")
		}
	}

	return nil
}

// AgentResult represents the outcome of an agent run
type AgentResult int

const (
	AgentResultComplete AgentResult = iota
	AgentResultHandoff
	AgentResultBlocked
	AgentResultError
)

func (w *Worker) runAgent(ctx context.Context, t *types.Task) AgentResult {
	// Check llama.cpp server health
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := w.llamaClient.Health(healthCtx); err != nil {
		log.Printf("[Worker %d] LLM server not available: %v", w.id, err)
		return AgentResultError
	}

	// Create agent
	ag := agent.NewAgent(t, w.llamaClient, w.cfg.Llama, w.projectDir)

	// Set up tool executor
	var mcpExecutor agent.ToolExecutor
	if w.mcpClient != nil && w.mcpClient.IsConnected() {
		mcpExecutor = mcp.NewToolExecutor(w.mcpClient)
	}

	// Wrap with builtin tool executor
	builtinExecutor := &BuiltinToolExecutor{
		mcpExecutor: mcpExecutor,
		task:        t,
		store:       w.store,
	}
	ag.SetToolExecutor(builtinExecutor)

	// Run agent with timeout
	agentCtx, agentCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer agentCancel()

	err := ag.Run(agentCtx)

	// Update task from agent
	*t = *ag.Task()

	// Persist state changes (e.g. from XML state tags)
	if err := w.store.Save(t); err != nil {
		log.Printf("[Worker %d] Failed to save task state: %v", w.id, err)
	}

	if err != nil {
		if err.Error() == "handoff required" {
			return AgentResultHandoff
		}
		log.Printf("[Worker %d] Agent error: %v", w.id, err)
		return AgentResultError
	}

	return AgentResultComplete
}

func (w *Worker) attemptMerge(ctx context.Context, t *types.Task) error {
	log.Printf("[Worker %d] Attempting merge for task %s", w.id, t.ID[:8])

	result, err := w.merger.AttemptMerge(t)
	if err != nil {
		log.Printf("[Worker %d] Merge error: %v", w.id, err)
		return err
	}

	if result.Success {
		log.Printf("[Worker %d] Merge successful: %s", w.id, result.Message)
		if err := task.TransitionTo(t, types.StateCompleted); err == nil {
			w.store.Move(t, types.StateMerging, types.StateCompleted)
		}
		// Cleanup branch
		w.branchMgr.CleanupTaskBranch(t)
		return nil
	}

	// Merge conflict
	log.Printf("[Worker %d] Merge conflict detected in %d files", w.id, len(result.ConflictFiles))

	// Create conflict resolution task
	conflictTask := task.NewConflictResolutionTask(t, result.ConflictFiles)
	if err := w.store.Save(conflictTask); err != nil {
		return err
	}
	w.queue.Enqueue(conflictTask)

	// Mark original task as blocked
	t.State = types.StateConflict
	t.ConflictFiles = result.ConflictFiles
	w.store.Save(t)

	return nil
}

// BuiltinToolExecutor handles built-in tools and delegates to MCP
type BuiltinToolExecutor struct {
	mcpExecutor agent.ToolExecutor
	task        *types.Task
	store       *task.Store
}

func (e *BuiltinToolExecutor) AvailableTools() []agent.ToolInfo {
	tools := []agent.ToolInfo{
		{
			Name:        "update_task_state",
			Description: "Update the state and phase of the current task. Use this to signal completion (new_state='completed') or phase change.",
			Parameters:  `{"type": "object", "properties": {"new_state": {"type": "string", "enum": ["planning", "implementing", "testing", "debugging", "review", "merging", "completed", "blocked"]}, "new_phase": {"type": "string", "description": "The new phase (e.g. 'implement', 'test')"}, "reason": {"type": "string"}}, "required": ["new_state", "reason"]}`,
		},
	}
	if e.mcpExecutor != nil {
		tools = append(tools, e.mcpExecutor.AvailableTools()...)
	}
	return tools
}

func (e *BuiltinToolExecutor) Execute(ctx context.Context, call types.ToolCall) types.ToolResult {
	if call.Name == "update_task_state" {
		newStateStr, _ := call.Arguments["new_state"].(string)
		newPhaseStr, _ := call.Arguments["new_phase"].(string)
		reason, _ := call.Arguments["reason"].(string)

		if newStateStr == "" {
			return types.ToolResult{
				CallID:  call.ID,
				Success: false,
				Error:   "new_state is required",
			}
		}

		newState := types.TaskState(newStateStr)

		// Use task package to transition if possible
		if err := task.TransitionTo(e.task, newState); err != nil {
			return types.ToolResult{
				CallID:  call.ID,
				Success: false,
				Error:   fmt.Sprintf("Invalid state transition: %v", err),
			}
		}

		if newPhaseStr != "" {
			e.task.Phase = types.TaskPhase(newPhaseStr)
		}

		// Save the task state
		if err := e.store.Save(e.task); err != nil {
			return types.ToolResult{
				CallID:  call.ID,
				Success: false,
				Error:   fmt.Sprintf("Failed to save task state: %v", err),
			}
		}

		log.Printf("[Worker] Tool updated task state to %s (Reason: %s)", newState, reason)

		return types.ToolResult{
			CallID:  call.ID,
			Success: true,
			Output:  fmt.Sprintf("Task state successfully updated to %s", newState),
		}
	}

	if e.mcpExecutor != nil {
		return e.mcpExecutor.Execute(ctx, call)
	}

	return types.ToolResult{
		CallID:  call.ID,
		Success: false,
		Error:   fmt.Sprintf("tool not found: %s", call.Name),
	}
}
