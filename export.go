package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"os"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

const exportHTMLHeader = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Claude Code Conversation</title>
<style>
* { box-sizing: border-box; }
body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif;
    max-width: 960px;
    margin: 0 auto;
    padding: 24px;
    background: #0d1117;
    color: #e6edf3;
    line-height: 1.7;
}
.header {
    border-bottom: 2px solid #30363d;
    padding-bottom: 16px;
    margin-bottom: 24px;
}
.header h1 { color: #7B2FBE; margin: 0 0 4px 0; font-size: 1.5em; }
.header .meta { color: #8b949e; font-size: 0.85em; }
.message {
    margin: 16px 0;
    border-radius: 8px;
    padding: 16px 20px;
}
.user {
    background: #161b22;
    border-left: 4px solid #5B5FC7;
}
.assistant {
    background: #0f1a14;
    border-left: 4px solid #2D8B4E;
}
.role-label {
    font-weight: 700;
    font-size: 0.8em;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 10px;
    display: flex;
    justify-content: space-between;
    align-items: center;
}
.role-label .ts { font-weight: 400; color: #8b949e; font-size: 0.9em; }
.user .role-label { color: #7B8CDE; }
.assistant .role-label { color: #4EBE7B; }
.content p { margin: 8px 0; }
.content ul, .content ol { padding-left: 24px; }
.content li { margin: 4px 0; }
.thinking {
    color: #8b949e;
    font-style: italic;
    background: #161b22;
    padding: 10px 14px;
    border-radius: 6px;
    margin: 10px 0;
    font-size: 0.9em;
    border: 1px solid #21262d;
    white-space: pre-wrap;
    max-height: 300px;
    overflow-y: auto;
}
.thinking summary {
    cursor: pointer;
    font-weight: 600;
    color: #8b949e;
}
.tool-use {
    color: #E5C07B;
    font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace;
    font-size: 0.85em;
    padding: 6px 10px;
    background: #1c1f26;
    border-radius: 4px;
    margin: 6px 0;
    border: 1px solid #30363d;
}
.tool-result {
    color: #8b949e;
    font-size: 0.8em;
    padding: 2px 10px;
}
.tokens {
    color: #484f58;
    font-size: 0.75em;
    margin-top: 12px;
    font-family: monospace;
}
pre {
    background: #161b22;
    padding: 16px;
    border-radius: 6px;
    overflow-x: auto;
    font-size: 0.9em;
    border: 1px solid #30363d;
    line-height: 1.5;
}
code {
    font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace;
}
p code, li code {
    background: #1c2128;
    padding: 2px 6px;
    border-radius: 3px;
    font-size: 0.9em;
}
.system-msg {
    color: #8b949e;
    font-style: italic;
    font-size: 0.8em;
    padding: 4px 20px;
    border-left: 2px solid #30363d;
    margin: 8px 0;
}
hr { border: none; border-top: 1px solid #21262d; margin: 24px 0; }
a { color: #58a6ff; }
</style>
</head>
<body>
<div class="header">
<h1>Claude Code Conversation</h1>
<div class="meta">Source: %s</div>
</div>
`

// markdownToHTML converts markdown to HTML using goldmark.
func markdownToHTML(text string) string {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(text), &buf); err != nil {
		return "<p>" + html.EscapeString(text) + "</p>"
	}
	return buf.String()
}

// exportHTMLTo writes a rendered conversation to the given writer.
func exportHTMLTo(entries []Entry, w io.Writer, sourcePath string) error {
	fmt.Fprintf(w, exportHTMLHeader, html.EscapeString(sourcePath))

	for _, entry := range entries {
		switch entry.Type {
		case "user":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)

			isToolResult := false
			for _, b := range blocks {
				if b.Type == "tool_result" {
					isToolResult = true
					break
				}
			}
			if isToolResult {
				fmt.Fprint(w, `<div class="tool-result">[result] returned</div>`)
				continue
			}

			ts := formatTimestampFull(entry.Timestamp)
			fmt.Fprint(w, `<div class="message user">`)
			fmt.Fprintf(w, `<div class="role-label"><span>User</span><span class="ts">%s</span></div>`, html.EscapeString(ts))
			fmt.Fprint(w, `<div class="content">`)
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					fmt.Fprint(w, markdownToHTML(b.Text))
				}
			}
			fmt.Fprint(w, `</div></div>`)

		case "assistant":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			modelName := entry.Parsed.Model
			ts := formatTimestampFull(entry.Timestamp)

			fmt.Fprint(w, `<div class="message assistant">`)
			label := "Assistant"
			if modelName != "" {
				label = fmt.Sprintf("Assistant (%s)", modelName)
			}
			fmt.Fprintf(w, `<div class="role-label"><span>%s</span><span class="ts">%s</span></div>`,
				html.EscapeString(label), html.EscapeString(ts))
			fmt.Fprint(w, `<div class="content">`)

			for _, b := range blocks {
				switch b.Type {
				case "thinking":
					if b.Thinking != "" {
						fmt.Fprintf(w, `<details class="thinking"><summary>Thinking...</summary><div>%s</div></details>`,
							html.EscapeString(b.Thinking))
					}
				case "text":
					if b.Text != "" {
						fmt.Fprint(w, markdownToHTML(b.Text))
					}
				case "tool_use":
					summary := formatToolUse(b.Name, b.Input)
					fmt.Fprintf(w, `<div class="tool-use">[tool] %s</div>`, html.EscapeString(summary))
				}
			}
			fmt.Fprint(w, `</div>`)

			if entry.Parsed.Usage != nil {
				u := entry.Parsed.Usage
				fmt.Fprintf(w, `<div class="tokens">tokens: in=%d out=%d</div>`, u.InputTokens, u.OutputTokens)
			}

			fmt.Fprint(w, `</div>`)

		case "system":
			if entry.Subtype == "local_command" {
				cmd := entry.Content
				if idx := strings.Index(cmd, "<command-name>"); idx >= 0 {
					start := idx + len("<command-name>")
					if end := strings.Index(cmd[start:], "</command-name>"); end >= 0 {
						cmd = cmd[start : start+end]
					}
				}
				fmt.Fprintf(w, `<div class="system-msg">[system] %s</div>`, html.EscapeString(cmd))
			}
		}
	}

	fmt.Fprint(w, "\n</body>\n</html>\n")
	return nil
}

// exportHTML writes a rendered conversation to a file.
func exportHTML(entries []Entry, outPath, sourcePath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return exportHTMLTo(entries, f, sourcePath)
}
