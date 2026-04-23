package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestScannerLineReaderPrintsPromptAndReadsLine(t *testing.T) {
	var out bytes.Buffer
	reader := newScannerLineReader(strings.NewReader("hello\n"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", nil)
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "hello" {
		t.Fatalf("ReadLine() line = %q, want hello", line)
	}
	if got, want := out.String(), "memax> "; got != want {
		t.Fatalf("prompt output = %q, want %q", got, want)
	}
}

func TestRawKeyLineReaderEditsCursorAndSubmits(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("helo\x1b[Dl\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "hello" {
		t.Fatalf("ReadLine() line = %q, want hello", line)
	}
	if !strings.Contains(out.String(), "\x1b[1D") {
		t.Fatalf("raw output did not move cursor left:\n%q", out.String())
	}
}

func TestRawKeyLineReaderHistoryPreviousNext(t *testing.T) {
	var out bytes.Buffer
	composer := &interactiveComposer{}
	composer.history.Record("first")
	composer.history.Record("second")
	reader := newRawKeyLineReader(strings.NewReader("\x1b[A\x1b[A\x1b[B!\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", composer)
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "second!" {
		t.Fatalf("ReadLine() line = %q, want second!", line)
	}
	if composer.history.browsing {
		t.Fatal("history browsing = true after edit/submit, want reset")
	}
}

func TestRawKeyLineReaderControlKeys(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("abc\x01X\x05Y\x7f\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "Xabc" {
		t.Fatalf("ReadLine() line = %q, want Xabc", line)
	}
}

func TestRawKeyLineReaderConsumesParameterizedCSISequences(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("a\x1b[1;5Cb\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "ab" {
		t.Fatalf("ReadLine() line = %q, want ab", line)
	}
}

func TestRawKeyLineReaderDeleteAcceptsCSIParams(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("ab\x1b[D\x1b[3;5~\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "a" {
		t.Fatalf("ReadLine() line = %q, want a", line)
	}
}

func TestRawKeyLineReaderConsumesUnknownCSISequences(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("a\x1b[200~b\x1b[201~c\r"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadLine() ok = false, want true")
	}
	if line != "abc" {
		t.Fatalf("ReadLine() line = %q, want abc", line)
	}
}

func TestRawKeyLineReaderPreservesBufferedInputAcrossLines(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("first\nsecond\n"), &out)

	first, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("first ReadLine() error = %v", err)
	}
	if !ok || first != "first" {
		t.Fatalf("first ReadLine() = %q, %t; want first, true", first, ok)
	}
	second, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("second ReadLine() error = %v", err)
	}
	if !ok || second != "second" {
		t.Fatalf("second ReadLine() = %q, %t; want second, true", second, ok)
	}
}

func TestRawKeyLineReaderCtrlDAtEmptyReturnsEOF(t *testing.T) {
	var out bytes.Buffer
	reader := newRawKeyLineReader(strings.NewReader("\x04"), &out)

	line, ok, err := reader.ReadLine(context.Background(), "memax> ", &interactiveComposer{})
	if err != nil {
		t.Fatalf("ReadLine() error = %v", err)
	}
	if ok {
		t.Fatalf("ReadLine() = %q, true; want EOF", line)
	}
}
