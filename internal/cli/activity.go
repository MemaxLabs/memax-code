package cli

import (
	"fmt"
	"sort"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

const maxTrackedCommandIDs = 4096

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
	commandIDOrder   []string
	commandStates    map[string]commandActivity
	commandSequence  int64
	lastTool         string
	lastCommand      string
	lastCommandState string
	lastPatch        string
	lastVerification string
	lastApproval     string
}

type commandActivity struct {
	ID            string
	Command       string
	Status        string
	PID           int
	ExitCode      int
	TimedOut      bool
	DurationMS    int
	OutputChunks  int
	DroppedChunks int
	DroppedBytes  int
	Seen          int64
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
	LastCommandState string
	LastPatch        string
	LastVerification string
	LastApproval     string
	ActiveTools      []string
	ActiveCommands   []commandActivity
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
	case memaxagent.EventCommandFinished, memaxagent.EventCommandStarted, memaxagent.EventCommandOutput, memaxagent.EventCommandInput, memaxagent.EventCommandResized, memaxagent.EventCommandStopped:
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
		LastCommandState: s.lastCommandState,
		LastPatch:        s.lastPatch,
		LastVerification: s.lastVerification,
		LastApproval:     s.lastApproval,
	}
	snapshot.ActiveTools = make([]string, len(s.activeTools))
	copy(snapshot.ActiveTools, s.activeTools)
	snapshot.ActiveCommands = s.activeCommandSnapshot()
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
	display := commandDisplay(event)
	if display == "" && event.Command.CommandID != "" {
		if command, ok := s.commandStates[event.Command.CommandID]; ok {
			display = command.Command
		}
	}
	if display != "" {
		s.lastCommand = display
	}
	if event.Command.CommandID == "" {
		s.commands++
		s.lastCommandState = commandActivityFromEvent(event, display).summary()
		return
	}
	if s.commandIDs == nil {
		s.commandIDs = make(map[string]struct{})
	}
	if _, ok := s.commandIDs[event.Command.CommandID]; !ok {
		s.commandIDs[event.Command.CommandID] = struct{}{}
		s.commandIDOrder = append(s.commandIDOrder, event.Command.CommandID)
		s.pruneCommandIDs()
		s.commands++
	}
	command := s.commandStates[event.Command.CommandID]
	next := commandActivityFromEvent(event, display)
	if command.Seen == 0 {
		s.commandSequence++
		next.Seen = s.commandSequence
	}
	command = command.merge(next)
	if s.commandStates == nil {
		s.commandStates = make(map[string]commandActivity)
	}
	s.lastCommandState = command.summary()
	if command.active() {
		s.commandStates[event.Command.CommandID] = command
		return
	}
	delete(s.commandStates, event.Command.CommandID)
}

