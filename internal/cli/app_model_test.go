package cli

import (
	"strings"
	"testing"
)

func TestAppShellFrameRendersDeterministicPanels(t *testing.T) {
	activity := activitySnapshot{
		Phase:      "running",
		Tools:      1,
		Commands:   1,
		ActiveTool: "run_command",
		ActiveCommands: []commandActivity{
			{ID: "cmd-1", Command: "go test ./...", Status: "running", PID: 123},
		},
		LastCommandState: "id=cmd-1 status=running pid=123 command=go test ./...",
		LastVerification: "go test ./...",
	}
	frame := newAppShellFrame(activity, []string{"assistant: checking tests"}, 80, 18, "3s")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"Memax Code | phase=running | elapsed=3s | tools=1 commands=1",
		"[active]\n  tool: run_command",
		"  command: id=cmd-1 status=running pid=123 command=go test ./...",
		"[recent]\n  command_status: id=cmd-1 status=running pid=123 command=go test ./...",
		"  verification: go test ./...",
		"[transcript]\nassistant: checking tests",
		"Ctrl+C cancel | /help commands | --ui tui for scrollback logs",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing %q:\n%s", want, got)
		}
	}
}

func TestAppShellFrameAttentionPanelSurfacesApprovalsAndErrors(t *testing.T) {
	activity := activitySnapshot{
		Phase:        "running",
		ToolErrors:   2,
		LastApproval: "requested:Apply patch",
	}
	frame := newAppShellFrame(activity, nil, 80, 14, "")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"[attention]",
		"  tool errors: 2",
		"  approval: Apply patch",
		"[recent]\n  none",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing %q:\n%s", want, got)
		}
	}
}

func TestAppShellFrameAttentionPanelHandlesBareApprovalRequest(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{
		Phase:        "running",
		LastApproval: "requested",
	}, nil, 80, 14, "")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"[attention]\n  approval: requested",
		"[recent]\n  none",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing %q:\n%s", want, got)
		}
	}
}

func TestAppShellFrameTranscriptViewportKeepsRecentLines(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{Phase: "running"}, []string{
		"old",
		"middle",
		"new",
	}, 60, 13, "")

	got := strings.Join(frame.Lines(), "\n")
	if strings.Contains(got, "old") {
		t.Fatalf("frame output included old transcript line:\n%s", got)
	}
	if !strings.Contains(got, "[transcript]\nmiddle\nnew") {
		t.Fatalf("frame output missing recent transcript tail:\n%s", got)
	}
	if lines := frame.Lines(); len(lines) > 13 {
		t.Fatalf("frame height = %d, want <= 13:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

func TestAppTranscriptViewportMarksHiddenEarlierLines(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
	}, 3, 0).Lines()
	want := []string{"↑ 3 earlier lines", "four", "five"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppTranscriptViewportCanScrollBack(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
		"six",
	}, 4, 2).Lines()
	want := []string{"one", "two", "three", "↓ 3 newer lines"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppTranscriptViewportMarksBothDirections(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
		"six",
		"seven",
		"eight",
		"nine",
		"ten",
	}, 3, 5).Lines()
	want := []string{"↑ 3 earlier lines", "four", "↓ 6 newer lines"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppTranscriptViewportClampsOffsetPastTop(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
	}, 3, 99).Lines()
	want := []string{"one", "two", "↓ 3 newer lines"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppTranscriptViewportShowsAllLinesWhenTheyFit(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
	}, 4, 0).Lines()
	want := []string{"one", "two", "three"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppTranscriptViewportOmitsMarkersOnTinyHeights(t *testing.T) {
	got := newAppTranscriptViewport([]string{
		"one",
		"two",
		"three",
		"four",
	}, 2, 0).Lines()
	want := []string{"three", "four"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("viewport lines = %#v, want %#v", got, want)
	}
}

func TestAppHiddenLineFormatsSingularAndPlural(t *testing.T) {
	if got, want := appHiddenLine("↑", 1, "earlier"), "↑ 1 earlier line"; got != want {
		t.Fatalf("hidden line = %q, want %q", got, want)
	}
	if got, want := appHiddenLine("↓", 2, "newer"), "↓ 2 newer lines"; got != want {
		t.Fatalf("hidden line = %q, want %q", got, want)
	}
}
