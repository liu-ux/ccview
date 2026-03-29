package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// ── Model ──

type model struct {
	state         viewState
	width, height int

	// Project list screen
	tree       *TreeData
	projCursor int
	projOffset int

	// Project detail screen
	activePane       pane
	projIndex        int          // index into tree.Projects
	currentProj      *TreeProject // pointer to selected project
	sidebar          []sidebarItem
	sidebarCursor    int
	sidebarOffset    int
	expandedConvPath string // which conversation's subagents are visible

	// Content pane
	contentLines  []string
	contentOffset int
	contentTitle  string
	contentPath   string
	contentKind   string

	directFile string
	err        error
	statusMsg  string

	// Export overlay
	export exportState
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

type treeLoadedMsg struct {
	tree *TreeData
	err  error
}
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

func newModel(directFile string) model {
	return model{state: viewLoading, directFile: directFile}
}

func (m model) Init() tea.Cmd {
	if m.directFile != "" {
		return loadConvCmd(m.directFile, m.directFile, 120)
	}
	return loadTreeCmd
}

func loadTreeCmd() tea.Msg {
	tree, err := loadTree()
	return treeLoadedMsg{tree, err}
}

func loadConvCmd(path, title string, width int) tea.Cmd {
	return func() tea.Msg {
		entries, err := parseConversation(path)
		if err != nil {
			return contentLoadedMsg{nil, title, path, "conversation", err}
		}
		lines := renderConversation(entries, width)
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

func buildSidebar(proj *TreeProject, plans []TreeFileRef, expandedConvPath string) []sidebarItem {
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
			return m, loadConvCmd(m.contentPath, m.contentTitle, rw)
		}
		return m, nil

	case treeLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.tree = msg.tree
		if m.directFile != "" {
			// Direct file mode handled by contentLoadedMsg
			return m, nil
		}
		m.state = viewProjectList
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

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// Export overlay intercepts all keys when active
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

func (m model) updateProjectList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.tree == nil || len(m.tree.Projects) == 0 {
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.projCursor > 0 {
			m.projCursor--
		}
	case "down", "j":
		if m.projCursor < len(m.tree.Projects)-1 {
			m.projCursor++
		}
	case "home", "g":
		m.projCursor = 0
	case "end", "G":
		m.projCursor = len(m.tree.Projects) - 1
	case "enter", "l", "right":
		m.openProject(m.projCursor)
		return m, nil
	}

	// Keep cursor visible
	viewH := m.height - 4
	itemH := 3 // lines per project item
	maxVisible := viewH / itemH
	if maxVisible < 1 {
		maxVisible = 1
	}
	if m.projCursor < m.projOffset {
		m.projOffset = m.projCursor
	}
	if m.projCursor >= m.projOffset+maxVisible {
		m.projOffset = m.projCursor - maxVisible + 1
	}

	return m, nil
}

func (m *model) openProject(idx int) {
	if idx < 0 || idx >= len(m.tree.Projects) {
		return
	}
	m.projIndex = idx
	m.currentProj = &m.tree.Projects[idx]
	m.expandedConvPath = ""
	m.sidebar = buildSidebar(m.currentProj, m.tree.Plans, m.expandedConvPath)
	m.sidebarCursor = 0
	// Move cursor to first navigable item
	if len(m.sidebar) > 0 && !m.sidebar[0].navigable() {
		m.sidebarCursor = nextNavigable(m.sidebar, -1, 1)
	}
	m.sidebarOffset = 0
	m.activePane = paneSidebar
	m.contentLines = nil
	m.contentTitle = ""
	m.contentPath = ""
	m.state = viewProjectDetail
}

func (m model) updateSidebar(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	_, rightW := m.paneWidths()

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc", "backspace", "h", "left":
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
				m.sidebar = buildSidebar(m.currentProj, m.tree.Plans, m.expandedConvPath)
				// Find cursor for the expanded conversation
				for i, si := range m.sidebar {
					if si.path == item.path && si.kind == "conversation" {
						m.sidebarCursor = i
						break
					}
				}
				m.contentTitle = item.label
				m.contentLines = nil
				return m, loadConvCmd(item.path, item.label, rightW)
			case "subagent":
				m.contentTitle = item.label
				m.contentLines = nil
				return m, loadConvCmd(item.path, item.label, rightW)
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
	case "esc", "h", "left", "tab":
		if m.directFile != "" {
			return m, tea.Quit
		}
		m.activePane = paneSidebar
	case "e":
		if m.contentPath != "" && m.contentKind == "conversation" {
			m.initExportOverlay(m.contentPath, m.contentTitle)
			return m, nil
		}
	case "o":
		if m.contentPath != "" {
			return m, openInEditor(m.contentPath)
		}
	}
	return m, nil
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
	if m.export.active {
		s = m.renderExportOverlay()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// ── Project List Screen ──

func (m model) renderProjectList() string {
	var b strings.Builder

	b.WriteString(titleStyle.Width(m.width).Render(" ccview "))
	b.WriteString("\n\n")

	if m.tree == nil || len(m.tree.Projects) == 0 {
		b.WriteString("  No projects found in ~/.claude/\n\n")
		b.WriteString(statusStyle.Render("  q: quit"))
		return b.String()
	}

	viewH := m.height - 5
	itemH := 3
	maxVisible := viewH / itemH
	if maxVisible < 1 {
		maxVisible = 1
	}

	end := m.projOffset + maxVisible
	if end > len(m.tree.Projects) {
		end = len(m.tree.Projects)
	}

	for i := m.projOffset; i < end; i++ {
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
	rendered := (end - m.projOffset) * itemH
	for i := rendered; i < viewH; i++ {
		b.WriteString("\n")
	}

	// Status
	total := len(m.tree.Projects)
	b.WriteString(statusStyle.Render(fmt.Sprintf(" %d projects | enter: open | j/k: navigate | q: quit", total)))

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

	b.WriteString(statusStyle.Render(" j/k: scroll | pgup/pgdn | e: export | q: quit"))
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

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// ── Status bar ──

func (m model) renderStatus() string {
	if m.statusMsg != "" {
		return statusHighlight.Render(" " + m.statusMsg)
	}

	var parts []string
	if m.activePane == paneSidebar {
		parts = append(parts,
			statusHighlight.Render("sidebar")+statusStyle.Render("/viewer"),
			"enter:open", "o:editor", "esc:back", "tab:viewer", "e:export", "q:quit",
		)
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
		parts = append(parts, "j/k:scroll", "pgup/dn", "tab:sidebar", "e:export", "q:quit")
	}
	return statusStyle.Render(" " + strings.Join(parts, "  "))
}

// ── Conversation rendering ──

func renderConversation(entries []Entry, width int) []string {
	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	var lines []string
	sep := faintStyle.Render(strings.Repeat("\u2500", width))

	for _, entry := range entries {
		switch entry.Type {
		case "user":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			isToolResult := false
			for _, b := range blocks {
				if b.Type == "tool_result" {
					isToolResult = true
					break
				}
			}
			if isToolResult {
				lines = append(lines, toolResultStyle.Render("  [result] returned"))
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
					show := 3
					prefix := "  [thinking] "
					contIndent := 15
					if len(thinkLines) > show {
						for i := 0; i < show; i++ {
							for _, wl := range wrapLine(prefix+thinkLines[i], contentWidth, contIndent) {
								lines = append(lines, thinkingStyle.Render(wl))
							}
						}
						lines = append(lines, thinkingStyle.Render(fmt.Sprintf("  ... (%d more lines)", len(thinkLines)-show)))
					} else {
						for _, tl := range thinkLines {
							for _, wl := range wrapLine(prefix+tl, contentWidth, contIndent) {
								lines = append(lines, thinkingStyle.Render(wl))
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
