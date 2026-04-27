package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPAddListRemoveUpdatesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"mcp", "add", "docs",
		"--config", configPath,
		"--env", "TOKEN=abc",
		"--parallel",
		"--inherit-env",
		"--startup-timeout", "45s",
		"--tool-timeout", "2m",
		"--max-result-bytes", "32768",
		"--max-rpc-message-bytes", "1048576",
		"--",
		"docs-server", "--stdio",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp add error = %v", err)
	}
	if !strings.Contains(stdout.String(), `Added MCP server "docs".`) {
		t.Fatalf("add stdout = %q", stdout.String())
	}

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, want := range []string{
		`"mcp_servers": {`,
		`"docs": {`,
		`"command": "docs-server"`,
		`"--stdio"`,
		`"TOKEN": "abc"`,
		`"inherit_env": true`,
		`"supports_parallel_tool_calls": true`,
		`"startup_timeout": "45s"`,
		`"tool_timeout": "2m"`,
		`"max_result_bytes": 32768`,
		`"max_rpc_message_bytes": 1048576`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("config missing %q:\n%s", want, body)
		}
	}

	stdout.Reset()
	err = Run(context.Background(), []string{"mcp", "list", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp list error = %v", err)
	}
	if !strings.Contains(stdout.String(), "docs\tenabled\tparallel\tdocs-server --stdio\tinherit_env=true startup_timeout=45s tool_timeout=2m max_result_bytes=32768 max_rpc_message_bytes=1048576") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	err = Run(context.Background(), []string{"mcp", "remove", "docs", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp remove error = %v", err)
	}
	if !strings.Contains(stdout.String(), `Removed MCP server "docs".`) {
		t.Fatalf("remove stdout = %q", stdout.String())
	}
	body, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(body), "mcp_servers") {
		t.Fatalf("remove left mcp servers:\n%s", body)
	}
}

func TestMCPGetRedactsEnvironment(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"mcp_servers": {
			"docs": {
				"command": "docs-server",
				"args": ["--stdio"],
				"env": {"DOCS_TOKEN": "secret-token", "PUBLIC_HINT": "also-hidden"},
				"inherit_env": true,
				"startup_timeout": "45s",
				"tool_timeout": "2m",
				"max_result_bytes": 32768,
				"max_rpc_message_bytes": 1048576
			}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"mcp", "get", "docs", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp get error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"name: docs",
		"enabled: true",
		"command: docs-server",
		"args: --stdio",
		"DOCS_TOKEN=<redacted>",
		"PUBLIC_HINT=<redacted>",
		"inherit_env: true",
		"bounds: inherit_env=true startup_timeout=45s tool_timeout=2m max_result_bytes=32768 max_rpc_message_bytes=1048576",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mcp get output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "also-hidden") {
		t.Fatalf("mcp get leaked env value:\n%s", out)
	}

	stdout.Reset()
	err = Run(context.Background(), []string{"mcp", "get", "docs", "--config", configPath, "--json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp get --json error = %v", err)
	}
	if strings.Contains(stdout.String(), "secret-token") || !strings.Contains(stdout.String(), `"<redacted>"`) {
		t.Fatalf("mcp get --json redaction output:\n%s", stdout.String())
	}
}

