package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// OpenCodeProvider loads sessions from OpenCode's SQLite database.
type OpenCodeProvider struct {
	dbPath string
}

func NewOpenCodeProvider() *OpenCodeProvider {
	home, _ := os.UserHomeDir()
	return &OpenCodeProvider{
		dbPath: filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
	}
}

func (p *OpenCodeProvider) Name() string { return "OpenCode" }

func (p *OpenCodeProvider) Available() bool {
	return fileExists(p.dbPath)
}

func (p *OpenCodeProvider) openDB() (*sql.DB, error) {
	return sql.Open("sqlite", p.dbPath+"?mode=ro&_journal_mode=WAL")
}

func (p *OpenCodeProvider) LoadTree() (*TreeData, error) {
	db, err := p.openDB()
	if err != nil {
		return nil, fmt.Errorf("opencode db: %w", err)
	}
	defer db.Close()

	data := &TreeData{}

	// Load projects
	projRows, err := db.Query(`SELECT id, worktree, name, time_created, time_updated FROM project ORDER BY time_updated DESC`)
	if err != nil {
		return nil, fmt.Errorf("opencode projects: %w", err)
	}
	defer projRows.Close()

	type dbProject struct {
		id          string
		worktree    string
		name        sql.NullString
		timeCreated int64
		timeUpdated int64
	}

	var dbProjects []dbProject
	for projRows.Next() {
		var dp dbProject
		if err := projRows.Scan(&dp.id, &dp.worktree, &dp.name, &dp.timeCreated, &dp.timeUpdated); err != nil {
			continue
		}
		dbProjects = append(dbProjects, dp)
	}

	for _, dp := range dbProjects {
		proj := TreeProject{
			DirName: dp.id,
			DirPath: dp.worktree,
			Source:  "opencode",
		}
		if dp.name.Valid && dp.name.String != "" {
			proj.DisplayName = dp.name.String
		} else {
			proj.DisplayName = shortenPath(dp.worktree)
		}

		// Load sessions for this project
		sessRows, err := db.Query(`
			SELECT s.id, s.title, s.slug, s.directory, s.time_created, s.time_updated, s.parent_id,
			       (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id) as msg_count
			FROM session s
			WHERE s.project_id = ? AND s.parent_id IS NULL AND s.time_archived IS NULL
			ORDER BY s.time_updated DESC
		`, dp.id)
		if err != nil {
			continue
		}

		var latestTime int64
		for sessRows.Next() {
			var (
				sid, title, slug, dir string
				created, updated      int64
				parentID              sql.NullString
				msgCount              int
			)
			if err := sessRows.Scan(&sid, &title, &slug, &dir, &created, &updated, &parentID, &msgCount); err != nil {
				continue
			}

			if updated > latestTime {
				latestTime = updated
			}

			modTime := time.UnixMilli(updated).Format(time.RFC3339)
			conv := TreeConversation{
				SessionID: sid,
				Path:      sid, // For OpenCode, path is the session ID
				ModTime:   modTime,
				Title:     title,
				Slug:      slug,
				CWD:       dir,
				MsgCount:  msgCount,
				Source:    "opencode",
			}

			// Get preview from first user message
			conv.Preview = p.getSessionPreview(db, sid)

			// Load child sessions (subagents)
			conv.SubAgents = p.loadChildSessions(db, sid)

			proj.Conversations = append(proj.Conversations, conv)
			proj.MsgCount += msgCount
		}
		sessRows.Close()

		proj.ConvCount = len(proj.Conversations)
		if latestTime > 0 {
			proj.LastActive = time.UnixMilli(latestTime).Format(time.RFC3339)
		}

		if proj.ConvCount > 0 {
			data.Projects = append(data.Projects, proj)
		}
	}

	// Compute stats
	for _, p := range data.Projects {
		data.Stats.TotalProjects++
		data.Stats.TotalConversations += p.ConvCount
		data.Stats.TotalMessages += p.MsgCount
	}

	return data, nil
}

func (p *OpenCodeProvider) getSessionPreview(db *sql.DB, sessionID string) string {
	var msgData string
	err := db.QueryRow(`
		SELECT m.data FROM message m
		WHERE m.session_id = ? AND json_extract(m.data, '$.role') = 'user'
		ORDER BY m.time_created ASC LIMIT 1
	`, sessionID).Scan(&msgData)
	if err != nil {
		return ""
	}

	// Get first text part for this message
	var msgID string
	// Extract message ID from data or use a different approach
	err = db.QueryRow(`
		SELECT m.id FROM message m
		WHERE m.session_id = ? AND json_extract(m.data, '$.role') = 'user'
		ORDER BY m.time_created ASC LIMIT 1
	`, sessionID).Scan(&msgID)
	if err != nil {
		return ""
	}

	var partData string
	err = db.QueryRow(`
		SELECT data FROM part
		WHERE message_id = ? AND json_extract(data, '$.type') = 'text'
		ORDER BY time_created ASC LIMIT 1
	`, msgID).Scan(&partData)
	if err != nil {
		return ""
	}

	var part struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(partData), &part) != nil {
		return ""
	}

	text := strings.ReplaceAll(part.Text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 100 {
		text = text[:97] + "..."
	}
	return text
}

