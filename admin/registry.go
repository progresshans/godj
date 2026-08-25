package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

const (
	DefaultListLimit      = 20
	MaximumListLimit      = 100
	MaximumListOffset     = 1_000_000
	MaximumSearchBytes    = 256
	MaximumSelectedIDs    = 100
	MaximumActions        = 32
	MaximumRegistryModels = 256
)

// ErrObjectNotFound is the adapter-neutral missing-row marker. Model callbacks
// wrap it when a row disappears between an Admin read and its write so the HTTP
// boundary can return 404 without importing an application package.
var ErrObjectNotFound = errors.New("admin: object not found")

// ErrReconciliationRequired marks a callback contract failure discovered only
// after the callback reported success. The callback may already have committed;
// callers must inspect durable state and must not retry automatically.
var ErrReconciliationRequired = errors.New("admin: reconciliation required; do not retry automatically")

// ConfigError reports an invalid Admin definition discovered before the
// immutable registry is published.
type ConfigError struct {
	Path  string
	Code  string
	Cause error `json:"-"`
}

func (e *ConfigError) Error() string {
	if e == nil {
		return "admin: <nil>"
	}
	if e.Path == "" {
		return "admin: " + e.Code
	}
	return fmt.Sprintf("admin: %s: %s", e.Path, e.Code)
}

// GoString keeps diagnostic %#v formatting on the same framework-owned,
// secret-free surface as Error while Unwrap retains Cause for errors.Is/As.
func (e ConfigError) GoString() string { return (&e).Error() }

