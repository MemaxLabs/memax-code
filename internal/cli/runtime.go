package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/session"
	"github.com/MemaxLabs/memax-go-agent-sdk/stack/coding"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/commandtools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/tasktools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/verifytools"
	"github.com/MemaxLabs/memax-go-agent-sdk/workspace"
)

const (
	maxVerificationOutputBytes = 16 * 1024
	resumeLatest               = "latest"
	maxSessionTitleRunes       = 80
)

func runPrompt(ctx context.Context, stdout io.Writer, opts options) error {
	_, err := runPromptWithSession(ctx, stdout, opts)
	return err
}

func runPromptWithSession(ctx context.Context, stdout io.Writer, opts options) (string, error) {
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := modelClient(opts)
	if err != nil {
		return "", err
	}

	stack, err := buildStack(opts)
	if err != nil {
		return "", err
	}
	agentOpts := stack.WithModel(client)
	agentOpts.SessionID = opts.ResumeSessionID
	events, err := memaxagent.Query(queryCtx, opts.Prompt, agentOpts)
	if err != nil {
		return "", err
	}
	var sessionID string
	observe := func(event memaxagent.Event) {
		if event.Kind == memaxagent.EventSessionStarted && sessionID == "" {
			sessionID = event.SessionID
		}
	}
	if opts.EventStream != eventStreamModeOff {
		if err := renderEventStreamObserved(stdout, events, opts.EventStream, observe); err != nil {
			return sessionID, err
		}
		return sessionID, nil
	}
	if err := renderEventsWithModeObserved(stdout, events, opts.UI, observe); err != nil {
		if errors.Is(err, contextCanceled) {
			cancel()
			return sessionID, nil
		}
		return sessionID, err
	}
	return sessionID, nil
}

func resolveResumeSession(ctx context.Context, opts *options) error {
	if opts.ResumeSessionID == "" {
		return nil
	}
	store := session.NewJSONLStore(opts.SessionDir)
	id, err := resolveSessionID(ctx, store, opts.SessionDir, opts.ResumeSessionID, "resume")
	if err != nil {
		return err
	}
	opts.ResumeSessionID = id
	return nil
}

func resolveSessionID(ctx context.Context, store *session.JSONLStore, dir, raw, action string) (string, error) {
	if strings.EqualFold(raw, resumeLatest) {
		id, err := latestSessionID(ctx, store, dir)
		if err != nil {
			return "", fmt.Errorf("%s latest: %w", action, err)
		}
		return id, nil
	}
	canonical, ok := session.CanonicalID(raw)
	if !ok {
		return "", fmt.Errorf("%s session %q: invalid session id", action, raw)
	}
	exists, err := store.Exists(ctx, canonical)
	if err != nil {
		return "", fmt.Errorf("%s session %q: %w", action, raw, err)
	}
	if !exists {
		return "", fmt.Errorf("%s session %q: session not found", action, raw)
	}
	return canonical, nil
}

func showSession(ctx context.Context, stdout io.Writer, opts options) error {
	store := session.NewJSONLStore(opts.SessionDir)
	id, err := resolveSessionID(ctx, store, opts.SessionDir, opts.ShowSessionID, "show")
	if err != nil {
		return err
	}
	sess, err := store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("show session %q: %w", id, err)
	}
	messages, err := store.Messages(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("show session %q messages: %w", id, err)
	}
	parent := sess.ParentID
	if parent == "" {
		parent = "-"
	}
	fmt.Fprintf(stdout, "session: %s\n", sess.ID)
	created := "-"
	if !sess.CreatedAt.IsZero() {
		created = sess.CreatedAt.Format(time.RFC3339)
	}
	fmt.Fprintf(stdout, "created: %s\n", created)
	fmt.Fprintf(stdout, "parent: %s\n", parent)
	fmt.Fprintf(stdout, "messages: %d\n", len(messages))
	for i, msg := range messages {
		if err := renderTranscriptMessage(stdout, i+1, msg); err != nil {
			return err
		}
	}
	return nil
}

