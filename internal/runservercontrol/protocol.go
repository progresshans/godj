// Package runservercontrol owns the small, inherited development-server
// control channel. Application stdout and stderr are never protocol input.
package runservercontrol

import (
	"errors"
	"io"
)

const (
	Version    = 1
	Argument   = "--godj-supervised"
	ListenerFD = 3
	ControlFD  = 4
	Prepared   = "godj-runserver/1 prepared\n"
	Activate   = "godj-runserver/1 activate\n"
	Active     = "godj-runserver/1 active\n"
	Retire     = "godj-runserver/1 retire\n"
	Retired    = "godj-runserver/1 retired\n"
)

var ErrProtocol = errors.New("runserver control: invalid message")

// Expect reads exactly one fixed message. An unexpected byte fails without
// retaining or formatting its contents, and cannot consume unbounded output.
func Expect(reader io.Reader, message string) error {
	for index := range message {
		var next [1]byte
		if _, err := io.ReadFull(reader, next[:]); err != nil {
			return err
		}
		if next[0] != message[index] {
			return ErrProtocol
		}
	}
	return nil
}

func Send(writer io.Writer, message string) error {
	n, err := io.WriteString(writer, message)
	if err == nil && n != len(message) {
		return io.ErrShortWrite
	}
	return err
}
