# Overseer

Autonomous agent orchestration system for local LLMs. Overseer manages coding tasks through a continuous Plan → Implement → Test → Debug loop, automatically handling context window limitations through intelligent agent handoffs.

## Features

### Core Capabilities
- **Continuous Operation**: Daemon watches for new tasks and processes them autonomously
- **Context Handoffs**: When an agent's context fills up, it summarizes progress and hands off to a fresh agent
- **Git Integration**: Each task gets its own branch; completed work is automatically merged
- **Conflict Resolution**: Merge conflicts spawn new tasks for resolution
- **Human-in-the-Loop**: Tasks can require approval before continuing; humans can inject new requests anytime
- **MCP Tool Access**: Agents use MCP servers for file operations, shell commands, git, and web fetching
- **Priority Queue**: Tasks are processed by priority with dependency tracking

### Storage & Persistence
- **Dual-Layer Storage**: SQLite for fast queries + JSONL for git-friendly persistence
- **Content Hashing**: SHA256 hashes detect duplicate tasks and content changes
- **Atomic Writes**: JSONL uses temp file + rename for crash safety

### Scheduling & Gates
- **Due Dates**: Set deadlines with `--due=+2d` or `--due=2026-01-15`
- **Task Deferral**: Hide tasks until ready with `--defer=tomorrow`
- **Gate System**: Block tasks on external conditions (GitHub Actions, PR approval, timers, human input)
- **Overdue Detection**: Query for tasks past their due dates

### Observability
- **Agent Health Tracking**: Worker heartbeats detect stuck agents
- **JSON Output**: All CLI commands support `--json` for programmatic access
- **Structured Logging**: Component-based colored logs with file output
- **Worker Status**: Real-time view of agent states and task assignments

### Git Operations
- **Debounced Commits**: 5-second batching prevents commit spam during agent work
- **Auto-Push**: Optional automatic push to remote
- **Branch Management**: Automatic branch creation, switching, and cleanup
- **Merge Conflict Detection**: Identifies conflicting files and spawns resolution tasks

## Requirements

