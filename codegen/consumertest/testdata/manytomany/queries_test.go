package consumer

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCollectionQueries(t *testing.T) { withCollectionBackends(t, runCollectionQueries) }

type collectionQueryCase[M any] struct{ typed, dynamic orm.QuerySet[M] }

func runQueryCases[M any](t *testing.T, name string, oracle map[string]json.RawMessage, cases map[string]collectionQueryCase[M], modelName func(M) string) {
	t.Helper()
	var expected map[string][]string
	check(t, json.Unmarshal(oracle[name], &expected))
	if len(cases) != len(expected) {
		t.Fatalf("%s case inventory: actual %d reference %d", name, len(cases), len(expected))
	}
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	t.Run(name, func(t *testing.T) {
		for _, key := range keys {
			t.Run(key, func(t *testing.T) {
				c, ok := cases[key]
				if !ok {
					t.Fatal("required reference case missing")
				}
				if !c.typed.Plan().Equal(c.dynamic.Plan()) {
					t.Fatal("typed and dynamic relation paths disagree")
				}
				for _, profile := range []struct {
					name string
					set  orm.QuerySet[M]
				}{{"typed", c.typed}, {"dynamic", c.dynamic}} {
					values, err := profile.set.All(t.Context())
					check(t, err)
					names := make([]string, len(values))
					for i, value := range values {
						names[i] = modelName(value)
					}
					if !slices.Equal(names, expected[key]) {
						t.Fatalf("%s: got %v want %v", profile.name, names, expected[key])
					}
					count, err := profile.set.Fresh().Count(t.Context())
					check(t, err)
					if count != int64(len(expected[key])) {
						t.Fatalf("%s cold count %d want %d", profile.name, count, len(expected[key]))
					}
				}
			})
		}
	})
}

