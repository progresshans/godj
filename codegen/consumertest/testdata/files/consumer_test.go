package consumer_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-files/models"
	"example.com/godj-files/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

//go:embed reference.json
var nativeFileReference []byte

type fileBackend interface {
	db.Session
	db.RelationAtomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestGeneratedFileProjection(t *testing.T) {
	metadata := (models.DocumentDescriptor{}).Metadata()
	if metadata.Fields[2].Kind != ir.FieldFile || metadata.Fields[2].MaxLength != 40 || metadata.Fields[3].MaxLength != 100 {
		t.Fatal("generated descriptor lost FileField")
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"file", "optional"})
	if err != nil || !spec.IsMultipart() {
		t.Fatal("generated model form lost file transport", err)
	}
	for _, field := range spec.Fields() {
		if field.Kind() != forms.FieldFile || field.Widget() != forms.ClearableFileInput {
			t.Fatal("model file became text input")
		}
	}
	current := models.Document{File: "", Optional: new("private/old.txt")}
	initial, err := formmodel.InitialValues(metadata, spec, current, (models.DocumentDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := initial["file"].AsFile(); !ok || value.Name() != "" {
		t.Fatal("empty name became NULL")
	}
	if value, ok := initial["optional"].AsFile(); !ok || value.Name() != *current.Optional {
		t.Fatal("nullable stored name lost")
	}
	if _, err := serializers.FromModel(metadata, serializers.ModelField{Name: "file"}); err == nil {
		t.Fatal("JSON string accepted as model upload")
	}
	jsonSpec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "file", ReadOnly: true}, serializers.ModelField{Name: "optional", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(jsonSpec, metadata, (models.DocumentDescriptor{}).WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(current); err != nil {
		t.Fatal("stored file references could not be rendered", err)
	}
	input, err := serializers.NewObject(serializers.MemberOf("file", serializers.String("other/private.txt")))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := jsonSpec.Bind(input, serializers.ModePartial)
	if err != nil || bound.Valid() {
		t.Fatal("read-only JSON reference became writable", err)
	}
}

func TestGeneratedFileStorageAndHistory(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "files.sqlite3")) + "?mode=rwc"
		runFiles(t, func(ctx context.Context) (fileBackend, error) { return sqlite.Open(ctx, dsn) })
	})
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL connection absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("godj_files_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		runFiles(t, func(ctx context.Context) (fileBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runFiles(t *testing.T, open func(context.Context) (fileBackend, error)) {
	t.Helper()
	ctx := t.Context()
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if backend != nil {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	var reference struct {
		Django  string `json:"django"`
		Storage struct {
			Collision              []string `json:"collision"`
			DBAfterRollback        string   `json:"db_after_rollback"`
			PublishedAfterRollback string   `json:"published_after_rollback"`
			SurvivesRollback       bool     `json:"published_survives_rollback"`
			SurvivesReplace        bool     `json:"old_survives_replace"`
			ClearedReference       string   `json:"cleared_reference"`
			SurvivesClear          bool     `json:"old_survives_clear"`
			SurvivesDelete         bool     `json:"file_survives_model_delete"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(nativeFileReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Storage.Collision) != 2 || !reference.Storage.SurvivesRollback || !reference.Storage.SurvivesReplace || !reference.Storage.SurvivesClear || !reference.Storage.SurvivesDelete {
		t.Fatal("incomplete native file persistence reference")
	}
	metadata := (models.DocumentDescriptor{}).Metadata()
	before := metadata.Clone()
	before.Fields[2].Kind, before.Fields[3].Kind = ir.FieldChar, ir.FieldChar
	imageMetadata := (models.PhotographDescriptor{}).Metadata()
	beforeImage := imageMetadata.Clone()
	beforeImage.Fields[2].Kind, beforeImage.Fields[2].WidthField, beforeImage.Fields[2].HeightField = ir.FieldFile, "", ""
	initial := migrations.Migration{App: "file_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "file_reference", Model: before},
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.ArchiveDescriptor{}).Metadata()},
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.LinkDescriptor{}).Metadata()},
		migrations.CreateModel{AppLabel: "file_reference", Model: beforeImage},
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.RawImageDescriptor{}).Metadata()},
		migrations.CreateModel{AppLabel: "file_reference", Model: (models.PhotoLinkDescriptor{}).Metadata()},
	}}
	change := migrations.Migration{App: "file_reference", Name: "0002_file", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AlterField{AppLabel: "file_reference", ModelName: "document", Before: before.Fields[2], After: metadata.Fields[2]},
		migrations.AlterField{AppLabel: "file_reference", ModelName: "document", Before: before.Fields[3], After: metadata.Fields[3]},
		migrations.AlterField{AppLabel: "file_reference", ModelName: "photograph", Before: beforeImage.Fields[2], After: imageMetadata.Fields[2]},
	}}
	swappedImage := imageMetadata.Fields[2]
	swappedImage.WidthField, swappedImage.HeightField = swappedImage.HeightField, swappedImage.WidthField
	dimensions := migrations.Migration{App: "file_reference", Name: "0003_image_dimensions", Dependencies: []migrations.MigrationKey{change.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "file_reference", ModelName: "photograph", Before: imageMetadata.Fields[2], After: swappedImage}}}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{initial, change, dimensions} {
		wire, err := definition.Encode(definition.Producer{Name: "file-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for _, renderer := range []migrationbackend.MigrationSQLRenderer{sqlite.NewMigrationSQLRenderer(), postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"})} {
		for _, target := range []migrations.MigrationKey{change.Key(), dimensions.Key()} {
			statements, err := migrations.RenderMigrationSQL(ctx, loaded, target, renderer)
			if err != nil || len(statements) != 0 {
				t.Fatal("file meaning transition attempted physical DDL", err, statements)
			}
		}
	}
	migrate := func(name string) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "file_reference", Name: name})))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	previous := migrate(initial.Name)
	rawImage, err := models.RawImageObjects.Create(ctx, backend, models.NewRawImageCreate("trusted/application-name.dat"))
	if err != nil || rawImage.Image != "trusted/application-name.dat" {
		t.Fatal("direct ImageField DDL/typed storage name failed", err)
	}

	archive, err := models.ArchiveObjects.Create(ctx, backend, models.NewArchiveCreate("archive/name.txt"))
	if err != nil || archive.Reference != "archive/name.txt" {
		t.Fatal("direct FileField DDL or write failed", err)
	}
	legacy, err := models.DocumentObjects.Create(ctx, backend, models.NewDocumentCreate("legacy").WithFile("existing/legacy.txt").WithOptional(""))
	if err != nil {
		t.Fatal(err)
	}
	if state := migrate(change.Name); state.Equal(previous) {
		t.Fatal("historical file kind lost")
	}
	read := func(id int64) models.Document {
		t.Helper()
		rows, err := models.DocumentObjects.Using(backend).Filter(models.DocumentFields.ID.Exact(id)).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("document lookup", len(rows), err)
		}
		return rows[0]
	}
	if got := read(legacy.ID); got.File != legacy.File || got.Optional == nil || *got.Optional != "" {
		t.Fatal("semantic migration changed references")
	}
	rootDir := t.TempDir()
	root, err := storage.OpenFilesystem(ctx, storage.FilesystemConfig{Directory: rootDir, Random: zeroEntropy{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if root != nil {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title", "file", "optional"})
	if err != nil {
		t.Fatal(err)
	}
	saver := formmodel.FileSaver[models.Document]{Field: "file", Backend: root, Name: func(_ models.Document, file uploads.File) (string, error) { return "documents/" + file.Name(), nil }}
	prepare := func(title, name, content string, current *models.Document) formmodel.PreparedInstance[models.Document] {
		t.Helper()
		file, err := uploads.NewFile(name, "text/plain", []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		form, err := formmodel.BindInstance(t.Context(), models.DocumentObjects, spec, forms.NewDataWithFiles(map[string][]string{"title": {title}}, map[string][]uploads.File{"file": {file}}), current, formmodel.PostClean{})
		if err != nil {
			t.Fatal(err)
		}
		if !form.BoundForm().Form().Valid() {
			t.Fatal(form.BoundForm().Form().Errors())
		}
		prepared, err := form.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepared.Model(); err == nil {
			t.Fatal("pending file became storable model")
		}
		return prepared
	}
	store := func(prepared formmodel.PreparedInstance[models.Document]) (models.Document, []formmodel.FilePublication) {
		t.Helper()
		resolved, results, err := prepared.SaveFiles(ctx, saver)
		if err != nil {
			t.Fatal(err)
		}
		value, err := resolved.Model()
		if err != nil {
			t.Fatal(err)
		}
		if err := backend.AtomicRelation(ctx, func(tx db.RelationSession) error { return resolved.Save(ctx, tx, &value) }); err != nil {
			t.Fatal(err)
		}
		if got := read(value.ID); got.File != results[0].Info().Name() {
			t.Fatal("DB stored proposal instead of published name")
		}
		return value, results
	}
	first, _ := store(prepare("first", "same.txt", "original", nil))
	second, _ := store(prepare("second", "same.txt", "replacement", nil))
	if first.File != reference.Storage.Collision[0] || second.File != reference.Storage.Collision[1] {
		t.Fatal("collision names differ from pinned Django", first.File, second.File)
	}
	if _, err := models.DocumentObjects.Create(ctx, backend, models.NewDocumentCreate("duplicate-reference").WithFile(first.File)); err == nil {
		t.Fatal("file reference uniqueness lost")
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(first.ID)); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		predicate orm.Predicate[models.Document]
		key       string
		value     any
		count     int
	}{
		{models.DocumentFields.File.Exact(first.File), "file", first.File, 1},
		{models.DocumentFields.File.IContains("SAME"), "file__icontains", "SAME", 2},
		{models.DocumentFields.Optional.IsNull(true), "optional__isnull", true, 2},
	} {
		dynamic, err := orm.ParseDynamic(models.DocumentDescriptor{}, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil {
			t.Fatal(err)
		}
		typed := models.DocumentObjects.Using(backend).Filter(probe.predicate)
		if !typed.Plan().Equal(models.DocumentObjects.Using(backend).Filter(dynamic...).Plan()) {
			t.Fatal("typed/dynamic file query diverged")
		}
		rows, err := typed.All(ctx)
		if err != nil || len(rows) != probe.count {
			t.Fatal("file lookup failed", probe.key, len(rows), err)
		}
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	links, err := models.LinkObjects.Using(backend).Filter(related.ModelsLink.Document.File.Exact(first.File)).All(ctx)
	if err != nil || len(links) != 1 {
		t.Fatal("related file query lost stored name", err)
	}
	resolved, receipts, err := prepare("first", "rollback.txt", "rollback upload", &first).SaveFiles(ctx, saver)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := resolved.Model()
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic rollback after scalar save")
	err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
		if err := resolved.Save(ctx, tx, &replacement); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal("transaction failure lost", err)
	}
	if read(first.ID).File != reference.Storage.DBAfterRollback || replacement.File != reference.Storage.PublishedAfterRollback || receipts[0].Outcome() != storage.Published {
		t.Fatal("DB rollback conflated with file publication")
	}
	for _, name := range []string{first.File, replacement.File} {
		if _, err := root.Stat(ctx, name); err != nil {
			t.Fatal("rollback deleted independent file", err)
		}
	}
	clear, err := formmodel.BindInstance(t.Context(), models.DocumentObjects, spec, forms.NewData(map[string][]string{"title": {first.Title}, "file-clear": {"on"}}), &first, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clear.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	value, err := cleared.Model()
	if err != nil || value.File != reference.Storage.ClearedReference {
		t.Fatal("clear did not prepare empty name", err)
	}
	if err := cleared.Save(ctx, backend, &value); err != nil {
		t.Fatal(err)
	}
	if read(first.ID).File != "" {
		t.Fatal("clear not persisted")
	}
	if _, err := root.Stat(ctx, first.File); err != nil {
		t.Fatal("clear deleted old content", err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleters.ModelsDocument.Delete(ctx, backend, &second); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(ctx, second.File); err != nil {
		t.Fatal("model deletion deleted shared file", err)
	}
	// Actual multipart transport -> typed preparation -> storage -> DB commit.
	// Admission is tested in Admin; this route isolates resource/DB ownership.
	policy := uploads.DefaultConfig()
	policy.MemoryBytes, policy.TempDir = 0, t.TempDir()
	configured, err := settings.New(settings.Definition{ProjectName: "file_consumer", InstalledApps: []apps.Config{{Name: "file_consumer", Label: "files"}}})
	if err != nil {
		t.Fatal(err)
	}
	var incoming uploads.File
	var received models.Document
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "files:receive", Method: http.MethodPost, Path: "/files/", Handler: func(request *web.Request) (web.Response, error) {
		parsed, err := request.Multipart(policy)
		if err != nil {
			return web.Response{}, err
		}
		form, err := formmodel.BindInstance(request.Context(), models.DocumentObjects, spec, forms.NewDataWithFiles(parsed.Values(), parsed.Files()), nil, formmodel.PostClean{})
		if err != nil {
			return web.Response{}, err
		}
		prepared, err := form.Prepare()
		if err != nil {
			return web.Response{}, err
		}
		file, _ := prepared.Input().File("file")
		incoming, _ = file.Upload()
		stored, _, err := prepared.SaveFiles(request.Context(), saver)
		if err != nil {
			return web.Response{}, err
		}
		received, err = stored.Model()
		if err != nil {
			return web.Response{}, err
		}
		if err := backend.AtomicRelation(request.Context(), func(tx db.RelationSession) error { return stored.Save(request.Context(), tx, &received) }); err != nil {
			return web.Response{}, err
		}
		return web.NewResponse(201, nil, []byte(received.File))
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("title", "http"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "http.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "binary\x00payload"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/files/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != 201 || response.Body.String() != "documents/http.bin" || read(received.ID).File != "documents/http.bin" {
		t.Fatal("HTTP upload not committed", response.Code, response.Body.String())
	}
	if _, err := incoming.Open(ctx); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("request capability survived handler", err)
	}
	if entries, err := os.ReadDir(policy.TempDir); err != nil || len(entries) != 0 {
		t.Fatal("multipart staging leaked", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root = nil
	root, err = storage.OpenFilesystem(ctx, storage.FilesystemConfig{Directory: rootDir})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := root.Open(ctx, received.File)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(payload) != "binary\x00payload" {
		t.Fatal("stored content lost across request/backend lifetime", err, closeErr)
	}
	t.Run("formset", func(t *testing.T) { runFileSet(t, backend, root) })
	t.Run("images", func(t *testing.T) { runImageBackends(t, backend) })
	t.Run("serving", func(t *testing.T) {
		t.Run("filesystem", func(t *testing.T) { runFileServing(t, backend, root, "disk") })
		t.Run("memory", func(t *testing.T) {
			memory, err := storage.NewMemory(storage.MemoryConfig{MaxBytes: 4 << 20, MaxFiles: 8})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := memory.Close(); err != nil {
					t.Error(err)
				}
			}()
			runFileServing(t, backend, memory, "memory")
		})
	})
	dimensionState := migrate(dimensions.Name)
	dimensionModel, ok := dimensionState.Model("file_reference", "photograph")
	if !ok || !dimensionModel.Fields[2].Equal(swappedImage) {
		t.Fatal("dimension-only migration lost historical meaning")
	}
	if rows, err := models.PhotographObjects.Using(backend).Filter(models.PhotographFields.Title.Exact("filesystem-set-current")).All(ctx); err != nil || len(rows) != 1 || rows[0].Width == nil || *rows[0].Width != 6 || rows[0].Height == nil || *rows[0].Height != 4 {
		t.Fatal("dimension-only migration rewrote stored dimensions", err)
	}
	restoredImage := migrate(change.Name)
	restoredModel, _ := restoredImage.Model("file_reference", "photograph")
	if !restoredModel.Fields[2].Equal(imageMetadata.Fields[2]) {
		t.Fatal("image reference reversal failed")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = nil
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := models.PhotographObjects.Using(backend).Filter(models.PhotographFields.Title.Exact("filesystem-first")).All(ctx); err != nil || len(rows) != 1 || rows[0].Photo == nil || *rows[0].Photo != "" || rows[0].Width != nil || rows[0].Height != nil {
		t.Fatal("image clear/dimensions lost on reopen", err)
	}
	if read(received.ID).File != received.File {
		t.Fatal("file reference lost on DB reopen")
	}
	if restored := migrate(initial.Name); !restored.Equal(previous) {
		t.Fatal("reverse migration did not restore string semantics")
	}
	if read(received.ID).File != received.File {
		t.Fatal("reverse migration rewrote stored name")
	}
}

type zeroEntropy struct{}

func (zeroEntropy) Read(p []byte) (int, error) { clear(p); return len(p), nil }
