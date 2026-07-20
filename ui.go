package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ── States ──

type viewState int

const (
	viewLoading viewState = iota
	viewProjectList
	viewProjectDetail
)

type pane int

const (
	paneSidebar pane = iota
	paneContent
)

// sidebarItem is a single navigable row in the project detail sidebar.
type sidebarItem struct {
	kind  string // "back", "file", "conversation", "subagent", "separator", "header"
	label string
	path  string
	badge string
}

type searchScope int

const (
	searchScopeProject searchScope = iota
	searchScopeGlobal
)

type sessionSearchState struct {
	active        bool
	scope         searchScope
	input         []rune
	results       []SearchResult
	cursor        int
	offset        int
	gen           int  // debounce generation — only the latest tick fires search
	contentSearch bool // true = search conversation content, false = search metadata only
	searching     bool // true while rg/grep is running
}

// ── Model ──

type model struct {
	state         viewState
	width, height int

	// Providers
	providers     []Provider
	providerTrees []*TreeData // one tree per provider
	providerTab   int         // active tab index in project list

	// Project list screen (current tab's tree)
	tree              *TreeData
	projCursor        int
	projOffset        int
	projectFilter     []rune // inline filter text for project list
	projectFilterActive bool // true when filter input is focused

	// Project detail screen
	activePane           pane
	projIndex            int          // index into tree.Projects
	currentProj          *TreeProject // pointer to selected project
	currentProvider      Provider     // provider for current project
	sidebar              []sidebarItem
	sidebarCursor        int
	sidebarOffset        int
	expandedConvPath     string // which conversation's subagents are visible
	historyTitles        map[string]string // cached history.jsonl titles
	projectDetailLoading bool              // true while Level 2 is loading
	loadGeneration       int               // incremented on each openProject, used to discard stale results
	cancelCurrentLoad    context.CancelFunc // cancels the in-flight Level 2 load goroutine

	// Content pane
	contentLines  []string
	contentOffset int
	contentTitle  string
	contentPath   string
	contentKind   string

	directFile    string
	directProject string // -project flag: auto-open this project after Level 0
	err           error
	statusMsg  string

	// Export overlay
	export exportState

	// Mouse selection
	mouseSelecting    bool
	mouseSelStart     [2]int // [row, col] in screen coordinates
	mouseSelEnd       [2]int
	mouseHasSelection bool

	// Content search
	contentSearchActive bool
	contentSearchInput  []rune
	contentSearchPos    int
	contentMatches      []int  // line indices with matches
	contentMatchIdx     int    // current match index
	contentSearchQuery  string // for highlighting
	contentSearchGen    int    // debounce generation

	// Tool detail toggle
	showToolDetails bool
	showToolResults bool // toggle tool_result display
	showThinking    bool // toggle thinking block display

	// Sidebar filter (from search results)
	sidebarFilter map[string]bool // conversation paths to show; nil = show all

	// Session search overlay
	sessionSearch       sessionSearchState
}

// ── Export overlay types ──

type exportStep int

const (
	exportStepWhat     exportStep = iota
	exportStepFormat
	exportStepPath
	exportStepFilename
	exportStepConfirm
)

type exportWhat int

const (
	exportFullConversation exportWhat = iota
	exportMainThread
	exportSelectedSubagent
)

type exportFormat int

const (
	exportFormatHTML exportFormat = iota
	exportFormatMarkdown
	exportFormatJSONL
)

type exportState struct {
	active           bool
	step             exportStep
	what             exportWhat
	whatCursor       int
	format           exportFormat
	formatCursor     int
	pathBuf          []rune
	pathCurPos       int
	filenameBuf      []rune
	filenameCurPos   int
	sourcePath       string
	sourceLabel      string
	convHasSubagents bool
}

// ── Messages ──

type contentLoadedMsg struct {
	lines []string
	title string
	path  string
	kind  string
	err   error
}
type statusClearMsg struct{}

type exportDoneMsg struct {
	outPath string
	err     error
}

type editorFinishedMsg struct {
	err error
}

type searchDebounceMsg struct {
	gen  int
	kind string // "content" or "session"
}

// ── Lazy-loading messages ──

type projectListReadyMsg struct {
	index int      // provider index
	tree  *TreeData // Level 0: project dirs + conv counts
	err   error
}

type projectMetaReadyMsg struct {
	index    int
	projects []TreeProject // Level 1: enriched with display names + last active
}

type projectDetailReadyMsg struct {
	providerIdx int
	projIdx     int
	proj        *TreeProject // Level 2: full conversation list
	generation  int          // discarded if != current loadGeneration
}

type historyTitlesLoadedMsg struct {
	titles map[string]string
}

type contentSearchDoneMsg struct {
	gen     int
	results []SearchResult
}

// ── Styles ──

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#D97706")).
			Padding(0, 1)

	projectNameStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#cfc8c4"))

	projectMetaStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#78716C"))

	projectBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#D97706"))

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#D97706"))

	paneTitleActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#D97706"))

	paneTitleInactive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#78716C"))

	loadedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2D8B4E"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#78716C"))

	faintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A8A29E"))

	sepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D6D3CD"))

	backStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6366F1"))

	userHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5B5FC7")).
			Padding(0, 1)

	assistantHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#2D8B4E")).
				Padding(0, 1)

	systemStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("#A8A29E"))

	thinkingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A8A29E")).
			Italic(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D97706"))

	toolResultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A8A29E"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#78716C"))

	statusHighlight = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D97706"))
)

// ── Init ──

func newModel(directFile, directProject string, providers []Provider) model {
	return model{
		state:         viewLoading,
		directFile:    directFile,
		directProject: directProject,
		providers:     providers,
	}
}

func (m model) Init() tea.Cmd {
	debugLog("Init: providers=%d", len(m.providers))
	if m.directFile != "" {
		return loadConvCmd(m.directFile, m.directFile, 120, nil, false, false, false)
	}
	return m.loadAllProjectListsCmd()
}

// treeLoadedMsg is now per-provider.
type providerTreeLoadedMsg struct {
	index int
	tree  *TreeData
	err   error
}

// loadAllProjectListsCmd starts Level 0 loading for all providers.
func (m model) loadAllProjectListsCmd() tea.Cmd {
	cmds := make([]tea.Cmd, len(m.providers))
	for i, prov := range m.providers {
		i, prov := i, prov
		cmds[i] = func() tea.Msg {
			tree, err := prov.LoadProjectList()
			return projectListReadyMsg{i, tree, err}
		}
	}
	return tea.Batch(cmds...)
}

// loadProjectMetaCmd starts Level 1 enrichment for all projects in a tree.
func loadProjectMetaCmd(providerIdx int, provider Provider, tree *TreeData) tea.Cmd {
	return func() tea.Msg {
		enriched := make([]TreeProject, len(tree.Projects))
		for i, proj := range tree.Projects {
			enriched[i] = provider.EnrichProjectMeta(proj.DirName, proj.DirPath)
			enriched[i].Source = proj.Source
		}
		return projectMetaReadyMsg{providerIdx, enriched}
	}
}

// loadProjectDetailCmd starts Level 2 loading for a single project.
func loadProjectDetailCmd(ctx context.Context, providerIdx, projIdx int, provider Provider, dirName, dirPath string, historyTitles map[string]string, generation int) tea.Cmd {
	return func() tea.Msg {
		proj := provider.LoadProjectDetail(ctx, dirName, dirPath, historyTitles)
		return projectDetailReadyMsg{providerIdx, projIdx, proj, generation}
	}
}

// loadHistoryTitlesCmd loads history.jsonl titles in the background.
func loadHistoryTitlesCmd() tea.Cmd {
	return func() tea.Msg {
		home, _ := os.UserHomeDir()
		titles := loadHistoryTitles(filepath.Join(home, ".claude", "history.jsonl"))
		return historyTitlesLoadedMsg{titles}
	}
}

func loadConvCmd(path, title string, width int, provider Provider, showToolDetails, showToolResults, showThinking bool) tea.Cmd {
	return func() tea.Msg {
		var entries []Entry
		var err error
		if provider != nil {
			entries, err = provider.LoadConversation(path)
		} else {
			entries, err = parseConversation(path)
		}
		if err != nil {
			return contentLoadedMsg{nil, title, path, "conversation", err}
		}
		lines := renderConversation(entries, width, showToolDetails, showToolResults, showThinking)
		return contentLoadedMsg{lines, title, path, "conversation", nil}
	}
}

func loadFileCmd(path, title string, width int) tea.Cmd {
	return func() tea.Msg {
		content, err := readFileContent(path)
		if err != nil {
			return contentLoadedMsg{nil, title, path, "file", err}
		}
		if strings.HasSuffix(path, ".md") {
			rendered := renderMarkdownTerm(content, width)
			return contentLoadedMsg{strings.Split(rendered, "\n"), title, path, "file", nil}
		}
		return contentLoadedMsg{strings.Split(content, "\n"), title, path, "file", nil}
	}
}

// ── Sidebar builder ──

func buildSidebar(proj *TreeProject, plans []TreeFileRef, expandedConvPath string, filter map[string]bool) []sidebarItem {
	items := []sidebarItem{
		{kind: "back", label: "< Back to Projects"},
		{kind: "separator"},
	}

	if proj.ClaudeMD != "" {
		items = append(items, sidebarItem{kind: "file", label: "CLAUDE.md", path: proj.ClaudeMD})
	}
	for _, mem := range proj.MemoryFiles {
		items = append(items, sidebarItem{kind: "file", label: mem.Name, path: mem.Path})
	}
	if proj.ClaudeMD != "" || len(proj.MemoryFiles) > 0 {
		items = append(items, sidebarItem{kind: "separator"})
	}

	// Plans (global)
	if len(plans) > 0 {
		items = append(items, sidebarItem{kind: "header", label: "PLANS"})
		for _, plan := range plans {
			items = append(items, sidebarItem{kind: "file", label: plan.Name, path: plan.Path})
		}
		items = append(items, sidebarItem{kind: "separator"})
	}

	for _, conv := range proj.Conversations {
		// Apply sidebar filter if active
		if filter != nil && !filter[conv.Path] {
			continue
		}
		title := conv.Title
		if title == "" {
			title = conv.Slug
		}
		if title == "" && len(conv.SessionID) >= 8 {
			title = conv.SessionID[:8]
		}
		badge := fmt.Sprintf("%s  %d msgs", formatDateSmart(conv.ModTime), conv.MsgCount)
		if len(conv.SubAgents) > 0 {
			badge = fmt.Sprintf("%s  %d msgs · %d agents", formatDateSmart(conv.ModTime), conv.MsgCount, len(conv.SubAgents))
		}
		items = append(items, sidebarItem{
			kind:  "conversation",
			label: title,
			path:  conv.Path,
			badge: badge,
		})
		// Only show subagents for the expanded conversation
		if conv.Path == expandedConvPath {
			for _, sa := range conv.SubAgents {
				desc := sa.Description
				if desc == "" {
					desc = sa.Name
				}
				at := sa.AgentType
				if at == "" {
					at = "agent"
				}
				lbl := at + ": " + desc
				if len(lbl) > 55 {
					lbl = lbl[:52] + "..."
				}
				items = append(items, sidebarItem{kind: "subagent", label: lbl, path: sa.Path})
			}
		}
	}
	return items
}

// navigable returns true if the sidebar item can be selected with cursor.
func (si sidebarItem) navigable() bool {
	return si.kind != "separator" && si.kind != "header"
}

