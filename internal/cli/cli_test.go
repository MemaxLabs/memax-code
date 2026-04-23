package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MemaxLabs/memax-go-agent-sdk/model"
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
		"effort: auto",
		"preset: safe_local",
		"ui: auto",
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

func TestDryRunPrintsEffortOverride(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--effort", "high",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"effort: high",
		"effort_description: Increase reasoning depth",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestDryRunLoadsConfigFileDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"provider": "anthropic",
		"model": "claude-test",
		"profile": "deep",
		"effort": "high",
		"preset": "safe_local",
		"ui": "plain",
		"session_dir": "./sessions",
		"inherit_command_env": true
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--config", configPath,
		"--cwd", repoRoot(t),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"provider: anthropic",
		"model: claude-test",
		"profile: deep",
		"effort: high",
		"preset: safe_local",
		"ui: plain",
		"config_loaded: true",
		"inherit_command_env: true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestDryRunPrintsCustomVerificationCommands(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--cwd", cwd,
		"--provider", "openai",
		"--model", "example-model",
		"--verify-command", "test=npm test",
		"--verify-command", "lint=npm run lint",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"verification: custom",
		"verify_command.lint: npm run lint",
		"verify_command.test: npm test",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestParseFlagAndEnvOverrideConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"provider": "anthropic",
		"model": "config-model",
		"profile": "deep",
		"effort": "high",
		"ui": "plain"
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OPENAI_MODEL", "env-model")
	t.Setenv("MEMAX_CODE_EFFORT", "low")

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{
		"--dry-run",
		"--config", configPath,
		"--provider", "openai",
		"--profile", "fast",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.Provider != providerOpenAI {
		t.Fatalf("Provider = %q, want openai", opts.Provider)
	}
	if opts.Model != "env-model" {
		t.Fatalf("Model = %q, want env override", opts.Model)
	}
	if opts.Profile != "fast" {
		t.Fatalf("Profile = %q, want flag override", opts.Profile)
	}
	if opts.Effort != "low" {
		t.Fatalf("Effort = %q, want env override", opts.Effort)
	}
	if opts.UI != renderModePlain {
		t.Fatalf("UI = %q, want config value", opts.UI)
	}
}

func TestParseVerifyCommandsFromConfigAndEnv(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"verify_commands": {
			"test": "go test ./...",
			"lint": "go vet ./..."
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(config) error = %v", err)
	}
	if opts.VerifyCommands["test"] != "go test ./..." || opts.VerifyCommands["lint"] != "go vet ./..." {
		t.Fatalf("VerifyCommands from config = %#v", opts.VerifyCommands)
	}

	t.Setenv("MEMAX_CODE_VERIFY_COMMANDS", `{"test":"npm test"}`)
	opts, err = parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(env) error = %v", err)
	}
	if opts.VerifyCommands["test"] != "npm test" || len(opts.VerifyCommands) != 1 {
		t.Fatalf("VerifyCommands from env = %#v, want env override", opts.VerifyCommands)
	}
}

func TestParseRejectsDuplicateVerifyCommands(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{
		"--dry-run",
		"--verify-command", "Test=go test ./...",
		"--verify-command", "test=npm test",
	}, &stderr)
	if err == nil || !strings.Contains(err.Error(), `duplicate verify command "test"`) {
		t.Fatalf("parseArgs(flags) error = %v, want duplicate verify command", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"verify_commands": {
			"Test": "go test ./...",
			"test": "npm test"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err = parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err == nil || !strings.Contains(err.Error(), `duplicate verify command "test"`) {
		t.Fatalf("parseArgs(config) error = %v, want duplicate verify command", err)
	}
}

func TestParseRejectsInvalidVerifyCommand(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--verify-command", "missing-equals"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "name=command") {
		t.Fatalf("parseArgs() error = %v, want verify command validation", err)
	}
}

func TestParseRejectsBadConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"provider": "openai", "surprise": true}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parseArgs() error = %v, want unknown field", err)
	}
}

