package cli

import (
	"strings"
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/toolkit/verifytools"
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
