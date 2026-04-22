package cli

import (
	"strings"
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/tool"
	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/commandtools"
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
