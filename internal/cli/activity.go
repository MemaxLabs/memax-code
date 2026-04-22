package cli

import (
	"fmt"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

type activityState struct {
	tools         int
	commands      int
	patches       int
	verifications int
	approvals     int
	toolErrors    int

	sessionID     string
	usage         string
	resultSeen    bool
	terminalError bool

	activeTool       string
	activeTools      []string
	commandIDs       map[string]struct{}
	lastTool         string
	lastCommand      string
	lastPatch        string
	lastVerification string
	lastApproval     string
}

func (s *activityState) apply(event memaxagent.Event) {
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		s.sessionID = event.SessionID
	case memaxagent.EventToolUseStart:
		if event.ToolUse != nil {
			s.tools++
			s.startActiveTool(event.ToolUse.Name)
			s.lastTool = event.ToolUse.Name
		}
	case memaxagent.EventToolUse:
		if event.ToolUse != nil {
			s.ensureActiveTool(event.ToolUse.Name)
			s.lastTool = event.ToolUse.Name
		}
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			s.finishActiveTool(event.ToolResult.Name)
			if event.ToolResult.IsError {
				s.toolErrors++
			}
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			s.patches++
			s.lastPatch = workspaceSummary(event.Workspace)
		}
	case memaxagent.EventCommandFinished:
		if event.Command != nil {
			s.observeCommand(event)
		}
	case memaxagent.EventCommandStarted:
		if event.Command != nil {
			s.observeCommand(event)
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			s.verifications++
			s.lastVerification = event.Verification.Name
		}
	case memaxagent.EventApprovalRequested:
		s.observeApproval("requested", event.Approval)
	case memaxagent.EventApprovalGranted:
		s.observeApproval("granted", event.Approval)
	case memaxagent.EventApprovalDenied:
		s.observeApproval("denied", event.Approval)
	case memaxagent.EventApprovalConsumed:
		s.observeApproval("consumed", event.Approval)
	case memaxagent.EventUsage:
		if event.Usage != nil {
			s.usage = fmt.Sprintf("input=%d output=%d total=%d", event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.TotalTokens)
		}
	case memaxagent.EventResult:
		if strings.TrimSpace(event.Result) != "" {
			s.resultSeen = true
		}
	case memaxagent.EventError:
		if event.Err != nil {
			s.terminalError = true
		}
	}
}

func (s *activityState) startActiveTool(name string) {
	if name == "" {
		return
	}
	s.activeTools = append(s.activeTools, name)
	s.activeTool = name
}

func (s *activityState) ensureActiveTool(name string) {
	if name == "" {
		return
	}
	for _, active := range s.activeTools {
		if active == name {
			return
		}
	}
	s.startActiveTool(name)
}

func (s *activityState) finishActiveTool(name string) {
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

func (s *activityState) observeApproval(action string, approval *memaxagent.ApprovalEvent) {
	if approval == nil {
		return
	}
	s.approvals++
	s.lastApproval = approvalActivity(action, approval)
}

func (s *activityState) observeCommand(event memaxagent.Event) {
	if event.Command == nil {
		return
	}
	s.lastCommand = commandDisplay(event)
	if event.Command.CommandID == "" {
		s.commands++
		return
	}
	if s.commandIDs == nil {
		s.commandIDs = make(map[string]struct{})
	}
	if _, ok := s.commandIDs[event.Command.CommandID]; ok {
		return
	}
	s.commandIDs[event.Command.CommandID] = struct{}{}
	s.commands++
}

func (s *activityState) phase() string {
	if s.terminalError {
		return "error"
	}
	if s.resultSeen {
		return "done"
	}
	return "running"
}

func (s *activityState) countsLine() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tools=%d commands=%d patches=%d verifications=%d", s.tools, s.commands, s.patches, s.verifications)
	if s.usage != "" {
		fmt.Fprintf(&b, " usage=%s", s.usage)
	}
	if s.resultSeen {
		b.WriteString(" done=true")
	}
	fmt.Fprintf(&b, " phase=%s", s.phase())
	return b.String()
}

func (s *activityState) detailsLine() string {
	var details []string
	if s.toolErrors > 0 {
		details = append(details, fmt.Sprintf("tool_errors=%d", s.toolErrors))
	}
	if s.terminalError {
		details = append(details, "error=true")
	}
	if s.approvals > 0 {
		details = append(details, fmt.Sprintf("approval_events=%d", s.approvals))
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
		return ""
	}
	return "activity: " + strings.Join(details, " ")
}

func approvalActivity(action string, approval *memaxagent.ApprovalEvent) string {
	if approval == nil {
		return ""
	}
	if approval.Action != "" {
		return action + ":" + approval.Action
	}
	if approval.Summary.Title != "" {
		return action + ":" + approval.Summary.Title
	}
	return action
}

func approvalLine(action string, approval *memaxagent.ApprovalEvent) string {
	if approval == nil {
		return ""
	}
	if approval.Action == "" {
		if approval.Summary.Title != "" {
			return fmt.Sprintf("%s: title=%q", action, approval.Summary.Title)
		}
		return action
	}
	if approval.Summary.Title != "" {
		return fmt.Sprintf("%s: %s title=%q", action, approval.Action, approval.Summary.Title)
	}
	return fmt.Sprintf("%s: %s", action, approval.Action)
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
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
