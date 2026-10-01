package model_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func saveSet(t *testing.T, collection bool) formmodel.PreparedSet[models.Article] {
	t.Helper()
	metadata := (models.ArticleDescriptor{}).Metadata()
	fields := []string{"title"}
	if collection {
		relation, err := ir.NormalizeManyToManyField("godj_conformance", metadata.Name, ir.ManyToManyField{Name: "labels", GoName: "Labels", Blank: true, Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}})
		if err != nil {
			t.Fatal(err)
		}
		metadata.ManyToMany = append(metadata.ManyToMany, relation)
		fields = append(fields, "labels")
	}
	manager := orm.NewManager[models.Article](collectionInstanceDescriptor{metadata: metadata})
	row, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		t.Fatal(err)
	}
	if collection {
		row, err = row.WithModelChoices("labels", forms.Choice{Value: forms.Integer(7), Label: "Allowed"})
		if err != nil {
			t.Fatal(err)
		}
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.CanDelete = "items", true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := formmodel.BindSet(t.Context(), manager, spec, modelSetData(
		map[string]string{"id": "1", "title": "changed"},
		map[string]string{"id": "2", "title": "two", "DELETE": "on"},
		map[string]string{"title": "new", "labels": "7"},
		map[string]string{"title": "discard", "DELETE": "on"},
		map[string]string{},
	), setArticles(), formmodel.PostClean{}, func(models.Article, ir.ManyToManyField) ([]int64, bool) { return []int64{7}, true })
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestSetSavePlanOwnsMutableRowsAndWriteOrder(t *testing.T) {
	prepared := saveSet(t, false)
	plan, err := prepared.SavePlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rows()) != 2 || len(plan.Deleted()) != 1 || plan.Rows()[0].Index() != 0 || plan.Rows()[1].Index() != 2 {
		t.Fatal("wrong write selection")
	}
	plan.Rows()[0].Model().Title = "server-value"
	*plan.Rows()[0].Model().Summary = "owned-summary"
	plan.Rows()[0].Changed()[0] = "forged"
	before, err := prepared.Rows()[0].Model()
	if err != nil || before.Title != "changed" || *before.Summary != "stored-one" || plan.Rows()[0].Changed()[0] != "title" {
		t.Fatal("plan shared preparation", err)
	}
	backend := &formSaveBackend{}
	calls := 0
	writes, err := plan.Save(t.Context(), backend, formmodel.SetSaveOptions[models.Article]{Delete: func(ctx context.Context, b db.Session, row formmodel.DeletedSetRow[models.Article]) error {
		calls++
		value, err := row.Model()
		if err != nil || value.ID != 2 || b != backend || ctx != t.Context() {
			t.Fatal("delete lost scope or snapshot", err)
		}
		backend.events = append(backend.events, "delete")
		return nil
	}})
	if err != nil || calls != 1 || !reflect.DeepEqual(backend.events, []string{"update", "delete", "insert"}) {
		t.Fatal("write order", backend.events, err)
	}
	if len(writes) != 3 || writes[0].Kind() != "update" || writes[1].Kind() != "delete" || writes[2].Kind() != "create" || writes[2].Index() != 2 || plan.Rows()[1].Model().ID != 17 {
		t.Fatal("write report/key")
	}
	if value, _ := plan.Deleted()[0].Model(); value.ID != 2 {
		t.Fatal("delete callback consumed original identity")
	}
	for _, value := range []any{plan, plan.Rows()[0], writes[0], formmodel.SetSaveOptions[models.Article]{}} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(verb, value), "server-value") {
				t.Fatal("format exposed row")
			}
		}
	}
}

func TestSetSavePlanPreflightDoesNotStartPartialWrites(t *testing.T) {
	prepared := saveSet(t, true)
	for _, name := range []string{"missing_collection", "duplicate_collection", "unknown_collection", "missing_delete", "conflicting_savers", "nil_context", "canceled", "nil_backend", "typed_nil_backend", "expired", "zero_plan", "nil_plan"} {
		t.Run(name, func(t *testing.T) {
			plan, err := prepared.SavePlan()
			if err != nil {
				t.Fatal(err)
			}
			b := &formSaveBackend{}
			var backend db.Session = b
			ctx := t.Context()
			calls := 0
			saver := formmodel.CollectionSaver[models.Article]{Field: "labels", Save: func(context.Context, db.Session, models.Article, []int64) error { calls++; return nil }}
			options := formmodel.SetSaveOptions[models.Article]{Collections: []formmodel.CollectionSaver[models.Article]{saver}, Delete: func(context.Context, db.Session, formmodel.DeletedSetRow[models.Article]) error { calls++; return nil }}
			switch name {
			case "missing_collection":
				options.Collections = nil
			case "duplicate_collection":
				options.Collections = append(options.Collections, saver)
			case "unknown_collection":
				options.Collections[0].Field = "unknown"
			case "missing_delete":
				options.Delete = nil
			case "conflicting_savers":
				options.Save = func(context.Context, db.Session, *formmodel.SetSaveRow[models.Article]) error { calls++; return nil }
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_backend":
				backend = nil
			case "typed_nil_backend":
				backend = (*formSaveBackend)(nil)
			case "expired":
				backend = &formSaveSession{formSaveBackend: b, expired: true}
			case "zero_plan":
				plan = &formmodel.SetSavePlan[models.Article]{}
			case "nil_plan":
				plan = nil
			}
			writes, err := plan.Save(ctx, backend, options)
			if err == nil || len(writes) != 0 || calls != 0 || len(b.events) != 0 {
				t.Fatal("invalid plan started I/O", name, writes, b.events, err)
			}
		})
	}
	if _, err := (formmodel.PreparedSet[models.Article]{}).SavePlan(); err == nil {
		t.Fatal("zero preparation accepted")
	}
}

