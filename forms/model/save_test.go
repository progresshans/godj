package model_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type formSaveBackend struct {
	events  []string
	failure error
}

func (b *formSaveBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	b.events = append(b.events, "query")
	return nil, errors.New("unexpected read")
}
func (b *formSaveBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	b.events = append(b.events, "insert")
	return 17, b.failure
}
func (b *formSaveBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	b.events = append(b.events, "update")
	return 1, b.failure
}
func (b *formSaveBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	b.events = append(b.events, "delete")
	return 1, b.failure
}
func (b *formSaveBackend) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	b.events = append(b.events, "begin")
	return errors.New("unexpected implicit transaction")
}

type formSaveSession struct {
	*formSaveBackend
	expired bool
}

func (b *formSaveSession) ValidateSession(context.Context) error {
	if b.expired {
		return errors.New("expired")
	}
	return nil
}
func (b *formSaveSession) RelationSetNull(context.Context, query.RelationSetNullPlan) (int64, error) {
	return 0, errors.New("unexpected relation mutation")
}

type formSaveNarrow struct{ db.Session }
type formSaveNarrowSession struct{ db.Session }

func (formSaveNarrowSession) ValidateSession(context.Context) error { return nil }

func preparedCollections(t *testing.T, selected ...string) formmodel.PreparedInstance[models.Article] {
	t.Helper()
	metadata := (models.ArticleDescriptor{}).Metadata()
	for _, name := range []string{"labels", "reviewers"} {
		field, err := ir.NormalizeManyToManyField("godj_conformance", metadata.Name, ir.ManyToManyField{Name: name, GoName: map[string]string{"labels": "Labels", "reviewers": "Reviewers"}[name], Blank: true, Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}})
		if err != nil {
			t.Fatal(err)
		}
		metadata.ManyToMany = append(metadata.ManyToMany, field)
	}
	manager := orm.NewManager[models.Article](collectionInstanceDescriptor{metadata: metadata})
	spec, err := formmodel.NewSpecForFields(metadata, append([]string{"title"}, selected...))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range selected {
		spec, err = spec.WithModelChoices(name, forms.Choice{Value: forms.Integer(7), Label: "Allowed"})
		if err != nil {
			t.Fatal(err)
		}
	}
	current := models.Article{Summary: new("original")}
	bound, err := formmodel.BindInstance(manager, spec, forms.NewData(map[string][]string{"title": {"candidate"}, "labels": {"7"}, "reviewers": {}}), &current, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestPreparedSaveOwnsCollectionOrderIdentityAndValues(t *testing.T) {
	prepared := preparedCollections(t, "reviewers", "labels")
	value, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	b := &formSaveBackend{}
	savers := []formmodel.CollectionSaver[models.Article]{
		{Field: "reviewers", Save: func(ctx context.Context, backend db.Session, owner models.Article, keys []int64) error {
			b.events = append(b.events, "reviewers")
			if backend != b || owner.ID != 17 || owner.Summary == nil || *owner.Summary != "original" || len(keys) != 0 {
				t.Fatal("callback lost owner, scope or clear intent")
			}
			return nil
		}},
		{Field: "labels", Save: func(ctx context.Context, backend db.Session, owner models.Article, keys []int64) error {
			b.events = append(b.events, "labels")
			if backend != b || owner.ID != 17 || !reflect.DeepEqual(keys, []int64{7}) {
				t.Fatal("callback lost owner, scope or selection")
			}
			keys[0] = 99
			*owner.Summary = "callback mutation"
			return nil
		}},
	}
	if err := prepared.Save(t.Context(), b, &value, savers...); err != nil {
		t.Fatal(err)
	}
	if value.ID != 17 || *value.Summary != "original" {
		t.Fatal("saved model identity or pointees changed")
	}
	if err := prepared.Save(t.Context(), b, &value, savers...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.events, []string{"insert", "labels", "reviewers", "update", "labels", "reviewers"}) {
		t.Fatal(b.events)
	}
	original, err := prepared.Model()
	if err != nil || original.ID != 0 || *original.Summary != "original" {
		t.Fatal("Save mutated prepared snapshot", err)
	}
	keys, _ := prepared.Collections().Integers("labels")
	if !reflect.DeepEqual(keys, []int64{7}) {
		t.Fatal("Save mutated pending selection")
	}
}

func TestPreparedSaveRejectsConfigurationBeforeScalarWrites(t *testing.T) {
	prepared := preparedCollections(t, "labels")
	called := 0
	good := formmodel.CollectionSaver[models.Article]{Field: "labels", Save: func(context.Context, db.Session, models.Article, []int64) error { called++; return nil }}
	for _, name := range []string{"missing", "duplicate", "unknown", "excluded", "scalar", "nil_saver", "nil_instance", "nil_backend", "typed_nil_backend", "nil_context", "canceled", "expired", "narrow_root", "narrow_session", "zero_prepared"} {
		t.Run(name, func(t *testing.T) {
			b := &formSaveBackend{}
			var backend db.Session = b
			value, err := prepared.Model()
			if err != nil {
				t.Fatal(err)
			}
			instance := &value
			candidate := prepared
			ctx := t.Context()
			savers := []formmodel.CollectionSaver[models.Article]{good}
			switch name {
			case "missing":
				savers = nil
			case "duplicate":
				savers = append(savers, good)
			case "unknown":
				savers[0].Field = "unknown"
			case "excluded":
				savers[0].Field = "reviewers"
			case "scalar":
				savers[0].Field = "title"
			case "nil_saver":
				savers[0].Save = nil
			case "nil_instance":
				instance = nil
			case "nil_backend":
				backend = nil
			case "typed_nil_backend":
				backend = (*formSaveBackend)(nil)
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "expired":
				backend = &formSaveSession{formSaveBackend: b, expired: true}
			case "narrow_root":
				backend = formSaveNarrow{Session: b}
			case "narrow_session":
				backend = formSaveNarrowSession{Session: b}
			case "zero_prepared":
				candidate = formmodel.PreparedInstance[models.Article]{}
			}
			if err := candidate.Save(ctx, backend, instance, savers...); err == nil {
				t.Fatal("invalid save accepted")
			}
			if len(b.events) != 0 || called != 0 || value.ID != 0 {
				t.Fatal("configuration failure wrote or invoked collection", b.events, called)
			}
		})
	}
}

func TestPreparedSaveDeferredKeyAndSessionLifetime(t *testing.T) {
	prepared := preparedCollections(t, "labels")
	value, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	b := &formSaveBackend{}
	calls := 0
	saver := formmodel.CollectionSaver[models.Article]{Field: "labels", Save: func(context.Context, db.Session, models.Article, []int64) error { calls++; return nil }}
	if err := prepared.SaveCollections(t.Context(), b, value, saver); err == nil || calls != 0 {
		t.Fatal("unsaved owner reached collection", err)
	}
	value = models.NewArticleWithID(0)
	if err := prepared.SaveCollections(t.Context(), b, value, saver); err != nil || calls != 1 || len(b.events) != 0 {
		t.Fatal("present zero key or deferred no-scalar contract lost", err)
	}
	session := &formSaveSession{formSaveBackend: b}
	saver.Save = func(_ context.Context, backend db.Session, _ models.Article, _ []int64) error {
		if backend != session {
			t.Fatal("session replaced")
		}
		session.expired = true
		return nil
	}
	if err := prepared.SaveCollections(t.Context(), session, value, saver); err == nil {
		t.Fatal("expired callback reported success")
	}
	excluded := preparedCollections(t)
	if err := excluded.SaveCollections(t.Context(), session, value); err == nil {
		t.Fatal("empty operation bypassed expired session")
	}
}

func TestPreparedSaveFailureAndCancellationPreserveBoundaries(t *testing.T) {
	prepared := preparedCollections(t, "labels", "reviewers")
	sentinel := errors.New("adapter failure")
	for _, mode := range []string{"scalar_failure", "first_failure", "second_failure", "canceled_after_first", "canceled_after_last"} {
		t.Run(mode, func(t *testing.T) {
			b := &formSaveBackend{}
			if mode == "scalar_failure" {
				b.failure = sentinel
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			callbacks := []formmodel.CollectionSaver[models.Article]{}
			for index, name := range []string{"labels", "reviewers"} {
				callbacks = append(callbacks, formmodel.CollectionSaver[models.Article]{Field: name, Save: func(context.Context, db.Session, models.Article, []int64) error {
					b.events = append(b.events, name)
					if index == 0 && mode == "first_failure" || index == 1 && mode == "second_failure" {
						return sentinel
					}
					if index == 0 && mode == "canceled_after_first" || index == 1 && mode == "canceled_after_last" {
						cancel()
					}
					return nil
				}})
			}
			value, err := prepared.Model()
			if err != nil {
				t.Fatal(err)
			}
			err = prepared.Save(ctx, b, &value, callbacks...)
			expected := sentinel
			count := 1
			if mode != "scalar_failure" {
				count = 2
				if value.ID != 17 {
					t.Fatal("later failure lost assigned key")
				}
			}
			if mode == "second_failure" || mode == "canceled_after_last" {
				count = 3
			}
			if mode == "canceled_after_first" || mode == "canceled_after_last" {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || len(b.events) != count {
				t.Fatal("failure changed or writes continued", err, b.events)
			}
		})
	}
}

func TestPreparedSaveConcurrentCopiesDoNotShareOwnersOrKeys(t *testing.T) {
	prepared := preparedCollections(t, "labels")
	for i := 0; i < 12; i++ {
		t.Run(fmt.Sprintf("copy_%d", i), func(t *testing.T) {
			t.Parallel()
			value, err := prepared.Model()
			if err != nil {
				t.Fatal(err)
			}
			b := &formSaveBackend{}
			saver := formmodel.CollectionSaver[models.Article]{Field: "labels", Save: func(_ context.Context, _ db.Session, owner models.Article, keys []int64) error {
				if *owner.Summary != "original" || !reflect.DeepEqual(keys, []int64{7}) {
					t.Fatal("shared prepared input")
				}
				*owner.Summary = "changed"
				keys[0] = 99
				return nil
			}}
			if err := prepared.Save(t.Context(), b, &value, saver); err != nil {
				t.Fatal(err)
			}
			if value.ID != 17 || *value.Summary != "original" {
				t.Fatal("shared callback model")
			}
		})
	}
}
