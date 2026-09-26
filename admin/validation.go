package admin

import (
	"context"
	"errors"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func formWithRejection(ctx context.Context, form forms.Form, err error) (forms.Form, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return forms.Form{}, errors.Join(err, contextErr)
	}
	diagnostics, rejected := validation.Rejected(err)
	if !rejected {
		return forms.Form{}, err
	}
	return form.WithErrors(diagnostics)
}
