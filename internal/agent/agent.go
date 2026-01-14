package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cuken/overseer/internal/logger"
	"github.com/cuken/overseer/pkg/types"
	"gopkg.in/yaml.v3"
)

// Agent manages a single agent session for a task
type Agent struct {
	task           *types.Task
	llama          *LlamaClient
	contextTracker *ContextTracker
	promptBuilder  *PromptBuilder
	workspaceDir   string
	projectDir     string
	sourceDir      string
	messages       []ChatMessage
	toolExecutor   ToolExecutor
	generation     int
	log            *logger.Logger
}

// ToolExecutor is an interface for executing tool calls
type ToolExecutor interface {
	Execute(ctx context.Context, call types.ToolCall) types.ToolResult
	AvailableTools() []ToolInfo
}

// NewAgent creates a new agent for a task
func NewAgent(task *types.Task, llama *LlamaClient, cfg types.LlamaConfig, projectDir, sourceDir, logsDir string) *Agent {
	workspaceDir := filepath.Join(projectDir, ".overseer", "workspaces", task.ID)
	os.MkdirAll(workspaceDir, 0755)

	// Create a task-specific logger
	log := logger.New(fmt.Sprintf("Agent-%s", task.ID[:8]), logsDir)

	return &Agent{
		task:           task,
		llama:          llama,
		contextTracker: NewContextTracker(cfg),
		promptBuilder:  NewPromptBuilder(),
		workspaceDir:   workspaceDir,
		projectDir:     projectDir,
		sourceDir:      sourceDir,
		messages:       make([]ChatMessage, 0),
		generation:     0,
		log:            log,
	}
}

// SetToolExecutor sets the tool executor for handling tool calls
func (a *Agent) SetToolExecutor(executor ToolExecutor) {
	a.toolExecutor = executor
}

// SetVerbose enables verbose logging for the agent
func (a *Agent) SetVerbose(v bool) {
	if a.log != nil {
		a.log.SetVerbose(v)
	}
}

// Run executes the agent loop until handoff or completion
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("Starting task %s (phase: %s, generation: %d)",
		a.task.ID[:8], a.task.Phase, a.generation)

	// Check if task is valid for agent execution
	if !a.task.State.IsActive() || a.task.State == types.StateMerging {
		a.log.Info("Task is in terminal/inactive state %s, skipping run", a.task.State)
		return nil
	}

	// Initialize with system prompt
	if err := a.initializeSession(); err != nil {
		return fmt.Errorf("failed to initialize session: %w", err)
	}

	// Main agent loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if handoff is needed before making request
		if a.contextTracker.NeedsHandoff() {
			a.log.Info("Context threshold reached, initiating handoff")
			return a.performHandoff()
		}

		// Get next action from LLM
		response, err := a.getNextAction(ctx)
		if err != nil {
			return fmt.Errorf("failed to get agent response: %w", err)
		}

		// Parse and execute response
		parsed, err := a.parseResponse(response)
		if err != nil {
			a.log.Error("Failed to parse response: %v", err)
			// Return error feedback to agent
			a.messages = append(a.messages, ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("System Error: %v", err),
			})
			continue
		}

		// Handle state changes
		var stateFeedback string
		if parsed.StateChange != nil {
			if err := a.handleStateChange(parsed.StateChange); err != nil {
				a.log.Warn("State change failed: %v", err)
				stateFeedback = fmt.Sprintf("State change failed: %v", err)
			} else {
				a.log.Info("State successfully changed to %s (Phase: %s)",
					a.task.State, a.task.Phase)
				stateFeedback = fmt.Sprintf("State successfully changed to %s (Phase: %s)",
					a.task.State, a.task.Phase)
			}
		}

		// Execute tool calls
		if len(parsed.ToolCalls) > 0 {
			// If there was state feedback, add it as a user message first
			if stateFeedback != "" {
				a.messages = append(a.messages, ChatMessage{
					Role:    "user",
					Content: stateFeedback,
				})
			}

			results := a.executeTools(ctx, parsed.ToolCalls)
			a.addToolResults(results)
		} else if stateFeedback != "" {
			// If no tools but state changed (or failed), we must send feedback
			// to acknowledge the action and prevent agent loop
			a.messages = append(a.messages, ChatMessage{
				Role:    "user",
				Content: stateFeedback,
			})
			a.contextTracker.UpdateUsage(a.contextTracker.TokensUsed() + EstimateTokens(stateFeedback))
		}

		// Check for handoff signal
		if parsed.NeedsHandoff {
			return a.performHandoff()
		}

		// Check if task is complete or inactive
		if !a.task.State.IsActive() || a.task.State == types.StateMerging {
			a.log.Info("Task reached terminal/inactive state: %s", a.task.State)
			return nil
		}

		// If we are here, the loop is continuing.
		// If the last message was from the assistant (meaning no tools were called and no state feedback was added),
		// we MUST append a user message to maintain the User->Assistant turn structure required by thinking models.
		// We also remind the agent to use tools or signal completion.
		if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
			a.log.Debug("Appending continuation prompt to maintain flow")
			prompt := "Acknowledged. Please proceed with the next step, or signal completion (using the update_task_state tool) if the task is finished."
			a.messages = append(a.messages, ChatMessage{
				Role:    "user",
				Content: prompt,
			})
			a.contextTracker.UpdateUsage(a.contextTracker.TokensUsed() + EstimateTokens(prompt))
		}
	}
}

