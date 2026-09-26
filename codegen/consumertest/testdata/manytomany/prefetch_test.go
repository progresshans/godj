package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestCollectionPrefetch(t *testing.T) { withCollectionBackends(t, runCollectionPrefetch) }

func prefetchNames[T, L any](t *testing.T, values []*orm.ManyCollection[T, L], name func(T) string) [][]string {
	t.Helper()
	result := make([][]string, len(values))
	for i, collection := range values {
		rows, err := collection.All(t.Context())
		check(t, err)
		result[i] = make([]string, len(rows))
		for j, row := range rows {
			result[i][j] = name(row)
		}
		slices.Sort(result[i])
	}
	return result
}
func prefetchReference(t *testing.T, expected map[string]json.RawMessage, key string, value any) {
	t.Helper()
	actual, err := json.Marshal(value)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(actual, &left))
	check(t, json.Unmarshal(expected[key], &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("%s got %s want %s", key, actual, expected[key])
	}
}

type prefetchProbe struct {
	collectionBackend
	plans           []query.Plan
	failAt          int
	failure         error
	corrupt         string
	cancelAfterScan context.CancelFunc
	cancelAt        int
	closes          int
}

func (p *prefetchProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.plans = append(p.plans, plan)
	if p.failAt == len(p.plans) {
		return nil, p.failure
	}
	rows, err := p.collectionBackend.Query(ctx, plan)
	if err != nil || p.corrupt == "" && p.cancelAfterScan == nil {
		return rows, err
	}
	index := -1
	if len(plan.Conditions()) > 0 {
		for i, field := range plan.SourceFields() {
			if field.Equal(plan.Conditions()[0].Field()) {
				index = i
				break
			}
		}
	}
	cancel := p.cancelAfterScan
	if p.cancelAt != 0 && p.cancelAt != len(p.plans) {
		cancel = nil
	}
	mode := p.corrupt
	if mode == "foreign_owner" && plan.ResultShape().Kind() == query.ResultPrefetch {
		mode, index = "foreign", len(plan.SourceFields())
	}
	return &prefetchRows{Rows: rows, mode: mode, index: index, cancel: cancel, closed: &p.closes}, nil
}

type prefetchRows struct {
	db.Rows
	mode   string
	index  int
	repeat bool
	cancel context.CancelFunc
	closed *int
}

