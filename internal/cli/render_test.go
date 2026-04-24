package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func TestRenderEventsPrintsToolErrors(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind: memaxagent.EventToolResult,
		ToolResult: &model.ToolResult{
			Name:    "shell",
			IsError: true,
		},
	}
	close(events)

	var out bytes.Buffer
	if err := renderEvents(&out, events); err != nil {
		t.Fatalf("renderEvents() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "tool_error: <empty tool error>") {
		t.Fatalf("render output = %q, want tool error marker", got)
	}
}

func TestRenderEventSeparatesAssistantTextFromFollowingEvents(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Content: "ok"}}
	close(events)

	var out bytes.Buffer
	if err := renderEvents(&out, events); err != nil {
		t.Fatalf("renderEvents() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "hello\ntool_result: ok\n") {
		t.Fatalf("render output = %q, want separated lines", got)
	}
}

func TestRenderEventsCoalescesAssistantStreamChunks(t *testing.T) {
	events := make(chan memaxagent.Event, 4)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: " world"}}},
	}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseDelta, ToolUseDelta: `{"cmd"`}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Content: "ok"}}
	close(events)

	var out bytes.Buffer
	if err := renderEvents(&out, events); err != nil {
		t.Fatalf("renderEvents() error = %v", err)
	}
	if got := out.String(); got != "hello world\ntool_result: ok\n" {
		t.Fatalf("render output = %q, want coalesced assistant text and no deltas", got)
	}
}

func TestRenderEventsWithModeAutoUsesPlainForNonTerminal(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeAuto); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); got != "hello" {
		t.Fatalf("render output = %q, want plain renderer", got)
	}
}

func TestRenderEventsWithModeLiveFallsBackToPlainForNonTerminal(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeLive); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); got != "hello" {
		t.Fatalf("render output = %q, want plain fallback", got)
	}
}

func TestRenderEventsWithModeAppFallsBackToPlainForNonTerminal(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeApp); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); got != "hello" {
		t.Fatalf("render output = %q, want plain fallback", got)
	}
}

func TestTerminalWriterInfoUsesPTYWidth(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("open pty: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 10, Cols: 37}); err != nil {
		t.Skipf("set pty size: %v", err)
	}
	terminal, width, height := terminalWriterInfo(ptmx)
	if !terminal {
		t.Fatal("terminalWriterInfo() terminal = false, want true")
	}
	if width != 36 {
		t.Fatalf("terminalWriterInfo() width = %d, want 36", width)
	}
	if height != 10 {
		t.Fatalf("terminalWriterInfo() height = %d, want 10", height)
	}
}

func TestRenderTUIEventsPrintsStructuredSectionsAndStatus(t *testing.T) {
	events := make(chan memaxagent.Event, 7)
	events <- memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "I will test it."}}},
	}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{
		Command:    "go test ./...",
		Argv:       []string{"sh", "-c", "go test ./..."},
		ExitCode:   0,
		DurationMS: 120,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventWorkspacePatch, Workspace: &memaxagent.WorkspaceEvent{
		Paths:   []string{"README.md"},
		Changes: 1,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventResult, Result: "done"}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Memax Code\n----------",
		"[session]\nid: 00000000-0000-7000-8000-000000000001",
		"[assistant]\nI will test it.\n",
		"[activity]\n> tool run_command",
		`+ command command="go test ./..." exit=0 timeout=false`,
		"~ patch README.md changes=1",
		"[result]\ndone",
		"[status]\nphase: done\nsession: 00000000-0000-7000-8000-000000000001\nsummary: tools=1 commands=1 patches=1 verifications=0 done=true",
		`last_tool="run_command"`,
		`last_command="go test ./..."`,
		`last_command_status="status=exited exit=0 duration=120ms command=go test ./..."`,
		`last_patch="README.md changes=1"`,
		"phase=done",
		"active_tools:\n  - run_command\n",
		"recent:\n  command: go test ./...\n  command_status: status=exited exit=0 duration=120ms command=go test ./...\n  patch: README.md changes=1\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
	}
}