// nextNavigable returns the next navigable index from pos in direction dir (+1/-1).
func nextNavigable(items []sidebarItem, pos, dir int) int {
	for i := pos + dir; i >= 0 && i < len(items); i += dir {
		if items[i].navigable() {
			return i
		}
	}
	return pos
}

// ── Update ──

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		oldW := m.width
		m.width = msg.Width
		m.height = msg.Height
		if m.state == viewProjectDetail && m.contentPath != "" && m.contentKind == "conversation" && oldW != msg.Width {
			_, rw := m.paneWidths()
			return m, loadConvCmd(m.contentPath, m.contentTitle, rw, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
		}
		return m, nil

	case providerTreeLoadedMsg:
		debugLog("providerTreeLoadedMsg: index=%d err=%v", msg.index, msg.err)
		// Initialize providerTrees slice if needed
		if m.providerTrees == nil {
			m.providerTrees = make([]*TreeData, len(m.providers))
		}
		if msg.index >= 0 && msg.index < len(m.providerTrees) {
			if msg.err == nil && msg.tree != nil {
				m.providerTrees[msg.index] = msg.tree
			} else {
				// Mark errored providers with empty tree so we know they've responded
				m.providerTrees[msg.index] = &TreeData{}
			}
		}
		// Count how many have responded
		loaded := 0
		for _, t := range m.providerTrees {
			if t != nil {
				loaded++
			}
		}
		if loaded < len(m.providers) {
			return m, nil // still waiting
		}
		if m.directFile != "" {
			return m, nil
		}
		// All loaded — set active tab to first provider with data
		for i, t := range m.providerTrees {
			if t != nil && len(t.Projects) > 0 {
				m.providerTab = i
				m.tree = t
				break
			}
		}
		if m.tree == nil {
			// Use first non-nil tree even if empty
			for i, t := range m.providerTrees {
				if t != nil {
					m.providerTab = i
					m.tree = t
					break
				}
			}
		}
		m.state = viewProjectList

		// Start Level 1 enrichment for Claude providers (which have lightweight Level 0 data)
		// Also start loading history titles for later use in Level 2
		var cmds []tea.Cmd
		cmds = append(cmds, loadHistoryTitlesCmd())
		for i, prov := range m.providers {
			tree := m.providerTrees[i]
			if tree != nil && len(tree.Projects) > 0 && tree.Projects[0].LastActive == "" {
				cmds = append(cmds, loadProjectMetaCmd(i, prov, tree))
			}
		}
		return m, tea.Batch(cmds...)

	case projectListReadyMsg:
		// Level 0 loaded — same logic as providerTreeLoadedMsg but from LoadProjectList()
		debugLog("projectListReadyMsg: index=%d err=%v projects=%d", msg.index, msg.err, len(msg.tree.Projects))
		if m.providerTrees == nil {
			m.providerTrees = make([]*TreeData, len(m.providers))
		}
		if msg.index >= 0 && msg.index < len(m.providerTrees) {
			if msg.err == nil && msg.tree != nil {
				m.providerTrees[msg.index] = msg.tree
			} else {
				m.providerTrees[msg.index] = &TreeData{}
			}
		}
		loaded := 0
		for _, t := range m.providerTrees {
			if t != nil {
				loaded++
			}
		}
		if loaded < len(m.providers) {
			return m, nil
		}
		if m.directFile != "" {
			return m, nil
		}
		for i, t := range m.providerTrees {
			if t != nil && len(t.Projects) > 0 {
				m.providerTab = i
				m.tree = t
				break
			}
		}
		if m.tree == nil {
			for i, t := range m.providerTrees {
				if t != nil {
					m.providerTab = i
					m.tree = t
					break
				}
			}
		}
		m.state = viewProjectList
		debugLog("projectListReadyMsg: state=viewProjectList, starting Level 1 enrichment")

		// Start Level 1 enrichment + history titles
		var cmds []tea.Cmd
		cmds = append(cmds, loadHistoryTitlesCmd())
		for i, prov := range m.providers {
			tree := m.providerTrees[i]
			if tree != nil && len(tree.Projects) > 0 && tree.Projects[0].LastActive == "" {
				cmds = append(cmds, loadProjectMetaCmd(i, prov, tree))
			}
		}

		// If -project was specified, find and auto-open the matching project
		if m.directProject != "" {
			if idx := m.findProjectByName(m.directProject); idx >= 0 {
				m.projCursor = idx
				cmd := m.openProject(idx)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.directProject = "" // consumed
			} else {
				m.statusMsg = fmt.Sprintf("No project matching %q", m.directProject)
				m.directProject = ""
			}
		}

		return m, tea.Batch(cmds...)

	case projectMetaReadyMsg:
		// Level 1 enrichment complete — update project metadata in-place
		debugLog("projectMetaReadyMsg: index=%d projects=%d", msg.index, len(msg.projects))
		if msg.index >= 0 && msg.index < len(m.providerTrees) {
			tree := m.providerTrees[msg.index]
			if tree != nil && len(msg.projects) == len(tree.Projects) {
				for i := range tree.Projects {
					tree.Projects[i].DisplayName = msg.projects[i].DisplayName
					tree.Projects[i].LastActive = msg.projects[i].LastActive
					tree.Projects[i].ClaudeMD = msg.projects[i].ClaudeMD
					tree.Projects[i].MemoryFiles = msg.projects[i].MemoryFiles
				}
				// Re-sort by last active
				sort.Slice(tree.Projects, func(a, b int) bool {
					return tree.Projects[a].LastActive > tree.Projects[b].LastActive
				})
			}
		}
		return m, nil

	case projectDetailReadyMsg:
		// Level 2 loading complete — replace project data and build sidebar
		debugLog("projectDetailReadyMsg: providerIdx=%d projIdx=%d proj=%v gen=%d current=%d", msg.providerIdx, msg.projIdx, msg.proj != nil, msg.generation, m.loadGeneration)
		// Discard stale results from a previous project navigation
		if msg.generation != m.loadGeneration {
			debugLog("projectDetailReadyMsg: discarded stale result (gen %d != %d)", msg.generation, m.loadGeneration)
			return m, nil
		}
		if msg.proj != nil && msg.providerIdx >= 0 && msg.providerIdx < len(m.providerTrees) {
			tree := m.providerTrees[msg.providerIdx]
			if tree != nil && msg.projIdx >= 0 && msg.projIdx < len(tree.Projects) {
				tree.Projects[msg.projIdx] = *msg.proj
				// If this is the currently viewed project, rebuild sidebar
				if m.currentProj != nil && m.currentProj.DirName == msg.proj.DirName {
					m.currentProj = &tree.Projects[msg.projIdx]
					m.sidebar = buildSidebar(m.currentProj, tree.Plans, m.expandedConvPath, m.sidebarFilter)
					m.sidebarCursor = 0
					if len(m.sidebar) > 0 && !m.sidebar[0].navigable() {
						m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
					}
				}
			}
		}
		m.projectDetailLoading = false
		return m, nil

	case historyTitlesLoadedMsg:
		debugLog("historyTitlesLoadedMsg: %d titles", len(msg.titles))
		m.historyTitles = msg.titles
		return m, nil

	case contentSearchDoneMsg:
		// Async content search completed
		if msg.gen == m.sessionSearch.gen {
			m.sessionSearch.results = msg.results
		}
		m.sessionSearch.searching = false
		return m, nil

	case contentLoadedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", msg.err)
			return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{} })
		}
		m.contentLines = msg.lines
		m.contentTitle = msg.title
		m.contentPath = msg.path
		m.contentKind = msg.kind
		m.contentOffset = 0
		if m.directFile != "" {
			m.state = viewProjectDetail
		}
		return m, nil

	case searchDebounceMsg:
		if msg.kind == "content" && msg.gen == m.contentSearchGen {
			m.contentSearchQuery = string(m.contentSearchInput)
			m.computeContentMatches()
			if len(m.contentMatches) > 0 {
				m.contentMatchIdx = 0
				m.scrollToMatch()
			}
		}
		if msg.kind == "session" && msg.gen == m.sessionSearch.gen {
			// Content search uses rg/grep which may be slow — run async
			if m.sessionSearch.contentSearch {
				m.sessionSearch.searching = true
				gen := msg.gen
				query := string(m.sessionSearch.input)
				scope := m.sessionSearch.scope
				// Pass providers for the goroutine to search with
				providers := m.providers
				var currentDirName string
				if m.currentProj != nil {
					currentDirName = m.currentProj.DirName
				}
				tabIdx := m.providerTab
				return m, func() tea.Msg {
					results := computeContentSearchResults(query, scope, providers, tabIdx, currentDirName)
					return contentSearchDoneMsg{gen: gen, results: results}
				}
			}
			m.computeSessionSearchResults()
		}
		return m, nil

	case statusClearMsg:
		m.statusMsg = ""
		return m, nil

	case exportDoneMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Export error: %v", msg.err)
		} else {
			m.statusMsg = fmt.Sprintf("Exported to %s", msg.outPath)
		}
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{} })

	case editorFinishedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Editor error: %v", msg.err)
			return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{} })
		}
		return m, nil

	case searchNavigateMsg:
		// Switch to the correct provider tab
		if msg.providerIdx >= 0 && msg.providerIdx < len(m.providerTrees) {
			m.switchProviderTab(msg.providerIdx)
		}
		// Open the project
		cmd := m.openProject(msg.projIdx)
		// If project detail is loaded, also load the conversation
		if !m.projectDetailLoading {
			_, rw := m.paneWidths()
			return m, tea.Batch(cmd, loadConvCmd(msg.convPath, msg.convTitle, rw, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking))
		}
		return m, cmd

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)
	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// Clear selection on any keypress
		if m.mouseHasSelection {
			m.mouseHasSelection = false
			m.mouseSelecting = false
		}
		// Overlays intercept all keys when active
		if m.sessionSearch.active {
			return m.updateSessionSearch(msg)
		}
		if m.export.active {
			return m.updateExportOverlay(msg)
		}
		switch m.state {
		case viewProjectList:
			return m.updateProjectList(msg)
		case viewProjectDetail:
			if m.directFile != "" {
				return m.updateContent(msg)
			}
			if m.activePane == paneContent {
				return m.updateContent(msg)
			}
			return m.updateSidebar(msg)
		}
	}
	return m, nil
}

func (m *model) switchProviderTab(idx int) {
	if idx < 0 || idx >= len(m.providerTrees) || m.providerTrees[idx] == nil {
		return
	}
	m.providerTab = idx
	m.tree = m.providerTrees[idx]
	m.projCursor = 0
	m.projOffset = 0
	m.projectFilter = nil
	m.projectFilterActive = false
}

func (m model) hasMultipleTabs() bool {
	count := 0
	for _, t := range m.providerTrees {
		if t != nil && len(t.Projects) > 0 {
			count++
		}
	}
	return count > 1
}

