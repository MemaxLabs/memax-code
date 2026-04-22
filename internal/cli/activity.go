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

type activitySnapshot struct {
	Tools         int
	Commands      int
	Patches       int
	Verifications int
	Approvals     int
	ToolErrors    int

	SessionID     string
	Usage         string
	Phase         string
	ResultSeen    bool
	TerminalError bool

	ActiveTool       string
	LastTool         string
	LastCommand      string
	LastPatch        string
	LastVerification string
	LastApproval     string
	ActiveTools      []string
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

func (s *activityState) snapshot() activitySnapshot {
	snapshot := activitySnapshot{
		Tools:            s.tools,
		Commands:         s.commands,
		Patches:          s.patches,
		Verifications:    s.verifications,
		Approvals:        s.approvals,
		ToolErrors:       s.toolErrors,
		SessionID:        s.sessionID,
		Usage:            s.usage,
		ResultSeen:       s.resultSeen,
		TerminalError:    s.terminalError,
		ActiveTool:       s.activeTool,
		LastTool:         s.lastTool,
		LastCommand:      s.lastCommand,
		LastPatch:        s.lastPatch,
		LastVerification: s.lastVerification,
		LastApproval:     s.lastApproval,
	}
	snapshot.ActiveTools = make([]string, len(s.activeTools))
	copy(snapshot.ActiveTools, s.activeTools)
	snapshot.Phase = snapshot.phase()
	return snapshot
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

func (s activitySnapshot) phase() string {
	if s.TerminalError {
		return "error"
	}
	if s.ResultSeen {
		return "done"
	}
	return "running"
}

func (s activitySnapshot) countsLine() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tools=%d commands=%d patches=%d verifications=%d", s.Tools, s.Commands, s.Patches, s.Verifications)
	if s.Usage != "" {
		fmt.Fprintf(&b, " usage=%s", s.Usage)
	}
	if s.ResultSeen {
		b.WriteString(" done=true")
	}
	fmt.Fprintf(&b, " phase=%s", s.Phase)
	return b.String()
}

func (s activitySnapshot) liveCountsLine() string {
	var counts []string
	if s.Tools > 0 {
		counts = append(counts, fmt.Sprintf("tools=%d", s.Tools))
	}
	if s.Commands > 0 {
		counts = append(counts, fmt.Sprintf("commands=%d", s.Commands))
	}
	if s.Patches > 0 {
		counts = append(counts, fmt.Sprintf("patches=%d", s.Patches))
	}
	if s.Verifications > 0 {
		counts = append(counts, fmt.Sprintf("checks=%d", s.Verifications))
	}
	if len(counts) == 0 {
		return ""
	}
	return strings.Join(counts, " ")
}

func (s activitySnapshot) detailsLine() string {
	var details []string
	if s.ToolErrors > 0 {
		details = append(details, fmt.Sprintf("tool_errors=%d", s.ToolErrors))
	}
	if s.TerminalError {
		details = append(details, "error=true")
	}
	if s.Approvals > 0 {
		details = append(details, fmt.Sprintf("approval_events=%d", s.Approvals))
	}
	if s.LastTool != "" {
		details = append(details, fmt.Sprintf("last_tool=%q", statusValue(s.LastTool)))
	}
	if s.ActiveTool != "" {
		details = append(details, fmt.Sprintf("active_tool=%q", statusValue(s.ActiveTool)))
	}
	if s.LastCommand != "" {
		details = append(details, fmt.Sprintf("last_command=%q", statusValue(s.LastCommand)))
	}
	if s.LastPatch != "" {
		details = append(details, fmt.Sprintf("last_patch=%q", statusValue(s.LastPatch)))
	}
	if s.LastVerification != "" {
		details = append(details, fmt.Sprintf("last_verification=%q", statusValue(s.LastVerification)))
	}
	if s.LastApproval != "" {
		details = append(details, fmt.Sprintf("last_approval=%q", statusValue(s.LastApproval)))
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
