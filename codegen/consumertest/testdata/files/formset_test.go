package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"example.com/godj-files/models"
	"example.com/godj-files/project"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

// This transport/storage consumer intentionally has no application auth claim.
// Helpdesk's authenticated Admin consumer exercises the plan's final admission.
func runFileSet(t *testing.T, backend fileBackend, root storage.Backend) {
	t.Helper()
	ctx := t.Context()
	current := []models.Document{}
	for _, name := range []string{"one", "two"} {
		info, err := root.Save(ctx, "sets/old-"+name+".txt", bytes.NewBufferString("old-"+name), storage.SaveOptions{MaxLength: 40})
		if err != nil {
			t.Fatal(err)
		}
		value, err := models.DocumentObjects.Create(ctx, backend, models.NewDocumentCreate("set-"+name).WithFile(info.Name()))
		if err != nil {
			t.Fatal(err)
		}
		current = append(current, value)
	}
	link, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(current[1].ID))
	if err != nil {
		t.Fatal(err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	row, err := formmodel.NewSpecForFields((models.DocumentDescriptor{}).Metadata(), []string{"title", "file"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.CanDelete = true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	policy := uploads.DefaultConfig()
	policy.MemoryBytes = 0
	policy.TempDir = t.TempDir()
	configured, err := settings.New(settings.Definition{ProjectName: "set_file_consumer", InstalledApps: []apps.Config{{Name: "set_files", Label: "files"}}})
	if err != nil {
		t.Fatal(err)
	}
	var incoming []uploads.File
	var plan *formmodel.SetSavePlan[models.Document]
	var receipts []formmodel.SetFilePublication
	var terminal error
	remove := func(ctx context.Context, scope db.Session, row formmodel.DeletedSetRow[models.Document]) error {
		session, ok := scope.(db.RelationSession)
		if !ok {
			return errors.New("lost borrowed relation scope")
		}
		current, err := row.Model()
		if err != nil {
			return err
		}
		_, err = deleters.ModelsDocument.DeleteInSession(ctx, session, current)
		return err
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "files:set", Method: http.MethodPost, Path: "/files/", Handler: func(request *web.Request) (web.Response, error) {
		parsed, err := request.Multipart(policy)
		if err != nil {
			return web.Response{}, err
		}
		for _, files := range parsed.Files() {
			incoming = append(incoming, files...)
		}
		set, err := formmodel.BindSet(request.Context(), models.DocumentObjects, spec, forms.NewDataWithFiles(parsed.Values(), parsed.Files()), current, formmodel.PostClean{})
		if err != nil {
			return web.Response{}, err
		}
		prepared, err := set.Prepare()
		if err != nil {
			return web.Response{}, err
		}
		if _, err := prepared.SavePlan(); err == nil {
			return web.Response{}, errors.New("pending upload allowed to DB")
		}
		stored, publications, err := prepared.SaveFiles(request.Context(), func(row formmodel.PreparedSetRow[models.Document]) ([]formmodel.FileSaver[models.Document], error) {
			return []formmodel.FileSaver[models.Document]{{Field: "file", Backend: root, Name: func(models.Document, uploads.File) (string, error) { return "sets/set.bin", nil }}}, nil
		})
		receipts = publications
		if err != nil {
			return web.Response{}, err
		}
		plan, err = stored.SavePlan()
		if err != nil {
			return web.Response{}, err
		}
		terminal = backend.AtomicRelation(request.Context(), func(session db.RelationSession) error {
			writes, err := plan.Save(request.Context(), session, formmodel.SetSaveOptions[models.Document]{Delete: remove})
			if len(writes) != 2 || writes[0].Index() != 0 || writes[1].Index() != 1 {
				return errors.New("unexpected protected write order")
			}
			return err
		})
		if _, protected := terminal.(*query.ProtectedForeignKeyError); protected {
			return web.NewResponse(http.StatusConflict, nil, []byte("protected"))
		}
		if terminal != nil {
			return web.Response{}, terminal
		}
		return web.Response{}, errors.New("protected delete was accepted")
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"items-TOTAL_FORMS": "4", "items-INITIAL_FORMS": "2", "items-0-id": strconv.FormatInt(current[0].ID, 10), "items-0-title": "set-replaced", "items-1-id": strconv.FormatInt(current[1].ID, 10), "items-1-title": "set-two", "items-1-DELETE": "on", "items-2-title": "set-new", "items-3-title": "set-discard", "items-3-DELETE": "on"}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range values {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		part, err := writer.CreateFormFile(fmt.Sprintf("items-%d-file", i), "set.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, fmt.Sprintf("payload-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/files/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || terminal == nil || plan == nil || len(receipts) != 2 || receipts[0].Index() != 0 || receipts[1].Index() != 2 {
		t.Fatal("multipart set boundary", response.Code, len(receipts), terminal)
	}
	for _, file := range incoming {
		if _, err := file.Open(ctx); !errors.Is(err, &uploads.Error{Code: "closed"}) {
			t.Fatal("set upload survived request", err)
		}
	}
	if entries, err := os.ReadDir(policy.TempDir); err != nil || len(entries) != 0 {
		t.Fatal("set upload staging leaked", err)
	}
	read := func(id int64) models.Document {
		t.Helper()
		rows, err := models.DocumentObjects.Using(backend).Filter(models.DocumentFields.ID.Exact(id)).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("set row read", len(rows), err)
		}
		return rows[0]
	}
	for _, old := range current {
		if actual := read(old.ID); actual.Title != old.Title || actual.File != old.File {
			t.Fatal("PROTECT failure did not roll back earlier update")
		}
	}
	for _, receipt := range receipts {
		if receipt.File().Outcome() != storage.Published {
			t.Fatal("DB rollback rewrote publication outcome")
		}
		reader, err := root.Open(ctx, receipt.File().Info().Name())
		if err != nil {
			t.Fatal("rollback removed published file", err)
		}
		contents, err := io.ReadAll(reader)
		closed := reader.Close()
		if err != nil || closed != nil || string(contents) != fmt.Sprintf("payload-%d", receipt.Index()) {
			t.Fatal("published content mismatch", err, closed)
		}
	}
	if receipts[0].File().Info().Name() == receipts[1].File().Info().Name() {
		t.Fatal("set upload collision overwrote first file")
	}
	// A separate, explicitly reconciled attempt reuses the published references;
	// it does not re-upload or automatically retry an unknown database outcome.
	if _, err := models.LinkObjects.Delete(ctx, backend, &link); err != nil {
		t.Fatal(err)
	}
	if err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		_, err := plan.Save(ctx, session, formmodel.SetSaveOptions[models.Document]{Delete: remove})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i, row := range plan.Rows() {
		if actual := read(row.Model().ID); actual.File != receipts[i].File().Info().Name() {
			t.Fatal("plan stored proposal instead of actual name")
		}
	}
	if exists, err := models.DocumentObjects.Using(backend).Filter(models.DocumentFields.ID.Exact(current[1].ID)).Exists(ctx); err != nil || exists {
		t.Fatal("selected row not deleted", err)
	}
	for _, old := range current {
		reader, err := root.Open(ctx, old.File)
		if err != nil {
			t.Fatal("replacement/deletion removed old file", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
