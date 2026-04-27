package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if !strings.Contains(stdout.String(), "docs\tenabled\tparallel\tdocs-server --stdio") {
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
				"supports_parallel_tool_calls": true
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
	if server.Command != "docs-server" || len(server.Args) != 1 || server.Args[0] != "--stdio" || !server.SupportsParallelToolCalls {
		t.Fatalf("server = %#v", server)
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

func TestMemaxCodeMCPServerHelper(t *testing.T) {
	if os.Getenv("MEMAX_CODE_MCP_TEST_SERVER") != "1" {
		return
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
