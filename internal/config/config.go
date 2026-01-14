package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cuken/overseer/pkg/types"
	"github.com/spf13/viper"
)

const (
	// DefaultConfigDir is the default directory for overseer state
	DefaultConfigDir = ".overseer"
	// ConfigFileName is the config file name without extension
	ConfigFileName = "config"
)

// Load reads configuration from the .overseer directory
func Load(projectDir string) (*types.Config, error) {
	configDir := filepath.Join(projectDir, DefaultConfigDir)

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	v := viper.New()
	v.SetConfigName(ConfigFileName)
	v.SetConfigType("yaml")
	v.AddConfigPath(configDir)

	// Set defaults from types.DefaultConfig()
	defaults := types.DefaultConfig()
	setDefaults(v, defaults)

	// Try to read config file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found, create one with defaults
			configPath := filepath.Join(configDir, ConfigFileName+".yaml")
			if err := WriteDefault(configPath); err != nil {
				return nil, fmt.Errorf("failed to write default config: %w", err)
			}
			// Re-read the newly created config
			if err := v.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("failed to read new config: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	var cfg types.Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// WriteDefault writes the default configuration to a file
func WriteDefault(path string) error {
	defaults := types.DefaultConfig()

	v := viper.New()
	setDefaults(v, defaults)

	// Add default MCP servers
	// Note: git and fetch are Python-based and use uvx, filesystem is npm-based and uses npx
	v.Set("mcp.servers", []map[string]interface{}{
		{
			"name":    "filesystem",
			"command": "npx",
			"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
		},
		{
			"name":    "git",
			"command": "uvx",
			"args":    []string{"mcp-server-git"},
		},
		{
			"name":    "fetch",
			"command": "uvx",
			"args":    []string{"mcp-server-fetch"},
		},
	})

	return v.WriteConfigAs(path)
}

func setDefaults(v *viper.Viper, cfg *types.Config) {
	// Llama defaults
	v.SetDefault("llama.server_url", cfg.Llama.ServerURL)
	v.SetDefault("llama.context_size", cfg.Llama.ContextSize)
	v.SetDefault("llama.handoff_threshold", cfg.Llama.HandoffThreshold)
	v.SetDefault("llama.model", cfg.Llama.Model)
	v.SetDefault("llama.temperature", cfg.Llama.Temperature)
	v.SetDefault("llama.max_tokens", cfg.Llama.MaxTokens)

	// Git defaults
	v.SetDefault("git.merge_target", cfg.Git.MergeTarget)
	v.SetDefault("git.branch_prefix", cfg.Git.BranchPrefix)
	v.SetDefault("git.auto_push", cfg.Git.AutoPush)
	v.SetDefault("git.sign_commits", cfg.Git.SignCommits)
	v.SetDefault("git.debounce_secs", cfg.Git.DebounceSecs)

	// Worker defaults
	v.SetDefault("workers.count", cfg.Workers.Count)
	v.SetDefault("workers.max_handoffs", cfg.Workers.MaxHandoffs)
	v.SetDefault("workers.idle_timeout_secs", cfg.Workers.IdleTimeoutSecs)

	// Path defaults
	v.SetDefault("paths.requests", cfg.Paths.Requests)
	v.SetDefault("paths.tasks", cfg.Paths.Tasks)
	v.SetDefault("paths.workspaces", cfg.Paths.Workspaces)
	v.SetDefault("paths.logs", cfg.Paths.Logs)
	v.SetDefault("paths.source", cfg.Paths.Source)
}

// EnsureDirectories creates all required directories for overseer operation
func EnsureDirectories(projectDir string, cfg *types.Config) error {
	dirs := []string{
		filepath.Join(projectDir, cfg.Paths.Requests),
		filepath.Join(projectDir, cfg.Paths.Tasks),
		filepath.Join(projectDir, cfg.Paths.Workspaces),
		filepath.Join(projectDir, cfg.Paths.Logs),
	}

	// Only create source directory if it's not "." (project root)
	if cfg.Paths.Source != "." && cfg.Paths.Source != "" {
		dirs = append(dirs, filepath.Join(projectDir, cfg.Paths.Source))
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// EnsureGitignore adds necessary entries to .gitignore for overseer
func EnsureGitignore(projectDir string) error {
	gitignorePath := filepath.Join(projectDir, ".gitignore")

	// Entries that should be in .gitignore
	entries := []string{
		"# Overseer - autonomous agent orchestration",
		".overseer/",
		"!.overseer/config.yaml",
		"!.overseer/tasks/tasks.jsonl",
		"!.overseer/requests/",
		".overseer/requests/*.md",
		"!.overseer/requests/.gitkeep",
	}

	// Read existing .gitignore
	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	// Check which entries are missing
	var toAdd []string
	for _, entry := range entries {
		if !containsLine(existing, entry) {
			toAdd = append(toAdd, entry)
		}
	}

	if len(toAdd) == 0 {
		return nil // All entries already present
	}

	// Append missing entries
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	// Add newline if file doesn't end with one
	if existing != "" && !endsWithNewline(existing) {
		f.WriteString("\n")
	}

	for _, entry := range toAdd {
		f.WriteString(entry + "\n")
	}

	return nil
}

// containsLine checks if a string contains a specific line
func containsLine(content, line string) bool {
	for _, l := range splitLines(content) {
		if l == line {
			return true
		}
	}
	return false
}

// splitLines splits content by newlines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// endsWithNewline checks if string ends with newline
func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}

// GetProjectDir finds the project root by looking for .overseer or .git
func GetProjectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	dir := cwd
	for {
		// Check for .overseer directory
		overseerDir := filepath.Join(dir, DefaultConfigDir)
		if info, err := os.Stat(overseerDir); err == nil && info.IsDir() {
			return dir, nil
		}

		// Check for .git directory
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			return dir, nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root, use current working directory
			return cwd, nil
		}
		dir = parent
	}
}
