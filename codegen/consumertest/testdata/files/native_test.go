package consumer_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"example.com/godj-files/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

type fileBackend interface {
	db.Session
	db.RelationAtomic
}

func nativeCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemLifecycle(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "files.sqlite3"))
	backend, err := sqlite.Open(ctx, dsn)
	nativeCheck(t, err)
	t.Cleanup(func() {
		if backend != nil {
			nativeCheck(t, backend.Close())
		}
	})
	migration := migrations.Migration{App: "file_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.DocumentDescriptor{}).Metadata()},
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.PhotographDescriptor{}).Metadata()},
	}}
	wire, err := definition.Encode(definition.Producer{Name: "native-files", Version: "1"}, migration)
	nativeCheck(t, err)
	loaded, _, err := definition.Load(definition.Source{SourceID: migration.App, Document: wire})
	nativeCheck(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	nativeCheck(t, err)
	media := t.TempDir()
	files, err := storage.OpenFilesystem(ctx, storage.FilesystemConfig{Directory: media})
	nativeCheck(t, err)
	t.Cleanup(func() { nativeCheck(t, files.Close()) })
	t.Run("publication_http", func(t *testing.T) { runFileServing(t, backend, files, "filesystem") })
	t.Run("image_rollback_reopen", func(t *testing.T) {
		spec, err := formmodel.NewSpec((models.PhotographDescriptor{}).Metadata())
		nativeCheck(t, err)
		var encoded bytes.Buffer
		nativeCheck(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 7, 5))))
		prepare := func(title, name string, data []byte) formmodel.InstanceForm[models.Photograph] {
			t.Helper()
			file, err := uploads.NewFile(name, "application/octet-stream", data)
			nativeCheck(t, err)
			form, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewDataWithFiles(map[string][]string{"title": {title}}, map[string][]uploads.File{"photo": {file}}), nil, formmodel.PostClean{})
			nativeCheck(t, err)
			return form
		}
		publish := func(title, name string) (formmodel.PreparedInstance[models.Photograph], models.Photograph) {
			t.Helper()
			prepared, err := prepare(title, name, encoded.Bytes()).Prepare()
			nativeCheck(t, err)
			stored, publications, err := prepared.SaveFiles(ctx, formmodel.FileSaver[models.Photograph]{Field: "photo", Backend: files, Name: func(_ models.Photograph, f uploads.File) (string, error) { return f.Name(), nil }})
			nativeCheck(t, err)
			if len(publications) != 1 || publications[0].Outcome() != storage.Published {
				t.Fatal("photo not published")
			}
			model, err := stored.Model()
			nativeCheck(t, err)
			if model.Photo == nil || *model.Photo != name || model.Width == nil || *model.Width != 7 || model.Height == nil || *model.Height != 5 {
				t.Fatal("photo dimensions or name differ")
			}
			return stored, model
		}
		first, photo := publish("original", "original.png")
		nativeCheck(t, first.Save(ctx, backend, &photo))
		if _, err := prepare("broken", "broken.png", []byte("invalid image")).Prepare(); err == nil {
			t.Fatal("invalid image accepted")
		}
		if _, err := os.Stat(filepath.Join(media, "broken.png")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid image published", err)
		}
		second, rolled := publish("rolled", "rollback.png")
		rollback := errors.New("rollback native write")
		err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
			if err := second.Save(ctx, tx, &rolled); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal("did not reach rollback", err)
		}
		nativeCheck(t, backend.Close())
		backend, err = sqlite.Open(ctx, dsn)
		nativeCheck(t, err)
		rows, err := models.PhotographObjects.Using(backend).All(ctx)
		nativeCheck(t, err)
		if len(rows) != 1 || rows[0].ID != photo.ID || rows[0].Photo == nil || *rows[0].Photo != "original.png" || rows[0].Width == nil || *rows[0].Width != 7 || rows[0].Height == nil || *rows[0].Height != 5 {
			t.Fatal("database reopen lost original image or committed rollback")
		}
		for _, name := range []string{"original.png", "rollback.png"} {
			reader, err := files.Open(ctx, name)
			nativeCheck(t, err)
			data, err := io.ReadAll(reader)
			nativeCheck(t, errors.Join(err, reader.Close()))
			if !bytes.Equal(data, encoded.Bytes()) {
				t.Fatal("published image bytes differ after reopen")
			}
		}
	})
}