func (a *Agent) initializeSession() error {
	// Build system prompt
	data := PromptData{
		Task:          a.task,
		WorkspaceDir:  a.workspaceDir,
		ProjectDir:    a.projectDir,
		SourceDir:     a.sourceDir,
		ContextStatus: a.contextTracker.Status(),
	}
	if a.toolExecutor != nil {
		data.AvailableTools = a.toolExecutor.AvailableTools()
	}

	systemPrompt, err := a.promptBuilder.BuildSystemPrompt(data)
	if err != nil {
		return err
	}

	a.messages = append(a.messages, ChatMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	// Check for existing handoff
	handoff, err := a.loadHandoff()
	if err == nil && handoff != nil {
		data.Handoff = handoff
		a.generation = handoff.Generation + 1
		userPrompt, err := a.promptBuilder.BuildHandoffPrompt(data)
		if err != nil {
			return err
		}
		a.messages = append(a.messages, ChatMessage{
			Role:    "user",
			Content: userPrompt,
		})
	} else {
		userPrompt, err := a.promptBuilder.BuildKickoffPrompt(data)
		if err != nil {
			return err
		}
		a.messages = append(a.messages, ChatMessage{
			Role:    "user",
			Content: userPrompt,
		})
	}

	// Estimate initial tokens
	totalText := ""
	for _, msg := range a.messages {
		totalText += msg.Content
	}
	a.contextTracker.SetTokensUsed(EstimateTokens(totalText))

	return nil
}

func (a *Agent) getNextAction(ctx context.Context) (string, error) {
	resp, err := a.llama.Chat(ctx, a.messages)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	content := resp.Choices[0].Message.Content

	// Update token tracking
	a.contextTracker.UpdateUsage(resp.Usage.TotalTokens)

	// Add assistant response to history
	a.messages = append(a.messages, ChatMessage{
		Role:    "assistant",
		Content: content,
	})

	a.log.Debug("Response received (tokens: %d/%d, %.1f%%)",
		a.contextTracker.TokensUsed(),
		a.contextTracker.contextSize,
		a.contextTracker.UsageRatio()*100)

	return content, nil
}

func (a *Agent) parseResponse(content string) (*types.AgentResponse, error) {
	response := &types.AgentResponse{}

	// Extract thinking
	thinkingRe := regexp.MustCompile(`(?s)<thinking>(.*?)</thinking>`)
	if match := thinkingRe.FindStringSubmatch(content); len(match) > 1 {
		response.Thinking = strings.TrimSpace(match[1])
		// Log thinking if verbose
		a.log.LogAgentThinking(response.Thinking)
	}

	// Extract tool calls
	toolCallsRe := regexp.MustCompile(`(?s)<tool_calls>(.*?)</tool_calls>`)
	if match := toolCallsRe.FindStringSubmatch(content); len(match) > 1 {
		rawJSON := strings.TrimSpace(match[1])

		// Sanitize the JSON to fix common issues
		rawJSON = sanitizeJSON(rawJSON)

		var calls []types.ToolCall
		// Try parsing as array first
		if err := json.Unmarshal([]byte(rawJSON), &calls); err != nil {
			// Try parsing as a single object
			var singleCall types.ToolCall
			if err2 := json.Unmarshal([]byte(rawJSON), &singleCall); err2 == nil {
				calls = []types.ToolCall{singleCall}
			} else {
				a.log.Error("Failed to parse tool calls: %v. Raw: %s", err, rawJSON)
				return nil, fmt.Errorf("failed to parse tool calls JSON: %w. Raw content: %s", err, rawJSON)
			}
		}

		// Ensure all calls have an ID
		for i := range calls {
			if calls[i].ID == "" {
				h := sha256.New()
				h.Write([]byte(fmt.Sprintf("%s-%d-%d", calls[i].Name, time.Now().UnixNano(), i)))
				calls[i].ID = fmt.Sprintf("call_%x", h.Sum(nil)[:4])
			}
		}
		response.ToolCalls = calls
	} else if strings.Contains(content, "<tool_calls>") {
		return nil, fmt.Errorf("detected <tool_calls> tag but failed to extract content. Please ensure JSON is strictly inside tags.")
	}
	// Check for handoff signal
	if strings.Contains(content, "<handoff>") || strings.Contains(content, "needs_handoff: true") {
		response.NeedsHandoff = true
	}

	// Extract state change requests
	stateRe := regexp.MustCompile(`(?s)<state_change>(.*?)</state_change>`)
	if match := stateRe.FindStringSubmatch(content); len(match) > 1 {
		var sc types.StateChange
		if err := json.Unmarshal([]byte(match[1]), &sc); err == nil {
			response.StateChange = &sc
		}
	}

	// Non-structured content is the message
	response.Message = content

	// Log the message (excluding thinking and tool calls)
	// Extract plain message text by removing XML tags
	plainMessage := content
	plainMessage = thinkingRe.ReplaceAllString(plainMessage, "")
	plainMessage = toolCallsRe.ReplaceAllString(plainMessage, "")
	plainMessage = stateRe.ReplaceAllString(plainMessage, "")
	plainMessage = strings.TrimSpace(plainMessage)
	if plainMessage != "" && !strings.Contains(plainMessage, "<handoff>") {
		a.log.LogAgentMessage(plainMessage)
	}

	return response, nil
}

// sanitizeJSON attempts to fix common JSON formatting issues from LLM output
func sanitizeJSON(rawJSON string) string {
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" {
		return ""
	}

	// 1. Remove comments (both // and # if they appear outside strings)
	// We do this with a state machine to avoid breaking strings
	var clean strings.Builder
	inString := false
	escaped := false
	inComment := false

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]

		if inComment {
			if ch == '\n' {
				inComment = false
				clean.WriteByte(ch)
			}
			continue
		}

		if escaped {
			clean.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' {
			clean.WriteByte(ch)
			if inString {
				escaped = true
			}
			continue
		}

		if ch == '"' {
			inString = !inString
			clean.WriteByte(ch)
			continue
		}

		if !inString {
			// Check for start of comment
			if ch == '#' {
				inComment = true
				continue
			}
			if ch == '/' && i+1 < len(trimmed) && trimmed[i+1] == '/' {
				inComment = true
				i++ // skip next /
				continue
			}
		}

		clean.WriteByte(ch)
	}

	trimmed = clean.String()

	// 2. Remove trailing commas before closing brackets
	commaRe := regexp.MustCompile(`,\s*([}\]])`)
	trimmed = commaRe.ReplaceAllString(trimmed, "$1")

	// 3. Fix literal characters in strings
	trimmed = escapeControlCharsInJSON(trimmed)

	// 4. Auto-balance braces and brackets
	// This handles cases where the LLM cuts off or forgets closers
	var stack []byte
	inString = false
	escaped = false

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString {
			if ch == '{' || ch == '[' {
				stack = append(stack, ch)
			} else if ch == '}' {
				if len(stack) > 0 && stack[len(stack)-1] == '{' {
					stack = stack[:len(stack)-1]
				}
			} else if ch == ']' {
				if len(stack) > 0 && stack[len(stack)-1] == '[' {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}

	// Append missing closers in reverse order
	var closer strings.Builder
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			closer.WriteByte('}')
		} else {
			closer.WriteByte(']')
		}
	}
	trimmed += closer.String()

	return trimmed
}

