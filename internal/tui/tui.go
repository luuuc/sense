package tui

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

// Mode represents the TUI interaction mode.
type Mode int

const (
	ModeNormal    Mode = iota
	ModeSelection      // node selected, arrow keys snap between nodes
	ModeBlast          // blast radius visualization active
	ModeSearch         // semantic search active
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeSelection:
		return "selection"
	case ModeBlast:
		return "blast"
	case ModeSearch:
		return "search"
	}
	return "?"
}

// graphStats holds the summary counts displayed in the initial view.
type graphStats struct {
	Symbols int
	Edges   int
}

// Run launches the TUI using the given adapter. The caller must ensure
// stdout is a TTY before calling this. The caller owns the adapter lifetime.
func Run(ctx context.Context, adapter *sqlite.Adapter, senseDir string, opts ...ModelOption) error {
	stats, err := loadStats(ctx, adapter)
	if err != nil {
		return fmt.Errorf("load graph stats: %w", err)
	}

	layout, err := LoadLayout(senseDir)
	if err != nil {
		return fmt.Errorf("load layout: %w", err)
	}
	if layout == nil {
		layout, err = ComputeAndCacheLayout(ctx, adapter, senseDir)
		if err != nil {
			return fmt.Errorf("compute layout: %w", err)
		}
	}

	se := buildSearchEngine(ctx, adapter, senseDir)
	root := filepath.Dir(senseDir)
	allOpts := append([]ModelOption{
		WithEcosystemPrompts(senseDir, root, parseMajorVersion(version.Version)),
	}, opts...)
	m := newModel(stats, layout, adapter.DB(), se, allOpts...)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = p.Run()
	return err
}

func buildSearchEngine(ctx context.Context, adapter *sqlite.Adapter, senseDir string) *search.Engine {
	root := filepath.Dir(senseDir)
	var vectorIdx search.VectorIndex
	var embedder embed.Embedder

	if config.IsEmbeddingsEnabled(root) {
		embeddings, err := adapter.LoadEmbeddings(ctx)
		if err == nil && len(embeddings) > 0 {
			vectorIdx = search.BuildHNSWIndex(embeddings)
			embedder, _ = embed.NewBundledEmbedder()
		}
	}
	return search.NewEngine(adapter, vectorIdx, embedder)
}

func loadStats(ctx context.Context, adapter *sqlite.Adapter) (graphStats, error) {
	symbols, err := adapter.SymbolCount(ctx)
	if err != nil {
		return graphStats{}, err
	}
	db := adapter.DB()
	var edges int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_edges").Scan(&edges); err != nil {
		return graphStats{}, err
	}
	return graphStats{Symbols: symbols, Edges: edges}, nil
}

// SenseDir resolves the .sense directory for a given project root.
func SenseDir(root string) string {
	return filepath.Join(root, ".sense")
}

// nodeCache stores per-node data that requires DB lookups, keyed by symbol ID.
type nodeCache struct {
	filePaths  map[int64]string
	lineStarts map[int64]int
	edgeCounts map[int64][2]int // [callers, callees]
}

func newNodeCache() nodeCache {
	return nodeCache{
		filePaths:  make(map[int64]string),
		lineStarts: make(map[int64]int),
		edgeCounts: make(map[int64][2]int),
	}
}

type model struct {
	stats       graphStats
	renderer    *GraphRenderer
	anim        animation
	width       int
	height      int
	quit        bool
	dimStyle    lipgloss.Style
	accentStyle lipgloss.Style
	mode        Mode
	selection   selectionState
	nodeInfo    *NodeInfo
	db           *sql.DB
	cache        nodeCache
	blast        *blastState
	searchEngine *search.Engine
	searchState  *searchState
	status       StatusData
	statusCh     <-chan StatusData
	pulse        pulseState
	nudge        nudgeState
	prompt       promptState
	majorVersion int
}

// ModelOption configures optional model dependencies.
type ModelOption func(*model)

// WithStatusChannel sets a channel for receiving live status updates.
func WithStatusChannel(ch <-chan StatusData) ModelOption {
	return func(m *model) {
		m.statusCh = ch
	}
}

// WithEcosystemPrompts enables ecosystem prompts with persistence in senseDir.
func WithEcosystemPrompts(senseDir, projectRoot string, majorVersion int) ModelOption {
	return func(m *model) {
		m.majorVersion = majorVersion
		m.prompt = newPromptState(senseDir, majorVersion, promptContext{
			ProjectRoot: projectRoot,
			Symbols:     m.status.Symbols,
		})
	}
}