func (s *activityState) pruneCommandIDs() {
	for len(s.commandIDOrder) > maxTrackedCommandIDs {
		removeAt := -1
		for i, id := range s.commandIDOrder {
			if _, active := s.commandStates[id]; !active {
				removeAt = i
				break
			}
		}
		if removeAt == -1 {
			return
		}
		id := s.commandIDOrder[removeAt]
		s.commandIDOrder = append(s.commandIDOrder[:removeAt], s.commandIDOrder[removeAt+1:]...)
		delete(s.commandIDs, id)
	}
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
	if s.LastCommandState != "" {
		details = append(details, fmt.Sprintf("last_command_status=%q", statusValue(s.LastCommandState)))
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

func (s *activityState) activeCommandSnapshot() []commandActivity {
	if len(s.commandStates) == 0 {
		return []commandActivity{}
	}
	commands := make([]commandActivity, 0, len(s.commandStates))
	for _, command := range s.commandStates {
		// commandStates should already contain only active entries; keep this
		// guard so stale state cannot leak into the rendered panel.
		if command.active() {
			commands = append(commands, command)
		}
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Seen != commands[j].Seen {
			return commands[i].Seen < commands[j].Seen
		}
		return commands[i].ID < commands[j].ID
	})
	return commands
}

func commandActivityFromEvent(event memaxagent.Event, display string) commandActivity {
	command := commandActivity{
		ID:            event.Command.CommandID,
		Command:       display,
		Status:        commandEventStatus(event),
		PID:           event.Command.PID,
		ExitCode:      event.Command.ExitCode,
		TimedOut:      event.Command.TimedOut,
		DurationMS:    event.Command.DurationMS,
		OutputChunks:  event.Command.OutputChunks,
		DroppedChunks: event.Command.DroppedChunks,
		DroppedBytes:  event.Command.DroppedBytes,
	}
	if command.Status == "" {
		command.Status = event.Command.Status
	}
	return command
}

func commandEventStatus(event memaxagent.Event) string {
	if event.Command == nil {
		return ""
	}
	switch event.Kind {
	case memaxagent.EventCommandStarted, memaxagent.EventCommandOutput, memaxagent.EventCommandInput, memaxagent.EventCommandResized:
		if event.Command.Status != "" {
			return event.Command.Status
		}
		return "running"
	case memaxagent.EventCommandStopped:
		if event.Command.Status != "" {
			return event.Command.Status
		}
		return "stopped"
	case memaxagent.EventCommandFinished:
		// A finished synchronous command only reports process metadata; the CLI
		// derives richer terminal states for status rendering.
		if event.Command.TimedOut {
			return "timed_out"
		}
		if event.Command.ExitCode != 0 {
			return "failed"
		}
		return "exited"
	default:
		return event.Command.Status
	}
}

func (c commandActivity) merge(next commandActivity) commandActivity {
	if next.ID != "" {
		c.ID = next.ID
	}
	if next.Command != "" {
		c.Command = next.Command
	}
	if next.Status != "" {
		c.Status = next.Status
	}
	if next.PID != 0 {
		c.PID = next.PID
	}
	if next.ExitCode != 0 || next.Status == "exited" || next.Status == "failed" || next.Status == "timed_out" {
		c.ExitCode = next.ExitCode
	}
	if next.TimedOut {
		c.TimedOut = true
	}
	if next.DurationMS != 0 {
		c.DurationMS = next.DurationMS
	}
	if next.OutputChunks != 0 {
		c.OutputChunks = next.OutputChunks
	}
	if next.DroppedChunks != 0 {
		c.DroppedChunks = next.DroppedChunks
	}
	if next.DroppedBytes != 0 {
		c.DroppedBytes = next.DroppedBytes
	}
	if c.Seen == 0 {
		c.Seen = next.Seen
	}
	return c
}

func (c commandActivity) active() bool {
	switch c.Status {
	case "running":
		return true
	default:
		return false
	}
}

func (c commandActivity) summary() string {
	parts := make([]string, 0, 8)
	if c.ID != "" {
		parts = append(parts, "id="+c.ID)
	}
	if c.Status != "" {
		parts = append(parts, "status="+c.Status)
	}
	if c.PID != 0 {
		parts = append(parts, fmt.Sprintf("pid=%d", c.PID))
	}
	if c.ExitCode != 0 || c.Status == "exited" || c.Status == "failed" || c.Status == "timed_out" {
		parts = append(parts, fmt.Sprintf("exit=%d", c.ExitCode))
	}
	if c.TimedOut {
		parts = append(parts, "timeout=true")
	}
	if c.DurationMS != 0 {
		parts = append(parts, fmt.Sprintf("duration=%dms", c.DurationMS))
	}
	if c.OutputChunks != 0 {
		parts = append(parts, fmt.Sprintf("chunks=%d", c.OutputChunks))
	}
	if c.DroppedChunks != 0 || c.DroppedBytes != 0 {
		parts = append(parts, fmt.Sprintf("dropped=%d/%dB", c.DroppedChunks, c.DroppedBytes))
	}
	if c.Command != "" {
		parts = append(parts, "command="+statusPanelValue(c.Command))
	}
	return strings.Join(parts, " ")
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
