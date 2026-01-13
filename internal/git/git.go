package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Git provides operations on a git repository
type Git struct {
	repoDir string
}

// New creates a new Git instance for the given repository directory
func New(repoDir string) *Git {
	return &Git{repoDir: repoDir}
}

// IsRepo checks if the directory is a git repository
func (g *Git) IsRepo() bool {
	_, err := g.run("rev-parse", "--git-dir")
	return err == nil
}

// Init initializes a new git repository
func (g *Git) Init() error {
	_, err := g.run("init")
	return err
}

// HasCommits checks if the repository has any commits
func (g *Git) HasCommits() bool {
	_, err := g.run("rev-parse", "HEAD")
	return err == nil
}

// CurrentBranch returns the name of the current branch
func (g *Git) CurrentBranch() (string, error) {
	out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BranchExists checks if a branch exists
func (g *Git) BranchExists(branch string) bool {
	_, err := g.run("rev-parse", "--verify", branch)
	return err == nil
}

// Checkout switches to the specified branch
func (g *Git) Checkout(branch string) error {
	_, err := g.run("checkout", branch)
	return err
}

// CheckoutNew creates and switches to a new branch
func (g *Git) CheckoutNew(branch string) error {
	_, err := g.run("checkout", "-b", branch)
	return err
}

// Add stages files for commit
func (g *Git) Add(paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, err := g.run(args...)
	return err
}

// AddAll stages all changes
func (g *Git) AddAll() error {
	_, err := g.run("add", "-A")
	return err
}

// Commit creates a new commit with the given message
// Uses stdin to handle multi-line messages and special characters safely
func (g *Git) Commit(message string) error {
	_, err := g.runWithInput(message, "commit", "-F", "-")
	return err
}

// CommitAllowEmpty creates a commit even if there are no changes
func (g *Git) CommitAllowEmpty(message string) error {
	_, err := g.run("commit", "--allow-empty", "-m", message)
	return err
}

// Push pushes the current branch to origin
func (g *Git) Push() error {
	_, err := g.run("push")
	return err
}

// PushSetUpstream pushes and sets upstream tracking
func (g *Git) PushSetUpstream(branch string) error {
	_, err := g.run("push", "-u", "origin", branch)
	return err
}

// Pull pulls the latest changes from origin
func (g *Git) Pull() error {
	_, err := g.run("pull")
	return err
}

// Fetch fetches from origin
func (g *Git) Fetch() error {
	_, err := g.run("fetch")
	return err
}

// Status returns the current status
func (g *Git) Status() (string, error) {
	return g.run("status", "--porcelain")
}

// HasChanges checks if there are uncommitted changes (staged or unstaged)
func (g *Git) HasChanges() (bool, error) {
	status, err := g.Status()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

// HasStagedChanges checks if there are staged changes ready to commit
func (g *Git) HasStagedChanges() (bool, error) {
	out, err := g.run("diff", "--cached", "--quiet")
	if err != nil {
		// Exit code 1 means there ARE staged changes
		if strings.Contains(err.Error(), "exit status 1") {
			return true, nil
		}
		return false, err
	}
	// Exit code 0 means no staged changes
	return out != "", nil
}

// Stash stashes current changes
func (g *Git) Stash() error {
	_, err := g.run("stash")
	return err
}

// StashPop pops the last stash
func (g *Git) StashPop() error {
	_, err := g.run("stash", "pop")
	return err
}

// Log returns recent commit history
func (g *Git) Log(count int) (string, error) {
	return g.run("log", fmt.Sprintf("-%d", count), "--oneline")
}

// Diff returns the diff of uncommitted changes
func (g *Git) Diff() (string, error) {
	return g.run("diff")
}

// DiffStaged returns the diff of staged changes
func (g *Git) DiffStaged() (string, error) {
	return g.run("diff", "--staged")
}

// DeleteBranch deletes a local branch
func (g *Git) DeleteBranch(branch string) error {
	_, err := g.run("branch", "-D", branch)
	return err
}

// DeleteRemoteBranch deletes a remote branch
func (g *Git) DeleteRemoteBranch(branch string) error {
	_, err := g.run("push", "origin", "--delete", branch)
	return err
}

// RemoteURL returns the origin remote URL
func (g *Git) RemoteURL() (string, error) {
	out, err := g.run("remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasRemote checks if origin remote exists
func (g *Git) HasRemote() bool {
	_, err := g.run("remote", "get-url", "origin")
	return err == nil
}

// run executes a git command and returns the output
func (g *Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.repoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}

	return stdout.String(), nil
}

// runWithInput executes a git command with stdin input
func (g *Git) runWithInput(input string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.repoDir
	cmd.Stdin = strings.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}

	return stdout.String(), nil
}
