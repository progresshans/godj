package consumer

import (
	"context"
	"database/sql"
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
	"github.com/progresshans/godj/schema/ir"
)

type singleCorruptBackend struct {
	collectionBackend
	table  string
	mode   string
	closed bool
}

func (b *singleCorruptBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := b.collectionBackend.Query(ctx, plan)
	if err != nil || plan.Table() != b.table {
		return rows, err
	}
	index := -1
	conditions := plan.Conditions()
	for i, field := range plan.SourceFields() {
		if b.mode == "different_target" && field.Name() == "id" || b.mode == "foreign" && len(conditions) > 0 && field.Equal(conditions[len(conditions)-1].Field()) {
			index = i
		}
	}
	return &singleCorruptRows{Rows: rows, backend: b, index: index}, nil
}

type singleCorruptRows struct {
	db.Rows
	backend        *singleCorruptBackend
	index          int
	repeat, second bool
}

func (r *singleCorruptRows) Next() bool {
	if r.repeat {
		r.repeat = false
		r.second = true
		return true
	}
	r.second = false
	present := r.Rows.Next()
	r.repeat = present && r.backend.mode == "different_target"
	return present
}
func (r *singleCorruptRows) Scan(destinations ...any) error {
	if err := r.Rows.Scan(destinations...); err != nil {
		return err
	}
	if r.backend.mode != "foreign" && !r.second {
		return nil
	}
	if r.index < 0 {
		return errors.New("missing single relation corruption column")
	}
	switch value := destinations[r.index].(type) {
	case sql.Scanner:
		return value.Scan(int64(-999999))
	case *int64:
		*value = -999999
		return nil
	default:
		return fmt.Errorf("unsupported single scan %T", value)
	}
}
func (r *singleCorruptRows) Close() error { r.backend.closed = true; return r.Rows.Close() }

func TestCustomSinglePrefetch(t *testing.T) {
	withCollectionBackends(t, runCustomSinglePrefetch)
}

