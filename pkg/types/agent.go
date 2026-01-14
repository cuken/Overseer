package types

import "time"

// AgentState represents the lifecycle state of an agent
type AgentState string

const (
	AgentStateIdle     AgentState = "idle"     // Connected but waiting for work
	AgentStateSpawning AgentState = "spawning" // Initializing context/tools
	AgentStateRunning  AgentState = "running"  // Active and healthy
	AgentStateWorking  AgentState = "working"  // Actively executing an LLM request
	AgentStateStuck    AgentState = "stuck"    // Heartbeat missed
	AgentStateDone     AgentState = "done"     // Task completed successfully
	AgentStateStopped  AgentState = "stopped"  // Manually stopped
	AgentStateDead     AgentState = "dead"     // Process disappeared
)

// WorkerStatus represents the health and state of a worker/agent
type WorkerStatus struct {
	ID            string     `json:"id"`
	Pid           int        `json:"pid"`
	TaskID        string     `json:"task_id,omitempty"`
	State         AgentState `json:"state"`
	LastHeartbeat time.Time  `json:"last_heartbeat"`
	StartedAt     time.Time  `json:"started_at"`
}
