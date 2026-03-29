package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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

	return entries, scanner.Err()
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
