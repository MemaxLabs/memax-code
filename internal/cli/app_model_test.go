package cli

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		"• tool run_command call",
		"  tool run_command ok",
		"! tool run_command error",
		"• command id=cmd-1 command=\"go test ./...\"",
		"✓ command command=\"go test ./...\" exit=0 timeout=false",
		"! command cmd-2 stopped status=killed",
		"✓ check go test ./... passed=true",
		"? approval Apply patch",
		"boom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact transcript missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"[session]", "[assistant]", "[activity]", "[result]", "[usage]", "[status]", "[error]", "Assistant", "Activity", "Result", "Usage", "Status", "Error", "$ command", "+ command", "019db69e-3b4f-7d79-a333-34d708f1d4a6", "done", "input=10", "phase: done", "line one", "line two"} {
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
		"! tool run_command error",
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
		"paragraph one",
		"",
		"paragraph two",
		"#123 is not a heading",
		"- inspect the repo",
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
		"paragraph one\n\nparagraph two",
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
}

func TestAppProgramTranscriptPreservesStreamedAssistantBlankLines(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.transcript = appTranscriptTail{}
	model.compactor = appProgramTranscriptCompactor{}

	model.appendTranscript("[assistant]\nparagraph one")
	model.appendTranscript("\n\n")
	model.appendTranscript("paragraph two\n")

	got := ansi.Strip(strings.Join(model.transcript.lines(maxAppTranscriptLines), "\n"))
	if !strings.Contains(got, "paragraph one\n\nparagraph two") {
		t.Fatalf("assistant paragraph break was not preserved:\n%q", got)
	}
}

func TestCompactAppProgramTranscriptTextDoesNotDuplicateTrailingNewline(t *testing.T) {
	var compactor appProgramTranscriptCompactor
	if got, want := compactor.compact("[assistant]\nhello\n"), "hello\n"; ansi.Strip(got) != want {
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
		"! tool run_command error",
		"error tail:",
		"line three",
		"line four",
		"line five",
		"line six",
		"line seven",
		"• tool read_file call",
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
		"! tool run_command error",
		"error tail:",
		"line one",
		"line two",
		"line three",
		"• tool read_file call",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("streamed error tail missing %q:\n%s", want, got)
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
