package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigInitCreatesStrictConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"config", "init",
		"--config", configPath,
		"--provider", "anthropic",
		"--model", "claude-test",
		"--profile", "deep",
		"--effort", "high",
		"--preset", "safe_local",
		"--ui", "plain",
		"--session-dir", ".memax-code/sessions",
		"--inherit-command-env",
		"--verify-command", "test=npm test",
		"--verify-command", "lint=npm run lint",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "created config: "+configPath) {
		t.Fatalf("stdout missing created path:\n%s", stdout.String())
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, want := range []string{
		`"provider": "anthropic"`,
		`"model": "claude-test"`,
		`"profile": "deep"`,
		`"effort": "high"`,
		`"preset": "safe_local"`,
		`"ui": "plain"`,
		`"session_dir": ".memax-code/sessions"`,
		`"inherit_command_env": true`,
		`"verify_commands": {`,
		`"lint": "npm run lint"`,
		`"test": "npm test"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("config missing %q:\n%s", want, body)
		}
	}

	var parseStderr bytes.Buffer
	for _, key := range []string{
		"MEMAX_CODE_PROVIDER",
		"MEMAX_CODE_PROFILE",
		"MEMAX_CODE_EFFORT",
		"MEMAX_CODE_PRESET",
		"MEMAX_CODE_UI",
		"MEMAX_CODE_SESSION_DIR",
		"MEMAX_CODE_INHERIT_COMMAND_ENV",
		"OPENAI_MODEL",
		"ANTHROPIC_MODEL",
	} {
		t.Setenv(key, "")
	}
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &parseStderr)
	if err != nil {
		t.Fatalf("parse generated config: %v", err)
	}
	if opts.Provider != providerAnthropic || opts.Profile != "deep" || opts.Effort != "high" || opts.UI != renderModePlain {
		t.Fatalf("parsed opts = %+v, want generated config values", opts)
	}
}

func TestConfigInitRefusesExistingUnlessForced(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"provider":"openai"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "init", "--config", configPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Run() error = %v, want force hint", err)
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(context.Background(), []string{"config", "init", "--config", configPath, "--force", "--provider", "anthropic"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() force error = %v", err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(body), `"provider": "anthropic"`) {
		t.Fatalf("forced config did not update provider:\n%s", body)
	}
}

func TestConfigInitOmitsUnsetOptionalBoolAndCanonicalizesValues(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"config", "init",
		"--config", configPath,
		"--provider", "ANTHROPIC",
		"--profile", "DEEP",
		"--effort", "HIGH",
		"--preset", "SAFE_LOCAL",
		"--ui", "LIVE",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, want := range []string{
		`"provider": "anthropic"`,
		`"profile": "deep"`,
		`"effort": "high"`,
		`"preset": "safe_local"`,
		`"ui": "live"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("config missing canonical value %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "inherit_command_env") {
		t.Fatalf("config wrote unset optional bool:\n%s", body)
	}
}

func TestConfigInitRejectsDirectoryPath(t *testing.T) {
	configPath := t.TempDir()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "init", "--config", configPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Run() error = %v, want regular-file error", err)
	}
}

func TestConfigShowPrintsLoadedConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"provider":"openai","profile":"fast"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "show", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"config: " + configPath,
		"config_loaded: true",
		`"provider": "openai"`,
		`"profile": "fast"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config show missing %q:\n%s", want, out)
		}
	}
}

func TestConfigShowMissingDefaultIsNotAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMAX_CODE_CONFIG", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "show"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "config_loaded: false") || !strings.Contains(out, filepath.Join(home, ".memax-code", "config.json")) {
		t.Fatalf("config show output = %q, want missing default config path", out)
	}
}

func TestConfigCommandRejectsInvalidValues(t *testing.T) {
	t.Setenv("MEMAX_CODE_CONFIG", "")
	configPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "init", "--config", configPath, "--profile", "huge-brain"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown model profile") {
		t.Fatalf("Run() error = %v, want profile validation", err)
	}
}

func TestConfigCommandWithoutSubcommandPrintsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: memax-code config init [flags]") {
		t.Fatalf("stdout = %q, want config usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
