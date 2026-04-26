package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func countTranscriptLine(text, line string) int {
	count := 0
	for _, candidate := range strings.Split(text, "\n") {
		if candidate == line {
			count++
		}
	}
	return count
}

func strippedViewRows(text string) []string {
	lines := strings.Split(text, "\n")
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = strings.TrimSpace(line)
	}
	return rows
}

func TestCompactAppProgramTranscriptTextCompactsStructuredSections(t *testing.T) {
	got := compactAppProgramTranscriptText(strings.Join([]string{
		"[session]",
		"id: 019db69e-3b4f-7d79-a333-34d708f1d4a6",
		"[assistant]",
		"working on it",
		"[activity]",
		"> tool run_command",
		"< tool run_command ok",
		"  result: line one",
		"  line two",
		"! tool run_command error",
		"$ command id=cmd-1 command=\"go test ./...\"",
		"+ command command=\"go test ./...\" exit=0 timeout=false",
		"! command cmd-2 stopped status=killed",
		"+ check go test ./... passed=true",
		"? approval Apply patch",
		"[result]",
		"done",
		"[usage]",
		"input=10 output=2 total=12",
		"[status]",
		"phase: done",
		"[error]",
		"boom",
	}, "\n"))

	for _, want := range []string{
		"working on it",
		"• Bash",
		"! Bash error",
		"• Bash(go test ./...) started id=cmd-1",
		"✓ done exit=0",
		"! command cmd-2 stopped status=killed",
		"✓ check go test ./... passed=true",
		"? approval Apply patch",
		"boom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"[session]", "[assistant]", "[activity]", "[result]", "[usage]", "[status]", "[error]", "Assistant", "Activity", "Result", "Usage", "Status", "Error", "$ command", "+ command", "019db69e-3b4f-7d79-a333-34d708f1d4a6", "input=10", "phase: done", "line one", "line two"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compact transcript leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramComposerDefaultsToOneLineAndExpandsForMultiline(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 80
	model.height = 24
	model.resize()

	if got, want := model.input.Height(), 1; got != want {
		t.Fatalf("input height = %d, want %d", got, want)
	}
	model.input.SetValue("line one\nline two\nline three")
	model.resize()
	if got, want := model.input.Height(), 3; got != want {
		t.Fatalf("multiline input height = %d, want %d", got, want)
	}
}

func TestAppProgramComposerShrinksAfterDeletion(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 80
	model.height = 24
	model.input.SetValue("line one\nline two\nline three")
	model.input.CursorEnd()
	model.resize()

	if got, want := model.input.Height(), 3; got != want {
		t.Fatalf("expanded input height = %d, want %d", got, want)
	}
	for strings.Contains(model.input.Value(), "\n") {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		model = updated.(*appProgramModel)
	}
	if got, want := model.input.Height(), 1; got != want {
		t.Fatalf("shrunk input height = %d, want %d", got, want)
	}
}

func TestAppProgramComposerHistoryUsesUpDownAtInputBoundaries(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.composer.loadHistory([]string{"first prompt", "second prompt"})
	model.input.SetValue("draft")
	model.input.CursorStart()

	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("up at first input row was not handled")
	}
	if got, want := model.input.Value(), "second prompt"; got != want {
		t.Fatalf("first history recall = %q, want %q", got, want)
	}
	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("second up at first input row was not handled")
	}
	if got, want := model.input.Value(), "first prompt"; got != want {
		t.Fatalf("second history recall = %q, want %q", got, want)
	}
	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("down at last input row was not handled")
	}
	if got, want := model.input.Value(), "second prompt"; got != want {
		t.Fatalf("history next = %q, want %q", got, want)
	}
	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("down back to draft was not handled")
	}
	if got, want := model.input.Value(), "draft"; got != want {
		t.Fatalf("restored draft = %q, want %q", got, want)
	}
}

func TestAppProgramComposerHistoryDoesNotStealUpInsideMultilineInput(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.composer.loadHistory([]string{"old prompt"})
	model.input.SetValue("line one\nline two")
	model.input.CursorEnd()

	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyUp}); handled {
		t.Fatal("up inside multiline input was handled as history recall")
	}
	if got, want := model.input.Value(), "line one\nline two"; got != want {
		t.Fatalf("input changed after non-boundary up = %q, want %q", got, want)
	}
}

func TestAppProgramBackslashEnterInsertsNewline(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.input.SetValue("line one\\")

	if !model.consumeTrailingBackslashForNewline() {
		t.Fatal("consumeTrailingBackslashForNewline() = false, want true")
	}
	if got, want := model.input.Value(), "line one\n"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	if got, want := model.input.Height(), 2; got != want {
		t.Fatalf("input height = %d, want %d", got, want)
	}
}

func TestAppProgramStructuredEventsRenderWithoutTranscriptParsing(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{
		Kind: memaxagent.EventAssistant,
		Message: &model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "working"},
		}},
	})
	m.appendEvent(memaxagent.Event{
		Kind: memaxagent.EventToolUse,
		ToolUse: &model.ToolUse{
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"go test ./..."}`),
		},
	})
	m.appendEvent(memaxagent.Event{
		Kind: memaxagent.EventCommandFinished,
		Command: &memaxagent.CommandEvent{
			Operation: "run",
			Command:   "go test ./...",
			ExitCode:  0,
		},
	})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"working",
		"• Bash(go test ./...)",
		"└ done exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"[assistant]", "[activity]", "$ command", "+ command", "command=\"go test ./...\""} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("structured transcript leaked parsed renderer text %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramStructuredEventsStartNewAssistantBlocksAfterActivity(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{
		Kind: memaxagent.EventAssistant,
		Message: &model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "first reply\n"},
		}},
	})
	m.appendEvent(memaxagent.Event{
		Kind:    memaxagent.EventToolUse,
		ToolUse: &model.ToolUse{ID: "tool-1", Name: "workspace_list_files"},
	})
	m.appendEvent(memaxagent.Event{
		Kind:       memaxagent.EventToolResult,
		ToolResult: &model.ToolResult{ToolUseID: "tool-1", Name: "workspace_list_files", Content: "ok"},
	})
	m.appendEvent(memaxagent.Event{
		Kind: memaxagent.EventAssistant,
		Message: &model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "second reply\n"},
		}},
	})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• first reply",
		"• List files",
		"• second reply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured transcript missing fresh assistant block %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n  second reply") {
		t.Fatalf("second assistant reply was rendered as continuation:\n%s", got)
	}
}

func TestAppProgramStructuredCommandAuxRowsKeepIdentity(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", Command: "npm test -- --watch", PID: 321}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", OutputChunks: 3, NextSeq: 4}},
		{Kind: memaxagent.EventCommandInput, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", InputBytes: 7}},
		{Kind: memaxagent.EventCommandResized, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", Cols: 100, Rows: 30}},
		{Kind: memaxagent.EventCommandStopped, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", Status: "killed"}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-2", Command: "go test ./...", ExitCode: 1, TimedOut: true}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Bash(npm test -- --watch)",
		"└ output id=cmd-1 chunks=3 next_seq=4",
		"└ input id=cmd-1 bytes=7",
		"└ resize id=cmd-1 cols=100 rows=30",
		"└ stopped id=cmd-1 status=killed",
		"└ failed id=cmd-2 exit=1 timeout=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured command transcript missing %q:\n%s", want, got)
		}
	}
}

func TestAppProgramStructuredCommandRowsKeepIdentityWhenEventsInterleave(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", Command: "ls -la"}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{CommandID: "cmd-2", Command: "find . -maxdepth 2"}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", OutputChunks: 1, NextSeq: 2}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{CommandID: "cmd-2", OutputChunks: 2, NextSeq: 3}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", ExitCode: 0}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-2", ExitCode: 0}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Bash(ls -la)",
		"• Bash(find . -maxdepth 2)",
		"  └ output id=cmd-1 chunks=1 next_seq=2",
		"  └ output id=cmd-2 chunks=2 next_seq=3",
		"  └ done id=cmd-1 exit=0",
		"  └ done id=cmd-2 exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("interleaved command transcript missing %q:\n%s", want, got)
		}
	}
	for _, header := range []string{"• Bash(ls -la)", "• Bash(find . -maxdepth 2)"} {
		if count := countTranscriptLine(got, header); count != 1 {
			t.Fatalf("command header %q count = %d, want 1 initial header:\n%s", header, count, got)
		}
	}
}

func TestAppProgramStructuredRepeatedCommandsWithoutIDsStaySeparate(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{Command: "ls -la"}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{Command: "ls -la"}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{Command: "ls -la", OutputChunks: 1, NextSeq: 2}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{Command: "ls -la", ExitCode: 0}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{Command: "ls -la", OutputChunks: 2, NextSeq: 3}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{Command: "ls -la", ExitCode: 0}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Bash(ls -la)",
		"  └ output chunks=1 next_seq=2",
		"  └ done exit=0",
		"  └ output chunks=2 next_seq=3",
		"  └ done exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeated commands without IDs missing %q:\n%s", want, got)
		}
	}
	if count := countTranscriptLine(got, "• Bash(ls -la)"); count != 2 {
		t.Fatalf("command header count = %d, want 2:\n%s", count, got)
	}
}

func TestCompactAppProgramTranscriptTextUsesCompactCommandCompletions(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[activity]",
		"$ command id=cmd-1 command=\"ls -la\"",
		"$ command id=cmd-2 command=\"find . -maxdepth 2\"",
		"+ command id=cmd-1 command=\"ls -la\" exit=0 timeout=false",
		"+ command id=cmd-2 command=\"find . -maxdepth 2\" exit=0 timeout=false",
		"< tool run_command ok",
	}, "\n")))

	for _, want := range []string{
		"• Bash(ls -la) started id=cmd-1",
		"• Bash(find . -maxdepth 2) started id=cmd-2",
		"✓ done id=cmd-1 exit=0",
		"✓ done id=cmd-2 exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact command transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "✓ Bash(ls -la) done") || strings.Contains(got, "✓ Bash(find . -maxdepth 2) done") || strings.Contains(got, "Bash ok") {
		t.Fatalf("compact command transcript repeated command labels or redundant ok:\n%s", got)
	}
	if strings.Count(got, "✓ done id=") != 2 {
		t.Fatalf("compact command completion count = %d, want 2:\n%s", strings.Count(got, "✓ done id="), got)
	}
}

func TestAppProgramStructuredEventsTailCommandOutputToolResults(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "wait_command_output",
		Content: strings.Join([]string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6"}, "\n"),
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Wait for command output",
		"└ ok",
		"output tail:",
		"line 2",
		"line 6",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line 1") {
		t.Fatalf("structured transcript did not tail command output:\n%s", got)
	}
}

func TestAppProgramStructuredCommandToolDoesNotDuplicateHeader(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"go test ./..."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-run-1",
		Name:      "run_command",
		Content:   "ok",
		Metadata: map[string]any{
			model.MetadataCommandOperation: "run",
			model.MetadataCommandString:    "go test ./...",
		},
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
		ExitCode:  0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command header count = %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(got, "  └ done id=cmd-1 exit=0") {
		t.Fatalf("command completion missing:\n%s", got)
	}
}

func TestAppProgramStructuredTailToolDoesNotDuplicateSingleLineSummary(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "wait_command_output",
		Content: "build succeeded in 12s",
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Wait for command output",
		"  └ ok",
		"  └ output: build succeeded in 12s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tail tool transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ok: build succeeded") {
		t.Fatalf("tail tool duplicated output in success summary:\n%s", got)
	}
}

func TestAppProgramStructuredRunCommandRendersBeforeResult(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"sleep 10 && curl -sS https://api.memax.app/health"}`),
	}})

	before := ansi.Strip(m.View())
	if !strings.Contains(before, "• Bash(sleep 10 && curl -sS https://api.memax.app/health)") {
		t.Fatalf("run_command did not render live before result:\n%s", before)
	}
	if strings.Contains(before, "done exit=0") {
		t.Fatalf("run_command completion rendered before result:\n%s", before)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-run-1",
		Name:      "run_command",
		Content:   `{"status":"ok"}`,
		Metadata: map[string]any{
			model.MetadataCommandOperation: "run",
			model.MetadataCommandString:    "sleep 10 && curl -sS https://api.memax.app/health",
			model.MetadataCommandExitCode:  0,
		},
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		Operation: "run",
		Command:   "sleep 10 && curl -sS https://api.memax.app/health",
		ExitCode:  0,
	}})

	after := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(after, "• Bash(sleep 10 && curl -sS https://api.memax.app/health)"); count != 1 {
		t.Fatalf("run_command header count = %d, want 1:\n%s", count, after)
	}
	if !strings.Contains(after, "• Bash(sleep 10 && curl -sS https://api.memax.app/health)\n  └ done exit=0") {
		t.Fatalf("run_command completion did not attach under invocation:\n%s", after)
	}
}

