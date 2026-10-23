package validation

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
