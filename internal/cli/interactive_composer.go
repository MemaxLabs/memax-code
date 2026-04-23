package cli

import (
	"fmt"
	"io"
	"strings"
)

type interactiveComposer struct {
	draftActive bool
	draftLines  []string
}

func (c *interactiveComposer) promptLabel() string {
	if c.draftActive {
		return "draft> "
	}
	return "memax> "
}

func (c *interactiveComposer) start(initial string) {
	c.draftActive = true
	c.draftLines = nil
	if strings.TrimSpace(initial) != "" {
		c.draftLines = append(c.draftLines, initial)
	}
}

func (c *interactiveComposer) appendLine(line string) {
	c.draftActive = true
	c.draftLines = append(c.draftLines, line)
}

func (c *interactiveComposer) cancel() {
	c.draftActive = false
	c.draftLines = nil
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
	return strings.TrimRight(strings.Join(c.draftLines, "\n"), "\r\n")
}

func (c *interactiveComposer) lineCount() int {
	return len(c.draftLines)
}

func (c *interactiveComposer) statusLine() string {
	if !c.draftActive {
		return "draft: inactive"
	}
	if len(c.draftLines) == 0 {
		return "draft: active empty"
	}
	return fmt.Sprintf("draft: active lines=%d chars=%d", len(c.draftLines), len(c.text()))
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
