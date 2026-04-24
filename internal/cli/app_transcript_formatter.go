package cli

import (
	"fmt"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/charmbracelet/lipgloss"
)

const maxAppCommandGroupChildren = 6

type appTranscriptFormatter struct {
	transcript             appTranscriptTail
	compactor              appProgramTranscriptCompactor
	pending                []string
	pendingToolsByID       map[string]*model.ToolUse
	pendingToolsByName     map[string][]*model.ToolUse
	pendingCommands        map[string]*appProgramCommandGroup
	pendingCommandOrder    []string
	pendingCommandID       map[string]string
	pendingCommandFallback map[string][]string
	flushedCommandKeys     map[string]bool
	renderedCommandToolIDs map[string]bool
	renderedToolKeys       map[string]bool
	renderedToolDisplays   map[string]string
	lastActivityCommandKey string
	lastActivityToolKey    string
	flushedAnonymous       bool
	liveCommandGroups      bool
	nextCommandGroup       int
}

func (f *appTranscriptFormatter) appendTranscript(text string) {
	compacted := f.compactor.compact(text)
	if compacted == "" {
		return
	}
	f.queuePrints(f.transcript.append(compacted))
}

func (f *appTranscriptFormatter) appendEvent(event memaxagent.Event) {
	switch event.Kind {
	case memaxagent.EventAssistant:
		if event.Message != nil {
			f.appendAssistantText(event.Message.PlainText())
		}
	case memaxagent.EventToolUse:
		f.appendToolUse(event.ToolUse)
	case memaxagent.EventToolResult:
		f.appendToolResult(event.ToolResult)
	case memaxagent.EventWorkspaceCheckpoint:
		if event.Workspace != nil {
			f.appendActivityLine(appProgramDimStyle.Render("~ checkpoint " + event.Workspace.CheckpointID))
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			f.appendActivityLine(appProgramDimStyle.Render("~ patch " + workspaceSummary(event.Workspace)))
		}
	case memaxagent.EventWorkspaceDiff:
		if event.Workspace != nil {
			f.appendActivityLine(appProgramDimStyle.Render("~ diff " + workspaceSummary(event.Workspace)))
		}
	case memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			f.appendActivityLine(appProgramDimStyle.Render("~ restore " + event.Workspace.CheckpointID))
		}
	case memaxagent.EventCommandStarted, memaxagent.EventCommandFinished, memaxagent.EventCommandOutput,
		memaxagent.EventCommandInput, memaxagent.EventCommandResized, memaxagent.EventCommandStopped:
		f.appendCommandEvent(event)
	case memaxagent.EventVerification:
		if event.Verification != nil {
			line := fmt.Sprintf("check %s passed=%t", event.Verification.Name, event.Verification.Passed)
			if event.Verification.Passed {
				f.appendActivityLine(appProgramSuccessStyle.Render("✓ " + line))
			} else {
				f.appendActivityLine(appProgramErrorStyle.Render("! " + line))
			}
		}
	case memaxagent.EventApprovalRequested:
		f.appendApprovalEvent("requested", event.Approval)
	case memaxagent.EventApprovalGranted:
		f.appendApprovalEvent("granted", event.Approval)
	case memaxagent.EventApprovalDenied:
		f.appendApprovalEvent("denied", event.Approval)
	case memaxagent.EventApprovalConsumed:
		f.appendApprovalEvent("consumed", event.Approval)
	case memaxagent.EventError:
		if event.Err != nil {
			f.appendLocalTranscriptLine("error", "error: "+event.Err.Error())
		}
	case memaxagent.EventSessionStarted, memaxagent.EventResult, memaxagent.EventUsage, memaxagent.EventToolUseStart, memaxagent.EventToolUseDelta:
		// The app transcript renders assistant text, concrete tool execution,
		// approvals, workspace updates, and errors. Session/result/usage/delta
		// events are status metadata for this surface.
	}
}

func (f *appTranscriptFormatter) appendAssistantText(text string) {
	if text == "" {
		return
	}
	f.flushUnprintedCommandGroups()
	f.lastActivityCommandKey = ""
	f.lastActivityToolKey = ""
	if f.compactor.section != "assistant" {
		f.appendTranscriptSpacer()
	}
	f.queueCompactorFlush(f.compactor.startSection("assistant"))
	f.appendTranscript(text)
}

