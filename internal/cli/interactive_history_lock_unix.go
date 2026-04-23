//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockPromptHistory(path string) (func(), error) {
	lockFile := path + ".lock"
	file, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open prompt history lock %s: %w", lockFile, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock prompt history %s: %w", lockFile, err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func lockPromptHistoryIfPresent(path string) (func(), error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			// Load is a startup snapshot; if another shell creates the file just
			// after this check, this shell can safely miss that new prompt until
			// the next restart.
			return func() {}, nil
		}
		return nil, fmt.Errorf("stat prompt history %s: %w", path, err)
	}
	unlock, err := lockPromptHistory(path)
	if err != nil {
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS) {
			return func() {}, nil
		}
		return nil, err
	}
	return unlock, nil
}
