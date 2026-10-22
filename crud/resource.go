// Package crud assembles model CRUD from a generated manager, an explicit
// writable-field policy and an owned query scope. It does not expose routes or
// grant permissions. Request admission stays before body parsing; current
// object/choice policy runs in the borrowed write session through SaveOptions
// or DeleteOptions. Use db.AtomicResult or db.RelationAtomicResult to prepare
// responses and audit before publishing a confirmed application result.
package crud

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

var (
	ErrNotFound = errors.New("crud: object not found in the current scope")
	ErrReadOnly = errors.New("crud: resource is read-only")
)

// Config is an explicit application policy, not a model-field discovery rule.
// New model fields never become writable automatically. Fields may include
// collections, but their writes must be performed explicitly by the owner in
// the same session; scalar CRUD never silently consumes collection intent.
type Config struct {
	Fields   []string
	ReadOnly bool
}

// Resource is immutable and contains no backend or per-request model state.
// Filter derives a scope by conjunction and cannot broaden its parent scope.
type Resource[M any] struct {
	manager  orm.Manager[M]
	model    ir.Model
	key      orm.AutoField[M]
	keyName  string
	fields   []string
	allowed  map[string]bool
	scope    []orm.Predicate[M]
	readOnly bool
	valid    bool
}

func New[M any](manager orm.Manager[M], config Config) (Resource[M], error) {
	var zero Resource[M]
	metadata, err := manager.Metadata()
	if err != nil {
		return zero, err
	}
	var value M
	if _, err := manager.ApplyValues(value, nil); err != nil {
		return zero, err
	}
	if _, err := orm.InitialModelValues(metadata); err != nil {
		return zero, err
	}
	result := Resource[M]{manager: manager, model: metadata, fields: slices.Clone(config.Fields), allowed: make(map[string]bool), readOnly: config.ReadOnly}
	known := make(map[string]bool, len(metadata.Fields)+len(metadata.ManyToMany))
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			if result.keyName != "" || field.Kind != ir.FieldAuto {
				return zero, invalid("resource requires one auto primary key")
			}
			result.key, result.keyName = orm.NewAutoField[M](field), field.Name
		}
		known[field.Name] = !field.PrimaryKey && !field.NonEditable
	}
	for _, field := range metadata.ManyToMany {
		known[field.Name] = true
	}
	if result.keyName == "" {
		return zero, invalid("resource primary key is missing")
	}
	if !config.ReadOnly && len(config.Fields) == 0 {
		return zero, invalid("writable resource requires an explicit field selection")
	}
	if config.ReadOnly && len(config.Fields) != 0 {
		return zero, invalid("read-only resource cannot declare writable fields")
	}
	for _, name := range config.Fields {
		if !known[name] || result.allowed[name] {
			return zero, invalid("resource field is unknown, read-only or repeated")
		}
		result.allowed[name] = true
	}
	result.valid = true
	return result, nil
}

// Fields returns an owned selection for adapters deriving their Form/JSON
// policies from the same model. Representation-specific options stay explicit.
func (resource Resource[M]) Fields() []string { return slices.Clone(resource.fields) }

// Manager returns the same immutable model source for transport adapters.
// It has no backend binding and grants no application or object authority.
func (resource Resource[M]) Manager() orm.Manager[M] { return resource.manager }

func (resource Resource[M]) Filter(predicates ...orm.Predicate[M]) Resource[M] {
	resource.scope = append(slices.Clone(resource.scope), predicates...)
	return resource
}

// Predicates lets a generated relation facade apply this same fixed scope
// while retaining its native eager/prefetch behavior and typed result shape.
func (resource Resource[M]) Predicates() []orm.Predicate[M] { return slices.Clone(resource.scope) }

func (resource Resource[M]) Query(backend db.Queryer) orm.QuerySet[M] {
	return resource.manager.Using(backend).Filter(resource.scope...)
}

func (resource Resource[M]) Get(ctx context.Context, backend db.Queryer, id int64) (M, bool, error) {
	var zero M
	if err := resource.check(ctx, backend); err != nil {
		return zero, false, err
	}
	return resource.Query(backend).Filter(resource.key.Exact(id)).OrderBy(resource.key.Asc()).First(ctx)
}

func (resource Resource[M]) check(ctx context.Context, backend any) error {
	if !resource.valid {
		return invalid("resource is zero or invalid")
	}
	if isNil(ctx) || isNil(backend) {
		return invalid("context and backend are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if validator, borrowed := backend.(db.SessionValidator); borrowed {
		return validator.ValidateSession(ctx)
	}
	return nil
}

func (resource Resource[M]) checkInput(input orm.ModelInput[M]) error {
	if resource.readOnly {
		return ErrReadOnly
	}
	model, valid := input.Model()
	if !valid || !model.Equal(resource.model) {
		return invalid("input does not belong to the resource model")
	}
	for name := range input.Values() {
		if !resource.allowed[name] {
			return invalid("input contains a field outside the writable selection")
		}
	}
	for name := range input.Collections() {
		if !resource.allowed[name] {
			return invalid("input contains a collection outside the writable selection")
		}
	}
	return nil
}

func invalid(detail string) error {
	return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidPlan, Detail: "crud: " + detail}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
