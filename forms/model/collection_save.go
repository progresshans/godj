package model

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// SaveManyToMany derives a model-form saver from an existing typed forward
// collection binding. No field name or backend-dispatch callback is needed.
// Save and SaveCollections check its declaring model before scalar writes.
// It uses the supplied root backend or borrowed session and never owns an outer
// transaction, admission, current choice validation or commit receipt.
//
// Options are copied. An optional custom ThroughDefaults is an opaque callback
// capability: like other ManyToManyInput implementations, it must be immutable
// and safe for the caller's concurrency. It is not evaluated at construction.
func SaveManyToMany[O, T, L any](relation orm.ManyToMany[O, T, L], options ...orm.ManyToManySetOptions[L]) (CollectionSaver[O], error) {
	model, field, err := relation.Declaration()
	if err != nil {
		return CollectionSaver[O]{}, err
	}
	if len(options) > 1 {
		return CollectionSaver[O]{}, &Error{Path: "collections." + field.Name, Code: "duplicate_options"}
	}
	var option orm.ManyToManySetOptions[L]
	if len(options) == 1 {
		option = options[0]
		if option.ThroughDefaults != nil && nilSaveValue(option.ThroughDefaults) {
			return CollectionSaver[O]{}, &Error{Path: "collections." + field.Name, Code: "nil_through_defaults"}
		}
	}
	return CollectionSaver[O]{
		Field:   field.Name,
		binding: &collectionSaveBinding{model: model, field: field},
		Save: func(ctx context.Context, backend db.Session, owner O, keys []int64) error {
			var collection *orm.ManyCollection[T, L]
			var err error
			if _, borrowed := backend.(db.SessionValidator); borrowed {
				collection, err = relation.InSession(backend, owner)
			} else {
				collection, err = relation.From(backend, owner)
			}
			if err != nil {
				return err
			}
			return collection.SetKeys(ctx, keys, option)
		},
	}, nil
}
