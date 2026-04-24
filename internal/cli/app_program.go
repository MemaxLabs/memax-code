package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	appProgramMinComposer = 1
)

var (
	appProgramBrandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	appProgramTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	appProgramMutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	appProgramDimStyle      = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
	appProgramErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	appProgramAccentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	appProgramSuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	appProgramUserStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Padding(0, 1)
	appProgramToolStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	appProgramMarkdownStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	appProgramHeadingStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	appProgramCodeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("188")).Background(lipgloss.Color("236")).Padding(0, 1)
	appProgramQuoteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Italic(true)
	appProgramComposerStyle = lipgloss.NewStyle().Background(lipgloss.Color("235")).Padding(0, 1)
)

type appProgramKeyMap struct {
	Send    key.Binding
	Newline key.Binding
	Help    key.Binding
	Clear   key.Binding
	Quit    key.Binding
}

func (m appProgramKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{m.Send, m.Newline, m.Help, m.Quit}
}

func (m appProgramKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.Send, m.Newline, m.Clear},
		{m.Help, m.Quit},
	}
}

var appProgramKeys = appProgramKeyMap{
	Send: key.NewBinding(
		key.WithKeys("enter", "ctrl+s"),
		key.WithHelp("Enter/Ctrl+S", "send"),
	),
	Newline: key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter"),
		key.WithHelp("\\+Enter or Shift/Alt+Enter", "newline"),
	),
	Help: key.NewBinding(
		key.WithKeys("f1"),
		key.WithHelp("F1", "help"),
	),
	Clear: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("Esc", "clear"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("Ctrl+C", "quit"),
	),
}

type appProgramTranscriptMsg struct {
	text string
}

type appProgramPromptDoneMsg struct {
	sessionID string
	err       error
	prompt    string
}

type appProgramExternalStatusMsg struct {
	text string
}

type appProgramTickMsg time.Time

type appProgramModel struct {
	ctx        context.Context
	opts       options
	runPrompt  interactivePromptRunner
	program    *tea.Program
	history    persistentPromptHistory
	composer   interactiveComposer
	input      textarea.Model
	help       help.Model
	keys       appProgramKeyMap
	transcript appTranscriptTail
	width      int
	height     int
	sessionID  string
	statusLine string
	lastError  string
	running    bool
	showHelp   bool
	firstErr   error
	compactor  appProgramTranscriptCompactor
	pending    []string
	spinner    int
	tickArmed  bool
}

func newAppProgramModel(ctx context.Context, opts options, runPrompt interactivePromptRunner) *appProgramModel {
	input := textarea.New()
	input.Prompt = "› "
	input.Placeholder = "Ask Memax Code to inspect, change, or verify the repo"
	input.ShowLineNumbers = false
	input.SetHeight(appProgramMinComposer)
	input.FocusedStyle.Base = lipgloss.NewStyle()
	input.BlurredStyle.Base = lipgloss.NewStyle()
	input.FocusedStyle.Placeholder = appProgramMutedStyle
	input.FocusedStyle.Prompt = appProgramAccentStyle
	input.FocusedStyle.CursorLine = lipgloss.NewStyle()
	input.BlurredStyle.CursorLine = lipgloss.NewStyle()
	input.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "› "
		}
		return "· "
	})
	input.Focus()

	helpModel := help.New()
	helpModel.ShowAll = false

	model := &appProgramModel{
		ctx:        ctx,
		opts:       opts,
		runPrompt:  runPrompt,
		history:    newPersistentPromptHistory(opts.HistoryFile),
		input:      input,
		help:       helpModel,
		keys:       appProgramKeys,
		statusLine: "idle",
	}
	model.appendLocalTranscriptLine("dim", "Welcome. Type a task or /help.")
	if opts.ResumeSessionID != "" {
		model.sessionID = opts.ResumeSessionID
		model.appendLocalTranscriptLine("dim", "resumed session: "+opts.ResumeSessionID)
	}
	if entries, err := model.history.Load(); err != nil {
		model.appendLocalTranscriptLine("dim", "warning: "+err.Error())
	} else {
		model.composer.loadHistory(entries)
	}
	model.syncComposerView()
	return model
}