func newModel(stats graphStats, layout *Layout, db *sql.DB, searchEngine *search.Engine, opts ...ModelOption) model {
	nodeCount := 0
	if layout != nil {
		nodeCount = len(layout.Nodes)
	}
	palette := DetectPalette()
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#586069"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#ABB2BF"))
	if !palette.Dark {
		dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#959DA5"))
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#24292E"))
	}
	m := model{
		stats: stats,
		renderer: &GraphRenderer{
			Layout:  layout,
			Mode:    DetectRenderMode(),
			Palette: palette,
		},
		anim:         newAnimation(nodeCount),
		dimStyle:     dim,
		accentStyle:  accent,
		mode:         ModeNormal,
		selection:    newSelectionState(),
		db:           db,
		cache:        newNodeCache(),
		searchEngine: searchEngine,
		status: StatusData{
			Symbols: stats.Symbols,
			Edges:   stats.Edges,
		},
		pulse: newPulseState(palette.Dark),
		nudge: newNudgeState(),
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.anim.start(), pulseTick(), nudgeCheck()}
	if m.statusCh != nil {
		cmds = append(cmds, listenForStatusUpdates(m.statusCh))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case animTickMsg:
		count, cmd := m.anim.update()
		m.renderer.VisibleCount = count
		return m, cmd
	case blastTickMsg:
		if m.blast != nil && m.blast.frame < blastFrames {
			m.blast.frame++
			m.applyBlastOverrides()
			if m.blast.frame < blastFrames {
				return m, blastTick()
			}
		}
	case searchDebounceMsg:
		if m.searchState != nil && msg.id == m.searchState.debounceID && m.searchEngine != nil {
			return m, executeSearch(m.searchEngine, m.searchState.query, msg.id)
		}
	case pulseTickMsg:
		m.pulse.render(time.Time(msg))
		return m, pulseTick()
	case nudgeCheckMsg:
		m.nudge.evaluate(m.status, time.Time(msg))
		return m, nudgeCheck()
	case statusUpdateMsg:
		m.status = StatusData(msg)
		m.pulse.event()
		if m.statusCh != nil {
			return m, listenForStatusUpdates(m.statusCh)
		}
	case searchResultMsg:
		if m.searchState != nil && msg.id == m.searchState.debounceID {
			m.searchState.results = msg.results
			m.searchState.cursor = 0
			m.searchState.pending = false
			m.renderer.NodeColorOverride = m.searchState.applyOverrides(m.renderer.Layout)
			if len(msg.results) > 0 {
				m.renderer.SelectedID = msg.results[0].SymbolID
			} else {
				m.renderer.SelectedID = 0
			}
		}
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.nudge.dismiss()

	if msg.String() == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}

	if m.selection.filterMode {
		return m.handleFilterKey(msg)
	}

	switch m.mode {
	case ModeSearch:
		return m.handleSearchKey(msg)
	case ModeBlast:
		return m.handleBlastKey(msg)
	case ModeSelection:
		if msg.String() == "x" && m.prompt.hasActive() {
			m.prompt.dismiss(m.majorVersion)
			return m, nil
		}
		return m.handleSelectionKey(msg)
	default:
		if msg.String() == "x" && m.prompt.hasActive() {
			m.prompt.dismiss(m.majorVersion)
			return m, nil
		}
		return m.handleNormalKey(msg)
	}
}

func (m model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.quit = true
		return m, tea.Quit
	case tea.KeyEnter:
		m.enterSelectionMode()
	case tea.KeyUp:
		step := m.renderer.Viewport.PanStep()
		m.renderer.Viewport.Pan(0, -step)
	case tea.KeyDown:
		step := m.renderer.Viewport.PanStep()
		m.renderer.Viewport.Pan(0, step)
	case tea.KeyLeft:
		step := m.renderer.Viewport.PanStep()
		m.renderer.Viewport.Pan(-step, 0)
	case tea.KeyRight:
		step := m.renderer.Viewport.PanStep()
		m.renderer.Viewport.Pan(step, 0)
	case tea.KeyTab:
		m.renderer.Lens = m.renderer.Lens.Next()
	case tea.KeyRunes:
		switch msg.String() {
		case "q":
			m.quit = true
			return m, tea.Quit
		case "k":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(0, -step)
		case "j":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(0, step)
		case "h":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(-step, 0)
		case "l":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(step, 0)
		case "+", "=":
			m.renderer.Viewport.ZoomIn()
		case "-", "_":
			m.renderer.Viewport.ZoomOut()
		case "s":
			m.enterSelectionMode()
		case "f":
			m.enterSelectionMode()
			m.selection.startFilter()
			m.selection.updateFilter(m.renderer.Layout)
		case "/":
			return m.enterSearchMode()
		}
	}
	return m, nil
}

