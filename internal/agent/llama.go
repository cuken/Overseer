package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cuken/overseer/pkg/types"
)

// LlamaClient interfaces with the llama.cpp server
type LlamaClient struct {
	serverURL   string
	model       string
	maxTokens   int
	temperature float64
	client      *http.Client
}

// NewLlamaClient creates a new llama.cpp client
func NewLlamaClient(cfg types.LlamaConfig) *LlamaClient {
	return &LlamaClient{
		serverURL:   cfg.ServerURL,
		model:       cfg.Model,
		maxTokens:   cfg.MaxTokens,
		temperature: cfg.Temperature,
		client: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for generation
		},
	}
}

// ChatMessage represents a message in the chat format
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a request to the chat completions endpoint
type ChatRequest struct {
	Model       string        `json:"model,omitempty"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

// ChatResponse represents a response from the chat completions endpoint
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// CompletionRequest represents a request to the completions endpoint
type CompletionRequest struct {
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"n_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Stream      bool    `json:"stream"`
}

// CompletionResponse represents a response from the completions endpoint
type CompletionResponse struct {
	Content          string `json:"content"`
	Stop             bool   `json:"stop"`
	TokensEvaluated  int    `json:"tokens_evaluated"`
	TokensPredicted  int    `json:"tokens_predicted"`
	Truncated        bool   `json:"truncated"`
}

// Chat sends a chat completion request
func (c *LlamaClient) Chat(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	req := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.serverURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// Complete sends a completion request (for models without chat support)
func (c *LlamaClient) Complete(ctx context.Context, prompt string, stop []string) (*CompletionResponse, error) {
	req := CompletionRequest{
		Prompt:      prompt,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		Stop:        stop,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.serverURL+"/completion", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	var compResp CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&compResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &compResp, nil
}

// Health checks if the llama.cpp server is available
func (c *LlamaClient) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.serverURL+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("server not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// GetServerInfo retrieves server information
func (c *LlamaClient) GetServerInfo(ctx context.Context) (map[string]interface{}, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.serverURL+"/props", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return info, nil
}