func TestAppProgramStructuredCommandResultSummarizesRuntime(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"go test ./..."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		Operation:       "run",
		Command:         "go test ./...",
		ExitCode:        0,
		DurationMS:      1234,
		StdoutBytes:     2048,
		StderrBytes:     12,
		OutputTruncated: true,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	want := "• Bash(go test ./...)\n  └ done exit=0 duration=1.2s stdout=2.0KB stderr=12B truncated=true"
	if !strings.Contains(got, want) {
		t.Fatalf("command result summary missing:\nwant %q\n%s", want, got)
	}
}

func TestAppProgramStructuredCommandResultSummarizesFailure(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"go test ./..."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		Operation:   "run",
		Command:     "go test ./...",
		ExitCode:    1,
		TimedOut:    true,
		DurationMS:  900,
		StderrBytes: 32,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	want := "• Bash(go test ./...)\n  └ failed exit=1 duration=900ms stderr=32B timeout=true"
	if !strings.Contains(got, want) {
		t.Fatalf("command failure summary missing:\nwant %q\n%s", want, got)
	}
}

func TestAppProgramStructuredRunCommandFinishWithoutCommandStaysGrouped(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"go test ./..."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		ExitCode:  0,
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-run-1",
		Name:      "run_command",
		Content:   "ok",
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "• Command") {
		t.Fatalf("command finish without command string rendered phantom header:\n%s", got)
	}
	if !strings.Contains(got, "• Bash(go test ./...)\n  └ done id=cmd-1 exit=0") {
		t.Fatalf("command completion did not attach under invocation:\n%s", got)
	}
}

func TestAppProgramStructuredSequentialIdenticalRunCommandsUseSeparateGroups(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	emitRun := func(toolID, commandID string) {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    toolID,
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"echo hello"}`),
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
			CommandID: commandID,
			Command:   "echo hello",
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: commandID,
			Command:   "echo hello",
			ExitCode:  0,
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: toolID,
			Name:      "run_command",
			Content:   "hello",
			Metadata: map[string]any{
				model.MetadataCommandOperation: "run",
				model.MetadataCommandString:    "echo hello",
				model.MetadataCommandExitCode:  0,
			},
		}})
	}

	emitRun("tool-run-1", "cmd-1")
	emitRun("tool-run-2", "cmd-2")

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(echo hello)"); count != 2 {
		t.Fatalf("command header count = %d, want 2:\n%s", count, got)
	}
	if count := countTranscriptLine(got, "  └ done id=cmd-1 exit=0"); count != 1 {
		t.Fatalf("cmd-1 completion count = %d, want 1:\n%s", count, got)
	}
	if count := countTranscriptLine(got, "  └ done id=cmd-2 exit=0"); count != 1 {
		t.Fatalf("cmd-2 completion count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramStructuredSequentialIdenticalRunCommandsWithoutStartedUseSeparateGroups(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	emitRun := func(toolID, commandID string) {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    toolID,
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"ls -la"}`),
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: toolID,
			Name:      "run_command",
			Content:   "total 16",
			Metadata: map[string]any{
				model.MetadataCommandOperation: "run",
				model.MetadataCommandString:    "ls -la",
				model.MetadataCommandExitCode:  0,
			},
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: commandID,
			Command:   "ls -la",
			ExitCode:  0,
		}})
	}

	emitRun("tool-run-1", "cmd-1")
	emitRun("tool-run-2", "cmd-2")

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(ls -la)"); count != 2 {
		t.Fatalf("command header count = %d, want 2:\n%s", count, got)
	}
	if count := countTranscriptLine(got, "  └ done id=cmd-1 exit=0"); count != 1 {
		t.Fatalf("cmd-1 completion count = %d, want 1:\n%s", count, got)
	}
	if count := countTranscriptLine(got, "  └ done id=cmd-2 exit=0"); count != 1 {
		t.Fatalf("cmd-2 completion count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramStructuredParallelRunCommandsRenderLiveThenFinalizeGrouped(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	m.width = 200

	commands := []string{
		`bash -lc 's=$((RANDOM % 9 + 2)); echo "agent1_sleep=${s}s"; sleep "$s"; curl -sS -i https://api.memax.app/health'`,
		`bash -lc 's=$((RANDOM % 9 + 2)); echo "agent2_sleep=${s}s"; sleep "$s"; curl -sS -i https://api.memax.app/health'`,
		`bash -lc 's=$((RANDOM % 9 + 2)); echo "agent3_sleep=${s}s"; sleep "$s"; curl -sS -i https://api.memax.app/health'`,
	}
	for i, command := range commands {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-run-" + string(rune('1'+i)),
			Name:  "run_command",
			Input: json.RawMessage(`{"command":` + strconv.Quote(command) + `}`),
		}})
	}

	live := ansi.Strip(m.View())
	for _, command := range commands {
		if !strings.Contains(live, "• Bash("+command+")") {
			t.Fatalf("live command missing %q:\n%s", command, live)
		}
	}
	if got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n")); strings.Contains(got, "Bash(") {
		t.Fatalf("active commands printed to scrollback before completion:\n%s", got)
	}

	for i, command := range commands {
		toolID := "tool-run-" + string(rune('1'+i))
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: toolID,
			Name:      "run_command",
			Content:   "ok",
			Metadata: map[string]any{
				model.MetadataCommandOperation: "run",
				model.MetadataCommandString:    command,
				model.MetadataCommandExitCode:  0,
			},
		}})
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			Operation: "run",
			Command:   command,
			ExitCode:  0,
		}})
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, command := range commands {
		want := "• Bash(" + command + ")\n  └ done exit=0"
		if !strings.Contains(got, want) {
			t.Fatalf("finished command did not finalize as one grouped block %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "continued") {
		t.Fatalf("parallel command transcript rendered continuation rows:\n%s", got)
	}
}

func TestAppProgramStructuredLiveCommandChildrenAreTailed(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
	}})
	for i := 1; i <= 8; i++ {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
			CommandID:    "cmd-1",
			OutputChunks: i,
			NextSeq:      i + 1,
		}})
	}

	live := ansi.Strip(m.View())
	if !strings.Contains(live, "  └ 2 earlier updates hidden") {
		t.Fatalf("live command did not collapse earlier updates:\n%s", live)
	}
	if strings.Contains(live, "chunks=1") || strings.Contains(live, "chunks=2") {
		t.Fatalf("live command kept dropped output rows:\n%s", live)
	}
	if !strings.Contains(live, "chunks=8 next_seq=9") {
		t.Fatalf("live command lost latest output row:\n%s", live)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		ExitCode:  0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "  └ 3 earlier updates hidden") {
		t.Fatalf("final command block did not collapse earlier updates:\n%s", got)
	}
	if strings.Contains(got, "chunks=1") || strings.Contains(got, "chunks=2") || strings.Contains(got, "chunks=3") {
		t.Fatalf("final command block kept dropped output rows:\n%s", got)
	}
	if !strings.Contains(got, "  └ done id=cmd-1 exit=0") {
		t.Fatalf("final command block lost terminal row:\n%s", got)
	}
}

func TestAppProgramStructuredCommandToolWithoutMetadataDoesNotDuplicateRenderedCommand(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-run-1",
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"go test ./..."}`),
		}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			Command:   "go test ./...",
		}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			ExitCode:  0,
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-run-1",
			Name:      "run_command",
			Content:   "ok",
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "  └ ok") {
		t.Fatalf("redundant command tool result rendered noisy ok row:\n%s", got)
	}
}

func TestAppProgramStructuredCommandToolWithoutLifecycleMetadataStaysVisible(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-run-1",
		Name:  "run_command",
		Input: json.RawMessage(`{"command":"go test ./..."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-run-1",
		Name:      "run_command",
		Content:   "ok",
	}})

	got := ansi.Strip(m.View())
	if !strings.Contains(got, "• Bash(go test ./...)") {
		t.Fatalf("command fallback header missing:\n%s", got)
	}
	if strings.Contains(got, "  └ ok") {
		t.Fatalf("redundant command tool result rendered noisy ok row:\n%s", got)
	}
}