// escapeControlCharsInJSON escapes literal control characters in JSON strings.
// Note: This function expects that strings are properly identified by double quotes.
func escapeControlCharsInJSON(s string) string {
	var result strings.Builder
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			result.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' {
			result.WriteByte(ch)
			// Only enter escaped mode if we're in a string or it's a quote
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			result.WriteByte(ch)
			continue
		}

		if inString {
			// We're inside a string - escape control characters
			switch ch {
			case '\t':
				result.WriteString("\\t")
			case '\n':
				result.WriteString("\\n")
			case '\r':
				result.WriteString("\\r")
			case '\b':
				result.WriteString("\\b")
			case '\f':
				result.WriteString("\\f")
			default:
				result.WriteByte(ch)
			}
		} else {
			result.WriteByte(ch)
		}
	}

	return result.String()
}

func (a *Agent) executeTools(ctx context.Context, calls []types.ToolCall) []types.ToolResult {
	if a.toolExecutor == nil {
		var results []types.ToolResult
		for _, call := range calls {
			results = append(results, types.ToolResult{
				CallID:  call.ID,
				Success: false,
				Error:   "no tool executor configured",
			})
		}
		return results
	}

	var results []types.ToolResult
	for _, call := range calls {
		a.log.Info("Executing tool: %s", call.Name)
		result := a.toolExecutor.Execute(ctx, call)
		results = append(results, result)
	}
	return results
}