func (p *OpenCodeProvider) loadChildSessions(db *sql.DB, parentID string) []TreeSubAgent {
	rows, err := db.Query(`
		SELECT s.id, s.title, s.slug, s.time_updated,
		       (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id) as msg_count
		FROM session s
		WHERE s.parent_id = ?
		ORDER BY s.time_created ASC
	`, parentID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var agents []TreeSubAgent
	for rows.Next() {
		var (
			sid, title, slug string
			updated          int64
			msgCount         int
		)
		if err := rows.Scan(&sid, &title, &slug, &updated, &msgCount); err != nil {
			continue
		}
		name := slug
		if name == "" {
			name = title
		}
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		agents = append(agents, TreeSubAgent{
			Name:    name,
			Path:    sid,
			ModTime: time.UnixMilli(updated).Format(time.RFC3339),
			MsgCount: msgCount,
		})
	}
	return agents
}

func (p *OpenCodeProvider) LoadProjectList() (*TreeData, error) {
	return p.LoadTree()
}

func (p *OpenCodeProvider) EnrichProjectMeta(dirName, dirPath string) TreeProject {
	return TreeProject{DirName: dirName, DirPath: dirPath, Source: "opencode"}
}

func (p *OpenCodeProvider) LoadProjectDetail(ctx context.Context, dirName, dirPath string, historyTitles map[string]string) *TreeProject {
	tree, err := p.LoadTree()
	if err != nil {
		return nil
	}
	for i := range tree.Projects {
		if tree.Projects[i].DirName == dirName {
			return &tree.Projects[i]
		}
	}
	return nil
}

func (p *OpenCodeProvider) LoadConversation(sessionID string) ([]Entry, error) {
	db, err := p.openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Load messages ordered by creation time
	msgRows, err := db.Query(`
		SELECT m.id, m.data, m.time_created
		FROM message m
		WHERE m.session_id = ?
		ORDER BY m.time_created ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer msgRows.Close()

	var entries []Entry
	for msgRows.Next() {
		var (
			msgID   string
			msgData string
			created int64
		)
		if err := msgRows.Scan(&msgID, &msgData, &created); err != nil {
			continue
		}

		entry, err := p.convertMessage(db, msgID, msgData, created)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// ocMessageData represents the JSON structure in OpenCode's message.data column.
type ocMessageData struct {
	Role       string `json:"role"`
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Mode       string `json:"mode"`
	Agent      string `json:"agent"`
	Finish     string `json:"finish"`
	Time       struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Path struct {
		CWD  string `json:"cwd"`
		Root string `json:"root"`
	} `json:"path"`
	Tokens struct {
		Total     int `json:"total"`
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

func (p *OpenCodeProvider) convertMessage(db *sql.DB, msgID, msgData string, created int64) (Entry, error) {
	var md ocMessageData
	if err := json.Unmarshal([]byte(msgData), &md); err != nil {
		return Entry{}, err
	}

	ts := time.UnixMilli(created).Format(time.RFC3339)
	entry := Entry{
		Type:      md.Role,
		UUID:      msgID,
		Timestamp: ts,
		CWD:       md.Path.CWD,
	}

	// Build content blocks from parts
	partRows, err := db.Query(`
		SELECT data FROM part
		WHERE message_id = ?
		ORDER BY time_created ASC
	`, msgID)
	if err != nil {
		return entry, nil
	}
	defer partRows.Close()

	var blocks []ContentBlock
	var usage *Usage

	for partRows.Next() {
		var partData string
		if err := partRows.Scan(&partData); err != nil {
			continue
		}

		var partType struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(partData), &partType) != nil {
			continue
		}

		switch partType.Type {
		case "text":
			var tp struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil && tp.Text != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: tp.Text})
			}

		case "tool":
			var tp struct {
				CallID string `json:"callID"`
				Tool   string `json:"tool"`
				State  struct {
					Status string          `json:"status"`
					Input  json.RawMessage `json:"input"`
					Output string          `json:"output"`
				} `json:"state"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil {
				blocks = append(blocks, ContentBlock{
					Type:  "tool_use",
					Name:  capitalizeToolName(tp.Tool),
					ID:    tp.CallID,
					Input: tp.State.Input,
				})
			}

		case "reasoning":
			var tp struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil && tp.Text != "" {
				blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: tp.Text})
			}

		case "step-finish":
			var tp struct {
				Tokens struct {
					Total     int `json:"total"`
					Input     int `json:"input"`
					Output    int `json:"output"`
					Reasoning int `json:"reasoning"`
					Cache     struct {
						Read  int `json:"read"`
						Write int `json:"write"`
					} `json:"cache"`
				} `json:"tokens"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil && tp.Tokens.Total > 0 {
				usage = &Usage{
					InputTokens:          tp.Tokens.Input,
					OutputTokens:         tp.Tokens.Output,
					CacheReadInputTokens: tp.Tokens.Cache.Read,
				}
			}

		case "file":
			var tp struct {
				Filename string `json:"filename"`
				URL      string `json:"url"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil && tp.Filename != "" {
				blocks = append(blocks, ContentBlock{
					Type: "text",
					Text: fmt.Sprintf("📎 %s", tp.Filename),
				})
			}

		case "patch":
			var tp struct {
				Files []string `json:"files"`
			}
			if json.Unmarshal([]byte(partData), &tp) == nil && len(tp.Files) > 0 {
				fileList := make([]string, len(tp.Files))
				for i, f := range tp.Files {
					fileList[i] = shortenPath(f)
				}
				blocks = append(blocks, ContentBlock{
					Type: "text",
					Text: fmt.Sprintf("[patch] %s", strings.Join(fileList, ", ")),
				})
			}
		}
	}

	// Build the ParsedMessage
	contentJSON, _ := json.Marshal(blocks)
	pm := &ParsedMessage{
		Role:  md.Role,
		Model: md.ModelID,
		Usage: usage,
	}
	pm.Content = contentJSON

	entry.Parsed = pm

	return entry, nil
}

