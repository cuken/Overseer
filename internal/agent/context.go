package agent

import (
	"github.com/cuken/overseer/pkg/types"
)

// ContextTracker monitors token usage and determines when handoffs are needed
type ContextTracker struct {
	contextSize      int
	handoffThreshold float64
	tokensUsed       int
	generation       int
}

// NewContextTracker creates a new context tracker
func NewContextTracker(cfg types.LlamaConfig) *ContextTracker {
	return &ContextTracker{
		contextSize:      cfg.ContextSize,
		handoffThreshold: cfg.HandoffThreshold,
		tokensUsed:       0,
		generation:       0,
	}
}

// UpdateUsage updates the token usage based on the total reported by the LLM
func (c *ContextTracker) UpdateUsage(totalTokens int) {
	c.tokensUsed = totalTokens
}

// SetTokensUsed sets the total tokens used directly
func (c *ContextTracker) SetTokensUsed(total int) {
	c.tokensUsed = total
}

// NeedsHandoff returns true if context usage exceeds the threshold
func (c *ContextTracker) NeedsHandoff() bool {
	return c.UsageRatio() >= c.handoffThreshold
}

// UsageRatio returns the current context usage as a ratio (0.0 to 1.0)
func (c *ContextTracker) UsageRatio() float64 {
	if c.contextSize == 0 {
		return 0
	}
	return float64(c.tokensUsed) / float64(c.contextSize)
}

// TokensUsed returns the number of tokens used
func (c *ContextTracker) TokensUsed() int {
	return c.tokensUsed
}

// TokensRemaining returns the estimated tokens remaining
func (c *ContextTracker) TokensRemaining() int {
	remaining := c.contextSize - c.tokensUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TokensUntilHandoff returns tokens until handoff threshold is reached
func (c *ContextTracker) TokensUntilHandoff() int {
	threshold := int(float64(c.contextSize) * c.handoffThreshold)
	remaining := threshold - c.tokensUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Reset clears the token count (for a new generation)
func (c *ContextTracker) Reset() {
	c.tokensUsed = 0
	c.generation++
}

// Generation returns the current generation number
func (c *ContextTracker) Generation() int {
	return c.generation
}

// EstimateTokens provides a rough token estimate for text
// Uses a simple heuristic: ~4 characters per token for English text
func EstimateTokens(text string) int {
	return len(text) / 4
}

// ContextStatus provides a summary of context state
type ContextStatus struct {
	TokensUsed      int
	TokensRemaining int
	UsagePercent    float64
	NeedsHandoff    bool
	Generation      int
}

// Status returns the current context status
func (c *ContextTracker) Status() ContextStatus {
	return ContextStatus{
		TokensUsed:      c.tokensUsed,
		TokensRemaining: c.TokensRemaining(),
		UsagePercent:    c.UsageRatio() * 100,
		NeedsHandoff:    c.NeedsHandoff(),
		Generation:      c.generation,
	}
}