func (e *ConfigError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Permissions is the exact model-level authorization surface used by the
// bounded Admin flow.
type Permissions struct {
	View   auth.Permission
	Add    auth.Permission
	Change auth.Permission
	Delete auth.Permission
}

// ListRequest is the bounded, backend-independent list/search input.
type ListRequest struct {
	Search string
	Offset int
	Limit  int
}

// HistoryRequest is the hard-bounded adapter input for one object's most
// recent process-lifetime semantic events.
type HistoryRequest struct {
	Limit int
}

// Page is the typed result returned by a model adapter. Total is a best-effort
// count and need not share a storage snapshot with Items; concurrent writes may
// happen between the adapter's count and row reads. The registry converts every
// item to a closed Object before an HTTP renderer can observe it and raises
// Total to the observed Offset+len(Items) lower bound when necessary.
type Page[M any] struct {
	Items  []M
	Total  int64
	Offset int
	Limit  int
}

// Object is a closed immutable model snapshot suitable for templates. It
// cannot expose generated methods, lazy ORM state, or arbitrary Go values.
type Object struct {
	id     int64
	label  string
	values templates.Value
}

func NewObject(id int64, label string, values map[string]templates.Value) (Object, error) {
	if id <= 0 {
		return Object{}, &ConfigError{Path: "object.id", Code: "invalid"}
	}
	if strings.TrimSpace(label) == "" || len(label) > MaximumDisplayBytes || !utf8.ValidString(label) || containsUnsafeDisplayControl(label) {
		return Object{}, &ConfigError{Path: "object.label", Code: "invalid"}
	}
	closed, err := templates.Object(values)
	if err != nil {
		return Object{}, &ConfigError{Path: "object.values", Code: "invalid", Cause: err}
	}
	return Object{id: id, label: label, values: closed}, nil
}

func (object Object) ID() int64               { return object.id }
func (object Object) Label() string           { return object.label }
func (object Object) Values() templates.Value { return object.values }

func (object Object) Value(name string) (templates.Value, bool) {
	return object.values.Member(name)
}

// ActionResult is the semantic, secret-free outcome of one selected-row
// action. MatchedIDs must be an ordered subset of the canonical selected IDs.
type ActionResult struct {
	MatchedIDs []int64
}

func (result ActionResult) Matched() int { return len(result.MatchedIDs) }

// ActionConfig declares one bounded selected-row action.
type ActionConfig struct {
	Name       string
	Label      string
	Permission auth.Permission
	Run        func(context.Context, auth.Principal, []int64) (ActionResult, error)
}

// ModelConfig is a typed startup definition. Structural form metadata comes
// exclusively from Model; persistence conversion remains in explicit typed
// application callbacks. Every callback must be safe for concurrent calls and
// must not retain request inputs after it returns. The registry passes only
// immutable values or detached slices/maps, but application I/O synchronization
// remains owned by the adapter and its backend.
type ModelConfig[M any] struct {
	AppLabel      string
	Slug          string
	Model         ir.Model
	FormOverrides []formmodel.Override
	ListFields    []string
	SearchFields  []string
	Permissions   Permissions

	List     func(context.Context, ListRequest) (Page[M], error)
	Get      func(context.Context, int64) (M, bool, error)
	Snapshot func(M) (Object, error)
	Initial  func(M) (map[string]forms.Value, error)
	Create   func(context.Context, auth.Principal, forms.Values) (M, error)
	Update   func(context.Context, auth.Principal, int64, forms.Values) (M, []string, error)
	Delete   func(context.Context, auth.Principal, int64) (M, error)
	History  func(context.Context, int64, HistoryRequest) ([]AuditEntry, error)
	Actions  []ActionConfig
}

// Builder is mutable only during single-threaded startup. Build seals it and
// returns an immutable concurrent-use Registry.
type Builder struct {
	state *builderState
}

type builderState struct {
	apps       apps.Registry
	models     []registeredModel
	byIdentity map[string]int
	bySlug     map[string]int
	sealed     bool
}

func NewBuilder(installed apps.Registry) *Builder {
	return &Builder{
		state: &builderState{
			apps:       installed,
			byIdentity: make(map[string]int),
			bySlug:     make(map[string]int),
		},
	}
}

// RegisterModel validates and type-erases one model adapter. A top-level
// generic function is used because Go methods cannot introduce type
// parameters that are absent from their receiver.
func RegisterModel[M any](builder *Builder, config ModelConfig[M]) error {
	if builder == nil || builder.state == nil || builder.state.byIdentity == nil || builder.state.bySlug == nil {
		return &ConfigError{Path: "builder", Code: "invalid"}
	}
	state := builder.state
	if state.sealed {
		return &ConfigError{Path: "builder", Code: "sealed"}
	}
	if len(state.models) >= MaximumRegistryModels {
		return &ConfigError{Path: "models", Code: "limit_exceeded"}
	}
	registered, err := prepareRegistration(config, state.apps)
	if err != nil {
		return err
	}
	identity := modelIdentity(registered.appLabel, registered.model.Name)
	if _, duplicate := state.byIdentity[identity]; duplicate {
		return &ConfigError{Path: "models." + identity, Code: "duplicate"}
	}
	if _, duplicate := state.bySlug[registered.slug]; duplicate {
		return &ConfigError{Path: "models." + registered.slug + ".slug", Code: "duplicate"}
	}
	index := len(state.models)
	state.models = append(state.models, registered)
	state.byIdentity[identity] = index
	state.bySlug[registered.slug] = index
	return nil
}

// Registry is an immutable model-registration snapshot. Its zero value is an
// empty registry; a nonzero registry is created by Builder.Build.
type Registry struct {
	models     []registeredModel
	byIdentity map[string]int
	bySlug     map[string]int
}

func (builder *Builder) Build() (Registry, error) {
	if builder == nil || builder.state == nil || builder.state.byIdentity == nil || builder.state.bySlug == nil {
		return Registry{}, &ConfigError{Path: "builder", Code: "invalid"}
	}
	state := builder.state
	if state.sealed {
		return Registry{}, &ConfigError{Path: "builder", Code: "sealed"}
	}
	state.sealed = true
	return Registry{
		models:     append([]registeredModel(nil), state.models...),
		byIdentity: cloneIndex(state.byIdentity),
		bySlug:     cloneIndex(state.bySlug),
	}, nil
}

// ModelDescriptor is a detached public registration description. It contains
// no persistence or authorization callback.
type ModelDescriptor struct {
	AppLabel     string
	Slug         string
	Model        ir.Model
	ListFields   []string
	SearchFields []string
	Permissions  Permissions
	Actions      []ActionDescriptor
	FormFields   []forms.Field
}

type ActionDescriptor struct {
	Name       string
	Label      string
	Permission auth.Permission
}

func (registry Registry) All() []ModelDescriptor {
	result := make([]ModelDescriptor, len(registry.models))
	for index := range registry.models {
		result[index] = registry.models[index].descriptor()
	}
	return result
}

func (registry Registry) Lookup(appLabel, modelName string) (ModelDescriptor, bool) {
	index, ok := registry.byIdentity[modelIdentity(appLabel, modelName)]
	if !ok || index < 0 || index >= len(registry.models) {
		return ModelDescriptor{}, false
	}
	return registry.models[index].descriptor(), true
}

type registeredModel struct {
	appLabel     string
	slug         string
	model        ir.Model
	form         forms.Spec
	listFields   []string
	searchFields []string
	permissions  Permissions
	actions      []registeredAction

	list    func(context.Context, auth.Principal, ListRequest) (registeredPage, error)
	get     func(context.Context, auth.Principal, int64) (registeredRecord, bool, error)
	create  func(context.Context, auth.Principal, forms.Form) (Object, error)
	update  func(context.Context, auth.Principal, int64, forms.Form) (Object, []string, error)
	delete  func(context.Context, auth.Principal, int64) (Object, error)
	history func(context.Context, auth.Principal, int64) ([]AuditEntry, error)
}

type registeredPage struct {
	objects []Object
	total   int64
	offset  int
	limit   int
}

type registeredRecord struct {
	object  Object
	initial map[string]forms.Value
}

type registeredAction struct {
	name       string
	label      string
	permission auth.Permission
	run        func(context.Context, auth.Principal, []int64) (ActionResult, error)
}

func prepareRegistration[M any](config ModelConfig[M], installed apps.Registry) (registeredModel, error) {
	if _, ok := installed.Lookup(config.AppLabel); !ok {
		return registeredModel{}, &ConfigError{Path: "model.app_label", Code: "not_installed"}
	}
	if !validSlug(config.Slug) {
		return registeredModel{}, &ConfigError{Path: "model.slug", Code: "invalid"}
	}
	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      config.AppLabel,
		Models:        []ir.Model{config.Model},
	})
	if err != nil {
		return registeredModel{}, &ConfigError{Path: "model.ir", Code: "invalid", Cause: err}
	}
	model := normalized.Models[0]
	form, err := formmodel.NewSpec(model, config.FormOverrides...)
	if err != nil {
		return registeredModel{}, &ConfigError{Path: "model.form", Code: "invalid", Cause: err}
	}
	fieldByName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		fieldByName[field.Name] = field
	}
	listFields, err := validateFieldSelection("model.list_fields", config.ListFields, fieldByName, false)
	if err != nil {
		return registeredModel{}, err
	}
	searchFields, err := validateFieldSelection("model.search_fields", config.SearchFields, fieldByName, true)
	if err != nil {
		return registeredModel{}, err
	}
	if err := validatePermissions(config.Permissions); err != nil {
		return registeredModel{}, err
	}
	permissions := config.Permissions
	if config.List == nil || config.Get == nil || config.Snapshot == nil || config.Initial == nil ||
		config.Create == nil || config.Update == nil || config.Delete == nil || config.History == nil {
		return registeredModel{}, &ConfigError{Path: "model.callbacks", Code: "missing"}
	}
	actions, err := prepareActions(config.Actions)
	if err != nil {
		return registeredModel{}, err
	}
	formFields := form.Fields()
	// A complete model POST carries one scalar value per editable field plus
	// the CSRF token. Reject definitions that cannot fit through the Site's
	// global input-count bound instead of publishing an unusable registry.
	if len(formFields)+1 > MaximumInputValues {
		return registeredModel{}, &ConfigError{Path: "model.form", Code: "limit_exceeded"}
	}
	editable := make(map[string]struct{}, len(formFields))
	for _, field := range formFields {
		if field.Name() == "csrfmiddlewaretoken" {
			return registeredModel{}, &ConfigError{Path: "model.form.csrfmiddlewaretoken", Code: "reserved"}
		}
		editable[field.Name()] = struct{}{}
	}
	snapshotFields := append([]ir.Field(nil), model.Fields...)

	registered := registeredModel{
		appLabel:     config.AppLabel,
		slug:         config.Slug,
		model:        model.Clone(),
		form:         form,
		listFields:   listFields,
		searchFields: searchFields,
		permissions:  permissions,
		actions:      actions,
	}
	registered.list = func(ctx context.Context, principal auth.Principal, request ListRequest) (registeredPage, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.View); err != nil {
			return registeredPage{}, err
		}
		normalizedRequest, err := normalizeListRequest(ctx, request)
		if err != nil {
			return registeredPage{}, err
		}
		page, err := config.List(ctx, normalizedRequest)
		if err != nil {
			return registeredPage{}, err
		}
		if page.Total < 0 || page.Offset != normalizedRequest.Offset || page.Limit != normalizedRequest.Limit || len(page.Items) > page.Limit {
			return registeredPage{}, &ConfigError{Path: "list.result", Code: "invalid"}
		}
		objects := make([]Object, len(page.Items))
		var previousID int64
		for index, item := range page.Items {
			object, err := config.Snapshot(item)
			if err != nil {
				return registeredPage{}, fmt.Errorf("admin: snapshot list item %d: %w", index, err)
			}
			if err := validateObject(object, snapshotFields); err != nil {
				return registeredPage{}, err
			}
			if index > 0 && object.id <= previousID {
				return registeredPage{}, &ConfigError{Path: "list.result.order", Code: "not_strictly_ascending"}
			}
			previousID = object.id
			objects[index] = object
		}
		total := page.Total
		observedEnd := int64(page.Offset) + int64(len(page.Items))
		if observedEnd > total {
			total = observedEnd
		}
		return registeredPage{objects: objects, total: total, offset: page.Offset, limit: page.Limit}, nil
	}
	// Change-form loading snapshots safe display values and typed form initial
	// values together. Nothing mutable is stored back into the registry.
	registered.get = func(ctx context.Context, principal auth.Principal, id int64) (registeredRecord, bool, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.View); err != nil {
			return registeredRecord{}, false, err
		}
		if err := validateOperation(ctx, id); err != nil {
			return registeredRecord{}, false, err
		}
		item, found, err := config.Get(ctx, id)
		if err != nil || !found {
			return registeredRecord{}, found, err
		}
		object, err := config.Snapshot(item)
		if err != nil {
			return registeredRecord{}, false, err
		}
		if object.id != id {
			return registeredRecord{}, false, &ConfigError{Path: "get.result.id", Code: "mismatch"}
		}
		if err := validateObject(object, snapshotFields); err != nil {
			return registeredRecord{}, false, err
		}
		initial, err := config.Initial(item)
		if err != nil {
			return registeredRecord{}, false, err
		}
		if len(initial) != len(formFields) {
			return registeredRecord{}, false, &ConfigError{Path: "get.result.initial", Code: "field_count_mismatch"}
		}
		unbound, err := form.Unbound(initial)
		if err != nil {
			return registeredRecord{}, false, &ConfigError{Path: "get.result.initial", Code: "invalid", Cause: err}
		}
		resolved := unbound.Initial()
		if err := validateInitialValues(resolved, formFields, object); err != nil {
			return registeredRecord{}, false, err
		}
		return registeredRecord{object: object, initial: valuesMap(resolved)}, true, nil
	}
	registered.create = func(ctx context.Context, principal auth.Principal, submitted forms.Form) (Object, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.Add); err != nil {
			return Object{}, err
		}
		values, err := validateBoundForm(submitted, form, formFields)
		if err != nil {
			return Object{}, err
		}
		item, err := config.Create(ctx, principal, values)
		if err != nil {
			return Object{}, err
		}
		object, err := config.Snapshot(item)
		if err != nil {
			return Object{}, reconciliationError("create", err)
		}
		if err := validateObject(object, snapshotFields); err != nil {
			return Object{}, reconciliationError("create", err)
		}
		return object, nil
	}
	registered.update = func(ctx context.Context, principal auth.Principal, id int64, submitted forms.Form) (Object, []string, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.Change); err != nil {
			return Object{}, nil, err
		}
		if id <= 0 {
			return Object{}, nil, &ConfigError{Path: "update.id", Code: "invalid"}
		}
		values, err := validateBoundForm(submitted, form, formFields)
		if err != nil {
			return Object{}, nil, err
		}
		item, changed, err := config.Update(ctx, principal, id, values)
		if err != nil {
			return Object{}, nil, err
		}
		object, err := config.Snapshot(item)
		if err != nil {
			return Object{}, nil, reconciliationError("update", err)
		}
		if object.id != id {
			return Object{}, nil, reconciliationError("update", &ConfigError{Path: "update.result.id", Code: "mismatch"})
		}
		if err := validateObject(object, snapshotFields); err != nil {
			return Object{}, nil, reconciliationError("update", err)
		}
		changed, err = validateChangedFields(changed, editable)
		if err != nil {
			return Object{}, nil, reconciliationError("update", err)
		}
		return object, changed, nil
	}
	registered.delete = func(ctx context.Context, principal auth.Principal, id int64) (Object, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.Delete); err != nil {
			return Object{}, err
		}
		if id <= 0 {
			return Object{}, &ConfigError{Path: "delete.id", Code: "invalid"}
		}
		item, err := config.Delete(ctx, principal, id)
		if err != nil {
			return Object{}, err
		}
		object, err := config.Snapshot(item)
		if err != nil {
			return Object{}, reconciliationError("delete", err)
		}
		if err := validateObject(object, snapshotFields); err != nil {
			return Object{}, reconciliationError("delete", err)
		}
		if object.id != id {
			return Object{}, reconciliationError("delete", &ConfigError{Path: "delete.result.id", Code: "mismatch"})
		}
		return object, nil
	}
	registered.history = func(ctx context.Context, principal auth.Principal, id int64) ([]AuditEntry, error) {
		if err := validatePrincipalPermission(ctx, principal, permissions.View); err != nil {
			return nil, err
		}
		if err := validateOperation(ctx, id); err != nil {
			return nil, err
		}
		entries, err := config.History(ctx, id, HistoryRequest{Limit: MaximumHistoryEntries})
		if err != nil {
			return nil, err
		}
		if len(entries) > MaximumHistoryEntries {
			return nil, &ConfigError{Path: "history.result", Code: "limit_exceeded"}
		}
		identity := modelIdentity(config.AppLabel, model.Name)
		var previous uint64
		result := make([]AuditEntry, len(entries))
		for index, entry := range entries {
			if entry.Sequence == 0 || entry.Sequence > math.MaxInt64 || entry.Sequence <= previous || entry.Model != identity || entry.ObjectID != id {
				return nil, &ConfigError{Path: "history.result", Code: "invalid"}
			}
			if _, err := PrepareEvent(entry.ActorID, entry.Model, entry.ObjectID, entry.Action, entry.ChangedFields, entry.DisplayLabel); err != nil {
				return nil, &ConfigError{Path: fmt.Sprintf("history.result[%d]", index), Code: "invalid", Cause: err}
			}
			if _, err := validateChangedFields(entry.ChangedFields, editable); err != nil {
				return nil, &ConfigError{Path: fmt.Sprintf("history.result[%d].changed_fields", index), Code: "invalid", Cause: err}
			}
			previous = entry.Sequence
			result[index] = entry.Clone()
		}
		return result, nil
	}
	return registered, nil
}

