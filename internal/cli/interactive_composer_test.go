package cli

import "testing"

func TestComposerBufferEditsWithCursor(t *testing.T) {
	var b composerBuffer
	b.InsertString("helo")
	b.MoveLeft()
	b.InsertRune('l')
	if got, want := b.Text(), "hello"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	if got, want := b.Cursor(), 4; got != want {
		t.Fatalf("Cursor() = %d, want %d", got, want)
	}

	if !b.Backspace() {
		t.Fatal("Backspace() = false, want true")
	}
	if got, want := b.Text(), "helo"; got != want {
		t.Fatalf("Text() after backspace = %q, want %q", got, want)
	}

	b.MoveEnd()
	b.InsertNewline()
	b.InsertString("world")
	b.MoveStart()
	if !b.Delete() {
		t.Fatal("Delete() = false, want true")
	}
	if got, want := b.Text(), "elo\nworld"; got != want {
		t.Fatalf("Text() after multiline edit = %q, want %q", got, want)
	}
}

func TestComposerHistoryNewestFirstAndRecall(t *testing.T) {
	var h composerHistory
	h.Record("first prompt")
	h.Record("second\nprompt")
	h.Record("second\nprompt")

	if got, want := h.Len(), 2; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	rows := h.Rows()
	if len(rows) != 2 || rows[0] != "second\nprompt" || rows[1] != "first prompt" {
		t.Fatalf("Rows() = %#v, want newest first", rows)
	}
	if got, ok := h.Resolve("latest"); !ok || got != "second\nprompt" {
		t.Fatalf("Resolve(latest) = %q, %t", got, ok)
	}
	if got, ok := h.Resolve("2"); !ok || got != "first prompt" {
		t.Fatalf("Resolve(2) = %q, %t", got, ok)
	}
	if _, ok := h.Resolve("3"); ok {
		t.Fatal("Resolve(3) ok = true, want false")
	}
	if _, ok := h.Resolve("1abc"); ok {
		t.Fatal("Resolve(1abc) ok = true, want false")
	}
}

func TestComposerHistoryPreservesSubmittedWhitespace(t *testing.T) {
	var h composerHistory
	h.Record("\t  indented prompt\n")

	got, ok := h.Resolve("latest")
	if !ok {
		t.Fatal("Resolve(latest) ok = false, want true")
	}
	if want := "\t  indented prompt"; got != want {
		t.Fatalf("Resolve(latest) = %q, want %q", got, want)
	}
}

func TestComposerHistoryPreviousNextRestoresDraft(t *testing.T) {
	var h composerHistory
	h.Record("first")
	h.Record("second")

	if got, ok := h.Previous("in-progress"); !ok || got != "second" {
		t.Fatalf("Previous() = %q, %t; want latest", got, ok)
	}
	if got, ok := h.Previous("ignored"); !ok || got != "first" {
		t.Fatalf("Previous() second = %q, %t; want older", got, ok)
	}
	if got, ok := h.Previous("ignored"); !ok || got != "first" {
		t.Fatalf("Previous() boundary = %q, %t; want same oldest", got, ok)
	}
	if got, ok := h.Next(); !ok || got != "second" {
		t.Fatalf("Next() = %q, %t; want newer", got, ok)
	}
	if got, ok := h.Next(); !ok || got != "in-progress" {
		t.Fatalf("Next() restore = %q, %t; want saved draft", got, ok)
	}
	if _, ok := h.Next(); ok {
		t.Fatal("Next() after traversal ok = true, want false")
	}
}

func TestInteractiveComposerRecallReportsDiscardedDraft(t *testing.T) {
	var c interactiveComposer
	c.history.Record("old prompt")
	c.start("new work")

	got, discardedLines, ok := c.recall("latest")
	if !ok {
		t.Fatal("recall() ok = false, want true")
	}
	if got != "old prompt" || c.text() != "old prompt" {
		t.Fatalf("recall() restored %q and draft %q, want old prompt", got, c.text())
	}
	if discardedLines != 1 {
		t.Fatalf("discardedLines = %d, want 1", discardedLines)
	}
}

func TestInteractiveComposerPreservesLeadingBlankLine(t *testing.T) {
	var c interactiveComposer
	c.start("")
	c.appendLine("")
	c.appendLine("hello")

	if got, want := c.text(), "\nhello"; got != want {
		t.Fatalf("text() = %q, want %q", got, want)
	}
}