func (m model) handleSelectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.exitSelectionMode()
	case tea.KeyEnter:
		return m.triggerBlast()
	case tea.KeyUp:
		m.selection.moveSelection(m.renderer.Layout, 0, -1)
		m.refreshNodeInfo()
	case tea.KeyDown:
		m.selection.moveSelection(m.renderer.Layout, 0, 1)
		m.refreshNodeInfo()
	case tea.KeyLeft:
		m.selection.moveSelection(m.renderer.Layout, -1, 0)
		m.refreshNodeInfo()
	case tea.KeyRight:
		m.selection.moveSelection(m.renderer.Layout, 1, 0)
		m.refreshNodeInfo()
	case tea.KeyTab:
		m.renderer.Lens = m.renderer.Lens.Next()
	case tea.KeyRunes:
		switch msg.String() {
		case "q":
			m.quit = true
			return m, tea.Quit
		case "k":
			m.selection.moveSelection(m.renderer.Layout, 0, -1)
			m.refreshNodeInfo()
		case "j":
			m.selection.moveSelection(m.renderer.Layout, 0, 1)
			m.refreshNodeInfo()
		case "h":
			m.selection.moveSelection(m.renderer.Layout, -1, 0)
			m.refreshNodeInfo()
		case "l":
			m.selection.moveSelection(m.renderer.Layout, 1, 0)
			m.refreshNodeInfo()
		case "+", "=":
			m.renderer.Viewport.ZoomIn()
		case "-", "_":
			m.renderer.Viewport.ZoomOut()
		case "f":
			m.selection.startFilter()
			m.selection.updateFilter(m.renderer.Layout)
		case "/":
			return m.enterSearchMode()
		}
	}
	return m, nil
}

func (m model) handleBlastKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.exitBlastMode()
	case tea.KeyRunes:
		switch msg.String() {
		case "q":
			m.quit = true
			return m, tea.Quit
		case "/":
			m.exitBlastMode()
			return m.enterSearchMode()
		}
	}
	return m, nil
}

func (m model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.exitSearchMode()
		return m, nil
	case tea.KeyEnter:
		if m.searchState != nil && len(m.searchState.results) > 0 {
			symID := m.searchState.selectedSymbolID()
			m.exitSearchMode()
			m.selectNodeByID(symID)
			return m.triggerBlast()
		}
		return m, nil
	case tea.KeyBackspace:
		if m.searchState != nil && len(m.searchState.query) > 0 {
			m.searchState.query = m.searchState.query[:len(m.searchState.query)-1]
			if len(m.searchState.query) == 0 {
				m.renderer.NodeColorOverride = nil
				m.renderer.SelectedID = 0
				m.searchState.results = nil
				return m, nil
			}
			_, cmd := m.searchState.scheduleSearch()
			return m, cmd
		}
		return m, nil
	case tea.KeyUp:
		if m.searchState != nil && m.searchState.cursor > 0 {
			m.searchState.cursor--
			m.renderer.SelectedID = m.searchState.selectedSymbolID()
		}
		return m, nil
	case tea.KeyDown:
		if m.searchState != nil && m.searchState.cursor < len(m.searchState.results)-1 {
			m.searchState.cursor++
			m.renderer.SelectedID = m.searchState.selectedSymbolID()
		}
		return m, nil
	case tea.KeyRunes:
		if m.searchState != nil {
			m.searchState.query += string(msg.Runes)
			_, cmd := m.searchState.scheduleSearch()
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

func (m model) enterSearchMode() (tea.Model, tea.Cmd) {
	if m.searchEngine == nil {
		return m, nil
	}
	m.mode = ModeSearch
	m.searchState = newSearchState()
	m.renderer.NodeColorOverride = nil
	return m, nil
}

func (m *model) exitSearchMode() {
	m.mode = ModeNormal
	m.searchState = nil
	m.renderer.NodeColorOverride = nil
	m.renderer.SelectedID = 0
}

func (m *model) selectNodeByID(symbolID int64) {
	if m.renderer.Layout == nil {
		return
	}
	for i, n := range m.renderer.Layout.Nodes {
		if n.ID == symbolID {
			m.selection.selectedIdx = i
			m.renderer.SelectedID = symbolID
			m.mode = ModeSelection
			return
		}
	}
}

func (m model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.selection.cancelFilter()
	case tea.KeyEnter:
		m.selection.confirmFilter()
		m.refreshNodeInfo()
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.selection.filterText) > 0 {
			m.selection.filterText = m.selection.filterText[:len(m.selection.filterText)-1]
			m.selection.updateFilter(m.renderer.Layout)
		}
	case tea.KeyUp:
		if m.selection.matchCursor > 0 {
			m.selection.matchCursor--
			if len(m.selection.matches) > 0 {
				m.selection.selectedIdx = m.selection.matches[m.selection.matchCursor]
			}
		}
	case tea.KeyDown:
		if m.selection.matchCursor < len(m.selection.matches)-1 {
			m.selection.matchCursor++
			m.selection.selectedIdx = m.selection.matches[m.selection.matchCursor]
		}
	case tea.KeyRunes:
		m.selection.filterText += string(msg.Runes)
		m.selection.updateFilter(m.renderer.Layout)
	}
	return m, nil
}

