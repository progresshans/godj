package consumer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/catalog"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type namedCollectionProbe struct {
	db.Session
	calls int
}

func (p *namedCollectionProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.calls++
	return p.Session.Query(ctx, plan)
}
func (p *namedCollectionProbe) ValidateSession(ctx context.Context) error {
	if session, ok := p.Session.(db.SessionValidator); ok {
		return session.ValidateSession(ctx)
	}
	return ctx.Err()
}

func seedNamedCollections(t *testing.T, session db.Session) {
	t.Helper()
	groups := map[string]int64{}
	for _, name := range []string{"x", "y", "empty"} {
		row, err := catalog.CategoryObjects.Create(t.Context(), session, catalog.NewCategoryCreate(name))
		check(t, err)
		groups[name] = row.ID
	}
	labels := map[string]int64{}
	for _, item := range [][2]string{{"l1", "x"}, {"l2", "x"}, {"l3", "y"}} {
		row, err := catalog.LabelObjects.Create(t.Context(), session, catalog.NewLabelCreate(item[0]).WithCategoryID(groups[item[1]]))
		check(t, err)
		labels[item[0]] = row.ID
	}
	tickets := map[string]int64{}
	for _, item := range []struct {
		code, group string
		amount      int64
		present     bool
	}{{"a", "x", 2, true}, {"b", "x", 5, true}, {"c", "y", 7, true}, {"d", "y", 0, false}, {"e", "", 0, false}} {
		input := records.NewTicketCreate(item.code)
		if item.group != "" {
			input = input.WithCategoryID(groups[item.group])
		}
		if item.present {
			input = input.WithAmount(item.amount)
		}
		row, err := records.TicketObjects.Create(t.Context(), session, input)
		check(t, err)
		tickets[item.code] = row.ID
	}
	for _, link := range [][2]string{{"a", "l1"}, {"a", "l2"}, {"b", "l1"}, {"c", "l2"}, {"d", "l3"}} {
		_, err := records.LinkObjects.Create(t.Context(), session, records.NewLinkCreate().WithTicketID(tickets[link[0]]).WithLabelID(labels[link[1]]))
		check(t, err)
	}
	_, err := records.DetailObjects.Create(t.Context(), session, records.NewDetailCreate(tickets["a"]).WithBonus(9))
	check(t, err)
}

