package model_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

func fileSaveSet(t *testing.T) formmodel.PreparedSet[models.Article] {
	t.Helper()
	manager, row := fileModel(t)
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.CanDelete = true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	current := setArticles()
	current[0].Title, current[1].Title = "old/one.txt", "old/two.txt"
	file, err := uploads.NewFile("same.txt", "text/plain", []byte("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	data := forms.NewDataWithFiles(map[string][]string{"items-TOTAL_FORMS": {"5"}, "items-INITIAL_FORMS": {"2"}, "items-0-id": {"1"}, "items-1-id": {"2"}, "items-1-DELETE": {"on"}, "items-4-DELETE": {"on"}}, map[string][]uploads.File{"items-0-title": {file}, "items-1-title": {file}, "items-2-title": {file}, "items-4-title": {file}})
	bound, err := formmodel.BindSet(t.Context(), manager, spec, data, current, formmodel.PostClean{})
	if err != nil || !bound.Valid() {
		t.Fatal("file set binding", err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestSetFilesPreflightAllRowsBeforePublishing(t *testing.T) {
	prepared := fileSaveSet(t)
	if _, err := prepared.SavePlan(); err == nil {
		t.Fatal("pending upload admitted to DB save plan")
	}
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	probe := &fileSaveProbe{Backend: root}
	for _, mode := range []string{"late_missing", "late_invalid_name", "selection_failure", "nil_selector", "nil_context", "canceled", "zero_prepared"} {
		t.Run(mode, func(t *testing.T) {
			set := prepared
			ctx := t.Context()
			selection := []int{}
			selector := func(row formmodel.PreparedSetRow[models.Article]) ([]formmodel.FileSaver[models.Article], error) {
				selection = append(selection, row.Index())
				saver := fileSaver("title", probe)
				if row.Index() == 2 {
					switch mode {
					case "late_missing":
						return nil, nil
					case "selection_failure":
						return nil, errors.New("denied")
					case "late_invalid_name":
						saver.Name = func(models.Article, uploads.File) (string, error) { return "../outside", nil }
					}
				}
				return []formmodel.FileSaver[models.Article]{saver}, nil
			}
			switch mode {
			case "nil_selector":
				selector = nil
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "zero_prepared":
				set = formmodel.PreparedSet[models.Article]{}
			}
			result, receipts, err := set.SaveFiles(ctx, selector)
			if err == nil || len(receipts) != 0 || probe.calls != 0 {
				t.Fatal("invalid later row published earlier upload", mode, probe.calls, err)
			}
			if _, err := result.SavePlan(); err == nil {
				t.Fatal("failure returned usable preparation")
			}
			if mode == "late_missing" && !reflect.DeepEqual(selection, []int{0, 2}) {
				t.Fatal("wrong publication selection", selection)
			}
		})
	}
}

func TestSetFilesKeepActualNamesAndIndexedPartialOutcomes(t *testing.T) {
	for _, failed := range []bool{false, true} {
		prepared := fileSaveSet(t)
		root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		probe := &fileSaveProbe{Backend: root}
		failure := &storage.Error{Code: "synthetic_uncertain_publication", Outcome: storage.Uncertain}
		probe.save = func(ctx context.Context, name string, r io.Reader, options storage.SaveOptions) (storage.Info, error) {
			info, err := root.Save(ctx, name, r, options)
			if err == nil && failed && probe.calls == 2 {
				return info, failure
			}
			return info, err
		}
		selected := []int{}
		resolved, receipts, err := prepared.SaveFiles(t.Context(), func(row formmodel.PreparedSetRow[models.Article]) ([]formmodel.FileSaver[models.Article], error) {
			selected = append(selected, row.Index())
			return []formmodel.FileSaver[models.Article]{fileSaver("title", probe)}, nil
		})
		if !reflect.DeepEqual(selected, []int{0, 2}) || probe.calls != 2 || len(receipts) != 2 || receipts[0].Index() != 0 || receipts[1].Index() != 2 {
			t.Fatal("lost publication row selection", selected, receipts, err)
		}
		if receipts[0].File().Outcome() != storage.Published || receipts[0].File().Info().Name() == receipts[1].File().Info().Name() {
			t.Fatal("actual collision outcomes lost")
		}
		for _, receipt := range receipts {
			reader, err := root.Open(t.Context(), receipt.File().Info().Name())
			if err != nil {
				t.Fatal("published file compensated/deleted", err)
			}
			payload, err := io.ReadAll(reader)
			closed := reader.Close()
			if err != nil || closed != nil || string(payload) != "replacement" {
				t.Fatal("published content differs", err, closed)
			}
		}
		if _, err := prepared.SavePlan(); err == nil {
			t.Fatal("storage consumed original preparation")
		}
		if failed {
			if !errors.Is(err, failure) || receipts[1].File().Outcome() != storage.Uncertain {
				t.Fatal("lost uncertain outcome", err)
			}
			if _, err := resolved.SavePlan(); err == nil {
				t.Fatal("partial storage admitted to DB")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			plan, err := resolved.SavePlan()
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Rows()) != 2 || len(plan.Deleted()) != 1 || plan.Rows()[0].Model().Title != receipts[0].File().Info().Name() || plan.Rows()[1].Model().Title != receipts[1].File().Info().Name() {
				t.Fatal("file names not connected to plan")
			}
		}
	}
}

func TestSetSavePlanSkipsUnchangedRequiredExtraAndReadOnlyRows(t *testing.T) {
	manager, row := fileModel(t)
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.MinForms = 1
	config.ValidateMin = false
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := formmodel.BindSet(t.Context(), manager, spec, forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"0"}}), nil, formmodel.PostClean{})
	if err != nil || !bound.Valid() {
		t.Fatal("required blank file row", err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if bound.FormSet().TotalForms() != 1 || len(prepared.Rows()) != 0 {
		t.Fatal("unchanged required extra was selected")
	}
	plan, err := prepared.SavePlan()
	if err != nil || len(plan.Rows()) != 0 {
		t.Fatal("unchanged required extra became a write", err)
	}
	// Existing unchanged rows remain in Prepare but not in the save selection.
	unchanged, err := formmodel.BindSet(t.Context(), manager, spec, forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"2"}, "items-0-id": {"1"}, "items-1-id": {"2"}}), setArticles(), formmodel.PostClean{})
	if err != nil || !unchanged.Valid() {
		t.Fatal("unchanged current rows", err)
	}
	unchangedPrepared, err := unchanged.Prepare()
	if err != nil || len(unchangedPrepared.Rows()) != 2 {
		t.Fatal("current candidates lost", err)
	}
	unchangedPlan, err := unchangedPrepared.SavePlan()
	if err != nil || len(unchangedPlan.Rows()) != 0 {
		t.Fatal("unchanged current row became a write", err)
	}
	backend := &formSaveBackend{}
	if writes, err := unchangedPlan.Save(t.Context(), backend, formmodel.SetSaveOptions[models.Article]{}); err != nil || len(writes) != 0 || len(backend.events) != 0 {
		t.Fatal("empty valid plan wrote data", err)
	}
	config.ReadOnlyInitial = true
	spec, err = forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	current := setArticles()[:1]
	file, err := uploads.NewFile("forged.txt", "text/plain", []byte("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	bound, err = formmodel.BindSet(t.Context(), manager, spec, forms.NewDataWithFiles(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"1"}, "items-0-id": {"1"}}, map[string][]uploads.File{"items-0-title": {file}}), current, formmodel.PostClean{})
	if err != nil || !bound.Valid() {
		t.Fatal("readonly row", err)
	}
	prepared, err = bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	resolved, receipts, err := prepared.SaveFiles(t.Context(), func(formmodel.PreparedSetRow[models.Article]) ([]formmodel.FileSaver[models.Article], error) {
		calls++
		return nil, errors.New("unexpected")
	})
	if err != nil || calls != 0 || len(receipts) != 0 {
		t.Fatal("readonly file published", err)
	}
	plan, err = resolved.SavePlan()
	if err != nil || len(plan.Rows()) != 0 {
		t.Fatal("readonly row became a write", err)
	}
}
