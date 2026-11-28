//go:build linux

package projectcheck

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func requireCreatesuperuserPrivateTerminalProfile(t *testing.T, terminal *os.File) {
	t.Helper()
	state, err := unix.IoctlGetTermios(int(terminal.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if state.Iflag&(unix.IGNBRK|unix.BRKINT|unix.PARMRK|unix.ISTRIP|unix.INLCR|unix.IGNCR|unix.ICRNL|unix.IXON) != 0 ||
		state.Lflag&(unix.ECHO|unix.ECHONL|unix.ICANON|unix.IEXTEN) != 0 ||
		state.Lflag&unix.ISIG == 0 || state.Cflag&unix.CSIZE != unix.CS8 || state.Cflag&unix.PARENB != 0 ||
		state.Cc[unix.VMIN] != 0 || state.Cc[unix.VTIME] != 1 || state.Cc[unix.VINTR] == 0 ||
		state.Cc[unix.VQUIT] != 0 || state.Cc[unix.VSUSP] != 0 {
		t.Fatalf("unexpected createsuperuser private terminal profile: %+v", *state)
	}
}
