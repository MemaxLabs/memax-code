package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

type renderMode string

const (
	renderModeAuto  renderMode = "auto"
	renderModeTUI   renderMode = "tui"
	renderModePlain renderMode = "plain"
)

func parseRenderMode(raw string) (renderMode, error) {
	switch mode := renderMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "", renderModeAuto:
		return renderModeAuto, nil
	case renderModeTUI, renderModePlain:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown ui %q (want one of: auto, tui, plain)", raw)
	}
}

func renderEventsWithMode(w io.Writer, events <-chan memaxagent.Event, mode renderMode) error {
	if mode == renderModeAuto {
		if isTerminalWriter(w) {
			mode = renderModeTUI
		} else {
			mode = renderModePlain
		}
	}
	if mode == renderModeTUI {
		return renderTUIEvents(w, events)
	}
	return renderEvents(w, events)
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func renderEvents(w io.Writer, events <-chan memaxagent.Event) error {
	var firstErr error
	state := renderState{}
	for event := range events {
		if err := state.render(w, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func renderTUIEvents(w io.Writer, events <-chan memaxagent.Event) error {
	var firstErr error
	state := tuiRenderState{}
	for event := range events {
		if err := state.render(w, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	state.finish(w)
	return firstErr
}

type tuiRenderState struct {
	headerWritten     bool
	section           string
	assistantLineOpen bool
	tools             int
	commands          int
	patches           int
	verifications     int
	approvalEvents    int
	toolErrors        int
	terminalError     bool
	sessionID         string
	usage             string
	resultSeen        bool
	activeTool        string
	activeTools       []string
	lastTool          string
	lastCommand       string
	lastPatch         string
	lastVerification  string
	lastApproval      string
}

func (s *tuiRenderState) render(w io.Writer, event memaxagent.Event) error {
	if !s.headerWritten {
		fmt.Fprintln(w, "Memax Code")
		fmt.Fprintln(w, "----------")
		s.headerWritten = true
	}
	if event.Kind != memaxagent.EventAssistant && event.Kind != memaxagent.EventToolUseDelta {
		s.closeAssistantLine(w)
	}
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		s.sessionID = event.SessionID
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
			s.tools++
			s.startActiveTool(event.ToolUse.Name)
			s.lastTool = event.ToolUse.Name
			s.sectionLine(w, "tool")
			fmt.Fprintf(w, "start: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolUseDelta:
		// The structured renderer waits for finalized tool-use events.
	case memaxagent.EventToolUse:
		if event.ToolUse != nil {
			s.ensureActiveTool(event.ToolUse.Name)
			s.lastTool = event.ToolUse.Name
			s.sectionLine(w, "tool")
			fmt.Fprintf(w, "call: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			s.finishActiveTool(event.ToolResult.Name)
			s.sectionLine(w, "tool")
			content := strings.TrimSpace(event.ToolResult.Content)
			label := "result"
			if event.ToolResult.IsError {
				label = "error"
				s.toolErrors++
				if content == "" {
					content = "<empty tool error>"
				}
			}
			if content == "" {
				fmt.Fprintf(w, "%s: %s\n", label, event.ToolResult.Name)
			} else {
				fmt.Fprintf(w, "%s: %s\n", label, content)
			}
		}
	case memaxagent.EventWorkspaceCheckpoint:
		if event.Workspace != nil {
			s.sectionLine(w, "workspace")
			fmt.Fprintf(w, "checkpoint: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			s.patches++
			s.lastPatch = workspaceSummary(event.Workspace)
			s.sectionLine(w, "workspace")
			fmt.Fprintf(w, "patch: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceDiff:
		if event.Workspace != nil {
			s.sectionLine(w, "workspace")
			fmt.Fprintf(w, "diff: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			s.sectionLine(w, "workspace")
			fmt.Fprintf(w, "restore: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventCommandFinished:
		if event.Command != nil {
			s.commands++
			s.lastCommand = commandDisplay(event)
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "%s exit=%d timeout=%t\n", commandDisplay(event), event.Command.ExitCode, event.Command.TimedOut)
		}
	case memaxagent.EventCommandStarted:
		if event.Command != nil {
			s.commands++
			s.lastCommand = commandDisplay(event)
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "started: %s pid=%d", event.Command.CommandID, event.Command.PID)
			if display := commandDisplay(event); display != "" {
				fmt.Fprintf(w, " command=%q", display)
			}
			fmt.Fprintln(w)
		}
	case memaxagent.EventCommandOutput:
		if event.Command != nil {
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "output: %s chunks=%d next_seq=%d\n", event.Command.CommandID, event.Command.OutputChunks, event.Command.NextSeq)
		}
	case memaxagent.EventCommandInput:
		if event.Command != nil {
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "input: %s bytes=%d\n", event.Command.CommandID, event.Command.InputBytes)
		}
	case memaxagent.EventCommandResized:
		if event.Command != nil {
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "resize: %s cols=%d rows=%d\n", event.Command.CommandID, event.Command.Cols, event.Command.Rows)
		}
	case memaxagent.EventCommandStopped:
		if event.Command != nil {
			s.sectionLine(w, "command")
			fmt.Fprintf(w, "stopped: %s status=%s\n", event.Command.CommandID, event.Command.Status)
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			s.verifications++
			s.lastVerification = event.Verification.Name
			s.sectionLine(w, "verification")
			fmt.Fprintf(w, "%s passed=%t\n", event.Verification.Name, event.Verification.Passed)
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
			s.usage = fmt.Sprintf("input=%d output=%d total=%d", event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.TotalTokens)
			s.sectionLine(w, "usage")
			fmt.Fprintln(w, s.usage)
		}
	case memaxagent.EventResult:
		if strings.TrimSpace(event.Result) != "" {
			s.resultSeen = true
			s.sectionLine(w, "result")
			fmt.Fprintln(w, event.Result)
		}
	case memaxagent.EventError:
		if event.Err == nil {
			return fmt.Errorf("agent emitted error event")
		}
		s.terminalError = true
		s.sectionLine(w, "error")
		fmt.Fprintln(w, event.Err)
		return event.Err
	}
	return nil
}

func (s *tuiRenderState) startActiveTool(name string) {
	if name == "" {
		return
	}
	s.activeTools = append(s.activeTools, name)
	s.activeTool = name
}

func (s *tuiRenderState) ensureActiveTool(name string) {
	if name == "" {
		return
	}
	for _, active := range s.activeTools {
		if active == name {
			return
		}
	}
	if s.activeTool == name {
		return
	}
	s.startActiveTool(name)
}

func (s *tuiRenderState) finishActiveTool(name string) {
	if name == "" {
		return
	}
	for i := len(s.activeTools) - 1; i >= 0; i-- {
		if s.activeTools[i] == name {
			s.activeTools = append(s.activeTools[:i], s.activeTools[i+1:]...)
			break
		}
	}
	if s.activeTool != name {
		return
	}
	s.activeTool = ""
	if len(s.activeTools) > 0 {
		s.activeTool = s.activeTools[len(s.activeTools)-1]
	}
}

func (s *tuiRenderState) renderApproval(w io.Writer, action string, approval *memaxagent.ApprovalEvent) {
	if approval == nil {
		return
	}
	s.approvalEvents++
	s.lastApproval = action
	if approval.Action != "" {
		s.lastApproval = action + ":" + approval.Action
	} else if approval.Summary.Title != "" {
		s.lastApproval = action + ":" + approval.Summary.Title
	}
	s.sectionLine(w, "approval")
	if approval.Action == "" {
		if approval.Summary.Title != "" {
			fmt.Fprintf(w, "%s: title=%q\n", action, approval.Summary.Title)
			return
		}
		fmt.Fprintf(w, "%s\n", action)
		return
	}
	if approval.Summary.Title != "" {
		fmt.Fprintf(w, "%s: %s title=%q\n", action, approval.Action, approval.Summary.Title)
		return
	}
	fmt.Fprintf(w, "%s: %s\n", action, approval.Action)
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

func (s *tuiRenderState) finish(w io.Writer) {
	if !s.headerWritten {
		return
	}
	s.closeAssistantLine(w)
	if s.section != "" {
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "[status]")
	if s.sessionID != "" {
		fmt.Fprintf(w, "session: %s\n", s.sessionID)
	}
	fmt.Fprintf(w, "tools=%d commands=%d patches=%d verifications=%d", s.tools, s.commands, s.patches, s.verifications)
	if s.usage != "" {
		fmt.Fprintf(w, " usage=%s", s.usage)
	}
	if s.resultSeen {
		fmt.Fprint(w, " done=true")
	}
	fmt.Fprintf(w, " phase=%s", s.phase())
	fmt.Fprintln(w)
	s.printStatusDetails(w)
}

func (s *tuiRenderState) printStatusDetails(w io.Writer) {
	var details []string
	if s.toolErrors > 0 {
		details = append(details, fmt.Sprintf("tool_errors=%d", s.toolErrors))
	}
	if s.terminalError {
		details = append(details, "error=true")
	}
	if s.approvalEvents > 0 {
		details = append(details, fmt.Sprintf("approval_events=%d", s.approvalEvents))
	}
	if s.lastTool != "" {
		details = append(details, fmt.Sprintf("last_tool=%q", statusValue(s.lastTool)))
	}
	if s.activeTool != "" {
		details = append(details, fmt.Sprintf("active_tool=%q", statusValue(s.activeTool)))
	}
	if s.lastCommand != "" {
		details = append(details, fmt.Sprintf("last_command=%q", statusValue(s.lastCommand)))
	}
	if s.lastPatch != "" {
		details = append(details, fmt.Sprintf("last_patch=%q", statusValue(s.lastPatch)))
	}
	if s.lastVerification != "" {
		details = append(details, fmt.Sprintf("last_verification=%q", statusValue(s.lastVerification)))
	}
	if s.lastApproval != "" {
		details = append(details, fmt.Sprintf("last_approval=%q", statusValue(s.lastApproval)))
	}
	if len(details) == 0 {
		return
	}
	fmt.Fprintf(w, "activity: %s\n", strings.Join(details, " "))
}

func workspaceSummary(event *memaxagent.WorkspaceEvent) string {
	if event == nil {
		return ""
	}
	switch len(event.Paths) {
	case 0:
		return fmt.Sprintf("paths=0 changes=%d", event.Changes)
	case 1:
		return fmt.Sprintf("%s changes=%d", event.Paths[0], event.Changes)
	default:
		return fmt.Sprintf("paths=%d first=%s changes=%d", len(event.Paths), event.Paths[0], event.Changes)
	}
}

func statusValue(value string) string {
	const max = 80
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func (s *tuiRenderState) phase() string {
	if s.terminalError {
		return "error"
	}
	if s.resultSeen {
		return "done"
	}
	return "running"
}

type renderState struct {
	assistantLineOpen bool
}

func (s *renderState) render(w io.Writer, event memaxagent.Event) error {
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
