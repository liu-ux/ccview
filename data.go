package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ── Tree structure returned by /api/tree ──

type TreeData struct {
	ClaudeDir      string        `json:"claudeDir"`
	GlobalClaudeMD string        `json:"globalClaudeMD,omitempty"`
	Plans          []TreeFileRef `json:"plans,omitempty"`
	Projects       []TreeProject `json:"projects"`
	Stats          TreeStats     `json:"stats"`
}

type TreeStats struct {
	TotalProjects      int `json:"totalProjects"`
	TotalConversations int `json:"totalConversations"`
	TotalMessages      int `json:"totalMessages"`
}

type TreeFileRef struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type TreeProject struct {
	DisplayName   string             `json:"displayName"`
	DirName       string             `json:"dirName"`
	DirPath       string             `json:"dirPath"`
	ClaudeMD      string             `json:"claudeMD,omitempty"`
	MemoryFiles   []TreeFileRef      `json:"memoryFiles,omitempty"`
	Conversations []TreeConversation `json:"conversations"`
	LastActive    string             `json:"lastActive"`
	ConvCount     int                `json:"convCount"`
	MsgCount      int                `json:"msgCount"`
	Source        string             `json:"source,omitempty"` // "claude" or "opencode"
}

type TreeConversation struct {
	SessionID     string         `json:"sessionId"`
	Path          string         `json:"path"`
	ModTime       string         `json:"modTime"`
	Title         string         `json:"title"`
	Preview       string         `json:"preview"`
	MsgCount      int            `json:"msgCount"`
	CWD           string         `json:"cwd,omitempty"`
	Version       string         `json:"version,omitempty"`
	GitBranch     string         `json:"gitBranch,omitempty"`
	Slug          string         `json:"slug,omitempty"`
	SubAgents     []TreeSubAgent `json:"subAgents,omitempty"`
	FileEditCount int            `json:"fileEditCount,omitempty"`
	ToolResults   int            `json:"toolResults,omitempty"`
	Source        string         `json:"source,omitempty"` // "claude" or "opencode"
}

type TreeSubAgent struct {
	Name        string `json:"name"`
	AgentType   string `json:"agentType,omitempty"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	ModTime     string `json:"modTime"`
	MsgCount    int    `json:"msgCount,omitempty"`
}

// ── Loading ──

func loadTree() (*TreeData, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	claudeDir := filepath.Join(home, ".claude")

	data := &TreeData{ClaudeDir: claudeDir}

	// Global CLAUDE.md
	globalMD := filepath.Join(claudeDir, "CLAUDE.md")
	if fileExists(globalMD) {
		data.GlobalClaudeMD = globalMD
	}

	// Plans
	data.Plans = loadFileRefs(filepath.Join(claudeDir, "plans"), ".md")

	// History titles (sessionId → first user input)
	historyTitles := loadHistoryTitles(filepath.Join(claudeDir, "history.jsonl"))

	// Projects
	data.Projects = loadProjects(
		filepath.Join(claudeDir, "projects"),
		filepath.Join(claudeDir, "file-history"),
		historyTitles,
	)

	// Compute stats
	for _, p := range data.Projects {
		data.Stats.TotalProjects++
		data.Stats.TotalConversations += p.ConvCount
		data.Stats.TotalMessages += p.MsgCount
	}

	return data, nil
}

func loadFileRefs(dir, suffix string) []TreeFileRef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var refs []TreeFileRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		refs = append(refs, TreeFileRef{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
		})
	}
	return refs
}

// loadHistoryTitles reads history.jsonl and returns a map of sessionId → first display text.
func loadHistoryTitles(path string) map[string]string {
	titles := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return titles
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 512*1024)

	for scanner.Scan() {
		var entry struct {
			Display   string `json:"display"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.SessionID == "" {
			continue
		}
		// Only keep the first display text per session (the opening message)
		if _, exists := titles[entry.SessionID]; exists {
			continue
		}
		text := entry.Display
		if text == "" || strings.HasPrefix(text, "/") {
			continue // skip slash commands
		}
		text = strings.ReplaceAll(text, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		if len(text) > 80 {
			text = text[:77] + "..."
		}
		titles[entry.SessionID] = text
	}
	return titles
}