func (m *appProgramModel) Init() tea.Cmd {
	return tea.Sequence(m.flushPrints(), textarea.Blink)
}

func (m *appProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyMsg:
		if cmd, handled := m.updateKey(msg); handled {
			return m, m.withFlush(cmd)
		}
	case appProgramTranscriptMsg:
		m.appendTranscript(msg.text)
		return m, m.flushPrints()
	case appProgramPromptDoneMsg:
		m.finishPrompt(msg)
		return m, m.flushPrints()
	case appProgramExternalStatusMsg:
		if strings.TrimSpace(msg.text) != "" {
			m.statusLine = strings.TrimSpace(msg.text)
		}
	case appProgramTickMsg:
		m.tickArmed = false
		if !m.running {
			return m, nil
		}
		m.spinner++
		return m, m.tick()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncComposerDraftFromInput()
	return m, m.withFlush(cmd)
}

func (m *appProgramModel) updateKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		m.flushTranscriptPartial()
		m.appendLocalTranscriptLine("dim", "bye")
		return tea.Quit, true
	case "f1":
		m.showHelp = !m.showHelp
		return nil, true
	case "esc":
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.Reset()
			m.syncComposerDraftFromInput()
			return nil, true
		}
	case "alt+enter", "shift+enter":
		m.insertInputNewline()
		return nil, true
	case "enter", "ctrl+m", "ctrl+j":
		if m.consumeTrailingBackslashForNewline() {
			return nil, true
		}
		if m.composer.draftActive {
			m.insertInputNewline()
			return nil, true
		}
		return m.submitCurrentInput(), true
	case "ctrl+s":
		return m.submitCurrentInput(), true
	}
	return nil, false
}

func (m *appProgramModel) insertInputNewline() {
	m.input.InsertRune('\n')
	m.syncComposerDraftFromInput()
	m.resize()
}

func (m *appProgramModel) consumeTrailingBackslashForNewline() bool {
	value := m.input.Value()
	if !strings.HasSuffix(value, "\\") {
		return false
	}
	m.input.SetValue(strings.TrimSuffix(value, "\\") + "\n")
	m.syncComposerDraftFromInput()
	m.resize()
	return true
}

func (m *appProgramModel) submitCurrentInput() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if isInteractiveCommandLine(text) {
		return m.handleCommand(text)
	}
	if m.running {
		return nil
	}
	return m.startPrompt(text)
}

func (m *appProgramModel) handleCommand(text string) tea.Cmd {
	m.syncComposerDraftFromInput()
	var out bytes.Buffer
	currentSession := m.sessionID
	result := handleInteractiveCommand(m.ctx, &out, m.opts, &currentSession, &m.composer, text)
	m.sessionID = currentSession
	if out.Len() > 0 {
		m.appendTranscript(out.String())
	}
	if result.Done {
		return tea.Sequence(m.flushPrints(), tea.Quit)
	}
	if result.SubmitPrompt != "" {
		return m.startPrompt(result.SubmitPrompt)
	}
	m.syncComposerView()
	return nil
}

func (m *appProgramModel) startPrompt(prompt string) tea.Cmd {
	if m.runPrompt == nil || m.program == nil {
		return nil
	}
	m.running = true
	m.lastError = ""
	m.statusLine = "running"
	m.appendLocalTranscriptLine("user", "› "+strings.ReplaceAll(strings.TrimSpace(prompt), "\n", " "))
	m.input.Reset()
	m.composer.cancel()
	m.syncComposerView()

	send := m.program.Send
	runPrompt := m.runPrompt
	opts := m.opts
	opts.Prompt = prompt
	opts.ResumeSessionID = m.sessionID
	opts.UI = renderModeTUI

	runCmd := func() tea.Msg {
		writer := &appProgramTranscriptWriter{send: send}
		sessionID, err := runPrompt(m.ctx, writer, opts)
		return appProgramPromptDoneMsg{
			sessionID: sessionID,
			err:       err,
			prompt:    prompt,
		}
	}
	return tea.Batch(runCmd, m.tick())
}

