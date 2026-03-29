package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func main() {
	exportPath := flag.String("export", "", "Export conversation to HTML file")
	filePath := flag.String("file", "", "Path to a specific JSONL conversation file")
	web := flag.Bool("web", false, "Start web server for interactive browsing")
	port := flag.Int("port", 3333, "Port for web server (used with --web)")
	flag.Parse()

	// Accept positional argument as file path
	if *filePath == "" && flag.NArg() > 0 {
		*filePath = flag.Arg(0)
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
			fmt.Fprintln(os.Stderr, "Usage: claude-log --export output.html --file conversation.jsonl")
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

	// Interactive TUI mode
	p := tea.NewProgram(newModel(*filePath))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