func TestParseRejectsConfigValidationWithOrigin(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "provider",
			body: `{"provider": "other"}`,
			want: "invalid config",
		},
		{
			name: "profile",
			body: `{"profile": "huge-brain"}`,
			want: "invalid config",
		},
		{
			name: "effort",
			body: `{"effort": "maximum"}`,
			want: "invalid config",
		},
		{
			name: "preset",
			body: `{"preset": "risky"}`,
			want: "invalid config",
		},
		{
			name: "ui",
			body: `{"ui": "fancy"}`,
			want: "invalid config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"MEMAX_CODE_PROVIDER", "MEMAX_CODE_PROFILE", "MEMAX_CODE_EFFORT", "MEMAX_CODE_PRESET", "MEMAX_CODE_UI"} {
				t.Setenv(key, "")
			}
			configPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configPath, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			var stderr bytes.Buffer
			_, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), configPath) {
				t.Fatalf("parseArgs() error = %v, want %q and config path", err, tt.want)
			}
		})
	}
}

func TestParseRejectsConfigDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.Mkdir(configPath, 0o700); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}

	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("parseArgs() error = %v, want not a regular file", err)
	}
}

func TestParseReportsBrokenDefaultConfigWithRecoveryHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".memax-code", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"surprise": true}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--list-sessions"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "fix or remove the default config file") {
		t.Fatalf("parseArgs() error = %v, want recovery hint", err)
	}
}

func TestParseIgnoresMissingDefaultConfigFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.ConfigLoaded {
		t.Fatal("ConfigLoaded = true, want false for missing default config")
	}
}

func TestDryRunPrintsUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--ui", "plain",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "ui: plain") {
		t.Fatalf("dry-run output missing ui:\n%s", out)
	}
}

func TestInspectToolsPrintsModelFacingContracts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--inspect-tools",
		"--cwd", t.TempDir(),
		"--provider", "openai",
		"--model", "example-model",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"tool: run_command\n",
		`"command":{"description":"Shell command string`,
		`"type":"string"`,
		"tool: start_command\n",
		`configured or default platform shell`,
		"tool: workspace_apply_patch\n",
		`"unified_diff"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspect-tools output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"operations"`) {
		t.Fatalf("inspect-tools output exposed structured patch operations:\n%s", out)
	}
}

func TestInspectToolsRejectsPrompt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--inspect-tools",
		"--cwd", t.TempDir(),
		"--provider", "openai",
		"--model", "example-model",
		"inspect",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--inspect-tools does not accept a prompt") {
		t.Fatalf("Run() error = %v, want prompt rejection", err)
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
		"--resume", "00000000-0000-7000-8000-000000000001",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("Run() error = %v, want missing session", err)
	}
}

func TestDryRunResolvesLatestResumeSession(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	first, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	second, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	if err := store.Append(ctx, first.ID, userMessage("continue the investigation")); err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, second.ID, time.Unix(200, 0))
	setTranscriptModTime(t, sessionDir, first.ID, time.Unix(300, 0))

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--session-dir", sessionDir,
		"--resume", "latest",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "resume_session: "+first.ID) {
		t.Fatalf("dry-run did not resolve latest to updated session %q:\n%s", first.ID, out)
	}
}

func TestDryRunResumeLatestSkipsCorruptNewestTranscript(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	valid, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, valid.ID, time.Unix(100, 0))
	corruptID := "00000000-0000-7000-8000-000000000099"
	if err := os.WriteFile(transcriptPath(sessionDir, corruptID), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("write corrupt transcript: %v", err)
	}
	setTranscriptModTime(t, sessionDir, corruptID, time.Unix(200, 0))

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--session-dir", sessionDir,
		"--resume", "latest",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "resume_session: "+valid.ID) {
		t.Fatalf("dry-run did not skip corrupt latest transcript %q:\n%s", corruptID, out)
	}
}

func TestDryRunResumeLatestRequiresExistingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
		"--session-dir", t.TempDir(),
		"--resume", "latest",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "resume latest: no sessions") {
		t.Fatalf("Run() error = %v, want no sessions", err)
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
		{"--list-sessions", "--resume", "00000000-0000-7000-8000-000000000000"},
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
	second, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, first.ID, time.Unix(100, 0))
	setTranscriptModTime(t, sessionDir, second.ID, time.Unix(200, 0))

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

