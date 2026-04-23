package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxAppActiveCommands = 3
	maxAppRecentLines    = 4
	minAppSidebarWidth   = 84
)

var (
	appShellChromeStyle = lipgloss.NewStyle()
	appShellPanelStyle  = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				Padding(0, 1)
	appShellHeaderStyle = lipgloss.NewStyle().Bold(true)
	appShellFooterStyle = lipgloss.NewStyle()
)

type appShellFrame struct {
	Header           string
	Panels           []appShellPanel
	Footer           string
	Width            int
	Height           int
	Transcript       []string
	TranscriptOffset int
	TranscriptStatus string
	HelpVisible      bool
}

type appShellPanel struct {
	Title string
	Lines []string
}

type appShellLayout struct {
	useSidebar       bool
	sidebarWidth     int
	sidebarHeight    int
	transcriptWidth  int
	transcriptHeight int
}

type appShellKeyMap struct {
	Scroll key.Binding
	Page   key.Binding
	Jump   key.Binding
	Help   key.Binding
	Cancel key.Binding
}

func (m appShellKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{m.Scroll, m.Page, m.Jump, m.Help, m.Cancel}
}

func (m appShellKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.Scroll, m.Page, m.Jump},
		{m.Help, m.Cancel},
	}
}

var appKeys = appShellKeyMap{
	Scroll: key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "scroll"),
	),
	Page: key.NewBinding(
		key.WithKeys("pgup", "pgdown"),
		key.WithHelp("PgUp/PgDn", "page"),
	),
	Jump: key.NewBinding(
		key.WithKeys("home", "end"),
		key.WithHelp("Home/End", "jump"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("Ctrl+C", "cancel"),
	),
}

func newAppShellFrame(activity activitySnapshot, transcript []string, width, height int, elapsed string) appShellFrame {
	return appShellFrame{
		Header:           appHeaderLine(activity, elapsed),
		Panels:           appPanels(activity),
		Footer:           "↑/↓ scroll | PgUp/PgDn page | Home/End jump | ? help | Ctrl+C cancel",
		Width:            width,
		Height:           height,
		Transcript:       transcript,
		TranscriptStatus: "live tail",
	}
}

func appPanels(activity activitySnapshot) []appShellPanel {
	panels := []appShellPanel{
		{Title: "active", Lines: appActiveLines(activity)},
	}
	if attention := appAttentionLines(activity); len(attention) > 0 {
		panels = append(panels, appShellPanel{Title: "attention", Lines: attention})
	}
	panels = append(panels, appShellPanel{Title: "recent", Lines: appRecentLines(activity)})
	return panels
}

func (f appShellFrame) Lines() []string {
	lines := strings.Split(f.View(), "\n")
	return fitFrameHeight(lines, f.Height)
}