// filteredProjectIndices returns indices of projects matching the current filter.
// If the filter is empty, all indices are returned.
func filteredProjectIndices(tree *TreeData, filter []rune) []int {
	if tree == nil {
		return nil
	}
	if len(filter) == 0 {
		idxs := make([]int, len(tree.Projects))
		for i := range tree.Projects {
			idxs[i] = i
		}
		return idxs
	}
	q := strings.ToLower(string(filter))
	var idxs []int
	for i, p := range tree.Projects {
		if strings.Contains(strings.ToLower(p.DisplayName), q) ||
			strings.Contains(strings.ToLower(p.DirName), q) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

func (m model) updateProjectList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// ── Filter input mode ──
	if m.projectFilterActive {
		switch msg.String() {
		case "esc":
			m.projectFilter = nil
			m.projectFilterActive = false
			m.projCursor = 0
			m.projOffset = 0
			return m, nil
		case "enter":
			m.projectFilterActive = false
			// Snap cursor to nearest match
			idxs := filteredProjectIndices(m.tree, m.projectFilter)
			if len(idxs) > 0 {
				found := false
				for _, idx := range idxs {
					if idx >= m.projCursor {
						m.projCursor = idx
						found = true
						break
					}
				}
				if !found {
					m.projCursor = idxs[len(idxs)-1]
				}
			}
			return m, nil
		case "backspace":
			if len(m.projectFilter) > 0 {
				m.projectFilter = m.projectFilter[:len(m.projectFilter)-1]
				m.projCursor = 0
				m.projOffset = 0
			}
			return m, nil
		default:
			r := []rune(msg.String())
			if len(r) == 1 && r[0] >= 32 {
				m.projectFilter = append(m.projectFilter, r[0])
				m.projCursor = 0
				m.projOffset = 0
			}
			return m, nil
		}
	}

	// ── Normal mode ──
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "f":
		m.projectFilterActive = true
		return m, nil
	case "1":
		if m.hasMultipleTabs() {
			m.switchProviderTab(0)
		}
	case "2":
		if len(m.providerTrees) > 1 && m.hasMultipleTabs() {
			m.switchProviderTab(1)
		}
	case "tab":
		if m.hasMultipleTabs() {
			next := (m.providerTab + 1) % len(m.providerTrees)
			// Skip nil trees
			for m.providerTrees[next] == nil || len(m.providerTrees[next].Projects) == 0 {
				next = (next + 1) % len(m.providerTrees)
				if next == m.providerTab {
					break
				}
			}
			m.switchProviderTab(next)
		}
	}

	if m.tree == nil || len(m.tree.Projects) == 0 {
		return m, nil
	}

	idxs := filteredProjectIndices(m.tree, m.projectFilter)
	if len(idxs) == 0 {
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		// Move to previous filtered project
		for i := len(idxs) - 1; i >= 0; i-- {
			if idxs[i] < m.projCursor {
				m.projCursor = idxs[i]
				break
			}
		}
	case "down", "j":
		// Move to next filtered project
		for _, idx := range idxs {
			if idx > m.projCursor {
				m.projCursor = idx
				break
			}
		}
	case "home", "g":
		m.projCursor = idxs[0]
	case "end", "G":
		m.projCursor = idxs[len(idxs)-1]
	case "enter", "l", "right":
		cmd := m.openProject(m.projCursor)
		return m, cmd
	case "/":
		m.openSessionSearch(searchScopeGlobal)
		return m, nil
	case "esc":
		// Esc clears filter if one is set (no-op if no filter)
		if len(m.projectFilter) > 0 {
			m.projectFilter = nil
			m.projCursor = 0
			m.projOffset = 0
		}
		return m, nil
	}

	// Keep cursor visible — account for tab bar height and filter bar
	// projOffset tracks position in the filtered list; projCursor is an index into the full list
	cursorPos := 0
	for i, idx := range idxs {
		if idx == m.projCursor {
			cursorPos = i
			break
		}
	}
	tabBarH := 0
	if m.hasMultipleTabs() {
		tabBarH = 2
	}
	filterBarH := 0
	if len(m.projectFilter) > 0 || m.projectFilterActive {
		filterBarH = 1
	}
	viewH := m.height - 4 - tabBarH - filterBarH
	itemH := 3 // lines per project item
	maxVisible := viewH / itemH
	if maxVisible < 1 {
		maxVisible = 1
	}
	if cursorPos < m.projOffset {
		m.projOffset = cursorPos
	}
	if cursorPos >= m.projOffset+maxVisible {
		m.projOffset = cursorPos - maxVisible + 1
	}

	return m, nil
}

func (m *model) openProject(idx int) tea.Cmd {
	debugLog("openProject: idx=%d total=%d", idx, len(m.tree.Projects))
	if idx < 0 || idx >= len(m.tree.Projects) {
		return nil
	}
	m.projIndex = idx
	m.currentProj = &m.tree.Projects[idx]
	// Set the provider for this project based on its source
	m.currentProvider = m.providers[m.providerTab]
	m.expandedConvPath = ""
	m.sidebarOffset = 0
	m.activePane = paneSidebar
	m.contentLines = nil
	m.contentTitle = ""
	m.contentPath = ""
	m.state = viewProjectDetail

	// If conversations are already loaded (Level 2 done), build sidebar immediately
	if len(m.currentProj.Conversations) > 0 {
		m.sidebar = buildSidebar(m.currentProj, m.tree.Plans, m.expandedConvPath, m.sidebarFilter)
		m.sidebarCursor = 0
		if len(m.sidebar) > 0 && !m.sidebar[0].navigable() {
			m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
		}
		m.projectDetailLoading = false
		return nil
	}

	// Level 2 not loaded yet — show loading state and trigger async load
	// Cancel any in-flight load from a previous project
	if m.cancelCurrentLoad != nil {
		m.cancelCurrentLoad()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelCurrentLoad = cancel

	m.projectDetailLoading = true
	m.loadGeneration++
	gen := m.loadGeneration
	m.sidebar = []sidebarItem{
		{kind: "back", label: "< Back to Projects"},
		{kind: "separator"},
		{kind: "header", label: "Loading conversations..."},
	}
	m.sidebarCursor = 0
	return loadProjectDetailCmd(
		ctx, m.providerTab, idx, m.currentProvider,
		m.currentProj.DirName, m.currentProj.DirPath,
		m.historyTitles, gen,
	)
}

// findProjectByName finds a project index by exact dir name, display name, or case-insensitive substring.
func (m *model) findProjectByName(name string) int {
	if m.tree == nil {
		return -1
	}
	lower := strings.ToLower(name)
	// 1. Exact DirName match
	for i, p := range m.tree.Projects {
		if p.DirName == name {
			return i
		}
	}
	// 2. Exact DisplayName match
	for i, p := range m.tree.Projects {
		if p.DisplayName == name {
			return i
		}
	}
	// 3. Case-insensitive substring on DirName
	for i, p := range m.tree.Projects {
		if strings.Contains(strings.ToLower(p.DirName), lower) {
			return i
		}
	}
	return -1
}

func (m model) updateSidebar(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	_, rightW := m.paneWidths()

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc", "backspace", "h", "left":
		// Clear sidebar filter if active, otherwise go back
		if m.sidebarFilter != nil {
			m.sidebarFilter = nil
			m.sidebar = buildSidebar(m.currentProj, m.tree.Plans, m.expandedConvPath, nil)
			m.sidebarCursor = 0
			if len(m.sidebar) > 0 && !m.sidebar[0].navigable() {
				m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
			}
			return m, nil
		}
		m.state = viewProjectList
		m.currentProj = nil
		m.sidebar = nil
		m.contentLines = nil
		m.contentTitle = ""
		m.contentPath = ""
		m.expandedConvPath = ""
		return m, nil
	case "up", "k":
		m.sidebarCursor = nextNavigable(m.sidebar, m.sidebarCursor, -1)
	case "down", "j":
		m.sidebarCursor = nextNavigable(m.sidebar, m.sidebarCursor, 1)
	case "home", "g":
		m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
	case "end", "G":
		m.sidebarCursor = nextNavigable(m.sidebar, len(m.sidebar), -1)
	case "tab", "l", "right":
		if len(m.contentLines) > 0 {
			m.activePane = paneContent
		}
	case "enter":
		if m.sidebarCursor < len(m.sidebar) {
			item := m.sidebar[m.sidebarCursor]
			switch item.kind {
			case "back":
				m.state = viewProjectList
				m.currentProj = nil
				m.sidebar = nil
				m.contentLines = nil
				m.expandedConvPath = ""
				return m, nil
			case "conversation":
				m.expandedConvPath = item.path
				m.sidebar = buildSidebar(m.currentProj, m.tree.Plans, m.expandedConvPath, m.sidebarFilter)
				// Find cursor for the expanded conversation
				for i, si := range m.sidebar {
					if si.path == item.path && si.kind == "conversation" {
						m.sidebarCursor = i
						break
					}
				}
				m.contentTitle = item.label
				m.contentLines = nil
				return m, loadConvCmd(item.path, item.label, rightW, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
			case "subagent":
				m.contentTitle = item.label
				m.contentLines = nil
				return m, loadConvCmd(item.path, item.label, rightW, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
			case "file":
				m.contentTitle = item.label
				m.contentLines = nil
				return m, loadFileCmd(item.path, item.label, rightW)
			}
		}
	case "e":
		if m.sidebarCursor < len(m.sidebar) {
			item := m.sidebar[m.sidebarCursor]
			if item.kind == "conversation" || item.kind == "subagent" {
				m.initExportOverlay(item.path, item.label)
				return m, nil
			}
		}
	case "o":
		if m.sidebarCursor < len(m.sidebar) {
			item := m.sidebar[m.sidebarCursor]
			if item.path != "" {
				return m, openInEditor(item.path)
			}
		}
	case "/":
		m.openSessionSearch(searchScopeProject)
		return m, nil
	}

	// Keep cursor visible
	sidebarH := m.height - 5
	if sidebarH < 1 {
		sidebarH = 1
	}
	if m.sidebarCursor < m.sidebarOffset {
		m.sidebarOffset = m.sidebarCursor
	}
	if m.sidebarCursor >= m.sidebarOffset+sidebarH {
		m.sidebarOffset = m.sidebarCursor - sidebarH + 1
	}

	return m, nil
}

func (m model) updateContent(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Content search mode intercepts keys
	if m.contentSearchActive {
		return m.updateContentSearch(msg)
	}

	_, paneH := m.contentPaneDims()
	contentH := paneH - 2
	maxOff := len(m.contentLines) - contentH
	if maxOff < 0 {
		maxOff = 0
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.contentOffset > 0 {
			m.contentOffset--
		}
	case "down", "j":
		if m.contentOffset < maxOff {
			m.contentOffset++
		}
	case "pgup", "b":
		m.contentOffset -= contentH
		if m.contentOffset < 0 {
			m.contentOffset = 0
		}
	case "pgdown", "f", "space":
		m.contentOffset += contentH
		if m.contentOffset > maxOff {
			m.contentOffset = maxOff
		}
	case "home", "g":
		m.contentOffset = 0
	case "end", "G":
		m.contentOffset = maxOff
	case "esc":
		if m.contentSearchQuery != "" {
			// Clear search highlights
			m.contentSearchQuery = ""
			m.contentMatches = nil
			return m, nil
		}
		if m.directFile != "" {
			return m, tea.Quit
		}
		m.activePane = paneSidebar
	case "h", "left", "tab":
		if m.directFile != "" {
			return m, tea.Quit
		}
		m.activePane = paneSidebar
	case "/":
		if len(m.contentLines) > 0 {
			m.contentSearchActive = true
			m.contentSearchInput = nil
			m.contentSearchPos = 0
			return m, nil
		}
	case "n":
		// Next match
		if len(m.contentMatches) > 0 {
			m.contentMatchIdx = (m.contentMatchIdx + 1) % len(m.contentMatches)
			m.scrollToMatch()
		}
	case "N":
		// Previous match
		if len(m.contentMatches) > 0 {
			m.contentMatchIdx--
			if m.contentMatchIdx < 0 {
				m.contentMatchIdx = len(m.contentMatches) - 1
			}
			m.scrollToMatch()
		}
	case "e":
		if m.contentPath != "" && m.contentKind == "conversation" {
			m.initExportOverlay(m.contentPath, m.contentTitle)
			return m, nil
		}
	case "t":
		// Toggle tool call detail
		if m.contentKind == "conversation" {
			m.showToolDetails = !m.showToolDetails
			_, rw := m.paneWidths()
			return m, loadConvCmd(m.contentPath, m.contentTitle, rw, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
		}
	case "T":
		// Toggle thinking block display
		if m.contentKind == "conversation" {
			m.showThinking = !m.showThinking
			_, rw := m.paneWidths()
			return m, loadConvCmd(m.contentPath, m.contentTitle, rw, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
		}
	case "R":
		// Toggle tool result display
		if m.contentKind == "conversation" {
			m.showToolResults = !m.showToolResults
			_, rw := m.paneWidths()
			return m, loadConvCmd(m.contentPath, m.contentTitle, rw, m.currentProvider, m.showToolDetails, m.showToolResults, m.showThinking)
		}
	case "o":
		if m.contentPath != "" {
			return m, openInEditor(m.contentPath)
		}
	}
	return m, nil
}

func (m model) contentSearchDebounceCmd() tea.Cmd {
	gen := m.contentSearchGen
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return searchDebounceMsg{gen: gen, kind: "content"}
	})
}

func (m model) updateContentSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.contentSearchActive = false
		return m, nil
	case "enter":
		m.contentSearchActive = false
		// Immediate compute on enter (bypass debounce)
		m.contentSearchQuery = string(m.contentSearchInput)
		m.computeContentMatches()
		if len(m.contentMatches) > 0 {
			m.contentMatchIdx = 0
			m.scrollToMatch()
		}
		return m, nil
	case "backspace":
		if len(m.contentSearchInput) > 0 {
			m.contentSearchInput = m.contentSearchInput[:len(m.contentSearchInput)-1]
			m.contentSearchGen++
			return m, m.contentSearchDebounceCmd()
		}
		return m, nil
	default:
		r := []rune(key)
		if len(r) == 1 && r[0] >= 32 {
			m.contentSearchInput = append(m.contentSearchInput, r[0])
			m.contentSearchGen++
			return m, m.contentSearchDebounceCmd()
		}
		return m, nil
	}
}

