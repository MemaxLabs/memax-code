package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	if len(opts.MCPServers) > 0 && !opts.RuntimeMCPReady {
		mcpTools, cleanup, err := prepareMCPTools(ctx, opts)
		if err != nil {
			return err
		}
		defer cleanup()
		opts.RuntimeMCPTools = mcpTools
		opts.RuntimeMCPReady = true
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

type interactiveCommandSpec struct {
	Name        string
	Usage       string
	Description string
}

func interactiveCommandSpecs() []interactiveCommandSpec {
	return []interactiveCommandSpec{
		{Name: "/help", Usage: "/help", Description: "show available slash commands"},
		{Name: "/status", Usage: "/status", Description: "show active runtime settings"},
		{Name: "/context", Usage: "/context [TARGET]", Description: "show context budgets and active checkpoint"},
		{Name: "/mcp", Usage: "/mcp", Description: "show configured MCP servers"},
		{Name: "/session", Usage: "/session", Description: "show the active session"},
		{Name: "/pick", Usage: "/pick", Description: "list recent sessions with numbers"},
		{Name: "/show", Usage: "/show [TARGET]", Description: "show current, latest, number, or ID"},
		{Name: "/sessions", Usage: "/sessions", Description: "list saved sessions"},
		{Name: "/resume", Usage: "/resume TARGET", Description: "resume by ID, latest, or number"},
		{Name: "/new", Usage: "/new", Description: "start the next prompt in a new session"},
		{Name: "/draft", Usage: "/draft [TEXT]", Description: "start a multi-line draft"},
		{Name: "/append", Usage: "/append TEXT", Description: "append one line to the draft"},
		{Name: "/show-draft", Usage: "/show-draft", Description: "show the active draft"},
		{Name: "/submit", Usage: "/submit", Description: "send the active draft"},
		{Name: "/cancel", Usage: "/cancel", Description: "discard the active draft"},
		{Name: "/history", Usage: "/history", Description: "list remembered prompts"},
		{Name: "/recall", Usage: "/recall [N|latest]", Description: "recall a prompt into the draft"},
		{Name: "/quit", Usage: "/quit", Description: "exit Memax Code"},
	}
}

func knownInteractiveCommand(name string) bool {
	switch name {
	case "/exit", "/draft-show":
		return true
	}
	for _, spec := range interactiveCommandSpecs() {
		if spec.Name == name {
			return true
		}
	}
	return false
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
	case "/context":
		if err := printInteractiveContext(ctx, w, opts, *currentSession, arg); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	case "/mcp":
		printInteractiveMCP(w, opts)
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
	if len(opts.MCPServers) > 0 {
		for _, name := range sortedMapKeysMCP(opts.MCPServers) {
			server := opts.MCPServers[name]
			status := "enabled"
			if !server.enabled() {
				status = "disabled"
			}
			parallel := "serial"
			if server.SupportsParallelToolCalls {
				parallel = "parallel"
			}
			fmt.Fprintf(w, "  mcp_server.%s: %s %s %s", name, status, parallel, mcpServerCommandDisplay(server))
			if suffix := server.runtimeSummary(); suffix != "" {
				fmt.Fprintf(w, " %s", suffix)
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintf(w, "  inherit_command_env: %t\n", opts.InheritCommandEnv)
	return nil
}

func printInteractiveMCP(w io.Writer, opts options) {
	if len(opts.MCPServers) == 0 {
		fmt.Fprintln(w, "no MCP servers")
		return
	}
	fmt.Fprintln(w, "mcp servers:")
	for _, name := range sortedMapKeysMCP(opts.MCPServers) {
		server := opts.MCPServers[name]
		status := "enabled"
		if !server.enabled() {
			status = "disabled"
		}
		parallel := "serial"
		if server.SupportsParallelToolCalls {
			parallel = "parallel"
		}
		fmt.Fprintf(w, "  %s: %s %s %s", name, status, parallel, mcpServerCommandDisplay(server))
		if suffix := server.runtimeSummary(); suffix != "" {
			fmt.Fprintf(w, " (%s)", suffix)
		}
		fmt.Fprintln(w)
	}
}

func printInteractiveContext(ctx context.Context, w io.Writer, opts options, currentSession, raw string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fmt.Fprintln(w, "context:")
	printContextBudgetFields(w, opts, "  ")

	target := strings.TrimSpace(raw)
	if target == "" {
		target = "current"
	}
	if target == "current" {
		if strings.TrimSpace(currentSession) == "" {
			fmt.Fprintln(w, "  active_session: <unset>")
			fmt.Fprintln(w, "  active_view: <unset>")
			return nil
		}
		target = currentSession
	}
	id, err := resolveInteractiveSessionID(ctx, opts, target, "context")
	if err != nil {
		return err
	}
	view, err := session.NewJSONLStore(opts.SessionDir).MessageView(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "  session: %s\n", id)
	fmt.Fprintf(w, "  raw_messages: %d\n", view.RawMessageCount)
	fmt.Fprintf(w, "  active_messages: %d\n", len(view.Messages))
	if view.Compaction == nil {
		fmt.Fprintln(w, "  active_checkpoint: none")
		return nil
	}
	checkpoint := view.Compaction
	fmt.Fprintf(w, "  active_checkpoint: %s\n", checkpoint.ID)
	fmt.Fprintf(w, "  checkpoint_created_at: %s\n", checkpoint.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  checkpoint_policy: %s\n", checkpoint.Policy)
	fmt.Fprintf(w, "  checkpoint_reason: %s\n", checkpoint.Reason)
	fmt.Fprintf(w, "  checkpoint_raw_messages: %d\n", checkpoint.RawMessageCount)
	fmt.Fprintf(w, "  summary_hash: %s\n", valueOrUnset(checkpoint.SummaryHash))
	if preview := strings.TrimSpace(checkpoint.SummaryPreview); preview != "" {
		fmt.Fprintf(w, "  summary_preview: %s\n", preview)
	}
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
	for _, spec := range interactiveCommandSpecs() {
		fmt.Fprintf(w, "  %-18s %s\n", spec.Usage, spec.Description)
	}
	fmt.Fprintln(w, "  //PROMPT           send a prompt that starts with /")
}
