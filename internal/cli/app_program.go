package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	appProgramMinComposer = 3
)

var (
	appProgramBrandStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	appProgramTitleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	appProgramMutedStyle      = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
	appProgramErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	appProgramAccentStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	appProgramSidebarKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("249")).Bold(true)
	appProgramRuleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	appProgramComposerStyle   = lipgloss.NewStyle().Padding(0, 1)
)

type appProgramKeyMap struct {
	Send    key.Binding
	Newline key.Binding
	Scroll  key.Binding
	Page    key.Binding
	Jump    key.Binding
	Help    key.Binding
	Clear   key.Binding
	Quit    key.Binding
}

func (m appProgramKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{m.Send, m.Newline, m.Page, m.Help, m.Quit}
}

func (m appProgramKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.Send, m.Newline, m.Clear},
		{m.Scroll, m.Page, m.Jump},
		{m.Help, m.Quit},
	}
}

var appProgramKeys = appProgramKeyMap{
	Send: key.NewBinding(
		key.WithKeys("enter", "ctrl+s"),
		key.WithHelp("Enter/Ctrl+S", "send"),
	),
	Newline: key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("Ctrl+J", "newline"),
	),
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

type appProgramModel struct {
	ctx        context.Context
	opts       options
	runPrompt  interactivePromptRunner
	program    *tea.Program
	history    persistentPromptHistory
	composer   interactiveComposer
	viewport   viewport.Model
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
	input.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "› "
		}
		return "· "
	})
	input.Focus()

	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{}
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 2

	helpModel := help.New()
	helpModel.ShowAll = false

	model := &appProgramModel{
		ctx:        ctx,
		opts:       opts,
		runPrompt:  runPrompt,
		history:    newPersistentPromptHistory(opts.HistoryFile),
		viewport:   vp,
		input:      input,
		help:       helpModel,
		keys:       appProgramKeys,
		statusLine: "idle",
	}
	model.appendTranscriptLine("Memax Code interactive shell")
	model.appendTranscriptLine("Type /help for commands, /quit to exit.")
	if opts.ResumeSessionID != "" {
		model.sessionID = opts.ResumeSessionID
		model.appendTranscriptLine("resumed session: " + opts.ResumeSessionID)
	}
	if entries, err := model.history.Load(); err != nil {
		model.appendTranscriptLine("warning: " + err.Error())
	} else {
		model.composer.loadHistory(entries)
	}
	model.syncComposerView()
	return model
}

func (m *appProgramModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m *appProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyMsg:
		if cmd, handled := m.updateKey(msg); handled {
			return m, cmd
		}
	case appProgramTranscriptMsg:
		m.appendTranscript(msg.text)
	case appProgramPromptDoneMsg:
		m.finishPrompt(msg)
	case appProgramExternalStatusMsg:
		if strings.TrimSpace(msg.text) != "" {
			m.statusLine = strings.TrimSpace(msg.text)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncComposerDraftFromInput()
	return m, cmd
}

func (m *appProgramModel) updateKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		m.appendTranscriptLine("bye")
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
	case "pgup":
		m.viewport.PageUp()
		return nil, true
	case "pgdown":
		m.viewport.PageDown()
		return nil, true
	case "home":
		m.viewport.GotoTop()
		return nil, true
	case "end":
		m.viewport.GotoBottom()
		return nil, true
	case "up":
		m.viewport.LineUp(1)
		return nil, true
	case "down":
		m.viewport.LineDown(1)
		return nil, true
	case "ctrl+j":
		if m.composer.draftActive {
			m.input.InsertRune('\n')
			m.syncComposerDraftFromInput()
			return nil, true
		}
		return m.submitCurrentInput(), true
	case "enter":
		if m.composer.draftActive {
			m.input, _ = m.input.Update(msg)
			m.syncComposerDraftFromInput()
			return nil, true
		}
		return m.submitCurrentInput(), true
	case "ctrl+s":
		return m.submitCurrentInput(), true
	}
	return nil, false
}

