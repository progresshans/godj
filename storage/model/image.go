// Package model connects explicit storage operations to canonical typed model
// metadata. It performs no database writes and grants no access authority.
package model

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

// RefreshImageDimensions inspects an explicitly selected ImageField's current
// storage name and returns a detached model with its declared width/height
// fields refreshed. The input, primary key, other fields and stored bytes are
// preserved. All image input limits and the backend's reader lifetime apply.
// A failure returns a zero model and inspection; no partial update escapes.
//
// An empty or NULL reference clears nullable dimensions without storage I/O
// and returns a zero inspection. Nonnullable dimensions reject that NULL
// instead of silently storing zero. With no dimension fields, the detached
// model is unchanged, no file is opened, and the inspection is zero. Use
// storage.InspectImage when only stored content metadata is needed.
//
// The caller owns fresh object/storage authorization, revision checks, and
// subsequent model validation and database persistence. No implicit cache or
// freshness guarantee extends beyond the independently opened reader.
func RefreshImageDimensions[M any](ctx context.Context, manager orm.Manager[M], current M, field string, backend storage.Backend, limits uploads.ImageLimits) (M, storage.ImageInspection, error) {
	var zero M
	if nilContext(ctx) {
		return zero, storage.ImageInspection{}, &storage.Error{Code: "invalid_context"}
	}
	if err := ctx.Err(); err != nil {
		return zero, storage.ImageInspection{}, err
	}
	limits, err := limits.Normalize()
	if err != nil {
		return zero, storage.ImageInspection{}, err
	}
	metadata, err := manager.Metadata()
	if err != nil {
		return zero, storage.ImageInspection{}, err
	}
	seen := make(map[string]bool, len(metadata.Fields))
	var selected ir.Field
	for _, candidate := range metadata.Fields {
		normalized, err := ir.NormalizeField(candidate)
		if err != nil {
			return zero, storage.ImageInspection{}, err
		}
		if !candidate.Equal(normalized) {
			return zero, storage.ImageInspection{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidPlan, Field: candidate.Name, Detail: "model field metadata is not normalized"}
		}
		if seen[candidate.Name] {
			return zero, storage.ImageInspection{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidPlan, Field: candidate.Name, Detail: "model field names are duplicated"}
		}
		seen[candidate.Name] = true
		if candidate.Name == field {
			selected = candidate
		}
	}
	if !seen[field] {
		return zero, storage.ImageInspection{}, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: field}
	}
	if selected.Kind != ir.FieldImage {
		return zero, storage.ImageInspection{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field, Detail: "dimension refresh requires an ImageField"}
	}
	if _, err := ir.ImageDimensionOwners(metadata); err != nil {
		return zero, storage.ImageInspection{}, err
	}
	snapshot, err := manager.ApplyValues(current, nil)
	if err != nil {
		return zero, storage.ImageInspection{}, err
	}
	values, err := manager.ModelValues(snapshot)
	if err != nil {
		return zero, storage.ImageInspection{}, err
	}
	if selected.WidthField == "" && selected.HeightField == "" {
		if err := ctx.Err(); err != nil {
			return zero, storage.ImageInspection{}, err
		}
		return snapshot, storage.ImageInspection{}, nil
	}
	name := ""
	if !values[field].IsNull() {
		var valid bool
		name, valid = values[field].String()
		if !valid {
			return zero, storage.ImageInspection{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field, Detail: "image reference is not a storage name"}
		}
	}
	width, height := query.Null(), query.Null()
	var inspection storage.ImageInspection
	if name != "" {
		inspection, err = storage.InspectImage(ctx, backend, name, limits)
		if err != nil {
			return zero, storage.ImageInspection{}, err
		}
		width, height = query.Integer(int64(inspection.Image().Width())), query.Integer(int64(inspection.Image().Height()))
	}
	changes := make(map[string]query.Value, 2)
	if selected.WidthField != "" {
		changes[selected.WidthField] = width
	}
	if selected.HeightField != "" {
		changes[selected.HeightField] = height
	}
	refreshed, err := manager.ApplyValues(snapshot, changes)
	if err != nil {
		return zero, storage.ImageInspection{}, err
	}
	if err := ctx.Err(); err != nil {
		return zero, storage.ImageInspection{}, err
	}
	return refreshed, inspection, nil
}

func nilContext(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	switch value := reflect.ValueOf(ctx); value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
		return value.IsNil()
	}
	return false
}
