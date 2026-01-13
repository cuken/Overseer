package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Transport handles communication with an MCP server process
type Transport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	scanner   *bufio.Scanner
	mu        sync.Mutex
	requestID atomic.Int64
	responses map[int64]chan *JSONRPCResponse
	respMu    sync.RWMutex
	closed    bool
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
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// NewTransport creates a new transport for an MCP server
func NewTransport(command string, args []string, env []string) (*Transport, error) {
	cmd := exec.Command(command, args...)
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

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	t := &Transport{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		scanner:   bufio.NewScanner(stdout),
		responses: make(map[int64]chan *JSONRPCResponse),
	}

	// Start response reader
	go t.readResponses()

	return t, nil
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

// Close shuts down the transport
func (t *Transport) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true

	t.stdin.Close()
	t.stdout.Close()

	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}

	return t.cmd.Wait()
}
