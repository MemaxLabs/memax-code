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
		"  Bash ok",
		"! Bash error",
		"• Bash(go test ./...) started id=cmd-1",
		"✓ Bash(go test ./...) done exit=0",
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
		"✓ Bash(go test ./...) done exit=0",
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

func TestAppProgramStructuredEventsTailCommandOutputToolResults(t *testing.T) {
	m := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	m.transcript = appTranscriptTail{}

	m.appendEvent(memaxagent.Event{Kind: memaxagent.EventToolResult, ToolResult: &model.ToolResult{
		Name:    "wait_command_output",
		Content: strings.Join([]string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6"}, "\n"),
	}})

	got := ansi.Strip(strings.Join(m.transcript.lines(maxAppTranscriptLines), "\n"))
	for _, want := range []string{
		"Wait for command output ok",
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
}

func TestAppProgramViewShowsActivityOnlyWhileRunning(t *testing.T) {
	model := newAppProgramModel(context.Background(), options{CWD: "."}, nil)
	model.width = 100
	model.running = true
	view := ansi.Strip(model.View())

	if !strings.Contains(view, "thinking") {
		t.Fatalf("running view missing thinking activity:\n%s", view)
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
			if !strings.Contains(got, "paragraph one\n\nparagraph two") {
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
			if got != "foo" {
				t.Fatalf("assistant transcript = %q, want %q", got, "foo")
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
		"  Bash ok",
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
