//go:build unix

package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func startAppProgramResizeWatcher(ctx context.Context, program *tea.Program, output io.Writer) func() {
	file, ok := output.(*os.File)
	if !ok || program == nil {
		return func() {}
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return func() {}
	}

	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	signal.Notify(signals, unix.SIGWINCH)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			// Bubble Tea closes its message channel when the program exits. A
			// SIGWINCH can race with shutdown, so a late Send must not take down
			// the whole CLI while restore defers are running.
			_ = recover()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-signals:
				width, height, err := term.GetSize(fd)
				if err != nil || width <= 0 || height <= 0 {
					continue
				}
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				default:
				}
				// Bubble Tea also emits WindowSizeMsg, but the explicit live
				// renderer needs a terminal-backed signal path while the standard
				// renderer is disabled. Duplicate resize messages are harmless:
				// appProgramModel resize handling is idempotent.
				program.Send(tea.WindowSizeMsg{Width: width, Height: height})
			}
		}
	}()

	return func() {
		once.Do(func() {
			signal.Stop(signals)
			close(done)
			wg.Wait()
		})
	}
}

func sendAppProgramResizeSignalForTest() error {
	return unix.Kill(unix.Getpid(), unix.SIGWINCH)
}
