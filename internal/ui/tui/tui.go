// Package tui 实现基于 Bubble Tea 的终端 TUI 看板。
// 支持模块选择、版本历史浏览、各阶段数据详情查看、解析触发等功能。
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"apr-tracker/internal/backend"
	"apr-tracker/internal/db"
	"apr-tracker/internal/models"
)

// ============================================================================
// 样式定义
// ============================================================================

var (
	// 基础色板
	primary   = lipgloss.Color("#0052cc")
	danger    = lipgloss.Color("#de350b")
	success   = lipgloss.Color("#00875a")
	warning   = lipgloss.Color("#ff991f")
	muted     = lipgloss.Color("#5e6c84")
	bgDark    = lipgloss.Color("#1a1a2e")
	bgPanel   = lipgloss.Color("#16213e")

	// 组件样式
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primary).
			MarginBottom(1).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(primary)

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2)

	activeTabStyle = tabStyle.
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(primary)

	inactiveTabStyle = tabStyle.
			Foreground(muted)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#333333")).
			Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(success).
			Bold(true)

	highlightStyle = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true).
			Background(lipgloss.Color("#ffebe6"))

	mutedStyle = lipgloss.NewStyle().
			Foreground(muted)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444")).
			Padding(1, 2)

	// WNS 数值颜色（负值红色，正值绿色）
	wnsColor = func(v string) lipgloss.Style {
		if strings.HasPrefix(v, "-") {
			return lipgloss.NewStyle().Foreground(danger).Bold(true)
		}
		return lipgloss.NewStyle().Foreground(success).Bold(true)
	}
)

// ============================================================================
// Tab 定义
// ============================================================================

type Tab int

const (
	TabOverview Tab = iota
	TabTiming
	TabDRC
	TabLatency
	TabCellUsage
	TabRuntime
)

var tabNames = []string{"📊 Overview", "⏱️ Timing", "📐 DRC", "🕐 Latency", "🔋 CellUsage", "⏳ Runtime"}

// ============================================================================
// Model
// ============================================================================

type Model struct {
	// 依赖
	db     *db.Database
	engine *backend.Engine
	cfg    *models.AppConfig

	// 数据状态
	modules       []string
	currentModule string
	records       []*models.VersionRecord
	selectedIdx   int

	// UI 状态
	activeTab    Tab
	loading      bool
	statusMsg    string
	errorMsg     string
	detailMode   bool // 是否在查看详情
	detailRecord *models.VersionRecord
	width        int
	height       int
	ready        bool

	// 子组件
	overviewTable table.Model
	detailVP      viewport.Model
}

// NewModel 创建 TUI 主模型
func NewModel(database *db.Database, eng *backend.Engine, cfg *models.AppConfig) Model {
	modules := make([]string, 0)
	if cfg != nil {
		for _, mods := range cfg.OwnerModules {
			modules = append(modules, mods...)
		}
	}
	// 从 DB 补充
	if len(modules) == 0 {
		dbMods, _ := database.GetAllModules()
		modules = dbMods
	}

	currentModule := ""
	if len(modules) > 0 {
		currentModule = modules[0]
	}

	m := Model{
		db:            database,
		engine:        eng,
		cfg:           cfg,
		modules:       modules,
		currentModule: currentModule,
		activeTab:     TabOverview,
		statusMsg:     "按 r 触发解析  |  q 退出  |  ←→ 切换模块  |  Tab 切换视图",
	}

	// 初始化表格
	m.overviewTable = table.New(
		table.WithColumns([]table.Column{}),
		table.WithRows([]table.Row{}),
		table.WithFocused(false),
		table.WithHeight(20),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(primary)
	s.Selected = s.Selected.Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#0052cc")).Bold(true)
	m.overviewTable.SetStyles(s)

	m.detailVP = viewport.New(80, 20)

	return m
}

// ============================================================================
// Bubble Tea 接口实现
// ============================================================================

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchHistoryCmd(),
		tea.EnterAltScreen,
	)
}

type fetchMsg struct {
	records []*models.VersionRecord
	err     error
}