func inspectTools(_ context.Context, stdout io.Writer, opts options) error {
	stack, err := buildStack(opts)
	if err != nil {
		return err
	}
	specs := append([]model.ToolSpec(nil), stack.Registry().Specs()...)
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
	for _, spec := range specs {
		fmt.Fprintf(stdout, "tool: %s\n", spec.Name)
		if spec.Description != "" {
			fmt.Fprintf(stdout, "description: %s\n", spec.Description)
		}
		fmt.Fprintf(stdout, "read_only: %t\n", spec.ReadOnly)
		fmt.Fprintf(stdout, "destructive: %t\n", spec.Destructive)
		fmt.Fprintf(stdout, "concurrency_safe: %t\n", spec.ConcurrencySafe)
		if spec.MaxResultBytes > 0 {
			fmt.Fprintf(stdout, "max_result_bytes: %d\n", spec.MaxResultBytes)
		}
		schema, err := json.Marshal(spec.InputSchema)
		if err != nil {
			return fmt.Errorf("inspect tool %q schema: %w", spec.Name, err)
		}
		fmt.Fprintf(stdout, "input_schema: %s\n\n", schema)
	}
	return nil
}

func renderTranscriptMessage(w io.Writer, index int, msg model.Message) error {
	fmt.Fprintf(w, "\n[%d] %s", index, msg.Role)
	if msg.ID != "" {
		fmt.Fprintf(w, " id=%s", msg.ID)
	}
	fmt.Fprintln(w)
	for _, block := range msg.Content {
		switch block.Type {
		case model.ContentText:
			writeIndented(w, sanitizeTranscriptText(block.Text))
		case model.ContentToolUse:
			if block.ToolUse == nil {
				continue
			}
			fmt.Fprintf(w, "  tool_use: %s id=%s\n", block.ToolUse.Name, block.ToolUse.ID)
			if input := compactJSON(block.ToolUse.Input); input != "" {
				writeIndented(w, "input: "+input)
			}
		case model.ContentProviderArtifact:
			if block.ProviderArtifact == nil {
				continue
			}
			artifact := block.ProviderArtifact
			fmt.Fprintf(w, "  provider_artifact: provider=%s type=%s", artifact.Provider, artifact.Type)
			if artifact.ID != "" {
				fmt.Fprintf(w, " id=%s", artifact.ID)
			}
			fmt.Fprintln(w)
		default:
			fmt.Fprintf(w, "  content: type=%s\n", block.Type)
		}
	}
	if msg.ToolResult != nil {
		result := msg.ToolResult
		label := "tool_result"
		if result.IsError {
			label = "tool_error"
		}
		fmt.Fprintf(w, "  %s: %s id=%s\n", label, result.Name, result.ToolUseID)
		writeIndented(w, sanitizeTranscriptText(result.Content))
	}
	return nil
}

func writeIndented(w io.Writer, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func compactJSON(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return sanitizeTranscriptText(string(raw))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return sanitizeTranscriptText(string(raw))
	}
	return string(encoded)
}

func sanitizeTranscriptText(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		case '\r':
			return -1
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

func latestSessionID(ctx context.Context, store *session.JSONLStore, dir string) (string, error) {
	candidates, err := sessionCandidates(dir)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no sessions")
	}
	for _, candidate := range candidates {
		sess, err := store.Get(ctx, candidate.ID)
		if err != nil {
			continue
		}
		return sess.ID, nil
	}
	return "", fmt.Errorf("no readable sessions")
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
	config.WorkspacePatchInputMode = coding.WorkspacePatchInputUnifiedDiff
	config.Sessions = session.NewJSONLStore(opts.SessionDir)
	config.Tasks = tasktools.NewMemoryStore(nil)
	config.Command.Runner = runner
	config.CommandSessions = commandSessions
	config.CommandSessionStartInputMode = coding.CommandSessionStartInputShellCommand
	config.Base.AppendSystemPrompt = appendPromptSection(config.Base.AppendSystemPrompt, cliToolContractGuidance)
	hasGoWorkspace := hasGoModule(opts.CWD)
	if len(opts.VerifyCommands) > 0 {
		config.Verifier.Verifier = verifier(runner, opts.VerifyCommands, hasGoWorkspace)
	} else if hasGoWorkspace {
		config.Verifier.Verifier = verifier(runner, nil, false)
	} else {
		// Without explicit host verification commands, do not trap non-Go
		// workspaces behind a verifier that can never pass.
		config.Policies.RequireVerificationBeforeFinal = false
		config.Policies.RecommendRollbackOnFailedVerification = false
	}
	stack, err := coding.New(config)
	if err != nil {
		return coding.Stack{}, fmt.Errorf("configure runtime: %w", userFacingError(err))
	}
	return stack, nil
}