- Go 1.21+
- [llama.cpp](https://github.com/ggerganov/llama.cpp) server running locally
- Node.js and/or Python (for MCP servers)
- Git

## Installation

```bash
# Clone the repository
git clone https://github.com/cuken/Overseer.git
cd Overseer

# Build
go build -o overseer ./cmd/overseer

# Or install to $GOPATH/bin
go install ./cmd/overseer
```

## Quick Start

### 1. Initialize a Project

```bash
cd /path/to/your/project
overseer init
```

This creates the `.overseer/` directory with:
- `config.yaml` - Configuration file
- `requests/` - Drop task files here
- `tasks/` - Task state storage (SQLite + JSONL)
- `workspaces/` - Agent working directories
- `logs/` - Execution logs

It also updates `.gitignore` to exclude runtime files.

### 2. Start llama.cpp Server

```bash
# In a separate terminal
llama-server -m /path/to/your/model.gguf --port 8080 --ctx-size 32768
```

Recommended models for coding tasks:
- Qwen2.5-Coder-32B-Instruct
- DeepSeek-Coder-V2
- CodeLlama-34B

### 3. Start the Daemon

```bash
overseer daemon
# Or with verbose logging
overseer daemon -v
```

The daemon will:
- Connect to MCP servers defined in config
- Watch for new request files
- Process tasks through the workflow
- Handle handoffs and merges automatically

### 4. Add Tasks

**Option A**: Use the CLI
```bash
# Basic task
overseer add my-feature.md

# With scheduling
overseer add my-feature.md --due=+3d --priority=5

# Deferred task (hidden until date)
overseer add cleanup.md --defer=+1w
```

**Option B**: Drop a markdown file in `.overseer/requests/`
```markdown
---
due: +2d
priority: 3
---
# Add user authentication

Implement JWT-based authentication with:
- Login endpoint at POST /api/auth/login
- Logout endpoint at POST /api/auth/logout
- Middleware to protect routes
- Token refresh mechanism
```

The daemon will automatically pick up new files.

## CLI Commands

### Core Commands

| Command | Description |
|---------|-------------|
| `overseer init` | Initialize Overseer in current directory |
| `overseer daemon [-v]` | Start the background daemon |
| `overseer add <file>` | Add a task request |
| `overseer list` | List all tasks by state |
| `overseer status [id]` | Show task status (all active or specific) |
| `overseer approve <id>` | Approve a task awaiting review |
| `overseer logs <id>` | View task workspace files |
| `overseer clean [id]` | Remove tasks and workspaces |
| `overseer agents` | Show active worker status |

### Gate Commands

| Command | Description |
|---------|-------------|
| `overseer gate list` | List all active gates |
| `overseer gate clear <id>` | Manually clear a gate to unblock task |

### Flags

| Flag | Commands | Description |
|------|----------|-------------|
| `--json` | All | Output in JSON format |
| `-v, --verbose` | daemon | Enable debug logging |
| `-c, --completed` | list | Include completed tasks |
| `--overdue` | list | Show only overdue tasks |
| `--deferred` | list | Show only deferred tasks |
| `--due` | add | Set due date (+2d, 2026-01-15) |
| `--defer` | add | Defer until date |
| `--priority` | add | Set task priority (higher = first) |
| `--branches` | clean | Also delete git branches |
| `-f, --force` | clean | Skip confirmation prompt |

Task IDs can be abbreviated (e.g., `os-a1b2` instead of full UUID).

## Configuration

Edit `.overseer/config.yaml`:

```yaml
llama:
  server_url: "http://localhost:8080"
  context_size: 32768
  handoff_threshold: 0.8  # Trigger handoff at 80% context usage
  model: "default"
  temperature: 0.7
  max_tokens: 4096

workers:
  count: 1              # Number of concurrent workers
  max_handoffs: 10      # Maximum handoffs per task
  idle_timeout_secs: 300

git:
  merge_target: "main"
  branch_prefix: "feature"
  auto_push: true
  sign_commits: false
  debounce_secs: 5      # Batch commits within this window

mcp:
  servers:
    - name: filesystem
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    - name: git
      command: "uvx"
      args: ["mcp-server-git"]
    - name: fetch
      command: "uvx"
      args: ["mcp-server-fetch"]

paths:
  requests: ".overseer/requests"
  tasks: ".overseer/tasks"
  workspaces: ".overseer/workspaces"
  logs: ".overseer/logs"
  source: "."
```

## Task Workflow

```
┌─────────┐     ┌──────────┐     ┌─────────────┐     ┌─────────┐
│ Pending │ ──► │ Planning │ ──► │Implementing │ ──► │ Testing │
└─────────┘     └──────────┘     └─────────────┘     └─────────┘
                     │                                     │
                     ▼                                     ▼
                ┌─────────┐                          ┌──────────┐
                │ Blocked │ ◄──────────────────────  │Debugging │
                └─────────┘                          └──────────┘
                     │                                     │
                     │ (gate clears)                       ▼
                     │                               ┌─────────┐
                     └──────────────────────────────►│ Review  │
                                                     └─────────┘
                                                          │
                                                          ▼
                                                     ┌─────────┐     ┌───────────┐
                                                     │ Merging │ ──► │ Completed │
                                                     └─────────┘     └───────────┘
                                                          │
                                                          ▼ (on conflict)
                                                     ┌──────────┐
                                                     │ Conflict │ ──► Spawns resolution task
                                                     └──────────┘
```

### Task States

| State | Description |
|-------|-------------|
| `pending` | Waiting in queue (respects defer date) |
| `planning` | Agent is analyzing and creating a plan |
| `implementing` | Agent is writing code |
| `testing` | Agent is running tests |
| `debugging` | Agent is fixing test failures |
| `review` | Waiting for human approval |
| `merging` | Attempting to merge to target branch |
| `completed` | Successfully merged |
| `conflict` | Merge conflict detected |
| `blocked` | Waiting on gate (external condition) |

### Gate Types

Gates block tasks until external conditions are met:

| Gate Type | Description |
|-----------|-------------|
| `github-run` | Wait for GitHub Actions workflow |
| `pr-approval` | Wait for pull request approval |
| `timer` | Wait until specified time |
| `human-input` | Wait for manual clearance |

## Context Handoffs

When an agent approaches the context limit (default 80%), it:

1. Writes a handoff summary to `.overseer/workspaces/<task-id>/handoff.yaml`
2. Documents what was accomplished, next steps, and any blockers
3. Exits gracefully

The next agent generation:
1. Reads the handoff file
2. Continues from where the previous agent left off
3. Has fresh context to work with

This allows tasks to run indefinitely regardless of model context limits.

## Storage Architecture

Overseer uses a dual-layer storage system inspired by [beads](https://github.com/beads-project/beads):

```
┌─────────────────────────────────────────┐
│           CLI / Daemon                   │
└─────────────────┬───────────────────────┘
                  │
                  ▼ (immediate writes)
┌─────────────────────────────────────────┐
│     SQLite Database (tasks.db)          │
│  - Fast queries with indexes            │
│  - WAL mode for concurrent access       │
│  - Worker status tracking               │
└─────────────────┬───────────────────────┘
                  │ (debounced sync)
                  ▼
┌─────────────────────────────────────────┐
│      JSONL File (tasks.jsonl)           │
│  - One task per line                    │
│  - Human readable                       │
│  - Git-friendly (merge-safe)            │
└─────────────────────────────────────────┘
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Daemon                               │
│  - Watches requests/ for new tasks                          │
│  - Manages worker pool                                       │
│  - Checks gate expirations                                   │
│  - Handles signals for graceful shutdown                    │
└─────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌──────────┐   ┌──────────┐   ┌──────────┐
        │ Worker 1 │   │ Worker 2 │   │ Worker N │
        │ (health) │   │ (health) │   │ (health) │
        └──────────┘   └──────────┘   └──────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│                         Agent                                │
│  - Communicates with llama.cpp server                       │
│  - Tracks context usage                                      │
│  - Executes tools via MCP                                   │
│  - Manages handoffs                                          │
│  - Updates task state                                        │
└─────────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│                      MCP Servers                             │
│  filesystem │ git │ fetch │ shell (optional)                │
└─────────────────────────────────────────────────────────────┘
```

## JSON Output Examples

All commands support `--json` for scripting:

```bash
# List tasks as JSON
overseer list --json | jq '.active[].title'

# Get task status
overseer status os-a1b2 --json

# Check agent health
overseer agents --json | jq '.[] | select(.state == "stuck")'

# List active gates
overseer gate list --json
```

## Development

```bash
# Run tests
go test ./...

# Build
go build -o overseer ./cmd/overseer

# Build with version info
go build -ldflags "-X main.version=1.0.0" -o overseer ./cmd/overseer
```

## Comparison with Similar Projects

Overseer draws inspiration from [beads](https://github.com/anthropics/beads), a git-backed issue tracker for AI agents:

| Feature | Overseer | beads |
|---------|----------|-------|
| **Purpose** | LLM agent orchestration | Task/issue tracking |
| **Storage** | SQLite + JSONL | SQLite + JSONL |
| **JSON CLI** | `--json` flag | `--json` flag |
| **Git debouncing** | 5-second batching | 5-second batching |
| **Due dates** | Yes | Yes |
| **Deferral** | Yes | Yes |
| **Gates/blocking** | 4 gate types | Similar gate system |
| **Agent health** | Heartbeat tracking | Agent state tracking |
| **Content hashing** | SHA256 | SHA256 |
| **LLM integration** | Full orchestration | None (tracking only) |
| **Context handoffs** | Yes | N/A |
| **MCP tools** | Yes | N/A |
| **Workflow templates** | No | Molecule/formula system |
| **Distributed IDs** | UUID-based | Hash-based (collision-free) |
| **Daemon model** | Single daemon | Per-workspace (LSP-style) |

Overseer is designed for autonomous code generation with LLMs, while beads focuses on task tracking infrastructure for AI-assisted workflows.

## Roadmap

- [x] SQLite + JSONL storage
- [x] Agent health tracking
- [x] Git debouncing
- [x] Gate system
- [x] Time-based scheduling
- [x] JSON CLI output
- [ ] Web UI for task monitoring
- [ ] Multiple model support (different models for different phases)
- [ ] Task templates / workflow formulas
- [ ] Metrics and observability dashboard
- [ ] Per-workspace daemon isolation
- [ ] Hash-based distributed IDs
- [ ] Plugin system for custom tools

## License

MIT

## Contributing

Contributions welcome! Please open an issue to discuss major changes before submitting a PR.