func (m *model) computeContentMatches() {
	m.contentMatches = nil
	if m.contentSearchQuery == "" {
		return
	}
	q := strings.ToLower(m.contentSearchQuery)
	for i, line := range m.contentLines {
		plain := strings.ToLower(ansi.Strip(line))
		if strings.Contains(plain, q) {
			m.contentMatches = append(m.contentMatches, i)
		}
	}
}

func (m *model) scrollToMatch() {
	if m.contentMatchIdx < 0 || m.contentMatchIdx >= len(m.contentMatches) {
		return
	}
	targetLine := m.contentMatches[m.contentMatchIdx]
	_, paneH := m.contentPaneDims()
	contentH := paneH - 2
	// Center the match in the viewport
	m.contentOffset = targetLine - contentH/2
	maxOff := len(m.contentLines) - contentH
	if maxOff < 0 {
		maxOff = 0
	}
	if m.contentOffset < 0 {
		m.contentOffset = 0
	}
	if m.contentOffset > maxOff {
		m.contentOffset = maxOff
	}
}

// ── Session search ──

func (m *model) openSessionSearch(scope searchScope) {
	m.sessionSearch = sessionSearchState{
		active: true,
		scope:  scope,
	}
}

func (m model) sessionSearchDebounceCmd() tea.Cmd {
	gen := m.sessionSearch.gen
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return searchDebounceMsg{gen: gen, kind: "session"}
	})
}

func (m model) updateSessionSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.sessionSearch.active = false
		return m, nil
	case "enter":
		if len(m.sessionSearch.results) > 0 && m.sessionSearch.cursor < len(m.sessionSearch.results) {
			result := m.sessionSearch.results[m.sessionSearch.cursor]
			m.sessionSearch.active = false
			return m, m.navigateToSearchResult(result)
		}
		return m, nil
	case "tab":
		// Toggle scope: global ↔ project
		if m.sessionSearch.scope == searchScopeGlobal {
			m.sessionSearch.scope = searchScopeProject
		} else {
			m.sessionSearch.scope = searchScopeGlobal
		}
		m.sessionSearch.gen++
		return m, m.sessionSearchDebounceCmd()
	case "alt+t":
		// Switch to title search mode
		m.sessionSearch.contentSearch = false
		m.sessionSearch.gen++
		return m, m.sessionSearchDebounceCmd()
	case "alt+c":
		// Switch to content search mode
		m.sessionSearch.contentSearch = true
		m.sessionSearch.gen++
		return m, m.sessionSearchDebounceCmd()
	case "alt+f":
		// Apply sidebar filter from search results
		if len(m.sessionSearch.results) > 0 {
			m.sidebarFilter = make(map[string]bool)
			for _, r := range m.sessionSearch.results {
				m.sidebarFilter[r.Path] = true
			}
			m.sessionSearch.active = false
			m.activePane = paneSidebar
			// Rebuild sidebar with filter
			if m.currentProj != nil {
				tree := m.providerTrees[m.providerTab]
				if tree != nil {
					m.sidebar = buildSidebar(m.currentProj, tree.Plans, m.expandedConvPath, m.sidebarFilter)
					m.sidebarCursor = 0
					if len(m.sidebar) > 0 && !m.sidebar[0].navigable() {
						m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
					}
				}
			}
			return m, nil
		}
	case "up":
		if m.sessionSearch.cursor > 0 {
			m.sessionSearch.cursor--
		}
	case "down":
		if m.sessionSearch.cursor < len(m.sessionSearch.results)-1 {
			m.sessionSearch.cursor++
		}
	case "backspace":
		if len(m.sessionSearch.input) > 0 {
			m.sessionSearch.input = m.sessionSearch.input[:len(m.sessionSearch.input)-1]
			m.sessionSearch.cursor = 0
			m.sessionSearch.offset = 0
			m.sessionSearch.gen++
			return m, m.sessionSearchDebounceCmd()
		}
		return m, nil
	default:
		r := []rune(key)
		if len(r) == 1 && r[0] >= 32 {
			m.sessionSearch.input = append(m.sessionSearch.input, r[0])
			m.sessionSearch.cursor = 0
			m.sessionSearch.offset = 0
			m.sessionSearch.gen++
			return m, m.sessionSearchDebounceCmd()
		}
		return m, nil
	}

	// Keep cursor visible
	maxVisible := m.height/2 - 6
	if maxVisible < 1 {
		maxVisible = 1
	}
	if m.sessionSearch.cursor < m.sessionSearch.offset {
		m.sessionSearch.offset = m.sessionSearch.cursor
	}
	if m.sessionSearch.cursor >= m.sessionSearch.offset+maxVisible {
		m.sessionSearch.offset = m.sessionSearch.cursor - maxVisible + 1
	}

	return m, nil
}

func (m *model) computeSessionSearchResults() {
	query := string(m.sessionSearch.input)
	if query == "" {
		m.sessionSearch.results = nil
		return
	}

	q := strings.ToLower(query)
	var allResults []SearchResult

	// Content search mode: use rg/grep to find matching files
	if m.sessionSearch.contentSearch {
		home, _ := os.UserHomeDir()
		claudeDir := filepath.Join(home, ".claude")
		matchedFiles := searchContentInFiles(query, claudeDir)
		if matchedFiles != nil {
			for pi, tree := range m.providerTrees {
				if tree == nil {
					continue
				}
				for i, proj := range tree.Projects {
					if m.sessionSearch.scope == searchScopeProject && m.currentProj != nil && proj.DirName != m.currentProj.DirName {
						continue
					}
					for _, conv := range proj.Conversations {
						if matchedFiles[conv.Path] {
							allResults = append(allResults, SearchResult{
								Source:      proj.Source,
								ProjectName: proj.DisplayName,
								Title:       conv.Title,
								Preview:     conv.Preview,
								Path:        conv.Path,
								ModTime:     conv.ModTime,
								ProjIndex:   i,
								MsgCount:    conv.MsgCount,
								CWD:         conv.CWD,
							})
						}
					}
					_ = pi
				}
			}
		}
		sortSearchResults(allResults)
		if len(allResults) > 50 {
			allResults = allResults[:50]
		}
		m.sessionSearch.results = allResults
		return
	}

	// Metadata search mode: search title/preview/slug/project name
	if m.sessionSearch.scope == searchScopeProject && m.currentProj != nil {
		// Project scope: search current project's conversations in the cached tree
		tree := m.providerTrees[m.providerTab]
		if tree != nil {
			for i, proj := range tree.Projects {
				if proj.DirName != m.currentProj.DirName {
					continue
				}
				for _, conv := range proj.Conversations {
					if matchConversation(conv, proj.DisplayName, q) {
						allResults = append(allResults, SearchResult{
							Source:      proj.Source,
							ProjectName: proj.DisplayName,
							Title:       conv.Title,
							Preview:     conv.Preview,
							Path:        conv.Path,
							ModTime:     conv.ModTime,
							ProjIndex:   i,
							MsgCount:    conv.MsgCount,
							CWD:         conv.CWD,
						})
					}
				}
			}
		}
	} else {
		// Global scope: search all provider trees
		for pi, tree := range m.providerTrees {
			if tree == nil {
				continue
			}
			for i, proj := range tree.Projects {
				for _, conv := range proj.Conversations {
					if matchConversation(conv, proj.DisplayName, q) {
						allResults = append(allResults, SearchResult{
							Source:      proj.Source,
							ProjectName: proj.DisplayName,
							Title:       conv.Title,
							Preview:     conv.Preview,
							Path:        conv.Path,
							ModTime:     conv.ModTime,
							ProjIndex:   i,
							MsgCount:    conv.MsgCount,
							CWD:         conv.CWD,
						})
					}
				}
				_ = pi // used for future provider index tracking
			}
		}
	}
	sortSearchResults(allResults)

	// Limit results
	if len(allResults) > 50 {
		allResults = allResults[:50]
	}
	m.sessionSearch.results = allResults
}

// computeContentSearchResults runs content search via providers (called from async goroutine).
func computeContentSearchResults(query string, scope searchScope, providers []Provider, tabIdx int, currentDirName string) []SearchResult {
	var allResults []SearchResult
	for _, prov := range providers {
		projectID := ""
		if scope == searchScopeProject && currentDirName != "" {
			projectID = currentDirName
		}
		results := prov.ContentSearch(query, projectID)
		allResults = append(allResults, results...)
	}

	sortSearchResults(allResults)
	if len(allResults) > 50 {
		allResults = allResults[:50]
	}
	return allResults
}

// matchConversation checks if a conversation matches the search query against metadata fields.
func matchConversation(conv TreeConversation, projName, q string) bool {
	title := strings.ToLower(conv.Title)
	preview := strings.ToLower(conv.Preview)
	slug := strings.ToLower(conv.Slug)
	pname := strings.ToLower(projName)
	return strings.Contains(title, q) || strings.Contains(preview, q) ||
		strings.Contains(slug, q) || strings.Contains(pname, q)
}

