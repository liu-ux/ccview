package main

import (
	"fmt"
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

// The query is literal and case-sensitive on every backend, so that a narrowed
// search can never disagree with the search that produced its windows.
func TestSearchContentInFiles_IsCaseSensitiveAndLiteral(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "proj")
	os.MkdirAll(projDir, 0755)

	os.WriteFile(filepath.Join(projDir, "a.jsonl"), []byte(
		`{"type":"user","content":"TypeError: Cannot read property"}`), 0644)
	os.WriteFile(filepath.Join(projDir, "b.jsonl"), []byte(
		`{"type":"user","content":"value is 50% of total"}`), 0644)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	aPath := filepath.Clean(filepath.Join(projDir, "a.jsonl"))
	bPath := filepath.Clean(filepath.Join(projDir, "b.jsonl"))

	if matches := searchContentInFiles("typeerror", claudeDir); matches[aPath] {
		t.Error("lowercase query must not match TypeError: matching is case-sensitive")
	}
	if matches := searchContentInFiles("TypeError", claudeDir); !matches[aPath] {
		t.Error("exact-case query should match")
	}
	if matches := searchContentInFiles("50% of", claudeDir); !matches[bPath] {
		t.Error("% must be matched literally, not as a wildcard")
	}
	if matches := searchContentInFiles("50.", claudeDir); matches[bPath] {
		t.Error(". must be matched literally, not as a regex metacharacter")
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

	os.WriteFile(filepath.Join(projDir, "aaa.jsonl"), []byte(`{"content":"typeerror in auth module"}`), 0644)
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
	results := computeContentSearchResults("typeerror", searchScopeGlobal, providers, 0, "").Results
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
	results := computeContentSearchResults("needle", searchScopeProject, providers, 0, "proj-a").Results
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

// Same contract as the manual fallback, but through the real rg/grep path. If
// neither is installed this still passes, since all three backends now agree.
func TestSearchContentInFiles_LiteralThroughRg(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	projDir := filepath.Join(claudeDir, "projects", "proj")
	os.MkdirAll(projDir, 0755)

	os.WriteFile(filepath.Join(projDir, "dotted.jsonl"), []byte(`{"content":"a.c is literal"}`), 0644)
	os.WriteFile(filepath.Join(projDir, "abc.jsonl"), []byte(`{"content":"abc is not"}`), 0644)

	dotted := filepath.Clean(filepath.Join(projDir, "dotted.jsonl"))
	abc := filepath.Clean(filepath.Join(projDir, "abc.jsonl"))

	matches := searchContentInFiles("a.c", claudeDir)
	if !matches[dotted] {
		t.Error("literal query should match the file containing it")
	}
	if matches[abc] {
		t.Error(". must not act as a regex wildcard")
	}
}

// ── Prefix narrowing ──

func TestWindowHasExtension(t *testing.T) {
	tests := []struct {
		name   string
		window string
		base   string
		ext    string
		want   bool
	}{
		{"only occurrence", "a needle", "a", " n", true},
		{"extension only at the second occurrence", "ac ab", "a", "b", true},
		{"no occurrence carries the extension", "ac ad", "a", "b", false},
		{"base absent", "xyz", "a", "b", false},
		{"overlapping occurrences", "aaa", "aa", "a", true},
		{"empty extension always matches", "anything", "thing", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowHasExtension(tc.window, tc.base, tc.ext); got != tc.want {
				t.Errorf("windowHasExtension(%q, %q, %q) = %v, want %v", tc.window, tc.base, tc.ext, got, tc.want)
			}
		})
	}
}

func TestBudgetedMatches(t *testing.T) {
	old := searchCacheBudgetBytes
	defer func() { searchCacheBudgetBytes = old }()

	searchCacheBudgetBytes = 100
	if _, ok := budgetedMatches([]ContentMatch{{Path: "a", Windows: []string{strings.Repeat("x", 100)}}}); !ok {
		t.Error("a set within budget must be kept")
	}
	got, ok := budgetedMatches([]ContentMatch{{Path: "a", Windows: []string{strings.Repeat("x", 101)}}})
	if ok || got != nil {
		t.Errorf("an over-budget set must be dropped whole, got ok=%v %v", ok, got)
	}
	// One oversized entity must not leave the rest partially cached.
	got, ok = budgetedMatches([]ContentMatch{
		{Path: "a", Windows: []string{"small"}},
		{Path: "b", Windows: []string{strings.Repeat("x", 200)}},
	})
	if ok || got != nil {
		t.Errorf("partial caching is unsound, got ok=%v %v", ok, got)
	}
}

func TestResolveSearchCacheBudget(t *testing.T) {
	oldEnv, hadEnv := os.LookupEnv("CCVIEW_SEARCH_CACHE_MB")
	defer func() {
		if hadEnv {
			os.Setenv("CCVIEW_SEARCH_CACHE_MB", oldEnv)
		} else {
			os.Unsetenv("CCVIEW_SEARCH_CACHE_MB")
		}
	}()

	os.Unsetenv("CCVIEW_SEARCH_CACHE_MB")
	if got := resolveSearchCacheBudget(0); got != int64(defaultSearchCacheMB)<<20 {
		t.Errorf("default = %d, want %d MB", got, defaultSearchCacheMB)
	}
	os.Setenv("CCVIEW_SEARCH_CACHE_MB", "7")
	if got := resolveSearchCacheBudget(0); got != 7<<20 {
		t.Errorf("env = %d, want 7 MB", got)
	}
	if got := resolveSearchCacheBudget(3); got != 3<<20 {
		t.Errorf("flag = %d, want 3 MB (flag must win over env)", got)
	}
	os.Setenv("CCVIEW_SEARCH_CACHE_MB", "not-a-number")
	if got := resolveSearchCacheBudget(0); got != int64(defaultSearchCacheMB)<<20 {
		t.Errorf("bad env = %d, want the default", got)
	}
}

// searchStateWithBase is a model whose last content search ran for "needle".
func searchStateWithBase() model {
	return model{
		height: 40,
		width:  100,
		sessionSearch: sessionSearchState{
			active:        true,
			contentSearch: true,
			input:         []rune("needle"),
			baseQuery:     "needle",
			baseAll: []SearchResult{
				{Path: "a.jsonl", Title: "A", ModTime: "2026-01-01T00:00:02Z"},
				{Path: "b.jsonl", Title: "B", ModTime: "2026-01-01T00:00:01Z"},
			},
			matches: []ContentMatch{
				{Path: "a.jsonl", Windows: []string{"needle in a haystack"}},
				{Path: "b.jsonl", Windows: []string{"needle"}},
			},
			narrowable: true,
		},
	}
}

func TestApplyQueryToSearchCache(t *testing.T) {
	t.Run("no base leaves the query pending", func(t *testing.T) {
		m := model{sessionSearch: sessionSearchState{active: true, contentSearch: true, input: []rune("x")}}
		m.applyQueryToSearchCache()
		if !m.sessionSearch.pending {
			t.Error("a query with no base must be pending")
		}
	})

	t.Run("extension narrows", func(t *testing.T) {
		m := searchStateWithBase()
		m.sessionSearch.input = []rune("needle in")
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 1 || m.sessionSearch.allResults[0].Path != "a.jsonl" {
			t.Errorf("narrowed results = %+v, want only a.jsonl", m.sessionSearch.allResults)
		}
		if m.sessionSearch.pending || m.sessionSearch.incomplete {
			t.Error("a narrowed query is neither pending nor incomplete")
		}
	})

	t.Run("query equal to the base restores every result", func(t *testing.T) {
		m := searchStateWithBase()
		m.clearSearchResults()
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 2 {
			t.Errorf("results = %d, want the full base set", len(m.sessionSearch.allResults))
		}
		if m.sessionSearch.pending {
			t.Error("the base query has been searched")
		}
	})

	t.Run("backspacing into the base prefix keeps results but flags them incomplete", func(t *testing.T) {
		m := searchStateWithBase()
		m.sessionSearch.input = []rune("need")
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 2 {
			t.Errorf("results = %d, want the base set kept", len(m.sessionSearch.allResults))
		}
		if !m.sessionSearch.pending || !m.sessionSearch.incomplete {
			t.Error("a query shorter than the base is pending and incomplete")
		}
		if got := m.renderSessionSearchOverlay(); strings.Contains(got, "No matches found") {
			t.Error("showing valid-but-incomplete results must not also say there are none")
		}
	})

	t.Run("divergence clears the display", func(t *testing.T) {
		m := searchStateWithBase()
		m.sessionSearch.input = []rune("dle")
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 0 {
			t.Errorf("results = %+v, want none", m.sessionSearch.allResults)
		}
		if !m.sessionSearch.pending || m.sessionSearch.incomplete {
			t.Error("a diverged query is pending, and what was shown was not a subset")
		}
	})

	t.Run("extension without windows clears instead of showing a superset", func(t *testing.T) {
		m := searchStateWithBase()
		m.sessionSearch.narrowable = false
		m.sessionSearch.input = []rune("needle in")
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 0 {
			t.Errorf("results = %+v, want none: base results are a superset here", m.sessionSearch.allResults)
		}
		if !m.sessionSearch.pending {
			t.Error("must wait for enter")
		}
	})

	t.Run("extension beyond the window clears", func(t *testing.T) {
		m := searchStateWithBase()
		m.sessionSearch.input = []rune("needle" + strings.Repeat("x", contentMatchWindowRunes+1))
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 0 || !m.sessionSearch.pending {
			t.Errorf("an extension past the window cannot be answered from the cache: %+v", m.sessionSearch.allResults)
		}
	})

	t.Run("exactly at the window still narrows", func(t *testing.T) {
		window := "needle" + strings.Repeat("x", contentMatchWindowRunes)
		m := searchStateWithBase()
		m.sessionSearch.matches = []ContentMatch{{Path: "a.jsonl", Windows: []string{window}}}
		m.sessionSearch.baseAll = []SearchResult{{Path: "a.jsonl"}}
		m.sessionSearch.input = []rune(window)
		m.applyQueryToSearchCache()
		if len(m.sessionSearch.allResults) != 1 || m.sessionSearch.pending {
			t.Error("an extension of exactly contentMatchWindowRunes must still narrow")
		}
	})
}

func TestSearchDisplayLimit_KeepsNarrowingReachable(t *testing.T) {
	const total = 60
	m := model{sessionSearch: sessionSearchState{active: true, contentSearch: true}}
	for i := 0; i < total; i++ {
		path := fmt.Sprintf("c%02d.jsonl", i)
		// c00 sorts last (oldest), so it falls outside the displayed page.
		m.sessionSearch.baseAll = append(m.sessionSearch.baseAll,
			SearchResult{Path: path, ModTime: fmt.Sprintf("2026-01-01T00:00:%02dZ", i)})
		window := "needle"
		if i == 0 {
			window = "needle xyz"
		}
		m.sessionSearch.matches = append(m.sessionSearch.matches, ContentMatch{Path: path, Windows: []string{window}})
	}
	m.sessionSearch.input = []rune("needle")
	m.sessionSearch.baseQuery = "needle"
	m.sessionSearch.narrowable = true
	m.setSearchResults(m.sessionSearch.baseAll)

	if len(m.sessionSearch.results) != searchDisplayLimit {
		t.Fatalf("displayed %d results, want %d", len(m.sessionSearch.results), searchDisplayLimit)
	}
	for _, r := range m.sessionSearch.results {
		if r.Path == "c00.jsonl" {
			t.Fatal("c00.jsonl should be past the display cap")
		}
	}

	m.sessionSearch.input = []rune("needle xyz")
	m.applyQueryToSearchCache()
	if len(m.sessionSearch.allResults) != 1 || m.sessionSearch.allResults[0].Path != "c00.jsonl" {
		t.Errorf("narrowing must reach matches past the display cap, got %+v", m.sessionSearch.allResults)
	}
}

func TestSessionSearch_EnterGate(t *testing.T) {
	keyMsg := func(r rune) tea.Msg { return tea.KeyPressMsg{Code: r, Text: string(r)} }
	update := func(m model, msg tea.Msg) (model, tea.Cmd) {
		got, cmd := m.Update(msg)
		return got.(model), cmd
	}

	m := model{height: 40, width: 100, sessionSearch: sessionSearchState{active: true, contentSearch: true}}
	m, cmd := update(m, keyMsg('n'))
	if cmd != nil {
		t.Error("typing in content mode must not start a search")
	}
	if !m.sessionSearch.pending {
		t.Error("the query should be pending until enter")
	}
	if got := m.renderSessionSearchOverlay(); strings.Contains(got, "No matches found") {
		t.Error("the overlay must not claim there are no matches before searching")
	}

	m, cmd = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter must start the content search")
	}
	if !m.sessionSearch.searching {
		t.Error("the overlay should show that a search is running")
	}

	// Metadata search keeps searching as you type: it costs nothing.
	title := model{height: 40, width: 100, sessionSearch: sessionSearchState{active: true}}
	if _, cmd := update(title, keyMsg('n')); cmd == nil {
		t.Error("metadata search should still debounce while typing")
	}

	// Explicit actions search rather than waiting for another enter.
	base := searchStateWithBase()
	base, cmd = update(base, tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd == nil {
		t.Error("switching scope must search immediately")
	}
	if base.sessionSearch.baseQuery != "" {
		t.Error("switching scope must drop the stale base")
	}
	base = searchStateWithBase()
	base, cmd = update(base, tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if cmd == nil {
		t.Error("switching to content mode must search immediately")
	}
}

func TestSessionSearchOverlay_ExplainsIncompleteResults(t *testing.T) {
	m := searchStateWithBase()
	m.sessionSearch.input = []rune("need")
	m.applyQueryToSearchCache()
	got := m.renderSessionSearchOverlay()
	if !strings.Contains(got, "enter to search again") {
		t.Errorf("overlay should say how to get complete results:\n%s", got)
	}
	if !strings.Contains(got, "enter:search") {
		t.Errorf("hint line should say enter searches, not opens:\n%s", got)
	}
}

// The property the whole cache rests on, checked against whatever corpus this
// machine has: narrowing a short query's windows must find exactly the
// entities a full search for the longer query finds — no misses, no
// inventions. Skips when there is no corpus to check against.
func TestNarrowing_AgreesWithFullSearchOnRealCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the local corpus")
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects")); err != nil {
		t.Skip("no local corpus")
	}
	p := &ClaudeProvider{}
	pairs := [][2]string{
		{"Compaction", "Compaction canceled"},
		{"Api key", "Api key is invalid"},
	}
	checked := 0
	for _, pair := range pairs {
		base, ext := pair[0], pair[1]
		baseRes := p.ContentSearch(base, "")
		full := p.ContentSearch(ext, "")
		narrowed := narrowMatches(baseRes.Matches, base, ext)
		if len(full.Results) == 0 {
			continue
		}
		checked++
		fullSet := make(map[string]bool, len(full.Results))
		for _, r := range full.Results {
			fullSet[r.Path] = true
		}
		for path := range fullSet {
			if !narrowed[path] {
				t.Errorf("narrowing %q to %q missed %s", base, ext, path)
			}
		}
		for path := range narrowed {
			if !fullSet[path] {
				t.Errorf("narrowing %q to %q invented %s", base, ext, path)
			}
		}
	}
	t.Logf("checked %d prefix pairs", checked)
}
