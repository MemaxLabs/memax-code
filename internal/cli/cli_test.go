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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
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
		"compaction: auto",
		"context_window: 200000",
		"context_summary_tokens: 8192",
		"context_main_tokens: 160000",
		"context_retry_tokens: 110000",
		"session_dir: ",
		"resume_session: <unset>",
		"verification: go",
		"subagents: explorer, reviewer, worker",
		"web: true",
		"web_fetch_max_bytes: 524288",
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

func TestParseInheritsCommandEnvByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEMAX_CODE_INHERIT_COMMAND_ENV", "")

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !opts.InheritCommandEnv {
		t.Fatal("InheritCommandEnv = false, want true by default")
	}
}

func TestParseEnablesWebByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEMAX_CODE_WEB", "")
	t.Setenv("MEMAX_CODE_WEB_FETCH_MAX_BYTES", "")

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !opts.WebEnabled {
		t.Fatal("WebEnabled = false, want true by default")
	}
	if opts.WebFetchMaxBytes != defaultWebFetchMaxBytes {
		t.Fatalf("WebFetchMaxBytes = %d, want %d", opts.WebFetchMaxBytes, defaultWebFetchMaxBytes)
	}
}

func TestParseEnablesCompactionByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEMAX_CODE_COMPACTION", "")
	t.Setenv("MEMAX_CODE_CONTEXT_WINDOW", "")
	t.Setenv("MEMAX_CODE_CONTEXT_SUMMARY_TOKENS", "")

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--provider", "openai", "--model", "gpt-5.4"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.Compaction != compactionModeAuto {
		t.Fatalf("Compaction = %q, want auto", opts.Compaction)
	}
	if got := effectiveContextWindow(opts, nil); got != 272000 {
		t.Fatalf("effectiveContextWindow() = %d, want 272000", got)
	}
	if got := effectiveContextSummaryTokens(opts, effectiveContextWindow(opts, nil)); got != 8192 {
		t.Fatalf("effectiveContextSummaryTokens() = %d, want 8192", got)
	}
	budgets := resolveContextBudgets(opts, nil)
	if budgets.WindowTokens != 272000 || budgets.SummaryTokens != 8192 || budgets.RetryTokens >= budgets.MainTokens {
		t.Fatalf("resolveContextBudgets() = %+v, want resolved budgets with retry < main", budgets)
	}
}

func TestResolveContextBudgetsClampsDryRunValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{
		"--dry-run",
		"--context-window", "1000",
		"--context-summary-tokens", "100000",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	budgets := resolveContextBudgets(opts, nil)
	if budgets.WindowTokens != minContextWindowTokens {
		t.Fatalf("WindowTokens = %d, want %d", budgets.WindowTokens, minContextWindowTokens)
	}
	if budgets.SummaryTokens != minContextWindowTokens/4 {
		t.Fatalf("SummaryTokens = %d, want %d", budgets.SummaryTokens, minContextWindowTokens/4)
	}
	if budgets.RetryTokens > budgets.MainTokens {
		t.Fatalf("RetryTokens = %d, MainTokens = %d; retry must not exceed main", budgets.RetryTokens, budgets.MainTokens)
	}
}

func TestResolveContextBudgetsPrefersClientCapabilities(t *testing.T) {
	opts := options{
		Provider:   providerOpenAI,
		Model:      "unknown-model",
		Compaction: compactionModeAuto,
	}
	client := capabilitiesOnlyClient{
		caps: model.Capabilities{
			Provider:            "custom",
			Model:               "unknown-model",
			ContextWindowTokens: 64000,
		},
	}
	budgets := resolveContextBudgets(opts, client)
	if budgets.WindowTokens != 64000 {
		t.Fatalf("WindowTokens = %d, want client capability 64000", budgets.WindowTokens)
	}
	if budgets.MainTokens != 51200 || budgets.RetryTokens != 35200 {
		t.Fatalf("budgets = %+v, want 80%%/55%% of client capability", budgets)
	}
}

