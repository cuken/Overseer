package main

import "github.com/cuken/overseer/pkg/types"

type InitResponse struct {
	Message      string            `json:"message"`
	Path         string            `json:"path"`
	Directories  map[string]string `json:"directories"`
	Instructions []string          `json:"instructions"`
}

type AddResponse struct {
	Message  string `json:"message"`
	Filename string `json:"filename"`
	Location string `json:"location"`
}

type StatusResponse struct {
	Active []*types.Task `json:"active"`
}

type ListResponse struct {
	Active    []*types.Task `json:"active,omitempty"`
	Pending   []*types.Task `json:"pending,omitempty"`
	Review    []*types.Task `json:"review,omitempty"`
	Completed []*types.Task `json:"completed,omitempty"`
}

type ApproveResponse struct {
	Message  string `json:"message"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	NewState string `json:"new_state"`
}

type LogsResponse struct {
	TaskID string            `json:"task_id"`
	Files  map[string]string `json:"files"`
}

type CleanResponse struct {
	Message      string   `json:"message"`
	CleanedTasks []string `json:"cleaned_tasks,omitempty"`
	CleanedLogs  bool     `json:"cleaned_logs"`
}
