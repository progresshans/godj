package orm

import "github.com/progresshans/godj/query"

func preparePrefetchEager[T any](selections []RelatedSelection[T], binding BoundModel[T], depth int, remaining *int) ([]preparedRelatedSelection[T], int, error) {
	before := *remaining
	targets, err := prepareSelectionSet(selections, depth, remaining)
	if err != nil {
		return nil, 0, err
	}
	if err := validatePrefetchEager(targets, binding); err != nil {
		return nil, 0, err
	}
	return targets, before - *remaining, nil
}

func validatePrefetchEager[T any](targets []preparedRelatedSelection[T], binding BoundModel[T]) error {
	for _, target := range targets {
		if err := target.validate(); err != nil {
			return err
		}
		source := target.path().source
		if source.snapshot != binding.snapshot || source.identity != binding.identity {
			return relationInvalidPlan("prefetch eager target belongs to another model binding")
		}
	}
	return nil
}

// Loading another descendant may replace graph entries. Duplicate owners keep
// private slices/maps while their already published descendant values stay immutable.
func copyRelatedValues[T any](values []relatedSelectedValue[T]) []relatedSelectedValue[T] {
	result := make([]relatedSelectedValue[T], len(values))
	for i, value := range values {
		result[i] = value
		result[i].targets = append([]cachedRelatedTarget(nil), value.targets...)
		if value.collections != nil {
			result[i].collections = make(map[string]cachedPrefetch, len(value.collections))
			for name, collection := range value.collections {
				result[i].collections[name] = collection
			}
		}
	}
	return result
}

func validatePrefetchModelProjection[T any](projection ProjectionDescriptor[T], value T, key query.Value, presence ProjectionPresence) error {
	if _, integer := key.Integer(); presence != ProjectionPresent || key.IsNull() || !integer {
		return relationInvalidPlan("prefetch target did not decode a present model with an integer key")
	}
	descriptor, ok := projection.(PrimaryKeyObjectDescriptor[T])
	if !ok {
		return relationInvalidPlan("prefetch target has no primary-key descriptor")
	}
	actual, err := manyObjectKey(descriptor, value)
	if err != nil {
		return err
	}
	if !key.Equal(query.Integer(actual)) {
		return relationInvalidPlan("prefetch decoded primary key does not match its model")
	}
	return nil
}
