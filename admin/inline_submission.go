package admin

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/web"
)

// InlineDescriptor describes the static view without exposing its loader.
type InlineDescriptor struct {
	Prefix, Label, RevisionField string
	Model                        ir.Model
	FormFields                   []forms.Field
	Config                       forms.SetConfig
	Permissions                  Permissions
}

func prepareInlines(input []Inline, installed apps.Registry, appLabel string, model ir.Model, formsForParent ...forms.Spec) ([]Inline, error) {
	if len(input) > MaximumInlines {
		return nil, &ConfigError{Path: "model.inlines", Code: "limit_exceeded"}
	}
	names := map[string]bool{"csrfmiddlewaretoken": true, "expected_revision": true}
	for _, field := range model.Fields {
		names[field.Name] = true
	}
	for _, field := range model.ManyToMany {
		names[field.Name] = true
	}
	for _, form := range formsForParent {
		for _, field := range form.Fields() {
			names[field.Name()] = true
		}
	}
	rows := 0
	for _, inline := range input {
		if inline.token == nil || inline.bind == nil || !inline.parent.Equal(model) || inline.parentIdentity.AppLabel != appLabel || inline.parentIdentity.ModelName != model.Name {
			return nil, &ConfigError{Path: "model.inlines", Code: "parent_mismatch"}
		}
		if _, ok := installed.Lookup(inline.childIdentity.AppLabel); !ok {
			return nil, &ConfigError{Path: "model.inlines", Code: "child_not_installed"}
		}
		if !validIdentifier(inline.prefix, MaximumModelBytes) {
			return nil, &ConfigError{Path: "model.inlines", Code: "invalid_prefix"}
		}
		if names[inline.prefix] {
			return nil, &ConfigError{Path: "model.inlines", Code: "prefix_conflict"}
		}
		names[inline.prefix] = true
		rows += inline.config.AbsoluteMax
		if rows > MaximumInlineRows {
			return nil, &ConfigError{Path: "model.inlines", Code: "row_limit_exceeded"}
		}
	}
	return slices.Clone(input), nil
}

func inlineDescriptors(inlines []Inline) []InlineDescriptor {
	result := make([]InlineDescriptor, len(inlines))
	for i, inline := range inlines {
		result[i] = InlineDescriptor{Prefix: inline.prefix, Label: inline.label, RevisionField: inline.revision, Model: inline.child.Clone(), FormFields: slices.Clone(inline.fields), Config: inline.config, Permissions: inline.permissions}
	}
	return result
}

func (model registeredModel) checkedInlines(ctx context.Context, actor auth.Principal, parentID int64, submission InlineSubmission) (InlineSubmission, error) {
	if len(model.inlines) == 0 {
		if submission.owner != nil || len(submission.entries) != 0 {
			return InlineSubmission{}, &ConfigError{Path: "inline.submission", Code: "unexpected"}
		}
		return InlineSubmission{}, nil
	}
	if submission.owner != model.inlineOwner || submission.parentID != parentID || !submission.valid() {
		return InlineSubmission{}, &ConfigError{Path: "inline.submission", Code: "not_bound_valid"}
	}
	seen := map[*inlineToken]bool{}
	for _, entry := range submission.entries {
		if entry.parentID != parentID || !entry.set.Bound() || !slices.ContainsFunc(model.inlines, func(inline Inline) bool { return inline.token == entry.definition.token }) || seen[entry.definition.token] {
			return InlineSubmission{}, &ConfigError{Path: "inline.submission", Code: "binding_mismatch"}
		}
		seen[entry.definition.token] = true
		if entry.access.View {
			if err := validatePrincipalRead(ctx, actor, entry.definition.permissions); err != nil {
				return InlineSubmission{}, err
			}
		}
		for _, right := range []struct {
			enabled    bool
			permission auth.Permission
		}{{entry.access.Add, entry.definition.permissions.Add}, {entry.access.Change, entry.definition.permissions.Change}, {entry.access.Delete, entry.definition.permissions.Delete}} {
			if right.enabled {
				if err := validatePrincipalPermission(ctx, actor, right.permission); err != nil {
					return InlineSubmission{}, err
				}
			}
		}
	}
	// Read-only validation is not write authority. The single application
	// writer rechecks current rows/choices in its own authorized transaction.
	return submission, nil
}

func (site *Site) loadInlines(request *web.Request, actor auth.Principal, model registeredModel, parentID int64, submitted url.Values, editable bool) (InlineSubmission, error) {
	if len(model.inlines) == 0 {
		return InlineSubmission{}, nil
	}
	result := InlineSubmission{owner: model.inlineOwner, parentID: parentID}
	for _, inline := range model.inlines {
		access := InlineAccess{}
		for _, right := range []struct {
			target     *bool
			permission auth.Permission
		}{{&access.View, inline.permissions.View}, {&access.Add, inline.permissions.Add}, {&access.Change, inline.permissions.Change}, {&access.Delete, inline.permissions.Delete}} {
			allowed, err := site.permissionGranted(request, actor, right.permission)
			if err != nil {
				return InlineSubmission{}, err
			}
			*right.target = allowed
		}
		access.View = access.read()
		if !editable {
			access.Add, access.Change, access.Delete = false, false, false
		}
		values := map[string][]string{}
		for name, raw := range submitted {
			if strings.HasPrefix(name, inline.prefix+"-") {
				values[name] = raw
			}
		}
		if !access.visible() {
			if len(values) != 0 {
				return InlineSubmission{}, NewOperationError(OperationDenied, nil)
			}
			continue
		}
		if access.Add || access.Change {
			for _, permission := range inline.choicePermissions {
				allowed, err := site.permissionGranted(request, actor, permission)
				if err != nil {
					return InlineSubmission{}, err
				}
				if !allowed {
					return InlineSubmission{}, NewOperationError(OperationDenied, nil)
				}
			}
		}
		var data *forms.Data
		if submitted != nil {
			value := forms.NewData(values)
			data = &value
		}
		bound, err := inline.bind(request.Context(), actor, parentID, access, data)
		if err != nil {
			return InlineSubmission{}, err
		}
		result.entries = append(result.entries, bound)
	}
	return result, nil
}

func (submission InlineSubmission) withRejection(ctx context.Context, err error) (InlineSubmission, bool, error) {
	rejection, ok := err.(*InlineRejection)
	if !ok || rejection == nil {
		return submission, false, nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return InlineSubmission{}, true, errors.Join(err, contextErr)
	}
	entries := slices.Clone(submission.entries)
	for i, entry := range entries {
		if entry.definition.prefix != rejection.prefix {
			continue
		}
		var applyErr error
		entries[i], applyErr = applyInlineRejection(entry, rejection)
		if applyErr != nil {
			return InlineSubmission{}, true, applyErr
		}
		submission.entries = entries
		return submission, true, nil
	}
	return InlineSubmission{}, true, &ConfigError{Path: "inline.rejection", Code: "unknown_inline"}
}

func inlineFormRejection(ctx context.Context, form forms.Form, submission InlineSubmission, failure error) (forms.Form, InlineSubmission, error) {
	updated, handled, err := submission.withRejection(ctx, failure)
	if handled {
		return form, updated, err
	}
	form, err = formWithRejection(ctx, form, failure)
	return form, submission, err
}
