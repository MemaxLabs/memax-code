//go:build !unix

package cli

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

func startAppProgramResizeWatcher(context.Context, *tea.Program, io.Writer) func() {
	return func() {}
}

func sendAppProgramResizeSignalForTest() error {
	return nil
}
