package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsResolvedSetup(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())
	sessionDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"provider": "openai",
		"model": "test-model",
		"profile": "deep",
		"session_dir": "`+filepath.ToSlash(sessionDir)+`"
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-key")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"doctor",
		"--config", configPath,
		"--cwd", repoRoot(t),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Memax Code doctor",
		"[ok] config: " + configPath + " (loaded)",
		"[ok] cwd: " + repoRoot(t),
		"[ok] provider: openai",
		"[ok] model: test-model",
		"[ok] api_key: OPENAI_API_KEY is set",
		"[ok] session_dir: " + sessionDir,
		"[ok] verification: go workspace detected",
		"[ok] command.go:",
		"summary: 0 fail,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorWarnsForMissingOptionalSetup(t *testing.T) {
	clearDoctorEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMAX_CODE_CONFIG", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"doctor",
		"--provider", "openai",
		"--cwd", repoRoot(t),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"[warn] config: " + filepath.Join(home, ".memax-code", "config.json") + " (not found; defaults/env/flags only)",
		"[warn] model: <unset>; pass --model, set OPENAI_MODEL, or write model in config",
		"[warn] api_key: OPENAI_API_KEY is not set",
		"summary: 0 fail,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorFailsForInvalidSessionDir(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(sessionFile, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"doctor",
		"--model", "test-model",
		"--session-dir", sessionFile,
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
		t.Fatalf("Run() error = %v, want doctor failure", err)
	}
	if out := stdout.String(); !strings.Contains(out, "[fail] session_dir: "+sessionFile+" is not a directory") {
		t.Fatalf("doctor output missing session dir failure:\n%s", out)
	}
}

func TestDoctorRejectsPrompt(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"doctor", "fix tests"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
		t.Fatalf("Run() error = %v, want prompt rejection", err)
	}
	if out := stdout.String(); !strings.Contains(out, "[fail] arguments: doctor does not accept a prompt") {
		t.Fatalf("doctor output missing prompt rejection:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDoctorHelpPrintsUsage(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"doctor", "--help"},
		{"doctor", "-help"},
		{"doctor", "-h"},
		{"doctor", "help"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), args, &stdout, &stderr)
		if err != nil {
			t.Fatalf("Run(%v) error = %v", args, err)
		}
		if !strings.Contains(stdout.String(), "Usage: memax-code doctor [flags]") {
			t.Fatalf("Run(%v) stdout = %q, want doctor usage", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%v) stderr = %q, want empty", args, stderr.String())
		}
	}
}

func TestDoctorHelpDoesNotInspectFlagValues(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"doctor", "--model", "--help", "--provider", "openai", "--cwd", repoRoot(t)}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, "Usage: memax-code doctor [flags]") {
		t.Fatalf("doctor treated flag value as help:\n%s", out)
	}
	if !strings.Contains(out, "[ok] model: --help") {
		t.Fatalf("doctor output missing model value:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDoctorRejectsOperationalFlags(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"doctor", "--list-sessions"},
		{"doctor", "--inspect-tools"},
		{"doctor", "--show-session", "latest"},
		{"doctor", "--resume", "latest"},
		{"doctor", "--dry-run"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
			t.Fatalf("Run(%v) error = %v, want doctor-specific flag rejection", args, err)
		}
		if out := stdout.String(); !strings.Contains(out, "[fail] arguments: doctor does not accept "+args[1]) {
			t.Fatalf("doctor output missing flag rejection for %v:\n%s", args, out)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
	}
}

func TestDoctorReportsUnknownFlagAsDiagnostic(t *testing.T) {
	clearDoctorEnv(t)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"doctor", "--nope"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
		t.Fatalf("Run() error = %v, want doctor failure", err)
	}
	if out := stdout.String(); !strings.Contains(out, "[fail] arguments: flag provided but not defined: -nope") {
		t.Fatalf("doctor output missing flag parse failure:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDoctorReportsParseFailureAsDiagnostic(t *testing.T) {
	clearDoctorEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".memax-code", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"provider":"bogus"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"doctor"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
		t.Fatalf("Run() error = %v, want doctor failure", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Memax Code doctor",
		"[fail] arguments: invalid config " + configPath + " provider: unknown provider \"bogus\"",
		"summary: 1 fail, 0 warn",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func clearDoctorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MEMAX_CODE_CONFIG",
		"MEMAX_CODE_PROVIDER",
		"MEMAX_CODE_PROFILE",
		"MEMAX_CODE_EFFORT",
		"MEMAX_CODE_PRESET",
		"MEMAX_CODE_UI",
		"MEMAX_CODE_SESSION_DIR",
		"MEMAX_CODE_INHERIT_COMMAND_ENV",
		"OPENAI_API_KEY",
		"OPENAI_MODEL",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_MODEL",
	} {
		t.Setenv(key, "")
	}
}
