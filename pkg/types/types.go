package types

import "time"

// TaskState represents the current state of a task
type TaskState string

const (
	StatePending      TaskState = "pending"
	StatePlanning     TaskState = "planning"
	StateImplementing TaskState = "implementing"
	StateTesting      TaskState = "testing"
	StateDebugging    TaskState = "debugging"
	StateReview       TaskState = "review"
	StateMerging      TaskState = "merging"
	StateConflict     TaskState = "conflict"
	StateCompleted    TaskState = "completed"
	StateBlocked      TaskState = "blocked"
)

// TaskPhase represents the workflow phase within active states
type TaskPhase string

const (
	PhasePlan      TaskPhase = "plan"
	PhaseImplement TaskPhase = "implement"
	PhaseTest      TaskPhase = "test"
	PhaseDebug     TaskPhase = "debug"
)

// Task represents a unit of work to be completed by agents
type Task struct {
	ID               string    `yaml:"id" json:"id"`
	Title            string    `yaml:"title" json:"title"`
	Description      string    `yaml:"description" json:"description"`
	Branch           string    `yaml:"branch" json:"branch"`
	State            TaskState `yaml:"state" json:"state"`
	Phase            TaskPhase `yaml:"phase" json:"phase"`
	Priority         int       `yaml:"priority" json:"priority"`
	RequiresApproval bool      `yaml:"requires_approval" json:"requires_approval"`
	Dependencies     []string  `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	MergeTarget      string    `yaml:"merge_target" json:"merge_target"`
	CreatedAt        time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt        time.Time `yaml:"updated_at" json:"updated_at"`
	Handoffs         int       `yaml:"handoffs" json:"handoffs"`
	ConflictFiles    []string  `yaml:"conflict_files,omitempty" json:"conflict_files,omitempty"`
	ParentTaskID     string    `yaml:"parent_task_id,omitempty" json:"parent_task_id,omitempty"`
}

// IsActive returns true if the task is actively being worked on
func (s TaskState) IsActive() bool {
	switch s {
	case StatePlanning, StateImplementing, StateTesting, StateDebugging, StateMerging:
		return true
	default:
		return false
	}
}

// CanTransitionTo checks if a state transition is valid
func (s TaskState) CanTransitionTo(next TaskState) bool {
	// State machine: review is only accessible after testing (not during planning/implementing)
	// This prevents agents from prematurely requesting human review
	validTransitions := map[TaskState][]TaskState{
		StatePending:      {StatePlanning},
		StatePlanning:     {StateImplementing, StateBlocked},           // No review from planning
		StateImplementing: {StateTesting, StateBlocked},                // No review from implementing
		StateTesting:      {StateDebugging, StateMerging, StateReview}, // Review only after testing
		StateDebugging:    {StateTesting, StateReview, StateBlocked},   // Review allowed from debugging
		StateReview:       {StatePlanning, StateImplementing, StateTesting, StateMerging, StateBlocked},
		StateMerging:      {StateCompleted, StateConflict},
		StateConflict:     {StateBlocked},
		StateBlocked:      {StatePending, StatePlanning, StateImplementing},
	}

	allowed, ok := validTransitions[s]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == next {
			return true
		}
	}
	return false
}

// MCPServer represents configuration for an MCP server
type MCPServer struct {
	Name    string   `yaml:"name" mapstructure:"name"`
	Command string   `yaml:"command" mapstructure:"command"`
	Args    []string `yaml:"args" mapstructure:"args"`
	Env     []string `yaml:"env,omitempty" mapstructure:"env"`
}

// LlamaConfig holds llama.cpp server configuration
type LlamaConfig struct {
	ServerURL        string  `yaml:"server_url" mapstructure:"server_url"`
	ContextSize      int     `yaml:"context_size" mapstructure:"context_size"`
	HandoffThreshold float64 `yaml:"handoff_threshold" mapstructure:"handoff_threshold"`
	Model            string  `yaml:"model" mapstructure:"model"`
	Temperature      float64 `yaml:"temperature" mapstructure:"temperature"`
	MaxTokens        int     `yaml:"max_tokens" mapstructure:"max_tokens"`
}

// GitConfig holds git operation configuration
type GitConfig struct {
	MergeTarget  string `yaml:"merge_target" mapstructure:"merge_target"`
	BranchPrefix string `yaml:"branch_prefix" mapstructure:"branch_prefix"`
	AutoPush     bool   `yaml:"auto_push" mapstructure:"auto_push"`
	SignCommits  bool   `yaml:"sign_commits" mapstructure:"sign_commits"`
}

// WorkerConfig holds worker pool configuration
type WorkerConfig struct {
	Count           int `yaml:"count" mapstructure:"count"`
	MaxHandoffs     int `yaml:"max_handoffs" mapstructure:"max_handoffs"`
	IdleTimeoutSecs int `yaml:"idle_timeout_secs" mapstructure:"idle_timeout_secs"`
}

// PathsConfig holds directory paths
type PathsConfig struct {
	Requests   string `yaml:"requests" mapstructure:"requests"`
	Tasks      string `yaml:"tasks" mapstructure:"tasks"`
	Workspaces string `yaml:"workspaces" mapstructure:"workspaces"`
	Logs       string `yaml:"logs" mapstructure:"logs"`
	Source     string `yaml:"source" mapstructure:"source"`
}

// Config is the root configuration structure
type Config struct {
	Llama   LlamaConfig  `yaml:"llama" mapstructure:"llama"`
	Git     GitConfig    `yaml:"git" mapstructure:"git"`
	Workers WorkerConfig `yaml:"workers" mapstructure:"workers"`
	MCP     struct {
		Servers []MCPServer `yaml:"servers" mapstructure:"servers"`
	} `yaml:"mcp" mapstructure:"mcp"`
	Paths PathsConfig `yaml:"paths" mapstructure:"paths"`
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Llama: LlamaConfig{
			ServerURL:        "http://localhost:8080",
			ContextSize:      32768,
			HandoffThreshold: 0.8,
			Model:            "default",
			Temperature:      0.7,
			MaxTokens:        4096,
		},
		Git: GitConfig{
			MergeTarget:  "main",
			BranchPrefix: "feature",
			AutoPush:     true,
			SignCommits:  false,
		},
		Workers: WorkerConfig{
			Count:           1,
			MaxHandoffs:     10,
			IdleTimeoutSecs: 300,
		},
		Paths: PathsConfig{
			Requests:   ".overseer/requests",
			Tasks:      ".overseer/tasks",
			Workspaces: ".overseer/workspaces",
			Logs:       ".overseer/logs",
			Source:     ".", // Project root - agent will work with entire codebase
		},
	}
}

// HandoffContext contains state passed between agent generations
type HandoffContext struct {
	TaskID        string    `yaml:"task_id"`
	Generation    int       `yaml:"generation"`
	TokensUsed    int       `yaml:"tokens_used"`
	Summary       string    `yaml:"summary"`
	NextSteps     []string  `yaml:"next_steps"`
	KeyLearnings  []string  `yaml:"key_learnings"`
	FilesModified []string  `yaml:"files_modified"`
	Blockers      []string  `yaml:"blockers,omitempty"`
	Timestamp     time.Time `yaml:"timestamp"`
}

// AgentResponse represents structured output from an agent
type AgentResponse struct {
	Thinking     string       `json:"thinking,omitempty"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
	Message      string       `json:"message,omitempty"`
	StateChange  *StateChange `json:"state_change,omitempty"`
	NeedsHandoff bool         `json:"needs_handoff"`
}

// ToolCall represents a request to execute an MCP tool
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	CallID  string `json:"call_id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// StateChange represents a requested task state transition
type StateChange struct {
	NewState TaskState `json:"new_state"`
	NewPhase TaskPhase `json:"new_phase,omitempty"`
	Reason   string    `json:"reason"`
}
