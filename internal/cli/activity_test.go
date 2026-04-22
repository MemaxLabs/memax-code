package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
)

func TestActivityStateTracksOverlappingTools(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "first"}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "second"}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{Name: "first"}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "first", Content: "ok"}})

	if state.tools != 2 {
		t.Fatalf("tools = %d, want 2", state.tools)
	}
	if state.activeTool != "second" {
		t.Fatalf("activeTool = %q, want second", state.activeTool)
	}
	if got := state.detailsLine(); !strings.Contains(got, `active_tool="second"`) {
		t.Fatalf("detailsLine() = %q, want active second tool", got)
	}
}

func TestActivityStateStatusLines(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"})
	state.apply(memaxagent.Event{Kind: memaxagent.EventUsage, Usage: &model.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "run_command", IsError: true}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventResult, Result: "done"})

	if state.phase() != "done" {
		t.Fatalf("phase() = %q, want done", state.phase())
	}
	if got := state.countsLine(); got != "tools=1 commands=0 patches=0 verifications=0 usage=input=10 output=2 total=12 done=true phase=done" {
		t.Fatalf("countsLine() = %q", got)
	}
	if got := state.detailsLine(); !strings.Contains(got, "tool_errors=1") || !strings.Contains(got, `last_tool="run_command"`) {
		t.Fatalf("detailsLine() = %q, want tool error and last tool", got)
	}
}

func TestActivityStateApprovalAndPatchSummaries(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventApprovalRequested, Approval: &memaxagent.ApprovalEvent{
		Summary: memaxagent.ApprovalSummaryEvent{Title: "Apply patch"},
	}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventWorkspacePatch, Workspace: &memaxagent.WorkspaceEvent{
		Paths:   []string{"README.md", "cmd/main.go"},
		Changes: 2,
	}})

	details := state.detailsLine()
	for _, want := range []string{
		"approval_events=1",
		`last_approval="requested:Apply patch"`,
		`last_patch="paths=2 first=README.md changes=2"`,
	} {
		if !strings.Contains(details, want) {
			t.Fatalf("detailsLine() = %q, missing %q", details, want)
		}
	}
}

func TestActivityStateCountsCommandLifecycleOnce(t *testing.T) {
	var state activityState
	start := memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
	}}
	finish := memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
		ExitCode:  0,
	}}

	state.apply(start)
	state.apply(finish)

	if state.commands != 1 {
		t.Fatalf("commands = %d, want 1", state.commands)
	}
	if got := state.detailsLine(); !strings.Contains(got, `last_command="go test ./..."`) {
		t.Fatalf("detailsLine() = %q, want last command", got)
	}
}

func TestStatusValueDoesNotSplitUTF8(t *testing.T) {
	value := strings.Repeat("界", 90)

	got := statusValue(value)

	if !utf8.ValidString(got) {
		t.Fatalf("statusValue returned invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("statusValue() = %q, want ellipsis suffix", got)
	}
	if count := utf8.RuneCountInString(got); count != 80 {
		t.Fatalf("statusValue rune count = %d, want 80", count)
	}
}
