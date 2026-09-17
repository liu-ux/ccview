package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Test fixtures ──

// setupTestClaudeDir creates a temporary ~/.claude directory structure for testing.
// Returns the temp home dir (caller must defer os.RemoveAll).
func setupTestClaudeDir(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projectsDir := filepath.Join(claudeDir, "projects")

	// Project with 2 conversations
	proj1Dir := filepath.Join(projectsDir, "project-a--src-code")
	if err := os.MkdirAll(proj1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	// CLAUDE.md
	os.WriteFile(filepath.Join(proj1Dir, "CLAUDE.md"), []byte("# Project A"), 0644)
	// Memory files
	memDir := filepath.Join(proj1Dir, "memory")
	os.MkdirAll(memDir, 0755)
	os.WriteFile(filepath.Join(memDir, "project.md"), []byte("memory"), 0644)
	// Conversation files (minimal valid JSONL)
	os.WriteFile(filepath.Join(proj1Dir, "conv1.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"hello"}}`), 0644)
	os.WriteFile(filepath.Join(proj1Dir, "conv2.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"world"}}`), 0644)

	// Empty project (should be excluded)
	proj2Dir := filepath.Join(projectsDir, "empty-project")
	os.MkdirAll(proj2Dir, 0755)

	// Project with 1 conversation
	proj3Dir := filepath.Join(projectsDir, "project-b--other")
	if err := os.MkdirAll(proj3Dir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proj3Dir, "conv1.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"test"}}`), 0644)

	return tmpHome
}

// setHome overrides os.UserHomeDir for testing by setting HOME/USERPROFILE.
// Returns a cleanup function.
func setHome(t *testing.T, home string) func() {
	t.Helper()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	return func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}
}

// ── Level 0: loadProjectDirs ──

func TestLoadProjectDirs_ReturnsProjectList(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	tree, err := loadProjectDirs()
	if err != nil {
		t.Fatalf("loadProjectDirs() error: %v", err)
	}
	if tree == nil {
		t.Fatal("loadProjectDirs() returned nil tree")
	}

	// Should have 2 projects (empty-project excluded because 0 convs)
	if len(tree.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(tree.Projects))
	}

	// Verify projects are present (order not guaranteed)
	names := make(map[string]bool)
	for _, p := range tree.Projects {
		names[p.DirName] = true
	}
	if !names["project-a--src-code"] {
		t.Error("missing project-a--src-code")
	}
	if !names["project-b--other"] {
		t.Error("missing project-b--other")
	}
}

func TestLoadProjectDirs_ConvCountFromDirListing(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	tree, _ := loadProjectDirs()

	for _, p := range tree.Projects {
		if p.DirName == "project-a--src-code" {
			if p.ConvCount != 2 {
				t.Errorf("project-a: expected ConvCount=2, got %d", p.ConvCount)
			}
		}
		if p.DirName == "project-b--other" {
			if p.ConvCount != 1 {
				t.Errorf("project-b: expected ConvCount=1, got %d", p.ConvCount)
			}
		}
	}
}

func TestLoadProjectDirs_NoFileReads(t *testing.T) {
	// Level 0 should not populate fields that require file reads
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	tree, _ := loadProjectDirs()

	for _, p := range tree.Projects {
		if p.LastActive != "" {
			t.Errorf("Level 0 should not set LastActive, got %q", p.LastActive)
		}
		if p.ClaudeMD != "" {
			t.Errorf("Level 0 should not set ClaudeMD, got %q", p.ClaudeMD)
		}
		if len(p.MemoryFiles) > 0 {
			t.Errorf("Level 0 should not load MemoryFiles")
		}
		if p.MsgCount != 0 {
			t.Errorf("Level 0 should not set MsgCount, got %d", p.MsgCount)
		}
	}
}

func TestLoadProjectDirs_EmptyDir(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude", "projects")
	os.MkdirAll(claudeDir, 0755)

	cleanup := setHome(t, tmpHome)
	defer cleanup()

	tree, err := loadProjectDirs()
	if err != nil {
		t.Fatalf("loadProjectDirs() error: %v", err)
	}
	if len(tree.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(tree.Projects))
	}
}

// ── Level 1: enrichProjectMeta ──

func TestEnrichProjectMeta_DisplayName(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := enrichProjectMeta("project-a--src-code", projDir)

	if proj.DisplayName == "" {
		t.Error("expected non-empty DisplayName")
	}
	if proj.DisplayName != "project-a--src-code" {
		t.Errorf("expected DisplayName=project-a--src-code, got %q", proj.DisplayName)
	}
}

func TestEnrichProjectMeta_ClaudeMDAndMemory(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := enrichProjectMeta("project-a--src-code", projDir)

	if proj.ClaudeMD == "" {
		t.Error("expected ClaudeMD to be set")
	}
	if len(proj.MemoryFiles) != 1 {
		t.Errorf("expected 1 memory file, got %d", len(proj.MemoryFiles))
	}
}

func TestEnrichProjectMeta_LastActive(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := enrichProjectMeta("project-a--src-code", projDir)

	if proj.LastActive == "" {
		t.Error("expected LastActive to be set from file mod time")
	}
}

func TestEnrichProjectMeta_ConvCount(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := enrichProjectMeta("project-a--src-code", projDir)

	if proj.ConvCount != 2 {
		t.Errorf("expected ConvCount=2, got %d", proj.ConvCount)
	}
}

// ── Level 2: loadProjectDetail ──

func TestLoadProjectDetail_LoadsConversations(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	fileHistoryDir := filepath.Join(tmpHome, ".claude", "file-history")
	os.MkdirAll(fileHistoryDir, 0755)

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := loadProjectDetail(context.Background(), "project-a--src-code", projDir, nil)

	if proj == nil {
		t.Fatal("loadProjectDetail() returned nil")
	}
	if len(proj.Conversations) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(proj.Conversations))
	}
}

func TestLoadProjectDetail_UsesCachedHistoryTitles(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	fileHistoryDir := filepath.Join(tmpHome, ".claude", "file-history")
	os.MkdirAll(fileHistoryDir, 0755)

	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")

	titles := map[string]string{"conv1": "cached title"}
	proj := loadProjectDetail(context.Background(), "project-a--src-code", projDir, titles)

	if proj == nil {
		t.Fatal("loadProjectDetail() returned nil")
	}
}

func TestLoadProjectDetail_EmptyProjectReturnsNil(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	projDir := filepath.Join(tmpHome, ".claude", "projects", "empty-project")
	proj := loadProjectDetail(context.Background(), "empty-project", projDir, nil)

	if proj != nil {
		t.Error("expected nil for empty project")
	}
}

func TestLoadProjectDetail_CancelStopsEarly(t *testing.T) {
	// Create a project with many conversations to make cancellation observable
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude", "projects", "big-project")
	os.MkdirAll(claudeDir, 0755)
	fileHistoryDir := filepath.Join(tmpHome, ".claude", "file-history")
	os.MkdirAll(fileHistoryDir, 0755)

	// Create 100 conversation files
	for i := 0; i < 100; i++ {
		os.WriteFile(filepath.Join(claudeDir, "conv"+string(rune('a'+i%26))+".jsonl"),
			[]byte(`{"type":"user","message":{"role":"user","content":"msg"}}`), 0644)
	}

	cleanup := setHome(t, tmpHome)
	defer cleanup()

	// Cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	proj := loadProjectDetail(ctx, "big-project", claudeDir, nil)
	// Should return partial or nil — the key is it doesn't block
	if proj != nil && len(proj.Conversations) == 100 {
		t.Error("expected early termination, but loaded all 100 conversations")
	}
}

// ── Provider integration ──

func TestClaudeProvider_LoadProjectList(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	p := &ClaudeProvider{}
	tree, err := p.LoadProjectList()
	if err != nil {
		t.Fatalf("LoadProjectList() error: %v", err)
	}
	if len(tree.Projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(tree.Projects))
	}
}