func TestSetSavePlanPartialFailureRetainsKeysAndExactError(t *testing.T) {
	prepared := saveSet(t, false)
	plan, err := prepared.SavePlan()
	if err != nil {
		t.Fatal(err)
	}
	backend := &formSaveBackend{}
	failure := errors.New("late audit failure")
	calls := 0
	writes, err := plan.Save(t.Context(), backend, formmodel.SetSaveOptions[models.Article]{
		Delete: func(context.Context, db.Session, formmodel.DeletedSetRow[models.Article]) error { calls++; return nil },
		Save: func(ctx context.Context, b db.Session, row *formmodel.SetSaveRow[models.Article]) error {
			calls++
			if err := row.Save(ctx, b); err != nil {
				return err
			}
			if !row.Existing() {
				return failure
			}
			return nil
		},
	})
	if err != failure || calls != 3 || len(writes) != 3 || writes[2].Err() != failure || writes[0].Err() != nil || plan.Rows()[1].Model().ID != 17 {
		t.Fatal("partial result lost identity/error", writes, err)
	}
	// Saving the caller-owned new-row pointer again updates the assigned key.
	if err := plan.Rows()[1].Save(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.events, []string{"update", "insert", "update"}) {
		t.Fatal("repeated row inserted a duplicate", backend.events)
	}
	for _, failAt := range []int{0, 1} {
		plan, err := prepared.SavePlan()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		options := formmodel.SetSaveOptions[models.Article]{
			Save: func(context.Context, db.Session, *formmodel.SetSaveRow[models.Article]) error {
				count++
				if failAt == 0 {
					return failure
				}
				return nil
			},
			Delete: func(context.Context, db.Session, formmodel.DeletedSetRow[models.Article]) error {
				count++
				return failure
			},
		}
		writes, err := plan.Save(t.Context(), backend, options)
		if err != failure || count != failAt+1 || len(writes) != count {
			t.Fatal("continued after failure", count, err)
		}
	}
}

func TestSetSavePlanDeferredCollectionsCheckEveryKeyFirst(t *testing.T) {
	prepared := saveSet(t, true)
	plan, err := prepared.SavePlan()
	if err != nil {
		t.Fatal(err)
	}
	backend := &formSaveBackend{}
	calls := 0
	saver := formmodel.CollectionSaver[models.Article]{Field: "labels", Save: func(_ context.Context, b db.Session, value models.Article, keys []int64) error {
		calls++
		if b != backend || (value.ID != 1 && value.ID != 17) {
			t.Fatal("collection lost scope/key")
		}
		if value.ID == 1 && len(keys) != 0 || value.ID == 17 && !reflect.DeepEqual(keys, []int64{7}) {
			t.Fatal("lost clear/selection", keys)
		}
		return nil
	}}
	err = plan.SaveCollections(t.Context(), backend, saver)
	var failure *formmodel.Error
	if !errors.As(err, &failure) || failure.Code != "primary_key_required" || calls != 0 || len(backend.events) != 0 {
		t.Fatal("unsaved late model allowed early relation write", err, calls)
	}
	for _, row := range plan.Rows() {
		if err := models.ArticleObjects.Save(t.Context(), backend, row.Model()); err != nil {
			t.Fatal(err)
		}
	}
	if err := plan.SaveCollections(t.Context(), backend, saver); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !reflect.DeepEqual(backend.events, []string{"update", "insert"}) {
		t.Fatal("deferred phase wrote scalars/deletions", calls, backend.events)
	}
}

func TestSetSavePlanCancellationAndExpiredScopeStopCallbacks(t *testing.T) {
	for _, expired := range []bool{false, true} {
		prepared := saveSet(t, false)
		plan, err := prepared.SavePlan()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		session := &formSaveSession{formSaveBackend: &formSaveBackend{}}
		calls := 0
		writes, err := plan.Save(ctx, session, formmodel.SetSaveOptions[models.Article]{
			Save: func(context.Context, db.Session, *formmodel.SetSaveRow[models.Article]) error {
				calls++
				if expired {
					session.expired = true
				} else {
					cancel()
				}
				return nil
			},
			Delete: func(context.Context, db.Session, formmodel.DeletedSetRow[models.Article]) error { calls++; return nil },
		})
		cancel()
		if err == nil || calls != 1 || len(writes) != 1 || writes[0].Err() != err {
			t.Fatal("scope expiry continued or lost failure", writes, err)
		}
	}
}

func TestSetSavePlanIndependentCopiesCanSaveConcurrently(t *testing.T) {
	prepared := saveSet(t, false)
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Go(func() {
			plan, err := prepared.SavePlan()
			if err != nil {
				t.Error(err)
				return
			}
			*plan.Rows()[0].Model().Summary = "owned"
			backend := &formSaveBackend{}
			if _, err := plan.Save(t.Context(), backend, formmodel.SetSaveOptions[models.Article]{Delete: func(context.Context, db.Session, formmodel.DeletedSetRow[models.Article]) error { return nil }}); err != nil {
				t.Error(err)
			}
			if plan.Rows()[1].Model().ID != 17 {
				t.Error("missing generated key")
			}
		})
	}
	group.Wait()
	before, err := prepared.Rows()[0].Model()
	if err != nil || *before.Summary != "stored-one" {
		t.Fatal("concurrent copies mutated preparation", err)
	}
}
