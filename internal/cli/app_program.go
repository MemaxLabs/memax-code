package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

const (
	appProgramMinComposer = 1
	appProgramStatusInset = 2
	appProgramBottomInset = 1
)

var (
	appProgramComposerBackground = lipgloss.Color("235")
	appProgramBrandStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	appProgramMutedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	appProgramDimStyle           = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
	appProgramErrorStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	appProgramAccentStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	appProgramSuccessStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	appProgramUserStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Padding(0, 1)
	appProgramToolStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	appProgramMarkdownStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	appProgramStrongStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	appProgramHeadingStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	appProgramCodeStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("188")).Background(lipgloss.Color("236")).Padding(0, 1)
	appProgramInlineCodeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("188"))
	appProgramQuoteStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Italic(true)
	appProgramComposerStyle      = lipgloss.NewStyle().Background(appProgramComposerBackground).Padding(1, 1)
	appProgramStatusMetaStyle    = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("242"))
)

type appProgramTranscriptMsg struct {
	text string
}

type appProgramEventMsg struct {
	event memaxagent.Event
}

type appProgramPromptDoneMsg struct {
	sessionID string
	err       error
	prompt    string
}

type appProgramTickMsg time.Time

type appProgramModel struct {
	ctx       context.Context
	opts      options
	runPrompt interactivePromptRunner
	runEvents interactiveEventPromptRunner
	program   *tea.Program
	plainOut  io.Writer
	history   persistentPromptHistory
	composer  interactiveComposer
	input     textarea.Model
	runCancel context.CancelFunc
	appTranscriptFormatter
	width         int
	height        int
	sessionID     string
	statusLine    string
	lastError     string
	runErrors     map[string]struct{}
	running       bool
	canceling     bool
	quitting      bool
	showHelp      bool
	firstErr      error
	spinner       int
	tickArmed     bool
	turnStartedAt time.Time
}

func newAppProgramModel(ctx context.Context, opts options, runPrompt interactivePromptRunner) *appProgramModel {
	return newAppProgramModelWithEvents(ctx, opts, runPrompt, nil)
}

func newAppProgramModelWithEvents(ctx context.Context, opts options, runPrompt interactivePromptRunner, runEvents interactiveEventPromptRunner) *appProgramModel {
	model := &appProgramModel{
		ctx:        ctx,
		opts:       opts,
		runPrompt:  runPrompt,
		runEvents:  runEvents,
		history:    newPersistentPromptHistory(opts.HistoryFile),
		input:      newAppProgramTextarea(),
		statusLine: "idle",
	}
	model.appTranscriptFormatter.liveCommandGroups = true
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

func newAppProgramTextarea() textarea.Model {
	input := textarea.New()
	input.Prompt = "› "
	input.Placeholder = "Ask Memax Code to inspect, change, or verify the repo"
	input.ShowLineNumbers = false
	input.SetHeight(appProgramMinComposer)
	input.FocusedStyle.Base = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.BlurredStyle.Base = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.FocusedStyle.Placeholder = appProgramMutedStyle.Background(appProgramComposerBackground)
	input.BlurredStyle.Placeholder = appProgramMutedStyle.Background(appProgramComposerBackground)
	input.FocusedStyle.Prompt = appProgramAccentStyle.Background(appProgramComposerBackground)
	input.BlurredStyle.Prompt = appProgramAccentStyle.Background(appProgramComposerBackground)
	input.FocusedStyle.Text = appProgramMarkdownStyle.Background(appProgramComposerBackground)
	input.BlurredStyle.Text = appProgramMarkdownStyle.Background(appProgramComposerBackground)
	input.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.BlurredStyle.CursorLine = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.BlurredStyle.EndOfBuffer = lipgloss.NewStyle().Background(appProgramComposerBackground)
	input.Cursor.Style = input.Cursor.Style.Background(appProgramComposerBackground)
	input.Cursor.TextStyle = input.Cursor.TextStyle.Background(appProgramComposerBackground)
	input.Cursor.SetMode(cursor.CursorStatic)
	input.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "› "
		}
		return "· "
	})
	input.Focus()
	return input
}

func (m *appProgramModel) Init() tea.Cmd {
	return m.withRender(nil)
}

func (m *appProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case tea.KeyMsg:
		if cmd, handled := m.updateKey(msg); handled {
			return m, m.withRender(cmd)
		}
	case appProgramTranscriptMsg:
		m.appendTranscript(msg.text)
		return m, m.withRender(nil)
	case appProgramEventMsg:
		m.appendEvent(msg.event)
		return m, m.withRender(nil)
	case appProgramPromptDoneMsg:
		m.finishPrompt(msg)
		return m, m.withRender(nil)
	case appProgramTickMsg:
		m.tickArmed = false
		if !m.running {
			return m, nil
		}
		m.spinner++
		return m, m.withRender(m.tick())
	}

	beforeInput := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncComposerDraftFromInput()
	if m.input.Value() != beforeInput {
		m.composer.history.ResetTraversal()
		m.resize()
	}
	return m, m.withRender(cmd)
}

func (m *appProgramModel) updateKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		if m.running {
			if m.canceling {
				if m.runCancel != nil {
					m.runCancel()
				}
				m.flushTranscriptPartial()
				m.appendLocalTranscriptLine("dim", "force quit")
				m.quitting = true
				return tea.Quit, true
			}
			if m.runCancel != nil {
				m.runCancel()
			}
			m.lastError = ""
			m.canceling = true
			m.statusLine = "canceling"
			m.appendLocalTranscriptLine("dim", "canceling current run")
			return nil, true
		}
		m.flushTranscriptPartial()
		m.appendLocalTranscriptLine("dim", "bye")
		m.quitting = true
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
	case "up":
		if m.inputCursorAtTop() {
			return m.recallPreviousPrompt(), true
		}
	case "down":
		if m.inputCursorAtBottom() {
			return m.recallNextPrompt(), true
		}
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
		m.quitting = true
		return tea.Sequence(m.flushPrints(), tea.Quit)
	}
	if result.SubmitPrompt != "" {
		return m.startPrompt(result.SubmitPrompt)
	}
	m.syncComposerView()
	return nil
}