func (r *prefetchRows) Next() bool {
	if r.mode == "duplicate" && r.repeat {
		r.repeat = false
		return true
	}
	value := r.Rows.Next()
	if value && r.mode == "duplicate" {
		r.repeat = true
	}
	return value
}
func (r *prefetchRows) Close() error { *r.closed++; return r.Rows.Close() }
func (r *prefetchRows) Scan(destinations ...any) error {
	if err := r.Rows.Scan(destinations...); err != nil {
		return err
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.mode == "foreign" {
		if r.index < 0 {
			return errors.New("missing owner column in probe")
		}
		if scanner, ok := destinations[r.index].(sql.Scanner); ok {
			return scanner.Scan(int64(-999999))
		}
		return errors.New("owner projection is not a scanner")
	}
	return nil
}

// PostgreSQL's native ordinary session also implements relation operations.
// This adapter deliberately exposes only Session plus its lifetime, and a
// trap proves that a read handle never starts an inner transaction for writes.
type prefetchReadSession struct {
	db.Session
	nested *int
}

func (s prefetchReadSession) ValidateSession(ctx context.Context) error {
	return s.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (s prefetchReadSession) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	*s.nested++
	return errors.New("nested relation transaction")
}

func runCollectionPrefetch(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	factory, err := project.BindCollections()
	check(t, err)
	first, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
	check(t, err)
	second, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("second"))
	check(t, err)
	empty, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("empty"))
	check(t, err)
	a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a").WithNote("original"))
	check(t, err)
	bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
	check(t, err)
	cc, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("c"))
	check(t, err)
	left, err := factory.OwnersOwnerLabels.From(b, first)
	check(t, err)
	check(t, left.Add(ctx, []labels.Label{a, bb}))
	right, err := factory.OwnersOwnerLabels.From(b, second)
	check(t, err)
	check(t, right.Add(ctx, []labels.Label{bb}))
	bytes, err := os.ReadFile("django-query.json")
	check(t, err)
	var reference struct {
		Observations map[string]json.RawMessage `json:"observations"`
	}
	check(t, json.Unmarshal(bytes, &reference))
	labelName := func(value labels.Label) string { return value.Name }
	ownerName := func(value owners.Owner) string { return value.Name }
	probe := &prefetchProbe{collectionBackend: b}
	t.Run("reference", func(t *testing.T) {
		reads := map[string]int{}
		before := len(probe.plans)
		sets, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, []owners.Owner{first, empty, second, first})
		check(t, err)
		reads["batch"] = len(probe.plans) - before
		before = len(probe.plans)
		prefetchReference(t, reference.Observations, "prefetch_membership", prefetchNames(t, sets, labelName))
		reads["warm"] = len(probe.plans) - before
		held, err := sets[0].Query()
		check(t, err)
		before = len(probe.plans)
		none, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, nil)
		check(t, err)
		if none == nil || len(none) != 0 {
			t.Fatal("empty prefetch result")
		}
		reads["empty"] = len(probe.plans) - before
		before = len(probe.plans)
		refined, err := held.Filter(labels.LabelFields.Name.Exact("a")).All(ctx)
		check(t, err)
		reads["refined"] = len(probe.plans) - before
		refinedNames := make([]string, len(refined))
		for i, row := range refined {
			refinedNames[i] = row.Name
		}
		check(t, sets[0].Add(ctx, []labels.Label{cc}))
		before = len(probe.plans)
		current := prefetchNames(t, []*orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]{sets[0], sets[3], sets[2]}, labelName)
		prior, err := held.All(ctx)
		check(t, err)
		priorNames := make([]string, len(prior))
		for i, row := range prior {
			priorNames[i] = row.Name
		}
		slices.Sort(priorNames)
		reads["after_mutation"] = len(probe.plans) - before
		prefetchReference(t, reference.Observations, "prefetch_cache", map[string][]string{"after_add": current[0], "duplicate_snapshot": current[1], "other_owner": current[2], "held_snapshot": priorNames, "refined": refinedNames})
		prefetchReference(t, reference.Observations, "prefetch_queries", reads)
		rows, err := sets[3].All(ctx)
		check(t, err)
		*rows[0].Note = "caller"
		rows, err = sets[3].All(ctx)
		check(t, err)
		if *rows[0].Note != "original" {
			t.Fatal("prefetch cache aliases caller values")
		}
	})
	t.Run("nullable_and_self", func(t *testing.T) {
		var loose []owners.Owner
		for _, name := range []string{"empty", "mixed", "null"} {
			row, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
			check(t, err)
			loose = append(loose, row)
		}
		for i, pair := range []struct{ owner, label *int64 }{{&loose[1].ID, &a.ID}, {&loose[1].ID, &bb.ID}, {&loose[1].ID, &a.ID}, {&loose[1].ID, nil}, {&loose[2].ID, nil}, {nil, &a.ID}} {
			input := owners.NewLooseLinkCreate(int64(i + 1))
			if pair.owner != nil {
				input = input.WithOwnerID(*pair.owner)
			}
			if pair.label != nil {
				input = input.WithLabelID(*pair.label)
			}
			_, err := owners.LooseLinkObjects.Create(ctx, b, input)
			check(t, err)
		}
		before := len(probe.plans)
		forward, err := factory.OwnersOwnerLoose.Prefetch(ctx, probe, loose)
		check(t, err)
		reverse, err := factory.LabelsLabelLooseOwners.Prefetch(ctx, probe, []labels.Label{a, bb, cc})
		check(t, err)
		batchReads := len(probe.plans) - before
		before = len(probe.plans)
		actual := map[string]any{"forward": prefetchNames(t, forward, labelName), "reverse": prefetchNames(t, reverse, ownerName), "batch_queries": batchReads, "warm_queries": len(probe.plans) - before}
		prefetchReference(t, reference.Observations, "prefetch_nullable", actual)
		var nodes []owners.Owner
		for _, name := range []string{"x", "y", "z"} {
			row, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
			check(t, err)
			nodes = append(nodes, row)
		}
		friends, err := factory.OwnersOwnerFriends.From(b, nodes[0])
		check(t, err)
		check(t, friends.Add(ctx, []owners.Owner{nodes[0], nodes[1]}))
		follows, err := factory.OwnersOwnerFollows.From(b, nodes[0])
		check(t, err)
		check(t, follows.Add(ctx, []owners.Owner{nodes[1]}))
		follows, err = factory.OwnersOwnerFollows.From(b, nodes[2])
		check(t, err)
		check(t, follows.Add(ctx, []owners.Owner{nodes[0]}))
		before = len(probe.plans)
		fs, err := factory.OwnersOwnerFriends.Prefetch(ctx, probe, nodes)
		check(t, err)
		following, err := factory.OwnersOwnerFollows.Prefetch(ctx, probe, nodes)
		check(t, err)
		followers, err := factory.OwnersOwnerFollowers.Prefetch(ctx, probe, nodes)
		check(t, err)
		batchReads = len(probe.plans) - before
		before = len(probe.plans)
		self := map[string]any{"friends": prefetchNames(t, fs, ownerName), "follows": prefetchNames(t, following, ownerName), "followers": prefetchNames(t, followers, ownerName), "batch_queries": batchReads, "warm_queries": len(probe.plans) - before}
		prefetchReference(t, reference.Observations, "prefetch_self", self)
	})
	t.Run("facade_query", func(t *testing.T) {
		api, err := project.Using(probe)
		check(t, err)
		base := api.OwnersOwner.Filter(owners.OwnerFields.ID.In(first.ID, second.ID, empty.ID)).OrderBy(owners.OwnerFields.ID.Asc())
		selected := base.PrefetchRelated(base.Prefetch.Labels, base.Prefetch.Friends, base.Prefetch.Labels)
		before := len(probe.plans)
		count, err := selected.Count(ctx)
		check(t, err)
		if count != 3 || len(probe.plans) != before+1 || !probe.plans[before].ResultShape().IsCountAll() {
			t.Fatal("cold count evaluated collections")
		}
		before = len(probe.plans)
		one, found, err := selected.First(ctx)
		check(t, err)
		if !found || one.ID != first.ID || len(probe.plans) != before+3 {
			t.Fatal("First did not bound owner prefetch")
		}
		before = len(probe.plans)
		rows, err := selected.All(ctx)
		check(t, err)
		if len(rows) != 3 || len(probe.plans) != before+3 {
			t.Fatal("prefetch query did not batch distinct selections")
		}
		before = len(probe.plans)
		again, err := selected.All(ctx)
		check(t, err)
		_, _, err = selected.First(ctx)
		check(t, err)
		_, err = selected.Count(ctx)
		check(t, err)
		for _, row := range again {
			view, err := row.Labels()
			check(t, err)
			_, err = view.All(ctx)
			check(t, err)
			friends, err := row.Friends()
			check(t, err)
			_, err = friends.All(ctx)
			check(t, err)
		}
		if len(probe.plans) != before {
			t.Fatal("warm prefetch performed I/O")
		}
		view, err := rows[0].Labels()
		check(t, err)
		check(t, view.RemoveKeys(ctx, a.ID))
		before = len(probe.plans)
		stale, err := again[0].Labels()
		check(t, err)
		values, err := stale.All(ctx)
		check(t, err)
		if len(values) != 3 || len(probe.plans) != before {
			t.Fatal("another materialization lost its snapshot")
		}
		warm, err := selected.All(ctx)
		check(t, err)
		warmView, err := warm[0].Labels()
		check(t, err)
		values, err = warmView.All(ctx)
		check(t, err)
		if len(values) != 3 {
			t.Fatal("returned collection changed the query's canonical cache")
		}
		fresh, err := selected.Fresh().All(ctx)
		check(t, err)
		freshView, err := fresh[0].Labels()
		check(t, err)
		values, err = freshView.All(ctx)
		check(t, err)
		if len(values) != 2 {
			t.Fatal("Fresh retained old membership")
		}
		dynamic, err := base.PrefetchRelatedPaths("labels", "friends")
		check(t, err)
		if values, err := dynamic.All(ctx); err != nil || len(values) != 3 {
			t.Fatal("dynamic prefetch", err)
		}
		limited, err := selected.Offset(1)
		check(t, err)
		limited, err = limited.Limit(1)
		check(t, err)
		before = len(probe.plans)
		values2, err := limited.All(ctx)
		check(t, err)
		if len(values2) != 1 || values2[0].ID != second.ID || len(probe.plans) != before+3 {
			t.Fatal("derived slice reused a cache")
		}
		r, err := project.BindRelations()
		check(t, err)
		multi := base.Filter(r.OwnersOwner.Labels.Name.In("b", "c")).PrefetchRelated(base.Prefetch.Labels)
		duplicates, err := multi.All(ctx)
		check(t, err)
		if len(duplicates) != 3 {
			t.Fatal("prefetch collapsed owner multiplicity")
		}
		unique, err := multi.Distinct().All(ctx)
		check(t, err)
		if len(unique) != 2 {
			t.Fatal("prefetch ignored explicit distinct")
		}
		foreign, err := project.Using(probe)
		check(t, err)
		before = len(probe.plans)
		if rows, err := base.PrefetchRelated(foreign.OwnersOwner.Prefetch.Labels).All(ctx); err == nil || rows != nil {
			t.Fatal("foreign prefetch selector accepted")
		}
		if _, err := base.PrefetchRelatedPaths("labels", "missing"); err == nil {
			t.Fatal("partial dynamic prefetch accepted")
		}
		var zero project.OwnersOwnerPrefetchSelector
		if _, err := base.PrefetchRelated(zero).Count(ctx); err == nil {
			t.Fatal("zero selector accepted")
		}
		if _, _, err := api.OwnersOwner.PrefetchRelated(api.OwnersOwner.Prefetch.Labels).First(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnorderedQuery}) {
			t.Fatal("unordered prefetch First", err)
		}
		if len(probe.plans) != before {
			t.Fatal("invalid prefetch performed I/O")
		}
		failure := errors.New("second relation failure")
		probe.failAt = len(probe.plans) + 3
		probe.failure = failure
		if rows, err := selected.Fresh().All(ctx); rows != nil || !errors.Is(err, failure) {
			t.Fatal("failed prefetch published owners", err)
		}
		probe.failAt = 0
		retry := selected.Fresh()
		probe.failAt = len(probe.plans) + 3
		if rows, err := retry.All(ctx); rows != nil || !errors.Is(err, failure) {
			t.Fatal("failed prefetch query", err)
		}
		probe.failAt = 0
		before = len(probe.plans)
		if _, err := retry.All(ctx); err != nil || len(probe.plans) != before+3 {
			t.Fatal("retry retained a partial source/cache", err)
		}
	})
	t.Run("batch_and_failures", func(t *testing.T) {
		// A present key is an explicit model state; absent database owners have
		// empty membership. Include enough distinct snapshots to cross a batch.
		inputs := make([]owners.Owner, 1001)
		for i := range inputs {
			inputs[i] = owners.NewOwnerWithID(int64(100000 + i))
		}
		inputs[0] = first
		before := len(probe.plans)
		sets, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, inputs)
		check(t, err)
		if len(sets) != len(inputs) || len(probe.plans) != before+2 {
			t.Fatal("prefetch did not bound owner batches")
		}
		failure := errors.New("late batch failure")
		probe.failAt = len(probe.plans) + 2
		probe.failure = failure
		if result, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, inputs); result != nil || !errors.Is(err, failure) {
			t.Fatal("late batch published a prefix", err)
		}
		probe.failAt = 0
		before = len(probe.plans)
		if result, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, []owners.Owner{first, {Name: "unsaved"}}); result != nil || !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) {
			t.Fatal("unsaved owner batch", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if result, err := factory.OwnersOwnerLabels.Prefetch(canceled, probe, nil); result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("empty canceled prefetch", err)
		}
		if _, err := factory.OwnersOwnerLabels.Prefetch(nil, probe, nil); err == nil {
			t.Fatal("nil context accepted")
		}
		if len(probe.plans) != before {
			t.Fatal("invalid batch performed I/O")
		}
		for _, mode := range []string{"foreign", "duplicate"} {
			probe.corrupt = mode
			result, err := factory.OwnersOwnerLabels.Prefetch(ctx, probe, []owners.Owner{first})
			if result != nil || !errors.Is(err, &query.Error{Code: query.CodeRelatedSetMembership}) {
				t.Fatal("untrusted prefetch rows", mode, err)
			}
		}
		probe.corrupt = ""
		interrupted, cancel := context.WithCancel(ctx)
		defer cancel()
		probe.cancelAfterScan = cancel
		closed := probe.closes
		if rows, err := factory.OwnersOwnerLabels.Prefetch(interrupted, probe, []owners.Owner{first}); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("scan cancellation published a prefix", err)
		}
		probe.cancelAfterScan = nil
		if probe.closes != closed+1 {
			t.Fatal("canceled prefetch leaked rows")
		}
	})
	t.Run("concurrent_query", func(t *testing.T) {
		counted := &countingCollectionBackend{collectionBackend: b}
		api, err := project.Using(counted)
		check(t, err)
		selected := api.OwnersOwner.Filter(owners.OwnerFields.ID.In(first.ID, second.ID, empty.ID)).PrefetchRelated(api.OwnersOwner.Prefetch.Labels, api.OwnersOwner.Prefetch.Friends)
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				rows, err := selected.All(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				for _, row := range rows {
					view, err := row.Labels()
					if err != nil {
						t.Error(err)
						return
					}
					values, err := view.All(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					for _, value := range values {
						value.Name = "caller"
					}
				}
			})
		}
		wg.Wait()
		if counted.queries.Load() != 3 {
			t.Fatal("concurrent prefetch did not share only its immutable evaluation", counted.queries.Load())
		}
	})
	t.Run("ordinary_session_reads", func(t *testing.T) {
		for _, mode := range []struct {
			name  string
			begin func(context.Context, func(db.Session) error) error
		}{{"atomic", b.(db.Atomic).Atomic}, {"coordinated", b.(db.CoordinatedAtomic).CoordinatedAtomic}} {
			t.Run(mode.name, func(t *testing.T) {
				var saved *project.OwnersOwner
				var raw *orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]
				nested := 0
				check(t, mode.begin(ctx, func(session db.Session) error {
					session = prefetchReadSession{Session: session, nested: &nested}
					var err error
					values, err := factory.OwnersOwnerLabels.PrefetchInSession(ctx, session, []owners.Owner{first})
					if err != nil {
						return err
					}
					raw = values[0]
					api, err := project.UsingSession(session)
					if err != nil {
						return err
					}
					values2, err := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).PrefetchRelated(api.OwnersOwner.Prefetch.Labels).All(ctx)
					if err != nil {
						return err
					}
					saved = values2[0]
					view, err := saved.Labels()
					if err != nil {
						return err
					}
					if _, err = view.All(ctx); err != nil {
						return err
					}
					if err = view.Clear(ctx); err == nil {
						return errors.New("ordinary session allowed relation mutation")
					}
					if err = raw.AddKeys(ctx, nil); err == nil {
						return errors.New("ordinary session hid unsupported mutation in a no-op")
					}
					if _, err = view.All(ctx); err != nil {
						return err
					}
					return nil
				}))
				if nested != 0 {
					t.Fatal("borrowed read session started an inner transaction")
				}
				if _, err := raw.All(ctx); err == nil {
					t.Fatal("ordinary prefetched cache escaped session")
				}
				if _, err := saved.Labels(); err == nil {
					t.Fatal("ordinary prefetched model escaped session")
				}
			})
		}
	})
	t.Run("session", func(t *testing.T) {
		var expired db.RelationSession
		var sets []*orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]
		var selected project.OwnersOwnerPrefetchQuery
		check(t, b.AtomicRelation(ctx, func(session db.RelationSession) error {
			expired = session
			var err error
			sets, err = factory.OwnersOwnerLabels.PrefetchInSession(ctx, session, []owners.Owner{first})
			if err != nil {
				return err
			}
			api, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			selected = api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).PrefetchRelated(api.OwnersOwner.Prefetch.Labels)
			if _, err = selected.All(ctx); err != nil {
				return err
			}
			if _, err = factory.OwnersOwnerLabels.Prefetch(ctx, session, nil); err == nil {
				return errors.New("borrowed prefetch accepted through root API")
			}
			return nil
		}))
		if _, err := sets[0].All(ctx); err == nil {
			t.Fatal("expired prefetched cache accepted")
		}
		if _, err := factory.OwnersOwnerLabels.PrefetchInSession(ctx, expired, nil); err == nil {
			t.Fatal("empty expired prefetch accepted")
		}
		if _, err := selected.All(ctx); err == nil {
			t.Fatal("expired prefetch query accepted")
		}
		if _, err := selected.Count(ctx); err == nil {
			t.Fatal("expired prefetch count accepted")
		}
	})
}

