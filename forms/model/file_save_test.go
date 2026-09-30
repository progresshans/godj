package model_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

// String storage is shared; only the canonical field semantics are different.
// A separate generated external consumer covers the actual FileField ABI.
type fileModelDescriptor struct {
	models.ArticleDescriptor
	metadata ir.Model
}

func (d fileModelDescriptor) Metadata() ir.Model { return d.metadata }

func fileModel(t *testing.T) (orm.Manager[models.Article], forms.Spec) {
	t.Helper()
	metadata := (models.ArticleDescriptor{}).Metadata()
	for i := range metadata.Fields {
		field := &metadata.Fields[i]
		if field.Name == "title" || field.Name == "summary" {
			field.Kind, field.Blank, field.MaxLength = ir.FieldFile, true, 40
		}
	}
	manager := orm.NewManager[models.Article](fileModelDescriptor{metadata: metadata})
	spec, err := formmodel.NewSpecForFields(metadata, []string{"title", "summary"})
	if err != nil {
		t.Fatal(err)
	}
	return manager, spec
}

func prepareFiles(t *testing.T, two bool) formmodel.PreparedInstance[models.Article] {
	t.Helper()
	manager, spec := fileModel(t)
	current := models.NewArticleWithID(42)
	current.Title, current.Summary = "old/title.txt", new("old/summary.txt")
	file, err := uploads.NewFile("same.txt", "text/plain", []byte("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]uploads.File{"title": {file}}
	if two {
		files["summary"] = []uploads.File{file}
	}
	form, err := formmodel.BindInstance(manager, spec, forms.NewDataWithFiles(nil, files), &current, formmodel.PostClean{})
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
	return prepared
}

func fileSaver(name string, backend storage.Backend) formmodel.FileSaver[models.Article] {
	return formmodel.FileSaver[models.Article]{Field: name, Backend: backend, Name: func(_ models.Article, file uploads.File) (string, error) { return "documents/" + file.Name(), nil }}
}

type fileSaveProbe struct {
	storage.Backend
	calls int
	save  func(context.Context, string, io.Reader, storage.SaveOptions) (storage.Info, error)
}

func (b *fileSaveProbe) Save(ctx context.Context, name string, r io.Reader, options storage.SaveOptions) (storage.Info, error) {
	b.calls++
	if b.save != nil {
		return b.save(ctx, name, r, options)
	}
	return b.Backend.Save(ctx, name, r, options)
}

func TestModelFilePreparationCannotDiscardPendingUploads(t *testing.T) {
	prepared := prepareFiles(t, false)
	names := prepared.PendingFiles()
	if !reflect.DeepEqual(names, []string{"title"}) {
		t.Fatal("missing model upload", names)
	}
	names[0] = "changed"
	if prepared.PendingFiles()[0] != "title" {
		t.Fatal("pending list shares mutable memory")
	}
	backend := &formSaveBackend{}
	instance := models.NewArticleWithID(42)
	for _, err := range []error{
		func() error { _, err := prepared.Model(); return err }(),
		prepared.Save(t.Context(), backend, &instance),
		prepared.SaveCollections(t.Context(), backend, instance),
	} {
		var failure *formmodel.Error
		if !errors.As(err, &failure) || failure.Code != "upload_pending" {
			t.Fatal("pending upload bypassed explicit storage", err)
		}
	}
	if len(backend.events) != 0 {
		t.Fatal("pending upload reached database", backend.events)
	}
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	resolved, results, err := prepared.SaveFiles(t.Context(), fileSaver("title", root))
	if err != nil || len(results) != 1 || results[0].Field() != "title" || results[0].Outcome() != storage.Published {
		t.Fatal("publish failed", err)
	}
	value, err := resolved.Model()
	if err != nil || value.Title != results[0].Info().Name() || value.Summary == nil || *value.Summary != "old/summary.txt" {
		t.Fatal("storage name or excluded reference lost", err)
	}
	if value.Title != "documents/same.txt" || len(resolved.PendingFiles()) != 0 {
		t.Fatal("proposal persisted instead of actual name")
	}
	if _, err := prepared.Model(); err == nil {
		t.Fatal("SaveFiles mutated original preparation")
	}
	*value.Summary = "changed"
	again, _ := resolved.Model()
	if *again.Summary != "old/summary.txt" {
		t.Fatal("resolved model shares nullable reference")
	}
	if err := resolved.Save(t.Context(), backend, &again); err != nil || !reflect.DeepEqual(backend.events, []string{"update"}) {
		t.Fatal("resolved scalar write missing", err, backend.events)
	}
}

func TestModelFileSaverPreflightRunsBeforeAnyPublication(t *testing.T) {
	prepared := prepareFiles(t, true)
	for _, mode := range []string{"missing", "duplicate", "unselected", "nil_backend", "typed_nil", "nil_namer", "name_error", "invalid_name", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			probe := &fileSaveProbe{}
			first, second := fileSaver("title", probe), fileSaver("summary", probe)
			savers := []formmodel.FileSaver[models.Article]{first, second}
			ctx := t.Context()
			switch mode {
			case "missing":
				savers = savers[:1]
			case "duplicate":
				savers = append(savers, first)
			case "unselected":
				savers = append(savers, fileSaver("published", probe))
			case "nil_backend":
				savers[1].Backend = nil
			case "typed_nil":
				var missing *fileSaveProbe
				savers[1].Backend = missing
			case "nil_namer":
				savers[1].Name = nil
			case "name_error":
				savers[1].Name = func(models.Article, uploads.File) (string, error) { return "", errors.New("name failed") }
			case "invalid_name":
				savers[1].Name = func(models.Article, uploads.File) (string, error) { return "../outside", nil }
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			resolved, results, err := prepared.SaveFiles(ctx, savers...)
			if err == nil || len(results) != 0 || probe.calls != 0 {
				t.Fatal("invalid file setup caused publication", err, len(results), probe.calls)
			}
			if _, err := resolved.Model(); err == nil {
				t.Fatal("failure returned apparently resolved model")
			}
		})
	}
}

func TestModelFilePublicationResultsSurviveLaterFailures(t *testing.T) {
	prepared := prepareFiles(t, true)
	for _, mode := range []string{"not_published", "published", "uncertain", "oversized_result", "wrong_size"} {
		t.Run(mode, func(t *testing.T) {
			root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			cause := errors.New("synthetic backend failure")
			second := &fileSaveProbe{save: func(ctx context.Context, name string, reader io.Reader, options storage.SaveOptions) (storage.Info, error) {
				if options.MaxLength != 40 {
					t.Fatal("lost complete name budget")
				}
				if mode == "not_published" {
					return storage.Info{}, &storage.Error{Code: "save_failed", Outcome: storage.NotPublished, Cause: cause}
				}
				info, err := root.Save(ctx, "second.txt", reader, options)
				if err != nil {
					return info, err
				}
				if mode == "oversized_result" {
					value, err := storage.NewInfo(strings.Repeat("x", 41), info.Size())
					return value, err
				}
				if mode == "wrong_size" {
					value, err := storage.NewInfo(info.Name(), info.Size()+1)
					return value, err
				}
				outcome := storage.Uncertain
				if mode == "published" {
					outcome = storage.Published
				}
				return info, &storage.Error{Code: "save_failed", Outcome: outcome, Cause: cause}
			}}
			resolved, results, err := prepared.SaveFiles(t.Context(), fileSaver("title", root), fileSaver("summary", second))
			if err == nil || len(results) != 2 || results[0].Outcome() != storage.Published || results[1].Field() != "summary" || results[1].Err() == nil {
				t.Fatal("partial outcome lost", err, len(results))
			}
			want := storage.Uncertain
			if mode == "not_published" {
				want = storage.NotPublished
			}
			if mode == "published" {
				want = storage.Published
			}
			if results[1].Outcome() != want {
				t.Fatal("failure outcome changed", results[1].Outcome(), want)
			}
			if mode != "oversized_result" && mode != "wrong_size" && !errors.Is(err, cause) {
				t.Fatal("backend error lost")
			}
			if _, err := resolved.Model(); err == nil {
				t.Fatal("partly published preparation accepted")
			}
			if _, err := root.Stat(t.Context(), results[0].Info().Name()); err != nil {
				t.Fatal("earlier publication was compensated without authority", err)
			}
			if mode == "published" || mode == "uncertain" {
				if _, err := root.Stat(t.Context(), "second.txt"); err != nil {
					t.Fatal("failing publication was deleted", err)
				}
			}
			if strings.Contains(fmt.Sprintf("%+v", results), "second.txt") {
				t.Fatal("publication diagnostics disclose references")
			}
		})
	}
}

func TestModelFilePreparationsAreImmutableAcrossConcurrentStorage(t *testing.T) {
	prepared := prepareFiles(t, false)
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	var group sync.WaitGroup
	results := make(chan string, 4)
	for range 4 {
		group.Go(func() {
			resolved, receipts, err := prepared.SaveFiles(t.Context(), fileSaver("title", root))
			if err != nil {
				t.Error(err)
				return
			}
			value, err := resolved.Model()
			if err != nil || value.Title != receipts[0].Info().Name() {
				t.Error("concurrent result mixed", err)
				return
			}
			results <- value.Title
		})
	}
	group.Wait()
	close(results)
	names := map[string]bool{}
	for name := range results {
		if names[name] {
			t.Error("concurrent upload overwrote another")
		}
		names[name] = true
	}
	if len(names) != 4 || len(prepared.PendingFiles()) != 1 {
		t.Fatal("concurrent preparation lost ownership")
	}
}

func TestModelFileFormsetPreservesInitialReferencesAndPendingRows(t *testing.T) {
	manager, row := fileModel(t)
	current := models.NewArticleWithID(42)
	current.Title, current.Summary = "old/title.txt", new("old/summary.txt")
	config := forms.DefaultSetConfig()
	config.Prefix = "files"
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := formmodel.UnboundSet(manager, spec, []models.Article{current})
	if err != nil || unbound.FormSet().InitialForms() != 1 {
		t.Fatal("file initial projection failed", err)
	}
	file, err := uploads.NewFile("new.txt", "text/plain", []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]string{"files-TOTAL_FORMS": {"2"}, "files-INITIAL_FORMS": {"1"}, "files-0-id": {"42"}, "files-0-summary-clear": {"on"}}
	set, err := formmodel.BindSet(manager, spec, forms.NewDataWithFiles(values, map[string][]uploads.File{"files-1-title": {file}}), []models.Article{current}, formmodel.PostClean{})
	if err != nil || !set.Valid() {
		t.Fatal("file rows did not bind", err)
	}
	prepared, err := set.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	rows := prepared.Rows()
	if len(rows) != 2 {
		t.Fatal("clear or upload row was dropped", len(rows))
	}
	retained, err := rows[0].Model()
	if err != nil || retained.Title != current.Title || retained.Summary == nil || *retained.Summary != "" {
		t.Fatal("file omission/nullable clear changed meaning", err)
	}
	if _, err := rows[1].Model(); err == nil {
		t.Fatal("formset lost pending upload")
	}
	if !reflect.DeepEqual(rows[1].Prepared().PendingFiles(), []string{"title"}) {
		t.Fatal("new row upload not assigned to model")
	}
	values["files-0-id"] = []string{"99"}
	forged, err := formmodel.BindSet(manager, spec, forms.NewDataWithFiles(values, map[string][]uploads.File{"files-1-title": {file}}), []models.Article{current}, formmodel.PostClean{})
	if err != nil || forged.Valid() {
		t.Fatal("file row bypassed identity checks", err)
	}
}

func TestModelFileInitialRejectsUploadsAndScalarNamingOverrides(t *testing.T) {
	manager, spec := fileModel(t)
	metadata, err := manager.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	file, err := uploads.NewFile("new.txt", "text/plain", []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Bind(forms.NewDataWithFiles(nil, map[string][]uploads.File{"title": {file}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := form.Cleaned().Get("title")
	if _, err := formmodel.Bind(metadata, spec, forms.NewData(nil), map[string]forms.Value{"title": value}, formmodel.PostClean{}); err == nil {
		t.Fatal("initial reference accepted request upload")
	}
	if _, err := formmodel.NewSpec(metadata, formmodel.OverrideField("title", formmodel.WithStringNormalizer(strings.TrimSpace))); err == nil {
		t.Fatal("file names accepted implicit text normalizer")
	}
	if _, err := formmodel.NewSpec(metadata, formmodel.OverrideField("title", formmodel.WithMaxLength(41))); err == nil {
		t.Fatal("input length widened model storage")
	}
	if _, err := formmodel.Bind(metadata, spec, forms.NewData(nil), nil, formmodel.PostClean{Fields: []string{"title"}, Clean: func(forms.Values) (forms.Values, validation.Errors) { return forms.Values{}, validation.Errors{} }}); err == nil {
		t.Fatal("scalar clean silently replaced file intent")
	}
}
