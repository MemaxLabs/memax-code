package cli

import (
	"fmt"
	"strconv"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/charmbracelet/lipgloss"
)

const maxAppCommandGroupChildren = 6
const metadataSubagentChildSessionID = "child_session_id"

type appTranscriptFormatter struct {
	transcript             appTranscriptTail
	compactor              appProgramTranscriptCompactor
	pending                []string
	pendingToolsByID       map[string]*model.ToolUse
	pendingToolsByName     map[string][]*model.ToolUse
	pendingToolGroups      map[string]*appProgramToolGroup
	pendingToolOrder       []string
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
	case memaxagent.EventToolUseStart:
		f.appendToolUseStart(event.ToolUse)
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
			f.appendRuntimeErrorLine(event.Err.Error())
		}
	case memaxagent.EventSessionStarted, memaxagent.EventResult, memaxagent.EventUsage, memaxagent.EventToolUseDelta:
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

func (f *appTranscriptFormatter) appendRuntimeErrorLine(text string) {
	text = strings.TrimSpace(normalizeAppTranscriptText(text))
	if text == "" {
		return
	}
	if !f.liveCommandGroups {
		f.flushPendingCommandGroups()
	}
	f.flushTranscriptPartial()
	f.compactor.resetSection()
	f.lastActivityCommandKey = ""
	f.lastActivityToolKey = ""
	f.queuePrints(f.transcript.appendStandaloneLine(compactAppProgramLocalLine("error", "error: "+text)))
}

func (f *appTranscriptFormatter) appendToolUseStart(toolUse *model.ToolUse) {
	if toolUse == nil || appToolUseDefersToCommandLifecycle(toolUse.Name) {
		return
	}
	f.appendToolUse(toolUse)
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
	if f.toolUseUsesLiveGroup(toolUse.Name) {
		f.pendingToolGroup(f.toolUseKey(toolUse), toolUse.Name, appToolUseDisplay(toolUse))
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
	if f.appendLiveToolResult(toolUse, result) {
		return
	}
	rendered, renderedKey, renderedDisplay := f.consumeRenderedToolUse(toolUse, result.ToolUseID)
	name := appToolDisplayName(result.Name)
	if result.IsError {
		lines := make([]string, 0, 4)
		if !rendered && toolUse == nil {
			lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplayOrName(nil, name)))
		} else if !rendered {
			lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplay(toolUse)))
		}
		lines = append(lines, appToolResultStatusLines(toolUse, result)...)
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
	lines := make([]string, 0, 4)
	if !rendered && toolUse == nil {
		lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplayOrName(nil, name)))
	} else if !rendered {
		lines = append(lines, appProgramToolStyle.Render("• "+appToolUseDisplay(toolUse)))
	}
	lines = append(lines, appToolResultStatusLines(toolUse, result)...)
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
	return fallback
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

func appToolResultStatusLines(toolUse *model.ToolUse, result *model.ToolResult) []string {
	if result == nil {
		return nil
	}
	if result.Name == "run_subagent" {
		return appSubagentResultStatusLines(toolUse, result)
	}
	if result.Name == "web_fetch" {
		return appWebFetchResultStatusLines(result)
	}
	if result.IsError {
		lines := []string{appProgramErrorStyle.Render("  └ error")}
		lines = append(lines, appActivityTailLines("error", appProgramErrorStyle, result.Content)...)
		return lines
	}
	if appToolShowsResultTail(result.Name) {
		lines := []string{appProgramDimStyle.Render("  └ ok")}
		lines = append(lines, appActivityTailLines("output", appProgramDimStyle, result.Content)...)
		return lines
	}
	lines := []string{appProgramDimStyle.Render(appToolResultSuccessLine(result))}
	return lines
}

func appWebFetchResultStatusLines(result *model.ToolResult) []string {
	if result == nil {
		return nil
	}
	if result.IsError {
		lines := []string{appProgramErrorStyle.Render("  └ error")}
		lines = append(lines, appActivityTailLines("error", appProgramErrorStyle, result.Content)...)
		return lines
	}
	var parts []string
	if status := appToolMetadataString(result.Metadata, model.MetadataWebStatusCode); status != "" {
		parts = append(parts, "status="+status)
	}
	if bytes := appToolMetadataString(result.Metadata, model.MetadataWebContentBytes); bytes != "" {
		parts = append(parts, "bytes="+bytes)
	}
	if len(parts) == 0 {
		parts = append(parts, "ok")
	} else {
		parts = append([]string{"ok"}, parts...)
	}
	lines := []string{appProgramSuccessStyle.Render("  └ " + strings.Join(parts, " "))}
	if title := appWebFetchContentField(result.Content, "Title"); title != "" {
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("title: "+appInlineSnippet(title, 120))))
	}
	if finalURL := appToolMetadataString(result.Metadata, model.MetadataWebFinalURL); finalURL != "" {
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("final: "+appInlineSnippet(finalURL, 120))))
	}
	return lines
}