func (a *Agent) addToolResults(results []types.ToolResult) {
	prompt, err := a.promptBuilder.BuildToolResultPrompt(results)
	if err != nil {
		a.log.Error("Failed to build tool result prompt: %v", err)
		return
	}

	a.messages = append(a.messages, ChatMessage{
		Role:    "user",
		Content: prompt,
	})

	a.contextTracker.UpdateUsage(a.contextTracker.TokensUsed() + EstimateTokens(prompt))
}

func (a *Agent) handleStateChange(change *types.StateChange) error {
	a.log.Info("State change requested: %s -> %s (%s)",
		a.task.State, change.NewState, change.Reason)

	if change.NewPhase != "" {
		a.task.Phase = change.NewPhase
	}

	oldState := a.task.State
	if oldState.CanTransitionTo(change.NewState) {
		a.task.State = change.NewState
		a.task.UpdatedAt = time.Now()
		return nil
	}

	return fmt.Errorf("invalid state transition: %s -> %s", oldState, change.NewState)
}

func (a *Agent) performHandoff() error {
	a.log.Info("Performing handoff for task %s", a.task.ID[:8])

	// Create handoff context
	handoff := &types.HandoffContext{
		TaskID:     a.task.ID,
		Generation: a.generation,
		TokensUsed: a.contextTracker.TokensUsed(),
		Timestamp:  time.Now(),
	}

	// Request summary from agent
	summaryPrompt := `Your context is nearly full. Please provide a handoff summary:

1. What have you accomplished so far?
2. What are the immediate next steps?
3. What key learnings should the next agent know?
4. What files have been modified?
5. Are there any blockers?

Respond in this exact format:
<handoff_summary>
summary: |
  Brief summary of accomplishments
next_steps:
  - step 1
  - step 2
key_learnings:
  - learning 1
files_modified:
  - file1.go
blockers:
  - blocker (if any)
</handoff_summary>`

	a.messages = append(a.messages, ChatMessage{
		Role:    "user",
		Content: summaryPrompt,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := a.llama.Chat(ctx, a.messages)
	if err != nil {
		a.log.Warn("Failed to get handoff summary: %v", err)
	} else if len(resp.Choices) > 0 {
		a.parseHandoffSummary(resp.Choices[0].Message.Content, handoff)
	}

	// Save handoff
	if err := a.saveHandoff(handoff); err != nil {
		a.log.Warn("Failed to save handoff: %v", err)
	}

	// Increment task handoff counter
	a.task.Handoffs++
	a.task.UpdatedAt = time.Now()

	return fmt.Errorf("handoff required")
}

func (a *Agent) parseHandoffSummary(content string, handoff *types.HandoffContext) {
	// Extract handoff summary block
	re := regexp.MustCompile(`(?s)<handoff_summary>(.*?)</handoff_summary>`)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		handoff.Summary = content // Use raw content as summary
		return
	}

	// Parse YAML-like format
	var parsed struct {
		Summary       string   `yaml:"summary"`
		NextSteps     []string `yaml:"next_steps"`
		KeyLearnings  []string `yaml:"key_learnings"`
		FilesModified []string `yaml:"files_modified"`
		Blockers      []string `yaml:"blockers"`
	}

	if err := yaml.Unmarshal([]byte(match[1]), &parsed); err != nil {
		handoff.Summary = match[1]
		return
	}

	handoff.Summary = parsed.Summary
	handoff.NextSteps = parsed.NextSteps
	handoff.KeyLearnings = parsed.KeyLearnings
	handoff.FilesModified = parsed.FilesModified
	handoff.Blockers = parsed.Blockers
}

func (a *Agent) loadHandoff() (*types.HandoffContext, error) {
	path := filepath.Join(a.workspaceDir, "handoff.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var handoff types.HandoffContext
	if err := yaml.Unmarshal(data, &handoff); err != nil {
		return nil, err
	}

	return &handoff, nil
}

func (a *Agent) saveHandoff(handoff *types.HandoffContext) error {
	path := filepath.Join(a.workspaceDir, "handoff.yaml")
	data, err := yaml.Marshal(handoff)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Task returns the current task state
func (a *Agent) Task() *types.Task {
	return a.task
}

// ContextStatus returns the current context tracker status
func (a *Agent) ContextStatus() ContextStatus {
	return a.contextTracker.Status()
}
