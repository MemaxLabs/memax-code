package cli

import (
	"fmt"
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
		PID:       123,
	}}
	finish := memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID:  "cmd-1",
		Command:    "go test ./...",
		ExitCode:   0,
		DurationMS: 240,
	}}

	state.apply(start)
	startSnapshot := state.snapshot()
	if len(startSnapshot.ActiveCommands) != 1 {
		t.Fatalf("active commands after start = %#v, want one running command", startSnapshot.ActiveCommands)
	}
	if got := startSnapshot.ActiveCommands[0].summary(); !strings.Contains(got, "id=cmd-1") || !strings.Contains(got, "status=running") || !strings.Contains(got, "pid=123") {
		t.Fatalf("active command summary = %q, want id/status/pid", got)
	}

	state.apply(finish)

	if state.commands != 1 {
		t.Fatalf("commands = %d, want 1", state.commands)
	}
	snapshot := state.snapshot()
	if len(snapshot.ActiveCommands) != 0 {
		t.Fatalf("active commands after finish = %#v, want none", snapshot.ActiveCommands)
	}
	if len(state.commandStates) != 0 {
		t.Fatalf("commandStates after finish = %#v, want pruned terminal command", state.commandStates)
	}
	if got := snapshot.detailsLine(); !strings.Contains(got, `last_command="go test ./..."`) || !strings.Contains(got, `last_command_status="id=cmd-1 status=exited pid=123 exit=0 duration=240ms command=go test ./..."`) {
		t.Fatalf("detailsLine() = %q, want last command", got)
	}
}

func TestActivityStateTracksTimedOutCommandAsTerminal(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
		PID:       123,
	}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID:  "cmd-1",
		Command:    "go test ./...",
		TimedOut:   true,
		DurationMS: 5000,
	}})

	snapshot := state.snapshot()
	if len(snapshot.ActiveCommands) != 0 {
		t.Fatalf("active commands after timeout = %#v, want none", snapshot.ActiveCommands)
	}
	if len(state.commandStates) != 0 {
		t.Fatalf("commandStates after timeout = %#v, want pruned terminal command", state.commandStates)
	}
	if got := snapshot.LastCommandState; !strings.Contains(got, "status=timed_out") || !strings.Contains(got, "timeout=true") || !strings.Contains(got, "duration=5000ms") {
		t.Fatalf("LastCommandState = %q, want timeout terminal summary", got)
	}
}

func TestActivityStateTracksCommandOutputAndStops(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
		PID:       123,
	}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:      "cmd-1",
		OutputChunks:   2,
		DroppedChunks:  1,
		DroppedBytes:   512,
		ResumeAfterSeq: 4,
	}})
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandStopped, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Status:    "stopped",
	}})

	if state.commands != 1 {
		t.Fatalf("commands = %d, want 1", state.commands)
	}
	snapshot := state.snapshot()
	if len(snapshot.ActiveCommands) != 0 {
		t.Fatalf("active commands = %#v, want stopped command removed", snapshot.ActiveCommands)
	}
	if got := snapshot.LastCommandState; !strings.Contains(got, "status=stopped") || !strings.Contains(got, "chunks=2") || !strings.Contains(got, "dropped=1/512B") {
		t.Fatalf("LastCommandState = %q, want stopped output summary", got)
	}
}

func TestActivityStatePrunesCommandIDsWithoutDroppingActiveCommands(t *testing.T) {
	var state activityState
	state.apply(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "active",
		Command:   "npm test -- --watch",
	}})
	for i := 0; i < maxTrackedCommandIDs+1; i++ {
		state.apply(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: fmt.Sprintf("done-%d", i),
			Command:   "true",
		}})
	}

	if _, ok := state.commandIDs["active"]; !ok {
		t.Fatalf("active command ID was pruned from commandIDs")
	}
	snapshot := state.snapshot()
	if len(snapshot.ActiveCommands) != 1 || snapshot.ActiveCommands[0].ID != "active" {
		t.Fatalf("active commands = %#v, want active command retained", snapshot.ActiveCommands)
	}
	if len(state.commandIDOrder) > maxTrackedCommandIDs {
		t.Fatalf("commandIDOrder length = %d, want <= %d", len(state.commandIDOrder), maxTrackedCommandIDs)
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
