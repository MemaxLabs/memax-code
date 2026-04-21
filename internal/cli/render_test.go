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

func TestRenderEventHandlesNilErrorEvent(t *testing.T) {
	err := renderEvent(&bytes.Buffer{}, memaxagent.Event{Kind: memaxagent.EventError})
	if err == nil || !strings.Contains(err.Error(), "agent emitted error event") {
		t.Fatalf("renderEvent() error = %v, want nil error fallback", err)
	}
}