func TestAppProgramStructuredRepeatedCommandToolWithoutLifecycleMetadataStaysVisible(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-run-1",
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"go test ./..."}`),
		}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			Command:   "go test ./...",
		}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			ExitCode:  0,
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-run-1",
			Name:      "run_command",
			Content:   "ok",
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-run-2",
			Name:  "run_command",
			Input: json.RawMessage(`{"command":"go test ./..."}`),
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-run-2",
			Name:      "run_command",
			Content:   "ok",
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n") + "\n" + m.View())
	if count := strings.Count(got, "• Bash(go test ./...)"); count != 2 {
		t.Fatalf("command header count = %d, want lifecycle block plus visible fallback:\n%s", count, got)
	}
	if strings.Contains(got, "  └ ok") {
		t.Fatalf("redundant command tool result rendered noisy ok row:\n%s", got)
	}
}

func TestAppProgramStructuredLiveCommandStaysGroupedAcrossToolResult(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{Command: "go test ./..."}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
			Command:      "go test ./...",
			OutputChunks: 1,
			NextSeq:      2,
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-read-1",
			Name:  "read_file",
			Input: json.RawMessage(`{"path":"README.md"}`),
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-read-1",
			Name:      "read_file",
			Content:   "contents",
		}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			Command:  "go test ./...",
			ExitCode: 0,
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	toolIndex := strings.Index(got, "• read_file")
	commandIndex := strings.Index(got, "• Bash(go test ./...)")
	if commandIndex < 0 || toolIndex < 0 {
		t.Fatalf("command/tool transcript missing block:\n%s", got)
	}
	for _, want := range []string{
		"• Bash(go test ./...)",
		"  └ output chunks=1 next_seq=2",
		"• read_file",
		"  └ ok",
		"  └ done exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command/tool transcript missing %q:\n%s", want, got)
		}
	}
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command initial header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "continued") {
		t.Fatalf("command rendered continuation header:\n%s", got)
	}
}

func TestAppProgramStructuredLateFinishAfterFlushDoesNotRenderEmptyCommand(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go build ./...",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:    "cmd-1",
		OutputChunks: 1,
		NextSeq:      2,
	}})
	m.appendLocalTranscriptLine("error", "run canceled")
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		ExitCode:  0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if strings.Contains(got, "\n• Bash\n") || strings.Contains(got, "\n• Command\n") {
		t.Fatalf("late finish rendered orphan command block:\n%s", got)
	}
	if count := countTranscriptLine(got, "• Bash(go build ./...)"); count != 1 {
		t.Fatalf("flushed command header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "continued") {
		t.Fatalf("flushed command rendered continuation:\n%s", got)
	}
}

func TestAppProgramStructuredNoIDLateFinishAfterFlushDoesNotDuplicateHeader(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		Command: "go build ./...",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		Command:      "go build ./...",
		OutputChunks: 1,
		NextSeq:      2,
	}})
	m.appendLocalTranscriptLine("error", "run canceled")
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		Command:  "go build ./...",
		ExitCode: 0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go build ./...)"); count != 1 {
		t.Fatalf("flushed no-ID command header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "continued") {
		t.Fatalf("late no-ID finish rendered continuation:\n%s", got)
	}
}

func TestAppProgramStructuredNoIDNoCommandLateFinishAfterFlushDoesNotRenderOrphan(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		Command: "go build ./...",
	}})
	m.appendLocalTranscriptLine("error", "run canceled")
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		ExitCode: 0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if strings.Contains(got, "\n• Command\n") || strings.Contains(got, "\n• Bash\n") {
		t.Fatalf("late no-ID/no-command finish rendered orphan block:\n%s", got)
	}
	if count := countTranscriptLine(got, "• Bash(go build ./...)"); count != 1 {
		t.Fatalf("flushed command header count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramStructuredStartCommandResultBeforeFinishDoesNotDuplicateHeader(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-start-1",
		Name:  "start_command",
		Input: json.RawMessage(`{"command":"npm test -- --watch"}`),
	}})

	before := ansi.Strip(m.View())
	if !strings.Contains(before, "• Bash(npm test -- --watch)") {
		t.Fatalf("start_command did not render live before result:\n%s", before)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-start-1",
		Name:      "start_command",
		Content:   "ok",
		Metadata: map[string]any{
			model.MetadataCommandOperation: "start",
			model.MetadataCommandString:    "npm test -- --watch",
			model.MetadataCommandSessionID: "cmd-1",
		},
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:    "cmd-1",
		OutputChunks: 2,
		NextSeq:      3,
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		ExitCode:  0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(npm test -- --watch)"); count != 1 {
		t.Fatalf("command header count = %d, want 1:\n%s", count, got)
	}
	want := strings.Join([]string{
		"• Bash(npm test -- --watch)",
		"  └ output id=cmd-1 chunks=2 next_seq=3",
		"  └ done id=cmd-1 exit=0",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("start_command block was not grouped:\n%s", got)
	}
}

func TestAppProgramStructuredVisibleCommandSurvivesAssistantBoundary(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:    "cmd-1",
		OutputChunks: 1,
		NextSeq:      2,
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventAssistant, Message: &model.Message{
		Role: model.RoleAssistant,
		Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "still inspecting\n"},
		},
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:    "cmd-1",
		OutputChunks: 2,
		NextSeq:      3,
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		ExitCode:  0,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Bash(npm test -- --watch)",
		"  └ output id=cmd-1 chunks=1 next_seq=2",
		"• still inspecting",
		"  └ output id=cmd-1 chunks=2 next_seq=3",
		"  └ done id=cmd-1 exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible command lost post-boundary event %q:\n%s", want, got)
		}
	}
	if count := countTranscriptLine(got, "• Bash(npm test -- --watch)"); count != 1 {
		t.Fatalf("visible command header count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "continued") {
		t.Fatalf("visible command rendered continuation:\n%s", got)
	}
}

func TestAppProgramStructuredNoIDCommandAfterFlushWithLiveIDCommand(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			Command:   "npm test -- --watch",
		}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{Command: "ls -la"}},
		{Kind: memaxagent.EventAssistant, Message: &model.Message{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{
				{Type: model.ContentText, Text: "still inspecting\n"},
			},
		}},
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{Command: "ls -la"}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{Command: "ls -la", OutputChunks: 1, NextSeq: 2}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{Command: "ls -la", ExitCode: 0}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			CommandID: "cmd-1",
			ExitCode:  0,
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(ls -la)"); count != 1 {
		t.Fatalf("no-id command header count = %d, want 1:\n%s", count, got)
	}
	for _, want := range []string{
		"• Bash(npm test -- --watch)",
		"• Bash(ls -la)",
		"• still inspecting",
		"  └ output chunks=1 next_seq=2",
		"  └ done exit=0",
		"  └ done id=cmd-1 exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("mixed ID/no-ID command transcript missing %q:\n%s", want, got)
		}
	}
}

func TestAppProgramStructuredTerminalCommandSealsID(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", Command: "go test ./..."}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", ExitCode: 0}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", ExitCode: 0}},
		{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", OutputChunks: 1, NextSeq: 2}},
	} {
		m.appendEvent(event)
	}
	m.flushPendingCommandGroups()

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("sealed command header count = %d, want 1:\n%s", count, got)
	}
	if count := strings.Count(got, "  └ done id=cmd-1 exit=0"); count != 1 {
		t.Fatalf("sealed command done count = %d, want 1:\n%s", count, got)
	}
	if strings.Contains(got, "output id=cmd-1") || strings.Contains(got, "• Command") {
		t.Fatalf("sealed command rendered late output/orphan group:\n%s", got)
	}
}

func TestAppProgramStructuredReplacingToolUseIDRemovesStaleNameQueueEntry(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:   "tool-1",
		Name: "read_file",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:   "tool-1",
		Name: "read_file",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-1",
		Name:      "read_file",
		Content:   "contents",
	}})

	if queue := m.pendingToolsByName["read_file"]; len(queue) != 0 {
		t.Fatalf("stale name queue entries = %d, want 0", len(queue))
	}
	if _, ok := m.pendingToolsByName["read_file"]; ok {
		t.Fatal("empty name queue key remained")
	}
	if toolUse := m.pendingToolsByID["tool-1"]; toolUse != nil {
		t.Fatalf("stale ID entry remained: %#v", toolUse)
	}
	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• read_file"); count != 1 {
		t.Fatalf("replacement rendered duplicate pending tool header count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramStructuredReplacingToolUseIDKeepsOriginalRenderedDisplay(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:   "tool-1",
			Name: "read_file",
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:   "tool-1",
			Name: "workspace_apply_patch",
		}},
		{Kind: memaxagent.EventWorkspaceCheckpoint, Workspace: &memaxagent.WorkspaceEvent{CheckpointID: "checkpoint-1"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-1",
			Name:      "workspace_apply_patch",
			Content:   "ok",
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• read_file",
		"~ checkpoint checkpoint-1",
		"  └ ok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("same-ID replacement transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "• Apply patch") {
		t.Fatalf("same-ID replacement changed visible tool name:\n%s", got)
	}
}

func TestAppProgramStructuredReplacingToolUseIDUsesGenericMismatchedResult(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:   "tool-1",
			Name: "read_file",
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-1",
			Name:  "run_subagent",
			Input: json.RawMessage(`{"agent":"worker","prompt":"inspect"}`),
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-1",
			Name:      "run_subagent",
			Content:   `subagent "worker" result: found README`,
			Metadata: map[string]any{
				"agent": "worker",
			},
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "• read_file\n  └ ok") {
		t.Fatalf("same-ID mismatched result did not stay generic under original header:\n%s", got)
	}
	for _, unwanted := range []string{"Subagent(worker)", `subagent "worker"`, "summary: found README"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("same-ID mismatched result leaked new tool-specific detail %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramStructuredNameOnlyToolResultsUseFIFO(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{Name: "read_file", Input: json.RawMessage(`{"path":"b.go"}`)}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "read_file", Content: "contents of a"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "read_file", Content: "contents of b"}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "• read_file"); count != 2 {
		t.Fatalf("read_file header count = %d, want 2:\n%s", count, got)
	}
	want := "• read_file\n  └ ok: 1 line, 13B"
	if count := strings.Count(got, want); count != 2 {
		t.Fatalf("read_file summary count = %d, want 2 for %q:\n%s", count, want, got)
	}
}

func TestAppProgramStructuredToolUseRendersBeforeResult(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-1",
		Name:  "workspace_read_file",
		Input: json.RawMessage(`{"path":"README.md"}`),
	}})

	before := ansi.Strip(strings.Join(m.activeActivityLines(), "\n"))
	if !strings.Contains(before, "• Read file(README.md)") {
		t.Fatalf("tool invocation did not render in live activity before result:\n%s", before)
	}
	if strings.Contains(before, "  └ ok") {
		t.Fatalf("tool result rendered before completion:\n%s", before)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-1",
		Name:      "workspace_read_file",
		Content:   "ok",
	}})

	after := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(after, "• Read file(README.md)"); count != 1 {
		t.Fatalf("tool header count = %d, want 1:\n%s", count, after)
	}
	if !strings.Contains(after, "• Read file(README.md)\n  └ ok") {
		t.Fatalf("tool result did not continue under invocation:\n%s", after)
	}
}

func TestAppProgramStructuredToolResultSummarizesContent(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "tool-1",
		Name:  "workspace_list_files",
		Input: json.RawMessage(`{"prefix":"."}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "tool-1",
		Name:      "workspace_list_files",
		Content:   "README.md\ngo.mod\ncmd/",
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	want := "• List files(.)\n  └ ok: 3 lines,"
	if !strings.Contains(got, want) {
		t.Fatalf("tool result summary missing:\nwant prefix %q\n%s", want, got)
	}
}