func TestListSessionsPrintsTitleAndOrdersByActivity(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	first, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	second, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	if err := store.Append(ctx, second.ID, userMessage("older visible prompt")); err != nil {
		t.Fatalf("Append() second error = %v", err)
	}
	if err := store.Append(ctx, first.ID, userMessage("newer visible prompt")); err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, second.ID, time.Unix(200, 0))
	setTranscriptModTime(t, sessionDir, first.ID, time.Unix(300, 0))
	corruptID := "00000000-0000-7000-8000-000000000098"
	if err := os.WriteFile(transcriptPath(sessionDir, corruptID), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("write corrupt transcript: %v", err)
	}
	setTranscriptModTime(t, sessionDir, corruptID, time.Unix(400, 0))

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
	for _, want := range []string{"UPDATED", "CREATED", "TITLE", "newer visible prompt", "older visible prompt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, corruptID) {
		t.Fatalf("list output included corrupt transcript:\n%s", out)
	}
	firstIndex := strings.Index(out, first.ID)
	secondIndex := strings.Index(out, second.ID)
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Fatalf("list output not ordered by latest activity:\n%s", out)
	}
}

func TestListSessionsStripsTitleControlBytes(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, userMessage("hello \x1b[31mred\x07 world")); err != nil {
		t.Fatalf("Append() error = %v", err)
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
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Fatalf("list output contains terminal control bytes:\n%q", out)
	}
	if !strings.Contains(out, "hello [31mred world") {
		t.Fatalf("list output missing sanitized title:\n%s", out)
	}
}

func TestShowSessionPrintsTranscript(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, userMessage("inspect this session")); err != nil {
		t.Fatalf("Append() user error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, model.Message{
		Role: model.RoleAssistant,
		Content: []model.ContentBlock{
			{Type: model.ContentText, Text: "I will read the file.\r\nThen continue."},
			{
				Type: model.ContentToolUse,
				ToolUse: &model.ToolUse{
					ID:    "tool-1",
					Name:  "read_file",
					Input: []byte(`{"path":"README.md"}`),
				},
			},
			{Type: model.ContentType("future_block")},
		},
	}); err != nil {
		t.Fatalf("Append() assistant error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, model.Message{
		Role: model.RoleTool,
		ToolResult: &model.ToolResult{
			ToolUseID: "tool-1",
			Name:      "read_file",
			Content:   "file contents",
		},
	}); err != nil {
		t.Fatalf("Append() tool error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{
		"--show-session", sess.ID,
		"--session-dir", sessionDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"session: " + sess.ID,
		"messages: 3",
		"[1] user",
		"inspect this session",
		"[2] assistant",
		"I will read the file.",
		"Then continue.",
		"tool_use: read_file id=tool-1",
		`input: {"path":"README.md"}`,
		"content: type=future_block",
		"[3] tool",
		"tool_result: read_file id=tool-1",
		"file contents",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n\n  Then continue.") {
		t.Fatalf("show output expanded CRLF into a blank line:\n%q", out)
	}
}

func TestShowSessionResolvesLatestAndIgnoresModelConfig(t *testing.T) {
	t.Setenv("MEMAX_CODE_PROVIDER", "not-a-provider")
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	older, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() older error = %v", err)
	}
	newer, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() newer error = %v", err)
	}
	if err := store.Append(ctx, older.ID, userMessage("older prompt")); err != nil {
		t.Fatalf("Append() older error = %v", err)
	}
	if err := store.Append(ctx, newer.ID, userMessage("newer prompt")); err != nil {
		t.Fatalf("Append() newer error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, older.ID, time.Unix(100, 0))
	setTranscriptModTime(t, sessionDir, newer.ID, time.Unix(200, 0))

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{
		"--show-session", "latest",
		"--session-dir", sessionDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "session: "+newer.ID) || !strings.Contains(out, "newer prompt") {
		t.Fatalf("show latest did not resolve newest session:\n%s", out)
	}
	if strings.Contains(out, older.ID) || strings.Contains(out, "older prompt") {
		t.Fatalf("show latest included older session:\n%s", out)
	}
}

func TestShowSessionRejectsConflictingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--show-session", "latest", "--list-sessions"},
		{"--show-session", "latest", "--resume", "00000000-0000-7000-8000-000000000000"},
		{"--show-session", "latest", "--dry-run"},
		{"--show-session", "latest", "prompt"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), append(args, "--session-dir", t.TempDir()), &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "--show-session") {
			t.Fatalf("Run(%v) error = %v, want show-session conflict", args, err)
		}
	}
}

