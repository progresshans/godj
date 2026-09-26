// Package nullableforwardproduct supplies driver-independent query inputs to
// real backend tests. It builds expressions from inputs, never expected rows.
package nullableforwardproduct

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type Leaf struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
	Type  string          `json:"type"`
}

type Node struct {
	Name     string
	Kind     string `json:"kind"`
	Children []Node `json:"children"`
}

func (n *Node) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &n.Name)
	}
	type plain Node
	return json.Unmarshal(data, (*plain)(n))
}

func SourceFields() []query.FieldRef {
	return []query.FieldRef{
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.NewFieldRef("title", "title", query.FieldString, false),
		query.NewFieldRef("author", "author_id", query.FieldInteger, false),
		query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true),
	}
}

func Plan(leaves map[string]Leaf, root Node) (query.Plan, error) {
	expression, err := Expression(leaves, root)
	if err != nil {
		return query.Plan{}, err
	}
	fields := SourceFields()
	return query.NewPlan("nullable_reference_post", fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending)).WithWhere(expression)
}

func Expression(leaves map[string]Leaf, node Node) (query.Expression, error) {
	return expressionFromInputs(leaves, node, Condition)
}

func expressionFromInputs(leaves map[string]Leaf, node Node, build func(Leaf) (query.Condition, error)) (query.Expression, error) {
	if node.Name != "" {
		leaf, ok := leaves[node.Name]
		if !ok {
			return query.Expression{}, fmt.Errorf("unknown reference input %q", node.Name)
		}
		condition, err := build(leaf)
		if err != nil {
			return query.Expression{}, err
		}
		return query.NewExpression(condition)
	}
	children := make([]query.Expression, len(node.Children))
	for i, child := range node.Children {
		value, err := expressionFromInputs(leaves, child, build)
		if err != nil {
			return query.Expression{}, err
		}
		children[i] = value
	}
	if node.Kind == "not" && len(children) == 1 {
		return query.NotExpression(children[0])
	}
	if len(children) >= 2 {
		switch node.Kind {
		case "and":
			return query.AndExpressions(children[0], children[1], children[2:]...)
		case "or":
			return query.OrExpressions(children[0], children[1], children[2:]...)
		}
	}
	return query.Expression{}, fmt.Errorf("invalid reference input expression %q", node.Kind)
}

func Condition(leaf Leaf) (query.Condition, error) {
	post := ir.ModelIdentity{AppLabel: "nullable_reference", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "nullable_reference", ModelName: "author"}
	if leaf.Path == "title" {
		var value string
		if err := json.Unmarshal(leaf.Value, &value); err != nil {
			return query.Condition{}, err
		}
		return query.NewCondition(SourceFields()[1], query.LookupExact, query.String(value)), nil
	}
	relation, terminal, found := strings.Cut(leaf.Path, "__")
	if !found || (relation != "author" && relation != "reviewer") {
		return query.Condition{}, fmt.Errorf("invalid input path %q", leaf.Path)
	}
	if terminal == "isnull" && relation == "reviewer" {
		path, err := query.NewForwardRelationIsNullPath(post, "nullable_reference_post", SourceFields()[3], author, "nullable_reference_author", "id", ir.RelationManyToOne)
		if err != nil {
			return query.Condition{}, err
		}
		var value bool
		if err := json.Unmarshal(leaf.Value, &value); err != nil {
			return query.Condition{}, err
		}
		return query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(value)), nil
	}
	var field query.FieldRef
	var value query.Value
	switch terminal {
	case "name", "bio":
		var text string
		if err := json.Unmarshal(leaf.Value, &text); err != nil {
			return query.Condition{}, err
		}
		field = query.NewFieldRef(terminal, terminal, query.FieldString, false)
		value = query.String(text)
	case "rank", "id":
		var integer int64
		if err := json.Unmarshal(leaf.Value, &integer); err != nil {
			return query.Condition{}, err
		}
		field = query.NewFieldRef(terminal, terminal, query.FieldInteger, false)
		value = query.Integer(integer)
	case "seen_at":
		var text string
		if err := json.Unmarshal(leaf.Value, &text); err != nil {
			return query.Condition{}, err
		}
		instant, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return query.Condition{}, err
		}
		field = query.NewFieldRef(terminal, terminal, query.FieldDateTime, false)
		value = query.DateTime(instant)
	default:
		return query.Condition{}, fmt.Errorf("invalid target input %q", terminal)
	}
	path, err := query.NewForwardRelationPath(post, "nullable_reference_post", relation, relation+"_id", author, "nullable_reference_author", "id", relation == "reviewer", field, ir.RelationManyToOne)
	if err != nil {
		return query.Condition{}, err
	}
	return query.NewRelatedCondition(path, query.LookupExact, value), nil
}

// Observation stores oracle outputs for tests. Plan and Expression accept only
// the input tree and leaves; expected rows never influence query construction.
type Observation struct {
	Name       string   `json:"name"`
	Expression Node     `json:"expression"`
	IDs        []int64  `json:"ids"`
	Count      int64    `json:"count"`
	SQL        []string `json:"sql"`
	CountSQL   []string `json:"count_sql"`
}

type Reference struct {
	Django       string          `json:"django"`
	Backend      string          `json:"backend"`
	Leaves       map[string]Leaf `json:"leaves"`
	Observations []Observation   `json:"observations"`
}

func Evaluate(ctx context.Context, backend db.Queryer, plan query.Plan) ([]int64, int64, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id, authorID int64
		var title string
		var reviewerID sql.NullInt64
		if err = rows.Scan(&id, &title, &authorID, &reviewerID); err != nil {
			break
		}
		ids = append(ids, id)
	}
	err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err())
	if err != nil {
		return nil, 0, err
	}
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		return nil, 0, err
	}
	countPlan, err := plan.WithResultShape(shape)
	if err != nil {
		return nil, 0, err
	}
	rows, err = backend.Query(ctx, countPlan)
	if err != nil {
		return nil, 0, err
	}
	var count int64
	if !rows.Next() {
		err = fmt.Errorf("COUNT returned no row")
	} else {
		err = rows.Scan(&count)
		if err == nil && rows.Next() {
			err = fmt.Errorf("COUNT returned extra rows")
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err())
	return ids, count, err
}