func TestAppRenderEventsPrintInlineTranscript(t *testing.T) {
	events := make(chan memaxagent.Event, 8)
	events <- memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "I will inspect the failure."}}},
	}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{
		Name:  "start_command",
		Input: []byte(`{"command":"npm test -- --watch"}`),
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
		PID:       123,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventVerification, Verification: &memaxagent.VerificationEvent{
		Name:   "npm test",
		Passed: true,
	}}
	close(events)

	var out bytes.Buffer
	renderer := &appRenderState{}
	if err := renderWith(&out, events, renderer); err != nil {
		t.Fatalf("renderWith(app) error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"session 00000000-0000-7000-8000-000000000001",
		"I will inspect the failure.",
		"• Bash(npm test -- --watch)",
		"• Bash(npm test -- --watch) started id=cmd-1 pid=123",
		"✓ check npm test passed=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("app output missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"[active]", "[transcript]", "Memax Code | phase=", "\x1b[H\x1b[2J"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("app output leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestAppRenderPrintsErrorBeforeReturning(t *testing.T) {
	wantErr := errors.New("boom")
	var out bytes.Buffer
	renderer := &appRenderState{}

	err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventError, Err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Render() error = %v, want %v", err, wantErr)
	}
	got := out.String()
	if !strings.Contains(got, "! error: boom") {
		t.Fatalf("app output missing error:\n%s", got)
	}
}

func TestAppRenderPrintsCompactSessionLineWithoutStructuredHeader(t *testing.T) {
	var out bytes.Buffer
	renderer := &appRenderState{}

	if err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}); err != nil {
		t.Fatalf("Render(session) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "session 00000000-0000-7000-8000-000000000001") {
		t.Fatalf("app output missing compact session line:\n%s", got)
	}
	for _, unwanted := range []string{"[session]", "id: 00000000-0000-7000-8000-000000000001"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("app output leaked structured session chrome %q:\n%s", unwanted, got)
		}
	}
}

func TestAppRenderDropsResultAndUsageMetadata(t *testing.T) {
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{Kind: memaxagent.EventResult, Result: "final answer duplicate"}
	events <- memaxagent.Event{Kind: memaxagent.EventUsage, Usage: &model.Usage{}}
	close(events)

	var out bytes.Buffer
	if err := renderWith(&out, events, &appRenderState{}); err != nil {
		t.Fatalf("renderWith(app) error = %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "" {
		t.Fatalf("app output = %q, want result/usage metadata omitted", got)
	}
}

func TestAppRenderHandleKeyCtrlCCancels(t *testing.T) {
	var out bytes.Buffer
	renderer := &appRenderState{}
	err := renderer.HandleKey(&out, rawKey{kind: rawKeyCtrlC})
	if !errors.Is(err, contextCanceled) {
		t.Fatalf("HandleKey(CtrlC) error = %v, want contextCanceled", err)
	}
}

func TestAppTranscriptTailBoundsStoredLinesAndKeepsPartial(t *testing.T) {
	var tail appTranscriptTail
	tail.limit = 3
	tail.append("one\ntwo\n")
	tail.append("three\nfour\npartial")

	got := tail.lines(10)
	want := []string{"two", "three", "four", "partial"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tail lines = %#v, want %#v", got, want)
	}
	if len(tail.entries) != 3 {
		t.Fatalf("stored line count = %d, want 3", len(tail.entries))
	}
}

func TestAppTranscriptTailStandaloneLineFlushesPartial(t *testing.T) {
	var tail appTranscriptTail
	tail.append("streaming partial")
	tail.appendStandaloneLine("local row")

	got := tail.lines(10)
	want := []string{"streaming partial", "local row"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tail lines = %#v, want %#v", got, want)
	}
	if tail.partial != "" {
		t.Fatalf("partial = %q, want empty", tail.partial)
	}
}

func TestAppRenderNormalizesCarriageReturnTranscriptChunks(t *testing.T) {
	var tail appTranscriptTail

	tail.append(normalizeAppTranscriptText("[assistant]\rhello\rworld\n"))

	got := tail.lines(10)
	want := []string{"[assistant]", "hello", "world"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("transcript lines = %#v, want %#v", got, want)
	}
}

func TestAppRenderNormalizesCRLFTranscriptChunks(t *testing.T) {
	var tail appTranscriptTail

	tail.append(normalizeAppTranscriptText("[assistant]\r\nhello\r\nworld\r\n"))

	got := tail.lines(10)
	want := []string{"[assistant]", "hello", "world"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("transcript lines = %#v, want %#v", got, want)
	}
}

func TestLiveRenderEventsPrintsTransientStatusAndFinalStatus(t *testing.T) {
	events := make(chan memaxagent.Event, 6)
	events <- memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
		PID:       123,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "run_command", Content: "ok"}}
	events <- memaxagent.Event{Kind: memaxagent.EventResult, Result: "done"}
	close(events)

	var out bytes.Buffer
	if err := renderWith(&out, events, &liveRenderState{statusWidth: 160}); err != nil {
		t.Fatalf("renderWith() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		clearLine + "Memax Code | running",
		"active=run_command",
		"active_cmd=cmd-1",
		clearLine + "\n[status]",
		"phase=done",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("live output missing %q:\n%q", want, got)
		}
	}
}

