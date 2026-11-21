package model_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	storagemodel "github.com/progresshans/godj/storage/model"
	"github.com/progresshans/godj/uploads"
)

type photograph struct {
	ID                                     int64
	Present                                bool
	Title                                  string
	Photo, Other                           *string
	Width, Height, OtherWidth, OtherHeight *int64
}

type photographDescriptor struct {
	metadata ir.Model
	badRead  bool
}

func (d photographDescriptor) Metadata() ir.Model { return d.metadata }
func (d photographDescriptor) Scan(db.Row) (photograph, error) {
	return photograph{}, errors.New("dimension refresh must not read the database")
}
func (d photographDescriptor) CloneModel(value photograph) photograph {
	return d.CloneWriteModel(value)
}
func (d photographDescriptor) CloneWriteModel(value photograph) photograph {
	for _, field := range []**string{&value.Photo, &value.Other} {
		if *field != nil {
			*field = new(**field)
		}
	}
	for _, field := range []**int64{&value.Width, &value.Height, &value.OtherWidth, &value.OtherHeight} {
		if *field != nil {
			*field = new(**field)
		}
	}
	return value
}
func (photographDescriptor) PrimaryKey(value photograph) (query.Value, bool) {
	return query.Integer(value.ID), value.Present
}
func (photographDescriptor) SetPrimaryKey(value *photograph, id int64) {
	value.ID, value.Present = id, true
}
func (photographDescriptor) ClearPrimaryKey(value *photograph) { value.ID, value.Present = 0, false }
func (d photographDescriptor) WriteFieldValue(value photograph, field ir.Field) (query.Value, bool) {
	switch field.Name {
	case "title":
		return query.String(value.Title), true
	case "photo", "other":
		name := value.Photo
		if field.Name == "other" {
			name = value.Other
		}
		if d.badRead && field.Name == "photo" {
			return query.Integer(99), true
		}
		if name == nil {
			return query.Null(), true
		}
		return query.String(*name), true
	}
	var dimension *int64
	switch field.Name {
	case "width":
		dimension = value.Width
	case "height":
		dimension = value.Height
	case "other_width":
		dimension = value.OtherWidth
	case "other_height":
		dimension = value.OtherHeight
	default:
		return query.Value{}, false
	}
	if dimension == nil {
		return query.Null(), true
	}
	return query.Integer(*dimension), true
}
func (photographDescriptor) SetFieldValue(value *photograph, field ir.Field, scalar query.Value) bool {
	if field.Name == "title" {
		text, valid := scalar.String()
		if valid {
			value.Title = text
		}
		return valid
	}
	if field.Name == "photo" || field.Name == "other" {
		var name *string
		if !scalar.IsNull() {
			text, valid := scalar.String()
			if !valid {
				return false
			}
			name = &text
		}
		if field.Name == "photo" {
			value.Photo = name
		} else {
			value.Other = name
		}
		return true
	}
	var dimension *int64
	if !scalar.IsNull() {
		number, valid := scalar.Integer()
		if !valid {
			return false
		}
		dimension = &number
	}
	switch field.Name {
	case "width":
		value.Width = dimension
	case "height":
		value.Height = dimension
	case "other_width":
		value.OtherWidth = dimension
	case "other_height":
		value.OtherHeight = dimension
	default:
		return false
	}
	return true
}

