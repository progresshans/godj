package model

import (
	"context"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/schema/ir"
)

// CollectionSaver binds one selected model collection to the application's
// authorized write adapter. Save must replace its membership with keys,
// including an empty set. It must use the supplied backend/session and return
// failures; it must not silently discard keys, start a nested transaction or
// retry writes. Through defaults and final admission remain adapter policy.
type CollectionSaver[M any] struct {
	Field string
	Save  func(context.Context, db.Session, M, []int64) error
	// Built-in adapters retain the declaration that selected their typed
	// relation. Custom application callbacks continue to own their binding.
	binding *collectionSaveBinding
}

func (CollectionSaver[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.CollectionSaver{redacted}")
}

type collectionSaveBinding struct {
	model ir.Model
	field ir.ManyToManyField
}

type collectionSave[M any] struct {
	keys []int64
	save func(context.Context, db.Session, M, []int64) error
}

// Save persists the caller-owned mutable instance, then every selected
// collection in Schema IR order. Obtain instance from Model; callers may edit
// it before saving, as with deferred ModelForm persistence. Generated keys stay
// on instance even if a later collection or outer transaction fails. Reusing
// that pointer preserves identity on subsequent saves.
//
// This method does not open a transaction, rebind inputs, recheck authority or
// run full_clean. Use an authorized AtomicRelation/CoordinatedAtomicRelation
// scope for all-or-nothing storage, and propagate its terminal result. A nil
// return inside that scope is provisional, not a commit receipt. Outside a
// transaction, scalar/earlier collection writes can survive a later failure.
func (prepared PreparedInstance[M]) Save(ctx context.Context, backend db.Session, instance *M, savers ...CollectionSaver[M]) error {
	operations, err := prepared.collectionSaves(ctx, backend, savers)
	if err != nil {
		return err
	}
	if instance == nil {
		return &Error{Path: "instance", Code: "nil"}
	}
	if err := prepared.manager.Save(ctx, backend, instance); err != nil {
		return err
	}
	return prepared.saveCollections(ctx, backend, *instance, operations)
}

// SaveCollections is the deferred relation phase, after the caller has saved
// its model. It never writes scalar fields. A selected collection requires an
// explicitly present primary key, including zero; excluded collections are
// untouched. Neither a key nor this method grants write authority.
func (prepared PreparedInstance[M]) SaveCollections(ctx context.Context, backend db.Session, instance M, savers ...CollectionSaver[M]) error {
	operations, err := prepared.collectionSaves(ctx, backend, savers)
	if err != nil {
		return err
	}
	return prepared.saveCollections(ctx, backend, instance, operations)
}

func (prepared PreparedInstance[M]) collectionSaves(ctx context.Context, backend db.Session, savers []CollectionSaver[M]) ([]collectionSave[M], error) {
	if err := prepared.requireStoredFiles(); err != nil {
		return nil, err
	}
	metadata, err := prepared.manager.Metadata()
	if err != nil {
		return nil, err
	}
	if err := validateSaveScope(ctx, backend); err != nil {
		return nil, err
	}
	byName := make(map[string]CollectionSaver[M], len(savers))
	for _, saver := range savers {
		path := "collections." + saver.Field
		if saver.binding != nil && (!saver.binding.model.Equal(metadata) || saver.binding.field.Name != saver.Field) {
			return nil, &Error{Path: path, Code: "binding_mismatch"}
		}
		if saver.Save == nil {
			return nil, &Error{Path: path, Code: "nil_saver"}
		}
		if _, exists := byName[saver.Field]; exists {
			return nil, &Error{Path: path, Code: "duplicate_saver"}
		}
		if _, selected := prepared.collections.Get(saver.Field); !selected {
			return nil, &Error{Path: path, Code: "unselected_saver"}
		}
		byName[saver.Field] = saver
	}
	var operations []collectionSave[M]
	for _, field := range metadata.ManyToMany {
		value, selected := prepared.collections.Get(field.Name)
		if !selected {
			continue
		}
		keys, valid := value.AsIntegers()
		if !valid {
			return nil, &Error{Path: "collections." + field.Name, Code: "type_mismatch"}
		}
		saver, found := byName[field.Name]
		if !found {
			return nil, &Error{Path: "collections." + field.Name, Code: "missing_saver"}
		}
		operations = append(operations, collectionSave[M]{keys: keys, save: saver.Save})
	}
	if len(operations) != 0 {
		if _, borrowed := backend.(db.SessionValidator); borrowed {
			if session, valid := backend.(db.RelationSession); !valid || nilSaveValue(session) {
				return nil, &Error{Path: "backend", Code: "relation_session_required"}
			}
		} else if atomic, valid := backend.(db.RelationAtomic); !valid || nilSaveValue(atomic) {
			return nil, &Error{Path: "backend", Code: "relation_atomic_required"}
		}
	}
	return operations, nil
}

func (prepared PreparedInstance[M]) saveCollections(ctx context.Context, backend db.Session, instance M, operations []collectionSave[M]) error {
	if err := validateSaveScope(ctx, backend); err != nil {
		return err
	}
	if len(operations) == 0 {
		return nil
	}
	snapshot, err := prepared.collectionOwner(instance)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if err := validateSaveScope(ctx, backend); err != nil {
			return err
		}
		// Each callback owns its model pointees and keys. It cannot rewrite a
		// later callback's owner snapshot or the reusable prepared intent.
		owner, err := prepared.manager.ApplyValues(snapshot, nil)
		if err != nil {
			return err
		}
		if err := operation.save(ctx, backend, owner, append([]int64(nil), operation.keys...)); err != nil {
			return err
		}
	}
	return validateSaveScope(ctx, backend)
}

func (prepared PreparedInstance[M]) collectionOwner(instance M) (M, error) {
	var zero M
	snapshot, err := prepared.manager.ApplyValues(instance, nil)
	if err != nil {
		return zero, err
	}
	values, err := prepared.manager.ModelValues(snapshot)
	if err != nil {
		return zero, err
	}
	metadata, err := prepared.manager.Metadata()
	if err != nil {
		return zero, err
	}
	for _, field := range metadata.Fields {
		if field.PrimaryKey && values[field.Name].IsNull() {
			return zero, &Error{Path: "instance." + field.Name, Code: "primary_key_required"}
		}
	}
	return snapshot, nil
}

func validateSaveScope(ctx context.Context, backend db.Session) error {
	if nilSaveValue(ctx) {
		return &Error{Path: "context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if nilSaveValue(backend) {
		return &Error{Path: "backend", Code: "nil"}
	}
	if session, borrowed := backend.(db.SessionValidator); borrowed {
		return session.ValidateSession(ctx)
	}
	return nil
}

func nilSaveValue(value any) bool {
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
