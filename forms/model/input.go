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
	input, _, err := prepareInput(manager, bound, false)
	return input, err
}

// PrepareInputBeforeFiles projects a valid form's selected scalar/collection
// intent while explicitly deferring pending upload fields. Derived image sizes
// remain selected. Each returned deferred name is omitted from the input; an
// upload's client filename never becomes a stored name.
//
// This input is incomplete and is only for prepublication admission. Owners
// must retain BoundForm, publish through PrepareInstance/SaveFiles outside a DB
// scope, then validate and save the actual returned names. Neither this input
// nor a successful admission authorizes storing the incomplete candidate.
func PrepareInputBeforeFiles[M any](manager orm.Manager[M], bound BoundForm) (orm.ModelInput[M], []string, error) {
	return prepareInput(manager, bound, true)
}

func prepareInput[M any](manager orm.Manager[M], bound BoundForm, deferUploads bool) (orm.ModelInput[M], []string, error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return orm.ModelInput[M]{}, nil, err
	}
	if !metadata.Equal(bound.model) {
		return orm.ModelInput[M]{}, nil, &Error{Path: "model", Code: "mismatch"}
	}
	input, err := bound.Input()
	if err != nil {
		return orm.ModelInput[M]{}, nil, err
	}
	values := make(map[string]query.Value)
	var deferred []string
	for _, field := range metadata.Fields {
		value, present := input.Get(field.Name)
		if !present {
			continue
		}
		if field.Kind.IsFile() {
			if file, ok := value.AsFile(); ok {
				if _, pending := file.Upload(); pending {
					if !deferUploads {
						return orm.ModelInput[M]{}, nil, &Error{Path: "input." + field.Name, Code: "pending_file"}
					}
					deferred = append(deferred, field.Name)
					continue
				}
				values[field.Name] = query.String(file.Name())
				continue
			}
		}
		scalar, valid := queryValue(value)
		if !valid {
			return orm.ModelInput[M]{}, nil, &Error{Path: "input." + field.Name, Code: "type_mismatch"}
		}
		values[field.Name] = scalar
	}
	result, err := manager.Input(values)
	if err != nil {
		return orm.ModelInput[M]{}, nil, err
	}
	for _, field := range metadata.Fields {
		if !field.PrimaryKey {
			continue
		}
		value, present := bound.candidate.Get(field.Name)
		key, valid := queryValue(value)
		if !present || !valid {
			return orm.ModelInput[M]{}, nil, &Error{Path: "instance." + field.Name, Code: "mismatch"}
		}
		result, err = result.WithTargetKey(key)
		if err != nil {
			return orm.ModelInput[M]{}, nil, err
		}
	}
	collections := make(map[string][]int64)
	for _, field := range metadata.ManyToMany {
		if value, present := input.Get(field.Name); present {
			keys, valid := value.AsIntegers()
			if !valid {
				return orm.ModelInput[M]{}, nil, &Error{Path: "input." + field.Name, Code: "type_mismatch"}
			}
			collections[field.Name] = keys
		}
	}
	result, err = result.WithCollections(collections)
	if err != nil {
		return orm.ModelInput[M]{}, nil, err
	}
	return result, deferred, nil
}

// ModelInput transfers this preparation's selected intent to a fresh write
// owner. Pending uploads must first pass SaveFiles. File fields then contain
// the actual returned storage names, including collision renames; command
// inputs and excluded model fields never become scalar assignments. Image
// dimensions derived during binding and collection presence are preserved.
//
// The returned input retains the original target and complete model source.
// It does not attest to current authorization, revision, file freshness or a
// database commit. In particular, callers must retain FilePublication results
// separately if a subsequent write is refused or has an uncertain outcome.
func (prepared PreparedInstance[M]) ModelInput() (orm.ModelInput[M], error) {
	if err := prepared.requireStoredFiles(); err != nil {
		return orm.ModelInput[M]{}, err
	}
	metadata, err := prepared.manager.Metadata()
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	actual, err := prepared.manager.ModelValues(prepared.value)
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	values := make(map[string]query.Value)
	var key query.Value
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key = actual[field.Name]
			continue
		}
		if _, present := prepared.input.Get(field.Name); present {
			values[field.Name] = actual[field.Name]
		}
	}
	result, err := prepared.manager.Input(values)
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	result, err = result.WithTargetKey(key)
	if err != nil {
		return orm.ModelInput[M]{}, err
	}
	collections := make(map[string][]int64)
	for _, field := range metadata.ManyToMany {
		if value, present := prepared.collections.Get(field.Name); present {
			keys, valid := value.AsIntegers()
			if !valid {
				return orm.ModelInput[M]{}, &Error{Path: "input." + field.Name, Code: "type_mismatch"}
			}
			collections[field.Name] = keys
		}
	}
	return result.WithCollections(collections)
}
