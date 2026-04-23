package cli

import (
	"fmt"
	"io"
	"strings"
)

type interactiveInputObserver interface {
	ObservePrompt(prompt string, buffer composerBuffer, composer *interactiveComposer)
}

type appInteractiveShell struct {
	out         io.Writer
	width       int
	height      int
	transcript  appTranscriptTail
	sessionID   string
	promptLabel string
	promptText  string
	draftStatus string
}

func newAppInteractiveShell(out io.Writer) *appInteractiveShell {
	_, width, height := terminalWriterInfo(out)
	return &appInteractiveShell{
		out:    out,
		width:  width,
		height: height,
	}
}

func (s *appInteractiveShell) Write(p []byte) (int, error) {
	s.appendTranscript(string(p))
	s.redraw()
	return len(p), nil
}

func (s *appInteractiveShell) ObservePrompt(prompt string, buffer composerBuffer, composer *interactiveComposer) {
	s.promptLabel = strings.TrimSpace(prompt)
	s.promptText = buffer.Text()
	if composer != nil {
		s.draftStatus = composer.statusLine()
	} else {
		s.draftStatus = ""
	}
	s.redraw()
}

func (s *appInteractiveShell) SetSession(sessionID string) {
	s.sessionID = strings.TrimSpace(sessionID)
	s.redraw()
}

func (s *appInteractiveShell) appendTranscript(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s.transcript.append(normalizeAppTranscriptText(text))
}

func (s *appInteractiveShell) redraw() {
	if s.out == nil {
		return
	}
	frame := newAppShellFrame(activitySnapshot{Phase: "idle"}, s.transcript.lines(maxAppTranscriptLines), s.panelWidth(), s.panelHeight(), "")
	frame.Panels = []appShellPanel{s.sessionPanel(), s.composerPanel()}
	frame.TranscriptStatus = "interactive shell"
	fmt.Fprint(s.out, appClearScreen)
	for _, line := range frame.Lines() {
		fmt.Fprintf(s.out, "\r%s\n", fitPanelLine(line, s.panelWidth()))
	}
}

func (s *appInteractiveShell) panelWidth() int {
	if s.width > 0 {
		return s.width
	}
	return defaultAppShellWidth
}

func (s *appInteractiveShell) panelHeight() int {
	if s.height > 0 {
		return s.height
	}
	return defaultAppShellHeight
}

func (s *appInteractiveShell) sessionPanel() appShellPanel {
	line := "id: none"
	if s.sessionID != "" {
		line = "id: " + s.sessionID
	}
	return appShellPanel{
		Title: "session",
		Lines: []string{line},
	}
}

func (s *appInteractiveShell) composerPanel() appShellPanel {
	lines := []string{"prompt: " + valueOrDefault(s.promptLabel, "memax>")}
	if s.draftStatus != "" {
		lines = append(lines, s.draftStatus)
	}
	if strings.TrimSpace(s.promptText) == "" {
		lines = append(lines, "input: empty")
		return appShellPanel{Title: "composer", Lines: lines}
	}
	summary := strings.ReplaceAll(strings.TrimSpace(s.promptText), "\n", " ⏎ ")
	lines = append(lines, "input: "+summarizeComposerHistoryText(summary))
	return appShellPanel{Title: "composer", Lines: lines}
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