// capitalizeToolName maps opencode tool names to display-friendly names.
func capitalizeToolName(name string) string {
	mapping := map[string]string{
		"read":       "Read",
		"write":      "Write",
		"edit":       "Edit",
		"bash":       "Bash",
		"grep":       "Grep",
		"glob":       "Glob",
		"agent":      "Agent",
		"skill":      "Skill",
		"fetch":      "Fetch",
		"list_files": "ListFiles",
		"search":     "Search",
		"todoread":   "TodoRead",
		"todowrite":  "TodoWrite",
	}
	if mapped, ok := mapping[name]; ok {
		return mapped
	}
	// Capitalize first letter
	if len(name) > 0 {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return name
}

func (p *OpenCodeProvider) ContentSearch(query string, projectID string) []SearchResult {
	db, err := p.openDB()
	if err != nil {
		return nil
	}
	defer db.Close()

	q := "%" + query + "%"
	var rows *sql.Rows

	if projectID != "" {
		rows, err = db.Query(`
			SELECT DISTINCT s.id, s.title, p.worktree, p.name, s.time_updated
			FROM part pt
			JOIN message m ON pt.message_id = m.id
			JOIN session s ON m.session_id = s.id
			JOIN project p ON s.project_id = p.id
			WHERE s.project_id = ? AND s.parent_id IS NULL AND s.time_archived IS NULL
			  AND pt.data LIKE ?
			ORDER BY s.time_updated DESC
			LIMIT 50
		`, projectID, q)
	} else {
		rows, err = db.Query(`
			SELECT DISTINCT s.id, s.title, p.worktree, p.name, s.time_updated
			FROM part pt
			JOIN message m ON pt.message_id = m.id
			JOIN session s ON m.session_id = s.id
			JOIN project p ON s.project_id = p.id
			WHERE s.parent_id IS NULL AND s.time_archived IS NULL
			  AND pt.data LIKE ?
			ORDER BY s.time_updated DESC
			LIMIT 50
		`, q)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var (
			sid, title string
			worktree   string
			projName   sql.NullString
			updated    int64
		)
		if err := rows.Scan(&sid, &title, &worktree, &projName, &updated); err != nil {
			continue
		}
		displayName := shortenPath(worktree)
		if projName.Valid && projName.String != "" {
			displayName = projName.String
		}
		results = append(results, SearchResult{
			Source:      "opencode",
			ProjectName: displayName,
			Title:       title,
			Path:        sid,
			ModTime:     time.UnixMilli(updated).Format(time.RFC3339),
		})
	}
	return results
}

// sortSearchResults sorts results by modification time (newest first).
func sortSearchResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].ModTime > results[j].ModTime
	})
}
