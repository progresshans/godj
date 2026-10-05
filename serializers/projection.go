package serializers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Projection is a request-local, lazy output view. Its callbacks are invoked
// synchronously, once per visited member/item, only after the enclosing budget
// admits traversal. Callers keep the source stable until EncodeProjection
// returns; callbacks must not perform I/O or retain the supplied context.
// A projection is not an immutable Value or a cacheable response.
type Projection struct {
	kind   ValueKind
	value  Value
	length int
	field  func(context.Context, int) (string, Projection, error)
	item   func(context.Context, int) (Projection, error)
}

func ValueProjection(value Value) Projection { return Projection{value: value} }

func ObjectProjection(length int, read func(context.Context, int) (string, Projection, error)) Projection {
	return Projection{kind: ValueObject, length: length, field: read}
}

func ArrayProjection(length int, read func(context.Context, int) (Projection, error)) Projection {
	return Projection{kind: ValueList, length: length, item: read}
}

// EncodeProjection projects and encodes into a single bounded document. Lazy
// containers and existing Values, including opaque JSON, share every budget.
// Any callback, value, budget or cancellation failure discards all output.
func EncodeProjection(ctx context.Context, projection Projection, limits Limits) ([]byte, error) {
	if ctx == nil {
		return nil, invalidConfig("context", "output requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := resolveLimits(limits)
	if err != nil {
		return nil, err
	}
	state := encodeState{ctx: ctx, limits: resolved}
	if err := state.appendProjection(projection, 1, "$"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state.document, nil
}

func (s *encodeState) appendProjection(projection Projection, depth int, path string) (err error) {
	defer func() {
		if err != nil {
			err = locateProjectionError(path, err)
		}
	}()
	if projection.kind == 0 {
		return s.appendValue(projection.value, depth)
	}
	if err := s.enterValue(depth); err != nil {
		return err
	}
	if projection.length < 0 {
		return invalidConfig("projection", "container length is negative")
	}
	object := projection.kind == ValueObject
	if object && projection.field == nil || !object && (projection.kind != ValueList || projection.item == nil) {
		return invalidConfig("projection", "container reader is missing or invalid")
	}
	limit := s.limits.MaxArrayItems
	opening, closing := byte('['), byte(']')
	if object {
		limit, opening, closing = s.limits.MaxObjectMembers, '{', '}'
	}
	if projection.length > limit || projection.length > s.limits.MaxValues-s.values {
		return resourceLimit("projection", "container exceeds the configured item or remaining value limit")
	}
	if projection.length > 0 && depth >= s.limits.MaxDepth {
		return resourceLimit("value.depth", "JSON nesting exceeds the configured depth limit")
	}
	if err := s.appendBytes([]byte{opening}); err != nil {
		return err
	}
	var names map[string]bool
	if object {
		names = make(map[string]bool, projection.length)
	}
	for index := range projection.length {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if s.values >= s.limits.MaxValues {
			return resourceLimit("value.values", "JSON value count exceeds the configured limit")
		}
		if index > 0 {
			if err := s.appendBytes([]byte{','}); err != nil {
				return err
			}
		}
		childPath := projectionPath(path, "["+strconv.Itoa(index)+"]")
		var child Projection
		var err error
		if object {
			var name string
			name, child, err = projection.field(s.ctx, index)
			if len(name) <= 128 && validMemberName(name) {
				childPath = projectionPath(path, "["+strconv.Quote(name)+"]")
			}
			if err == nil {
				if !validMemberName(name) || names[name] {
					return invalidValue("projection.name", "member name is invalid or duplicated")
				}
				names[name] = true
				if err = s.appendString(name, "projection.name"); err == nil {
					err = s.appendBytes([]byte{':'})
				}
			}
		} else {
			child, err = projection.item(s.ctx, index)
		}
		if err != nil {
			return locateProjectionError(childPath, err)
		}
		if err := s.appendProjection(child, depth+1, childPath); err != nil {
			return err
		}
	}
	return s.appendBytes([]byte{closing})
}

type projectionError struct {
	path  string
	cause error
}

func (err *projectionError) Error() string {
	return fmt.Sprintf("serializer output at %s: %v", err.path, err.cause)
}
func (err *projectionError) Unwrap() error { return err.cause }

func locateProjectionError(path string, err error) error {
	var located *projectionError
	if errors.As(err, &located) {
		return err
	}
	return &projectionError{path: path, cause: err}
}

func projectionPath(parent, suffix string) string {
	if len(parent)+len(suffix) > 1024 {
		return parent
	}
	return parent + suffix
}

// Resolve applies serializer defaults and validates hard resource caps. The
// returned value is detached and can be retained by a prepared declaration.
func (limits Limits) Resolve() (Limits, error) { return resolveLimits(limits) }