func TestInferredContextWindowHonorsGatewayModelFamily(t *testing.T) {
	tests := []struct {
		name      string
		provider  provider
		modelName string
		want      int
	}{
		{
			name:      "openai transport openai model",
			provider:  providerOpenAI,
			modelName: "openai/gpt-5.5-pro",
			want:      272000,
		},
		{
			name:      "openai transport anthropic model through gateway",
			provider:  providerOpenAI,
			modelName: "anthropic/claude-opus-4.7",
			want:      200000,
		},
		{
			name:      "anthropic transport openai model through gateway",
			provider:  providerAnthropic,
			modelName: "openai/gpt-5.5-pro",
			want:      272000,
		},
		{
			name:      "anthropic transport anthropic model",
			provider:  providerAnthropic,
			modelName: "anthropic/claude-sonnet-4.6",
			want:      200000,
		},
		{
			name:      "unknown openai transport keeps default",
			provider:  providerOpenAI,
			modelName: "gateway/custom-model",
			want:      defaultContextWindowTokens,
		},
		{
			name:      "unknown anthropic transport keeps anthropic default",
			provider:  providerAnthropic,
			modelName: "gateway/custom-model",
			want:      200000,
		},
		{
			name:      "known openai family unknown model keeps openai default",
			provider:  providerAnthropic,
			modelName: "openai/custom-model",
			want:      defaultContextWindowTokens,
		},
		{
			name:      "known anthropic family unknown model keeps anthropic default",
			provider:  providerOpenAI,
			modelName: "anthropic/future-model",
			want:      200000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferredContextWindow(tt.provider, tt.modelName); got != tt.want {
				t.Fatalf("inferredContextWindow(%q, %q) = %d, want %d", tt.provider, tt.modelName, got, tt.want)
			}
		})
	}
}

func TestEstimateApproxTokensIsConservative(t *testing.T) {
	msg := model.Message{
		Role: model.RoleUser,
		Content: []model.ContentBlock{{
			Type: model.ContentText,
			Text: "1234567890",
		}},
	}
	if got, want := estimateApproxTokens(msg), 4; got != want {
		t.Fatalf("estimateApproxTokens() = %d, want %d", got, want)
	}
}

type capabilitiesOnlyClient struct {
	caps model.Capabilities
}

func (c capabilitiesOnlyClient) Stream(context.Context, model.Request) (model.Stream, error) {
	return nil, model.ErrEndOfStream
}

func (c capabilitiesOnlyClient) Capabilities() model.Capabilities {
	return c.caps
}

func TestParseCanDisableCompactionAndOverrideContextBudget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{
		"--dry-run",
		"--compaction", "off",
		"--context-window", "64000",
		"--context-summary-tokens", "4096",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.Compaction != compactionModeOff {
		t.Fatalf("Compaction = %q, want off", opts.Compaction)
	}
	if opts.ContextWindow != 64000 {
		t.Fatalf("ContextWindow = %d, want 64000", opts.ContextWindow)
	}
	if opts.ContextSummary != 4096 {
		t.Fatalf("ContextSummary = %d, want 4096", opts.ContextSummary)
	}
}

func TestDryRunShowsDisabledContextBudgetsWhenCompactionOff(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--compaction", "off",
		"--context-window", "64000",
		"--context-summary-tokens", "4096",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"compaction: off",
		"context_window: disabled",
		"context_summary_tokens: disabled",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestParseCompactionConfigAndEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"compaction": "off", "context_window": 32000, "context_summary_tokens": 2048}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MEMAX_CODE_COMPACTION", "auto")

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.Compaction != compactionModeAuto {
		t.Fatalf("Compaction = %q, want env override auto", opts.Compaction)
	}
	if opts.ContextWindow != 32000 || opts.ContextSummary != 2048 {
		t.Fatalf("context budgets = %d/%d, want config values", opts.ContextWindow, opts.ContextSummary)
	}
}

func TestParseCanDisableDefaultWeb(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, args := range [][]string{
		{"--dry-run", "--web=false"},
		{"--dry-run", "--no-web"},
	} {
		var stderr bytes.Buffer
		opts, err := parseArgs(args, &stderr)
		if err != nil {
			t.Fatalf("parseArgs(%v) error = %v", args, err)
		}
		if opts.WebEnabled {
			t.Fatalf("parseArgs(%v) WebEnabled = true, want false", args)
		}
	}
}

func TestParseNoWebFalseEnablesWeb(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"web": false}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath, "--no-web=false"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !opts.WebEnabled {
		t.Fatal("WebEnabled = false, want --no-web=false to enable web")
	}
}

func TestParseWebConfigAndEnvCanOptOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"web": false, "web_fetch_max_bytes": 1024}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(config) error = %v", err)
	}
	if opts.WebEnabled {
		t.Fatal("config web=false did not override default")
	}
	if opts.WebFetchMaxBytes != 1024 {
		t.Fatalf("config WebFetchMaxBytes = %d, want 1024", opts.WebFetchMaxBytes)
	}

	t.Setenv("MEMAX_CODE_WEB", "false")
	t.Setenv("MEMAX_CODE_WEB_FETCH_MAX_BYTES", "2048")
	opts, err = parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(env) error = %v", err)
	}
	if opts.WebEnabled {
		t.Fatal("MEMAX_CODE_WEB=false did not override default")
	}
	if opts.WebFetchMaxBytes != 2048 {
		t.Fatalf("env WebFetchMaxBytes = %d, want 2048", opts.WebFetchMaxBytes)
	}
}

func TestParseRejectsConflictingWebFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "--web", "--no-web"},
		{"--dry-run", "--web=true", "--no-web=false"},
	} {
		var stderr bytes.Buffer
		_, err := parseArgs(args, &stderr)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("parseArgs(%v) error = %v, want conflicting web flags", args, err)
		}
	}
}

func TestParseRejectsInvalidWebFetchMaxBytes(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "--web-fetch-max-bytes", "0"},
		{"--dry-run", "--web-fetch-max-bytes", "-1"},
	} {
		var stderr bytes.Buffer
		_, err := parseArgs(args, &stderr)
		if err == nil || !strings.Contains(err.Error(), "web-fetch-max-bytes must be greater than 0") {
			t.Fatalf("parseArgs(%v) error = %v, want invalid web fetch max", args, err)
		}
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"web_fetch_max_bytes": 0}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "web-fetch-max-bytes must be greater than 0") {
		t.Fatalf("parseArgs(config zero) error = %v, want invalid web fetch max", err)
	}
}

func TestParseCanDisableInheritedCommandEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, args := range [][]string{
		{"--dry-run", "--inherit-command-env=false"},
		{"--dry-run", "--no-inherit-command-env"},
	} {
		var stderr bytes.Buffer
		opts, err := parseArgs(args, &stderr)
		if err != nil {
			t.Fatalf("parseArgs(%v) error = %v", args, err)
		}
		if opts.InheritCommandEnv {
			t.Fatalf("parseArgs(%v) InheritCommandEnv = true, want false", args)
		}
	}
}

func TestParseNoInheritedCommandEnvFalseEnablesInheritance(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inherit_command_env": false}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath, "--no-inherit-command-env=false"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !opts.InheritCommandEnv {
		t.Fatal("InheritCommandEnv = false, want --no-inherit-command-env=false to enable inheritance")
	}
}

func TestParseInheritedCommandEnvConfigAndEnvCanOptOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inherit_command_env": false}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(config) error = %v", err)
	}
	if opts.InheritCommandEnv {
		t.Fatal("config inherit_command_env=false did not override default")
	}

	t.Setenv("MEMAX_CODE_INHERIT_COMMAND_ENV", "false")
	opts, err = parseArgs([]string{"--dry-run"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs(env) error = %v", err)
	}
	if opts.InheritCommandEnv {
		t.Fatal("MEMAX_CODE_INHERIT_COMMAND_ENV=false did not override default")
	}
}

