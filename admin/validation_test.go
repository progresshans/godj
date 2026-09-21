package admin

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestFormWithRejectionPreservesFailuresAndChecksSelectedFields(t *testing.T) {
	field, err := forms.CharField("title")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Bind(forms.NewData(map[string][]string{"title": {"raw"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	rejection := validation.Reject(validation.NewErrors(validation.New("title", validation.CodeUnique)), nil)
	with, err := formWithRejection(t.Context(), form, rejection)
	if err != nil || with.Valid() || with.Errors().Len() != 1 || !form.Valid() {
		t.Fatal("confirmed rejection was not applied", err)
	}
	for _, failure := range []error{errors.New("database failure"), errors.Join(rejection, errors.New("rollback failed")), fmt.Errorf("wrapped execution: %w", rejection)} {
		if _, err := formWithRejection(t.Context(), form, failure); err != failure {
			t.Fatal("execution owner lost", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := formWithRejection(ctx, form, rejection); !errors.Is(err, context.Canceled) || !errors.Is(err, rejection) {
		t.Fatal("cancellation was rendered as input", err)
	}
	if _, err := formWithRejection(t.Context(), form, validation.Reject(validation.NewErrors(validation.New("hidden", validation.CodeUnique)), nil)); err == nil {
		t.Fatal("unselected diagnostic exposed")
	}
}
