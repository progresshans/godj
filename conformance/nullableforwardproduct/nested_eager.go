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
)

// RawMessage retains exact integer values and explicit NULL independently of
// the Go model types and the reference transport's indentation.
type NestedEagerNode map[string]json.RawMessage
type NestedEagerRow struct {
	Source  NestedEagerNode            `json:"source"`
	Targets map[string]NestedEagerNode `json:"targets"`
}
type NestedEagerObservation struct {
	NestedInput
	Name        string           `json:"name"`
	Count       int64            `json:"count"`
	First       *NestedEagerRow  `json:"first"`
	Rows        []NestedEagerRow `json:"rows"`
	WarmCount   int64            `json:"warm_count"`
	WarmFirst   *NestedEagerRow  `json:"warm_first"`
	WarmRows    []NestedEagerRow `json:"warm_rows"`
	WarmQueries int              `json:"warm_queries"`
	CountSQL    []string         `json:"count_sql"`
	FirstSQL    []string         `json:"first_sql"`
	AllSQL      []string         `json:"all_sql"`
}
type NestedEagerReference struct {
	Django       string                   `json:"django"`
	Leaves       map[string]Leaf          `json:"leaves"`
	Observations []NestedEagerObservation `json:"observations"`
}

// EagerNode encodes observed values, never expected data or selection inputs.
func EagerNode(values map[string]any) (NestedEagerNode, error) {
	result := make(NestedEagerNode, len(values))
	for name, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		result[name] = data
	}
	return result, nil
}

// EagerInstant matches the reference's aware datetime transport. The fixture
// uses UTC instants; fractional precision follows Django's microseconds.
func EagerInstant(instant *time.Time) any {
	if instant == nil {
		return nil
	}
	layout := "2006-01-02T15:04:05+00:00"
	if instant.Nanosecond() != 0 {
		layout = "2006-01-02T15:04:05.000000+00:00"
	}
	return instant.UTC().Format(layout)
}

func NestedEagerPlan(leaves map[string]Leaf, input NestedInput) (query.Plan, error) {
	base := input
	base.Selected = nil
	plan, err := NestedPlan(leaves, base)
	if err != nil {
		return query.Plan{}, err
	}
	models, err := nestedModels()
	if err != nil {
		return query.Plan{}, err
	}
	var projections []query.RelationProjection
	seen := map[string]bool{}
	for _, selected := range input.Selected {
		parts := strings.Split(selected, "__")
		for depth := 1; depth <= len(parts); depth++ {
			prefix := strings.Join(parts[:depth], "__")
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			path, key, _, err := nestedOperand(Leaf{Path: prefix + "__id"})
			if err != nil {
				return query.Plan{}, err
			}
			hops := path.Hops()
			if len(hops) == 0 {
				return query.Plan{}, fmt.Errorf("empty selected route %s", prefix)
			}
			columns := nestedFields(models[hops[len(hops)-1].Target()])
			projection, err := query.NewForwardChainProjection(hops, key, columns)
			if err != nil {
				return query.Plan{}, err
			}
			projections = append(projections, projection)
		}
	}
	return plan.WithRelationProjections(projections...)
}

func EvaluateNestedEager(ctx context.Context, backend db.Queryer, plan query.Plan) (int64, *NestedEagerRow, []NestedEagerRow, error) {
	return evaluateEagerRows(ctx, backend, plan, nestedEagerRows)
}

type nestedEagerCell struct {
	integer sql.NullInt64
	text    sql.NullString
	boolean sql.NullBool
}

func (cell *nestedEagerCell) destination(field query.FieldRef) any {
	switch field.Kind() {
	case query.FieldInteger:
		return &cell.integer
	case query.FieldBoolean:
		return &cell.boolean
	default:
		return &cell.text
	}
}
func (cell nestedEagerCell) value(field query.FieldRef) (any, bool, error) {
	switch field.Kind() {
	case query.FieldInteger:
		return cell.integer.Int64, cell.integer.Valid, nil
	case query.FieldBoolean:
		return cell.boolean.Bool, cell.boolean.Valid, nil
	case query.FieldDateTime:
		if !cell.text.Valid {
			return nil, false, nil
		}
		var instant time.Time
		var err error
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999"} {
			instant, err = time.Parse(layout, cell.text.String)
			if err == nil {
				return EagerInstant(&instant), true, nil
			}
		}
		return nil, false, fmt.Errorf("invalid observed datetime: %w", err)
	default:
		return cell.text.String, cell.text.Valid, nil
	}
}
func observedEagerNode(fields []query.FieldRef, cells []nestedEagerCell, primaryKey string) (NestedEagerNode, error) {
	values := make(map[string]any, len(fields))
	present := false
	nonNull := 0
	for i, field := range fields {
		value, valid, err := cells[i].value(field)
		if err != nil {
			return nil, err
		}
		if field.Column() == primaryKey {
			present = valid
		}
		if valid {
			nonNull++
			values[field.Column()] = value
		} else {
			values[field.Column()] = nil
		}
	}
	if !present {
		if nonNull > 0 {
			return nil, fmt.Errorf("partial absent selected node")
		}
		return nil, nil
	}
	for _, field := range fields {
		if !field.Nullable() && values[field.Column()] == nil {
			return nil, fmt.Errorf("incomplete selected node field %s", field.Name())
		}
	}
	return EagerNode(values)
}
func nestedEagerRows(ctx context.Context, backend db.Queryer, plan query.Plan) ([]NestedEagerRow, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := make([]NestedEagerRow, 0)
	projections := plan.RelationProjections()
	for rows.Next() {
		rootFields := plan.SourceFields()
		rootCells := make([]nestedEagerCell, len(rootFields))
		destinations := make([]any, 0, len(rootFields))
		for i, field := range rootFields {
			destinations = append(destinations, rootCells[i].destination(field))
		}
		targetCells := make([][]nestedEagerCell, len(projections))
		targetFields := make([][]query.FieldRef, len(projections))
		for i, projection := range projections {
			targetFields[i] = projection.TargetColumns()
			targetCells[i] = make([]nestedEagerCell, len(targetFields[i]))
			for j, field := range targetFields[i] {
				destinations = append(destinations, targetCells[i][j].destination(field))
			}
		}
		if err = rows.Scan(destinations...); err != nil {
			break
		}
		var row NestedEagerRow
		row.Source, err = observedEagerNode(rootFields, rootCells, "id")
		if err != nil {
			break
		}
		if row.Source == nil {
			err = fmt.Errorf("absent root")
			break
		}
		row.Targets = make(map[string]NestedEagerNode, len(projections))
		for i, projection := range projections {
			parts := make([]string, 0)
			for _, hop := range projection.Path().Hops() {
				parts = append(parts, hop.Field())
			}
			path := strings.Join(parts, "__")
			row.Targets[path], err = observedEagerNode(targetFields[i], targetCells[i], projection.TerminalHop().TargetPrimaryKeyColumn())
			if err != nil {
				break
			}
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