func (m *appProgramModel) startPrompt(prompt string) tea.Cmd {
	if m.running {
		m.appendLocalTranscriptLine("dim", "run already active; press Ctrl+C to cancel")
		return nil
	}
	if (m.runPrompt == nil && m.runEvents == nil) || m.program == nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(m.ctx)
	m.running = true
	m.canceling = false
	m.runCancel = cancel
	m.lastError = ""
	m.runErrors = nil
	m.statusLine = "running"
	m.turnStartedAt = time.Now()
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
		var sessionID string
		var err error
		if m.runEvents != nil {
			sessionID, err = m.runEvents(runCtx, opts, func(event memaxagent.Event) {
				send(appProgramEventMsg{event: event})
			})
		} else {
			writer := &appProgramTranscriptWriter{send: send}
			sessionID, err = runPrompt(runCtx, writer, opts)
		}
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
	m.canceling = false
	m.turnStartedAt = time.Time{}
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
	if strings.TrimSpace(msg.sessionID) != "" {
		if m.sessionID != msg.sessionID {
			m.appendLocalTranscriptLine("dim", "session: "+msg.sessionID)
		}
		m.sessionID = msg.sessionID
	}
	m.flushPendingCommandGroups()
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, contextCanceled) {
			m.lastError = ""
			m.statusLine = "idle"
			m.flushTranscriptPartial()
			m.appendLocalTranscriptLine("dim", "canceled")
			return
		}
		errText := msg.err.Error()
		m.lastError = errText
		m.statusLine = "error"
		m.flushTranscriptPartial()
		if !m.hasRunError(errText) {
			m.recordRunError(errText)
			m.appendLocalTranscriptLine("error", "error: "+errText)
		}
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

func (m *appProgramModel) appendEvent(event memaxagent.Event) {
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		if event.SessionID != "" {
			m.sessionID = event.SessionID
		}
	case memaxagent.EventError:
		if event.Err != nil {
			if errText := event.Err.Error(); errText != "" {
				if m.hasRunError(errText) {
					return
				}
				m.recordRunError(errText)
			}
		}
		m.appTranscriptFormatter.appendEvent(event)
	default:
		m.appTranscriptFormatter.appendEvent(event)
	}
}

func (m *appProgramModel) hasRunError(errText string) bool {
	if errText == "" || m.runErrors == nil {
		return false
	}
	_, ok := m.runErrors[errText]
	return ok
}

func (m *appProgramModel) recordRunError(errText string) {
	if errText == "" {
		return
	}
	if m.runErrors == nil {
		m.runErrors = make(map[string]struct{}, 1)
	}
	m.runErrors[errText] = struct{}{}
}

func (m *appProgramModel) inputCursorAtTop() bool {
	if m.input.Line() > 0 {
		return false
	}
	info := m.input.LineInfo()
	return info.RowOffset <= 0
}

func (m *appProgramModel) inputCursorAtBottom() bool {
	if m.input.Line() < m.input.LineCount()-1 {
		return false
	}
	info := m.input.LineInfo()
	return info.RowOffset >= max(0, info.Height-1)
}

func (m *appProgramModel) recallPreviousPrompt() tea.Cmd {
	text, ok := m.composer.history.Previous(m.input.Value())
	if !ok {
		return nil
	}
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.syncComposerDraftFromInput()
	m.resize()
	return nil
}

func (m *appProgramModel) recallNextPrompt() tea.Cmd {
	text, ok := m.composer.history.Next()
	if !ok {
		return nil
	}
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.syncComposerDraftFromInput()
	m.resize()
	return nil
}

func (m *appProgramModel) flushPrints() tea.Cmd {
	lines := m.drainPendingPrints()
	if len(lines) == 0 {
		return nil
	}
	if m.width > 0 {
		lines = appProgramFitPrintedLines(lines, appProgramLiveRegionWidth(m.width))
	}
	if m.plainOut != nil {
		for _, line := range lines {
			fmt.Fprintln(m.plainOut, xansi.Strip(line))
		}
		return nil
	}
	return tea.Println(strings.Join(lines, "\n"))
}

func (m *appProgramModel) withRender(cmd tea.Cmd) tea.Cmd {
	return m.withFlush(cmd)
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
	renderWidth := appProgramLiveRegionWidth(width)
	m.compactor.width = renderWidth
	composerHeight := max(appProgramMinComposer, min(8, strings.Count(m.input.Value(), "\n")+1))
	m.input.SetWidth(appProgramComposerContentWidth(renderWidth))
	m.input.SetHeight(composerHeight)
}

