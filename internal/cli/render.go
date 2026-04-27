package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/MemaxLabs/memax-code/internal/cli/ui"
	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"golang.org/x/term"
)

var contextCanceled = errors.New("render canceled")

type renderMode = ui.Mode

const (
	renderModeAuto  = ui.ModeAuto
	renderModeApp   = ui.ModeApp
	renderModeLive  = ui.ModeLive
	renderModeTUI   = ui.ModeStructured
	renderModePlain = ui.ModePlain
)

func parseRenderMode(raw string) (renderMode, error) {
	return ui.ParseMode(raw)
}

func renderEventsWithMode(w io.Writer, events <-chan memaxagent.Event, mode renderMode) error {
	return renderEventsWithModeObserved(w, events, mode, nil)
}

func renderEventsWithModeObserved(w io.Writer, events <-chan memaxagent.Event, mode renderMode, observe func(memaxagent.Event)) error {
	terminal, width, _ := terminalWriterInfo(w)
	mode = ui.ResolveMode(mode, terminal)
	renderer, err := ui.SelectRenderer(mode, ui.Renderers{
		Plain:      &renderState{},
		App:        &appRenderState{},
		Live:       &liveRenderState{statusWidth: width},
		Structured: &tuiRenderState{},
	})
	if err != nil {
		return err
	}
	return renderWithObserved(w, events, renderer, observe)
}

func terminalWriterInfo(w io.Writer) (bool, int, int) {
	file, ok := w.(*os.File)
	if !ok {
		return false, 0, 0
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return false, 0, 0
	}
	width, height, err := term.GetSize(fd)
	if err != nil {
		return true, 0, 0
	}
	if width <= 1 {
		width = 0
	} else {
		width--
	}
	if height <= 0 {
		height = 0
	}
	return true, width, height
}

func renderEvents(w io.Writer, events <-chan memaxagent.Event) error {
	return renderWith(w, events, &renderState{})
}

func renderWith(w io.Writer, events <-chan memaxagent.Event, renderer ui.Renderer) error {
	return renderWithObserved(w, events, renderer, nil)
}

func renderWithObserved(w io.Writer, events <-chan memaxagent.Event, renderer ui.Renderer, observe func(memaxagent.Event)) error {
	return renderWithTicksObserved(w, events, renderer, nil, observe)
}

type tickRenderer interface {
	ui.Renderer
	Tick(io.Writer) error
	TickInterval() time.Duration
}

func renderWithTicks(w io.Writer, events <-chan memaxagent.Event, renderer ui.Renderer, ticks <-chan time.Time) error {
	return renderWithTicksObserved(w, events, renderer, ticks, nil)
}

