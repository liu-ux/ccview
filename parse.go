package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"
)

// ── JSONL entry types ──

type Entry struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID *string         `json:"parentUuid"`
	Timestamp  string          `json:"timestamp"`
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd"`
	Version    string          `json:"version"`
	GitBranch  string          `json:"gitBranch"`
	Subtype    string          `json:"subtype"`
	Content    string          `json:"content"`
	Level      string          `json:"level"`
	Slug       string          `json:"slug"`
	Message    json.RawMessage `json:"message"`
	Parsed     *ParsedMessage  `json:"-"`

	// System entry fields (Type == "system").
	Error           json.RawMessage  `json:"error"`           // subtype api_error
	DurationMs      int64            `json:"durationMs"`      // subtype turn_duration
	MessageCount    int              `json:"messageCount"`    // subtype turn_duration
	CompactMetadata *CompactMetadata `json:"compactMetadata"` // subtype compact_boundary
}

// CompactMetadata describes a context compaction, carried by system entries
// with subtype "compact_boundary".
type CompactMetadata struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"preTokens"`
	PostTokens int    `json:"postTokens"`
	DurationMs int64  `json:"durationMs"`
}

type ParsedMessage struct {
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *Usage          `json:"usage"`
	MsgID      string          `json:"id"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Content   string          `json:"content"` // tool_result uses "content" instead of "text"
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// ── Parsing ──

func parseConversation(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 10*1024*1024)

	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		if (entry.Type == "user" || entry.Type == "assistant") && len(entry.Message) > 0 {
			var pm ParsedMessage
			if err := json.Unmarshal(entry.Message, &pm); err == nil {
				entry.Parsed = &pm
			}
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return mergeSplitEntries(entries), nil
}

// mergeSplitEntries merges consecutive assistant entries that share the same
// message ID. Claude's JSONL sometimes splits a single assistant response
// (thinking + text + tool_use) across multiple lines with the same MsgID.
func mergeSplitEntries(entries []Entry) []Entry {
	if len(entries) == 0 {
		return entries
	}
	var merged []Entry
	for _, e := range entries {
		if len(merged) > 0 {
			prev := &merged[len(merged)-1]
			if e.Type == "assistant" && prev.Type == "assistant" &&
				e.Parsed != nil && prev.Parsed != nil &&
				e.Parsed.MsgID != "" && e.Parsed.MsgID == prev.Parsed.MsgID {
				// Merge content blocks into the previous entry
				prevBlocks := getContentBlocks(prev.Parsed)
				newBlocks := getContentBlocks(e.Parsed)
				prevBlocks = append(prevBlocks, newBlocks...)
				contentJSON, _ := json.Marshal(prevBlocks)
				prev.Parsed.Content = contentJSON
				// Keep the last non-empty model/usage/stop_reason
				if e.Parsed.Model != "" {
					prev.Parsed.Model = e.Parsed.Model
				}
				if e.Parsed.Usage != nil {
					prev.Parsed.Usage = e.Parsed.Usage
				}
				if e.Parsed.StopReason != "" {
					prev.Parsed.StopReason = e.Parsed.StopReason
				}
				continue
			}
		}
		merged = append(merged, e)
	}
	return merged
}

// ── Metadata scanning (fast single-pass) ──

type conversationMeta struct {
	Preview   string
	MsgCount  int
	CWD       string
	Version   string
	GitBranch string
	Slug      string
}

