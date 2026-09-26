package admin

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// RelatedChoices owns the authorized query for one editable relation field.
// Permission protects the target rows/labels independently from this model's
// mutation permission. Choices are loaded after authentication/CSRF and before
// binding, then checked again before the write callback. The callback must also
// resolve the submitted key in its transaction to close the remaining race.
type RelatedChoices struct {
	Field      string
	Permission auth.Permission
	Load       func(context.Context, auth.Principal) ([]forms.Choice, error)
}

func prepareRelatedChoices(form forms.Spec, sources []RelatedChoices) (func(context.Context, auth.Principal) (forms.Spec, error), []auth.Permission, error) {
	byName := make(map[string]RelatedChoices, len(sources))
	for _, source := range sources {
		if source.Field == "" || source.Load == nil {
			return nil, nil, &ConfigError{Path: "model.related_choices", Code: "invalid"}
		}
		if _, found := byName[source.Field]; found {
			return nil, nil, &ConfigError{Path: "model.related_choices." + source.Field, Code: "duplicate"}
		}
		if err := validatePermission("model.related_choices."+source.Field+".permission", source.Permission); err != nil {
			return nil, nil, err
		}
		byName[source.Field] = source
	}
	var ordered []RelatedChoices
	var permissions []auth.Permission
	seenPermissions := map[auth.Permission]bool{}
	for _, field := range form.Fields() {
		if !field.ModelChoice() {
			continue
		}
		source, found := byName[field.Name()]
		if !found {
			return nil, nil, &ConfigError{Path: "model.related_choices." + field.Name(), Code: "missing"}
		}
		delete(byName, field.Name())
		ordered = append(ordered, source)
		if !seenPermissions[source.Permission] {
			permissions = append(permissions, source.Permission)
			seenPermissions[source.Permission] = true
		}
	}
	if len(byName) != 0 {
		return nil, nil, &ConfigError{Path: "model.related_choices", Code: "not_selected_relation"}
	}
	resolve := func(ctx context.Context, principal auth.Principal) (forms.Spec, error) {
		for _, permission := range permissions {
			if err := validatePrincipalPermission(ctx, principal, permission); err != nil {
				return forms.Spec{}, err
			}
		}
		result := form
		for _, source := range ordered {
			choices, err := source.Load(ctx, principal)
			if err != nil {
				return forms.Spec{}, errors.Join(err, ctx.Err())
			}
			if err = ctx.Err(); err != nil {
				return forms.Spec{}, err
			}
			result, err = result.WithModelChoices(source.Field, choices...)
			if err != nil {
				return forms.Spec{}, &ConfigError{Path: "model.related_choices." + source.Field, Code: "invalid_result", Cause: err}
			}
		}
		return result, nil
	}
	return resolve, permissions, nil
}

func relatedChoiceRejection(form forms.Form, fields []forms.Field) error {
	if form.Errors().Empty() {
		return nil
	}
	related := map[validation.Field]bool{}
	for _, field := range fields {
		if field.ModelChoice() {
			related[validation.Field(field.Name())] = true
		}
	}
	for _, failure := range form.Errors().All() {
		if !related[failure.Field()] || failure.Code() != "invalid_choice" {
			return nil
		}
	}
	return validation.Reject(form.Errors(), nil)
}

func (site *Site) relatedChoicesAllowed(request *web.Request, principal auth.Principal, model registeredModel) (bool, error) {
	for _, permission := range model.choicePermissions {
		allowed, err := site.permissionGranted(request, principal, permission)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}
