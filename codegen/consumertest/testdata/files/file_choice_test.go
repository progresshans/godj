package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/godj-files/models"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

//go:embed file-choice-reference.json
var nativeFileChoices []byte

func runReferenceChoices(t *testing.T, backend fileBackend, root storage.Backend, label string) {
	t.Helper()
	ctx := t.Context()
	var reference struct {
		Django      string
		Persistence struct {
			Selected       imageObservation
			AfterRollback  imageObservation `json:"after_rollback"`
			OriginalExists bool             `json:"original_exists"`
			SelectedExists bool             `json:"selected_exists"`
		}
	}
	if err := json.Unmarshal(nativeFileChoices, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || !reference.Persistence.OriginalExists || !reference.Persistence.SelectedExists {
		t.Fatal("incomplete native persistence reference")
	}
	files := &servingStorage{Backend: root}
	for index, name := range []string{"images/a.png", "images/b.png"} {
		if _, err := storage.SaveUpload(ctx, root, name, generatedImageUpload(t, "image.png", 3+4*index, 2+3*index), storage.SaveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	inspector, err := storage.NewImageInspector(files)
	if err != nil {
		t.Fatal(err)
	}
	model := (models.SelectedImageDescriptor{}).Metadata()
	spec, err := formmodel.NewSpec(model, formmodel.OverrideField("photo", formmodel.WithImageChoiceInspector(inspector.Inspect)))
	if err != nil || spec.IsMultipart() {
		t.Fatal("generated image choices projection", err)
	}
	if _, err := serializers.FromModel(model, serializers.ModelField{Name: "photo"}); err == nil {
		t.Fatal("JSON wrote a stored file reference")
	}
	bind := func(name string, current *models.SelectedImage) formmodel.PreparedInstance[models.SelectedImage] {
		t.Helper()
		bound, err := formmodel.BindInstance(ctx, models.SelectedImageObjects, spec, forms.NewData(map[string][]string{"title": {label + "-selected"}, "photo": {name}, "width": {"999"}}), current, formmodel.PostClean{})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := bound.Prepare()
		if err != nil || len(prepared.PendingFiles()) != 0 {
			t.Fatal("selected file requires publication", err)
		}
		return prepared
	}
	first := bind("images/a.png", nil)
	value, err := first.Model()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(ctx, backend, &value); err != nil {
		t.Fatal(err)
	}
	read := func() models.SelectedImage {
		t.Helper()
		rows, err := models.SelectedImageObjects.Using(backend).Filter(models.SelectedImageFields.ID.Exact(value.ID)).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("choice read", err, len(rows))
		}
		return rows[0]
	}
	check := func(got models.SelectedImage, want imageObservation) {
		t.Helper()
		if got.Photo == nil || *got.Photo != want.Name || !equalImageInteger(got.Width, want.Width) || !equalImageInteger(got.Height, want.Height) {
			t.Fatal("selected image mismatch", got, want)
		}
	}
	check(read(), reference.Persistence.AfterRollback)
	next := bind("images/b.png", &value)
	changed, err := next.Model()
	if err != nil {
		t.Fatal(err)
	}
	check(changed, reference.Persistence.Selected)
	check(value, reference.Persistence.AfterRollback)
	failure := errors.New("synthetic choice rollback")
	err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
		if err := next.Save(ctx, tx, &changed); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal("choice rollback error lost", err)
	}
	check(read(), reference.Persistence.AfterRollback)
	if err := next.Save(ctx, backend, &changed); err != nil {
		t.Fatal(err)
	}
	check(read(), reference.Persistence.Selected)
	if files.saves.Load() != 0 || files.opens.Load() != 2 || files.closes.Load() != 2 {
		t.Fatal("choice republished or leaked reader", files.saves.Load(), files.opens.Load(), files.closes.Load())
	}
	// A fresh DB read selects the file for an already authorized route. Query
	// parameters never select the storage reference. Auth/session admission is
	// separately exercised by this consumer's serving suite and Admin HTTP tests.
	configured, err := settings.New(settings.Definition{ProjectName: "choice_serving", InstalledApps: []apps.Config{{Name: "files", Label: "files"}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "files:choice", Method: "GET", Path: "/chosen/", Handler: func(request *web.Request) (web.Response, error) {
		rows, err := models.SelectedImageObjects.Using(backend).Filter(models.SelectedImageFields.ID.Exact(value.ID)).All(request.Context())
		if err != nil {
			return web.Response{}, err
		}
		if len(rows) != 1 || rows[0].Photo == nil {
			return web.NewResponse(404, nil, nil)
		}
		return web.FileResponse(files, *rows[0].Photo, web.FileOptions{})
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/chosen/?name=images/a.png", nil))
	if response.Code != 200 {
		t.Fatal("selected file serving", response.Code, response.Body.String())
	}
	served, err := uploads.NewFile("served.png", "image/png", response.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	info, err := uploads.InspectImage(ctx, served, uploads.ImageLimits{})
	if err != nil || info.Width() != 7 || info.Height() != 5 {
		t.Fatal("download followed an untrusted name", err)
	}
	for _, name := range []string{"images/a.png", "images/b.png"} {
		r, err := root.Open(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal("choice deleted stored file", readErr, closeErr)
		}
	}
	fileSpec, err := formmodel.NewSpec((models.AssetReferenceDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []map[string][]string{{"title": {label + "-default"}}, {"title": {label + "-explicit"}, "reference": {"files/b.txt"}}} {
		bound, err := formmodel.BindInstance(ctx, models.AssetReferenceObjects, fileSpec, forms.NewData(input), nil, formmodel.PostClean{})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := bound.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		asset, err := prepared.Model()
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.PendingFiles()) != 0 || (strings.HasSuffix(asset.Title, "-default") && asset.Reference != "files/a.txt") {
			t.Fatal("file choice default lost")
		}
		if err := prepared.Save(ctx, backend, &asset); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("formset_unique", func(t *testing.T) {
		// The same stored name can be observed with different dimensions. Those
		// observations must not make an equal DB uniqueness tuple distinct.
		count := 0
		inspect := func(ctx context.Context, name string, limits uploads.ImageLimits) (uploads.ImageInfo, error) {
			count++
			return uploads.InspectImage(ctx, generatedImageUpload(t, "asset.png", 3+count, 2), limits)
		}
		row, err := formmodel.NewSpec(model, formmodel.OverrideField("photo", formmodel.WithImageChoiceInspector(inspect)))
		if err != nil {
			t.Fatal(err)
		}
		config := forms.DefaultSetConfig()
		config.Prefix = "choices"
		setSpec, err := forms.NewSetSpec(row, config)
		if err != nil {
			t.Fatal(err)
		}
		set, err := formmodel.BindSet(ctx, models.SelectedImageObjects, setSpec, forms.NewData(map[string][]string{
			"choices-TOTAL_FORMS": {"2"}, "choices-INITIAL_FORMS": {"0"}, "choices-0-title": {"siblings"}, "choices-1-title": {"siblings"}, "choices-0-photo": {"images/a.png"}, "choices-1-photo": {"images/a.png"},
		}), nil, formmodel.PostClean{})
		if err != nil || set.Valid() || count != 2 {
			t.Fatal("duplicate stored-name tuple survived different image inspection", err, count)
		}
		if _, err := set.Prepare(); err == nil {
			t.Fatal("duplicate image choices prepared")
		}
	})
}
