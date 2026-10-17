package consumer_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"slices"
	"strconv"
	"testing"

	"example.com/godj-files/models"
	"example.com/godj-files/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

//go:embed image-reference.json
var nativeImageReference []byte

type imageObservation struct {
	Name          string
	Width, Height *int64
}

func runImageBackends(t *testing.T, backend fileBackend) {
	t.Helper()
	for _, kind := range fileStorageKinds(t) {
		t.Run(kind, func(t *testing.T) {
			var root storage.Backend
			var close func() error
			if kind == "s3" {
				remote := newFileS3(t)
				root, close = remote, remote.Close
			} else if kind == "memory" {
				memory, err := storage.NewMemory(storage.MemoryConfig{Random: zeroEntropy{}})
				if err != nil {
					t.Fatal(err)
				}
				root, close = memory, memory.Close
			} else {
				filesystem, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir(), Random: zeroEntropy{}})
				if err != nil {
					t.Fatal(err)
				}
				root, close = filesystem, filesystem.Close
			}
			t.Cleanup(func() {
				if err := close(); err != nil {
					t.Error(err)
				}
			})
			runImages(t, backend, root, kind)
			t.Run("reference_choices", func(t *testing.T) { runReferenceChoices(t, backend, root, kind) })
		})
	}
}

