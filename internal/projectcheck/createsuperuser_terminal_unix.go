//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const terminalPollMilliseconds = 50

func readCreatesuperuserTerminal(
	ctx context.Context,
	interrupt <-chan struct{},
	stdin *os.File,
	stderr io.Writer,
	report *CreatesuperuserReport,
) (username []byte, password []byte, resultFailure *CreatesuperuserFailure) {
	if report == nil {
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInternal, createsuperuserprotocol.CodeProjectInternalError)
		return nil, nil, &failure
	}
	report.TerminalChecks++
	if stdin == nil || !term.IsTerminal(int(stdin.Fd())) {
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputNotTerminal)
		return nil, nil, &failure
	}
	if failure := createsuperuserInputBarrier(ctx, interrupt); failure != nil {
		return nil, nil, failure
	}
	fd := int(stdin.Fd())
	state, err := enterCreatesuperuserTerminalPrivateInput(fd)
	if err != nil || state == nil {
		if state != nil {
			report.TerminalRestoreAttempts++
			_ = restoreCreatesuperuserTerminalAndDiscardInput(fd, state)
		}
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeTerminalStateFailed)
		return nil, nil, &failure
	}
	defer func() {
		report.TerminalRestoreAttempts++
		if err := restoreCreatesuperuserTerminalAndDiscardInput(fd, state); err != nil {
			clear(username)
			clear(password)
			username = nil
			password = nil
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeTerminalStateFailed)
			resultFailure = &failure
		}
	}()

	report.UsernamePromptWrites++
	if !completeCreatesuperuserWrite(stderr, []byte("Username: ")) {
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
		return nil, nil, &failure
	}
	username, overflow, readFailure := readBoundedCreatesuperuserLine(
		ctx,
		interrupt,
		fd,
		createsuperuserprotocol.MaxUsernameBytes,
		stderr,
		createsuperuserTerminalVisibleEcho(stdin, stderr),
	)
	if readFailure != nil {
		clear(username)
		return nil, nil, readFailure
	}
	if overflow || !createsuperuserprotocol.ValidUsername(username) {
		clear(username)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInvalidUsername)
		return nil, nil, &failure
	}

	report.PasswordPromptWrites++
	if !completeCreatesuperuserWrite(stderr, []byte("Password: ")) {
		clear(username)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
		return nil, nil, &failure
	}
	password, overflow, readFailure = readBoundedCreatesuperuserLine(
		ctx, interrupt, fd, createsuperuserprotocol.MaxPasswordBytes, stderr, nil,
	)
	if readFailure != nil {
		clear(username)
		clear(password)
		return nil, nil, readFailure
	}
	if overflow || !createsuperuserprotocol.ValidPassword(password) {
		clear(username)
		clear(password)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInvalidPassword)
		return nil, nil, &failure
	}

	report.ConfirmationPromptWrites++
	if !completeCreatesuperuserWrite(stderr, []byte("Password (again): ")) {
		clear(username)
		clear(password)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
		return nil, nil, &failure
	}
	confirmation, confirmationOverflow, confirmationFailure := readBoundedCreatesuperuserLine(
		ctx, interrupt, fd, createsuperuserprotocol.MaxPasswordBytes, stderr, nil,
	)
	if confirmationFailure != nil {
		clear(username)
		clear(password)
		clear(confirmation)
		return nil, nil, confirmationFailure
	}
	if confirmationOverflow || !createsuperuserprotocol.ValidPassword(confirmation) {
		clear(username)
		clear(password)
		clear(confirmation)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInvalidPassword)
		return nil, nil, &failure
	}
	equal := constantTimeBytesEqual(password, confirmation)
	clear(confirmation)
	if !equal {
		clear(username)
		clear(password)
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodePasswordMismatch)
		return nil, nil, &failure
	}
	return username, password, nil
}