func TestParseRejectsConflictingInheritedCommandEnvFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "--inherit-command-env", "--no-inherit-command-env"},
		{"--dry-run", "--inherit-command-env=true", "--no-inherit-command-env=false"},
	} {
		var stderr bytes.Buffer
		_, err := parseArgs(args, &stderr)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("parseArgs(%v) error = %v, want conflicting inherit env flags", args, err)
		}
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
	if strings.Contains(out, "[31m") {
		t.Fatalf("list output contains ANSI fragment:\n%q", out)
	}
	if !strings.Contains(out, "hello red world") {
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

func TestRunInteractiveLoadsAndPersistsPromptHistory(t *testing.T) {
	tempDir := t.TempDir()
	historyFile := filepath.Join(tempDir, "history.jsonl")
	if err := newPersistentPromptHistory(historyFile).Append("persisted prompt"); err != nil {
		t.Fatalf("append seed history: %v", err)
	}
	var stdout, stderr bytes.Buffer
	var prompts []string
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("/history\n/recall latest\n/submit\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: tempDir, HistoryFile: historyFile, UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			prompts = append(prompts, opts.Prompt)
			fmt.Fprintln(w, "ran prompt")
			return "00000000-0000-7000-8000-000000000123", nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "persisted prompt" {
		t.Fatalf("submitted prompts = %#v, want persisted prompt", prompts)
	}
	body, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if got := strings.Count(string(body), `"text":"persisted prompt"`); got != 1 {
		t.Fatalf("history text count = %d, want one deduped persisted prompt:\n%s", got, body)
	}
	out := stderr.String()
	for _, want := range []string{
		"prompt history:",
		"1) persisted prompt",
		"recalled prompt: lines=1 chars=16",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunInteractiveWarnsButContinuesWhenPromptHistoryUnavailable(t *testing.T) {
	tempDir := t.TempDir()
	historyFile := filepath.Join(tempDir, "history-dir")
	if err := os.Mkdir(historyFile, 0o700); err != nil {
		t.Fatalf("mkdir history dir: %v", err)
	}
	var stdout, stderr bytes.Buffer
	var prompts []string
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("fresh prompt\n/quit\n"),
		&stdout,
		&stderr,
		options{SessionDir: tempDir, HistoryFile: historyFile, UI: renderModePlain},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			prompts = append(prompts, opts.Prompt)
			return "00000000-0000-7000-8000-000000000123", nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "fresh prompt" {
		t.Fatalf("submitted prompts = %#v, want fresh prompt", prompts)
	}
	out := stderr.String()
	for _, want := range []string{
		"warning: prompt history",
		"is a directory",
		"warning: open prompt history",
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
		"inherit_command_env: true",
		"resumed session: " + sess.ID,
		"active_session: " + sess.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunResumeWithoutPromptStartsInteractiveShellOnTerminalIO(t *testing.T) {
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

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- RunWithIO(ctx, []string{
			"--resume", "latest",
			"--session-dir", sessionDir,
		}, tty, tty, tty)
	}()

	if _, err := io.WriteString(ptmx, "/session\r/quit\r"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("RunWithIO() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithIO() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}
	out := output.String()
	for _, want := range []string{
		sess.ID,
		"session: " + sess.ID,
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("interactive output entered alt screen:\n%s", out)
	}
}

func TestRunWithoutPromptStartsAppOnTerminalIO(t *testing.T) {
	ctx := context.Background()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- RunWithIO(ctx, []string{
			"--session-dir", t.TempDir(),
		}, tty, tty, tty)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("RunWithIO() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithIO() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}
	out := output.String()
	for _, want := range []string{
		"Welcome. Type a task or /help.",
		"Memax Code",
		"session none",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("interactive output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("interactive output used full-screen clear:\n%s", out)
	}
}

func isClosedPTYRead(err error) bool {
	return strings.Contains(err.Error(), "input/output error")
}

func TestRunResumeWithoutPromptRejectsNonTerminalIO(t *testing.T) {
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
		"--resume", "latest",
		"--session-dir", sessionDir,
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("RunWithIO() error = %v, want missing prompt error", err)
	}
}

func TestShouldImplicitlyStartInteractiveWithTerminalIO(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	if !shouldImplicitlyStartInteractive(tty, tty, tty, options{UI: renderModePlain}) {
		t.Fatal("shouldImplicitlyStartInteractive() = false, want true for terminal stdin and shell output")
	}
}

func TestShouldImplicitlyStartInteractiveUsesStderrForShellSurface(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	if !shouldImplicitlyStartInteractive(tty, &bytes.Buffer{}, tty, options{UI: renderModeApp}) {
		t.Fatal("shouldImplicitlyStartInteractive() = false, want true when stdin/stderr are terminals even if stdout is redirected")
	}
}

func TestShouldImplicitlyStartInteractiveUsesStdoutForAppSurface(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	if !shouldImplicitlyStartInteractive(tty, tty, &bytes.Buffer{}, options{UI: renderModeApp}) {
		t.Fatal("shouldImplicitlyStartInteractive() = false, want true when stdin/stdout are terminals even if stderr is redirected")
	}
}

func TestShouldImplicitlyStartInteractiveRejectsNonTerminalIO(t *testing.T) {
	if shouldImplicitlyStartInteractive(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, options{UI: renderModePlain}) {
		t.Fatal("shouldImplicitlyStartInteractive() = true, want false for non-terminal IO")
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

func TestRunInteractiveAppFlagUsesInlineApp(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWithRunner(
			context.Background(),
			tty,
			tty,
			tty,
			options{SessionDir: t.TempDir(), UI: renderModeApp},
			func(_ context.Context, w io.Writer, opts options) (string, error) {
				fmt.Fprintf(w, "ran prompt %q\n", opts.Prompt)
				return "", nil
			},
		)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/help\r"); err != nil {
		t.Fatalf("WriteString(/help) error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString(/quit) error = %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runInteractiveWithRunner() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runInteractiveWithRunner() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}

	out := output.String()
	for _, want := range []string{
		"Welcome. Type a task or /help.",
		"Memax Code",
		"session none",
		"input draft: inactive",
		"slash commands:",
		"/quit              exit",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive app-compat output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("interactive app-compat output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("interactive app output used full-screen clear:\n%s", out)
	}
}

func TestRunInteractiveAppUsesInlineRendererWithoutAltScreen(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 120}); err != nil {
		t.Skipf("set pty size: %v", err)
	}

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWithRunner(
			context.Background(),
			tty,
			tty,
			tty,
			options{SessionDir: t.TempDir(), UI: renderModeApp},
			func(_ context.Context, w io.Writer, opts options) (string, error) {
				fmt.Fprintf(w, "ran prompt %q\n", opts.Prompt)
				return "", nil
			},
		)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runInteractiveWithRunner() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runInteractiveWithRunner() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}

	out := output.String()
	for _, want := range []string{"Welcome. Type a task or /help.", "Memax Code", "session none", "bye"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inline app output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("inline app output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("inline app output used full-screen clear:\n%s", out)
	}
	// Memax Code writes these explicitly because app mode disables Bubble
	// Tea's standard renderer and owns the inline live region.
	if !strings.Contains(out, ansi.SetBracketedPasteMode) {
		t.Fatalf("inline app output did not enable bracketed paste:\n%s", out)
	}
	if !strings.Contains(out, ansi.ResetBracketedPasteMode) {
		t.Fatalf("inline app output did not reset bracketed paste:\n%s", out)
	}
	if !strings.Contains(out, ansi.HideCursor) {
		t.Fatalf("inline app output did not hide cursor during live-region repaint:\n%s", out)
	}
	if !strings.Contains(out, ansi.ShowCursor) {
		t.Fatalf("inline app output did not restore cursor after live-region repaint:\n%s", out)
	}
}

func TestRunInteractiveAppComposerUpdatesBeforeEnter(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 28, Cols: 100}); err != nil {
		t.Skipf("set pty size: %v", err)
	}

	output := make(chan []byte, 32)
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				output <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				readDone <- err
				close(output)
				return
			}
		}
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWithRunner(
			context.Background(),
			tty,
			tty,
			tty,
			options{SessionDir: t.TempDir(), UI: renderModeApp},
			func(_ context.Context, w io.Writer, opts options) (string, error) {
				fmt.Fprintf(w, "ran prompt %q\n", opts.Prompt)
				return "", nil
			},
		)
	}()

	drainOutputChunks(output, 150*time.Millisecond)
	if _, err := io.WriteString(ptmx, "X"); err != nil {
		t.Fatalf("WriteString(X) error = %v", err)
	}
	typed := ansi.Strip(string(bytes.Join(drainOutputChunks(output, 400*time.Millisecond), nil)))
	if !strings.Contains(typed, "› X") {
		t.Fatalf("composer did not update before Enter; output after X:\n%q", typed)
	}
	if _, err := io.WriteString(ptmx, "\x03"); err != nil {
		t.Fatalf("WriteString(ctrl+c) error = %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runInteractiveWithRunner() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runInteractiveWithRunner() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-readDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("pty read error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pty read timed out")
	}
}

func TestInteractiveAppProgramHandlesExplicitResizeMessages(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 120}); err != nil {
		t.Skipf("set pty size: %v", err)
	}

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newAppProgramModel(ctx, options{SessionDir: t.TempDir(), UI: renderModeApp}, func(_ context.Context, w io.Writer, opts options) (string, error) {
		fmt.Fprintf(w, "ran prompt %q\n", opts.Prompt)
		return "", nil
	})
	model.width = 121
	model.height = 30
	model.resize()
	model.output = tty
	program := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx), tea.WithoutRenderer())
	model.program = program

	runDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		runDone <- err
	}()

	time.Sleep(150 * time.Millisecond)
	for _, size := range []tea.WindowSizeMsg{
		{Width: 58, Height: 18},
		{Width: 34, Height: 36},
		{Width: 132, Height: 32},
		{Width: 34, Height: 36},
		{Width: 72, Height: 20},
		{Width: 118, Height: 28},
	} {
		program.Send(size)
		time.Sleep(40 * time.Millisecond)
	}
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString(/quit) error = %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("program.Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program.Run() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}

	out := output.String()
	for _, want := range []string{"Welcome. Type a task or /help.", "Memax Code", "bye"} {
		if !strings.Contains(out, want) {
			t.Fatalf("explicit resize output missing %q:\n%s", want, out)
		}
	}
	if count := strings.Count(ansi.Strip(out), "Ask Memax Code"); count > 3 {
		t.Fatalf("explicit resize repainted idle composer %d times:\n%s", count, out)
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("explicit resize output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("explicit resize output used full-screen clear:\n%s", out)
	}
}

func drainOutputChunks(output <-chan []byte, d time.Duration) [][]byte {
	timer := time.NewTimer(d)
	defer timer.Stop()
	var chunks [][]byte
	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				return chunks
			}
			chunks = append(chunks, chunk)
		case <-timer.C:
			return chunks
		}
	}
}

