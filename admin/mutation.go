package admin

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
)

// Mutation carries the object selected by the route and the revision observed
// by the form. Revision is zero only for a model without a revision policy.
// The callback must compare it inside its own write transaction; the Site's
// earlier read is not a substitute for that final check.
type Mutation struct {
	ID       int64
	Revision int64
}

type OperationErrorCode string

const (
	OperationConflict       OperationErrorCode = "revision_conflict"
	OperationOutcomeUnknown OperationErrorCode = "outcome_unknown"
	OperationDenied         OperationErrorCode = "permission_denied"
)

// OperationError preserves a callback's classified result and private cause.
// Only this outer type is rendered; joined/wrapped storage failures remain
// execution errors and cannot become a confirmed input rejection.
type OperationError struct {
	Code  OperationErrorCode
	state *operationFailure
}

type operationFailure struct{ cause error }

func NewOperationError(code OperationErrorCode, cause error) *OperationError {
	return &OperationError{Code: code, state: &operationFailure{cause: cause}}
}

func (e *OperationError) Error() string {
	if e == nil {
		return "admin: operation failure"
	}
	switch e.Code {
	case OperationConflict, OperationOutcomeUnknown, OperationDenied:
		return "admin: " + string(e.Code)
	}
	return "admin: invalid operation result"
}
func (OperationError) MarshalJSON() ([]byte, error) {
	return []byte(`"admin.OperationError{redacted}"`), nil
}
func (e OperationError) GoString() string           { return (&e).Error() }
func (e OperationError) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte(e.Error())) }
func (e *OperationError) Unwrap() error {
	if e == nil || e.state == nil {
		return nil
	}
	return e.state.cause
}

func operationResponse(err error) (web.Response, error) {
	if err == ErrObjectNotFound {
		return siteNotFound()
	}
	if failure, ok := err.(*OperationError); ok && failure != nil {
		switch failure.Code {
		case OperationConflict:
			return siteText(http.StatusConflict, "This object changed. Reload it and review the current values before submitting again.\n")
		case OperationOutcomeUnknown:
			return siteText(http.StatusServiceUnavailable, "The outcome is unknown. Check the current stored state before starting another operation. Do not retry automatically.\n")
		case OperationDenied:
			return siteForbidden()
		}
	}
	return web.Response{}, err
}

func (model registeredModel) submittedMutation(id int64, values url.Values) (Mutation, error) {
	mutation := Mutation{ID: id}
	if model.revisionField != "" {
		raw, found := exactValue(values, "expected_revision")
		number, err := strconv.ParseInt(raw, 10, 64)
		if !found || err != nil || strconv.FormatInt(number, 10) != raw {
			return Mutation{}, &ConfigError{Path: "mutation.revision", Code: "invalid"}
		}
		mutation.Revision = number
	}
	return mutation, model.validateMutation(mutation)
}

func (model registeredModel) checkObservedMutation(value Mutation, object Object) error {
	revision, err := model.revision(object)
	if err != nil {
		return err
	}
	if revision != value.Revision {
		return &OperationError{Code: OperationConflict}
	}
	return nil
}

func revisionContext(values map[string]templates.Value, revision int64) {
	values["has_revision"] = templates.Bool(revision != 0)
	values["revision"] = templates.Integer(revision)
}

func (model registeredModel) validateMutation(value Mutation) error {
	if value.ID <= 0 {
		return &ConfigError{Path: "mutation.id", Code: "invalid"}
	}
	if model.revisionField == "" {
		if value.Revision != 0 {
			return &ConfigError{Path: "mutation.revision", Code: "unexpected"}
		}
	} else if value.Revision <= 0 || value.Revision == math.MaxInt64 {
		return &ConfigError{Path: "mutation.revision", Code: "invalid"}
	}
	return nil
}

func (model registeredModel) revision(object Object) (int64, error) {
	if model.revisionField == "" {
		return 0, nil
	}
	value, found := object.Value(model.revisionField)
	if !found || value.Kind() != templates.ValueInteger {
		return 0, &ConfigError{Path: "object.revision", Code: "invalid"}
	}
	number, ok := value.AsInteger()
	if !ok || number <= 0 {
		return 0, &ConfigError{Path: "object.revision", Code: "invalid"}
	}
	return number, nil
}

func (model registeredModel) validateMutationResult(before Mutation, object Object, changed bool) error {
	after, err := model.revision(object)
	if err != nil {
		return err
	}
	if model.revisionField == "" {
		return nil
	}
	if changed && after <= before.Revision || !changed && after != before.Revision {
		return &ConfigError{Path: "mutation.result.revision", Code: "mismatch"}
	}
	return nil
}