type parseDoneMsg struct {
	records []*models.VersionRecord
	err     error
}

func (m Model) fetchHistoryCmd() tea.Cmd {
	return func() tea.Msg {
		if m.currentModule == "" {
			return fetchMsg{records: nil, err: nil}
		}
		records, err := m.db.GetHistory(m.currentModule)
		return fetchMsg{records: records, err: err}
	}
}

func (m Model) runParseCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		results, err := m.engine.RunParse(ctx, m.currentModule)
		if err != nil {
			return parseDoneMsg{err: err}
		}

		saved := 0
		for _, r := range results {
			if r.Error != nil {
				continue
			}
			record := &models.VersionRecord{
				Module:      r.Module,
				Version:     r.Version,
				StagesFound: r.StagesFound,
				Timing:      r.Timing,
				DRC:         r.DRC,
				Latency:     r.Latency,
				Runtime:     r.Runtime,
				CellUsage:   r.CellUsage,
			}
			if err := m.db.UpsertVersion(record); err != nil {
				continue
			}
			saved++
		}

		records, _ := m.db.GetHistory(m.currentModule)
		return parseDoneMsg{records: records, err: nil}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.ready = true
		}
		m.overviewTable.SetWidth(msg.Width - 4)
		m.overviewTable.SetHeight(msg.Height - 10)
		m.detailVP.Width = msg.Width - 4
		m.detailVP.Height = msg.Height - 10

	case tea.KeyMsg:
		// 详情模式下单独处理按键
		if m.detailMode {
			switch msg.String() {
			case "esc", "backspace":
				m.detailMode = false
				return m, nil
			case "up", "k":
				m.detailVP.LineUp(1)
				return m, nil
			case "down", "j":
				m.detailVP.LineDown(1)
				return m, nil
			case "pgup":
				m.detailVP.PageUp()
				return m, nil
			case "pgdown":
				m.detailVP.PageDown()
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "tab":
			m.activeTab = (m.activeTab + 1) % Tab(len(tabNames))
			m.updateOverviewTable()
			return m, nil

		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + Tab(len(tabNames))) % Tab(len(tabNames))
			m.updateOverviewTable()
			return m, nil

		case "left", "h":
			// 切换上一个模块
			if len(m.modules) > 0 {
				idx := m.indexOf(m.modules, m.currentModule)
				idx = (idx - 1 + len(m.modules)) % len(m.modules)
				m.currentModule = m.modules[idx]
				m.selectedIdx = 0
				m.statusMsg = fmt.Sprintf("切换到模块: %s", m.currentModule)
				cmds = append(cmds, m.fetchHistoryCmd())
			}

		case "right", "l":
			// 切换下一个模块
			if len(m.modules) > 0 {
				idx := m.indexOf(m.modules, m.currentModule)
				idx = (idx + 1) % len(m.modules)
				m.currentModule = m.modules[idx]
				m.selectedIdx = 0
				m.statusMsg = fmt.Sprintf("切换到模块: %s", m.currentModule)
				cmds = append(cmds, m.fetchHistoryCmd())
			}

		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
			m.updateOverviewTable()

		case "down", "j":
			if m.selectedIdx < len(m.records)-1 {
				m.selectedIdx++
			}
			m.updateOverviewTable()

		case "r":
			if m.currentModule == "" {
				m.statusMsg = "请先选择一个模块"
				return m, nil
			}
			m.loading = true
			m.statusMsg = fmt.Sprintf("正在解析 %s ...", m.currentModule)
			m.errorMsg = ""
			cmds = append(cmds, m.runParseCmd())

		case "R":
			if m.currentModule != "" {
				m.statusMsg = "正在刷新数据..."
				cmds = append(cmds, m.fetchHistoryCmd())
			}

		case "enter":
			if m.selectedIdx >= 0 && m.selectedIdx < len(m.records) {
				m.detailRecord = m.records[m.selectedIdx]
				m.detailMode = true
				m.detailVP.SetContent(m.buildDetailView(m.detailRecord))
				m.detailVP.GotoTop()
			}

		case "1", "2", "3", "4", "5", "6":
			idx := int(msg.String()[0] - '1')
			if idx < len(tabNames) {
				m.activeTab = Tab(idx)
				m.updateOverviewTable()
			}
		}

	case fetchMsg:
		m.loading = false
		if msg.err != nil {
			m.errorMsg = fmt.Sprintf("查询失败: %v", msg.err)
			m.statusMsg = "查询出错"
		} else {
			m.records = msg.records
			if m.records == nil {
				m.records = []*models.VersionRecord{}
			}
			m.errorMsg = ""
			m.statusMsg = fmt.Sprintf("模块 %s 共 %d 条版本记录", m.currentModule, len(m.records))
			m.updateOverviewTable()
		}

	case parseDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.errorMsg = fmt.Sprintf("解析失败: %v", msg.err)
			m.statusMsg = "解析出错"
		} else {
			m.records = msg.records
			if m.records == nil {
				m.records = []*models.VersionRecord{}
			}
			m.errorMsg = ""
			m.statusMsg = fmt.Sprintf("✅ 解析完成！%s 共 %d 条记录", m.currentModule, len(m.records))
			m.updateOverviewTable()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if !m.ready {
		return "初始化中..."
	}

	if m.detailMode {
		return m.renderDetail()
	}

	return m.renderMain()
}