func TestRunInteractiveAppKeepsPromptTranscriptInInlineRenderer(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 28, Cols: 100}); err != nil {
		t.Skipf("set pty size: %v", err)
	}

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	promptStarted := make(chan struct{})
	promptDone := make(chan struct{})
	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWithRunner(
			context.Background(),
			tty,
			tty,
			tty,
			options{SessionDir: t.TempDir(), UI: renderModeApp},
			func(_ context.Context, w io.Writer, opts options) (string, error) {
				close(promptStarted)
				fmt.Fprintln(w, "[assistant]")
				fmt.Fprintln(w, "streaming transcript line before resize")
				time.Sleep(250 * time.Millisecond)
				fmt.Fprintln(w, "streaming transcript line after resize")
				close(promptDone)
				return "00000000-0000-7000-8000-000000000456", nil
			},
		)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "resize while running\r"); err != nil {
		t.Fatalf("WriteString(prompt) error = %v", err)
	}
	select {
	case <-promptStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt runner did not start")
	}
	select {
	case <-promptDone:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt runner did not finish")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString(/quit) error = %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runInteractiveWithRunner() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runInteractiveWithRunner() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}

	out := output.String()
	for _, want := range []string{
		"streaming transcript line before resize",
		"streaming transcript line after resize",
		"Memax Code",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("inline active app output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("inline active app output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("inline active app output used full-screen clear:\n%s", out)
	}
}

func TestRunInteractiveAppFallsBackToPlainOutputForNonTerminal(t *testing.T) {
	var stdout bytes.Buffer
	err := runInteractiveAppWithEvents(
		context.Background(),
		strings.NewReader("/quit\n"),
		&stdout,
		options{SessionDir: t.TempDir(), UI: renderModeApp},
		func(_ context.Context, w io.Writer, opts options) (string, error) {
			fmt.Fprintf(w, "ran prompt %q\n", opts.Prompt)
			return "", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("runInteractiveAppWithEvents() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Welcome. Type a task or /help.", "bye"} {
		if !strings.Contains(out, want) {
			t.Fatalf("non-terminal app output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("non-terminal app output contains ANSI escapes:\n%q", out)
	}
}

func TestRunInteractiveAppFlagCapturesPromptRunTranscript(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	var output bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		copyDone <- err
	}()

	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWithRunner(
			context.Background(),
			tty,
			tty,
			tty,
			options{SessionDir: t.TempDir(), UI: renderModeApp},
			func(_ context.Context, w io.Writer, opts options) (string, error) {
				if opts.UI != renderModeTUI {
					return "", fmt.Errorf("prompt runner UI = %q, want %q", opts.UI, renderModeTUI)
				}
				fmt.Fprintln(w, "[assistant]")
				fmt.Fprintln(w, "working on it")
				fmt.Fprintln(w, "[result]")
				fmt.Fprintln(w, "done")
				return "00000000-0000-7000-8000-000000000123", nil
			},
		)
	}()

	if _, err := io.WriteString(ptmx, "first prompt\r"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(ptmx, "/quit\r"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runInteractiveWithRunner() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runInteractiveWithRunner() timed out")
	}
	if err := tty.Close(); err != nil {
		t.Fatalf("tty.Close() error = %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil && !isClosedPTYRead(err) {
			t.Fatalf("Copy() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Copy() timed out")
	}

	out := output.String()
	for _, want := range []string{
		"Welcome. Type a task or /help.",
		"first prompt",
		"working on it",
		"bye",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive app-compat output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[?1049h") {
		t.Fatalf("interactive app-compat output entered alt screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("interactive app output used full-screen clear:\n%s", out)
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

func TestDryRunPrintsInheritedCommandEnvDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEMAX_CODE_INHERIT_COMMAND_ENV", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--dry-run",
		"--provider", "openai",
		"--model", "example-model",
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
