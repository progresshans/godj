package consumer

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type singlePatch[M any] func(M) orm.Mutation[M]

func (build singlePatch[M]) BuildPatch(value M) orm.Mutation[M] { return build(value) }

type upsertProbe struct {
	collectionBackend
	reads, inserts, updates, transactions, savepoints, uniqueFailures atomic.Int64
	activeScopes, lockedReads                                         atomic.Int64
	beforeQuery                                                       func(context.Context, query.Plan) error
	beforeInsert, afterInsert, afterUpdate                            func(context.Context) error
	afterCommit                                                       func()
}

func (probe *upsertProbe) observe(ctx context.Context, plan query.Plan) error {
	probe.reads.Add(1)
	if _, locked := plan.RowLock(); locked {
		probe.lockedReads.Add(1)
	}
	if probe.beforeQuery != nil {
		return probe.beforeQuery(ctx, plan)
	}
	return nil
}
func (probe *upsertProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := probe.observe(ctx, plan); err != nil {
		return nil, err
	}
	return probe.collectionBackend.Query(ctx, plan)
}
func (probe *upsertProbe) Atomic(ctx context.Context, callback func(db.Session) error) error {
	probe.transactions.Add(1)
	err := probe.collectionBackend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
		probe.activeScopes.Add(1)
		defer probe.activeScopes.Add(-1)
		return callback(upsertObservedSession{Session: session, probe: probe})
	})
	if err == nil && probe.afterCommit != nil {
		probe.afterCommit()
	}
	return err
}

type upsertObservedSession struct {
	db.Session
	probe *upsertProbe
}

func (session upsertObservedSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session upsertObservedSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	return session.Session.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx)
}
func (session upsertObservedSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	session.probe.savepoints.Add(1)
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error {
		return callback(upsertObservedSession{Session: child, probe: session.probe})
	})
}
func (session upsertObservedSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := session.probe.observe(ctx, plan); err != nil {
		return nil, err
	}
	return session.Session.Query(ctx, plan)
}
func (session upsertObservedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.probe.inserts.Add(1)
	if hook := session.probe.beforeInsert; hook != nil {
		if err := hook(ctx); err != nil {
			return 0, err
		}
	}
	key, err := session.Session.Insert(ctx, plan)
	if errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
		session.probe.uniqueFailures.Add(1)
	}
	if err == nil && session.probe.afterInsert != nil {
		err = session.probe.afterInsert(ctx)
	}
	return key, err
}
func (session upsertObservedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.probe.updates.Add(1)
	count, err := session.Session.Update(ctx, plan)
	if err == nil && session.probe.afterUpdate != nil {
		err = session.probe.afterUpdate(ctx)
	}
	return count, err
}

func TestUpdateOrCreate(t *testing.T) { withCollectionBackends(t, runUpdateOrCreate) }

