package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MemaxLabs/memax-go-agent-sdk/mcpbridge"
)

type mcpServerConfig struct {
	Command                   string            `json:"command,omitempty"`
	Args                      []string          `json:"args,omitempty"`
	Env                       map[string]string `json:"env,omitempty"`
	InheritEnv                bool              `json:"inherit_env,omitempty"`
	CWD                       string            `json:"cwd,omitempty"`
	Enabled                   *bool             `json:"enabled,omitempty"`
	SupportsParallelToolCalls bool              `json:"supports_parallel_tool_calls,omitempty"`
	StartupTimeout            string            `json:"startup_timeout,omitempty"`
	ToolTimeout               string            `json:"tool_timeout,omitempty"`
	MaxResultBytes            int               `json:"max_result_bytes,omitempty"`
	MaxRPCMessageBytes        int               `json:"max_rpc_message_bytes,omitempty"`
}

func (c mcpServerConfig) enabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func runMCPCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printMCPUsage(stdout)
		return nil
	}
	switch args[0] {
	case "add":
		return runMCPAdd(args[1:], stdout, stderr)
	case "list":
		return runMCPList(args[1:], stdout, stderr)
	case "get":
		return runMCPGet(args[1:], stdout, stderr)
	case "test":
		return runMCPTest(ctx, args[1:], stdout, stderr)
	case "remove", "rm":
		return runMCPRemove(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printMCPUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown mcp command %q (want add, list, get, test, or remove)", args[0])
	}
}

func printMCPUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: memax-code mcp add NAME [flags] -- COMMAND [ARGS...]")
	fmt.Fprintln(w, "       memax-code mcp list [flags]")
	fmt.Fprintln(w, "       memax-code mcp get NAME [flags]")
	fmt.Fprintln(w, "       memax-code mcp test NAME [flags]")
	fmt.Fprintln(w, "       memax-code mcp remove NAME [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  add      add or update a stdio MCP server")
	fmt.Fprintln(w, "  list     list configured MCP servers")
	fmt.Fprintln(w, "  get      show one MCP server with secrets redacted")
	fmt.Fprintln(w, "  test     start one MCP server and list discovered tools")
	fmt.Fprintln(w, "  remove   remove a configured MCP server")
}

func runMCPAdd(args []string, stdout, stderr io.Writer) error {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("memax-code mcp add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	cwd := fs.String("cwd", "", "working directory for the MCP server process")
	parallel := fs.Bool("parallel", false, "mark this server's tools safe for parallel calls")
	disabled := fs.Bool("disabled", false, "add the server but keep it disabled")
	inheritEnv := fs.Bool("inherit-env", false, "forward the full parent environment to the MCP server; default only passes safe process variables plus --env")
	startupTimeout := fs.String("startup-timeout", "", "startup timeout for initialize/tools/list, such as 30s; empty uses the SDK default")
	toolTimeout := fs.String("tool-timeout", "", "per-tool-call timeout, such as 120s; empty uses the SDK default")
	maxResultBytes := fs.Int("max-result-bytes", 0, "maximum bytes returned per MCP tool result; 0 uses the SDK default")
	maxRPCMessageBytes := fs.Int("max-rpc-message-bytes", 0, "maximum bytes per MCP JSON-RPC message; 0 uses the SDK default")
	envs := newMCPEnvFlag()
	fs.Var(envs, "env", "environment variable KEY=VALUE for the MCP server; repeatable")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code mcp add NAME [flags] -- COMMAND [ARGS...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name = strings.TrimSpace(rest[0])
		rest = rest[1:]
	}
	if name == "" || len(rest) < 1 {
		return fmt.Errorf("mcp add requires NAME and COMMAND")
	}
	command := strings.TrimSpace(rest[0])
	if command == "" {
		return fmt.Errorf("mcp add requires COMMAND")
	}
	startupTimeoutValue, err := normalizeMCPDurationFlag("startup-timeout", *startupTimeout)
	if err != nil {
		return err
	}
	toolTimeoutValue, err := normalizeMCPDurationFlag("tool-timeout", *toolTimeout)
	if err != nil {
		return err
	}
	if err := validateMCPByteLimitFlag("max-result-bytes", *maxResultBytes); err != nil {
		return err
	}
	if err := validateMCPByteLimitFlag("max-rpc-message-bytes", *maxRPCMessageBytes); err != nil {
		return err
	}
	configPath, cfg, err := loadWritableConfig(*configRaw)
	if err != nil {
		return err
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]mcpServerConfig{}
	}
	enabled := !*disabled
	cfg.MCPServers[name] = mcpServerConfig{
		Command:                   command,
		Args:                      append([]string(nil), rest[1:]...),
		Env:                       cloneStringMap(envs.values),
		InheritEnv:                *inheritEnv,
		CWD:                       strings.TrimSpace(*cwd),
		Enabled:                   boolPtr(enabled),
		SupportsParallelToolCalls: *parallel,
		StartupTimeout:            startupTimeoutValue,
		ToolTimeout:               toolTimeoutValue,
		MaxResultBytes:            *maxResultBytes,
		MaxRPCMessageBytes:        *maxRPCMessageBytes,
	}
	if err := writeConfigFile(configPath, cfg, true); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added MCP server %q.\n", name)
	return nil
}

