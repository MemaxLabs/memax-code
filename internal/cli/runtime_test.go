package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/session"
	"github.com/MemaxLabs/memax-go-agent-sdk/tool"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/commandtools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/subagents"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/tasktools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/verifytools"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/workspacetools"
)

func TestVerificationArgvRejectsUnknownCheck(t *testing.T) {
	_, err := verificationArgv(verifytools.Request{Name: "lint"})
	if err == nil || !strings.Contains(err.Error(), `unsupported verification "lint"`) {
		t.Fatalf("verificationArgv() error = %v, want unsupported verification", err)
	}
}

func TestVerificationArgvRejectsFlagTargets(t *testing.T) {
	_, err := verificationArgv(verifytools.Request{Name: "test", Target: "-exec=/tmp/run"})
	if err == nil || !strings.Contains(err.Error(), "target must be a package path") {
		t.Fatalf("verificationArgv() error = %v, want flag target rejection", err)
	}
}

func TestVerificationArgvRejectsMultiArgTargets(t *testing.T) {
	_, err := verificationArgv(verifytools.Request{Name: "vet", Target: "./... -x"})
	if err == nil || !strings.Contains(err.Error(), "one package path") {
		t.Fatalf("verificationArgv() error = %v, want multi-arg target rejection", err)
	}
}

func TestCustomVerificationCommandUsesConfiguredShellCommand(t *testing.T) {
	command, argv, err := verificationCommand(verifytools.Request{Name: "lint"}, map[string]string{
		"lint": "npm run lint",
	}, false)
	if err != nil {
		t.Fatalf("verificationCommand() error = %v", err)
	}
	if command != "npm run lint" {
		t.Fatalf("command = %q, want configured command", command)
	}
	if len(argv) < 3 || argv[len(argv)-1] != command {
		t.Fatalf("argv = %#v, want shell argv for configured command", argv)
	}
}

func TestCustomVerificationCommandSubstitutesQuotedTarget(t *testing.T) {
	command, _, err := verificationCommand(verifytools.Request{Name: "test", Target: "./pkg/api"}, map[string]string{
		"test": "npm test -- {target}",
	}, false)
	if err != nil {
		t.Fatalf("verificationCommand() error = %v", err)
	}
	if command != "npm test -- './pkg/api'" {
		t.Fatalf("command = %q, want shell-quoted target", command)
	}
}

func TestCustomVerificationCommandRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{"-rf", "./pkg with spaces", "$(whoami)"} {
		t.Run(target, func(t *testing.T) {
			_, _, err := verificationCommand(verifytools.Request{Name: "test", Target: target}, map[string]string{
				"test": "npm test -- {target}",
			}, false)
			if err == nil || !strings.Contains(err.Error(), "invalid verification target") {
				t.Fatalf("verificationCommand() error = %v, want invalid target", err)
			}
		})
	}
}

func TestCustomVerificationCommandUsesGoFallbackForMissingChecks(t *testing.T) {
	command, argv, err := verificationCommand(verifytools.Request{Name: "test"}, map[string]string{
		"lint": "golangci-lint run",
	}, true)
	if err != nil {
		t.Fatalf("verificationCommand() error = %v", err)
	}
	if command != "go test ./..." || strings.Join(argv, " ") != "go test ./..." {
		t.Fatalf("command = %q argv = %#v, want Go fallback test", command, argv)
	}
}

func TestShellQuoteForGOOS(t *testing.T) {
	if got := shellQuoteForGOOS("linux", "./pkg/api"); got != "'./pkg/api'" {
		t.Fatalf("linux quote = %q", got)
	}
	if got := shellQuoteForGOOS("windows", "./pkg/api"); got != `"./pkg/api"` {
		t.Fatalf("windows quote = %q", got)
	}
}

