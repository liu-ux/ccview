package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeProvider loads sessions from Claude Code's ~/.claude/ directory.
type ClaudeProvider struct{}

func (p *ClaudeProvider) Name() string { return "Claude Code" }

func (p *ClaudeProvider) Available() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return dirExists(filepath.Join(home, ".claude", "projects"))
}

func (p *ClaudeProvider) LoadTree() (*TreeData, error) {
	tree, err := loadTree()
	if err != nil {
		return nil, err
	}
	// Tag all projects and conversations with source
	for i := range tree.Projects {
		tree.Projects[i].Source = "claude"
		for j := range tree.Projects[i].Conversations {
			tree.Projects[i].Conversations[j].Source = "claude"
		}
	}
	return tree, nil
}

func (p *ClaudeProvider) LoadConversation(path string) ([]Entry, error) {
	return parseConversation(path)
}

func (p *ClaudeProvider) LoadProjectList() (*TreeData, error) {
	return loadProjectDirs()
}

func (p *ClaudeProvider) EnrichProjectMeta(dirName, dirPath string) TreeProject {
	return enrichProjectMeta(dirName, dirPath)
}

func (p *ClaudeProvider) LoadProjectDetail(ctx context.Context, dirName, dirPath string, historyTitles map[string]string) *TreeProject {
	return loadProjectDetail(ctx, dirName, dirPath, historyTitles)
}

func (p *ClaudeProvider) SearchSessions(query, projectID string) []SearchResult {
	tree, err := p.LoadTree()
	if err != nil || tree == nil {
		return nil
	}
	q := strings.ToLower(query)
	var results []SearchResult
	for i, proj := range tree.Projects {
		if projectID != "" && proj.DirName != projectID {
			continue
		}
		for _, conv := range proj.Conversations {
			title := strings.ToLower(conv.Title)
			preview := strings.ToLower(conv.Preview)
			slug := strings.ToLower(conv.Slug)
			projName := strings.ToLower(proj.DisplayName)
			if strings.Contains(title, q) || strings.Contains(preview, q) ||
				strings.Contains(slug, q) || strings.Contains(projName, q) {
				results = append(results, SearchResult{
					Source:      "claude",
					ProjectName: proj.DisplayName,
					Title:       conv.Title,
					Preview:     conv.Preview,
					Path:        conv.Path,
					ModTime:     conv.ModTime,
					ProjIndex:   i,
				})
			}
		}
	}
	return results
}