func runUpdateOrCreate(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), _ func(string) error, postgres bool) {
	t.Cleanup(func() { check(t, backend.Close()) })
	migrateCollections(t, backend)
	ctx := t.Context()
	probe := &upsertProbe{collectionBackend: backend}
	api, err := project.Using(probe)
	check(t, err)
	first, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("first upsert owner"))
	check(t, err)
	second, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("second upsert owner"))
	check(t, err)
	var badges []owners.Badge
	var values []labels.Label
	collections, err := project.BindCollections()
	check(t, err)
	for i, owner := range []owners.Owner{first, second} {
		badge, err := owners.BadgeObjects.Create(ctx, backend, owners.BadgeCreate{}.WithOwnerID(owner.ID).WithName(fmt.Sprint("badge-", i)))
		check(t, err)
		badges = append(badges, badge)
		label, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate(fmt.Sprint("upsert-label-", i)).WithNote("original"))
		check(t, err)
		values = append(values, label)
		links, err := collections.OwnersOwnerLabels.From(backend, owner)
		check(t, err)
		check(t, links.Add(ctx, []labels.Label{label}))
		_, err = owners.RankedLinkObjects.Create(ctx, backend, owners.NewRankedLinkCreate(int64(i)).WithOwnerID(owner.ID).WithLabelID(label.ID))
		check(t, err)
	}
	required, err := owners.RequiredOwnerObjects.Create(ctx, backend, owners.NewRequiredOwnerCreate(first.ID, 100))
	check(t, err)

	t.Run("branches_cache_and_dynamic", func(t *testing.T) {
		base := api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(values[0].ID))
		warm, err := base.All(ctx)
		check(t, err)
		creates, patches := 0, 0
		current, created, err := base.UpdateOrCreate(ctx, singleCreate[labels.Label](func() orm.Mutation[labels.Label] {
			creates++
			return labels.LabelCreate{}.BuildCreate()
		}), singlePatch[labels.Label](func(value labels.Label) orm.Mutation[labels.Label] {
			patches++
			return (labels.LabelPatch{}.WithNote(*value.Note + " patched")).BuildPatch(value)
		}))
		check(t, err)
		if created || creates != 0 || patches != 1 || *current.Note != "original patched" || *warm[0].Note != "original" {
			t.Fatal("existing branch lost lazy input or cache ownership")
		}
		updates := probe.updates.Load()
		current, created, err = base.UpdateOrCreate(ctx, nil, labels.LabelPatch{})
		check(t, err)
		if created || current.ID != values[0].ID || probe.updates.Load() != updates {
			t.Fatal("empty patch issued an UPDATE")
		}
		*current.Note = "private returned value"
		cached, err := base.All(ctx)
		check(t, err)
		if *cached[0].Note != "original" {
			t.Fatal("returned upsert changed the source cache")
		}
		dynamic, err := orm.ParseDynamic(labels.LabelDescriptor{}, nil, []orm.LookupInput{{Key: "name", Value: "dynamic lookup"}})
		check(t, err)
		parsed := labels.LabelObjects.Using(probe).Filter(dynamic...)
		fresh, created, err := parsed.UpdateOrCreate(ctx, labels.NewLabelCreate("explicit outside lookup"), singlePatch[labels.Label](nil))
		check(t, err)
		if !created || fresh.Name != "explicit outside lookup" {
			t.Fatal("creation copied filters or evaluated an unused typed-nil patch")
		}
		if _, _, err := parsed.UpdateOrCreate(ctx, labels.NewLabelCreate(values[0].Name), nil); !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
			t.Fatal("unrelated unique conflict became a successful upsert", err)
		}
	})

	graphQuery := func(api project.Models, id int64) project.OwnersRequiredOwnerPrefetchQuery {
		base := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(id))
		return base.SelectRelated(base.Related.Owner.WithChildren(api.OwnersOwner.Related.Badge)).PrefetchRelated(
			base.Prefetch.Owner.WithChildren(api.OwnersOwner.Prefetch.Labels.WithChildren(api.LabelsLabel.Prefetch.Owners), api.OwnersOwner.Prefetch.RankedLinkRows.SelectRelated(api.OwnersRankedLink.Related.Label)),
		)
	}
	assertGraph := func(t *testing.T, value *project.OwnersRequiredOwner, owner owners.Owner, badge owners.Badge, label labels.Label) {
		t.Helper()
		before := probe.reads.Load()
		parent, err := value.Owner(ctx)
		check(t, err)
		if parent.ID != owner.ID {
			t.Fatal("graph retained the old FK")
		}
		child, found, err := parent.Badge(ctx)
		check(t, err)
		if !found || child.ID != badge.ID {
			t.Fatal("selected badge was lost")
		}
		links, err := parent.Labels()
		check(t, err)
		linked, err := links.All(ctx)
		check(t, err)
		if len(linked) != 1 || linked[0].ID != label.ID {
			t.Fatal("prefetched collection was lost")
		}
		reverse, err := linked[0].Owners()
		check(t, err)
		parents, err := reverse.All(ctx)
		check(t, err)
		if len(parents) != 1 || parents[0].ID != owner.ID {
			t.Fatal("nested collection retained an expired scope")
		}
		rows, err := parent.RankedLinkRows()
		check(t, err)
		ranked, err := rows.All(ctx)
		check(t, err)
		if len(ranked) != 1 {
			t.Fatal("reverse collection was lost")
		}
		selectedLabel, found, err := ranked[0].Label(ctx)
		check(t, err)
		if !found || selectedLabel.ID != label.ID || probe.reads.Load() != before {
			t.Fatal("ready graph performed lazy I/O or lost its selected target")
		}
	}
	t.Run("selected_graph_after_fk_change", func(t *testing.T) {
		base := graphQuery(api, required.ID)
		warm, err := base.Get(ctx)
		check(t, err)
		value, created, err := base.UpdateOrCreate(ctx, nil, owners.RequiredOwnerPatch{}.WithOwnerID(second.ID).WithAmount(101))
		check(t, err)
		if created || value.Amount != 101 {
			t.Fatal("existing graph was not updated")
		}
		assertGraph(t, value, second, badges[1], values[1])
		assertGraph(t, warm, first, badges[0], values[0])
	})
	t.Run("created_selected_graph", func(t *testing.T) {
		value, created, err := graphQuery(api, -987).UpdateOrCreate(ctx, owners.NewRequiredOwnerCreate(first.ID, 202), nil)
		check(t, err)
		if !created || value.Amount != 202 {
			t.Fatal("creation did not materialize explicit input")
		}
		assertGraph(t, value, first, badges[0], values[0])
	})
	t.Run("graph_failure_rolls_back", func(t *testing.T) {
		failure := errors.New("returned descendant read failed")
		t.Cleanup(func() { probe.beforeQuery = nil })
		probe.beforeQuery = func(_ context.Context, plan query.Plan) error {
			if plan.Table() == (owners.RankedLinkDescriptor{}).Metadata().DBTable {
				return failure
			}
			return nil
		}
		for _, create := range []bool{false, true} {
			id := required.ID
			if create {
				id = -988
			}
			value, created, err := graphQuery(api, id).UpdateOrCreate(ctx, owners.NewRequiredOwnerCreate(first.ID, 303), owners.RequiredOwnerPatch{}.WithAmount(303))
			if !errors.Is(err, failure) || value != nil || created {
				t.Fatal("graph failure followed a successful write", err)
			}
		}
		probe.beforeQuery = nil
		value, err := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID)).Get(ctx)
		check(t, err)
		if value.Amount != 101 {
			t.Fatal("failed returned graph committed an update")
		}
		count, err := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.Amount.Exact(303)).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("failed returned graph committed creation")
		}
	})
	t.Run("borrowed_scopes", func(t *testing.T) {
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			t.Run(mode, func(t *testing.T) {
				for _, finish := range []string{"commit", "rollback"} {
					t.Run(finish, func(t *testing.T) {
						row, err := owners.RequiredOwnerObjects.Create(ctx, backend, owners.NewRequiredOwnerCreate(first.ID, 401))
						check(t, err)
						rollback := errors.New("rollback caller")
						var held *project.OwnersRequiredOwner
						var createdID int64
						err = runSingleScope(ctx, backend, mode, func(parent db.Session) error {
							policy, err := parent.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx)
							if err != nil || policy == "" {
								return fmt.Errorf("missing native policy: %w", err)
							}
							bound, err := project.UsingSession(upsertObservedSession{Session: parent, probe: probe})
							if err != nil {
								return err
							}
							var created bool
							held, created, err = graphQuery(bound, row.ID).UpdateOrCreate(ctx, nil, owners.RequiredOwnerPatch{}.WithOwnerID(second.ID).WithAmount(402))
							if err != nil || created {
								return fmt.Errorf("borrowed update: %w", err)
							}
							assertGraph(t, held, second, badges[1], values[1])
							newValue, created, err := graphQuery(bound, -989).UpdateOrCreate(ctx, owners.NewRequiredOwnerCreate(first.ID, 403), nil)
							if err != nil || !created {
								return fmt.Errorf("borrowed create: %w", err)
							}
							createdID = newValue.ID
							assertGraph(t, newValue, first, badges[0], values[0])
							_, _, err = bound.LabelsLabel.Filter(labels.LabelFields.Name.Exact("unrelated borrowed")).UpdateOrCreate(ctx, labels.NewLabelCreate(values[0].Name), nil)
							if !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
								return fmt.Errorf("native unique failure disappeared: %w", err)
							}
							if _, err := labels.LabelObjects.Create(ctx, parent, labels.NewLabelCreate("after-upsert-"+mode+"-"+finish)); err != nil {
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
						current, err := owners.RequiredOwnerObjects.Using(backend).Filter(owners.RequiredOwnerFields.ID.Exact(row.ID)).Get(ctx)
						check(t, err)
						want := int64(402)
						if finish == "rollback" {
							want = 401
							if _, err := owners.RequiredOwnerObjects.Using(backend).Filter(owners.RequiredOwnerFields.ID.Exact(createdID)).Get(ctx); !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
								t.Fatal("inner RELEASE escaped caller rollback", err)
							}
						}
						if current.Amount != want {
							t.Fatal("borrowed update committed independently")
						}
						if _, err := held.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan, Category: query.CategoryBackend}) {
							t.Fatal("returned graph escaped its caller lifetime", err)
						}
					})
				}
			})
		}
	})
	t.Run("post_commit_cancellation", func(t *testing.T) {
		for _, kind := range []string{"plain", "eager", "prefetch"} {
			for _, creating := range []bool{false, true} {
				callCtx, cancel := context.WithCancel(ctx)
				probe := &upsertProbe{collectionBackend: backend, afterCommit: cancel}
				bound, err := project.Using(probe)
				check(t, err)
				id := required.ID
				if creating {
					id = -990
				}
				base := bound.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(id))
				var value *project.OwnersRequiredOwner
				var created bool
				create, patch := owners.NewRequiredOwnerCreate(first.ID, 501), owners.RequiredOwnerPatch{}.WithAmount(501)
				switch kind {
				case "plain":
					value, created, err = base.UpdateOrCreate(callCtx, create, patch)
				case "eager":
					value, created, err = base.SelectRelated(base.Related.Owner).UpdateOrCreate(callCtx, create, patch)
				case "prefetch":
					value, created, err = base.PrefetchRelated(base.Prefetch.Owner).UpdateOrCreate(callCtx, create, patch)
				}
				cancel()
				check(t, err)
				if value == nil || value.Amount != 501 || created != creating || callCtx.Err() == nil {
					t.Fatal("confirmed commit was converted to cancellation", kind, creating)
				}
			}
		}
	})
	t.Run("capability_and_lock_options", func(t *testing.T) {
		if _, ok := any(backend).(db.ReadModifyWriteSession); ok {
			t.Fatal("root backend claimed a transaction policy")
		}
		check(t, backend.(db.SnapshotReader).ReadSnapshot(ctx, func(reader db.Queryer) error {
			if _, ok := reader.(db.ReadModifyWriteSession); ok {
				return errors.New("read-only snapshot exposed a write policy")
			}
			_, _, err := labels.LabelObjects.Using(reader).UpdateOrCreate(ctx, nil, labels.LabelPatch{})
			if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				return fmt.Errorf("snapshot accepted upsert: %w", err)
			}
			return nil
		}))
		q := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID))
		for _, options := range []orm.RowLockOptions{{}, {NoWait: true}, {NoKey: true}, {SkipLocked: true}} {
			_, _, err := q.SelectForUpdate(options, q.LockTarget()).UpdateOrCreate(ctx, nil, owners.RequiredOwnerPatch{})
			if postgres && !options.SkipLocked {
				check(t, err)
			} else if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatal("unsafe explicit upsert locking accepted", err)
			}
		}
		relations, err := project.BindRelations()
		check(t, err)
		_, _, err = q.SelectRelated(q.Related.Owner).SelectForUpdate(orm.RowLockOptions{}, relations.OwnersRequiredOwner.Owner.LockTarget()).UpdateOrCreate(ctx, nil, owners.RequiredOwnerPatch{})
		if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal("upsert without a source lock accepted", err)
		}
		check(t, backend.(db.Atomic).Atomic(ctx, func(parent db.Session) error {
			bound, err := project.UsingSession(parent)
			if err != nil {
				return err
			}
			return bound.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(required.ID)).Iterate(ctx, 1, func(_ context.Context, _ *project.OwnersRequiredOwner) (bool, error) {
				_, _, err := labels.LabelObjects.Using(parent).UpdateOrCreate(ctx, nil, singlePatch[labels.Label](func(value labels.Label) orm.Mutation[labels.Label] {
					return orm.InvalidMutation[labels.Label](errors.New("active cursor must reject before patch"))
				}))
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan, Category: query.CategoryBackend}) {
					return false, fmt.Errorf("active parent cursor allowed a nested write: %w", err)
				}
				return false, nil
			})
		}))
	})
	t.Run("root_batch_affinity", func(t *testing.T) {
		bound, err := project.Using(backend)
		check(t, err)
		calls := 0
		check(t, bound.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).Iterate(ctx, 1, func(batchContext context.Context, value *project.OwnersOwner) (bool, error) {
			calls++
			label, created, err := labels.LabelObjects.Using(backend).Filter(labels.LabelFields.Name.Exact("upsert in root batch")).UpdateOrCreate(batchContext, labels.NewLabelCreate("upsert in root batch"), nil)
			if err != nil || !created || label.ID == 0 || value.ID != first.ID {
				return false, fmt.Errorf("root batch lost its transaction affinity: %w", err)
			}
			return false, nil
		}))
		if calls != 1 {
			t.Fatal("missing root batch callback")
		}
	})
	t.Run("relation_query_scope", func(t *testing.T) {
		owner, err := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).Get(ctx)
		check(t, err)
		links, err := owner.Labels()
		check(t, err)
		base, err := links.Query()
		check(t, err)
		_, created, err := base.Filter(labels.LabelFields.ID.Exact(values[0].ID)).UpdateOrCreate(ctx, nil, labels.LabelPatch{}.WithNote("relation update"))
		check(t, err)
		if created {
			t.Fatal("relation query did not update its existing target")
		}
		value, created, err := base.Filter(labels.LabelFields.Name.Exact("outside relation")).UpdateOrCreate(ctx, labels.NewLabelCreate("outside relation"), nil)
		check(t, err)
		if !created || value.ID == 0 {
			t.Fatal("relation creation failed")
		}
		all, err := links.All(ctx)
		check(t, err)
		if len(all) != 1 {
			t.Fatal("upsert inferred an unauthorized collection Add")
		}
	})
}
