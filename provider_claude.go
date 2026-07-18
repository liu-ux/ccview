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

func (p *ClaudeProvider) LoadProjectList() (*TreeData, error) {
	return loadProjectDirs()
}

func (p *ClaudeProvider) EnrichProjectMeta(dirName, dirPath string) TreeProject {
	return enrichProjectMeta(dirName, dirPath)
}

func (p *ClaudeProvider) LoadProjectDetail(ctx context.Context, dirName, dirPath string, historyTitles map[string]string) *TreeProject {
	return loadProjectDetail(ctx, dirName, dirPath, historyTitles)
}

func (p *ClaudeProvider) LoadConversation(path string) ([]Entry, error) {
	return parseConversation(path)
}

func (p *ClaudeProvider) ContentSearch(query string, projectID string) []SearchResult {
	home, _ := os.UserHomeDir()
	claudeDir := filepath.Join(home, ".claude")
	matchedFiles := searchContentInFiles(query, claudeDir)
	if len(matchedFiles) == 0 {
		return nil
	}

	projectsDir := filepath.Join(claudeDir, "projects")
	projEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var results []SearchResult
	for _, pe := range projEntries {
		if !pe.IsDir() {
			continue
		}
		if projectID != "" && pe.Name() != projectID {
			continue
		}
		projPath := filepath.Join(projectsDir, pe.Name())
		convEntries, _ := os.ReadDir(projPath)
		for _, ce := range convEntries {
			if ce.IsDir() || !strings.HasSuffix(ce.Name(), ".jsonl") {
				continue
			}
			convPath := filepath.Clean(filepath.Join(projPath, ce.Name()))
			if !matchedFiles[convPath] {
				continue
			}
			sessionID := strings.TrimSuffix(ce.Name(), ".jsonl")
			title := sessionID
			if len(title) > 8 {
				title = title[:8]
			}
			results = append(results, SearchResult{
				Source:      "claude",
				ProjectName: pe.Name(),
				Title:       title,
				Path:        convPath,
			})
		}
	}
	return results
}
