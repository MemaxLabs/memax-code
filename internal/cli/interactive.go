package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	case "/resume":
		if arg == "" {
			fmt.Fprintln(w, "usage: /resume SESSION_ID|latest")
			return false
		}
		store := session.NewJSONLStore(opts.SessionDir)
		id, err := resolveSessionID(ctx, store, opts.SessionDir, arg, "resume")
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return false
		}
		*currentSession = id
		fmt.Fprintf(w, "resumed session: %s\n", id)
	case "/sessions":
		if err := listSessions(ctx, w, opts); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	default:
		fmt.Fprintf(w, "unknown command %q; type /help\n", name)
	}
	return false
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
	fmt.Fprintln(w, "  /session           show the active session")
	fmt.Fprintln(w, "  /sessions          list saved sessions")
	fmt.Fprintln(w, "  /resume ID|latest  resume a saved session")
	fmt.Fprintln(w, "  /new               start the next prompt in a new session")
	fmt.Fprintln(w, "  /quit              exit")
	fmt.Fprintln(w, "  //PROMPT           send a prompt that starts with /")
}
