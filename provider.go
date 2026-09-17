package main

import (
	"context"
	"strings"
	"unicode/utf8"
)

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
	//
	// The query is matched literally and case-sensitively on every backend, and
	// the result is complete: truncation is a display concern, applied by the
	// caller. Prefix narrowing depends on both properties.
	ContentSearch(query string, projectID string) ContentSearchResult
}

// contentMatchWindowRunes bounds how much text a ContentMatch keeps after each
// match, and therefore how far a query can be extended before it can no longer
// be answered from the windows alone.
const contentMatchWindowRunes = 50

// ContentMatch is the text of one entity's matching lines, kept so that a
// longer query can be answered without reading the entity again: any query
// that extends the one that produced the match, by at most
// contentMatchWindowRunes runes, can be tested against the windows alone.
//
// Every occurrence of the query inside a line must be inside its window, or a
// narrowed search would silently miss matches.
type ContentMatch struct {
	Path    string   // SearchResult.Path of the entity this text came from
	Windows []string // one window per matching line
}

// ContentSearchResult is a complete content search result.
type ContentSearchResult struct {
	Results []SearchResult
	Matches []ContentMatch
}

// contentWindows returns one window per line of text that contains query. Each
// window starts at the line's first match and ends contentMatchWindowRunes
// runes past its last match, clamped to the end of the line. Because every
// occurrence in the line lies inside the window, testing a window for a longer
// query is equivalent to testing the whole file — as long as the query has not
// grown by more than contentMatchWindowRunes runes.
func contentWindows(text, query string) []string {
	if query == "" {
		return nil
	}
	var windows []string
	for _, line := range strings.Split(text, "\n") {
		first := strings.Index(line, query)
		if first < 0 {
			continue
		}
		lineRunes := []rune(line)
		start := utf8.RuneCountInString(line[:first])
		end := utf8.RuneCountInString(line[:strings.LastIndex(line, query)+len(query)]) + contentMatchWindowRunes
		if end > len(lineRunes) {
			end = len(lineRunes)
		}
		windows = append(windows, string(lineRunes[start:end]))
	}
	return windows
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