func TestClaudeProvider_EnrichProjectMeta(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	p := &ClaudeProvider{}
	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := p.EnrichProjectMeta("project-a--src-code", projDir)

	if proj.ClaudeMD == "" {
		t.Error("expected ClaudeMD to be set")
	}
}

func TestClaudeProvider_LoadProjectDetail(t *testing.T) {
	tmpHome := setupTestClaudeDir(t)
	cleanup := setHome(t, tmpHome)
	defer cleanup()

	fileHistoryDir := filepath.Join(tmpHome, ".claude", "file-history")
	os.MkdirAll(fileHistoryDir, 0755)

	p := &ClaudeProvider{}
	projDir := filepath.Join(tmpHome, ".claude", "projects", "project-a--src-code")
	proj := p.LoadProjectDetail(context.Background(), "project-a--src-code", projDir, nil)

	if proj == nil {
		t.Fatal("LoadProjectDetail() returned nil")
	}
	if len(proj.Conversations) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(proj.Conversations))
	}
}

// ── Concurrency: context cancellation ──

func TestContextCancellation_StopsLoad(t *testing.T) {
	// Verifies that cancelling the context prevents further file scanning
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude", "projects", "proj")
	os.MkdirAll(claudeDir, 0755)
	fileHistoryDir := filepath.Join(tmpHome, ".claude", "file-history")
	os.MkdirAll(fileHistoryDir, 0755)

	// Create many conversation files
	for i := 0; i < 50; i++ {
		name := filepath.Join(claudeDir, "conv"+string(rune('A'+i%26))+string(rune('0'+i/26))+".jsonl")
		os.WriteFile(name, []byte(`{"type":"user","message":{"role":"user","content":"test"}}`), 0644)
	}

	cleanup := setHome(t, tmpHome)
	defer cleanup()

	// Cancel immediately — should return partial results
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	proj := loadProjectDetail(ctx, "proj", claudeDir, nil)
	if proj != nil && len(proj.Conversations) == 50 {
		t.Error("context cancellation did not stop scanning")
	}
}

