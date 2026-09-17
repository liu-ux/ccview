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

- **`Provider`** (`provider.go`): Interface abstracting data sources — `Name()`, `Available()`, `LoadTree()`, `LoadProjectList()`, `EnrichProjectMeta()`, `LoadProjectDetail()`, `LoadConversation()`, `ContentSearch()`. `ContentSearch` returns a `ContentSearchResult`: complete results plus the `ContentMatch` windows prefix narrowing needs.
- **`TreeData`** (`data.go`): Hierarchical structure of projects → conversations → sub-agents
- **`Entry`** (`parse.go`): Single JSONL line — has `Type` (user/assistant/system), `Parsed` message, and content blocks
- **`model`** (`ui.go`): Bubble Tea model — holds all TUI state (view state, sidebar, content, search, export overlay, mouse selection)

### File Responsibilities

| File | Purpose |
|------|---------|
| `main.go` | CLI flag parsing, mode dispatch (TUI/web/export) |
| `provider.go` | `Provider` interface + `SearchResult` / `ContentSearchResult` / `ContentMatch` types + `contentWindows()` |
| `provider_claude.go` | Claude Code provider — reads `~/.claude/` directory tree |
| `provider_opencode.go` | OpenCode provider — reads SQLite DB via `modernc.org/sqlite` (pure Go, no CGO) |
| `data.go` | Tree loading for Claude (`loadTree`, `loadProject`, `findSubAgents`), file/dir helpers |
| `parse.go` | JSONL parsing, metadata scanning, content block extraction, timestamp formatting |
| `ui.go` | Entire TUI — Bubble Tea model, Update/View, all keybindings, sidebar/content rendering, search, export overlay, mouse handling |
| `server.go` | Web UI HTTP server with API endpoints (`/api/tree`, `/api/messages`, `/api/content`, `/api/export`) and embedded single-page HTML |
| `export.go` | HTML/Markdown export, goldmark markdown→HTML conversion, multi-file subagent export |

## Key Patterns

### Provider Pattern
The `Provider` interface decouples data sources from UI. Claude reads filesystem JSONL files; OpenCode reads a SQLite database. Both return the same `TreeData`/`Entry` types. A new provider implements the 8 methods above; `ContentSearch` carries the heaviest contract, see [Content Search Prefix Narrowing](#content-search-prefix-narrowing).

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

### System Entry Rendering
Entries with `type: "system"` (harness metadata: command output, compaction, turn durations, API errors) are formatted by `formatSystemEntry()` in `parse.go`, which returns a `(label, body)` pair. It is the single source of truth for all four surfaces — TUI (`renderConversation`), HTML export (`export.go`, 2 call sites), Markdown export, and the web UI (`serveMessages`) — so a subtype only ever needs formatting in one place. Unknown subtypes with content fall through to a generic rendering, because the set of subtypes grows with each Claude Code release. Fields the formatter needs (`Error`, `DurationMs`, `MessageCount`, `CompactMetadata`) are parsed into `Entry`; an unparsed field silently renders as missing.

### Debounced Search
`contentSearchGen` (in-viewer `/` search) and `sessionSearch.gen` use a generation-counter debounce: each keypress increments the counter, a `tea.Tick` fires after the delay, and only the latest generation triggers the search. Metadata search still works this way; content search deliberately does not — see below.

### Content Search Prefix Narrowing
Content search is the only search that touches the disk (`rg`/`grep` per keystroke, or a scan of the OpenCode `part` table), so it is **gated**: typing never searches, `Enter` (or a scope/mode change) does, and afterwards typing narrows the previous search's result set locally. `sessionSearchState` holds the base (`baseQuery`, `baseAll`, `matches`, `narrowable`, `pending`, `incomplete`) and `applyQueryToSearchCache()` implements the three cases — query extends the base (narrow), query is a proper prefix of the base (base results are valid but incomplete, shown with a marker), otherwise diverged (show nothing).

The cache is `ContentMatch.Windows`: per matching line, the text from its first match to 50 runes past its last one. This is what makes narrowing exact rather than approximate — a result may still contain the longer query after its window has run out, and a result whose window cannot answer cannot be shown at all, because the base's results are a superset once the query grows. Two invariants keep it sound, and breaking either loses matches silently:

- **Every occurrence in the line must be inside its window.** A line like `ac ab` must match base `a` extended to `ab` from its *second* occurrence, so windows are built from first match to last match, never from the first match alone.
- **All or nothing under the budget.** `budgetedMatches()` drops the entire window set when it exceeds `searchCacheBudgetBytes` (`--search-cache-mb`, `CCVIEW_SEARCH_CACHE_MB`, default 32MB). Caching what fits would make narrowing miss matches.

Beyond `contentMatchWindowRunes` of growth, or without windows, narrowing is unavailable and the UI asks for `enter to search again`. Providers must therefore return **complete** results (no SQL `LIMIT`) and match the query **literally and case-sensitively** — `rg -F`, `instr()`, `strings.Contains` — because a backend whose rule differs would make narrowing disagree with the search that produced its windows. The 50-result display cap lives in `setSearchResults()`, not in the providers.

### Embedded Web UI
The web UI HTML/CSS/JS is a single const string (`indexHTML` in `server.go`) — not a separate file. Highlight.js is loaded from CDN.

### Project List Filter
The project list screen has an inline filter (`f` key) that narrows projects by substring match on `DisplayName`/`DirName`. State: `model.projectFilter []rune` + `model.projectFilterActive bool`. The helper `filteredProjectIndices(tree, filter)` returns matching indices. `projCursor` remains an index into the full `tree.Projects` slice; `projOffset` tracks position in the filtered list for scrolling. Filter persists across project open/close but clears on provider tab switch.

## Gotchas

- **Tests** — `data_test.go` and `search_test.go` cover lazy loading, providers, search, rendering, and filtering. Run with `go test -count=1 -timeout 30s ./...`.
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
- **Content search truncation** — the complete match set is `sessionSearch.allResults`; `sessionSearch.results` is its first `searchDisplayLimit` entries. Never cap in a provider or in `computeContentSearchResults`: narrowing works off the complete set, so a truncating provider makes a narrowed search miss matches past the cap.
- **Paste** — bracketed paste is enabled (nothing sets `DisableBracketedPasteMode` on the `tea.View`), so pasted text arrives as `tea.PasteMsg`, **never** as a run of `tea.KeyPressMsg`. Every text input must be routed in `handlePaste()`; adding a new text input without a case there means it silently ignores pastes. Text is normalized by `sanitizePaste()` (whitespace runs collapse to one space, control characters dropped, capped at `maxPasteLen`).
- **Display toggles are TUI-only** — `t` / `T` / `R` / `S` change what the viewer renders, and nothing else. Export (HTML, Markdown) and the web UI always render everything, because they are the complete record. Do not make export honour a toggle to "fix" the inconsistency.
- **`showSystem` defaults to on** — unlike the other display toggles, which rely on the zero value, `showSystem` is initialized in `newModel()`. Building a `model` literal without it (as tests do) yields a viewer with system entries hidden.
