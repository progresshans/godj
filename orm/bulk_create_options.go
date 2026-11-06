package orm

import (
	"slices"

	"github.com/progresshans/godj/query"
)

// BulkConflictField includes a generated root primary key as well as ordinary
// scalar fields. The sealed model marker prevents another model's field from
// being used as this model's conflict target.
type BulkConflictField[M any] interface {
	bulkConflictField(M) (query.FieldRef, error)
}

func (f field[M]) bulkConflictField(M) (query.FieldRef, error) { return f.reference, f.err }

type bulkCreateOptionKind uint8

const (
	bulkCreateBatchSize bulkCreateOptionKind = iota + 1
	bulkCreateIgnore
	bulkCreateTypedUpdate
	bulkCreateDynamicUpdate
)

// BulkCreateOption is immutable and model-specific. Constructors snapshot
// caller containers; operations resolve them against their Manager snapshot.
type BulkCreateOption[M any] struct {
	kind               bulkCreateOptionKind
	batchSize          int
	target             []BulkConflictField[M]
	update             []WritableField[M]
	targetNames, names []string
}

// BulkBatchSize requests an upper bound on rows per native statement. The
// actual size also obeys backend limits. Explicit zero/negative sizes fail.
func BulkBatchSize[M any](size int) BulkCreateOption[M] {
	return BulkCreateOption[M]{kind: bulkCreateBatchSize, batchSize: size}
}

func BulkIgnoreConflicts[M any]() BulkCreateOption[M] {
	return BulkCreateOption[M]{kind: bulkCreateIgnore}
}

func BulkUpdateConflicts[M any](target []BulkConflictField[M], update ...WritableField[M]) BulkCreateOption[M] {
	return BulkCreateOption[M]{kind: bulkCreateTypedUpdate, target: slices.Clone(target), update: slices.Clone(update)}
}

// BulkUpdateConflictNames is the dynamic path to the same policy and AST.
func BulkUpdateConflictNames[M any](target, update []string) BulkCreateOption[M] {
	return BulkCreateOption[M]{kind: bulkCreateDynamicUpdate, targetNames: slices.Clone(target), names: slices.Clone(update)}
}

type preparedBulkCreateOptions struct {
	batchSize int
	conflict  query.BulkConflict
}

func prepareBulkCreateOptions[M any](prepared *preparedModel, options []BulkCreateOption[M]) (preparedBulkCreateOptions, error) {
	var result preparedBulkCreateOptions
	batchSet, policySet := false, false
	for _, option := range options {
		if option.kind == bulkCreateBatchSize {
			if batchSet || option.batchSize <= 0 {
				return result, invalidBulkArgument("bulk batch size must be positive and supplied once")
			}
			result.batchSize, batchSet = option.batchSize, true
			continue
		}
		if policySet {
			return result, invalidBulkArgument("bulk conflict policies are mutually exclusive and may be supplied once")
		}
		policySet = true
		mode := query.BulkConflictUpdate
		var target, update []query.FieldRef
		switch option.kind {
		case bulkCreateIgnore:
			mode = query.BulkConflictIgnore
		case bulkCreateTypedUpdate:
			var zero M
			for _, field := range option.target {
				if interfaceIsNil(field) {
					return result, invalidBulkArgument("bulk conflict target field is nil")
				}
				reference, err := field.bulkConflictField(zero)
				if err != nil {
					return result, err
				}
				target = append(target, reference)
			}
			for _, field := range option.update {
				if interfaceIsNil(field) {
					return result, invalidBulkArgument("bulk conflict update field is nil")
				}
				reference, err := field.writableField(zero)
				if err != nil {
					return result, err
				}
				update = append(update, reference)
			}
		case bulkCreateDynamicUpdate:
			for index, names := range [][]string{option.targetNames, option.names} {
				for _, name := range names {
					field, ok := prepared.byName[name]
					if !ok {
						return result, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: name}
					}
					reference := fieldReference(prepared.metadata.Fields[field])
					if index == 0 {
						target = append(target, reference)
					} else {
						update = append(update, reference)
					}
				}
			}
		default:
			return result, invalidBulkArgument("unknown bulk create option")
		}
		if mode == query.BulkConflictUpdate {
			if err := validateBulkConflictFields(prepared, target, update); err != nil {
				return result, err
			}
		}
		policy, err := query.NewBulkConflict(mode, target, update)
		if err != nil {
			return result, err
		}
		result.conflict = policy
	}
	return result, nil
}

func validateBulkConflictFields(prepared *preparedModel, target, update []query.FieldRef) error {
	if prepared.uniqueErr != "" {
		return invalidWritePlan(prepared.uniqueErr)
	}
	for index, fields := range [][]query.FieldRef{target, update} {
		if len(fields) == 0 {
			return invalidBulkArgument("bulk conflict update requires target and update fields")
		}
		seen := make(map[string]bool, len(fields))
		for _, reference := range fields {
			field, ok := prepared.mutationField(reference)
			if !ok || seen[reference.Name()] {
				return invalidWritePlan("bulk conflict has an unknown, foreign or repeated field")
			}
			if index == 1 && field.PrimaryKey {
				return primaryKeyUpdateField(field.Name)
			}
			seen[reference.Name()] = true
		}
	}
	if len(target) == 1 {
		field, _ := prepared.mutationField(target[0])
		if field.PrimaryKey || field.Unique {
			return nil
		}
	}
	for _, constraint := range prepared.unique {
		if len(constraint.fields) != len(target) {
			continue
		}
		matches := true
		for _, field := range constraint.fields {
			matches = matches && slices.Contains(target, fieldReference(field))
		}
		if matches {
			return nil
		}
	}
	return invalidBulkArgument("bulk conflict target is not a declared primary or unique key")
}

func invalidBulkArgument(detail string) error {
	return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue, Detail: detail}
}
