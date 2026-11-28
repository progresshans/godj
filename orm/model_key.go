package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// PrimaryKeyDescriptor is the presence-aware read capability for one model's
// primary key. It grants no assignment or persistence capability. Generated
// WriteDescriptor and PrimaryKeyObjectDescriptor implementations already
// satisfy it; a read-only descriptor can implement it independently.
type PrimaryKeyDescriptor[M any] interface {
	ModelDescriptor[M]
	PrimaryKey(M) (query.Value, bool)
}

// PrimaryKeyField validates the read capability and the manager's unique
// construction-time primary key, then returns detached metadata. It invokes no
// descriptor callbacks and performs no I/O. The current Schema IR supports
// AutoField primary keys; a writer capability is not required.
func (m Manager[M]) PrimaryKeyField() (ir.Field, error) {
	if m.prepared == nil {
		return ir.Field{}, invalidWritePlan("descriptor is nil")
	}
	primary, valid := autoPrimaryKey(m.prepared.metadata)
	if !valid {
		return ir.Field{}, invalidWritePlan("metadata must contain exactly one AutoField primary key")
	}
	if field := NewAutoField[M](primary); field.err != nil {
		return ir.Field{}, field.err
	}
	descriptor, ok := m.descriptor.(PrimaryKeyDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return ir.Field{}, invalidWritePlan("descriptor does not implement primary key reads")
	}
	return primary.Clone(), nil
}

// PrimaryKey reads an owned model snapshot without I/O. It validates the value
// against the manager's construction-time key metadata and preserves explicit
// zero and negative keys. An absent key returns NULL, false, nil; a nonzero Go
// field alone never manufactures presence. Reading does not require a writer.
func (m Manager[M]) PrimaryKey(value M) (query.Value, bool, error) {
	primary, err := m.PrimaryKeyField()
	if err != nil {
		return query.Value{}, false, err
	}
	descriptor := m.descriptor.(PrimaryKeyDescriptor[M])
	key, present := descriptor.PrimaryKey(descriptor.CloneModel(value))
	if !present {
		return query.Null(), false, nil
	}
	if key.IsNull() || !mutationValueMatches(primary, key) {
		return query.Value{}, false, invalidModelValue(primary.Name, "descriptor returned an invalid primary key value")
	}
	return key, true, nil
}