func (m *appProgramModel) finishPrompt(msg appProgramPromptDoneMsg) {
	m.running = false
	if strings.TrimSpace(msg.sessionID) != "" {
		if m.sessionID != msg.sessionID {
			m.appendLocalTranscriptLine("dim", "session: "+msg.sessionID)
		}
		m.sessionID = msg.sessionID
	}
	if msg.err != nil {
		m.lastError = msg.err.Error()
		m.statusLine = "error"
		m.flushTranscriptPartial()
		m.appendLocalTranscriptLine("error", "error: "+msg.err.Error())
		if m.firstErr == nil {
			m.firstErr = msg.err
		}
		return
	}
	if m.composer.history.Record(msg.prompt) {
		if err := m.history.Append(msg.prompt); err != nil {
			m.appendLocalTranscriptLine("dim", "warning: "+err.Error())
		}
	}
	m.statusLine = "idle"
	m.flushTranscriptPartial()
}

func (m *appProgramModel) syncComposerDraftFromInput() {
	if !m.composer.draftActive {
		return
	}
	m.composer.buffer.SetText(m.input.Value())
	trimmed := strings.TrimSpace(m.composer.buffer.Text())
	m.composer.draftHasInput = trimmed != ""
}

func (m *appProgramModel) syncComposerView() {
	m.input.Prompt = m.composer.promptLabel()
	m.input.SetValue(m.composer.text())
	m.resize()
}

func (m *appProgramModel) appendTranscript(text string) {
	compacted := m.compactor.compact(text)
	if compacted == "" {
		return
	}
	m.queuePrints(m.transcript.append(compacted))
	m.resize()
}

func (m *appProgramModel) appendTranscriptLine(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.appendTranscript(text + "\n")
}

func (m *appProgramModel) appendLocalTranscriptLine(kind, text string) {
	kind = strings.TrimSpace(kind)
	text = strings.TrimSpace(normalizeAppTranscriptText(text))
	if kind == "" || text == "" {
		return
	}
	m.queuePrints(m.transcript.appendStandaloneLine(compactAppProgramLocalLine(kind, text)))
	m.resize()
}

func (m *appProgramModel) flushTranscriptPartial() {
	m.queuePrints(m.transcript.append(m.compactor.flush()))
	m.queuePrints(m.transcript.flushPartial())
}

func (m *appProgramModel) queuePrints(lines []string) {
	if len(lines) == 0 {
		return
	}
	m.pending = append(m.pending, lines...)
}

func (m *appProgramModel) flushPrints() tea.Cmd {
	lines := m.drainPendingPrints()
	if len(lines) == 0 {
		return nil
	}
	return tea.Println(strings.Join(lines, "\n"))
}

func (m *appProgramModel) drainPendingPrints() []string {
	if len(m.pending) == 0 {
		return nil
	}
	lines := append([]string(nil), m.pending...)
	m.pending = nil
	return lines
}

func (m *appProgramModel) withFlush(cmd tea.Cmd) tea.Cmd {
	flush := m.flushPrints()
	if flush == nil {
		return cmd
	}
	if cmd == nil {
		return flush
	}
	return tea.Sequence(flush, cmd)
}

func (m *appProgramModel) tick() tea.Cmd {
	if m.tickArmed {
		return nil
	}
	m.tickArmed = true
	return tea.Tick(appShellTickInterval, func(t time.Time) tea.Msg {
		return appProgramTickMsg(t)
	})
}

func (m *appProgramModel) resize() {
	width := m.width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	composerHeight := max(appProgramMinComposer, min(8, strings.Count(m.input.Value(), "\n")+1))
	if m.showHelp {
		composerHeight = max(composerHeight, 4)
	}
	m.input.SetWidth(max(12, width-2))
	m.input.SetHeight(composerHeight)
}

func (m *appProgramModel) View() string {
	width := m.width
	if width <= 0 {
		width = defaultAppShellWidth
	}

	status := m.statusView()
	composer := m.composerView(width)
	footer := appProgramDimStyle.Render(m.help.View(m.keys))
	return lipgloss.JoinVertical(lipgloss.Left, status, composer, footer)
}

func (m *appProgramModel) phaseLabel() string {
	if m.lastError != "" {
		return appProgramErrorStyle.Render("error")
	}
	if m.running {
		return appProgramAccentStyle.Render("working")
	}
	return appProgramSuccessStyle.Render(m.statusLine)
}

