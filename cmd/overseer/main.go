package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cuken/overseer/internal/config"
	"github.com/cuken/overseer/internal/daemon"
	"github.com/cuken/overseer/internal/git"
	"github.com/cuken/overseer/internal/task"
	"github.com/cuken/overseer/pkg/types"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "overseer",
	Short: "Autonomous agent orchestration system",
	Long: `Overseer orchestrates local LLM agents in a continuous loop with
intelligent context passing, git branch management, and human-in-the-loop capabilities.

It manages tasks through a Plan -> Implement -> Test -> Debug cycle,
automatically handling context window limitations through agent handoffs.`,
	Version: fmt.Sprintf("%s (commit: %s)", version, commit),
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the overseer daemon",
	Long:  `Starts the background daemon that watches for new requests and orchestrates agent workers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		d, err := daemon.New(projectDir)
		if err != nil {
			return fmt.Errorf("failed to create daemon: %w", err)
		}

		d.SetVerbose(verbose)

		return d.Run(context.Background())
	},
}

var verbose bool

func init() {
	daemonCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize overseer in current directory",
	Long:  `Creates the .overseer directory structure and default configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}

		cfg, err := config.Load(cwd)
		if err != nil {
			return fmt.Errorf("failed to load/create config: %w", err)
		}

		if err := config.EnsureDirectories(cwd, cfg); err != nil {
			return fmt.Errorf("failed to create directories: %w", err)
		}

		// Initialize git if needed
		g := git.New(cwd)
		if !g.IsRepo() {
			fmt.Println("Initializing git repository...")
			if err := g.Init(); err != nil {
				return fmt.Errorf("failed to init git: %w", err)
			}
			// Create initial commit to establish main/master branch
			if err := g.CommitAllowEmpty("Initial commit"); err != nil {
				return fmt.Errorf("failed to create initial commit: %w", err)
			}
		}

		fmt.Println("Initialized overseer in", cwd)
		fmt.Println("\nCreated directories:")
		fmt.Printf("  %s/\n", cfg.Paths.Requests)
		fmt.Printf("  %s/{active,pending,review,completed}/\n", cfg.Paths.Tasks)
		fmt.Printf("  %s/\n", cfg.Paths.Workspaces)
		fmt.Printf("  %s/\n", cfg.Paths.Logs)
		fmt.Printf("  %s/\n", cfg.Paths.Source)
		fmt.Println("\nEdit .overseer/config.yaml to customize settings.")
		fmt.Println("Drop .md files in .overseer/requests/ to add tasks.")
		fmt.Println("  1. Start llama.cpp server: llama-server -m <model> --port 8080")
		fmt.Println("  2. Run the daemon: overseer daemon")
		fmt.Println("  3. Add a task: overseer add task.md")
		return nil
	},
}