func TestRenderWithTicksRendererWhileEventStreamIsIdle(t *testing.T) {
	events := make(chan memaxagent.Event)
	ticks := make(chan time.Time)
	renderer := &tickSpyRenderer{ticked: make(chan struct{})}
	done := make(chan error, 1)

	go func() {
		done <- renderWithTicks(&bytes.Buffer{}, events, renderer, ticks)
	}()

	ticks <- time.Now()
	select {
	case <-renderer.ticked:
	case <-time.After(time.Second):
		t.Fatal("renderer did not receive idle tick")
	}

	close(events)
	if err := <-done; err != nil {
		t.Fatalf("renderWithTicks() error = %v", err)
	}
	if renderer.finished != 1 {
		t.Fatalf("Finish calls = %d, want 1", renderer.finished)
	}
}

func TestRenderWithTicksPollerObservedFinishesOnCancel(t *testing.T) {
	events := make(chan memaxagent.Event)
	ticks := make(chan time.Time, 1)
	renderer := &cancelSpyRenderer{}
	poller := &stubRawKeyPoller{keys: []rawKey{{kind: rawKeyCtrlC}}}
	done := make(chan error, 1)

	go func() {
		done <- renderWithTicksPollerObserved(&bytes.Buffer{}, events, renderer, ticks, poller, nil)
	}()

	ticks <- time.Now()
	err := <-done
	if !errors.Is(err, contextCanceled) {
		t.Fatalf("renderWithTicksPollerObserved() error = %v, want contextCanceled", err)
	}
	if renderer.finishCalls != 1 {
		t.Fatalf("renderer.Finish() calls = %d, want 1", renderer.finishCalls)
	}
}

func TestRenderEventsWithModeObservedReceivesEvents(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}
	close(events)

	var observed []memaxagent.EventKind
	if err := renderEventsWithModeObserved(&bytes.Buffer{}, events, renderModeTUI, func(event memaxagent.Event) {
		observed = append(observed, event.Kind)
	}); err != nil {
		t.Fatalf("renderEventsWithModeObserved() error = %v", err)
	}
	if len(observed) != 1 || observed[0] != memaxagent.EventSessionStarted {
		t.Fatalf("observed = %#v, want session started", observed)
	}
}

func TestLiveRenderTickAnimatesStatusWhileRunning(t *testing.T) {
	var out bytes.Buffer
	start := time.Date(2026, 4, 22, 19, 0, 0, 0, time.UTC)
	now := start
	renderer := &liveRenderState{statusWidth: 120, now: func() time.Time { return now }}
	if err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}); err != nil {
		t.Fatalf("Render(session) error = %v", err)
	}
	if err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}}); err != nil {
		t.Fatalf("Render(tool start) error = %v", err)
	}
	now = start.Add(90 * time.Second)
	if err := renderer.Tick(&out); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	now = start.Add(91 * time.Second)
	if err := renderer.Tick(&out); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		clearLine + "Memax Code - | running",
		clearLine + "Memax Code \\ | running",
		"elapsed=1m30s",
		"elapsed=1m31s",
		"tools=1",
		"active=run_command",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("live output missing %q:\n%q", want, got)
		}
	}
}