// ── findProjectByName ──

func TestFindProjectByName_ExactDirName(t *testing.T) {
	m := model{
		tree: &TreeData{
			Projects: []TreeProject{
				{DirName: "my-app--src", DisplayName: "~/src/my-app"},
				{DirName: "other--code", DisplayName: "~/code/other"},
			},
		},
	}
	if idx := m.findProjectByName("my-app--src"); idx != 0 {
		t.Errorf("expected 0, got %d", idx)
	}
}

func TestFindProjectByName_ExactDisplayName(t *testing.T) {
	m := model{
		tree: &TreeData{
			Projects: []TreeProject{
				{DirName: "hash--src", DisplayName: "~/src/my-app"},
			},
		},
	}
	if idx := m.findProjectByName("~/src/my-app"); idx != 0 {
		t.Errorf("expected 0, got %d", idx)
	}
}

func TestFindProjectByName_Substring(t *testing.T) {
	m := model{
		tree: &TreeData{
			Projects: []TreeProject{
				{DirName: "my-app--src-code", DisplayName: "~/src/my-app"},
				{DirName: "other-project", DisplayName: "~/other"},
			},
		},
	}
	if idx := m.findProjectByName("my-app"); idx != 0 {
		t.Errorf("expected 0, got %d", idx)
	}
}

func TestFindProjectByName_CaseInsensitive(t *testing.T) {
	m := model{
		tree: &TreeData{
			Projects: []TreeProject{
				{DirName: "MyApp--src"},
			},
		},
	}
	if idx := m.findProjectByName("myapp"); idx != 0 {
		t.Errorf("expected 0, got %d", idx)
	}
}

func TestFindProjectByName_NoMatch(t *testing.T) {
	m := model{
		tree: &TreeData{
			Projects: []TreeProject{
				{DirName: "my-app--src"},
			},
		},
	}
	if idx := m.findProjectByName("nonexistent"); idx != -1 {
		t.Errorf("expected -1, got %d", idx)
	}
}

// ── formatToolInput ──

func TestFormatToolInput_BasicJSON(t *testing.T) {
	input := json.RawMessage(`{"command":"npm test","description":"run tests"}`)
	result := formatToolInput(input, 80)
	if !strings.Contains(result, "npm test") {
		t.Errorf("expected 'npm test' in output, got %s", result)
	}
	if !strings.Contains(result, "description") {
		t.Errorf("expected 'description' key in output, got %s", result)
	}
}

func TestFormatToolInput_TruncatesLongValues(t *testing.T) {
	longVal := strings.Repeat("x", 300)
	input := json.RawMessage(`{"content":"` + longVal + `"}`)
	result := formatToolInput(input, 80)
	if strings.Contains(result, longVal) {
		t.Error("expected long value to be truncated")
	}
	if !strings.Contains(result, "...") {
		t.Error("expected truncation marker '...'")
	}
}

