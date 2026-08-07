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
}

// WriteDescriptor is an optional generated capability layered on the M1 read
// descriptor. Keeping it separate preserves read-only user descriptors while
// making auto-key presence explicit for create, update, and delete.
type WriteDescriptor[M any] interface {
	ModelDescriptor[M]
	PrimaryKey(M) (query.Value, bool)
	SetPrimaryKey(*M, int64)
	ClearPrimaryKey(*M)
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