func TestLiveRenderTruncatesTransientStatusToTerminalWidth(t *testing.T) {
	t.Setenv("COLUMNS", "40")

	events := make(chan memaxagent.Event, 4)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "very_long_tool_name_that_would_wrap"}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./... && go vet ./... && go test ./...",
		PID:       123,
	}}
	close(events)

	var out bytes.Buffer
	if err := renderWith(&out, events, &liveRenderState{}); err != nil {
		t.Fatalf("renderWith() error = %v", err)
	}
	for _, status := range liveStatusLines(out.String()) {
		if len([]rune(status)) > 39 {
			t.Fatalf("status line width = %d, want <= 39: %q", len([]rune(status)), status)
		}
	}
}

func TestLiveRenderUsesConfiguredTerminalWidth(t *testing.T) {
	t.Setenv("COLUMNS", "120")

	events := make(chan memaxagent.Event, 4)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "very_long_tool_name_that_would_wrap"}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./... && go vet ./... && go test ./...",
		PID:       123,
	}}
	close(events)

	var out bytes.Buffer
	if err := renderWith(&out, events, &liveRenderState{statusWidth: 24}); err != nil {
		t.Fatalf("renderWith() error = %v", err)
	}
	for _, status := range liveStatusLines(out.String()) {
		if len([]rune(status)) > 24 {
			t.Fatalf("status line width = %d, want <= 24: %q", len([]rune(status)), status)
		}
	}
}

func TestLiveRenderStatusIncludesCompactCounts(t *testing.T) {
	start := time.Date(2026, 4, 22, 19, 0, 0, 0, time.UTC)
	now := start.Add(3 * time.Second)
	renderer := &liveRenderState{
		statusWidth: 160,
		startedAt:   start,
		now:         func() time.Time { return now },
	}
	renderer.transcript.headerWritten = true
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./...",
		PID:       123,
	}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventWorkspacePatch, Workspace: &memaxagent.WorkspaceEvent{
		Paths:   []string{"README.md"},
		Changes: 1,
	}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventVerification, Verification: &memaxagent.VerificationEvent{
		Name:   "go test ./...",
		Passed: true,
	}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "run_command",
		IsError: true,
	}})

	got := renderer.statusLine("", renderer.transcript.activity.snapshot())
	for _, want := range []string{
		"Memax Code | running",
		"tool_errors=1",
		"elapsed=3s",
		"last_tool=run_command",
		"active_cmd=cmd-1",
		"tools=1 commands=1 patches=1 checks=1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("statusLine() missing %q:\n%s", want, got)
		}
	}
}

func TestLiveRenderPrioritizesErrorsUnderNarrowWidth(t *testing.T) {
	start := time.Date(2026, 4, 22, 19, 0, 0, 0, time.UTC)
	renderer := &liveRenderState{
		statusWidth: 60,
		startedAt:   start,
		now:         func() time.Time { return start.Add(3 * time.Second) },
	}
	renderer.transcript.headerWritten = true
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "very_long_tool_name_that_would_wrap_status"}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "go test ./... && go vet ./... && go test ./...",
		PID:       123,
	}})
	renderer.transcript.activity.apply(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "very_long_tool_name_that_would_wrap_status",
		IsError: true,
	}})

	got := renderer.statusLine("", renderer.transcript.activity.snapshot())
	if len([]rune(got)) > 60 {
		t.Fatalf("statusLine() width = %d, want <= 60: %q", len([]rune(got)), got)
	}
	for _, want := range []string{
		"Memax Code | running",
		"tool_errors=1",
		"elapsed=3s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("statusLine() missing priority field %q:\n%s", want, got)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{name: "seconds", elapsed: 3 * time.Second, want: "3s"},
		{name: "truncated seconds", elapsed: 1500 * time.Millisecond, want: "1s"},
		{name: "minutes", elapsed: 90 * time.Second, want: "1m30s"},
		{name: "hours", elapsed: 3*time.Hour + 5*time.Minute + 59*time.Second, want: "3h05m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatElapsed(tt.elapsed); got != tt.want {
				t.Fatalf("formatElapsed(%v) = %q, want %q", tt.elapsed, got, tt.want)
			}
		})
	}
}

func TestTruncateStatusLinePreservesUTF8(t *testing.T) {
	got := truncateStatusLine("Memax Code | active=工具工具工具", 24)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncateStatusLine() = %q, want ellipsis", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("truncateStatusLine() = %q, want valid UTF-8 without replacement runes", got)
	}
}