func (m *model) enterSelectionMode() {
	m.mode = ModeSelection
	if m.selection.selectedIdx < 0 {
		m.selection.selectNearest(m.renderer.Layout, m.renderer.Viewport)
	}
	m.refreshNodeInfo()
	m.syncSelectedID()
}

func (m model) triggerBlast() (tea.Model, tea.Cmd) {
	n := m.selection.selectedNode(m.renderer.Layout)
	if n == nil || m.db == nil {
		return m, nil
	}
	bs, err := computeBlast(m.db, n.ID)
	if err != nil {
		return m, nil
	}
	totalNodes := 0
	if m.renderer.Layout != nil {
		totalNodes = len(m.renderer.Layout.Nodes)
	}
	bs.hubNode = totalNodes > 0 && len(bs.hopMap) > totalNodes/2
	bs.frame = 0
	m.blast = bs
	m.mode = ModeBlast
	m.applyBlastOverrides()
	return m, blastTick()
}

func (m *model) exitBlastMode() {
	m.mode = ModeSelection
	m.blast = nil
	m.renderer.NodeColorOverride = nil
	m.refreshNodeInfo()
}

func (m *model) applyBlastOverrides() {
	if m.blast == nil || m.renderer.Layout == nil {
		m.renderer.NodeColorOverride = nil
		return
	}
	overrides := make(map[int64]uint8, len(m.renderer.Layout.Nodes))
	maxHop := m.blast.visibleHops()

	if m.blast.hubNode && m.blast.frame >= blastFrames {
		for _, n := range m.renderer.Layout.Nodes {
			if hop, ok := m.blast.hopMap[n.ID]; ok && hop == 0 {
				overrides[n.ID] = colorBlastSubject
			} else {
				overrides[n.ID] = colorFaded
			}
		}
	} else {
		for _, n := range m.renderer.Layout.Nodes {
			hop, affected := m.blast.hopMap[n.ID]
			if !affected || hop > maxHop {
				overrides[n.ID] = colorFaded
			} else {
				overrides[n.ID] = blastColorForHop(hop)
			}
		}
	}
	m.renderer.NodeColorOverride = overrides
}

func (m *model) exitSelectionMode() {
	m.mode = ModeNormal
	m.selection.selectedIdx = -1
	m.nodeInfo = nil
	m.renderer.SelectedID = 0
}

func (m *model) syncSelectedID() {
	if n := m.selection.selectedNode(m.renderer.Layout); n != nil {
		m.renderer.SelectedID = n.ID
	} else {
		m.renderer.SelectedID = 0
	}
}

func (m *model) refreshNodeInfo() {
	m.syncSelectedID()
	n := m.selection.selectedNode(m.renderer.Layout)
	if n == nil {
		m.nodeInfo = nil
		return
	}
	info := &NodeInfo{
		Name:      n.Name,
		Qualified: n.Qualified,
		Kind:      n.Kind,
	}
	if m.db != nil {
		info.FilePath = m.lookupFilePath(n.FileID)
		info.LineStart = m.lookupLineStart(n.ID)
		info.Callers, info.Callees = m.lookupEdgeCounts(n.ID)
	}
	m.nodeInfo = info
}

func (m *model) lookupFilePath(fileID int64) string {
	if p, ok := m.cache.filePaths[fileID]; ok {
		return p
	}
	var path string
	if err := m.db.QueryRow("SELECT path FROM sense_files WHERE id = ?", fileID).Scan(&path); err != nil {
		return ""
	}
	m.cache.filePaths[fileID] = path
	return path
}