func TestAppProgramStructuredWorkspaceToolLabelsIncludeInputs(t *testing.T) {
	tests := []struct {
		name string
		use  model.ToolUse
		want string
	}{
		{
			name: "list prefix",
			use:  model.ToolUse{ID: "tool-1", Name: "workspace_list_files", Input: json.RawMessage(`{"prefix":"internal/cli"}`)},
			want: "• List files(internal/cli)",
		},
		{
			name: "patch file",
			use:  model.ToolUse{ID: "tool-1", Name: "workspace_apply_patch", Input: json.RawMessage(`{"operations":[{"path":"README.md"}]}`)},
			want: "• Apply patch(README.md)",
		},
		{
			name: "dry run patch",
			use:  model.ToolUse{ID: "tool-1", Name: "workspace_apply_patch", Input: json.RawMessage(`{"dry_run":true,"unified_diff":"diff --git a/README.md b/README.md"}`)},
			want: "• Review patch(unified diff)",
		},
		{
			name: "diff base",
			use:  model.ToolUse{ID: "tool-1", Name: "workspace_diff", Input: json.RawMessage(`{"base_id":"checkpoint-1234"}`)},
			want: "• Show diff(base checkpoint-1234)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
			m.transcript = appTranscriptTail{}
			use := tt.use
			m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &use})

			got := ansi.Strip(strings.Join(m.activeActivityLines(), "\n"))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("workspace tool label missing %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestAppProgramStructuredToolUseStartMergesWithFinalToolUse(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{
		ID:   "fetch-1",
		Name: "web_fetch",
	}})
	started := ansi.Strip(strings.Join(m.activeActivityLines(), "\n"))
	if !strings.Contains(started, "• Web fetch") {
		t.Fatalf("tool-use start did not render active generic row:\n%s", started)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "fetch-1",
		Name:  "web_fetch",
		Input: json.RawMessage(`{"url":"https://example.com/health"}`),
	}})
	updated := ansi.Strip(strings.Join(m.activeActivityLines(), "\n"))
	if !strings.Contains(updated, "• Web fetch(https://example.com/health)") {
		t.Fatalf("final tool-use did not update active row display:\n%s", updated)
	}
	if strings.Contains(updated, "Web fetch\n") {
		t.Fatalf("generic tool-use start row survived after final tool-use:\n%s", updated)
	}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "fetch-1",
		Name:      "web_fetch",
		Content:   "URL: https://example.com/health\nStatus: 200\nTitle: Health",
		Metadata: map[string]any{
			model.MetadataWebStatusCode:   200,
			model.MetadataWebContentBytes: 12,
		},
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Web fetch(https://example.com/health)"); count != 1 {
		t.Fatalf("merged web_fetch header count = %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(got, "• Web fetch(https://example.com/health)\n  └ ok status=200 bytes=12\n  └ title: Health") {
		t.Fatalf("merged web_fetch result did not stay under invocation:\n%s", got)
	}
	for _, unwanted := range []string{"Web fetch\n", "Web fetch result"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("merged web_fetch transcript leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramStructuredCommandToolUseStartDoesNotCreateDuplicateCommandGroup(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	command := "go test ./..."
	input := json.RawMessage(`{"command":` + strconv.Quote(command) + `}`)

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{
			ID:    "tool-run-1",
			Name:  "run_command",
			Input: input,
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "tool-run-1",
			Name:  "run_command",
			Input: input,
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-run-1",
			Name:      "run_command",
			Content:   "ok",
			Metadata: map[string]any{
				model.MetadataCommandOperation: "run",
				model.MetadataCommandString:    command,
				model.MetadataCommandExitCode:  0,
			},
		}},
		{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
			Operation: "run",
			Command:   command,
			ExitCode:  0,
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash("+command+")"); count != 1 {
		t.Fatalf("command tool-use start produced duplicate command headers = %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(got, "• Bash("+command+")\n  └ done exit=0") {
		t.Fatalf("command tool-use start result did not finalize grouped block:\n%s", got)
	}
	if len(m.pendingCommands) != 0 || len(m.pendingCommandFallback) != 0 {
		t.Fatalf("command tool-use start left pending command state: commands=%d fallback=%d", len(m.pendingCommands), len(m.pendingCommandFallback))
	}
}

func TestAppProgramViewKeepsThinkingStatusWithActiveTool(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	m.width = 100
	m.running = true

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:   "tool-1",
		Name: "workspace_list_files",
	}})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "• List files") {
		t.Fatalf("running view missing active tool cell:\n%s", view)
	}
	if !strings.Contains(view, "thinking") {
		t.Fatalf("running view with active tool should keep thinking status:\n%s", view)
	}
}

func TestAppProgramStructuredToolResultAfterInterveningActivityKeepsContext(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:   "tool-1",
			Name: "workspace_list_files",
		}},
		{Kind: memaxagent.EventWorkspaceCheckpoint, Workspace: &memaxagent.WorkspaceEvent{CheckpointID: "checkpoint-1"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-1",
			Name:      "workspace_list_files",
			Content:   "ok",
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• List files",
		"~ checkpoint checkpoint-1",
		"  └ ok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("interleaved tool transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n\n  └ ok") {
		t.Fatalf("tool result rendered as orphan child line:\n%s", got)
	}
}

func TestAppProgramStructuredToolErrorAfterInterveningActivityKeepsContext(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:   "tool-1",
			Name: "read_file",
		}},
		{Kind: memaxagent.EventWorkspaceCheckpoint, Workspace: &memaxagent.WorkspaceEvent{CheckpointID: "checkpoint-1"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "tool-1",
			Name:      "read_file",
			IsError:   true,
			Content:   "permission denied",
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• read_file",
		"~ checkpoint checkpoint-1",
		"  └ error",
		"  └ error: permission denied",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("interleaved tool error transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "• read_file result\n  └ error") {
		t.Fatalf("tool error continuation used neutral result label:\n%s", got)
	}
}

func TestAppProgramFinishPromptDedupesMatchingEventError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	err := errors.New("receive model event: quota exceeded")

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: err})
	m.finishPrompt(appProgramPromptDoneMsg{err: err})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: receive model event: quota exceeded"); count != 1 {
		t.Fatalf("matching event/run error count = %d, want 1:\n%s", count, got)
	}
	if m.lastError != err.Error() {
		t.Fatalf("lastError = %q, want %q", m.lastError, err.Error())
	}
}

func TestAppProgramDedupesRepeatedEventError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	err := errors.New("receive model event: provider quota exceeded")

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: err})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: err})
	m.finishPrompt(appProgramPromptDoneMsg{err: err})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: receive model event: provider quota exceeded"); count != 1 {
		t.Fatalf("duplicate event/run error count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramDedupesRepeatedDistinctEventError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	first := errors.New("first model error")
	second := errors.New("second model error")

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: first})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: second})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: second})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: first model error"); count != 1 {
		t.Fatalf("first error count = %d, want 1:\n%s", count, got)
	}
	if count := strings.Count(got, "! error: second model error"); count != 1 {
		t.Fatalf("second error count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramFinishPromptDedupesFirstEventError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	first := errors.New("first model error")
	second := errors.New("second model error")

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: first})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: second})
	m.finishPrompt(appProgramPromptDoneMsg{err: first})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: first model error"); count != 1 {
		t.Fatalf("first error count = %d, want 1:\n%s", count, got)
	}
	if count := strings.Count(got, "! error: second model error"); count != 1 {
		t.Fatalf("second error count = %d, want 1:\n%s", count, got)
	}
	if m.lastError != first.Error() {
		t.Fatalf("lastError = %q, want %q", m.lastError, first.Error())
	}
}

func TestAppProgramFinishPromptDedupesAnyRenderedEventError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	first := errors.New("first model error")
	second := errors.New("second model error")

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: first})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: second})
	m.finishPrompt(appProgramPromptDoneMsg{err: second})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: first model error"); count != 1 {
		t.Fatalf("first error count = %d, want 1:\n%s", count, got)
	}
	if count := strings.Count(got, "! error: second model error"); count != 1 {
		t.Fatalf("second error count = %d, want 1:\n%s", count, got)
	}
	if m.lastError != second.Error() {
		t.Fatalf("lastError = %q, want %q", m.lastError, second.Error())
	}
}

func TestAppProgramFinishPromptRecordsRenderedTerminalError(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}
	err := errors.New("terminal model error")

	m.finishPrompt(appProgramPromptDoneMsg{err: err})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: err})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := strings.Count(got, "! error: terminal model error"); count != 1 {
		t.Fatalf("terminal error count = %d, want 1:\n%s", count, got)
	}
}

func TestAppProgramParallelWebFetchesCommitAsGroupedCells(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "fetch-1",
			Name:  "web_fetch",
			Input: json.RawMessage(`{"url":"https://example.com/a"}`),
		}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    "fetch-2",
			Name:  "web_fetch",
			Input: json.RawMessage(`{"url":"https://example.com/b"}`),
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "fetch-2",
			Name:      "web_fetch",
			Content:   "URL: https://example.com/b\nStatus: 200\nTitle: B",
			Metadata: map[string]any{
				model.MetadataWebStatusCode:   200,
				model.MetadataWebContentBytes: 42,
			},
		}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: "fetch-1",
			Name:      "web_fetch",
			Content:   "URL: https://example.com/a\nStatus: 200\nTitle: A",
			Metadata: map[string]any{
				model.MetadataWebStatusCode:   200,
				model.MetadataWebContentBytes: 21,
			},
		}},
	} {
		m.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Web fetch(https://example.com/b)\n  └ ok status=200 bytes=42\n  └ title: B",
		"• Web fetch(https://example.com/a)\n  └ ok status=200 bytes=21\n  └ title: A",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("parallel web fetch transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"web_fetch result", "web_fetch call"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("parallel web fetch transcript leaked raw %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramParallelWebFetchStartsStayGroupedByID(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	for _, id := range []string{"fetch-1", "fetch-2", "fetch-3"} {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{
			ID:   id,
			Name: "web_fetch",
		}})
	}
	for _, item := range []struct {
		id  string
		url string
	}{
		{"fetch-1", "https://example.com/a"},
		{"fetch-2", "https://example.com/b"},
		{"fetch-3", "https://example.com/c"},
	} {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    item.id,
			Name:  "web_fetch",
			Input: json.RawMessage(`{"url":` + strconv.Quote(item.url) + `}`),
		}})
	}
	live := ansi.Strip(strings.Join(m.activeActivityLines(), "\n"))
	for _, url := range []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"} {
		if !strings.Contains(live, "• Web fetch("+url+")") {
			t.Fatalf("active web_fetch group missing %q:\n%s", url, live)
		}
	}

	for _, item := range []struct {
		id     string
		url    string
		title  string
		status int
	}{
		{"fetch-3", "https://example.com/c", "C", 203},
		{"fetch-1", "https://example.com/a", "A", 201},
		{"fetch-2", "https://example.com/b", "B", 202},
	} {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: item.id,
			Name:      "web_fetch",
			Content:   "URL: " + item.url + "\nStatus: 200\nTitle: " + item.title,
			Metadata: map[string]any{
				model.MetadataWebStatusCode:   item.status,
				model.MetadataWebContentBytes: len(item.title),
			},
		}})
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Web fetch(https://example.com/c)\n  └ ok status=203 bytes=1\n  └ title: C",
		"• Web fetch(https://example.com/a)\n  └ ok status=201 bytes=1\n  └ title: A",
		"• Web fetch(https://example.com/b)\n  └ ok status=202 bytes=1\n  └ title: B",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("parallel web_fetch grouped transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"Web fetch\n", "Web fetch result"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("parallel web_fetch start transcript leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramStructuredSubagentResultIsCompact(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "delegate-1",
		Name:  "run_subagent",
		Input: json.RawMessage(`{"agent":"explorer","prompt":"inspect health check behavior and report concise evidence"}`),
	}})
	live := ansi.Strip(m.View())
	if !strings.Contains(live, "• Subagent(explorer) inspect health check behavior and report concise evid...") {
		t.Fatalf("live subagent invocation missing before result:\n%s", live)
	}
	if got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n")); strings.Contains(got, "Subagent(explorer)") {
		t.Fatalf("active subagent committed to scrollback before result:\n%s", got)
	}
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "delegate-1",
		Name:      "run_subagent",
		Content: strings.Join([]string{
			`subagent "explorer" result: Reported fields:`,
			"- Delay used: not applicable / not executed",
			"- Command exit status: not available",
			"- HTTP response/status/body: not available",
		}, "\n"),
		Metadata: map[string]any{
			"agent":                        "explorer",
			metadataSubagentChildSessionID: "019dbe66-3b4f-7d79-a333-34d708f1d4a6",
			model.MetadataTaskID:           "task-1",
			model.MetadataTaskStatus:       "completed",
		},
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Subagent(explorer) inspect health check behavior and report concise evid...",
		"  └ done",
		"  └ child 019dbe66...d4a6",
		"  └ task task-1 completed",
		"  └ summary: Reported fields:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subagent transcript missing %q:\n%s", want, got)
		}
	}
	for _, noisy := range []string{"output tail:", "Delay used:", "HTTP response/status/body"} {
		if strings.Contains(got, noisy) {
			t.Fatalf("subagent transcript leaked raw output tail %q:\n%s", noisy, got)
		}
	}
}

