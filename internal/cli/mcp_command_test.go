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
		"--startup-timeout", "45s",
		"--tool-timeout", "2m",
		"--max-result-bytes", "32768",
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
		`"supports_parallel_tool_calls": true`,
		`"startup_timeout": "45s"`,
		`"tool_timeout": "2m"`,
		`"max_result_bytes": 32768`,
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
	if !strings.Contains(stdout.String(), "docs\tenabled\tparallel\tdocs-server --stdio\tstartup_timeout=45s tool_timeout=2m max_result_bytes=32768") {
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
				"max_result_bytes": 32768
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
		server.StartupTimeout != "45s" || server.ToolTimeout != "2m" || server.MaxResultBytes != 32768 {
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
