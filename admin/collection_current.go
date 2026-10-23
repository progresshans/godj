package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
)

// CollectionSetAccess retains the request's admitted operations, including the
// Site deny overlay. Current stored authorization remains the writer's policy.
type CollectionSetAccess struct {
	View, Add, Change, Delete bool
	choices                   *inlineChoiceAccess
}

func (access CollectionSetAccess) read() bool { return access.View || access.Change }
func (access CollectionSetAccess) writable() bool {
	return access.Add || access.Change || access.Delete
}
func (access CollectionSetAccess) visible() bool { return access.read() || access.writable() }
func (access CollectionSetAccess) allowedChoices() map[string]bool {
	if access.choices == nil {
		return nil
	}
	return access.choices.allowed
}

// ChoiceAllowed reports the admitted Site deny overlay for a declared target
// field. It is not current actor or object authorization. A readonly resolver
// must keep denied target labels private as well as checking current policy.
func (access CollectionSetAccess) ChoiceAllowed(field string) bool {
	return access.choices == nil || access.choices.allowed[field]
}

// CollectionSetSnapshot owns the complete current allowed cohort. Related is a
// pure reader over already loaded data; it must not hide I/O during binding.
type CollectionSetSnapshot[M any] struct {
	Current []M
	Related func(M, ir.ManyToManyField) ([]int64, bool)
}

// CollectionSetCurrent is returned from the caller's borrowed write scope.
// ResolveChoices runs synchronously only after identity/management/revision
// admission. Resolve each row using preflight.Current/ChoiceKeys; a search page
// or a selection for another row cannot authorize this row's original keys.
type CollectionSetCurrent[M any] struct {
	Rows           CollectionSetSnapshot[M]
	ResolveChoices func(context.Context, formmodel.SetPreflight[M]) (forms.RowModelChoices, error)
}

type ModelCollectionFormSet[M any] struct {
	view        CollectionFormSet
	bindCurrent func(context.Context, CollectionSetAccess, forms.Data, CollectionSetCurrent[M]) (formmodel.InstanceSet[M], error)
}

func (definition ModelCollectionFormSet[M]) View() CollectionFormSet { return definition.view }

// CollectionSetSubmission is issued to one invocation of the registered Run.
// A copy preserves raw data and upload capabilities; it carries no transaction
// or commit proof and cannot be made writable by constructing a zero value.
type CollectionSetSubmission struct {
	declaration, owner *collectionSetToken
	actor              string
	access             CollectionSetAccess
	data               forms.Data
	set                forms.Set
}

func (submission CollectionSetSubmission) FormSet() forms.Set          { return submission.set }
func (submission CollectionSetSubmission) Access() CollectionSetAccess { return submission.access }

// BoundCollectionFormSet contains a current typed result and its admitted
// access. Check Set().Valid before Prepare/SaveFiles/SavePlan or a custom writer.
type BoundCollectionFormSet[M any] struct {
	set                         formmodel.InstanceSet[M]
	access                      CollectionSetAccess
	declaration, owner, current *collectionSetToken
}

func (bound BoundCollectionFormSet[M]) Set() formmodel.InstanceSet[M] { return bound.set }
func (bound BoundCollectionFormSet[M]) Access() CollectionSetAccess   { return bound.access }

// BindCurrent verifies the exact declaration, request and actor before calling
// load exactly once. Load uses the caller's transaction, reads the entire
// current authorized cohort (never just submitted IDs), and returns a resolver
// tied to that same scope. No display loader or read-only Validate callback is
// run. All work using the borrowed scope must finish within its callback.
//
// Pure form/model validation repeats against current state and original input.
// Current object policy, DB constraints and revision-fenced writes remain the
// application's responsibility. The initial deny overlay is never widened.
func (definition ModelCollectionFormSet[M]) BindCurrent(ctx context.Context, actor auth.Principal, submission CollectionSetSubmission, load func(context.Context, CollectionSetAccess) (CollectionSetCurrent[M], error)) (BoundCollectionFormSet[M], error) {
	if ctx == nil {
		return BoundCollectionFormSet[M]{}, &ConfigError{Path: "collection_formset.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return BoundCollectionFormSet[M]{}, err
	}
	if definition.view.token == nil || definition.bindCurrent == nil || load == nil {
		return BoundCollectionFormSet[M]{}, &ConfigError{Path: "collection_formset.current", Code: "uninitialized"}
	}
	if submission.owner == nil || submission.declaration != definition.view.token || !submission.set.Valid() || submission.actor == "" {
		return BoundCollectionFormSet[M]{}, &ConfigError{Path: "collection_formset.submission", Code: "binding_mismatch"}
	}
	if actor.ID() != submission.actor {
		return BoundCollectionFormSet[M]{}, NewOperationError(OperationDenied, nil)
	}
	if err := validateCollectionSetAccess(ctx, actor, submission.access, definition.view); err != nil {
		return BoundCollectionFormSet[M]{}, err
	}
	current, err := load(ctx, submission.access)
	if contextErr := ctx.Err(); contextErr != nil {
		err = errors.Join(err, contextErr)
	}
	if err != nil {
		return BoundCollectionFormSet[M]{}, err
	}
	set, err := definition.bindCurrent(ctx, submission.access, submission.data, current)
	if err != nil {
		return BoundCollectionFormSet[M]{}, err
	}
	return BoundCollectionFormSet[M]{set: set, access: submission.access, declaration: definition.view.token, owner: submission.owner, current: &collectionSetToken{}}, nil
}

func validateCollectionSetAccess(ctx context.Context, actor auth.Principal, access CollectionSetAccess, definition CollectionFormSet) error {
	if ctx == nil {
		return &ConfigError{Path: "collection_formset.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !actor.Authenticated() || !access.visible() {
		return NewOperationError(OperationDenied, nil)
	}
	if access.View && !actor.Has(definition.permissions.View) && !actor.Has(definition.permissions.Change) {
		return NewOperationError(OperationDenied, nil)
	}
	for _, right := range []struct {
		enabled    bool
		permission auth.Permission
	}{{access.Add, definition.permissions.Add}, {access.Change, definition.permissions.Change}, {access.Delete, definition.permissions.Delete}} {
		if right.enabled && !actor.Has(right.permission) {
			return NewOperationError(OperationDenied, nil)
		}
	}
	for _, permission := range definition.additional {
		if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
			return err
		}
	}
	if access.Add || access.Change {
		for _, permission := range definition.choicePermissions {
			if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
				return err
			}
		}
	}
	return nil
}