func TestAppProgramStructuredParallelSubagentsStayDistinct(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	delegations := []struct {
		id     string
		agent  string
		prompt string
		child  string
	}{
		{"delegate-1", "worker", "curl the health endpoint after a short delay", "019dbe66-0000-7000-8000-000000000001"},
		{"delegate-2", "worker", "curl the health endpoint with headers", "019dbe66-0000-7000-8000-000000000002"},
		{"delegate-3", "worker", "curl the health endpoint and summarize status", "019dbe66-0000-7000-8000-000000000003"},
	}
	for _, delegation := range delegations {
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
			ID:    delegation.id,
			Name:  "run_subagent",
			Input: json.RawMessage(`{"agent":` + strconv.Quote(delegation.agent) + `,"prompt":` + strconv.Quote(delegation.prompt) + `}`),
		}})
	}
	for i := len(delegations) - 1; i >= 0; i-- {
		delegation := delegations[i]
		m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
			ToolUseID: delegation.id,
			Name:      "run_subagent",
			Content:   `subagent "worker" result: HTTP 200 {"status":"ok","ready":true}`,
			Metadata: map[string]any{
				"agent":                        delegation.agent,
				metadataSubagentChildSessionID: delegation.child,
			},
		}})
	}

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, delegation := range delegations {
		header := "• Subagent(worker) " + delegation.prompt
		if count := countTranscriptLine(got, header); count != 1 {
			t.Fatalf("subagent header %q count = %d, want 1:\n%s", header, count, got)
		}
	}
	if count := strings.Count(got, "  └ done"); count != len(delegations) {
		t.Fatalf("subagent done rows = %d, want %d:\n%s", count, len(delegations), got)
	}
	if strings.Contains(got, "output tail:") {
		t.Fatalf("parallel subagents rendered raw output tails:\n%s", got)
	}
}