func appWebFetchContentField(content, field string) string {
	// webtools currently emits response fields before the blank-line body
	// separator. Keep transcript summaries on that metadata header instead of
	// scanning fetched page content.
	prefix := field + ":"
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
		if line == "" {
			return ""
		}
	}
	return ""
}

func appSubagentResultStatusLines(toolUse *model.ToolUse, result *model.ToolResult) []string {
	status := "done"
	style := appProgramSuccessStyle
	if result.IsError {
		status = "failed"
		style = appProgramErrorStyle
	}
	lines := []string{style.Render("  └ " + status)}
	if childSessionID := appToolMetadataString(result.Metadata, metadataSubagentChildSessionID); childSessionID != "" {
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("child "+appShortDisplayID(childSessionID))))
	}
	taskID := appToolMetadataString(result.Metadata, model.MetadataTaskID)
	taskStatus := appToolMetadataString(result.Metadata, model.MetadataTaskStatus)
	switch {
	case taskID != "" && taskStatus != "":
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("task "+taskID+" "+taskStatus)))
	case taskID != "":
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("task "+taskID)))
	}
	if progressErr := appToolMetadataString(result.Metadata, model.MetadataTaskProgressError); progressErr != "" {
		lines = append(lines, appProgramErrorStyle.Render(appActivityChildLine("progress update failed: "+appInlineSnippet(progressErr, 96))))
	}
	if result.IsError {
		lines = append(lines, appSubagentErrorTailLines(toolUse, result)...)
		return lines
	}
	if summary := appSubagentResultSummary(toolUse, result); summary != "" {
		lines = append(lines, appProgramDimStyle.Render(appActivityChildLine("summary: "+summary)))
	}
	return lines
}

func appSubagentErrorTailLines(toolUse *model.ToolUse, result *model.ToolResult) []string {
	content := appSubagentResultContent(toolUse, result)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return appActivityTailLines("error", appProgramErrorStyle, content)
}

func appSubagentResultSummary(toolUse *model.ToolUse, result *model.ToolResult) string {
	for _, line := range strings.Split(appSubagentResultContent(toolUse, result), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		return appInlineSnippet(line, 120)
	}
	return ""
}

func appSubagentResultContent(toolUse *model.ToolUse, result *model.ToolResult) string {
	if result == nil {
		return ""
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return ""
	}
	prefixes := appSubagentResultPrefixes(toolUse, result)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, prefix := range prefixes {
			if next, ok := strings.CutPrefix(line, prefix+" result:"); ok {
				lines[i] = strings.TrimSpace(next)
				return strings.TrimSpace(strings.Join(lines[i:], "\n"))
			}
			if next, ok := strings.CutPrefix(line, prefix+" failed:"); ok {
				lines[i] = strings.TrimSpace(next)
				return strings.TrimSpace(strings.Join(lines[i:], "\n"))
			}
		}
	}
	return content
}

func appSubagentResultPrefixes(toolUse *model.ToolUse, result *model.ToolResult) []string {
	seen := make(map[string]bool)
	var prefixes []string
	add := func(agent string) {
		agent = strings.TrimSpace(agent)
		if agent == "" {
			return
		}
		prefix := `subagent "` + agent + `"`
		if seen[prefix] {
			return
		}
		seen[prefix] = true
		prefixes = append(prefixes, prefix)
	}
	if subagent, ok := appToolUseSubagentInput(toolUse); ok {
		add(subagent.Agent)
	}
	if result != nil {
		add(appToolMetadataString(result.Metadata, "agent"))
	}
	return prefixes
}

func appToolMetadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func appToolMetadataInt(metadata map[string]any, key string) (int, bool) {
	if len(metadata) == 0 {
		return 0, false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case uint:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func appToolMetadataBool(metadata map[string]any, key string) (bool, bool) {
	if len(metadata) == 0 {
		return false, false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}
	return false, false
}

func appShortDisplayID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "..." + id[len(id)-4:]
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
		f.flushPendingToolGroups()
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
	f.flushPendingToolGroups()
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

func (f *appTranscriptFormatter) activeActivityLines() []string {
	if !f.liveCommandGroups {
		return nil
	}
	// Live activity groups are intentionally mutable UI state. Their finalized
	// transcript block is committed on finish, so scrollback stays grouped even
	// when unrelated assistant/tool rows arrived while it ran.
	var lines []string
	if len(f.pendingCommands) > 0 {
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
	}
	if len(f.pendingToolGroups) > 0 {
		for _, key := range f.pendingToolOrder {
			group := f.pendingToolGroups[key]
			if group == nil {
				continue
			}
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, group.render()...)
		}
	}
	return lines
}

func (f *appTranscriptFormatter) toolUseUsesLiveGroup(name string) bool {
	return f.liveCommandGroups && !appToolUseDefersToCommandLifecycle(name)
}

func (f *appTranscriptFormatter) pendingToolGroup(key, name, display string) *appProgramToolGroup {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	name = strings.TrimSpace(name)
	display = strings.TrimSpace(display)
	if f.pendingToolGroups == nil {
		f.pendingToolGroups = make(map[string]*appProgramToolGroup)
	}
	group := f.pendingToolGroups[key]
	if group == nil {
		group = &appProgramToolGroup{name: name, display: display}
		f.pendingToolGroups[key] = group
		f.pendingToolOrder = append(f.pendingToolOrder, key)
		return group
	}
	if group.name == "" {
		group.name = name
	}
	if display != "" && (name == "" || group.name == "" || group.name == name) {
		group.display = display
	}
	return group
}

func (f *appTranscriptFormatter) appendLiveToolResult(toolUse *model.ToolUse, result *model.ToolResult) bool {
	key := f.toolResultKey(toolUse, result.ToolUseID)
	if key == "" || len(f.pendingToolGroups) == 0 {
		return false
	}
	group := f.pendingToolGroups[key]
	if group == nil {
		return false
	}
	group.appendChildren(appToolResultStatusLinesForGroup(group, toolUse, result)...)
	f.appendToolBlock(key, group.render()...)
	f.deletePendingToolGroup(key)
	return true
}

func appToolResultStatusLinesForGroup(group *appProgramToolGroup, toolUse *model.ToolUse, result *model.ToolResult) []string {
	if group != nil && group.name != "" && result != nil && result.Name != "" && group.name != result.Name {
		return appGenericToolResultStatusLines(result)
	}
	return appToolResultStatusLines(toolUse, result)
}

func appGenericToolResultStatusLines(result *model.ToolResult) []string {
	if result == nil {
		return nil
	}
	if result.IsError {
		lines := []string{appProgramErrorStyle.Render("  └ error")}
		lines = append(lines, appActivityTailLines("error", appProgramErrorStyle, result.Content)...)
		return lines
	}
	return []string{appProgramDimStyle.Render("  └ ok")}
}

func appToolResultSuccessLine(result *model.ToolResult) string {
	if result == nil {
		return "  └ ok"
	}
	if command := appCommandMetadataResultSummary(result.Metadata); command != "" {
		return "  └ " + command
	}
	if summary := appToolResultContentSummary(result.Name, result.Content); summary != "" {
		return "  └ ok: " + summary
	}
	return "  └ ok"
}

func appCommandMetadataResultSummary(metadata map[string]any) string {
	exitCode, hasExit := appToolMetadataInt(metadata, model.MetadataCommandExitCode)
	timedOut, hasTimedOut := appToolMetadataBool(metadata, model.MetadataCommandTimedOut)
	durationMS, hasDuration := appToolMetadataInt(metadata, model.MetadataCommandDurationMS)
	stdoutBytes, hasStdout := appToolMetadataInt(metadata, model.MetadataCommandStdoutBytes)
	stderrBytes, hasStderr := appToolMetadataInt(metadata, model.MetadataCommandStderrBytes)
	truncated, hasTruncated := appToolMetadataBool(metadata, model.MetadataCommandOutputTruncated)
	if !hasExit && !hasTimedOut && !hasDuration && !hasStdout && !hasStderr && !hasTruncated {
		return ""
	}
	status := "done"
	if exitCode != 0 || timedOut {
		status = "failed"
	}
	parts := []string{status}
	if hasExit {
		parts = append(parts, fmt.Sprintf("exit=%d", exitCode))
	}
	parts = append(parts, appCommandRuntimeSummaryParts(durationMS, stdoutBytes, stderrBytes, truncated)...)
	if timedOut {
		parts = append(parts, "timeout=true")
	}
	return strings.Join(parts, " ")
}

func appToolResultContentSummary(name string, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lower := strings.ToLower(content)
	switch lower {
	case "ok", "done", "success":
		return ""
	}
	lines := strings.Split(content, "\n")
	nonEmpty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return ""
	}
	if nonEmpty == 1 {
		if !appToolMayInlineResultSnippet(name) {
			return fmt.Sprintf("1 line, %s", appFormatBytes(len(content)))
		}
		return appInlineSnippet(content, 120)
	}
	return fmt.Sprintf("%d lines, %s", nonEmpty, appFormatBytes(len(content)))
}

