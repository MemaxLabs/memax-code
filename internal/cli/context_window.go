package cli

import (
	"fmt"
	"strings"

	"github.com/MemaxLabs/memax-go-agent-sdk/contextwindow"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
	"github.com/MemaxLabs/memax-go-agent-sdk/providers/anthropic"
	"github.com/MemaxLabs/memax-go-agent-sdk/providers/openai"
)

type compactionMode string

const (
	compactionModeAuto compactionMode = "auto"
	compactionModeOff  compactionMode = "off"
)

const (
	defaultContextWindowTokens  = 128000
	defaultContextSummaryTokens = 8192
	minContextWindowTokens      = 4096
	minContextSummaryTokens     = 512
)

type resolvedContextBudgets struct {
	WindowTokens  int
	SummaryTokens int
	MainTokens    int
	RetryTokens   int
}

func parseCompactionMode(raw string) (compactionMode, error) {
	switch compactionMode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", compactionModeAuto:
		return compactionModeAuto, nil
	case compactionModeOff:
		return compactionModeOff, nil
	default:
		return "", fmt.Errorf("unknown compaction mode %q (want auto or off)", raw)
	}
}

func effectiveContextWindow(opts options, client model.Client) int {
	if opts.ContextWindow > 0 {
		return opts.ContextWindow
	}
	if caps, ok := model.ClientCapabilities(client); ok && caps.ContextWindowTokens > 0 {
		return caps.ContextWindowTokens
	}
	return inferredContextWindow(opts.Provider, opts.Model)
}

func effectiveContextSummaryTokens(opts options, window int) int {
	if opts.ContextSummary > 0 {
		return opts.ContextSummary
	}
	if window <= 0 {
		return defaultContextSummaryTokens
	}
	summary := defaultContextSummaryTokens
	if maxSummary := window / 4; maxSummary > 0 && summary > maxSummary {
		summary = maxSummary
	}
	if summary < minContextSummaryTokens {
		return minContextSummaryTokens
	}
	return summary
}

func inferredContextWindow(provider provider, modelName string) int {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if window, ok := gatewayModelContextWindow(name); ok {
		return window
	}
	switch provider {
	case providerOpenAI:
		if caps := openai.CapabilitiesForModel(name); caps.ContextWindowTokens > 0 {
			return caps.ContextWindowTokens
		}
	case providerAnthropic:
		if caps := anthropic.CapabilitiesForModel(name); caps.ContextWindowTokens > 0 {
			return caps.ContextWindowTokens
		}
		return 200000
	}
	return defaultContextWindowTokens
}

func gatewayModelContextWindow(modelName string) (int, bool) {
	family, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(modelName)), "/")
	if !ok {
		return 0, false
	}
	switch family {
	case "openai":
		if caps := openai.CapabilitiesForModel(modelName); caps.ContextWindowTokens > 0 {
			return caps.ContextWindowTokens, true
		}
		return defaultContextWindowTokens, true
	case "anthropic":
		if caps := anthropic.CapabilitiesForModel(modelName); caps.ContextWindowTokens > 0 {
			return caps.ContextWindowTokens, true
		}
		return 200000, true
	default:
		return 0, false
	}
}

func resolveContextBudgets(opts options, client model.Client) resolvedContextBudgets {
	window := effectiveContextWindow(opts, client)
	if window < minContextWindowTokens {
		window = minContextWindowTokens
	}
	summaryTokens := effectiveContextSummaryTokens(opts, window)
	if summaryTokens >= window {
		summaryTokens = window / 4
	}
	if summaryTokens < minContextSummaryTokens {
		summaryTokens = minContextSummaryTokens
	}
	if summaryTokens >= window {
		summaryTokens = window - 1
	}

	mainBudget := int(float64(window) * 0.80)
	if mainBudget <= summaryTokens {
		mainBudget = summaryTokens + 1
	}
	if mainBudget > window {
		mainBudget = window
	}

	retryBudget := int(float64(window) * 0.55)
	if retryBudget <= summaryTokens {
		retryBudget = summaryTokens + 1
	}
	if retryBudget > mainBudget {
		retryBudget = mainBudget
	}

	return resolvedContextBudgets{
		WindowTokens:  window,
		SummaryTokens: summaryTokens,
		MainTokens:    mainBudget,
		RetryTokens:   retryBudget,
	}
}

func estimateApproxTokens(msg model.Message) int {
	runes := contextwindow.EstimateByRunes(msg)
	if runes <= 0 {
		return runes
	}
	// Use a deliberately conservative local approximation until providers expose
	// stable token counters through the SDK. Code and JSON tool payloads often
	// tokenize denser than prose, so 3 runes per token is safer than the common
	// English-text 4-runes heuristic.
	return (runes + 2) / 3
}

func contextPolicies(opts options, client model.Client) (contextwindow.Policy, contextwindow.Policy) {
	if opts.Compaction == compactionModeOff || client == nil {
		return nil, nil
	}
	budgets := resolveContextBudgets(opts, client)
	summarizer := contextwindow.ModelSummarizer{
		Model:        client,
		SystemPrompt: "You compact coding-agent transcripts for future turns. Preserve user goals, repo facts, decisions, changed files, command results, open risks, and next actions. Do not add new facts.",
	}
	return contextwindow.PreserveImportant{
			Policy: contextwindow.SummarizingBudget{
				MaxTokens:        budgets.MainTokens,
				MaxSummaryTokens: budgets.SummaryTokens,
				Estimate:         estimateApproxTokens,
				Summarizer:       summarizer,
				SummaryRole:      model.RoleUser,
				SummaryPrefix:    "Earlier context summary for this coding session:",
			},
			MaxMessages: 12,
		}, contextwindow.PreserveImportant{
			Policy: contextwindow.SummarizingBudget{
				MaxTokens:        budgets.RetryTokens,
				MaxSummaryTokens: budgets.SummaryTokens,
				Estimate:         estimateApproxTokens,
				Summarizer:       summarizer,
				SummaryRole:      model.RoleUser,
				SummaryPrefix:    "Earlier context summary for this coding session after context-window recovery:",
			},
			MaxMessages: 12,
		}
}
