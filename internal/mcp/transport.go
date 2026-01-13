package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Transport handles communication with an MCP server process
type Transport struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	scanner        *bufio.Scanner
	mu             sync.Mutex
	requestID      atomic.Int64
	responses      map[int64]chan *JSONRPCResponse
	respMu         sync.RWMutex
	closed         bool
	stderr         io.ReadCloser
	requestHandler func(context.Context, *JSONRPCRequest) (interface{}, error)
}

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// NewTransport creates a new transport for an MCP server
// workingDir sets the working directory for the spawned process
func NewTransport(command string, args []string, env []string, workingDir string) (*Transport, error) {
	cmd := exec.Command(command, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	t := &Transport{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		scanner:   bufio.NewScanner(stdout),
		responses: make(map[int64]chan *JSONRPCResponse),
	}

	// Start response reader
	go t.readResponses()
	// Start stderr logger
	go t.logStderr()

	return t, nil
}

// SetRequestHandler sets the handler for incoming requests
func (t *Transport) SetRequestHandler(handler func(context.Context, *JSONRPCRequest) (interface{}, error)) {
	t.requestHandler = handler
}

// Send sends a request and waits for a response
func (t *Transport) Send(ctx context.Context, method string, params interface{}) (*JSONRPCResponse, error) {
	if t.closed {
		return nil, fmt.Errorf("transport is closed")
	}

	id := t.requestID.Add(1)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Create response channel
	respChan := make(chan *JSONRPCResponse, 1)
	t.respMu.Lock()
	t.responses[id] = respChan
	t.respMu.Unlock()

	defer func() {
		t.respMu.Lock()
		delete(t.responses, id)
		t.respMu.Unlock()
	}()

	// Send request
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	t.mu.Lock()
	_, err = fmt.Fprintf(t.stdin, "%s\n", data)
	t.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-respChan:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Notify sends a notification (no response expected)
func (t *Transport) Notify(method string, params interface{}) error {
	if t.closed {
		return fmt.Errorf("transport is closed")
	}

	req := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	t.mu.Lock()
	_, err = fmt.Fprintf(t.stdin, "%s\n", data)
	t.mu.Unlock()

	return err
}

func (t *Transport) readResponses() {
	for t.scanner.Scan() {
		line := t.scanner.Text()
		if line == "" {
			continue
		}

		// Try parsing as generic JSON to determine type
		var generic map[string]interface{}
		if err := json.Unmarshal([]byte(line), &generic); err != nil {
			continue
		}

		// Check if it's a request (has "method")
		if _, isRequest := generic["method"]; isRequest {
			var req JSONRPCRequest
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			go t.handleRequest(&req)
			continue
		}

		// Otherwise assume it's a response
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		t.respMu.RLock()
		ch, ok := t.responses[resp.ID]
		t.respMu.RUnlock()

		if ok {
			select {
			case ch <- &resp:
			default:
			}
		}
	}
}

func (t *Transport) handleRequest(req *JSONRPCRequest) {
	if t.requestHandler == nil {
		return
	}

	result, err := t.requestHandler(context.Background(), req)

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	if err != nil {
		resp.Error = &JSONRPCError{
			Code:    -32000,
			Message: err.Error(),
		}
	} else {
		resBytes, _ := json.Marshal(result)
		resp.Result = resBytes
	}

	data, _ := json.Marshal(resp)

	t.mu.Lock()
	fmt.Fprintf(t.stdin, "%s\n", data)
	t.mu.Unlock()
}

func (t *Transport) logStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		log.Printf("[MCP %p stderr] %s", t, scanner.Text())
	}
}

// Close shuts down the transport
func (t *Transport) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true

	t.stdin.Close()
	t.stdout.Close()
	t.stderr.Close()

	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}

	return t.cmd.Wait()
}
