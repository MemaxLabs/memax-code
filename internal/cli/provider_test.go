package cli

import (
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/providers/anthropic"
	"github.com/MemaxLabs/memax-go-agent-sdk/providers/openai"
	"github.com/MemaxLabs/memax-go-agent-sdk/stack/coding"
)

func TestModelClientAppliesOpenAIEffortOverrideAfterProfile(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	client, err := modelClient(options{
		Provider: providerOpenAI,
		Model:    "test-model",
		Profile:  coding.ModelProfileFast.String(),
		Effort:   coding.ModelEffortHigh.String(),
	})
	if err != nil {
		t.Fatalf("modelClient() error = %v", err)
	}
	openAIClient, ok := client.(*openai.Client)
	if !ok {
		t.Fatalf("client type = %T, want *openai.Client", client)
	}
	if openAIClient.Reasoning == nil || openAIClient.Reasoning.Effort != openai.ReasoningEffortHigh {
		t.Fatalf("Reasoning = %+v, want high effort", openAIClient.Reasoning)
	}
}

func TestModelClientPreservesOpenAIProfileForAutoEffort(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	client, err := modelClient(options{
		Provider: providerOpenAI,
		Model:    "test-model",
		Profile:  coding.ModelProfileFast.String(),
		Effort:   coding.ModelEffortAuto.String(),
	})
	if err != nil {
		t.Fatalf("modelClient() error = %v", err)
	}
	openAIClient, ok := client.(*openai.Client)
	if !ok {
		t.Fatalf("client type = %T, want *openai.Client", client)
	}
	if openAIClient.Reasoning == nil || openAIClient.Reasoning.Effort != openai.ReasoningEffortLow {
		t.Fatalf("Reasoning = %+v, want profile effort low", openAIClient.Reasoning)
	}
}

func TestModelClientAppliesAnthropicEffortOverrideAfterProfile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	client, err := modelClient(options{
		Provider: providerAnthropic,
		Model:    "test-model",
		Profile:  coding.ModelProfileFast.String(),
		Effort:   coding.ModelEffortHigh.String(),
	})
	if err != nil {
		t.Fatalf("modelClient() error = %v", err)
	}
	anthropicClient, ok := client.(*anthropic.Client)
	if !ok {
		t.Fatalf("client type = %T, want *anthropic.Client", client)
	}
	if anthropicClient.Effort != anthropic.EffortHigh {
		t.Fatalf("Effort = %q, want high", anthropicClient.Effort)
	}
}

func TestModelClientPreservesAnthropicProfileForAutoEffort(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	client, err := modelClient(options{
		Provider: providerAnthropic,
		Model:    "test-model",
		Profile:  coding.ModelProfileFast.String(),
		Effort:   coding.ModelEffortAuto.String(),
	})
	if err != nil {
		t.Fatalf("modelClient() error = %v", err)
	}
	anthropicClient, ok := client.(*anthropic.Client)
	if !ok {
		t.Fatalf("client type = %T, want *anthropic.Client", client)
	}
	if anthropicClient.Effort != anthropic.EffortLow {
		t.Fatalf("Effort = %q, want profile effort low", anthropicClient.Effort)
	}
}
