package sessions

import "time"

// AccessPolicy is the immutable validation and sliding-expiry policy used by
// one atomic Store.Access. Its clock runs only after a present record passes
// validation, while the store owns its atomic boundary. The clock must return
// promptly and must not perform I/O or reenter a manager or store.
type AccessPolicy struct {
	idleTimeout time.Duration
	limits      Limits
	clock       func() time.Time
}

func NewAccessPolicy(idleTimeout time.Duration, limits Limits, clock func() time.Time) (AccessPolicy, error) {
	if idleTimeout <= 0 || idleTimeout > maximumLifetime {
		return AccessPolicy{}, &Error{Code: CodeInvalidConfig, Field: "idle_timeout", Detail: "idle timeout is outside the supported range"}
	}
	normalized, err := normalizeLimits(limits)
	if err != nil {
		return AccessPolicy{}, err
	}
	if clock == nil {
		clock = time.Now
	}
	return AccessPolicy{idleTimeout: idleTimeout, limits: normalized, clock: clock}, nil
}

// Apply validates the current authoritative record before sampling the clock
// or deriving any mutation. Stores must propagate its error without writing.
func (policy AccessPolicy) Apply(id ID, current Record) (Record, AccessStatus, error) {
	if policy.clock == nil || policy.idleTimeout <= 0 {
		return Record{}, AccessMissing, &accessPolicyError{&Error{Code: CodeInvalidConfig, Field: "access_policy", Detail: "access policy is uninitialized"}}
	}
	if !current.valid(policy.limits) || current.id != id {
		return Record{}, AccessMissing, &accessPolicyError{&Error{Code: CodeInvalidRecord, Detail: "store returned an invalid session record"}}
	}
	now := canonicalTime(policy.clock())
	if now.IsZero() {
		return Record{}, AccessMissing, &accessPolicyError{&Error{Code: CodeInvalidConfig, Field: "clock", Detail: "clock returned the zero time"}}
	}
	if now.Before(current.accessedAt) {
		now = current.accessedAt
	}
	return current.Touch(now, minimumTime(now.Add(policy.idleTimeout), current.absoluteExpiresAt))
}

// Preserve Manager's policy-error authority even though validation now runs
// inside the store. Storage errors still receive the redacted store wrapper.
type accessPolicyError struct{ cause *Error }

func (err *accessPolicyError) Error() string { return err.cause.Error() }
func (err *accessPolicyError) Unwrap() error { return err.cause }
