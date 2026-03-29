# claude-log

A terminal-based explorer and renderer for Claude Code conversation histories. Browse projects, view conversations with proper markdown rendering, inspect sub-agents, and export to HTML.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), and [Glamour](https://github.com/charmbracelet/glamour).

## Install

```bash
go install claude-log@latest
```

Or build from source:

```bash
git clone <repo>
cd claude-log
go build -o claude-log .
```

## Usage

### TUI Explorer (default)

```bash
./claude-log
```

Split-pane interactive explorer. Left pane shows the project tree, right pane renders content.

```
 Claude Code Explorer
EXPLORER                          |  tidy-marinating-dragon
──────────────────────────────────|  ──────────────────────────────
v ~/Work/project                  |  ─────────────────────────────
    CLAUDE.md                     |   USER  14:30:05
    MEMORY.md                     |
  * tidy-marinating-dragon  202   |    What is the meaning of life?
      Explore: a236375c           |
      General: aed6d1cb           |  ─────────────────────────────
v ~/other-project                 |   ASSISTANT  claude-opus-4-6
  > toasty-yawning-hanrahan       |
                                  |    The meaning of life is...
```

### Web Explorer

```bash
./claude-log --web
./claude-log --web --port 8080
```

Opens an interactive web UI at `http://localhost:3333` with Claude's color scheme. Features a tree sidebar for navigation and a content viewer with collapsible thinking blocks, tool call cards, and token usage.

### Direct File

```bash
./claude-log --file path/to/conversation.jsonl
```

Opens a specific JSONL file directly in the TUI viewer (full-width, no tree).

### HTML Export

```bash
./claude-log --export output.html --file path/to/conversation.jsonl
```

Generates a self-contained HTML file with dark theme, syntax-highlighted code blocks, and collapsible thinking sections.

## TUI Keybindings

### Tree Pane

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up/down |
| `Enter` | Open conversation or file in viewer |
| `Space` | Expand/collapse project or conversation |
| `l` / `Right` | Expand node or switch to viewer |
| `h` / `Left` | Collapse node |
| `Tab` | Switch to viewer pane |
| `e` | Export selected conversation to HTML |
| `g` / `G` | Jump to top/bottom |
| `q` | Quit |

### Viewer Pane

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll up/down |
| `Space` / `f` | Page down |
| `b` | Page up |
| `g` / `G` | Jump to top/bottom |
| `Tab` / `h` | Switch to tree pane |
| `e` | Export current conversation to HTML |
| `q` | Quit |

## Data Hierarchy

claude-log reads from `~/.claude/` and organizes data as:

```
Global
  CLAUDE.md (global instructions)
  Plans (markdown plan documents)

Project: ~/path/to/project
  CLAUDE.md (project instructions)
  Memory files (persistent knowledge)
  Conversations:
    session-slug (N messages, M sub-agents)
      Sub-agent: Explore
      Sub-agent: General
```

### What it reads

| Source | Description |
|--------|-------------|
| `~/.claude/projects/*/` | Project directories grouped by working directory |
| `*.jsonl` | Conversation message logs |
| `*/subagents/*.jsonl` | Sub-agent conversation threads |
| `*/subagents/*.meta.json` | Agent type metadata |
| `*/memory/*.md` | Per-project memory files |
| `*/CLAUDE.md` | Project-level instructions |
| `~/.claude/plans/*.md` | Plan documents |
| `~/.claude/file-history/` | File edit counts per session |

### Message Types Rendered

- **User messages** - with glamour markdown rendering
- **Assistant messages** - with model name, markdown rendering, token usage
- **Thinking blocks** - truncated in TUI, collapsible in web
- **Tool calls** - summarized (Read, Write, Edit, Bash, Grep, Glob, Agent, etc.)
- **System messages** - slash commands shown inline

## Architecture

```
main.go       CLI entry point, flag parsing
data.go       Tree types, project scanning, filesystem loading
parse.go      JSONL parsing, glamour rendering, formatting helpers
ui.go         Bubble Tea TUI with split-pane layout
server.go     HTTP server with embedded SPA (Claude color scheme)
export.go     Static HTML export with goldmark markdown
```

## Dependencies

- [charm.land/bubbletea/v2](https://github.com/charmbracelet/bubbletea) - TUI framework
- [charm.land/lipgloss/v2](https://github.com/charmbracelet/lipgloss) - Terminal styling
- [github.com/charmbracelet/glamour](https://github.com/charmbracelet/glamour) - Terminal markdown rendering
- [github.com/yuin/goldmark](https://github.com/yuin/goldmark) - HTML markdown rendering
