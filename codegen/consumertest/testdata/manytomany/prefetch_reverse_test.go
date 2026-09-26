package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func TestReverseCollectionPrefetch(t *testing.T) {
	withCollectionBackends(t, runReverseCollectionPrefetch)
}
func reversePrefetchGraph(t *testing.T, rows []*project.OwnersOwner) []any {
	t.Helper()
	result := make([]any, 0, len(rows))
	for _, owner := range rows {
		view, err := owner.RankedLinkRows()
		check(t, err)
		links, err := view.All(t.Context())
		check(t, err)
		members := make([]any, 0, len(links))
		for _, link := range links {
			label, present, err := link.Label(t.Context())
			check(t, err)
			if !present {
				t.Fatal("reference label missing")
			}
			members = append(members, []any{link.Amount, label.Name})
		}
		result = append(result, []any{owner.Name, members})
	}
	return result
}
func runReverseCollectionPrefetch(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	first, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
	check(t, err)
	second, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("second"))
	check(t, err)
	a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a").WithNote("original"))
	check(t, err)
	bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b").WithNote("original"))
	check(t, err)
	for i, pair := range [][2]int64{{first.ID, a.ID}, {first.ID, bb.ID}, {second.ID, bb.ID}} {
		_, err = owners.RankedLinkObjects.Create(ctx, b, owners.NewRankedLinkCreate(int64(i+1)).WithOwnerID(pair[0]).WithLabelID(pair[1]))
		check(t, err)
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
	base := api.OwnersOwner.Filter(owners.OwnerFields.ID.In(first.ID, second.ID)).OrderBy(owners.OwnerFields.Name.Asc())
	selector := base.Prefetch.RankedLinkRows.OrderBy(owners.RankedLinkFields.Amount.Desc()).SelectRelated(api.OwnersRankedLink.Related.Label)
	for _, mode := range []string{"typed", "path_merge", "single_child"} {
		t.Run("reference_"+mode, func(t *testing.T) {
			q := base.PrefetchRelated(selector)
			if mode == "path_merge" {
				path, err := base.PrefetchPath("ranked_link_rows__label")
				check(t, err)
				q = base.PrefetchRelated(selector, path)
			}
			if mode == "single_child" {
				q = base.PrefetchRelated(base.Prefetch.RankedLinkRows.OrderBy(owners.RankedLinkFields.Amount.Desc()).WithChildren(api.OwnersRankedLink.Prefetch.Label))
			}
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			batch := len(probe.plans) - before
			before = len(probe.plans)
			members := reversePrefetchGraph(t, rows)
			again, err := q.All(ctx)
			check(t, err)
			if !reflect.DeepEqual(members, reversePrefetchGraph(t, again)) {
				t.Fatal("warm reverse graph changed")
			}
			if mode == "single_child" {
				if batch != 3 {
					t.Fatal("single child was not batched", batch)
				}
				batch--
			}
			prefetchReference(t, reference.Observations, "prefetch_eager_child", map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
		})
	}
	t.Run("held_query_composition", func(t *testing.T) {
		rows, err := base.PrefetchRelated(selector).All(ctx)
		check(t, err)
		view, err := rows[0].RankedLinkRows()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		checkLabels := func(values []*project.OwnersRankedLink) {
			for _, v := range values {
				_, ok, err := v.Label(ctx)
				check(t, err)
				if !ok {
					t.Fatal("configured eager label missing")
				}
			}
		}
		before := len(probe.plans)
		values, err := held.OrderBy(owners.RankedLinkFields.Amount.Asc()).All(ctx)
		check(t, err)
		checkLabels(values)
		if len(values) != 2 || len(probe.plans)-before != 1 {
			t.Fatal("refinement discarded target eager or owner scope")
		}
		before = len(probe.plans)
		one, ok, err := held.Fresh().First(ctx)
		check(t, err)
		if !ok || one.Amount != 2 {
			t.Fatal("configured First")
		}
		checkLabels([]*project.OwnersRankedLink{one})
		if len(probe.plans)-before != 1 {
			t.Fatal("cold First lost target eager")
		}
		before = len(probe.plans)
		eager := held.SelectRelated(held.Related.Owner)
		values, err = eager.All(ctx)
		check(t, err)
		checkLabels(values)
		for _, v := range values {
			owner, ok, err := v.Owner(ctx)
			check(t, err)
			if !ok || owner.ID != first.ID {
				t.Fatal("composed eager owner")
			}
		}
		if len(probe.plans)-before != 1 {
			t.Fatal("additional eager replaced configured targets")
		}
		before = len(probe.plans)
		q := held.PrefetchRelated(held.Prefetch.Owner.WithChildren(api.OwnersOwner.Prefetch.Labels)).SelectRelated(held.Related.Owner)
		values, err = q.All(ctx)
		check(t, err)
		checkLabels(values)
		for _, v := range values {
			owner, _, err := v.Owner(ctx)
			check(t, err)
			labels, err := owner.Labels()
			check(t, err)
			_, err = labels.All(ctx)
			check(t, err)
		}
		if len(probe.plans)-before != 2 {
			t.Fatal("configured eager and new child graph not retained")
		}
		limited, err := held.Fresh().Offset(1)
		check(t, err)
		one, ok, err = limited.First(ctx)
		check(t, err)
		if !ok || one.Amount != 1 {
			t.Fatal("configured offset First")
		}
		count, err := held.Fresh().Count(ctx)
		check(t, err)
		if count != 2 {
			t.Fatal("configured count escaped scope")
		}
	})
	t.Run("lookup_origin_budget", func(t *testing.T) {
		foreign, err := project.Using(probe)
		check(t, err)
		before := len(probe.plans)
		path, err := base.PrefetchPath("ranked_link_rows__label")
		check(t, err)
		selections := make([]project.OwnersRankedLinkRelationSelector, orm.MaximumRelatedSelectionNodes)
		for i := range selections {
			selections[i] = api.OwnersRankedLink.Related.Label
		}
		queries := []project.OwnersOwnerPrefetchQuery{base.PrefetchRelated(path, selector), base.PrefetchRelated(base.Prefetch.RankedLinkRows.SelectRelated(foreign.OwnersRankedLink.Related.Label)), base.PrefetchRelated(base.Prefetch.RankedLinkRows.WithChildren(foreign.OwnersRankedLink.Prefetch.Label)), base.PrefetchRelated(base.Prefetch.RankedLinkRows.SelectRelated(selections...))}
		for _, q := range queries {
			if rows, err := q.All(ctx); err == nil || rows != nil {
				t.Fatal("invalid lookup/origin/budget accepted")
			}
		}
		if len(probe.plans) != before {
			t.Fatal("invalid reverse selection performed I/O")
		}
	})
	t.Run("failure_foreign_cancel_retry", func(t *testing.T) {
		sentinel := errors.New("reverse target failure")
		q := base.PrefetchRelated(selector)
		probe.failAt = len(probe.plans) + 2
		probe.failure = sentinel
		if rows, err := q.All(ctx); !errors.Is(err, sentinel) || rows != nil {
			t.Fatal("target failure leaked owners", err)
		}
		probe.failAt = 0
		_, err = q.All(ctx)
		check(t, err)
		q = q.Fresh()
		probe.corrupt = "foreign"
		probe.corruptAt = len(probe.plans) + 2
		if rows, err := q.All(ctx); err == nil || rows != nil {
			t.Fatal("foreign target accepted")
		}
		probe.corrupt = ""
		probe.corruptAt = 0
		_, err = q.All(ctx)
		check(t, err)
		canceled, cancel := context.WithCancel(ctx)
		probe.cancelAfterScan = cancel
		probe.cancelAt = len(probe.plans) + 2
		q = q.Fresh()
		if rows, err := q.All(canceled); !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal("canceled reverse graph escaped", err)
		}
		cancel()
		probe.cancelAfterScan = nil
		probe.cancelAt = 0
		_, err = q.All(ctx)
		check(t, err)
	})
	t.Run("independent_reset_and_concurrent", func(t *testing.T) {
		q := base.PrefetchRelated(selector.Filter(owners.RankedLinkFields.Amount.Exact(2)))
		rows, err := q.All(ctx)
		check(t, err)
		again, err := q.All(ctx)
		check(t, err)
		view, err := rows[0].RankedLinkRows()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		values, err := held.All(ctx)
		check(t, err)
		label, _, err := values[0].Label(ctx)
		check(t, err)
		*label.Note = "caller"
		check(t, view.Invalidate())
		before := len(probe.plans)
		old, err := held.All(ctx)
		check(t, err)
		label, _, err = old[0].Label(ctx)
		check(t, err)
		if len(old) != 1 || *label.Note != "original" || len(probe.plans) != before {
			t.Fatal("held snapshot changed")
		}
		other, err := again[0].RankedLinkRows()
		check(t, err)
		cached, err := other.All(ctx)
		check(t, err)
		if len(cached) != 1 || len(probe.plans) != before {
			t.Fatal("independent owner cache changed")
		}
		current, err := view.All(ctx)
		check(t, err)
		if len(current) != 2 || current[0].Amount != 1 {
			t.Fatal("Invalidate did not restore default scope/order")
		}
		fresh, err := other.Fresh()
		check(t, err)
		current, err = fresh.All(ctx)
		check(t, err)
		if len(current) != 2 {
			t.Fatal("manager Fresh retained custom filter")
		}
		before = len(probe.plans)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				values, err := q.All(ctx)
				if err != nil || len(values) != 2 {
					t.Error("warm concurrent owners", err)
				}
			}()
		}
		wg.Wait()
		if len(probe.plans) != before {
			t.Fatal("warm concurrent graph did I/O")
		}
	})

	t.Run("native_indexed_target", func(t *testing.T) {
		binding, err := project.Bind()
		check(t, err)
		owner, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}, owners.OwnerDescriptor{})
		check(t, err)
		link, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "ranked_link"}, owners.RankedLinkDescriptor{})
		check(t, err)
		label, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}, labels.LabelDescriptor{})
		check(t, err)
		reverse, err := orm.BindReverseObject(owner, "ranked_link_rows", link)
		check(t, err)
		forward, err := orm.BindNullableForwardObject(link, "label", label)
		check(t, err)
		graph, err := orm.PrefetchRelated(owners.OwnerObjects.Using(probe).Filter(owners.OwnerFields.ID.Exact(first.ID)), reverse.WithChildren().OrderBy(owners.RankedLinkFields.Amount.Desc()).SelectRelated(orm.SelectNullableForward(forward))).All(ctx)
		check(t, err)
		set, found, err := reverse.FromPrefetched(graph[0])
		check(t, err)
		if !found {
			t.Fatal("native reverse cache absent")
		}
		held, err := set.Query()
		check(t, err)
		before := len(probe.plans)
		value, found, err := held.Fresh().At(ctx, 1)
		check(t, err)
		if !found || value.Amount != 1 || len(probe.plans)-before != 1 || len(probe.plans[before].RelationProjections()) != 1 {
			t.Fatal("indexed target lost eager configuration or owner scope")
		}
		before = len(probe.plans)
		_, found, err = held.Fresh().At(ctx, 1<<31+1)
		check(t, err)
		if found || len(probe.plans)-before != 1 {
			t.Fatal("large index row-drain boundary")
		}
		sentinel := errors.New("indexed target failure")
		probe.failAt = len(probe.plans) + 1
		probe.failure = sentinel
		if _, found, err := held.Fresh().At(ctx, 1); !errors.Is(err, sentinel) || found {
			t.Fatal("indexed eager failure escaped", err)
		}
		probe.failAt = 0
		_, found, err = held.Fresh().At(ctx, 1)
		check(t, err)
		if !found {
			t.Fatal("indexed query did not retry")
		}
		before = len(probe.plans)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := held.Fresh().At(canceled, 0); !errors.Is(err, context.Canceled) {
			t.Fatal("indexed cancellation", err)
		}
		if len(probe.plans) != before {
			t.Fatal("canceled index performed I/O")
		}
	})
	t.Run("session_lifetime", func(t *testing.T) {
		var rows []*project.OwnersOwner
		var held project.OwnersRankedLinkQuery
		var view *project.OwnersOwnerRankedLinkRowsCollection
		check(t, b.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			scoped, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			rows, err = scoped.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).PrefetchRelated(scoped.OwnersOwner.Prefetch.RankedLinkRows.SelectRelated(scoped.OwnersRankedLink.Related.Label)).All(ctx)
			if err != nil {
				return err
			}
			view, err = rows[0].RankedLinkRows()
			if err != nil {
				return err
			}
			held, err = view.Query()
			if err != nil {
				return err
			}
			_, err = held.All(ctx)
			return err
		}))
		if _, err := view.All(ctx); err == nil {
			t.Fatal("expired view read")
		}
		if _, err := held.All(ctx); err == nil {
			t.Fatal("expired held query")
		}
		if err := view.Invalidate(); err == nil {
			t.Fatal("expired invalidation")
		}
		if _, err := view.Fresh(); err == nil {
			t.Fatal("expired fresh")
		}
	})
}