func TestNamedCollectionRuntime(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, _ string, _ collectionPhysicalWriter) {
		relations, err := project.BindRelations()
		check(t, err)
		within := func(t *testing.T, run func(db.Session)) {
			t.Helper()
			rollback := errors.New("named collection rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error { seedNamedCollections(t, session); run(session); return rollback })
			if !errors.Is(err, rollback) {
				t.Fatal("named collection rollback", err)
			}
			for _, count := range []func(context.Context) (int64, error){catalog.CategoryObjects.Using(backend).Count, records.TicketObjects.Using(backend).Count, records.LinkObjects.Using(backend).Count} {
				n, err := count(t.Context())
				check(t, err)
				if n != 0 {
					t.Fatal("named collection retained rows", n)
				}
			}
		}
		t.Run("definition_time_join_and_multiple_collections", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				base := api.CatalogCategory.OrderBy(catalog.CategoryFields.ID.Asc())
				ticket := relations.CatalogCategory.Tickets
				before, count, err := project.AnnotateCatalogCategoryAggregate(base, "counted", orm.Count(ticket.ID))
				check(t, err)
				after, total, err := project.AnnotateCatalogCategoryAggregate(before.Filter(ticket.Amount.GreaterThan(3)), "total", orm.Sum(ticket.Amount))
				check(t, err)
				rows, err := after.All(t.Context())
				check(t, err)
				if len(rows) != 2 {
					t.Fatal(rows)
				}
				for i, want := range []int64{10, 14} {
					n, err := count.Get(rows[i])
					check(t, err)
					sum, err := total.Get(rows[i])
					check(t, err)
					if value, ok := sum.Get(); n != 2 || !ok || value != want {
						t.Fatal("later filter rebound earlier aggregate", i, n, sum)
					}
				}
				filtered, count, err := project.AnnotateCatalogCategoryAggregate(base.Filter(ticket.Amount.GreaterThan(3)), "counted", orm.Count(ticket.ID))
				check(t, err)
				rows, err = filtered.All(t.Context())
				check(t, err)
				for _, row := range rows {
					n, err := count.Get(row)
					check(t, err)
					if n != 1 {
						t.Fatal("filter-before aggregate lost reuse", n)
					}
				}
				product, err := base.Annotate(orm.Named("tickets_count", orm.DynamicF("tickets__id")).Count(), orm.Named("labels_count", orm.DynamicF("labels__id")).Count())
				check(t, err)
				left, err := product.Column("tickets_count")
				check(t, err)
				right, err := product.Column("labels_count")
				check(t, err)
				rows, err = product.All(t.Context())
				check(t, err)
				for i, want := range []int64{4, 2, 0} {
					a, err := left.Get(rows[i])
					check(t, err)
					b, err := right.Get(rows[i])
					check(t, err)
					if n, _ := a.Integer(); n != want {
						t.Fatal("independent collection row product", a)
					}
					if n, _ := b.Integer(); n != want {
						t.Fatal("independent collection row product", b)
					}
				}
				distinct, err := base.Annotate(orm.Named("tickets_count", orm.DynamicF("tickets__id")).Count().Distinct(), orm.Named("labels_count", orm.DynamicF("labels__id")).Count().Distinct())
				check(t, err)
				left, err = distinct.Column("tickets_count")
				check(t, err)
				right, err = distinct.Column("labels_count")
				check(t, err)
				rows, err = distinct.All(t.Context())
				check(t, err)
				for i, want := range [][2]int64{{2, 2}, {2, 1}, {0, 0}} {
					a, err := left.Get(rows[i])
					check(t, err)
					b, err := right.Get(rows[i])
					check(t, err)
					if n, _ := a.Integer(); n != want[0] {
						t.Fatal(a)
					}
					if n, _ := b.Integer(); n != want[1] {
						t.Fatal(b)
					}
				}
			})
		})
		t.Run("relation_names_and_project_authority", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &namedCollectionProbe{Session: session}
				api, err := project.UsingSession(probe)
				check(t, err)
				for _, name := range []string{"id", "code", "tickets", "labels"} {
					if _, err := api.CatalogCategory.Annotate(orm.Named(name, orm.DynamicValue(1))); err == nil {
						t.Fatal("declared category name shadowed", name)
					}
				}
				for _, name := range []string{"category", "labels", "label_links", "detail"} {
					if _, err := api.RecordsTicket.Alias(orm.Named(name, orm.DynamicValue(1))); err == nil {
						t.Fatal("declared ticket accessor shadowed", name)
					}
				}
				named, err := api.CatalogCategory.Annotate(orm.Named("counted", orm.DynamicF("tickets__id")).Count())
				check(t, err)
				projection, err := named.ProjectExpressions(orm.DynamicF("code"))
				check(t, err)
				selected, err := project.SelectCatalogCategoryNamedQuery(named, projection)
				check(t, err)
				if _, err := orm.AnnotateDynamic(selected, orm.Named("tickets", orm.DynamicValue(1))); err == nil {
					t.Fatal("projection dropped relation name authority")
				}
				reused, err := orm.AnnotateDynamic(selected, orm.Named("other_count", orm.DynamicF("tickets__id")).Count())
				check(t, err)
				if _, err := reused.Expression(orm.DynamicF("tickets__amount")); err == nil {
					t.Fatal("collection operand became one scalar row")
				}
				if _, err := named.Annotate(orm.Named("unknown", orm.DynamicF("tickets__unknown")).Sum()); err == nil {
					t.Fatal("unknown relation leaf accepted")
				}
				if probe.calls != 0 {
					t.Fatal("name authority validation performed I/O", probe.calls)
				}
			})
		})
		t.Run("aggregate_dependencies_filters_and_nulls", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				base := api.CatalogCategory.OrderBy(catalog.CategoryFields.ID.Asc())
				ticket := relations.CatalogCategory.Tickets
				named, filtered, err := project.AnnotateCatalogCategoryAggregate(base, "matching", orm.Count(ticket.ID).Where(ticket.Amount.GreaterThan(3)))
				check(t, err)
				named, double, err := project.AnnotateCatalogCategory(named, "double", orm.Multiply(filtered, int64(2)))
				check(t, err)
				predicate, err := named.ExpressionPredicate(orm.DynamicOr(orm.DynamicF("double").GreaterThan(0), orm.DynamicF("code").Exact("empty")))
				check(t, err)
				page, err := named.Filter(predicate).Page(t.Context(), 10, 0)
				check(t, err)
				if page.Total != 3 || len(page.Rows) != 3 {
					t.Fatal("mixed result predicate", page)
				}
				for i, want := range []int64{2, 2, 0} {
					value, err := double.Get(page.Rows[i])
					check(t, err)
					if n, ok := value.Get(); !ok || n != want {
						t.Fatal("filtered aggregate dependency", value)
					}
				}
				projection, err := named.ProjectExpressions(orm.DynamicF("code"), orm.DynamicF("matching"), orm.DynamicF("double"))
				check(t, err)
				dynamic, err := project.SelectCatalogCategoryNamedQuery(named, projection)
				check(t, err)
				typed, err := project.SelectCatalogCategoryNamedQuery(named, orm.Project3(orm.F(catalog.CategoryFields.Code), filtered, double, func(code orm.Optional[string], count int64, n orm.Optional[int64]) []query.Value {
					key, present := code.Get()
					if !present {
						t.Fatal("required code expression is NULL")
					}
					value, ok := n.Get()
					if !ok {
						t.Fatal("non-null count arithmetic is NULL")
					}
					return []query.Value{query.String(key), query.Integer(count), query.Integer(value)}
				}))
				check(t, err)
				if !typed.Plan().Equal(dynamic.Plan()) {
					t.Fatal("collection typed/dynamic AST differs")
				}
				a, err := typed.All(t.Context())
				check(t, err)
				b, err := dynamic.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(a, b) {
					t.Fatal(a, b)
				}
				sums, total, err := project.AnnotateCatalogCategoryAggregate(base, "total", orm.Sum(ticket.Amount))
				check(t, err)
				rows, err := sums.All(t.Context())
				check(t, err)
				if value, err := total.Get(rows[2]); err != nil || value.Valid() {
					t.Fatal("empty collection SUM lost NULL", value, err)
				}
				many, err := api.CatalogLabel.OrderBy(catalog.LabelFields.ID.Asc()).Annotate(orm.Named("counted", orm.DynamicF("tickets__id")).Count())
				check(t, err)
				manyRows, err := many.All(t.Context())
				check(t, err)
				count, err := many.Column("counted")
				check(t, err)
				for i, want := range []int64{2, 2, 1} {
					value, err := count.Get(manyRows[i])
					check(t, err)
					if n, _ := value.Integer(); n != want {
						t.Fatal("ManyToMany named count", value)
					}
				}
			})
		})
		t.Run("model_relation_wrappers_and_storage", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				named, plus, err := project.AnnotateRecordsTicket(api.RecordsTicket.Filter(records.TicketFields.Code.Exact("a")), "plus", orm.Add(orm.F(records.TicketFields.Amount), int64(1)))
				check(t, err)
				rows, err := named.All(t.Context())
				check(t, err)
				if len(rows) != 1 {
					t.Fatal(rows)
				}
				category, present, err := rows[0].Value.Category(t.Context())
				check(t, err)
				if !present || category.Code != "x" {
					t.Fatal("named model lost forward relation", category, present)
				}
				labels, err := rows[0].Value.Labels()
				check(t, err)
				items, err := labels.All(t.Context())
				check(t, err)
				if len(items) != 2 {
					t.Fatal("named model lost collection", len(items))
				}
				*rows[0].Value.Amount = 12
				check(t, rows[0].Value.Save(t.Context()))
				stored, err := api.RecordsTicket.Filter(records.TicketFields.Code.Exact("a")).Get(t.Context())
				check(t, err)
				if stored.Amount == nil || *stored.Amount != 12 || stored.CategoryID == nil || *stored.CategoryID != category.ID {
					t.Fatal("saving named model lost stored fields", stored)
				}
				value, err := plus.Get(rows[0])
				check(t, err)
				if n, ok := value.Get(); !ok || n != 3 {
					t.Fatal("model save rewrote prior named cell", value)
				}
				fresh, err := named.Fresh().All(t.Context())
				check(t, err)
				value, err = plus.Get(fresh[0])
				check(t, err)
				if n, ok := value.Get(); !ok || n != 13 {
					t.Fatal("fresh named value did not reflect stored update", value)
				}
			})
		})
	})
}