func TestTruncateStatusLinePreservesANSISequences(t *testing.T) {
	line := "\x1b[1mMemax Code | running | verifying long command\x1b[0m"
	got := truncateStatusLine(line, 20)
	if ansi.StringWidth(got) > 20 {
		t.Fatalf("truncateStatusLine() width = %d, want <= 20: %q", ansi.StringWidth(got), got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("truncateStatusLine() = %q, want reset sequence preserved", got)
	}
}

func TestLiveRenderDoesNotDrawStatusInsideAssistantLine(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "thinking"}}},
	}
	close(events)

	var out bytes.Buffer
	if err := renderWith(&out, events, &liveRenderState{}); err != nil {
		t.Fatalf("renderWith() error = %v", err)
	}
	got := out.String()
	if strings.Contains(got, clearLine+"Memax Code | running") {
		t.Fatalf("live output = %q, want no transient status while assistant line is open", got)
	}
	if !strings.Contains(got, "thinking\n\n[status]") {
		t.Fatalf("live output = %q, want final status after assistant line closes", got)
	}
}

func TestRenderTUIEventsTracksActivityStatus(t *testing.T) {
	events := make(chan memaxagent.Event, 8)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "start_command"}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "npm test -- --watch",
		PID:       123,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventApprovalRequested, Approval: &memaxagent.ApprovalEvent{
		Action: "workspace_apply_patch",
		Summary: memaxagent.ApprovalSummaryEvent{
			Title: "Apply patch",
		},
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventApprovalGranted, Approval: &memaxagent.ApprovalEvent{Action: "workspace_apply_patch"}}
	events <- memaxagent.Event{Kind: memaxagent.EventVerification, Verification: &memaxagent.VerificationEvent{
		Name:   "go test ./...",
		Passed: true,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "start_command", Content: "started"}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`$ command id=cmd-1 pid=123 command="npm test -- --watch"`,
		`[activity]`,
		`? approval requested: workspace_apply_patch title="Apply patch"`,
		`+ approval granted: workspace_apply_patch`,
		`+ check go test ./... passed=true`,
		`< tool start_command ok`,
		`  result: started`,
		`approval_events=2`,
		`last_tool="start_command"`,
		`last_command="npm test -- --watch"`,
		`last_command_status="id=cmd-1 status=running pid=123 command=npm test -- --watch"`,
		`last_verification="go test ./..."`,
		`last_approval="granted:workspace_apply_patch"`,
		`phase=running`,
		`phase: running`,
		`summary: tools=1 commands=1 patches=0 verifications=1`,
		`active_commands:`,
		`  - id=cmd-1 status=running pid=123 command=npm test -- --watch`,
		`recent:`,
		`  command: npm test -- --watch`,
		`  command_status: id=cmd-1 status=running pid=123 command=npm test -- --watch`,
		`  verification: go test ./...`,
		`  approval: granted:workspace_apply_patch`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `active_tool="start_command"`) {
		t.Fatalf("tui output = %q, want active tool cleared after result", got)
	}
}

