package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

// ── Paste handling ──

func TestSanitizePaste(t *testing.T) {
	long := strings.Repeat("x", maxPasteLen+500)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"lf collapses to space", "a\nb", "a b"},
		{"crlf collapses to space", "a\r\nb", "a b"},
		{"tab collapses to space", "a\tb", "a b"},
		{"whitespace run collapses", "a  \n\t  b", "a b"},
		{"leading and trailing trimmed", "  hello world  ", "hello world"},
		{"other control chars dropped", "a\x01\x02b", "ab"},
		{"only whitespace", "\n\n  \t", ""},
		{"empty", "", ""},
		{"multibyte preserved", "中文\n粘贴", "中文 粘贴"},
		{"capped at maxPasteLen", long, long[:maxPasteLen]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizePaste(tc.in); got != tc.want {
				t.Errorf("sanitizePaste(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizePaste_CapsRunesNotBytes(t *testing.T) {
	// Multi-byte runes must be truncated by rune count, not byte count.
	got := sanitizePaste(strings.Repeat("中", maxPasteLen+10))
	if n := len([]rune(got)); n != maxPasteLen {
		t.Errorf("expected %d runes, got %d", maxPasteLen, n)
	}
}

func TestInsertAtCursor(t *testing.T) {
	tests := []struct {
		name    string
		buf     string
		pos     int
		text    string
		wantBuf string
		wantPos int
	}{
		{"middle", "/tmp/ab", 5, "XY", "/tmp/XYab", 7},
		{"start", "ab", 0, "XY", "XYab", 2},
		{"end", "ab", 2, "XY", "abXY", 4},
		{"empty buffer", "", 0, "XY", "XY", 2},
		{"negative pos clamps", "ab", -3, "X", "Xab", 1},
		{"past end clamps", "ab", 99, "X", "abX", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf, pos := insertAtCursor([]rune(tc.buf), tc.pos, []rune(tc.text))
			if string(buf) != tc.wantBuf || pos != tc.wantPos {
				t.Errorf("insertAtCursor(%q, %d, %q) = (%q, %d), want (%q, %d)",
					tc.buf, tc.pos, tc.text, string(buf), pos, tc.wantBuf, tc.wantPos)
			}
		})
	}
}

func TestPaste_ContentSearch(t *testing.T) {
	m := model{contentSearchActive: true}
	got, cmd := m.Update(tea.PasteMsg{Content: "foo\nbar"})
	um := got.(model)
	if string(um.contentSearchInput) != "foo bar" {
		t.Errorf("input = %q, want %q", string(um.contentSearchInput), "foo bar")
	}
	if um.contentSearchGen != 1 {
		t.Errorf("gen = %d, want 1", um.contentSearchGen)
	}
	if cmd == nil {
		t.Error("expected a debounce command")
	}
}

func TestPaste_SessionSearch(t *testing.T) {
	m := model{sessionSearch: sessionSearchState{active: true, cursor: 3, offset: 2}}
	got, cmd := m.Update(tea.PasteMsg{Content: "hello\nworld"})
	um := got.(model)
	if string(um.sessionSearch.input) != "hello world" {
		t.Errorf("input = %q, want %q", string(um.sessionSearch.input), "hello world")
	}
	if um.sessionSearch.cursor != 0 || um.sessionSearch.offset != 0 {
		t.Errorf("cursor/offset = %d/%d, want 0/0", um.sessionSearch.cursor, um.sessionSearch.offset)
	}
	if um.sessionSearch.gen != 1 {
		t.Errorf("gen = %d, want 1", um.sessionSearch.gen)
	}
	if cmd == nil {
		t.Error("expected a debounce command")
	}
}

func TestPaste_ProjectFilter(t *testing.T) {
	m := model{projectFilterActive: true, projCursor: 5, projOffset: 4}
	got, _ := m.Update(tea.PasteMsg{Content: "alpha\nbeta"})
	um := got.(model)
	if string(um.projectFilter) != "alpha beta" {
		t.Errorf("filter = %q, want %q", string(um.projectFilter), "alpha beta")
	}
	if um.projCursor != 0 || um.projOffset != 0 {
		t.Errorf("cursor/offset = %d/%d, want 0/0", um.projCursor, um.projOffset)
	}
}

func TestPaste_ExportPathInsertsAtCursor(t *testing.T) {
	m := model{export: exportState{
		active:     true,
		step:       exportStepPath,
		pathBuf:    []rune("/tmp/ab"),
		pathCurPos: 5,
	}}
	got, _ := m.Update(tea.PasteMsg{Content: "XY"})
	um := got.(model)
	if string(um.export.pathBuf) != "/tmp/XYab" {
		t.Errorf("pathBuf = %q, want %q", string(um.export.pathBuf), "/tmp/XYab")
	}
	if um.export.pathCurPos != 7 {
		t.Errorf("pathCurPos = %d, want 7", um.export.pathCurPos)
	}
}

func TestPaste_ExportFilenameInsertsAtCursor(t *testing.T) {
	m := model{export: exportState{
		active:         true,
		step:           exportStepFilename,
		filenameBuf:    []rune("out.html"),
		filenameCurPos: 3,
	}}
	got, _ := m.Update(tea.PasteMsg{Content: "XY"})
	um := got.(model)
	if string(um.export.filenameBuf) != "outXY.html" {
		t.Errorf("filenameBuf = %q, want %q", string(um.export.filenameBuf), "outXY.html")
	}
	if um.export.filenameCurPos != 5 {
		t.Errorf("filenameCurPos = %d, want 5", um.export.filenameCurPos)
	}
}

func TestPaste_IgnoredWhenNoInputFocused(t *testing.T) {
	// Viewer with no search active, export wizard on a non-text step: paste is a no-op.
	m := model{export: exportState{active: true, step: exportStepWhat, pathBuf: []rune("/tmp/")}}
	got, cmd := m.Update(tea.PasteMsg{Content: "XY"})
	um := got.(model)
	if string(um.export.pathBuf) != "/tmp/" {
		t.Errorf("pathBuf changed on non-text step: %q", string(um.export.pathBuf))
	}
	if cmd != nil {
		t.Error("expected no command")
	}
}

func TestPaste_EmptyIsNoOp(t *testing.T) {
	m := model{contentSearchActive: true}
	got, cmd := m.Update(tea.PasteMsg{Content: "\n\n  \t"})
	um := got.(model)
	if len(um.contentSearchInput) != 0 {
		t.Errorf("input = %q, want empty", string(um.contentSearchInput))
	}
	if um.contentSearchGen != 0 {
		t.Errorf("gen = %d, want 0 (paste must not trigger a search)", um.contentSearchGen)
	}
	if cmd != nil {
		t.Error("expected no command for an empty paste")
	}
}
