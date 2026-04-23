package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

const (
	appClearScreen        = "\x1b[H\x1b[2J"
	defaultAppShellWidth  = defaultLiveStatusWidth
	defaultAppShellHeight = 28
	appShellTickInterval  = 120 * time.Millisecond
	maxAppTranscriptLines = 512
	maxAppActiveCommands  = 3
	maxAppRecentLines     = 4
)

type appRenderState struct {
	// The first app shell reuses the structured transcript renderer as its
	// transcript model. Keep the buffered tail bounded here so a richer
	// event-native transcript model can replace it without changing UI modes.
	transcriptRenderer       tuiRenderState
	transcriptTail           appTranscriptTail
	transcriptHeaderStripped bool
	width                    int
	height                   int
	startedAt                time.Time
	now                      func() time.Time
}

func (s *appRenderState) Render(w io.Writer, event memaxagent.Event) error {
	s.markStarted()
	var chunk bytes.Buffer
	err := s.transcriptRenderer.Render(&chunk, event)
	s.appendTranscriptChunk(chunk.String())
	s.redraw(w)
	return err
}

func (s *appRenderState) Finish(w io.Writer) error {
	var chunk bytes.Buffer
	err := s.transcriptRenderer.Finish(&chunk)
	s.appendTranscriptChunk(chunk.String())
	s.redraw(w)
	return err
}

func (s *appRenderState) Tick(w io.Writer) error {
	activity := s.transcriptRenderer.activity.snapshot()
	if activity.ResultSeen || activity.TerminalError {
		return nil
	}
	s.markStarted()
	s.redraw(w)
	return nil
}

func (s *appRenderState) TickInterval() time.Duration {
	return appShellTickInterval
}

func (s *appRenderState) appendTranscriptChunk(text string) {
	if !s.transcriptHeaderStripped {
		text = strings.TrimPrefix(text, "Memax Code\n----------\n")
		s.transcriptHeaderStripped = true
	}
	s.transcriptTail.append(text)
}

func (s *appRenderState) redraw(w io.Writer) {
	activity := s.transcriptRenderer.activity.snapshot()
	width := s.panelWidth()
	height := s.panelHeight()
	lines := s.frameLines(activity, width, height)
	fmt.Fprint(w, appClearScreen)
	for _, line := range lines {
		fmt.Fprintln(w, fitPanelLine(line, width))
	}
}

func (s *appRenderState) frameLines(activity activitySnapshot, width, height int) []string {
	lines := make([]string, 0, height)
	lines = append(lines, s.headerLine(activity), strings.Repeat("-", width))

	lines = append(lines, "[active]")
	lines = append(lines, appActiveLines(activity)...)

	lines = append(lines, "", "[recent]")
	if recent := appRecentLines(activity); len(recent) > 0 {
		for _, line := range recent {
			lines = append(lines, "  "+line)
		}
	} else {
		lines = append(lines, "  none")
	}

	lines = append(lines, "", "[transcript]")
	footer := "Ctrl+C cancel | /help commands | --ui tui for scrollback logs"
	transcriptBudget := height - len(lines) - 2
	if transcriptBudget < 0 {
		transcriptBudget = 0
	}
	lines = append(lines, s.transcriptTail.lines(transcriptBudget)...)
	lines = append(lines, strings.Repeat("-", width), footer)
	return fitFrameHeight(lines, height)
}

func appActiveLines(activity activitySnapshot) []string {
	if activity.ActiveTool == "" && len(activity.ActiveCommands) == 0 {
		return []string{"  idle"}
	}
	lines := make([]string, 0, maxAppActiveCommands+2)
	if activity.ActiveTool != "" {
		lines = append(lines, "  tool: "+activity.ActiveTool)
	}
	limit := len(activity.ActiveCommands)
	if limit > maxAppActiveCommands {
		limit = maxAppActiveCommands
	}
	for _, command := range activity.ActiveCommands[:limit] {
		lines = append(lines, "  command: "+command.summary())
	}
	if extra := len(activity.ActiveCommands) - limit; extra > 0 {
		lines = append(lines, fmt.Sprintf("  ... %d more active commands", extra))
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
	if activity.LastApproval != "" {
		lines = append(lines, "approval: "+activity.LastApproval)
	}
	if len(lines) > maxAppRecentLines {
		return lines[len(lines)-maxAppRecentLines:]
	}
	return lines
}

func (s *appRenderState) headerLine(activity activitySnapshot) string {
	parts := []string{"Memax Code", "phase=" + activity.Phase}
	if elapsed := s.elapsedStatus(); elapsed != "" {
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

func (s *appRenderState) markStarted() {
	if !s.startedAt.IsZero() {
		return
	}
	s.startedAt = s.currentTime()
}

func (s *appRenderState) elapsedStatus() string {
	if s.startedAt.IsZero() {
		return ""
	}
	elapsed := s.currentTime().Sub(s.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return formatElapsed(elapsed)
}

func (s *appRenderState) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *appRenderState) panelWidth() int {
	if s.width > 0 {
		return s.width
	}
	return defaultAppShellWidth
}

func (s *appRenderState) panelHeight() int {
	if s.height > 0 {
		return s.height
	}
	return defaultAppShellHeight
}

type appTranscriptTail struct {
	entries []string
	partial string
	limit   int
}

func (t *appTranscriptTail) append(text string) {
	if text == "" {
		return
	}
	text = t.partial + text
	parts := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		t.partial = ""
	} else {
		t.partial = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	for _, line := range parts {
		t.appendLine(line)
	}
}

func (t *appTranscriptTail) appendLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	t.entries = append(t.entries, line)
	limit := t.effectiveLimit()
	if len(t.entries) > limit {
		t.entries = append([]string(nil), t.entries[len(t.entries)-limit:]...)
	}
}

func (t *appTranscriptTail) lines(limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := make([]string, 0, len(t.entries)+1)
	lines = append(lines, t.entries...)
	if strings.TrimSpace(t.partial) != "" {
		lines = append(lines, t.partial)
	}
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func (t *appTranscriptTail) effectiveLimit() int {
	if t.limit > 0 {
		return t.limit
	}
	return maxAppTranscriptLines
}

func fitFrameHeight(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if height == 1 {
		return []string{lines[len(lines)-1]}
	}
	fitted := make([]string, 0, height)
	fitted = append(fitted, lines[:height-1]...)
	return append(fitted, lines[len(lines)-1])
}

func fitPanelLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return truncateStatusLine(line, width)
}
