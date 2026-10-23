package admin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// ChoiceOperation is the registered Admin operation requesting target access.
type ChoiceOperation string

const (
	ChoiceAdd           ChoiceOperation = "add"
	ChoiceChange        ChoiceOperation = "change"
	ChoiceInline        ChoiceOperation = "inline"
	ChoiceCollection    ChoiceOperation = "collection"
	ChoiceCollectionSet ChoiceOperation = "collection-set"
)

// ChoiceOrigin is constructed only after source and current identity admission.
// Row positions are never authority. Applications must recheck stored policy in Scope.
type ChoiceOrigin struct {
	source    ir.ModelIdentity
	operation ChoiceOperation
	objectID  int64
	parent    ir.ModelIdentity
	parentID  int64
	rowID     int64
	name      string
	valid     bool
}

func (origin ChoiceOrigin) Source() ir.ModelIdentity   { return origin.source }
func (origin ChoiceOrigin) Operation() ChoiceOperation { return origin.operation }
func (origin ChoiceOrigin) ObjectID() (int64, bool)    { return origin.objectID, origin.objectID != 0 }
func (origin ChoiceOrigin) Parent() (ir.ModelIdentity, int64, bool) {
	return origin.parent, origin.parentID, origin.parent.ModelName != ""
}
func (origin ChoiceOrigin) RowIdentity() (int64, bool) { return origin.rowID, origin.rowID != 0 }
func (origin ChoiceOrigin) Name() string               { return origin.name }
func (ChoiceOrigin) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.ChoiceOrigin{redacted}")
}

// RelatedChoices is sealed: selected-key resolution and paged search retain
// their canonical ModelChoices binding and cannot be replaced by an unbounded loader.
type RelatedChoices interface {
	choiceField() string
	choicePermission() auth.Permission
	choiceMetadata() formmodel.ModelChoiceMetadata
	resolve(context.Context, auth.Principal, ChoiceOrigin, []int64) ([]forms.Choice, error)
	search(context.Context, auth.Principal, ChoiceOrigin, formmodel.ChoiceRequest) (formmodel.ChoicePage, error)
}

type RelatedChoiceConfig[T any] struct {
	Field      string
	Choices    formmodel.ModelChoices[T]
	Permission auth.Permission
	// Scope invokes visit exactly once and synchronously using a current
	// authorized read scope. The queryer and predicates never escape the call.
	Scope func(context.Context, auth.Principal, ChoiceOrigin, func(db.Queryer, []orm.Predicate[T]) error) error
}
type relatedChoices[T any] struct{ config RelatedChoiceConfig[T] }