func prepareActions(actions []ActionConfig) ([]registeredAction, error) {
	if len(actions) > MaximumActions {
		return nil, &ConfigError{Path: "model.actions", Code: "limit_exceeded"}
	}
	seen := make(map[string]struct{}, len(actions))
	result := make([]registeredAction, len(actions))
	for index, action := range actions {
		path := fmt.Sprintf("model.actions[%d]", index)
		if !validSlug(action.Name) {
			return nil, &ConfigError{Path: path + ".name", Code: "invalid"}
		}
		if _, duplicate := seen[action.Name]; duplicate {
			return nil, &ConfigError{Path: path + ".name", Code: "duplicate"}
		}
		seen[action.Name] = struct{}{}
		if action.Label == "" || len(action.Label) > MaximumDisplayBytes || !utf8.ValidString(action.Label) || containsUnsafeControl(action.Label) {
			return nil, &ConfigError{Path: path + ".label", Code: "invalid"}
		}
		if err := validatePermission(path+".permission", action.Permission); err != nil {
			return nil, err
		}
		if action.Run == nil {
			return nil, &ConfigError{Path: path + ".run", Code: "missing"}
		}
		run := action.Run
		permission := action.Permission
		result[index] = registeredAction{
			name:       action.Name,
			label:      action.Label,
			permission: permission,
			run: func(ctx context.Context, principal auth.Principal, selected []int64) (ActionResult, error) {
				if err := validatePrincipalPermission(ctx, principal, permission); err != nil {
					return ActionResult{}, err
				}
				canonical, err := canonicalSelectedIDs(selected)
				if err != nil {
					return ActionResult{}, err
				}
				callbackIDs := append([]int64(nil), canonical...)
				outcome, err := run(ctx, principal, callbackIDs)
				if err != nil {
					return ActionResult{}, err
				}
				matched, err := validateActionResult(outcome.MatchedIDs, canonical)
				if err != nil {
					return ActionResult{}, reconciliationError("action "+action.Name, err)
				}
				return ActionResult{MatchedIDs: matched}, nil
			},
		}
	}
	return result, nil
}