func TestRenderTUIEventsQuotesCommandAndHandlesEmptyDisplay(t *testing.T) {
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   `grep -RInE "id=123|timeout=false" README.md`,
		PID:       123,
	}}
	events <- memaxagent.Event{Kind: memaxagent.EventCommandFinished, Command: &memaxagent.CommandEvent{CommandID: "cmd-1", ExitCode: 0}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`$ command id=cmd-1 pid=123 command="grep -RInE \"id=123|timeout=false\" README.md"`,
		`+ command id=cmd-1 exit=0 timeout=false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "command  exit") {
		t.Fatalf("tui output = %q, want no double-space command row", got)
	}
}

func TestRenderTUIEventsIndentsMultilineToolResult(t *testing.T) {
	events := make(chan memaxagent.Event, 1)
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "run_command",
		Content: "line one\nline two\nline three",
	}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "< tool run_command ok\n  result: line one\n  line two\n  line three\n") {
		t.Fatalf("tui output = %q, want all result lines indented", got)
	}
}

func TestRenderTUIEventsKeepsOverlappingActiveTool(t *testing.T) {
	events := make(chan memaxagent.Event, 4)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "first"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "second"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "first", Content: "ok"}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, `active_tool="second"`) {
		t.Fatalf("tui output = %q, want second tool to remain active", got)
	} else if !strings.Contains(got, "active_tools:\n  - second\n") {
		t.Fatalf("tui output = %q, want active tools panel", got)
	}
}

func TestRenderTUIStatusPanelCollapsesMultilineRecentValues(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{Kind: memaxagent.EventCommandStarted, Command: &memaxagent.CommandEvent{
		CommandID: "cmd-1",
		Command:   "echo first\necho second",
	}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "recent:\n  command: echo first echo second\n") {
		t.Fatalf("tui output = %q, want single-line recent command", got)
	}
	if strings.Contains(got, "\necho second\n") {
		t.Fatalf("tui output = %q, want no dangling multiline command", got)
	}
}

func TestRenderTUIEventsDoesNotDoublePushFinalizedActiveTool(t *testing.T) {
	events := make(chan memaxagent.Event, 5)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "first"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "second"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUse, ToolUse: &model.ToolUse{Name: "first"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "first", Content: "ok"}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `active_tool="second"`) {
		t.Fatalf("tui output = %q, want second tool to remain active", got)
	}
	if strings.Contains(got, `active_tool="first"`) {
		t.Fatalf("tui output = %q, want first tool removed after result", got)
	}
}

func TestRenderTUIEventsTruncatesLongActivityValues(t *testing.T) {
	long := strings.Repeat("a", 120)
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: long}}
	events <- memaxagent.Event{Kind: memaxagent.EventWorkspacePatch, Workspace: &memaxagent.WorkspaceEvent{
		Paths:   []string{long},
		Changes: 1,
	}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `last_tool="`) || !strings.Contains(got, `..."`) {
		t.Fatalf("tui output = %q, want truncated long tool name", got)
	}
	if strings.Contains(got, `last_tool="`+long+`"`) {
		t.Fatalf("tui output = %q, want long tool name truncated", got)
	}
	if strings.Contains(got, long+` changes=1"`) {
		t.Fatalf("tui output = %q, want long patch status truncated", got)
	}
}

func TestRenderTUIEventsPrintsTitleOnlyApproval(t *testing.T) {
	events := make(chan memaxagent.Event, 1)
	events <- memaxagent.Event{Kind: memaxagent.EventApprovalRequested, Approval: &memaxagent.ApprovalEvent{
		Summary: memaxagent.ApprovalSummaryEvent{Title: "Apply patch"},
	}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `requested: title="Apply patch"`) {
		t.Fatalf("tui output = %q, want title-only approval rendered", got)
	}
	if !strings.Contains(got, `last_approval="requested:Apply patch"`) {
		t.Fatalf("tui output = %q, want title-only approval in status", got)
	}
}

func TestRenderTUIEventsSummarizesMultiPathPatch(t *testing.T) {
	events := make(chan memaxagent.Event, 1)
	events <- memaxagent.Event{Kind: memaxagent.EventWorkspacePatch, Workspace: &memaxagent.WorkspaceEvent{
		Paths:   []string{"README.md", "cmd/main.go", "internal/cli/render.go"},
		Changes: 3,
	}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, `last_patch="paths=3 first=README.md changes=3"`) {
		t.Fatalf("tui output = %q, want summarized patch status", got)
	}
}

func TestRenderTUIEventsCoalescesAssistantStreamChunks(t *testing.T) {
	events := make(chan memaxagent.Event, 4)
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: "hello"}}},
	}
	events <- memaxagent.Event{
		Kind:    memaxagent.EventAssistant,
		Message: &model.Message{Content: []model.ContentBlock{{Type: model.ContentText, Text: " world"}}},
	}
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseDelta, ToolUseDelta: `{"cmd"`}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Content: "ok"}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[assistant]\nhello world\n") {
		t.Fatalf("tui output = %q, want coalesced assistant text", got)
	}
	if !strings.Contains(got, "[activity]\n< tool <unknown> ok\n  result: ok\n") {
		t.Fatalf("tui output = %q, want following event on its own section", got)
	}
}

