# ADR-004: Search Improvements

## Status

Accepted

## Context

The current search system has four issues:
1. Claude's `SearchSessions` calls `LoadTree()` on every query, triggering a full JSONL scan — directly conflicting with the lazy loading design.
2. Session search only matches metadata (title, preview, slug, project name). Users often need to find sessions by content they remember from the conversation.
3. After finding search results, there's no way to filter the sidebar to show only matching sessions. Users must repeatedly re-search to browse results.
4. Search results lack context (message count, CWD) that helps identify the right session.

## Decision

### 1. Performance: Search Against Cached Tree

**Problem**: `ClaudeProvider.SearchSessions` → `LoadTree()` → full JSONL scan on every keystroke (after debounce).

**Solution**: `computeSessionSearchResults` in `ui.go` searches directly against the already-loaded `m.providerTrees` data. For Claude, this means searching the in-memory `TreeData` that was populated by Levels 0/1/2. For OpenCode, the existing SQL-based search is already fast and unchanged.

Remove `SearchSessions` from the `Provider` interface. Replace with a local function that iterates `providerTrees`.

### 2. Content Scope Toggle in Session Search

Add a scope toggle: `session` (current: title/preview/slug) ↔ `content` (searches conversation message content).

**Content search strategy** (priority order):
1. **ripgrep** (`rg`): Fastest. `rg --json -l <query> ~/.claude/projects/` to find matching files.
2. **grep** (`grep -rl`): Fallback if rg unavailable.
3. **Manual scan**: Parse JSONL files line by line (slowest, last resort).

Detection happens once at session search open time. Results are file paths, which map back to conversations via the cached tree.

Toggle key: `tab` cycles `session metadata` → `content search` → `session metadata`. Status badge shows current mode.

### 3. Sidebar Filter from Search Results

After a session search, pressing `f` (or `Enter` on a result) activates a **sidebar filter**:
- The sidebar shows only conversations that appeared in search results.
- The filter is stored as `model.sidebarFilter map[string]bool` (paths of matching conversations).
- When active, `buildSidebar` skips conversations not in the filter set.
- Status bar shows a "filtered" indicator.
- Pressing `Esc` or `/` clears the filter.

This allows users to: search → filter → browse results without re-searching.

### 4. Enriched Search Results

Add to `SearchResult`:
- `MsgCount int` — message count for the conversation
- `CWD string` — working directory

Display in the search overlay: `Title  [CC] 2024-01-15  42 msgs  ~/src/my-app`.

## Consequences

### Positive

- Session search is instant (in-memory for Claude, SQL for OpenCode).
- Content search uses native rg/grep when available — sub-second even for large datasets.
- Sidebar filter eliminates the "search → open → back → search again" loop.
- Richer results help identify the right session faster.

### Negative

- Content search only works for Claude (filesystem-based JSONL). OpenCode stores data in SQLite, so rg/grep can't search it directly. OpenCode content search falls back to SQL queries.
- rg/grep results need mapping from file paths back to conversation entries — adds a post-processing step.
- Sidebar filter state needs careful management when navigating between projects.

## Implementation Plan

1. Remove `SearchSessions` from `Provider` interface; search `providerTrees` directly in `ui.go`.
2. Add `searchContent` toggle + rg/grep/manual content search logic.
3. Add `sidebarFilter` model field + filter logic in `buildSidebar`.
4. Add `MsgCount`, `CWD` to `SearchResult`; display in overlay.
5. Update search overlay rendering with scope toggle indicator.
