package consumer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestCollectionFacadeSessions(t *testing.T) { withCollectionBackends(t, runFacadeSessions) }

type countingCollectionBackend struct {
	collectionBackend
	queries atomic.Int64
}

func (b *countingCollectionBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.queries.Add(1)
	return b.collectionBackend.Query(ctx, plan)
}

type tracedSession struct {
	db.RelationSession
	nested  *atomic.Int64
	queries *atomic.Int64
}

func (s tracedSession) ValidateSession(ctx context.Context) error {
	return s.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (s tracedSession) InsertOnConflict(ctx context.Context, p query.ConflictInsertPlan) (bool, error) {
	return s.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, p)
}
func (s tracedSession) Query(ctx context.Context, p query.Plan) (db.Rows, error) {
	s.queries.Add(1)
	return s.RelationSession.Query(ctx, p)
}
func (s tracedSession) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	s.nested.Add(1)
	return errors.New("nested transaction")
}

func runFacadeSessions(t *testing.T, b collectionBackend, open func() (collectionBackend, error), exec func(string) error, pg bool) {
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := t.Context()
	migrateCollections(t, b)
	owner, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("owner"))
	check(t, err)
	a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a"))
	check(t, err)
	bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
	check(t, err)
	cc, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("c"))
	check(t, err)
	factory, err := project.BindCollections()
	check(t, err)
	root, err := factory.OwnersOwnerLabels.From(b, owner)
	check(t, err)
	count := &countingCollectionBackend{collectionBackend: b}
	api, err := project.Using(count)
	check(t, err)
	model, err := api.OwnersOwner.New(owner)
	check(t, err)
	ta, err := api.LabelsLabel.New(a)
	check(t, err)
	tb, err := api.LabelsLabel.New(bb)
	check(t, err)
	tc, err := api.LabelsLabel.New(cc)
	check(t, err)
	t.Run("facade_cache_and_origin", func(t *testing.T) {
		relation, err := model.Labels()
		check(t, err)
		check(t, relation.Add(ctx, []*project.LabelsLabel{ta, tb}))
		before := count.queries.Load()
		values, err := relation.All(ctx)
		check(t, err)
		if len(values) != 2 {
			t.Fatal(values)
		}
		again, err := model.Labels()
		check(t, err)
		_, err = again.All(ctx)
		check(t, err)
		if count.queries.Load() != before+1 {
			t.Fatal("same owner did not share collection cache")
		}
		values[0].Name = "caller edit"
		values, err = again.All(ctx)
		check(t, err)
		if values[0].Name == "caller edit" {
			t.Fatal("facade target aliases canonical cache")
		}
		held, err := relation.Query()
		check(t, err)
		_, err = held.All(ctx)
		check(t, err)
		check(t, again.Add(ctx, []*project.LabelsLabel{tc}))
		values, err = relation.All(ctx)
		check(t, err)
		if len(values) != 3 {
			t.Fatal("shared view cache remained warm")
		}
		prior, err := held.All(ctx)
		check(t, err)
		if len(prior) != 2 {
			t.Fatal("held query changed")
		}
		copied := *relation
		if err := copied.Clear(ctx); err == nil {
			t.Fatal("copied collection view accepted")
		}
		otherAPI, err := project.Using(b)
		check(t, err)
		otherTarget, err := otherAPI.LabelsLabel.New(a)
		check(t, err)
		check(t, root.RemoveKeys(ctx, cc.ID))
		if err := relation.Add(ctx, []*project.LabelsLabel{otherTarget}); err == nil {
			t.Fatal("mixed facade origin accepted")
		}
		values, err = relation.All(ctx)
		check(t, err)
		if len(values) != 2 {
			t.Fatal("failed facade mutation retained stale cache")
		}
		unsaved, err := api.LabelsLabel.New(labels.Label{Name: "unsaved"})
		check(t, err)
		if err := relation.Add(ctx, []*project.LabelsLabel{unsaved}); !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) {
			t.Fatal(err)
		}
		model.ID++
		if err := relation.Clear(ctx); err == nil {
			t.Fatal("changed owner identity accepted through held view")
		}
		model.ID--
		reverse, err := ta.Owners()
		check(t, err)
		parents, err := reverse.All(ctx)
		check(t, err)
		if len(parents) != 1 || parents[0].ID != owner.ID {
			t.Fatal("facade reverse", parents)
		}
		check(t, reverse.Remove(ctx, model))
		check(t, relation.Set(ctx, []*project.LabelsLabel{ta, tb}))
		check(t, relation.Invalidate())
		fresh, err := relation.Fresh()
		check(t, err)
		check(t, fresh.Clear(ctx))
		newOwner, err := api.OwnersOwner.New(owners.Owner{Name: "new"})
		check(t, err)
		if _, err := newOwner.Labels(); !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) {
			t.Fatal(err)
		}
		check(t, newOwner.Save(ctx))
		created, err := newOwner.Labels()
		check(t, err)
		check(t, created.Add(ctx, []*project.LabelsLabel{ta}))
		check(t, created.Clear(ctx))
		// Concurrent accessors share a cell; neither binder nor cache publication races.
		check(t, relation.Invalidate())
		before = count.queries.Load()
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				view, err := model.Labels()
				if err != nil {
					t.Error(err)
					return
				}
				_, err = view.All(ctx)
				if err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if count.queries.Load() != before+1 {
			t.Fatal("concurrent collection cache was duplicated")
		}
	})
	t.Run("borrowed_composition", func(t *testing.T) {
		for _, coordinated := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("coordinated_%v_commit_%v", coordinated, commit), func(t *testing.T) {
					check(t, root.Set(ctx, []labels.Label{a}))
					// An independently cached root snapshot must not see provisional work.
					independent, err := factory.OwnersOwnerLabels.From(b, owner)
					check(t, err)
					_, err = independent.All(ctx)
					check(t, err)
					var borrowed *orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]
					var held orm.QuerySet[labels.Label]
					var txOwner *project.OwnersOwner
					var txView *project.OwnersOwnerLabelsCollection
					var txQuery project.LabelsLabelQuery
					var nested, reads atomic.Int64
					aborted := errors.New("outer rollback after successful collection write")
					run := b.AtomicRelation
					if coordinated {
						run = b.CoordinatedAtomicRelation
					}
					err = run(ctx, func(session db.RelationSession) error {
						tracked := tracedSession{RelationSession: session, nested: &nested, queries: &reads}
						if _, err := factory.OwnersOwnerLabels.From(tracked, owner); err == nil {
							return errors.New("borrowed session accepted as root")
						}
						if _, err := project.Using(tracked); err == nil {
							return errors.New("borrowed facade accepted as root")
						}
						var err error
						borrowed, err = factory.OwnersOwnerLabels.InSession(tracked, owner)
						if err != nil {
							return err
						}
						if err := borrowed.Set(ctx, []labels.Label{bb}); err != nil {
							return err
						}
						held, err = borrowed.Query()
						if err != nil {
							return err
						}
						rows, err := held.All(ctx)
						if err != nil || len(rows) != 1 || rows[0].ID != bb.ID {
							return fmt.Errorf("provisional low-level read: %v", err)
						}
						tx, err := project.UsingSession(tracked)
						if err != nil {
							return err
						}
						txOwner, err = tx.OwnersOwner.New(owner)
						if err != nil {
							return err
						}
						txTarget, err := tx.LabelsLabel.New(cc)
						if err != nil {
							return err
						}
						txView, err = txOwner.Labels()
						if err != nil {
							return err
						}
						if err := txView.Add(ctx, []*project.LabelsLabel{txTarget}); err != nil {
							return err
						}
						txQuery, err = txView.Query()
						if err != nil {
							return err
						}
						current, err := txQuery.All(ctx)
						if err != nil || len(current) != 2 {
							return fmt.Errorf("provisional facade read: %v", err)
						}
						txOwner.Name = "transaction name"
						if err := txOwner.Save(ctx); err != nil {
							return err
						}
						if commit {
							return nil
						}
						return aborted
					})
					if commit {
						check(t, err)
					} else if !errors.Is(err, aborted) {
						t.Fatal(err)
					}
					if nested.Load() != 0 {
						t.Fatal("nested transaction used")
					}
					queriesBefore := reads.Load()
					if _, err := held.All(ctx); err == nil {
						t.Fatal("closed warm collection query returned provisional cache")
					}
					if _, err := borrowed.All(ctx); err == nil {
						t.Fatal("closed collection read accepted")
					}
					if err := borrowed.AddKeys(ctx, nil); err == nil {
						t.Fatal("closed empty mutation accepted")
					}
					if _, err := borrowed.Fresh(); err == nil {
						t.Fatal("closed Fresh accepted")
					}
					if _, err := txOwner.Unwrap(); err == nil {
						t.Fatal("closed provisional model accepted")
					}
					if _, err := txQuery.All(ctx); err == nil {
						t.Fatal("closed facade query cache accepted")
					}
					if err := txView.Clear(ctx); err == nil {
						t.Fatal("closed facade write accepted")
					}
					if reads.Load() != queriesBefore {
						t.Fatal("closed session reached backend")
					}
					old, err := independent.All(ctx)
					check(t, err)
					if len(old) != 1 || old[0].ID != a.ID {
						t.Fatal("independent root cache published provisional changes")
					}
					fresh, err := root.Fresh()
					check(t, err)
					actual, err := fresh.All(ctx)
					check(t, err)
					if commit && len(actual) != 2 || !commit && (len(actual) != 1 || actual[0].ID != a.ID) {
						t.Fatal("outer outcome and collection storage differ", actual)
					}
				})
			}
		}
	})
	t.Run("query_and_model_lifetime", func(t *testing.T) {
		runners := map[string]func(context.Context, func(db.Session) error) error{
			"atomic": b.(db.Atomic).Atomic, "coordinated": b.(db.CoordinatedAtomic).CoordinatedAtomic,
			"relation": func(ctx context.Context, fn func(db.Session) error) error {
				return b.AtomicRelation(ctx, func(s db.RelationSession) error { return fn(s) })
			},
			"coordinated_relation": func(ctx context.Context, fn func(db.Session) error) error {
				return b.CoordinatedAtomicRelation(ctx, func(s db.RelationSession) error { return fn(s) })
			},
		}
		for name, run := range runners {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_commit_%v", name, commit), func(t *testing.T) {
					var held, empty orm.QuerySet[labels.Label]
					var eager project.OwnersOwnerLabelsLinkSelectRelatedQuery

					var scope db.SessionValidator
					var draft *project.OwnersOwner
					aborted := errors.New("rollback scalar and cached reads")
					err := run(ctx, func(session db.Session) error {
						var ok bool
						scope, ok = session.(db.SessionValidator)
						if !ok {
							return errors.New("missing native session validator")
						}
						if err := scope.ValidateSession(ctx); err != nil {
							return err
						}
						held = labels.LabelObjects.Using(session).OrderBy(labels.LabelFields.ID.Asc())
						objects, err := project.BindObjects()
						if err != nil {
							return err
						}
						eager = objects.OwnersOwnerLabelsLink.SelectRelated(owners.OwnerLabelsLinkObjects.Using(session).OrderBy(owners.OwnerLabelsLinkFields.ID.Asc())).WithSource()
						if _, err := eager.All(ctx); err != nil {
							return err
						}

						if _, err := held.All(ctx); err != nil {
							return err
						}
						empty = held.Filter(labels.LabelFields.ID.In())
						if rows, err := empty.All(ctx); err != nil || len(rows) != 0 {
							return fmt.Errorf("empty session query: %v", err)
						}
						tx, err := project.UsingSession(session)
						if err != nil {
							return err
						}
						draft, err = tx.OwnersOwner.New(owners.Owner{Name: "draft-" + name})
						if err != nil {
							return err
						}
						if err := draft.Save(ctx); err != nil {
							return err
						}
						if commit {
							return nil
						}
						return aborted
					})
					if commit {
						check(t, err)
					} else if !errors.Is(err, aborted) {
						t.Fatal(err)
					}
					if err := scope.ValidateSession(ctx); err == nil {
						t.Fatal("native session still active")
					}
					if _, err := held.All(ctx); err == nil {
						t.Fatal("warm All escaped scope")
					}
					if count, err := held.Count(ctx); err == nil || count != 0 {
						t.Fatal("warm Count escaped scope", count, err)
					}
					if found, err := held.Exists(ctx); err == nil || found {
						t.Fatal("warm Exists escaped scope", found, err)
					}
					if _, found, err := held.First(ctx); err == nil || found {
						t.Fatal("warm First escaped scope", found, err)
					}
					if _, found, err := held.At(ctx, 99); err == nil || found {
						t.Fatal("warm missing index escaped scope", found, err)
					}
					if rows, err := empty.All(ctx); err == nil || rows != nil {
						t.Fatal("empty cache escaped scope", rows, err)
					}
					called := false
					if err := held.Iterate(ctx, func(labels.Label) error { called = true; return nil }); err == nil || called {
						t.Fatal("iterator escaped scope", err)
					}
					if _, err := eager.All(ctx); err == nil {
						t.Fatal("warm eager cache escaped scope")
					}
					if count, err := eager.Count(ctx); err == nil || count != 0 {
						t.Fatal("warm eager count escaped scope", count, err)
					}
					if _, err := draft.Unwrap(); err == nil {
						t.Fatal("expired scalar facade accepted")
					}
				})
			}
		}
	})
}
