package identityadmin

import (
	"errors"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func operationError(err error) error {
	if failure, ok := err.(*accountmodel.Error); ok && failure != nil {
		switch failure.Code {
		case accountmodel.CodeNotFound:
			return admin.ErrObjectNotFound
		case accountmodel.CodePermission:
			return admin.NewOperationError(admin.OperationDenied, err)
		case accountmodel.CodeConflict:
			return admin.NewOperationError(admin.OperationConflict, err)
		case accountmodel.CodeOutcomeUnknown:
			return admin.NewOperationError(admin.OperationOutcomeUnknown, err)
		}
	}
	if failures, ok := validation.Rejected(err); ok && failures.Len() == 1 {
		item, _ := failures.At(0)
		if protected, ok := errors.Unwrap(err).(*query.ProtectedForeignKeyError); ok && protected != nil && item.Field() == validation.NonField && item.Code() == "protected" {
			return protected
		}
	}
	if failure, ok := err.(*identity.Error); ok && failure != nil {
		switch failure.Code {
		case identity.CodeNotFound:
			return admin.ErrObjectNotFound
		case identity.CodePermission:
			return admin.NewOperationError(admin.OperationDenied, err)
		case identity.CodeConflict:
			return admin.NewOperationError(admin.OperationConflict, err)
		case identity.CodeOutcomeUnknown:
			return admin.NewOperationError(admin.OperationOutcomeUnknown, err)
		}
	}
	return err
}

func checkRevision(expected, actual int64) error {
	if expected != actual {
		return admin.NewOperationError(admin.OperationConflict, nil)
	}
	return nil
}

func passwordError(err error) error {
	return passwordInputError(err, "password2")
}

func passwordInputError(err error, field string) error {
	if failures, ok := validation.Rejected(err); ok {
		items := failures.All()
		for i, item := range items {
			if item.Field() == "password" {
				items[i] = validation.New(validation.Field(field), item.Code(), item.Params()...)
			}
		}
		// The manager owns whether rollback was confirmed. Keep its complete
		// rejection as the cause instead of classifying wrapped storage errors.
		return validation.Reject(validation.NewErrors(items...), err)
	}
	return operationError(err)
}

func notFound(err error) bool {
	value, ok := err.(*identity.Error)
	return ok && value != nil && value.Code == identity.CodeNotFound
}
func invalidInput() error {
	return &admin.ConfigError{Path: "identity.form", Code: "invalid_cleaned_values"}
}