func scanConversationMeta(path string) conversationMeta {
	f, err := os.Open(path)
	if err != nil {
		return conversationMeta{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 1024*1024)

	var meta conversationMeta

	for scanner.Scan() {
		var quick struct {
			Type      string          `json:"type"`
			CWD       string          `json:"cwd"`
			Version   string          `json:"version"`
			GitBranch string          `json:"gitBranch"`
			Slug      string          `json:"slug"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &quick); err != nil {
			continue
		}

		if quick.Type == "user" || quick.Type == "assistant" {
			meta.MsgCount++
		}
		if meta.CWD == "" && quick.CWD != "" {
			meta.CWD = quick.CWD
		}
		if meta.Version == "" && quick.Version != "" {
			meta.Version = quick.Version
		}
		if meta.GitBranch == "" && quick.GitBranch != "" {
			meta.GitBranch = quick.GitBranch
		}
		if meta.Slug == "" && quick.Slug != "" {
			meta.Slug = quick.Slug
		}

		if meta.Preview == "" && quick.Type == "user" && len(quick.Message) > 0 {
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(quick.Message, &msg) == nil {
				var text string
				if json.Unmarshal(msg.Content, &text) == nil && text != "" {
					text = strings.ReplaceAll(text, "\n", " ")
					text = strings.Join(strings.Fields(text), " ")
					if len(text) > 100 {
						text = text[:97] + "..."
					}
					meta.Preview = text
				}
			}
		}
	}

	return meta
}

func quickMessageCount(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 256*1024)
	count := 0

	for scanner.Scan() {
		var quick struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(scanner.Bytes(), &quick) == nil {
			if quick.Type == "user" || quick.Type == "assistant" {
				count++
			}
		}
	}
	return count
}

// scanSubAgentDescription extracts a description from the first user message in a sub-agent JSONL.
func scanSubAgentDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 512*1024)

	for scanner.Scan() {
		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type != "user" {
			continue
		}
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(entry.Message, &msg) != nil {
			continue
		}
		// Content may be string or array
		var text string
		if json.Unmarshal(msg.Content, &text) == nil && text != "" {
			text = strings.ReplaceAll(text, "\n", " ")
			text = strings.Join(strings.Fields(text), " ")
			if len(text) > 60 {
				text = text[:57] + "..."
			}
			return text
		}
		// Try array of content blocks
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(msg.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					text = strings.ReplaceAll(b.Text, "\n", " ")
					text = strings.Join(strings.Fields(text), " ")
					if len(text) > 60 {
						text = text[:57] + "..."
					}
					return text
				}
			}
		}
		break // only check first user message
	}
	return ""
}

// ── Content block helpers ──

func getContentBlocks(pm *ParsedMessage) []ContentBlock {
	if pm == nil || len(pm.Content) == 0 {
		return nil
	}

	var str string
	if err := json.Unmarshal(pm.Content, &str); err == nil {
		return []ContentBlock{{Type: "text", Text: str}}
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(pm.Content, &blocks); err == nil {
		return blocks
	}

	return nil
}

func formatToolUse(name string, input json.RawMessage) string {
	var m map[string]any
	json.Unmarshal(input, &m)

	str := func(key string) string {
		if v, ok := m[key]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}

	truncate := func(s string, n int) string {
		if len(s) > n {
			return s[:n-3] + "..."
		}
		return s
	}

	switch name {
	case "Read":
		return fmt.Sprintf("Read: %s", str("file_path"))
	case "Write":
		return fmt.Sprintf("Write: %s", str("file_path"))
	case "Edit":
		return fmt.Sprintf("Edit: %s", str("file_path"))
	case "Bash":
		return fmt.Sprintf("Bash: %s", truncate(str("command"), 60))
	case "Grep":
		p := str("path")
		if p == "" {
			p = "."
		}
		return fmt.Sprintf("Grep: %q in %s", str("pattern"), p)
	case "Glob":
		return fmt.Sprintf("Glob: %s", str("pattern"))
	case "Agent":
		return fmt.Sprintf("Agent: %s", str("description"))
	case "Skill":
		return fmt.Sprintf("Skill: %s", str("skill"))
	case "TaskCreate":
		return fmt.Sprintf("TaskCreate: %s", truncate(str("subject"), 50))
	case "TaskUpdate":
		return fmt.Sprintf("TaskUpdate: #%s -> %s", str("taskId"), str("status"))
	default:
		return name
	}
}

// formatToolInput pretty-prints tool input JSON for the detail toggle view.
// Long string values are truncated to maxLen characters.
func formatToolInput(input json.RawMessage, width int) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return string(input)
	}
	maxValLen := 200
	if width > 0 && maxValLen > width {
		maxValLen = width
	}
	var b strings.Builder
	b.WriteString("{")
	first := true
	for k, v := range m {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString("\n")
		val := fmt.Sprintf("%v", v)
		if len(val) > maxValLen {
			val = val[:maxValLen-3] + "..."
		}
		fmt.Fprintf(&b, "  %q: %q", k, val)
	}
	b.WriteString("\n}")
	return b.String()
}

func formatTimestamp(ts string) string {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Local().Format("15:04:05")
		}
	}
	return ts
}

func formatTimestampFull(ts string) string {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Local().Format("2006-01-02 15:04:05")
		}
	}
	return ts
}

// ── System entries ──

// maxSystemBodyLen caps the rendered body of a system entry. api_error bodies
// carry a full Node stack trace; only the first line is signal.
const maxSystemBodyLen = 120

// formatSystemEntry renders a system transcript entry as a short label and a
// single-line body, both safe to print as-is. ok is false when the entry
// carries nothing worth showing, in which case the caller must skip it.
func formatSystemEntry(e Entry) (label, body string, ok bool) {
	label, body, ok = systemEntryParts(e)
	if !ok {
		return "", "", false
	}
	return label, truncateSystemBody(body), true
}

// systemEntryParts is the per-subtype half of formatSystemEntry; it does not
// normalize the body.
//
// Unknown subtypes that carry content fall through to a generic rendering: new
// harness versions add subtypes (stop_hook_summary, away_summary, ...), and
// silently dropping those is worse than an unstyled line.
func systemEntryParts(e Entry) (label, body string, ok bool) {
	switch e.Subtype {
	case "local_command":
		if e.Content == "" {
			return "", "", false
		}
		return "system", extractCommandName(e.Content), true

	case "compact_boundary":
		body = e.Content
		if body == "" {
			body = "Conversation compacted"
		}
		if md := e.CompactMetadata; md != nil {
			body = fmt.Sprintf("%s (pre %d \u2192 post %d tok", body, md.PreTokens, md.PostTokens)
			if md.DurationMs > 0 {
				body += fmt.Sprintf(", %s", formatDurationMs(md.DurationMs))
			}
			body += ")"
		}
		return "compact", body, true

	case "turn_duration":
		if e.DurationMs <= 0 {
			return "", "", false
		}
		body = formatDurationMs(e.DurationMs)
		if e.MessageCount > 0 {
			body += fmt.Sprintf(" \u00b7 %d msgs", e.MessageCount)
		}
		return "turn", body, true

	case "api_error":
		status, msg := parseSystemError(e.Error)
		switch {
		case status > 0 && msg != "":
			return "error", fmt.Sprintf("%d %s", status, msg), true
		case status > 0:
			return "error", fmt.Sprintf("HTTP %d", status), true
		case msg != "":
			return "error", msg, true
		}
		return "error", "request failed", true
	}

	if e.Content != "" {
		return "system", e.Content, true
	}
	return "", "", false
}

// truncateSystemBody folds a body onto one line and caps its length.
func truncateSystemBody(s string) string {
	full := collapseWhitespace(s, 0)
	runes := []rune(full)
	if len(runes) <= maxSystemBodyLen {
		return full
	}
	return string(runes[:maxSystemBodyLen]) + "\u2026"
}

// extractCommandName pulls the command out of a local_command system entry,
// whose content is "<command-name>/clear</command-name>". Anything else is
// returned unchanged.
func extractCommandName(content string) string {
	if idx := strings.Index(content, "<command-name>"); idx >= 0 {
		start := idx + len("<command-name>")
		if end := strings.Index(content[start:], "</command-name>"); end >= 0 {
			return content[start : start+end]
		}
	}
	return content
}

// parseSystemError digs the HTTP status and message out of an api_error
// payload. Either half can be missing: some failures carry only a status, and
// one observed shape carries only a type.
func parseSystemError(raw json.RawMessage) (status int, msg string) {
	if len(raw) == 0 {
		return 0, ""
	}
	var payload struct {
		Status int             `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, ""
	}
	return payload.Status, errorMessage(payload.Error)
}

// errorMessage digs the message out of an error payload. The nesting differs by
// producer: the router writes {"message": ...} while the API client wraps it as
// {"error": {"message": ...}}, so walk down until a message shows up.
func errorMessage(raw json.RawMessage) string {
	for depth := 0; depth < 3 && len(raw) > 0; depth++ {
		var level struct {
			Message string          `json:"message"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &level); err != nil {
			return ""
		}
		if level.Message != "" {
			return level.Message
		}
		raw = level.Error
	}
	return ""
}

// formatDurationMs renders a millisecond duration compactly: 950ms, 59s,
// 10m47s, 2h05m.
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// collapseWhitespace folds every whitespace run into a single space, drops
// other control characters, trims the edges, and truncates to max runes (max
// <= 0 means no limit). Shared by paste handling and system entry rendering,
// both of which must turn multi-line text into one line.
func collapseWhitespace(s string, max int) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if b.Len() > 0 {
				pendingSpace = true
			}
			continue
		}
		if r < 32 || r == 127 {
			continue // drop other control characters
		}
		if pendingSpace {
			b.WriteRune(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if max > 0 {
		if runes := []rune(out); len(runes) > max {
			out = string(runes[:max])
		}
	}
	return out
}

func readFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// renderMarkdownTerm renders markdown to styled terminal output using glamour.
func renderMarkdownTerm(md string, width int) string {
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	// Trim trailing whitespace from each line to prevent overflow in split-pane
	raw := strings.TrimRight(out, "\n ")
	parts := strings.Split(raw, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimRight(p, " ")
	}
	return strings.Join(parts, "\n")
}
