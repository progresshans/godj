package model_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/storage"
	storagemodel "github.com/progresshans/godj/storage/model"
	"github.com/progresshans/godj/uploads"
)

type nativeStoredImage struct {
	Name          *string
	Width, Height *int64
}

type storedImageReference struct {
	Django, Python, Pillow string
	Sources, Payloads      map[string]string
	Cases                  []struct {
		Case struct {
			Name          string
			File          *string
			Width, Height bool
		}
		Before, After nativeStoredImage
		Error         *string
		Opens, Closes int
	}
	Cache struct {
		First         nativeStoredImage
		Reused, Fresh struct {
			Value nativeStoredImage
			Opens int
		}
	}
}

func storedImageNative(t *testing.T) storedImageReference {
	t.Helper()
	content, err := os.ReadFile("testdata/stored-image-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference storedImageReference
	if err := json.Unmarshal(content, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Pillow != "12.3.0" || len(reference.Cases) != 9 || reference.Sources["django.db.models.fields.files"] != "fed8e0f0f32feb483bcc96fb16ce417f77493079981c252182a4fee17b4298c8" || reference.Sources["django.core.files.images"] != "32985b35b2436e046568049eed52caff8232271c45b3b5cc5533d0238915fc33" {
		t.Fatal("stored image reference is not the pinned independent observation")
	}
	return reference
}

func nativeImagePayload(t *testing.T, reference storedImageReference, name string) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
	if err != nil || len(content) == 0 {
		t.Fatal("missing native synthetic image", name, err)
	}
	return content
}

type nativeImageReader struct {
	io.ReadCloser
	closes *int
}

func (reader nativeImageReader) Close() error       { (*reader.closes)++; return reader.ReadCloser.Close() }
func (reader nativeImageReader) Info() storage.Info { return reader.ReadCloser.(storage.Reader).Info() }

func TestRefreshStoredImagesAgainstPinnedDjangoAndExplicitContentDifferences(t *testing.T) {
	reference := storedImageNative(t)
	for _, observed := range reference.Cases {
		t.Run(observed.Case.Name, func(t *testing.T) {
			metadata := photographMetadata(t)
			if !observed.Case.Width {
				metadata.Fields[2].WidthField = ""
			}
			if !observed.Case.Height {
				metadata.Fields[2].HeightField = ""
			}
			manager := orm.NewManager[photograph](photographDescriptor{metadata: metadata})
			current := currentPhotograph()
			current.Photo, current.Width, current.Height = observed.Before.Name, observed.Before.Width, observed.Before.Height
			before := photographDescriptor{}.CloneModel(current)
			root, err := storage.NewMemory(storage.MemoryConfig{})
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			for name := range reference.Payloads {
				if _, err := root.Save(t.Context(), "images/"+name+".png", bytes.NewReader(nativeImagePayload(t, reference, name)), storage.SaveOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			closes := 0
			backend := &modelImageBackend{open: func(ctx context.Context, name string) (io.ReadCloser, error) {
				reader, err := root.Open(ctx, name)
				if err != nil {
					return nil, err
				}
				return nativeImageReader{reader, &closes}, nil
			}}
			value, inspection, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", backend, uploads.ImageLimits{})
			if !reflect.DeepEqual(current, before) || backend.opens != observed.Opens || closes != observed.Closes {
				t.Fatal("inspection mutated current or changed fresh-reader ownership", err, backend.opens, closes)
			}
			switch observed.Case.Name {
			case "missing":
				if observed.Error == nil || *observed.Error != "FileNotFoundError" || !errors.Is(err, fs.ErrNotExist) || inspection.Valid() || value != (photograph{}) {
					t.Fatal("missing image lost operational failure", err)
				}
			case "corrupt":
				if observed.Error != nil || observed.After.Width != nil || observed.After.Height != nil || !errors.Is(err, &uploads.Error{Code: "unsupported_image"}) || inspection.Valid() || value != (photograph{}) {
					t.Fatal("corrupt image difference became a successful NULL update", err)
				}
			case "header_only":
				if observed.Error != nil || observed.After.Width == nil || *observed.After.Width != 3 || observed.After.Height == nil || *observed.After.Height != 2 || !errors.Is(err, &uploads.Error{Code: "invalid_image"}) || inspection.Valid() || value != (photograph{}) {
					t.Fatal("header dimensions became verified complete content", err)
				}
			default:
				got := nativeStoredImage{value.Photo, value.Width, value.Height}
				if err != nil || observed.Error != nil || !reflect.DeepEqual(got, observed.After) || inspection.Valid() != (observed.Opens != 0) {
					t.Fatal("stored image dimensions differ from native observation", err, got, observed.After)
				}
			}
		})
	}
}

func TestRefreshStoredImageReopensInsteadOfUsingDjangoFileDimensionCache(t *testing.T) {
	reference := storedImageNative(t)
	if reference.Cache.Reused.Opens != 0 || reference.Cache.Fresh.Opens != 1 || !reflect.DeepEqual(reference.Cache.First, reference.Cache.Reused.Value) || reflect.DeepEqual(reference.Cache.First, reference.Cache.Fresh.Value) {
		t.Fatal("native same-FieldFile dimension cache difference missing")
	}
	backend, err := storage.NewMemory(storage.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	name := *reference.Cache.First.Name
	if _, err := backend.Save(t.Context(), name, bytes.NewReader(nativeImagePayload(t, reference, "full")), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	manager := orm.NewManager[photograph](photographDescriptor{metadata: photographMetadata(t)})
	current := currentPhotograph()
	current.Photo = &name
	first, _, err := storagemodel.RefreshImageDimensions(t.Context(), manager, current, "photo", backend, uploads.ImageLimits{})
	if err != nil || !reflect.DeepEqual(nativeStoredImage{first.Photo, first.Width, first.Height}, reference.Cache.First) {
		t.Fatal("first image refresh", err)
	}
	if err := backend.Delete(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Save(t.Context(), name, bytes.NewReader(nativeImagePayload(t, reference, "replacement")), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	second, _, err := storagemodel.RefreshImageDimensions(t.Context(), manager, first, "photo", backend, uploads.ImageLimits{})
	if err != nil || !reflect.DeepEqual(nativeStoredImage{second.Photo, second.Width, second.Height}, reference.Cache.Fresh.Value) || !reflect.DeepEqual(nativeStoredImage{first.Photo, first.Width, first.Height}, reference.Cache.First) {
		t.Fatal("explicit refresh used cached dimensions or mutated previous result", err)
	}
}