// ============================================================================
// 视图渲染
// ============================================================================

func (m Model) renderMain() string {
	// ---- 顶部标题栏 ----
	header := m.renderHeader()

	// ---- Tab 栏 ----
	tabs := m.renderTabs()

	// ---- 主体内容 ----
	var content string
	switch m.activeTab {
	case TabOverview:
		content = m.renderOverview()
	case TabTiming:
		content = m.renderTimingTab()
	case TabDRC:
		content = m.renderDRCTab()
	case TabLatency:
		content = m.renderLatencyTab()
	case TabCellUsage:
		content = m.renderCellUsageTab()
	case TabRuntime:
		content = m.renderRuntimeTab()
	}

	// ---- 底部状态栏 ----
	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabs,
		content,
		footer,
	)
}

func (m Model) renderHeader() string {
	// 模块选择器（箭头切换）
	moduleNav := fmt.Sprintf("◀ %s ▶", m.currentModule)
	navStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ffffff")).
		Background(primary).
		Padding(0, 2)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#333333")).
		Padding(0, 2).
		Render("📦 APR Tracker")

	loading := ""
	if m.loading {
		loading = lipgloss.NewStyle().Foreground(warning).Render(" ⏳ 解析中...")
	}

	return lipgloss.JoinHorizontal(lipgloss.Center,
		title,
		navStyle.Render(moduleNav),
		loading,
	)
}

func (m Model) renderTabs() string {
	var tabs []string
	for i, name := range tabNames {
		if Tab(i) == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(name))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(name))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) renderOverview() string {
	return lipgloss.NewStyle().
		MarginTop(1).
		Render(m.overviewTable.View())
}

