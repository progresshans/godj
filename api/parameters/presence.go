package parameters

import "github.com/progresshans/godj/api/openapi"

// Input adds a presence policy to a scalar codec. It is immutable, and its
// zero value is invalid. Optional uses a pointer to distinguish absence from
// every present scalar, including zero or empty text; parameter values are not null.
type Input[T any] struct {
	decode       func(string, bool) (T, string)
	headerSchema func(int) (openapi.Schema, error)
	schema       openapi.Schema
	required     bool
	empty        bool
	err          error
}

func prepare[T any](codec Codec[T], fallback *T) (openapi.Schema, error) {
	if codec.decode == nil || codec.schema == nil {
		return openapi.Schema{}, configError("scalar codec is zero or invalid", nil)
	}
	return codec.schema(fallback)
}

func prepareHeader[T any](codec Codec[T], fallback *T) func(int) (openapi.Schema, error) {
	var owned *T
	if fallback != nil {
		value := *fallback
		owned = &value
	}
	return func(maximumBytes int) (openapi.Schema, error) {
		if codec.decode == nil || codec.headerSchema == nil {
			return openapi.Schema{}, configError("header scalar codec is zero or invalid", nil)
		}
		return codec.headerSchema(maximumBytes, owned)
	}
}

func Required[T any](codec Codec[T]) Input[T] {
	schema, err := prepare(codec, nil)
	return Input[T]{schema: schema, headerSchema: prepareHeader(codec, nil), err: err, required: true, empty: codec.empty,
		decode: func(raw string, present bool) (T, string) {
			var zero T
			if !present {
				return zero, "required"
			}
			value, valid := codec.decode(raw)
			if !valid {
				return zero, "invalid"
			}
			return value, ""
		}}
}

// Default validates the omission value against the same accepted scalar domain
// and publishes it as the schema default. An explicitly empty value is parsed;
// it never activates the default. Codecs support immutable scalar values only.
func Default[T any](codec Codec[T], fallback T) Input[T] {
	schema, err := prepare(codec, &fallback)
	return Input[T]{schema: schema, headerSchema: prepareHeader(codec, &fallback), err: err, empty: codec.empty,
		decode: func(raw string, present bool) (T, string) {
			if !present {
				return fallback, ""
			}
			value, valid := codec.decode(raw)
			if !valid {
				var zero T
				return zero, "invalid"
			}
			return value, ""
		}}
}

func Optional[T any](codec Codec[T]) Input[*T] {
	schema, err := prepare(codec, nil)
	return Input[*T]{schema: schema, headerSchema: prepareHeader(codec, nil), err: err, empty: codec.empty,
		decode: func(raw string, present bool) (*T, string) {
			if !present {
				return nil, ""
			}
			value, valid := codec.decode(raw)
			if !valid {
				return nil, "invalid"
			}
			return &value, ""
		}}
}
