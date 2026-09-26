package querytest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// CreatePrefetchWindowFixture has no dependency on the Go compiler or captured
// expected values. The data mirrors the independently executed Django cases.
func CreatePrefetchWindowFixture(t *testing.T, table func(string) string, jsonType string, execute func(string) error) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE ` + table("slice_label") + ` (id BIGINT PRIMARY KEY, name TEXT NOT NULL, note TEXT NULL)`,
		`CREATE TABLE ` + table("slice_owner") + ` (id BIGINT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE ` + table("slice_link") + ` (id BIGINT PRIMARY KEY, owner_id BIGINT NULL REFERENCES ` + table("slice_owner") + ` (id), label_id BIGINT NULL REFERENCES ` + table("slice_label") + ` (id), token BIGINT NOT NULL)`,
		`INSERT INTO ` + table("slice_label") + ` VALUES (1,'a',NULL),(2,'b',NULL),(3,'c',NULL),(4,'d',NULL),(5,'e',NULL),(11,'a',NULL),(12,'b',NULL)`,
		`INSERT INTO ` + table("slice_owner") + ` VALUES (0,'zero'),(1,'first'),(2,'second'),(3,'empty'),(7,'duplicates'),(8,'reverse-first'),(9,'reverse-second'),(11,'scope-first'),(12,'scope-second'),(13,'scope-outside')`,
		`INSERT INTO ` + table("slice_link") + ` VALUES (1,1,1,1),(2,1,2,2),(3,1,3,3),(4,1,4,4),(5,2,2,5),(6,2,3,6),(7,2,4,7),(8,2,5,8),
          (11,7,1,1),(12,7,1,2),(13,7,2,3),(14,NULL,1,4),(15,7,NULL,5),
          (21,8,1,1),(22,8,2,2),(23,8,3,3),(24,9,1,4),(25,9,2,5),(26,9,3,6),
          (31,11,12,1),(32,13,11,2),(33,12,11,3),(34,12,12,4),(35,0,1,1)`,
		`CREATE TABLE ` + table("slice_json") + ` (id BIGINT PRIMARY KEY, owner_id BIGINT NOT NULL, payload ` + jsonType + ` NOT NULL)`,
		`INSERT INTO ` + table("slice_json") + ` VALUES (1,1,'{"rank":2}'),(2,1,'{"rank":1}'),(3,2,'{"rank":3}'),(4,2,'{"rank":10}')`,
	} {
		if err := execute(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func CheckPrefetchWindows(t *testing.T, backend db.Queryer, compile func(query.Plan) (string, []any, error), counts func() (uint64, uint64)) {
	t.Helper()
	data, err := os.ReadFile("../../orm/testdata/many-to-many-django61-sqlite.json")
	checkPrefetch(t, err)
	var reference struct {
		Observations struct {
			Slices map[string]json.RawMessage `json:"prefetch_slices"`
		} `json:"observations"`
	}
	checkPrefetch(t, json.Unmarshal(data, &reference))
	if len(reference.Observations.Slices) != 14 {
		t.Fatal("slice reference roster incomplete")
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	note := query.NewFieldRef("note", "note", query.FieldString, true)
	owner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, true)
	label := query.NewFieldRef("label", "label_id", query.FieldInteger, true)
	token := query.NewFieldRef("token", "token", query.FieldInteger, false)
	linkModel := ir.ModelIdentity{AppLabel: "slice", ModelName: "link"}
	labelModel := ir.ModelIdentity{AppLabel: "slice", ModelName: "label"}
	ownerModel := ir.ModelIdentity{AppLabel: "slice", ModelName: "owner"}
	raw, err := query.NewReverseRelationPath(linkModel, "slice_link", "label", "label_id", labelModel, "slice_label", "id", "links", true, owner, ir.RelationOneToMany)
	checkPrefetch(t, err)
	path, err := query.NewRelationChain(raw.Hops(), []query.FieldRef{id, id}, owner, query.RelationTerminalRelatedField)
	checkPrefetch(t, err)
	base := query.NewPlan("slice_label", []query.FieldRef{id, name, note}).WithOrderings(query.NewOrdering(name, query.Ascending))
	withSlice := func(plan query.Plan, offset, limit int) query.Plan {
		if offset >= 0 {
			plan, err = plan.WithOffset(offset)
			checkPrefetch(t, err)
		}
		if limit >= 0 {
			plan, err = plan.WithLimit(limit)
			checkPrefetch(t, err)
		}
		return plan
	}
	read := func(t *testing.T, plan query.Plan, cells int, wantReads uint64) [][]any {
		t.Helper()
		statement, _, err := compile(plan)
		checkPrefetch(t, err)
		if _, window := plan.PrefetchWindow(); window && (!strings.Contains(statement, "ROW_NUMBER() OVER (PARTITION BY") || strings.Contains(statement, " LIMIT ") || strings.Contains(statement, " OFFSET ")) {
			t.Fatal("owner slice compiled as global pagination", statement)
		}
		before, beforeTotal := counts()
		rows, err := backend.Query(t.Context(), plan)
		checkPrefetch(t, err)
		defer rows.Close()
		result := make([][]any, 0)
		for rows.Next() {
			values := make([]any, cells)
			pointers := make([]any, cells)
			for i := range values {
				pointers[i] = &values[i]
			}
			checkPrefetch(t, rows.Scan(pointers...))
			result = append(result, values)
		}
		checkPrefetch(t, rows.Err())
		checkPrefetch(t, rows.Close())
		after, afterTotal := counts()
		if after-before != wantReads || wantReads == 0 && afterTotal != beforeTotal {
			t.Fatalf("reads=%d total=%d want=%d", after-before, afterTotal-beforeTotal, wantReads)
		}
		return result
	}
	group := func(t *testing.T, rows [][]any, keys []int64) [][]string {
		t.Helper()
		result := make([][]string, len(keys))
		for i := range result {
			result[i] = []string{}
		}
		for _, row := range rows {
			key, ok := row[3].(int64)
			if !ok {
				t.Fatal("owner cell was not preserved", row)
			}
			member, ok := row[1].(string)
			if !ok {
				t.Fatal("target name was not preserved", row)
			}
			found := false
			for i, requested := range keys {
				if key == requested {
					result[i] = append(result[i], member)
					found = true
				}
			}
			if !found {
				t.Fatal("unrequested output owner escaped SQL qualification", row)
			}
		}
		return result
	}
	for _, test := range []struct {
		name          string
		offset, limit int
		descending    bool
	}{
		{"head", 0, 1, false}, {"middle", 1, 2, false}, {"tail", 2, -1, false}, {"empty", 0, 0, false}, {"beyond", 9, 1, false}, {"descending", 1, 2, true}, {"empty_batch", 0, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var expected struct {
				Members [][]string `json:"members"`
				Queries uint64     `json:"batch_queries"`
			}
			checkPrefetch(t, json.Unmarshal(reference.Observations.Slices[test.name], &expected))
			keys := []int64{1, 2, 1, 3}
			if test.name == "empty_batch" {
				keys = nil
			}
			plan := withSlice(base, test.offset, test.limit)
			if test.descending {
				plan = plan.WithOrderings(query.NewOrdering(name, query.Descending))
			}
			plan, err := plan.ForPrefetchOwners(path, keys)
			checkPrefetch(t, err)
			actual := group(t, read(t, plan, 4, expected.Queries), keys)
			if !reflect.DeepEqual(actual, expected.Members) {
				t.Fatalf("got %v want %v", actual, expected.Members)
			}
		})
	}
	t.Run("distinct_duplicates", func(t *testing.T) {
		plan := withSlice(base.WithDistinct(), 0, 2)
		plan, err := plan.ForPrefetchOwners(path, []int64{7})
		checkPrefetch(t, err)
		var expected struct {
			Members []string `json:"members"`
		}
		checkPrefetch(t, json.Unmarshal(reference.Observations.Slices["distinct_duplicates"], &expected))
		actual := group(t, read(t, plan, 4, 1), []int64{7})[0]
		if !reflect.DeepEqual(actual, expected.Members) {
			t.Fatal("DISTINCT changed ranked duplicate membership", actual)
		}
	})
	forward, err := query.NewForwardRelationPath(linkModel, "slice_link", "owner", "owner_id", ownerModel, "slice_owner", "id", true, name, ir.RelationManyToOne)
	checkPrefetch(t, err)
	ownerName, err := query.NewRelationChain(append(raw.Hops(), forward.Hops()...), []query.FieldRef{id, id, id}, name, query.RelationTerminalRelatedField)
	checkPrefetch(t, err)
	condition, err := query.NewRelatedInCondition(ownerName, []query.Value{query.String("scope-first"), query.String("scope-outside")})
	checkPrefetch(t, err)
	scoped := Conditions(t, base, condition)
	scoped = Conditions(t, scoped, query.NewRelatedCondition(ownerName, query.LookupExact, query.String("scope-second")))
	for index, name := range []string{"scope_head", "scope_tail"} {
		t.Run(name, func(t *testing.T) {
			plan, err := withSlice(scoped, index, 1).ForPrefetchOwners(path, []int64{11, 12})
			checkPrefetch(t, err)
			var expected struct {
				Members [][]string `json:"members"`
			}
			checkPrefetch(t, json.Unmarshal(reference.Observations.Slices[name], &expected))
			actual := group(t, read(t, plan, 4, 1), []int64{11, 12})
			if !reflect.DeepEqual(actual, expected.Members) {
				t.Fatalf("late owner boundary changed ranks: %v want %v", actual, expected.Members)
			}
		})
	}
	t.Run("reverse_eager", func(t *testing.T) {
		projection, err := query.NewForwardRelationProjection(linkModel, "slice_link", label, labelModel, "slice_label", id, []query.FieldRef{id, name, note}, ir.RelationManyToOne)
		checkPrefetch(t, err)
		plan := query.NewPlan("slice_link", []query.FieldRef{id, owner, label, token}).WithOrderings(query.NewOrdering(token, query.Descending))
		plan, err = plan.WithRelationProjections(projection)
		checkPrefetch(t, err)
		plan, err = withSlice(plan, 1, 1).ForPrefetchForeignKey(owner, []int64{8, 9})
		checkPrefetch(t, err)
		actual := [][][]any{{}, {}}
		for _, row := range read(t, plan, 7, 1) {
			key, ok := row[1].(int64)
			if !ok || key < 8 || key > 9 {
				t.Fatal("reverse owner cell", row)
			}
			actual[key-8] = append(actual[key-8], []any{row[3], row[5]})
		}
		var expected struct {
			Members json.RawMessage `json:"members"`
		}
		checkPrefetch(t, json.Unmarshal(reference.Observations.Slices["reverse_eager"], &expected))
		encoded, err := json.Marshal(actual)
		checkPrefetch(t, err)
		var want, got any
		checkPrefetch(t, json.Unmarshal(expected.Members, &want))
		checkPrefetch(t, json.Unmarshal(encoded, &got))
		if !reflect.DeepEqual(got, want) {
			t.Fatal("reverse eager row shape or slice", got, want)
		}
	})
	t.Run("zero_owner_unsorted_and_large_limit", func(t *testing.T) {
		for _, limit := range []int{1, int(^uint(0) >> 1)} {
			plan, err := withSlice(base.WithOrderings(), 0, limit).ForPrefetchOwners(path, []int64{0})
			checkPrefetch(t, err)
			if got := group(t, read(t, plan, 4, 1), []int64{0}); !reflect.DeepEqual(got, [][]string{{"a"}}) {
				t.Fatal(got)
			}
		}
		plan, err := withSlice(base, math.MaxInt32, int(^uint(0)>>1)).ForPrefetchOwners(path, []int64{0})
		checkPrefetch(t, err)
		if len(read(t, plan, 4, 1)) != 0 {
			t.Fatal("large offset wrapped")
		}
	})
	t.Run("json_sort_parameters_and_distinct", func(t *testing.T) {
		jsonOwner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
		payload := query.NewFieldRef("payload", "payload", query.FieldJSON, false)
		jsonPath, err := query.NewJSONPath(query.JSONKey("rank"))
		checkPrefetch(t, err)
		expression, err := query.JSONPathResult(payload, jsonPath)
		checkPrefetch(t, err)
		ordering, err := query.NewResultOrdering(expression, query.Ascending)
		checkPrefetch(t, err)
		for _, distinct := range []bool{false, true} {
			plan := Conditions(t, query.NewPlan("slice_json", []query.FieldRef{id, jsonOwner, payload}), query.NewCondition(id, query.LookupGreaterThan, query.Integer(0))).WithOrderings(ordering)
			if distinct {
				plan = plan.WithDistinct()
			}
			plan, err = withSlice(plan, 1, 1).ForPrefetchForeignKey(jsonOwner, []int64{1, 2})
			checkPrefetch(t, err)
			rows := read(t, plan, 3, 1)
			if len(rows) != 2 || rows[0][0] != int64(1) || rows[1][0] != int64(4) {
				t.Fatal("JSON ordering/bound parameter order changed", rows)
			}
		}
	})
	t.Run("empty_plan_and_cancellation_still_validate", func(t *testing.T) {
		plan, err := withSlice(base, 0, 0).ForPrefetchOwners(path, []int64{1})
		checkPrefetch(t, err)
		invalid := plan.WithOrderings(query.NewOrdering(query.NewFieldRef("foreign", "foreign", query.FieldString, false), query.Ascending))
		before, beforeTotal := counts()
		rows, err := backend.Query(t.Context(), invalid)
		if rows != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid empty plan escaped preflight", rows, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		rows, err = backend.Query(ctx, plan)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled empty plan published rows", err)
		}
		after, afterTotal := counts()
		if after != before || afterTotal != beforeTotal {
			t.Fatal("failed preflight reached the connection")
		}
	})
}

func checkPrefetch(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
