package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestCollectionPrefetchFiltered(t *testing.T) {
	withCollectionBackends(t, runCollectionPrefetchFiltered)
}

func filteredLabelGraph(t *testing.T, values []*project.LabelsLabel) []any {
	t.Helper()
	result := make([]any, 0, len(values))
	for _, label := range values {
		view, err := label.Owners()
		check(t, err)
		parents, err := view.All(t.Context())
		check(t, err)
		names := make([]string, len(parents))
		for i, parent := range parents {
			names[i] = parent.Name
		}
		result = append(result, []any{label.Name, names})
	}
	return result
}
func filteredNames(values []*project.LabelsLabel) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = v.Name
	}
	return result
}

func runCollectionPrefetchFiltered(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	factory, err := project.BindCollections()
	check(t, err)
	var sources []owners.Owner
	for _, name := range []string{"first", "second", "empty"} {
		v, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
		check(t, err)
		sources = append(sources, v)
	}
	var targets []labels.Label
	for _, name := range []string{"a", "b", "c"} {
		v, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
		check(t, err)
		targets = append(targets, v)
	}
	for i, values := range [][]labels.Label{targets, targets[1:2]} {
		view, err := factory.OwnersOwnerLabels.From(b, sources[i])
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
	selection := base.Prefetch.Labels.Filter(labels.LabelFields.Name.In("a", "c")).OrderBy(labels.LabelFields.Name.Desc()).WithChildren(api.LabelsLabel.Prefetch.Owners)

	t.Run("reference_and_cache", func(t *testing.T) {
		before := len(probe.plans)
		q := base.PrefetchRelated(selection)
		rows, err := q.All(ctx)
		check(t, err)
		batch := len(probe.plans) - before - 1
		before = len(probe.plans)
		again, err := q.All(ctx)
		check(t, err)
		members := make([]any, 0, 4)
		for _, row := range []*project.OwnersOwner{rows[0], rows[1], again[0], rows[2]} {
			view, err := row.Labels()
			check(t, err)
			values, err := view.All(ctx)
			check(t, err)
			members = append(members, filteredLabelGraph(t, values))
		}
		warm := len(probe.plans) - before
		view, err := rows[0].Labels()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		before = len(probe.plans)
		refined, err := held.Filter(labels.LabelFields.Name.Exact("a")).All(ctx)
		check(t, err)
		graph := filteredLabelGraph(t, refined)
		prefetchReference(t, reference.Observations, "prefetch_filtered", map[string]any{"members": members, "batch_queries": batch, "warm_queries": warm, "refined": graph, "refined_queries": len(probe.plans) - before})
		check(t, view.AddKeys(ctx, []int64{targets[1].ID}))
		before = len(probe.plans)
		current, err := view.All(ctx)
		check(t, err)
		prior, err := held.All(ctx)
		check(t, err)
		duplicateView, err := again[0].Labels()
		check(t, err)
		duplicate, err := duplicateView.All(ctx)
		check(t, err)
		prefetchReference(t, reference.Observations, "prefetch_filtered_cache", map[string]any{"after_noop_add": filteredNames(current), "held": filteredNames(prior), "duplicate": filteredNames(duplicate), "queries": len(probe.plans) - before})
	})

	t.Run("lookup_order", func(t *testing.T) {
		foreign, err := project.Using(probe)
		check(t, err)
		foreignPath, err := foreign.OwnersOwner.PrefetchPath("labels__owners")
		check(t, err)
		before := len(probe.plans)
		if _, err := base.PrefetchRelated(foreignPath).All(ctx); err == nil {
			t.Fatal("foreign path selector accepted")
		}
		var invalid orm.Predicate[labels.Label]
		if _, err := base.PrefetchRelated(base.Prefetch.Labels.Filter(invalid)).Count(ctx); err == nil {
			t.Fatal("invalid target predicate accepted")
		}
		if len(probe.plans) != before {
			t.Fatal("invalid custom selection performed I/O")
		}
		path, err := base.PrefetchPath("labels__owners")
		check(t, err)
		filtered := base.Prefetch.Labels.Filter(labels.LabelFields.Name.Exact("a"))
		before = len(probe.plans)
		_, err = base.PrefetchRelated(path, filtered).All(ctx)
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || len(probe.plans) != before {
			t.Fatal("late target query redefinition was accepted", err)
		}
		if _, err := base.PrefetchRelated(filtered, filtered).Count(ctx); err == nil {
			t.Fatal("repeated custom query was accepted")
		}
		rows, err := base.Filter(owners.OwnerFields.ID.Exact(sources[0].ID)).PrefetchRelated(filtered, path).All(ctx)
		check(t, err)
		view, err := rows[0].Labels()
		check(t, err)
		values, err := view.All(ctx)
		check(t, err)
		graph := filteredLabelGraph(t, values)
		prefetchReference(t, reference.Observations, "prefetch_order_conflicts", map[string]any{"redefined": "ValueError", "filtered_first": graph, "queries": len(probe.plans) - before})
		// The path appended to a custom lookup is an evaluation request; it
		// must not become a retained child option on the held custom queryset.
		plainCustom := base.Prefetch.Labels.Filter(labels.LabelFields.Name.In("a", "c")).OrderBy(labels.LabelFields.Name.Desc())
		rows, err = base.PrefetchRelated(plainCustom, path).All(ctx)
		check(t, err)
		view, err = rows[0].Labels()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		before = len(probe.plans)
		values, err = held.Filter().All(ctx)
		check(t, err)
		filteredLabelGraph(t, values)
		if len(probe.plans)-before != 3 {
			t.Fatal("implicit child path survived refinement")
		}
	})

	t.Run("refinement_first_and_fresh", func(t *testing.T) {
		rows, err := base.PrefetchRelated(selection).All(ctx)
		check(t, err)
		view, err := rows[0].Labels()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		ordered := held.OrderBy(labels.LabelFields.Name.Asc())
		before := len(probe.plans)
		first, ok, err := ordered.First(ctx)
		check(t, err)
		if !ok || first.Name != "a" {
			t.Fatal("custom target First")
		}
		filteredLabelGraph(t, []*project.LabelsLabel{first})
		if len(probe.plans)-before != 2 {
			t.Fatal("cold First lost target prefetch")
		}
		before = len(probe.plans)
		values, err := ordered.All(ctx)
		check(t, err)
		filteredLabelGraph(t, values)
		if len(probe.plans)-before != 2 || len(values) != 2 {
			t.Fatal("cold First populated full custom cache")
		}
		before = len(probe.plans)
		first, ok, err = ordered.First(ctx)
		check(t, err)
		if !ok {
			t.Fatal("warm First absent")
		}
		filteredLabelGraph(t, []*project.LabelsLabel{first})
		if len(probe.plans) != before {
			t.Fatal("warm First lost graph")
		}
		before = len(probe.plans)
		values, err = held.Fresh().All(ctx)
		check(t, err)
		filteredLabelGraph(t, values)
		if len(probe.plans)-before != 2 || !reflect.DeepEqual(filteredNames(values), []string{"c", "a"}) {
			t.Fatal("query Fresh lost custom configuration")
		}
		fresh, err := view.Fresh()
		check(t, err)
		before = len(probe.plans)
		values, err = fresh.All(ctx)
		check(t, err)
		if len(probe.plans)-before != 1 || len(values) != 3 {
			t.Fatal("manager Fresh retained custom query")
		}
		before = len(probe.plans)
		count, err := held.Filter().Count(ctx)
		check(t, err)
		if count != 2 || len(probe.plans)-before != 1 {
			t.Fatal("cold count loaded children or lost filter")
		}
	})

	t.Run("failure_membership_and_retry", func(t *testing.T) {
		q := base.PrefetchRelated(selection)
		failure := errors.New("configured child failed")
		probe.failure = failure
		probe.failAt = len(probe.plans) + 3
		if values, err := q.All(ctx); values != nil || !errors.Is(err, failure) {
			t.Fatal("partial filtered graph published", err)
		}
		probe.failAt = 0
		before := len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		if len(probe.plans)-before != 3 {
			t.Fatal("retry reused partial target rows")
		}
		view, err := rows[0].Labels()
		check(t, err)
		held, err := view.Query()
		check(t, err)
		derived := held.Filter()
		probe.failAt = len(probe.plans) + 2
		if values, err := derived.All(ctx); values != nil || !errors.Is(err, failure) {
			t.Fatal("refined target cached partial graph", err)
		}
		probe.failAt = 0
		before = len(probe.plans)
		values, err := derived.All(ctx)
		check(t, err)
		filteredLabelGraph(t, values)
		if len(probe.plans)-before != 2 {
			t.Fatal("refined target retry skipped child reload")
		}
		probe.corrupt = "foreign_owner"
		if values, err := q.Fresh().All(ctx); values != nil || err == nil {
			t.Fatal("foreign owner projection published")
		}
		probe.corrupt = ""
		interrupted, cancel := context.WithCancel(ctx)
		probe.cancelAfterScan = cancel
		probe.cancelAt = len(probe.plans) + 2
		if values, err := q.Fresh().All(interrupted); values != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled custom target published", err)
		}
		cancel()
		probe.cancelAfterScan = nil
		probe.cancelAt = 0
	})

	t.Run("nullable_duplicates_and_distinct", func(t *testing.T) {
		for i, pair := range []struct{ owner, label *int64 }{{&sources[0].ID, &targets[0].ID}, {&sources[0].ID, &targets[0].ID}, {&sources[0].ID, &targets[1].ID}, {&sources[0].ID, nil}, {nil, &targets[0].ID}} {
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
		for _, distinct := range []bool{false, true} {
			selection := base.Prefetch.Loose.Filter(labels.LabelFields.Name.In("a", "b")).OrderBy(labels.LabelFields.Name.Asc())
			if distinct {
				selection = selection.Distinct()
			}
			rows, err := base.PrefetchRelated(selection).All(ctx)
			check(t, err)
			view, err := rows[0].Loose()
			check(t, err)
			values, err := view.All(ctx)
			check(t, err)
			want := []string{"a", "a", "b"}
			if distinct {
				want = []string{"a", "b"}
			}
			if !reflect.DeepEqual(filteredNames(values), want) {
				t.Fatal("custom query changed through multiplicity", filteredNames(values))
			}
		}
	})

	t.Run("session_lifetime", func(t *testing.T) {
		var held project.LabelsLabelQuery
		check(t, b.AtomicRelation(ctx, func(session db.RelationSession) error {
			api, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			rows, err := api.OwnersOwner.OrderBy(owners.OwnerFields.ID.Asc()).PrefetchRelated(api.OwnersOwner.Prefetch.Labels.Filter(labels.LabelFields.Name.Exact("a")).WithChildren(api.LabelsLabel.Prefetch.Owners)).All(ctx)
			if err != nil {
				return err
			}
			view, err := rows[0].Labels()
			if err != nil {
				return err
			}
			held, err = view.Query()
			if err != nil {
				return err
			}
			values, err := held.Filter().All(ctx)
			if err != nil {
				return err
			}
			filteredLabelGraph(t, values)
			return nil
		}))
		if values, err := held.All(ctx); values != nil || err == nil {
			t.Fatal("custom prefetch query escaped session")
		}
	})

	t.Run("native_terminals", func(t *testing.T) {
		q := orm.PrefetchRelated(owners.OwnerObjects.Using(probe).Filter(owners.OwnerFields.ID.Exact(sources[0].ID)), factory.OwnersOwnerLabels.WithChildren(factory.LabelsLabelOwners).Filter(labels.LabelFields.Name.In("a", "c")).OrderBy(labels.LabelFields.Name.Asc()))
		rows, err := q.All(ctx)
		check(t, err)
		view, ok, err := factory.OwnersOwnerLabels.FromPrefetched(rows[0])
		check(t, err)
		if !ok {
			t.Fatal("native filtered cache missing")
		}
		held, err := view.Query()
		check(t, err)
		before := len(probe.plans)
		value, ok, err := held.Filter().At(ctx, 1)
		check(t, err)
		if !ok || value.Name != "c" || len(probe.plans)-before != 2 {
			t.Fatal("native indexed query lost configured children")
		}
		before = len(probe.plans)
		if err := held.Iterate(ctx, func(labels.Label) error { return nil }); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || len(probe.plans) != before {
			t.Fatal("streaming silently ignored configured prefetch", err)
		}
		before = len(probe.plans)
		composed, err := orm.PrefetchRelated(held.Filter(), factory.LabelsLabelRankedOwners).All(ctx)
		check(t, err)
		for _, row := range composed {
			if _, ok, err := factory.LabelsLabelOwners.FromPrefetched(row); err != nil || !ok {
				t.Fatal("additional prefetch discarded inherited child", err)
			}
			if _, ok, err := factory.LabelsLabelRankedOwners.FromPrefetched(row); err != nil || !ok {
				t.Fatal("additional prefetch was not loaded", err)
			}
		}
		if len(probe.plans)-before != 3 {
			t.Fatal("composed prefetch did not batch each selection")
		}
		foreign, err := project.BindCollections()
		check(t, err)
		before = len(probe.plans)
		if _, err := orm.PrefetchRelated(held, foreign.LabelsLabelOwners).All(ctx); err == nil || len(probe.plans) != before {
			t.Fatal("composed source accepted a foreign project binding", err)
		}
		children := make([]orm.PrefetchSelection[labels.Label], orm.MaximumRelatedSelectionNodes)
		for i := range children {
			children[i] = factory.LabelsLabelOwners
		}
		if _, err := orm.PrefetchRelated(held, children...).Count(ctx); err == nil || len(probe.plans) != before {
			t.Fatal("composed prefetch did not account for inherited node budget", err)
		}
	})
}