func (m Model) renderTimingTab() string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.records) {
		return mutedStyle.Render("  无数据")
	}
	r := m.records[m.selectedIdx]
	if r.Timing == nil || len(r.Timing) == 0 {
		return mutedStyle.Render("  该版本无时序数据")
	}

	var lines []string
	orderedStages := []string{"10.initial", "30.place", "40.cts", "50.route"}

	for _, stage := range orderedStages {
		t, ok := r.Timing[stage]
		if !ok {
			continue
		}

		// 汇总信息
		var summaryLine string
		if t.Reg2RegSummary != nil {
			s := t.Reg2RegSummary
			wnsStyled := wnsColor(s.WNS).Render(s.WNS)
			summaryLine = fmt.Sprintf("WNS: %s  TNS: %s  NVP: %s  Density: %s  Overflow: %s",
				wnsStyled, s.TNS, s.NVP, t.Density, t.Congestion)
		} else {
			summaryLine = fmt.Sprintf("Density: %s  Overflow: %s", t.Density, t.Congestion)
		}

		stageTitle := lipgloss.NewStyle().Bold(true).Foreground(primary).Render(fmt.Sprintf("▶ %s", stage))
		lines = append(lines, stageTitle)
		lines = append(lines, "  "+summaryLine)

		// 各分组详情
		for groupName, g := range t.Groups {
			if groupName == "reg2reg_summary" {
				continue
			}
			wnsV := wnsColor(g.WNS).Render(g.WNS)
			lines = append(lines, fmt.Sprintf("    %-20s  WNS: %8s  TNS: %10s  NVP: %6s",
				groupName, wnsV, g.TNS, g.NVP))
		}
		lines = append(lines, "")
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderDRCTab() string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.records) {
		return mutedStyle.Render("  无数据")
	}
	r := m.records[m.selectedIdx]
	if r.DRC == nil || r.DRC["50"] == nil {
		return mutedStyle.Render("  该版本无 DRC 数据（需要 50.route 阶段）")
	}

	d := r.DRC["50"]
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(primary).Render("▶ 50.route DRC 违例汇总"),
		"",
		fmt.Sprintf("  DRC 总违例数:   %s",
			dangerForeground(d.ECODRCMax > 0, fmt.Sprintf("%d", d.ECODRCMax))),
		fmt.Sprintf("  M1-M4 (DP):     %d", d.ECODRCDp),
		fmt.Sprintf("  M5+ (Q):        %d", d.ECODRCQ),
		"",
		lipgloss.NewStyle().Bold(true).Foreground(warning).Render("⚠️ Short 违例"),
		"",
		fmt.Sprintf("  M1-M4 Short (DP): %d", d.ECOShortDp),
		fmt.Sprintf("  M5+ Short (Q):    %d", d.ECOShortQ),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderLatencyTab() string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.records) {
		return mutedStyle.Render("  无数据")
	}
	r := m.records[m.selectedIdx]
	if r.Latency == nil {
		return mutedStyle.Render("  该版本无 Latency 数据（需要 40.cts 或之后阶段）")
	}

	var lines []string

	if len(r.Latency.Standard) > 0 {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(primary).Render("▶ Standard Latency"))
		for _, e := range r.Latency.Standard {
			lines = append(lines, fmt.Sprintf("  [%s] %s  Min:%s  Max:%s  Avg:%s  SkewW:%s",
				e.Domain, e.Group, e.Min, e.Max, e.Avg, e.SkewW))
		}
		lines = append(lines, "")
	}

	if len(r.Latency.Cluster) > 0 {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7c3aed")).Render("▶ Cluster Latency"))
		for _, e := range r.Latency.Cluster {
			lines = append(lines, fmt.Sprintf("  [%s] %s  Min:%s  Max:%s  Avg:%s  SkewW:%s",
				e.Domain, e.Group, e.Min, e.Max, e.Avg, e.SkewW))
		}
	}

	if len(lines) == 0 {
		return mutedStyle.Render("  无 Latency 条目")
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderCellUsageTab() string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.records) {
		return mutedStyle.Render("  无数据")
	}
	r := m.records[m.selectedIdx]
	if r.CellUsage == nil || len(r.CellUsage) == 0 {
		return mutedStyle.Render("  该版本无 Cell Usage 数据")
	}

	var lines []string
	orderedStages := []string{"10.initial", "30.place", "40.cts", "50.route"}

	for _, stage := range orderedStages {
		cu, ok := r.CellUsage[stage]
		if !ok {
			continue
		}
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(primary).Render(fmt.Sprintf("▶ %s", stage)))
		lines = append(lines, fmt.Sprintf("  %-15s %10s %8s %10s %8s", "VT Type", "Count", "Count%", "Area", "Area%"))
		lines = append(lines, "  "+strings.Repeat("─", 58))
		for vtName, vt := range cu {
			lines = append(lines, fmt.Sprintf("  %-15s %10s %7s%% %10s %7s%%",
				vtName, vt.Count, vt.CountPerc, vt.Area, vt.AreaPerc))
		}
		lines = append(lines, "")
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderRuntimeTab() string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.records) {
		return mutedStyle.Render("  无数据")
	}
	r := m.records[m.selectedIdx]
	if r.Runtime == nil || len(r.Runtime) == 0 {
		return mutedStyle.Render("  该版本无 Runtime 数据")
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(primary).Render("▶ Stage Runtime"))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %-12s %s", "Stage", "Duration"))
	lines = append(lines, "  "+strings.Repeat("─", 30))

	for stage, dur := range r.Runtime {
		lines = append(lines, fmt.Sprintf("  %-12s ⏳ %s", stage, dur))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderDetail() string {
	if m.detailRecord == nil {
		return "无数据"
	}

	version := lipgloss.NewStyle().Bold(true).Foreground(primary).Render(m.detailRecord.Version)
	header := fmt.Sprintf("📋 详情: %s  [ESC 返回]", version)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		m.detailVP.View(),
	)
}

