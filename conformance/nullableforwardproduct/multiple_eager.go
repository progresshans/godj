package nullableforwardproduct

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// MultipleEagerInput is deliberately independent of expected rows and SQL.
type MultipleEagerInput struct {
	Selected   []string `json:"selected"`
	Expression Node     `json:"expression"`
	Distinct   bool     `json:"distinct"`
	Offset     int      `json:"offset"`
	Limit      *int     `json:"limit"`
}
type MultipleEagerTarget struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Nickname *string `json:"nickname"`
	Active   bool    `json:"active"`
	Label    string  `json:"label"`
}
type MultipleEagerRow struct {
	ID      int64                           `json:"id"`
	Targets map[string]*MultipleEagerTarget `json:"targets"`
}
type MultipleEagerObservation struct {
	MultipleEagerInput
	Name        string             `json:"name"`
	Count       int64              `json:"count"`
	First       *MultipleEagerRow  `json:"first"`
	Rows        []MultipleEagerRow `json:"rows"`
	WarmCount   int64              `json:"warm_count"`
	WarmFirst   *MultipleEagerRow  `json:"warm_first"`
	CountSQL    []string           `json:"count_sql"`
	FirstSQL    []string           `json:"first_sql"`
	AllSQL      []string           `json:"all_sql"`
	WarmQueries int                `json:"warm_queries"`
}
type MultipleEagerReference struct {
	Django       string                     `json:"django"`
	Leaves       map[string]Leaf            `json:"leaves"`
	Observations []MultipleEagerObservation `json:"observations"`
}

func MultipleEagerPlan(leaves map[string]Leaf, input MultipleEagerInput) (query.Plan, error) {
	where, err := expressionFromInputs(leaves, input.Expression, eagerJoinCondition)
	if err != nil {
		return query.Plan{}, err
	}
	fields := append(SourceFields(), query.NewFieldRef("team", "team_id", query.FieldInteger, true))
	plan, err := query.NewPlan("join_reference_post", fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending)).WithWhere(where)
	if err != nil {
		return query.Plan{}, err
	}
	if input.Distinct {
		plan = plan.WithDistinct()
	}
	if plan, err = plan.WithOffset(input.Offset); err != nil {
		return query.Plan{}, err
	}
	if input.Limit != nil {
		if plan, err = plan.WithLimit(*input.Limit); err != nil {
			return query.Plan{}, err
		}
	}
	projections := make([]query.RelationProjection, 0, len(input.Selected))
	for _, name := range input.Selected {
		var key query.FieldRef
		model := "person"
		columns := []query.FieldRef{fields[0], query.NewFieldRef("name", "name", query.FieldString, false), query.NewFieldRef("nickname", "nickname", query.FieldString, true), query.NewFieldRef("active", "active", query.FieldBoolean, false)}
		switch name {
		case "author":
			key = fields[2]
		case "reviewer":
			key = fields[3]
		case "team":
			key = fields[4]
			model = "team"
			columns = []query.FieldRef{fields[0], query.NewFieldRef("label", "label", query.FieldString, false)}
		default:
			return query.Plan{}, fmt.Errorf("unknown selected input %q", name)
		}
		projection, err := query.NewForwardRelationProjection(ir.ModelIdentity{AppLabel: "join_reference", ModelName: "post"}, "join_reference_post", key, ir.ModelIdentity{AppLabel: "join_reference", ModelName: model}, "join_reference_"+model, fields[0], columns)
		if err != nil {
			return query.Plan{}, err
		}
		projections = append(projections, projection)
	}
	return plan.WithRelationProjections(projections...)
}

func EvaluateMultipleEager(ctx context.Context, backend db.Queryer, plan query.Plan) (int64, *MultipleEagerRow, []MultipleEagerRow, error) {
	return evaluateEagerRows(ctx, backend, plan, multipleEagerRows)
}

// Scan each actual database cell; target identity is taken from the plan, never
// from oracle output. The generated consumer independently checks typed scans.
func multipleEagerRows(ctx context.Context, backend db.Queryer, plan query.Plan) ([]MultipleEagerRow, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := make([]MultipleEagerRow, 0)
	for rows.Next() {
		var id, author int64
		var title string
		var reviewer, team sql.NullInt64
		type targetCells struct {
			id                    sql.NullInt64
			name, nickname, label sql.NullString
			active                sql.NullBool
		}
		projections := plan.RelationProjections()
		cells := make([]targetCells, len(projections))
		destinations := []any{&id, &title, &author, &reviewer, &team}
		for i, projection := range projections {
			cell := &cells[i]
			if projection.TerminalHop().Field() == "team" {
				destinations = append(destinations, &cell.id, &cell.label)
			} else {
				destinations = append(destinations, &cell.id, &cell.name, &cell.nickname, &cell.active)
			}
		}
		if err = rows.Scan(destinations...); err != nil {
			break
		}
		row := MultipleEagerRow{ID: id, Targets: make(map[string]*MultipleEagerTarget, len(cells))}
		for i, projection := range projections {
			cell := cells[i]
			name := projection.TerminalHop().Field()
			if !cell.id.Valid {
				if cell.name.Valid || cell.nickname.Valid || cell.label.Valid || cell.active.Valid {
					err = fmt.Errorf("partial absent %s", name)
					break
				}
				row.Targets[name] = nil
				continue
			}
			if name == "team" && !cell.label.Valid || name != "team" && (!cell.name.Valid || !cell.active.Valid) {
				err = fmt.Errorf("partial present %s", name)
				break
			}
			target := &MultipleEagerTarget{ID: cell.id.Int64, Name: cell.name.String, Active: cell.active.Bool, Label: cell.label.String}
			if cell.nickname.Valid {
				nickname := cell.nickname.String
				target.Nickname = &nickname
			}
			row.Targets[name] = target
		}
		if err != nil {
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
