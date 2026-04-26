package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/session"
)

const interactiveScannerMaxBytes = 1024 * 1024

type interactiveInputObserver interface {
	ObservePrompt(prompt string, buffer composerBuffer, composer *interactiveComposer)
}

type interactivePromptRunner func(context.Context, io.Writer, options) (string, error)
type interactiveEventPromptRunner func(context.Context, options, func(memaxagent.Event)) (string, error)

func runInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, opts options) error {
	return runInteractiveWithEventRunner(ctx, stdin, stdout, stderr, opts, runPromptWithSession, runPromptWithEvents)
}

func runInteractiveWithRunner(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, opts options, runPrompt interactivePromptRunner) error {
	return runInteractiveWithEventRunner(ctx, stdin, stdout, stderr, opts, runPrompt, nil)
}

func runInteractiveWithEventRunner(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, opts options, runPrompt interactivePromptRunner, runEvents interactiveEventPromptRunner) error {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	if runPrompt == nil {
		runPrompt = runPromptWithSession
	}
	resolvedUI := resolveInteractiveMode(opts.UI, stdout)
	if resolvedUI == renderModeApp {
		return runInteractiveAppWithEvents(ctx, stdin, stdout, opts, runPrompt, runEvents)
	}
	shellOut := stderr
	var inputObserver interactiveInputObserver
	currentSession := opts.ResumeSessionID
	composer := &interactiveComposer{}
	historyStore := newPersistentPromptHistory(opts.HistoryFile)
	if entries, err := historyStore.Load(); err != nil {
		fmt.Fprintf(shellOut, "warning: %v\n", err)
	} else {
		composer.loadHistory(entries)
	}
	fmt.Fprintln(shellOut, "Memax Code interactive shell")
	fmt.Fprintln(shellOut, "Type /help for commands, /quit to exit.")

	lineReader, err := newInteractiveLineReader(stdin, shellOut, inputObserver)
	if err != nil {
		return err
	}
	var firstErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rawLine, ok, err := lineReader.ReadLine(ctx, composer.promptLabel(), composer)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		commandLine := strings.TrimSpace(rawLine)
		if commandLine == "" {
			if composer.draftActive {
				composer.appendLine(rawLine)
			}
			continue
		}
		commandCandidate := commandLine
		if composer.draftActive {
			commandCandidate = rawLine
		}
		if isInteractiveCommandLine(commandCandidate) {
			result := handleInteractiveCommand(ctx, shellOut, opts, &currentSession, composer, commandCandidate)
			if result.Done {
				return firstErr
			}
			if result.SubmitPrompt == "" {
				continue
			}
			rawLine = result.SubmitPrompt
		} else if composer.draftActive {
			composer.appendLine(unescapeInteractivePrompt(rawLine))
			continue
		} else {
			rawLine = strings.TrimSpace(unescapeInteractivePrompt(rawLine))
		}

		promptText := rawLine
		turnOpts := opts
		turnOpts.Prompt = promptText
		turnOpts.ResumeSessionID = currentSession
		turnOpts.UI = resolvedUI
		sessionID, err := runPrompt(ctx, stdout, turnOpts)
		if err != nil {
			if sessionID != "" {
				currentSession = sessionID
			}
			fmt.Fprintf(shellOut, "error: %v\n", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if sessionID != "" {
			currentSession = sessionID
		}
		if composer.history.Record(promptText) {
			if err := historyStore.Append(promptText); err != nil {
				fmt.Fprintf(shellOut, "warning: %v\n", err)
			}
		}
	}
	return firstErr
}

func resolveInteractiveMode(mode renderMode, stdout io.Writer) renderMode {
	if mode == renderModeAuto || mode == renderModeApp {
		if writerIsTerminal(stdout) {
			return renderModeApp
		}
		return renderModePlain
	}
	return mode
}

type interactiveCommandResult struct {
	Done         bool
	SubmitPrompt string
}

func handleInteractiveCommand(ctx context.Context, w io.Writer, opts options, currentSession *string, composer *interactiveComposer, line string) interactiveCommandResult {
	name, arg := splitInteractiveCommand(line)
	switch name {
	case "/quit", "/exit":
		fmt.Fprintln(w, "bye")
		return interactiveCommandResult{Done: true}
	case "/help":
		printInteractiveHelp(w)
	case "/new":
		*currentSession = ""
		fmt.Fprintln(w, "started a new session")
	case "/session":
		if strings.TrimSpace(*currentSession) == "" {
			fmt.Fprintln(w, "no active session")
		} else {
			fmt.Fprintf(w, "session: %s\n", *currentSession)
		}
	case "/status":
		if err := printInteractiveStatus(ctx, w, opts, *currentSession); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
		if composer != nil {
			fmt.Fprintf(w, "  %s\n", composer.statusLine())
		}
	case "/draft":
		if composer == nil {
			fmt.Fprintln(w, "drafts are unavailable")
			return interactiveCommandResult{}
		}
		if composer.draftActive && composer.lineCount() > 0 {
			fmt.Fprintf(w, "discarded draft: lines=%d\n", composer.lineCount())
		}
		composer.start(unescapeInteractivePrompt(arg))
		fmt.Fprintln(w, "draft started; type lines, /submit to send, /cancel to discard")
	case "/append":
		if composer == nil {
			fmt.Fprintln(w, "drafts are unavailable")
			return interactiveCommandResult{}
		}
		if !composer.draftActive {
			fmt.Fprintln(w, "draft started; type lines, /submit to send, /cancel to discard")
		}
		composer.appendLine(unescapeInteractivePrompt(arg))
		fmt.Fprintf(w, "draft appended: lines=%d\n", composer.lineCount())
	case "/draft-show", "/show-draft":
		printInteractiveDraft(w, composer)
	case "/history":
		printInteractivePromptHistory(w, composer)
	case "/recall":
		if composer == nil {
			fmt.Fprintln(w, "drafts are unavailable")
			return interactiveCommandResult{}
		}
		text, discardedLines, ok := composer.recall(arg)
		if !ok {
			fmt.Fprintln(w, "no matching prompt history")
			return interactiveCommandResult{}
		}
		if discardedLines > 0 {
			fmt.Fprintf(w, "discarded draft: lines=%d\n", discardedLines)
		}
		fmt.Fprintf(w, "recalled prompt: lines=%d chars=%d\n", strings.Count(text, "\n")+1, len([]rune(text)))
	case "/cancel":
		if composer == nil || !composer.draftActive {
			fmt.Fprintln(w, "no active draft")
		} else {
			composer.cancel()
			fmt.Fprintln(w, "draft canceled")
		}
	case "/submit":
		if composer == nil {
			fmt.Fprintln(w, "drafts are unavailable")
			return interactiveCommandResult{}
		}
		if !composer.draftActive {
			fmt.Fprintln(w, "no active draft")
			return interactiveCommandResult{}
		}
		prompt, ok := composer.submit()
		if !ok {
			fmt.Fprintln(w, "draft is empty")
			return interactiveCommandResult{}
		}
		return interactiveCommandResult{SubmitPrompt: prompt}
	case "/show":
		if err := showInteractiveSession(ctx, w, opts, *currentSession, arg); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	case "/resume":
		if arg == "" {
			fmt.Fprintln(w, "usage: /resume SESSION_ID|latest|N")
			return interactiveCommandResult{}
		}
		id, err := resolveInteractiveSessionID(ctx, opts, arg, "resume")
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return interactiveCommandResult{}
		}
		*currentSession = id
		fmt.Fprintf(w, "resumed session: %s\n", id)
	case "/pick":
		if err := printInteractiveSessionPicker(ctx, w, opts); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	case "/sessions":
		if err := listSessions(ctx, w, opts); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	default:
		fmt.Fprintf(w, "unknown command %q; type /help\n", name)
	}
	return interactiveCommandResult{}
}