func (m *model) lookupLineStart(symbolID int64) int {
	if line, ok := m.cache.lineStarts[symbolID]; ok {
		return line
	}
	var line int
	if err := m.db.QueryRow("SELECT line_start FROM sense_symbols WHERE id = ?", symbolID).Scan(&line); err != nil {
		return 0
	}
	m.cache.lineStarts[symbolID] = line
	return line
}

func (m *model) lookupEdgeCounts(symbolID int64) (callers, callees int) {
	if counts, ok := m.cache.edgeCounts[symbolID]; ok {
		return counts[0], counts[1]
	}
	_ = m.db.QueryRow(
		"SELECT COUNT(*) FROM sense_edges WHERE target_id = ? AND source_id IS NOT NULL",
		symbolID,
	).Scan(&callers)
	_ = m.db.QueryRow(
		"SELECT COUNT(*) FROM sense_edges WHERE source_id = ?",
		symbolID,
	).Scan(&callees)
	m.cache.edgeCounts[symbolID] = [2]int{callers, callees}
	return
}

func (m model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	hasNudge := m.nudge.active != nil
	hasPrompt := m.prompt.hasActive()
	reservedRows := 1
	if hasNudge {
		reservedRows++
	}
	if hasPrompt {
		reservedRows++
	}
	if m.mode == ModeSelection && m.nodeInfo != nil {
		reservedRows++
	}
	if m.mode == ModeBlast && m.blast != nil {
		reservedRows++
	}
	if m.mode == ModeSearch && m.searchState != nil {
		reservedRows += 1 + searchMaxVisible
	}
	if m.selection.filterMode {
		reservedRows++
	}

	graphRows := m.height - reservedRows
	if graphRows < 1 {
		graphRows = 1
	}

	graph := m.renderer.Render(m.width, graphRows)

	var panels []string
	if m.selection.filterMode {
		panels = append(panels, renderFilterOverlay(m.selection, m.dimStyle, m.accentStyle))
	} else if m.mode == ModeSearch && m.searchState != nil {
		searchInput := renderSearchInput(m.searchState, m.dimStyle, m.accentStyle)
		results := renderSearchResults(m.searchState, searchMaxVisible, m.width, m.dimStyle, m.accentStyle)
		if results != "" {
			panels = append(panels, searchInput, results)
		} else {
			panels = append(panels, searchInput)
		}
	} else if m.mode == ModeBlast && m.blast != nil {
		blastInfo := m.blast.statusText()
		if m.blast.hubNode {
			blastInfo += "  (hub node — affects most of the graph)"
		}
		panels = append(panels, m.accentStyle.Render(blastInfo))
	} else if m.mode == ModeSelection && m.nodeInfo != nil {
		panels = append(panels, renderInfoPanel(*m.nodeInfo, m.dimStyle, m.accentStyle))
	}

	if hasPrompt {
		panels = append(panels, m.prompt.render(m.width, m.dimStyle, m.accentStyle))
	}
	if hasNudge {
		panels = append(panels, m.nudge.render(m.width, m.dimStyle, m.accentStyle))
	}
	panels = append(panels, m.statusBar())
	bottom := strings.Join(panels, "\n")
	return graph + "\n" + bottom
}

func (m model) statusBar() string {
	left := m.pulse.render(time.Now()) + " " + renderSessionStatus(m.status, m.width, m.dimStyle)
	center := fmt.Sprintf("lens:%s  zoom:%s", m.renderer.Lens, m.renderer.Viewport.Zoom)

	var hints string
	switch {
	case m.selection.filterMode:
		hints = "↑↓:navigate  enter:select  esc:cancel"
	case m.mode == ModeSearch:
		hints = "↑↓:navigate  enter:blast  esc:back"
	case m.mode == ModeBlast:
		hints = "/:search  esc:back  q:quit"
	case m.mode == ModeSelection:
		hints = "hjkl:move  enter:blast  /:search  f:find  esc:back  q:quit"
	default:
		hints = "hjkl:pan  +/-:zoom  tab:lens  s:select  /:search  q:quit"
	}

	if m.width < 60 {
		hints = "q:quit"
	} else if m.width < 90 && m.mode == ModeNormal {
		hints = "s:select  tab:lens  q:quit"
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(center) - lipgloss.Width(hints)
	if gap < 2 {
		return left + "  " + m.accentStyle.Render(center)
	}

	leftPad := gap / 2
	rightPad := gap - leftPad
	return left + strings.Repeat(" ", leftPad) + m.accentStyle.Render(center) + strings.Repeat(" ", rightPad) + m.dimStyle.Render(hints)
}
