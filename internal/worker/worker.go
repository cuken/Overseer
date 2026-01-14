package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cuken/overseer/internal/agent"
	"github.com/cuken/overseer/internal/git"
	"github.com/cuken/overseer/internal/logger"
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
	log         *logger.Logger
}

// SetVerbose enables verbose logging
func (w *Worker) SetVerbose(v bool) {
	w.verbose = v
	if w.llamaClient != nil {
		w.llamaClient.SetDebug(v)
	}
	if w.log != nil {
		w.log.SetVerbose(v)
	}
}

// NewWorker creates a new worker
func NewWorker(id int, projectDir string, cfg *types.Config, store *task.Store, queue *task.Queue, mcpClient *mcp.Client, logsDir string) *Worker {
	gitClient := git.New(projectDir)
	branchMgr := git.NewBranchManager(gitClient, cfg.Git)
	merger := git.NewMerger(gitClient, branchMgr, cfg.Git)
	llamaClient := agent.NewLlamaClient(cfg.Llama)
	log := logger.New(fmt.Sprintf("Worker-%d", id), logsDir)

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
		log:         log,
	}
}

// Run starts the worker loop
func (w *Worker) Run(ctx context.Context) {
	w.log.Info("Started")
	defer w.log.Info("Stopped")

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

		w.log.Info("Processing task %s: %s", t.ID[:8], t.Title)

		// Process the task
		if err := w.processTask(ctx, t); err != nil {
			w.log.LogError(err, fmt.Sprintf("Task %s failed", t.ID[:8]))
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
			w.log.Warn("Failed to create branch: %v", err)
			// Continue without branch for now
		} else {
			w.log.Success("Created branch %s", t.Branch)
		}
	} else {
		if err := w.branchMgr.SwitchToTaskBranch(t); err != nil {
			w.log.Warn("Failed to switch to branch: %v", err)
		}
	}

	// Save task state
	if err := w.store.Save(t); err != nil {
		return err
	}

	// Initialize debouncer for auto-commits
	debounceInterval := time.Duration(w.cfg.Git.DebounceSecs) * time.Second
	if debounceInterval == 0 {
		debounceInterval = 5 * time.Second
	}
	debouncer := git.NewDebouncer(w.gitClient, debounceInterval, t.Branch, w.cfg.Git.AutoPush, w.log)
	defer debouncer.Stop()

	// Run agent loop
	for t.State.IsActive() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if approval is required
		if t.RequiresApproval {
			w.log.Info("Task %s requires approval", t.ID[:8])
			oldState := t.State
			if err := task.TransitionTo(t, types.StateReview); err == nil {
				w.store.Move(t, oldState, types.StateReview)
			}
			return nil
		}

		// Run agent
		agentResult := w.runAgent(ctx, t, debouncer)

		// Handle agent result
		switch agentResult {
		case AgentResultComplete:
			// Agent completed its work - check if ready to merge
			// First priority: if state is already merging, do the merge
			if t.State == types.StateMerging {
				return w.attemptMerge(ctx, t)
			}

			// Second: if we're in test/debug phase and tests passed, transition to merging
			if t.Phase == types.PhaseDebug || t.Phase == types.PhaseTest {
				if err := task.TransitionTo(t, types.StateMerging); err == nil {
					w.store.Save(t)
					return w.attemptMerge(ctx, t)
				}
				// If transition failed, log it but continue - agent may need to do more work
				w.log.Debug("Could not transition to merging: state=%s, phase=%s",
					t.State, t.Phase)
			}

			// Agent completed but not ready to merge - continue the loop
			// The agent should have updated state/phase appropriately
			continue
		case AgentResultHandoff:
			// Agent needs handoff, continue with fresh agent
			w.log.Info("Agent handoff for task %s (generation %d)",
				t.ID[:8], t.Handoffs)
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
			w.log.Error("Agent error for task %s", t.ID[:8])
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

func (w *Worker) runAgent(ctx context.Context, t *types.Task, debouncer *git.Debouncer) AgentResult {
	// Check llama.cpp server health
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := w.llamaClient.Health(healthCtx); err != nil {
		w.log.Error("LLM server not available: %v", err)
		return AgentResultError
	}

	// Create agent
	logsDir := filepath.Join(w.projectDir, w.cfg.Paths.Logs)
	ag := agent.NewAgent(t, w.llamaClient, w.cfg.Llama, w.projectDir, filepath.Join(w.projectDir, w.cfg.Paths.Source), logsDir)
	ag.SetVerbose(w.verbose)

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
		debouncer:   debouncer,
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
		w.log.Warn("Failed to save task state: %v", err)
	}

	if err != nil {
		if err.Error() == "handoff required" {
			return AgentResultHandoff
		}
		w.log.LogError(err, "Agent execution failed")
		return AgentResultError
	}

	return AgentResultComplete
}

