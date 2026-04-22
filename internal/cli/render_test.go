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
	terminal, width := terminalWriterInfo(ptmx)
	if !terminal {
		t.Fatal("terminalWriterInfo() terminal = false, want true")
	}
	if width != 36 {
		t.Fatalf("terminalWriterInfo() width = %d, want 36", width)
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
		Command:  "go test ./...",
		Argv:     []string{"sh", "-c", "go test ./..."},
		ExitCode: 0,
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
		"[tool]\nstart: run_command",
		"[command]\ngo test ./... exit=0 timeout=false",
		"[workspace]\npatch: paths=README.md changes=1",
		"[result]\ndone",
		"[status]\nsession: 00000000-0000-7000-8000-000000000001\ntools=1 commands=1 patches=1 verifications=0 done=true",
		`last_tool="run_command"`,
		`last_command="go test ./..."`,
		`last_patch="README.md changes=1"`,
		"phase=done",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
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
	if err := renderWith(&out, events, &liveRenderState{}); err != nil {
		t.Fatalf("renderWith() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		clearLine + "Memax Code | running",
		"active=run_command",
		"cmd=go test ./...",
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

func TestLiveRenderTickAnimatesStatusWhileRunning(t *testing.T) {
	var out bytes.Buffer
	renderer := &liveRenderState{statusWidth: 80}
	if err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventSessionStarted, SessionID: "00000000-0000-7000-8000-000000000001"}); err != nil {
		t.Fatalf("Render(session) error = %v", err)
	}
	if err := renderer.Render(&out, memaxagent.Event{Kind: memaxagent.EventToolUseStart, ToolUse: &model.ToolUse{Name: "run_command"}}); err != nil {
		t.Fatalf("Render(tool start) error = %v", err)
	}
	if err := renderer.Tick(&out); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if err := renderer.Tick(&out); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		clearLine + "Memax Code - | running",
		clearLine + "Memax Code \\ | running",
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
	for _, line := range strings.Split(out.String(), clearLine) {
		if line == "" || strings.HasPrefix(line, "\n") {
			continue
		}
		status, _, _ := strings.Cut(line, "\n")
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
	for _, line := range strings.Split(out.String(), clearLine) {
		if line == "" || strings.HasPrefix(line, "\n") {
			continue
		}
		status, _, _ := strings.Cut(line, "\n")
		if len([]rune(status)) > 24 {
			t.Fatalf("status line width = %d, want <= 24: %q", len([]rune(status)), status)
		}
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
		`started: cmd-1 pid=123 command="npm test -- --watch"`,
		`[approval]`,
		`requested: workspace_apply_patch title="Apply patch"`,
		`granted: workspace_apply_patch`,
		`approval_events=2`,
		`last_tool="start_command"`,
		`last_command="npm test -- --watch"`,
		`last_verification="go test ./..."`,
		`last_approval="granted:workspace_apply_patch"`,
		`phase=running`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `active_tool="start_command"`) {
		t.Fatalf("tui output = %q, want active tool cleared after result", got)
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
	if !strings.Contains(got, "[tool]\nresult: ok\n") {
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