func (m *appProgramModel) statusView() string {
	parts := []string{
		appProgramBrandStyle.Render("Memax Code"),
		m.phaseLabel(),
		appProgramTitleStyle.Render("session") + " " + nonEmptyOr(shortSessionID(m.sessionID), "none"),
		appProgramTitleStyle.Render("workspace") + " " + filepath.Base(m.opts.CWD),
	}
	if m.opts.Model != "" {
		parts = append(parts, m.opts.Model)
	}
	if m.opts.Effort != "" && m.opts.Effort != "auto" {
		parts = append(parts, "effort "+m.opts.Effort)
	}
	if m.running {
		frame := liveStatusFrames[m.spinner%len(liveStatusFrames)]
		parts = append(parts, appProgramAccentStyle.Render(frame+" thinking"))
	} else {
		parts = append(parts, appProgramTitleStyle.Render("input")+" "+m.composer.statusLine())
	}
	if m.lastError != "" {
		parts = append(parts, appProgramErrorStyle.Render("error "+m.lastError))
	}
	lines := []string{strings.Join(parts, appProgramDimStyle.Render("  ·  "))}
	if m.showHelp {
		lines = append(lines, appProgramMutedStyle.Render("/help /status /session /pick /show /sessions /resume /new /draft /submit /cancel /quit"))
	}
	return strings.Join(lines, "\n")
}

func (m *appProgramModel) composerView(width int) string {
	return appProgramComposerStyle.Width(width).Render(m.input.View())
}

func compactAppProgramTranscriptText(text string) string {
	var compactor appProgramTranscriptCompactor
	return strings.ReplaceAll(compactor.compact(text)+compactor.flush(), appTranscriptBlankLine, "")
}

type appProgramTranscriptCompactor struct {
	section              string
	skipActivityDetail   bool
	assistantInCodeBlock bool
	activityDetail       *appProgramActivityDetail
}

type appProgramActivityDetail struct {
	label string
	style lipgloss.Style
	lines []string
}

func (d *appProgramActivityDetail) append(line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	d.lines = append(d.lines, line)
	const maxActivityDetailTailLines = 5
	if len(d.lines) > maxActivityDetailTailLines {
		d.lines = append([]string(nil), d.lines[len(d.lines)-maxActivityDetailTailLines:]...)
	}
}

func (d *appProgramActivityDetail) render() []string {
	if d == nil || len(d.lines) == 0 {
		return nil
	}
	if len(d.lines) == 1 {
		return []string{d.style.Render("  " + d.label + ": " + d.lines[0])}
	}
	out := []string{d.style.Render(fmt.Sprintf("  %s tail:", d.label))}
	for _, line := range d.lines {
		out = append(out, d.style.Render("    "+line))
	}
	return out
}

func (c *appProgramTranscriptCompactor) compact(text string) string {
	text = normalizeAppTranscriptText(text)
	if strings.TrimSpace(text) == "" && (c.section != "assistant" || !strings.Contains(text, "\n")) {
		return ""
	}
	trailingNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if trailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	out := make([]string, 0, len(lines))
	leadingAssistantBoundary := c.section == "assistant" && strings.HasPrefix(text, "\n")
	for i, line := range lines {
		for _, compacted := range c.compactLine(line) {
			if leadingAssistantBoundary && i == 0 && compacted == appTranscriptBlankLine {
				out = append(out, "")
				continue
			}
			if compacted != "" {
				out = append(out, compacted)
			}
		}
	}
	text = strings.Join(out, "\n")
	if text == "" && leadingAssistantBoundary && trailingNewline {
		return "\n"
	}
	if text != "" && trailingNewline {
		text += "\n"
	}
	return text
}

func (c *appProgramTranscriptCompactor) compactLine(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		if c.activityDetail != nil {
			c.activityDetail.append("")
			return nil
		}
		if c.section == "assistant" {
			return []string{appTranscriptBlankLine}
		}
		return []string{line}
	}
	if section, label, ok := compactAppProgramSectionLabel(trimmed); ok {
		out := c.flushActivityDetail()
		c.section = section
		c.skipActivityDetail = false
		c.assistantInCodeBlock = false
		if label != "" {
			out = append(out, label)
		}
		return out
	}
	switch c.section {
	case "assistant":
		return []string{c.compactAssistantLine(line)}
	case "activity":
		return c.compactActivityLine(trimmed)
	case "result", "session", "usage", "status":
		return nil
	case "error":
		return []string{compactAppProgramErrorLine(trimmed)}
	default:
		return []string{line}
	}
}

