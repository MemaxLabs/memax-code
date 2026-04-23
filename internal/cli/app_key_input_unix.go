//go:build !windows

package cli

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type terminalRawKeyPoller struct {
	file    *os.File
	fd      int
	state   *term.State
	flags   int
	decoder rawKeyDecoder
}

func newTerminalRawKeyPoller(file *os.File) (*terminalRawKeyPoller, error) {
	if file == nil {
		return nil, nil
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		_ = term.Restore(fd, state)
		return nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = term.Restore(fd, state)
		return nil, err
	}
	return &terminalRawKeyPoller{
		file:  file,
		fd:    fd,
		state: state,
		flags: flags,
	}, nil
}

func (p *terminalRawKeyPoller) Poll() ([]rawKey, error) {
	var keys []rawKey
	var buf [64]byte
	for {
		n, err := p.file.Read(buf[:])
		if n > 0 {
			keys = append(keys, p.decoder.Append(buf[:n])...)
		}
		if n == 0 && err == nil {
			return keys, nil
		}
		if err == nil {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return keys, nil
		}
		if errors.Is(err, io.EOF) {
			return keys, nil
		}
		return keys, err
	}
}

func (p *terminalRawKeyPoller) Close() error {
	var firstErr error
	if _, err := unix.FcntlInt(uintptr(p.fd), unix.F_SETFL, p.flags); err != nil {
		firstErr = err
	}
	if err := term.Restore(p.fd, p.state); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
