// Package ui defines terminal rendering contracts for memax-code.
//
// The package intentionally owns only renderer selection and lifecycle. Concrete
// renderers live in the CLI package so transcript formatting and future live
// terminal presentation can evolve independently.
package ui

import (
	"fmt"
	"io"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

// Mode selects the CLI event renderer.
type Mode string

const (
	// ModeAuto selects the structured renderer for terminals and the plain
	// transcript renderer for non-terminal writers.
	ModeAuto Mode = "auto"
	// ModeLive renders an interactive transcript with a live status line.
	ModeLive Mode = "live"
	// ModeStructured renders a sectioned terminal transcript with final status.
	ModeStructured Mode = "tui"
	// ModePlain renders a compact transcript suitable for pipes and logs.
	ModePlain Mode = "plain"
)

// Renderer consumes agent events and writes user-facing terminal output.
type Renderer interface {
	Render(io.Writer, memaxagent.Event) error
	Finish(io.Writer) error
}

// Renderers groups the concrete renderers available to the selector.
type Renderers struct {
	Plain      Renderer
	Live       Renderer
	Structured Renderer
}

// ParseMode parses a user-supplied renderer mode.
func ParseMode(raw string) (Mode, error) {
	switch mode := Mode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeLive, ModeStructured, ModePlain:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown ui %q (want one of: auto, live, tui, plain)", raw)
	}
}

// ResolveMode resolves auto and non-terminal live mode using terminal detection.
func ResolveMode(mode Mode, terminal bool) Mode {
	if mode == ModeLive && !terminal {
		return ModePlain
	}
	if mode != ModeAuto {
		return mode
	}
	if terminal {
		return ModeStructured
	}
	return ModePlain
}

// SelectRenderer selects one of the provided renderers for a resolved mode.
func SelectRenderer(mode Mode, renderers Renderers) (Renderer, error) {
	var selected Renderer
	switch mode {
	case ModePlain:
		selected = renderers.Plain
	case ModeLive:
		selected = renderers.Live
	case ModeStructured:
		selected = renderers.Structured
	default:
		return nil, fmt.Errorf("cannot select renderer for unresolved ui mode %q", mode)
	}
	if selected == nil {
		return nil, fmt.Errorf("no renderer configured for ui mode %q", mode)
	}
	return selected, nil
}
