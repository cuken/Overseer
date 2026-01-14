package types

import "time"

// GateType represents the type of external condition being waited on
type GateType string

const (
	GateGitHubRun  GateType = "github-run"
	GatePRApproval GateType = "pr-approval"
	GateTimer      GateType = "timer"
	GateHumanInput GateType = "human-input"
)

// GateStatus represents the state of a gate
type GateStatus string

const (
	GateStatusPending GateStatus = "pending"
	GateStatusCleared GateStatus = "cleared"
	GateStatusFailed  GateStatus = "failed"
	GateStatusExpired GateStatus = "expired"
)

// Gate represents an external condition that blocks a task
type Gate struct {
	ID        string     `yaml:"id" json:"id"`
	Type      GateType   `yaml:"type" json:"type"`
	Status    GateStatus `yaml:"status" json:"status"`
	Reference string     `yaml:"ref,omitempty" json:"ref,omitempty"` // run-id, PR number, etc.
	Message   string     `yaml:"message,omitempty" json:"message,omitempty"`
	Timeout   time.Time  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	CreatedAt time.Time  `yaml:"created_at" json:"created_at"`
	ClearedAt *time.Time `yaml:"cleared_at,omitempty" json:"cleared_at,omitempty"`
}