func renderWithTicksObserved(w io.Writer, events <-chan memaxagent.Event, renderer ui.Renderer, ticks <-chan time.Time, observe func(memaxagent.Event)) error {
	tickerRenderer, ticking := renderer.(tickRenderer)
	if !ticking {
		ticks = nil
	} else if ticks == nil {
		interval := tickerRenderer.TickInterval()
		if interval > 0 {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			ticks = ticker.C
		}
	}

	var firstErr error
	for {
		select {
		case event, ok := <-events:
			if !ok {
				if err := renderer.Finish(w); err != nil && firstErr == nil {
					firstErr = err
				}
				return firstErr
			}
			if observe != nil {
				observe(event)
			}
			if err := renderer.Render(w, event); err != nil && firstErr == nil {
				firstErr = err
			}
		case _, ok := <-ticks:
			if !ok {
				ticks = nil
				continue
			}
			if ticking {
				if err := tickerRenderer.Tick(w); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
}

type tuiRenderState struct {
	// liveRenderState layers transient status on top of this structured
	// transcript state, so these fields are part of that package-local contract.
	headerWritten     bool
	section           string
	assistantLineOpen bool
	activity          activityState
}

func (s *tuiRenderState) Render(w io.Writer, event memaxagent.Event) error {
	if !s.headerWritten {
		fmt.Fprintln(w, "Memax Code")
		fmt.Fprintln(w, "----------")
		s.headerWritten = true
	}
	if event.Kind != memaxagent.EventAssistant && event.Kind != memaxagent.EventToolUseDelta {
		s.closeAssistantLine(w)
	}
	// Normalize activity first; several render branches print derived status.
	s.activity.apply(event)
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		s.sectionLine(w, "session")
		fmt.Fprintf(w, "id: %s\n", event.SessionID)
	case memaxagent.EventAssistant:
		if event.Message != nil {
			text := event.Message.PlainText()
			if text != "" {
				s.sectionLine(w, "assistant")
				fmt.Fprint(w, text)
				s.assistantLineOpen = !strings.HasSuffix(text, "\n")
			}
		}
	case memaxagent.EventToolUseStart:
		if event.ToolUse != nil {
			s.renderActivity(w, "> tool "+event.ToolUse.Name)
		}
	case memaxagent.EventToolUseDelta:
		// The structured renderer waits for finalized tool-use events.
	case memaxagent.EventToolUse:
		if event.ToolUse != nil {
			s.renderActivity(w, "> tool "+event.ToolUse.Name+" call")
		}
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			content := strings.TrimSpace(event.ToolResult.Content)
			name := timelineToolName(event.ToolResult.Name)
			line := "< tool " + name + " ok"
			if event.ToolResult.IsError {
				line = "! tool " + name + " error"
				if content == "" {
					content = "<empty tool error>"
				}
			}
			s.renderActivity(w, line)
			if content != "" {
				if event.ToolResult.IsError {
					renderTimelineDetail(w, "error", content)
				} else {
					renderTimelineDetail(w, "result", content)
				}
			}
		}
	case memaxagent.EventContextApplied:
		// Keep routine context-selection events out of the human transcript.
		// Compaction events remain visible; machine-readable event streams still
		// expose both event kinds.
	case memaxagent.EventContextCompacted:
		if event.Compaction != nil {
			s.renderActivity(w, contextCompactionLine(event.Compaction.OriginalMessages, event.Compaction.SentMessages))
		}
	case memaxagent.EventWorkspaceCheckpoint:
		if event.Workspace != nil {
			s.renderActivity(w, "~ checkpoint "+event.Workspace.CheckpointID)
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			s.renderActivity(w, "~ patch "+workspaceSummary(event.Workspace))
		}
	case memaxagent.EventWorkspaceDiff:
		if event.Workspace != nil {
			s.renderActivity(w, "~ diff "+workspaceSummary(event.Workspace))
		}
	case memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			s.renderActivity(w, "~ restore "+event.Workspace.CheckpointID)
		}
	case memaxagent.EventCommandFinished:
		if event.Command != nil {
			s.renderActivity(w, timelineCommandFinishedLine(event))
		}
	case memaxagent.EventCommandStarted:
		if event.Command != nil {
			s.renderActivity(w, timelineCommandStartedLine(event))
		}
	case memaxagent.EventCommandOutput:
		if event.Command != nil {
			s.renderActivity(w, fmt.Sprintf("$ output %s chunks=%d next_seq=%d", event.Command.CommandID, event.Command.OutputChunks, event.Command.NextSeq))
		}
	case memaxagent.EventCommandInput:
		if event.Command != nil {
			s.renderActivity(w, fmt.Sprintf("$ input %s bytes=%d", event.Command.CommandID, event.Command.InputBytes))
		}
	case memaxagent.EventCommandResized:
		if event.Command != nil {
			s.renderActivity(w, fmt.Sprintf("$ resize %s cols=%d rows=%d", event.Command.CommandID, event.Command.Cols, event.Command.Rows))
		}
	case memaxagent.EventCommandStopped:
		if event.Command != nil {
			s.renderActivity(w, fmt.Sprintf("! command %s stopped status=%s", event.Command.CommandID, event.Command.Status))
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			prefix := "+"
			if !event.Verification.Passed {
				prefix = "!"
			}
			s.renderActivity(w, fmt.Sprintf("%s check %s passed=%t", prefix, event.Verification.Name, event.Verification.Passed))
		}
	case memaxagent.EventApprovalRequested:
		s.renderApproval(w, "requested", event.Approval)
	case memaxagent.EventApprovalGranted:
		s.renderApproval(w, "granted", event.Approval)
	case memaxagent.EventApprovalDenied:
		s.renderApproval(w, "denied", event.Approval)
	case memaxagent.EventApprovalConsumed:
		s.renderApproval(w, "consumed", event.Approval)
	case memaxagent.EventUsage:
		if event.Usage != nil {
			s.sectionLine(w, "usage")
			fmt.Fprintln(w, s.activity.snapshot().Usage)
		}
	case memaxagent.EventResult:
		if strings.TrimSpace(event.Result) != "" {
			s.sectionLine(w, "result")
			fmt.Fprintln(w, event.Result)
		}
	case memaxagent.EventError:
		if event.Err == nil {
			return fmt.Errorf("agent emitted error event")
		}
		s.sectionLine(w, "error")
		fmt.Fprintln(w, event.Err)
		return event.Err
	}
	return nil
}

func (s *tuiRenderState) renderApproval(w io.Writer, action string, approval *memaxagent.ApprovalEvent) {
	line := approvalLine(action, approval)
	if line == "" {
		return
	}
	prefix := approvalTimelinePrefix(action)
	s.renderActivity(w, prefix+" approval "+line)
}