func compactAppProgramLocalLine(kind, text string) string {
	switch kind {
	case "user":
		return appProgramUserStyle.Render(text)
	case "error":
		return appProgramErrorStyle.Render("! " + text)
	default:
		return appProgramDimStyle.Render(text)
	}
}

func compactAppProgramSectionLabel(trimmed string) (section, label string, ok bool) {
	switch trimmed {
	case "[assistant]":
		return "assistant", "", true
	case "[activity]":
		return "activity", "", true
	case "[result]":
		return "result", "", true
	case "[session]":
		return "session", "", true
	case "[usage]":
		return "usage", "", true
	case "[status]":
		return "status", "", true
	case "[error]":
		return "error", "", true
	default:
		return "", "", false
	}
}

func (c *appProgramTranscriptCompactor) compactAssistantLine(line string) string {
	trimmedRight := strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(trimmedRight)
	if trimmed == "" {
		return appTranscriptBlankLine
	}
	if strings.HasPrefix(trimmed, "```") {
		c.assistantInCodeBlock = !c.assistantInCodeBlock
		return appProgramCodeStyle.Render(trimmed)
	}
	if c.assistantInCodeBlock {
		return appProgramCodeStyle.Render(strings.TrimRight(trimmedRight, "\t "))
	}
	if heading, ok := appMarkdownHeading(trimmed); ok {
		return appProgramHeadingStyle.Render(heading)
	}
	if strings.HasPrefix(trimmed, ">") && !strings.HasPrefix(trimmed, "> tool ") {
		return appProgramQuoteStyle.Render("│ " + strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
	}
	if indent, bullet, rest, ok := appMarkdownBulletLine(trimmedRight); ok {
		return appProgramMarkdownStyle.Render(indent + bullet + " " + rest)
	}
	if strings.HasPrefix(trimmedRight, "    ") || strings.HasPrefix(trimmedRight, "\t") {
		return appProgramCodeStyle.Render(strings.TrimRight(trimmedRight, "\t "))
	}
	return appProgramMarkdownStyle.Render(trimmedRight)
}

func appMarkdownHeading(line string) (heading string, ok bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes == len(line) || line[hashes] != ' ' {
		return "", false
	}
	if hashes > 6 {
		return "", false
	}
	heading = strings.TrimSpace(line[hashes+1:])
	if heading == "" {
		return "", false
	}
	return heading, true
}

func appMarkdownBulletLine(line string) (indent, bullet, rest string, ok bool) {
	indent, content := appMarkdownIndentPrefix(line)
	bullet, rest, ok = appMarkdownBullet(content)
	if !ok {
		return "", "", "", false
	}
	return indent, bullet, rest, true
}

func appMarkdownIndentPrefix(line string) (indent, content string) {
	width := 0
	i := 0
	for i < len(line) {
		switch line[i] {
		case ' ':
			width++
			i++
		case '\t':
			width += 2
			i++
		default:
			if width > 6 {
				width = 6
			}
			return strings.Repeat(" ", width), line[i:]
		}
	}
	if width > 6 {
		width = 6
	}
	return strings.Repeat(" ", width), ""
}

func appMarkdownBullet(line string) (bullet, rest string, ok bool) {
	if len(line) < 3 {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
		return "•", strings.TrimSpace(line[2:]), true
	case strings.HasPrefix(line, "+ "):
		return "+", strings.TrimSpace(line[2:]), true
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			if r == '.' && i > 0 && i+1 < len(line) && line[i+1] == ' ' {
				return line[:i+1], strings.TrimSpace(line[i+2:]), true
			}
			return "", "", false
		}
	}
	return "", "", false
}