func printInteractiveStatus(ctx context.Context, w io.Writer, opts options, currentSession string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	candidates, err := sessionCandidates(opts.SessionDir)
	if err != nil {
		return err
	}
	profile, err := parseModelProfile(opts.Profile)
	if err != nil {
		return err
	}
	effort, err := parseModelEffort(opts.Effort)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "status:")
	fmt.Fprintf(w, "  provider: %s\n", opts.Provider)
	fmt.Fprintf(w, "  model: %s\n", valueOrUnset(opts.Model))
	fmt.Fprintf(w, "  profile: %s\n", profile)
	fmt.Fprintf(w, "  effort: %s\n", effort)
	fmt.Fprintf(w, "  preset: %s\n", opts.Preset)
	fmt.Fprintf(w, "  ui: %s\n", opts.UI)
	fmt.Fprintf(w, "  cwd: %s\n", opts.CWD)
	fmt.Fprintf(w, "  session_dir: %s\n", opts.SessionDir)
	fmt.Fprintf(w, "  history_file: %s\n", opts.HistoryFile)
	fmt.Fprintf(w, "  active_session: %s\n", valueOrUnset(currentSession))
	fmt.Fprintf(w, "  saved_sessions: %d\n", len(candidates))
	fmt.Fprintf(w, "  verification: %s\n", verificationMode(opts.CWD, opts.VerifyCommands))
	if len(opts.VerifyCommands) > 0 {
		for _, name := range sortedMapKeys(opts.VerifyCommands) {
			fmt.Fprintf(w, "  verify_command.%s: %s\n", name, opts.VerifyCommands[name])
		}
	}
	fmt.Fprintf(w, "  inherit_command_env: %t\n", opts.InheritCommandEnv)
	return nil
}