// CollectionSetWrite names a completed operation in its original submitted row.
// Kind is create/update/delete. An ID is positive and server assigned; update
// and delete IDs must equal that row's current identity.
type CollectionSetWrite struct {
	Index int
	Kind  string
	ID    int64
}

// CollectionSetResult is sealed to the current binding and Run invocation. It
// is a checked write summary, not proof of commit. Return it from Run only once
// the outer transaction confirms commit; any terminal error takes precedence.
type CollectionSetResult struct {
	declaration, owner, current *collectionSetToken
	writes                      []CollectionSetWrite
}

func (result CollectionSetResult) Writes() []CollectionSetWrite { return slices.Clone(result.writes) }

// Result checks exact write intents without publishing files, saving rows or
// assuming an application-specific storage strategy. It accepts ordinary
// SavePlan output or a custom writer such as native bulk creation. No-op and
// read-only rows cannot be reported as writes, and deleted extras are ignored.
// Call it inside the transaction so malformed results can still roll back.
func (bound BoundCollectionFormSet[M]) Result(writes []CollectionSetWrite) (CollectionSetResult, error) {
	failure := func() (CollectionSetResult, error) {
		return CollectionSetResult{}, reconciliationError("collection formset result", &ConfigError{Path: "collection_formset.result", Code: "invalid_writes"})
	}
	if bound.declaration == nil || bound.owner == nil || bound.current == nil || !bound.set.Valid() {
		return failure()
	}
	active, err := bound.set.FormSet().ActiveForms()
	if err != nil {
		return CollectionSetResult{}, err
	}
	deleted, err := bound.set.FormSet().DeletedForms()
	if err != nil {
		return CollectionSetResult{}, err
	}
	expected := make(map[int]CollectionSetWrite)
	surviving := make(map[int64]bool)
	for _, row := range active {
		id, existing := bound.set.Identity(row.Index())
		key, _ := id.AsInteger()
		if existing {
			surviving[key] = true
		}
		if row.Form().ReadOnly() || len(row.Form().Changed()) == 0 {
			continue
		}
		kind := "create"
		if existing {
			kind = "update"
		}
		expected[row.Index()] = CollectionSetWrite{Index: row.Index(), Kind: kind, ID: key}
	}
	for _, row := range deleted {
		id, existing := bound.set.Identity(row.Index())
		if !existing {
			continue
		}
		key, _ := id.AsInteger()
		expected[row.Index()] = CollectionSetWrite{Index: row.Index(), Kind: "delete", ID: key}
	}
	if len(writes) != len(expected) {
		return failure()
	}
	seenIndices, created := make(map[int]bool), make(map[int64]bool)
	for _, write := range writes {
		intent, present := expected[write.Index]
		if !present || seenIndices[write.Index] || write.Kind != intent.Kind || write.ID <= 0 {
			return failure()
		}
		seenIndices[write.Index] = true
		if write.Kind == "create" {
			if created[write.ID] || surviving[write.ID] {
				return failure()
			}
			created[write.ID] = true
		} else if write.ID != intent.ID {
			return failure()
		}
	}
	owned := slices.Clone(writes)
	slices.SortFunc(owned, func(a, b CollectionSetWrite) int { return a.Index - b.Index })
	return CollectionSetResult{declaration: bound.declaration, owner: bound.owner, current: bound.current, writes: owned}, nil
}

func (ModelCollectionFormSet[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.ModelCollectionFormSet{redacted}")
}
func (CollectionSetCurrent[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.CollectionSetCurrent{redacted}")
}
func (CollectionSetSnapshot[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.CollectionSetSnapshot{redacted}")
}
func (CollectionSetSubmission) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.CollectionSetSubmission{redacted}")
}
func (BoundCollectionFormSet[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.BoundCollectionFormSet{redacted}")
}
func (CollectionSetResult) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.CollectionSetResult{redacted}")
}
