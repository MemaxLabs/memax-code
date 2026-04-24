package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/charmbracelet/x/ansi"
)

const (
	appClearScreen        = "\x1b[H\x1b[2J"
	defaultAppShellWidth  = defaultLiveStatusWidth
	defaultAppShellHeight = 24
	appShellTickInterval  = 120 * time.Millisecond
	maxAppTranscriptLines = 512
)

type appRenderState struct {
	// The non-interactive app renderer still reuses the structured transcript
	// renderer as its transcript model. Keep the buffered tail bounded here so
	// an event-native transcript model can replace it without changing CLI
	// contracts.
	transcriptRenderer       tuiRenderState
	transcriptTail           appTranscriptTail
	transcriptHeaderStripped bool
	width                    int
	height                   int
	transcriptOffset         int
	helpVisible              bool
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

func (s *appRenderState) HandleKey(w io.Writer, key rawKey) error {
	switch key.kind {
	case rawKeyRune:
		if key.char == '?' {
			s.helpVisible = !s.helpVisible
			s.redraw(w)
		}
		return nil
	case rawKeyHistoryPrev:
		s.helpVisible = false
		s.scrollTranscript(1)
	case rawKeyHistoryNext:
		s.helpVisible = false
		s.scrollTranscript(-1)
	case rawKeyPageUp:
		s.helpVisible = false
		s.scrollTranscript(s.transcriptPageStep())
	case rawKeyPageDown:
		s.helpVisible = false
		s.scrollTranscript(-s.transcriptPageStep())
	case rawKeyHome:
		s.helpVisible = false
		s.scrollTranscript(maxAppTranscriptLines)
	case rawKeyEnd:
		s.helpVisible = false
		s.transcriptOffset = 0
	case rawKeyCtrlC:
		return contextCanceled
	default:
		return nil
	}
	s.redraw(w)
	return nil
}

func (s *appRenderState) appendTranscriptChunk(text string) {
	if !s.transcriptHeaderStripped {
		text = strings.TrimPrefix(text, "Memax Code\n----------\n")
		s.transcriptHeaderStripped = true
	}
	text = normalizeAppTranscriptText(text)
	before := len(s.transcriptTail.lines(maxAppTranscriptLines))
	s.transcriptTail.append(text)
	if s.transcriptOffset > 0 {
		if delta := len(s.transcriptTail.lines(maxAppTranscriptLines)) - before; delta > 0 {
			s.transcriptOffset += delta
		}
	}
	s.clampTranscriptOffset()
}

func normalizeAppTranscriptText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return sanitizeTranscriptText(text)
}

func (s *appRenderState) redraw(w io.Writer) {
	activity := s.transcriptRenderer.activity.snapshot()
	width := s.panelWidth()
	height := s.panelHeight()
	lines := s.frameLines(activity, width, height)
	fmt.Fprint(w, appClearScreen)
	for _, line := range lines {
		fmt.Fprintf(w, "\r%s\n", fitPanelLine(line, width))
	}
}

func (s *appRenderState) frameLines(activity activitySnapshot, width, height int) []string {
	frame := newAppShellFrame(activity, s.transcriptTail.lines(maxAppTranscriptLines), width, height, s.elapsedStatus())
	frame.TranscriptOffset = s.transcriptOffset
	frame.TranscriptStatus = s.transcriptStatus()
	frame.HelpVisible = s.helpVisible
	return frame.Lines()
}

func (s *appRenderState) transcriptStatus() string {
	if s.transcriptOffset == 0 {
		return "live tail"
	}
	return "manual scroll (" + appHiddenLine("↓", s.transcriptOffset, "newer") + " below)"
}

func (s *appRenderState) scrollTranscript(delta int) {
	s.transcriptOffset += delta
	s.clampTranscriptOffset()
}

func (s *appRenderState) clampTranscriptOffset() {
	if s.transcriptOffset < 0 {
		s.transcriptOffset = 0
		return
	}
	lines := s.transcriptTail.lines(maxAppTranscriptLines)
	activity := s.transcriptRenderer.activity.snapshot()
	frame := newAppShellFrame(activity, lines, s.panelWidth(), s.panelHeight(), s.elapsedStatus())
	maxOffset := len(lines) - frame.transcriptBudget()
	if maxOffset < 0 {
		maxOffset = 0
	}
	if s.transcriptOffset > maxOffset {
		s.transcriptOffset = maxOffset
	}
}

func (s *appRenderState) transcriptPageStep() int {
	lines := s.transcriptTail.lines(maxAppTranscriptLines)
	activity := s.transcriptRenderer.activity.snapshot()
	frame := newAppShellFrame(activity, lines, s.panelWidth(), s.panelHeight(), s.elapsedStatus())
	step := frame.transcriptBudget()
	if step < 1 {
		return 1
	}
	return step
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

func (t *appTranscriptTail) appendStandaloneLine(line string) {
	// Local rows are inserted at prompt/session boundaries, not mid-assistant
	// stream. Flush first so local UI rows never glue onto streamed text.
	t.flushPartial()
	t.appendLine(line)
}

func (t *appTranscriptTail) flushPartial() {
	if strings.TrimSpace(t.partial) == "" {
		t.partial = ""
		return
	}
	partial := t.partial
	t.partial = ""
	t.appendLine(partial)
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

func appTranscriptVisualLineCount(lines []string, width int) int {
	if width < 1 {
		width = 1
	}
	// Bubble's viewport scrolls logical lines, while the terminal wraps visual
	// rows. Use display width here only to reserve enough vertical space for
	// short transcripts before they reach the scrollable viewport path.
	count := 0
	for _, line := range lines {
		lineWidth := ansi.StringWidth(line)
		if lineWidth <= 0 {
			count++
			continue
		}
		count += (lineWidth + width - 1) / width
	}
	return count
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
