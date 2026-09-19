package nullableforwardproduct

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// EagerJoinInput contains only public query inputs, independent of oracle output.
type EagerJoinInput struct {
	Selected   string `json:"selected"`
	Expression Node   `json:"expression"`
	Distinct   bool   `json:"distinct"`
	Offset     int    `json:"offset"`
	Limit      *int   `json:"limit"`
}
type EagerJoinTarget struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Nickname *string `json:"nickname"`
	Active   bool    `json:"active"`
}
type EagerJoinRow struct {
	ID     int64            `json:"id"`
	Target *EagerJoinTarget `json:"target"`
}
type EagerJoinObservation struct {
	EagerJoinInput
	Name        string         `json:"name"`
	Count       int64          `json:"count"`
	First       *EagerJoinRow  `json:"first"`
	Rows        []EagerJoinRow `json:"rows"`
	WarmCount   int64          `json:"warm_count"`
	WarmFirst   *EagerJoinRow  `json:"warm_first"`
	CountSQL    []string       `json:"count_sql"`
	FirstSQL    []string       `json:"first_sql"`
	AllSQL      []string       `json:"all_sql"`
	WarmQueries int            `json:"warm_queries"`
}
type EagerJoinReference struct {
	Django       string                 `json:"django"`
	Leaves       map[string]Leaf        `json:"leaves"`
	Observations []EagerJoinObservation `json:"observations"`
}

func EagerJoinPlan(leaves map[string]Leaf, input EagerJoinInput) (query.Plan, error) {
	expression, err := expressionFromInputs(leaves, input.Expression, eagerJoinCondition)
	if err != nil {
		return query.Plan{}, err
	}
	source := SourceFields()
	plan, err := query.NewPlan("join_reference_post", source).WithOrderings(query.NewOrdering(source[0], query.Ascending)).WithWhere(expression)
	if err != nil {
		return query.Plan{}, err
	}
	if input.Distinct {
		plan = plan.WithDistinct()
	}
	plan, err = plan.WithOffset(input.Offset)
	if err != nil {
		return query.Plan{}, err
	}
	if input.Limit != nil {
		plan, err = plan.WithLimit(*input.Limit)
		if err != nil {
			return query.Plan{}, err
		}
	}
	var sourceKey query.FieldRef
	switch input.Selected {
	case "author":
		sourceKey = source[2]
	case "reviewer":
		sourceKey = source[3]
	default:
		return query.Plan{}, fmt.Errorf("unknown selected input %q", input.Selected)
	}
	target := []query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, false), query.NewFieldRef("name", "name", query.FieldString, false), query.NewFieldRef("nickname", "nickname", query.FieldString, true), query.NewFieldRef("active", "active", query.FieldBoolean, false)}
	projection, err := query.NewForwardRelationProjection(ir.ModelIdentity{AppLabel: "join_reference", ModelName: "post"}, "join_reference_post", sourceKey, ir.ModelIdentity{AppLabel: "join_reference", ModelName: "person"}, "join_reference_person", target[0], target)
	if err != nil {
		return query.Plan{}, err
	}
	return plan.WithRelationProjections(projection)
}

// EagerJoinValue returns the actual scalar/list input for public dynamic APIs.
func EagerJoinValue(leaf Leaf) (any, error) {
	var value any
	err := json.Unmarshal(leaf.Value, &value)
	return value, err
}

