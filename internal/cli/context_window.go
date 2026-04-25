package cli

import (
	"fmt"
	"strings"

	"github.com/MemaxLabs/memax-go-agent-sdk/contextwindow"
	"github.com/MemaxLabs/memax-go-agent-sdk/model"
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

func effectiveContextWindow(opts options) int {
	if opts.ContextWindow > 0 {
		return opts.ContextWindow
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
	switch provider {
	case providerOpenAI:
		switch {
		case strings.Contains(name, "gpt-5"), strings.Contains(name, "gpt-4.1"), strings.Contains(name, "gpt-4o"):
			return 128000
		}
	case providerAnthropic:
		return 200000
	}
	return defaultContextWindowTokens
}

func resolveContextBudgets(opts options) resolvedContextBudgets {
	window := effectiveContextWindow(opts)
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
	budgets := resolveContextBudgets(opts)
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
