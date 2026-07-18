package main

import "context"

// Provider abstracts a session data source (Claude Code, OpenCode, etc.).
type Provider interface {
	Name() string
	Available() bool
	// LoadTree loads all data (used by web API and search).
	LoadTree() (*TreeData, error)
	// LoadProjectList returns project directories with conversation counts only (Level 0).
	LoadProjectList() (*TreeData, error)
	// EnrichProjectMeta fills in display name, last active, CLAUDE.md, memory (Level 1).
	EnrichProjectMeta(dirName, dirPath string) TreeProject
	// LoadProjectDetail loads a single project's full conversation list (Level 2).
	// The ctx is checked between file scans; if cancelled, returns partial results early.
	LoadProjectDetail(ctx context.Context, dirName, dirPath string, historyTitles map[string]string) *TreeProject
	// LoadConversation loads a single conversation's entries.
	LoadConversation(path string) ([]Entry, error)
	// ContentSearch searches within conversation content.
	// For Claude: uses rg/grep on filesystem. For OpenCode: uses SQL on database.
	ContentSearch(query string, projectID string) []SearchResult
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
	MsgCount    int
	CWD         string
}