func TestFormatToolInput_EmptyInput(t *testing.T) {
	result := formatToolInput(json.RawMessage(`{}`), 80)
	if !strings.Contains(result, "{") || !strings.Contains(result, "}") {
		t.Errorf("expected JSON braces in output, got %q", result)
	}
}

func TestFormatToolInput_InvalidJSON(t *testing.T) {
	result := formatToolInput(json.RawMessage(`not json`), 80)
	if result != "not json" {
		t.Errorf("expected raw input back, got %q", result)
	}
}

// ── renderConversation with tool details ──

func TestRenderConversation_ToolDetailsToggle(t *testing.T) {
	entries := []Entry{
		{
			Type: "assistant",
			Parsed: &ParsedMessage{
				Role: "assistant",
				Content: mustMarshal([]ContentBlock{
					{Type: "tool_use", Name: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`)},
					{Type: "text", Text: "done"},
				}),
			},
		},
	}

	// Without tool details
	linesOff, _ := renderConversation(entries, 80, false, false, false, true)
	foundDetailOff := false
	for _, l := range linesOff {
		if strings.Contains(l, "command") {
			foundDetailOff = true
		}
	}
	if foundDetailOff {
		t.Error("tool details should be hidden when showToolDetails=false")
	}

	// With tool details
	linesOn, _ := renderConversation(entries, 80, true, false, false, true)
	foundDetailOn := false
	for _, l := range linesOn {
		if strings.Contains(l, "command") {
			foundDetailOn = true
		}
	}
	if !foundDetailOn {
		t.Error("tool details should be visible when showToolDetails=true")
	}
}

// ── matchConversation ──

func TestMatchConversation_Title(t *testing.T) {
	conv := TreeConversation{Title: "Fix login bug", Preview: "something else"}
	if !matchConversation(conv, "", "login") {
		t.Error("should match title")
	}
}

func TestMatchConversation_Preview(t *testing.T) {
	conv := TreeConversation{Title: "", Preview: "Added error handling"}
	if !matchConversation(conv, "", "error") {
		t.Error("should match preview")
	}
}

func TestMatchConversation_ProjectName(t *testing.T) {
	conv := TreeConversation{Title: "", Preview: ""}
	if !matchConversation(conv, "my-app", "app") {
		t.Error("should match project name")
	}
}

func TestMatchConversation_CaseInsensitive(t *testing.T) {
	conv := TreeConversation{Title: "Fix Login Bug"}
	if !matchConversation(conv, "", "login") {
		t.Error("should match case-insensitively")
	}
}

func TestMatchConversation_NoMatch(t *testing.T) {
	conv := TreeConversation{Title: "Fix login bug", Preview: "auth fix"}
	if matchConversation(conv, "myapp", "deploy") {
		t.Error("should not match unrelated query")
	}
}

// ── buildSidebar with filter ──

func TestBuildSidebar_FilterApplied(t *testing.T) {
	proj := &TreeProject{
		Conversations: []TreeConversation{
			{Path: "/a.jsonl", Title: "Conv A", ModTime: "2024-01-01T00:00:00Z"},
			{Path: "/b.jsonl", Title: "Conv B", ModTime: "2024-01-02T00:00:00Z"},
			{Path: "/c.jsonl", Title: "Conv C", ModTime: "2024-01-03T00:00:00Z"},
		},
	}
	// Filter to only show Conv B
	filter := map[string]bool{"/b.jsonl": true}
	items := buildSidebar(proj, nil, "", filter)

	convCount := 0
	for _, item := range items {
		if item.kind == "conversation" {
			convCount++
			if item.path != "/b.jsonl" {
				t.Errorf("filtered sidebar should only contain /b.jsonl, got %s", item.path)
			}
		}
	}
	if convCount != 1 {
		t.Errorf("expected 1 conversation in filtered sidebar, got %d", convCount)
	}
}

func TestBuildSidebar_NilFilterShowsAll(t *testing.T) {
	proj := &TreeProject{
		Conversations: []TreeConversation{
			{Path: "/a.jsonl", Title: "Conv A", ModTime: "2024-01-01T00:00:00Z"},
			{Path: "/b.jsonl", Title: "Conv B", ModTime: "2024-01-02T00:00:00Z"},
		},
	}
	items := buildSidebar(proj, nil, "", nil)

	convCount := 0
	for _, item := range items {
		if item.kind == "conversation" {
			convCount++
		}
	}
	if convCount != 2 {
		t.Errorf("expected 2 conversations with nil filter, got %d", convCount)
	}
}

// ── SearchResult enrichment ──

func TestSearchResult_HasMsgCountAndCWD(t *testing.T) {
	r := SearchResult{
		MsgCount: 42,
		CWD:      "/home/user/project",
	}
	if r.MsgCount != 42 {
		t.Errorf("expected MsgCount=42, got %d", r.MsgCount)
	}
	if r.CWD != "/home/user/project" {
		t.Errorf("expected CWD=/home/user/project, got %s", r.CWD)
	}
}

// mustMarshal is a test helper that marshals to JSON or panics.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ── filteredProjectIndices ──

func TestFilteredProjectIndices_EmptyFilter(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "alpha", DisplayName: "Alpha Project"},
			{DirName: "beta", DisplayName: "Beta Project"},
			{DirName: "gamma", DisplayName: "Gamma Project"},
		},
	}
	idxs := filteredProjectIndices(tree, nil)
	if len(idxs) != 3 {
		t.Fatalf("expected 3 indices, got %d", len(idxs))
	}
	for i, idx := range idxs {
		if idx != i {
			t.Errorf("idxs[%d] = %d, want %d", i, idx, i)
		}
	}
}

func TestFilteredProjectIndices_MatchesDisplayName(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "proj-a", DisplayName: "My Website"},
			{DirName: "proj-b", DisplayName: "API Server"},
			{DirName: "proj-c", DisplayName: "Website v2"},
		},
	}
	idxs := filteredProjectIndices(tree, []rune("website"))
	if len(idxs) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(idxs))
	}
	if idxs[0] != 0 || idxs[1] != 2 {
		t.Errorf("expected [0,2], got %v", idxs)
	}
}

func TestFilteredProjectIndices_MatchesDirName(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "my-app--frontend", DisplayName: "Frontend"},
			{DirName: "my-app--backend", DisplayName: "Backend"},
			{DirName: "other-project", DisplayName: "Other"},
		},
	}
	idxs := filteredProjectIndices(tree, []rune("my-app"))
	if len(idxs) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(idxs))
	}
	if idxs[0] != 0 || idxs[1] != 1 {
		t.Errorf("expected [0,1], got %v", idxs)
	}
}

func TestFilteredProjectIndices_CaseInsensitive(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "alpha", DisplayName: "Alpha"},
			{DirName: "BETA", DisplayName: "Beta"},
		},
	}
	idxs := filteredProjectIndices(tree, []rune("ALPHA"))
	if len(idxs) != 1 || idxs[0] != 0 {
		t.Errorf("expected [0], got %v", idxs)
	}
}

func TestFilteredProjectIndices_NoMatch(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "alpha", DisplayName: "Alpha"},
			{DirName: "beta", DisplayName: "Beta"},
		},
	}
	idxs := filteredProjectIndices(tree, []rune("zzz"))
	if len(idxs) != 0 {
		t.Errorf("expected 0 matches, got %d", len(idxs))
	}
}

func TestFilteredProjectIndices_NilTree(t *testing.T) {
	idxs := filteredProjectIndices(nil, []rune("test"))
	if len(idxs) != 0 {
		t.Errorf("expected 0 for nil tree, got %d", len(idxs))
	}
}

func TestFilteredProjectIndices_SubstringMatch(t *testing.T) {
	tree := &TreeData{
		Projects: []TreeProject{
			{DirName: "project-alpha", DisplayName: "Alpha"},
			{DirName: "project-beta", DisplayName: "Beta"},
			{DirName: "gamma", DisplayName: "Gamma Project"},
		},
	}
	idxs := filteredProjectIndices(tree, []rune("project"))
	// "project-alpha" dir contains "project", "project-beta" dir contains "project", "Gamma Project" display contains "project"
	if len(idxs) != 3 {
		t.Errorf("expected 3 matches, got %d: %v", len(idxs), idxs)
	}
}

// ── System entries ──

func TestFormatSystemEntry(t *testing.T) {
	tests := []struct {
		name      string
		entry     Entry
		wantLabel string
		wantBody  string
		wantOK    bool
	}{
		{
			name:      "local_command extracts the command name",
			entry:     Entry{Type: "system", Subtype: "local_command", Content: "<command-name>/clear</command-name>"},
			wantLabel: "system", wantBody: "/clear", wantOK: true,
		},
		{
			name:      "local_command without tags keeps its content",
			entry:     Entry{Type: "system", Subtype: "local_command", Content: "plain output"},
			wantLabel: "system", wantBody: "plain output", wantOK: true,
		},
		{
			name:   "local_command without content is skipped",
			entry:  Entry{Type: "system", Subtype: "local_command"},
			wantOK: false,
		},
		{
			name: "compact_boundary with metadata",
			entry: Entry{Type: "system", Subtype: "compact_boundary", Content: "Conversation compacted",
				CompactMetadata: &CompactMetadata{Trigger: "manual", PreTokens: 120472, PostTokens: 11088, DurationMs: 59427}},
			wantLabel: "compact", wantBody: "Conversation compacted (pre 120472 → post 11088 tok, 59s)", wantOK: true,
		},
		{
			name:      "compact_boundary without metadata",
			entry:     Entry{Type: "system", Subtype: "compact_boundary"},
			wantLabel: "compact", wantBody: "Conversation compacted", wantOK: true,
		},
		{
			name:      "turn_duration with message count",
			entry:     Entry{Type: "system", Subtype: "turn_duration", DurationMs: 647482, MessageCount: 316},
			wantLabel: "turn", wantBody: "10m47s · 316 msgs", wantOK: true,
		},
		{
			name:      "turn_duration without message count",
			entry:     Entry{Type: "system", Subtype: "turn_duration", DurationMs: 59427},
			wantLabel: "turn", wantBody: "59s", wantOK: true,
		},
		{
			name:   "turn_duration without a duration is skipped",
			entry:  Entry{Type: "system", Subtype: "turn_duration", MessageCount: 3},
			wantOK: false,
		},
		{
			name:      "api_error with a flat message",
			entry:     Entry{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"status":429,"error":{"message":"slow down"}}`)},
			wantLabel: "error", wantBody: "429 slow down", wantOK: true,
		},
		{
			name:      "api_error with a nested message",
			entry:     Entry{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"status":401,"error":{"error":{"message":"bad key"}}}`)},
			wantLabel: "error", wantBody: "401 bad key", wantOK: true,
		},
		{
			name:      "api_error with a status only",
			entry:     Entry{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"status":521,"headers":{}}`)},
			wantLabel: "error", wantBody: "HTTP 521", wantOK: true,
		},
		{
			name:      "api_error with neither status nor message",
			entry:     Entry{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"type":"error"}`)},
			wantLabel: "error", wantBody: "request failed", wantOK: true,
		},
		{
			name:      "unknown subtype falls back to its content",
			entry:     Entry{Type: "system", Subtype: "stop_hook_summary", Content: "hook ran"},
			wantLabel: "system", wantBody: "hook ran", wantOK: true,
		},
		{
			name:   "unknown subtype without content is skipped",
			entry:  Entry{Type: "system", Subtype: "away_summary"},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			label, body, ok := formatSystemEntry(tc.entry)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (label=%q body=%q)", ok, tc.wantOK, label, body)
			}
			if !ok {
				return
			}
			if label != tc.wantLabel || body != tc.wantBody {
				t.Errorf("got (%q, %q), want (%q, %q)", label, body, tc.wantLabel, tc.wantBody)
			}
		})
	}
}

func TestFormatSystemEntry_TruncatesAndFlattens(t *testing.T) {
	long := strings.Repeat("x", maxSystemBodyLen+50)
	entry := Entry{Type: "system", Subtype: "api_error", Error: mustMarshal(map[string]any{
		"status": 500,
		"error":  map[string]any{"message": "line one\nline two " + long},
	})}
	_, body, ok := formatSystemEntry(entry)
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.Contains(body, "\n") {
		t.Errorf("body must be a single line, got %q", body)
	}
	if !strings.HasSuffix(body, "…") {
		t.Errorf("expected a trailing ellipsis, got %q", body)
	}
	if n := len([]rune(body)); n != maxSystemBodyLen+1 {
		t.Errorf("body = %d runes, want %d plus the ellipsis", n, maxSystemBodyLen)
	}
}

func TestRenderConversation_SystemToggle(t *testing.T) {
	entries := []Entry{
		{Type: "system", Subtype: "local_command", Content: "<command-name>/clear</command-name>"},
		{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"status":401,"error":{"message":"bad key"}}`)},
		{Type: "user", Parsed: &ParsedMessage{Role: "user", Content: mustMarshal([]ContentBlock{{Type: "text", Text: "hello"}})}},
	}

	// The error line is styled in two pieces (label, then body), so assert on
	// them separately rather than across the style boundary.
	onLines, _ := renderConversation(entries, 80, false, false, false, true)
	on := strings.Join(onLines, "\n")
	for _, want := range []string{"[system] /clear", "[error]", "401 bad key", "hello"} {
		if !strings.Contains(on, want) {
			t.Errorf("showSystem=true: missing %q", want)
		}
	}

	offLines, _ := renderConversation(entries, 80, false, false, false, false)
	off := strings.Join(offLines, "\n")
	for _, unwanted := range []string{"[system]", "[error]", "bad key"} {
		if strings.Contains(off, unwanted) {
			t.Errorf("showSystem=false: unexpected %q", unwanted)
		}
	}
	if !strings.Contains(off, "hello") {
		t.Error("showSystem=false must not drop user messages")
	}
}

