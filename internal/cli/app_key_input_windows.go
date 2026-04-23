//go:build windows

package cli

import "os"

type terminalRawKeyPoller struct{}

func newTerminalRawKeyPoller(file *os.File) (*terminalRawKeyPoller, error) {
	return nil, nil
}

func (p *terminalRawKeyPoller) Poll() ([]rawKey, error) {
	return nil, nil
}

func (p *terminalRawKeyPoller) Close() error {
	return nil
}
