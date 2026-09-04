//go:build darwin

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
	state, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
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
	private.Cc[unix.VQUIT] = 0xff
	private.Cc[unix.VSUSP] = 0xff
	private.Cc[unix.VDSUSP] = 0xff
	if err := setCreatesuperuserDarwinTermios(fd, unix.TIOCSETA, &private); err != nil {
		return saved, err
	}
	return saved, flushCreatesuperuserDarwinInput(fd)
}

func restoreCreatesuperuserTerminalAndDiscardInput(fd int, state *createsuperuserTerminalState) error {
	if state == nil {
		return syscall.EINVAL
	}
	flushErr := flushCreatesuperuserDarwinInput(fd)
	restoreErr := setCreatesuperuserDarwinTermios(fd, unix.TIOCSETA, &state.termios)
	return errors.Join(flushErr, restoreErr)
}

func setCreatesuperuserDarwinTermios(fd int, request uint, state *unix.Termios) error {
	for {
		err := unix.IoctlSetTermios(fd, request, state)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func flushCreatesuperuserDarwinInput(fd int) error {
	for {
		// TIOCFLUSH is encoded as _IOW(..., int) on Darwin, so the kernel
		// expects a pointer to the FREAD/FWRITE mask. x/sys does not expose
		// FREAD, but Darwin's input-only TCIFLUSH value is the same mask bit.
		err := unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