func runCollectionQueries(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx := t.Context()
	r, err := project.BindRelations()
	check(t, err)
	c, err := project.BindCollections()
	check(t, err)
	first, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
	check(t, err)
	second, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("second"))
	check(t, err)
	_, err = owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("empty"))
	check(t, err)
	a, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("a").WithNote("red"))
	check(t, err)
	bb, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("b"))
	check(t, err)
	_, err = labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate("c"))
	check(t, err)
	firstLabels, err := c.OwnersOwnerLabels.From(b, first)
	check(t, err)
	secondLabels, err := c.OwnersOwnerLabels.From(b, second)
	check(t, err)
	check(t, firstLabels.Add(ctx, []labels.Label{a, bb}))
	check(t, secondLabels.Add(ctx, []labels.Label{bb}))
	bytes, err := os.ReadFile("django-query.json")
	check(t, err)
	var reference struct {
		Django       string                     `json:"django"`
		Observations map[string]json.RawMessage `json:"observations"`
	}
	check(t, json.Unmarshal(bytes, &reference))
	if reference.Django != "6.1" {
		t.Fatal("wrong reference profile")
	}
	dynamic := func(key string, value any) orm.Predicate[owners.Owner] {
		predicates, err := r.OwnersOwner.ParseDynamic(nil, []orm.LookupInput{{Key: key, Value: value}})
		check(t, err)
		if len(predicates) != 1 {
			t.Fatal("missing dynamic predicate")
		}
		return predicates[0]
	}
	labelDynamic := func(key string, value any) orm.Predicate[labels.Label] {
		predicates, err := r.LabelsLabel.ParseDynamic(nil, []orm.LookupInput{{Key: key, Value: value}})
		check(t, err)
		if len(predicates) != 1 {
			t.Fatal("missing dynamic predicate")
		}
		return predicates[0]
	}
	base := owners.OwnerObjects.Using(b)
	ownerCase := func(typed, dynamic orm.QuerySet[owners.Owner]) collectionQueryCase[owners.Owner] {
		return collectionQueryCase[owners.Owner]{typed.OrderBy(owners.OwnerFields.Name.Asc()), dynamic.OrderBy(owners.OwnerFields.Name.Asc())}
	}
	labelCase := func(typed, dynamic orm.QuerySet[labels.Label]) collectionQueryCase[labels.Label] {
		return collectionQueryCase[labels.Label]{typed.OrderBy(labels.LabelFields.Name.Asc()), dynamic.OrderBy(labels.LabelFields.Name.Asc())}
	}
	pa, pb := r.OwnersOwner.Labels.Name.Exact("a"), r.OwnersOwner.Labels.Name.Exact("b")
	da, dbb := dynamic("labels__name", "a"), dynamic("labels__name", "b")
	in, din := r.OwnersOwner.Labels.Name.In("a", "b"), dynamic("labels__name__in", []string{"a", "b"})
	ownerNames := func(value owners.Owner) string { return value.Name }
	labelNames := func(value labels.Label) string { return value.Name }
	// The reverse observation selects labels; validate it with its own typed root.
	scopes := map[string]collectionQueryCase[owners.Owner]{
		"same_filter":           ownerCase(base.Filter(orm.And(pa, pb)), base.Filter(orm.And(da, dbb))),
		"successive_filters":    ownerCase(base.Filter(pa).Filter(pb), base.Filter(da).Filter(dbb)),
		"successive_membership": ownerCase(base.Filter(in).Filter(in), base.Filter(din).Filter(din)),
		"reused_predicate":      ownerCase(base.Filter(pa).Filter(pa), base.Filter(da).Filter(da)),
		"same_member_fields":    ownerCase(base.Filter(pa, r.OwnersOwner.Labels.Note.Exact("red")), base.Filter(da, dynamic("labels__note", "red"))),
		"two_collections":       ownerCase(base.Filter(r.OwnersOwner.Labels.Owners().Name.Exact("first")), base.Filter(dynamic("labels__owners__name", "first"))),
		"three_collections":     ownerCase(base.Filter(r.OwnersOwner.Labels.Owners().Labels().Name.Exact("a")), base.Filter(dynamic("labels__owners__labels__name", "a"))),
	}
	var allScopes map[string]json.RawMessage
	check(t, json.Unmarshal(reference.Observations["query_filter_scopes"], &allScopes))
	reverseExpected := allScopes["reverse_successive"]
	delete(allScopes, "reverse_successive")
	ownerScopeJSON, err := json.Marshal(allScopes)
	check(t, err)
	ownerOracle := map[string]json.RawMessage{"query_filter_scopes": ownerScopeJSON}
	runQueryCases(t, "query_filter_scopes", ownerOracle, scopes, ownerNames)
	lbase := labels.LabelObjects.Using(b)
	runQueryCases(t, "reverse_scope", map[string]json.RawMessage{"reverse_scope": json.RawMessage(`{"reverse_successive":` + string(reverseExpected) + `}`)}, map[string]collectionQueryCase[labels.Label]{"reverse_successive": labelCase(lbase.Filter(r.LabelsLabel.Owners.Name.Exact("first")).Filter(r.LabelsLabel.Owners.Name.Exact("second")), lbase.Filter(labelDynamic("owners__name", "first")).Filter(labelDynamic("owners__name", "second")))}, labelNames)
	empty := owners.OwnerFields.Name.Exact("empty")
	boolCases := map[string]collectionQueryCase[owners.Owner]{
		"or_root":               ownerCase(base.Filter(orm.Or(pa, empty)), base.Filter(orm.Or(da, empty))),
		"or_members":            ownerCase(base.Filter(orm.Or(pa, pb)), base.Filter(orm.Or(da, dbb))),
		"not_a":                 ownerCase(base.Filter(orm.Not(pa)), base.Filter(orm.Not(da))),
		"not_and":               ownerCase(base.Filter(orm.Not(orm.And(pa, pb))), base.Filter(orm.Not(orm.And(da, dbb)))),
		"not_or":                ownerCase(base.Filter(orm.Not(orm.Or(pa, pb))), base.Filter(orm.Not(orm.Or(da, dbb)))),
		"double_not":            ownerCase(base.Filter(orm.Not(orm.Not(pa))), base.Filter(orm.Not(orm.Not(da)))),
		"positive_and_negative": ownerCase(base.Filter(orm.And(pa, orm.Not(pb))), base.Filter(orm.And(da, orm.Not(dbb)))),
		"or_negative":           ownerCase(base.Filter(orm.Or(pa, orm.Not(pb))), base.Filter(orm.Or(da, orm.Not(dbb)))),
		"absent":                ownerCase(base.Filter(r.OwnersOwner.Labels.IsNull(true)), base.Filter(dynamic("labels__isnull", true))),
		"present":               ownerCase(base.Filter(r.OwnersOwner.Labels.IsNull(false)), base.Filter(dynamic("labels__isnull", false))),
		"field_null":            ownerCase(base.Filter(r.OwnersOwner.Labels.Note.IsNull(true)), base.Filter(dynamic("labels__note__isnull", true))),
		"exclude_field_null":    ownerCase(base.Filter(orm.Not(r.OwnersOwner.Labels.Note.IsNull(true))), base.Filter(orm.Not(dynamic("labels__note__isnull", true)))),
		"empty_membership":      ownerCase(base.Filter(r.OwnersOwner.Labels.Name.In()), base.Filter(dynamic("labels__name__in", []string{}))),
		"empty_or_root":         ownerCase(base.Filter(orm.Or(r.OwnersOwner.Labels.Name.In(), empty)), base.Filter(orm.Or(dynamic("labels__name__in", []string{}), empty))),
	}
	runQueryCases(t, "query_boolean_presence", reference.Observations, boolCases, ownerNames)
	runQueryCases(t, "query_boolean_order", reference.Observations, map[string]collectionQueryCase[owners.Owner]{
		"positive_first":      boolCases["positive_and_negative"],
		"negative_first":      ownerCase(base.Filter(orm.And(orm.Not(pb), pa)), base.Filter(orm.And(orm.Not(dbb), da))),
		"successive_negative": ownerCase(base.Filter(pa).Filter(orm.Not(pb)), base.Filter(da).Filter(orm.Not(dbb))),
		"successive_positive": ownerCase(base.Filter(orm.Not(pb)).Filter(pa), base.Filter(orm.Not(dbb)).Filter(da)),
		"negative_same_field": ownerCase(base.Filter(orm.And(pa, orm.Not(r.OwnersOwner.Labels.Note.Exact("red")))), base.Filter(orm.And(da, orm.Not(dynamic("labels__note", "red"))))),
		"or_negative_first":   ownerCase(base.Filter(orm.Or(orm.Not(pb), pa)), base.Filter(orm.Or(orm.Not(dbb), da))),
		"double_not_pair":     ownerCase(base.Filter(orm.Not(orm.Not(orm.And(pa, pb)))), base.Filter(orm.Not(orm.Not(orm.And(da, dbb))))),
	}, ownerNames)
	member, err := firstLabels.Query()
	check(t, err)
	pf, ps := r.LabelsLabel.Owners.Name.Exact("first"), r.LabelsLabel.Owners.Name.Exact("second")
	df, ds := labelDynamic("owners__name", "first"), labelDynamic("owners__name", "second")
	runQueryCases(t, "query_manager_scopes", reference.Observations, map[string]collectionQueryCase[labels.Label]{
		"direct":         labelCase(member.Filter(ps), member.Filter(ds)),
		"manager_all":    labelCase(member.Filter(ps), member.Filter(ds)),
		"ordered":        labelCase(member.OrderBy(labels.LabelFields.Name.Asc()).Filter(ps), member.OrderBy(labels.LabelFields.Name.Asc()).Filter(ds)),
		"successive":     labelCase(member.Filter(pf).Filter(ps), member.Filter(df).Filter(ds)),
		"scalar_first":   labelCase(member.Filter(labels.LabelFields.Name.Exact("b")).Filter(ps), member.Filter(labels.LabelFields.Name.Exact("b")).Filter(ds)),
		"empty_first":    labelCase(member.Filter().Filter(ps), member.Filter().Filter(ds)),
		"query_clone":    labelCase(member.Fresh().Filter(ps), member.Fresh().Filter(ds)),
		"distinct_first": labelCase(member.Distinct().Filter(ps), member.Distinct().Filter(ds)),
		"or_root":        labelCase(member.Filter(orm.Or(ps, labels.LabelFields.Name.Exact("a"))), member.Filter(orm.Or(ds, labels.LabelFields.Name.Exact("a")))),
	}, labelNames)
	var loose []owners.Owner
	for _, name := range []string{"loose-empty", "loose-mixed", "loose-null"} {
		value, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(name))
		check(t, err)
		loose = append(loose, value)
	}
	for _, pair := range []struct {
		owner, label *int64
		amount       int64
	}{{&loose[1].ID, &a.ID, 1}, {&loose[1].ID, &a.ID, 2}, {&loose[1].ID, &bb.ID, 3}, {&loose[1].ID, nil, 4}, {&loose[2].ID, nil, 5}, {&loose[2].ID, nil, 6}, {nil, &a.ID, 7}} {
		input := owners.NewLooseLinkCreate(pair.amount)
		if pair.owner != nil {
			input = input.WithOwnerID(*pair.owner)
		}
		if pair.label != nil {
			input = input.WithLabelID(*pair.label)
		}
		_, err := owners.LooseLinkObjects.Create(ctx, b, input)
		check(t, err)
	}
	looseBase := base.Filter(owners.OwnerFields.Name.In("loose-empty", "loose-mixed", "loose-null"))
	li, ldi := r.OwnersOwner.Loose.Name.In("a", "b"), dynamic("loose__name__in", []string{"a", "b"})
	var looseCases map[string]json.RawMessage
	check(t, json.Unmarshal(reference.Observations["query_nullable_links"], &looseCases))
	looseReverse := looseCases["reverse_absent"]
	delete(looseCases, "reverse_absent")
	looseJSON, err := json.Marshal(looseCases)
	check(t, err)
	runQueryCases(t, "query_nullable_links", map[string]json.RawMessage{"query_nullable_links": looseJSON}, map[string]collectionQueryCase[owners.Owner]{
		"members":                      ownerCase(looseBase.Filter(li), looseBase.Filter(ldi)),
		"distinct":                     ownerCase(looseBase.Filter(li).Distinct(), looseBase.Filter(ldi).Distinct()),
		"absent":                       ownerCase(looseBase.Filter(r.OwnersOwner.Loose.IsNull(true)), looseBase.Filter(dynamic("loose__isnull", true))),
		"present":                      ownerCase(looseBase.Filter(r.OwnersOwner.Loose.IsNull(false)), looseBase.Filter(dynamic("loose__isnull", false))),
		"field_null":                   ownerCase(looseBase.Filter(r.OwnersOwner.Loose.Note.IsNull(true)), looseBase.Filter(dynamic("loose__note__isnull", true))),
		"field_present":                ownerCase(looseBase.Filter(r.OwnersOwner.Loose.Note.IsNull(false)), looseBase.Filter(dynamic("loose__note__isnull", false))),
		"exclude_a":                    ownerCase(looseBase.Filter(orm.Not(r.OwnersOwner.Loose.Name.Exact("a"))), looseBase.Filter(orm.Not(dynamic("loose__name", "a")))),
		"exclude_null":                 ownerCase(looseBase.Filter(orm.Not(r.OwnersOwner.Loose.Note.IsNull(true))), looseBase.Filter(orm.Not(dynamic("loose__note__isnull", true)))),
		"shared_through_predicate":     ownerCase(looseBase.Filter(r.OwnersOwner.Loose.Name.Exact("a"), r.OwnersOwner.LooseLinkRows.Amount.Exact(3)), looseBase.Filter(dynamic("loose__name", "a"), dynamic("loose_link_rows__amount", int64(3)))),
		"successive_through_predicate": ownerCase(looseBase.Filter(r.OwnersOwner.Loose.Name.Exact("a")).Filter(r.OwnersOwner.LooseLinkRows.Amount.Exact(3)), looseBase.Filter(dynamic("loose__name", "a")).Filter(dynamic("loose_link_rows__amount", int64(3)))),
	}, ownerNames)
	runQueryCases(t, "reverse_nullable", map[string]json.RawMessage{"reverse_nullable": json.RawMessage(`{"reverse_absent":` + string(looseReverse) + `}`)}, map[string]collectionQueryCase[labels.Label]{"reverse_absent": labelCase(lbase.Filter(r.LabelsLabel.LooseOwners.IsNull(true)), lbase.Filter(labelDynamic("loose_owners__isnull", true)))}, labelNames)
	t.Run("binding_authority", func(t *testing.T) {
		predicates, err := r.OwnersOwner.ParseDynamic(func(field ir.Field, lookup query.Lookup) bool { return field.Name != "note" }, []orm.LookupInput{{Key: "labels__name", Value: "a"}, {Key: "labels__note", Value: "red"}})
		if err == nil || predicates != nil {
			t.Fatal("failed lookup policy published a partial predicate batch")
		}
		binding, err := project.Bind()
		check(t, err)
		owner, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}, owners.OwnerDescriptor{})
		check(t, err)
		other, err := project.Bind()
		check(t, err)
		label, err := orm.BindModel(other, ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}, labels.LabelDescriptor{})
		check(t, err)
		if _, err := orm.BindQueryRelation(owner, "labels", label); err == nil {
			t.Fatal("cross-project query relation accepted")
		}
	})
}
