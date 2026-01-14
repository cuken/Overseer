package agent

import (
	"testing"

	"github.com/cuken/overseer/internal/logger"
)

func TestParseResponse_SingleToolCall(t *testing.T) {
	a := &Agent{
		log: logger.New("TestAgent", ""),
	}
	a.log.SetVerbose(true)

	content := `<thinking>I should list the directory.</thinking>
<tool_calls>
{"name": "list_directory", "arguments": {"path": "/foo/bar"}}
</tool_calls>`

	// We need to initialize regexps if they are not global, but they seem to be local to parseResponse in the code I saw?
	// No, they are inside parseResponse.

	resp, err := a.parseResponse(content)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(resp.ToolCalls))
	} else {
		call := resp.ToolCalls[0]
		if call.Name != "list_directory" {
			t.Errorf("Expected tool name list_directory, got %s", call.Name)
		}
		if call.ID == "" {
			t.Errorf("Expected tool call to have an auto-generated ID")
		}
	}
}

func TestParseResponse_ArrayToolCalls(t *testing.T) {
	a := &Agent{
		log: logger.New("TestAgent", ""),
	}

	content := `<tool_calls>
[
	{"name": "read_file", "arguments": {"path": "a.go"}},
	{"name": "read_file", "arguments": {"path": "b.go"}}
]
</tool_calls>`

	resp, err := a.parseResponse(content)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(resp.ToolCalls) != 2 {
		t.Errorf("Expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
}
func TestParseResponse_UserReportedSingleToolCall(t *testing.T) {
	a := &Agent{
		log: logger.New("TestAgent", ""),
	}

	content := `<tool_calls>{"name": "list_directory", "arguments": {"path": "/home/cuken/Code/github/cuken/hello_world"}}</tool_calls>`

	resp, err := a.parseResponse(content)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(resp.ToolCalls))
	} else {
		if resp.ToolCalls[0].Name != "list_directory" {
			t.Errorf("Expected list_directory, got %s", resp.ToolCalls[0].Name)
		}
	}
}
