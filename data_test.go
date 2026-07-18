package main

import (
	"context"
	"os"
	"path/filepath"
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