func NewRelatedChoices[T any](config RelatedChoiceConfig[T]) (RelatedChoices, error) {
	if !config.Choices.Initialized() || config.Scope == nil {
		return nil, &ConfigError{Path: "related_choices", Code: "uninitialized"}
	}
	metadata := config.Choices.Metadata()
	if metadata.Kind == formmodel.ModelChoiceRelation {
		if config.Field != "" && config.Field != metadata.Field {
			return nil, &ConfigError{Path: "related_choices.field", Code: "mismatch"}
		}
		config.Field = metadata.Field
	} else if metadata.Kind != formmodel.ModelChoiceTarget || config.Field == "" {
		return nil, &ConfigError{Path: "related_choices.field", Code: "missing"}
	}
	if err := validatePermission("related_choices.permission", config.Permission); err != nil {
		return nil, err
	}
	return relatedChoices[T]{config}, nil
}
func (source relatedChoices[T]) choiceField() string               { return source.config.Field }
func (source relatedChoices[T]) choicePermission() auth.Permission { return source.config.Permission }
func (source relatedChoices[T]) choiceMetadata() formmodel.ModelChoiceMetadata {
	return source.config.Choices.Metadata()
}
func (source relatedChoices[T]) resolve(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, keys []int64) ([]forms.Choice, error) {
	var result []forms.Choice
	err := source.scoped(ctx, actor, origin, func(work context.Context, backend db.Queryer, choices formmodel.ModelChoices[T]) error {
		selection, err := choices.Resolve(work, backend, keys)
		if err == nil {
			result = selection.Items()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (source relatedChoices[T]) search(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, request formmodel.ChoiceRequest) (formmodel.ChoicePage, error) {
	var result formmodel.ChoicePage
	err := source.scoped(ctx, actor, origin, func(work context.Context, backend db.Queryer, choices formmodel.ModelChoices[T]) error {
		var err error
		result, err = choices.Search(work, backend, request)
		return err
	})
	if err != nil {
		return formmodel.ChoicePage{}, err
	}
	return result, nil
}
func (source relatedChoices[T]) scoped(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, run func(context.Context, db.Queryer, formmodel.ModelChoices[T]) error) error {
	if ctx == nil || !origin.valid {
		return &ConfigError{Path: "related_choices.origin", Code: "invalid"}
	}
	if err := validatePrincipalPermission(ctx, actor, source.config.Permission); err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	sealed, entries, completed := false, 0, 0
	var callbackErr error
	var active sync.WaitGroup
	visit := func(backend db.Queryer, predicates []orm.Predicate[T]) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return &ConfigError{Path: "related_choices.scope", Code: "expired"}
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return &ConfigError{Path: "related_choices.scope", Code: "duplicate_callback"}
		}
		active.Add(1)
		mu.Unlock()
		guarded := scopedChoiceQueryer{backend: backend, check: func() error {
			mu.Lock()
			defer mu.Unlock()
			if sealed {
				return &ConfigError{Path: "related_choices.scope", Code: "expired"}
			}
			return work.Err()
		}}
		err := run(work, guarded, source.config.Choices.Filter(slices.Clone(predicates)...))
		mu.Lock()
		completed++
		callbackErr = err
		mu.Unlock()
		active.Done()
		return err
	}
	scopeErr := source.config.Scope(work, actor, origin, visit)
	mu.Lock()
	sealed = true
	invalid := entries != 1 || completed != 1
	mu.Unlock()
	if invalid {
		cancel()
	}
	active.Wait()
	mu.Lock()
	defer mu.Unlock()
	if entries == 0 && scopeErr != nil {
		return choiceErrors(scopeErr, ctx.Err())
	}
	if invalid {
		return errors.Join(&ConfigError{Path: "related_choices.scope", Code: "callback_contract"}, scopeErr, callbackErr, ctx.Err())
	}
	return choiceErrors(scopeErr, callbackErr, ctx.Err())
}

type choiceKeys func(string) ([]int64, bool, error)
type choiceFormFor func(context.Context, auth.Principal, ChoiceOrigin, choiceKeys) (forms.Spec, error)

// choiceFieldView is presentation-only. Its Items never enter validationSpec.
type choiceFieldView struct {
	URL            string
	Items          []forms.Choice
	Offset, Limit  int
	More, Disabled bool
}

func prepareRelatedChoices(model ir.Model, identity ir.ModelIdentity, form forms.Spec, sources []RelatedChoices, targetOnly bool) (choiceFormFor, []auth.Permission, error) {
	byName := make(map[string]RelatedChoices, len(sources))
	for _, source := range sources {
		if source == nil || source.choiceField() == "" {
			return nil, nil, &ConfigError{Path: "model.related_choices", Code: "invalid"}
		}
		name := source.choiceField()
		if _, found := byName[name]; found {
			return nil, nil, &ConfigError{Path: "model.related_choices." + name, Code: "duplicate"}
		}
		if err := validatePermission("model.related_choices."+name+".permission", source.choicePermission()); err != nil {
			return nil, nil, err
		}
		metadata := source.choiceMetadata()
		if targetOnly && metadata.Kind == formmodel.ModelChoiceTarget {
			// Command-only inputs can explicitly select a target without a relation.
		} else if metadata.Kind != formmodel.ModelChoiceRelation || metadata.Source != identity || !metadata.SourceModel.Equal(model) || metadata.Field != name {
			return nil, nil, &ConfigError{Path: "model.related_choices." + name, Code: "source_mismatch"}
		}
		byName[name] = source
	}
	var ordered []RelatedChoices
	var permissions []auth.Permission
	for _, field := range form.Fields() {
		if !field.ModelChoice() {
			continue
		}
		source, found := byName[field.Name()]
		if !found {
			return nil, nil, &ConfigError{Path: "model.related_choices." + field.Name(), Code: "missing"}
		}
		metadata := source.choiceMetadata()
		if metadata.Kind == formmodel.ModelChoiceRelation && metadata.Multiple != (field.Kind() == forms.FieldIntegerList) {
			return nil, nil, &ConfigError{Path: "model.related_choices." + field.Name(), Code: "multiplicity_mismatch"}
		}
		delete(byName, field.Name())
		ordered = append(ordered, source)
		if !slices.Contains(permissions, source.choicePermission()) {
			permissions = append(permissions, source.choicePermission())
		}
	}
	if len(byName) != 0 {
		return nil, nil, &ConfigError{Path: "model.related_choices", Code: "not_selected_relation"}
	}
	resolve := func(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, keys choiceKeys) (forms.Spec, error) {
		for _, permission := range permissions {
			if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
				return forms.Spec{}, err
			}
		}
		result := form
		for _, source := range ordered {
			if keys == nil {
				return forms.Spec{}, &ConfigError{Path: "model.related_choices.keys", Code: "missing"}
			}
			selected, valid, err := keys(source.choiceField())
			if err != nil {
				return forms.Spec{}, err
			}
			var choices []forms.Choice
			if valid {
				choices, err = source.resolve(ctx, actor, origin, selected)
				if err != nil {
					return forms.Spec{}, err
				}
			}
			result, err = result.WithModelChoices(source.choiceField(), choices...)
			if err != nil {
				return forms.Spec{}, err
			}
		}
		return result, nil
	}
	return resolve, permissions, nil
}

func modelChoiceKeys(model ir.Model, spec forms.Spec, data forms.Data, initial map[string]forms.Value, unbound bool) (choiceKeys, error) {
	input, err := formmodel.PrepareChoiceInput(model, spec, data, initial)
	if err != nil {
		return nil, err
	}
	if unbound {
		return input.InitialChoiceKeys, nil
	}
	return input.ChoiceKeys, nil
}
func formChoiceKeys(spec forms.Spec, data forms.Data, unbound bool) choiceKeys {
	return func(name string) ([]int64, bool, error) {
		for _, field := range spec.Fields() {
			if field.Name() == name {
				if unbound {
					form, err := spec.Unbound(nil)
					if err != nil {
						return nil, false, err
					}
					value, found := form.Initial().Get(name)
					if !found || value.IsNull() {
						return nil, true, nil
					}
					if key, ok := value.AsInteger(); ok {
						return []int64{key}, true, nil
					}
					if keys, ok := value.AsIntegers(); ok {
						return keys, true, nil
					}
					return nil, false, nil
				}
				return field.ModelChoiceKeys(data)
			}
		}
		return nil, false, &ConfigError{Path: "related_choices.keys", Code: "unknown_field"}
	}
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
func (site *Site) addPermissionsAllowed(request *web.Request, principal auth.Principal, model registeredModel) (bool, error) {
	for index, permission := range model.addPermissions {
		if index == 0 {
			continue
		}
		allowed, err := site.permissionGranted(request, principal, permission)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func choiceErrors(input ...error) error {
	var result error
	for _, failure := range input {
		if failure == nil {
			continue
		}
		if result == nil {
			result = failure
		} else if !errors.Is(result, failure) {
			result = errors.Join(result, failure)
		}
	}
	return result
}

type scopedChoiceQueryer struct {
	backend db.Queryer
	check   func() error
}

func (backend scopedChoiceQueryer) ValidateSession(ctx context.Context) error {
	if err := backend.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend.backend == nil || reflect.ValueOf(backend.backend).Kind() == reflect.Pointer && reflect.ValueOf(backend.backend).IsNil() {
		return &ConfigError{Path: "related_choices.backend", Code: "nil"}
	}
	if session, ok := backend.backend.(db.SessionValidator); ok {
		return session.ValidateSession(ctx)
	}
	return nil
}
func (backend scopedChoiceQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := backend.ValidateSession(ctx); err != nil {
		return nil, err
	}
	return backend.backend.Query(ctx, plan)
}