func reconciliationError(operation string, cause error) error {
	return fmt.Errorf("admin: %s callback returned an invalid result after success: %w: %w", operation, ErrReconciliationRequired, cause)
}

func (model registeredModel) descriptor() ModelDescriptor {
	actions := make([]ActionDescriptor, len(model.actions))
	for index, action := range model.actions {
		actions[index] = ActionDescriptor{Name: action.name, Label: action.label, Permission: action.permission}
	}
	return ModelDescriptor{
		AppLabel:     model.appLabel,
		Slug:         model.slug,
		Model:        model.model.Clone(),
		ListFields:   append([]string(nil), model.listFields...),
		SearchFields: append([]string(nil), model.searchFields...),
		Permissions:  model.permissions,
		Actions:      actions,
		FormFields:   model.form.Fields(),
	}
}

func validateFieldSelection(path string, fields []string, known map[string]ir.Field, charOnly bool) ([]string, error) {
	if len(fields) == 0 {
		return nil, &ConfigError{Path: path, Code: "empty"}
	}
	seen := make(map[string]struct{}, len(fields))
	result := append([]string(nil), fields...)
	for index, name := range result {
		field, ok := known[name]
		if !ok {
			return nil, &ConfigError{Path: fmt.Sprintf("%s[%d]", path, index), Code: "unknown_field"}
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, &ConfigError{Path: fmt.Sprintf("%s[%d]", path, index), Code: "duplicate"}
		}
		seen[name] = struct{}{}
		if charOnly && field.Kind != ir.FieldChar {
			return nil, &ConfigError{Path: fmt.Sprintf("%s[%d]", path, index), Code: "not_searchable"}
		}
	}
	return result, nil
}

