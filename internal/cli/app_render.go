package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

const (
	defaultAppShellWidth  = defaultLiveStatusWidth
	appShellTickInterval  = 120 * time.Millisecond
	maxAppTranscriptLines = 512
)

type appRenderState struct {
	formatter appTranscriptFormatter
	firstErr  error
}

func (s *appRenderState) Render(w io.Writer, event memaxagent.Event) error {
	lines, err := s.renderEvent(event)
	writeAppRenderLines(w, lines)
	return err
}

func (s *appRenderState) Finish(w io.Writer) error {
	s.formatter.flushPendingCommandGroups()
	s.formatter.flushTranscriptPartial()
	writeAppRenderLines(w, s.formatter.drainPendingPrints())
	return s.firstErr
}

func (s *appRenderState) Tick(w io.Writer) error {
	// The non-interactive app renderer has no animation work. It still exposes
	// a tick interval so renderWithTicksPollerObserved keeps polling Ctrl+C
	// while an event stream is quiet.
	return nil
}

func (s *appRenderState) TickInterval() time.Duration {
	return appShellTickInterval
}

func (s *appRenderState) HandleKey(w io.Writer, key rawKey) error {
	if key.kind == rawKeyCtrlC {
		return contextCanceled
	}
	return nil
}

func (s *appRenderState) renderEvent(event memaxagent.Event) ([]string, error) {
	if event.Kind == memaxagent.EventSessionStarted && event.SessionID != "" {
		s.formatter.appendLocalTranscriptLine("dim", "session "+event.SessionID)
		return s.formatter.drainPendingPrints(), nil
	}
	s.formatter.appendEvent(event)
	if event.Kind == memaxagent.EventError && event.Err != nil && s.firstErr == nil {
		s.firstErr = event.Err
	}
	return s.formatter.drainPendingPrints(), event.Err
}

func normalizeAppTranscriptText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return sanitizeTranscriptText(text)
}

func writeAppRenderLines(w io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

type appTranscriptTail struct {
	entries []string
	partial string
	limit   int
}

const appTranscriptBlankLine = "\x00memax-transcript-blank-line\x00"

func (t *appTranscriptTail) append(text string) []string {
	if text == "" {
		return nil
	}
	text = t.partial + text
	parts := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		t.partial = ""
	} else {
		t.partial = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	appended := make([]string, 0, len(parts))
	for _, line := range parts {
		if t.appendLine(line) {
			appended = append(appended, displayTranscriptLine(line))
		}
	}
	return appended
}

func (t *appTranscriptTail) appendStandaloneLine(line string) []string {
	// Local rows are inserted at prompt/session boundaries, not mid-assistant
	// stream. Flush first so local UI rows never glue onto streamed text.
	appended := t.flushPartial()
	if t.appendLine(line) {
		appended = append(appended, displayTranscriptLine(line))
	}
	return appended
}

func (t *appTranscriptTail) appendBlankLine() []string {
	appended := t.flushPartial()
	if len(t.entries) == 0 || t.entries[len(t.entries)-1] == "" {
		return appended
	}
	if t.appendLine(appTranscriptBlankLine) {
		appended = append(appended, "")
	}
	return appended
}

func (t *appTranscriptTail) flushPartial() []string {
	if strings.TrimSpace(t.partial) == "" {
		t.partial = ""
		return nil
	}
	partial := t.partial
	t.partial = ""
	if t.appendLine(partial) {
		return []string{partial}
	}
	return nil
}

func (t *appTranscriptTail) appendLine(line string) bool {
	if line == appTranscriptBlankLine {
		t.entries = append(t.entries, "")
		limit := t.effectiveLimit()
		if len(t.entries) > limit {
			t.entries = append([]string(nil), t.entries[len(t.entries)-limit:]...)
		}
		return true
	}
	if strings.TrimSpace(line) == "" {
		return false
	}
	t.entries = append(t.entries, line)
	limit := t.effectiveLimit()
	if len(t.entries) > limit {
		t.entries = append([]string(nil), t.entries[len(t.entries)-limit:]...)
	}
	return true
}

func displayTranscriptLine(line string) string {
	if line == appTranscriptBlankLine {
		return ""
	}
	return line
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
