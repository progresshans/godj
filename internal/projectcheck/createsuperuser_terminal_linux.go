//go:build linux

package projectcheck

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

type createsuperuserTerminalState struct {
	termios unix.Termios
}

func enterCreatesuperuserTerminalPrivateInput(fd int) (*createsuperuserTerminalState, error) {
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	saved := &createsuperuserTerminalState{termios: *state}
	private := *state
	private.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	private.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
	private.Lflag |= unix.ISIG
	private.Cflag &^= unix.CSIZE | unix.PARENB
	private.Cflag |= unix.CS8
	// Poll readiness can race with VINTR flushing the queued input. Keep the
	// following read bounded so the interrupt barrier is always revisited.
	private.Cc[unix.VMIN] = 0
	private.Cc[unix.VTIME] = 1
	private.Cc[unix.VQUIT] = 0
	private.Cc[unix.VSUSP] = 0
	if err := setCreatesuperuserLinuxTermios(fd, unix.TCSETS, &private); err != nil {
		return saved, err
	}
	return saved, flushCreatesuperuserLinuxInput(fd)
}

func restoreCreatesuperuserTerminalAndDiscardInput(fd int, state *createsuperuserTerminalState) error {
	if state == nil {
		return syscall.EINVAL
	}
	flushErr := flushCreatesuperuserLinuxInput(fd)
	restoreErr := setCreatesuperuserLinuxTermios(fd, unix.TCSETS, &state.termios)
	return errors.Join(flushErr, restoreErr)
}

func setCreatesuperuserLinuxTermios(fd int, request uint, state *unix.Termios) error {
	for {
		err := unix.IoctlSetTermios(fd, request, state)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func flushCreatesuperuserLinuxInput(fd int) error {
	for {
		err := unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