func TestMCPTestStartsServerAndListsTools(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := fmt.Sprintf(`{
		"mcp_servers": {
			"docs": {
				"command": %q,
				"args": ["-test.run=TestMemaxCodeMCPServerHelper", "--"],
				"env": {"MEMAX_CODE_MCP_TEST_SERVER": "1"},
				"supports_parallel_tool_calls": true
			}
		}
	}`, os.Args[0])
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"mcp", "test", "docs", "--config", configPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp test error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		`[ok] MCP server "docs" started and returned 1 tool(s).`,
		"tool: mcp__docs__lookup [read-only, parallel]",
		"Lookup docs.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mcp test output missing %q:\n%s", want, out)
		}
	}

	stdout.Reset()
	err = Run(context.Background(), []string{"mcp", "test", "docs", "--config", configPath, "--json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("mcp test --json error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"ok": true`) || !strings.Contains(stdout.String(), `"name": "mcp__docs__lookup"`) {
		t.Fatalf("mcp test --json output:\n%s", stdout.String())
	}
}

func TestMCPTestReportsStartupFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"mcp_servers": {
			"broken": {"command": "definitely-not-a-memax-code-test-command"}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"mcp", "test", "broken", "--config", configPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `mcp server "broken" test failed`) {
		t.Fatalf("mcp test error = %v, want failure", err)
	}
	if out := stdout.String(); !strings.Contains(out, `[error] MCP server "broken" failed:`) {
		t.Fatalf("mcp test failure output:\n%s", out)
	}
}

func TestParseArgsLoadsMCPServersFromConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"provider": "openai",
		"mcp_servers": {
			"docs": {
				"command": "docs-server",
				"args": ["--stdio"],
				"supports_parallel_tool_calls": true,
				"startup_timeout": "45s",
				"tool_timeout": "2m",
				"max_result_bytes": 32768,
				"max_rpc_message_bytes": 1048576,
				"inherit_env": true
			}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"--dry-run", "--config", configPath}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	server, ok := opts.MCPServers["docs"]
	if !ok {
		t.Fatalf("MCPServers missing docs: %#v", opts.MCPServers)
	}
	if server.Command != "docs-server" || len(server.Args) != 1 || server.Args[0] != "--stdio" || !server.SupportsParallelToolCalls ||
		server.StartupTimeout != "45s" || server.ToolTimeout != "2m" || server.MaxResultBytes != 32768 ||
		server.MaxRPCMessageBytes != 1048576 || !server.InheritEnv {
		t.Fatalf("server = %#v", server)
	}
}

func TestMCPAddRejectsInvalidTimeout(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"mcp", "add", "docs",
		"--config", configPath,
		"--startup-timeout", "soon",
		"--",
		"docs-server",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--startup-timeout must be a Go duration") {
		t.Fatalf("error = %v", err)
	}
}

func TestMCPAddRejectsNegativeTimeoutAndByteLimits(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative startup timeout",
			args: []string{"mcp", "add", "docs", "--config", configPath, "--startup-timeout", "-1s", "--", "docs-server"},
			want: "--startup-timeout must be non-negative",
		},
		{
			name: "negative result bytes",
			args: []string{"mcp", "add", "docs", "--config", configPath, "--max-result-bytes", "-1", "--", "docs-server"},
			want: "--max-result-bytes must be non-negative",
		},
		{
			name: "negative rpc bytes",
			args: []string{"mcp", "add", "docs", "--config", configPath, "--max-rpc-message-bytes", "-1", "--", "docs-server"},
			want: "--max-rpc-message-bytes must be non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tt.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMCPBridgeServerConfigRejectsNegativeConfigValues(t *testing.T) {
	tests := []struct {
		name   string
		server mcpServerConfig
		want   string
	}{
		{
			name: "negative config timeout",
			server: mcpServerConfig{
				Command:        "docs-server",
				StartupTimeout: "-1s",
			},
			want: "startup_timeout must be non-negative",
		},
		{
			name: "negative result bytes",
			server: mcpServerConfig{
				Command:        "docs-server",
				MaxResultBytes: -1,
			},
			want: "max_result_bytes must be non-negative",
		},
		{
			name: "negative rpc bytes",
			server: mcpServerConfig{
				Command:            "docs-server",
				MaxRPCMessageBytes: -1,
			},
			want: "max_rpc_message_bytes must be non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mcpBridgeServerConfig("docs", tt.server)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInspectToolsIncludesConfiguredMCPTools(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := fmt.Sprintf(`{
		"provider": "openai",
		"mcp_servers": {
			"docs": {
				"command": %q,
				"args": ["-test.run=TestMemaxCodeMCPServerHelper", "--"],
				"env": {"MEMAX_CODE_MCP_TEST_SERVER": "1"}
			}
		}
	}`, os.Args[0])
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--config", configPath, "--inspect-tools"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("inspect tools error = %v", err)
	}
	if !strings.Contains(stdout.String(), "tool: mcp__docs__lookup\n") {
		t.Fatalf("inspect tools missing MCP tool:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "description: Lookup docs.") {
		t.Fatalf("inspect tools missing MCP description:\n%s", stdout.String())
	}
}

func TestRunInteractivePreloadsMCPServersOnce(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "mcp-starts.txt")
	opts := options{
		SessionDir: t.TempDir(),
		UI:         renderModePlain,
		MCPServers: map[string]mcpServerConfig{
			"docs": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestMemaxCodeMCPServerHelper", "--"},
				Env: map[string]string{
					"MEMAX_CODE_MCP_TEST_SERVER":  "1",
					"MEMAX_CODE_MCP_TEST_COUNTER": counterPath,
				},
			},
		},
	}

	var stdout, stderr bytes.Buffer
	var prompts []string
	err := runInteractiveWithRunner(
		context.Background(),
		strings.NewReader("first\nsecond\n/quit\n"),
		&stdout,
		&stderr,
		opts,
		func(_ context.Context, _ io.Writer, opts options) (string, error) {
			if !opts.RuntimeMCPReady {
				t.Fatal("RuntimeMCPReady = false, want preloaded MCP tools")
			}
			if len(opts.RuntimeMCPTools) != 1 {
				t.Fatalf("RuntimeMCPTools = %d, want 1", len(opts.RuntimeMCPTools))
			}
			prompts = append(prompts, opts.Prompt)
			return fmt.Sprintf("00000000-0000-7000-8000-%012d", len(prompts)), nil
		},
	)
	if err != nil {
		t.Fatalf("runInteractiveWithRunner() error = %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts = %#v, want two prompts", prompts)
	}
	body, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if starts := strings.Count(string(body), "start\n"); starts != 1 {
		t.Fatalf("MCP server starts = %d, want 1; counter:\n%s", starts, body)
	}
}

func TestMemaxCodeMCPServerHelper(t *testing.T) {
	if os.Getenv("MEMAX_CODE_MCP_TEST_SERVER") != "1" {
		return
	}
	if counterPath := os.Getenv("MEMAX_CODE_MCP_TEST_COUNTER"); counterPath != "" {
		file, err := os.OpenFile(counterPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString("start\n")
			_ = file.Close()
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     int64  `json:"id,omitempty"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.ID == 0 {
			continue
		}
		switch req.Method {
		case "initialize":
			writeMCPTestResult(encoder, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "docs"},
			})
		case "tools/list":
			writeMCPTestResult(encoder, req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "lookup",
					"description": "Lookup docs.",
					"annotations": map[string]any{"readOnlyHint": true},
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"query": map[string]any{"type": "string"}},
					},
				}},
			})
		case "tools/call":
			writeMCPTestResult(encoder, req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok"}},
			})
		}
	}
	os.Exit(0)
}

func writeMCPTestResult(encoder *json.Encoder, id int64, result any) {
	_ = encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}
