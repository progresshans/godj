package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

func TestGroupedRuntime(t *testing.T) {
	withGroupedBackends(t, func(t *testing.T, backend groupedBackend, open func() (groupedBackend, error), postgres bool) {
		ctx := t.Context()
		probe := &groupedProbe{groupedBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		fields := records.ItemFields
		opened := orm.Count(fields.ID).Where(fields.Enabled.Exact(true))
		t.Run("typed_dynamic_plan_and_decoding", func(t *testing.T) {
			type counts struct{ Total, Present, Unique, Opened int64 }
			type result struct {
				Rank   *int64
				Values counts
			}
			aggregate := orm.Aggregate4(orm.CountRows[records.Item](), orm.Count(fields.Note), orm.Count(fields.Note).Distinct(), opened, func(total, present, unique, opened int64) counts { return counts{total, present, unique, opened} })
			typed, err := project.GroupRecordsItemBy(api.RecordsItem, orm.Project1(fields.Rank, func(v *int64) *int64 { return v }), aggregate, func(k *int64, a counts) result { return result{k, a} })
			check(t, err)
			typed = typed.Having(opened.GreaterThanOrEqual(1)).OrderBy(fields.Rank.Asc().NullsLast())
			dynamic, err := api.RecordsItem.GroupValues([]string{"rank"}, groupedInputs()[:4])
			check(t, err)
			dynamic = dynamic.HavingDynamic(orm.GroupLookupInput{Column: 4, Lookup: query.LookupGreaterThanOrEqual, Value: int64(1)}).OrderByDynamic(orm.GroupOrderInput{Column: 0, Direction: query.Ascending, Nulls: query.NullsLast})
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("generated typed/dynamic plans differ")
			}
			actual, err := typed.All(ctx)
			check(t, err)
			raw := make([]map[string]any, len(actual))
			for i, row := range actual {
				raw[i] = map[string]any{"rank": row.Rank, "total": row.Values.Total, "present": row.Values.Present, "unique": row.Values.Unique, "opened": row.Values.Opened}
			}
			vendor := "sqlite"
			if postgres {
				vendor = "postgres"
			}
			want := groupedReferenceNames(t, vendor)["nullable_key"]
			var selected []map[string]any
			check(t, json.Unmarshal(want, &selected))
			for _, row := range selected {
				delete(row, "minimum")
				delete(row, "maximum")
			}
			expected, err := json.Marshal(selected)
			check(t, err)
			equalGroupedJSON(t, raw, expected)
			equalGroupedJSON(t, groupedResult(t, ctx, dynamic, []string{"rank", "total", "present", "unique", "opened"}), expected)
			if len(actual) != 4 || actual[0].Rank == nil {
				t.Fatal("typed nullable rank")
			}
			*actual[0].Rank = 900
			warm, err := typed.All(ctx)
			check(t, err)
			if *warm[0].Rank != 0 {
				t.Fatal("typed cache aliases published key")
			}
			count, err := orm.AggregateInto(ctx, records.ItemObjects.Using(probe), orm.Aggregate1(orm.Count(fields.Note).Distinct().Where(fields.Enabled.Exact(true)), func(n int64) int64 { return n }))
			check(t, err)
			if count != 3 {
				t.Fatal("plain conditional distinct aggregate", count)
			}
		})
		t.Run("typed_forward_keys_and_count", func(t *testing.T) {
			relations, err := project.BindRelations()
			check(t, err)
			group := relations.RecordsItem.Group
			type result struct {
				Region                 *string
				Total, Present, Unique int64
			}
			grouped, err := project.GroupRecordsItemBy(api.RecordsItem, orm.Project1(group.Region, func(v *string) *string { return v }), orm.Aggregate3(orm.CountRows[records.Item](), orm.Count(group.Name), orm.Count(group.Name).Distinct(), func(a, b, c int64) [3]int64 { return [3]int64{a, b, c} }), func(k *string, a [3]int64) result { return result{k, a[0], a[1], a[2]} })
			check(t, err)
			grouped = grouped.OrderBy(orm.GroupKey(group.Region).Asc().NullsLast())
			actual, err := grouped.All(ctx)
			check(t, err)
			want := []result{{pointer("east"), 2, 2, 1}, {nil, 6, 3, 1}}
			if !reflect.DeepEqual(actual, want) {
				t.Fatal("optional forward count lost NULL roots or counted NULL", actual)
			}
			dynamic, err := api.RecordsItem.GroupValues([]string{"group__region"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}, {Kind: query.ResultCount, Field: "group__name"}, {Kind: query.ResultCount, Field: "group__name", Distinct: true}})
			check(t, err)
			dynamic = dynamic.OrderByDynamic(orm.GroupOrderInput{Column: 0, Direction: query.Ascending, Nulls: query.NullsLast})
			if !dynamic.Plan().Equal(grouped.Plan()) {
				t.Fatal("forward metadata differs across surfaces")
			}
			page, err := grouped.Page(ctx, 2, 0)
			check(t, err)
			if page.Total != 2 || !reflect.DeepEqual(page.Rows, want) {
				t.Fatal("typed forward page", page)
			}
		})
		t.Run("page_snapshot_and_cache", func(t *testing.T) {
			grouped := groupedReferenceQuery(t, api.RecordsItem)
			warm := groupedResult(t, ctx, grouped, groupedBaseNames)
			second, err := open()
			check(t, err)
			defer func() { check(t, second.Close()) }()
			writes := 0
			probe.afterFirstRow = func() error {
				writes++
				_, err := records.ItemObjects.Using(second).Filter(fields.ID.Exact(8)).Update(ctx, orm.Assign(fields.Rank, int64(9)))
				return err
			}
			before := len(probe.plans)
			page, err := grouped.Page(ctx, 10, 0)
			probe.afterFirstRow = nil
			check(t, err)
			if page.Total != 4 || len(page.Rows) != 4 || len(probe.plans) != before+1 || writes != 1 {
				t.Fatal("page split its count/rows snapshot", page.Total, len(page.Rows), writes)
			}
			expected, err := json.Marshal(warm)
			check(t, err)
			equalGroupedJSON(t, groupedCanonicalRows(t, page.Rows, groupedBaseNames), expected)
			if count, err := grouped.Count(ctx); err != nil || count != 4 {
				t.Fatal("page changed warm group cache", count, err)
			}
			fresh, err := grouped.Page(ctx, 2, 0)
			check(t, err)
			if fresh.Total != 5 || len(fresh.Rows) != 2 {
				t.Fatal("page did not read fresh database state", fresh)
			}
			zero, err := grouped.Page(ctx, 0, 0)
			check(t, err)
			past, err := grouped.Page(ctx, 2, 99)
			check(t, err)
			if zero.Total != 5 || past.Total != 5 || len(zero.Rows) != 0 || len(past.Rows) != 0 {
				t.Fatal("empty page dropped total", zero, past)
			}
			filtered := grouped.Having(orm.CountRows[records.Item]().GreaterThan(1))
			filteredPage, err := filtered.Page(ctx, 1, 0)
			check(t, err)
			if filteredPage.Total != 3 || len(filteredPage.Rows) != 1 {
				t.Fatal("HAVING total", filteredPage)
			}
			_, err = records.ItemObjects.Using(second).Filter(fields.ID.Exact(8)).Update(ctx, orm.AssignNull(fields.Rank))
			check(t, err)
		})
		t.Run("native_ordered_codecs", func(t *testing.T) {
			inputs := []orm.DynamicAggregateInput{{Kind: query.ResultMin, Field: "binary"}, {Kind: query.ResultMax, Field: "identity"}, {Kind: query.ResultMin, Field: "price"}, {Kind: query.ResultMin, Field: "date"}, {Kind: query.ResultMin, Field: "time"}, {Kind: query.ResultMin, Field: "stamp"}, {Kind: query.ResultMin, Field: "duration"}, {Kind: query.ResultMin, Field: "score"}}
			grouped, err := api.RecordsItem.GroupValues([]string{"enabled"}, inputs)
			check(t, err)
			grouped = grouped.OrderBy(fields.Enabled.Asc())
			page, err := grouped.Page(ctx, 2, 0)
			check(t, err)
			if page.Total != 2 {
				t.Fatal(page)
			}
			names := []string{"enabled", "binary", "identity", "price", "date", "time", "stamp", "duration", "score"}
			want := json.RawMessage(`[{"enabled":false,"binary":"0000ff","identity":"00000000-0000-0000-0000-000000000001","price":"0.25","date":"2026-01-01","time":"12:30:00.123456","stamp":"2026-01-01T12:30:00+00:00","duration":0,"score":0.5},{"enabled":true,"binary":"0000ff","identity":"00000000-0000-0000-0000-000000000001","price":"0.25","date":"2026-01-01","time":"12:30:00.123456","stamp":"2026-01-01T12:30:00+00:00","duration":0,"score":0.5}]`)
			equalGroupedJSON(t, groupedCanonicalRows(t, page.Rows, names), want)
			minimum, err := decimal.Parse("0.25")
			check(t, err)
			filtered := grouped.Having(orm.Min(fields.Price).Exact(orm.Some(minimum)))
			if count, err := filtered.Count(ctx); err != nil || count != 2 {
				t.Fatal("physical aggregate comparison", count, err)
			}
			type values struct {
				Binary orm.Optional[binaryvalue.Value]
				UUID   orm.Optional[uuid.UUID]
				Price  orm.Optional[decimal.Decimal]
				Date   orm.Optional[calendar.Date]
			}
			typed, err := project.GroupRecordsItemBy(api.RecordsItem, orm.Project1(fields.Enabled, func(v bool) bool { return v }), orm.Aggregate4(orm.Min(fields.Binary), orm.Max(fields.Identity), orm.Min(fields.Price), orm.Min(fields.Date), func(a orm.Optional[binaryvalue.Value], b orm.Optional[uuid.UUID], c orm.Optional[decimal.Decimal], d orm.Optional[calendar.Date]) values {
				return values{a, b, c, d}
			}), func(_ bool, v values) values { return v })
			check(t, err)
			typedPage, err := typed.Page(ctx, 2, 0)
			check(t, err)
			if len(typedPage.Rows) != 2 {
				t.Fatal(typedPage)
			}
			for _, row := range typedPage.Rows {
				binary, valid := row.Binary.Get()
				if !valid || !reflect.DeepEqual(binary.Bytes(), []byte{0, 0, 255}) {
					t.Fatal(row)
				}
				price, valid := row.Price.Get()
				if !valid || price != minimum || !row.UUID.Valid() || !row.Date.Valid() {
					t.Fatal(row)
				}
			}
		})
		t.Run("source_guards_before_io", func(t *testing.T) {
			before := len(probe.plans)
			limited, err := api.RecordsItem.Limit(0)
			check(t, err)
			for _, source := range []project.RecordsItemQuery{limited, api.RecordsItem.OrderBy(fields.Amount.Asc()), api.RecordsItem.SelectForUpdate(orm.RowLockOptions{})} {
				if _, err := source.GroupValues([]string{"rank"}, groupedInputs()); err == nil {
					t.Fatal("unsupported source state")
				}
			}
			for _, keys := range [][]string{nil, {"rank", "rank"}, {"rank; DROP TABLE x"}, {"document"}} {
				if _, err := api.RecordsItem.GroupValues(keys, groupedInputs()); err == nil {
					t.Fatal("invalid key accepted", keys)
				}
			}
			grouped := groupedReferenceQuery(t, api.RecordsItem)
			for _, invalid := range []orm.GroupedQuery[records.Item, orm.GroupRow]{grouped.Having(orm.Max(fields.ID).GreaterThan(orm.Some(int64(1)))), grouped.OrderBy(fields.Name.Asc()), grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: query.LookupGreaterThan, Value: "1"})} {
				if _, err := invalid.All(ctx); err == nil {
					t.Fatal("invalid selector accepted")
				}
			}
			if _, err := grouped.Page(ctx, -1, 0); err == nil {
				t.Fatal("negative page")
			}
			if _, err := grouped.Page(ctx, 1, -1); err == nil {
				t.Fatal("negative offset")
			}
			sliced, err := grouped.Limit(1)
			check(t, err)
			if _, err := sliced.Page(ctx, 1, 0); err == nil {
				t.Fatal("page accepted presliced group")
			}
			var uninitialized project.RecordsItemQuery
			if _, err := uninitialized.GroupValues([]string{"rank"}, groupedInputs()); err == nil {
				t.Fatal("zero facade group")
			}
			binding, err := project.Bind()
			check(t, err)
			bound, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "records", ModelName: "item"}, records.ItemDescriptor{})
			check(t, err)
			if _, err := orm.GroupValuesIn(orm.NewManager[records.Item](groupedForeignDescriptor{}).Using(probe), bound, []string{"rank"}, groupedInputs()); err == nil {
				t.Fatal("dynamic project binding accepted a foreign source descriptor/table")
			}
			if _, err := orm.GroupValuesIn(records.ItemObjects.Using(probe), orm.BoundModel[records.Item]{}, []string{"rank"}, groupedInputs()); err == nil {
				t.Fatal("dynamic project binding accepted an unbound model")
			}
			if len(probe.plans) != before {
				t.Fatal("invalid construction performed database query")
			}
		})
		t.Run("borrowed_scope_and_cancellation", func(t *testing.T) {
			var held, childHeld orm.GroupedQuery[records.Item, orm.GroupRow]
			check(t, backend.Atomic(ctx, func(session db.Session) error {
				bound, err := project.UsingSession(session)
				if err != nil {
					return err
				}
				held = groupedReferenceQuery(t, bound.RecordsItem)
				if _, err := held.All(ctx); err != nil {
					return err
				}
				if err := db.WithSavepoint(ctx, session, func(child db.Session) error {
					if rows, err := held.All(ctx); err == nil || rows != nil {
						t.Fatal("suspended parent served cached groups")
					}
					if _, err := held.Count(ctx); err == nil {
						t.Fatal("suspended parent served count")
					}
					if _, err := held.Page(ctx, 1, 0); err == nil {
						t.Fatal("suspended parent served page")
					}
					childFacade, err := project.UsingSession(child)
					if err != nil {
						return err
					}
					childHeld = groupedReferenceQuery(t, childFacade.RecordsItem)
					_, err = childHeld.All(ctx)
					return err
				}); err != nil {
					return err
				}
				if _, err := childHeld.Count(ctx); err == nil {
					t.Fatal("expired child served group cache")
				}
				n, err := held.Count(ctx)
				if err == nil && n != 4 {
					t.Fatal(n)
				}
				return err
			}))
			if _, err := held.All(ctx); err == nil {
				t.Fatal("expired parent served groups")
			}
			if _, err := held.Count(ctx); err == nil {
				t.Fatal("expired parent served count")
			}
			if _, err := held.Page(ctx, 1, 0); err == nil {
				t.Fatal("expired parent served page")
			}
			grouped := groupedReferenceQuery(t, api.RecordsItem)
			_, err := grouped.All(ctx)
			check(t, err)
			before := len(probe.plans)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := grouped.All(cancelled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := grouped.Count(cancelled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := grouped.Page(cancelled, 2, 0); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if len(probe.plans) != before {
				t.Fatal("cancelled grouped terminal queried")
			}
		})
		t.Run("read_only_transaction", func(t *testing.T) {
			var held orm.GroupedQuery[records.Item, int64]
			check(t, backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
				var err error
				held, err = orm.GroupBy(records.ItemObjects.Using(reader), orm.Project1(fields.Rank, func(v *int64) *int64 { return v }), orm.Aggregate1(orm.CountRows[records.Item](), func(n int64) int64 { return n }), func(_ *int64, n int64) int64 { return n })
				if err != nil {
					return err
				}
				page, err := held.Page(ctx, 10, 0)
				if err != nil {
					return err
				}
				if page.Total != 4 || len(page.Rows) != 4 {
					t.Fatal(page)
				}
				_, err = held.All(ctx)
				return err
			}))
			if _, err := held.Count(ctx); err == nil {
				t.Fatal("expired read-only snapshot served cache")
			}
		})
	})
}

type groupedForeignDescriptor struct{ records.ItemDescriptor }

func (groupedForeignDescriptor) Metadata() ir.Model {
	value := (records.ItemDescriptor{}).Metadata()
	value.DBTable = "foreign_items"
	return value
}
