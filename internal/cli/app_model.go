package cli

import (
	"fmt"
	"strings"
)

const (
	maxAppActiveCommands = 3
	maxAppRecentLines    = 4
)

type appShellFrame struct {
	Header           string
	Panels           []appShellPanel
	Footer           string
	Width            int
	Height           int
	Transcript       []string
	TranscriptOffset int
}

type appShellPanel struct {
	Title string
	Lines []string
}

func newAppShellFrame(activity activitySnapshot, transcript []string, width, height int, elapsed string) appShellFrame {
	return appShellFrame{
		Header:     appHeaderLine(activity, elapsed),
		Panels:     appPanels(activity),
		Footer:     "Ctrl+C cancel | /help commands | --ui tui for scrollback logs",
		Width:      width,
		Height:     height,
		Transcript: transcript,
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
	capacity := len(f.Panels)*3 + maxAppActiveCommands + maxAppRecentLines + 8
	if capacity < f.Height {
		capacity = f.Height
	}
	lines := make([]string, 0, capacity)
	rule := appRule(f.Width)
	lines = append(lines, f.Header, rule)
	for index, panel := range f.Panels {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "["+panel.Title+"]")
		if len(panel.Lines) == 0 {
			lines = append(lines, "  none")
			continue
		}
		for _, line := range panel.Lines {
			lines = append(lines, "  "+line)
		}
	}

	lines = append(lines, "", "[transcript]")
	// Tight terminals preserve status panels and footer first; transcript rows
	// may temporarily collapse until the app shell gets interactive scrolling.
	transcriptBudget := f.Height - len(lines) - 2
	if transcriptBudget < 0 {
		transcriptBudget = 0
	}
	lines = append(lines, newAppTranscriptViewport(f.Transcript, transcriptBudget, f.TranscriptOffset).Lines()...)
	lines = append(lines, rule, f.Footer)
	return fitFrameHeight(lines, f.Height)
}

func appRule(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat("-", width)
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
		// Marker rows need at least one content row between them; tiny panes
		// keep raw transcript rows instead of spending space on indicators.
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
