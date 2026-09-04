package orm

import (
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ModelDescriptor is owned by the generic consumer. Generated zero-state
// concrete descriptor types satisfy it without runtime registration or a
// mutable freeze phase. Scan's M return keeps descriptor instantiations model
// specific at compile time.
type ModelDescriptor[M any] interface {
	Metadata() ir.Model
	Scan(db.Row) (M, error)
	// CloneModel returns a model value that does not retain caller-visible
	// aliases from the input. Generated descriptors deep-clone nullable pointer
	// fields so QuerySet's canonical evaluation cache is never exposed.
	CloneModel(M) M
}

// RelationObjectDescriptor is the additive immutable capability required by
// project-bound relation object loaders. Implementations return a generated
// named, non-pointer, zero-size snapshot so BoundModel never retains mutable
// caller-owned descriptor state.
type RelationObjectDescriptor[M any] interface {
	ModelDescriptor[M]
	SnapshotRelationObjectDescriptor() RelationObjectDescriptor[M]
	BindRelationStorage(ir.Field) (RelationStorage[M], bool)
}

// PrimaryKeyObjectDescriptor is the sealed, presence-aware capability used by
// reverse object accessors. BindReverseObject asserts it only from the
// immutable RelationObjectDescriptor snapshot retained by BoundModel.
type PrimaryKeyObjectDescriptor[M any] interface {
	RelationObjectDescriptor[M]
	PrimaryKey(M) (query.Value, bool)
}

// RelationStorage extracts one structurally bound ForeignKey value. Generated
// implementations use direct field access; reflection is restricted to cold
// binder validation of the storage value's immutable shape.
type RelationStorage[M any] interface {
	Field() ir.Field
	Value(M) (query.Value, bool)
}

// WriteDescriptor is an optional generated capability layered on the M1 read
// descriptor. Keeping it separate preserves read-only user descriptors while
// making auto-key presence explicit for create, update, and delete.
type WriteDescriptor[M any] interface {
	ModelDescriptor[M]
	PrimaryKey(M) (query.Value, bool)
	SetPrimaryKey(*M, int64)
	ClearPrimaryKey(*M)
	CloneWriteModel(M) M
	WriteFieldValue(M, ir.Field) (query.Value, bool)
}

// descriptorIsNil handles both a nil interface and an interface containing a
// typed nil pointer. Reflection is limited to this cold API validation path;
// row decoding remains reflection-free.
func descriptorIsNil[M any](descriptor ModelDescriptor[M]) bool {
	return interfaceIsNil(descriptor)
}

func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func immutableZeroStateValue(value any) bool {
	if interfaceIsNil(value) {
		return false
	}
	valueType := reflect.TypeOf(value)
	return valueType.Kind() == reflect.Struct && valueType.Name() != "" && valueType.Size() == 0
}