func loadProjects(projectsDir, fileHistoryDir string, historyTitles map[string]string) []TreeProject {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var projects []TreeProject
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirPath := filepath.Join(projectsDir, e.Name())
		proj := loadProject(e.Name(), dirPath, fileHistoryDir, historyTitles)
		if len(proj.Conversations) > 0 || proj.ClaudeMD != "" || len(proj.MemoryFiles) > 0 {
			projects = append(projects, proj)
		}
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastActive > projects[j].LastActive
	})

	return projects
}

func loadProject(dirName, dirPath, fileHistoryDir string, historyTitles map[string]string) TreeProject {
	proj := TreeProject{
		DirName:     dirName,
		DirPath:     dirPath,
		DisplayName: dirName, // updated from first conversation's CWD
	}

	// CLAUDE.md
	claudeMD := filepath.Join(dirPath, "CLAUDE.md")
	if fileExists(claudeMD) {
		proj.ClaudeMD = claudeMD
	}

	// Memory
	memDir := filepath.Join(dirPath, "memory")
	if dirExists(memDir) {
		proj.MemoryFiles = loadFileRefs(memDir, ".md")
	}

	// Conversations
	entries, _ := os.ReadDir(dirPath)
	var latestMod time.Time

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		convPath := filepath.Join(dirPath, e.Name())
		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
		info, _ := e.Info()
		modTime := time.Time{}
		if info != nil {
			modTime = info.ModTime()
		}

		if modTime.After(latestMod) {
			latestMod = modTime
		}

		meta := scanConversationMeta(convPath)

		if proj.DisplayName == dirName && meta.CWD != "" {
			proj.DisplayName = shortenPath(meta.CWD)
		}

		// Title: history.jsonl display > preview > slug > sessionID
		title := historyTitles[sessionID]
		if title == "" {
			title = meta.Preview
		}
		if title == "" && meta.Slug != "" {
			title = meta.Slug
		}
		if title == "" && len(sessionID) >= 8 {
			title = sessionID[:8]
		}

		conv := TreeConversation{
			SessionID: sessionID,
			Path:      convPath,
			ModTime:   modTime.Format(time.RFC3339),
			Title:     title,
			Preview:   meta.Preview,
			MsgCount:  meta.MsgCount,
			CWD:       meta.CWD,
			Version:   meta.Version,
			GitBranch: meta.GitBranch,
			Slug:      meta.Slug,
		}

		proj.MsgCount += meta.MsgCount

		// Sub-agents
		sessionDir := filepath.Join(dirPath, sessionID)
		conv.SubAgents = findSubAgents(filepath.Join(sessionDir, "subagents"))

		// Tool results
		conv.ToolResults = countDirEntries(filepath.Join(sessionDir, "tool-results"))

		// File edits
		conv.FileEditCount = countDirEntries(filepath.Join(fileHistoryDir, sessionID))

		proj.Conversations = append(proj.Conversations, conv)
	}

	proj.ConvCount = len(proj.Conversations)

	sort.Slice(proj.Conversations, func(i, j int) bool {
		return proj.Conversations[i].ModTime > proj.Conversations[j].ModTime
	})

	if !latestMod.IsZero() {
		proj.LastActive = latestMod.Format(time.RFC3339)
	}

	return proj
}

func findSubAgents(dir string) []TreeSubAgent {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var agents []TreeSubAgent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		agentPath := filepath.Join(dir, e.Name())
		name := strings.TrimSuffix(e.Name(), ".jsonl")
		name = strings.TrimPrefix(name, "agent-")

		info, _ := e.Info()
		modTime := ""
		if info != nil {
			modTime = info.ModTime().Format(time.RFC3339)
		}

		// Try to read .meta.json for agent type
		agentType := ""
		metaPath := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".jsonl")+".meta.json")
		if metaData, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				AgentType string `json:"agentType"`
			}
			if json.Unmarshal(metaData, &meta) == nil {
				agentType = meta.AgentType
			}
		}

		// Quick count and description from first user message
		msgCount := quickMessageCount(agentPath)
		desc := scanSubAgentDescription(agentPath)

		agents = append(agents, TreeSubAgent{
			Name:        name,
			AgentType:   agentType,
			Description: desc,
			Path:        agentPath,
			ModTime:     modTime,
			MsgCount:    msgCount,
		})
	}

	return agents
}

// ── Helpers ──

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func countDirEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	return count
}

func shortenPath(p string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