func (c *appProgramTranscriptCompactor) compactActivityLine(trimmed string) []string {
	if strings.HasPrefix(trimmed, "memax> ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramUserStyle.Render("› "+strings.TrimSpace(strings.TrimPrefix(trimmed, "memax> "))))
	}
	if strings.HasPrefix(trimmed, "> tool ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramToolStyle.Render("• "+strings.TrimSpace(strings.TrimPrefix(trimmed, "> "))))
	}
	if strings.HasPrefix(trimmed, "< tool ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramDimStyle.Render("  "+strings.TrimSpace(strings.TrimPrefix(trimmed, "< "))))
	}
	if strings.HasPrefix(trimmed, "! tool ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramErrorStyle.Render("! "+strings.TrimSpace(strings.TrimPrefix(trimmed, "! "))))
	}
	if strings.HasPrefix(trimmed, "$ command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramToolStyle.Render("• "+strings.TrimSpace(strings.TrimPrefix(trimmed, "$ "))))
	}
	if strings.HasPrefix(trimmed, "+ command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramSuccessStyle.Render("✓ "+strings.TrimSpace(strings.TrimPrefix(trimmed, "+ "))))
	}
	if strings.HasPrefix(trimmed, "! command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramErrorStyle.Render("! "+strings.TrimSpace(strings.TrimPrefix(trimmed, "! "))))
	}
	if strings.HasPrefix(trimmed, "+ check ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramSuccessStyle.Render("✓ "+strings.TrimSpace(strings.TrimPrefix(trimmed, "+ "))))
	}
	if strings.HasPrefix(trimmed, "! check ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramErrorStyle.Render("! "+strings.TrimSpace(strings.TrimPrefix(trimmed, "! "))))
	}
	if strings.HasPrefix(trimmed, "? approval ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramAccentStyle.Render("? "+strings.TrimSpace(strings.TrimPrefix(trimmed, "? "))))
	}
	if strings.HasPrefix(trimmed, "+ approval ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramSuccessStyle.Render("✓ "+strings.TrimSpace(strings.TrimPrefix(trimmed, "+ "))))
	}
	if strings.HasPrefix(trimmed, "! approval ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramErrorStyle.Render("! "+strings.TrimSpace(strings.TrimPrefix(trimmed, "! "))))
	}
	if strings.HasPrefix(trimmed, "result:") {
		c.activityDetail = nil
		c.skipActivityDetail = true
		return nil
	}
	if strings.HasPrefix(trimmed, "error:") {
		c.skipActivityDetail = true
		c.activityDetail = &appProgramActivityDetail{label: "error", style: appProgramErrorStyle}
		c.activityDetail.append(strings.TrimSpace(strings.TrimPrefix(trimmed, "error:")))
		return nil
	}
	if c.activityDetail != nil {
		c.activityDetail.append(trimmed)
		return nil
	}
	if c.skipActivityDetail {
		return nil
	}
	return []string{appProgramDimStyle.Render(trimmed)}
}

func (c *appProgramTranscriptCompactor) flushActivityDetail() []string {
	if c.activityDetail == nil {
		return nil
	}
	out := c.activityDetail.render()
	c.activityDetail = nil
	return out
}

func (c *appProgramTranscriptCompactor) flush() string {
	out := c.flushActivityDetail()
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func compactAppProgramErrorLine(trimmed string) string {
	return appProgramErrorStyle.Render("! " + trimmed)
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 13 {
		return id
	}
	return id[:8] + "..." + id[len(id)-4:]
}

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type appProgramTranscriptWriter struct {
	send func(tea.Msg)
}

func (w *appProgramTranscriptWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.send != nil {
		w.send(appProgramTranscriptMsg{text: string(p)})
	}
	return len(p), nil
}

func runInteractiveApp(ctx context.Context, stdin io.Reader, stdout io.Writer, opts options, runPrompt interactivePromptRunner) error {
	model := newAppProgramModel(ctx, opts, runPrompt)
	if lines := model.drainPendingPrints(); len(lines) > 0 {
		fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	}
	programOpts := []tea.ProgramOption{
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithContext(ctx),
	}
	program := tea.NewProgram(model, programOpts...)
	model.program = program
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	if result, ok := finalModel.(*appProgramModel); ok && result.firstErr != nil {
		return result.firstErr
	}
	return nil
}
