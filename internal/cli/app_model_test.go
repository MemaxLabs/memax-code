package cli

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
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
		"> tool run_command call",
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
		"• Bash call",
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
		"• workspace_list_files call",
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
		"• Wait for command output call",
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

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command fallback header count = %d, want 1:\n%s", count, got)
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

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 2 {
		t.Fatalf("command header count = %d, want lifecycle block plus visible fallback:\n%s", count, got)
	}
	if strings.Contains(got, "  └ ok") {
		t.Fatalf("redundant command tool result rendered noisy ok row:\n%s", got)
	}
}

func TestAppProgramStructuredToolResultPrintsEarlierUnprintedCommandFirst(t *testing.T) {
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
	commandIndex := strings.Index(got, "• Bash(go test ./...)")
	toolIndex := strings.Index(got, "• read_file call")
	if commandIndex < 0 || toolIndex < 0 || commandIndex > toolIndex {
		t.Fatalf("tool result rendered before earlier command block:\n%s", got)
	}
	for _, want := range []string{
		"• Bash(go test ./...)",
		"  └ output chunks=1 next_seq=2",
		"• read_file call",
		"  └ ok",
		"• Bash(go test ./...) continued",
		"  └ done exit=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command/tool transcript missing %q:\n%s", want, got)
		}
	}
	if count := countTranscriptLine(got, "• Bash(go test ./...)"); count != 1 {
		t.Fatalf("command initial header count = %d, want 1:\n%s", count, got)
	}
	if count := countTranscriptLine(got, "• Bash(go test ./...) continued"); count != 1 {
		t.Fatalf("command continuation header count = %d, want 1:\n%s", count, got)
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
	if count := countTranscriptLine(got, "• Bash(go build ./...) continued"); count != 1 {
		t.Fatalf("flushed command continuation count = %d, want 1:\n%s", count, got)
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
	if strings.Contains(got, "  └ done exit=0") {
		t.Fatalf("late no-ID finish rendered after flushed command:\n%s", got)
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
		"• Bash(npm test -- --watch) continued",
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
	if count := countTranscriptLine(got, "• Bash(npm test -- --watch) continued"); count != 1 {
		t.Fatalf("visible command continuation count = %d, want 1:\n%s", count, got)
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
	if count := countTranscriptLine(got, "• Bash(ls -la)"); count != 2 {
		t.Fatalf("no-id command header count = %d, want 2:\n%s", count, got)
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
	if count := strings.Count(got, "• read_file call"); count != 2 {
		t.Fatalf("read_file header count = %d, want 2:\n%s", count, got)
	}
	if count := strings.Count(got, "  └ ok"); count != 2 {
		t.Fatalf("read_file ok count = %d, want 2:\n%s", count, got)
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

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	want := strings.Join([]string{
		"• Bash(go test ./...)",
		"  └ output chunks=1 next_seq=2",
		"✓ check go test ./... passed=true",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("unprinted command did not flush before activity:\n%s", got)
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
	if promptAt != 2 || rows[promptAt-1] != "" || rows[promptAt-2] != "" {
		t.Fatalf("idle view should have exactly one margin row plus composer padding before prompt:\n%s", view)
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
		"• read_file call",
		"  └ ok",
		"",
		"• workspace_list_files call",
		"  └ ok",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("tool calls missing spacer:\n%s", got)
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

func TestAppProgramBottomStatusDimsMetadata(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	model := newAppProgramModel(context.Background(), options{CWD: ".", Model: "gpt-5.4"}, nil)
	raw := model.bottomStatusView()
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

func TestAppProgramViewShowsActivityOnlyWhileRunning(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 100
	model.running = true
	view := ansi.Strip(model.View())

	if !strings.Contains(view, "thinking") {
		t.Fatalf("running view missing thinking activity:\n%s", view)
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

var bareANSIFragmentRE = regexp.MustCompile(`(^|[^\x1b])\[[0-9;]*m`)

func TestCompactAppProgramTranscriptTextDoesNotRewriteAssistantContent(t *testing.T) {
	got := compactAppProgramTranscriptText(strings.Join([]string{
		"[assistant]",
		"id: 42",
		"memax> do the thing",
		"> tool run_command call",
		"$ command id=cmd-1",
		"[memax-app:error] should remain assistant text",
	}, "\n"))

	for _, want := range []string{
		"id: 42",
		"memax> do the thing",
		"> tool run_command call",
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
		"• tool run_command call",
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
		"> tool read_file call",
	}, "\n")))

	for _, want := range []string{
		"! Bash error",
		"error tail:",
		"line three",
		"line four",
		"line five",
		"line six",
		"line seven",
		"• read_file call",
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
		"> tool wait_command_output call",
		"< tool wait_command_output ok",
		"  result: command output for cmd-1: npm test -- --watch",
		"  status: running",
		"  next_seq: 4",
		"  resume_after_seq: 3",
		"  [stdout #3]",
		"  PASS widget.test.ts",
		"> tool run_command call",
		"< tool run_command ok",
		"  result: command succeeded: go test ./...",
		"  verbose output that should stay collapsed",
	}, "\n")))

	for _, want := range []string{
		"• Wait for command output call",
		"  Wait for command output ok",
		"  Wait for command output ok\n  output tail:",
		"output tail:",
		"next_seq: 4",
		"resume_after_seq: 3",
		"[stdout #3]",
		"PASS widget.test.ts",
		"• Bash call",
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
		"> tool wait_command_output call",
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
		"> tool wait_command_output call",
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
		"> tool wait_command_output call",
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
		"> tool wait_command_output call",
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
		"> tool read_file call\n",
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
		"• read_file call",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("streamed error tail missing %q:\n%s", want, got)
		}
	}
}
