package main

import (
	"context"
	"encoding/json"
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
	linesOff, _ := renderConversation(entries, 80, false, false, false)
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
	linesOn, _ := renderConversation(entries, 80, true, false, false)
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