// searchContentInFiles searches for query in JSONL files using rg or grep.
// Returns a set of absolute file paths (using filepath.Clean) that contain the query.
func searchContentInFiles(query string, claudeDir string) map[string]bool {
	matches := make(map[string]bool)
	searchDir := filepath.Join(claudeDir, "projects")

	// Try rg first, then grep
	if rgPath, err := exec.LookPath("rg"); err == nil {
		cmd := exec.Command(rgPath, "-l", query, searchDir)
		out, err := cmd.Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "" {
					continue
				}
				if !filepath.IsAbs(line) {
					line = filepath.Join(searchDir, line)
				}
				matches[filepath.Clean(line)] = true
			}
			return matches
		}
	}

	if grepPath, err := exec.LookPath("grep"); err == nil {
		cmd := exec.Command(grepPath, "-rl", query, searchDir)
		out, err := cmd.Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "" {
					continue
				}
				if !filepath.IsAbs(line) {
					line = filepath.Join(searchDir, line)
				}
				matches[filepath.Clean(line)] = true
			}
			return matches
		}
	}

	// Manual fallback: walk projects dir and scan each JSONL file
	q := strings.ToLower(query)
	filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0), 1024*1024)
		for scanner.Scan() {
			if strings.Contains(strings.ToLower(scanner.Text()), q) {
				matches[filepath.Clean(path)] = true
				break
			}
		}
		return nil
	})
	return matches
}

func (m model) navigateToSearchResult(result SearchResult) tea.Cmd {
	// Find the provider and project that matches
	for provIdx, prov := range m.providers {
		// Match source: "claude" matches "Claude Code", "opencode" matches "OpenCode"
		provKey := strings.ToLower(strings.ReplaceAll(prov.Name(), " ", ""))
		if !strings.Contains(provKey, result.Source) && result.Source != provKey {
			continue
		}
		tree := m.providerTrees[provIdx]
		if tree == nil {
			continue
		}
		for projIdx, proj := range tree.Projects {
			for _, conv := range proj.Conversations {
				if conv.Path == result.Path || conv.SessionID == result.Path {
					// Found the project and conversation
					return func() tea.Msg {
						return searchNavigateMsg{
							providerIdx: provIdx,
							projIdx:     projIdx,
							convPath:    result.Path,
							convTitle:   result.Title,
						}
					}
				}
			}
		}
	}
	return nil
}

type searchNavigateMsg struct {
	providerIdx int
	projIdx     int
	convPath    string
	convTitle   string
}

func (m model) renderSessionSearchOverlay() string {
	overlayW := 70
	if m.width-10 < overlayW {
		overlayW = m.width - 10
	}
	if overlayW < 40 {
		overlayW = 40
	}
	innerW := overlayW - 6

	var lines []string
	lines = append(lines, titleStyle.Render(" Search Sessions "))
	lines = append(lines, "")

	// Search input
	query := string(m.sessionSearch.input)
	scopeLabel := "global"
	if m.sessionSearch.scope == searchScopeProject {
		scopeLabel = "project"
	}
	modeLabel := "title"
	if m.sessionSearch.contentSearch {
		modeLabel = "content"
	}
	inputLine := fmt.Sprintf(" /%s", query+"\u2588")
	scopeBadge := dimStyle.Render(fmt.Sprintf("[%s %s]", modeLabel, scopeLabel))
	gap := innerW - lipgloss.Width(inputLine) - lipgloss.Width(scopeBadge)
	if gap < 1 {
		gap = 1
	}
	lines = append(lines, inputLine+strings.Repeat(" ", gap)+scopeBadge)
	lines = append(lines, faintStyle.Render(strings.Repeat("\u2500", innerW)))

	// Results
	maxVisible := m.height/2 - 8
	if maxVisible < 3 {
		maxVisible = 3
	}
	if m.sessionSearch.searching {
		lines = append(lines, dimStyle.Render(" Searching..."))
	} else if len(m.sessionSearch.results) == 0 {
		if len(query) > 0 {
			lines = append(lines, dimStyle.Render(" No matches found"))
		} else {
			lines = append(lines, dimStyle.Render(" Type to search..."))
		}
	} else {
		end := m.sessionSearch.offset + maxVisible
		if end > len(m.sessionSearch.results) {
			end = len(m.sessionSearch.results)
		}
		for i := m.sessionSearch.offset; i < end; i++ {
			r := m.sessionSearch.results[i]
			isCur := i == m.sessionSearch.cursor

			// Source badge
			srcBadge := "[CC]"
			if strings.Contains(strings.ToLower(r.Source), "open") {
				srcBadge = "[OC]"
			}

			title := r.Title
			dateStr := formatDateSmart(r.ModTime)
			msgInfo := ""
			if r.MsgCount > 0 {
				msgInfo = fmt.Sprintf(" %d msgs", r.MsgCount)
			}
			cwdInfo := ""
			if r.CWD != "" {
				cwdInfo = " " + shortenPath(r.CWD)
			}
			rightPart := fmt.Sprintf(" %s%s%s", srcBadge, msgInfo, dateStr)
			maxTitle := innerW - lipgloss.Width(rightPart) - 4
			if maxTitle < 10 {
				maxTitle = 10
			}
			if len(title) > maxTitle {
				title = title[:maxTitle-3] + "..."
			}

			entryLine := fmt.Sprintf(" %s", title)
			titleGap := innerW - lipgloss.Width(entryLine) - lipgloss.Width(rightPart)
			if titleGap < 1 {
				titleGap = 1
			}
			entryLine += strings.Repeat(" ", titleGap) + rightPart

			if isCur {
				lines = append(lines, selectedStyle.Render(truncTo(entryLine, innerW)))
			} else {
				lines = append(lines, truncTo(entryLine, innerW))
			}

			// Show project name and CWD on second line for current selection
			if isCur && (r.ProjectName != "" || cwdInfo != "") {
				subLine := r.ProjectName
				if cwdInfo != "" {
					if subLine != "" {
						subLine += "  " + cwdInfo
					} else {
						subLine = cwdInfo
					}
				}
				lines = append(lines, dimStyle.Render("   "+subLine))
			}
		}
	}

	lines = append(lines, "")
	resultCount := len(m.sessionSearch.results)
	hint := fmt.Sprintf(" %d results  ↑↓:navigate  enter:open  alt+f:filter  alt+t:title  alt+c:content  tab:scope  esc:close", resultCount)
	lines = append(lines, dimStyle.Render(hint))

	overlayBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#D97706")).
		Padding(1, 2).
		Width(overlayW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox)
}

// ── Mouse handling ──

// contentPaneRect returns the screen rectangle of the content pane (x, y, w, h).
func (m model) contentPaneRect() (int, int, int, int) {
	if m.directFile != "" {
		return 0, 1, m.width, m.height - 2
	}
	leftW, rightW := m.paneWidths()
	x := leftW + 3 // left pane + separator " │ "
	y := 1          // title bar
	return x, y, rightW, m.height - 2
}

// sidebarPaneRect returns the screen rectangle of the sidebar pane.
func (m model) sidebarPaneRect() (int, int, int, int) {
	leftW, _ := m.paneWidths()
	return 0, 1, leftW, m.height - 2
}

func (m model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.export.active || m.state != viewProjectDetail {
		return m, nil
	}
	if msg.Button != tea.MouseLeft {
		return m, nil
	}

	cx, cy, cw, ch := m.contentPaneRect()

	// Check if click is within content pane
	if msg.X >= cx && msg.X < cx+cw && msg.Y >= cy && msg.Y < cy+ch {
		m.activePane = paneContent
		m.mouseSelecting = true
		m.mouseHasSelection = false
		m.mouseSelStart = [2]int{msg.Y, msg.X}
		m.mouseSelEnd = m.mouseSelStart
		return m, nil
	}

	// Check if click is within sidebar
	if m.directFile == "" {
		sx, sy, sw, sh := m.sidebarPaneRect()
		if msg.X >= sx && msg.X < sx+sw && msg.Y >= sy && msg.Y < sy+sh {
			m.activePane = paneSidebar
			m.mouseSelecting = false
			m.mouseHasSelection = false
			return m, nil
		}
	}

	return m, nil
}

func (m model) handleMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if !m.mouseSelecting {
		return m, nil
	}
	m.mouseSelEnd = [2]int{msg.Y, msg.X}
	m.mouseHasSelection = true
	return m, nil
}

func (m model) handleMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	if !m.mouseSelecting {
		return m, nil
	}
	m.mouseSelecting = false
	m.mouseSelEnd = [2]int{msg.Y, msg.X}

	if m.mouseSelStart == m.mouseSelEnd {
		m.mouseHasSelection = false
		return m, nil
	}

	m.mouseHasSelection = true

	// Extract selected text from content pane
	text := m.extractSelectedText()
	if text != "" {
		return m, copyToClipboard(text)
	}
	return m, nil
}

func (m model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.state != viewProjectDetail {
		return m, nil
	}

	cx, cy, cw, ch := m.contentPaneRect()
	inContent := msg.X >= cx && msg.X < cx+cw && msg.Y >= cy && msg.Y < cy+ch

	if inContent || m.activePane == paneContent {
		_, paneH := m.contentPaneDims()
		contentH := paneH - 2
		maxOff := len(m.contentLines) - contentH
		if maxOff < 0 {
			maxOff = 0
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.contentOffset -= 3
			if m.contentOffset < 0 {
				m.contentOffset = 0
			}
		case tea.MouseWheelDown:
			m.contentOffset += 3
			if m.contentOffset > maxOff {
				m.contentOffset = maxOff
			}
		}
		return m, nil
	}

	// Scroll sidebar
	if m.directFile == "" {
		sidebarH := m.height - 5
		switch msg.Button {
		case tea.MouseWheelUp:
			m.sidebarOffset -= 3
			if m.sidebarOffset < 0 {
				m.sidebarOffset = 0
			}
		case tea.MouseWheelDown:
			maxOff := len(m.sidebar) - sidebarH
			if maxOff < 0 {
				maxOff = 0
			}
			m.sidebarOffset += 3
			if m.sidebarOffset > maxOff {
				m.sidebarOffset = maxOff
			}
		}
	}
	return m, nil
}

// extractSelectedText gets text from contentLines within the selection rectangle.
func (m model) extractSelectedText() string {
	if !m.mouseHasSelection || len(m.contentLines) == 0 {
		return ""
	}

	cx, cy, cw, _ := m.contentPaneRect()

	// Normalize selection coordinates to content-relative
	startRow, startCol := m.mouseSelStart[0], m.mouseSelStart[1]
	endRow, endCol := m.mouseSelEnd[0], m.mouseSelEnd[1]

	// Ensure start <= end
	if startRow > endRow || (startRow == endRow && startCol > endCol) {
		startRow, endRow = endRow, startRow
		startCol, endCol = endCol, startCol
	}

	// Convert screen coords to content line indices
	contentStartY := cy + 2 // title + separator
	startLine := (startRow - contentStartY) + m.contentOffset
	endLine := (endRow - contentStartY) + m.contentOffset

	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(m.contentLines) {
		endLine = len(m.contentLines) - 1
	}
	if startLine > endLine {
		return ""
	}

	// Convert X positions to content-relative
	relStartCol := startCol - cx
	relEndCol := endCol - cx
	if relStartCol < 0 {
		relStartCol = 0
	}
	if relEndCol > cw {
		relEndCol = cw
	}

	var lines []string
	for i := startLine; i <= endLine; i++ {
		line := m.contentLines[i]
		// Strip ANSI for text extraction
		plain := ansi.Strip(line)

		sc := 0
		ec := len(plain)
		if i == startLine {
			sc = relStartCol
		}
		if i == endLine {
			ec = relEndCol
		}
		if sc > len(plain) {
			sc = len(plain)
		}
		if ec > len(plain) {
			ec = len(plain)
		}
		if sc < ec {
			lines = append(lines, plain[sc:ec])
		} else {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard copies text to the system clipboard.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("pbcopy")
		case "linux":
			// Try wl-copy first (Wayland), then xclip (X11)
			if _, err := exec.LookPath("wl-copy"); err == nil {
				cmd = exec.Command("wl-copy")
			} else if _, err := exec.LookPath("xclip"); err == nil {
				cmd = exec.Command("xclip", "-selection", "clipboard")
			} else if _, err := exec.LookPath("xsel"); err == nil {
				cmd = exec.Command("xsel", "--clipboard", "--input")
			} else {
				return statusClearMsg{}
			}
		default:
			return statusClearMsg{}
		}
		cmd.Stdin = strings.NewReader(text)
		cmd.Run()
		return statusClearMsg{}
	}
}

