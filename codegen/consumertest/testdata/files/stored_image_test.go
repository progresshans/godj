package consumer_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"example.com/godj-files/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/storage"
	storagemodel "github.com/progresshans/godj/storage/model"
	"github.com/progresshans/godj/uploads"
)

//go:embed stored-image-reference.json
var nativeStoredImageReference []byte

type persistedImageObservation struct {
	Photo         string
	Width, Height *int64
}

type imageRefreshBackend struct {
	storage.Backend
	opens, closes int
	closeErr      error
}

func (backend *imageRefreshBackend) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	backend.opens++
	reader, err := backend.Backend.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	return &imageRefreshReader{Reader: reader.(storage.Reader), owner: backend}, nil
}
func (*imageRefreshBackend) Stat(context.Context, string) (storage.Info, error) {
	return storage.Info{}, errors.New("inspection must use its opened reader metadata")
}
func (*imageRefreshBackend) Save(context.Context, string, io.Reader, storage.SaveOptions) (storage.Info, error) {
	return storage.Info{}, errors.New("inspection must not publish files")
}
func (*imageRefreshBackend) Delete(context.Context, string) error {
	return errors.New("inspection must not delete files")
}

type imageRefreshReader struct {
	storage.Reader
	owner *imageRefreshBackend
}

func (reader *imageRefreshReader) Close() error {
	reader.owner.closes++
	return errors.Join(reader.Reader.Close(), reader.owner.closeErr)
}

func runStoredImageInspection(t *testing.T, database fileBackend, root storage.Backend, label string) {
	t.Helper()
	ctx := t.Context()
	var native struct {
		Django, Python, Pillow string
		Payloads               map[string]string
		Persistence            struct {
			Before        persistedImageObservation `json:"before"`
			BeforeSave    persistedImageObservation `json:"before_save"`
			AfterRollback persistedImageObservation `json:"after_rollback"`
			AfterSave     persistedImageObservation `json:"after_save"`
			Candidate     imageObservation          `json:"candidate"`
		}
	}
	if err := json.Unmarshal(nativeStoredImageReference, &native); err != nil {
		t.Fatal(err)
	}
	if native.Django != "6.1" || native.Python != "3.14.3" || native.Pillow != "12.3.0" || native.Persistence.Before.Width == nil || native.Persistence.Before.Height == nil || native.Persistence.AfterSave.Width == nil || native.Persistence.AfterSave.Height == nil {
		t.Fatal("incomplete native image persistence observation")
	}
	content, err := base64.StdEncoding.DecodeString(native.Payloads["replacement"])
	if err != nil || len(content) == 0 {
		t.Fatal("missing native stored bytes", err)
	}
	info, err := root.Save(ctx, native.Persistence.Before.Photo, bytes.NewReader(content), storage.SaveOptions{})
	if err != nil || info.Name() != native.Persistence.Before.Photo {
		t.Fatal("prepare image content", err)
	}
	current, err := models.PhotographObjects.Create(ctx, database, models.NewPhotographCreate(label+"-refreshed-image").WithPhoto(info.Name()).WithWidth(*native.Persistence.Before.Width).WithHeight(*native.Persistence.Before.Height))
	if err != nil {
		t.Fatal(err)
	}
	check := func(value models.Photograph, want persistedImageObservation) {
		t.Helper()
		if value.Photo == nil || *value.Photo != want.Photo || !equalImageInteger(value.Width, want.Width) || !equalImageInteger(value.Height, want.Height) || value.ID != current.ID || value.Title != current.Title {
			t.Fatal("stored image model differs from explicit native persistence")
		}
	}
	read := func() models.Photograph {
		t.Helper()
		rows, err := models.PhotographObjects.Using(database).Filter(models.PhotographFields.ID.Exact(current.ID)).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("read stored image model", err)
		}
		return rows[0]
	}
	check(read(), native.Persistence.Before)
	probe := &imageRefreshBackend{Backend: root}
	refreshed, inspection, err := storagemodel.RefreshImageDimensions(ctx, models.PhotographObjects, current, "photo", probe, uploads.ImageLimits{})
	if err != nil || !inspection.Valid() || inspection.File().Name() != *current.Photo || inspection.File().Size() != int64(len(content)) || probe.opens != 1 || probe.closes != 1 {
		t.Fatal("model inspection lost independent storage lifetime", err, probe.opens, probe.closes)
	}
	check(refreshed, persistedImageObservation{native.Persistence.Candidate.Name, native.Persistence.Candidate.Width, native.Persistence.Candidate.Height})
	check(current, native.Persistence.Before)
	check(read(), native.Persistence.BeforeSave)
	fields := orm.UpdateFields[models.Photograph](models.PhotographFields.Width, models.PhotographFields.Height)
	rollback := errors.New("synthetic dimension refresh rollback")
	err = database.AtomicRelation(ctx, func(tx db.RelationSession) error {
		if err := models.PhotographObjects.Save(ctx, tx, &refreshed, fields); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("dimension write lost outer rollback", err)
	}
	check(read(), native.Persistence.AfterRollback)
	if err := models.PhotographObjects.Save(ctx, database, &refreshed, fields); err != nil {
		t.Fatal(err)
	}
	check(read(), native.Persistence.AfterSave)
	// A late Close error cannot yield a replacement model or a database write.
	probe.closeErr = errors.New("synthetic stored image close failure")
	failed, result, err := storagemodel.RefreshImageDimensions(ctx, models.PhotographObjects, current, "photo", probe, uploads.ImageLimits{})
	if !errors.Is(err, probe.closeErr) || result.Valid() || failed != (models.Photograph{}) || probe.opens != 2 || probe.closes != 2 {
		t.Fatal("late inspection failure yielded a prepared model", err)
	}
	check(current, native.Persistence.Before)
	check(read(), native.Persistence.AfterSave)
	// Generated integer fields cannot represent an empty image's NULL dimensions.
	strict := models.StrictImage{Photo: "", Width: 19, Height: 23}
	if value, result, err := storagemodel.RefreshImageDimensions(ctx, models.StrictImageObjects, strict, "photo", probe, uploads.ImageLimits{}); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue, Field: "height"}) || result.Valid() || value != (models.StrictImage{}) || strict.Width != 19 || strict.Height != 23 || probe.opens != 2 {
		t.Fatal("nonnullable image dimensions silently became zero", err)
	}
	// A model without dimension targets has no implicit storage read.
	raw := models.RawImage{Image: "absent-but-unselected.png"}
	if value, result, err := storagemodel.RefreshImageDimensions(ctx, models.RawImageObjects, raw, "image", probe, uploads.ImageLimits{}); err != nil || result.Valid() || value.Image != raw.Image || probe.opens != 2 {
		t.Fatal("image without dimension targets was opened", err)
	}
	reader, err := root.Open(ctx, info.Name())
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, content) {
		t.Fatal("inspection or rollback mutated stored image bytes", readErr, closeErr)
	}
}
