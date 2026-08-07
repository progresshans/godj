package orm

// Change preserves the difference between an omitted non-null field and an
// explicit value, including the scalar zero value. Private state and value
// receiver methods make it immutable to consumers.
type Change[T any] struct {
	set   bool
	value T
}

func Set[T any](value T) Change[T] {
	return Change[T]{set: true, value: value}
}

func (c Change[T]) IsSet() bool {
	return c.set
}

func (c Change[T]) Get() (T, bool) {
	return c.value, c.set
}

type NullableChangeState uint8

const (
	NullableChangeUnset NullableChangeState = iota
	NullableChangeValue
	NullableChangeNull
)

// NullableChange adds explicit NULL to the omitted/value states. It is kept
// separate from Change so generated non-null fields cannot accept NULL.
type NullableChange[T any] struct {
	state NullableChangeState
	value T
}

func SetNullable[T any](value T) NullableChange[T] {
	return NullableChange[T]{state: NullableChangeValue, value: value}
}

func SetNull[T any]() NullableChange[T] {
	return NullableChange[T]{state: NullableChangeNull}
}

func (c NullableChange[T]) State() NullableChangeState {
	return c.state
}

func (c NullableChange[T]) Get() (T, NullableChangeState) {
	return c.value, c.state
}
