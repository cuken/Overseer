package agent

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/cuken/overseer/pkg/types"
)

// PromptBuilder constructs prompts for agent sessions
type PromptBuilder struct {
	templates map[string]*template.Template
}

// NewPromptBuilder creates a new prompt builder with default templates
func NewPromptBuilder() *PromptBuilder {
	pb := &PromptBuilder{
		templates: make(map[string]*template.Template),
	}
	pb.registerDefaultTemplates()
	return pb
}

func (pb *PromptBuilder) registerDefaultTemplates() {
	pb.templates["system"] = template.Must(template.New("system").Parse(systemPromptTemplate))
	pb.templates["kickoff"] = template.Must(template.New("kickoff").Parse(kickoffPromptTemplate))
	pb.templates["handoff"] = template.Must(template.New("handoff").Parse(handoffPromptTemplate))
	pb.templates["tool_result"] = template.Must(template.New("tool_result").Parse(toolResultTemplate))
}

// PromptData contains all data needed to render prompts
type PromptData struct {
	Task           *types.Task
	Handoff        *types.HandoffContext
	ToolResults    []types.ToolResult
	WorkspaceDir   string
	AvailableTools []ToolInfo
	ContextStatus  ContextStatus
}

// ToolInfo describes an available tool
type ToolInfo struct {
	Name        string
	Description string
	Parameters  string
}

// BuildSystemPrompt creates the system prompt for an agent
func (pb *PromptBuilder) BuildSystemPrompt(data PromptData) (string, error) {
	return pb.render("system", data)
}

// BuildKickoffPrompt creates the initial prompt for starting a task
func (pb *PromptBuilder) BuildKickoffPrompt(data PromptData) (string, error) {
	return pb.render("kickoff", data)
}

// BuildHandoffPrompt creates a prompt for continuing from a previous agent
func (pb *PromptBuilder) BuildHandoffPrompt(data PromptData) (string, error) {
	return pb.render("handoff", data)
}

// BuildToolResultPrompt creates a prompt containing tool execution results
func (pb *PromptBuilder) BuildToolResultPrompt(results []types.ToolResult) (string, error) {
	data := PromptData{ToolResults: results}
	return pb.render("tool_result", data)
}

func (pb *PromptBuilder) render(name string, data PromptData) (string, error) {
	tmpl, ok := pb.templates[name]
	if !ok {
		return "", fmt.Errorf("template not found: %s", name)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return buf.String(), nil
}

// Template definitions
const systemPromptTemplate = `You are an autonomous coding agent managed by Overseer. You work on software development tasks through a structured workflow.

## Your Capabilities
You have access to these tools:
{{range .AvailableTools}}
- **{{.Name}}**: {{.Description}}
{{end}}

## Response Format
You must respond in a structured format. Each response should include:

1. **Thinking**: Your analysis and reasoning (wrapped in <thinking> tags)
2. **Tool Calls**: Any tools you need to execute (as JSON array)
3. **State Updates**: Any state changes for the task

Example response:
<thinking>
I need to read the existing code to understand the structure before making changes.
</thinking>

<tool_calls>
[{"name": "filesystem_read", "arguments": {"path": "src/main.go"}}]
</tool_calls>

## Important Rules
1. Work incrementally - make small, testable changes
2. Commit frequently with descriptive messages
3. If you encounter an error, try to fix it before asking for help
4. If you're stuck or need human input, set requires_approval: true
5. Monitor your context usage - when approaching the limit, write a handoff summary

## Context Management
- Current context usage: {{.ContextStatus.UsagePercent}}%
- Tokens remaining: {{.ContextStatus.TokensRemaining}}
- When context usage exceeds 80%, prepare a handoff:
  1. Summarize what you've accomplished
  2. Document next steps
  3. Note any blockers or concerns
  4. Signal that handoff is needed
`

const kickoffPromptTemplate = `# Task Assignment

## Task Details
- **ID**: {{.Task.ID}}
- **Title**: {{.Task.Title}}
- **Branch**: {{.Task.Branch}}
- **Current Phase**: {{.Task.Phase}}
- **Priority**: {{.Task.Priority}}

## Description
{{.Task.Description}}

## Workspace
Your working directory is: {{.WorkspaceDir}}

Context files to read:
- .overseer/workspaces/{{.Task.ID}}/plan.md (if exists)
- .overseer/workspaces/{{.Task.ID}}/context.md (if exists)

## Your Instructions

{{if eq .Task.Phase "plan"}}
### Planning Phase
1. Explore the codebase to understand the structure
2. Identify files that need to be created or modified
3. Create a detailed plan in .overseer/workspaces/{{.Task.ID}}/plan.md
4. When the plan is complete, update the task phase to "implement"
{{else if eq .Task.Phase "implement"}}
### Implementation Phase
1. Read the plan from .overseer/workspaces/{{.Task.ID}}/plan.md
2. Implement the changes step by step
3. Commit your changes frequently
4. When implementation is complete, move to "test" phase
{{else if eq .Task.Phase "test"}}
### Testing Phase
1. Run the project's test suite
2. Verify your changes work as expected
3. If tests fail, move to "debug" phase
4. If tests pass, move to "merging" phase
{{else if eq .Task.Phase "debug"}}
### Debugging Phase
1. Analyze test failures
2. Fix the issues
3. Return to "test" phase when fixed
{{end}}

Begin working on this task now.
`

const handoffPromptTemplate = `# Task Continuation

## Task Details
- **ID**: {{.Task.ID}}
- **Title**: {{.Task.Title}}
- **Branch**: {{.Task.Branch}}
- **Current Phase**: {{.Task.Phase}}

## Previous Agent Summary
Generation: {{.Handoff.Generation}}

### What was completed:
{{.Handoff.Summary}}

### Next steps identified:
{{range .Handoff.NextSteps}}
- {{.}}
{{end}}

### Key learnings:
{{range .Handoff.KeyLearnings}}
- {{.}}
{{end}}

### Files modified:
{{range .Handoff.FilesModified}}
- {{.}}
{{end}}

{{if .Handoff.Blockers}}
### Blockers:
{{range .Handoff.Blockers}}
- {{.}}
{{end}}
{{end}}

## Your Instructions
Continue from where the previous agent left off. Read the context files for full details:
- .overseer/workspaces/{{.Task.ID}}/plan.md
- .overseer/workspaces/{{.Task.ID}}/context.md
- .overseer/workspaces/{{.Task.ID}}/handoff.md

Begin working now.
`

const toolResultTemplate = `## Tool Execution Results

{{range .ToolResults}}
### {{.CallID}}
{{if .Success}}
**Success**
` + "```" + `
{{.Output}}
` + "```" + `
{{else}}
**Error**: {{.Error}}
{{end}}
{{end}}

Continue with your task based on these results.
`

// FormatToolsForPrompt formats available tools for inclusion in prompts
func FormatToolsForPrompt(tools []ToolInfo) string {
	var sb strings.Builder
	for _, tool := range tools {
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\nParameters:\n%s\n\n",
			tool.Name, tool.Description, tool.Parameters))
	}
	return sb.String()
}