func (m *appProgramModel) submitCurrentInput() tea.Cmd {
	if m.running {
		return nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if isInteractiveCommandLine(text) {
		return m.handleCommand(text)
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
		return tea.Quit
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
	m.appendTranscriptLine("memax> " + strings.ReplaceAll(strings.TrimSpace(prompt), "\n", " "))
	m.input.Reset()
	m.composer.cancel()
	m.syncComposerView()

	send := m.program.Send
	runPrompt := m.runPrompt
	opts := m.opts
	opts.Prompt = prompt
	opts.ResumeSessionID = m.sessionID
	opts.UI = renderModeTUI

	return func() tea.Msg {
		writer := &appProgramTranscriptWriter{send: send}
		sessionID, err := runPrompt(m.ctx, writer, opts)
		return appProgramPromptDoneMsg{
			sessionID: sessionID,
			err:       err,
			prompt:    prompt,
		}
	}
}

func (m *appProgramModel) finishPrompt(msg appProgramPromptDoneMsg) {
	m.running = false
	if strings.TrimSpace(msg.sessionID) != "" {
		m.sessionID = msg.sessionID
	}
	if msg.err != nil {
		m.lastError = msg.err.Error()
		m.statusLine = "error"
		m.appendTranscriptLine("error: " + msg.err.Error())
		if m.firstErr == nil {
			m.firstErr = msg.err
		}
		return
	}
	if m.composer.history.Record(msg.prompt) {
		if err := m.history.Append(msg.prompt); err != nil {
			m.appendTranscriptLine("warning: " + err.Error())
		}
	}
	m.statusLine = "idle"
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
	if strings.TrimSpace(text) == "" {
		return
	}
	atBottom := m.viewport.AtBottom()
	m.transcript.append(normalizeAppTranscriptText(text))
	m.refreshViewport(atBottom)
}

func (m *appProgramModel) appendTranscriptLine(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.appendTranscript(text + "\n")
}

func (m *appProgramModel) refreshViewport(stickBottom bool) {
	lines := m.transcript.lines(maxAppTranscriptLines)
	m.viewport.SetContent(strings.Join(lines, "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m *appProgramModel) resize() {
	width := m.width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	height := m.height
	if height <= 0 {
		height = defaultAppShellHeight
	}
	composerHeight := max(appProgramMinComposer, min(8, max(3, strings.Count(m.input.Value(), "\n")+2)))
	if m.showHelp {
		composerHeight = max(composerHeight, 5)
	}
	bodyHeight := max(8, height-composerHeight-7)
	m.viewport.Width = width
	m.viewport.Height = bodyHeight
	if m.viewport.Width < 1 {
		m.viewport.Width = 1
	}
	if m.viewport.Height < 1 {
		m.viewport.Height = 1
	}
	m.input.SetWidth(max(12, width-2))
	m.input.SetHeight(composerHeight)
	m.refreshViewport(false)
}

func (m *appProgramModel) View() string {
	width := m.width
	if width <= 0 {
		width = defaultAppShellWidth
	}

	header := appProgramBrandStyle.Render("Memax Code") + appProgramMutedStyle.Render("  ") + m.headerStatus()
	body := m.bodyView(width)
	status := m.statusView()
	composer := m.composerView(width)
	footer := appProgramMutedStyle.Render(m.help.View(m.keys))
	return lipgloss.JoinVertical(lipgloss.Left, header, appProgramRule(width), body, appProgramRule(width), status, composer, appProgramRule(width), footer)
}

func (m *appProgramModel) headerStatus() string {
	parts := []string{m.statusLine}
	if m.sessionID != "" {
		parts = append(parts, "session="+m.sessionID)
	}
	if m.opts.Model != "" {
		parts = append(parts, "model="+m.opts.Model)
	}
	if base := filepath.Base(m.opts.CWD); base != "" && base != "." && base != string(filepath.Separator) {
		parts = append(parts, "dir="+base)
	}
	return strings.Join(parts, " | ")
}

func (m *appProgramModel) bodyView(width int) string {
	return lipgloss.NewStyle().Width(width).Render(
		appProgramTitleStyle.Render("Conversation") + "\n" +
			appProgramMutedStyle.Render(m.transcriptStatusLine()) + "\n" +
			m.viewport.View())
}

func (m *appProgramModel) statusView() string {
	parts := []string{
		appProgramSidebarKeyStyle.Render("Session") + " " + nonEmptyOr(m.sessionID, "none"),
		appProgramSidebarKeyStyle.Render("Workspace") + " " + filepath.Base(m.opts.CWD),
		appProgramSidebarKeyStyle.Render("Composer") + " " + m.composer.statusLine(),
	}
	if m.lastError != "" {
		parts = append(parts, appProgramErrorStyle.Render("Error "+m.lastError))
	}
	lines := []string{strings.Join(parts, appProgramMutedStyle.Render("  •  "))}
	if m.showHelp {
		lines = append(lines, appProgramMutedStyle.Render("/help /status /session /pick /show /sessions /resume /new /draft /submit /cancel /quit"))
	}
	return strings.Join(lines, "\n")
}

func (m *appProgramModel) composerView(width int) string {
	title := "Ready"
	if m.running {
		title = "Running"
	}
	head := appProgramTitleStyle.Render(title)
	if m.composer.draftActive {
		head += appProgramMutedStyle.Render("  draft mode")
	}
	return appProgramComposerStyle.Width(width).Render(head + "\n" + m.input.View())
}

func (m *appProgramModel) transcriptStatusLine() string {
	lines := m.transcript.lines(maxAppTranscriptLines)
	if len(lines) == 0 {
		return "idle"
	}
	if m.viewport.AtBottom() {
		return "live tail · " + strconv.Itoa(len(lines)) + " lines"
	}
	return "scrollback · " + strconv.Itoa(len(lines)) + " lines"
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

func appProgramRule(width int) string {
	if width <= 0 {
		width = defaultAppShellWidth
	}
	return appProgramRuleStyle.Render(strings.Repeat("─", max(8, width)))
}