func TestCustomVerificationCommandRejectsTargetWithoutPlaceholder(t *testing.T) {
	_, _, err := verificationCommand(verifytools.Request{Name: "lint", Target: "pkg"}, map[string]string{
		"lint": "npm run lint",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "include {target}") {
		t.Fatalf("verificationCommand() error = %v, want target placeholder error", err)
	}
}

func TestCustomVerificationCommandReportsConfiguredChecks(t *testing.T) {
	_, _, err := verificationCommand(verifytools.Request{Name: "typecheck"}, map[string]string{
		"lint": "npm run lint",
		"test": "npm test",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured checks: lint, test") {
		t.Fatalf("verificationCommand() error = %v, want configured checks", err)
	}
}

func TestTailBytesCapsOutput(t *testing.T) {
	got := tailBytes("0123456789", 4)
	if got != "... output truncated ...\n6789" {
		t.Fatalf("tailBytes() = %q", got)
	}
}

func TestSanitizeTranscriptTextStripsANSISequences(t *testing.T) {
	got := sanitizeTranscriptText("hello \x1b[31mred\x1b[0m\tok\r\n\x00done")
	want := "hello red\tok\ndone"
	if got != want {
		t.Fatalf("sanitizeTranscriptText() = %q, want %q", got, want)
	}
	for _, fragment := range []string{"[31m", "[0m"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("sanitizeTranscriptText() leaked ANSI fragment %q in %q", fragment, got)
		}
	}
}

func TestBuildStackEnablesCustomVerificationOutsideGoModule(t *testing.T) {
	stack, err := buildStack(options{
		CWD:            t.TempDir(),
		Preset:         "interactive_dev",
		SessionDir:     t.TempDir(),
		VerifyCommands: map[string]string{"test": "npm test"},
	})
	if err != nil {
		t.Fatalf("buildStack() error = %v", err)
	}
	if _, ok := toolSpec(stack.Registry(), verifytools.ToolName); !ok {
		t.Fatalf("registry missing %q with custom verification configured", verifytools.ToolName)
	}
}

func TestBuildStackUsesModelFriendlyToolContracts(t *testing.T) {
	stack, err := buildStack(options{
		CWD:        t.TempDir(),
		Preset:     "interactive_dev",
		SessionDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildStack() error = %v", err)
	}

	spec, ok := toolSpec(stack.Registry(), workspacetools.ApplyPatchToolName)
	if !ok {
		t.Fatalf("registry missing %q", workspacetools.ApplyPatchToolName)
	}
	properties := schemaProperties(t, spec)
	if _, ok := properties["unified_diff"]; !ok {
		t.Fatalf("patch input schema = %#v, want unified_diff property", spec.InputSchema)
	}
	if _, ok := properties["operations"]; ok {
		t.Fatalf("patch input schema = %#v, did not want operations property", spec.InputSchema)
	}

	spec, ok = toolSpec(stack.Registry(), commandtools.ToolName)
	if !ok {
		t.Fatalf("registry missing %q", commandtools.ToolName)
	}
	properties = schemaProperties(t, spec)
	command, ok := properties["command"].(map[string]any)
	if !ok {
		t.Fatalf("command input schema = %#v, want command object", spec.InputSchema)
	}
	if command["type"] != "string" {
		t.Fatalf("run command schema = %#v, want shell command string", spec.InputSchema)
	}
	if _, ok := properties["argv"]; ok {
		t.Fatalf("run command schema = %#v, did not want argv property", spec.InputSchema)
	}

	spec, ok = toolSpec(stack.Registry(), commandtools.StartToolName)
	if !ok {
		t.Fatalf("registry missing %q", commandtools.StartToolName)
	}
	properties = schemaProperties(t, spec)
	command, ok = properties["command"].(map[string]any)
	if !ok {
		t.Fatalf("start command input schema = %#v, want command object", spec.InputSchema)
	}
	if command["type"] != "string" {
		t.Fatalf("start command schema = %#v, want shell command string", spec.InputSchema)
	}
	if command["type"] == "array" {
		t.Fatalf("start command schema = %#v, did not want argv array", spec.InputSchema)
	}

	prompt := stack.Options().AppendSystemPrompt
	for _, want := range []string{
		"Use managed command sessions when continuous feedback helps",
		"CLI tool contract:",
		"Use run_command with command as one shell command string",
		"Use start_command with command as one shell command string",
		"Use workspace_apply_patch with exactly one unified_diff string",
		"Do not provide structured patch operations",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("AppendSystemPrompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildStackRegistersCLIManagedSubagents(t *testing.T) {
	stack, err := buildStack(options{
		CWD:        t.TempDir(),
		Preset:     "interactive_dev",
		SessionDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildStack() error = %v", err)
	}

	spec, ok := toolSpec(stack.Registry(), subagents.ToolName)
	if !ok {
		t.Fatalf("registry missing %q", subagents.ToolName)
	}
	if !spec.ConcurrencySafe {
		t.Fatalf("%s should be concurrency-safe for parallel bounded delegation", spec.Name)
	}
	if !spec.Destructive {
		t.Fatalf("%s should be marked destructive because worker subagents can edit", spec.Name)
	}
	if spec.MaxResultBytes != maxSubagentResultBytes {
		t.Fatalf("MaxResultBytes = %d, want %d", spec.MaxResultBytes, maxSubagentResultBytes)
	}

	properties := schemaProperties(t, spec)
	agent, ok := properties["agent"].(map[string]any)
	if !ok {
		t.Fatalf("subagent schema agent = %#v, want object", properties["agent"])
	}
	values, ok := agent["enum"].([]any)
	if !ok {
		t.Fatalf("subagent schema agent enum = %#v, want enum", agent["enum"])
	}
	for _, want := range []string{"explorer", "reviewer", "worker"} {
		if !containsAnyString(values, want) {
			t.Fatalf("subagent enum = %#v, missing %q", values, want)
		}
	}

	prompt := stack.Options().AppendSystemPrompt
	for _, want := range []string{
		"Subagent delegation:",
		"Use run_subagent for bounded parallel work",
		"Use explorer only for read-only repository inspection",
		"It cannot run shell commands",
		"reviewer for code-review risk checks",
		"Use worker for isolated implementation, shell commands, network checks through shell",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("AppendSystemPrompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildStackSubagentExecutesChildSession(t *testing.T) {
	client := &scriptedTextClient{text: "child result"}
	sessionDir := t.TempDir()
	stack, err := buildStackWithModel(options{
		CWD:        t.TempDir(),
		Preset:     "interactive_dev",
		SessionDir: sessionDir,
	}, client)
	if err != nil {
		t.Fatalf("buildStackWithModel() error = %v", err)
	}
	delegate, ok := stack.Registry().Get(subagents.ToolName)
	if !ok {
		t.Fatalf("registry missing %q", subagents.ToolName)
	}

	result, err := delegate.Execute(context.Background(), tool.Call{
		Use: model.ToolUse{
			ID:    "delegate-1",
			Name:  subagents.ToolName,
			Input: json.RawMessage(`{"agent":"explorer","prompt":"inspect README and report evidence"}`),
		},
		Runtime: tool.Runtime{
			SessionID: "00000000-0000-7000-8000-000000000001",
			Sessions:  session.NewJSONLStore(sessionDir),
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result IsError = true: %s", result.Content)
	}
	if result.Content != "child result" {
		t.Fatalf("result content = %q, want child result", result.Content)
	}
	if result.Metadata["parent_session_id"] != "00000000-0000-7000-8000-000000000001" {
		t.Fatalf("metadata = %#v, want parent_session_id", result.Metadata)
	}
	childSessionID, ok := result.Metadata["child_session_id"].(string)
	if !ok || childSessionID == "" {
		t.Fatalf("metadata = %#v, want child_session_id", result.Metadata)
	}
	if len(client.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.ParentSessionID != "00000000-0000-7000-8000-000000000001" {
		t.Fatalf("ParentSessionID = %q, want parent session", req.ParentSessionID)
	}
	if req.SessionID != childSessionID {
		t.Fatalf("request SessionID = %q, want child session %q", req.SessionID, childSessionID)
	}
	if hasTool(req.Tools, subagents.ToolName) {
		t.Fatalf("child tool list unexpectedly includes recursive %q", subagents.ToolName)
	}
	if !hasTool(req.Tools, workspacetools.ReadToolName) || !hasTool(req.Tools, workspacetools.ListToolName) {
		t.Fatalf("child tools = %#v, want read/list workspace tools", toolNames(req.Tools))
	}
	if hasTool(req.Tools, workspacetools.ApplyPatchToolName) || hasTool(req.Tools, commandtools.ToolName) {
		t.Fatalf("explorer child tools = %#v, want read-only tools", toolNames(req.Tools))
	}

	result, err = delegate.Execute(context.Background(), tool.Call{
		Use: model.ToolUse{
			ID:    "delegate-2",
			Name:  subagents.ToolName,
			Input: json.RawMessage(`{"agent":"worker","prompt":"make a scoped edit and verify it"}`),
		},
		Runtime: tool.Runtime{
			SessionID: "00000000-0000-7000-8000-000000000001",
			Sessions:  session.NewJSONLStore(sessionDir),
		},
	})
	if err != nil {
		t.Fatalf("worker Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("worker result IsError = true: %s", result.Content)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(client.requests))
	}
	req = client.requests[1]
	for _, want := range []string{
		workspacetools.ApplyPatchToolName,
		workspacetools.CheckpointToolName,
		workspacetools.RestoreToolName,
		commandtools.ToolName,
		commandtools.StartToolName,
		tasktools.UpsertToolName,
	} {
		if !hasTool(req.Tools, want) {
			t.Fatalf("worker child tools = %#v, missing %q", toolNames(req.Tools), want)
		}
	}
	if hasTool(req.Tools, subagents.ToolName) {
		t.Fatalf("worker child tool list unexpectedly includes recursive %q", subagents.ToolName)
	}
	workerPrompt := req.SystemPrompt + "\n" + req.AppendSystemPrompt
	for _, want := range []string{
		"CLI tool contract:",
		"Use run_command with command as one shell command string",
		"Use workspace_apply_patch with exactly one unified_diff string",
		"obey checkpoint, command, approval, and verification gates",
	} {
		if !strings.Contains(workerPrompt, want) {
			t.Fatalf("worker prompt missing %q:\n%s", want, workerPrompt)
		}
	}
}

func TestAppToolUseDisplaySubagentProfile(t *testing.T) {
	got := appToolUseDisplay(&model.ToolUse{
		ID:    "delegate-1",
		Name:  subagents.ToolName,
		Input: json.RawMessage(`{"agent":"reviewer","prompt":"review the diff"}`),
	})
	if got != "Subagent(reviewer) review the diff" {
		t.Fatalf("appToolUseDisplay() = %q, want Subagent(reviewer) review the diff", got)
	}
	if appToolShowsResultTail(subagents.ToolName) {
		t.Fatalf("subagent results should use compact lifecycle summaries, not raw tails")
	}
}

func TestAppendPromptSection(t *testing.T) {
	t.Parallel()

	if got := appendPromptSection("", "next"); got != "next" {
		t.Fatalf("appendPromptSection(empty) = %q, want next", got)
	}
	if got := appendPromptSection("base", "next"); got != "base\n\nnext" {
		t.Fatalf("appendPromptSection(base) = %q, want separated sections", got)
	}
	if got := appendPromptSection("base", ""); got != "base" {
		t.Fatalf("appendPromptSection(empty section) = %q, want base", got)
	}
}

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasTool(specs []model.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func toolNames(specs []model.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

type scriptedTextClient struct {
	text     string
	requests []model.Request
}

func (c *scriptedTextClient) Stream(_ context.Context, req model.Request) (model.Stream, error) {
	c.requests = append(c.requests, req)
	return &scriptedTextStream{events: []model.StreamEvent{{Kind: model.StreamText, Text: c.text}}}, nil
}

type scriptedTextStream struct {
	events []model.StreamEvent
	index  int
}

func (s *scriptedTextStream) Recv() (model.StreamEvent, error) {
	if s.index >= len(s.events) {
		return model.StreamEvent{}, model.ErrEndOfStream
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *scriptedTextStream) Close() error {
	return nil
}

func toolSpec(registry *tool.Registry, name string) (model.ToolSpec, bool) {
	for _, spec := range registry.Specs() {
		if spec.Name == name {
			return spec, true
		}
	}
	return model.ToolSpec{}, false
}

func schemaProperties(t *testing.T, spec model.ToolSpec) map[string]any {
	t.Helper()
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s input schema properties = %#v, want object", spec.Name, spec.InputSchema["properties"])
	}
	return properties
}
