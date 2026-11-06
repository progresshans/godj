package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestSingleObjectCreation(t *testing.T) { withCollectionBackends(t, runSingleObjectCreation) }

type singleCreate[M any] func() orm.Mutation[M]

func (build singleCreate[M]) BuildCreate() orm.Mutation[M] { return build() }

type singleProbe struct {
	collectionBackend
	reads, transactions atomic.Int64
	afterCommit         func()
}

func (probe *singleProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	probe.reads.Add(1)
	return probe.collectionBackend.Query(ctx, plan)
}
func (probe *singleProbe) Atomic(ctx context.Context, callback func(db.Session) error) error {
	probe.transactions.Add(1)
	err := probe.collectionBackend.(db.Atomic).Atomic(ctx, callback)
	if err == nil && probe.afterCommit != nil {
		probe.afterCommit()
	}
	return err
}

func runSingleObjectCreation(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, backend.Close()) })
	migrateCollections(t, backend)
	ctx := t.Context()
	probe := &singleProbe{collectionBackend: backend}
	api, err := project.Using(probe)
	check(t, err)
	owner, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("single owner"))
	check(t, err)
	label, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate("linked label").WithNote("original"))
	check(t, err)
	collections, err := project.BindCollections()
	check(t, err)
	links, err := collections.OwnersOwnerLabels.From(backend, owner)
	check(t, err)
	check(t, links.Add(ctx, []labels.Label{label}))
	required, err := owners.RequiredOwnerObjects.Create(ctx, backend, owners.NewRequiredOwnerCreate(owner.ID, 1))
	check(t, err)

	t.Run("fresh_cache_cardinality_dynamic", func(t *testing.T) {
		base := api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(label.ID))
		warm, err := base.All(ctx)
		check(t, err)
		_, err = labels.LabelObjects.Patch(ctx, backend, label, labels.LabelPatch{}.WithNote("updated"))
		check(t, err)
		current, err := base.Get(ctx)
		check(t, err)
		if *current.Note != "updated" || *warm[0].Note != "original" || current.Note == warm[0].Note {
			t.Fatal("fresh Get did not own its current result")
		}
		*current.Note = "caller change"
		cached, err := base.All(ctx)
		check(t, err)
		if *cached[0].Note != "original" {
			t.Fatal("Get changed the source cache")
		}
		dynamic, err := orm.ParseDynamic(labels.LabelDescriptor{}, nil, []orm.LookupInput{{Key: "id", Value: label.ID}})
		check(t, err)
		typed := labels.LabelObjects.Using(probe).Filter(labels.LabelFields.ID.Exact(label.ID))
		parsed := labels.LabelObjects.Using(probe).Filter(dynamic...)
		if !typed.Plan().Equal(parsed.Plan()) {
			t.Fatal("single-object typed/dynamic plans differ")
		}
		value, err := parsed.Get(ctx)
		check(t, err)
		if value.ID != label.ID {
			t.Fatal(value)
		}
		if _, err := base.Filter(labels.LabelFields.Name.Exact("absent")).Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatal("missing Get", err)
		}
		for i := range 22 {
			_, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate(fmt.Sprintf("card-%02d", i)))
			check(t, err)
		}
		if _, err := api.LabelsLabel.Filter(labels.LabelFields.Name.IContains("card-")).Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeMultipleObjectsReturned}) || !strings.Contains(err.Error(), "more than 20") {
			t.Fatal("bounded cardinality", err)
		}
	})
	t.Run("slice_and_empty", func(t *testing.T) {
		ordered := api.LabelsLabel.Filter(labels.LabelFields.Name.IContains("card-")).OrderBy(labels.LabelFields.Name.Desc())
		limited, err := ordered.Limit(1)
		check(t, err)
		value, err := limited.Get(ctx)
		check(t, err)
		if value.Name != "card-21" {
			t.Fatal("slice ordering", value.Name)
		}
		offset, err := ordered.Offset(1)
		check(t, err)
		offset, err = offset.Limit(1)
		check(t, err)
		value, err = offset.Get(ctx)
		check(t, err)
		if value.Name != "card-20" {
			t.Fatal("offset slice", value.Name)
		}
		empty, err := ordered.Limit(0)
		check(t, err)
		if _, err := empty.Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatal("empty Get", err)
		}
		if _, err := api.LabelsLabel.Filter(labels.LabelFields.ID.In()).Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatal("empty membership", err)
		}
	})
	t.Run("existing_graphs", func(t *testing.T) {
		base := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID))
		prefetched := base.PrefetchRelated(base.Prefetch.Labels)
		warm, err := prefetched.All(ctx)
		check(t, err)
		value, created, err := prefetched.GetOrCreate(ctx, nil)
		check(t, err)
		if created || value.ID != owner.ID {
			t.Fatal("existing prefetch", value, created)
		}
		before := probe.reads.Load()
		view, err := value.Labels()
		check(t, err)
		rows, err := view.All(ctx)
		check(t, err)
		if len(rows) != 1 || rows[0].ID != label.ID || probe.reads.Load() != before {
			t.Fatal("existing graph lost prefetched rows")
		}
		rows[0].Name = "caller change"
		oldView, err := warm[0].Labels()
		check(t, err)
		old, err := oldView.All(ctx)
		check(t, err)
		if old[0].Name != "linked label" {
			t.Fatal("GetOrCreate shared a cached graph")
		}
		q := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID))
		eager := q.SelectRelated(q.Related.Owner)
		selected, err := eager.Get(ctx)
		check(t, err)
		before = probe.reads.Load()
		parent, err := selected.Owner(ctx)
		check(t, err)
		if parent.ID != owner.ID || probe.reads.Load() != before {
			t.Fatal("Get lost selected relation")
		}
		selected, created, err = eager.GetOrCreate(ctx, nil)
		check(t, err)
		before = probe.reads.Load()
		parent, err = selected.Owner(ctx)
		check(t, err)
		if created || parent.ID != owner.ID || probe.reads.Load() != before {
			t.Fatal("GetOrCreate lost selected relation")
		}
		combo := q.SelectRelated(q.Related.Owner).PrefetchRelated(q.Prefetch.Owner.WithChildren(api.OwnersOwner.Prefetch.Labels))
		combined, created, err := combo.GetOrCreate(ctx, nil)
		check(t, err)
		before = probe.reads.Load()
		parent, err = combined.Owner(ctx)
		check(t, err)
		view, err = parent.Labels()
		check(t, err)
		rows, err = view.All(ctx)
		check(t, err)
		if created || len(rows) != 1 || probe.reads.Load() != before {
			t.Fatal("combined eager/prefetch graph lost")
		}
	})
	t.Run("created_graphs", func(t *testing.T) {
		base := api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact("new prefetched"))
		value, created, err := base.PrefetchRelated(base.Prefetch.Labels).GetOrCreate(ctx, owners.NewOwnerCreate("new prefetched"))
		check(t, err)
		if !created {
			t.Fatal("prefetched create not reported")
		}
		view, err := value.Labels()
		check(t, err)
		check(t, view.AddKeys(ctx, []int64{label.ID}))
		rows, err := view.All(ctx)
		check(t, err)
		if len(rows) != 1 {
			t.Fatal("new graph retained an empty prefetch cache")
		}
		baseRequired := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.Amount.Exact(2))
		selected, created, err := baseRequired.SelectRelated(baseRequired.Related.Owner).GetOrCreate(ctx, owners.NewRequiredOwnerCreate(value.ID, 2))
		check(t, err)
		parent, err := selected.Owner(ctx)
		check(t, err)
		if !created || parent.ID != value.ID {
			t.Fatal("created eager handle used an expired transaction")
		}
		selected.Amount = 3
		check(t, selected.Save(ctx))
		loaded, err := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(selected.ID)).Get(ctx)
		check(t, err)
		if loaded.Amount != 3 {
			t.Fatal("created facade lost root write binding")
		}
	})
	t.Run("explicit_input_and_lazy_validation", func(t *testing.T) {
		base := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("query value"))
		value, created, err := base.GetOrCreate(ctx, labels.NewLabelCreate("explicit value"))
		check(t, err)
		if !created || value.Name != "explicit value" {
			t.Fatal("query predicates changed explicit input")
		}
		if _, err := base.Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatal(err)
		}
		builds := 0
		value, created, err = api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(label.ID)).GetOrCreate(ctx, singleCreate[labels.Label](func() orm.Mutation[labels.Label] { builds++; return labels.LabelCreate{}.BuildCreate() }))
		check(t, err)
		if created || value.ID != label.ID || builds != 0 {
			t.Fatal("existing result evaluated invalid input")
		}
		_, created, err = api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("invalid create")).GetOrCreate(ctx, labels.LabelCreate{})
		if created || !errors.Is(err, &query.Error{Code: query.CodeRequiredField}) {
			t.Fatal("missing input validation", created, err)
		}
	})
	t.Run("relation_query_creation", func(t *testing.T) {
		related, err := links.Query()
		check(t, err)
		value, created, err := related.Filter(labels.LabelFields.ID.Exact(label.ID)).GetOrCreate(ctx, nil)
		check(t, err)
		if created || value.ID != label.ID {
			t.Fatal("bound query existing", value, created)
		}
		fresh, created, err := related.Filter(labels.LabelFields.Name.Exact("relation explicit")).GetOrCreate(ctx, labels.NewLabelCreate("relation explicit"))
		check(t, err)
		if !created || fresh.Name != "relation explicit" {
			t.Fatal("bound model lost write snapshot")
		}
		// GetOrCreate on a QuerySet does not imply a collection Add. Membership
		// remains the explicit collection mutation's responsibility.
		if _, err := related.Filter(labels.LabelFields.ID.Exact(fresh.ID)).Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatal("query creation forged collection membership", err)
		}
	})
	t.Run("borrowed_scopes", func(t *testing.T) {
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			t.Run(mode, func(t *testing.T) {
				for _, finish := range []string{"commit", "rollback"} {
					t.Run(finish, func(t *testing.T) {
						name := mode + "-" + finish
						rollback := errors.New("parent rollback")
						var held *project.OwnersRequiredOwner
						var childQuery project.OwnersRequiredOwnerQuery
						var labelID int64
						err := runSingleScope(ctx, backend, mode, func(session db.Session) error {
							scoped, err := project.UsingSession(session)
							if err != nil {
								return err
							}
							_, err = labels.LabelObjects.Create(ctx, session, labels.NewLabelCreate("before-"+name))
							if err != nil {
								return err
							}
							_, created, err := scoped.LabelsLabel.Filter(labels.LabelFields.Name.Exact("unrelated-"+name)).GetOrCreate(ctx, labels.NewLabelCreate(label.Name))
							if created || !errors.Is(err, &query.Error{Category: query.CategoryIntegrity}) {
								return fmt.Errorf("unrelated unique failure changed: %v", err)
							}
							value, created, err := scoped.LabelsLabel.Filter(labels.LabelFields.Name.Exact(name)).GetOrCreate(ctx, labels.NewLabelCreate(name))
							if err != nil {
								return err
							}
							if !created {
								return errors.New("missing borrowed create")
							}
							labelID = value.ID
							value.Note = new("updated in parent")
							if err := value.Save(ctx); err != nil {
								return err
							}
							q := scoped.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.Amount.Exact(100 + labelID))
							held, created, err = q.SelectRelated(q.Related.Owner).GetOrCreate(ctx, owners.NewRequiredOwnerCreate(owner.ID, 100+labelID))
							if err != nil {
								return err
							}
							if !created {
								return errors.New("missing borrowed eager create")
							}
							parent, err := held.Owner(ctx)
							if err != nil {
								return err
							}
							if parent.ID != owner.ID {
								return errors.New("created handle did not bind to parent")
							}
							childQuery = q
							_, err = labels.LabelObjects.Create(ctx, session, labels.NewLabelCreate("after-"+name))
							if err != nil {
								return err
							}
							if finish == "rollback" {
								return rollback
							}
							return nil
						})
						if finish == "rollback" {
							if !errors.Is(err, rollback) {
								t.Fatal(err)
							}
						} else {
							check(t, err)
						}
						_, err = api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(labelID)).Get(ctx)
						if finish == "rollback" {
							if !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
								t.Fatal("child RELEASE escaped parent rollback", err)
							}
						} else {
							check(t, err)
						}
						for _, edge := range []string{"before-", "after-"} {
							_, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact(edge + name)).Get(ctx)
							if finish == "commit" {
								check(t, err)
							} else if !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
								t.Fatal("parent writes escaped rollback", err)
							}
						}
						if _, err := childQuery.Get(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
							t.Fatal("expired parent query", err)
						}
						if _, err := held.Owner(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
							t.Fatal("cached relation escaped parent lifetime", err)
						}
					})
				}
			})
		}
	})
	t.Run("post_commit_cancellation", func(t *testing.T) {
		for _, kind := range []string{"plain", "prefetch", "eager"} {
			callCtx, cancel := context.WithCancel(ctx)
			probe := &singleProbe{collectionBackend: backend, afterCommit: cancel}
			api, err := project.Using(probe)
			check(t, err)
			var created bool
			if kind == "eager" {
				q := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.Amount.Exact(99999))
				value, wasCreated, e := q.SelectRelated(q.Related.Owner).GetOrCreate(callCtx, owners.NewRequiredOwnerCreate(owner.ID, 99999))
				created, err = wasCreated, e
				if value == nil {
					t.Error("committed eager result missing")
				}
			} else {
				q := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("late-" + kind))
				var value *project.LabelsLabel
				if kind == "prefetch" {
					value, created, err = q.PrefetchRelated(q.Prefetch.Owners).GetOrCreate(callCtx, labels.NewLabelCreate("late-"+kind))
				} else {
					value, created, err = q.GetOrCreate(callCtx, labels.NewLabelCreate("late-"+kind))
				}
				if value == nil {
					t.Error("committed result missing")
				}
			}
			cancel()
			check(t, err)
			if !created || callCtx.Err() == nil {
				t.Fatal("confirmed commit lost after cancellation")
			}
		}
	})
	t.Run("root_batch_affinity", func(t *testing.T) {
		called := 0
		native, err := project.Using(backend)
		check(t, err)
		err = native.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID)).Iterate(ctx, 1, func(batchContext context.Context, value *project.OwnersOwner) (bool, error) {
			called++
			label, created, err := labels.LabelObjects.Using(backend).Filter(labels.LabelFields.Name.Exact("inside root batch")).GetOrCreate(batchContext, labels.NewLabelCreate("inside root batch"))
			if err != nil {
				return false, err
			}
			if !created || label.ID == 0 || value.ID != owner.ID {
				return false, errors.New("root batch creation lost affinity")
			}
			return false, nil
		})
		check(t, err)
		if called != 1 {
			t.Fatal("root batch callback", called)
		}
	})
}

func runSingleScope(ctx context.Context, backend collectionBackend, mode string, callback func(db.Session) error) error {
	switch mode {
	case "atomic":
		return backend.(db.Atomic).Atomic(ctx, callback)
	case "coordinated":
		return backend.(db.CoordinatedAtomic).CoordinatedAtomic(ctx, callback)
	case "relation":
		return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
	case "coordinated_relation":
		return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
	default:
		return errors.New("unknown scope mode")
	}
}