func (f *appTranscriptFormatter) appendActivityLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	f.flushUnprintedCommandGroups()
	f.flushTranscriptPartial()
	f.compactor.resetSection()
	f.lastActivityCommandKey = ""
	f.lastActivityToolKey = ""
	f.queuePrints(f.transcript.appendStandaloneLine(line))
}

func (f *appTranscriptFormatter) appendToolUse(toolUse *model.ToolUse) {
	if toolUse == nil {
		return
	}
	alreadyRendered := f.pendingToolIDAlreadyRendered(toolUse.ID)
	f.storePendingTool(toolUse)
	if appToolUseDefersToCommandLifecycle(toolUse.Name) {
		if !alreadyRendered {
			f.appendImmediateCommandToolUse(toolUse)
		}
		return
	}
	if alreadyRendered {
		return
	}
	f.printUnprintedCommandGroups()
	display := appToolUseDisplay(toolUse)
	f.appendToolBlock(f.toolUseKey(toolUse), appProgramToolStyle.Render("• "+display))
	f.markToolUseRendered(toolUse)
}

func (f *appTranscriptFormatter) appendToolResult(result *model.ToolResult) {
	if result == nil {
		return
	}
	f.printUnprintedCommandGroups()
	toolUse := f.takePendingTool(result.ToolUseID, result.Name)
	rendered, renderedKey, renderedDisplay := f.consumeRenderedToolUse(toolUse, result.ToolUseID)
	name := appToolDisplayName(result.Name)
	if result.IsError {
		lines := make([]string, 0, 2)
		if !rendered && toolUse == nil {
			lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplayOrName(nil, name)))
		} else if !rendered {
			lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplay(toolUse)))
		}
		lines = append(lines, appProgramErrorStyle.Render("  └ error"))
		lines = append(lines, appActivityTailLines("error", appProgramErrorStyle, result.Content)...)
		if rendered {
			f.appendToolResultLines(renderedKey, renderedDisplay, true, lines...)
		} else {
			f.appendActivityGroup(lines...)
		}
		return
	}
	if appToolResultIsRedundant(result.Name) {
		if !appToolResultHasCommandLifecycleMetadata(result) {
			if f.consumeRenderedCommandToolUse(toolUse, name) {
				return
			}
			lines := make([]string, 0, 1)
			if toolUse == nil {
				lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplayOrName(nil, name)))
			} else {
				lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplay(toolUse)))
			}
			f.appendActivityGroup(lines...)
		} else {
			f.consumeRenderedCommandToolUse(toolUse, name)
		}
		return
	}
	lines := make([]string, 0, 2)
	if !rendered && toolUse == nil {
		lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplayOrName(nil, name)))
	} else if !rendered {
		lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplay(toolUse)))
	}
	lines = append(lines, appProgramDimStyle.Render("  └ ok"))
	if appToolShowsResultTail(result.Name) {
		lines = append(lines, appActivityTailLines("output", appProgramDimStyle, result.Content)...)
	}
	if rendered {
		f.appendToolResultLines(renderedKey, renderedDisplay, false, lines...)
	} else {
		f.appendActivityGroup(lines...)
	}
}

func (f *appTranscriptFormatter) appendCommandEvent(event memaxagent.Event) {
	if event.Command == nil {
		return
	}
	f.appendGroupedCommandEvent(event)
}

func (f *appTranscriptFormatter) appendImmediateCommandToolUse(toolUse *model.ToolUse) bool {
	if toolUse == nil {
		return false
	}
	operation := ""
	switch toolUse.Name {
	case "run_command":
		operation = "run"
	case "start_command":
		operation = "start"
	default:
		return false
	}
	command := appToolUseCommand(toolUse)
	if command == "" {
		return false
	}
	commandEvent := &memaxagent.CommandEvent{
		Operation: operation,
		Command:   command,
	}
	key := f.startCommandGroupKey(commandEvent)
	f.ensurePendingCommands()
	group := f.pendingCommandGroup(key, commandEvent)
	if group.printed {
		return true
	}
	if f.liveCommandGroups {
		f.markCommandGroupRendered(group)
		return true
	}
	f.appendCommandBlock(key, group.renderHeader()...)
	f.markCommandGroupRendered(group)
	group.printed = true
	return true
}