func TestAppProgramStructuredSubagentFlushedBeforeResultDoesNotDuplicateHeader(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "delegate-1",
		Name:  "run_subagent",
		Input: json.RawMessage(`{"agent":"explorer","prompt":"inspect filesystem state"}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventError, Err: errors.New("stream warning while child is still running")})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "delegate-1",
		Name:      "run_subagent",
		Content:   `subagent "explorer" result: found README and go.mod`,
		Metadata: map[string]any{
			"agent": "explorer",
		},
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Subagent(explorer) inspect filesystem state"); count != 1 {
		t.Fatalf("subagent header count = %d, want 1:\n%s", count, got)
	}
	for _, want := range []string{
		"error: stream warning while child is still running",
		"• Subagent(explorer) inspect filesystem state",
		"  └ done",
		"  └ summary: found README and go.mod",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subagent flushed transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Subagent(explorer) inspect filesystem state result") {
		t.Fatalf("subagent result rendered detached continuation header:\n%s", got)
	}
}

func TestAppProgramStructuredSubagentErrorKeepsTail(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		ID:    "delegate-1",
		Name:  "run_subagent",
		Input: json.RawMessage(`{"agent":"worker","prompt":"run the failing verification"}`),
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		ToolUseID: "delegate-1",
		Name:      "run_subagent",
		IsError:   true,
		Content: strings.Join([]string{
			`subagent "worker" failed: verification failed`,
			"exit status 1",
			"missing generated file",
		}, "\n"),
		Metadata: map[string]any{
			"agent": "worker",
		},
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Subagent(worker) run the failing verification",
		"  └ failed",
		"  └ error tail:",
		"    verification failed",
		"    exit status 1",
		"    missing generated file",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subagent error transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `subagent "worker" failed:`) {
		t.Fatalf("subagent error transcript kept redundant wrapper prefix:\n%s", got)
	}
}

func TestAppProgramStructuredUnterminatedCommandFlushesAtLocalBoundary(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go build ./...",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		CommandID:    "cmd-1",
		OutputChunks: 1,
		NextSeq:      2,
	}})
	m.appendLocalTranscriptLine("error", "run canceled")

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		"• Bash(go build ./...)",
		"  └ output id=cmd-1 chunks=1 next_seq=2",
		"! run canceled",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("unterminated command did not flush before local boundary:\n%s", got)
	}
}

func TestAppProgramStructuredUnprintedCommandFlushesBeforeActivity(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		Command: "go test ./...",
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventCommandOutput, Command: &memaxagent.CommandEvent{
		Command:      "go test ./...",
		OutputChunks: 1,
		NextSeq:      2,
	}})
	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventVerification, Verification: &memaxagent.VerificationEvent{
		Name:   "go test ./...",
		Passed: true,
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n") + "\n" + m.View())
	for _, want := range []string{
		"• Bash(go test ./...)",
		"  └ output chunks=1 next_seq=2",
		"✓ check go test ./... passed=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unprinted command/activity missing %q:\n%s", want, got)
		}
	}
}

func TestAppProgramEnterAddsDraftLineUntilSubmit(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.composer.start("")
	model.syncComposerView()
	model.input.SetValue("line one")

	cmd, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("Enter was not handled")
	}
	if cmd != nil {
		t.Fatalf("Enter in draft returned command, want nil")
	}
	if got, want := model.input.Value(), "line one\n"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	if !model.composer.draftActive {
		t.Fatal("draft became inactive after Enter")
	}
}

func TestAppProgramLocalLinesAreStyledAfterSanitize(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.appendLocalTranscriptLine("user", "› make a plan")
	model.appendLocalTranscriptLine("user", "› \x1b[31mred\x1b[0m text")
	got := strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n")

	for _, want := range []string{
		"Welcome. Type a task or /help.",
		"› make a plan",
		"› red text",
	} {
		if !strings.Contains(ansi.Strip(got), want) {
			t.Fatalf("local transcript missing %q:\nraw=%q\nstripped=%q", want, got, ansi.Strip(got))
		}
	}
	if bareANSIFragmentRE.MatchString(got) {
		t.Fatalf("local transcript contains broken ANSI fragments:\nraw=%q\nstripped=%q", got, ansi.Strip(got))
	}
}

func TestAppProgramLocalLineFlushesStreamingPartial(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nhello")
	model.appendLocalTranscriptLine("user", "› next")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "hello\n") || !strings.Contains(got, "› next") {
		t.Fatalf("local row was not separated from streaming partial:\n%q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, " ", ""), "hello›next") {
		t.Fatalf("local row glued to streaming partial:\n%q", got)
	}
}

func TestAppProgramFinishPromptErrorFlushesToolErrorTail(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[activity]\n",
		"! tool run_command error\n",
		"  error: line one\n",
		"  line two\n",
	} {
		model.appendTranscript(chunk)
	}
	model.finishPrompt(appProgramPromptDoneMsg{err: errors.New("prompt failed")})

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"! Bash error",
		"error tail:",
		"line one",
		"line two",
		"! error: prompt failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error prompt transcript missing %q:\n%s", want, got)
		}
	}
	if model.compactor.activityDetail != nil {
		t.Fatalf("activity detail remained buffered after error prompt: %#v", model.compactor.activityDetail.lines)
	}

	model.appendTranscript("[activity]\n")
	afterNextPromptStart := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if strings.Count(afterNextPromptStart, "line one") != 1 || strings.Count(afterNextPromptStart, "line two") != 1 {
		t.Fatalf("previous error tail leaked after next prompt start:\n%s", afterNextPromptStart)
	}
}

func TestAppProgramCtrlCFlushesToolErrorTail(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[activity]\n",
		"! tool run_command error\n",
		"  error: line one\n",
		"  line two\n",
	} {
		model.appendTranscript(chunk)
	}
	if _, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC}); !handled {
		t.Fatal("ctrl+c was not handled")
	}

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"error tail:",
		"line one",
		"line two",
		"bye",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ctrl+c transcript missing %q:\n%s", want, got)
		}
	}
}

func TestAppProgramCtrlCCancelsRunningPrompt(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	canceled := false
	model.running = true
	model.runCancel = func() { canceled = true }

	cmd, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !handled {
		t.Fatal("ctrl+c was not handled")
	}
	if cmd != nil {
		t.Fatal("ctrl+c while running returned a quit command")
	}
	if !canceled {
		t.Fatal("ctrl+c did not cancel the active run")
	}
	if model.statusLine != "canceling" {
		t.Fatalf("statusLine = %q, want canceling", model.statusLine)
	}
	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "canceling current run") {
		t.Fatalf("cancel transcript missing:\n%s", got)
	}
	if strings.Contains(got, "bye") {
		t.Fatalf("running ctrl+c should not quit:\n%s", got)
	}
}

func TestAppProgramSecondCtrlCWhileCancelingQuits(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	cancelCount := 0
	model.running = true
	model.canceling = true
	model.statusLine = "canceling"
	model.runCancel = func() { cancelCount++ }

	cmd, handled := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !handled {
		t.Fatal("ctrl+c was not handled")
	}
	if cmd == nil {
		t.Fatal("second ctrl+c while canceling did not return a quit command")
	}
	if cancelCount != 1 {
		t.Fatalf("cancel count = %d, want 1", cancelCount)
	}
	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "force quit") {
		t.Fatalf("force quit transcript missing:\n%s", got)
	}
}

func TestAppProgramFinishPromptCanceledDoesNotRecordError(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	canceled := false
	model.running = true
	model.canceling = true
	model.statusLine = "canceling"
	model.runCancel = func() { canceled = true }

	model.finishPrompt(appProgramPromptDoneMsg{err: context.Canceled})

	if !canceled {
		t.Fatal("finishPrompt did not release the run cancel function")
	}
	if model.running {
		t.Fatal("model still running after cancellation")
	}
	if model.canceling {
		t.Fatal("model still canceling after cancellation")
	}
	if model.firstErr != nil {
		t.Fatalf("firstErr = %v, want nil", model.firstErr)
	}
	if model.lastError != "" {
		t.Fatalf("lastError = %q, want empty", model.lastError)
	}
	if model.statusLine != "idle" {
		t.Fatalf("statusLine = %q, want idle", model.statusLine)
	}
	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "canceled") {
		t.Fatalf("cancel transcript missing:\n%s", got)
	}
}

func TestAppProgramStartPromptRejectsReentry(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	oldCancel := func() {}
	model.running = true
	model.runCancel = oldCancel

	cmd := model.startPrompt("second prompt")

	if cmd != nil {
		t.Fatal("startPrompt returned a command while already running")
	}
	if model.runCancel == nil {
		t.Fatal("startPrompt cleared existing run cancel")
	}
	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "run already active") {
		t.Fatalf("reentry warning missing:\n%s", got)
	}
}

func TestAppProgramViewUsesQuietIdleStatus(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: ".", Model: "gpt-5.4"}, nil)
	model.width = 100
	view := ansi.Strip(model.View())

	if strings.Contains(view, "Ctrl+C") || strings.Contains(view, "Enter/Ctrl+S") {
		t.Fatalf("idle view leaked verbose key help:\n%s", view)
	}
	if !strings.Contains(view, "Memax Code") || !strings.Contains(view, "input draft: inactive") {
		t.Fatalf("idle status missing expected compact status:\n%s", view)
	}
	if !strings.Contains(view, "F1 help") {
		t.Fatalf("idle status missing compact help affordance:\n%s", view)
	}
	if strings.Contains(view, "thinking") {
		t.Fatalf("idle view should not show activity line:\n%s", view)
	}
	rows := strippedViewRows(view)
	promptAt := -1
	for i, row := range rows {
		if strings.HasPrefix(row, "› Ask Memax Code") {
			promptAt = i
			break
		}
	}
	expectedPromptAt := appProgramBottomInset + 1
	if promptAt != expectedPromptAt || rows[promptAt-1] != "" {
		t.Fatalf("idle view should not reserve hidden activity rows above the composer:\n%s", view)
	}
}

func TestAppProgramComposerViewUsesVerticalPadding(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	got := ansi.Strip(model.composerView(80))
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("composer view lines = %d, want vertical padding around input:\n%q", len(lines), got)
	}
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("composer top padding = %q, want blank padding line:\n%q", lines[0], got)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Fatalf("composer bottom padding = %q, want blank padding line:\n%q", lines[len(lines)-1], got)
	}
	if !strings.Contains(got, "›") {
		t.Fatalf("composer prompt missing:\n%q", got)
	}
}

func TestAppProgramComposerViewPaintsTrailingWhitespace(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	raw := model.composerView(20)
	if strings.Contains(raw, "\x1b[0m  \x1b[0m") {
		t.Fatalf("composer rendered unpainted trailing whitespace gap:\n%q", raw)
	}
	if strings.Contains(raw, "\x1b[7") {
		t.Fatalf("empty composer placeholder rendered inverse cursor block:\n%q", raw)
	}
	if !strings.Contains(raw, "\x1b[48;5;235m") {
		t.Fatalf("composer missing expected background color:\n%q", raw)
	}
}

func TestAppProgramComposerViewPaintsFullLiveRegionWidth(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	raw := model.composerView(120)
	for _, line := range strings.Split(raw, "\n") {
		if got := lipgloss.Width(line); got != 120 {
			t.Fatalf("composer line width = %d, want full live region width:\n%q", got, raw)
		}
	}
}

func TestAppProgramFlushPrintsBatchesTranscriptLines(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 64
	_ = model.drainPendingPrints()
	model.appendLocalTranscriptLine("dim", "first transcript line")
	model.appendLocalTranscriptLine("dim", "second transcript line")

	cmd := model.flushPrints()
	if cmd == nil {
		t.Fatal("flushPrints returned nil")
	}
	msgText := ansi.Strip(fmt.Sprint(cmd()))
	if !strings.Contains(msgText, "first transcript line\nsecond transcript line") {
		t.Fatalf("flushPrints should emit one ordered print message, got %q", msgText)
	}
	if cmd := model.flushPrints(); cmd != nil {
		t.Fatal("flushPrints returned a command after draining pending prints")
	}
}

func TestAppProgramViewHandlesRepeatedExplicitResizeMessages(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{
		CWD:   "/workspace/memax-code",
		Model: "anthropic/claude-sonnet-4-6",
	}, nil)
	sizes := []tea.WindowSizeMsg{
		{Width: 118, Height: 32},
		{Width: 34, Height: 36},
		{Width: 96, Height: 24},
		{Width: 28, Height: 18},
		{Width: 132, Height: 40},
		{Width: 30, Height: 20},
	}
	for _, size := range sizes {
		updated, _ := model.Update(size)
		model = updated.(*appProgramModel)
		view := model.View()
		stripped := ansi.Strip(view)
		if got := strings.Count(stripped, "Ask Memax Code"); got != 1 {
			t.Fatalf("resize %dx%d rendered placeholder %d times:\n%s", size.Width, size.Height, got, stripped)
		}
		for _, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got >= size.Width {
				t.Fatalf("resize %dx%d line width = %d, want < %d:\n%s\nfull view:\n%s", size.Width, size.Height, got, size.Width, line, view)
			}
		}
	}
}

func TestAppProgramUserPromptTranscriptHasVerticalPadding(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}

	model.appendLocalTranscriptLine("dim", "Welcome.")
	model.appendLocalTranscriptLine("user", "› inspect the repo")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		"Welcome.",
		"",
		" › inspect the repo ",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("user prompt missing vertical padding before prompt:\n%s", got)
	}
	if !strings.HasSuffix(got, " › inspect the repo \n") {
		t.Fatalf("user prompt missing trailing padding line:\n%q", got)
	}
}

func TestAppProgramToolCallsHaveSpacing(t *testing.T) {
	app := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	app.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{ID: "tool-1", Name: "read_file"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{ToolUseID: "tool-1", Name: "read_file", Content: "ok"}},
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{ID: "tool-2", Name: "workspace_list_files"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{ToolUseID: "tool-2", Name: "workspace_list_files", Content: "ok"}},
	} {
		app.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(app.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		"• read_file",
		"  └ ok",
		"",
		"• List files",
		"  └ ok",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("tool calls missing spacer:\n%s", got)
	}
}

func TestAppProgramAssistantAfterUserPromptHasSingleSpacer(t *testing.T) {
	app := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	app.transcript = appTranscriptTail{}

	app.appendLocalTranscriptLine("user", "› inspect the repo")
	app.appendEvent(memaxagent.Event{Kind: memaxagent.EventAssistant, Message: &model.Message{
		Role: model.RoleAssistant,
		Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "I will inspect it.\n"},
		},
	}})

	got := ansi.Strip(strings.Join(app.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		" › inspect the repo ",
		"",
		"• I will inspect it.",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("assistant after user prompt missing single spacer:\n%s", got)
	}
	if strings.Contains(got, " › inspect the repo \n\n\n• I will inspect it.") {
		t.Fatalf("assistant after user prompt rendered double spacer:\n%s", got)
	}
}

func TestAppProgramAssistantAfterToolCallHasSpacing(t *testing.T) {
	app := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	app.transcript = appTranscriptTail{}

	for _, event := range []memaxagent.Event{
		{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{ID: "tool-1", Name: "read_file"}},
		{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{ToolUseID: "tool-1", Name: "read_file", Content: "ok"}},
		{Kind: memaxagent.EventAssistant, Message: &model.Message{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{
				{Type: model.ContentText, Text: "Done reading.\n"},
			},
		}},
	} {
		app.appendEvent(event)
	}

	got := ansi.Strip(strings.Join(app.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		"• read_file",
		"  └ ok",
		"",
		"• Done reading.",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("assistant after tool call missing spacer:\n%s", got)
	}
}

func TestAppProgramComposerContentWidthHandlesNarrowTerminal(t *testing.T) {
	for _, width := range []int{0, 1, 2, 3, 14, 80} {
		if got := appProgramComposerContentWidth(width); got < 1 {
			t.Fatalf("content width for %d = %d, want positive", width, got)
		}
	}
	if got, want := appProgramComposerContentWidth(3), 1; got != want {
		t.Fatalf("content width for narrow terminal = %d, want %d", got, want)
	}
	if got, want := appProgramComposerContentWidth(80), 78; got != want {
		t.Fatalf("content width for normal terminal = %d, want %d", got, want)
	}
}

func TestAppProgramLiveRegionWidthReservesPhysicalWrapColumn(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  int
	}{
		{0, 1},
		{1, 1},
		{2, 1},
		{48, 47},
		{120, 119},
	} {
		if got := appProgramLiveRegionWidth(tc.width); got != tc.want {
			t.Fatalf("appProgramLiveRegionWidth(%d) = %d, want %d", tc.width, got, tc.want)
		}
	}
}

func TestAppProgramFitPrintedLinesWrapsBeforeTerminalEdge(t *testing.T) {
	lines := appProgramFitPrintedLines([]string{
		"short",
		strings.Repeat("x", 18),
	}, 8)
	if len(lines) != 4 {
		t.Fatalf("wrapped line count = %d, want 4: %#v", len(lines), lines)
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got > 8 {
			t.Fatalf("printed line width = %d, want <= 8: %#v", got, lines)
		}
	}
}

func TestAppProgramFitPrintedLinesPreservesANSIStyledText(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	styled := appProgramErrorStyle.Render(strings.Repeat("x", 18))
	lines := appProgramFitPrintedLines([]string{styled}, 8)
	if len(lines) < 2 {
		t.Fatalf("styled line was not wrapped: %#v", lines)
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got > 8 {
			t.Fatalf("styled printed line width = %d, want <= 8: %#v", got, lines)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("styled wrapping stripped ANSI escapes: %q", joined)
	}
	if got, want := strings.ReplaceAll(ansi.Strip(joined), "\n", ""), strings.Repeat("x", 18); got != want {
		t.Fatalf("styled wrapping changed visible text = %q, want %q", got, want)
	}
}

func TestAppProgramBottomStatusDimsMetadata(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	model := newAppProgramModel(context.Background(), options{CWD: ".", Model: "gpt-5.4"}, nil)
	raw := model.bottomStatusView(140)
	if !strings.Contains(raw, "\x1b[2;") && !strings.Contains(raw, "\x1b[2m") {
		t.Fatalf("bottom status metadata did not use faint styling:\n%q", raw)
	}
	stripped := ansi.Strip(raw)
	if !strings.HasPrefix(stripped, strings.Repeat(" ", appProgramStatusInset)+"Memax Code") {
		t.Fatalf("bottom status missing left padding:\n%q", stripped)
	}
	for _, want := range []string{"Memax Code", "session none", "workspace .", "gpt-5.4", "input draft: inactive"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("bottom status missing %q:\n%s", want, stripped)
		}
	}
}

func TestAppProgramViewFitsAfterTerminalResize(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{
		CWD:    "/very/long/workspace/name/that/should/not/break/layout",
		Model:  "openai/gpt-5.5-pro-with-a-long-gateway-name",
		Effort: "extraordinarily-deep",
	}, nil)
	model.showHelp = true
	model.running = true
	model.turnStartedAt = time.Now().Add(-2*time.Minute - 5*time.Second)
	model.lastActivityToolKey = "tool"
	model.pendingToolGroups = map[string]*appProgramToolGroup{
		"tool": {
			name:    "web_fetch",
			display: "Web fetch(https://example.com/some/really/long/path/that/should/not-overflow-the-terminal)",
			children: []string{
				appProgramDimStyle.Render("  └ title: " + strings.Repeat("wide status ", 12)),
			},
		},
	}
	model.pendingToolOrder = []string{"tool"}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	model = updated.(*appProgramModel)
	view := model.View()
	stripped := ansi.Strip(view)
	for _, want := range []string{"Web fetch(", "title: wide status", "input draft: inactive", "F1 help"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("resized view missing %q:\n%s", want, stripped)
		}
	}
	for _, line := range strings.Split(stripped, "\n") {
		if strings.Contains(line, "input draft: inactive") && strings.Contains(line, "Memax Code") {
			t.Fatalf("resized narrow status should prioritize input/help over brand:\n%s", stripped)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got >= 48 {
			t.Fatalf("view line width = %d, want < 48 to avoid terminal autowrap:\n%s\nfull view:\n%s", got, line, view)
		}
	}
	if model.width != 48 || model.height != 18 {
		t.Fatalf("model size = %dx%d, want 48x18", model.width, model.height)
	}
}

func TestAppProgramViewShowsActivityOnlyWhileRunning(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 100
	model.running = true
	model.turnStartedAt = time.Now().Add(-13*time.Second - 100*time.Millisecond)
	view := ansi.Strip(model.View())

	if !strings.Contains(view, "thinking") {
		t.Fatalf("running view missing thinking activity:\n%s", view)
	}
	if !strings.Contains(view, "running 13s") {
		t.Fatalf("running view missing elapsed turn time:\n%s", view)
	}
	rows := strippedViewRows(view)
	statusAt, promptAt := -1, -1
	for i, row := range rows {
		if strings.Contains(row, "thinking") {
			statusAt = i
		}
		if strings.HasPrefix(row, "› Ask Memax Code") {
			promptAt = i
		}
	}
	if statusAt != 1 || rows[statusAt-1] != "" {
		t.Fatalf("running view should have exactly one top margin row before activity status:\n%s", view)
	}
	if promptAt-statusAt != 3 || rows[promptAt-1] != "" || rows[promptAt-2] != "" {
		t.Fatalf("running view should have exactly one margin row plus composer padding before prompt:\n%s", view)
	}
}

func TestAppProgramViewDropsActivityRowsWhenIdle(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 100
	model.running = true
	runningRows := strippedViewRows(ansi.Strip(model.View()))
	model.running = false
	model.statusLine = "idle"
	idleRows := strippedViewRows(ansi.Strip(model.View()))

	if got, want := len(runningRows)-len(idleRows), 2; got != want {
		t.Fatalf("idle view changed by %d rows, want only activity+margin removed: idle=%d running=%d\nidle:\n%s\nrunning:\n%s", got, len(idleRows), len(runningRows), strings.Join(idleRows, "\n"), strings.Join(runningRows, "\n"))
	}
	for _, row := range idleRows {
		if strings.Contains(row, "thinking") {
			t.Fatalf("idle view leaked activity status:\n%s", strings.Join(idleRows, "\n"))
		}
	}
}

func TestAppProgramViewKeepsActivityRowForCancelingAndError(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*appProgramModel)
		want string
	}{
		{
			name: "canceling",
			set: func(model *appProgramModel) {
				model.running = true
				model.canceling = true
			},
			want: "canceling",
		},
		{
			name: "error",
			set: func(model *appProgramModel) {
				model.lastError = "boom"
			},
			want: "! boom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
			model.width = 100
			tc.set(model)

			rows := strippedViewRows(ansi.Strip(model.View()))
			statusAt, promptAt := -1, -1
			for i, row := range rows {
				if statusAt == -1 && strings.Contains(row, tc.want) {
					statusAt = i
				}
				if strings.HasPrefix(row, "› Ask Memax Code") {
					promptAt = i
				}
			}
			if statusAt != 1 || promptAt-statusAt != 3 {
				t.Fatalf("%s view should keep activity row above composer:\n%s", tc.name, strings.Join(rows, "\n"))
			}
		})
	}
}

var bareANSIFragmentRE = regexp.MustCompile(`(^|[^\x1b])\[[0-9;]*m`)

func TestCompactAppProgramTranscriptTextDoesNotRewriteAssistantContent(t *testing.T) {
	got := compactAppProgramTranscriptText(strings.Join([]string{
		"[assistant]",
		"id: 42",
		"memax> do the thing",
		"> tool run_command",
		"$ command id=cmd-1",
		"[memax-app:error] should remain assistant text",
	}, "\n"))

	for _, want := range []string{
		"id: 42",
		"memax> do the thing",
		"> tool run_command",
		"$ command id=cmd-1",
		"[memax-app:error] should remain assistant text",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assistant content was rewritten, missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"session 42",
		"› do the thing",
		"• tool run_command",
		"• command id=cmd-1",
		"! should remain assistant text",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("assistant content was rewritten with %q:\n%s", unwanted, got)
		}
	}
}

func TestCompactAppProgramTranscriptTextFormatsAssistantMarkdown(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[assistant]",
		"# Plan",
		"paragraph **one** with `code`",
		"paragraph **strong `code` rest** done",
		"",
		"paragraph two",
		"#123 is not a heading",
		"- inspect the **repo**",
		"    - nested item",
		"  2. nested ordered",
		"1. run focused tests",
		"> note from context",
		"```go",
		"fmt.Println(\"ok\")",
		"```",
	}, "\n")))

	for _, want := range []string{
		"Plan",
		"paragraph one with code",
		"paragraph strong code rest done",
		"paragraph two",
		"#123 is not a heading",
		"• inspect the repo",
		"    • nested item",
		"  2. nested ordered",
		"1. run focused tests",
		"│ note from context",
		"```go",
		"fmt.Println(\"ok\")",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"###", "**", "with `code`"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("markdown transcript leaked marker %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramInlineMarkdownHandlesUnclosedMarkers(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "**unclosed", want: "**unclosed"},
		{in: "foo `", want: "foo `"},
		{in: "**a** **b", want: "a **b"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			done := make(chan string, 1)
			go func() {
				done <- ansi.Strip(appRenderInlineMarkdown(tt.in))
			}()
			select {
			case got := <-done:
				if got != tt.want {
					t.Fatalf("rendered = %q, want %q", got, tt.want)
				}
			case <-time.After(time.Second):
				t.Fatalf("appRenderInlineMarkdown did not return for %q", tt.in)
			}
		})
	}
}

