package cli

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCompactAppProgramTranscriptTextCompactsStructuredSections(t *testing.T) {
	got := compactAppProgramTranscriptText(strings.Join([]string{
		"[session]",
		"id: 019db69e-3b4f-7d79-a333-34d708f1d4a6",
		"[assistant]",
		"working on it",
		"[activity]",
		"> tool run_command call",
		"< tool run_command ok",
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
		"session 019db69e-3b4f-7d79-a333-34d708f1d4a6",
		"working on it",
		"• tool run_command call",
		"  tool run_command ok",
		"! tool run_command error",
		"• command id=cmd-1 command=\"go test ./...\"",
		"✓ command command=\"go test ./...\" exit=0 timeout=false",
		"! command cmd-2 stopped status=killed",
		"✓ check go test ./... passed=true",
		"? approval Apply patch",
		"done",
		"input=10 output=2 total=12",
		"phase: done",
		"boom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"[session]", "[assistant]", "[activity]", "[result]", "[usage]", "[status]", "[error]", "Assistant", "Activity", "Result", "Usage", "Status", "Error", "$ command", "+ command"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compact transcript leaked %q:\n%s", unwanted, got)
		}
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

func TestAppProgramResizeCountsWrappedTranscriptRows(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 10
	model.height = 20
	model.transcript = appTranscriptTail{}
	model.transcript.append("abcdefghijklmnopqrstuvwxyz\n")

	model.resize()

	if got, want := model.viewport.Height, 3; got != want {
		t.Fatalf("viewport height = %d, want %d for wrapped transcript line", got, want)
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
	frame := newAppShellFrame(activity, []string{"assistant: checking tests"}, 120, 18, "3s")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"Memax Code | phase=running | elapsed=3s | tools=1 commands=1",
		"[active]",
		"tool: run_command",
		"command: id=cmd-1 ",
		"[recent]",
		"command_status: id=cmd-1 ",
		"verification: go test ./...",
		"[transcript] live tail",
		"assistant: checking tests",
		"↑/↓ scroll | PgUp/PgDn page | Home/End jump | ? help | Ctrl+C cancel",
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
	frame := newAppShellFrame(activity, nil, 120, 14, "")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"[attention]",
		"tool errors: 2",
		"approval: Apply patch",
		"[recent]",
		"none",
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
	}, nil, 120, 14, "")

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"[attention]",
		"approval: requested",
		"[recent]",
		"none",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing %q:\n%s", want, got)
		}
	}
}

func TestAppShellFrameTranscriptViewportKeepsRecentLines(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{Phase: "running"}, []string{
		"old-1",
		"old-2",
		"old-3",
		"old-4",
		"old-5",
		"old-6",
		"old-7",
		"old-8",
		"old-9",
		"old-10",
		"middle",
		"new",
	}, 120, 14, "")

	got := strings.Join(frame.Lines(), "\n")
	if strings.Contains(got, "old-1\n") || strings.Contains(got, "old-1 ") {
		t.Fatalf("frame output included old transcript line:\n%s", got)
	}
	for _, want := range []string{"[transcript] live tail", "middle", "new", "↑"} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing recent transcript content %q:\n%s", want, got)
		}
	}
	if lines := frame.Lines(); len(lines) > 14 {
		t.Fatalf("frame height = %d, want <= 14:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

func TestAppShellFrameTranscriptHeadingReflectsManualScroll(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{Phase: "running"}, []string{"one", "two", "three"}, 60, 12, "")
	frame.TranscriptStatus = "manual scroll (↓ 2 newer lines below)"

	got := strings.Join(frame.Lines(), "\n")
	if !strings.Contains(got, "[transcript] manual scroll") {
		t.Fatalf("frame output missing manual scroll heading:\n%s", got)
	}
}

func TestAppShellFrameHelpOverlayReplacesTranscriptViewport(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{Phase: "running"}, []string{"one", "two", "three"}, 60, 18, "")
	frame.HelpVisible = true

	got := strings.Join(frame.Lines(), "\n")
	for _, want := range []string{
		"[help] press ? to return",
		"PgUp/PgDn",
		"Ctrl+C cancel",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\none\n") {
		t.Fatalf("help overlay leaked transcript content:\n%s", got)
	}
}

func TestAppShellFrameStacksPanelsOnNarrowWidths(t *testing.T) {
	frame := newAppShellFrame(activitySnapshot{
		Phase:      "running",
		ActiveTool: "run_command",
		ActiveCommands: []commandActivity{
			{ID: "cmd-1", Command: "go test ./...", Status: "running", PID: 123},
		},
		LastVerification: "go test ./...",
	}, []string{
		"assistant: checking tests",
		"assistant: still checking",
	}, 56, 16, "3s")

	lines := frame.Lines()
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"[active]",
		"[recent]",
		"[transcript] live tail",
		"assistant: still checking",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("narrow frame output missing %q:\n%s", want, got)
		}
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