func runCustomSinglePrefetch(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	first, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
	check(t, err)
	second, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("second"))
	check(t, err)
	a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a"))
	check(t, err)
	bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
	check(t, err)
	collections, err := project.BindCollections()
	check(t, err)
	for i, parent := range []owners.Owner{first, second} {
		manager, err := collections.OwnersOwnerLabels.From(b, parent)
		check(t, err)
		values := []labels.Label{a, bb}
		if i == 1 {
			values = values[1:]
		}
		check(t, manager.Add(ctx, values))
	}
	var links []owners.RankedLink
	for i, parent := range []owners.Owner{first, first, second} {
		_, err := owners.RequiredOwnerObjects.Create(ctx, b, owners.RequiredOwnerCreate{}.WithOwnerID(parent.ID).WithAmount(int64(i+1)))
		check(t, err)
		target := a
		if i > 0 {
			target = bb
		}
		link, err := owners.RankedLinkObjects.Create(ctx, b, owners.NewRankedLinkCreate(int64(i+1)).WithOwnerID(parent.ID).WithLabelID(target.ID))
		check(t, err)
		links = append(links, link)
	}
	for i, id := range []*int64{&first.ID, &second.ID, nil} {
		input := owners.NewLooseLinkCreate(int64(i))
		if id != nil {
			input = input.WithOwnerID(*id)
		}
		_, err := owners.LooseLinkObjects.Create(ctx, b, input)
		check(t, err)
	}
	for _, id := range []*int64{&links[0].ID, &links[1].ID, nil} {
		input := owners.NewOptionalCreate()
		if id != nil {
			input = input.WithLinkID(*id)
		}
		_, err := owners.OptionalObjects.Create(ctx, b, input)
		check(t, err)
	}
	_, err = owners.BadgeObjects.Create(ctx, b, owners.BadgeCreate{}.WithOwnerID(first.ID).WithName("hidden"))
	check(t, err)
	_, err = owners.BadgeObjects.Create(ctx, b, owners.BadgeCreate{}.WithOwnerID(second.ID).WithName("visible"))
	check(t, err)
	data, err := os.ReadFile("django-query.json")
	check(t, err)
	var envelope map[string]json.RawMessage
	check(t, json.Unmarshal(data, &envelope))
	var observations map[string]json.RawMessage
	check(t, json.Unmarshal(envelope["observations"], &observations))
	var reference map[string]json.RawMessage
	check(t, json.Unmarshal(observations["prefetch_custom_single"], &reference))
	probe := &prefetchProbe{collectionBackend: b}
	api, err := project.Using(probe)
	check(t, err)
	base := api.OwnersRequiredOwner.OrderBy(owners.RequiredOwnerFields.Amount.Asc())
	selector := base.Prefetch.Owner.Filter(owners.OwnerFields.Name.Exact("first")).OrderBy(owners.OwnerFields.Name.Desc()).WithChildren(api.OwnersOwner.Prefetch.Labels)
	missing := map[string]any{"error": "RelatedObjectDoesNotExist"}
	ownerGraph := func(value *project.OwnersOwner) any {
		view, err := value.Labels()
		check(t, err)
		children, err := view.All(ctx)
		check(t, err)
		names := make([]string, len(children))
		for i, child := range children {
			names[i] = child.Name
		}
		return []any{value.Name, names}
	}
	requiredGraph := func(rows []*project.OwnersRequiredOwner, children bool) []any {
		result := make([]any, 0, len(rows))
		for _, row := range rows {
			value, err := row.Owner(ctx)
			if errors.Is(err, &query.Error{Code: query.CodeRelatedObjectMissing}) {
				result = append(result, missing)
				continue
			}
			check(t, err)
			if children {
				result = append(result, ownerGraph(value))
			} else {
				result = append(result, value.Name)
			}
		}
		return result
	}
	for _, name := range []string{"required_filtered", "required_eager_custom_children", "required_eager_explicit_children", "required_join_duplicates"} {
		t.Run(name, func(t *testing.T) {
			q := base.PrefetchRelated(selector)
			switch name {
			case "required_eager_custom_children":
				q = base.SelectRelated(base.Related.Owner).PrefetchRelated(selector)
			case "required_eager_explicit_children":
				path, err := base.PrefetchPath("owner__labels")
				check(t, err)
				q = base.PrefetchRelated(base.Prefetch.Owner.Filter(owners.OwnerFields.Name.Exact("first")), path).SelectRelated(base.Related.Owner)
			case "required_join_duplicates":
				relations, err := project.BindRelations()
				check(t, err)
				q = base.PrefetchRelated(base.Prefetch.Owner.Filter(relations.OwnersOwner.Labels.Name.In("a", "b")).OrderBy(owners.OwnerFields.Name.Desc()))
			}
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			batch := len(probe.plans) - before
			before = len(probe.plans)
			members := requiredGraph(rows, name != "required_join_duplicates")
			prefetchReference(t, reference, name, map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
			if name == "required_join_duplicates" {
				relations, err := project.BindRelations()
				check(t, err)
				distinct, err := base.PrefetchRelated(base.Prefetch.Owner.Filter(relations.OwnersOwner.Labels.Name.In("a", "b")).Distinct()).All(ctx)
				check(t, err)
				if !reflect.DeepEqual(requiredGraph(distinct, false), members) {
					t.Fatal("single target distinct changed owners")
				}
			}
		})
	}
	t.Run("nullable_filtered", func(t *testing.T) {
		q := api.OwnersLooseLink.OrderBy(owners.LooseLinkFields.Amount.Asc()).PrefetchRelated(api.OwnersLooseLink.Prefetch.Owner.Filter(owners.OwnerFields.Name.Exact("first")))
		before := len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		batch := len(probe.plans) - before
		before = len(probe.plans)
		members := make([]any, 0, len(rows))
		for _, row := range rows {
			value, present, err := row.Owner(ctx)
			check(t, err)
			if present {
				members = append(members, value.Name)
			} else {
				if value != nil {
					t.Fatal("absent target is not nil")
				}
				members = append(members, nil)
			}
		}
		prefetchReference(t, reference, "nullable_filtered", map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
	})
	t.Run("reverse_filtered", func(t *testing.T) {
		q := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).PrefetchRelated(api.OwnersOwner.Prefetch.Badge.Filter(owners.BadgeFields.Name.Exact("visible")))
		before := len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		batch := len(probe.plans) - before
		before = len(probe.plans)
		members := make([]any, 0, len(rows))
		for _, row := range rows {
			value, present, err := row.Badge(ctx)
			check(t, err)
			if present {
				members = append(members, value.Name)
			} else {
				if value != nil {
					t.Fatal("absent reverse target is not nil")
				}
				members = append(members, missing)
			}
		}
		prefetchReference(t, reference, "reverse_filtered", map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
	})
	t.Run("target_eager", func(t *testing.T) {
		selection := api.OwnersOptional.Prefetch.Link.Filter(owners.RankedLinkFields.Amount.Exact(1)).SelectRelated(api.OwnersRankedLink.Related.Owner)
		q := api.OwnersOptional.OrderBy(owners.OptionalFields.ID.Asc()).PrefetchRelated(selection)
		before := len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		batch := len(probe.plans) - before
		before = len(probe.plans)
		members := make([]any, 0, len(rows))
		for _, row := range rows {
			value, present, err := row.Link(ctx)
			check(t, err)
			if !present {
				members = append(members, nil)
				continue
			}
			parent, present, err := value.Owner(ctx)
			check(t, err)
			if !present {
				t.Fatal("target eager parent missing")
			}
			members = append(members, []any{value.Amount, parent.Name})
		}
		prefetchReference(t, reference, "target_eager", map[string]any{"members": members, "batch_queries": batch, "warm_queries": len(probe.plans) - before})
	})
	t.Run("validation", func(t *testing.T) {
		foreign, err := project.Using(probe)
		check(t, err)
		before := len(probe.plans)
		bad := []project.OwnersRequiredOwnerPrefetchQuery{
			base.PrefetchRelated(base.Prefetch.Owner, selector), base.PrefetchRelated(selector, selector),
			base.PrefetchRelated(base.Prefetch.Owner.Filter(orm.Predicate[owners.Owner]{})),
			base.PrefetchRelated(base.Prefetch.Owner.SelectRelated()),
			base.PrefetchRelated(base.Prefetch.Owner.WithChildren(foreign.OwnersOwner.Prefetch.Labels)),
		}
		for _, q := range bad {
			if values, err := q.All(ctx); err == nil || values != nil {
				t.Fatal("invalid custom single selection accepted")
			}
		}
		q := api.OwnersOptional.PrefetchRelated(api.OwnersOptional.Prefetch.Link.SelectRelated(foreign.OwnersRankedLink.Related.Owner))
		if values, err := q.All(ctx); err == nil || values != nil {
			t.Fatal("foreign eager target accepted")
		}
		leaves := make([]project.OwnersRankedLinkRelationSelector, orm.MaximumRelatedSelectionNodes)
		for i := range leaves {
			leaves[i] = api.OwnersRankedLink.Related.Owner
		}
		if values, err := api.OwnersOptional.PrefetchRelated(api.OwnersOptional.Prefetch.Link.SelectRelated(leaves...)).All(ctx); err == nil || values != nil {
			t.Fatal("eager target budget lost")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := bad[0].All(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("context precedence lost", err)
		}
		if len(probe.plans) != before {
			t.Fatal("invalid selection performed I/O")
		}
	})
	t.Run("refinement_and_independent_cache", func(t *testing.T) {
		q := base.PrefetchRelated(selector)
		before := len(probe.plans)
		count, err := q.Count(ctx)
		check(t, err)
		if count != 3 || len(probe.plans)-before != 1 {
			t.Fatal("count loaded target")
		}
		before = len(probe.plans)
		one, present, err := q.First(ctx)
		check(t, err)
		if !present || one.Amount != 1 || len(probe.plans)-before != 3 {
			t.Fatal("cold first lost target graph")
		}
		before = len(probe.plans)
		rows, err := q.All(ctx)
		check(t, err)
		if len(rows) != 3 || len(probe.plans)-before != 3 {
			t.Fatal("first contaminated all")
		}
		target, err := rows[0].Owner(ctx)
		check(t, err)
		target.Name = "changed"
		other, err := rows[1].Owner(ctx)
		check(t, err)
		if other.Name != "first" {
			t.Fatal("duplicate owner wrappers alias")
		}
		before = len(probe.plans)
		again, err := q.All(ctx)
		check(t, err)
		requiredGraph(again, true)
		if len(probe.plans) != before {
			t.Fatal("warm graph queried")
		}
		limited, err := q.Fresh().Offset(1)
		check(t, err)
		limited, err = limited.Limit(1)
		check(t, err)
		before = len(probe.plans)
		rows, err = limited.All(ctx)
		check(t, err)
		if len(rows) != 1 || rows[0].Amount != 2 || len(probe.plans)-before != 3 {
			t.Fatal("source slice lost configured target")
		}
		rows, err = q.Filter(owners.RequiredOwnerFields.Amount.Exact(3)).All(ctx)
		check(t, err)
		if len(rows) != 1 || !reflect.DeepEqual(requiredGraph(rows, true), []any{missing}) {
			t.Fatal("filter or absent target changed")
		}
	})
	t.Run("failure_cancel_retry", func(t *testing.T) {
		failure := errors.New("single child failed")
		probe.failure = failure
		for at := 1; at <= 3; at++ {
			q := base.PrefetchRelated(selector)
			probe.failAt = len(probe.plans) + at
			if values, err := q.All(ctx); !errors.Is(err, failure) || values != nil {
				t.Fatal("partial custom single graph escaped", err)
			}
			probe.failAt = 0
			before := len(probe.plans)
			rows, err := q.All(ctx)
			check(t, err)
			if len(rows) != 3 || len(probe.plans)-before != 3 {
				t.Fatal("retry reused partial graph")
			}
		}
		interrupted, cancel := context.WithCancel(ctx)
		probe.cancelAfterScan = cancel
		probe.cancelAt = len(probe.plans) + 2
		q := base.PrefetchRelated(selector)
		if values, err := q.All(interrupted); !errors.Is(err, context.Canceled) || values != nil {
			t.Fatal("canceled target graph published", err)
		}
		cancel()
		probe.cancelAfterScan = nil
		probe.cancelAt = 0
		rows, err := q.All(ctx)
		check(t, err)
		if len(rows) != 3 {
			t.Fatal("cancel retry missing roots")
		}
	})
	t.Run("loaded_absence_preserves_foreign_key", func(t *testing.T) {
		rows, err := base.Filter(owners.RequiredOwnerFields.Amount.Exact(3)).PrefetchRelated(selector).All(ctx)
		check(t, err)
		if len(rows) != 1 {
			t.Fatal("missing required root")
		}
		held := rows[0]
		derived, err := held.WithOwnerID(second.ID)
		check(t, err)
		before := len(probe.plans)
		for _, row := range []*project.OwnersRequiredOwner{held, derived} {
			if _, err := row.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeRelatedObjectMissing}) {
				t.Fatal("required filtered absence", err)
			}
		}
		if len(probe.plans) != before {
			t.Fatal("derived absence queried")
		}
		check(t, derived.Save(ctx))
		raw, err := owners.RequiredOwnerObjects.Using(b).Filter(owners.RequiredOwnerFields.ID.Exact(held.ID)).All(ctx)
		check(t, err)
		if len(raw) != 1 || raw[0].OwnerID != second.ID {
			t.Fatal("read absence cleared required FK")
		}
		before = len(probe.plans)
		if _, err := derived.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeRelatedObjectMissing}) || len(probe.plans) != before {
			t.Fatal("save lost cached absence", err)
		}
		changed, err := held.WithOwnerID(first.ID)
		check(t, err)
		parent, err := changed.Owner(ctx)
		check(t, err)
		if parent.Name != "first" {
			t.Fatal("FK change retained absent target")
		}
		optional, err := api.OwnersLooseLink.Filter(owners.LooseLinkFields.Amount.Exact(1)).PrefetchRelated(api.OwnersLooseLink.Prefetch.Owner.Filter(owners.OwnerFields.Name.Exact("first"))).All(ctx)
		check(t, err)
		if len(optional) != 1 {
			t.Fatal("missing nullable root")
		}
		copy, err := optional[0].WithOwnerID(second.ID)
		check(t, err)
		check(t, copy.Save(ctx))
		if _, present, err := copy.Owner(ctx); err != nil || present {
			t.Fatal("nullable loaded absence changed", err)
		}
		stored, err := owners.LooseLinkObjects.Using(b).Filter(owners.LooseLinkFields.ID.Exact(copy.ID)).All(ctx)
		check(t, err)
		if len(stored) != 1 || stored[0].OwnerID == nil || *stored[0].OwnerID != second.ID {
			t.Fatal("read absence cleared nullable FK")
		}
	})
	t.Run("native_required_absence", func(t *testing.T) {
		binding, err := project.Bind()
		check(t, err)
		source, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "required_owner"}, owners.RequiredOwnerDescriptor{})
		check(t, err)
		target, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}, owners.OwnerDescriptor{})
		check(t, err)
		relation, err := orm.BindRequiredForwardObject(source, "owner", target)
		check(t, err)
		q := owners.RequiredOwnerObjects.Using(probe).Filter(owners.RequiredOwnerFields.Amount.Exact(3))
		values, err := orm.PrefetchRelated(q, orm.PrefetchRequiredForward(relation).Filter(owners.OwnerFields.Name.Exact("first"))).All(ctx)
		check(t, err)
		if len(values) != 1 {
			t.Fatal("native missing owner")
		}
		objects, err := project.BindObjectsIn(binding)
		check(t, err)
		object, err := objects.OwnersRequiredOwner.FromSelected(values[0])
		check(t, err)
		before := len(probe.plans)
		if _, err := object.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeRelatedObjectMissing}) || len(probe.plans) != before {
			t.Fatal("low level required absence", err)
		}
		fresh, err := object.Fresh()
		check(t, err)
		parent, err := fresh.Owner(ctx)
		check(t, err)
		if parent.Name != "second" {
			t.Fatal("fresh retained target filter")
		}
		before = len(probe.plans)
		if _, err := object.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeRelatedObjectMissing}) || len(probe.plans) != before {
			t.Fatal("fresh changed held absence", err)
		}
	})
	t.Run("foreign_and_cardinality", func(t *testing.T) {
		table := ""
		for _, model := range owners.GoDjRelationSchema().Models {
			if model.Name == "badge" {
				table = model.DBTable
			}
		}
		if table == "" {
			t.Fatal("missing badge table")
		}
		for _, mode := range []string{"foreign", "different_target"} {
			corrupt := &singleCorruptBackend{collectionBackend: b, table: table, mode: mode}
			scoped, err := project.Using(corrupt)
			check(t, err)
			q := scoped.OwnersOwner.PrefetchRelated(scoped.OwnersOwner.Prefetch.Badge.Filter(owners.BadgeFields.Name.Exact("visible")))
			code := query.CodeRelatedObjectProjection
			if mode == "different_target" {
				code = query.CodeRelatedObjectCardinality
			}
			rows, err := q.All(ctx)
			if !errors.Is(err, &query.Error{Code: code}) || rows != nil || !corrupt.closed {
				t.Fatal("single target integrity or row close lost", mode, err)
			}
		}
	})
	t.Run("concurrent_warm", func(t *testing.T) {
		q := base.PrefetchRelated(selector)
		_, err := q.All(ctx)
		check(t, err)
		before := len(probe.plans)
		var wg sync.WaitGroup
		failures := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Go(func() {
				rows, err := q.All(ctx)
				if err != nil {
					failures <- err
					return
				}
				owner, err := rows[0].Owner(ctx)
				if err != nil {
					failures <- err
					return
				}
				if owner.Name != "first" {
					failures <- fmt.Errorf("concurrent cache changed: %s", owner.Name)
					return
				}
				owner.Name = "private"
				view, err := owner.Labels()
				if err != nil {
					failures <- err
					return
				}
				values, err := view.All(ctx)
				if err != nil || len(values) != 2 {
					failures <- fmt.Errorf("concurrent children: %d %v", len(values), err)
				}
			})
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if len(probe.plans) != before {
			t.Fatal("warm concurrent graph queried")
		}
	})
	t.Run("session_lifetime", func(t *testing.T) {
		var held []*project.OwnersRequiredOwner
		check(t, b.AtomicRelation(ctx, func(session db.RelationSession) error {
			api, err := project.UsingSession(session)
			if err != nil {
				return err
			}
			held, err = api.OwnersRequiredOwner.OrderBy(owners.RequiredOwnerFields.Amount.Asc()).PrefetchRelated(api.OwnersRequiredOwner.Prefetch.Owner.Filter(owners.OwnerFields.Name.Exact("first")).WithChildren(api.OwnersOwner.Prefetch.Labels)).All(ctx)
			return err
		}))
		for _, row := range held {
			if _, err := row.Owner(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired custom target cache", err)
			}
		}
	})
}