func (f *appTranscriptFormatter) appendApprovalEvent(action string, approval *memaxagent.ApprovalEvent) {
	line := approvalLine(action, approval)
	if line == "" {
		return
	}
	prefix := approvalTimelinePrefix(action)
	style := appProgramSuccessStyle
	if prefix == "?" {
		style = appProgramAccentStyle
	} else if prefix == "!" {
		style = appProgramErrorStyle
		prefix = "!"
	} else {
		prefix = "✓"
	}
	f.appendActivityLine(style.Render(prefix + " approval " + line))
}

func (f *appTranscriptFormatter) appendActivityDetail(label string, style lipgloss.Style, content string) {
	f.appendActivityGroup(appActivityTailLines(label, style, content)...)
}

func (f *appTranscriptFormatter) queueCompactorFlush(text string) {
	if text == "" {
		return
	}
	f.queuePrints(f.transcript.append(text))
}

func (f *appTranscriptFormatter) appendLocalTranscriptLine(kind, text string) {
	kind = strings.TrimSpace(kind)
	text = strings.TrimSpace(normalizeAppTranscriptText(text))
	if kind == "" || text == "" {
		return
	}
	f.flushPendingCommandGroups()
	f.queuePrints(f.transcript.append(f.compactor.flush()))
	f.compactor.resetSection()
	f.lastActivityCommandKey = ""
	f.lastActivityToolKey = ""
	if kind == "user" {
		f.appendTranscriptSpacer()
	}
	f.queuePrints(f.transcript.appendStandaloneLine(compactAppProgramLocalLine(kind, text)))
	if kind == "user" {
		f.appendTranscriptSpacer()
	}
}

func (f *appTranscriptFormatter) appendActivityGroup(lines ...string) {
	f.flushTranscriptPartial()
	f.compactor.resetSection()
	f.lastActivityCommandKey = ""
	f.lastActivityToolKey = ""
	f.appendTranscriptSpacer()
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f.queuePrints(f.transcript.appendStandaloneLine(line))
	}
}

func (f *appTranscriptFormatter) appendTranscriptSpacer() {
	f.queuePrints(f.transcript.appendBlankLine())
}

func (f *appTranscriptFormatter) appendCommandActivityGroup(key string, lines ...string) {
	f.flushTranscriptPartial()
	f.compactor.resetSection()
	f.lastActivityToolKey = ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f.queuePrints(f.transcript.appendStandaloneLine(line))
	}
	f.lastActivityCommandKey = key
}

func (f *appTranscriptFormatter) appendCommandBlock(key string, lines ...string) {
	f.appendTranscriptSpacer()
	f.appendCommandActivityGroup(key, lines...)
}

func (f *appTranscriptFormatter) appendToolBlock(key string, lines ...string) {
	f.appendTranscriptSpacer()
	f.appendToolActivityGroup(key, lines...)
}

func (f *appTranscriptFormatter) appendToolResultLines(key, display string, isError bool, lines ...string) {
	if len(lines) == 0 {
		return
	}
	if strings.TrimSpace(key) == "" || f.lastActivityToolKey != key {
		f.appendTranscriptSpacer()
		if display = strings.TrimSpace(display); display != "" {
			f.queuePrints(f.transcript.appendStandaloneLine(appProgramToolStyle.Render("• " + appToolContinuationDisplay(display, isError))))
		}
	}
	f.appendToolActivityGroup(key, lines...)
}

func (f *appTranscriptFormatter) appendToolActivityGroup(key string, lines ...string) {
	f.flushTranscriptPartial()
	f.compactor.resetSection()
	f.lastActivityCommandKey = ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f.queuePrints(f.transcript.appendStandaloneLine(line))
	}
	f.lastActivityToolKey = key
}

func (f *appTranscriptFormatter) storePendingTool(toolUse *model.ToolUse) {
	if toolUse == nil {
		return
	}
	if f.pendingToolsByID == nil {
		f.pendingToolsByID = make(map[string]*model.ToolUse)
	}
	if f.pendingToolsByName == nil {
		f.pendingToolsByName = make(map[string][]*model.ToolUse)
	}
	if id := strings.TrimSpace(toolUse.ID); id != "" {
		if existing := f.pendingToolsByID[id]; existing != nil {
			f.removePendingTool(existing)
		}
		f.pendingToolsByID[id] = toolUse
	}
	name := strings.TrimSpace(toolUse.Name)
	if name != "" {
		f.pendingToolsByName[name] = append(f.pendingToolsByName[name], toolUse)
	}
}

