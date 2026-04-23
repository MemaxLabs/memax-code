package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type interactiveComposer struct {
	draftActive   bool
	draftHasInput bool
	buffer        composerBuffer
	history       composerHistory
}

func (c *interactiveComposer) promptLabel() string {
	if c.draftActive {
		return "draft> "
	}
	return "memax> "
}

func (c *interactiveComposer) start(initial string) {
	c.draftActive = true
	c.draftHasInput = false
	c.buffer.SetText("")
	if strings.TrimSpace(initial) != "" {
		c.buffer.InsertString(initial)
		c.draftHasInput = true
	}
	c.history.ResetTraversal()
}

func (c *interactiveComposer) appendLine(line string) {
	c.draftActive = true
	c.buffer.MoveEnd()
	if c.draftHasInput {
		c.buffer.InsertNewline()
	}
	c.buffer.InsertString(line)
	c.draftHasInput = true
	c.history.ResetTraversal()
}

func (c *interactiveComposer) cancel() {
	c.draftActive = false
	c.draftHasInput = false
	c.buffer.SetText("")
	c.history.ResetTraversal()
}

func (c *interactiveComposer) submit() (string, bool) {
	text := c.text()
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	c.cancel()
	return text, true
}

func (c *interactiveComposer) text() string {
	return strings.TrimRight(c.buffer.Text(), "\r\n")
}

func (c *interactiveComposer) lineCount() int {
	text := c.text()
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func (c *interactiveComposer) statusLine() string {
	if !c.draftActive {
		return "draft: inactive"
	}
	if c.text() == "" {
		return "draft: active empty"
	}
	return fmt.Sprintf("draft: active lines=%d chars=%d cursor=%d history=%d", c.lineCount(), len([]rune(c.text())), c.buffer.Cursor(), c.history.Len())
}

func (c *interactiveComposer) recall(ref string) (string, int, bool) {
	text, ok := c.history.Resolve(ref)
	if !ok {
		return "", 0, false
	}
	discardedLines := 0
	if c.draftActive && strings.TrimSpace(c.text()) != "" {
		discardedLines = c.lineCount()
	}
	c.draftActive = true
	c.draftHasInput = true
	c.buffer.SetText(text)
	c.history.ResetTraversal()
	return text, discardedLines, true
}

func (c *interactiveComposer) historyRows() []string {
	return c.history.Rows()
}

func printInteractiveDraft(w io.Writer, composer *interactiveComposer) {
	if composer == nil || !composer.draftActive {
		fmt.Fprintln(w, "no active draft")
		return
	}
	text := composer.text()
	if text == "" {
		fmt.Fprintln(w, "draft is empty")
		return
	}
	fmt.Fprintln(w, "draft:")
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func printInteractivePromptHistory(w io.Writer, composer *interactiveComposer) {
	if composer == nil || composer.history.Len() == 0 {
		fmt.Fprintln(w, "no prompt history")
		return
	}
	fmt.Fprintln(w, "prompt history:")
	for i, text := range composer.historyRows() {
		fmt.Fprintf(w, "  %d) %s\n", i+1, summarizeComposerHistoryText(text))
	}
	fmt.Fprintln(w, "Use /recall N or /recall latest to restore one into the draft.")
}

func summarizeComposerHistoryText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const summaryWidth = 80
	if len([]rune(text)) <= summaryWidth {
		return text
	}
	runes := []rune(text)
	return string(runes[:summaryWidth-1]) + "..."
}

type composerBuffer struct {
	text   []rune
	cursor int
}

func (b *composerBuffer) Text() string {
	return string(b.text)
}

func (b *composerBuffer) Cursor() int {
	return b.cursor
}

func (b *composerBuffer) SetText(text string) {
	b.text = []rune(text)
	b.cursor = len(b.text)
}

func (b *composerBuffer) InsertString(text string) {
	for _, r := range text {
		b.InsertRune(r)
	}
}

func (b *composerBuffer) InsertNewline() {
	b.InsertRune('\n')
}

func (b *composerBuffer) InsertRune(r rune) {
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor > len(b.text) {
		b.cursor = len(b.text)
	}
	b.text = append(b.text, 0)
	copy(b.text[b.cursor+1:], b.text[b.cursor:])
	b.text[b.cursor] = r
	b.cursor++
}

func (b *composerBuffer) Backspace() bool {
	if b.cursor <= 0 || len(b.text) == 0 {
		return false
	}
	copy(b.text[b.cursor-1:], b.text[b.cursor:])
	b.text = b.text[:len(b.text)-1]
	b.cursor--
	return true
}

func (b *composerBuffer) Delete() bool {
	if b.cursor < 0 || b.cursor >= len(b.text) {
		return false
	}
	copy(b.text[b.cursor:], b.text[b.cursor+1:])
	b.text = b.text[:len(b.text)-1]
	return true
}

func (b *composerBuffer) MoveLeft() bool {
	if b.cursor <= 0 {
		return false
	}
	b.cursor--
	return true
}

func (b *composerBuffer) MoveRight() bool {
	if b.cursor >= len(b.text) {
		return false
	}
	b.cursor++
	return true
}

func (b *composerBuffer) MoveStart() {
	b.cursor = 0
}

func (b *composerBuffer) MoveEnd() {
	b.cursor = len(b.text)
}

type composerHistory struct {
	entries    []string
	browsing   bool
	index      int
	savedDraft string
}

func (h *composerHistory) Record(text string) {
	stored := strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(stored) == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == stored {
		return
	}
	h.entries = append(h.entries, stored)
	h.ResetTraversal()
}

func (h *composerHistory) Len() int {
	return len(h.entries)
}

func (h *composerHistory) ResetTraversal() {
	h.browsing = false
	h.index = 0
	h.savedDraft = ""
}

func (h *composerHistory) Resolve(ref string) (string, bool) {
	ref = strings.TrimSpace(strings.ToLower(ref))
	if len(h.entries) == 0 {
		return "", false
	}
	if ref == "" || ref == "latest" {
		return h.entries[len(h.entries)-1], true
	}
	index, err := strconv.Atoi(ref)
	if err != nil || index < 1 || index > len(h.entries) {
		return "", false
	}
	return h.entries[len(h.entries)-index], true
}

func (h *composerHistory) Previous(currentDraft string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if !h.browsing {
		h.browsing = true
		h.savedDraft = currentDraft
		h.index = len(h.entries) - 1
		return h.entries[h.index], true
	}
	if h.index <= 0 {
		return h.entries[h.index], true
	}
	h.index--
	return h.entries[h.index], true
}

func (h *composerHistory) Next() (string, bool) {
	if !h.browsing || len(h.entries) == 0 {
		return "", false
	}
	if h.index >= len(h.entries)-1 {
		draft := h.savedDraft
		h.ResetTraversal()
		return draft, true
	}
	h.index++
	return h.entries[h.index], true
}

func (h *composerHistory) Rows() []string {
	rows := make([]string, 0, len(h.entries))
	for i := len(h.entries) - 1; i >= 0; i-- {
		rows = append(rows, h.entries[i])
	}
	return rows
}
