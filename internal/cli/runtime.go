package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/session"
	"github.com/MemaxLabs/memax-go-agent-sdk/stack/coding"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/commandtools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/tasktools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/verifytools"
	"github.com/MemaxLabs/memax-go-agent-sdk/workspace"
)

const maxVerificationOutputBytes = 16 * 1024

func runPrompt(ctx context.Context, stdout io.Writer, opts options) error {
	client, err := modelClient(opts)
	if err != nil {
		return err
	}

	stack, err := buildStack(opts)
	if err != nil {
		return err
	}
	agentOpts := stack.WithModel(client)
	agentOpts.SessionID = opts.ResumeSessionID
	events, err := memaxagent.Query(ctx, opts.Prompt, agentOpts)
	if err != nil {
		return err
	}
	return renderEvents(stdout, events)
}

func validateResumeSession(ctx context.Context, opts options) error {
	if opts.ResumeSessionID == "" {
		return nil
	}
	store := session.NewJSONLStore(opts.SessionDir)
	exists, err := store.Exists(ctx, opts.ResumeSessionID)
	if err != nil {
		return fmt.Errorf("resume session %q: %w", opts.ResumeSessionID, err)
	}
	if !exists {
		return fmt.Errorf("resume session %q: session not found", opts.ResumeSessionID)
	}
	return nil
}

func buildStack(opts options) (coding.Stack, error) {
	preset, err := parsePreset(opts.Preset)
	if err != nil {
		return coding.Stack{}, err
	}
	config, err := preset.Config()
	if err != nil {
		return coding.Stack{}, err
	}

	ws, err := workspace.NewOSStore(opts.CWD)
	if err != nil {
		return coding.Stack{}, fmt.Errorf("open workspace: %w", err)
	}
	runnerOpts := []commandtools.OSRunnerOption{}
	sessionOpts := []commandtools.OSSessionManagerOption{}
	if opts.InheritCommandEnv {
		runnerOpts = append(runnerOpts, commandtools.WithOSRunnerInheritEnv(true))
		sessionOpts = append(sessionOpts, commandtools.WithOSSessionManagerInheritEnv(true))
	}
	runner, err := commandtools.NewOSRunner(opts.CWD, runnerOpts...)
	if err != nil {
		return coding.Stack{}, fmt.Errorf("create command runner: %w", err)
	}
	commandSessions, err := commandtools.NewOSSessionManager(opts.CWD, sessionOpts...)
	if err != nil {
		return coding.Stack{}, fmt.Errorf("create command session manager: %w", err)
	}

	config.Workspace = ws
	config.Sessions = session.NewJSONLStore(opts.SessionDir)
	config.Tasks = tasktools.NewMemoryStore(nil)
	config.Command.Runner = runner
	config.CommandSessions = commandSessions
	if hasGoModule(opts.CWD) {
		config.Verifier.Verifier = verifier(runner)
	} else {
		// The initial CLI ships a Go verifier because the runtime is
		// currently Go-oriented. For other workspaces, do not trap the agent in a
		// required verifier that can never pass; a configurable verifier is the
		// next product slice.
		config.Policies.RequireVerificationBeforeFinal = false
		config.Policies.RecommendRollbackOnFailedVerification = false
	}
	stack, err := coding.New(config)
	if err != nil {
		return coding.Stack{}, fmt.Errorf("configure runtime: %w", userFacingError(err))
	}
	return stack, nil
}

func listSessions(ctx context.Context, stdout io.Writer, opts options) error {
	store := session.NewJSONLStore(opts.SessionDir)
	sessions, err := session.List(ctx, store)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(stdout, "no sessions")
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "SESSION ID\tCREATED\tPARENT")
	// session.List returns oldest-first; the CLI presents the newest sessions first.
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		parent := s.ParentID
		if parent == "" {
			parent = "-"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\n", s.ID, s.CreatedAt.Format(time.RFC3339), parent); err != nil {
			return err
		}
	}
	return table.Flush()
}

func hasGoModule(root string) bool {
	info, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil && !info.IsDir()
}

func verificationMode(root string) string {
	if hasGoModule(root) {
		return "go"
	}
	return "disabled_no_go_mod"
}

func parsePreset(raw string) (coding.Preset, error) {
	value := coding.Preset(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case coding.PresetSafeLocal, coding.PresetCIRepair, coding.PresetInteractiveDev:
		return value, nil
	default:
		return "", fmt.Errorf("unknown preset %q", raw)
	}
}

func verifier(runner commandtools.Runner) verifytools.Verifier {
	return verifytools.VerifierFunc(func(ctx context.Context, req verifytools.Request) (verifytools.Result, error) {
		argv, err := verificationArgv(req)
		if err != nil {
			return verifytools.Result{
				Name:   req.Name,
				Passed: false,
				Output: err.Error(),
			}, nil
		}
		result, err := runner.RunCommand(ctx, commandtools.Request{
			Argv:    argv,
			Purpose: "workspace verification: " + req.Name,
		})
		if err != nil {
			return verifytools.Result{}, err
		}
		command := strings.Join(argv, " ")
		output := strings.TrimSpace(strings.Join(nonEmpty(result.Stdout, result.Stderr), "\n"))
		if output == "" {
			output = fmt.Sprintf("%s exited with code %d", command, result.ExitCode)
		} else {
			output = fmt.Sprintf("$ %s\n%s", command, tailBytes(output, maxVerificationOutputBytes))
		}
		return verifytools.Result{
			Name:   req.Name,
			Passed: result.ExitCode == 0 && !result.TimedOut,
			Output: output,
		}, nil
	})
}

func verificationArgv(req verifytools.Request) ([]string, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = "./..."
	}
	if err := validateVerificationTarget(target); err != nil {
		return nil, err
	}
	switch name {
	case "vet":
		return []string{"go", "vet", target}, nil
	case "test", "default", "":
		return []string{"go", "test", target}, nil
	default:
		return nil, fmt.Errorf("unsupported verification %q; supported checks: test, vet", req.Name)
	}
}

func validateVerificationTarget(target string) error {
	if strings.HasPrefix(target, "-") {
		return fmt.Errorf("invalid verification target %q: target must be a package path, not a flag", target)
	}
	if strings.ContainsAny(target, "\x00\r\n\t ") {
		return fmt.Errorf("invalid verification target %q: target must be one package path", target)
	}
	return nil
}

func tailBytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return "... output truncated ...\n" + value[len(value)-maxBytes:]
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