func (f *appTranscriptFormatter) takePendingTool(id, name string) *model.ToolUse {
	if id = strings.TrimSpace(id); id != "" && len(f.pendingToolsByID) > 0 {
		if toolUse := f.pendingToolsByID[id]; toolUse != nil {
			f.removePendingTool(toolUse)
			return toolUse
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || len(f.pendingToolsByName) == 0 {
		return nil
	}
	queue := f.pendingToolsByName[name]
	for len(queue) > 0 {
		toolUse := queue[0]
		queue = queue[1:]
		f.pendingToolsByName[name] = queue
		if toolUse != nil {
			if id := strings.TrimSpace(toolUse.ID); id != "" {
				delete(f.pendingToolsByID, id)
			}
			if len(queue) == 0 {
				delete(f.pendingToolsByName, name)
			}
			return toolUse
		}
	}
	delete(f.pendingToolsByName, name)
	return nil
}

func (f *appTranscriptFormatter) removePendingTool(target *model.ToolUse) {
	if target == nil {
		return
	}
	if id := strings.TrimSpace(target.ID); id != "" {
		delete(f.pendingToolsByID, id)
	}
	name := strings.TrimSpace(target.Name)
	if name == "" || len(f.pendingToolsByName) == 0 {
		return
	}
	queue := f.pendingToolsByName[name]
	for i, toolUse := range queue {
		if toolUse == target {
			queue = append(queue[:i], queue[i+1:]...)
			break
		}
	}
	if len(queue) == 0 {
		delete(f.pendingToolsByName, name)
	} else {
		f.pendingToolsByName[name] = queue
	}
}

func (f *appTranscriptFormatter) pendingToolIDAlreadyRendered(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(f.pendingToolsByID) == 0 {
		return false
	}
	existing := f.pendingToolsByID[id]
	if existing == nil {
		return false
	}
	return f.renderedToolKeys[f.toolUseKey(existing)]
}

func (f *appTranscriptFormatter) markToolUseRendered(toolUse *model.ToolUse) {
	if toolUse == nil {
		return
	}
	key := f.toolUseKey(toolUse)
	if key == "" {
		return
	}
	if f.renderedToolKeys == nil {
		f.renderedToolKeys = make(map[string]bool)
	}
	if f.renderedToolDisplays == nil {
		f.renderedToolDisplays = make(map[string]string)
	}
	f.renderedToolKeys[key] = true
	f.renderedToolDisplays[key] = appToolUseDisplay(toolUse)
}

func (f *appTranscriptFormatter) consumeRenderedToolUse(toolUse *model.ToolUse, resultID string) (bool, string, string) {
	if toolUse != nil {
		key := f.toolUseKey(toolUse)
		if f.renderedToolKeys[key] {
			display := f.renderedToolDisplays[key]
			delete(f.renderedToolKeys, key)
			delete(f.renderedToolDisplays, key)
			return true, key, display
		}
		return false, key, ""
	}
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return false, "", ""
	}
	key := "tool:id:" + resultID
	if !f.renderedToolKeys[key] {
		return false, "", ""
	}
	display := f.renderedToolDisplays[key]
	delete(f.renderedToolKeys, key)
	delete(f.renderedToolDisplays, key)
	return true, key, display
}

func (f *appTranscriptFormatter) toolUseKey(toolUse *model.ToolUse) string {
	if toolUse == nil {
		return ""
	}
	if id := strings.TrimSpace(toolUse.ID); id != "" {
		return "tool:id:" + id
	}
	return fmt.Sprintf("tool:ptr:%p", toolUse)
}

func appToolUseDisplayOrName(toolUse *model.ToolUse, fallback string) string {
	if toolUse != nil {
		return appToolUseDisplay(toolUse)
	}
	if strings.TrimSpace(fallback) == "" {
		return "tool"
	}
	return fallback + " call"
}

func appToolContinuationDisplay(display string, isError bool) string {
	display = strings.TrimSpace(display)
	suffix := "result"
	if isError {
		suffix = "error"
	}
	if display == "" {
		return "tool " + suffix
	}
	if base, ok := strings.CutSuffix(display, " call"); ok {
		base = strings.TrimSpace(base)
		if base == "" {
			base = "tool"
		}
		return base + " " + suffix
	}
	return display + " " + suffix
}

func appToolResultHasCommandLifecycleMetadata(result *model.ToolResult) bool {
	if result == nil || len(result.Metadata) == 0 {
		return false
	}
	for _, key := range []string{
		model.MetadataCommandOperation,
		model.MetadataCommandString,
		model.MetadataCommandSessionID,
		model.MetadataCommandStatus,
	} {
		if _, ok := result.Metadata[key]; ok {
			return true
		}
	}
	return false
}

func appToolUseDefersToCommandLifecycle(name string) bool {
	return appToolResultIsRedundant(name)
}

func (f *appTranscriptFormatter) appendGroupedCommandEvent(event memaxagent.Event) {
	command := event.Command
	if command == nil {
		return
	}
	switch event.Kind {
	case memaxagent.EventCommandStarted:
		key := f.startCommandGroupKey(command)
		f.ensurePendingCommands()
		group := f.pendingCommandGroup(key, command)
		if strings.TrimSpace(command.CommandID) != "" && !group.printed && !f.liveCommandGroups {
			f.appendCommandBlock(key, group.renderHeader()...)
			f.markCommandGroupRendered(group)
			group.printed = true
		}
		return
	case memaxagent.EventCommandOutput, memaxagent.EventCommandInput, memaxagent.EventCommandResized:
		key := f.commandGroupKey(command)
		if key == "" {
			return
		}
		if f.commandGroupWasFlushed(key) {
			return
		}
		group := f.pendingCommandGroup(key, command)
		if line := appCommandAuxLine(appCommandAuxAction(event.Kind), command); line != "" {
			child := appProgramDimStyle.Render(appActivityChildLine(strings.TrimSpace(line)))
			if group.printed {
				f.appendPrintedCommandChild(key, group, child)
			} else {
				group.appendChild(child)
			}
		}
		return
	case memaxagent.EventCommandFinished, memaxagent.EventCommandStopped:
		key := f.commandGroupKey(command)
		if key == "" {
			return
		}
		if f.commandGroupWasFlushed(key) {
			return
		}
		group := f.pendingCommandGroup(key, command)
		line, style := appCommandTerminalChildLine(event)
		if line != "" {
			child := style.Render(appActivityChildLine(line))
			if group.printed {
				f.appendPrintedCommandChild(key, group, child)
			} else {
				group.appendChild(child)
			}
		}
		if !group.printed {
			f.appendCommandBlock(key, group.render()...)
			f.markCommandGroupRendered(group)
		}
		f.deletePendingCommandGroup(key)
		f.finishCommandGroupKey(command, key, group)
		f.markCommandGroupFlushed(key, group)
	}
}

func (f *appTranscriptFormatter) ensurePendingCommands() {
	if f.pendingCommands == nil {
		f.pendingCommands = make(map[string]*appProgramCommandGroup)
	}
	if f.pendingCommandID == nil {
		f.pendingCommandID = make(map[string]string)
	}
	if f.pendingCommandFallback == nil {
		f.pendingCommandFallback = make(map[string][]string)
	}
}

func (f *appTranscriptFormatter) pendingCommandGroup(key string, command *memaxagent.CommandEvent) *appProgramCommandGroup {
	f.ensurePendingCommands()
	group := f.pendingCommands[key]
	if group == nil {
		group = newAppProgramCommandGroup(command)
		f.pendingCommands[key] = group
		f.pendingCommandOrder = append(f.pendingCommandOrder, key)
		return group
	}
	group.merge(command)
	return group
}

type appProgramCommandGroup struct {
	display         string
	fallbackKey     string
	children        []string
	droppedChildren int
	printed         bool
}

func newAppProgramCommandGroup(command *memaxagent.CommandEvent) *appProgramCommandGroup {
	group := &appProgramCommandGroup{}
	group.merge(command)
	return group
}

func (g *appProgramCommandGroup) merge(command *memaxagent.CommandEvent) {
	if command == nil {
		return
	}
	display := commandDisplay(memaxagent.Event{Command: command})
	if display != "" {
		g.display = display
	}
	if fallback := appCommandFallbackKey(command); fallback != "operation:" {
		g.fallbackKey = fallback
	}
}

func (g *appProgramCommandGroup) render() []string {
	lines := g.renderHeader()
	if g.droppedChildren > 0 {
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine(fmt.Sprintf("%d earlier updates hidden", g.droppedChildren))))
	}
	lines = append(lines, g.children...)
	return lines
}

