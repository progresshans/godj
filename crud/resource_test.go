package crud_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	article "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func labelInput(t *testing.T, values map[string]query.Value) orm.ModelInput[models.Label] {
	t.Helper()
	input, err := models.LabelObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestResourceOwnsSelectionAndRejectsUnboundOrServerInputBeforeIO(t *testing.T) {
	fields := []string{"name"}
	resource, err := crud.New(models.LabelObjects, crud.Config{Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = "category"
	copy := resource.Fields()
	copy[0] = "category"
	if !slices.Equal(resource.Fields(), []string{"name"}) {
		t.Fatal("writable selection aliases caller storage")
	}
	for _, fields := range [][]string{nil, {"id"}, {"missing"}, {"name", "name"}} {
		if _, err := crud.New(models.LabelObjects, crud.Config{Fields: fields}); err == nil {
			t.Fatal("invalid startup selection accepted", fields)
		}
	}
	if _, err := crud.New(models.TicketObjects, crud.Config{Fields: []string{"external_payload_digest"}}); err == nil {
		t.Fatal("noneditable field became writable")
	}
	if _, err := crud.New(models.LabelObjects, crud.Config{ReadOnly: true, Fields: []string{"name"}}); err == nil {
		t.Fatal("read-only resource declared writable fields")
	}
	backend := &countedSession{}
	for _, input := range []orm.ModelInput[models.Label]{{}, labelInput(t, map[string]query.Value{"name": query.String("ok"), "category": query.Integer(2)})} {
		if result, err := resource.CreateInSession(t.Context(), backend, input, crud.SaveOptions[models.Label]{}); err == nil || result.Value.ID != 0 {
			t.Fatal("invalid input reached creation", err)
		}
		if result, err := resource.UpdateInSession(t.Context(), backend, 1, input, crud.SaveOptions[models.Label]{}); err == nil || result.Value.ID != 0 {
			t.Fatal("invalid input reached lookup", err)
		}
	}
	bound, err := labelInput(t, map[string]query.Value{"name": query.String("ok")}).WithTargetKey(query.Integer(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resource.CreateInSession(t.Context(), backend, bound, crud.SaveOptions[models.Label]{}); err == nil {
		t.Fatal("existing form became create")
	}
	if _, err := resource.UpdateInSession(t.Context(), backend, 4, bound, crud.SaveOptions[models.Label]{}); err == nil {
		t.Fatal("bound input crossed object identity")
	}
	readonly, err := crud.New(models.LabelObjects, crud.Config{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readonly.CreateInSession(t.Context(), backend, labelInput(t, nil), crud.SaveOptions[models.Label]{}); !errors.Is(err, crud.ErrReadOnly) {
		t.Fatal("readonly create", err)
	}
	if _, err := readonly.DeleteInSession(t.Context(), backend, 1, crud.DeleteOptions[models.Label]{}); !errors.Is(err, crud.ErrReadOnly) {
		t.Fatal("readonly delete", err)
	}
	if _, err := resource.DeleteInSession(t.Context(), backend, 1, crud.DeleteOptions[models.Label]{}); err == nil {
		t.Fatal("implicit deletion policy accepted")
	}
	if backend.calls != 0 {
		t.Fatal("rejected policy performed I/O", backend.calls)
	}
	metadata := (article.ArticleDescriptor{}).Metadata()
	metadata.Fields[2].Default.Boolean = true
	foreign := orm.NewManager[article.Article](changedArticleSource{metadata: metadata})
	input, err := foreign.Input(map[string]query.Value{"title": query.String("same Go type")})
	if err != nil {
		t.Fatal(err)
	}
	articles, err := crud.New(article.ArticleObjects, crud.Config{Fields: []string{"title"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := articles.CreateInSession(t.Context(), backend, input, crud.SaveOptions[article.Article]{}); err == nil || backend.calls != 0 {
		t.Fatal("different model policy crossed the resource", err)
	}
}

func TestResourceSharesScopedCRUDCurrentChecksAndExplicitDelete(t *testing.T) {
	backend, err := sqlite.OpenMemory(t.Context(), "crud-scoped")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE helpdesk_label (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(64) NOT NULL, category_id INTEGER NOT NULL, UNIQUE(category_id,name))`); err != nil {
		t.Fatal(err)
	}
	resource, err := crud.New(models.LabelObjects, crud.Config{Fields: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}
	owned := resource.Filter(models.LabelFields.CategoryID.Exact(10))
	create := func(name string, category int64) models.Label {
		t.Helper()
		result, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (crud.Mutation[models.Label], error) {
			return resource.CreateInSession(ctx, session, labelInput(t, map[string]query.Value{"name": query.String(name)}), crud.SaveOptions[models.Label]{ServerValues: map[string]query.Value{"category": query.Integer(category)}})
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Value
	}
	first, second, foreign := create("first", 10), create("second", 10), create("foreign", 20)
	page, err := owned.List(t.Context(), backend, crud.ListOptions[models.Label]{Offset: 1, Limit: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != second.ID {
		t.Fatal("scope/count/page mismatch", page, err)
	}
	if _, found, err := owned.Get(t.Context(), backend, foreign.ID); err != nil || found {
		t.Fatal("foreign object escaped scope", err)
	}
	if page, err := owned.Filter(models.LabelFields.CategoryID.Exact(20)).List(t.Context(), backend, crud.ListOptions[models.Label]{Limit: 10}); err != nil || page.Total != 0 {
		t.Fatal("derived scope broadened its parent", page, err)
	}
	denied := errors.New("private current policy denial")
	for _, mode := range []string{"scope", "denied", "key substitution", "success", "no-op", "unique"} {
		t.Run(mode, func(t *testing.T) {
			id, name := first.ID, "changed"
			if mode == "scope" {
				id = foreign.ID
			}
			if mode == "unique" {
				name = "second"
			}
			input := labelInput(t, map[string]query.Value{"name": query.String(name)})
			if mode == "no-op" {
				input = labelInput(t, nil)
			}
			calls, writes := 0, 0
			result, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (crud.Mutation[models.Label], error) {
				observed := &observedSession{Session: session, writes: &writes}
				return owned.UpdateInSession(ctx, observed, id, input, crud.SaveOptions[models.Label]{Before: func(ctx context.Context, session db.Session, current, candidate *models.Label) error {
					calls++
					if current == nil || current.ID != first.ID || candidate.CategoryID != 10 {
						t.Fatal("policy did not receive the fresh scoped object")
					}
					current.Name = "callback-only"
					if mode == "denied" {
						return denied
					}
					if mode == "key substitution" {
						*candidate = models.NewLabelWithID(foreign.ID)
						candidate.Name, candidate.CategoryID = "forged", 10
					}
					return nil
				}})
			})
			ok := mode == "success" || mode == "no-op"
			if (err == nil) != ok || !ok && result.Value.ID != 0 {
				t.Fatal("unconfirmed update result", result, err)
			}
			if mode == "scope" && (err != crud.ErrNotFound || calls != 0) {
				t.Fatal("scope miss ran object policy", err, calls)
			}
			if mode != "scope" && calls != 1 {
				t.Fatal("object policy did not run once", calls)
			}
			if mode == "denied" && !errors.Is(err, denied) {
				t.Fatal("policy error was replaced", err)
			}
			if mode == "unique" {
				if failures, ok := validation.Rejected(err); !ok || failures.Empty() {
					t.Fatal("current uniqueness was not checked", err)
				}
			}
			if mode == "success" {
				if writes != 1 || result.Before == nil || result.Before.Name != "first" || !slices.Equal(result.ChangedFields, []string{"name"}) {
					t.Fatal("actual update or before ownership changed", result, writes)
				}
			} else if writes != 0 {
				t.Fatal("rejection/no-op wrote data", writes)
			}
		})
	}
	removed, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (models.Label, error) {
		return owned.DeleteInSession(ctx, session, first.ID, crud.DeleteOptions[models.Label]{Delete: func(ctx context.Context, session db.Session, value models.Label) (int64, error) {
			return models.LabelObjects.Delete(ctx, session, &value)
		}})
	})
	if err != nil || removed.ID != first.ID || removed.Name != "changed" {
		t.Fatal("explicit delete failed", removed, err)
	}
	if _, found, err := owned.Get(t.Context(), backend, first.ID); err != nil || found {
		t.Fatal("delete did not persist", err)
	}
}

func TestResourceRequiresCompleteCollectionConfigurationBeforeAnyIO(t *testing.T) {
	resource, err := crud.New(models.TicketObjects, crud.Config{Fields: []string{"subject", "labels"}})
	if err != nil {
		t.Fatal(err)
	}
	input, err := models.TicketObjects.Input(map[string]query.Value{"subject": query.String("ticket")})
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithCollections(map[string][]int64{"labels": {1, 1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &countedSession{}
	save := func(context.Context, db.Session, models.Ticket, []int64) (bool, error) {
		t.Fatal("invalid collection configuration called a saver")
		return false, nil
	}
	for _, savers := range [][]crud.CollectionSaver[models.Ticket]{nil, {{Field: "labels"}}, {{Field: "other", Save: save}}, {{Field: "labels", Save: save}, {Field: "labels", Save: save}}} {
		result, err := resource.CreateInSession(t.Context(), backend, input, crud.SaveOptions[models.Ticket]{ServerValues: map[string]query.Value{"category": query.Integer(1)}, Collections: savers})
		if err == nil || !reflect.DeepEqual(result, crud.Mutation[models.Ticket]{}) || backend.calls != 0 {
			t.Fatal("invalid collection policy reached I/O", err, backend.calls)
		}
	}
}

type changedArticleSource struct {
	article.ArticleDescriptor
	metadata ir.Model
}

func (descriptor changedArticleSource) Metadata() ir.Model { return descriptor.metadata.Clone() }

type countedSession struct{ calls int }

func (session *countedSession) Query(context.Context, query.Plan) (db.Rows, error) {
	session.calls++
	return nil, errors.New("unexpected I/O")
}
func (session *countedSession) Insert(context.Context, query.InsertPlan) (int64, error) {
	session.calls++
	return 1, nil
}
func (session *countedSession) Update(context.Context, query.UpdatePlan) (int64, error) {
	session.calls++
	return 1, nil
}
func (session *countedSession) Delete(context.Context, query.DeletePlan) (int64, error) {
	session.calls++
	return 1, nil
}

type observedSession struct {
	db.Session
	writes *int
}

func (session *observedSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *observedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	*session.writes++
	return session.Session.Update(ctx, plan)
}