func (s *tuiRenderState) renderActivity(w io.Writer, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	s.sectionLine(w, "activity")
	fmt.Fprintln(w, line)
}

func approvalTimelinePrefix(action string) string {
	switch action {
	case "requested":
		return "?"
	case "denied":
		return "!"
	default:
		return "+"
	}
}

func timelineToolName(name string) string {
	name = statusValue(name)
	if name == "" {
		return "<unknown>"
	}
	return name
}

func renderTimelineDetail(w io.Writer, label, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	lines := strings.Split(content, "\n")
	fmt.Fprintf(w, "  %s: %s\n", label, lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func timelineCommandStartedLine(event memaxagent.Event) string {
	parts := []string{"$ command"}
	if event.Command == nil {
		return strings.Join(parts, " ")
	}
	if event.Command.CommandID != "" {
		parts = append(parts, "id="+event.Command.CommandID)
	}
	if event.Command.PID != 0 {
		parts = append(parts, fmt.Sprintf("pid=%d", event.Command.PID))
	}
	if display := commandDisplay(event); display != "" {
		parts = append(parts, fmt.Sprintf("command=%q", display))
	}
	return strings.Join(parts, " ")
}

func timelineCommandFinishedLine(event memaxagent.Event) string {
	prefix := "+"
	if event.Command != nil && (event.Command.ExitCode != 0 || event.Command.TimedOut) {
		prefix = "!"
	}
	parts := []string{prefix, "command"}
	if event.Command == nil {
		return strings.Join(parts, " ")
	}
	if event.Command.CommandID != "" {
		parts = append(parts, "id="+event.Command.CommandID)
	}
	if display := commandDisplay(event); display != "" {
		parts = append(parts, fmt.Sprintf("command=%q", display))
	}
	parts = append(parts, fmt.Sprintf("exit=%d", event.Command.ExitCode), fmt.Sprintf("timeout=%t", event.Command.TimedOut))
	return strings.Join(parts, " ")
}

func (s *tuiRenderState) sectionLine(w io.Writer, section string) {
	if s.section == section {
		return
	}
	if s.section != "" {
		fmt.Fprintln(w)
	}
	s.section = section
	fmt.Fprintf(w, "[%s]\n", section)
}

func (s *tuiRenderState) closeAssistantLine(w io.Writer) {
	if s.assistantLineOpen {
		fmt.Fprintln(w)
		s.assistantLineOpen = false
	}
}

func (s *tuiRenderState) Finish(w io.Writer) error {
	if !s.headerWritten {
		return nil
	}
	s.closeAssistantLine(w)
	if s.section != "" {
		fmt.Fprintln(w)
	}
	activity := s.activity.snapshot()
	renderActivityStatus(w, activity)
	return nil
}

func renderActivityStatus(w io.Writer, activity activitySnapshot) {
	fmt.Fprintln(w, "[status]")
	fmt.Fprintf(w, "phase: %s\n", activity.Phase)
	if activity.SessionID != "" {
		fmt.Fprintf(w, "session: %s\n", activity.SessionID)
	}
	// Keep the compact key=value summary for grep-friendly transcript scans.
	fmt.Fprintln(w, "summary: "+activity.countsLine())
	if details := activity.detailsLine(); details != "" {
		// Keep the legacy detail line alongside the human-oriented panel rows.
		fmt.Fprintln(w, details)
	}
	if len(activity.ActiveTools) > 0 {
		fmt.Fprintln(w, "active_tools:")
		for _, name := range activity.ActiveTools {
			fmt.Fprintf(w, "  - %s\n", statusPanelValue(name))
		}
	}
	if len(activity.ActiveCommands) > 0 {
		fmt.Fprintln(w, "active_commands:")
		for _, command := range activity.ActiveCommands {
			fmt.Fprintf(w, "  - %s\n", statusPanelValue(command.summary()))
		}
	}
	if activity.LastCommand != "" || activity.LastCommandState != "" || activity.LastPatch != "" || activity.LastVerification != "" || activity.LastApproval != "" {
		fmt.Fprintln(w, "recent:")
		renderRecentStatus(w, "command", activity.LastCommand)
		renderRecentStatus(w, "command_status", activity.LastCommandState)
		renderRecentStatus(w, "patch", activity.LastPatch)
		renderRecentStatus(w, "verification", activity.LastVerification)
		renderRecentStatus(w, "approval", activity.LastApproval)
	}
}

func renderRecentStatus(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "  %s: %s\n", label, statusPanelValue(value))
}

func statusPanelValue(value string) string {
	return statusValue(strings.Join(strings.Fields(value), " "))
}

type renderState struct {
	assistantLineOpen bool
}

func (s *renderState) Render(w io.Writer, event memaxagent.Event) error {
	if event.Kind != memaxagent.EventAssistant && event.Kind != memaxagent.EventToolUseDelta {
		s.closeAssistantLine(w)
	}
	err := renderEvent(w, event)
	if event.Kind == memaxagent.EventAssistant && event.Message != nil {
		text := event.Message.PlainText()
		if text != "" {
			s.assistantLineOpen = !strings.HasSuffix(text, "\n")
		}
	}
	return err
}

func (s *renderState) Finish(io.Writer) error {
	return nil
}

func (s *renderState) closeAssistantLine(w io.Writer) {
	if s.assistantLineOpen {
		fmt.Fprintln(w)
		s.assistantLineOpen = false
	}
}

func renderEvent(w io.Writer, event memaxagent.Event) error {
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		fmt.Fprintf(w, "session: %s\n", event.SessionID)
	case memaxagent.EventAssistant:
		if event.Message != nil {
			text := event.Message.PlainText()
			fmt.Fprintf(w, "%s", text)
		}
	case memaxagent.EventToolUseStart:
		if event.ToolUse != nil {
			fmt.Fprintf(w, "\ntool_start: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolUseDelta:
		// Deltas arrive token-by-token and are useful for protocol observers,
		// not for the default terminal transcript. The finalized tool-use event
		// prints the actionable call.
	case memaxagent.EventToolUse:
		if event.ToolUse != nil {
			fmt.Fprintf(w, "\ntool: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			content := strings.TrimSpace(event.ToolResult.Content)
			label := "tool_result"
			if event.ToolResult.IsError {
				label = "tool_error"
				if content == "" {
					content = "<empty tool error>"
				}
			}
			if content != "" {
				fmt.Fprintf(w, "%s: %s\n", label, content)
			}
		}
	case memaxagent.EventWorkspaceCheckpoint:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_checkpoint: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_patch: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceDiff:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_diff: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_restore: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventCommandFinished:
		if event.Command != nil {
			fmt.Fprintf(w, "command: %s exit=%d timeout=%t\n", commandDisplay(event), event.Command.ExitCode, event.Command.TimedOut)
		}
	case memaxagent.EventCommandStarted:
		if event.Command != nil {
			fmt.Fprintf(w, "command_started: %s pid=%d\n", event.Command.CommandID, event.Command.PID)
		}
	case memaxagent.EventCommandOutput:
		if event.Command != nil {
			fmt.Fprintf(w, "command_output: %s chunks=%d next_seq=%d\n", event.Command.CommandID, event.Command.OutputChunks, event.Command.NextSeq)
		}
	case memaxagent.EventCommandInput:
		if event.Command != nil {
			fmt.Fprintf(w, "command_input: %s bytes=%d\n", event.Command.CommandID, event.Command.InputBytes)
		}
	case memaxagent.EventCommandResized:
		if event.Command != nil {
			fmt.Fprintf(w, "command_resized: %s cols=%d rows=%d\n", event.Command.CommandID, event.Command.Cols, event.Command.Rows)
		}
	case memaxagent.EventCommandStopped:
		if event.Command != nil {
			fmt.Fprintf(w, "command_stopped: %s status=%s\n", event.Command.CommandID, event.Command.Status)
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			fmt.Fprintf(w, "verification: %s passed=%t\n", event.Verification.Name, event.Verification.Passed)
		}
	case memaxagent.EventUsage:
		if event.Usage != nil {
			fmt.Fprintf(w, "usage: input=%d output=%d total=%d\n", event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.TotalTokens)
		}
	case memaxagent.EventContextApplied:
		if event.Context != nil {
			fmt.Fprintf(w, "context: messages=%d/%d\n", event.Context.SentMessages, event.Context.OriginalMessages)
		}
	case memaxagent.EventContextCompacted:
		if event.Compaction != nil {
			fmt.Fprintf(w, "context_compacted: summarized=%d sent=%d policy=%s\n", event.Compaction.SummarizedMessages, event.Compaction.SentMessages, event.Compaction.Policy)
		}
	case memaxagent.EventResult:
		if strings.TrimSpace(event.Result) != "" {
			fmt.Fprintf(w, "\nresult: %s\n", event.Result)
		}
	case memaxagent.EventError:
		if event.Err == nil {
			return fmt.Errorf("agent emitted error event")
		}
		return event.Err
	}
	return nil
}

func contextCompactionLine(original, sent int) string {
	if original > 0 && sent > 0 {
		return fmt.Sprintf("~ context compacted %d -> %d messages", original, sent)
	}
	return "~ context compacted"
}

func commandDisplay(event memaxagent.Event) string {
	if event.Command == nil {
		return ""
	}
	command := strings.TrimSpace(event.Command.Command)
	if command == "" {
		command = strings.Join(event.Command.Argv, " ")
	}
	return command
}
