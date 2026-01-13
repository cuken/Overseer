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
	ID               string    `yaml:"id"`
	Title            string    `yaml:"title"`
	Description      string    `yaml:"description"`
	Branch           string    `yaml:"branch"`
	State            TaskState `yaml:"state"`
	Phase            TaskPhase `yaml:"phase"`
	Priority         int       `yaml:"priority"`
	RequiresApproval bool      `yaml:"requires_approval"`
	Dependencies     []string  `yaml:"dependencies,omitempty"`
	MergeTarget      string    `yaml:"merge_target"`
	CreatedAt        time.Time `yaml:"created_at"`
	UpdatedAt        time.Time `yaml:"updated_at"`
	Handoffs         int       `yaml:"handoffs"`
	ConflictFiles    []string  `yaml:"conflict_files,omitempty"`
	ParentTaskID     string    `yaml:"parent_task_id,omitempty"`
}

// StateDirectory returns the directory name for a given state
func (s TaskState) Directory() string {
	switch s {
	case StatePending, StatePlanning, StateImplementing, StateTesting, StateDebugging:
		return "active"
	case StateReview:
		return "review"
	case StateCompleted:
		return "completed"
	case StateBlocked, StateConflict, StateMerging:
		return "active"
	default:
		return "pending"
	}
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
	validTransitions := map[TaskState][]TaskState{
		StatePending:      {StatePlanning},
		StatePlanning:     {StateImplementing, StateReview, StateBlocked},
		StateImplementing: {StateTesting, StateReview, StateBlocked},
		StateTesting:      {StateDebugging, StateMerging, StateReview},
		StateDebugging:    {StateTesting, StateReview, StateBlocked},
		StateReview:       {StatePlanning, StateImplementing, StateMerging, StateBlocked},
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
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Env     []string `yaml:"env,omitempty"`
}

// LlamaConfig holds llama.cpp server configuration
type LlamaConfig struct {
	ServerURL         string  `yaml:"server_url"`
	ContextSize       int     `yaml:"context_size"`
	HandoffThreshold  float64 `yaml:"handoff_threshold"`
	Model             string  `yaml:"model"`
	Temperature       float64 `yaml:"temperature"`
	MaxTokens         int     `yaml:"max_tokens"`
}

// GitConfig holds git operation configuration
type GitConfig struct {
	MergeTarget  string `yaml:"merge_target"`
	BranchPrefix string `yaml:"branch_prefix"`
	AutoPush     bool   `yaml:"auto_push"`
	SignCommits  bool   `yaml:"sign_commits"`
}

// WorkerConfig holds worker pool configuration
type WorkerConfig struct {
	Count           int `yaml:"count"`
	MaxHandoffs     int `yaml:"max_handoffs"`
	IdleTimeoutSecs int `yaml:"idle_timeout_secs"`
}

// PathsConfig holds directory paths
type PathsConfig struct {
	Requests   string `yaml:"requests"`
	Tasks      string `yaml:"tasks"`
	Workspaces string `yaml:"workspaces"`
	Logs       string `yaml:"logs"`
}

// Config is the root configuration structure
type Config struct {
	Llama   LlamaConfig  `yaml:"llama"`
	Git     GitConfig    `yaml:"git"`
	Workers WorkerConfig `yaml:"workers"`
	MCP     struct {
		Servers []MCPServer `yaml:"servers"`
	} `yaml:"mcp"`
	Paths PathsConfig `yaml:"paths"`
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
		},
	}
}

// HandoffContext contains state passed between agent generations
type HandoffContext struct {
	TaskID          string    `yaml:"task_id"`
	Generation      int       `yaml:"generation"`
	TokensUsed      int       `yaml:"tokens_used"`
	Summary         string    `yaml:"summary"`
	NextSteps       []string  `yaml:"next_steps"`
	KeyLearnings    []string  `yaml:"key_learnings"`
	FilesModified   []string  `yaml:"files_modified"`
	Blockers        []string  `yaml:"blockers,omitempty"`
	Timestamp       time.Time `yaml:"timestamp"`
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