func readBoundedCreatesuperuserLine(
	ctx context.Context,
	interrupt <-chan struct{},
	fd int,
	maximum int,
	output io.Writer,
	visibleEcho io.Writer,
) ([]byte, bool, *CreatesuperuserFailure) {
	retained := make([]byte, 0, maximum)
	unit := []byte{0}
	overflow := false
	for {
		if failure := createsuperuserInputBarrier(ctx, interrupt); failure != nil {
			clear(retained)
			clear(unit)
			return nil, false, failure
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, terminalPollMilliseconds)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			clear(retained)
			clear(unit)
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
			return nil, false, &failure
		}
		if ready == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 ||
			poll[0].Revents&unix.POLLHUP != 0 && poll[0].Revents&unix.POLLIN == 0 {
			clear(retained)
			clear(unit)
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
			return nil, false, &failure
		}
		read, err := unix.Read(fd, unit)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			clear(retained)
			clear(unit)
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
			return nil, false, &failure
		}
		// VMIN=0/VTIME=1 makes a readiness-flush race return zero rather
		// than blocking forever. Revisit the interrupt/context barrier.
		if read == 0 {
			continue
		}
		if read != 1 {
			clear(retained)
			clear(unit)
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
			return nil, false, &failure
		}
		character := unit[0]
		unit[0] = 0
		if character == '\n' || character == '\r' {
			if output != nil && !completeCreatesuperuserWrite(output, []byte("\n")) {
				clear(retained)
				failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
				return nil, false, &failure
			}
			return retained, overflow, nil
		}
		if character == '\b' || character == 0x7f {
			if !overflow && len(retained) > 0 {
				retained = removeLastCreatesuperuserInputRune(retained)
				if visibleEcho != nil && !completeCreatesuperuserWrite(visibleEcho, []byte("\b \b")) {
					clear(retained)
					failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
					return nil, false, &failure
				}
			}
			continue
		}
		if len(retained) < maximum {
			retained = append(retained, character)
			if visibleEcho != nil && (character >= 0x20 || character >= 0x80) &&
				!completeCreatesuperuserWrite(visibleEcho, []byte{character}) {
				clear(retained)
				failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
				return nil, false, &failure
			}
		} else {
			overflow = true
			if output != nil && !completeCreatesuperuserWrite(output, []byte("\n")) {
				clear(retained)
				failure := createsuperuserFailure(createsuperuserprotocol.CategoryInput, createsuperuserprotocol.CodeInputReadFailed)
				return nil, false, &failure
			}
			return retained, overflow, nil
		}
	}
}

func createsuperuserTerminalVisibleEcho(stdin *os.File, stderr io.Writer) io.Writer {
	stderrFile, ok := stderr.(*os.File)
	if !ok || stdin == nil || stderrFile == nil || !term.IsTerminal(int(stderrFile.Fd())) {
		return nil
	}
	var inputIdentity, outputIdentity unix.Stat_t
	if unix.Fstat(int(stdin.Fd()), &inputIdentity) != nil || unix.Fstat(int(stderrFile.Fd()), &outputIdentity) != nil ||
		!sameIdentity(inputIdentity, outputIdentity) {
		return nil
	}
	return stderrFile
}

func removeLastCreatesuperuserInputRune(input []byte) []byte {
	if len(input) == 0 {
		return input
	}
	if utf8.Valid(input) {
		_, size := utf8.DecodeLastRune(input)
		if size > 0 {
			return input[:len(input)-size]
		}
	}
	return input[:len(input)-1]
}

func createsuperuserInputBarrier(ctx context.Context, interrupt <-chan struct{}) *CreatesuperuserFailure {
	if interrupt != nil {
		select {
		case <-interrupt:
			failure := createsuperuserFailure(createsuperuserprotocol.CategoryProcess, createsuperuserprotocol.CodeProjectInterrupted)
			return &failure
		default:
		}
	}
	if ctx != nil && ctx.Err() != nil {
		failure := createsuperuserFailure(createsuperuserprotocol.CategoryProcess, createsuperuserprotocol.CodeProjectCanceled)
		return &failure
	}
	return nil
}

func createsuperuserFailure(category, code string) CreatesuperuserFailure {
	candidate := CreatesuperuserFailure{Category: category, Code: code}
	if _, ok := createsuperuserprotocol.ExitCode(candidate); !ok {
		return CreatesuperuserFailure{
			Category: createsuperuserprotocol.CategoryInternal,
			Code:     createsuperuserprotocol.CodeProjectInternalError,
		}
	}
	return candidate
}

func completeCreatesuperuserWrite(writer io.Writer, document []byte) bool {
	if writer == nil {
		return false
	}
	written, err := writeOnce(writer, document)
	return err == nil && written == len(document)
}

// This avoids an early mismatch branch while comparing two secret inputs. It
// is not a password hash or a replacement for authenticated verification.
func constantTimeBytesEqual(left, right []byte) bool {
	maximum := len(left)
	if len(right) > maximum {
		maximum = len(right)
	}
	difference := len(left) ^ len(right)
	for index := 0; index < maximum; index++ {
		var leftByte, rightByte byte
		if index < len(left) {
			leftByte = left[index]
		}
		if index < len(right) {
			rightByte = right[index]
		}
		difference |= int(leftByte ^ rightByte)
	}
	return difference == 0
}