func (g *appProgramCommandGroup) renderHeader() []string {
	display := g.display
	if strings.TrimSpace(display) == "" {
		return []string{appProgramToolStyle.Render("• Command")}
	}
	return []string{appProgramToolStyle.Render("• " + appCommandDisplay(display))}
}

func (g *appProgramCommandGroup) renderContinuationHeader() []string {
	display := g.display
	if strings.TrimSpace(display) == "" {
		return []string{appProgramToolStyle.Render("• Command continued")}
	}
	return []string{appProgramToolStyle.Render("• " + appCommandDisplay(display) + " continued")}
}

func (g *appProgramCommandGroup) appendChild(child string) {
	if strings.TrimSpace(child) == "" {
		return
	}
	g.children = append(g.children, child)
	if len(g.children) <= maxAppCommandGroupChildren {
		return
	}
	drop := len(g.children) - maxAppCommandGroupChildren
	g.children = append([]string(nil), g.children[drop:]...)
	g.droppedChildren += drop
}

func (f *appTranscriptFormatter) flushPendingCommandGroups() {
	if len(f.pendingCommands) == 0 {
		return
	}
	for _, key := range append([]string(nil), f.pendingCommandOrder...) {
		group := f.pendingCommands[key]
		if group == nil {
			continue
		}
		if !group.printed {
			f.appendCommandBlock(key, group.render()...)
			f.markCommandGroupRendered(group)
		}
		f.markCommandGroupFlushed(key, group)
		f.deletePendingCommandGroup(key)
	}
	f.pendingCommandFallback = nil
	f.pendingCommandID = nil
}

