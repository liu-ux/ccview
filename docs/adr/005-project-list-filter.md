# ADR-005: Project List Filter

## Status

Accepted

## Context

Users with many projects (dozens of Claude Code or OpenCode workspaces) find it tedious to scroll through the full project list. There is no way to narrow the list by keyword. The existing `/` key opens a global session search overlay, which searches across conversations — not what you want when you just need to find a project by name.

## Decision

Add an inline filter to the project list screen, triggered by the `f` key (keeping `/` for session search).

### State

Add two fields to `model`:

- `projectFilter []rune` — the current filter text
- `projectFilterActive bool` — true when the filter input is focused

### Activation & Input

- **`f`** on the project list screen activates filter input mode.
- When active, all printable rune input appends to `projectFilter`.
- **Backspace** deletes the last rune.
- **Enter** confirms the filter and deactivates input mode (filter persists).
- **Esc** clears the filter entirely and deactivates input mode.
- **`f`** when filter is already active deactivates input mode (filter persists).

### Filtering Logic

A helper function `filteredProjectIndices(tree *TreeData, filter []rune) []int` returns the indices of projects whose `DisplayName` or `DirName` contains the filter string (case-insensitive substring match). If the filter is empty, all indices are returned.

### Rendering

When `projectFilter` is non-empty:
- The project list shows only matching projects.
- A filter indicator is rendered below the title: `  Filter: <text>  (Esc: clear, f: edit)` in a distinct style.
- The status bar shows the filtered count: `3 / 20 projects`.
- The cursor and offset are clamped to the filtered list bounds.

When filter is empty:
- Normal rendering (all projects shown).

### Cursor Behavior

- `projCursor` is an index into the **full** `tree.Projects` slice.
- Navigation (up/down/home/end) operates on the filtered index list — pressing down moves to the next matching project.
- When the filter changes, the cursor snaps to the nearest matching project.

### Persistence

- Filter persists when navigating into a project and back.
- Filter persists when switching provider tabs (1/2/Tab).
- Filter is cleared on `switchProviderTab` (different provider may have different naming conventions; clearing is safer).

### Key Binding Change

- `/` remains session search (global).
- `f` is the new filter key (only on project list screen).

## Consequences

### Positive

- Users can quickly narrow a large project list by typing a substring.
- Inline filter (not an overlay) keeps the list visible — immediate visual feedback.
- Filter persists during project browsing, so you can open multiple matching projects without re-typing.

### Negative

- `projCursor` tracking across filter changes adds complexity — must map between filtered positions and full project indices.
- Filter state is lost on tab switch — acceptable tradeoff for simplicity.