// cliToolContractGuidance intentionally names the fixed default tool names
// registered by buildStack. Update this guidance if the CLI ever exposes tool
// name customization.
const cliToolContractGuidance = `CLI tool contract:
- Use run_command with command as one shell command string, not an argv array.
- Use start_command with command as one shell command string for long-running processes such as dev servers, test watchers, and REPLs.
- Use workspace_apply_patch with exactly one unified_diff string. Do not provide structured patch operations.
- If a tool schema error says a field has the wrong type, retry with the contract above before changing strategy.`

func appendPromptSection(base, section string) string {
	base = strings.TrimSpace(base)
	section = strings.TrimSpace(section)
	switch {
	case base == "":
		return section
	case section == "":
		return base
	default:
		return base + "\n\n" + section
	}
}

func listSessions(ctx context.Context, stdout io.Writer, opts options) error {
	store := session.NewJSONLStore(opts.SessionDir)
	rows, err := loadSessionRows(ctx, store, opts.SessionDir)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(stdout, "no sessions")
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "SESSION ID\tUPDATED\tCREATED\tPARENT\tTITLE")
	for _, row := range rows {
		s := row.Session
		parent := s.ParentID
		if parent == "" {
			parent = "-"
		}
		title := row.Title
		if title == "" {
			title = "-"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			s.ID,
			row.UpdatedAt.Format(time.RFC3339),
			s.CreatedAt.Format(time.RFC3339),
			parent,
			title,
		); err != nil {
			return err
		}
	}
	return table.Flush()
}

type sessionRow struct {
	Session   session.Session
	UpdatedAt time.Time
	Title     string
}

type sessionCandidate struct {
	ID        string
	UpdatedAt time.Time
}