func (f *appTranscriptFormatter) flushUnprintedCommandGroups() {
	if f.liveCommandGroups {
		return
	}
	if len(f.pendingCommands) == 0 {
		return
	}
	for _, key := range append([]string(nil), f.pendingCommandOrder...) {
		group := f.pendingCommands[key]
		if group == nil || group.printed {
			continue
		}
		f.appendCommandBlock(key, group.render()...)
		f.markCommandGroupRendered(group)
		f.markCommandGroupFlushed(key, group)
		f.removeCommandFallbackKey(group.fallbackKey, key)
		f.deletePendingCommandGroup(key)
	}
	if len(f.pendingCommands) == 0 {
		f.pendingCommandFallback = nil
		f.pendingCommandID = nil
	}
}

func (f *appTranscriptFormatter) printUnprintedCommandGroups() {
	if f.liveCommandGroups {
		return
	}
	if len(f.pendingCommands) == 0 {
		return
	}
	for _, key := range append([]string(nil), f.pendingCommandOrder...) {
		group := f.pendingCommands[key]
		if group == nil || group.printed {
			continue
		}
		f.appendCommandBlock(key, group.render()...)
		f.markCommandGroupRendered(group)
		group.printed = true
	}
}

func (f *appTranscriptFormatter) appendPrintedCommandChild(key string, group *appProgramCommandGroup, child string) {
	if strings.TrimSpace(child) == "" {
		return
	}
	if group != nil && f.lastActivityCommandKey != key && !f.liveCommandGroups {
		f.appendCommandBlock(key, group.renderContinuationHeader()...)
	}
	f.appendCommandActivityGroup(key, child)
}

func (f *appTranscriptFormatter) activeCommandLines() []string {
	if !f.liveCommandGroups || len(f.pendingCommands) == 0 {
		return nil
	}
	// Live command groups are intentionally mutable UI state. Their finalized
	// transcript block is committed on command finish, so scrollback stays
	// grouped even when unrelated assistant/tool rows arrived while it ran.
	var lines []string
	for _, key := range f.pendingCommandOrder {
		group := f.pendingCommands[key]
		if group == nil || group.printed {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, group.render()...)
	}
	return lines
}