func (m Model) buildDetailView(r *models.VersionRecord) string {
	var lines []string

	// Stages
	stages := strings.Join(r.StagesFound, ", ")
	if stages == "" {
		stages = "无"
	}
	lines = append(lines, fmt.Sprintf("阶段: %s", stages))

	// Timing 各阶段组详情
	if r.Timing != nil {
		lines = append(lines, "")
		lines = append(lines, "━━━ 时序详情 ━━━")
		orderedStages := []string{"10.initial", "30.place", "40.cts", "50.route"}
		for _, stage := range orderedStages {
			t, ok := r.Timing[stage]
			if !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("\n▶ %s  Density:%s  Overflow:%s", stage, t.Density, t.Congestion))
			if t.Reg2RegSummary != nil {
				s := t.Reg2RegSummary
				lines = append(lines, fmt.Sprintf("  reg2reg Summary → WNS:%s  TNS:%s  NVP:%s", s.WNS, s.TNS, s.NVP))
			}
			for groupName, g := range t.Groups {
				lines = append(lines, fmt.Sprintf("  %-20s WNS:%-8s TNS:%-10s NVP:%-6s", groupName, g.WNS, g.TNS, g.NVP))
			}
		}
	}

	// DRC
	if r.DRC != nil && r.DRC["50"] != nil {
		d := r.DRC["50"]
		lines = append(lines, "")
		lines = append(lines, "━━━ DRC ━━━")
		lines = append(lines, fmt.Sprintf("Max: %d  DP(M1-M4): %d  Q(M5+): %d  ShortDP: %d  ShortQ: %d",
			d.ECODRCMax, d.ECODRCDp, d.ECODRCQ, d.ECOShortDp, d.ECOShortQ))
	}

	// Latency
	if r.Latency != nil {
		lines = append(lines, "")
		lines = append(lines, "━━━ Latency ━━━")
		for _, e := range r.Latency.Standard {
			lines = append(lines, fmt.Sprintf("[Std] %s %s Min:%s Max:%s Avg:%s", e.Domain, e.Group, e.Min, e.Max, e.Avg))
		}
		for _, e := range r.Latency.Cluster {
			lines = append(lines, fmt.Sprintf("[Cls] %s %s Min:%s Max:%s Avg:%s", e.Domain, e.Group, e.Min, e.Max, e.Avg))
		}
	}

	// Runtime
	if r.Runtime != nil {
		lines = append(lines, "")
		lines = append(lines, "━━━ Runtime ━━━")
		for stage, dur := range r.Runtime {
			lines = append(lines, fmt.Sprintf("%s: %s", stage, dur))
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderFooter() string {
	help := lipgloss.NewStyle().Foreground(muted).Render(
		"r:解析  ←→:换模块  ↑↓:导航  Tab:切视图  1-6:跳Tab  Enter:详情  R:刷新  q:退出",
	)

	status := ""
	if m.errorMsg != "" {
		status = errorStyle.Render("❌ " + m.errorMsg)
	} else {
		status = m.statusMsg
	}

	left := lipgloss.NewStyle().Width(m.width - len(help) - 4).Render(status)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
}

// ============================================================================
// 辅助方法
// ============================================================================

func (m *Model) updateOverviewTable() {
	columns := []table.Column{
		{Title: "Version", Width: 25},
	}

	var rows []table.Row

	switch m.activeTab {
	case TabOverview:
		columns = append(columns,
			table.Column{Title: "Stages", Width: 20},
			table.Column{Title: "WNS(reg2reg)", Width: 15},
			table.Column{Title: "TNS(reg2reg)", Width: 15},
			table.Column{Title: "DRC Max", Width: 10},
			table.Column{Title: "Runtime", Width: 25},
		)
		for _, r := range m.records {
			wns := "-"
			tns := "-"
			drcMax := "-"
			runtime := "-"

			if r.Timing != nil {
				for _, stage := range []string{"50.route", "40.cts", "30.place", "10.initial"} {
					if t, ok := r.Timing[stage]; ok && t.Reg2RegSummary != nil {
						wns = t.Reg2RegSummary.WNS
						tns = t.Reg2RegSummary.TNS
						break
					}
				}
			}
			if r.DRC != nil && r.DRC["50"] != nil {
				drcMax = fmt.Sprintf("%d", r.DRC["50"].ECODRCMax)
			}
			if len(r.Runtime) > 0 {
				parts := make([]string, 0)
				for stage, dur := range r.Runtime {
					parts = append(parts, fmt.Sprintf("%s:%s", stage[:2], dur))
				}
				runtime = strings.Join(parts, " ")
			}

			rows = append(rows, table.Row{
				r.Version,
				strings.Join(r.StagesFound, ", "),
				wns, tns, drcMax, runtime,
			})
		}

	case TabTiming, TabDRC, TabLatency, TabCellUsage, TabRuntime:
		// 共用同一种版本列表（简洁版）
		columns = append(columns,
			table.Column{Title: "Stages", Width: 25},
			table.Column{Title: "Status", Width: 30},
		)
		for _, r := range m.records {
			status := "✓"
			switch m.activeTab {
			case TabTiming:
				if r.Timing != nil && len(r.Timing) > 0 {
					ws := 0
					for range r.Timing {
						ws++
					}
					status = fmt.Sprintf("✓ %d stages", ws)
				} else {
					status = "✗ 无数据"
				}
			case TabDRC:
				if r.DRC != nil && r.DRC["50"] != nil {
					status = fmt.Sprintf("✓ Max=%d", r.DRC["50"].ECODRCMax)
				} else {
					status = "✗ 无DRC"
				}
			case TabLatency:
				if r.Latency != nil {
					cnt := len(r.Latency.Standard) + len(r.Latency.Cluster)
					status = fmt.Sprintf("✓ %d entries", cnt)
				} else {
					status = "✗ 无数据"
				}
			case TabCellUsage:
				if r.CellUsage != nil && len(r.CellUsage) > 0 {
					status = fmt.Sprintf("✓ %d stages", len(r.CellUsage))
				} else {
					status = "✗ 无数据"
				}
			case TabRuntime:
				if r.Runtime != nil && len(r.Runtime) > 0 {
					status = fmt.Sprintf("✓ %d stages", len(r.Runtime))
				} else {
					status = "✗ 无数据"
				}
			}
			rows = append(rows, table.Row{
				r.Version,
				strings.Join(r.StagesFound, ", "),
				status,
			})
		}
	}

	m.overviewTable.SetColumns(columns)
	m.overviewTable.SetRows(rows)

	// 高亮选中的行
	if m.selectedIdx >= 0 && m.selectedIdx < len(rows) {
		m.overviewTable.SetCursor(m.selectedIdx)
	}
}

func (m Model) indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return 0
}

func dangerForeground(cond bool, s string) string {
	if cond {
		return lipgloss.NewStyle().Foreground(danger).Bold(true).Render(s)
	}
	return lipgloss.NewStyle().Foreground(success).Bold(true).Render(s)
}