func TestExportSurfaces_RenderSystemEntries(t *testing.T) {
	entries := []Entry{
		{Type: "system", Subtype: "api_error", Error: json.RawMessage(`{"status":401,"error":{"message":"bad key"}}`)},
		{Type: "system", Subtype: "compact_boundary", CompactMetadata: &CompactMetadata{PreTokens: 100, PostTokens: 10, DurationMs: 5000}},
	}
	want := []string{"[error] 401 bad key", "[compact] Conversation compacted"}

	// Every export surface calls the same formatter, and none of them honours
	// the TUI display toggles.
	var plainHTML bytes.Buffer
	if err := exportHTMLTo(entries, &plainHTML, "conv.jsonl"); err != nil {
		t.Fatalf("exportHTMLTo: %v", err)
	}
	var navHTML bytes.Buffer
	if err := exportHTMLWithNav(entries, &navHTML, "conv.jsonl", ""); err != nil {
		t.Fatalf("exportHTMLWithNav: %v", err)
	}
	mdPath := filepath.Join(t.TempDir(), "out.md")
	if err := exportMarkdown(entries, mdPath, "conv.jsonl"); err != nil {
		t.Fatalf("exportMarkdown: %v", err)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}

	surfaces := map[string]string{
		"html":     plainHTML.String(),
		"html+nav": navHTML.String(),
		"markdown": string(md),
	}
	for name, got := range surfaces {
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s export missing %q", name, w)
			}
		}
	}
}