func (f *appTranscriptFormatter) markCommandGroupRendered(group *appProgramCommandGroup) {
	if group == nil || strings.TrimSpace(group.display) == "" {
		return
	}
	display := appCommandDisplay(group.display)
	for id, toolUse := range f.pendingToolsByID {
		if appToolUseDisplay(toolUse) != display {
			continue
		}
		if f.renderedCommandToolIDs == nil {
			f.renderedCommandToolIDs = make(map[string]bool)
		}
		f.renderedCommandToolIDs[id] = true
	}
}

func (f *appTranscriptFormatter) consumeRenderedCommandToolUse(toolUse *model.ToolUse, fallbackName string) bool {
	if toolUse == nil {
		return false
	}
	id := strings.TrimSpace(toolUse.ID)
	if id == "" || !f.renderedCommandToolIDs[id] {
		return false
	}
	delete(f.renderedCommandToolIDs, id)
	return true
}

func (f *appTranscriptFormatter) deletePendingCommandGroup(key string) {
	delete(f.pendingCommands, key)
	if len(f.pendingCommandOrder) == 0 {
		return
	}
	next := f.pendingCommandOrder[:0]
	for _, candidate := range f.pendingCommandOrder {
		if candidate != key {
			next = append(next, candidate)
		}
	}
	f.pendingCommandOrder = next
}

func (f *appTranscriptFormatter) markCommandGroupFlushed(key string, group *appProgramCommandGroup) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if f.flushedCommandKeys == nil {
		f.flushedCommandKeys = make(map[string]bool)
	}
	f.flushedCommandKeys[key] = true
	if strings.HasPrefix(key, "anon:") {
		f.flushedAnonymous = true
	}
	if group != nil && strings.TrimSpace(group.fallbackKey) != "" {
		f.flushedCommandKeys[group.fallbackKey] = true
	}
}

func (f *appTranscriptFormatter) commandGroupWasFlushed(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if key == "operation:" && f.flushedAnonymous {
		return true
	}
	return f.flushedCommandKeys[key]
}

func (f *appTranscriptFormatter) startCommandGroupKey(command *memaxagent.CommandEvent) string {
	f.ensurePendingCommands()
	if id := strings.TrimSpace(command.CommandID); id != "" {
		if key := f.pendingCommandID[id]; key != "" {
			return key
		}
		fallback := appCommandFallbackKey(command)
		if queue := f.pendingCommandFallback[fallback]; len(queue) > 0 {
			f.pendingCommandID[id] = queue[0]
			return queue[0]
		}
		key := "id:" + id
		f.pendingCommandID[id] = key
		return key
	}
	f.nextCommandGroup++
	key := fmt.Sprintf("anon:%d", f.nextCommandGroup)
	fallback := appCommandFallbackKey(command)
	f.pendingCommandFallback[fallback] = append(f.pendingCommandFallback[fallback], key)
	return key
}

func (f *appTranscriptFormatter) commandGroupKey(command *memaxagent.CommandEvent) string {
	if id := strings.TrimSpace(command.CommandID); id != "" {
		if key := f.pendingCommandID[id]; key != "" {
			return key
		}
		key := "id:" + id
		if f.pendingCommands[key] != nil {
			f.ensurePendingCommands()
			f.pendingCommandID[id] = key
			return key
		}
		fallback := appCommandFallbackKey(command)
		if len(f.pendingCommandFallback) > 0 {
			if queue := f.pendingCommandFallback[fallback]; len(queue) > 0 {
				f.ensurePendingCommands()
				f.pendingCommandID[id] = queue[0]
				return queue[0]
			}
		}
		f.ensurePendingCommands()
		f.pendingCommandID[id] = key
		return key
	}
	// Without a command ID, simultaneous identical commands cannot be
	// disambiguated. The fallback queue preserves serialized command pairs.
	fallback := appCommandFallbackKey(command)
	if fallback == "operation:" {
		if f.liveCommandGroups {
			return f.onlyPendingCommandGroupKey()
		}
		return fallback
	}
	if len(f.pendingCommandFallback) > 0 {
		if queue := f.pendingCommandFallback[fallback]; len(queue) > 0 {
			return queue[0]
		}
	}
	return fallback
}

func (f *appTranscriptFormatter) onlyPendingCommandGroupKey() string {
	if len(f.pendingCommands) != 1 {
		return ""
	}
	for _, key := range f.pendingCommandOrder {
		if f.pendingCommands[key] != nil {
			return key
		}
	}
	return ""
}

