package cli

import (
	"fmt"
	"strings"

	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/providers/anthropic"
	"github.com/MemaxLabs/memax-go-agent-sdk/providers/openai"
	"github.com/MemaxLabs/memax-go-agent-sdk/stack/coding"
)

type provider string

const (
	providerOpenAI    provider = "openai"
	providerAnthropic provider = "anthropic"
)

func parseProvider(raw string) (provider, error) {
	switch provider(strings.ToLower(strings.TrimSpace(raw))) {
	case providerOpenAI:
		return providerOpenAI, nil
	case providerAnthropic:
		return providerAnthropic, nil
	default:
		return "", fmt.Errorf("unknown provider %q", raw)
	}
}

func (p provider) modelEnv() string {
	switch p {
	case providerOpenAI:
		return "OPENAI_MODEL"
	case providerAnthropic:
		return "ANTHROPIC_MODEL"
	default:
		return "MODEL"
	}
}

func (p provider) keyEnv() string {
	switch p {
	case providerOpenAI:
		return "OPENAI_API_KEY"
	case providerAnthropic:
		return "ANTHROPIC_API_KEY"
	default:
		return "API_KEY"
	}
}

func parseModelProfile(raw string) (coding.ModelProfile, error) {
	return coding.ParseModelProfile(raw)
}

func validModelProfiles() string {
	profiles := coding.ModelProfiles()
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.String())
	}
	return strings.Join(names, ", ")
}

func modelClient(opts options) (model.Client, error) {
	profile, err := parseModelProfile(opts.Profile)
	if err != nil {
		return nil, userFacingError(err)
	}
	switch opts.Provider {
	case providerOpenAI:
		modelOpts, err := coding.OpenAIModelOptions(profile)
		if err != nil {
			return nil, userFacingError(err)
		}
		client := openai.NewFromEnv(opts.Model, modelOpts...)
		if strings.TrimSpace(client.APIKey) == "" {
			return nil, fmt.Errorf("openai api key is required; set %s", opts.Provider.keyEnv())
		}
		if strings.TrimSpace(client.Model) == "" {
			return nil, fmt.Errorf("openai model is required; pass --model or set %s", opts.Provider.modelEnv())
		}
		return client, nil
	case providerAnthropic:
		modelOpts, err := coding.AnthropicModelOptions(profile)
		if err != nil {
			return nil, userFacingError(err)
		}
		client := anthropic.NewFromEnv(opts.Model, modelOpts...)
		if strings.TrimSpace(client.APIKey) == "" {
			return nil, fmt.Errorf("anthropic api key is required; set %s", opts.Provider.keyEnv())
		}
		if strings.TrimSpace(client.Model) == "" {
			return nil, fmt.Errorf("anthropic model is required; pass --model or set %s", opts.Provider.modelEnv())
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", opts.Provider)
	}
}