func (f appShellFrame) View() string {
	width := f.Width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	height := f.Height
	if height <= 0 {
		height = defaultAppShellHeight
	}
	header := appShellHeaderStyle.Width(width).Render(truncateStatusLine(f.Header, width))
	bodyHeight := appBodyHeight(height)
	body := appShellChromeStyle.Width(width).Height(bodyHeight).Render(f.bodyView(width, bodyHeight))
	footer := appShellFooterStyle.Width(width).Render(truncateStatusLine(f.Footer, width))
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (f appShellFrame) transcriptBudget() int {
	layout := f.layout()
	if layout.transcriptHeight <= 0 {
		return 0
	}
	frameSize := appShellPanelStyle.GetVerticalFrameSize()
	if frameSize >= layout.transcriptHeight {
		return 0
	}
	return layout.transcriptHeight - frameSize - 1
}

func appBodyHeight(height int) int {
	if height <= 2 {
		if height < 0 {
			return 0
		}
		return height
	}
	return height - 2
}

func (f appShellFrame) bodyView(width, height int) string {
	if height <= 0 {
		return ""
	}
	layout := f.layout()
	sidebar := appShellChromeStyle.Width(layout.sidebarWidth).Height(layout.sidebarHeight).Render(f.sidebarView(layout.sidebarWidth, layout.sidebarHeight))
	transcript := appShellChromeStyle.Width(layout.transcriptWidth).Height(layout.transcriptHeight).Render(f.transcriptView(layout.transcriptWidth, layout.transcriptHeight))
	if layout.useSidebar {
		return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", transcript)
	}
	if layout.sidebarHeight <= 0 {
		return transcript
	}
	return lipgloss.JoinVertical(lipgloss.Left, transcript, sidebar)
}

func (f appShellFrame) bodyUsesSidebar() bool {
	width := f.Width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	return width >= minAppSidebarWidth
}

func appSidebarWidth(width int) int {
	sidebar := width / 3
	if sidebar < 28 {
		sidebar = 28
	}
	if sidebar > 36 {
		sidebar = 36
	}
	return sidebar
}

func (f appShellFrame) layout() appShellLayout {
	width := f.Width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	height := appBodyHeight(f.Height)
	if height <= 0 {
		return appShellLayout{}
	}
	if f.bodyUsesSidebar() {
		sidebarWidth := appSidebarWidth(width)
		transcriptWidth := width - sidebarWidth - 1
		if transcriptWidth < 24 {
			transcriptWidth = 24
			sidebarWidth = max(18, width-transcriptWidth-1)
		}
		return appShellLayout{
			useSidebar:       true,
			sidebarWidth:     sidebarWidth,
			sidebarHeight:    height,
			transcriptWidth:  transcriptWidth,
			transcriptHeight: height,
		}
	}

	sidebarHeight := min(max(6, len(f.Panels)*4), max(0, height-5))
	transcriptHeight := height - sidebarHeight
	if sidebarHeight > 0 {
		transcriptHeight--
	}
	if transcriptHeight <= 0 {
		transcriptHeight = height
		sidebarHeight = 0
	}
	return appShellLayout{
		useSidebar:       false,
		sidebarWidth:     width,
		sidebarHeight:    sidebarHeight,
		transcriptWidth:  width,
		transcriptHeight: transcriptHeight,
	}
}

func (f appShellFrame) sidebarView(width, height int) string {
	if len(f.Panels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f.Panels))
	for _, panel := range f.Panels {
		parts = append(parts, appPanelBox(panel.Title, panel.Lines, width))
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (f appShellFrame) transcriptView(width, height int) string {
	panelStyle := appShellPanelStyle
	innerWidth := width - panelStyle.GetHorizontalFrameSize()
	innerHeight := height - panelStyle.GetVerticalFrameSize()
	if innerWidth < 1 {
		innerWidth = 1
	}
	if innerHeight < 1 {
		innerHeight = 1
	}
	vp := viewport.New(innerWidth, innerHeight)
	vp.SetContent(f.transcriptContent(innerWidth, innerHeight))
	return panelStyle.Render(vp.View())
}

func (f appShellFrame) transcriptContent(width, height int) string {
	if f.HelpVisible {
		return strings.Join(appHelpLines(width), "\n")
	}
	lines := []string{appShellHeaderStyle.Render(truncateStatusLine(f.transcriptHeading(), width))}
	lines = append(lines, newAppTranscriptViewport(f.Transcript, max(0, height-1), f.TranscriptOffset).Lines()...)
	return strings.Join(lines, "\n")
}

func (f appShellFrame) transcriptHeading() string {
	if f.HelpVisible {
		return "[help] press ? to return"
	}
	if f.TranscriptStatus == "" {
		return "[transcript]"
	}
	return "[transcript] " + f.TranscriptStatus
}

func appHeaderLine(activity activitySnapshot, elapsed string) string {
	parts := []string{"Memax Code", "phase=" + activity.Phase}
	if elapsed != "" {
		parts = append(parts, "elapsed="+elapsed)
	}
	if activity.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("tool_errors=%d", activity.ToolErrors))
	}
	if counts := activity.liveCountsLine(); counts != "" {
		parts = append(parts, counts)
	}
	if activity.Usage != "" {
		parts = append(parts, activity.Usage)
	}
	return strings.Join(parts, " | ")
}

func appActiveLines(activity activitySnapshot) []string {
	if activity.ActiveTool == "" && len(activity.ActiveCommands) == 0 {
		return []string{"idle"}
	}
	lines := make([]string, 0, maxAppActiveCommands+2)
	if activity.ActiveTool != "" {
		lines = append(lines, "tool: "+activity.ActiveTool)
	}
	limit := len(activity.ActiveCommands)
	if limit > maxAppActiveCommands {
		limit = maxAppActiveCommands
	}
	for _, command := range activity.ActiveCommands[:limit] {
		lines = append(lines, "command: "+command.summary())
	}
	if extra := len(activity.ActiveCommands) - limit; extra > 0 {
		lines = append(lines, fmt.Sprintf("... %d more active commands", extra))
	}
	return lines
}