func generatedImageUpload(t *testing.T, name string, width, height int) uploads.File {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	file, err := uploads.NewFile(name, "application/x-untrusted", output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func runImages(t *testing.T, backend fileBackend, root storage.Backend, label string) {
	t.Helper()
	ctx := t.Context()
	var reference struct {
		Django  string
		Storage struct {
			First, Collision imageObservation
			Assigned         imageObservation `json:"assigned_before_rollback"`
			Rollback         imageObservation `json:"after_rollback"`
			SurvivesRollback bool             `json:"rollback_file_exists"`
			Cleared          struct {
				Name           string
				Width, Height  *int64
				OriginalExists bool `json:"original_exists"`
			}
			Deleted struct {
				FileExists bool `json:"file_exists"`
			}
		}
	}
	if err := json.Unmarshal(nativeImageReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || !reference.Storage.SurvivesRollback || !reference.Storage.Cleared.OriginalExists || !reference.Storage.Deleted.FileExists {
		t.Fatal("incomplete native image storage reference")
	}
	metadata := (models.PhotographDescriptor{}).Metadata()
	if metadata.Fields[2].Kind != ir.FieldImage || metadata.Fields[2].WidthField != "width" || metadata.Fields[2].HeightField != "height" {
		t.Fatal("generated ImageField lost metadata")
	}
	tampered := metadata.Clone()
	tampered.Fields[2].WidthField = "height"
	if _, err := formmodel.NewSpec(tampered); err == nil {
		t.Fatal("generated metadata failed to retain dimension ownership")
	}
	spec, err := formmodel.NewSpec(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Fields()) != 2 || spec.Fields()[1].Kind() != forms.FieldImage {
		t.Fatal("generated model form exposes managed dimensions")
	}
	readonly, err := serializers.FromModel(metadata, serializers.ModelField{Name: "photo", ReadOnly: true}, serializers.ModelField{Name: "width", ReadOnly: true}, serializers.ModelField{Name: "height", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(readonly, metadata, (models.PhotographDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	check := func(value models.Photograph, want imageObservation) {
		t.Helper()
		if value.Photo == nil || *value.Photo != want.Name || !equalImageInteger(value.Width, want.Width) || !equalImageInteger(value.Height, want.Height) {
			t.Fatalf("stored image differs: name %v width %v height %v", value.Photo, value.Width, value.Height)
		}
	}
	read := func(id int64) models.Photograph {
		t.Helper()
		rows, err := models.PhotographObjects.Using(backend).Filter(models.PhotographFields.ID.Exact(id)).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("image lookup", len(rows), err)
		}
		return rows[0]
	}
	saver := func(width, height int) formmodel.FileSaver[models.Photograph] {
		return formmodel.FileSaver[models.Photograph]{Field: "photo", Backend: root, Name: func(value models.Photograph, file uploads.File) (string, error) {
			if value.Width == nil || value.Height == nil || *value.Width != int64(width) || *value.Height != int64(height) {
				return "", errors.New("upload_to did not receive derived dimensions")
			}
			return "photos/" + file.Name(), nil
		}}
	}
	prepare := func(title, name string, width, height int, current *models.Photograph) formmodel.PreparedInstance[models.Photograph] {
		t.Helper()
		file := generatedImageUpload(t, name, width, height)
		form, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewDataWithFiles(map[string][]string{"title": {title}, "width": {"999"}, "height": {"888"}}, map[string][]uploads.File{"photo": {file}}), current, formmodel.PostClean{})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := form.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(prepared.PendingFiles(), []string{"photo"}) {
			t.Fatal("image upload capability lost")
		}
		if _, err := prepared.Model(); err == nil {
			t.Fatal("pending image became a model")
		}
		return prepared
	}
	save := func(prepared formmodel.PreparedInstance[models.Photograph], width, height int) models.Photograph {
		t.Helper()
		stored, publications, err := prepared.SaveFiles(ctx, saver(width, height))
		if err != nil || len(publications) != 1 || publications[0].Outcome() != storage.Published {
			t.Fatal("image publication failed", err)
		}
		value, err := stored.Model()
		if err != nil {
			t.Fatal(err)
		}
		if err := stored.Save(ctx, backend, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := save(prepare(label+"-first", "same.png", 7, 5, nil), 7, 5)
	check(first, reference.Storage.First)
	check(read(first.ID), reference.Storage.First)
	if _, err := encoder.Encode(first); err != nil {
		t.Fatal("image JSON rendering", err)
	}
	second := save(prepare(label+"-second", "same.png", 9, 6, nil), 9, 6)
	check(second, reference.Storage.Collision)
	for _, probe := range []struct {
		predicate orm.Predicate[models.Photograph]
		key       string
		value     any
		count     int
	}{
		{models.PhotographFields.Photo.Exact(*first.Photo), "photo", *first.Photo, 1},
		{models.PhotographFields.Photo.IContains("SAME"), "photo__icontains", "SAME", 2},
	} {
		dynamic, err := orm.ParseDynamic(models.PhotographDescriptor{}, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil {
			t.Fatal(err)
		}
		typed := models.PhotographObjects.Using(backend).Filter(probe.predicate).Filter(models.PhotographFields.Title.IContains(label + "-"))
		if !typed.Plan().Equal(models.PhotographObjects.Using(backend).Filter(dynamic...).Filter(models.PhotographFields.Title.IContains(label + "-")).Plan()) {
			t.Fatal("image typed/dynamic AST diverged")
		}
		rows, err := typed.All(ctx)
		if err != nil || len(rows) != probe.count {
			t.Fatal("image string lookup", err, len(rows))
		}
	}
	link, err := models.PhotoLinkObjects.Create(ctx, backend, models.NewPhotoLinkCreate(first.ID))
	if err != nil {
		t.Fatal(err)
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := models.PhotoLinkObjects.Using(backend).Filter(related.ModelsPhotoLink.Photograph.Photo.Exact(*first.Photo)).Filter(models.PhotoLinkFields.ID.Exact(link.ID)).All(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("related ImageField query", err)
	}
	// Retained names do not read storage or rederive stale server dimensions.
	retained := first
	retained.Width, retained.Height = new(int64(19)), new(int64(23))
	retain, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewData(map[string][]string{"title": {first.Title}}), &retained, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := retain.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	value, err := preserved.Model()
	if err != nil || value.Width == nil || *value.Width != 19 || value.Height == nil || *value.Height != 23 {
		t.Fatal("retained dimensions changed", err)
	}
	// New values remain detached from the caller and are visible before filename generation.
	replacement := prepare(first.Title, "rollback.png", 11, 8, &first)
	stored, publications, err := replacement.SaveFiles(ctx, saver(11, 8))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := stored.Model()
	if err != nil {
		t.Fatal(err)
	}
	check(changed, reference.Storage.Assigned)
	check(first, reference.Storage.First)
	failure := errors.New("synthetic image transaction rollback")
	err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
		if err := stored.Save(ctx, tx, &changed); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || len(publications) != 1 || publications[0].Outcome() != storage.Published {
		t.Fatal("image storage/DB result conflated", err)
	}
	check(read(first.ID), reference.Storage.Rollback)
	for _, name := range []string{*first.Photo, *changed.Photo} {
		if _, err := root.Stat(ctx, name); err != nil {
			t.Fatal("rollback deleted independent image", err)
		}
	}
	// A late database uniqueness failure cannot erase an already-published image.
	duplicate := prepare(first.Title, "duplicate.png", 4, 3, nil)
	duplicate, _, err = duplicate.SaveFiles(ctx, saver(4, 3))
	if err != nil {
		t.Fatal(err)
	}
	duplicateModel, err := duplicate.Model()
	if err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Save(ctx, backend, &duplicateModel); err == nil {
		t.Fatal("image write bypassed unique title")
	}
	if _, err := root.Stat(ctx, *duplicateModel.Photo); err != nil {
		t.Fatal("database failure deleted image", err)
	}
	clear, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewData(map[string][]string{"title": {first.Title}, "photo-clear": {"on"}}), &first, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clear.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	value, err = cleared.Model()
	if err != nil {
		t.Fatal(err)
	}
	check(value, imageObservation{reference.Storage.Cleared.Name, reference.Storage.Cleared.Width, reference.Storage.Cleared.Height})
	if err := cleared.Save(ctx, backend, &value); err != nil {
		t.Fatal(err)
	}
	check(read(first.ID), imageObservation{reference.Storage.Cleared.Name, nil, nil})
	if _, err := root.Stat(ctx, *first.Photo); err != nil {
		t.Fatal("clear deleted original image", err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleters.ModelsPhotograph.Delete(ctx, backend, &second); err != nil {
		t.Fatal(err)
	}
	reader, err := root.Open(ctx, *second.Photo)
	if err != nil {
		t.Fatal("delete removed image", err)
	}
	imageBytes, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	decoded, err := png.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || decoded.Width != 9 || decoded.Height != 6 {
		t.Fatal("stored image bytes changed", err)
	}
	t.Run("formset", func(t *testing.T) { runImageSet(t, backend, root, label, spec) })
	t.Run("stored_inspection", func(t *testing.T) { runStoredImageInspection(t, backend, root, label) })
	t.Run("codecs", func(t *testing.T) { runImageCodecs(t, backend, root, label, spec) })
}

func equalImageInteger(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func runImageSet(t *testing.T, backend fileBackend, root storage.Backend, label string, row forms.Spec) {
	t.Helper()
	ctx := t.Context()
	current, err := models.PhotographObjects.Create(ctx, backend, models.NewPhotographCreate(label+"-set-current").WithPhoto("photos/old.png").WithWidth(3).WithHeight(2))
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "photos"
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string][]string{"photos-TOTAL_FORMS": {"2"}, "photos-INITIAL_FORMS": {"1"}, "photos-0-id": {strconv.FormatInt(current.ID, 10)}, "photos-0-title": {current.Title}, "photos-1-title": {label + "-set-new"}, "photos-0-width": {"999"}}
	data := forms.NewDataWithFiles(raw, map[string][]uploads.File{"photos-0-photo": {generatedImageUpload(t, "set-current.png", 6, 4)}, "photos-1-photo": {generatedImageUpload(t, "set-new.png", 8, 5)}})
	set, err := formmodel.BindSet(ctx, models.PhotographObjects, spec, data, []models.Photograph{current}, formmodel.PostClean{})
	if err != nil || !set.Valid() {
		t.Fatal("image formset", err)
	}
	prepared, err := set.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.SavePlan(); err == nil {
		t.Fatal("pending image set became a save plan")
	}
	stored, receipts, err := prepared.SaveFiles(ctx, func(row formmodel.PreparedSetRow[models.Photograph]) ([]formmodel.FileSaver[models.Photograph], error) {
		return []formmodel.FileSaver[models.Photograph]{{Field: "photo", Backend: root, Name: func(value models.Photograph, file uploads.File) (string, error) {
			if value.Width == nil || value.Height == nil {
				return "", errors.New("dimensions absent in set file callback")
			}
			return "photos/" + file.Name(), nil
		}}}, nil
	})
	if err != nil || len(receipts) != 2 {
		t.Fatal("image set publication", err)
	}
	plan, err := stored.SavePlan()
	if err != nil {
		t.Fatal(err)
	}
	err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
		writes, err := plan.Save(ctx, tx, formmodel.SetSaveOptions[models.Photograph]{})
		if len(writes) != 2 {
			return errors.New("lost image set write")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, row := range plan.Rows() {
		value := row.Model()
		wantWidth, wantHeight := int64(6), int64(4)
		if index == 1 {
			wantWidth, wantHeight = 8, 5
		}
		rows, err := models.PhotographObjects.Using(backend).Filter(models.PhotographFields.ID.Exact(value.ID)).All(ctx)
		if err != nil || len(rows) != 1 || rows[0].Width == nil || *rows[0].Width != wantWidth || rows[0].Height == nil || *rows[0].Height != wantHeight {
			t.Fatal("formset lost dimensions", err)
		}
	}
	if current.Width == nil || *current.Width != 3 {
		t.Fatal("image formset mutated current instance")
	}
}
