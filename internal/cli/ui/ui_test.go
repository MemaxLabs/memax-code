package ui

import (
	"io"
	"testing"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Mode
	}{
		{name: "empty", raw: "", want: ModeAuto},
		{name: "auto", raw: "auto", want: ModeAuto},
		{name: "live", raw: "live", want: ModeLive},
		{name: "structured", raw: "tui", want: ModeStructured},
		{name: "plain", raw: "plain", want: ModePlain},
		{name: "case and spaces", raw: " TUI ", want: ModeStructured},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMode(tt.raw)
			if err != nil {
				t.Fatalf("ParseMode(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseModeRejectsUnknown(t *testing.T) {
	if _, err := ParseMode("fancy"); err == nil {
		t.Fatal("ParseMode() error = nil, want error")
	}
}

func TestResolveMode(t *testing.T) {
	if got := ResolveMode(ModeAuto, true); got != ModeStructured {
		t.Fatalf("ResolveMode(auto, terminal) = %q, want %q", got, ModeStructured)
	}
	if got := ResolveMode(ModeAuto, false); got != ModePlain {
		t.Fatalf("ResolveMode(auto, non-terminal) = %q, want %q", got, ModePlain)
	}
	if got := ResolveMode(ModeLive, false); got != ModePlain {
		t.Fatalf("ResolveMode(live, non-terminal) = %q, want %q", got, ModePlain)
	}
	if got := ResolveMode(ModeLive, true); got != ModeLive {
		t.Fatalf("ResolveMode(live, terminal) = %q, want %q", got, ModeLive)
	}
	if got := ResolveMode(ModePlain, true); got != ModePlain {
		t.Fatalf("ResolveMode(plain, terminal) = %q, want %q", got, ModePlain)
	}
}

func TestSelectRenderer(t *testing.T) {
	plain := stubRenderer{name: "plain"}
	live := stubRenderer{name: "live"}
	structured := stubRenderer{name: "structured"}
	renderers := Renderers{Plain: plain, Live: live, Structured: structured}

	got, err := SelectRenderer(ModePlain, renderers)
	if err != nil {
		t.Fatalf("SelectRenderer(plain) error = %v", err)
	}
	if got != plain {
		t.Fatalf("SelectRenderer(plain) = %v, want plain", got)
	}

	got, err = SelectRenderer(ModeLive, renderers)
	if err != nil {
		t.Fatalf("SelectRenderer(live) error = %v", err)
	}
	if got != live {
		t.Fatalf("SelectRenderer(live) = %v, want live", got)
	}

	got, err = SelectRenderer(ModeStructured, renderers)
	if err != nil {
		t.Fatalf("SelectRenderer(structured) error = %v", err)
	}
	if got != structured {
		t.Fatalf("SelectRenderer(structured) = %v, want structured", got)
	}

	if _, err := SelectRenderer(ModeAuto, renderers); err == nil {
		t.Fatal("SelectRenderer(auto) error = nil, want unresolved mode error")
	}
}

func TestSelectRendererRejectsMissingRenderer(t *testing.T) {
	if _, err := SelectRenderer(ModeStructured, Renderers{Plain: stubRenderer{name: "plain"}, Live: stubRenderer{name: "live"}}); err == nil {
		t.Fatal("SelectRenderer() error = nil, want missing renderer error")
	}
}

type stubRenderer struct {
	name string
}

func (r stubRenderer) Render(io.Writer, memaxagent.Event) error {
	return nil
}

func (r stubRenderer) Finish(io.Writer) error {
	return nil
}
