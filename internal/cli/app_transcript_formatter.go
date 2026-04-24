package cli

import (
	"fmt"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/charmbracelet/lipgloss"
)

type appTranscriptFormatter struct {
	transcript appTranscriptTail
	compactor  appProgramTranscriptCompactor
	pending    []string
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
	f.queueCompactorFlush(f.compactor.startSection("assistant"))
	f.appendTranscript(text)
}

func (f *appTranscriptFormatter) appendActivityLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	f.flushTranscriptPartial()
	f.queuePrints(f.transcript.appendStandaloneLine(line))
}

func (f *appTranscriptFormatter) appendToolUse(toolUse *model.ToolUse) {
	if toolUse == nil {
		return
	}
	f.appendActivityLine(appProgramToolStyle.Render("• " + appToolUseDisplay(toolUse)))
}

func (f *appTranscriptFormatter) appendToolResult(result *model.ToolResult) {
	if result == nil {
		return
	}
	name := appToolDisplayName(result.Name)
	if result.IsError {
		f.appendActivityLine(appProgramErrorStyle.Render("! " + name + " error"))
		f.appendActivityDetail("error", appProgramErrorStyle, result.Content)
		return
	}
	f.appendActivityLine(appProgramDimStyle.Render("  " + name + " ok"))
	if appToolShowsResultTail(result.Name) {
		f.appendActivityDetail("output", appProgramDimStyle, result.Content)
	}
}

func (f *appTranscriptFormatter) appendCommandEvent(event memaxagent.Event) {
	if event.Command == nil {
		return
	}
	line, style := appCommandEventLine(event)
	if line == "" {
		return
	}
	f.appendActivityLine(style.Render(line))
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
	detail := &appProgramActivityDetail{label: label, style: style}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		detail.append(line)
	}
	for _, line := range detail.render() {
		f.appendActivityLine(line)
	}
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
	f.queuePrints(f.transcript.append(f.compactor.flush()))
	f.queuePrints(f.transcript.appendStandaloneLine(compactAppProgramLocalLine(kind, text)))
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
