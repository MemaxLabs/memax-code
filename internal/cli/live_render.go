package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

const clearLine = "\r\x1b[2K"

// Keep one terminal column free so the transient status does not trigger a
// soft-wrap on common 80-column terminals.
const defaultLiveStatusWidth = 79
const liveStatusTickInterval = 120 * time.Millisecond

var liveStatusFrames = [...]string{"-", "\\", "|", "/"}

type liveRenderState struct {
	transcript    tuiRenderState
	statusShown   bool
	statusWidth   int
	spinnerOffset int
	startedAt     time.Time
	now           func() time.Time
}

func (s *liveRenderState) Render(w io.Writer, event memaxagent.Event) error {
	s.markStarted()
	s.clearStatus(w)
	err := s.transcript.Render(w, event)
	s.drawStatus(w)
	return err
}

func (s *liveRenderState) Finish(w io.Writer) error {
	s.clearStatus(w)
	return s.transcript.Finish(w)
}

func (s *liveRenderState) Tick(w io.Writer) error {
	activity := s.transcript.activity.snapshot()
	if !s.canDrawStatus() || activity.ResultSeen || activity.TerminalError {
		return nil
	}
	s.markStarted()
	fmt.Fprint(w, clearLine)
	fmt.Fprint(w, s.statusLine(s.nextSpinnerFrame(), activity))
	s.statusShown = true
	return nil
}

func (s *liveRenderState) TickInterval() time.Duration {
	return liveStatusTickInterval
}

func (s *liveRenderState) clearStatus(w io.Writer) {
	if !s.statusShown {
		return
	}
	fmt.Fprint(w, clearLine)
	s.statusShown = false
}

func (s *liveRenderState) drawStatus(w io.Writer) {
	if !s.canDrawStatus() {
		return
	}
	activity := s.transcript.activity.snapshot()
	fmt.Fprint(w, clearLine)
	fmt.Fprint(w, s.statusLine("", activity))
	s.statusShown = true
}

func (s *liveRenderState) canDrawStatus() bool {
	return s.transcript.headerWritten && !s.transcript.assistantLineOpen
}

func (s *liveRenderState) nextSpinnerFrame() string {
	frame := liveStatusFrames[s.spinnerOffset%len(liveStatusFrames)]
	s.spinnerOffset++
	return frame
}

func (s *liveRenderState) markStarted() {
	if !s.startedAt.IsZero() {
		return
	}
	s.startedAt = s.currentTime()
}

func (s *liveRenderState) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *liveRenderState) statusLine(frame string, activity activitySnapshot) string {
	title := "Memax Code"
	if frame != "" {
		title += " " + frame
	}
	parts := []string{title, activity.Phase}
	if activity.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("tool_errors=%d", activity.ToolErrors))
	}
	if elapsed := s.elapsedStatus(); elapsed != "" {
		parts = append(parts, "elapsed="+elapsed)
	}
	if activity.ActiveTool != "" {
		parts = append(parts, "active="+statusValue(activity.ActiveTool))
	} else if activity.LastTool != "" {
		parts = append(parts, "last_tool="+statusValue(activity.LastTool))
	}
	if len(activity.ActiveCommands) > 0 {
		command := activity.ActiveCommands[0]
		label := command.ID
		if label == "" {
			label = command.Command
		}
		if label != "" {
			parts = append(parts, "active_cmd="+statusValue(label))
		}
	}
	duplicateActiveCommand := len(activity.ActiveCommands) > 0 && activity.ActiveCommands[0].Command == activity.LastCommand
	if activity.LastCommand != "" && !duplicateActiveCommand {
		parts = append(parts, "cmd="+statusValue(activity.LastCommand))
	}
	if activity.Approvals > 0 && activity.LastApproval != "" {
		parts = append(parts, "approval="+statusValue(activity.LastApproval))
	}
	if counts := activity.liveCountsLine(); counts != "" {
		parts = append(parts, counts)
	}
	if activity.Usage != "" {
		parts = append(parts, activity.Usage)
	}
	return truncateStatusLine(strings.Join(parts, " | "), s.width())
}

func (s *liveRenderState) elapsedStatus() string {
	if s.startedAt.IsZero() {
		return ""
	}
	elapsed := s.currentTime().Sub(s.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return formatElapsed(elapsed)
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

func formatElapsed(elapsed time.Duration) string {
	seconds := int(elapsed / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%02dm", hours, minutes)
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
