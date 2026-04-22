package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/MemaxLabs/memax-go-agent-sdk/session"
)

const interactiveScannerMaxBytes = 1024 * 1024

func runInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, opts options) error {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	currentSession := opts.ResumeSessionID
	fmt.Fprintln(stderr, "Memax Code interactive shell")
	fmt.Fprintln(stderr, "Type /help for commands, /quit to exit.")

	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), interactiveScannerMaxBytes)
	var firstErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprint(stderr, "memax> ")
		// Scanner reads are intentionally line-oriented in this Foundation shell.
		// Context cancellation is checked between prompts; a future full-screen
		// shell can use async input to interrupt a blocked read immediately.
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if isInteractiveCommandLine(line) {
			if done := handleInteractiveCommand(ctx, stderr, opts, &currentSession, line); done {
				return firstErr
			}
			continue
		}
		line = unescapeInteractivePrompt(line)

		turnOpts := opts
		turnOpts.Prompt = line
		turnOpts.ResumeSessionID = currentSession
		sessionID, err := runPromptWithSession(ctx, stdout, turnOpts)
		if err != nil {
			if sessionID != "" {
				currentSession = sessionID
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if sessionID != "" {
			currentSession = sessionID
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read interactive input: %w", err)
	}
	return firstErr
}

func handleInteractiveCommand(ctx context.Context, w io.Writer, opts options, currentSession *string, line string) bool {
	name, arg := splitInteractiveCommand(line)
	switch name {
	case "/quit", "/exit":
		fmt.Fprintln(w, "bye")
		return true
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
	case "/show":
		if err := showInteractiveSession(ctx, w, opts, *currentSession, arg); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	case "/resume":
		if arg == "" {
			fmt.Fprintln(w, "usage: /resume SESSION_ID|latest|N")
			return false
		}
		id, err := resolveInteractiveSessionID(ctx, opts, arg, "resume")
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return false
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
	return false
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
	fmt.Fprintf(w, "  active_session: %s\n", valueOrUnset(currentSession))
	fmt.Fprintf(w, "  saved_sessions: %d\n", len(candidates))
	fmt.Fprintf(w, "  verification: %s\n", verificationMode(opts.CWD))
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
	// Interactive shell commands write to stderr so stdout remains the agent
	// transcript stream. The standalone --show-session command still writes to
	// stdout for scriptability.
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
	fmt.Fprintln(w, "  /quit              exit")
	fmt.Fprintln(w, "  //PROMPT           send a prompt that starts with /")
}
