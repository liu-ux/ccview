package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

var debugStart time.Time

func debugLog(format string, args ...any) {
	if debugStart.IsZero() {
		return
	}
	log.Printf(format, args...)
}

func main() {
	exportPath := flag.String("export", "", "Export conversation to HTML file")
	filePath := flag.String("file", "", "Path to a specific JSONL conversation file")
	web := flag.Bool("web", false, "Start web server for interactive browsing")
	port := flag.Int("port", 3333, "Port for web server (used with --web)")
	debugLog := flag.String("debug", "", "Write debug timing logs to this file")
	flag.Parse()

	// Accept positional argument as file path
	if *filePath == "" && flag.NArg() > 0 {
		*filePath = flag.Arg(0)
	}

	// Debug logging
	if *debugLog != "" {
		f, err := os.OpenFile(*debugLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err == nil {
			log.SetOutput(f)
			log.SetFlags(log.Ltime | log.Lmicroseconds)
			debugStart = time.Now()
			log.Printf("main: startup")
		}
	}

	// Web server mode
	if *web {
		if err := startServer(*port); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Direct export mode (no TUI)
	if *exportPath != "" {
		src := *filePath
		if src == "" {
			fmt.Fprintln(os.Stderr, "Usage: ccview --export output.html --file conversation.jsonl")
			os.Exit(1)
		}
		entries, err := parseConversation(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", src, err)
			os.Exit(1)
		}
		if err := exportHTML(entries, *exportPath, src); err != nil {
			fmt.Fprintf(os.Stderr, "Error exporting: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported to %s\n", *exportPath)
		return
	}

	// Discover available providers
	var providers []Provider
	claude := &ClaudeProvider{}
	if claude.Available() {
		providers = append(providers, claude)
	}
	opencode := NewOpenCodeProvider()
	if opencode.Available() {
		providers = append(providers, opencode)
	}
	if len(providers) == 0 && *filePath == "" {
		fmt.Fprintln(os.Stderr, "No session data found. Checked ~/.claude/ and ~/.local/share/opencode/")
		os.Exit(1)
	}

	// Interactive TUI mode
	p := tea.NewProgram(newModel(*filePath, providers))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