func photographMetadata(t *testing.T) ir.Model {
	t.Helper()
	built, err := schema.Build(schema.Definition{AppLabel: "stored_images", Models: []schema.Model{{Name: "photograph", GoName: "Photograph", Fields: []schema.Field{
		schema.CharField("title", "Title", 60),
		schema.ImageField("photo", "Photo", schema.Nullable(), schema.ImageDimensions("width", "height")),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
		schema.ImageField("other", "Other", schema.Nullable(), schema.ImageDimensions("other_width", "other_height")),
		schema.IntegerField("other_width", "OtherWidth", schema.Nullable()), schema.IntegerField("other_height", "OtherHeight", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return built.Models[0]
}

func currentPhotograph() photograph {
	return photograph{ID: 0, Present: true, Title: "private title", Photo: new("images/old.png"), Width: new(int64(19)), Height: new(int64(23)), Other: new("images/other.png"), OtherWidth: new(int64(11)), OtherHeight: new(int64(13))}
}

func imageContent(t *testing.T, width, height int) []byte {
	t.Helper()
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

type modelImageBackend struct {
	storage.Backend
	open  func(context.Context, string) (io.ReadCloser, error)
	opens int
}

func (backend *modelImageBackend) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	backend.opens++
	return backend.open(ctx, name)
}

func TestRefreshStoredImageDimensionsOwnsSnapshotAndOnlyChangesItsIRTargets(t *testing.T) {
	for _, mode := range []string{"both", "width", "height", "neither"} {
		t.Run(mode, func(t *testing.T) {
			metadata := photographMetadata(t)
			if mode == "height" || mode == "neither" {
				metadata.Fields[2].WidthField = ""
			}
			if mode == "width" || mode == "neither" {
				metadata.Fields[2].HeightField = ""
			}
			manager := orm.NewManager[photograph](photographDescriptor{metadata: metadata})
			current := currentPhotograph()
			before := photographDescriptor{}.CloneModel(current)
			backend := &modelImageBackend{open: func(ctx context.Context, name string) (io.ReadCloser, error) {
				if ctx != t.Context() || name != *before.Photo {
					t.Fatal("model inspection lost name/context binding")
				}
				return io.NopCloser(bytes.NewReader(imageContent(t, 3, 2))), nil
			}}
			// The manager's construction-time policy owns this reference.
			metadata.Fields[2].WidthField = "other_width"
			value, inspection, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", backend, uploads.ImageLimits{})
			want := photographDescriptor{}.CloneModel(before)
			if mode == "both" || mode == "width" {
				want.Width = new(int64(3))
			}
			if mode == "both" || mode == "height" {
				want.Height = new(int64(2))
			}
			wantOpens := 1
			if mode == "neither" {
				wantOpens = 0
			}
			if err != nil || inspection.Valid() != (mode != "neither") || !reflect.DeepEqual(value, want) || !reflect.DeepEqual(current, before) || backend.opens != wantOpens {
				t.Fatal("dimension refresh lost model ownership", err, backend.opens)
			}
			*value.Photo, *value.Other = "changed", "changed"
			*value.Width, *value.Height, *value.OtherWidth, *value.OtherHeight = 999, 999, 999, 999
			if !reflect.DeepEqual(current, before) {
				t.Fatal("refreshed model retained caller pointers")
			}
		})
	}
}

func TestRefreshStoredImageDimensionsClearsEmptyReferencesWithoutReading(t *testing.T) {
	for _, name := range []*string{nil, new("")} {
		for _, nullable := range []bool{true, false} {
			metadata := photographMetadata(t)
			metadata.Fields[3].Nullable = nullable
			manager := orm.NewManager[photograph](photographDescriptor{metadata: metadata})
			current := currentPhotograph()
			current.Photo = name
			before := photographDescriptor{}.CloneModel(current)
			// A missing backend is immaterial when there is no referenced file.
			value, inspection, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", nil, uploads.ImageLimits{})
			if !nullable {
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidValue, Field: "width"}) || value != (photograph{}) || inspection.Valid() {
					t.Fatal("unrepresentable NULL became zero/partial dimensions", err)
				}
			} else {
				want := photographDescriptor{}.CloneModel(before)
				want.Width, want.Height = nil, nil
				if err != nil || !reflect.DeepEqual(value, want) || inspection.Valid() {
					t.Fatal("empty reference was opened or its null/empty form changed", err)
				}
			}
			if !reflect.DeepEqual(current, before) {
				t.Fatal("empty refresh mutated current")
			}
		}
	}
}

func TestRefreshStoredImageDimensionsRejectsInvalidPolicyBeforeStorage(t *testing.T) {
	for _, mode := range []string{"unknown", "not_image", "duplicate", "unnormalized", "wrong_target", "missing_target", "primary_key", "shared", "wrong_value", "nil_manager", "nil_context", "typed_nil_context", "canceled", "limits"} {
		t.Run(mode, func(t *testing.T) {
			metadata := photographMetadata(t)
			field, ctx, limits := "photo", t.Context(), uploads.ImageLimits{}
			descriptor := photographDescriptor{metadata: metadata}
			switch mode {
			case "unknown":
				field = "missing"
			case "not_image":
				field = "title"
			case "duplicate":
				metadata.Fields = append(metadata.Fields, metadata.Fields[3])
				descriptor.metadata = metadata
			case "unnormalized":
				metadata.Fields[2].Column = ""
			case "wrong_target":
				metadata.Fields[2].WidthField = "title"
			case "missing_target":
				metadata.Fields[2].WidthField = "missing"
			case "primary_key":
				metadata.Fields[2].WidthField = "id"
			case "shared":
				metadata.Fields[2].WidthField = "other_width"
			case "wrong_value":
				descriptor.badRead = true
			case "nil_context":
				ctx = nil
			case "typed_nil_context":
				var missing *imageNilContext
				ctx = missing
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "limits":
				limits.MaxWidth = -1
			}
			manager := orm.NewManager[photograph](descriptor)
			if mode == "nil_manager" {
				manager = orm.Manager[photograph]{}
			}
			backend := &modelImageBackend{open: func(context.Context, string) (io.ReadCloser, error) {
				t.Fatal("invalid model input reached storage")
				return nil, nil
			}}
			current := currentPhotograph()
			before := photographDescriptor{}.CloneModel(current)
			value, result, err := storagemodel.RefreshImageDimensions(ctx, manager, current, field, backend, limits)
			if err == nil || value != (photograph{}) || result.Valid() || backend.opens != 0 || !reflect.DeepEqual(current, before) {
				t.Fatal("invalid model policy became an I/O operation", err, backend.opens)
			}
		})
	}
}

type imageNilContext struct{ context.Context }

type modelImageCloseFailure struct{ io.Reader }

var imageCloseFailure = errors.New("synthetic image close failure")

func (modelImageCloseFailure) Close() error { return imageCloseFailure }

func TestRefreshStoredImageDimensionsPreservesCurrentOnInspectionFailure(t *testing.T) {
	manager := orm.NewManager[photograph](photographDescriptor{metadata: photographMetadata(t)})
	for _, mode := range []string{"missing", "close", "invalid", "pixels", "name"} {
		t.Run(mode, func(t *testing.T) {
			current := currentPhotograph()
			before := photographDescriptor{}.CloneModel(current)
			limits := uploads.ImageLimits{}
			if mode == "pixels" {
				limits.MaxWidth = 2
			}
			if mode == "name" {
				current.Photo, before.Photo = new("../outside"), new("../outside")
			}
			backend := &modelImageBackend{open: func(context.Context, string) (io.ReadCloser, error) {
				if mode == "missing" {
					return nil, io.EOF
				}
				content := imageContent(t, 3, 2)
				if mode == "invalid" {
					content = []byte("invalid image")
				}
				if mode == "close" {
					return modelImageCloseFailure{bytes.NewReader(content)}, nil
				}
				return io.NopCloser(bytes.NewReader(content)), nil
			}}
			value, result, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", backend, limits)
			if err == nil || value != (photograph{}) || result.Valid() || !reflect.DeepEqual(current, before) {
				t.Fatal("failed inspection overwrote current dimensions", err)
			}
			if mode == "close" && !errors.Is(err, imageCloseFailure) {
				t.Fatal("model refresh hid a storage close failure", err)
			}
			if mode == "name" && backend.opens != 0 {
				t.Fatal("invalid storage name reached backend")
			}
		})
	}
}

func TestRefreshStoredImageDimensionsConcurrentCopiesStayIndependent(t *testing.T) {
	manager := orm.NewManager[photograph](photographDescriptor{metadata: photographMetadata(t)})
	current := currentPhotograph()
	before := photographDescriptor{}.CloneModel(current)
	backend, err := storage.NewMemory(storage.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if _, err := backend.Save(t.Context(), *current.Photo, bytes.NewReader(imageContent(t, 3, 2)), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			value, result, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", backend, uploads.ImageLimits{})
			if err != nil || !result.Valid() || value.Width == nil || *value.Width != 3 || value.Height == nil || *value.Height != 2 {
				t.Error("concurrent model image refresh", err)
				return
			}
			*value.Width, *value.Photo = 999, "other name"
		})
	}
	workers.Wait()
	if !reflect.DeepEqual(current, before) {
		t.Fatal("concurrent refresh shared model pointees")
	}
}