// Custom target filters may already contain multiple joins to the intermediary.
// Keep their scopes and read the owner identity selected by the grouping query.
func TestCollectionPrefetchOwnerPlans(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
		t.Cleanup(func() { check(t, b.Close()) })
		migrateCollections(t, b)
		ctx := t.Context()
		factory, err := project.BindCollections()
		check(t, err)
		relations, err := project.BindRelations()
		check(t, err)
		var targets []labels.Label
		for _, name := range []string{"a", "b", "c"} {
			v, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
			check(t, err)
			targets = append(targets, v)
		}
		var sources []owners.Owner
		for _, name := range []string{"first", "second"} {
			v, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
			check(t, err)
			sources = append(sources, v)
		}
		for i, source := range sources {
			set, err := factory.OwnersOwnerLabels.From(b, source)
			check(t, err)
			check(t, set.Add(ctx, targets[i:i+2]))
		}
		set, err := factory.OwnersOwnerLabels.From(b, sources[0])
		check(t, err)
		manager, err := set.Query()
		check(t, err)
		rawPath, ok := manager.Plan().Conditions()[0].RelationPath()
		if !ok {
			t.Fatal("missing physical membership path")
		}
		targetBase := labels.LabelObjects.Using(b)
		var targetKey, linkKey query.FieldRef
		for _, field := range targetBase.Plan().SourceFields() {
			if field.Name() == "id" {
				targetKey = field
			}
		}
		for _, field := range (owners.OwnerLabelsLinkDescriptor{}).Metadata().Fields {
			if field.PrimaryKey {
				linkKey = query.NewFieldRef(field.Name, field.Column, query.FieldInteger, field.Nullable)
			}
		}
		path, err := query.NewRelationChain(rawPath.Hops(), []query.FieldRef{targetKey, linkKey}, rawPath.Terminal(), query.RelationTerminalRelatedField)
		check(t, err)
		bytes, err := os.ReadFile("django-query.json")
		check(t, err)
		var oracle struct {
			Observations map[string]json.RawMessage `json:"observations"`
		}
		check(t, json.Unmarshal(bytes, &oracle))
		var expected map[string]struct {
			Members      [][]string `json:"members"`
			BatchQueries int        `json:"batch_queries"`
		}
		check(t, json.Unmarshal(oracle.Observations["prefetch_filtered_relation"], &expected))
		for _, name := range []string{"owner_second", "owner_either", "successive_owners", "successive_only_first", "successive_only_second", "distinct_owners", "excluded_owner"} {
			t.Run(name, func(t *testing.T) {
				q := targetBase
				actualProbe := &prefetchProbe{collectionBackend: b}
				api, err := project.Using(actualProbe)
				check(t, err)
				selector := api.OwnersOwner.Prefetch.Labels
				apply := func(predicates ...orm.Predicate[labels.Label]) {
					q = q.Filter(predicates...)
					selector = selector.Filter(predicates...)
				}
				requested := sources
				switch name {
				case "owner_second":
					apply(relations.LabelsLabel.Owners.Name.Exact("second"))
				case "owner_either", "distinct_owners":
					apply(relations.LabelsLabel.Owners.Name.In("first", "second"))
					if name == "distinct_owners" {
						q = q.Distinct()
						selector = selector.Distinct()
					}
				case "successive_owners", "successive_only_first", "successive_only_second":
					apply(relations.LabelsLabel.Owners.Name.Exact("first"))
					apply(relations.LabelsLabel.Owners.Name.Exact("second"))
					if name == "successive_only_first" {
						requested = sources[:1]
					}
					if name == "successive_only_second" {
						requested = sources[1:]
					}
				case "excluded_owner":
					apply(orm.Not(relations.LabelsLabel.Owners.Name.Exact("second")))
				}
				keys := make([]int64, len(requested))
				members := make([][]string, len(requested))
				positions := map[int64]int{}
				for i, source := range requested {
					keys[i] = source.ID
					positions[source.ID] = i
					members[i] = []string{}
				}
				plan, err := q.OrderBy(labels.LabelFields.Name.Asc()).Plan().ForPrefetchOwners(path, keys)
				check(t, err)
				probe := &prefetchProbe{collectionBackend: b}
				rows, err := probe.Query(ctx, plan)
				check(t, err)
				for rows.Next() {
					var id, owner int64
					var label string
					var note sql.NullString
					check(t, rows.Scan(&id, &label, &note, &owner))
					position, present := positions[owner]
					if !present {
						t.Fatal("owner projection escaped its requested batch", owner)
					}
					members[position] = append(members[position], label)
				}
				check(t, rows.Err())
				check(t, rows.Close())
				want, present := expected[name]
				if !present {
					t.Fatal("missing independent prefetch observation", name)
				}
				if !reflect.DeepEqual(members, want.Members) || len(probe.plans) != want.BatchQueries {
					t.Fatal("custom prefetch membership", members, "want", want.Members, "queries", len(probe.plans))
				}

				actual, err := api.OwnersOwner.Filter(owners.OwnerFields.ID.In(keys...)).OrderBy(owners.OwnerFields.ID.Asc()).PrefetchRelated(selector.OrderBy(labels.LabelFields.Name.Asc())).All(ctx)
				check(t, err)
				actualMembers := make([][]string, len(actual))
				for i, owner := range actual {
					view, err := owner.Labels()
					check(t, err)
					values, err := view.All(ctx)
					check(t, err)
					actualMembers[i] = make([]string, len(values))
					for j, v := range values {
						actualMembers[i][j] = v.Name
					}
				}
				if !reflect.DeepEqual(actualMembers, want.Members) || len(actualProbe.plans) != 1+want.BatchQueries {
					t.Fatal("generated custom prefetch changed oracle membership", actualMembers, want.Members, len(actualProbe.plans))
				}
			})
		}
	})
}