func (m *appProgramModel) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	renderWidth := appProgramLiveRegionWidth(width)

	rows := make([]string, 0, 8)
	active := m.activeRuntimeActivityView(renderWidth)
	activity := m.activityStatusView(renderWidth)
	if active != "" {
		rows = appendAppProgramBlankRows(rows, appProgramBottomInset)
		rows = append(rows, active)
	}
	if activity != "" {
		rows = appendAppProgramBlankRows(rows, appProgramBottomInset)
		rows = append(rows, activity)
	}
	rows = appendAppProgramBlankRows(rows, appProgramBottomInset)
	rows = append(rows, m.composerView(renderWidth))
	rows = append(rows, m.bottomStatusView(renderWidth))
	if m.showHelp {
		rows = append(rows, m.helpView(renderWidth))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func appendAppProgramBlankRows(rows []string, count int) []string {
	for range count {
		rows = append(rows, "")
	}
	return rows
}

func (m *appProgramModel) activityStatusView(width int) string {
	var line string
	if m.running {
		label := "thinking"
		if m.canceling {
			label = "canceling"
		}
		frame := liveStatusFrames[m.spinner%len(liveStatusFrames)]
		if elapsed := m.turnElapsed(); elapsed != "" {
			line = appProgramAccentStyle.Render(frame+" "+label) + appProgramDimStyle.Render(" · "+elapsed)
		} else {
			line = appProgramAccentStyle.Render(frame + " " + label)
		}
		return appProgramFitLine(line, width)
	}
	if m.lastError != "" {
		line = appProgramErrorStyle.Render("! " + m.lastError)
		return appProgramFitLine(line, width)
	}
	return ""
}

func (m *appProgramModel) turnElapsed() string {
	if m.turnStartedAt.IsZero() {
		return ""
	}
	elapsed := time.Since(m.turnStartedAt).Truncate(time.Second)
	if elapsed < time.Second {
		elapsed = time.Second
	}
	return "running " + elapsed.String()
}

func (m *appProgramModel) activeRuntimeActivityView(width int) string {
	lines := m.activeActivityLines()
	if len(lines) == 0 {
		return ""
	}
	for i, line := range lines {
		lines[i] = appProgramFitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func (m *appProgramModel) bottomStatusView(width int) string {
	primary := []string{
		appProgramBrandStyle.Render("Memax Code"),
		m.phaseLabel(),
	}
	contextParts := []string{
		appProgramStatusPart("session", nonEmptyOr(shortSessionID(m.sessionID), "none")),
		appProgramStatusPart("workspace", filepath.Base(m.opts.CWD)),
	}
	if m.opts.Model != "" {
		contextParts = append(contextParts, appProgramStatusMetaStyle.Render(m.opts.Model))
	}
	if m.opts.Effort != "" && m.opts.Effort != "auto" {
		contextParts = append(contextParts, appProgramStatusPart("effort", m.opts.Effort))
	}
	secondary := []string{
		appProgramStatusPart("input", m.composer.statusLine()),
		appProgramStatusMetaStyle.Render("F1 help"),
	}
	parts := append(append([]string{}, primary...), contextParts...)
	parts = append(parts, secondary...)
	line := appProgramStatusLine(parts)
	for width > 0 && lipgloss.Width(line) > width && len(contextParts) > 0 {
		contextParts = contextParts[:len(contextParts)-1]
		parts = append(append([]string{}, primary...), contextParts...)
		parts = append(parts, secondary...)
		line = appProgramStatusLine(parts)
	}
	for _, compactParts := range [][]string{
		append([]string{m.phaseLabel()}, secondary...),
		{m.phaseLabel(), appProgramStatusMetaStyle.Render("F1 help")},
		{m.phaseLabel()},
	} {
		compactLine := appProgramStatusLine(compactParts)
		if width <= 0 || lipgloss.Width(line) <= width || lipgloss.Width(compactLine) <= width {
			if width > 0 && lipgloss.Width(line) > width {
				line = compactLine
			}
			break
		}
	}
	return appProgramFitLine(line, width)
}

func appProgramStatusLine(parts []string) string {
	return lipgloss.NewStyle().PaddingLeft(appProgramStatusInset).Render(strings.Join(parts, appProgramDimStyle.Render("  ·  ")))
}

func appProgramStatusPart(label, value string) string {
	return appProgramStatusMetaStyle.Render(label + " " + value)
}

func (m *appProgramModel) phaseLabel() string {
	if m.lastError != "" {
		return appProgramErrorStyle.Render("error")
	}
	if m.running {
		if m.canceling {
			return appProgramAccentStyle.Render("canceling")
		}
		return appProgramAccentStyle.Render("working")
	}
	return appProgramSuccessStyle.Render(m.statusLine)
}

func (m *appProgramModel) helpView(width int) string {
	return appProgramFitLine(appProgramMutedStyle.Render("/help /status /session /pick /show /sessions /resume /new /draft /submit /cancel /quit"), width)
}

func (m *appProgramModel) composerView(width int) string {
	contentWidth := appProgramComposerContentWidth(width)
	lines := strings.Split(m.input.View(), "\n")
	if m.input.Value() == "" {
		lines = []string{appProgramEmptyComposerLine(m.input.Placeholder)}
	}
	for i, line := range lines {
		lines[i] = appProgramFitLine(line, contentWidth)
	}
	return appProgramComposerStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func appProgramEmptyComposerLine(placeholder string) string {
	return appProgramAccentStyle.
		Background(appProgramComposerBackground).
		Render("› ") + appProgramMutedStyle.
		Background(appProgramComposerBackground).
		Render(placeholder)
}

func appProgramComposerContentWidth(width int) int {
	if width <= 2 {
		return 1
	}
	return width - 2
}

func appProgramLiveRegionWidth(width int) int {
	if width <= 1 {
		return 1
	}
	// Keep one physical terminal column free. Many terminals auto-wrap when
	// output reaches the final column, while Bubble Tea still accounts for one
	// logical row; reserving a column keeps resize repaint math aligned.
	return width - 1
}

func appProgramFitPrintedLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if xansi.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(xansi.Hardwrap(line, width, true), "\n")...)
	}
	return out
}

func appProgramFitLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return truncateStatusLine(line, width)
}

func compactAppProgramTranscriptText(text string) string {
	compactor := appProgramTranscriptCompactor{width: defaultAppShellWidth}
	return strings.ReplaceAll(compactor.compact(text)+compactor.flush(), appTranscriptBlankLine, "")
}

type appProgramTranscriptCompactor struct {
	width                   int
	section                 string
	skipActivityDetail      bool
	assistantInCodeBlock    bool
	assistantHasContent     bool
	assistantAtLineBoundary bool
	assistantLineBuffer     string
	assistantTableRows      []appMarkdownTableRow
	outputHasOpenLine       bool
	lastActivityTool        string
	activityDetail          *appProgramActivityDetail
}

type appMarkdownTableRow struct {
	raw   string
	cells []string
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
	if d.label == "output" && appSkipCommandOutputDetailLine(line) {
		return
	}
	d.lines = append(d.lines, line)
	const maxActivityDetailTailLines = 5
	if len(d.lines) > maxActivityDetailTailLines {
		d.lines = append([]string(nil), d.lines[len(d.lines)-maxActivityDetailTailLines:]...)
	}
}

func appSkipCommandOutputDetailLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "command output for ") || strings.HasPrefix(trimmed, "wrote input to command session ") || strings.HasPrefix(trimmed, "status:")
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
	if c.dropWhitespaceOnlyChunk(text) {
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
		completeLine := trailingNewline || i < len(lines)-1
		for _, compacted := range c.compactLine(line, completeLine) {
			if c.section == "assistant" && compacted == appTranscriptBlankLine && !c.assistantHasContent {
				continue
			}
			if leadingAssistantBoundary && i == 0 && compacted == appTranscriptBlankLine {
				if c.assistantAtLineBoundary {
					out = append(out, appTranscriptBlankLine)
				} else {
					out = append(out, "")
				}
				continue
			}
			if compacted != "" {
				out = append(out, compacted)
				if c.section == "assistant" && compacted != appTranscriptBlankLine {
					c.assistantHasContent = true
				}
			}
		}
	}
	c.assistantAtLineBoundary = c.section == "assistant" && trailingNewline && len(out) > 0
	text = strings.Join(out, "\n")
	if text == "" && leadingAssistantBoundary && trailingNewline {
		c.outputHasOpenLine = false
		return "\n"
	}
	if text != "" && (trailingNewline || c.assistantLineBuffer != "") {
		text += "\n"
	}
	if text != "" {
		c.outputHasOpenLine = !strings.HasSuffix(text, "\n")
	}
	return text
}

func (c *appProgramTranscriptCompactor) startSection(section string) string {
	if c.section == section {
		return ""
	}
	out := c.flush()
	c.section = section
	c.skipActivityDetail = false
	c.lastActivityTool = ""
	c.activityDetail = nil
	c.assistantInCodeBlock = false
	if section == "assistant" {
		c.assistantHasContent = false
		c.assistantAtLineBoundary = false
		c.assistantLineBuffer = ""
		c.assistantTableRows = nil
	}
	return out
}

func (c *appProgramTranscriptCompactor) dropWhitespaceOnlyChunk(text string) bool {
	if c.section == "assistant" {
		return text == ""
	}
	return strings.TrimSpace(text) == "" && (c.section != "assistant" || !strings.Contains(text, "\n"))
}

