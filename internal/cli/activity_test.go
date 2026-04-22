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
	if got := state.snapshot().detailsLine(); !strings.Contains(got, `active_tool="second"`) {
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

	snapshot := state.snapshot()
	if snapshot.Phase != "done" {
		t.Fatalf("Phase = %q, want done", snapshot.Phase)
	}
	if got := snapshot.countsLine(); got != "tools=1 commands=0 patches=0 verifications=0 usage=input=10 output=2 total=12 done=true phase=done" {
		t.Fatalf("countsLine() = %q", got)
	}
	if got := snapshot.detailsLine(); !strings.Contains(got, "tool_errors=1") || !strings.Contains(got, `last_tool="run_command"`) {
		t.Fatalf("detailsLine() = %q, want tool error and last tool", got)
	}
}

func TestActivityStateSnapshotIsStableValue(t *testing.T) {
	var state activityState
	if empty := state.snapshot(); empty.ActiveTools == nil {
		t.Fatal("empty snapshot ActiveTools is nil, want non-nil empty slice")
	}
	state.apply(memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"})
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}})

	snapshot := state.snapshot()
	countsLine := snapshot.countsLine()
	state.apply(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "run_command", IsError: true}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventResult, Result: "done"})

	if snapshot.Phase != "running" {
		t.Fatalf("snapshot phase = %q, want running", snapshot.Phase)
	}
	if snapshot.ActiveTool != "run_command" {
		t.Fatalf("snapshot active tool = %q, want run_command", snapshot.ActiveTool)
	}
	if len(snapshot.ActiveTools) != 1 || snapshot.ActiveTools[0] != "run_command" {
		t.Fatalf("snapshot active tools = %#v, want run_command", snapshot.ActiveTools)
	}
	if snapshot.ToolErrors != 0 {
		t.Fatalf("snapshot tool errors = %d, want 0", snapshot.ToolErrors)
	}
	if got := snapshot.countsLine(); got != countsLine {
		t.Fatalf("snapshot counts line changed after later events: got %q want %q", got, countsLine)
	}
	if state.snapshot().Phase != "done" {
		t.Fatalf("current state phase = %q, want done", state.snapshot().Phase)
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

	details := state.snapshot().detailsLine()
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
	if got := state.snapshot().detailsLine(); !strings.Contains(got, `last_command="go test ./..."`) {
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