func loadSessionRows(ctx context.Context, store *session.JSONLStore, dir string) ([]sessionRow, error) {
	candidates, err := sessionCandidates(dir)
	if err != nil {
		return nil, err
	}
	rows := make([]sessionRow, 0, len(candidates))
	for _, candidate := range candidates {
		sess, err := store.Get(ctx, candidate.ID)
		if err != nil {
			continue
		}
		messages, err := store.Messages(ctx, sess.ID)
		if err != nil {
			continue
		}
		rows = append(rows, sessionRow{
			Session:   sess,
			UpdatedAt: candidate.UpdatedAt,
			Title:     sessionTitle(messages),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.Session.CreatedAt.Equal(right.Session.CreatedAt) {
			return left.Session.CreatedAt.After(right.Session.CreatedAt)
		}
		return left.Session.ID > right.Session.ID
	})
	return rows, nil
}

func sessionCandidates(dir string) ([]sessionCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list session directory: %w", err)
	}
	candidates := make([]sessionCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !session.ValidID(id) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		candidates = append(candidates, sessionCandidate{
			ID:        id,
			UpdatedAt: info.ModTime().UTC(),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		return left.ID > right.ID
	})
	return candidates, nil
}

func sessionTitle(messages []model.Message) string {
	for _, msg := range messages {
		if msg.Role != model.RoleUser {
			continue
		}
		text := strings.Join(strings.Fields(sanitizeTitleText(msg.PlainText())), " ")
		if text == "" {
			continue
		}
		return truncateRunes(text, maxSessionTitleRunes)
	}
	return ""
}

func sanitizeTitleText(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func hasGoModule(root string) bool {
	info, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil && !info.IsDir()
}

func verificationMode(root string, commands map[string]string) string {
	if len(commands) > 0 {
		return "custom"
	}
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

func verifier(runner commandtools.Runner, commands map[string]string, goFallback bool) verifytools.Verifier {
	commands = cloneStringMap(commands)
	return verifytools.VerifierFunc(func(ctx context.Context, req verifytools.Request) (verifytools.Result, error) {
		command, argv, err := verificationCommand(req, commands, goFallback)
		if err != nil {
			return verifytools.Result{
				Name:   req.Name,
				Passed: false,
				Output: err.Error(),
			}, nil
		}
		result, err := runner.RunCommand(ctx, commandtools.Request{
			Command: command,
			Argv:    argv,
			Purpose: "workspace verification: " + req.Name,
		})
		if err != nil {
			return verifytools.Result{}, err
		}
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
	_, argv, err := defaultVerificationCommand(req)
	return argv, err
}

func verificationCommand(req verifytools.Request, commands map[string]string, goFallback bool) (string, []string, error) {
	if len(commands) == 0 {
		return defaultVerificationCommand(req)
	}
	return customVerificationCommand(req, commands, goFallback)
}

func defaultVerificationCommand(req verifytools.Request) (string, []string, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = "./..."
	}
	if err := validateVerificationTarget(target); err != nil {
		return "", nil, err
	}
	switch name {
	case "vet":
		argv := []string{"go", "vet", target}
		return strings.Join(argv, " "), argv, nil
	case "test", "default", "":
		argv := []string{"go", "test", target}
		return strings.Join(argv, " "), argv, nil
	default:
		return "", nil, fmt.Errorf("unsupported verification %q; supported checks: test, vet", req.Name)
	}
}

func customVerificationCommand(req verifytools.Request, commands map[string]string, goFallback bool) (string, []string, error) {
	name := normalizeVerifyName(req.Name)
	if name == "" || name == "default" {
		if command := strings.TrimSpace(commands["default"]); command != "" {
			command, err := verificationCommandWithTarget(nameOrDefault(name), command, req.Target)
			if err != nil {
				return "", nil, err
			}
			return command, shellCommandArgv(command), nil
		}
		name = "test"
	}
	command := strings.TrimSpace(commands[name])
	if command == "" {
		if goFallback {
			return defaultVerificationCommand(req)
		}
		return "", nil, fmt.Errorf("unsupported verification %q; configured checks: %s", req.Name, strings.Join(sortedMapKeys(commands), ", "))
	}
	command, err := verificationCommandWithTarget(name, command, req.Target)
	if err != nil {
		return "", nil, err
	}
	return command, shellCommandArgv(command), nil
}

func verificationCommandWithTarget(name, command, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return command, nil
	}
	if err := validateVerificationTarget(target); err != nil {
		return "", err
	}
	if !strings.Contains(command, "{target}") {
		return "", fmt.Errorf("verification %q does not accept a target; include {target} in the configured command", name)
	}
	return strings.ReplaceAll(command, "{target}", shellQuote(target)), nil
}

func nameOrDefault(name string) string {
	if strings.TrimSpace(name) == "" {
		return "default"
	}
	return name
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shellCommandArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"sh", "-c", command}
}

func shellQuote(value string) string {
	return shellQuoteForGOOS(runtime.GOOS, value)
}

func shellQuoteForGOOS(goos, value string) string {
	if goos == "windows" {
		if value == "" {
			return `""`
		}
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateVerificationTarget(target string) error {
	if strings.HasPrefix(target, "-") {
		return fmt.Errorf("invalid verification target %q: target must be a package path, not a flag", target)
	}
	if strings.ContainsAny(target, "\x00\r\n\t ") {
		return fmt.Errorf("invalid verification target %q: target must be one package path", target)
	}
	if strings.ContainsAny(target, "\"'$;&|<>%!`()[]{}") {
		return fmt.Errorf("invalid verification target %q: target must be one safe package path", target)
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