func (c *appProgramTranscriptCompactor) compactLine(line string, completeLine bool) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		if c.section == "assistant" {
			if !completeLine {
				c.assistantLineBuffer += line
				return nil
			}
			out := c.flushAssistantLineBuffer()
			out = append(out, c.flushAssistantTable()...)
			return append(out, appTranscriptBlankLine)
		}
		if c.activityDetail != nil {
			c.activityDetail.append("")
			return nil
		}
		return []string{line}
	}
	if section, label, ok := compactAppProgramSectionLabel(trimmed); ok {
		out := c.flushAssistantLineBuffer()
		out = append(out, c.flushAssistantTable()...)
		out = append(out, c.flushActivityDetail()...)
		c.lastActivityTool = ""
		if section == "assistant" {
			c.assistantHasContent = false
			c.assistantAtLineBoundary = false
			c.assistantLineBuffer = ""
		}
		c.section = section
		c.skipActivityDetail = false
		c.assistantInCodeBlock = false
		c.assistantTableRows = nil
		if label != "" {
			out = append(out, label)
		}
		return out
	}
	switch c.section {
	case "assistant":
		return c.compactAssistantLineChunk(line, completeLine)
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

func (c *appProgramTranscriptCompactor) compactAssistantLineChunk(line string, completeLine bool) []string {
	if !completeLine {
		c.assistantLineBuffer += line
		return nil
	}
	if c.assistantLineBuffer != "" {
		line = c.assistantLineBuffer + line
		c.assistantLineBuffer = ""
	}
	return c.compactAssistantLine(line)
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

func (c *appProgramTranscriptCompactor) compactAssistantLine(line string) []string {
	trimmedRight := strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(trimmedRight)
	if trimmed == "" {
		out := c.flushAssistantTable()
		return append(out, appTranscriptBlankLine)
	}
	if strings.HasPrefix(trimmed, "```") || c.assistantInCodeBlock {
		return c.compactAssistantNonTableWithFlushedTable(trimmedRight, trimmed)
	}
	if c.appendAssistantTableLine(trimmedRight) {
		return nil
	}
	return c.compactAssistantNonTableWithFlushedTable(trimmedRight, trimmed)
}

func (c *appProgramTranscriptCompactor) compactAssistantNonTableWithFlushedTable(trimmedRight, trimmed string) []string {
	out := c.flushAssistantTable()
	if len(out) > 0 {
		c.assistantHasContent = true
	}
	rendered := c.compactAssistantNonTableLine(trimmedRight, trimmed)
	c.assistantHasContent = true
	return append(out, rendered)
}

func (c *appProgramTranscriptCompactor) compactAssistantNonTableLine(trimmedRight, trimmed string) string {
	prefix := c.assistantLinePrefix()
	if strings.HasPrefix(trimmed, "```") {
		c.assistantInCodeBlock = !c.assistantInCodeBlock
		return prefix + appProgramCodeStyle.Render(trimmed)
	}
	if c.assistantInCodeBlock {
		return prefix + appProgramCodeStyle.Render(strings.TrimRight(trimmedRight, "\t "))
	}
	if heading, ok := appMarkdownHeading(trimmed); ok {
		return prefix + appProgramHeadingStyle.Render(appStripMarkdownDelimiters(heading))
	}
	if strings.HasPrefix(trimmed, ">") && !strings.HasPrefix(trimmed, "> tool ") {
		return prefix + appProgramQuoteStyle.Render("│ "+appStripMarkdownDelimiters(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
	}
	if appMarkdownHorizontalRule(trimmed) {
		return prefix + appProgramDimStyle.Render(strings.Repeat("─", 64))
	}
	if indent, bullet, rest, ok := appMarkdownBulletLine(trimmedRight); ok {
		if !c.assistantHasContent && indent == "" && bullet == "•" {
			return prefix + appRenderInlineMarkdown(rest)
		}
		return prefix + appProgramMarkdownStyle.Render(indent+bullet+" ") + appRenderInlineMarkdown(rest)
	}
	if strings.HasPrefix(trimmedRight, "    ") || strings.HasPrefix(trimmedRight, "\t") {
		return prefix + appProgramCodeStyle.Render(strings.TrimRight(trimmedRight, "\t "))
	}
	return prefix + appRenderInlineMarkdown(trimmedRight)
}

func (c *appProgramTranscriptCompactor) appendAssistantTableLine(line string) bool {
	cells, ok := appMarkdownTableCells(line)
	if !ok || len(cells) < 2 {
		return false
	}
	c.assistantTableRows = append(c.assistantTableRows, appMarkdownTableRow{
		raw:   line,
		cells: cells,
	})
	return true
}

func (c *appProgramTranscriptCompactor) flushAssistantTable() []string {
	if len(c.assistantTableRows) == 0 {
		return nil
	}
	rows := c.assistantTableRows
	c.assistantTableRows = nil
	if !appMarkdownTableRowsContainSeparator(rows) {
		return c.flushAssistantTableRowsAsText(rows)
	}
	widths := c.appMarkdownTableWidths(rows)
	out := make([]string, 0, len(rows)*2)
	hadContent := c.assistantHasContent
	for _, row := range rows {
		prefix := "  "
		if !hadContent {
			prefix = appProgramMarkdownStyle.Render("• ")
			hadContent = true
		}
		for _, line := range appRenderMarkdownTableRow(row.cells, widths) {
			out = append(out, prefix+line)
			prefix = "  "
		}
	}
	c.assistantHasContent = true
	return out
}

func (c *appProgramTranscriptCompactor) flushAssistantTableRowsAsText(rows []appMarkdownTableRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, c.compactAssistantNonTableLine(row.raw, strings.TrimSpace(row.raw)))
		c.assistantHasContent = true
	}
	return out
}

func appMarkdownTableRowsContainSeparator(rows []appMarkdownTableRow) bool {
	for _, row := range rows {
		if appMarkdownTableSeparator(row.cells) {
			return true
		}
	}
	return false
}

func (c *appProgramTranscriptCompactor) appMarkdownTableWidths(rows []appMarkdownTableRow) []int {
	var widths []int
	for _, row := range rows {
		cells := row.cells
		if len(widths) < len(cells) {
			widths = append(widths, make([]int, len(cells)-len(widths))...)
		}
		if appMarkdownTableSeparator(cells) {
			for i := range cells {
				widths[i] = max(widths[i], 3)
			}
			continue
		}
		for i, cell := range cells {
			widths[i] = max(widths[i], lipgloss.Width(appMarkdownTableCellText(cell)))
		}
	}
	for i, width := range widths {
		widths[i] = max(3, width)
	}
	c.shrinkMarkdownTableWidths(widths)
	return widths
}

func (c *appProgramTranscriptCompactor) shrinkMarkdownTableWidths(widths []int) {
	if len(widths) == 0 {
		return
	}
	available := c.markdownTableContentWidth(len(widths))
	total := appMarkdownTableContentWidth(widths)
	minWidth := 6
	if len(widths) >= 4 {
		minWidth = 4
	}
	for total > available {
		idx := -1
		for i, width := range widths {
			if width <= minWidth {
				continue
			}
			if idx < 0 || width > widths[idx] {
				idx = i
			}
		}
		if idx < 0 {
			return
		}
		widths[idx]--
		total--
	}
}

func (c *appProgramTranscriptCompactor) markdownTableContentWidth(cols int) int {
	width := c.width
	if width <= 0 {
		width = defaultAppShellWidth
	}
	available := width - 10 - max(0, cols-1)*3
	if available < cols*3 {
		// Keep a readable floor for pathologically narrow terminals. The table may
		// still overflow, but columns do not collapse to zero-width fragments.
		return cols * 3
	}
	return available
}

func appMarkdownTableContentWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

func appRenderMarkdownTableRow(cells []string, widths []int) []string {
	if appMarkdownTableSeparator(cells) {
		segments := make([]string, len(widths))
		for i, width := range widths {
			segments[i] = strings.Repeat("─", width+2)
		}
		return []string{appProgramDimStyle.Render("  ├" + strings.Join(segments, "┼") + "┤")}
	}
	wrapped := make([][]string, len(widths))
	maxLines := 1
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		wrapped[i] = appWrapMarkdownTableCell(cell, widths[i])
		maxLines = max(maxLines, len(wrapped[i]))
	}
	out := make([]string, 0, maxLines)
	for rowLine := 0; rowLine < maxLines; rowLine++ {
		rendered := make([]string, len(widths))
		for i := range widths {
			cell := ""
			if rowLine < len(wrapped[i]) {
				cell = wrapped[i][rowLine]
			}
			rendered[i] = appPadRenderedRight(appRenderInlineMarkdown(strings.TrimSpace(cell)), widths[i])
		}
		out = append(out, appProgramMarkdownStyle.Render("  │ ")+strings.Join(rendered, appProgramDimStyle.Render(" │ "))+appProgramMarkdownStyle.Render(" │"))
	}
	return out
}

func appWrapMarkdownTableCell(cell string, width int) []string {
	cell = strings.Join(strings.Fields(appMarkdownTableCellText(cell)), " ")
	if cell == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{cell}
	}
	words := strings.Fields(cell)
	var lines []string
	current := ""
	for _, word := range words {
		if lipgloss.Width(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, appSplitLongTableWord(word, width)...)
			continue
		}
		if current == "" {
			current = word
			continue
		}
		if lipgloss.Width(current)+1+lipgloss.Width(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func appMarkdownTableCellText(cell string) string {
	return strings.TrimSpace(appStripMarkdownDelimiters(cell))
}

func appSplitLongTableWord(word string, width int) []string {
	if width <= 0 {
		return []string{word}
	}
	var lines []string
	var current strings.Builder
	currentWidth := 0
	for _, r := range word {
		part := string(r)
		partWidth := lipgloss.Width(part)
		if currentWidth > 0 && currentWidth+partWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			currentWidth = 0
		}
		current.WriteString(part)
		currentWidth += partWidth
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (c *appProgramTranscriptCompactor) assistantLinePrefix() string {
	if c.assistantHasContent {
		return "  "
	}
	return appProgramMarkdownStyle.Render("• ")
}

func (c *appProgramTranscriptCompactor) resetSection() {
	c.section = ""
	c.skipActivityDetail = false
	c.assistantInCodeBlock = false
	c.assistantHasContent = false
	c.assistantAtLineBoundary = false
	c.assistantLineBuffer = ""
	c.assistantTableRows = nil
	c.outputHasOpenLine = false
	c.lastActivityTool = ""
	c.activityDetail = nil
}

func appRenderInlineMarkdown(text string) string {
	return appRenderInlineMarkdownWithBase(text, appProgramMarkdownStyle)
}

func appRenderInlineMarkdownWithBase(text string, base lipgloss.Style) string {
	var out strings.Builder
	for text != "" {
		if strings.HasPrefix(text, "**") {
			if end := strings.Index(text[2:], "**"); end >= 0 {
				inner := text[2 : 2+end]
				if strings.TrimSpace(inner) == "" {
					out.WriteString(base.Render("**" + inner + "**"))
				} else {
					out.WriteString(appRenderInlineMarkdownWithBase(inner, appProgramStrongStyle))
				}
				text = text[2+end+2:]
				continue
			}
		}
		if strings.HasPrefix(text, "`") {
			if end := strings.Index(text[1:], "`"); end >= 0 {
				inner := text[1 : 1+end]
				if strings.TrimSpace(inner) == "" {
					out.WriteString(base.Render("`" + inner + "`"))
				} else {
					out.WriteString(appProgramInlineCodeStyle.Render(inner))
				}
				text = text[1+end+1:]
				continue
			}
		}
		next := appNextInlineMarkdownMarker(text)
		if next < 0 {
			out.WriteString(base.Render(text))
			break
		}
		if next == 0 {
			if strings.HasPrefix(text, "**") {
				out.WriteString(base.Render("**"))
				text = text[2:]
			} else {
				out.WriteString(base.Render(text[:1]))
				text = text[1:]
			}
			continue
		}
		out.WriteString(base.Render(text[:next]))
		text = text[next:]
	}
	return out.String()
}

func appNextInlineMarkdownMarker(text string) int {
	code := strings.Index(text, "`")
	strong := strings.Index(text, "**")
	switch {
	case code < 0:
		return strong
	case strong < 0:
		return code
	case code < strong:
		return code
	default:
		return strong
	}
}

func appStripMarkdownDelimiters(text string) string {
	text = strings.ReplaceAll(text, "**", "")
	return strings.ReplaceAll(text, "`", "")
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
	width, indent, content := appMarkdownIndentPrefix(line)
	if width > 6 {
		return "", "", "", false
	}
	bullet, rest, ok = appMarkdownBullet(content)
	if !ok {
		return "", "", "", false
	}
	return indent, bullet, rest, true
}

func appMarkdownTableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return nil, false
	}
	line = strings.TrimSuffix(strings.TrimPrefix(trimmed, "|"), "|")
	raw := strings.Split(line, "|")
	cells := make([]string, len(raw))
	for i, cell := range raw {
		cells[i] = strings.TrimSpace(cell)
	}
	return cells, true
}

func appMarkdownTableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.Trim(cell, " :-")
		if cell != "" {
			return false
		}
	}
	return true
}

func appMarkdownHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	var marker rune
	count := 0
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			continue
		}
		if marker == 0 {
			switch r {
			case '-', '*', '_':
				marker = r
			default:
				return false
			}
		}
		if r != marker {
			return false
		}
		count++
	}
	return count >= 3
}

func appPadRenderedRight(text string, width int) string {
	if pad := width - lipgloss.Width(text); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}

func appMarkdownIndentPrefix(line string) (width int, indent, content string) {
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
				return width, strings.Repeat(" ", 6), line[i:]
			}
			return width, strings.Repeat(" ", width), line[i:]
		}
	}
	if width > 6 {
		return width, strings.Repeat(" ", 6), ""
	}
	return width, strings.Repeat(" ", width), ""
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
		c.lastActivityTool = appActivityToolName(trimmed)
		return append(out, appProgramToolStyle.Render("• "+appFormatToolLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "> ")))))
	}
	if strings.HasPrefix(trimmed, "< tool ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		c.lastActivityTool = appActivityToolName(trimmed)
		if appToolResultIsRedundant(c.lastActivityTool) {
			return out
		}
		return append(out, appProgramDimStyle.Render("  "+appFormatToolLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "< ")))))
	}
	if strings.HasPrefix(trimmed, "! tool ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		c.lastActivityTool = appActivityToolName(trimmed)
		return append(out, appProgramErrorStyle.Render("! "+appFormatToolLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "! ")))))
	}
	if strings.HasPrefix(trimmed, "$ command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramToolStyle.Render("• "+appFormatCommandLine("started", strings.TrimSpace(strings.TrimPrefix(trimmed, "$ command ")))))
	}
	if strings.HasPrefix(trimmed, "+ command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramSuccessStyle.Render("✓ "+appFormatCommandLine("done", strings.TrimSpace(strings.TrimPrefix(trimmed, "+ command ")))))
	}
	if strings.HasPrefix(trimmed, "! command ") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = false
		return append(out, appProgramErrorStyle.Render("! "+appFormatCommandLine("failed", strings.TrimSpace(strings.TrimPrefix(trimmed, "! command ")))))
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
		out := c.flushActivityDetail()
		if appToolShowsResultTail(c.lastActivityTool) {
			c.skipActivityDetail = true
			c.activityDetail = &appProgramActivityDetail{label: "output", style: appProgramDimStyle}
			c.activityDetail.append(strings.TrimSpace(strings.TrimPrefix(trimmed, "result:")))
			return out
		}
		c.activityDetail = nil
		c.skipActivityDetail = true
		return out
	}
	if strings.HasPrefix(trimmed, "error:") {
		out := c.flushActivityDetail()
		c.skipActivityDetail = true
		c.activityDetail = &appProgramActivityDetail{label: "error", style: appProgramErrorStyle}
		c.activityDetail.append(strings.TrimSpace(strings.TrimPrefix(trimmed, "error:")))
		return out
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

func appActivityToolName(trimmed string) string {
	fields := strings.Fields(trimmed)
	if len(fields) < 3 || fields[1] != "tool" {
		return ""
	}
	return fields[2]
}

func appFormatToolLine(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "tool" {
		return line
	}
	name := appToolDisplayName(fields[1])
	if len(fields) > 2 {
		rest := strings.Join(fields[2:], " ")
		if rest == "call" {
			return name
		}
		return name + " " + rest
	}
	return name
}

func appToolDisplayName(name string) string {
	switch name {
	case "run_command", "start_command":
		return "Bash"
	case "read_command_output":
		return "Read command output"
	case "wait_command_output":
		return "Wait for command output"
	case "write_command_input":
		return "Write command input"
	case "stop_command":
		return "Stop command"
	case "resize_command_terminal":
		return "Resize command terminal"
	case "workspace_apply_patch":
		return "Apply patch"
	case "workspace_read_file":
		return "Read file"
	case "workspace_list_files":
		return "List files"
	case "workspace_diff":
		return "Show diff"
	case "run_subagent":
		return "Subagent"
	case "web_fetch":
		return "Web fetch"
	case "web_search":
		return "Web search"
	default:
		return statusValue(name)
	}
}

func appToolUseDisplay(toolUse *model.ToolUse) string {
	if toolUse == nil {
		return ""
	}
	name := appToolDisplayName(toolUse.Name)
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}
	if command := appToolUseCommand(toolUse); command != "" {
		return name + "(" + command + ")"
	}
	if subagent, ok := appToolUseSubagentInput(toolUse); ok {
		display := name + "(" + subagent.Agent + ")"
		if prompt := appInlineSnippet(subagent.Prompt, 56); prompt != "" {
			display += " " + prompt
		}
		return display
	}
	if toolUse.Name == "web_fetch" {
		if url := appToolUseWebFetchURL(toolUse); url != "" {
			return name + "(" + appInlineSnippet(url, 96) + ")"
		}
	}
	if display := appWorkspaceToolUseDisplay(toolUse, name); display != "" {
		return display
	}
	return name
}

