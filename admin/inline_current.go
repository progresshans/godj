package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

// InlineCurrent is a freshly authorized snapshot in the writer's borrowed
// scope. Children.Related reads only the supplied snapshot. ResolveChoices is
// invoked synchronously after parent/cohort/management admission, while that
// scope is still borrowed. It resolves each admitted row separately; a search
// page or another row's selections must never authorize this row. No callback
// is retained by the bound result.
type InlineCurrent[P, C any] struct {
	Parent         P
	Children       InlineSnapshot[C]
	ResolveChoices func(context.Context, formmodel.InlinePreflight[P, C]) (forms.RowModelChoices, error)
}

// ModelInline retains the typed form and canonical relation policy used by
// its Admin view. It is immutable and safe for concurrent requests. It owns no
// database scope, current model values, file publication or commit outcome.
type ModelInline[P, C any] struct {
	view        Inline
	bindCurrent func(context.Context, int64, InlineAccess, forms.Data, InlineCurrent[P, C]) (formmodel.InlineSet[P, C], error)
}

// View supplies the type-erased adapter for a parent's heterogeneous Inlines.
// Keep the typed declaration to rebind the admitted submission in the writer.
func (inline ModelInline[P, C]) View() Inline { return inline.view }

// BoundInline keeps the same admitted access as the original request. Set may
// contain current validation errors; check Valid before preparing any writes.
type BoundInline[P, C any] struct {
	set    formmodel.InlineSet[P, C]
	access InlineAccess
}

func (bound BoundInline[P, C]) Set() formmodel.InlineSet[P, C] { return bound.set }
func (bound BoundInline[P, C]) Access() InlineAccess           { return bound.access }

// BindCurrent is called from the registered parent's writer with its checked
// submission and the current principal. It checks the declaration, actor and
// admitted operation rights before calling load exactly once, synchronously.
// The loader must use the caller's transaction and return the current parent,
// complete child cohort and a row-scoped selection resolver. The resolver is
// skipped for rejected admission. No initial view
// loader or read-only Validate callback is invoked here.
// The supplied principal keeps the application's authentication policy; this
// method does not reload credentials or retroactively revoke admitted work.
//
// Binding repeats pure form/model validation with the current snapshot and
// preserves the original raw input and file capabilities. The caller still
// owns current object/DB constraints, revision-fenced writes, collections,
// deletion, audit, file outcomes and the single outer transaction. A returned
// form, including one with no changes, is never proof of write authority or
// commit. Work using a borrowed session must finish before its callback ends.
func (inline ModelInline[P, C]) BindCurrent(ctx context.Context, actor auth.Principal, submission InlineSubmission, load func(context.Context, InlineAccess) (InlineCurrent[P, C], error)) (BoundInline[P, C], bool, error) {
	if ctx == nil {
		return BoundInline[P, C]{}, false, &ConfigError{Path: "inline.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return BoundInline[P, C]{}, false, err
	}
	if inline.view.token == nil || inline.bindCurrent == nil || load == nil {
		return BoundInline[P, C]{}, false, &ConfigError{Path: "inline.current", Code: "uninitialized"}
	}
	if submission.owner == nil || !submission.valid() || submission.writerActor == "" {
		return BoundInline[P, C]{}, false, &ConfigError{Path: "inline.submission", Code: "not_checked_for_writer"}
	}
	if !actor.Authenticated() || submission.writerActor != actor.ID() {
		return BoundInline[P, C]{}, false, NewOperationError(OperationDenied, nil)
	}
	var selected *inlineBound
	for index := range submission.entries {
		entry := &submission.entries[index]
		if entry.definition.prefix != inline.view.prefix {
			continue
		}
		if selected != nil || entry.definition.token != inline.view.token || entry.parentID != submission.parentID || !entry.set.Bound() {
			return BoundInline[P, C]{}, false, &ConfigError{Path: "inline.submission", Code: "binding_mismatch"}
		}
		selected = entry
	}
	if selected == nil {
		return BoundInline[P, C]{}, false, nil
	}
	if err := validateInlineAccess(ctx, actor, submission.parentID, selected.access, inline.view); err != nil {
		return BoundInline[P, C]{}, false, err
	}
	current, err := load(ctx, selected.access)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = errors.Join(err, contextErr)
		}
		return BoundInline[P, C]{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return BoundInline[P, C]{}, false, err
	}
	set, err := inline.bindCurrent(ctx, submission.parentID, selected.access, selected.set.Submitted(), current)
	if err != nil {
		return BoundInline[P, C]{}, false, err
	}
	return BoundInline[P, C]{set: set, access: selected.access}, true, nil
}

func validateInlineAccess(ctx context.Context, actor auth.Principal, parentID int64, access InlineAccess, definition Inline) error {
	if ctx == nil {
		return &ConfigError{Path: "inline.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !actor.Authenticated() || parentID < 0 || !access.visible() {
		return NewOperationError(OperationDenied, nil)
	}
	if access.View {
		if !actor.Has(definition.permissions.View) && !actor.Has(definition.permissions.Change) {
			return NewOperationError(OperationDenied, nil)
		}
	}
	for _, right := range []struct {
		enabled    bool
		permission auth.Permission
	}{{access.Add, definition.permissions.Add}, {access.Change, definition.permissions.Change}, {access.Delete, definition.permissions.Delete}} {
		if right.enabled && !actor.Has(right.permission) {
			return NewOperationError(OperationDenied, nil)
		}
	}
	if access.Add || access.Change {
		for _, permission := range definition.choicePermissions {
			if !actor.Has(permission) {
				return NewOperationError(OperationDenied, nil)
			}
		}
	}
	return nil
}

func (ModelInline[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.ModelInline{redacted}")
}
func (InlineCurrent[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.InlineCurrent{redacted}")
}
func (BoundInline[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.BoundInline{redacted}")
}
