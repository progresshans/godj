package validation

import "errors"

// Reject reports an expected input rejection. Callers may retain an underlying
// cause for diagnostics without placing its text in the presentation message.
// Empty diagnostics return cause unchanged, including nil.
//
// A rejection is renderable only when it reaches the consumer unchanged. If a
// transaction adds a rollback failure or an owner wraps it as an execution or
// reconciliation error, Rejected must not turn that failure into user input.
func Reject(diagnostics Errors, cause error) error {
	if diagnostics.Empty() {
		return cause
	}
	return &rejection{diagnostics: diagnostics, cause: cause}
}

type rejection struct {
	diagnostics Errors
	cause       error
	origin      *RejectionOrigin
}

func (*rejection) Error() string   { return "input validation rejected" }
func (r *rejection) Unwrap() error { return r.cause }

// Rejected recognizes only a directly returned input rejection. It deliberately
// does not search a wrapped or joined error tree: an additional owner or failure
// may make the operation's outcome unknown. Such errors remain execution errors.
func Rejected(err error) (Errors, bool) {
	r, ok := err.(*rejection)
	if !ok || r == nil {
		return Errors{}, false
	}
	return r.diagnostics, true
}

// RejectionOrigin is an opaque diagnostic namespace owned by one producer.
// Allocate a distinct origin with new(RejectionOrigin). An origin is metadata,
// not an execution failure or authority; marked rejections still have no cause.
// Its nonzero size ensures separately allocated origins have distinct identity.
type RejectionOrigin struct{ marker byte }

// Reject marks value-free diagnostics as a direct input rejection. An empty
// diagnostic set succeeds. A nil origin cannot issue a marked rejection.
func (origin *RejectionOrigin) Reject(diagnostics Errors) error {
	if diagnostics.Empty() {
		return nil
	}
	if origin == nil {
		return errors.New("validation: rejection origin is nil")
	}
	return &rejection{diagnostics: diagnostics, origin: origin}
}

// Diagnostics identifies only a directly returned rejection from this exact
// origin. Wrapping or joining adds an owner/failure and deliberately prevents
// an adapter from reclassifying that result as input.
func (origin *RejectionOrigin) Diagnostics(err error) (Errors, bool) {
	r, ok := err.(*rejection)
	if origin == nil || !ok || r == nil || r.origin != origin {
		return Errors{}, false
	}
	return r.diagnostics, true
}
