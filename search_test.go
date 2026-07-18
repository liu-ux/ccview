package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchContentInFiles_ManualFallback(t *testing.T) {
	// Setup: create a fake ~/.claude/projects/ with JSONL files containing known content
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "test-project")
	os.MkdirAll(projDir, 0755)

	// File that contains the query
	os.WriteFile(filepath.Join(projDir, "conv1.jsonl"), []byte(
		`{"type":"user","message":{"role":"user","content":"hello world"}}
{"type":"assistant","message":{"role":"assistant","content":"foo bar baz"}}
`), 0644)

	// File that does NOT contain the query
	os.WriteFile(filepath.Join(projDir, "conv2.jsonl"), []byte(
		`{"type":"user","message":{"role":"user","content":"nothing here"}}
`), 0644)

	// Force manual fallback by clearing PATH so rg/grep can't be found
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	matches := searchContentInFiles("foo bar", claudeDir)
	if matches == nil {
		t.Fatal("searchContentInFiles returned nil (no results)")
	}

	conv1Path := filepath.Clean(filepath.Join(projDir, "conv1.jsonl"))
	conv2Path := filepath.Clean(filepath.Join(projDir, "conv2.jsonl"))

	if !matches[conv1Path] {
		t.Error("conv1.jsonl should match 'foo bar'")
	}
	if matches[conv2Path] {
		t.Error("conv2.jsonl should NOT match 'foo bar'")
	}
}

func TestSearchContentInFiles_CaseInsensitive(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "proj")
	os.MkdirAll(projDir, 0755)

	os.WriteFile(filepath.Join(projDir, "a.jsonl"), []byte(
		`{"type":"user","content":"TypeError: Cannot read property"}`), 0644)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	matches := searchContentInFiles("typeerror", claudeDir)
	if matches == nil {
		t.Fatal("searchContentInFiles returned nil")
	}
	aPath := filepath.Clean(filepath.Join(projDir, "a.jsonl"))
	if !matches[aPath] {
		t.Error("should match case-insensitively")
	}
}

func TestSearchContentInFiles_EmptyQuery(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "proj")
	os.MkdirAll(projDir, 0755)
	os.WriteFile(filepath.Join(projDir, "a.jsonl"), []byte(`{"content":"test"}`), 0644)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	// Empty query should match everything (strings.Contains("", "") == true)
	matches := searchContentInFiles("", claudeDir)
	if matches == nil {
		t.Fatal("empty query should return matches")
	}
}

func TestSearchContentInFiles_NoMatch(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "proj")
	os.MkdirAll(projDir, 0755)
	os.WriteFile(filepath.Join(projDir, "a.jsonl"), []byte(`{"content":"hello"}`), 0644)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	matches := searchContentInFiles("zzz_nonexistent_zzz", claudeDir)
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestComputeContentSearchResults_MatchesTreeConversations(t *testing.T) {
	// Setup: create JSONL files in the expected directory structure
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "my-project")
	os.MkdirAll(projDir, 0755)

	os.WriteFile(filepath.Join(projDir, "aaa.jsonl"), []byte(`{"content":"TypeError in auth module"}`), 0644)
	os.WriteFile(filepath.Join(projDir, "bbb.jsonl"), []byte(`{"content":"nothing relevant"}`), 0644)

	// Set HOME so ContentSearch finds our test data
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	// Force manual fallback
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	providers := []Provider{&ClaudeProvider{}}
	results := computeContentSearchResults("typeerror", searchScopeGlobal, providers, 0, "")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result, got 0")
	}
	found := false
	for _, r := range results {
		if strings.HasSuffix(r.Path, "aaa.jsonl") {
			found = true
			if r.ProjectName != "my-project" {
				t.Errorf("expected project name 'my-project', got %q", r.ProjectName)
			}
		}
		if strings.HasSuffix(r.Path, "bbb.jsonl") {
			t.Error("bbb.jsonl should NOT match 'typeerror'")
		}
	}
	if !found {
		t.Error("aaa.jsonl should be in results")
	}
}

func TestComputeContentSearchResults_ProjectScope(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	proj1Dir := filepath.Join(claudeDir, "projects", "proj-a")
	proj2Dir := filepath.Join(claudeDir, "projects", "proj-b")
	os.MkdirAll(proj1Dir, 0755)
	os.MkdirAll(proj2Dir, 0755)

	os.WriteFile(filepath.Join(proj1Dir, "a.jsonl"), []byte(`{"content":"needle in haystack"}`), 0644)
	os.WriteFile(filepath.Join(proj2Dir, "b.jsonl"), []byte(`{"content":"needle in haystack"}`), 0644)

	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	// Project scope: only search proj-a
	providers := []Provider{&ClaudeProvider{}}
	results := computeContentSearchResults("needle", searchScopeProject, providers, 0, "proj-a")
	if len(results) != 1 {
		t.Fatalf("expected 1 result in project scope, got %d", len(results))
	}
	if !strings.HasSuffix(results[0].Path, "a.jsonl") {
		t.Errorf("should only find file in proj-a, got %s", results[0].Path)
	}
}