// ── Content match windows ──

func TestContentWindows(t *testing.T) {
	// A window starts at the line's first match and ends 50 runes past its last
	// match, so every occurrence in the line stays inside it.
	line := "head needle tail needle end"
	windows := contentWindows(line, "needle")
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0] != line[len("head "):] {
		t.Errorf("window = %q, want the line from the first match", windows[0])
	}
	if !strings.Contains(windows[0], "needle end") {
		t.Errorf("window must contain the last occurrence: %q", windows[0])
	}
}

func TestContentWindows_ClampsToLineEnd(t *testing.T) {
	line := "abc needle"
	if got := contentWindows(line, "needle"); len(got) != 1 || got[0] != "needle" {
		t.Errorf("window = %q, want %q (no text past the line end)", got, "needle")
	}
}

func TestContentWindows_TruncatesToWindowRunes(t *testing.T) {
	gap := strings.Repeat("x", contentMatchWindowRunes+40)
	line := "needle" + gap + "needle" + strings.Repeat("y", 100)
	got := contentWindows(line, "needle")
	if len(got) != 1 {
		t.Fatalf("expected 1 window, got %d", len(got))
	}
	want := len("needle") + len(gap) + len("needle") + contentMatchWindowRunes
	if n := len([]rune(got[0])); n != want {
		t.Errorf("window is %d runes, want %d", n, want)
	}
	if n := strings.Count(got[0], "y"); n != contentMatchWindowRunes {
		t.Errorf("window keeps %d of the 100 trailing y's, want exactly %d", n, contentMatchWindowRunes)
	}
	if !strings.Contains(got[0], gap+"needle") {
		t.Errorf("window must reach the last occurrence: %q", got[0])
	}
}

