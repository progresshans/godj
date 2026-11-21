package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestCollectionPrefetchTree(t *testing.T) { withCollectionBackends(t, runCollectionPrefetchTree) }

func prefetchLabelGraph(t *testing.T, values []*project.LabelsLabel) []any {
	t.Helper()
	result := make([]any, 0, len(values))
	for _, label := range values {
		view, err := label.Owners()
		check(t, err)
		parents, err := view.All(t.Context())
		check(t, err)
		members := make([]any, 0, len(parents))
		for _, parent := range parents {
			view, err := parent.Labels()
			check(t, err)
			children, err := view.All(t.Context())
			check(t, err)
			names := make([]string, len(children))
			for i, child := range children {
				names[i] = child.Name
			}
			members = append(members, []any{parent.Name, names})
		}
		result = append(result, []any{label.Name, members})
	}
	return result
}
func prefetchOwnerGraph(t *testing.T, values []*project.OwnersOwner) []any {
	t.Helper()
	result := make([]any, len(values))
	for i, owner := range values {
		view, err := owner.Labels()
		check(t, err)
		children, err := view.All(t.Context())
		check(t, err)
		result[i] = prefetchLabelGraph(t, children)
	}
	return result
}

func runCollectionPrefetchTree(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	factory, err := project.BindCollections()
	check(t, err)
	var source []owners.Owner
	for _, name := range []string{"first", "second", "empty"} {
		value, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
		check(t, err)
		source = append(source, value)
	}
	var targets []labels.Label
	for _, name := range []string{"a", "b", "c"} {
		value, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name).WithNote("original"))
		check(t, err)
		targets = append(targets, value)
	}
	for i, values := range [][]labels.Label{targets[:2], targets[1:]} {
		view, err := factory.OwnersOwnerLabels.From(b, source[i])
		check(t, err)
		check(t, view.Add(ctx, values))
	}
	data, err := os.ReadFile("django-query.json")
	check(t, err)
	var reference struct {
		Observations map[string]json.RawMessage `json:"observations"`
	}
	check(t, json.Unmarshal(data, &reference))
	probe := &prefetchProbe{collectionBackend: b}
	api, err := project.Using(probe)
	check(t, err)
	base := api.OwnersOwner.OrderBy(owners.OwnerFields.ID.Asc())
	selection := base.Prefetch.Labels.WithChildren(api.LabelsLabel.Prefetch.Owners.WithChildren(base.Prefetch.Labels))

	for _, mode := range []string{"typed", "path", "merged"} {
		t.Run("reference_"+mode, func(t *testing.T) {
			q := base.PrefetchRelated(selection)
			if mode == "path" {
				q, err = base.PrefetchRelatedPaths("labels__owners__labels")
				check(t, err)
			}
			if mode == "merged" {
				q = base.PrefetchRelated(base.Prefetch.Labels, base.Prefetch.Labels.WithChildren(api.LabelsLabel.Prefetch.Owners), selection)
			}
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			batch := len(probe.plans) - before - 1 // The oracle starts from already loaded owners.
			before = len(probe.plans)
			again, err := q.All(ctx)
			check(t, err)
			graph := prefetchOwnerGraph(t, []*project.OwnersOwner{rows[0], rows[1], again[0], rows[2]})
			warm := len(probe.plans) - before
			view, err := rows[0].Labels()
			check(t, err)
			held, err := view.Query()
			check(t, err)
			before = len(probe.plans)
			refined, err := held.Filter(labels.LabelFields.Name.Exact("b")).All(ctx)
			check(t, err)
			refinedGraph := prefetchLabelGraph(t, refined)
			prefetchReference(t, reference.Observations, "prefetch_nested", map[string]any{"members": graph, "batch_queries": batch, "warm_queries": warm, "refined": refinedGraph, "refined_queries": len(probe.plans) - before})
			// First on a ready ordered cache retains the same descendant graph.
			before = len(probe.plans)
			ordered := base.PrefetchRelated(selection)
			first, found, err := ordered.First(ctx)
			check(t, err)
			if !found || len(probe.plans)-before != 4 {
				t.Fatal("cold First must load one complete tree")
			}
			prefetchOwnerGraph(t, []*project.OwnersOwner{first})
			before = len(probe.plans)
			_, err = ordered.All(ctx)
			check(t, err)
			if len(probe.plans)-before != 4 {
				t.Fatal("cold First populated full cache")
			}
			before = len(probe.plans)
			first, found, err = ordered.First(ctx)
			check(t, err)
			if !found {
				t.Fatal("warm First absent")
			}
			prefetchOwnerGraph(t, []*project.OwnersOwner{first})
			if len(probe.plans) != before {
				t.Fatal("warm First lost descendants")
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		before := len(probe.plans)
		foreign, err := project.Using(probe)
		check(t, err)
		bad := base.Prefetch.Labels.WithChildren(foreign.LabelsLabel.Prefetch.Owners)
		if values, err := base.PrefetchRelated(bad).All(ctx); err == nil || values != nil {
			t.Fatal("foreign child accepted")
		}
		for _, path := range []string{"labels__missing", "labels__", "__labels", strings.Repeat("friends__", query.MaximumRelationHops) + "friends"} {
			if _, err := base.PrefetchRelatedPaths(path); err == nil {
				t.Fatalf("invalid path %q accepted", path)
			}
		}
		leaf := base.Prefetch.Friends
		deep := leaf
		for i := 0; i < query.MaximumRelationHops; i++ {
			deep = leaf.WithChildren(deep)
		}
		if _, err := base.PrefetchRelated(deep).All(ctx); err == nil {
			t.Fatal("typed depth overflow accepted")
		}
		children := make([]project.OwnersOwnerPrefetchSelector, orm.MaximumRelatedSelectionNodes)
		for i := range children {
			children[i] = leaf
		}
		if _, err := base.PrefetchRelated(leaf.WithChildren(children...)).Count(ctx); err == nil {
			t.Fatal("merged duplicate nodes escaped the budget")
		}
		// A pointer can make a native immutable value refer to itself.
		cycle := factory.OwnersOwnerFriends.WithChildren()
		cycle = cycle.WithChildren(&cycle)
		if _, err := orm.PrefetchRelated(owners.OwnerObjects.Using(probe), cycle).All(ctx); err == nil {
			t.Fatal("native cycle accepted")
		}
		if len(probe.plans) != before {
			t.Fatal("invalid tree performed I/O")
		}
	})

	t.Run("failure_retry", func(t *testing.T) {
		failure := errors.New("deep child failure")
		q := base.PrefetchRelated(selection)
		probe.failure = failure
		probe.failAt = len(probe.plans) + 4
		if values, err := q.All(ctx); values != nil || !errors.Is(err, failure) {
			t.Fatal("partial tree published", err)
		}
		probe.failAt = 0
		before := len(probe.plans)
		values, err := q.All(ctx)
		check(t, err)
		if len(probe.plans)-before != 4 {
			t.Fatal("failed tree retained partial cache")
		}
		prefetchOwnerGraph(t, values)
		before = len(probe.plans)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if values, err := q.All(canceled); values != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("warm tree ignored cancellation", err)
		}
		if len(probe.plans) != before {
			t.Fatal("canceled tree performed I/O")
		}
		q = q.Fresh()
		interrupted, stop := context.WithCancel(ctx)
		probe.cancelAfterScan = stop
		probe.cancelAt = len(probe.plans) + 4
		if values, err := q.All(interrupted); values != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("deep cancellation published partial graph", err)
		}
		stop()
		probe.cancelAfterScan = nil
		probe.cancelAt = 0
		before = len(probe.plans)
		values, err = q.All(ctx)
		check(t, err)
		if len(probe.plans)-before != 4 {
			t.Fatal("canceled tree retained partial cache")
		}
		prefetchOwnerGraph(t, values)
	})

	t.Run("independent_snapshots", func(t *testing.T) {
		q := base.PrefetchRelated(selection)
		values, err := q.All(ctx)
		check(t, err)
		again, err := q.All(ctx)
		check(t, err)
		view, err := values[0].Labels()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		children, err := held.All(ctx)
		check(t, err)
		*children[0].Note = "caller"
		parentView, err := children[0].Owners()
		check(t, err)
		parents, err := parentView.All(ctx)
		check(t, err)
		inner, err := parents[0].Labels()
		check(t, err)
		check(t, inner.RemoveKeys(ctx, targets[0].ID))
		before := len(probe.plans)
		cached, err := held.All(ctx)
		check(t, err)
		if *cached[0].Note != "original" {
			t.Fatal("nested model clone aliases caller memory")
		}
		original := prefetchOwnerGraph(t, again)
		if !reflect.DeepEqual(prefetchOwnerGraph(t, values), original) || len(probe.plans) != before {
			t.Fatal("nested mutation changed another materialization")
		}
		current, err := inner.All(ctx)
		check(t, err)
		if len(current) != 1 || current[0].Name != "b" {
			t.Fatal("mutated manager did not refresh")
		}
		check(t, inner.AddKeys(ctx, []int64{targets[0].ID}))
	})

	t.Run("concurrent_warm", func(t *testing.T) {
		q := base.PrefetchRelated(selection)
		_, err := q.All(ctx)
		check(t, err)
		before := len(probe.plans)
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := q.All(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				prefetchOwnerGraph(t, rows)
			}()
		}
		wg.Wait()
		if len(probe.plans) != before {
			t.Fatal("concurrent warm tree performed I/O")
		}
	})

	t.Run("multiplicity_and_copy", func(t *testing.T) {
		r, err := project.BindRelations()
		check(t, err)
		before := len(probe.plans)
		rows, err := base.Filter(r.OwnersOwner.Labels.Name.In("a", "b")).PrefetchRelated(selection).All(ctx)
		check(t, err)
		if len(rows) != 3 || rows[0].ID != rows[1].ID || len(probe.plans)-before != 4 {
			t.Fatal("tree collapsed owner multiplicity or loaded per owner")
		}
		before = len(probe.plans)
		graphs := prefetchOwnerGraph(t, rows)
		if !reflect.DeepEqual(graphs[0], graphs[1]) || len(probe.plans) != before {
			t.Fatal("duplicate owner lost its nested graph")
		}
		leaf := base.Prefetch.Labels
		_ = leaf.WithChildren(api.LabelsLabel.Prefetch.Owners)
		before = len(probe.plans)
		_, err = base.PrefetchRelated(leaf).All(ctx)
		check(t, err)
		if len(probe.plans)-before != 2 {
			t.Fatal("child builder mutated original selector")
		}
		before = len(probe.plans)
		rows, err = base.Filter(owners.OwnerFields.ID.Exact(-1)).PrefetchRelated(selection).All(ctx)
		check(t, err)
		if len(rows) != 0 || len(probe.plans)-before != 1 {
			t.Fatal("empty owner batch performed child reads")
		}
	})

	t.Run("session_lifetime", func(t *testing.T) {
		var held project.LabelsLabelQuery
		var nested *project.LabelsLabel
		check(t, b.AtomicRelation(ctx, func(session db.RelationSession) error {
			api, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			q, err := api.OwnersOwner.OrderBy(owners.OwnerFields.ID.Asc()).PrefetchRelatedPaths("labels__owners__labels")
			if err != nil {
				return err
			}
			rows, err := q.All(ctx)
			if err != nil {
				return err
			}
			prefetchOwnerGraph(t, rows)
			view, err := rows[0].Labels()
			if err != nil {
				return err
			}
			held, err = view.Query()
			if err != nil {
				return err
			}
			children, err := held.All(ctx)
			if err != nil {
				return err
			}
			nested = children[0]
			return nil
		}))
		if values, err := held.All(ctx); values != nil || err == nil {
			t.Fatal("nested model graph escaped session")
		}
		if _, err := nested.Owners(); err == nil {
			t.Fatal("nested manager escaped session")
		}
	})
}
