package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type interactiveLineReader interface {
	ReadLine(context.Context, string, *interactiveComposer) (string, bool, error)
}

func newInteractiveLineReader(stdin io.Reader, stderr io.Writer) (interactiveLineReader, error) {
	if input, ok := stdin.(*os.File); ok && writerIsTerminal(stderr) && term.IsTerminal(int(input.Fd())) {
		return newTerminalRawKeyLineReader(input, stderr), nil
	}
	return newScannerLineReader(stdin, stderr), nil
}

func writerIsTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

type scannerLineReader struct {
	scanner *bufio.Scanner
	out     io.Writer
}

func newScannerLineReader(r io.Reader, out io.Writer) *scannerLineReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), interactiveScannerMaxBytes)
	return &scannerLineReader{scanner: scanner, out: out}
}

func (r *scannerLineReader) ReadLine(ctx context.Context, prompt string, _ *interactiveComposer) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	fmt.Fprint(r.out, prompt)
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return "", false, fmt.Errorf("read interactive input: %w", err)
		}
		return "", false, nil
	}
	return r.scanner.Text(), true, nil
}

type rawKeyLineReader struct {
	input *bufio.Reader
	out   io.Writer
}

type terminalRawKeyLineReader struct {
	file  *os.File
	input *bufio.Reader
	out   io.Writer
}

func newTerminalRawKeyLineReader(input *os.File, out io.Writer) *terminalRawKeyLineReader {
	return &terminalRawKeyLineReader{file: input, input: bufio.NewReader(input), out: out}
}

func (r *terminalRawKeyLineReader) ReadLine(ctx context.Context, prompt string, composer *interactiveComposer) (string, bool, error) {
	state, err := term.MakeRaw(int(r.file.Fd()))
	if err != nil {
		return "", false, fmt.Errorf("enable raw terminal input: %w", err)
	}
	defer func() {
		_ = term.Restore(int(r.file.Fd()), state)
	}()
	return (&rawKeyLineReader{input: r.input, out: r.out}).ReadLine(ctx, prompt, composer)
}

func newRawKeyLineReader(input io.Reader, out io.Writer) *rawKeyLineReader {
	return &rawKeyLineReader{input: bufio.NewReader(input), out: out}
}

func (r *rawKeyLineReader) ReadLine(ctx context.Context, prompt string, composer *interactiveComposer) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	var buffer composerBuffer
	r.redraw(prompt, &buffer)
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		key, err := r.readKey()
		if err == io.EOF {
			if buffer.Text() == "" {
				return "", false, nil
			}
			fmt.Fprintln(r.out)
			return buffer.Text(), true, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read interactive key: %w", err)
		}
		switch key.kind {
		case rawKeyRune:
			buffer.InsertRune(key.char)
			if composer != nil {
				composer.history.ResetTraversal()
			}
			r.redraw(prompt, &buffer)
		case rawKeyEnter:
			fmt.Fprintln(r.out)
			if composer != nil {
				composer.history.ResetTraversal()
			}
			return buffer.Text(), true, nil
		case rawKeyCtrlC:
			buffer.SetText("")
			if composer != nil {
				composer.history.ResetTraversal()
			}
			fmt.Fprintln(r.out, "^C")
			r.redraw(prompt, &buffer)
		case rawKeyCtrlD:
			if buffer.Text() == "" {
				fmt.Fprintln(r.out)
				return "", false, nil
			}
			if buffer.Delete() && composer != nil {
				composer.history.ResetTraversal()
			}
			r.redraw(prompt, &buffer)
		case rawKeyBackspace:
			if buffer.Backspace() && composer != nil {
				composer.history.ResetTraversal()
			}
			r.redraw(prompt, &buffer)
		case rawKeyDelete:
			if buffer.Delete() && composer != nil {
				composer.history.ResetTraversal()
			}
			r.redraw(prompt, &buffer)
		case rawKeyLeft:
			buffer.MoveLeft()
			r.redraw(prompt, &buffer)
		case rawKeyRight:
			buffer.MoveRight()
			r.redraw(prompt, &buffer)
		case rawKeyHome:
			buffer.MoveStart()
			r.redraw(prompt, &buffer)
		case rawKeyEnd:
			buffer.MoveEnd()
			r.redraw(prompt, &buffer)
		case rawKeyHistoryPrev:
			if composer == nil {
				break
			}
			text, ok := composer.history.Previous(buffer.Text())
			if ok {
				buffer.SetText(text)
				r.redraw(prompt, &buffer)
			}
		case rawKeyHistoryNext:
			if composer == nil {
				break
			}
			text, ok := composer.history.Next()
			if ok {
				buffer.SetText(text)
				r.redraw(prompt, &buffer)
			}
		case rawKeyClear:
			r.redraw(prompt, &buffer)
		}
	}
}

