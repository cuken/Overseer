package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cuken/overseer/internal/config"
	"github.com/cuken/overseer/internal/daemon"
	"github.com/cuken/overseer/internal/git"
	"github.com/cuken/overseer/internal/task"
	"github.com/cuken/overseer/pkg/types"
)

var (
	version    = "dev"
	commit     = "none"
	jsonOutput bool
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
			if !jsonOutput {
				fmt.Println("Initializing git repository...")
			}
			if err := g.Init(); err != nil {
				return fmt.Errorf("failed to init git: %w", err)
			}
		}

		// Ensure repo has at least one commit (required for branching)
		if !g.HasCommits() {
			if !jsonOutput {
				fmt.Println("Creating initial commit...")
			}
			if err := g.CommitAllowEmpty("Hic sunt dracones"); err != nil {
				return fmt.Errorf("failed to create initial commit: %w", err)
			}
		}

		if jsonOutput {
			resp := InitResponse{
				Message: fmt.Sprintf("Initialized overseer in %s", cwd),
				Path:    cwd,
				Directories: map[string]string{
					"requests":   cfg.Paths.Requests,
					"tasks":      cfg.Paths.Tasks,
					"workspaces": cfg.Paths.Workspaces,
					"logs":       cfg.Paths.Logs,
					"source":     cfg.Paths.Source,
				},
				Instructions: []string{
					"Edit .overseer/config.yaml to customize settings.",
					"Drop .md files in .overseer/requests/ to add tasks.",
					"1. Start llama.cpp server: llama-server -m <model> --port 8080",
					"2. Run the daemon: overseer daemon",
					"3. Add a task: overseer add task.md",
				},
			}
			return printJSON(resp)
		}

		fmt.Println("Initialized overseer in", cwd)
		fmt.Println("\nCreated directories:")
		fmt.Printf("  %s/\n", cfg.Paths.Requests)
		fmt.Printf("  %s/\n", cfg.Paths.Tasks)
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

		if jsonOutput {
			return printJSON(AddResponse{
				Message:  "Added request successfully",
				Filename: filename,
				Location: dstPath,
			})
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

		store, err := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()

		if len(args) > 0 {
			// Show specific task
			t, err := store.LoadByPrefix(args[0])
			if err != nil {
				return fmt.Errorf("task not found: %w", err)
			}
			if jsonOutput {
				return printJSON(t)
			}
			printTaskDetails(t)
		} else {
			// Show all active tasks
			active, err := store.ListActive()
			if err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(StatusResponse{Active: active})
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

		store, err := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()

		showCompleted, _ := cmd.Flags().GetBool("completed")

		// List by category
		if jsonOutput {
			resp := ListResponse{}
			if tasks, err := store.ListActive(); err == nil {
				resp.Active = tasks
			}
			if tasks, err := store.ListPending(); err == nil {
				resp.Pending = tasks
			}
			if tasks, err := store.ListReview(); err == nil {
				resp.Review = tasks
			}
			if showCompleted {
				if tasks, err := store.ListByState(types.StateCompleted); err == nil {
					resp.Completed = tasks
				}
			}
			return printJSON(resp)
		}

		categories := []struct {
			name   string
			loader func() ([]*types.Task, error)
		}{
			{"Active", store.ListActive},
			{"Pending", store.ListPending},
			{"Review", store.ListReview},
		}

		if showCompleted {
			categories = append(categories, struct {
				name   string
				loader func() ([]*types.Task, error)
			}{"Completed", func() ([]*types.Task, error) {
				return store.ListByState(types.StateCompleted)
			}})
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

		store, err := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()

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
		case types.PhaseTest, types.PhaseDebug:
			// If we're approving a task in test/debug phase, it means the work is accepted
			// and ready to be merged.
			newState = types.StateMerging
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

		if jsonOutput {
			return printJSON(ApproveResponse{
				Message:  "Approved task",
				TaskID:   t.ID,
				Title:    t.Title,
				NewState: string(t.State),
			})
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

		store, err := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()
		t, err := store.LoadByPrefix(args[0])
		if err != nil {
			return fmt.Errorf("task not found: %w", err)
		}

		// Check workspace for context files
		workspaceDir := filepath.Join(projectDir, cfg.Paths.Workspaces, t.ID)

		files := []string{"plan.md", "context.md", "handoff.yaml"}

		if jsonOutput {
			resp := LogsResponse{
				TaskID: t.ID,
				Files:  make(map[string]string),
			}
			for _, f := range files {
				path := filepath.Join(workspaceDir, f)
				if content, err := os.ReadFile(path); err == nil {
					resp.Files[f] = string(content)
				}
			}
			return printJSON(resp)
		}

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

var cleanCmd = &cobra.Command{
	Use:   "clean [task-id]",
	Short: "Clean tasks and workspaces",
	Long: `Removes task files and workspaces. If no task-id is provided, cleans ALL tasks.
This will:
- Remove task files from all state directories
- Delete workspace directories
- Remove task-specific log files
- Optionally delete git branches (with --branches flag)

WARNING: This operation cannot be undone!`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := config.GetProjectDir()
		if err != nil {
			return fmt.Errorf("failed to find project directory: %w", err)
		}

		cfg, err := config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store, err := task.NewStore(filepath.Join(projectDir, cfg.Paths.Tasks))
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()
		gitClient := git.New(projectDir)

		// Get flags
		cleanBranches, _ := cmd.Flags().GetBool("branches")
		force, _ := cmd.Flags().GetBool("force")

		if len(args) == 0 {
			// Clean all tasks
			if !force && !jsonOutput {
				fmt.Print("This will remove ALL tasks and workspaces. Are you sure? (yes/no): ")
				var response string
				fmt.Scanln(&response)
				if response != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			return cleanAll(store, projectDir, cfg, gitClient, cleanBranches, jsonOutput)
		}

		// Clean specific task
		taskID := args[0]
		t, err := store.LoadByPrefix(taskID)
		if err != nil {
			return fmt.Errorf("task not found: %w", err)
		}

		if err := cleanTask(t, store, projectDir, cfg, gitClient, cleanBranches, jsonOutput); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(CleanResponse{
				Message:      "Task cleaned successfully",
				CleanedTasks: []string{t.ID},
			})
		}
		return nil
	},
}

func cleanTask(t *types.Task, store *task.Store, projectDir string, cfg *types.Config, gitClient *git.Git, cleanBranches bool, quiet bool) error {
	if !quiet {
		fmt.Printf("Cleaning task %s (%s)...\n", t.ID[:8], t.Title)
	}

	// Remove task file from current state directory
	if err := store.Delete(t); err != nil && !quiet {
		fmt.Printf("Warning: failed to delete task file: %v\n", err)
	}

	// Remove workspace directory
	workspaceDir := filepath.Join(projectDir, cfg.Paths.Workspaces, t.ID)
	if err := os.RemoveAll(workspaceDir); err != nil {
		if !quiet {
			fmt.Printf("Warning: failed to delete workspace: %v\n", err)
		}
	} else if !quiet {
		fmt.Printf("  ✓ Removed workspace\n")
	}

	// Remove task-specific log file
	logFile := filepath.Join(projectDir, cfg.Paths.Logs, fmt.Sprintf("agent-%s.log", t.ID[:8]))
	if err := os.Remove(logFile); err != nil && !os.IsNotExist(err) {
		if !quiet {
			fmt.Printf("Warning: failed to delete log file: %v\n", err)
		}
	} else if err == nil && !quiet {
		fmt.Printf("  ✓ Removed log file\n")
	}

	// Remove git branch if requested
	if cleanBranches && t.Branch != "" {
		if gitClient.BranchExists(t.Branch) {
			// Switch to main/master first
			mainBranch := cfg.Git.MergeTarget
			if mainBranch == "" {
				mainBranch = "main"
			}
			gitClient.Checkout(mainBranch)

			if err := gitClient.DeleteBranch(t.Branch); err != nil {
				if !quiet {
					fmt.Printf("Warning: failed to delete branch %s: %v\n", t.Branch, err)
				}
			} else if !quiet {
				fmt.Printf("  ✓ Removed branch %s\n", t.Branch)
			}
		}
	}

	if !quiet {
		fmt.Printf("Task %s cleaned successfully\n", t.ID[:8])
	}
	return nil
}

func cleanAll(store *task.Store, projectDir string, cfg *types.Config, gitClient *git.Git, cleanBranches bool, quiet bool) error {
	if !quiet {
		fmt.Println("Cleaning all tasks...")
	}

	// Collect all tasks from all states
	var allTasks []*types.Task

	states := []struct {
		name   string
		loader func() ([]*types.Task, error)
	}{
		{"pending", store.ListPending},
		{"active", store.ListActive},
		{"review", store.ListReview},
		{"completed", func() ([]*types.Task, error) {
			return store.ListByState(types.StateCompleted)
		}},
	}

	for _, state := range states {
		tasks, err := state.loader()
		if err != nil {
			if !quiet {
				fmt.Printf("Warning: failed to load %s tasks: %v\n", state.name, err)
			}
			continue
		}
		allTasks = append(allTasks, tasks...)
		if !quiet {
			fmt.Printf("Found %d %s tasks\n", len(tasks), state.name)
		}
	}

	if len(allTasks) == 0 {
		if !quiet {
			fmt.Println("No tasks found to clean")
		}
		if quiet {
			// Return empty success
			return printJSON(CleanResponse{Message: "No tasks found to clean"})
		}
		return nil
	}

	if !quiet {
		fmt.Printf("\nCleaning %d tasks...\n", len(allTasks))
	}

	var cleanedIDs []string
	// Clean each task
	for _, t := range allTasks {
		if err := cleanTask(t, store, projectDir, cfg, gitClient, cleanBranches, quiet); err != nil {
			if !quiet {
				fmt.Printf("Warning: failed to clean task %s: %v\n", t.ID[:8], err)
			}
		} else {
			cleanedIDs = append(cleanedIDs, t.ID)
		}
	}

	// Clean all workspaces (in case there are orphaned ones)
	workspacesDir := filepath.Join(projectDir, cfg.Paths.Workspaces)
	if err := os.RemoveAll(workspacesDir); err != nil {
		if !quiet {
			fmt.Printf("Warning: failed to remove workspaces directory: %v\n", err)
		}
	}
	os.MkdirAll(workspacesDir, 0755)
	if !quiet {
		fmt.Println("  ✓ Cleaned all workspaces")
	}

	// Clean all agent log files
	logsDir := filepath.Join(projectDir, cfg.Paths.Logs)
	entries, err := os.ReadDir(logsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" {
				name := entry.Name()
				if strings.HasPrefix(name, "agent-") || name == "errors.log" {
					logPath := filepath.Join(logsDir, name)
					os.Remove(logPath)
				}
			}
		}
		if !quiet {
			fmt.Println("  ✓ Cleaned agent log files")
		}
	}

	if !quiet {
		fmt.Printf("\n✓ Successfully cleaned all tasks\n")
	} else {
		return printJSON(CleanResponse{
			Message:      "Successfully cleaned all tasks",
			CleanedTasks: cleanedIDs,
			CleanedLogs:  true,
		})
	}
	return nil
}

func init() {
	cleanCmd.Flags().Bool("branches", false, "Also delete git branches for cleaned tasks")
	cleanCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt (use with caution)")

	listCmd.Flags().BoolP("completed", "c", false, "Include completed tasks")

	daemonCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(approveCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(cleanCmd)
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

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
