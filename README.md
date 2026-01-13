# Overseer

Autonomous agent orchestration system for local LLMs. Overseer manages coding tasks through a continuous Plan → Implement → Test → Debug loop, automatically handling context window limitations through intelligent agent handoffs.

## Features

- **Continuous Operation**: Daemon watches for new tasks and processes them autonomously
- **Context Handoffs**: When an agent's context fills up, it summarizes progress and hands off to a fresh agent
- **Git Integration**: Each task gets its own branch; completed work is automatically merged
- **Conflict Resolution**: Merge conflicts spawn new tasks for resolution
- **Human-in-the-Loop**: Tasks can require approval before continuing; humans can inject new requests anytime
- **MCP Tool Access**: Agents use MCP servers for file operations, shell commands, git, and web fetching
- **Priority Queue**: Tasks are processed by priority with dependency tracking

## Requirements

- Go 1.21+
- [llama.cpp](https://github.com/ggerganov/llama.cpp) server running locally
- Node.js (for MCP servers)
- Git

## Installation

```bash
# Clone the repository
git clone https://github.com/cuken/Overseer.git
cd Overseer

# Build
make build

# Or install to $GOPATH/bin
make install
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
- `tasks/` - Task state storage
- `workspaces/` - Agent working directories
- `logs/` - Execution logs

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
```

The daemon will:
- Connect to MCP servers defined in config
- Watch for new request files
- Process tasks through the workflow
- Handle handoffs and merges automatically

### 4. Add Tasks

**Option A**: Use the CLI
```bash
overseer add my-feature.md
```

**Option B**: Drop a markdown file in `.overseer/requests/`
```markdown
# Add user authentication

Implement JWT-based authentication with:
- Login endpoint at POST /api/auth/login
- Logout endpoint at POST /api/auth/logout
- Middleware to protect routes
- Token refresh mechanism
```

The daemon will automatically pick up new files.

## CLI Commands

| Command | Description |
|---------|-------------|
| `overseer init` | Initialize Overseer in current directory |
| `overseer daemon` | Start the background daemon |
| `overseer add <file>` | Add a task request |
| `overseer list` | List all tasks by state |
| `overseer status [id]` | Show task status (all active or specific) |
| `overseer approve <id>` | Approve a task awaiting review |
| `overseer logs <id>` | View task workspace files |

Task IDs can be abbreviated to their first 8 characters.

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
  count: 1  # Number of concurrent workers

git:
  merge_target: "main"
  branch_prefix: "feature"
  auto_push: true

mcp:
  servers:
    - name: filesystem
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    - name: shell
      command: "npx"
      args: ["-y", "@mako10k/mcp-shell-server"]
    - name: git
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-git"]
    - name: fetch
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-fetch"]

paths:
  requests: ".overseer/requests"
  tasks: ".overseer/tasks"
  workspaces: ".overseer/workspaces"
  logs: ".overseer/logs"
```

## Task Workflow

```
┌─────────┐     ┌──────────┐     ┌─────────────┐     ┌─────────┐
│ Pending │ ──► │ Planning │ ──► │Implementing │ ──► │ Testing │
└─────────┘     └──────────┘     └─────────────┘     └─────────┘
                     │                                     │
                     ▼                                     ▼
                ┌─────────┐                          ┌──────────┐
                │ Review  │ ◄──────────────────────  │Debugging │
                └─────────┘                          └──────────┘
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
| `pending` | Waiting in queue |
| `planning` | Agent is analyzing and creating a plan |
| `implementing` | Agent is writing code |
| `testing` | Agent is running tests |
| `debugging` | Agent is fixing test failures |
| `review` | Waiting for human approval |
| `merging` | Attempting to merge to target branch |
| `completed` | Successfully merged |
| `conflict` | Merge conflict detected |
| `blocked` | Waiting on dependency or human input |

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

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Daemon                               │
│  - Watches requests/ for new tasks                          │
│  - Manages worker pool                                       │
│  - Handles signals for graceful shutdown                    │
└─────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌──────────┐   ┌──────────┐   ┌──────────┐
        │ Worker 1 │   │ Worker 2 │   │ Worker N │
        └──────────┘   └──────────┘   └──────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│                         Agent                                │
│  - Communicates with llama.cpp server                       │
│  - Tracks context usage                                      │
│  - Executes tools via MCP                                   │
│  - Manages handoffs                                          │
└─────────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│                      MCP Servers                             │
│  filesystem │ shell │ git │ fetch                           │
└─────────────────────────────────────────────────────────────┘
```

## Development

```bash
# Run tests
make test

# Format code
make fmt

# Lint
make lint

# Build with version info
make build
```

## Roadmap

- [ ] Web UI for task monitoring
- [ ] Multiple model support (different models for different phases)
- [ ] Task templates for common patterns
- [ ] Metrics and observability
- [ ] Remote llama.cpp server support
- [ ] Plugin system for custom tools

## License

MIT

## Contributing

Contributions welcome! Please open an issue to discuss major changes before submitting a PR.