func (f *appTranscriptFormatter) finishCommandGroupKey(command *memaxagent.CommandEvent, key string, group *appProgramCommandGroup) {
	if len(f.pendingCommandFallback) == 0 && len(f.pendingCommandID) == 0 {
		return
	}
	if command != nil {
		if id := strings.TrimSpace(command.CommandID); id != "" && f.pendingCommandID[id] == key {
			delete(f.pendingCommandID, id)
		}
	}
	if group != nil && strings.TrimSpace(group.fallbackKey) != "" {
		f.removeCommandFallbackKey(group.fallbackKey, key)
		return
	}
	f.removeCommandFallbackKey(appCommandFallbackKey(command), key)
}

func (f *appTranscriptFormatter) removeCommandFallbackKey(fallback, key string) {
	if len(f.pendingCommandFallback) == 0 {
		return
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return
	}
	queue := f.pendingCommandFallback[fallback]
	for i, candidate := range queue {
		if candidate == key {
			queue = append(queue[:i], queue[i+1:]...)
			break
		}
	}
	if len(queue) == 0 {
		delete(f.pendingCommandFallback, fallback)
	} else {
		f.pendingCommandFallback[fallback] = queue
	}
}

func appCommandFallbackKey(command *memaxagent.CommandEvent) string {
	if command == nil {
		return "command:"
	}
	if strings.TrimSpace(command.Command) != "" {
		return "command:" + strings.TrimSpace(command.Command)
	}
	return "operation:" + strings.TrimSpace(command.Operation)
}

func appCommandAuxAction(kind memaxagent.EventKind) string {
	switch kind {
	case memaxagent.EventCommandOutput:
		return "output"
	case memaxagent.EventCommandInput:
		return "input"
	case memaxagent.EventCommandResized:
		return "resize"
	default:
		return ""
	}
}

func appCommandTerminalChildLine(event memaxagent.Event) (string, lipgloss.Style) {
	command := event.Command
	if command == nil {
		return "", appProgramDimStyle
	}
	switch event.Kind {
	case memaxagent.EventCommandFinished:
		status := "done"
		style := appProgramSuccessStyle
		if command.ExitCode != 0 || command.TimedOut {
			status = "failed"
			style = appProgramErrorStyle
		}
		parts := []string{status}
		if command.CommandID != "" {
			parts = append(parts, "id="+command.CommandID)
		}
		parts = append(parts, fmt.Sprintf("exit=%d", command.ExitCode))
		if command.TimedOut {
			parts = append(parts, "timeout=true")
		}
		return strings.Join(parts, " "), style
	case memaxagent.EventCommandStopped:
		status := command.Status
		if status == "" {
			status = "stopped"
		}
		parts := []string{"stopped"}
		if command.CommandID != "" {
			parts = append(parts, "id="+command.CommandID)
		}
		parts = append(parts, "status="+status)
		return strings.Join(parts, " "), appProgramErrorStyle
	default:
		return "", appProgramDimStyle
	}
}

func appActivityTailLines(label string, style lipgloss.Style, content string) []string {
	detail := &appProgramActivityDetail{label: label, style: style}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		detail.append(line)
	}
	if len(detail.lines) == 0 {
		return nil
	}
	if len(detail.lines) == 1 {
		return []string{style.Render("  └ " + label + ": " + detail.lines[0])}
	}
	out := []string{style.Render("  └ " + label + " tail:")}
	for _, line := range detail.lines {
		out = append(out, style.Render("    "+line))
	}
	return out
}

func appActivityChildLine(line string) string {
	line = strings.TrimSpace(line)
	return "  └ " + line
}

func (f *appTranscriptFormatter) flushTranscriptPartial() {
	f.queuePrints(f.transcript.append(f.compactor.flush()))
	f.queuePrints(f.transcript.flushPartial())
}

func (f *appTranscriptFormatter) queuePrints(lines []string) {
	if len(lines) == 0 {
		return
	}
	f.pending = append(f.pending, lines...)
}

func (f *appTranscriptFormatter) drainPendingPrints() []string {
	if len(f.pending) == 0 {
		return nil
	}
	lines := append([]string(nil), f.pending...)
	f.pending = nil
	return lines
}
