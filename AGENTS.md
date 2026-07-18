# AGENTS.md

## Project Overview

**ccview** is a Go CLI tool that provides a TUI (terminal UI) and web UI for browsing conversation histories from Claude Code and OpenCode. It reads JSONL conversation files and SQLite databases, renders them with markdown formatting, and supports exporting to HTML/Markdown/JSONL.

## Essential Commands

```bash
# Build
go build -o ccview .

# Run TUI (default mode)
go run .

# Run web UI
go run . --web
go run . --web --port 8080

# Direct file view
go run . --file path/to/conversation.jsonl

# Export
go run . --export output.html --file conversation.jsonl

# Release build (CGO disabled, cross-platform)
goreleaser release --clean
```

## Architecture

Single Go package (`main`), flat file structure — no subdirectories.

### Data Flow

```
main.go (CLI flags, mode dispatch)
  │
  ├─ TUI mode → ui.go (Bubble Tea model)
  │    ├─ provider.go (Provider interface)
  │    │    ├─ provider_claude.go (reads ~/.claude/projects/*/*.jsonl)
  │    │    └─ provider_opencode.go (reads ~/.local/share/opencode/opencode.db via SQLite)
  │    ├─ data.go (TreeData loading, lazy-loading functions)
  │    └─ parse.go (JSONL entry parsing, metadata scanning)
  │
  ├─ Web mode → server.go (HTTP server, embedded HTML/CSS/JS)
  │
  └─ Export mode → export.go (HTML/Markdown/JSONL output)
```

### Key Types

- **`Provider`** (`provider.go`): Interface abstracting data sources — `LoadTree()`, `LoadConversation()`, `SearchSessions()`
- **`TreeData`** (`data.go`): Hierarchical structure of projects → conversations → sub-agents
- **`Entry`** (`parse.go`): Single JSONL line — has `Type` (user/assistant/system), `Parsed` message, and content blocks
- **`model`** (`ui.go`): Bubble Tea model — holds all TUI state (view state, sidebar, content, search, export overlay, mouse selection)

### File Responsibilities

| File | Purpose |
|------|---------|
| `main.go` | CLI flag parsing, mode dispatch (TUI/web/export) |
| `provider.go` | `Provider` interface + `SearchResult` type |
| `provider_claude.go` | Claude Code provider — reads `~/.claude/` directory tree |
| `provider_opencode.go` | OpenCode provider — reads SQLite DB via `modernc.org/sqlite` (pure Go, no CGO) |
| `data.go` | Tree loading for Claude (`loadTree`, `loadProject`, `findSubAgents`), file/dir helpers |
| `parse.go` | JSONL parsing, metadata scanning, content block extraction, timestamp formatting |
| `ui.go` | Entire TUI — Bubble Tea model, Update/View, all keybindings, sidebar/content rendering, search, export overlay, mouse handling |
| `server.go` | Web UI HTTP server with API endpoints (`/api/tree`, `/api/messages`, `/api/content`, `/api/export`) and embedded single-page HTML |
| `export.go` | HTML/Markdown export, goldmark markdown→HTML conversion, multi-file subagent export |

## Key Patterns

### Provider Pattern
The `Provider` interface decouples data sources from UI. Claude reads filesystem JSONL files; OpenCode reads a SQLite database. Both return the same `TreeData`/`Entry` types. Adding a new provider means implementing the 5-method interface.

### Lazy Loading (3-Level Hierarchy)
The Claude provider uses a 3-level lazy loading strategy to avoid scanning all JSONL files at startup. See `docs/adr/001-lazy-loading.md` for the full design.

- **Level 0** (`LoadProjectList`): Returns project directory names + `.jsonl` file counts. No file reads — just `os.ReadDir`. Instant.
- **Level 1** (`EnrichProjectMeta`): Fills in display names (from CWD), last active times (from file mod times), CLAUDE.md, memory files. Async background after Level 0.
- **Level 2** (`LoadProjectDetail`): Full conversation list with previews, message counts, sub-agents. Triggered per-project when the user opens it.
- **Level 3** (`LoadConversation`): Full parsed conversation content. Already lazy — loads on conversation select.

History titles from `~/.claude/history.jsonl` are cached in `model.historyTitles` after the first load (triggered async at startup alongside Level 1 enrichment).

### Bubble Tea v2
Uses `charm.land/bubbletea/v2` (not v1). The import alias is `tea "charm.land/bubbletea/v2"`. Lip Gloss is also v2 (`charm.land/lipgloss/v2`).

### TUI State Machine
Three view states: `viewLoading` → `viewProjectList` → `viewProjectDetail`. Within project detail, two panes: `paneSidebar` and `paneContent`. Overlays (session search, export wizard) intercept all key events when active.

### Content Block Rendering
Both providers normalize their data into `ContentBlock` with types: `text`, `thinking`, `tool_use`, `tool_result`. The `getContentBlocks()` function handles polymorphic JSON content (string or array).

### Debounced Search
Content search and session search use a generation-counter debounce pattern. Each keypress increments `contentSearchGen`/`sessionSearch.gen`; a `tea.Tick` fires after delay and only the latest generation triggers the actual search.

### Embedded Web UI
The web UI HTML/CSS/JS is a single const string (`indexHTML` in `server.go`) — not a separate file. Highlight.js is loaded from CDN.

## Gotchas

- **No tests exist** — there is no test suite. Verify changes manually.
- **No CGO** — release builds use `CGO_ENABLED=0`. The SQLite driver (`modernc.org/sqlite`) is pure Go. Don't introduce CGO dependencies.
- **Scanner buffer sizes** — JSONL files can be large. The code sets explicit scanner buffers (up to 10MB in `parseConversation`). If adding new scanners, set appropriate buffer sizes.
- **Bubble Tea v2 API** — this is NOT the v1 API. Message types and method signatures differ from v1 examples you may find online.
- **OpenCode timestamps** — stored as Unix milliseconds (`int64`), not RFC3339 strings. The provider converts them.
- **`tree` field** — `model.tree` points to the active provider's tree. When switching tabs, `m.tree` must be updated via `switchProviderTab()`.
- **Sidebar cursor** — separator and header items are non-navigable. Use `nextNavigable()` to skip them when moving cursor.
- **Export overlay** — the export wizard has 5 steps (what → format → path → filename → confirm). Each step has its own key handler in `updateExportOverlay`.
- **`model.directFile`** — when a file is passed via `--file`, the TUI skips provider loading and goes straight to content view. Many code paths check for this.
- **Lazy loading state** — `model.projectDetailLoading` is true while Level 2 is loading for a project. The sidebar shows a "Loading..." header during this time. The `projectDetailReadyMsg` handler rebuilds the sidebar when data arrives.
- **Web server API** — the web mode's `/api/tree` endpoint only loads Claude data (calls `loadTree()` directly, not through providers). OpenCode data is not served via web mode.