func appWorkspaceToolUseDisplay(toolUse *model.ToolUse, displayName string) string {
	if toolUse == nil {
		return ""
	}
	switch toolUse.Name {
	case "workspace_read_file":
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(toolUse.Input, &input); err == nil && strings.TrimSpace(input.Path) != "" {
			return displayName + "(" + appInlineSnippet(strings.TrimSpace(input.Path), 96) + ")"
		}
		return displayName
	case "workspace_list_files":
		var input struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(toolUse.Input, &input); err == nil && strings.TrimSpace(input.Prefix) != "" {
			return displayName + "(" + appInlineSnippet(strings.TrimSpace(input.Prefix), 96) + ")"
		}
		return displayName
	case "workspace_apply_patch":
		var input struct {
			Operations []struct {
				Path string `json:"path"`
			} `json:"operations"`
			UnifiedDiff string `json:"unified_diff"`
			DryRun      bool   `json:"dry_run"`
		}
		if err := json.Unmarshal(toolUse.Input, &input); err != nil {
			return displayName
		}
		prefix := displayName
		if input.DryRun {
			prefix = "Review patch"
		}
		if len(input.Operations) == 1 && strings.TrimSpace(input.Operations[0].Path) != "" {
			return prefix + "(" + appInlineSnippet(strings.TrimSpace(input.Operations[0].Path), 96) + ")"
		}
		if len(input.Operations) > 1 {
			return fmt.Sprintf("%s(%d files)", prefix, len(input.Operations))
		}
		if strings.TrimSpace(input.UnifiedDiff) != "" {
			return prefix + "(unified diff)"
		}
		return prefix
	case "workspace_diff":
		var input struct {
			BaseID string `json:"base_id"`
		}
		if err := json.Unmarshal(toolUse.Input, &input); err == nil && strings.TrimSpace(input.BaseID) != "" {
			return displayName + "(base " + appInlineSnippet(strings.TrimSpace(input.BaseID), 32) + ")"
		}
		return displayName
	default:
		return ""
	}
}

func appToolUseCommand(toolUse *model.ToolUse) string {
	if toolUse == nil {
		return ""
	}
	switch toolUse.Name {
	case "run_command", "start_command":
	default:
		return ""
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(toolUse.Input, &input); err != nil {
		return ""
	}
	return strings.TrimSpace(input.Command)
}

func appToolUseWebFetchURL(toolUse *model.ToolUse) string {
	if toolUse == nil || toolUse.Name != "web_fetch" {
		return ""
	}
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(toolUse.Input, &input); err != nil {
		return ""
	}
	return strings.TrimSpace(input.URL)
}

func appToolUseSubagent(toolUse *model.ToolUse) string {
	input, ok := appToolUseSubagentInput(toolUse)
	if !ok {
		return ""
	}
	return input.Agent
}

type appSubagentToolInput struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
}

func appToolUseSubagentInput(toolUse *model.ToolUse) (appSubagentToolInput, bool) {
	if toolUse == nil || toolUse.Name != "run_subagent" {
		return appSubagentToolInput{}, false
	}
	var input appSubagentToolInput
	if err := json.Unmarshal(toolUse.Input, &input); err != nil {
		return appSubagentToolInput{}, false
	}
	input.Agent = strings.TrimSpace(input.Agent)
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Agent == "" {
		return appSubagentToolInput{}, false
	}
	return input, true
}

