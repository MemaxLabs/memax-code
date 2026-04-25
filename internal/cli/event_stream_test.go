package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/contextwindow"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
)

func TestParseEventStreamMode(t *testing.T) {
	mode, err := parseEventStreamMode("json")
	if err != nil {
		t.Fatalf("parseEventStreamMode(json) error = %v", err)
	}
	if mode != eventStreamModeJSON {
		t.Fatalf("parseEventStreamMode(json) = %q, want %q", mode, eventStreamModeJSON)
	}
	if _, err := parseEventStreamMode("jsonl"); err == nil {
		t.Fatal("parseEventStreamMode(jsonl) error = nil, want invalid mode")
	}
}

func TestProjectStreamEventIncludesContextCompaction(t *testing.T) {
	event := memaxagent.Event{
		Kind: memaxagent.EventContextCompacted,
		Compaction: &contextwindow.CompactionRecord{
			Policy:             "SummarizingBudget",
			Reason:             contextwindow.CompactionReasonBudget,
			OriginalMessages:   20,
			SentMessages:       8,
			SummarizedMessages: 13,
			RetainedMessages:   1,
			ReplacedSummaries:  0,
			SummaryHash:        "abc123",
			SummaryPreview:     "older context",
		},
	}

	projected := projectStreamEvent(event)
	compaction, ok := projected.Compaction["summarized_messages"].(int)
	if !ok || compaction != 13 {
		t.Fatalf("compaction = %#v, want summarized_messages=13", projected.Compaction)
	}
}

func TestRenderEventStreamObservedJSON(t *testing.T) {
	events := make(chan memaxagent.Event, 3)
	events <- memaxagent.Event{
		Kind:      memaxagent.EventSessionStarted,
		SessionID: "019db69e-3b4f-7d79-a333-34d708f1d4a6",
		Turn:      1,
		Time:      time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
	}
	events <- memaxagent.Event{
		Kind:      memaxagent.EventAssistant,
		SessionID: "019db69e-3b4f-7d79-a333-34d708f1d4a6",
		Turn:      1,
		Message: &model.Message{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{{
				Type: model.ContentText,
				Text: "hello",
			}},
		},
	}
	events <- memaxagent.Event{
		Kind:      memaxagent.EventResult,
		SessionID: "019db69e-3b4f-7d79-a333-34d708f1d4a6",
		Turn:      1,
		Result:    "done",
	}
	close(events)

	var out bytes.Buffer
	if err := renderEventStreamObserved(&out, events, eventStreamModeJSON, nil); err != nil {
		t.Fatalf("renderEventStreamObserved() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("json event lines = %d, want 3\n%s", len(lines), out.String())
	}
	var session, assistant, result map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &session); err != nil {
		t.Fatalf("Unmarshal(session) error = %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &assistant); err != nil {
		t.Fatalf("Unmarshal(assistant) error = %v", err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &result); err != nil {
		t.Fatalf("Unmarshal(result) error = %v", err)
	}
	if session["type"] != "session_started" {
		t.Fatalf("session type = %v, want session_started", session["type"])
	}
	if assistant["text"] != "hello" {
		t.Fatalf("assistant text = %v, want hello", assistant["text"])
	}
	if result["result"] != "done" {
		t.Fatalf("result = %v, want done", result["result"])
	}
}

func TestProjectStreamEventIncludesToolResultMetadata(t *testing.T) {
	event := memaxagent.Event{
		Kind: memaxagent.EventToolResult,
		ToolResult: &model.ToolResult{
			ToolUseID: "delegate-1",
			Name:      "run_subagent",
			Content:   "child result",
			Metadata: map[string]any{
				"parent_session_id": "00000000-0000-7000-8000-000000000001",
				"child_session_id":  "00000000-0000-7000-8000-000000000002",
			},
		},
	}

	projected := projectStreamEvent(event)
	metadata, ok := projected.ToolResult["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("tool result metadata = %#v, want map", projected.ToolResult["metadata"])
	}
	if metadata["child_session_id"] != "00000000-0000-7000-8000-000000000002" {
		t.Fatalf("metadata = %#v, want child session id", metadata)
	}
}

func TestRunRejectsInteractiveEventStream(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunWithIO(context.Background(), []string{
		"--interactive",
		"--event-stream", "json",
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--interactive cannot be combined with --event-stream") {
		t.Fatalf("RunWithIO() error = %v, want interactive/event-stream conflict", err)
	}
}
