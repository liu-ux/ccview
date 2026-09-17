# ccview

ccview renders the transcripts that coding agents leave behind. It is a reader: it
displays, searches, and exports what an agent wrote to disk, and never sees the
requests the agent sent to a model.

## Language

### What is stored

**Transcript**:
The record a coding agent writes of one session — JSONL files for Claude Code, rows
in a SQLite database for OpenCode. The unit ccview parses.
_Avoid_: log, history, session file, dump

**System entry**:
A transcript entry of type `system`: harness metadata emitted *during* a session, such
as local command output, compaction boundaries, turn durations, or API errors. It carries
no instruction text.
_Avoid_: system prompt, system message, system info

**System prompt**:
The instruction text an agent sends to a model ahead of the conversation. Never written
to a transcript, so ccview cannot display it.
_Avoid_: system entry, system message, prompt template

### What the user browses

**Project**:
A group of conversations, keyed by the working directory the agent ran in.
_Avoid_: workspace, repo, folder

**Conversation**:
One transcript, and the unit listed in the sidebar and opened in the content pane.
_Avoid_: chat, thread, history item

**Sub-agent**:
A conversation spawned by a tool call inside another conversation, listed beneath
its parent.
_Avoid_: sidechain, child session, task

**Session**:
The agent's own identity for one conversation (a Claude Code session UUID, an OpenCode
session id). Present in the transcript, not a grouping ccview invents.
_Avoid_: conversation, when you mean the identifier
