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
)

func TestSinglePrefetchComposition(t *testing.T) {
	withCollectionBackends(t, runSinglePrefetchComposition)
}

func singlePrefetchGraph(t *testing.T, rows []*project.OwnersRankedLink) []any {
	t.Helper()
	result := make([]any, 0, len(rows))
	for _, row := range rows {
		parent, present, err := row.Owner(t.Context())
		check(t, err)
		if !present {
			result = append(result, []any{row.Amount, nil, []string{}})
			continue
		}
		view, err := parent.Ranked()
		check(t, err)
		children, err := view.All(t.Context())
		check(t, err)
		names := make([]string, len(children))
		for i, child := range children {
			names[i] = child.Name
		}
		result = append(result, []any{row.Amount, parent.Name, names})
	}
	return result
}

func runSinglePrefetchComposition(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
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
	base := api.OwnersRankedLink.OrderBy(owners.RankedLinkFields.Amount.Asc())
	selector := base.Prefetch.Owner.WithChildren(api.OwnersOwner.Prefetch.Ranked)
	eager := func() project.OwnersRankedLinkPrefetchQuery {
		return base.SelectRelated(base.Related.Owner).PrefetchRelated(selector)
	}
	for _, mode := range []string{"typed", "path", "prefetch_first", "prefetch_first_path", "cold_typed", "cold_path"} {
		t.Run("reference_"+mode, func(t *testing.T) {
			q := eager()
			switch mode {
			case "path":
				q, err = base.SelectRelated(base.Related.Owner).PrefetchRelatedPaths("owner__ranked")
				check(t, err)
			case "prefetch_first":
				q = base.PrefetchRelated(selector).SelectRelated(base.Related.Owner)
			case "prefetch_first_path":
				q, err = base.PrefetchRelated(selector).SelectRelatedPaths("owner")
				check(t, err)
			case "cold_typed":
				q = base.PrefetchRelated(selector)
			case "cold_path":
				q, err = base.PrefetchRelatedPaths("owner__ranked")
				check(t, err)
			}
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			batch := len(probe.plans) - before
			before = len(probe.plans)
			members := singlePrefetchGraph(t, rows)
			again, err := q.All(ctx)
			check(t, err)
			if !reflect.DeepEqual(singlePrefetchGraph(t, again), members) {
				t.Fatal("warm graph changed")
			}
			if mode == "cold_typed" || mode == "cold_path" {
				if batch != 3 {
					t.Fatalf("cold batch=%d want3", batch)
				}
				batch--
			}
			prefetchReference(t, reference.Observations, "prefetch_eager_owner", map[string]any{"batch_queries": batch, "warm_queries": len(probe.plans) - before, "members": members})
		})
	}
	t.Run("first_refinement_count", func(t *testing.T) {
		q := eager()
		before := len(probe.plans)
		n, err := q.Count(ctx)
		check(t, err)
		if n != 3 || len(probe.plans)-before != 1 {
			t.Fatal("count loaded child graph", n)
		}
		before = len(probe.plans)
		one, ok, err := q.First(ctx)
		check(t, err)
		if !ok || one.Amount != 1 || len(probe.plans)-before != 2 {
			t.Fatal("cold first did not limit graph")
		}
		before = len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		if len(rows) != 3 || len(probe.plans)-before != 2 {
			t.Fatal("first contaminated full cache")
		}
		before = len(probe.plans)
		rows, err = q.Filter(owners.RankedLinkFields.Amount.Exact(3)).All(ctx)
		check(t, err)
		if len(rows) != 1 || rows[0].Amount != 3 || len(probe.plans)-before != 2 {
			t.Fatal("derived source/eager plan lost")
		}
		before = len(probe.plans)
		singlePrefetchGraph(t, rows)
		if len(probe.plans) != before {
			t.Fatal("derived child cache lost")
		}
		limited, err := q.Fresh().Offset(1)
		check(t, err)
		limited, err = limited.Limit(1)
		check(t, err)
		rows, err = limited.All(ctx)
		check(t, err)
		if len(rows) != 1 || rows[0].Amount != 2 {
			t.Fatal("offset/limit lost")
		}
	})
	t.Run("validation", func(t *testing.T) {
		foreign, err := project.Using(probe)
		check(t, err)
		before := len(probe.plans)
		bad := base.Prefetch.Owner.WithChildren(foreign.OwnersOwner.Prefetch.Ranked)
		queries := []project.OwnersRankedLinkPrefetchQuery{base.PrefetchRelated(bad), base.SelectRelated(base.Related.Owner).PrefetchRelated(foreign.OwnersRankedLink.Prefetch.Owner), base.PrefetchRelated(selector).SelectRelated(foreign.OwnersRankedLink.Related.Owner)}
		leaves := make([]project.OwnersRankedLinkPrefetchSelector, orm.MaximumRelatedSelectionNodes)
		for i := range leaves {
			leaves[i] = base.Prefetch.Owner
		}
		queries = append(queries, base.SelectRelated(base.Related.Owner).PrefetchRelated(leaves...), base.PrefetchRelated(leaves...).SelectRelated(base.Related.Owner))
		for _, q := range queries {
			if rows, err := q.All(ctx); err == nil || rows != nil {
				t.Fatal("invalid origin/budget accepted")
			}
		}
		if _, err := base.SelectRelated(base.Related.Owner).PrefetchRelatedPaths("owner__missing"); err == nil {
			t.Fatal("invalid path accepted")
		}
		if len(probe.plans) != before {
			t.Fatal("validation performed I/O")
		}
	})
	t.Run("failure_retry", func(t *testing.T) {
		sentinel := errors.New("child failure")
		for _, cold := range []bool{false, true} {
			q := eager()
			count := 2
			if cold {
				q = base.PrefetchRelated(selector)
				count = 3
			}
			for at := 1; at <= count; at++ {
				attempt := q.Fresh()
				probe.failAt = len(probe.plans) + at
				probe.failure = sentinel
				if rows, err := attempt.All(ctx); !errors.Is(err, sentinel) || rows != nil {
					t.Fatal("partial result escaped", err)
				}
				probe.failAt = 0
				before := len(probe.plans)
				rows, err := attempt.All(ctx)
				check(t, err)
				if len(rows) != 3 || len(probe.plans)-before != count {
					t.Fatal("failed attempt cached partial graph")
				}
			}
		}
		q := eager()
		cancelCtx, cancel := context.WithCancel(ctx)
		probe.cancelAfterScan = cancel
		probe.cancelAt = len(probe.plans) + 2
		if rows, err := q.All(cancelCtx); !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal("canceled graph published", err)
		}
		probe.cancelAfterScan = nil
		probe.cancelAt = 0
		cancel()
		_, err := q.All(ctx)
		check(t, err)
	})
	t.Run("independent_and_concurrent", func(t *testing.T) {
		q := eager()
		rows, err := q.All(ctx)
		check(t, err)
		again, err := q.All(ctx)
		check(t, err)
		parent, _, err := rows[0].Owner(ctx)
		check(t, err)
		parent.Name = "caller"
		view, err := parent.Ranked()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		children, err := held.All(ctx)
		check(t, err)
		*children[0].Note = "caller"
		check(t, view.RemoveKeys(ctx, a.ID))
		before := len(probe.plans)
		sibling, _, err := rows[1].Owner(ctx)
		check(t, err)
		if sibling.Name != "first" {
			t.Fatal("duplicate parent shared mutable wrapper")
		}
		original := singlePrefetchGraph(t, again)
		if len(probe.plans) != before || len(original) != 3 {
			t.Fatal("independent materialization lost cache")
		}
		cached, err := held.All(ctx)
		check(t, err)
		if len(cached) != 2 || *cached[0].Note != "original" {
			t.Fatal("held graph mutated")
		}
		current, err := view.All(ctx)
		check(t, err)
		if len(current) != 1 || current[0].Name != "b" {
			t.Fatal("manager failed to refresh")
		}
		check(t, view.AddKeys(ctx, []int64{a.ID}, owners.RankedLinkCreate{}.WithAmount(1)))
		before = len(probe.plans)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				values, err := q.All(ctx)
				if err != nil || len(values) != 3 {
					t.Error("concurrent warm graph", err)
				}
			}()
		}
		wg.Wait()
		if len(probe.plans) != before {
			t.Fatal("warm graph performed I/O")
		}
	})
	t.Run("nullable_and_session", func(t *testing.T) {
		null, err := owners.RankedLinkObjects.Create(ctx, b, owners.NewRankedLinkCreate(4))
		check(t, err)
		nullBase := base.Filter(owners.RankedLinkFields.ID.Exact(null.ID))
		for _, q := range []project.OwnersRankedLinkPrefetchQuery{nullBase.PrefetchRelated(selector), nullBase.SelectRelated(base.Related.Owner).PrefetchRelated(selector)} {
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			if len(rows) != 1 {
				t.Fatal("null row lost")
			}
			singlePrefetchGraph(t, rows)
			if len(probe.plans)-before != 1 {
				t.Fatal("null parent performed child I/O")
			}
		}
		var captured []*project.OwnersRankedLink
		var selected project.OwnersRankedLinkPrefetchQuery
		check(t, b.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			scoped, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			source := scoped.OwnersRankedLink.OrderBy(owners.RankedLinkFields.Amount.Asc())
			selected = source.SelectRelated(source.Related.Owner).PrefetchRelated(source.Prefetch.Owner.WithChildren(scoped.OwnersOwner.Prefetch.Ranked))
			captured, err = selected.All(ctx)
			if err != nil {
				return err
			}
			singlePrefetchGraph(t, captured)
			return nil
		}))
		if rows, err := selected.All(ctx); err == nil || rows != nil {
			t.Fatal("expired prefetch cache escaped")
		}
		for _, row := range captured {
			if _, _, err := row.Owner(ctx); err == nil {
				t.Fatal("expired parent cache escaped")
			}
		}
	})
}