func (r *rawKeyLineReader) redraw(prompt string, buffer *composerBuffer) {
	text := buffer.Text()
	fmt.Fprintf(r.out, "\r\x1b[2K%s%s", prompt, text)
	if back := len([]rune(text)) - buffer.Cursor(); back > 0 {
		fmt.Fprintf(r.out, "\x1b[%dD", back)
	}
}

type rawKeyKind int

const (
	rawKeyNone rawKeyKind = iota
	rawKeyRune
	rawKeyEnter
	rawKeyBackspace
	rawKeyDelete
	rawKeyLeft
	rawKeyRight
	rawKeyHome
	rawKeyEnd
	rawKeyHistoryPrev
	rawKeyHistoryNext
	rawKeyCtrlC
	rawKeyCtrlD
	rawKeyClear
)

type rawKey struct {
	kind rawKeyKind
	char rune
}

func (r *rawKeyLineReader) readKey() (rawKey, error) {
	b, err := r.input.ReadByte()
	if err != nil {
		return rawKey{}, err
	}
	switch b {
	case '\r', '\n':
		return rawKey{kind: rawKeyEnter}, nil
	case 0x01:
		return rawKey{kind: rawKeyHome}, nil
	case 0x03:
		return rawKey{kind: rawKeyCtrlC}, nil
	case 0x04:
		return rawKey{kind: rawKeyCtrlD}, nil
	case 0x05:
		return rawKey{kind: rawKeyEnd}, nil
	case 0x0c:
		return rawKey{kind: rawKeyClear}, nil
	case 0x7f, 0x08:
		return rawKey{kind: rawKeyBackspace}, nil
	case 0x1b:
		return r.readEscapeKey()
	}
	if b < 0x20 {
		return rawKey{kind: rawKeyNone}, nil
	}
	if b < utf8.RuneSelf {
		return rawKey{kind: rawKeyRune, char: rune(b)}, nil
	}
	if err := r.input.UnreadByte(); err != nil {
		return rawKey{}, err
	}
	rr, _, err := r.input.ReadRune()
	if err != nil {
		return rawKey{}, err
	}
	return rawKey{kind: rawKeyRune, char: rr}, nil
}

func (r *rawKeyLineReader) readEscapeKey() (rawKey, error) {
	next, err := r.input.ReadByte()
	if err != nil {
		return rawKey{kind: rawKeyNone}, nil
	}
	switch next {
	case '[':
		return r.readCSIKey()
	case 'O':
		return r.readSS3Key()
	default:
		return rawKey{kind: rawKeyNone}, nil
	}
}

func (r *rawKeyLineReader) readSS3Key() (rawKey, error) {
	code, err := r.input.ReadByte()
	if err != nil {
		return rawKey{kind: rawKeyNone}, nil
	}
	switch code {
	case 'A':
		return rawKey{kind: rawKeyHistoryPrev}, nil
	case 'B':
		return rawKey{kind: rawKeyHistoryNext}, nil
	case 'C':
		return rawKey{kind: rawKeyRight}, nil
	case 'D':
		return rawKey{kind: rawKeyLeft}, nil
	case 'F':
		return rawKey{kind: rawKeyEnd}, nil
	case 'H':
		return rawKey{kind: rawKeyHome}, nil
	}
	return rawKey{kind: rawKeyNone}, nil
}

func (r *rawKeyLineReader) readCSIKey() (rawKey, error) {
	// Real CSI key sequences are short. The cap keeps a malformed sequence
	// from consuming an unbounded amount of subsequent input.
	const maxCSIBytes = 64
	seq := make([]byte, 0, 8)
	for len(seq) < maxCSIBytes {
		b, err := r.input.ReadByte()
		if err != nil {
			return rawKey{kind: rawKeyNone}, nil
		}
		if isCSIFinal(b) {
			return parseCSIKey(seq, b), nil
		}
		seq = append(seq, b)
	}
	return rawKey{kind: rawKeyNone}, nil
}

func parseCSIKey(seq []byte, final byte) rawKey {
	switch final {
	case 'A':
		return rawKey{kind: rawKeyHistoryPrev}
	case 'B':
		return rawKey{kind: rawKeyHistoryNext}
	case 'C':
		return rawKey{kind: rawKeyRight}
	case 'D':
		return rawKey{kind: rawKeyLeft}
	case 'F':
		return rawKey{kind: rawKeyEnd}
	case 'H':
		return rawKey{kind: rawKeyHome}
	case '~':
		switch firstCSIParam(seq) {
		case "1", "7":
			return rawKey{kind: rawKeyHome}
		case "3":
			return rawKey{kind: rawKeyDelete}
		case "4", "8":
			return rawKey{kind: rawKeyEnd}
		}
	}
	return rawKey{kind: rawKeyNone}
}

func isCSIFinal(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

func firstCSIParam(seq []byte) string {
	param := string(seq)
	if before, _, ok := strings.Cut(param, ";"); ok {
		return before
	}
	return param
}
