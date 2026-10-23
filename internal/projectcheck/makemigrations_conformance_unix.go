//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MakemigrationsConformanceFault selects one closed failure point used by the
// repository's product conformance adapter. It deliberately exposes neither a
// callback nor the publication step vocabulary outside this internal package.
type MakemigrationsConformanceFault uint8

const (
	MakemigrationsConformanceCancelBeforeRename MakemigrationsConformanceFault = iota + 1
	MakemigrationsConformanceFailAfterFirstCandidate
)

// MakemigrationsConformanceFinalCatalogBarrier is a closed two-writer
// rendezvous used by the repository's concurrent-publication product adapter.
// It exposes no arbitrary callback or publication vocabulary and is available
// only through Go's internal-package boundary.
type MakemigrationsConformanceFinalCatalogBarrier struct {
	mu       sync.Mutex
	release  chan struct{}
	timeout  time.Duration
	claims   int
	arrivals int
	aborted  bool
	closed   bool
	cancels  [2]context.CancelFunc
}

const makemigrationsConformanceFinalCatalogTimeout = 30 * time.Second

// NewMakemigrationsConformanceFinalCatalogBarrier creates one barrier for
// exactly two concurrent RunMakemigrationsConformanceFinalCatalog calls.
func NewMakemigrationsConformanceFinalCatalogBarrier() *MakemigrationsConformanceFinalCatalogBarrier {
	return newMakemigrationsConformanceFinalCatalogBarrier(makemigrationsConformanceFinalCatalogTimeout)
}

func newMakemigrationsConformanceFinalCatalogBarrier(timeout time.Duration) *MakemigrationsConformanceFinalCatalogBarrier {
	return &MakemigrationsConformanceFinalCatalogBarrier{
		release: make(chan struct{}),
		timeout: timeout,
	}
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) claim(
	parent context.Context,
) (context.Context, context.CancelFunc, error) {
	if barrier == nil || barrier.release == nil || barrier.timeout <= 0 {
		return nil, nil, errors.New("makemigrations conformance: uninitialized final catalog barrier")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	barrier.mu.Lock()
	if barrier.aborted || barrier.claims >= len(barrier.cancels) {
		tooMany := barrier.claims >= len(barrier.cancels)
		barrier.mu.Unlock()
		cancel()
		if tooMany {
			barrier.abort()
		}
		return nil, nil, errors.New("makemigrations conformance: final catalog barrier requires exactly two writers")
	}
	barrier.cancels[barrier.claims] = cancel
	barrier.claims++
	barrier.mu.Unlock()
	return ctx, cancel, nil
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) abort() {
	barrier.mu.Lock()
	barrier.aborted = true
	if !barrier.closed {
		close(barrier.release)
		barrier.closed = true
	}
	cancels := barrier.cancels
	barrier.mu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) abortIfIncomplete() {
	barrier.mu.Lock()
	if barrier.aborted || barrier.arrivals == len(barrier.cancels) {
		barrier.mu.Unlock()
		return
	}
	barrier.aborted = true
	if !barrier.closed {
		close(barrier.release)
		barrier.closed = true
	}
	cancels := barrier.cancels
	barrier.mu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) arrive(
	ctx context.Context,
	interrupt <-chan struct{},
) bool {
	barrier.mu.Lock()
	if barrier.aborted {
		barrier.mu.Unlock()
		return false
	}
	barrier.arrivals++
	if barrier.arrivals > len(barrier.cancels) {
		barrier.mu.Unlock()
		barrier.abort()
		return false
	}
	if barrier.arrivals == len(barrier.cancels) && !barrier.closed {
		close(barrier.release)
		barrier.closed = true
	}
	release := barrier.release
	timeout := barrier.timeout
	barrier.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-release:
	case <-ctx.Done():
		barrier.abort()
	case <-interrupt:
		barrier.abort()
	case <-timer.C:
		barrier.abortIfIncomplete()
	}
	barrier.mu.Lock()
	ok := !barrier.aborted && barrier.arrivals == len(barrier.cancels)
	barrier.mu.Unlock()
	return ok
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) complete() bool {
	barrier.mu.Lock()
	defer barrier.mu.Unlock()
	return !barrier.aborted && barrier.claims == len(barrier.cancels) && barrier.arrivals == len(barrier.cancels)
}

func (barrier *MakemigrationsConformanceFinalCatalogBarrier) arrivalCount() int {
	barrier.mu.Lock()
	defer barrier.mu.Unlock()
	return barrier.arrivals
}

// RunMakemigrationsConformanceFinalCatalog runs one member of a closed
// two-writer pair. Both writers must capture the same final pre-lock catalog
// before either may enter publication. A member that exits before that point
// releases its peer and reports a conformance-harness error instead of hanging.
func RunMakemigrationsConformanceFinalCatalog(
	input MakemigrationsInvocation,
	barrier *MakemigrationsConformanceFinalCatalogBarrier,
) (MakemigrationsReport, error) {
	if barrier == nil {
		return MakemigrationsReport{}, errors.New("makemigrations conformance: nil final catalog barrier")
	}
	ctx, cancel, err := barrier.claim(input.Context)
	if err != nil {
		return MakemigrationsReport{}, err
	}
	defer cancel()
	input.Context = ctx
	reached := false
	input.afterFinalCatalogSnapshot = func() {
		if reached {
			barrier.abort()
			cancel()
			return
		}
		reached = true
		if !barrier.arrive(ctx, input.Interrupt) {
			cancel()
		}
	}
	report := RunMakemigrations(input)
	if !reached {
		barrier.abort()
		return report, errors.New("makemigrations conformance: writer did not reach final catalog barrier")
	}
	if !barrier.complete() {
		barrier.abort()
		return report, errors.New("makemigrations conformance: incomplete final catalog barrier")
	}
	return report, nil
}

// RunMakemigrationsConformanceFault runs the real global writer with one
// bounded conformance-only fault. Production callers continue to use
// RunMakemigrations and cannot install arbitrary publication hooks.
func RunMakemigrationsConformanceFault(
	input MakemigrationsInvocation,
	fault MakemigrationsConformanceFault,
) (MakemigrationsReport, error) {
	switch fault {
	case MakemigrationsConformanceCancelBeforeRename:
		ctx := input.Context
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		input.Context = ctx
		input.publication = makemigrationsPublicationHooks{after: func(
			step makemigrationsPublicationStep,
			_ string,
			_ int,
		) error {
			if step == makemigrationsStepTempFsynced {
				cancel()
			}
			return nil
		}}
	case MakemigrationsConformanceFailAfterFirstCandidate:
		input.publication = makemigrationsPublicationHooks{after: func(
			step makemigrationsPublicationStep,
			_ string,
			index int,
		) error {
			if step == makemigrationsStepCandidateCommitted && index == 0 {
				return errors.New("makemigrations conformance: injected failure after first candidate")
			}
			return nil
		}}
	default:
		return MakemigrationsReport{}, errors.New("makemigrations conformance: invalid fault selector")
	}
	return RunMakemigrations(input), nil
}
