//go:build !windows

package tui

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"time"
)

func readInput(timeout time.Duration) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	ready := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(ready, int(timeout.Milliseconds()))
	if errors.Is(err, unix.EINTR) {
		return nil, nil
	}
	if err != nil || n == 0 {
		return nil, err
	}
	var buf [256]byte
	count, err := unix.Read(fd, buf[:])
	if count == 0 && err == nil {
		err = io.EOF
	}
	return buf[:max(count, 0)], err
}

func enableVTOutput() (func(), error) { return func() {}, nil }
