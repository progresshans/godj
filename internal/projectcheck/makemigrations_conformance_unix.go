//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
)

// MakemigrationsConformanceFault selects one closed failure point used by the
// repository's product conformance adapter. It deliberately exposes neither a
// callback nor the publication step vocabulary outside this internal package.
type MakemigrationsConformanceFault uint8

const (
	MakemigrationsConformanceCancelBeforeRename MakemigrationsConformanceFault = iota + 1
	MakemigrationsConformanceFailAfterFirstCandidate
)

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