// ── Export overlay ──

func (m *model) initExportOverlay(sourcePath, label string) {
	cwd, _ := os.Getwd()
	defaultFilename := fmt.Sprintf("ccview-export-%s.html", time.Now().Format("20060102-150405"))

	hasSubagents := false
	if m.currentProj != nil {
		for _, conv := range m.currentProj.Conversations {
			if conv.Path == sourcePath && len(conv.SubAgents) > 0 {
				hasSubagents = true
				break
			}
		}
	}

	m.export = exportState{
		active:           true,
		step:             exportStepWhat,
		sourcePath:       sourcePath,
		sourceLabel:      label,
		convHasSubagents: hasSubagents,
		pathBuf:          []rune(cwd),
		pathCurPos:       len([]rune(cwd)),
		filenameBuf:      []rune(defaultFilename),
		filenameCurPos:   len([]rune(defaultFilename)),
	}
}

func (m model) updateExportOverlay(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		m.export.active = false
		return m, nil
	}

	switch m.export.step {
	case exportStepWhat:
		maxOpt := 2 // full, main-only
		if m.export.convHasSubagents {
			maxOpt = 3
		}
		switch key {
		case "up", "k":
			if m.export.whatCursor > 0 {
				m.export.whatCursor--
			}
		case "down", "j":
			if m.export.whatCursor < maxOpt-1 {
				m.export.whatCursor++
			}
		case "enter":
			m.export.what = exportWhat(m.export.whatCursor)
			m.export.step = exportStepFormat
		}

	case exportStepFormat:
		switch key {
		case "up", "k":
			if m.export.formatCursor > 0 {
				m.export.formatCursor--
			}
		case "down", "j":
			if m.export.formatCursor < 2 {
				m.export.formatCursor++
			}
		case "enter":
			m.export.format = exportFormat(m.export.formatCursor)
			// Update filename extension
			m.updateExportFilenameExt()
			m.export.step = exportStepPath
		case "backspace":
			m.export.step = exportStepWhat
		}

	case exportStepPath:
		switch key {
		case "enter":
			m.export.step = exportStepFilename
		case "backspace":
			if m.export.pathCurPos > 0 {
				m.export.pathBuf = append(m.export.pathBuf[:m.export.pathCurPos-1], m.export.pathBuf[m.export.pathCurPos:]...)
				m.export.pathCurPos--
			} else {
				m.export.step = exportStepFormat
			}
		case "left":
			if m.export.pathCurPos > 0 {
				m.export.pathCurPos--
			}
		case "right":
			if m.export.pathCurPos < len(m.export.pathBuf) {
				m.export.pathCurPos++
			}
		case "home", "ctrl+a":
			m.export.pathCurPos = 0
		case "end", "ctrl+e":
			m.export.pathCurPos = len(m.export.pathBuf)
		default:
			r := []rune(key)
			if len(r) == 1 && r[0] >= 32 {
				m.export.pathBuf = append(m.export.pathBuf[:m.export.pathCurPos], append([]rune{r[0]}, m.export.pathBuf[m.export.pathCurPos:]...)...)
				m.export.pathCurPos++
			}
		}

	case exportStepFilename:
		switch key {
		case "enter":
			m.export.step = exportStepConfirm
		case "backspace":
			if m.export.filenameCurPos > 0 {
				m.export.filenameBuf = append(m.export.filenameBuf[:m.export.filenameCurPos-1], m.export.filenameBuf[m.export.filenameCurPos:]...)
				m.export.filenameCurPos--
			} else {
				m.export.step = exportStepPath
			}
		case "left":
			if m.export.filenameCurPos > 0 {
				m.export.filenameCurPos--
			}
		case "right":
			if m.export.filenameCurPos < len(m.export.filenameBuf) {
				m.export.filenameCurPos++
			}
		case "home", "ctrl+a":
			m.export.filenameCurPos = 0
		case "end", "ctrl+e":
			m.export.filenameCurPos = len(m.export.filenameBuf)
		default:
			r := []rune(key)
			if len(r) == 1 && r[0] >= 32 {
				m.export.filenameBuf = append(m.export.filenameBuf[:m.export.filenameCurPos], append([]rune{r[0]}, m.export.filenameBuf[m.export.filenameCurPos:]...)...)
				m.export.filenameCurPos++
			}
		}

	case exportStepConfirm:
		switch key {
		case "enter", "y":
			m.export.active = false
			return m, m.executeExport()
		case "backspace", "n":
			m.export.step = exportStepFilename
		}
	}

	return m, nil
}

func (m *model) updateExportFilenameExt() {
	name := string(m.export.filenameBuf)
	// Strip existing extension
	for _, ext := range []string{".html", ".md", ".jsonl"} {
		if strings.HasSuffix(name, ext) {
			name = strings.TrimSuffix(name, ext)
			break
		}
	}
	// Add new extension
	switch m.export.format {
	case exportFormatHTML:
		name += ".html"
	case exportFormatMarkdown:
		name += ".md"
	case exportFormatJSONL:
		name += ".jsonl"
	}
	m.export.filenameBuf = []rune(name)
	m.export.filenameCurPos = len(m.export.filenameBuf)
}

func (m model) executeExport() tea.Cmd {
	ex := m.export
	var proj *TreeProject
	if m.currentProj != nil {
		p := *m.currentProj
		proj = &p
	}
	return func() tea.Msg {
		outDir := string(ex.pathBuf)
		outFile := string(ex.filenameBuf)
		outPath := filepath.Join(outDir, outFile)

		switch ex.format {
		case exportFormatHTML:
			if ex.what == exportFullConversation && ex.convHasSubagents {
				err := exportHTMLDir(ex.sourcePath, proj, outDir, outFile)
				if err != nil {
					return exportDoneMsg{"", err}
				}
				return exportDoneMsg{filepath.Join(outDir, strings.TrimSuffix(outFile, filepath.Ext(outFile))), nil}
			}
			entries, err := parseConversation(ex.sourcePath)
			if err != nil {
				return exportDoneMsg{"", err}
			}
			err = exportHTML(entries, outPath, ex.sourcePath)
			return exportDoneMsg{outPath, err}

		case exportFormatMarkdown:
			entries, err := parseConversation(ex.sourcePath)
			if err != nil {
				return exportDoneMsg{"", err}
			}
			err = exportMarkdown(entries, outPath, ex.sourcePath)
			return exportDoneMsg{outPath, err}

		case exportFormatJSONL:
			err := copyFile(ex.sourcePath, outPath)
			return exportDoneMsg{outPath, err}
		}

		return exportDoneMsg{"", fmt.Errorf("unknown format")}
	}
}

func (m model) renderExportOverlay() string {
	overlayW := 64
	if m.width-10 < overlayW {
		overlayW = m.width - 10
	}
	if overlayW < 30 {
		overlayW = 30
	}
	innerW := overlayW - 6 // padding + border

	var lines []string
	lines = append(lines, titleStyle.Render(" Export Conversation "))
	lines = append(lines, "")

	switch m.export.step {
	case exportStepWhat:
		lines = append(lines, " What to export:")
		lines = append(lines, "")
		options := []string{"Full conversation (with subagents)", "Main thread only"}
		if m.export.convHasSubagents {
			options = append(options, "Selected subagent only")
		}
		for i, opt := range options {
			if i == m.export.whatCursor {
				lines = append(lines, selectedStyle.Render(" > "+opt))
			} else {
				lines = append(lines, "   "+opt)
			}
		}
	case exportStepFormat:
		lines = append(lines, " Output format:")
		lines = append(lines, "")
		formats := []string{"HTML", "Markdown (.md)", "JSONL (raw copy)"}
		for i, f := range formats {
			if i == m.export.formatCursor {
				lines = append(lines, selectedStyle.Render(" > "+f))
			} else {
				lines = append(lines, "   "+f)
			}
		}
	case exportStepPath:
		lines = append(lines, " Output directory:")
		lines = append(lines, "")
		pathStr := string(m.export.pathBuf)
		if len(pathStr) > innerW-4 {
			pathStr = "..." + pathStr[len(pathStr)-innerW+7:]
		}
		lines = append(lines, " "+pathStr+"\u2588")
	case exportStepFilename:
		lines = append(lines, " Filename:")
		lines = append(lines, "")
		fnStr := string(m.export.filenameBuf)
		if len(fnStr) > innerW-4 {
			fnStr = "..." + fnStr[len(fnStr)-innerW+7:]
		}
		lines = append(lines, " "+fnStr+"\u2588")
	case exportStepConfirm:
		lines = append(lines, " Confirm export:")
		lines = append(lines, "")
		whats := []string{"Full conversation", "Main thread only", "Selected subagent"}
		fmts := []string{"HTML", "Markdown", "JSONL"}
		lines = append(lines, fmt.Sprintf("   What:   %s", whats[m.export.what]))
		lines = append(lines, fmt.Sprintf("   Format: %s", fmts[m.export.format]))
		lines = append(lines, fmt.Sprintf("   Path:   %s", string(m.export.pathBuf)))
		lines = append(lines, fmt.Sprintf("   File:   %s", string(m.export.filenameBuf)))
		lines = append(lines, "")
		lines = append(lines, loadedStyle.Render(" Press enter to export"))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(" esc:cancel  enter:confirm  backspace:back"))

	overlayBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#D97706")).
		Padding(1, 2).
		Width(overlayW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox)
}

// ── Open in editor ──

func openInEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		if runtime.GOOS == "darwin" {
			editor = "open"
		} else {
			editor = "xdg-open"
		}
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err}
	})
}

// ── Dimensions ──

func (m model) paneWidths() (int, int) {
	leftW := m.width * 35 / 100
	if leftW < 28 {
		leftW = 28
	}
	if leftW > 60 {
		leftW = 60
	}
	rightW := m.width - leftW - 3
	if rightW < 20 {
		rightW = 20
	}
	return leftW, rightW
}

func (m model) contentPaneDims() (int, int) {
	_, rw := m.paneWidths()
	return rw, m.height - 2
}

// ── View ──

