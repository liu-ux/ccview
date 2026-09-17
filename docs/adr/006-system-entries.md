# ADR-006: Render System Entries on Every Surface

## Status

Accepted

## Context

Claude Code transcripts contain entries with `type: "system"` — harness metadata emitted *during* a session, not conversation content. In one real session (Claude Code 2.1.114, 620 turns) they appear as:

| Subtype | Content | Count |
|---|---|---|
| `api_error` | `status` plus a nested provider error message | 16 |
| `turn_duration` | `durationMs`, `messageCount` | 11 |
| `compact_boundary` | `content`, `compactMetadata` (pre/post tokens, duration) | 3 |
| `local_command` | `<command-name>` / `<local-command-stdout>` payload | 3 |

ccview rendered only `local_command`, and dropped the other three silently. There was no way to see them, and no way to hide the one kind that did render.

A request to display "the system prompt of a conversation" led here, so the boundary is worth stating: a **system entry** is not a **system prompt**. The system prompt is the instruction text sent to the model; it is never written to a transcript (verified against Claude Code JSONL, `~/.claude/transcripts/*.jsonl`, and the OpenCode `message`/`part`/`session_context_epoch` tables). ccview cannot display it, and must not pretend to.

## Decision

Render system entries on **all four surfaces** through one shared formatter, and govern them in the TUI with a display toggle.

### Rendering

`formatSystemEntry(entry) (label, body string, ok bool)` in `parse.go` is the single source of truth, called from the TUI (`renderConversation`), HTML export (2 call sites), Markdown export, and the web UI (`serveMessages`). It returns a short bracketed label and a single-line body:

```
  [system] /clear
  [compact] Conversation compacted (pre 120472 → post 11088 tok, 59s)
  [turn] 10m47s · 316 msgs
  [error] 429 slow down
```

Bodies are folded onto one line (whitespace runs collapse to a single space) and capped at 120 runes with a trailing `…`: an `api_error` body carries a full Node stack trace, and only its first line is signal. `ok=false` means the entry has nothing to show and the caller skips it.

Labels are per-subtype, so `[error]` can be coloured differently from routine metadata. The styling itself stays at the call site: the TUI applies `toolStyle` to the `[error]` label and `systemStyle` to everything else, while the exporters emit HTML or Markdown.

**Unknown subtypes fall through to a generic rendering when they carry content.** The subtype set is not stable — `stop_hook_summary` and `away_summary` are produced by other Claude Code versions — so a hardcoded switch that dropped unknown subtypes would silently lose data on the next release.

### Toggle

`S` toggles `showSystem` in the content viewer, alongside `t` (tool details), `T` (thinking) and `R` (tool results). Unlike those three, which rely on the Go zero value, `showSystem` defaults to **on** and is initialized in `newModel()`: the point of the feature is to see these entries, and defaulting to hidden would also silently change existing behaviour by removing the `local_command` lines that always rendered.

### The toggle is TUI-only

Export and the web UI always render every system entry, and honour no display toggle. They are the complete record — a user exporting HTML to share a session expects to get everything, including the fact that the session hit a 401 fifteen times — and this matches the existing behaviour of `t` / `T` / `R`, which export has always ignored. Honouring `S` alone would be a strange asymmetry: hiding rows on screen would delete metadata from the exported artefact.

## Consequences

### Positive

- The 30 pieces of harness metadata in a long session become visible, and can be hidden in one keystroke when they are noise.
- Adding a subtype is a one-line change in one function, and unlisted subtypes degrade to a readable line instead of vanishing.
- The three near-duplicate `case "system"` blocks in `export.go` collapse into the shared formatter, removing an existing drift risk.

### Negative

- Four call sites must stay in sync — the shared formatter makes them agree on *format*, not on *whether* to render.
- The web UI and export are not covered by tests for this path (`server.go` has no test file); the formatter is covered, so the untested part is 3 lines of call site.

## What we deliberately did not do

**Read `~/.claude-code-router/logs/*.log` to recover the real system prompt.** On a machine routing Claude Code through claude-code-router with `"LOG": true`, the `msg: "final request"` lines contain the full HTTP body, including a 26,741-character `role: "system"` message. It is the genuine system prompt, and it is tempting.

It was rejected because the correlation does not exist: the logged request body carries `metadata: {}` and no session id, so a prompt cannot be attached to the conversation being viewed — only guessed at by timestamp. The result would be a pile of prompts ordered by time, next to a conversation, inviting the reader to assume they belong together. An honest absence beats a confident wrong answer. It is also machine- and client-specific: OpenCode sessions never pass through the router at all.

**Synthesize an approximate system prompt** from `settings.json`, `CLAUDE.md` or the project's memory files (ccview already reads the latter two). Same objection, worse: a synthesized prompt would be presented as the model's instructions while being a guess at them.
