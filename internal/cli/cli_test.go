package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
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
		"verification: go",
		"prompt: fix tests",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
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