func (w *Worker) attemptMerge(ctx context.Context, t *types.Task) error {
	w.log.Info("Attempting merge for task %s", t.ID[:8])

	// Safety net: commit any uncommitted changes the agent left behind
	if hasChanges, _ := w.gitClient.HasChanges(); hasChanges {
		w.log.Info("Found uncommitted changes, committing before merge")
		if err := w.gitClient.AddAll(); err != nil {
			w.log.Warn("Failed to stage changes: %v", err)
		} else {
			commitMsg := fmt.Sprintf("[%s] Auto-commit before merge\n\nTask: %s", t.ID[:8], t.Title)
			if err := w.gitClient.Commit(commitMsg); err != nil {
				w.log.Warn("Failed to commit changes: %v", err)
			} else {
				w.log.Success("Auto-committed changes")
			}
		}
	}

	result, err := w.merger.AttemptMerge(t)
	if err != nil {
		w.log.LogError(err, "Merge failed")
		return err
	}

	if result.Success {
		w.log.Success("Merge successful: %s", result.Message)
		if err := task.TransitionTo(t, types.StateCompleted); err == nil {
			w.store.Move(t, types.StateMerging, types.StateCompleted)
		}
		// Cleanup branch
		w.branchMgr.CleanupTaskBranch(t)
		return nil
	}

	// Merge conflict
	w.log.Warn("Merge conflict detected in %d files", len(result.ConflictFiles))

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
	debouncer   *git.Debouncer
}

func (e *BuiltinToolExecutor) AvailableTools() []agent.ToolInfo {
	tools := []agent.ToolInfo{
		{
			Name:        "update_task_state",
			Description: "Update the state and phase of the current task. Use this to progress through the workflow: planning→implementing→testing→debugging→merging. Always provide BOTH new_state and new_phase together. Do NOT use 'review' - complete the full workflow instead.",
			Parameters:  `{"type": "object", "properties": {"new_state": {"type": "string", "enum": ["implementing", "testing", "debugging", "merging", "blocked"], "description": "The workflow state to transition to. Use: implementing (after planning), testing (after implementing), debugging (if tests fail), merging (when tests pass), blocked (if stuck)."}, "new_phase": {"type": "string", "enum": ["implement", "test", "debug"], "description": "The execution phase. Use: implement (with implementing state), test (with testing state), debug (with debugging state)."}, "reason": {"type": "string", "description": "Brief explanation for the transition"}}, "required": ["new_state", "reason"]}`,
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

		// Note: using a simple log here since this is called from multiple workers
		fmt.Printf("[Worker] Tool updated task state to %s (Reason: %s)\n", newState, reason)

		return types.ToolResult{
			CallID:  call.ID,
			Success: true,
			Output:  fmt.Sprintf("Task state successfully updated to %s", newState),
		}
	}

	var result types.ToolResult
	if e.mcpExecutor != nil {
		result = e.mcpExecutor.Execute(ctx, call)
	} else {
		result = types.ToolResult{
			CallID:  call.ID,
			Success: false,
			Error:   fmt.Sprintf("tool not found: %s", call.Name),
		}
	}

	// Trigger debounced commit if tool succeeded
	// The flush will only commit if there are actual git changes
	if result.Success && e.debouncer != nil {
		e.debouncer.MarkDirty(fmt.Sprintf("Auto-commit: Used tool %s", call.Name))
	}

	return result
}
