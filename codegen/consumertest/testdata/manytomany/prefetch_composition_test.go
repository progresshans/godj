package consumer

import (
	"errors"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
)

func TestFilteredPrefetchComposition(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
		t.Cleanup(func() { check(t, b.Close()) })
		migrateCollections(t, b)
		ctx := t.Context()
		first, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
		check(t, err)
		highlight, err := labels.FeaturedOwnerObjects.Create(ctx, b, labels.NewFeaturedOwnerCreate("highlight"))
		check(t, err)
		a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a").WithFeaturedOwnerID(highlight.ID))
		check(t, err)
		bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
		check(t, err)
		factory, err := project.BindCollections()
		check(t, err)
		manager, err := factory.OwnersOwnerLabels.From(b, first)
		check(t, err)
		check(t, manager.Add(ctx, []labels.Label{a, bb}))
		probe := &prefetchProbe{collectionBackend: b}
		api, err := project.Using(probe)
		check(t, err)
		getHeld := func(api project.Models) (project.LabelsLabelQuery, error) {
			rows, err := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID)).PrefetchRelated(api.OwnersOwner.Prefetch.Labels.Filter(labels.LabelFields.Name.In("a", "b")).WithChildren(api.LabelsLabel.Prefetch.Owners)).All(ctx)
			if err != nil {
				return project.LabelsLabelQuery{}, err
			}
			view, err := rows[0].Labels()
			if err != nil {
				return project.LabelsLabelQuery{}, err
			}
			return view.Query()
		}
		verify := func(rows []*project.LabelsLabel) {
			for _, row := range rows {
				featured, present, err := row.FeaturedOwner(ctx)
				check(t, err)
				if row.Name == "a" {
					if !present || featured.ID != highlight.ID {
						t.Fatal("selected required value missing")
					}
				} else if present || featured != nil {
					t.Fatal("selected nullable absence changed")
				}
				view, err := row.Owners()
				check(t, err)
				parents, err := view.All(ctx)
				check(t, err)
				if len(parents) != 1 || parents[0].ID != first.ID {
					t.Fatal("inherited target prefetch changed")
				}
			}
		}
		t.Run("single_child", func(t *testing.T) {
			base := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID))
			typed := base.PrefetchRelated(base.Prefetch.Labels.WithChildren(api.LabelsLabel.Prefetch.FeaturedOwner, api.LabelsLabel.Prefetch.Owners))
			paths, err := base.PrefetchRelatedPaths("labels__featured_owner", "labels__owners")
			check(t, err)
			for _, q := range []project.OwnersOwnerPrefetchQuery{typed, paths} {
				before := len(probe.plans)
				rows, err := q.All(ctx)
				check(t, err)
				if len(rows) != 1 || len(probe.plans)-before != 4 {
					t.Fatal("mixed collection/single tree was not batched")
				}
				before = len(probe.plans)
				view, err := rows[0].Labels()
				check(t, err)
				children, err := view.All(ctx)
				check(t, err)
				verify(children)
				if len(probe.plans) != before {
					t.Fatal("mixed child graph lost")
				}
			}
			configured := base.PrefetchRelated(base.Prefetch.Labels.Filter(labels.LabelFields.Name.In("a", "b")).WithChildren(api.LabelsLabel.Prefetch.FeaturedOwner))
			rows, err := configured.All(ctx)
			check(t, err)
			view, err := rows[0].Labels()
			check(t, err)
			held, err := view.Query()
			check(t, err)
			before := len(probe.plans)
			refined, err := held.OrderBy(labels.LabelFields.Name.Asc()).All(ctx)
			check(t, err)
			if len(refined) != 2 || len(probe.plans)-before != 2 {
				t.Fatal("configured single child did not survive refinement")
			}
			before = len(probe.plans)
			featured, found, err := refined[0].FeaturedOwner(ctx)
			check(t, err)
			if !found || featured.ID != highlight.ID || len(probe.plans) != before {
				t.Fatal("configured child cache missing")
			}
		})
		t.Run("many_target_eager", func(t *testing.T) {
			base := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(first.ID))
			selector := base.Prefetch.Labels.OrderBy(labels.LabelFields.Name.Asc()).SelectRelated(api.LabelsLabel.Related.FeaturedOwner).WithChildren(api.LabelsLabel.Prefetch.Owners)
			before := len(probe.plans)
			rows, err := base.PrefetchRelated(selector).All(ctx)
			check(t, err)
			view, err := rows[0].Labels()
			check(t, err)
			held, err := view.Query()
			check(t, err)
			children, err := held.All(ctx)
			check(t, err)
			verify(children)
			if len(probe.plans)-before != 3 {
				t.Fatal("many target eager and child not batched")
			}
			before = len(probe.plans)
			children, err = held.Fresh().OrderBy(labels.LabelFields.Name.Asc()).All(ctx)
			check(t, err)
			verify(children)
			if len(probe.plans)-before != 2 {
				t.Fatal("many target eager configuration lost on refinement")
			}
			before = len(probe.plans)
			one, found, err := held.Fresh().First(ctx)
			check(t, err)
			if !found {
				t.Fatal("configured first absent")
			}
			verify([]*project.LabelsLabel{one})
			if len(probe.plans)-before != 2 {
				t.Fatal("many target eager cold First")
			}
			before = len(probe.plans)
			combined, err := held.PrefetchRelated(held.Prefetch.RankedOwners).All(ctx)
			check(t, err)
			verify(combined)
			if len(probe.plans)-before != 3 {
				t.Fatal("additional prefetch dropped target eager")
			}
		})
		t.Run("eager_and_children", func(t *testing.T) {
			held, err := getHeld(api)
			check(t, err)
			eager := held.OrderBy(labels.LabelFields.Name.Asc()).SelectRelated(held.Related.FeaturedOwner)
			before := len(probe.plans)
			row, present, err := eager.First(ctx)
			check(t, err)
			if !present {
				t.Fatal("cold First missing")
			}
			verify([]*project.LabelsLabel{row})
			if len(probe.plans)-before != 2 {
				t.Fatal("cold eager First lost prefetch or selected relation")
			}
			before = len(probe.plans)
			rows, err := eager.All(ctx)
			check(t, err)
			if len(rows) != 2 {
				t.Fatal("cold First populated full eager cache")
			}
			verify(rows)
			if len(probe.plans)-before != 2 {
				t.Fatal("eager source and child were not batched")
			}
			before = len(probe.plans)
			again, err := eager.All(ctx)
			check(t, err)
			verify(again)
			row, present, err = eager.First(ctx)
			check(t, err)
			if !present {
				t.Fatal("warm First missing")
			}
			verify([]*project.LabelsLabel{row})
			if len(probe.plans) != before {
				t.Fatal("warm composed result performed I/O")
			}
			before = len(probe.plans)
			count, err := eager.Fresh().Count(ctx)
			check(t, err)
			if count != 2 || len(probe.plans)-before != 1 {
				t.Fatal("cold composed count loaded relations")
			}
			before = len(probe.plans)
			extra, err := held.PrefetchRelated(held.Prefetch.RankedOwners).All(ctx)
			check(t, err)
			for _, row := range extra {
				view, err := row.Owners()
				check(t, err)
				_, err = view.All(ctx)
				check(t, err)
				other, err := row.RankedOwners()
				check(t, err)
				_, err = other.All(ctx)
				check(t, err)
			}
			if len(probe.plans)-before != 3 {
				t.Fatal("additional generated prefetch discarded inherited configuration")
			}
		})
		t.Run("failure_retry", func(t *testing.T) {
			held, err := getHeld(api)
			check(t, err)
			eager := held.SelectRelated(held.Related.FeaturedOwner)
			failure := errors.New("eager child prefetch failure")
			probe.failure = failure
			probe.failAt = len(probe.plans) + 2
			if rows, err := eager.All(ctx); rows != nil || !errors.Is(err, failure) {
				t.Fatal("partial eager graph published", err)
			}
			probe.failAt = 0
			before := len(probe.plans)
			rows, err := eager.All(ctx)
			check(t, err)
			verify(rows)
			if len(probe.plans)-before != 2 {
				t.Fatal("eager retry reused partial source or children")
			}
		})
		t.Run("session", func(t *testing.T) {
			var eager project.LabelsLabelEagerQuery
			check(t, b.AtomicRelation(ctx, func(session db.RelationSession) error {
				api, err := project.UsingSession(session)
				if err != nil {
					return err
				}
				held, err := getHeld(api)
				if err != nil {
					return err
				}
				eager = held.SelectRelated(held.Related.FeaturedOwner)
				rows, err := eager.All(ctx)
				if err != nil {
					return err
				}
				verify(rows)
				return nil
			}))
			if rows, err := eager.All(ctx); rows != nil || err == nil {
				t.Fatal("composed eager graph escaped session")
			}
		})
	})
}