func TestRenderEventsDrainsAfterErrorEvent(t *testing.T) {
	wantErr := errors.New("boom")
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{Kind: memaxagent.EventError, Err: wantErr}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Content: "still rendered"}}
	close(events)

	var out bytes.Buffer
	err := renderEvents(&out, events)
	if !errors.Is(err, wantErr) {
		t.Fatalf("renderEvents() error = %v, want %v", err, wantErr)
	}
	if got := out.String(); !strings.Contains(got, "still rendered") {
		t.Fatalf("render output = %q, want events after error drained", got)
	}
}

func TestRenderTUIEventsDrainsAfterErrorEvent(t *testing.T) {
	wantErr := errors.New("boom")
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{Kind: memaxagent.EventError, Err: wantErr}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Content: "still rendered"}}
	close(events)

	var out bytes.Buffer
	err := renderEventsWithMode(&out, events, renderModeTUI)
	if !errors.Is(err, wantErr) {
		t.Fatalf("renderEventsWithMode() error = %v, want %v", err, wantErr)
	}
	got := out.String()
	if !strings.Contains(got, "[error]\nboom\n") {
		t.Fatalf("tui output = %q, want error section", got)
	}
	if !strings.Contains(got, "still rendered") {
		t.Fatalf("tui output = %q, want events after error drained", got)
	}
	if !strings.Contains(got, "error=true") || !strings.Contains(got, "phase=error") {
		t.Fatalf("tui output = %q, want terminal error status", got)
	}
}

func TestRenderTUIEventsCountsToolErrors(t *testing.T) {
	events := make(chan memaxagent.Event, 2)
	events <- memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}}
	events <- memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{Name: "run_command", IsError: true}}
	close(events)

	var out bytes.Buffer
	if err := renderEventsWithMode(&out, events, renderModeTUI); err != nil {
		t.Fatalf("renderEventsWithMode() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "tool_errors=1") {
		t.Fatalf("tui output = %q, want tool error count", got)
	}
}

func TestRenderEventHandlesNilErrorEvent(t *testing.T) {
	err := renderEvent(&bytes.Buffer{}, memaxagent.Event{Kind: memaxagent.EventError})
	if err == nil || !strings.Contains(err.Error(), "agent emitted error event") {
		t.Fatalf("renderEvent() error = %v, want nil error fallback", err)
	}
}

func liveStatusLines(output string) []string {
	var lines []string
	for _, chunk := range strings.Split(output, clearLine) {
		if chunk == "" || strings.HasPrefix(chunk, "\n") {
			continue
		}
		line, _, _ := strings.Cut(chunk, "\n")
		if strings.HasPrefix(line, "Memax Code") && strings.Contains(line, " | ") {
			lines = append(lines, line)
		}
	}
	return lines
}

type tickSpyRenderer struct {
	ticked   chan struct{}
	ticks    int
	finished int
}

func (r *tickSpyRenderer) Render(io.Writer, memaxagent.Event) error {
	return nil
}

func (r *tickSpyRenderer) Finish(io.Writer) error {
	r.finished++
	return nil
}

func (r *tickSpyRenderer) Tick(io.Writer) error {
	r.ticks++
	if r.ticks == 1 {
		close(r.ticked)
	}
	return nil
}

func (r *tickSpyRenderer) TickInterval() time.Duration {
	return time.Hour
}

type cancelSpyRenderer struct {
	finishCalls int
}

func (r *cancelSpyRenderer) Render(io.Writer, memaxagent.Event) error {
	return nil
}

func (r *cancelSpyRenderer) Finish(io.Writer) error {
	r.finishCalls++
	return nil
}

func (r *cancelSpyRenderer) HandleKey(io.Writer, rawKey) error {
	return contextCanceled
}

func (r *cancelSpyRenderer) Tick(io.Writer) error {
	return nil
}

func (r *cancelSpyRenderer) TickInterval() time.Duration {
	return time.Hour
}

type stubRawKeyPoller struct {
	keys []rawKey
}

func (p *stubRawKeyPoller) Poll() ([]rawKey, error) {
	keys := append([]rawKey(nil), p.keys...)
	p.keys = nil
	return keys, nil
}

func (p *stubRawKeyPoller) Close() error {
	return nil
}