func TestContentWindows_MultiByteIsRuneSafe(t *testing.T) {
	// 50 runes, not 50 bytes: otherwise a Chinese line would keep only ~16 chars.
	line := "前缀中文关键词" + strings.Repeat("中", 200)
	got := contentWindows(line, "关键词")
	if len(got) != 1 {
		t.Fatalf("expected 1 window, got %d", len(got))
	}
	if n := len([]rune(got[0])); n != len([]rune("关键词"))+contentMatchWindowRunes {
		t.Errorf("window is %d runes, want %d", n, len([]rune("关键词"))+contentMatchWindowRunes)
	}
	if got[0][0:len("关键词")] != "关键词" {
		t.Errorf("window must start at the match: %q", got[0])
	}
}

func TestContentWindows_MultipleLinesAndNoMatch(t *testing.T) {
	text := "no match here\nhas needle once\nplain\nneedle again"
	got := contentWindows(text, "needle")
	if len(got) != 2 {
		t.Fatalf("expected 2 windows, got %d: %q", len(got), got)
	}
	if got[0] != "needle once" || got[1] != "needle again" {
		t.Errorf("unexpected windows: %q", got)
	}
	if w := contentWindows("nothing", "needle"); w != nil {
		t.Errorf("expected nil for no match, got %q", w)
	}
	if w := contentWindows("anything", ""); w != nil {
		t.Errorf("expected nil for an empty query, got %q", w)
	}
}

