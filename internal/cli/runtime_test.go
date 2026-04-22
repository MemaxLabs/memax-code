package cli

import (
	"strings"
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/tool"
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

func TestTailBytesCapsOutput(t *testing.T) {
	got := tailBytes("0123456789", 4)
	if got != "... output truncated ...\n6789" {
		t.Fatalf("tailBytes() = %q", got)
	}
}

func TestBuildStackUsesUnifiedDiffPatchInput(t *testing.T) {
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
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("patch input schema properties = %#v, want object", spec.InputSchema["properties"])
	}
	if _, ok := properties["unified_diff"]; !ok {
		t.Fatalf("patch input schema = %#v, want unified_diff property", spec.InputSchema)
	}
	if _, ok := properties["operations"]; ok {
		t.Fatalf("patch input schema = %#v, did not want operations property", spec.InputSchema)
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