func TestShowSessionReportsInvalidMissingAndCorruptSessions(t *testing.T) {
	sessionDir := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "invalid id",
			args: []string{"--show-session", "not-a-session", "--session-dir", sessionDir},
			want: "invalid session id",
		},
		{
			name: "missing id",
			args: []string{"--show-session", "00000000-0000-7000-8000-000000000077", "--session-dir", sessionDir},
			want: "session not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run() error = %v, want %q", err, tc.want)
			}
		})
	}

	corruptID := "00000000-0000-7000-8000-000000000078"
	if err := os.WriteFile(transcriptPath(sessionDir, corruptID), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("write corrupt transcript: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--show-session", corruptID,
		"--session-dir", sessionDir,
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "decode transcript line") {
		t.Fatalf("Run() error = %v, want decode error", err)
	}
}

func TestShowSessionHandlesTranscriptWithoutSessionEntry(t *testing.T) {
	sessionDir := t.TempDir()
	id := "00000000-0000-7000-8000-000000000079"
	transcript := `{"type":"message","timestamp":"2026-04-22T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"message only"}]}}` + "\n"
	if err := os.WriteFile(transcriptPath(sessionDir, id), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--show-session", id,
		"--session-dir", sessionDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"session: " + id, "created: -", "message only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0001-01-01") {
		t.Fatalf("show output rendered zero time:\n%s", out)
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

func TestRunInteractiveHandlesSlashCommandsWithoutProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunWithIO(context.Background(), []string{
		"--interactive",
		"--session-dir", t.TempDir(),
	}, strings.NewReader("/help\n/session\n/new\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no transcript for slash-only session", stdout.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"Memax Code interactive shell",
		"slash commands:",
		"/status",
		"no active session",
		"started a new session",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveDraftCommandsWithoutProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunWithIO(context.Background(), []string{
		"--interactive",
		"--session-dir", t.TempDir(),
	}, strings.NewReader("/draft\nfirst line\n  indented line\n  /etc/hosts\n\n//literal slash\n/show-draft\n/cancel\n/status\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no transcript for slash-only draft session", stdout.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"draft started; type lines, /submit to send, /cancel to discard",
		"draft> ",
		"draft:",
		"  first line",
		"    indented line",
		"    /etc/hosts",
		"  /literal slash",
		"draft canceled",
		"draft: inactive",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveDraftPolishCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunWithIO(context.Background(), []string{
		"--interactive",
		"--session-dir", t.TempDir(),
	}, strings.NewReader("/append first\n/draft replacement\n/submit\n/quit\n"), &stdout, &stderr)
	if err == nil {
		t.Fatalf("RunWithIO() error = nil, want provider setup error after draft submit")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no transcript without provider", stdout.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"draft started; type lines, /submit to send, /cancel to discard",
		"draft appended: lines=1",
		"discarded draft: lines=1",
		"error:",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveSubmitsDraftAsOnePrompt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var prompts []string
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("/draft Refactor this\nwith detail\n/submit\n/session\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: t.TempDir(), UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			prompts = append(prompts, opts.Prompt)
			fmt.Fprintln(w, "ran prompt")
			return "00000000-0000-7000-8000-000000000123", nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("submitted prompts = %#v, want one prompt", prompts)
	}
	if want := "Refactor this\nwith detail"; prompts[0] != want {
		t.Fatalf("submitted prompt = %q, want %q", prompts[0], want)
	}
	if got := stdout.String(); !strings.Contains(got, "ran prompt") {
		t.Fatalf("stdout = %q, want fake prompt output", got)
	}
	if out := stderr.String(); !strings.Contains(out, "session: 00000000-0000-7000-8000-000000000123") {
		t.Fatalf("interactive stderr missing updated session:\n%s", out)
	}
}

func TestRunInteractivePromptHistoryRecall(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var prompts []string
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("first prompt\n/draft second\nline\n/submit\n/history\n/recall 2\n/show-draft\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: t.TempDir(), UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			prompts = append(prompts, opts.Prompt)
			fmt.Fprintf(w, "ran %d\n", len(prompts))
			return fmt.Sprintf("00000000-0000-7000-8000-%012d", len(prompts)), nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("submitted prompts = %#v, want two prompts", prompts)
	}
	if prompts[0] != "first prompt" || prompts[1] != "second\nline" {
		t.Fatalf("submitted prompts = %#v", prompts)
	}
	out := stderr.String()
	for _, want := range []string{
		"prompt history:",
		"1) second line",
		"2) first prompt",
		"recalled prompt: lines=1 chars=12",
		"draft:",
		"  first prompt",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractivePromptHistorySkipsFailedDraftSubmit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("/draft failing\n/submit\n/history\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: t.TempDir(), UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			return "", fmt.Errorf("boom")
		},
	)
	if err == nil {
		t.Fatal("runInteractiveWithRunner() error = nil, want prompt failure")
	}
	out := stderr.String()
	if strings.Contains(out, "1) failing") {
		t.Fatalf("failed draft submit was recorded in history:\n%s", out)
	}
	if !strings.Contains(out, "no prompt history") {
		t.Fatalf("interactive stderr missing empty history message:\n%s", out)
	}
}

func TestRunInteractiveRecallWarnsBeforeReplacingDraft(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("old prompt\n/draft new work\n/recall latest\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: t.TempDir(), UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			return "00000000-0000-7000-8000-000000000123", nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"discarded draft: lines=1",
		"recalled prompt: lines=1 chars=10",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveStatus(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, userMessage("status prompt")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--provider", "openai",
		"--model", "example-model",
		"--profile", "fast",
		"--effort", "high",
		"--preset", "interactive_dev",
		"--ui", "plain",
		"--cwd", repoRoot(t),
		"--session-dir", sessionDir,
	}, strings.NewReader("/status\n/resume latest\n/status\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"status:",
		"provider: openai",
		"model: example-model",
		"profile: fast",
		"effort: high",
		"preset: interactive_dev",
		"ui: plain",
		"cwd: " + repoRoot(t),
		"session_dir: " + sessionDir,
		"active_session: <unset>",
		"saved_sessions: 1",
		"verification: go",
		"inherit_command_env: false",
		"resumed session: " + sess.ID,
		"active_session: " + sess.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveResumeLatestCommand(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, userMessage("resume me")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/resume latest\n/session\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"resumed session: " + sess.ID,
		"session: " + sess.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractivePickAndResumeByIndex(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	older, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() older error = %v", err)
	}
	newer, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() newer error = %v", err)
	}
	if err := store.Append(ctx, older.ID, userMessage("older task")); err != nil {
		t.Fatalf("Append() older error = %v", err)
	}
	if err := store.Append(ctx, newer.ID, userMessage("newer task")); err != nil {
		t.Fatalf("Append() newer error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, older.ID, time.Unix(100, 0))
	setTranscriptModTime(t, sessionDir, newer.ID, time.Unix(200, 0))

	var stdout, stderr bytes.Buffer
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/pick\n/resume 1\n/session\n/resume 2\n/session\n/resume 3\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"recent sessions:",
		"1) " + newer.ID,
		"2) " + older.ID,
		"resumed session: " + newer.ID,
		"session: " + newer.ID,
		"resumed session: " + older.ID,
		"session: " + older.ID,
		"resume session index 3 out of range; choose 1-2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractivePickAndResumeIndexErrorPaths(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	err := RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/pick\n/resume 3\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() empty error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"no sessions",
		"resume session index 3: no sessions",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}

	store := session.NewJSONLStore(sessionDir)
	if _, err := store.Create(ctx); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/resume 0\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() zero error = %v", err)
	}
	if out := stderr.String(); !strings.Contains(out, "resume session index 0 out of range; choose 1-1") {
		t.Fatalf("interactive stderr missing zero-index error:\n%s", out)
	}
}

func TestRunInteractiveShowSessionTargets(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	older, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() older error = %v", err)
	}
	newer, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() newer error = %v", err)
	}
	if err := store.Append(ctx, older.ID, userMessage("older transcript prompt")); err != nil {
		t.Fatalf("Append() older error = %v", err)
	}
	if err := store.Append(ctx, newer.ID, userMessage("newer transcript prompt")); err != nil {
		t.Fatalf("Append() newer error = %v", err)
	}
	setTranscriptModTime(t, sessionDir, older.ID, time.Unix(100, 0))
	setTranscriptModTime(t, sessionDir, newer.ID, time.Unix(200, 0))

	var stdout, stderr bytes.Buffer
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/show\n/show garbage\n/show 99\n/show latest\n/show 2\n/resume 2\n/show\n/show current\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"show session: no active session",
		`show session "garbage": invalid session id`,
		"show session index 99 out of range; choose 1-2",
		"session: " + newer.ID,
		"newer transcript prompt",
		"session: " + older.ID,
		"older transcript prompt",
		"resumed session: " + older.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
	if count := strings.Count(out, "session: "+older.ID); count < 3 {
		t.Fatalf("show current did not print active older session twice after resume; count=%d:\n%s", count, out)
	}
}

func TestRunInteractiveSlashCommandErrorPathsContinue(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	store := session.NewJSONLStore(sessionDir)
	sess, err := store.Create(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Append(ctx, sess.ID, userMessage("list me")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = RunWithIO(ctx, []string{
		"--interactive",
		"--session-dir", sessionDir,
	}, strings.NewReader("/resume\n/resume not-a-session\n/sessions\n/nope\n/quit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunWithIO() error = %v", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"usage: /resume SESSION_ID|latest|N",
		"invalid session id",
		"SESSION ID",
		sess.ID,
		`unknown command "/nope"`,
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestInteractivePromptSlashEscape(t *testing.T) {
	if !isInteractiveCommandLine("/help") {
		t.Fatal("/help was not treated as a slash command")
	}
	if isInteractiveCommandLine("//etc/hosts is broken") {
		t.Fatal("//etc/hosts was treated as a slash command")
	}
	if got := unescapeInteractivePrompt("//etc/hosts is broken"); got != "/etc/hosts is broken" {
		t.Fatalf("unescapeInteractivePrompt() = %q, want leading slash prompt", got)
	}
	if got := unescapeInteractivePrompt("regular prompt"); got != "regular prompt" {
		t.Fatalf("unescapeInteractivePrompt() = %q, want unchanged regular prompt", got)
	}
}

func TestRunInteractiveRejectsPromptAndConflictingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--interactive", "initial prompt"},
		{"--interactive", "--dry-run"},
		{"--interactive", "--list-sessions"},
		{"--interactive", "--show-session", "latest"},
		{"--interactive", "--inspect-tools"},
	} {
		var stdout, stderr bytes.Buffer
		err := RunWithIO(context.Background(), append(args, "--session-dir", t.TempDir()), strings.NewReader("/quit\n"), &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "--interactive") {
			t.Fatalf("RunWithIO(%v) error = %v, want interactive conflict", args, err)
		}
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
	// Keep the SDK-specific wording out of CLI-facing validation errors.
	if strings.Contains(strings.ToLower(err.Error()), "coding"+" stack") {
		t.Fatalf("parseArgs() leaked SDK wording: %v", err)
	}
}

func TestParseRejectsUnknownEffortWithCLIError(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--effort", "maximum"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown model effort") || !strings.Contains(err.Error(), "xhigh") {
		t.Fatalf("parseArgs() error = %v, want unknown model effort", err)
	}
	// Keep the SDK-specific wording out of CLI-facing validation errors.
	if strings.Contains(strings.ToLower(err.Error()), "coding"+" stack") {
		t.Fatalf("parseArgs() leaked SDK wording: %v", err)
	}
}

func TestParseRejectsUnknownUI(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--ui", "fancy"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unknown ui "fancy"`) || !strings.Contains(err.Error(), "live") {
		t.Fatalf("parseArgs() error = %v, want unknown ui", err)
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

func TestParseDefaultsEffortFromEnv(t *testing.T) {
	t.Setenv("MEMAX_CODE_EFFORT", "high")
	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	effort, err := parseModelEffort(opts.Effort)
	if err != nil {
		t.Fatalf("parseModelEffort() error = %v", err)
	}
	if effort.String() != "high" {
		t.Fatalf("effort = %q, want high", effort)
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

func userMessage(text string) model.Message {
	return model.Message{
		Role: model.RoleUser,
		Content: []model.ContentBlock{
			{Type: model.ContentText, Text: text},
		},
	}
}

func transcriptPath(dir, id string) string {
	return filepath.Join(dir, id+".jsonl")
}

func setTranscriptModTime(t *testing.T, dir, id string, ts time.Time) {
	t.Helper()
	if err := os.Chtimes(transcriptPath(dir, id), ts, ts); err != nil {
		t.Fatalf("set transcript mtime for %s: %v", id, err)
	}
}
