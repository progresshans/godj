package wirejson

import (
	"errors"
	"io"
	"math"
)

// OverflowPolicy selects whether later transport errors still take precedence
// once the retained document proves that the protocol byte limit was exceeded.
type OverflowPolicy uint8

const (
	DrainToEOF OverflowPolicy = iota
	StopOnOverflow
)

// Read retains at most maximum+1 bytes. The extra byte proves overflow without
// discarding the caller's choice of transport-error precedence.
func Read(reader io.Reader, maximum int, policy OverflowPolicy) ([]byte, error) {
	if reader == nil || maximum < 0 || maximum == math.MaxInt || policy > StopOnOverflow {
		return nil, errors.New("invalid bounded wire reader")
	}
	retained := make([]byte, 0, min(maximum+1, 32<<10))
	buffer := make([]byte, 32<<10)
	emptyReads := 0
	for {
		count, err := reader.Read(buffer)
		if count < 0 || count > len(buffer) {
			return nil, errors.New("invalid wire reader count")
		}
		if count > 0 {
			emptyReads = 0
			remaining := maximum + 1 - len(retained)
			retained = append(retained, buffer[:min(count, remaining)]...)
		} else if err == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return retained, nil
			}
			return nil, err
		}
		if len(retained) > maximum && policy == StopOnOverflow {
			return retained, nil
		}
	}
}