func TestAppProgramTranscriptRendersAssistantMarkdownAcrossChunks(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[assistant]\nThis repo is called **",
		"Memax Code",
		"** and uses `",
		"Go",
		"`.\n",
	} {
		model.appendTranscript(chunk)
	}

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "This repo is called Memax Code and uses Go.") {
		t.Fatalf("split assistant markdown was not rendered:\n%s", got)
	}
	for _, unwanted := range []string{"**", "`Go`"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("split assistant markdown leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramTranscriptPreservesAssistantSpaceChunks(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[assistant]\nWhat",
		" ",
		"it",
		" ",
		"supports\n",
	} {
		model.appendTranscript(chunk)
	}

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "What it supports") {
		t.Fatalf("assistant space chunks were not preserved:\n%s", got)
	}
	if strings.Contains(got, "Whatit") || strings.Contains(got, "itsupports") {
		t.Fatalf("assistant space chunks were glued:\n%s", got)
	}
}

func TestAppProgramTranscriptRendersAssistantHeadingsAcrossChunks(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[assistant]\n### High level",
		"\n",
		"From `README.md`, this is a **coding-agent CLI**.\n",
	} {
		model.appendTranscript(chunk)
	}

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"High level",
		"From README.md, this is a coding-agent CLI.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assistant heading markdown missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"###", "**", "`README.md`", "levelFrom"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("assistant heading markdown leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramTranscriptRendersAssistantMarkdownTables(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\n| Area | Status |\n| --- | --- |\n| UI | **better** |\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"•   │ Area │ Status │",
		"  ├──────┼────────┤",
		"  │ UI   │ better │",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown table transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"| Area | Status |", "| --- | --- |", "**better**"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("markdown table transcript leaked raw marker %q:\n%s", unwanted, got)
		}
	}
	var tableWidths []int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "│") || strings.Contains(line, "├") {
			tableWidths = append(tableWidths, lipgloss.Width(line))
		}
	}
	if len(tableWidths) != 3 {
		t.Fatalf("table row count = %d, want 3:\n%s", len(tableWidths), got)
	}
	for _, width := range tableWidths[1:] {
		if width != tableWidths[0] {
			t.Fatalf("table row widths = %v, want aligned rows:\n%s", tableWidths, got)
		}
	}
}

func TestAppProgramTranscriptRendersAssistantMarkdownTableEmptyCells(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\n| A | B | C |\n| --- | --- | --- |\n| a | | c |\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "  │ a   │     │ c   │") {
		t.Fatalf("markdown table empty cell was not preserved:\n%s", got)
	}
}

func TestAppProgramTranscriptWrapsWideAssistantMarkdownTables(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{width: 72}

	model.appendTranscript("[assistant]\n| Area | Details |\n| --- | --- |\n| Tool System | Workspace read/write/diff, shell commands, managed sessions, web fetch, and patch application through unified diffs |\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "│") && lipgloss.Width(line) > 72 {
			t.Fatalf("table line width = %d, want <= 72:\n%s\nfull:\n%s", lipgloss.Width(line), line, got)
		}
	}
	for _, want := range []string{"Tool System", "Workspace read/write/diff", "shell commands", "unified diffs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped table missing %q:\n%s", want, got)
		}
	}
}

