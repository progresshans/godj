package querytest

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Fixed inputs are independent of the captured expected results and ORM writes.
func CreateNestedFixture(t *testing.T, table func(string) string, datetime string, exec func(string) error) {
	t.Helper()
	o, team, p, post, c := table("nested_reference_organization"), table("nested_reference_team"), table("nested_reference_person"), table("nested_reference_post"), table("nested_reference_comment")
	for _, statement := range []string{
		`CREATE TABLE ` + o + `(id BIGINT PRIMARY KEY,name TEXT NULL,active BOOLEAN NOT NULL,updated ` + datetime + ` NULL)`,
		`CREATE TABLE ` + team + `(id BIGINT PRIMARY KEY,label TEXT NOT NULL,organization_id BIGINT NOT NULL REFERENCES ` + o + `(id),parent_id BIGINT NULL REFERENCES ` + team + `(id))`,
		`CREATE TABLE ` + p + `(id BIGINT PRIMARY KEY,name TEXT NOT NULL,points BIGINT NULL,team_id BIGINT NOT NULL REFERENCES ` + team + `(id),backup_id BIGINT NULL REFERENCES ` + team + `(id),manager_id BIGINT NULL REFERENCES ` + p + `(id))`,
		`CREATE TABLE ` + post + `(id BIGINT PRIMARY KEY,title TEXT NOT NULL,author_id BIGINT NOT NULL REFERENCES ` + p + `(id),reviewer_id BIGINT NULL REFERENCES ` + p + `(id))`,
		`CREATE TABLE ` + c + `(id BIGINT PRIMARY KEY,post_id BIGINT NOT NULL REFERENCES ` + post + `(id),body TEXT NOT NULL)`,
		`INSERT INTO ` + o + ` VALUES(1,'North',TRUE,'2000-01-01 00:00:00.000000'),(2,'South',FALSE,'2001-01-01 00:00:00.000000'),(3,NULL,TRUE,NULL)`,
		`INSERT INTO ` + team + ` VALUES(1,'Red',1,NULL),(2,'Blue',2,NULL),(3,'Green',3,NULL),(4,'Quiet',1,NULL)`,
		`UPDATE ` + team + ` SET parent_id=CASE id WHEN 1 THEN 3 WHEN 2 THEN 1 WHEN 3 THEN 2 ELSE NULL END`,
		`INSERT INTO ` + p + ` VALUES(1,'Ada',0,1,NULL,NULL),(2,'Bob',2,2,1,NULL),(3,'Cleo',NULL,3,2,NULL),(4,'Dana',-1,4,3,NULL)`,
		`UPDATE ` + p + ` SET manager_id=CASE id WHEN 1 THEN 2 WHEN 2 THEN 1 WHEN 3 THEN 2 ELSE NULL END`,
		`INSERT INTO ` + post + ` VALUES(1,'keep',1,NULL),(2,'drop',2,NULL),(3,'keep',1,1),(4,'drop',2,1),(5,'keep',1,2),(6,'drop',2,2),(7,'keep',3,3),(8,'drop',4,4)`,
		`INSERT INTO ` + c + ` VALUES(1,1,'match'),(2,1,'match'),(3,2,'match'),(4,3,'other'),(5,4,'match'),(6,4,'match'),(7,5,'match'),(8,7,'match'),(9,8,'match')`,
	} {
		if err := exec(statement); err != nil {
			t.Fatalf("nested fixture: %v", err)
		}
	}
}

