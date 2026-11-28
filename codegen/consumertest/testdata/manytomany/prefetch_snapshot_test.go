package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestCollectionPrefetchSnapshots(t *testing.T) {
	withCollectionBackends(t, runCollectionPrefetchSnapshots)
}

// The backend validates an empty plan and returns synthetic rows. Those calls
// are retained in the probe but are not SQL statements in the Django oracle.
func snapshotStatementCounts(plans []query.Plan) (int, int) {
	reads, windows := 0, 0
	for _, plan := range plans {
		if !plan.EmptyResult() {
			reads++
			if _, ok := plan.PrefetchWindow(); ok {
				windows++
			}
		}
	}
	return reads, windows
}

func runCollectionPrefetchSnapshots(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	var source []owners.Owner
	for _, name := range []string{"first", "second", "empty"} {
		v, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
		check(t, err)
		source = append(source, v)
	}
	var target []labels.Label
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		v, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
		check(t, err)
		target = append(target, v)
	}
	factory, err := project.BindCollections()
	check(t, err)
	for i, values := range [][]labels.Label{target[:4], target[1:]} {
		view, err := factory.OwnersOwnerLabels.From(b, source[i])
		check(t, err)
		check(t, view.Add(ctx, values))
	}
	data, err := os.ReadFile("django-query.json")
	check(t, err)
	var reference struct {
		Observations struct {
			Slices map[string]json.RawMessage `json:"prefetch_slices"`
		} `json:"observations"`
	}
	check(t, json.Unmarshal(data, &reference))
	probe := &prefetchProbe{collectionBackend: b}
	api, err := project.Using(probe)
	check(t, err)
	base := api.OwnersOwner.Filter(owners.OwnerFields.ID.In(source[0].ID, source[1].ID, source[2].ID)).OrderBy(owners.OwnerFields.ID.Asc())
	for _, test := range []struct {
		name          string
		offset, limit int
		descending    bool
	}{
		{"head", 0, 1, false}, {"middle", 1, 2, false}, {"tail", 2, -1, false}, {"empty", 0, 0, false}, {"beyond", 9, 1, false}, {"descending", 1, 2, true}, {"empty_batch", 0, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := base.Prefetch.Labels.Snapshot("label_rows").OrderBy(labels.LabelFields.Name.Asc())
			if test.descending {
				selection = selection.OrderBy(labels.LabelFields.Name.Desc())
			}
			selection, err = selection.Offset(test.offset)
			check(t, err)
			if test.limit >= 0 {
				selection, err = selection.Limit(test.limit)
				check(t, err)
			}
			roots := base
			if test.name == "empty_batch" {
				roots = roots.Filter(owners.OwnerFields.ID.Exact(-123))
			}
			q := roots.PrefetchRelated(selection)
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			again, err := q.All(ctx)
			check(t, err)
			batch, windows := snapshotStatementCounts(probe.plans[before+1:])
			var values []*project.OwnersOwner
			if len(rows) > 0 {
				values = []*project.OwnersOwner{rows[0], rows[1], again[0], rows[2]}
			}
			members := make([][]string, 0, len(values))
			before = len(probe.plans)
			for _, owner := range values {
				got, present, err := selection.Read(ctx, owner)
				check(t, err)
				if !present {
					t.Fatal("loaded snapshot absent")
				}
				members = append(members, filteredNames(got))
			}
			warm := len(probe.plans) - before
			managers := make([][]string, 0, len(values))
			before = len(probe.plans)
			for _, owner := range values {
				view, err := owner.Labels()
				check(t, err)
				got, err := view.All(ctx)
				check(t, err)
				managers = append(managers, filteredNames(got))
			}
			prefetchReference(t, reference.Observations.Slices, test.name, map[string]any{"members": members, "batch_queries": batch, "window_queries": windows, "warm_queries": warm, "managers": managers, "manager_queries": len(probe.plans) - before})
		})
	}

	t.Run("nested_and_independent", func(t *testing.T) {
		selection := base.Prefetch.Labels.Snapshot("label_rows").OrderBy(labels.LabelFields.Name.Asc()).WithChildren(api.LabelsLabel.Prefetch.Owners)
		selection, err = selection.Offset(1)
		check(t, err)
		selection, err = selection.Limit(1)
		check(t, err)
		q := base.PrefetchRelated(selection)
		before := len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		again, err := q.All(ctx)
		check(t, err)
		batch, windows := snapshotStatementCounts(probe.plans[before+1:])
		values := []*project.OwnersOwner{rows[0], rows[1], again[0], rows[2]}
		before = len(probe.plans)
		members := make([]any, 0, len(values))
		for _, owner := range values {
			got, present, err := selection.Read(ctx, owner)
			check(t, err)
			if !present {
				t.Fatal("nested snapshot absent")
			}
			members = append(members, filteredLabelGraph(t, got))
		}
		warm := len(probe.plans) - before
		before = len(probe.plans)
		managers := make([][]string, 0, len(values))
		for _, owner := range values {
			view, err := owner.Labels()
			check(t, err)
			got, err := view.All(ctx)
			check(t, err)
			managers = append(managers, filteredNames(got))
		}
		prefetchReference(t, reference.Observations.Slices, "nested", map[string]any{"members": members, "batch_queries": batch, "window_queries": windows, "warm_queries": warm, "managers": managers, "manager_queries": len(probe.plans) - before})
		first, _, err := selection.Read(ctx, rows[0])
		check(t, err)
		first[0].Name = "changed"
		fresh, _, err := selection.Read(ctx, rows[0])
		check(t, err)
		if fresh[0].Name != "b" {
			t.Fatal("snapshot caller mutation escaped")
		}
		view, err := rows[0].Labels()
		check(t, err)
		check(t, view.AddKeys(ctx, []int64{target[4].ID}))
		got, _, err := selection.Read(ctx, rows[0])
		check(t, err)
		if !reflect.DeepEqual(filteredNames(got), []string{"b"}) {
			t.Fatal("manager mutation changed snapshot")
		}
		check(t, view.RemoveKeys(ctx, target[4].ID))
		before = len(probe.plans)
		var group sync.WaitGroup
		failures := make(chan error, 8)
		for range 8 {
			group.Go(func() {
				owners, err := q.All(ctx)
				if err != nil {
					failures <- err
					return
				}
				v, present, err := selection.Read(ctx, owners[0])
				if err != nil || !present || len(v) != 1 || v[0].Name != "b" {
					failures <- fmt.Errorf("concurrent snapshot: %v", err)
				}
			})
		}
		group.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if len(probe.plans) != before {
			t.Fatal("warm snapshot performed I/O")
		}
	})

	t.Run("reverse_eager_and_distinct", func(t *testing.T) {
		for i, owner := range source[:2] {
			for j, label := range target[:3] {
				_, err := owners.RankedLinkObjects.Create(ctx, b, owners.NewRankedLinkCreate(int64(i*3+j+1)).WithOwnerID(owner.ID).WithLabelID(label.ID))
				check(t, err)
			}
		}
		selection := base.Prefetch.RankedLinkRows.Snapshot("link_rows").OrderBy(owners.RankedLinkFields.Amount.Desc()).SelectRelated(api.OwnersRankedLink.Related.Label)
		selection, err = selection.Offset(1)
		check(t, err)
		selection, err = selection.Limit(1)
		check(t, err)
		before := len(probe.plans)
		rows, err := base.Filter(owners.OwnerFields.ID.In(source[0].ID, source[1].ID)).PrefetchRelated(selection).All(ctx)
		check(t, err)
		batch, _ := snapshotStatementCounts(probe.plans[before+1:])
		before = len(probe.plans)
		members := make([]any, 0, 2)
		for _, owner := range rows {
			links, present, err := selection.Read(ctx, owner)
			check(t, err)
			if !present {
				t.Fatal("reverse snapshot absent")
			}
			values := make([]any, 0, len(links))
			for _, link := range links {
				label, present, err := link.Label(ctx)
				check(t, err)
				if !present {
					t.Fatal("eager label absent")
				}
				values = append(values, []any{link.Amount, label.Name})
			}
			members = append(members, values)
		}
		prefetchReference(t, reference.Observations.Slices, "reverse_eager", map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
		for i, label := range []labels.Label{target[0], target[0], target[1]} {
			_, err := owners.LooseLinkObjects.Create(ctx, b, owners.NewLooseLinkCreate(int64(i)).WithOwnerID(source[0].ID).WithLabelID(label.ID))
			check(t, err)
		}
		loose := base.Prefetch.Loose.Snapshot("duplicate_rows").Filter(labels.LabelFields.Name.In("a", "b")).OrderBy(labels.LabelFields.Name.Asc()).Distinct()
		loose, err = loose.Limit(2)
		check(t, err)
		before = len(probe.plans)
		rows, err = base.Filter(owners.OwnerFields.ID.Exact(source[0].ID)).PrefetchRelated(loose).All(ctx)
		check(t, err)
		batch, _ = snapshotStatementCounts(probe.plans[before+1:])
		got, present, err := loose.Read(ctx, rows[0])
		check(t, err)
		if !present {
			t.Fatal("duplicate snapshot absent")
		}
		prefetchReference(t, reference.Observations.Slices, "distinct_duplicates", map[string]any{"members": filteredNames(got), "batch_queries": batch})
	})

	t.Run("named_children_and_multiple_snapshots", func(t *testing.T) {
		parents := api.LabelsLabel.Prefetch.Owners.Snapshot("parent_rows")
		head := base.Prefetch.Labels.Snapshot("head").OrderBy(labels.LabelFields.Name.Asc()).WithChildren(parents)
		head, err = head.Limit(1)
		check(t, err)
		tail := base.Prefetch.Labels.Snapshot("tail").OrderBy(labels.LabelFields.Name.Desc())
		tail, err = tail.Limit(1)
		check(t, err)
		rows, err := base.PrefetchRelated(head, tail).All(ctx)
		check(t, err)
		before := len(probe.plans)
		first, present, err := head.Read(ctx, rows[0])
		check(t, err)
		if !present || !reflect.DeepEqual(filteredNames(first), []string{"a"}) {
			t.Fatal("head alias lost")
		}
		last, present, err := tail.Read(ctx, rows[0])
		check(t, err)
		if !present || !reflect.DeepEqual(filteredNames(last), []string{"d"}) {
			t.Fatal("tail alias overwritten")
		}
		parent, present, err := parents.Read(ctx, first[0])
		check(t, err)
		if !present || len(parent) != 1 || parent[0].Name != "first" {
			t.Fatal("named child graph lost")
		}
		if len(probe.plans) != before {
			t.Fatal("nested snapshot read performed I/O")
		}
		eager := api.OwnersRankedLink.SelectRelated(api.OwnersRankedLink.Related.Owner).PrefetchRelated(api.OwnersRankedLink.Prefetch.Owner.WithChildren(head))
		links, err := eager.All(ctx)
		check(t, err)
		before = len(probe.plans)
		for _, link := range links {
			owner, present, err := link.Owner(ctx)
			check(t, err)
			if !present {
				t.Fatal("eager owner missing")
			}
			values, present, err := head.Read(ctx, owner)
			check(t, err)
			if !present || len(values) != 1 {
				t.Fatal("eager parent snapshot lost")
			}
			if _, present, err := parents.Read(ctx, values[0]); err != nil || !present {
				t.Fatal("eager nested snapshot lost", err)
			}
		}
		if len(probe.plans) != before {
			t.Fatal("eager snapshot graph performed extra I/O")
		}
	})

	t.Run("validation_and_namespace", func(t *testing.T) {
		plain, err := base.All(ctx)
		check(t, err)
		before := len(probe.plans)
		for _, name := range []string{"", "id", "Name", "labels", "ranked_link_rows", "labels__owners", "a-b"} {
			if _, err := base.PrefetchRelated(base.Prefetch.Labels.Snapshot(name)).Count(ctx); err == nil {
				t.Fatal("invalid snapshot name accepted", name)
			}
		}
		unspecified, err := base.Prefetch.Labels.Limit(1)
		check(t, err)
		if _, err := base.PrefetchRelated(unspecified).All(ctx); err == nil {
			t.Fatal("sliced manager accepted")
		}
		reverse, err := base.Prefetch.RankedLinkRows.Limit(1)
		check(t, err)
		if _, err := base.PrefetchRelated(reverse).Count(ctx); err == nil {
			t.Fatal("sliced reverse manager accepted")
		}
		selection := base.Prefetch.Labels.Snapshot("chosen")
		if _, err := base.PrefetchRelated(selection, selection).Count(ctx); err == nil {
			t.Fatal("snapshot redefinition accepted")
		}
		if _, err := base.PrefetchRelated(selection, base.Prefetch.RankedLinkRows.Snapshot("chosen")).Count(ctx); err == nil {
			t.Fatal("cross-type alias collision accepted")
		}
		if _, err := selection.Limit(-1); err == nil {
			t.Fatal("negative slice accepted")
		}
		if values, present, err := selection.Read(ctx, plain[0]); err != nil || present || values != nil {
			t.Fatal("unloaded snapshot did not report absence", err)
		}
		if _, _, err := base.Prefetch.Labels.Read(ctx, plain[0]); err == nil {
			t.Fatal("unnamed snapshot read accepted")
		}
		if _, _, err := selection.Read(nil, plain[0]); err == nil {
			t.Fatal("nil snapshot context accepted")
		}
		if len(probe.plans) != before {
			t.Fatal("invalid or absent snapshot performed I/O")
		}
		rows, err := base.PrefetchRelated(selection, base.Prefetch.Labels).All(ctx)
		check(t, err)
		before = len(probe.plans)
		view, err := rows[0].Labels()
		check(t, err)
		_, err = view.All(ctx)
		check(t, err)
		if len(probe.plans) != before {
			t.Fatal("normal manager selection was lost")
		}
		copy := *rows[0]
		if _, _, err := selection.Read(ctx, &copy); err == nil {
			t.Fatal("copied owner accepted")
		}
		rows[0].ID++
		if _, _, err := selection.Read(ctx, rows[0]); err == nil {
			t.Fatal("changed owner identity accepted")
		}
		rows[0].ID--
		foreign, err := project.Using(probe)
		check(t, err)
		if _, _, err := foreign.OwnersOwner.Prefetch.Labels.Snapshot("chosen").Read(ctx, rows[0]); err == nil {
			t.Fatal("foreign facade snapshot accepted")
		}
		if values, present, err := base.Prefetch.Labels.Snapshot("absent").Read(ctx, rows[0]); err != nil || present || values != nil {
			t.Fatal("unselected alias read")
		}
		if _, _, err := base.Prefetch.Ranked.Snapshot("chosen").Read(ctx, rows[0]); err == nil {
			t.Fatal("snapshot read through another relation")
		}
		if len(probe.plans) != before {
			t.Fatal("snapshot validation performed I/O")
		}
		zero, err := base.Prefetch.Labels.Offset(0)
		check(t, err)
		zeroReverse, err := base.Prefetch.RankedLinkRows.Offset(0)
		check(t, err)
		before = len(probe.plans)
		rows, err = base.PrefetchRelated(zero, zeroReverse).All(ctx)
		check(t, err)
		if len(probe.plans)-before != 3 {
			t.Fatal("zero offset changed collection batch count")
		}
		for _, plan := range probe.plans[before:] {
			if _, window := plan.PrefetchWindow(); window {
				t.Fatal("zero offset introduced a window")
			}
		}
		before = len(probe.plans)
		view, err = rows[0].Labels()
		check(t, err)
		got, err := view.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(filteredNames(got), []string{"a", "b", "c", "d"}) {
			t.Fatal("zero offset changed manager membership")
		}
		reverseView, err := rows[0].RankedLinkRows()
		check(t, err)
		links, err := reverseView.All(ctx)
		check(t, err)
		if len(links) != 3 || len(probe.plans) != before {
			t.Fatal("zero offset did not retain ordinary manager caches")
		}
	})

	t.Run("failure_cancel_retry_and_native", func(t *testing.T) {
		selection := base.Prefetch.Labels.Snapshot("small").WithChildren(api.LabelsLabel.Prefetch.Owners).OrderBy(labels.LabelFields.Name.Asc())
		selection, err = selection.Limit(1)
		check(t, err)
		q := base.PrefetchRelated(selection)
		failure := errors.New("snapshot child failure")
		probe.failAt = len(probe.plans) + 3
		probe.failure = failure
		if rows, err := q.All(ctx); rows != nil || !errors.Is(err, failure) {
			t.Fatal("partial snapshot escaped", err)
		}
		probe.failAt = 0
		rows, err := q.All(ctx)
		check(t, err)
		before := len(probe.plans)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if values, present, err := selection.Read(canceled, rows[0]); values != nil || present || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled warm snapshot escaped", err)
		}
		if len(probe.plans) != before {
			t.Fatal("canceled read performed I/O")
		}
		probe.corrupt, probe.corruptAt = "foreign_owner", len(probe.plans)+2
		if rows, err := q.Fresh().All(ctx); rows != nil || err == nil {
			t.Fatal("foreign snapshot owner escaped")
		}
		probe.corrupt = ""
		cancelCtx, cancel := context.WithCancel(ctx)
		probe.cancelAfterScan, probe.cancelAt = cancel, len(probe.plans)+2
		if rows, err := q.Fresh().All(cancelCtx); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled snapshot published owner", err)
		}
		probe.cancelAfterScan = nil
		native := factory.OwnersOwnerLabels.WithChildren(factory.LabelsLabelOwners).Snapshot("native").OrderBy(labels.LabelFields.Name.Asc())
		native, err = native.Limit(1)
		check(t, err)
		raw, err := orm.PrefetchRelated(owners.OwnerObjects.Using(probe).Filter(owners.OwnerFields.ID.Exact(source[0].ID)), native).All(ctx)
		check(t, err)
		before = len(probe.plans)
		values, present, err := native.Read(ctx, raw[0])
		check(t, err)
		if !present || len(values) != 1 {
			t.Fatal("native snapshot absent")
		}
		if _, present, err := factory.OwnersOwnerLabels.FromPrefetched(raw[0]); err != nil || present {
			t.Fatal("snapshot installed a normal manager", err)
		}
		if _, present, err := factory.LabelsLabelOwners.FromPrefetched(values[0]); err != nil || !present {
			t.Fatal("native snapshot child absent", err)
		}
		if len(probe.plans) != before {
			t.Fatal("native read performed I/O")
		}
	})

	t.Run("session_lifetime", func(t *testing.T) {
		var readAfter func() error
		check(t, b.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			scoped, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			selection := scoped.OwnersOwner.Prefetch.Labels.Snapshot("session_rows").OrderBy(labels.LabelFields.Name.Asc())
			selection, err = selection.Limit(1)
			if err != nil {
				return err
			}
			rows, err := scoped.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(source[0].ID)).PrefetchRelated(selection).All(ctx)
			if err != nil {
				return err
			}
			readAfter = func() error {
				v, present, err := selection.Read(ctx, rows[0])
				if err != nil && (v != nil || present) {
					return errors.New("failed read exposed provisional snapshot")
				}
				return err
			}
			return readAfter()
		}))
		if err := readAfter(); err == nil {
			t.Fatal("snapshot escaped session")
		}
	})

	t.Run("owner_universe_across_batches", func(t *testing.T) {
		var last owners.Owner
		check(t, b.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			for i := 0; i < 1000; i++ {
				name := fmt.Sprintf("filler-%d", i)
				if i == 999 {
					name = "batch-second"
				}
				v, err := owners.OwnerObjects.Create(ctx, session, owners.NewOwnerCreate(name))
				if err != nil {
					return err
				}
				last = v
			}
			return nil
		}))
		outside, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("batch-outside"))
		check(t, err)
		a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a"))
		check(t, err)
		bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
		check(t, err)
		for _, pair := range []struct {
			owner  owners.Owner
			values []labels.Label
		}{{source[0], []labels.Label{bb}}, {outside, []labels.Label{a}}, {last, []labels.Label{a, bb}}} {
			view, err := factory.OwnersOwnerLabels.From(b, pair.owner)
			check(t, err)
			check(t, view.Add(ctx, pair.values))
		}
		relations, err := project.BindRelations()
		check(t, err)
		roots := api.OwnersOwner.Filter(owners.OwnerFields.ID.LessThanOrEqual(last.ID)).OrderBy(owners.OwnerFields.ID.Asc())
		custom := roots.Prefetch.Labels.Filter(relations.LabelsLabel.Owners.Name.In("first", "batch-outside")).Filter(relations.LabelsLabel.Owners.Name.Exact("batch-second")).OrderBy(labels.LabelFields.Name.Asc())
		before := len(probe.plans)
		rows, err := roots.PrefetchRelated(custom).All(ctx)
		check(t, err)
		view, err := rows[0].Labels()
		check(t, err)
		got, err := view.All(ctx)
		check(t, err)
		if len(rows) != 1003 || !reflect.DeepEqual(filteredNames(got), []string{"b"}) || len(probe.plans)-before != 2 {
			t.Fatal("custom membership universe was split", len(rows), filteredNames(got), len(probe.plans)-before)
		}
		for offset := 0; offset < 2; offset++ {
			selected, err := custom.Snapshot("scope").Offset(offset)
			check(t, err)
			selected, err = selected.Limit(1)
			check(t, err)
			before = len(probe.plans)
			rows, err := roots.PrefetchRelated(selected).All(ctx)
			check(t, err)
			values, present, err := selected.Read(ctx, rows[0])
			check(t, err)
			want := []string{}
			if offset == 1 {
				want = []string{"b"}
			}
			if !present || !reflect.DeepEqual(filteredNames(values), want) || len(probe.plans)-before != 2 {
				t.Fatal("window membership universe was split", filteredNames(values), want)
			}
			values, present, err = selected.Read(ctx, rows[len(rows)-1])
			check(t, err)
			if !present || len(values) != 0 {
				t.Fatal("grouping used the partition owner")
			}
		}
	})
}
