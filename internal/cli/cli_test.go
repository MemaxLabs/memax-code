package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MemaxLabs/memax-go-agent-sdk/session"
)

func TestDryRunPrintsResolvedConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--cwd", repoRoot(t),
		"--provider", "anthropic",
		"--profile", "deep",
		"--model", "example-model",
		"--preset", "safe_local",
		"fix tests",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"provider: anthropic",
		"model: example-model",
		"profile: deep",
		"preset: safe_local",
		"session_dir: ",
		"resume_session: <unset>",
		"verification: go",
		"prompt: fix tests",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestDryRunPrintsSessionConfig(t *testing.T) {
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(context.Background())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--session-dir", sessionDir,
		"--resume", sess.ID,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"session_dir: " + filepath.Clean(sessionDir),
		"resume_session: " + sess.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestDryRunValidatesResumeSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--session-dir", t.TempDir(),
		"--resume", "fedcba9876543210fedcba9876543210",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("Run() error = %v, want missing session", err)
	}
}

func TestListSessionsDoesNotRequirePromptOrModel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--list-sessions",
		"--provider", "openai",
		"--session-dir", t.TempDir(),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); strings.TrimSpace(out) != "no sessions" {
		t.Fatalf("list output = %q, want no sessions", out)
	}
}

func TestListSessionsIgnoresModelConfig(t *testing.T) {
	t.Setenv("MEMAX_CODE_PROVIDER", "not-a-provider")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--list-sessions",
		"--profile", "not-a-profile",
		"--session-dir", t.TempDir(),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); strings.TrimSpace(out) != "no sessions" {
		t.Fatalf("list output = %q, want no sessions", out)
	}
}

func TestListSessionsRejectsConflictingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--list-sessions", "--resume", "0123456789abcdef0123456789abcdef"},
		{"--list-sessions", "--dry-run"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), append(args, "--session-dir", t.TempDir()), &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "--list-sessions cannot be combined") {
			t.Fatalf("Run(%v) error = %v, want conflict", args, err)
		}
	}
}

func TestListSessionsPrintsNewestFirst(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	first, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{
		"--list-sessions",
		"--provider", "openai",
		"--session-dir", sessionDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	firstIndex := strings.Index(out, first.ID)
	secondIndex := strings.Index(out, second.ID)
	if firstIndex < 0 || secondIndex < 0 {
		t.Fatalf("list output missing session ids:\n%s", out)
	}
	if secondIndex > firstIndex {
		t.Fatalf("sessions not newest-first:\n%s", out)
	}
}

func TestRunValidatesResumeSessionBeforeProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--provider", "openai",
		"--session-dir", t.TempDir(),
		"--resume", "not-a-session",
		"continue",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `resume session "not-a-session"`) {
		t.Fatalf("Run() error = %v, want resume validation", err)
	}
	if strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("Run() checked provider before resume validation: %v", err)
	}
}

func TestDryRunDisablesVerificationOutsideGoModule(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--cwd", cwd,
		"--provider", "openai",
		"--model", "example-model",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "verification: disabled_no_go_mod") {
		t.Fatalf("dry-run output missing disabled verifier:\n%s", out)
	}
}

func TestRunRequiresPromptUnlessDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("Run() error = %v, want prompt required", err)
	}
}

func TestParseRejectsUnknownProvider(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--provider", "other"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unknown provider "other"`) {
		t.Fatalf("parseArgs() error = %v, want unknown provider", err)
	}
}

func TestParseRejectsUnknownProviderFromEnvWithOrigin(t *testing.T) {
	t.Setenv("MEMAX_CODE_PROVIDER", "other")
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid MEMAX_CODE_PROVIDER") {
		t.Fatalf("parseArgs() error = %v, want env origin", err)
	}
}

func TestParseRejectsUnknownProfileWithCLIError(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--profile", "huge-brain"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown model profile") {
		t.Fatalf("parseArgs() error = %v, want unknown model profile", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "coding"+" stack") {
		t.Fatalf("parseArgs() leaked SDK wording: %v", err)
	}
}

func TestParseCWDFlagAliases(t *testing.T) {
	cwd := t.TempDir()
	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--cwd", cwd}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.CWD != filepath.Clean(cwd) {
		t.Fatalf("CWD = %q, want %q", opts.CWD, filepath.Clean(cwd))
	}

	other := t.TempDir()
	opts, err = parseArgs([]string{"--dry-run", "-C", cwd, "--cd", other}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.CWD != filepath.Clean(other) {
		t.Fatalf("CWD = %q, want last alias %q", opts.CWD, filepath.Clean(other))
	}
}

func TestParseExpandsHomeSessionDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--session-dir", "~/sessions"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	want := filepath.Join(home, "sessions")
	if opts.SessionDir != want {
		t.Fatalf("SessionDir = %q, want %q", opts.SessionDir, want)
	}
}

func TestParseRejectsMissingCWD(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--cwd", filepath.Join(t.TempDir(), "missing")}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "stat cwd") {
		t.Fatalf("parseArgs() error = %v, want stat cwd", err)
	}
}

func TestDryRunPrintsInheritedCommandEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--inherit-command-env",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "inherit_command_env: true") {
		t.Fatalf("dry-run output missing inherited env:\n%s", out)
	}
}

func TestParseDefaultsProfileAndModelFromEnv(t *testing.T) {
	t.Setenv("MEMAX_CODE_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "env-model")
	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.Provider != providerOpenAI {
		t.Fatalf("Provider = %q, want openai", opts.Provider)
	}
	if opts.Model != "env-model" {
		t.Fatalf("Model = %q, want env-model", opts.Model)
	}
	profile, err := parseModelProfile(opts.Profile)
	if err != nil {
		t.Fatalf("parseModelProfile() error = %v", err)
	}
	if profile.String() != "balanced" {
		t.Fatalf("profile = %q, want balanced", profile)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}
