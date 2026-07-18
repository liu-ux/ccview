# ADR-003: Toggle Tool Call Detail in Content Viewer

## Status

Accepted

## Context

Tool calls in the conversation viewer are rendered as compact one-line summaries (e.g. `[tool] Read: path/to/file`). The full input JSON (command arguments, file contents, search patterns) is hidden. Users debugging agent behavior need to see these details without exporting to HTML.

## Decision

Add a `showToolDetails` toggle to the content viewer, bound to the `t` key.

### Behavior

- **Default (`showToolDetails = false`)**: Current behavior — compact summary via `formatToolUse()`.
- **Toggled on (`showToolDetails = true`)**: Each `tool_use` block shows the summary line followed by the formatted JSON input, indented and syntax-colored.

### Rendering

When enabled, tool_use blocks render as:

```
  [tool] Bash: npm test
    {
      "command": "npm test",
      "description": "Run tests"
    }
```

The JSON is pretty-printed with 2-space indentation, wrapped to fit the pane width. Long values are truncated at 200 characters.

### Re-rendering

Toggling `showToolDetails` triggers a re-render of the current conversation content (same path as window resize). The toggle state persists across conversation switches within the same session.

### Key binding

`t` is only active in the content pane (not sidebar, not overlays). It's added to the keybinding table in the help/status line.

## Consequences

### Positive

- No need to export to HTML just to inspect tool arguments
- Toggle is instant — re-renders from cached entries, no re-parsing

### Negative

- JSON rendering in terminal has no syntax highlighting (unlike the web UI with highlight.js). Acceptable for debugging.
- Large tool inputs (e.g. full file writes) may produce many lines. Truncation at 200 chars per value mitigates this.