func showInteractiveSession(ctx context.Context, w io.Writer, opts options, currentSession, raw string) error {
	raw = strings.TrimSpace(raw)
	switch strings.ToLower(raw) {
	case "":
		if strings.TrimSpace(currentSession) == "" {
			return fmt.Errorf("show session: no active session; use /show latest, /show N, or /show SESSION_ID")
		}
		raw = currentSession
	case "current":
		if strings.TrimSpace(currentSession) == "" {
			return fmt.Errorf("show current session: no active session")
		}
		raw = currentSession
	}
	id, err := resolveInteractiveSessionID(ctx, opts, raw, "show")
	if err != nil {
		return err
	}
	// Interactive shell commands follow the active shell surface. The standalone
	// --show-session command still writes to stdout for scriptability.
	showOpts := opts
	showOpts.ShowSessionID = id
	return showSession(ctx, w, showOpts)
}

func resolveInteractiveSessionID(ctx context.Context, opts options, raw, action string) (string, error) {
	raw = strings.TrimSpace(raw)
	action = strings.TrimSpace(action)
	if action == "" {
		action = "resolve"
	}
	if index, ok := parseSessionIndex(raw); ok {
		rows, err := loadSessionRows(ctx, session.NewJSONLStore(opts.SessionDir), opts.SessionDir)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			return "", fmt.Errorf("%s session index %d: no sessions", action, index)
		}
		if index < 1 || index > len(rows) {
			return "", fmt.Errorf("%s session index %d out of range; choose 1-%d", action, index, len(rows))
		}
		return rows[index-1].Session.ID, nil
	}
	store := session.NewJSONLStore(opts.SessionDir)
	return resolveSessionID(ctx, store, opts.SessionDir, raw, action)
}

func parseSessionIndex(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return index, true
}

func printInteractiveSessionPicker(ctx context.Context, w io.Writer, opts options) error {
	rows, err := loadSessionRows(ctx, session.NewJSONLStore(opts.SessionDir), opts.SessionDir)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no sessions")
		return err
	}
	fmt.Fprintln(w, "recent sessions:")
	for i, row := range rows {
		title := row.Title
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(w, "  %d) %s  %s  %s\n",
			i+1,
			row.Session.ID,
			row.UpdatedAt.Format("2006-01-02 15:04"),
			title,
		)
	}
	fmt.Fprintln(w, "Use /resume N, /resume latest, or /resume SESSION_ID.")
	return nil
}

func splitInteractiveCommand(line string) (string, string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", ""
	}
	name := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	return name, arg
}

func isInteractiveCommandLine(line string) bool {
	return strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "//")
}

func unescapeInteractivePrompt(line string) string {
	return strings.TrimPrefix(line, "/")
}

func printInteractiveHelp(w io.Writer) {
	fmt.Fprintln(w, "slash commands:")
	fmt.Fprintln(w, "  /help              show this help")
	fmt.Fprintln(w, "  /status            show active runtime settings")
	fmt.Fprintln(w, "  /session           show the active session")
	fmt.Fprintln(w, "  /pick              list recent sessions with numbers")
	fmt.Fprintln(w, "  /show [TARGET]     show current, latest, number, or ID")
	fmt.Fprintln(w, "  /sessions          list saved sessions")
	fmt.Fprintln(w, "  /resume TARGET     resume by ID, latest, or number")
	fmt.Fprintln(w, "  /new               start the next prompt in a new session")
	fmt.Fprintln(w, "  /draft [TEXT]      start a multi-line draft")
	fmt.Fprintln(w, "  /append TEXT       append one line to the draft")
	fmt.Fprintln(w, "  /show-draft        show the active draft")
	fmt.Fprintln(w, "  /submit            send the active draft")
	fmt.Fprintln(w, "  /cancel            discard the active draft")
	fmt.Fprintln(w, "  /history           list remembered prompts")
	fmt.Fprintln(w, "  /recall [N|latest] recall a prompt into the draft")
	fmt.Fprintln(w, "  /quit              exit")
	fmt.Fprintln(w, "  //PROMPT           send a prompt that starts with /")
}
