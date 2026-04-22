package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

const clearLine = "\r\x1b[2K"

// Keep one terminal column free so the transient status does not trigger a
// soft-wrap on common 80-column terminals.
const defaultLiveStatusWidth = 79

type liveRenderState struct {
	transcript  tuiRenderState
	statusShown bool
	statusWidth int
}

func (s *liveRenderState) Render(w io.Writer, event memaxagent.Event) error {
	s.clearStatus(w)
	err := s.transcript.Render(w, event)
	s.drawStatus(w)
	return err
}

func (s *liveRenderState) Finish(w io.Writer) error {
	s.clearStatus(w)
	return s.transcript.Finish(w)
}

func (s *liveRenderState) clearStatus(w io.Writer) {
	if !s.statusShown {
		return
	}
	fmt.Fprint(w, clearLine)
	s.statusShown = false
}

func (s *liveRenderState) drawStatus(w io.Writer) {
	if !s.transcript.headerWritten || s.transcript.assistantLineOpen {
		return
	}
	fmt.Fprint(w, clearLine)
	fmt.Fprint(w, s.statusLine())
	s.statusShown = true
}

func (s *liveRenderState) statusLine() string {
	activity := &s.transcript.activity
	parts := []string{"Memax Code", activity.phase()}
	if activity.activeTool != "" {
		parts = append(parts, "active="+statusValue(activity.activeTool))
	} else if activity.lastTool != "" {
		parts = append(parts, "last_tool="+statusValue(activity.lastTool))
	}
	if activity.lastCommand != "" {
		parts = append(parts, "cmd="+statusValue(activity.lastCommand))
	}
	if activity.approvals > 0 && activity.lastApproval != "" {
		parts = append(parts, "approval="+statusValue(activity.lastApproval))
	}
	if activity.usage != "" {
		parts = append(parts, activity.usage)
	}
	return truncateStatusLine(strings.Join(parts, " | "), s.width())
}

func (s *liveRenderState) width() int {
	if s.statusWidth > 0 {
		return s.statusWidth
	}
	return liveStatusWidth()
}

func liveStatusWidth() int {
	columns, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || columns <= 1 {
		return defaultLiveStatusWidth
	}
	return columns - 1
}

func truncateStatusLine(line string, width int) string {
	runes := []rune(line)
	if width <= 0 || len(runes) <= width {
		return line
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}
