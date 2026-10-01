//go:build !darwin && !linux

package multiruntimeworker

import "context"

// RunScenario remains explicit on platforms where os/exec does not support
// inherited anonymous descriptors. Hosted PostgreSQL attestation runs on
// Linux; portable conformance consumes the source-bound attestation instead.
func RunScenario(context.Context, string, DatabaseConfig) (Facts, error) {
	return Facts{}, newError(CodeUnsupported)
}
