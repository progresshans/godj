package model

import (
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// PrepareInput transfers a valid model form to transport-independent typed
// intent. Model clean and input validation are not repeated. Selected scalars
// and declared clean changes retain presence; command fields stay on BoundForm
// and collections remain separate. The complete model source must match.
//
// This is not current write authority. Use the destination manager's
// PrepareInput against the fresh authorized current row in the write scope.
// Pending uploads require PrepareInstance and its explicit file publication
// phase; this function rejects them instead of storing an upload filename.
func PrepareInput[M any](manager orm.Manager[M], bound BoundForm) (orm.ModelInput[M], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	if !metadata.Equal(bound.model) {
		return orm.ModelInput[M]{}, &Error{Path: "model", Code: "mismatch"}
	}
	input, err := bound.Input()
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	values := make(map[string]query.Value)
	for _, field := range metadata.Fields {
		value, present := input.Get(field.Name)
		if !present {
			continue
		}
		if field.Kind.IsFile() {
			if file, ok := value.AsFile(); ok {
				if _, pending := file.Upload(); pending {
					return orm.ModelInput[M]{}, &Error{Path: "input." + field.Name, Code: "pending_file"}
				}
				values[field.Name] = query.String(file.Name())
				continue
			}
		}
		scalar, valid := queryValue(value)
		if !valid {
			return orm.ModelInput[M]{}, &Error{Path: "input." + field.Name, Code: "type_mismatch"}
		}
		values[field.Name] = scalar
	}
	result, err := manager.Input(values)
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	for _, field := range metadata.Fields {
		if !field.PrimaryKey {
			continue
		}
		value, present := bound.candidate.Get(field.Name)
		key, valid := queryValue(value)
		if !present || !valid {
			return orm.ModelInput[M]{}, &Error{Path: "instance." + field.Name, Code: "mismatch"}
		}
		result, err = result.WithTargetKey(key)
		if err != nil {
			return orm.ModelInput[M]{}, err
		}
	}
	collections := make(map[string][]int64)
	for _, field := range metadata.ManyToMany {
		if value, present := input.Get(field.Name); present {
			keys, valid := value.AsIntegers()
			if !valid {
				return orm.ModelInput[M]{}, &Error{Path: "input." + field.Name, Code: "type_mismatch"}
			}
			collections[field.Name] = keys
		}
	}
	return result.WithCollections(collections)
}
