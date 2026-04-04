package main

// Provider abstracts a session data source (Claude Code, OpenCode, etc.).
type Provider interface {
	Name() string
	Available() bool
	LoadTree() (*TreeData, error)
	LoadConversation(path string) ([]Entry, error)
	SearchSessions(query, projectID string) []SearchResult
}

// SearchResult is a single match from session search.
type SearchResult struct {
	Source      string // provider name
	ProjectName string
	Title       string
	Preview     string
	Path        string // conversation path or session ID
	ModTime     string
	ProjIndex   int // index into provider's tree.Projects
}