func appToolShowsResultTail(name string) bool {
	switch name {
	case "read_command_output", "wait_command_output", "write_command_input":
		return true
	default:
		return false
	}
}

func appInlineSnippet(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}

func appToolResultIsRedundant(name string) bool {
	switch name {
	case "run_command", "start_command", "stop_command", "resize_command_terminal":
		return true
	default:
		return false
	}
}

func appFormatCommandLine(status, raw string) string {
	fields := appParseActivityFields(raw)
	command := fields["command"]
	if command == "" {
		return strings.TrimSpace("command " + raw)
	}
	if status != "started" {
		parts := []string{status}
		if id := fields["id"]; id != "" {
			parts = append(parts, "id="+id)
		}
		if exit := fields["exit"]; exit != "" {
			parts = append(parts, "exit="+exit)
		}
		if timeout := fields["timeout"]; timeout == "true" {
			parts = append(parts, "timeout=true")
		}
		return strings.Join(parts, " ")
	}
	parts := []string{"Bash(" + command + ")", "started"}
	if id := fields["id"]; id != "" {
		parts = append(parts, "id="+id)
	}
	if pid := fields["pid"]; pid != "" {
		parts = append(parts, "pid="+pid)
	}
	return strings.Join(parts, " ")
}

func appCommandAuxLine(action string, command *memaxagent.CommandEvent) string {
	idPart := ""
	if command.CommandID != "" {
		idPart = " id=" + command.CommandID
	}
	switch action {
	case "output":
		return fmt.Sprintf("  output%s chunks=%d next_seq=%d", idPart, command.OutputChunks, command.NextSeq)
	case "input":
		return fmt.Sprintf("  input%s bytes=%d", idPart, command.InputBytes)
	case "resize":
		return fmt.Sprintf("  resize%s cols=%d rows=%d", idPart, command.Cols, command.Rows)
	default:
		return ""
	}
}

func appCommandDisplay(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "Bash"
	}
	return "Bash(" + command + ")"
}

func appParseActivityFields(raw string) map[string]string {
	fields := make(map[string]string)
	for i := 0; i < len(raw); {
		for i < len(raw) && raw[i] == ' ' {
			i++
		}
		start := i
		for i < len(raw) && raw[i] != '=' && raw[i] != ' ' {
			i++
		}
		if i >= len(raw) || raw[i] != '=' || start == i {
			for i < len(raw) && raw[i] != ' ' {
				i++
			}
			continue
		}
		key := raw[start:i]
		i++
		if i >= len(raw) {
			fields[key] = ""
			break
		}
		if raw[i] == '"' {
			value, next := appParseQuotedActivityValue(raw[i:])
			fields[key] = value
			i += next
			continue
		}
		valueStart := i
		for i < len(raw) && raw[i] != ' ' {
			i++
		}
		fields[key] = raw[valueStart:i]
	}
	return fields
}

func appParseQuotedActivityValue(raw string) (string, int) {
	for i := 1; i < len(raw); i++ {
		if raw[i] != '"' || appEscapedQuote(raw, i) {
			continue
		}
		quoted := raw[:i+1]
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return strings.Trim(quoted, `"`), i + 1
		}
		return value, i + 1
	}
	return strings.TrimPrefix(raw, `"`), len(raw)
}

func appEscapedQuote(raw string, quoteIndex int) bool {
	backslashes := 0
	for i := quoteIndex - 1; i >= 0 && raw[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func (c *appProgramTranscriptCompactor) flushActivityDetail() []string {
	if c.activityDetail == nil {
		return nil
	}
	out := c.activityDetail.render()
	c.activityDetail = nil
	return out
}

func (c *appProgramTranscriptCompactor) flushAssistantLineBuffer() []string {
	if c.section != "assistant" || c.assistantLineBuffer == "" {
		return nil
	}
	line := c.assistantLineBuffer
	c.assistantLineBuffer = ""
	if strings.TrimSpace(line) == "" {
		return nil
	}
	return c.compactAssistantLine(line)
}

func (c *appProgramTranscriptCompactor) flush() string {
	out := c.flushAssistantLineBuffer()
	out = append(out, c.flushAssistantTable()...)
	out = append(out, c.flushActivityDetail()...)
	if len(out) == 0 {
		return ""
	}
	text := strings.Join(out, "\n") + "\n"
	if c.outputHasOpenLine {
		text = "\n" + text
	}
	c.outputHasOpenLine = false
	return text
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
	return runInteractiveAppWithEvents(ctx, stdin, stdout, opts, runPrompt, nil)
}

func runInteractiveAppWithEvents(ctx context.Context, stdin io.Reader, stdout io.Writer, opts options, runPrompt interactivePromptRunner, runEvents interactiveEventPromptRunner) error {
	model := newAppProgramModelWithEvents(ctx, opts, runPrompt, runEvents)
	terminal, terminalWidth, terminalHeight := terminalWriterInfo(stdout)
	if terminal {
		// terminalWriterInfo reserves one physical column for terminal wrapping.
		// appProgramModel.width stores the real terminal width and applies its
		// own width reserve when wrapping printed transcript lines.
		if terminalWidth > 0 {
			model.width = terminalWidth + 1
		} else {
			model.width = defaultAppShellWidth
		}
		if terminalHeight > 0 {
			model.height = terminalHeight
		} else {
			model.height = defaultAppShellHeight
		}
		model.resize()
	} else {
		model.plainOut = stdout
	}
	programOpts := []tea.ProgramOption{
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithContext(ctx),
	}
	if !terminal {
		programOpts = append(programOpts, tea.WithoutRenderer())
	}
	program := tea.NewProgram(model, programOpts...)
	model.program = program
	stopResizeWatcher := func() {}
	if terminal {
		stopResizeWatcher = startAppProgramResizeWatcher(ctx, program, stdout)
	}
	defer stopResizeWatcher()
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	if result, ok := finalModel.(*appProgramModel); ok && result.firstErr != nil {
		return result.firstErr
	}
	return nil
}