var addCmd = &cobra.Command{
	Use:   "add <file.md>",
	Short: "Add a new task request",
	Long:  `Adds a markdown file as a new task request. The file will be copied to the requests directory.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		srcPath := args[0]
		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		// Copy to requests directory with original filename
		filename := filepath.Base(srcPath)
		dstPath := filepath.Join(projectDir, cfg.Paths.Requests, filename)
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write request: %w", err)
		}

		fmt.Printf("Added request: %s\n", filename)
		fmt.Printf("Location: %s\n", dstPath)
		fmt.Println("\nThe daemon will pick this up automatically if running.")
		fmt.Println("Run 'overseer list' to see task status.")
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status [task-id]",
	Short: "Show task status",
	Long:  `Shows the status of a specific task or all active tasks if no ID is provided.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))

		if len(args) > 0 {
			// Show specific task
			t, err := store.LoadByPrefix(args[0])
			if err != nil {
				return fmt.Errorf("task not found: %w", err)
			}
			printTaskDetails(t)
		} else {
			// Show all active tasks
			active, err := store.ListActive()
			if err != nil {
				return err
			}

			if len(active) == 0 {
				fmt.Println("No active tasks")
				return nil
			}

			fmt.Println("Active Tasks:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATE\tPHASE\tTITLE\tHANDOFFS")
			for _, t := range active {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
					t.ID[:8], t.State, t.Phase, truncate(t.Title, 40), t.Handoffs)
			}
			w.Flush()
		}
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tasks",
	Long:  `Lists all tasks grouped by their current state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))

		// List by category
		categories := []struct {
			name   string
			loader func() ([]*types.Task, error)
		}{
			{"Active", store.ListActive},
			{"Pending", store.ListPending},
			{"Review", store.ListReview},
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		for _, cat := range categories {
			tasks, err := cat.loader()
			if err != nil {
				continue
			}
			if len(tasks) == 0 {
				continue
			}

			fmt.Printf("\n%s (%d):\n", cat.name, len(tasks))
			fmt.Fprintln(w, "  ID\tSTATE\tPHASE\tPRIORITY\tTITLE")
			for _, t := range tasks {
				fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%s\n",
					t.ID[:8], t.State, t.Phase, t.Priority, truncate(t.Title, 40))
			}
			w.Flush()
		}

		return nil
	},
}

var approveCmd = &cobra.Command{
	Use:   "approve <task-id>",
	Short: "Approve a task awaiting review",
	Long:  `Approves a task that is waiting for human review, allowing it to continue processing.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))

		t, err := store.LoadByPrefix(args[0])
		if err != nil {
			return fmt.Errorf("task not found: %w", err)
		}

		if t.State != types.StateReview {
			return fmt.Errorf("task is not awaiting review (current state: %s)", t.State)
		}

		// Clear approval flag and transition back to planning/implementing
		t.RequiresApproval = false
		oldState := t.State

		// Determine next state based on phase
		var newState types.TaskState
		switch t.Phase {
		case types.PhasePlan:
			newState = types.StatePlanning
		case types.PhaseImplement:
			newState = types.StateImplementing
		case types.PhaseTest:
			newState = types.StateTesting
		case types.PhaseDebug:
			newState = types.StateDebugging
		default:
			// If phase is unknown or "review", default to planning and reset phase to "plan"
			newState = types.StatePlanning
			t.Phase = types.PhasePlan
		}

		if err := task.TransitionTo(t, newState); err != nil {
			return fmt.Errorf("failed to transition task: %w", err)
		}

		if err := store.Move(t, oldState, newState); err != nil {
			return fmt.Errorf("failed to save task: %w", err)
		}

		fmt.Printf("Approved task %s\n", t.ID[:8])
		fmt.Printf("  Title: %s\n", t.Title)
		fmt.Printf("  New state: %s\n", t.State)
		fmt.Println("\nThe daemon will continue processing this task.")
		return nil
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "View task logs",
	Long:  `Displays or tails the log file for a specific task.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		t, err := store.LoadByPrefix(args[0])
		if err != nil {
			return fmt.Errorf("task not found: %w", err)
		}

		// Check workspace for context files
		workspaceDir := filepath.Join(projectDir, cfg.Paths.Workspaces, t.ID)

		files := []string{"plan.md", "context.md", "handoff.yaml"}
		for _, f := range files {
			path := filepath.Join(workspaceDir, f)
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("=== %s ===\n", f)
				content, _ := os.ReadFile(path)
				fmt.Println(string(content))
				fmt.Println()
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(approveCmd)
	rootCmd.AddCommand(logsCmd)
}

func printTaskDetails(t *types.Task) {
	fmt.Printf("Task: %s\n", t.ID)
	fmt.Printf("  Title:       %s\n", t.Title)
	fmt.Printf("  State:       %s\n", t.State)
	fmt.Printf("  Phase:       %s\n", t.Phase)
	fmt.Printf("  Branch:      %s\n", t.Branch)
	fmt.Printf("  Priority:    %d\n", t.Priority)
	fmt.Printf("  Handoffs:    %d\n", t.Handoffs)
	fmt.Printf("  Created:     %s\n", t.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated:     %s\n", t.UpdatedAt.Format("2006-01-02 15:04:05"))
	if t.RequiresApproval {
		fmt.Printf("  Approval:    REQUIRED\n")
	}
	if len(t.Dependencies) > 0 {
		fmt.Printf("  Dependencies: %v\n", t.Dependencies)
	}
	if len(t.ConflictFiles) > 0 {
		fmt.Printf("  Conflicts:   %v\n", t.ConflictFiles)
	}
	fmt.Printf("\nDescription:\n%s\n", t.Description)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