func appToolMayInlineResultSnippet(name string) bool {
	switch name {
	case "workspace_list_files", "workspace_apply_patch", "workspace_verify":
		return true
	default:
		return false
	}
}

func appCommandRuntimeSummaryParts(durationMS int, stdoutBytes int, stderrBytes int, truncated bool) []string {
	parts := make([]string, 0, 4)
	if durationMS > 0 {
		parts = append(parts, "duration="+appFormatDurationMS(durationMS))
	}
	if stdoutBytes > 0 {
		parts = append(parts, "stdout="+appFormatBytes(stdoutBytes))
	}
	if stderrBytes > 0 {
		parts = append(parts, "stderr="+appFormatBytes(stderrBytes))
	}
	if truncated {
		parts = append(parts, "truncated=true")
	}
	return parts
}

func appFormatDurationMS(ms int) string {
	if ms <= 0 {
		return "0ms"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func appFormatBytes(bytes int) string {
	if bytes < 0 {
		bytes = 0
	}
	const kb = 1024
	const mb = kb * 1024
	if bytes < kb {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < mb {
		return fmt.Sprintf("%.1fKB", float64(bytes)/kb)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/mb)
}

func (f *appTranscriptFormatter) flushPendingToolGroups() {
	if len(f.pendingToolGroups) == 0 {
		return
	}
	for _, key := range append([]string(nil), f.pendingToolOrder...) {
		group := f.pendingToolGroups[key]
		if group == nil {
			continue
		}
		f.appendToolBlock(key, group.render()...)
		f.markToolGroupRendered(key, group.display)
		f.deletePendingToolGroup(key)
	}
}

func (f *appTranscriptFormatter) markToolGroupRendered(key string, display string) {
	key = strings.TrimSpace(key)
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
	f.renderedToolDisplays[key] = strings.TrimSpace(display)
}

func (f *appTranscriptFormatter) deletePendingToolGroup(key string) {
	delete(f.pendingToolGroups, key)
	if len(f.pendingToolOrder) == 0 {
		return
	}
	next := f.pendingToolOrder[:0]
	for _, candidate := range f.pendingToolOrder {
		if candidate != key {
			next = append(next, candidate)
		}
	}
	f.pendingToolOrder = next
	if len(f.pendingToolOrder) == 0 {
		f.pendingToolGroups = nil
	}
}

func (f *appTranscriptFormatter) toolResultKey(toolUse *model.ToolUse, resultID string) string {
	if toolUse != nil {
		return f.toolUseKey(toolUse)
	}
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return ""
	}
	return "tool:id:" + resultID
}

type appProgramToolGroup struct {
	name     string
	display  string
	children []string
}

func (g *appProgramToolGroup) render() []string {
	if g == nil {
		return nil
	}
	display := strings.TrimSpace(g.display)
	if display == "" {
		display = "tool"
	}
	lines := []string{appProgramToolStyle.Render("• " + display)}
	lines = append(lines, g.children...)
	return lines
}

func (g *appProgramToolGroup) appendChildren(children ...string) {
	if g == nil {
		return
	}
	for _, child := range children {
		if strings.TrimSpace(child) == "" {
			continue
		}
		g.children = append(g.children, child)
	}
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
		parts = append(parts, appCommandRuntimeSummaryParts(command.DurationMS, command.StdoutBytes, command.StderrBytes, command.OutputTruncated)...)
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
