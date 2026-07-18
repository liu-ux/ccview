# ADR-001: Lazy Loading for Claude Projects

## Status

Accepted

## Context

ccview loads all data from `~/.claude/projects/` at startup in a single blocking call. For users with many projects and conversations, this causes a noticeable delay before the TUI becomes interactive. The root cause is `loadProject()` which calls `scanConversationMeta()` on **every** `.jsonl` file in a project — each call reads the entire file line-by-line with `json.Unmarshal` on every line, just to extract a preview string and message count.

The current loading sequence:

```
loadTree()
  ├── loadHistoryTitles()     — reads entire history.jsonl
  └── loadProjects()          — sequential loop over all project dirs
        └── loadProject()     — per project:
              ├── scanConversationMeta() × N  — reads each .jsonl fully
              ├── findSubAgents() × N        — reads each subagent .jsonl twice
              ├── countDirEntries() × N      — reads tool-results/ and file-history/
              └── sort conversations
```

With 50 projects × 20 conversations each, this means ~1000 full JSONL file reads before the first frame renders.

## Decision

Implement a **3-level lazy loading hierarchy** where each level loads only the data needed for the current view, deferring expensive operations until the user navigates deeper.

### Level 0 — Project List (instant)

**What loads:** Project directory names only.

**How:** `os.ReadDir(~/.claude/projects/)` returns directory entries in <1ms. Use `DirName` as `DisplayName`. Set `ConvCount` to the number of `.jsonl` files in the directory (one `ReadDir`, no file reads).

**What's deferred:** Everything else — `MsgCount`, `LastActive`, `Preview`, `ClaudeMD`, `MemoryFiles`, sub-agents, history titles.

**User sees:** A list of project paths with conversation counts, no message counts or timestamps. The UI is interactive immediately.

### Level 1 — Project List Metadata (async, background)

**What loads:** Per-project metadata needed for the project list view — `DisplayName` (from first conversation's CWD), `LastActive` (from file mod times), `ClaudeMD`, `MemoryFiles`.

**How:** A background Bubble Tea command iterates projects. For each project, it reads `CLAUDE.md` existence, `memory/` directory, and gets `LastActive` from the most recently modified `.jsonl` file's `DirEntry.Info().ModTime()`. It does **not** read file contents.

**What's deferred:** `MsgCount`, conversation previews, sub-agents.

**User sees:** Projects populate with real display names and "last active" timestamps as they load. No message counts yet.

### Level 2 — Sidebar Conversation List (on project open)

**What loads:** Full conversation list for a single project — `Title`, `ModTime`, `MsgCount`, `Preview`, `SubAgents`.

**How:** When the user opens a project (Enter/l/Right), a Bubble Tea command runs `loadProject()` for that single project only. This reads conversation files to extract metadata. History titles from `history.jsonl` are loaded lazily at this point (first project open triggers the read; subsequent opens reuse a cached map).

**What's deferred:** Full conversation message content.

**User sees:** The sidebar populates with conversation titles, timestamps, message counts, and previews.

### Level 3 — Conversation Content (on conversation select)

**What loads:** Full parsed conversation entries.

**How:** Already implemented — `loadConvCmd()` calls `parseConversation()` or `provider.LoadConversation()`. No change needed.

### History Titles Caching

`history.jsonl` is read once on first Level 2 trigger and cached in the model as `map[string]string`. Subsequent project opens reuse the cache.

### Concurrency Model

Each level uses a Bubble Tea command (goroutine) that returns a message when complete. The model processes the message and updates state. No shared mutable state between goroutines — the model is only mutated in `Update()`.

## Changes to Provider Interface

Add one new method to `Provider`:

```go
LoadProjectDetail(dirName, dirPath string) (*TreeProject, error)
```

This loads a single project's full conversation list (Level 2). The existing `LoadTree()` is replaced by a lightweight `LoadProjectList()` that returns only project directory entries (Level 0/1).

## New Message Types

```go
type projectListReadyMsg struct {
    projects []TreeProject  // Level 0: dir names + conv counts
}

type projectMetaReadyMsg struct {
    projects []TreeProject  // Level 1: with display names + last active
}

type projectDetailReadyMsg struct {
    index int
    proj  TreeProject  // Level 2: full conversation list
}
```

## UI State Changes

| State | Before | After |
|-------|--------|-------|
| Startup | Blocking "Loading..." until all data ready | Shows project list instantly (Level 0), metadata fills in async (Level 1) |
| Open project | Sidebar shows immediately with pre-loaded data | Sidebar shows "Loading..." briefly, then populates (Level 2) |
| Open conversation | Loads on select (unchanged) | Same — Level 3 already lazy |

## Consequences

### Positive

- **TUI interactive in <10ms** regardless of data size — only `ReadDir` calls at startup.
- **Per-project cost is bounded** — opening one project scans only that project's files, not all projects.
- **Memory proportional to what's viewed** — not all conversations loaded upfront.
- **No wasted work** — if user never opens a project, its conversations are never scanned.

### Negative

- **Project list initially lacks rich metadata** — no message counts or last-active times until Level 1 completes. Acceptable because the list is navigable immediately.
- **Slight delay on project open** — Level 2 scan adds a brief loading state when opening a project. For a single project this is typically <500ms.
- **Increased code complexity** — more message types, more async state management in the model.

### Mitigations

- Level 0 data (dir names + conv counts) is sufficient for navigation — users can start browsing immediately.
- Level 1 fills in metadata progressively — the UI updates in-place without blocking.
- Level 2 is scoped to a single project — even large projects with 100 conversations scan in <2s.
- History titles are cached after first load — no repeated file reads.