func eagerJoinCondition(leaf Leaf) (query.Condition, error) {
	post := ir.ModelIdentity{AppLabel: "join_reference", ModelName: "post"}
	person := ir.ModelIdentity{AppLabel: "join_reference", ModelName: "person"}
	if leaf.Path == "title" {
		var value string
		if err := json.Unmarshal(leaf.Value, &value); err != nil {
			return query.Condition{}, err
		}
		return query.NewCondition(SourceFields()[1], query.LookupExact, query.String(value)), nil
	}
	if leaf.Path == "comments__body" {
		path, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "join_reference", ModelName: "comment"}, "join_reference_comment", "post", "post_id", post, "join_reference_post", "id", "comments", false, query.NewFieldRef("body", "body", query.FieldString, false))
		if err != nil {
			return query.Condition{}, err
		}
		var value string
		if err := json.Unmarshal(leaf.Value, &value); err != nil {
			return query.Condition{}, err
		}
		return query.NewRelatedCondition(path, query.LookupExact, query.String(value)), nil
	}
	if leaf.Path == "reviewer__isnull" {
		path, err := query.NewNullableForwardRelationIsNullPath(post, "join_reference_post", SourceFields()[3], person, "join_reference_person", "id")
		if err != nil {
			return query.Condition{}, err
		}
		var value bool
		if err := json.Unmarshal(leaf.Value, &value); err != nil {
			return query.Condition{}, err
		}
		return query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(value)), nil
	}
	normalized := leaf
	if len(strings.Split(leaf.Path, "__")) == 2 {
		normalized.Path += "__exact"
	}
	field, lookup, raw, err := LookupInput(normalized)
	if err != nil {
		return query.Condition{}, err
	}
	relation, _, _ := strings.Cut(leaf.Path, "__")
	path, err := query.NewForwardRelationPath(post, "join_reference_post", relation, relation+"_id", person, "join_reference_person", "id", relation == "reviewer", field)
	if err != nil {
		return query.Condition{}, err
	}
	if lookup == query.LookupIn {
		inputs := raw.([]any)
		values := make([]query.Value, len(inputs))
		for i, item := range inputs {
			values[i], err = lookupValue(item)
			if err != nil {
				return query.Condition{}, err
			}
		}
		return query.NewRelatedInCondition(path, values)
	}
	value, err := lookupValue(raw)
	if err != nil {
		return query.Condition{}, err
	}
	return query.NewRelatedCondition(path, lookup, value), nil
}

// EvaluateEagerJoin exercises real SQL compilation and scanning. ORM cache and
// generated surfaces are checked independently in the generated consumer.
func EvaluateEagerJoin(ctx context.Context, backend db.Queryer, plan query.Plan) (int64, *EagerJoinRow, []EagerJoinRow, error) {
	return evaluateEagerRows(ctx, backend, plan, eagerJoinRows)
}

func evaluateEagerRows[R any](ctx context.Context, backend db.Queryer, plan query.Plan, read func(context.Context, db.Queryer, query.Plan) ([]R, error)) (int64, *R, []R, error) {
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		return 0, nil, nil, err
	}
	countPlan, err := plan.WithoutRelationProjections().WithResultShape(shape)
	if err != nil {
		return 0, nil, nil, err
	}
	rows, err := backend.Query(ctx, countPlan)
	if err != nil {
		return 0, nil, nil, err
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
	if err != nil {
		return 0, nil, nil, err
	}
	firstPlan := plan
	if limit, set := plan.Limit(); !set || limit > 1 {
		firstPlan, err = plan.WithLimit(1)
		if err != nil {
			return 0, nil, nil, err
		}
	}
	firstRows, err := read(ctx, backend, firstPlan)
	if err != nil {
		return 0, nil, nil, err
	}
	if len(firstRows) > 1 {
		return 0, nil, nil, fmt.Errorf("First returned extra rows")
	}
	var first *R
	if len(firstRows) == 1 {
		first = &firstRows[0]
	}
	all, err := read(ctx, backend, plan)
	return count, first, all, err
}

func eagerJoinRows(ctx context.Context, backend db.Queryer, plan query.Plan) ([]EagerJoinRow, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := make([]EagerJoinRow, 0)
	for rows.Next() {
		var id, author int64
		var title string
		var reviewer, targetID sql.NullInt64
		var name, nickname sql.NullString
		var active sql.NullBool
		if err = rows.Scan(&id, &title, &author, &reviewer, &targetID, &name, &nickname, &active); err != nil {
			break
		}
		row := EagerJoinRow{ID: id}
		if targetID.Valid {
			if !name.Valid || !active.Valid {
				err = fmt.Errorf("incomplete selected target")
				break
			}
			row.Target = &EagerJoinTarget{ID: targetID.Int64, Name: name.String, Active: active.Bool}
			if nickname.Valid {
				row.Target.Nickname = &nickname.String
			}
		} else if name.Valid || nickname.Valid || active.Valid {
			err = fmt.Errorf("partial absent target")
			break
		}
		result = append(result, row)
	}
	err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err())
	if err != nil {
		return nil, err
	}
	return result, nil
}