func runMCPList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("memax-code mcp list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code mcp list [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("mcp list does not accept positional arguments")
	}
	configPath, err := resolvePath(*configRaw)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	cfg, loaded, err := loadConfig(configPath, mcpConfigExplicit(fs))
	if err != nil {
		return err
	}
	if !loaded || len(cfg.MCPServers) == 0 {
		fmt.Fprintln(stdout, "no MCP servers")
		return nil
	}
	for _, name := range sortedMapKeysMCP(cfg.MCPServers) {
		server := cfg.MCPServers[name]
		redacted := redactMCPServerConfig(server)
		status := "enabled"
		if !server.enabled() {
			status = "disabled"
		}
		parallel := "serial"
		if server.SupportsParallelToolCalls {
			parallel = "parallel"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s", name, status, parallel, redacted.Command)
		if len(redacted.Args) > 0 {
			fmt.Fprintf(stdout, " %s", strings.Join(redacted.Args, " "))
		}
		if suffix := redacted.runtimeSummary(); suffix != "" {
			fmt.Fprintf(stdout, "\t%s", suffix)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func runMCPGet(args []string, stdout, stderr io.Writer) error {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("memax-code mcp get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	jsonOutput := fs.Bool("json", false, "print redacted server configuration as JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code mcp get NAME [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name = strings.TrimSpace(rest[0])
		rest = rest[1:]
	}
	if name == "" || len(rest) > 0 {
		return fmt.Errorf("mcp get requires exactly one NAME")
	}
	server, ok, err := loadMCPServer(*configRaw, name, mcpConfigExplicit(fs))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no MCP server named %q found", name)
	}
	redacted := redactMCPServerConfig(server)
	if *jsonOutput {
		return writeMCPJSON(stdout, redacted)
	}
	fmt.Fprintf(stdout, "name: %s\n", name)
	fmt.Fprintf(stdout, "enabled: %t\n", server.enabled())
	fmt.Fprintf(stdout, "command: %s\n", redacted.Command)
	if len(redacted.Args) > 0 {
		fmt.Fprintf(stdout, "args: %s\n", strings.Join(redacted.Args, " "))
	}
	if redacted.CWD != "" {
		fmt.Fprintf(stdout, "cwd: %s\n", redacted.CWD)
	}
	if len(redacted.Env) > 0 {
		fmt.Fprintln(stdout, "env:")
		for _, key := range sortedStringKeys(redacted.Env) {
			fmt.Fprintf(stdout, "  %s=%s\n", key, redacted.Env[key])
		}
	}
	if redacted.InheritEnv {
		fmt.Fprintln(stdout, "inherit_env: true")
	}
	if redacted.SupportsParallelToolCalls {
		fmt.Fprintln(stdout, "supports_parallel_tool_calls: true")
	}
	if suffix := redacted.runtimeSummary(); suffix != "" {
		fmt.Fprintf(stdout, "bounds: %s\n", suffix)
	}
	return nil
}

func runMCPTest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("memax-code mcp test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	jsonOutput := fs.Bool("json", false, "print diagnostics as JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code mcp test NAME [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name = strings.TrimSpace(rest[0])
		rest = rest[1:]
	}
	if name == "" || len(rest) > 0 {
		return fmt.Errorf("mcp test requires exactly one NAME")
	}
	server, ok, err := loadMCPServer(*configRaw, name, mcpConfigExplicit(fs))
	if err != nil {
		if *jsonOutput {
			if writeErr := writeMCPDiagnosticError(stdout, name, err); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if !ok {
		err := fmt.Errorf("no MCP server named %q found", name)
		if *jsonOutput {
			if writeErr := writeMCPDiagnosticError(stdout, name, err); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	cfg, err := mcpBridgeServerConfig(name, server)
	if err != nil {
		if *jsonOutput {
			if writeErr := writeMCPDiagnosticError(stdout, name, err); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	redacted := redactMCPServerConfig(server)
	diagnostic := mcpTestDiagnostic{
		Name:       name,
		Enabled:    server.enabled(),
		Command:    redacted.Command,
		Args:       append([]string(nil), redacted.Args...),
		InheritEnv: server.InheritEnv,
		Parallel:   server.SupportsParallelToolCalls,
	}
	client, err := mcpbridge.NewStdioClient(ctx, cfg)
	if err != nil {
		diagnostic.OK = false
		diagnostic.Error = err.Error()
		if writeErr := writeMCPTestDiagnostic(stdout, diagnostic, *jsonOutput); writeErr != nil {
			return writeErr
		}
		return fmt.Errorf("mcp server %q test failed: %s", name, diagnostic.Error)
	}
	defer client.Close()
	set, err := mcpbridge.DiscoverTools(ctx, client, cfg)
	if err != nil {
		diagnostic.OK = false
		diagnostic.Error = err.Error()
		if writeErr := writeMCPTestDiagnostic(stdout, diagnostic, *jsonOutput); writeErr != nil {
			return writeErr
		}
		return fmt.Errorf("mcp server %q test failed: %s", name, diagnostic.Error)
	}
	diagnostic.OK = true
	for _, discovered := range set.Tools() {
		spec := discovered.Spec()
		diagnostic.Tools = append(diagnostic.Tools, mcpTestToolDiagnostic{
			Name:            spec.Name,
			Description:     spec.Description,
			ReadOnly:        spec.ReadOnly,
			Destructive:     spec.Destructive,
			ConcurrencySafe: spec.ConcurrencySafe,
		})
	}
	return writeMCPTestDiagnostic(stdout, diagnostic, *jsonOutput)
}

type mcpTestDiagnostic struct {
	Name       string                  `json:"name"`
	OK         bool                    `json:"ok"`
	Enabled    bool                    `json:"enabled"`
	Command    string                  `json:"command"`
	Args       []string                `json:"args,omitempty"`
	InheritEnv bool                    `json:"inherit_env,omitempty"`
	Parallel   bool                    `json:"parallel,omitempty"`
	Error      string                  `json:"error,omitempty"`
	Tools      []mcpTestToolDiagnostic `json:"tools,omitempty"`
}

type mcpTestToolDiagnostic struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	Destructive     bool   `json:"destructive,omitempty"`
	ConcurrencySafe bool   `json:"concurrency_safe,omitempty"`
}

func writeMCPTestDiagnostic(stdout io.Writer, diagnostic mcpTestDiagnostic, jsonOutput bool) error {
	if jsonOutput {
		return writeMCPJSON(stdout, diagnostic)
	}
	if diagnostic.OK {
		if !diagnostic.Enabled {
			fmt.Fprintln(stdout, "[warn] server is disabled in config; test started it anyway")
		}
		if diagnostic.InheritEnv {
			fmt.Fprintln(stdout, "[warn] inherit_env=true forwards the full parent process environment")
		}
		if diagnostic.Parallel {
			fmt.Fprintln(stdout, "[info] server tools are configured as parallel-allowed")
		}
		fmt.Fprintf(stdout, "[ok] MCP server %q started and returned %d tool(s).\n", diagnostic.Name, len(diagnostic.Tools))
	} else {
		fmt.Fprintf(stdout, "[error] MCP server %q failed: %s\n", diagnostic.Name, diagnostic.Error)
		return nil
	}
	for _, discovered := range diagnostic.Tools {
		var flags []string
		if discovered.ReadOnly {
			flags = append(flags, "read-only")
		}
		if discovered.Destructive {
			flags = append(flags, "destructive")
		}
		suffix := ""
		if len(flags) > 0 {
			suffix = " [" + strings.Join(flags, ", ") + "]"
		}
		fmt.Fprintf(stdout, "tool: %s%s\n", discovered.Name, suffix)
		if discovered.Description != "" {
			fmt.Fprintf(stdout, "  %s\n", discovered.Description)
		}
	}
	return nil
}

func writeMCPDiagnosticError(stdout io.Writer, name string, err error) error {
	return writeMCPJSON(stdout, mcpTestDiagnostic{
		Name:  name,
		OK:    false,
		Error: err.Error(),
	})
}

func normalizeMCPDurationFlag(flagName, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("--%s must be a Go duration like 30s or 2m: %w", flagName, err)
	}
	if duration < 0 {
		return "", fmt.Errorf("--%s must be non-negative", flagName)
	}
	return value, nil
}

func validateMCPByteLimitFlag(flagName string, value int) error {
	if value < 0 {
		return fmt.Errorf("--%s must be non-negative", flagName)
	}
	return nil
}

func writeMCPJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func mcpConfigExplicit(fs *flag.FlagSet) bool {
	return flagWasSet(fs, "config") || strings.TrimSpace(os.Getenv("MEMAX_CODE_CONFIG")) != ""
}

func loadMCPServer(rawConfigPath, name string, explicit bool) (mcpServerConfig, bool, error) {
	configPath, err := resolvePath(rawConfigPath)
	if err != nil {
		return mcpServerConfig{}, false, fmt.Errorf("resolve config path: %w", err)
	}
	cfg, loaded, err := loadConfig(configPath, explicit)
	if err != nil {
		return mcpServerConfig{}, false, err
	}
	if !loaded || len(cfg.MCPServers) == 0 {
		return mcpServerConfig{}, false, nil
	}
	server, ok := cfg.MCPServers[name]
	return server, ok, nil
}

func redactMCPServerConfig(server mcpServerConfig) mcpServerConfig {
	out := server
	out.Command = redactMCPDisplayValue(server.Command)
	out.Args = redactMCPArgs(server.Args)
	out.CWD = redactMCPDisplayValue(server.CWD)
	out.Env = map[string]string{}
	for key := range server.Env {
		out.Env[key] = "<redacted>"
	}
	if len(out.Env) == 0 {
		out.Env = nil
	}
	enabled := server.enabled()
	out.Enabled = &enabled
	return out
}

func redactMCPArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	redactNext := false
	for i, arg := range args {
		if redactNext {
			out[i] = "<redacted>"
			redactNext = false
			continue
		}
		key, _, hasValue := strings.Cut(arg, "=")
		if hasValue && isMCPSecretFlag(key) {
			out[i] = key + "=<redacted>"
			continue
		}
		if isMCPSecretFlag(arg) {
			out[i] = arg
			redactNext = true
			continue
		}
		out[i] = redactMCPDisplayValue(arg)
	}
	return out
}

func redactMCPDisplayValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	if redacted, ok := redactMCPURLUserinfo(trimmed); ok {
		return redacted
	}
	if looksLikeMCPSecretValue(trimmed) {
		return "<redacted>"
	}
	return value
}

func redactMCPURLUserinfo(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil || parsed.Scheme == "" || parsed.Host == "" {
		return value, false
	}
	parsed.User = url.User("redacted")
	return parsed.String(), true
}

func isMCPSecretFlag(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "-") {
		return false
	}
	value = strings.TrimLeft(strings.ToLower(strings.TrimSpace(value)), "-")
	value = strings.ReplaceAll(value, "_", "-")
	for _, marker := range []string{
		"api-key",
		"apikey",
		"auth",
		"authorization",
		"bearer",
		"client-secret",
		"credential",
		"password",
		"passwd",
		"private-key",
		"secret",
		"token",
	} {
		if value == marker || strings.HasSuffix(value, "-"+marker) {
			return true
		}
	}
	return false
}

func looksLikeMCPSecretValue(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"sk-",
		"sk_or_",
		"ghp_",
		"github_pat_",
		"glpat-",
		"xoxb-",
		"xoxp-",
		"ya29.",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if len(value) >= 20 && strings.HasPrefix(value, "AKIA") {
		return true
	}
	return false
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c mcpServerConfig) runtimeSummary() string {
	var parts []string
	if c.InheritEnv {
		parts = append(parts, "inherit_env=true")
	}
	if c.StartupTimeout != "" {
		parts = append(parts, "startup_timeout="+c.StartupTimeout)
	}
	if c.ToolTimeout != "" {
		parts = append(parts, "tool_timeout="+c.ToolTimeout)
	}
	if c.MaxResultBytes != 0 {
		parts = append(parts, fmt.Sprintf("max_result_bytes=%d", c.MaxResultBytes))
	}
	if c.MaxRPCMessageBytes != 0 {
		parts = append(parts, fmt.Sprintf("max_rpc_message_bytes=%d", c.MaxRPCMessageBytes))
	}
	return strings.Join(parts, " ")
}

func runMCPRemove(args []string, stdout, stderr io.Writer) error {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("memax-code mcp remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code mcp remove NAME [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name = strings.TrimSpace(rest[0])
		rest = rest[1:]
	}
	if name == "" || len(rest) > 0 {
		return fmt.Errorf("mcp remove requires exactly one NAME")
	}
	configPath, cfg, err := loadWritableConfig(*configRaw)
	if err != nil {
		return err
	}
	if len(cfg.MCPServers) == 0 {
		fmt.Fprintf(stdout, "No MCP server named %q found.\n", name)
		return nil
	}
	if _, ok := cfg.MCPServers[name]; !ok {
		fmt.Fprintf(stdout, "No MCP server named %q found.\n", name)
		return nil
	}
	delete(cfg.MCPServers, name)
	if len(cfg.MCPServers) == 0 {
		cfg.MCPServers = nil
	}
	if err := writeConfigFile(configPath, cfg, true); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed MCP server %q.\n", name)
	return nil
}

func loadWritableConfig(rawPath string) (string, fileConfig, error) {
	configPath, err := resolvePath(rawPath)
	if err != nil {
		return "", fileConfig{}, fmt.Errorf("resolve config path: %w", err)
	}
	cfg, loaded, err := loadConfig(configPath, false)
	if err != nil {
		return "", fileConfig{}, err
	}
	if !loaded {
		cfg = fileConfig{}
	}
	return configPath, cfg, nil
}

type mcpEnvFlag struct {
	values map[string]string
}

func newMCPEnvFlag() *mcpEnvFlag {
	return &mcpEnvFlag{values: map[string]string{}}
}

func (f *mcpEnvFlag) String() string {
	encoded, _ := json.Marshal(f.values)
	return string(encoded)
}

func (f *mcpEnvFlag) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return fmt.Errorf("mcp env must be KEY=VALUE")
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	if _, exists := f.values[key]; exists {
		return fmt.Errorf("duplicate mcp env %q", key)
	}
	f.values[key] = value
	return nil
}

func cloneMCPServers(in map[string]mcpServerConfig) map[string]mcpServerConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]mcpServerConfig, len(in))
	for name, server := range in {
		server.Args = append([]string(nil), server.Args...)
		server.Env = cloneStringMap(server.Env)
		if server.Enabled != nil {
			enabled := *server.Enabled
			server.Enabled = &enabled
		}
		out[name] = server
	}
	return out
}

func sortedMapKeysMCP(m map[string]mcpServerConfig) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