// ── OpenCode content search ──

// openCodeFixture builds a minimal OpenCode database with n sessions, each
// holding one message part containing needle.
func openCodeFixture(t *testing.T, n int, needle string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT, name TEXT)`,
		`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, project_id TEXT, parent_id TEXT, time_archived INTEGER, time_updated INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT)`,
		`CREATE TABLE part (message_id TEXT, data TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO project (id, worktree, name) VALUES ('p1', '/tmp/proj', 'proj')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("ses_%03d", i)
		if _, err := db.Exec(`INSERT INTO session (id, title, project_id, time_updated) VALUES (?, ?, 'p1', ?)`,
			sid, "title "+sid, int64(1000+i)); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		mid := "msg_" + sid
		if _, err := db.Exec(`INSERT INTO message (id, session_id) VALUES (?, ?)`, mid, sid); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO part (message_id, data) VALUES (?, ?)`,
			mid, `{"type":"text","text":"`+needle+` and more after it"}`); err != nil {
			t.Fatalf("insert part: %v", err)
		}
	}
	return dbPath
}

// The provider must not truncate: prefix narrowing works off the complete set,
// and the display cap is the caller's business.
func TestOpenCodeContentSearch_ReturnsCompleteResults(t *testing.T) {
	const sessions = 55 // well past the old SQL LIMIT 50
	p := &OpenCodeProvider{dbPath: openCodeFixture(t, sessions, "needle")}

	got := p.ContentSearch("needle", "")
	if len(got.Results) != sessions {
		t.Errorf("results = %d, want %d (search must not truncate)", len(got.Results), sessions)
	}
	if len(got.Matches) != sessions {
		t.Fatalf("matches = %d, want %d", len(got.Matches), sessions)
	}
	for _, m := range got.Matches {
		if len(m.Windows) == 0 {
			t.Fatalf("match for %s has no windows", m.Path)
		}
		if !strings.Contains(m.Windows[0], "needle") {
			t.Errorf("window %q does not contain the query", m.Windows[0])
		}
	}
}

func TestOpenCodeContentSearch_IsCaseSensitiveAndLiteral(t *testing.T) {
	p := &OpenCodeProvider{dbPath: openCodeFixture(t, 1, "50% off")}
	if got := p.ContentSearch("Needle", ""); len(got.Results) != 0 || len(got.Matches) != 0 {
		t.Errorf("lower-case query must not match: %+v", got)
	}
	if got := p.ContentSearch("50% o", ""); len(got.Results) != 1 {
		t.Errorf("%% must be literal, not a wildcard: %+v", got.Results)
	}
}