func appAttentionLines(activity activitySnapshot) []string {
	var lines []string
	if activity.ToolErrors > 0 {
		lines = append(lines, fmt.Sprintf("tool errors: %d", activity.ToolErrors))
	}
	if appApprovalNeedsAttention(activity.LastApproval) {
		lines = append(lines, "approval: "+appApprovalAttentionSummary(activity.LastApproval))
	}
	if activity.TerminalError {
		lines = append(lines, "terminal error")
	}
	return lines
}

func appRecentLines(activity activitySnapshot) []string {
	var lines []string
	if activity.LastCommandState != "" {
		lines = append(lines, "command_status: "+activity.LastCommandState)
	} else if activity.LastCommand != "" {
		lines = append(lines, "command: "+activity.LastCommand)
	}
	if activity.LastPatch != "" {
		lines = append(lines, "patch: "+activity.LastPatch)
	}
	if activity.LastVerification != "" {
		lines = append(lines, "verification: "+activity.LastVerification)
	}
	if activity.LastApproval != "" && !appApprovalNeedsAttention(activity.LastApproval) {
		lines = append(lines, "approval: "+activity.LastApproval)
	}
	if len(lines) > maxAppRecentLines {
		return lines[len(lines)-maxAppRecentLines:]
	}
	return lines
}

func appApprovalNeedsAttention(approval string) bool {
	return approval == "requested" || strings.HasPrefix(approval, "requested:")
}

func appApprovalAttentionSummary(approval string) string {
	if approval == "requested" {
		return approval
	}
	return strings.TrimPrefix(approval, "requested:")
}

func appPanelBox(title string, lines []string, width int) string {
	panelStyle := appShellPanelStyle
	innerWidth := width - panelStyle.GetHorizontalFrameSize()
	if innerWidth < 1 {
		innerWidth = 1
	}
	content := make([]string, 0, len(lines)+1)
	content = append(content, appShellHeaderStyle.Render(truncateStatusLine("["+title+"]", innerWidth)))
	if len(lines) == 0 {
		content = append(content, "  none")
	} else {
		for _, line := range lines {
			content = append(content, truncateStatusLine("  "+line, innerWidth))
		}
	}
	return panelStyle.Render(strings.Join(content, "\n"))
}

type appTranscriptViewport struct {
	lines  []string
	height int
	offset int
}

func newAppTranscriptViewport(lines []string, height, offset int) appTranscriptViewport {
	if offset < 0 {
		offset = 0
	}
	return appTranscriptViewport{
		lines:  lines,
		height: height,
		offset: offset,
	}
}

func (v appTranscriptViewport) Lines() []string {
	if v.height <= 0 || len(v.lines) == 0 {
		return nil
	}

	offset := v.offset
	maxOffset := len(v.lines) - v.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}

	end := len(v.lines) - offset
	start := end - v.height
	if start < 0 {
		start = 0
	}

	hasOlder := start > 0
	hasNewer := end < len(v.lines)
	if v.height < 3 || (!hasOlder && !hasNewer) {
		return append([]string(nil), v.lines[start:end]...)
	}

	contentStart := start
	contentEnd := end
	visible := make([]string, 0, v.height)
	if hasOlder {
		contentStart++
		visible = append(visible, appHiddenLine("↑", contentStart, "earlier"))
	}
	if hasNewer {
		contentEnd--
	}
	if contentStart < contentEnd {
		visible = append(visible, v.lines[contentStart:contentEnd]...)
	}
	if hasNewer {
		visible = append(visible, appHiddenLine("↓", len(v.lines)-contentEnd, "newer"))
	}
	return visible
}

func appHiddenLine(prefix string, count int, label string) string {
	if count == 1 {
		return fmt.Sprintf("%s 1 %s line", prefix, label)
	}
	return fmt.Sprintf("%s %d %s lines", prefix, count, label)
}

func appHelpLines(width int) []string {
	helpView := help.New()
	helpView.Width = width
	helpView.ShowAll = true
	helpView.ShortSeparator = " | "
	helpView.FullSeparator = "    "
	return []string{
		appShellHeaderStyle.Render("[help] press ? to return"),
		helpView.View(appKeys),
		"",
		"Ctrl+C cancel the active run",
		"Home jumps to the oldest retained transcript line",
		"End returns to the live transcript tail",
	}
}
