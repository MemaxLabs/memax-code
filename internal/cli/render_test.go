package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
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
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tui output missing %q:\n%s", want, got)
		}
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
}

func TestRenderEventHandlesNilErrorEvent(t *testing.T) {
	err := renderEvent(&bytes.Buffer{}, memaxagent.Event{Kind: memaxagent.EventError})
	if err == nil || !strings.Contains(err.Error(), "agent emitted error event") {
		t.Fatalf("renderEvent() error = %v, want nil error fallback", err)
	}
}