func (m model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("  Loading...")
	}
	if m.err != nil {
		return tea.NewView(fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.\n", m.err))
	}

	var s string

	switch m.state {
	case viewLoading:
		s = "\n  Loading...\n"
	case viewProjectList:
		s = m.renderProjectList()
	case viewProjectDetail:
		if m.directFile != "" {
			s = m.renderFullWidth()
		} else {
			s = m.renderProjectDetail()
		}
	}
	if m.sessionSearch.active {
		s = m.renderSessionSearchOverlay()
	}
	if m.export.active {
		s = m.renderExportOverlay()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// ── Project List Screen ──

func (m model) renderProjectList() string {
	var b strings.Builder

	b.WriteString(titleStyle.Width(m.width).Render(" ccview "))
	b.WriteString("\n")

	// Tab bar (only shown when multiple providers have data)
	tabBarH := 0
	if m.hasMultipleTabs() {
		tabBarH = 2
		var tabs []string
		for i, prov := range m.providers {
			t := m.providerTrees[i]
			if t == nil || len(t.Projects) == 0 {
				continue
			}
			label := fmt.Sprintf(" %d: %s (%d) ", i+1, prov.Name(), len(t.Projects))
			if i == m.providerTab {
				tabs = append(tabs, selectedStyle.Render(label))
			} else {
				tabs = append(tabs, dimStyle.Render(label))
			}
		}
		b.WriteString("\n " + strings.Join(tabs, "  "))
		b.WriteString("\n")
	} else {
		b.WriteString("\n")
	}

	if m.tree == nil || len(m.tree.Projects) == 0 {
		b.WriteString("  No projects found.\n\n")
		b.WriteString(statusStyle.Render("  q: quit"))
		return b.String()
	}

	idxs := filteredProjectIndices(m.tree, m.projectFilter)

	// Filter bar
	filterBarH := 0
	if len(m.projectFilter) > 0 || m.projectFilterActive {
		filterBarH = 1
		if m.projectFilterActive {
			b.WriteString(backStyle.Render("  Filter: "+string(m.projectFilter)+"_") + dimStyle.Render("  (enter: confirm, esc: clear)"))
		} else {
			b.WriteString(backStyle.Render("  Filter: "+string(m.projectFilter)) + dimStyle.Render("  (f: edit, esc: clear)"))
		}
		b.WriteString("\n")
	}

	if len(idxs) == 0 {
		b.WriteString(dimStyle.Render("  No matching projects.\n"))
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  0 / %d projects | f: filter | esc: clear | q: quit", len(m.tree.Projects))))
		return b.String()
	}

	// Find cursor position in filtered list for scrolling
	cursorPos := 0
	for i, idx := range idxs {
		if idx == m.projCursor {
			cursorPos = i
			break
		}
	}

	viewH := m.height - 5 - tabBarH - filterBarH
	itemH := 3
	maxVisible := viewH / itemH
	if maxVisible < 1 {
		maxVisible = 1
	}

	// Compute visible start without mutating model (value receiver)
	visStart := m.projOffset
	if cursorPos < visStart {
		visStart = cursorPos
	}
	if cursorPos >= visStart+maxVisible {
		visStart = cursorPos - maxVisible + 1
	}

	end := visStart + maxVisible
	if end > len(idxs) {
		end = len(idxs)
	}

	for fi := visStart; fi < end; fi++ {
		i := idxs[fi]
		proj := m.tree.Projects[i]
		isSelected := i == m.projCursor

		// Line 1: project path
		name := proj.DisplayName
		if lipgloss.Width(name) > m.width-6 {
			name = "..." + name[len(name)-m.width+9:]
		}

		line1 := "  " + name
		if isSelected {
			line1 = selectedStyle.Width(m.width).Render(line1)
		} else {
			line1 = projectNameStyle.Render(line1)
		}

		// Line 2: metadata
		meta := fmt.Sprintf("  %d conv", proj.ConvCount)
		if proj.ConvCount != 1 {
			meta += "s"
		}
		meta += fmt.Sprintf(" · %d msgs", proj.MsgCount)
		if proj.LastActive != "" {
			meta += " · " + formatDateSmart(proj.LastActive)
		}

		// Badges
		badges := ""
		if proj.ClaudeMD != "" {
			badges += "  CLAUDE.md"
		}
		if len(proj.MemoryFiles) > 0 {
			badges += fmt.Sprintf("  Memory(%d)", len(proj.MemoryFiles))
		}

		line2 := projectMetaStyle.Render(meta) + projectBadgeStyle.Render(badges)

		b.WriteString(line1 + "\n")
		b.WriteString(line2 + "\n")
		b.WriteString("\n")
	}

	// Pad remaining
	rendered := (end - visStart) * itemH
	for i := rendered; i < viewH; i++ {
		b.WriteString("\n")
	}

	// Status
	total := len(m.tree.Projects)
	filtered := len(idxs)
	hint := " %d projects | enter: open | j/k: navigate | f: filter | q: quit"
	if m.hasMultipleTabs() {
		hint = " %d projects | enter: open | j/k: navigate | f: filter | tab/1-2: switch | q: quit"
	}
	if filtered < total {
		hint = fmt.Sprintf(" %d / %d projects", filtered, total) + hint[len(fmt.Sprintf(" %d projects", total)):]
	}
	b.WriteString(statusStyle.Render(fmt.Sprintf(hint, total)))

	return b.String()
}

func formatDateSmart(iso string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, iso); err == nil {
			t = t.Local()
			now := time.Now()
			if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
				return t.Format("15:04")
			}
			if now.Sub(t) < 7*24*time.Hour {
				return t.Format("Mon 15:04")
			}
			if t.Year() == now.Year() {
				return t.Format("Jan 2 15:04")
			}
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// ── Project Detail Screen (split pane) ──

func (m model) renderProjectDetail() string {
	leftW, rightW := m.paneWidths()
	paneH := m.height - 2
	if paneH < 3 {
		paneH = 3
	}

	sideLines := m.buildSidebarLines(leftW, paneH)
	contentLines := m.buildContentLines(rightW, paneH)

	padL := lipgloss.NewStyle().Width(leftW).MaxWidth(leftW)
	padR := lipgloss.NewStyle().MaxWidth(rightW)
	sep := sepStyle.Render(" \u2502 ")

	var b strings.Builder
	b.WriteString(titleStyle.Width(m.width).Render(fmt.Sprintf(" %s ", m.currentProj.DisplayName)))
	b.WriteString("\n")

	for i := 0; i < paneH; i++ {
		left := ""
		if i < len(sideLines) {
			left = sideLines[i]
		}
		right := ""
		if i < len(contentLines) {
			right = contentLines[i]
		}
		b.WriteString(padL.Render(left))
		b.WriteString(sep)
		b.WriteString(padR.Render(right))
		b.WriteString("\n")
	}

	b.WriteString(m.renderStatus())
	return b.String()
}

func (m model) renderFullWidth() string {
	paneH := m.height - 2
	lines := m.buildContentLines(m.width, paneH)

	var b strings.Builder
	title := m.contentTitle
	if lipgloss.Width(title) > m.width-4 {
		title = ansi.Truncate(title, m.width-7, "...")
	}
	b.WriteString(titleStyle.Width(m.width).Render(fmt.Sprintf(" %s ", title)))
	b.WriteString("\n")

	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.contentSearchActive {
		query := string(m.contentSearchInput)
		matchInfo := ""
		if len(m.contentMatches) > 0 {
			matchInfo = fmt.Sprintf("  %d/%d", m.contentMatchIdx+1, len(m.contentMatches))
		} else if len(query) > 0 {
			matchInfo = "  no matches"
		}
		b.WriteString(statusHighlight.Render(" /") + statusStyle.Render(query+"\u2588") + dimStyle.Render(matchInfo))
	} else {
		b.WriteString(statusStyle.Render(" j/k: scroll | pgup/pgdn | /: search | t: tools | e: export | q: quit"))
	}
	return b.String()
}

// ── Sidebar rendering ──

func (m model) buildSidebarLines(w, h int) []string {
	lines := make([]string, 0, h)

	// Pane title
	if m.activePane == paneSidebar {
		lines = append(lines, paneTitleActive.Render("CONVERSATIONS"))
	} else {
		lines = append(lines, paneTitleInactive.Render("CONVERSATIONS"))
	}
	lines = append(lines, faintStyle.Render(strings.Repeat("\u2500", w)))

	contentH := h - 2
	if contentH < 1 {
		contentH = 1
	}

	end := m.sidebarOffset + contentH
	if end > len(m.sidebar) {
		end = len(m.sidebar)
	}

	for i := m.sidebarOffset; i < end; i++ {
		item := m.sidebar[i]
		isCur := i == m.sidebarCursor && m.activePane == paneSidebar
		isLoaded := item.path != "" && item.path == m.contentPath

		switch item.kind {
		case "separator":
			lines = append(lines, faintStyle.Render(strings.Repeat("\u2500", w)))

		case "back":
			text := " " + item.label
			if isCur {
				lines = append(lines, selectedStyle.Render(truncTo(text, w)))
			} else {
				lines = append(lines, backStyle.Render(truncTo(text, w)))
			}

		case "file":
			text := " " + item.label
			if isCur {
				lines = append(lines, selectedStyle.Render(truncTo(text, w)))
			} else if isLoaded {
				lines = append(lines, loadedStyle.Render(truncTo(text, w)))
			} else {
				lines = append(lines, dimStyle.Render(truncTo(text, w)))
			}

		case "header":
			lines = append(lines, dimStyle.Bold(true).Render(" "+item.label))

		case "conversation":
			label := item.label
			badgeW := lipgloss.Width(item.badge)
			maxL := w - badgeW - 4
			if maxL < 8 {
				maxL = 8
			}
			if lipgloss.Width(label) > maxL {
				label = ansi.Truncate(label, maxL-3, "...")
			}
			text := " " + label
			if item.badge != "" {
				gap := w - lipgloss.Width(text) - badgeW - 1
				if gap > 0 {
					text += strings.Repeat(" ", gap) + item.badge
				}
			}
			if isCur {
				lines = append(lines, selectedStyle.Render(truncTo(text, w)))
			} else if isLoaded {
				lines = append(lines, loadedStyle.Render(truncTo(text, w)))
			} else {
				lines = append(lines, truncTo(text, w))
			}

		case "subagent":
			text := "   " + item.label
			if isCur {
				lines = append(lines, selectedStyle.Render(truncTo(text, w)))
			} else if isLoaded {
				lines = append(lines, loadedStyle.Render(truncTo(text, w)))
			} else {
				lines = append(lines, faintStyle.Render(truncTo(text, w)))
			}
		}
	}

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

func truncTo(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w > 3 {
		return ansi.Truncate(s, w-3, "...")
	}
	return ansi.Truncate(s, w, "")
}

// wrapLine wraps a plain text line at width, with continuation lines indented.
func wrapLine(text string, width, contIndent int) []string {
	if len(text) <= width {
		return []string{text}
	}
	var result []string
	indent := strings.Repeat(" ", contIndent)
	first := true
	for len(text) > 0 {
		w := width
		if !first {
			w = width - contIndent
		}
		if w <= 0 {
			w = 10
		}
		if len(text) <= w {
			if first {
				result = append(result, text)
			} else {
				result = append(result, indent+text)
			}
			break
		}
		cut := strings.LastIndex(text[:w], " ")
		if cut <= 0 {
			cut = w
		}
		if first {
			result = append(result, text[:cut])
		} else {
			result = append(result, indent+text[:cut])
		}
		text = strings.TrimLeft(text[cut:], " ")
		first = false
	}
	return result
}

// ── Content pane ──

func (m model) buildContentLines(w, h int) []string {
	lines := make([]string, 0, h)

	if m.contentTitle != "" {
		title := m.contentTitle
		if lipgloss.Width(title) > w-2 {
			title = ansi.Truncate(title, w-5, "...")
		}
		if m.activePane == paneContent {
			lines = append(lines, paneTitleActive.Render(title))
		} else {
			lines = append(lines, paneTitleInactive.Render(title))
		}
		lines = append(lines, faintStyle.Render(strings.Repeat("\u2500", w)))
	} else {
		lines = append(lines, paneTitleInactive.Render("VIEWER"))
		lines = append(lines, faintStyle.Render(strings.Repeat("\u2500", w)))
	}

	contentH := h - 2
	if contentH < 1 {
		contentH = 1
	}

	if len(m.contentLines) == 0 {
		blank := (contentH - 4) / 2
		for i := 0; i < blank; i++ {
			lines = append(lines, "")
		}
		lines = append(lines, faintStyle.Render("  Select a conversation"))
		lines = append(lines, "")
		lines = append(lines, faintStyle.Render("  enter  open"))
		lines = append(lines, faintStyle.Render("  tab    switch pane"))
	} else {
		end := m.contentOffset + contentH
		if end > len(m.contentLines) {
			end = len(m.contentLines)
		}
		for i := m.contentOffset; i < end; i++ {
			lines = append(lines, m.contentLines[i])
		}
	}

	// Apply mouse selection highlighting
	if m.mouseHasSelection && m.activePane == paneContent {
		cx, cy, _, _ := m.contentPaneRect()
		startRow, startCol := m.mouseSelStart[0], m.mouseSelStart[1]
		endRow, endCol := m.mouseSelEnd[0], m.mouseSelEnd[1]
		if startRow > endRow || (startRow == endRow && startCol > endCol) {
			startRow, endRow = endRow, startRow
			startCol, endCol = endCol, startCol
		}
		selStyle := lipgloss.NewStyle().Reverse(true)
		for idx := range lines {
			screenY := cy + idx
			if screenY < startRow || screenY > endRow {
				continue
			}
			plain := ansi.Strip(lines[idx])
			sc := 0
			ec := len(plain)
			if screenY == startRow {
				sc = startCol - cx
			}
			if screenY == endRow {
				ec = endCol - cx
			}
			if sc < 0 {
				sc = 0
			}
			if ec > len(plain) {
				ec = len(plain)
			}
			if sc >= ec || sc >= len(plain) {
				continue
			}
			lines[idx] = plain[:sc] + selStyle.Render(plain[sc:ec]) + plain[ec:]
		}
	}

	// Apply content search highlighting
	if m.contentSearchQuery != "" && len(m.contentMatches) > 0 {
		highlightStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#D97706")).
			Foreground(lipgloss.Color("#000000"))
		currentMatchStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#FBBF24")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true)
		q := strings.ToLower(m.contentSearchQuery)
		for idx := range lines {
			lineIdx := m.contentOffset + idx - 2 // subtract header lines
			if lineIdx < 0 {
				continue
			}
			plain := ansi.Strip(lines[idx])
			plainLower := strings.ToLower(plain)
			if !strings.Contains(plainLower, q) {
				continue
			}
			// Determine if this is the current match line
			isCurrentMatch := false
			if m.contentMatchIdx >= 0 && m.contentMatchIdx < len(m.contentMatches) {
				isCurrentMatch = m.contentMatches[m.contentMatchIdx] == lineIdx
			}
			// Rebuild line with highlighted matches
			var result strings.Builder
			pos := 0
			for {
				i := strings.Index(plainLower[pos:], q)
				if i < 0 {
					result.WriteString(plain[pos:])
					break
				}
				result.WriteString(plain[pos : pos+i])
				matchEnd := pos + i + len(q)
				if isCurrentMatch {
					result.WriteString(currentMatchStyle.Render(plain[pos+i : matchEnd]))
				} else {
					result.WriteString(highlightStyle.Render(plain[pos+i : matchEnd]))
				}
				pos = matchEnd
			}
			lines[idx] = result.String()
		}
	}

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// ── Status bar ──

func (m model) renderStatus() string {
	// Content search input bar
	if m.contentSearchActive {
		query := string(m.contentSearchInput)
		matchInfo := ""
		if len(m.contentMatches) > 0 {
			matchInfo = fmt.Sprintf("  %d/%d", m.contentMatchIdx+1, len(m.contentMatches))
		} else if len(query) > 0 {
			matchInfo = "  no matches"
		}
		return statusHighlight.Render(" /") +
			statusStyle.Render(query+"\u2588") +
			dimStyle.Render(matchInfo) +
			statusStyle.Render("  enter:search  esc:cancel")
	}

	if m.statusMsg != "" {
		return statusHighlight.Render(" " + m.statusMsg)
	}

	var parts []string
	if m.activePane == paneSidebar {
		parts = append(parts,
			statusHighlight.Render("sidebar")+statusStyle.Render("/viewer"),
			"enter:open", "o:editor", "esc:back", "tab:viewer", "e:export", "/:search", "q:quit",
		)
		if m.sidebarFilter != nil {
			parts = append(parts, statusHighlight.Render("filtered (esc:clear)"))
		}
	} else {
		parts = append(parts,
			statusStyle.Render("sidebar/")+statusHighlight.Render("viewer"),
		)
		if len(m.contentLines) > 0 {
			_, ph := m.contentPaneDims()
			total := len(m.contentLines) - (ph - 2)
			pct := 100
			if total > 0 {
				pct = (m.contentOffset * 100) / total
			}
			parts = append(parts, fmt.Sprintf("%d%%", pct))
		}
		if len(m.contentMatches) > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d", m.contentMatchIdx+1, len(m.contentMatches)))
		}
		parts = append(parts, "j/k:scroll", "/:search", "n/N:match", "t:tools", "T:think", "R:result", "tab:sidebar", "e:export", "q:quit")
	}
	return statusStyle.Render(" " + strings.Join(parts, "  "))
}

// ── Conversation rendering ──

func renderConversation(entries []Entry, width int, showToolDetails bool, showToolResults bool, showThinking bool) []string {
	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	var lines []string
	sep := faintStyle.Render(strings.Repeat("\u2500", width))

	// Pre-pass: collect tool results by tool_use_id for inline matching
	toolResults := make(map[string]string)
	for _, entry := range entries {
		if entry.Type != "user" || entry.Parsed == nil {
			continue
		}
		for _, b := range getContentBlocks(entry.Parsed) {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				resultText := b.Text
				if resultText == "" {
					resultText = b.Content
				}
				if resultText != "" {
					toolResults[b.ToolUseID] = resultText
				}
			}
		}
	}
	renderedResults := make(map[string]bool)

	for _, entry := range entries {
		switch entry.Type {
		case "user":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			isToolResult := false
			allInline := true
			for _, b := range blocks {
				if b.Type == "tool_result" {
					isToolResult = true
					if !renderedResults[b.ToolUseID] {
						allInline = false
					}
				}
			}
			if isToolResult {
				// Skip if all results were already rendered inline with tool calls
				if allInline {
					continue
				}
				// Show orphaned results (no matching tool_use in a previous assistant message)
				for _, b := range blocks {
					if b.Type != "tool_result" || renderedResults[b.ToolUseID] {
						continue
					}
					resultText := b.Text
					if resultText == "" {
						resultText = b.Content
					}
					if resultText == "" {
						continue
					}
					if showToolResults {
						lines = append(lines, toolResultStyle.Render("  [result]"))
						rendered := renderMarkdownTerm(resultText, contentWidth-4)
						for _, rl := range strings.Split(rendered, "\n") {
							lines = append(lines, dimStyle.Render("  "+rl))
						}
					} else {
						lines = append(lines, toolResultStyle.Render("  [result] returned  (R to expand)"))
					}
				}
				continue
			}

			ts := formatTimestamp(entry.Timestamp)
			header := userHeaderStyle.Render(fmt.Sprintf(" USER  %s ", ts))
			lines = append(lines, "", sep, header, "")
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					rendered := renderMarkdownTerm(b.Text, contentWidth)
					for _, line := range strings.Split(rendered, "\n") {
						lines = append(lines, "  "+line)
					}
				}
			}

		case "assistant":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			modelName := entry.Parsed.Model
			ts := formatTimestamp(entry.Timestamp)

			label := "ASSISTANT"
			if modelName != "" {
				label = fmt.Sprintf("ASSISTANT  %s", modelName)
			}
			header := assistantHeaderStyle.Render(fmt.Sprintf(" %s  %s ", label, ts))
			lines = append(lines, "", sep, header, "")

			for _, b := range blocks {
				switch b.Type {
				case "thinking":
					if b.Thinking == "" {
						continue
					}
					thinkLines := strings.Split(b.Thinking, "\n")
					contIndent := 2
					if showThinking {
						for _, tl := range thinkLines {
							for _, wl := range wrapLine(tl, contentWidth-4, contIndent) {
								lines = append(lines, thinkingStyle.Render("  "+wl))
							}
						}
					} else {
						show := 1
						if len(thinkLines) > show {
							for i := 0; i < show; i++ {
								for _, wl := range wrapLine(thinkLines[i], contentWidth-4, contIndent) {
									lines = append(lines, thinkingStyle.Render("  "+wl))
								}
							}
							lines = append(lines, dimStyle.Render(fmt.Sprintf("  ... (%d more lines, T to expand)", len(thinkLines)-show)))
						} else {
							for _, tl := range thinkLines {
								for _, wl := range wrapLine(tl, contentWidth-4, contIndent) {
									lines = append(lines, thinkingStyle.Render("  "+wl))
								}
							}
						}
					}
					lines = append(lines, "")

				case "text":
					if b.Text == "" {
						continue
					}
					rendered := renderMarkdownTerm(b.Text, contentWidth-2)
					for _, line := range strings.Split(rendered, "\n") {
						lines = append(lines, "  "+line)
					}

				case "tool_use":
					summary := formatToolUse(b.Name, b.Input)
					fullLine := fmt.Sprintf("  [tool] %s", summary)
					for _, wl := range wrapLine(fullLine, contentWidth, 11) {
						lines = append(lines, toolStyle.Render(wl))
					}
					if showToolDetails && len(b.Input) > 0 {
						detail := formatToolInput(b.Input, contentWidth-4)
						for _, dl := range strings.Split(detail, "\n") {
							lines = append(lines, dimStyle.Render("    "+dl))
						}
					}
					// Render matched result inline
					if result, ok := toolResults[b.ID]; ok {
						renderedResults[b.ID] = true
						if showToolResults {
							for _, rl := range strings.Split(result, "\n") {
								for _, wl := range wrapLine(rl, contentWidth-6, 4) {
									lines = append(lines, dimStyle.Render("    "+wl))
								}
							}
						} else {
							lines = append(lines, dimStyle.Render("    (R to expand result)"))
						}
					}
				}
			}

			if entry.Parsed.Usage != nil {
				u := entry.Parsed.Usage
				usage := fmt.Sprintf("in:%d out:%d", u.InputTokens, u.OutputTokens)
				if u.CacheReadInputTokens > 0 {
					usage += fmt.Sprintf(" cache_read:%d", u.CacheReadInputTokens)
				}
				lines = append(lines, dimStyle.Render(fmt.Sprintf("  [tokens] %s", usage)))
			}

		case "system":
			if entry.Subtype == "local_command" {
				cmd := extractCommandName(entry.Content)
				lines = append(lines, systemStyle.Render(fmt.Sprintf("  [system] %s", cmd)))
			}
		}
	}

	lines = append(lines, "", sep, dimStyle.Render("  End of conversation"), "")
	return lines
}