func CheckNestedReference(t *testing.T, backend db.Queryer, compile func(query.Plan) (string, []any, error), counts func() (uint64, uint64)) {
	t.Helper()
	data, err := os.ReadFile("../../orm/testdata/nested-forward-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.NestedReference
	if err = json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 146 {
		t.Fatal("nested reference roster incomplete")
	}
	seen := map[string]bool{}
	for _, observation := range reference.Observations {
		if seen[observation.Name] {
			t.Fatal("duplicate reference", observation.Name)
		}
		seen[observation.Name] = true
		t.Run(observation.Name, func(t *testing.T) {
			plan, err := nullableforwardproduct.NestedPlan(reference.Leaves, observation.NestedInput)
			if err != nil {
				t.Fatal(err)
			}
			beforeReads, beforeTotal := counts()
			count, first, rows, err := nullableforwardproduct.EvaluateNested(t.Context(), backend, plan)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(rows))
			targets := make([]map[string]*nullableforwardproduct.NestedTarget, len(rows))
			for i, row := range rows {
				ids[i], targets[i] = row.ID, row.Targets
			}
			var firstID *int64
			if first != nil {
				firstID = &first.ID
			}
			if count != observation.Count || !reflect.DeepEqual(firstID, observation.First) || !reflect.DeepEqual(ids, observation.IDs) || !reflect.DeepEqual(targets, observation.Targets) {
				t.Fatalf("Count=%d First=%v IDs=%v targets=%v; want Count=%d First=%v IDs=%v targets=%v", count, firstID, ids, targets, observation.Count, observation.First, observation.IDs, observation.Targets)
			}
			wantReads := uint64(len(observation.AllSQL) + len(observation.FirstSQL) + len(observation.CountSQL))
			reads, total := counts()
			if reads-beforeReads != wantReads || wantReads == 0 && total != beforeTotal {
				t.Fatalf("SQL reads=%d total=%d want reads=%d", reads-beforeReads, total-beforeTotal, wantReads)
			}
			statement, _, err := compile(plan)
			if err != nil {
				t.Fatal(err)
			}
			if len(observation.AllSQL) > 0 && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.AllSQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("outer JOIN semantics differ:\n%s\n%s", statement, observation.AllSQL[0])
			}
		})
	}
	for name, plan := range ConflictingNestedPlans(t) {
		t.Run("invalid/"+name, func(t *testing.T) {
			_, before := counts()
			statement, args, err := compile(plan)
			if statement != "" || len(args) != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatalf("conflicting compile=%q,%v,%v", statement, args, err)
			}
			rows, err := backend.Query(t.Context(), plan)
			if rows != nil {
				_ = rows.Close()
				t.Fatal("invalid query returned rows")
			}
			_, after := counts()
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || after != before {
				t.Fatalf("invalid query performed I/O: %v", err)
			}
		})
	}
}

func ConflictingNestedPlans(t *testing.T) map[string]query.Plan {
	t.Helper()
	result := map[string]query.Plan{}
	author, err := nullableforwardproduct.NestedCondition(nullableforwardproduct.Leaf{Path: "author__team__organization__name", Value: json.RawMessage(`"North"`)})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := nullableforwardproduct.NestedCondition(nullableforwardproduct.Leaf{Path: "reviewer__team__organization__name", Value: json.RawMessage(`"North"`)})
	if err != nil {
		t.Fatal(err)
	}
	path, _ := reviewer.RelationPath()
	for _, variant := range []string{"column", "nullable", "primary key", "table", "root identity"} {
		hops := path.Hops()
		for i, hop := range hops {
			source, target := hop.Source(), hop.Target()
			sourceTable, targetTable, column, pk, nullable := hop.SourceTable(), hop.TargetTable(), hop.SourceColumn(), hop.TargetPrimaryKeyColumn(), hop.Nullable()
			if variant == "column" && i == 1 {
				column = "other_team_id"
			}
			if variant == "nullable" && i == 2 {
				nullable = true
			}
			if variant == "primary key" && i == 1 {
				pk = "alternate_pk"
			}
			if variant == "root identity" && i == 0 {
				source = ir.ModelIdentity{AppLabel: "other", ModelName: "post"}
			}
			if variant == "table" {
				if i == 1 {
					targetTable = "other_team"
				}
				if i == 2 {
					sourceTable = "other_team"
				}
			}
			p, err := query.NewForwardRelationPath(source, sourceTable, hop.Field(), column, target, targetTable, pk, nullable, path.Terminal())
			if err != nil {
				t.Fatal(err)
			}
			hops[i] = p.Hops()[0]
		}
		changed, err := query.NewForwardRelationChain(hops, path.Terminal(), path.TerminalScope())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("nested_reference_post", nullableforwardproduct.SourceFields()).WithConditions(author, query.NewRelatedCondition(changed, query.LookupExact, query.String("North")))
		if err != nil {
			t.Fatal(err)
		}
		result[variant] = plan
		zero, err := plan.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		result[variant+" empty"] = zero
	}
	return result
}