func TestAppProgramTranscriptWrapsDelimitedMarkdownTableCells(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{width: 54}

	model.appendTranscript("[assistant]\n| Area | Details |\n| --- | --- |\n| UI | **bold spans words** and `code spans words` should wrap cleanly |\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "│") && lipgloss.Width(line) > 54 {
			t.Fatalf("table line width = %d, want <= 54:\n%s\nfull:\n%s", lipgloss.Width(line), line, got)
		}
	}
	for _, want := range []string{"bold spans words", "code spans words", "wrap cleanly"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped delimited table missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"**", "`"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("markdown delimiter leaked into table via %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramTranscriptRendersAssistantHorizontalRule(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nBefore\n---\n- - -\nAfter\n")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Before",
		"  " + strings.Repeat("─", 64),
		"  " + strings.Repeat("─", 64) + "\n  " + strings.Repeat("─", 64),
		"  After",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("horizontal rule transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n  ---\n") || strings.Contains(got, "\n  - - -\n") {
		t.Fatalf("horizontal rule leaked raw markdown:\n%s", got)
	}
}

func TestAppProgramTranscriptDoesNotRenderTablesInsideCodeBlocks(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nExample table:\n```\n| col1 | col2 |\n| --- | --- |\n| a | b |\n```\nDone.\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Example table:",
		"  ```",
		"  | col1 | col2 |",
		"  | --- | --- |",
		"  | a | b |",
		"  Done.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assistant code-block table transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"├", "┼", "│ col1 │"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("assistant code-block table rendered as table via %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramTranscriptDoesNotRenderProsePipesAsTables(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nRun `grep foo | head` to filter.\nUse --flag yes|no for that.\n| TODO | done |\n")
	model.flushTranscriptPartial()

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"• Run grep foo | head to filter.",
		"  Use --flag yes|no for that.",
		"  | TODO | done |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assistant prose pipe line missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "│") || strings.Contains(got, "├") {
		t.Fatalf("assistant prose pipe line rendered as table:\n%s", got)
	}
}

func TestAppProgramTranscriptPreservesWhitespaceOnlyAssistantEvents(t *testing.T) {
	app := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	app.transcript = appTranscriptTail{}
	app.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"###",
		" ",
		"1. Strong CLI/config hygiene",
		"\n",
		"The parser is careful about:",
		"\n",
		"- source precedence",
		"\n",
		"- conflict validation",
		"\n",
		"\n",
		"So the repo is not just a thin wrapper.",
		"\n",
	} {
		app.appendEvent(memaxagent.Event{
			Kind: memaxagent.EventAssistant,
			Message: &model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{
				{Type: model.ContentText, Text: chunk},
			}},
		})
	}

	got := ansi.Strip(strings.Join(app.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"1. Strong CLI/config hygiene",
		"The parser is careful about:",
		"• source precedence",
		"• conflict validation",
		"So the repo is not just a thin wrapper.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assistant whitespace event transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"###1.",
		"hygieneThe parser",
		"precedence- conflict",
		"validationSo the repo",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("assistant whitespace event transcript glued markdown with %q:\n%s", unwanted, got)
		}
	}
}

func TestAppProgramTranscriptAvoidsDoubleDotForLeadingAssistantBullet(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\n- first action\n- second action\n")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "• first action\n  • second action") {
		t.Fatalf("assistant leading bullet was not rendered with message dot plus nested continuation:\n%s", got)
	}
	if strings.Contains(got, "• • first action") {
		t.Fatalf("assistant leading bullet rendered a double dot:\n%s", got)
	}
}

func TestAppProgramTranscriptSeparatesCompleteLineBeforePartial(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	for _, chunk := range []string{
		"[assistant]\nfirst sentence.\nThis is **Memax",
		" Code**.\n",
	} {
		model.appendTranscript(chunk)
	}

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "• first sentence.\n  This is Memax Code.") {
		t.Fatalf("complete line glued to following partial:\n%s", got)
	}
	if strings.Contains(got, "sentence.This") || strings.Contains(got, "**Memax") {
		t.Fatalf("complete line before partial rendered incorrectly:\n%s", got)
	}
}

func TestAppProgramTranscriptPreservesStreamedAssistantBlankLines(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nparagraph one")
	model.appendTranscript("\n\n")
	model.appendTranscript("paragraph two\n")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "• paragraph one\n\n  paragraph two") {
		t.Fatalf("assistant paragraph break was not preserved:\n%q", got)
	}
}

func TestAppProgramTranscriptPreservesSplitAssistantBlankLines(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{
			name: "paragraph break split after first newline",
			chunks: []string{
				"[assistant]\nparagraph one\n",
				"\nparagraph two\n",
			},
		},
		{
			name: "paragraph break split as separate newline chunks",
			chunks: []string{
				"[assistant]\nparagraph one",
				"\n",
				"\n",
				"paragraph two\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
			model.transcript = appTranscriptTail{}
			model.compactor = appProgramTranscriptCompactor{}

			for _, chunk := range tt.chunks {
				model.appendTranscript(chunk)
			}

			got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
			if !strings.Contains(got, "• paragraph one\n\n  paragraph two") {
				t.Fatalf("assistant split paragraph break was not preserved:\n%q", got)
			}
		})
	}
}

func TestAppProgramTranscriptDropsLeadingAssistantBlankLines(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{
			name: "single chunk header with leading blank",
			chunks: []string{
				"[assistant]\n\nfoo\n",
			},
		},
		{
			name: "split header then leading blank",
			chunks: []string{
				"[assistant]\n",
				"\nfoo\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
			model.transcript = appTranscriptTail{}
			model.compactor = appProgramTranscriptCompactor{}

			for _, chunk := range tt.chunks {
				model.appendTranscript(chunk)
			}

			got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
			if strings.HasPrefix(got, "\n") {
				t.Fatalf("assistant transcript gained leading blank:\n%q", got)
			}
			if got != "• foo" {
				t.Fatalf("assistant transcript = %q, want %q", got, "• foo")
			}
		})
	}
}

func TestCompactAppProgramTranscriptTextKeepsDeepIndentedDashAsCode(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[assistant]",
		"code:",
		"        - literal dash in indented text",
	}, "\n")))

	if strings.Contains(got, "• literal dash in indented text") {
		t.Fatalf("deep indented dash was rendered as markdown bullet:\n%s", got)
	}
	if !strings.Contains(got, "        - literal dash in indented text") {
		t.Fatalf("deep indented dash lost code indentation:\n%s", got)
	}
}

func TestCompactAppProgramTranscriptTextDoesNotDuplicateTrailingNewline(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	if got, want := compactor.compact("[assistant]\nhello\n"), "• hello\n"; ansi.Strip(got) != want {
		t.Fatalf("compact trailing newline = %q, want %q", ansi.Strip(got), want)
	}
}

func TestCompactAppProgramTranscriptTextTailsToolErrors(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[activity]",
		"! tool run_command error",
		"  error: line one",
		"  line two",
		"  line three",
		"  line four",
		"  line five",
		"  line six",
		"  line seven",
		"> tool read_file",
	}, "\n")))

	for _, want := range []string{
		"! Bash error",
		"error tail:",
		"line three",
		"line four",
		"line five",
		"line six",
		"line seven",
		"• read_file",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error tail missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"line one", "line two"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("error tail leaked old line %q:\n%s", unwanted, got)
		}
	}
}

func TestCompactAppProgramTranscriptTextTailsCommandOutputResults(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[activity]",
		"> tool wait_command_output",
		"< tool wait_command_output ok",
		"  result: command output for cmd-1: npm test -- --watch",
		"  status: running",
		"  next_seq: 4",
		"  resume_after_seq: 3",
		"  [stdout #3]",
		"  PASS widget.test.ts",
		"> tool run_command",
		"< tool run_command ok",
		"  result: command succeeded: go test ./...",
		"  verbose output that should stay collapsed",
	}, "\n")))

	for _, want := range []string{
		"• Wait for command output",
		"  Wait for command output ok",
		"  Wait for command output ok\n  output tail:",
		"output tail:",
		"next_seq: 4",
		"resume_after_seq: 3",
		"[stdout #3]",
		"PASS widget.test.ts",
		"• Bash",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command output transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"command output for cmd-1",
		"status: running",
		"command succeeded: go test ./...",
		"verbose output that should stay collapsed",
		"  Bash ok",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("command output transcript leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestCompactAppProgramTranscriptTextFlushesConsecutiveResultDetails(t *testing.T) {
	got := ansi.Strip(compactAppProgramTranscriptText(strings.Join([]string{
		"[activity]",
		"> tool wait_command_output",
		"< tool wait_command_output ok",
		"  result: command output for cmd-1",
		"  next_seq: 5",
		"  result: command output for cmd-1",
		"  next_seq: 6",
	}, "\n")))

	for _, want := range []string{
		"  output: next_seq: 5",
		"  output: next_seq: 6",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("consecutive result detail missing %q:\n%s", want, got)
		}
	}
}

func TestCompactAppProgramTranscriptTextFlushDoesNotMergeOpenLine(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	got := ansi.Strip(compactor.compact(strings.Join([]string{
		"[activity]",
		"> tool wait_command_output",
		"< tool wait_command_output ok",
		"  result: command output for cmd-1",
		"  next_seq: 5",
	}, "\n")) + compactor.flush())

	if !strings.Contains(got, "  Wait for command output ok\n  output: next_seq: 5") {
		t.Fatalf("flush merged open line:\n%s", got)
	}
}

func TestAppProgramTranscriptCompactorPreservesOpenLineAcrossBufferedDetail(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	got := ansi.Strip(compactor.compact(strings.Join([]string{
		"[activity]",
		"> tool wait_command_output",
		"< tool wait_command_output ok",
	}, "\n")))
	got += ansi.Strip(compactor.compact("  result: command output for cmd-1\n  next_seq: 5\n"))
	got += ansi.Strip(compactor.flush())

	if !strings.Contains(got, "  Wait for command output ok\n  output: next_seq: 5") {
		t.Fatalf("buffered detail flush merged open line:\n%s", got)
	}
}

func TestAppProgramTranscriptCompactorPreservesOpenLineAcrossWhitespaceChunk(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	got := ansi.Strip(compactor.compact(strings.Join([]string{
		"[activity]",
		"> tool wait_command_output",
		"< tool wait_command_output ok",
	}, "\n")))
	got += ansi.Strip(compactor.compact("   "))
	got += ansi.Strip(compactor.compact("  result: command output for cmd-1\n  next_seq: 5\n"))
	got += ansi.Strip(compactor.flush())

	if !strings.Contains(got, "  Wait for command output ok\n  output: next_seq: 5") {
		t.Fatalf("whitespace chunk before detail flush merged open line:\n%s", got)
	}
}

func TestAppProgramTranscriptCompactorStreamsToolErrorTail(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	var out strings.Builder
	for _, chunk := range []string{
		"[activity]\n",
		"! tool run_command error\n",
		"  error: line one\n",
		"  line two\n",
		"  line three\n",
		"> tool read_file\n",
	} {
		out.WriteString(compactor.compact(chunk))
	}
	out.WriteString(compactor.flush())
	got := ansi.Strip(out.String())

	for _, want := range []string{
		"! Bash error",
		"error tail:",
		"line one",
		"line two",
		"line three",
		"• read_file",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("streamed error tail missing %q:\n%s", want, got)
		}
	}
}