func validatePermissions(permissions Permissions) error {
	checks := []struct {
		name       string
		permission auth.Permission
	}{
		{"view", permissions.View},
		{"add", permissions.Add},
		{"change", permissions.Change},
		{"delete", permissions.Delete},
	}
	for _, check := range checks {
		if err := validatePermission("model.permissions."+check.name, check.permission); err != nil {
			return err
		}
	}
	return nil
}

func validatePermission(path string, permission auth.Permission) error {
	validated, err := auth.NewPermission(string(permission))
	if err != nil || validated != permission {
		return &ConfigError{Path: path, Code: "invalid", Cause: err}
	}
	return nil
}

func normalizeListRequest(ctx context.Context, request ListRequest) (ListRequest, error) {
	if ctx == nil {
		return ListRequest{}, &ConfigError{Path: "list.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return ListRequest{}, err
	}
	if request.Offset < 0 || request.Offset > MaximumListOffset {
		return ListRequest{}, &ConfigError{Path: "list.offset", Code: "invalid"}
	}
	if request.Limit == 0 {
		request.Limit = DefaultListLimit
	}
	if request.Limit < 1 || request.Limit > MaximumListLimit {
		return ListRequest{}, &ConfigError{Path: "list.limit", Code: "invalid"}
	}
	if len(request.Search) > MaximumSearchBytes || !utf8.ValidString(request.Search) || strings.ContainsRune(request.Search, 0) {
		return ListRequest{}, &ConfigError{Path: "list.search", Code: "invalid"}
	}
	return request, nil
}

func validateOperation(ctx context.Context, id int64) error {
	if ctx == nil {
		return &ConfigError{Path: "operation.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id <= 0 {
		return &ConfigError{Path: "operation.id", Code: "invalid"}
	}
	return nil
}

func validatePrincipalPermission(ctx context.Context, principal auth.Principal, permission auth.Permission) error {
	if ctx == nil {
		return &ConfigError{Path: "operation.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !principal.Authenticated() {
		return &ConfigError{Path: "operation.principal", Code: "anonymous"}
	}
	if !principal.Has(permission) {
		return &ConfigError{Path: "operation.permission", Code: "denied"}
	}
	return nil
}

func validateBoundForm(submitted forms.Form, spec forms.Spec, fields []forms.Field) (forms.Values, error) {
	if !submitted.Bound() || !submitted.Valid() || !submitted.Errors().Empty() {
		return forms.Values{}, &ConfigError{Path: "form", Code: "not_bound_valid"}
	}
	values := submitted.Cleaned()
	entries := values.All()
	if len(entries) != len(fields) {
		return forms.Values{}, &ConfigError{Path: "form.cleaned", Code: "field_count_mismatch"}
	}
	canonicalData := make(map[string][]string, len(fields))
	for index, field := range fields {
		entry := entries[index]
		if entry.Name() != field.Name() {
			return forms.Values{}, &ConfigError{Path: fmt.Sprintf("form.cleaned[%d]", index), Code: "field_order_mismatch"}
		}
		if !validFormValue(entry.Value(), field) {
			return forms.Values{}, &ConfigError{Path: "form.cleaned." + field.Name(), Code: "type_or_constraint_mismatch"}
		}
		switch field.Kind() {
		case forms.FieldChar:
			if entry.Value().IsNull() {
				canonicalData[field.Name()] = []string{""}
			} else {
				value, _ := entry.Value().AsString()
				canonicalData[field.Name()] = []string{value}
			}
		case forms.FieldBoolean:
			value, _ := entry.Value().AsBoolean()
			if value {
				canonicalData[field.Name()] = []string{"true"}
			} else {
				canonicalData[field.Name()] = []string{"false"}
			}
		}
	}
	revalidated, err := spec.Bind(forms.NewData(canonicalData), nil)
	if err != nil || !revalidated.Valid() || !revalidated.Errors().Empty() {
		return forms.Values{}, &ConfigError{Path: "form.cleaned", Code: "spec_validation_failed", Cause: err}
	}
	return revalidated.Cleaned(), nil
}

func validInitialValue(value forms.Value, field forms.Field) bool {
	switch field.Kind() {
	case forms.FieldChar:
		if value.IsNull() {
			return field.Nullable()
		}
		text, ok := value.AsString()
		return ok && utf8.ValidString(text) && !strings.ContainsRune(text, 0) &&
			(field.MaxLength() == 0 || utf8.RuneCountInString(text) <= field.MaxLength())
	case forms.FieldBoolean:
		_, ok := value.AsBoolean()
		return ok
	default:
		return false
	}
}

func validateInitialValues(values forms.Values, fields []forms.Field, object Object) error {
	entries := values.All()
	if len(entries) != len(fields) {
		return &ConfigError{Path: "get.result.initial", Code: "field_count_mismatch"}
	}
	for index, field := range fields {
		entry := entries[index]
		if entry.Name() != field.Name() {
			return &ConfigError{Path: fmt.Sprintf("get.result.initial[%d]", index), Code: "field_order_mismatch"}
		}
		if !validInitialValue(entry.Value(), field) {
			return &ConfigError{Path: "get.result.initial." + field.Name(), Code: "type_or_constraint_mismatch"}
		}
		snapshot, ok := object.Value(field.Name())
		if !ok || !initialMatchesSnapshot(entry.Value(), snapshot) {
			return &ConfigError{Path: "get.result.initial." + field.Name(), Code: "snapshot_mismatch"}
		}
	}
	return nil
}

func initialMatchesSnapshot(initial forms.Value, snapshot templates.Value) bool {
	switch initial.Kind() {
	case forms.ValueNull:
		return snapshot.IsNull()
	case forms.ValueString:
		left, leftOK := initial.AsString()
		right, rightOK := snapshot.AsString()
		return leftOK && rightOK && snapshot.Kind() == templates.ValueString && left == right
	case forms.ValueBoolean:
		left, leftOK := initial.AsBoolean()
		right, rightOK := snapshot.AsBool()
		return leftOK && rightOK && left == right
	default:
		return false
	}
}

func validFormValue(value forms.Value, field forms.Field) bool {
	switch field.Kind() {
	case forms.FieldChar:
		if value.IsNull() {
			return field.Nullable() && !field.Required()
		}
		text, ok := value.AsString()
		if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || strings.TrimSpace(text) != text {
			return false
		}
		if field.Required() && text == "" {
			return false
		}
		return field.MaxLength() == 0 || utf8.RuneCountInString(text) <= field.MaxLength()
	case forms.FieldBoolean:
		boolean, ok := value.AsBoolean()
		return ok && (!field.Required() || boolean)
	default:
		return false
	}
}

func validateObject(object Object, fields []ir.Field) error {
	if object.id <= 0 || object.label == "" || object.values.Kind() != templates.ValueObject {
		return &ConfigError{Path: "snapshot", Code: "invalid"}
	}
	members, ok := object.values.Members()
	if !ok || len(members) != len(fields) {
		return &ConfigError{Path: "snapshot.values", Code: "field_count_mismatch"}
	}
	fieldByName := make(map[string]ir.Field, len(fields))
	for _, field := range fields {
		fieldByName[field.Name] = field
	}
	for _, member := range members {
		field, exists := fieldByName[member.Name()]
		if !exists {
			return &ConfigError{Path: "snapshot." + member.Name(), Code: "unknown_field"}
		}
		if !validSnapshotValue(member.Value(), field, object.id) {
			return &ConfigError{Path: "snapshot." + member.Name(), Code: "type_or_constraint_mismatch"}
		}
	}
	return nil
}

func validSnapshotValue(value templates.Value, field ir.Field, objectID int64) bool {
	switch field.Kind {
	case ir.FieldAuto:
		integer, ok := value.AsInteger()
		return ok && integer > 0 && (!field.PrimaryKey || integer == objectID)
	case ir.FieldChar:
		if value.IsNull() {
			return field.Nullable
		}
		text, ok := value.AsString()
		return ok && value.Kind() == templates.ValueString && utf8.ValidString(text) &&
			!strings.ContainsRune(text, 0) && (field.MaxLength == 0 || utf8.RuneCountInString(text) <= field.MaxLength)
	case ir.FieldBoolean:
		_, ok := value.AsBool()
		return ok
	case ir.FieldForeignKey:
		if value.IsNull() {
			return field.Nullable
		}
		integer, ok := value.AsInteger()
		return ok && integer > 0
	default:
		return false
	}
}

func validateChangedFields(fields []string, editable map[string]struct{}) ([]string, error) {
	seen := make(map[string]struct{}, len(fields))
	result := append([]string(nil), fields...)
	for index, field := range result {
		if _, ok := editable[field]; !ok {
			return nil, &ConfigError{Path: fmt.Sprintf("update.changed[%d]", index), Code: "unknown_field"}
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, &ConfigError{Path: fmt.Sprintf("update.changed[%d]", index), Code: "duplicate"}
		}
		seen[field] = struct{}{}
	}
	return result, nil
}

func canonicalSelectedIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > MaximumSelectedIDs {
		return nil, &ConfigError{Path: "action.selected", Code: "invalid_count"}
	}
	result := append([]int64(nil), ids...)
	for _, id := range result {
		if id <= 0 {
			return nil, &ConfigError{Path: "action.selected", Code: "invalid_id"}
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	write := 0
	for _, id := range result {
		if write != 0 && result[write-1] == id {
			continue
		}
		result[write] = id
		write++
	}
	return result[:write], nil
}

func validateActionResult(matched, selected []int64) ([]int64, error) {
	selectedSet := make(map[int64]struct{}, len(selected))
	for _, id := range selected {
		selectedSet[id] = struct{}{}
	}
	result := append([]int64(nil), matched...)
	var previous int64
	for index, id := range result {
		if id <= 0 || index > 0 && id <= previous {
			return nil, &ConfigError{Path: "action.result", Code: "not_canonical"}
		}
		if _, ok := selectedSet[id]; !ok {
			return nil, &ConfigError{Path: "action.result", Code: "unselected_id"}
		}
		previous = id
	}
	return result, nil
}

func valuesMap(values forms.Values) map[string]forms.Value {
	entries := values.All()
	result := make(map[string]forms.Value, len(entries))
	for _, entry := range entries {
		result[entry.Name()] = entry.Value()
	}
	return result
}

func cloneIndex(input map[string]int) map[string]int {
	clone := make(map[string]int, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}

func modelIdentity(appLabel, modelName string) string { return appLabel + "." + modelName }

func validSlug(value string) bool {
	if value == "" || len(value) > MaximumModelBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' ||
			index > 0 && character == '-' {
			continue
		}
		return false
	}
	return true
}
