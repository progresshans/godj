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

type NestedInput struct {
	Selected   []string `json:"selected"`
	Expression Node     `json:"expression"`
	Distinct   bool     `json:"distinct"`
	Offset     int      `json:"offset"`
	Limit      *int     `json:"limit"`
}
type NestedTarget struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Points  *int64 `json:"points"`
	Team    int64  `json:"team"`
	Backup  *int64 `json:"backup"`
	Manager *int64 `json:"manager"`
}
type NestedRow struct {
	ID      int64
	Targets map[string]*NestedTarget
}
type NestedObservation struct {
	NestedInput
	Name        string                     `json:"name"`
	IDs         []int64                    `json:"ids"`
	Targets     []map[string]*NestedTarget `json:"targets"`
	Count       int64                      `json:"count"`
	First       *int64                     `json:"first"`
	WarmCount   int64                      `json:"warm_count"`
	WarmFirst   *int64                     `json:"warm_first"`
	WarmQueries int                        `json:"warm_queries"`
	CountSQL    []string                   `json:"count_sql"`
	FirstSQL    []string                   `json:"first_sql"`
	AllSQL      []string                   `json:"all_sql"`
}
type NestedReference struct {
	Django       string              `json:"django"`
	Leaves       map[string]Leaf     `json:"leaves"`
	Observations []NestedObservation `json:"observations"`
}

func nestedModels() (map[ir.ModelIdentity]ir.Model, error) {
	schemas, err := NestedSchemas()
	if err != nil {
		return nil, err
	}
	models := map[ir.ModelIdentity]ir.Model{}
	for _, s := range schemas {
		for _, model := range s.Models {
			models[ir.ModelIdentity{AppLabel: s.AppLabel, ModelName: model.Name}] = model
		}
	}
	return models, nil
}
func nestedField(field ir.Field) query.FieldRef {
	kind := query.FieldInteger
	switch field.Kind {
	case ir.FieldChar, ir.FieldText:
		kind = query.FieldString
	case ir.FieldBoolean:
		kind = query.FieldBoolean
	case ir.FieldDateTime:
		kind = query.FieldDateTime
	}
	return query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
}
func nestedFields(model ir.Model) []query.FieldRef {
	fields := make([]query.FieldRef, len(model.Fields))
	for i, f := range model.Fields {
		fields[i] = nestedField(f)
	}
	return fields
}

// This input-only resolver builds raw Query AST from the independent schema. It
// does not call the ORM parser or use any expected rows, counts, joins or SQL.
func nestedOperand(leaf Leaf) (query.RelationPath, query.FieldRef, query.Lookup, error) {
	models, err := nestedModels()
	if err != nil {
		return query.RelationPath{}, query.FieldRef{}, "", err
	}
	identity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	model := models[identity]
	parts := strings.Split(leaf.Path, "__")
	lookup := query.LookupExact
	if len(parts) > 1 {
		switch query.Lookup(parts[len(parts)-1]) {
		case query.LookupExact, query.LookupIsNull, query.LookupIn, query.LookupIContains, query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
			lookup = query.Lookup(parts[len(parts)-1])
			parts = parts[:len(parts)-1]
		}
	}
	if len(parts) == 2 && parts[0] == "comments" {
		terminal := query.NewFieldRef("body", "body", query.FieldString, false)
		path, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "blog", ModelName: "comment"}, "nested_reference_comment", "post", "post_id", identity, model.DBTable, "id", "comments", false, terminal)
		return path, terminal, lookup, err
	}
	var hops []query.RelationHop
	for i, name := range parts {
		var field ir.Field
		found := false
		for _, candidate := range model.Fields {
			if candidate.Name == name {
				field = candidate
				found = true
				break
			}
		}
		if !found {
			return query.RelationPath{}, query.FieldRef{}, "", fmt.Errorf("unknown fixture field %s.%s", identity, name)
		}
		terminal := nestedField(field)
		if field.Relation == nil {
			if i != len(parts)-1 {
				return query.RelationPath{}, query.FieldRef{}, "", fmt.Errorf("scalar fixture traversal %s", leaf.Path)
			}
			if len(hops) == 0 {
				return query.RelationPath{}, terminal, lookup, nil
			}
			path, err := query.NewForwardRelationChain(hops, terminal, query.RelationTerminalRelatedField)
			return path, terminal, lookup, err
		}
		target := models[field.Relation.Target]
		path, err := query.NewForwardRelationPath(identity, model.DBTable, field.Name, field.Column, field.Relation.Target, target.DBTable, "id", field.Nullable, query.NewFieldRef("id", "id", query.FieldInteger, false))
		if err != nil {
			return query.RelationPath{}, query.FieldRef{}, "", err
		}
		hops = append(hops, path.Hops()...)
		if i == len(parts)-1 && lookup == query.LookupIsNull {
			path, err = query.NewForwardRelationChain(hops, terminal, query.RelationTerminalSourceKey)
			return path, terminal, lookup, err
		}
		identity, model = field.Relation.Target, target
	}
	return query.RelationPath{}, query.FieldRef{}, "", fmt.Errorf("fixture route lacks terminal %s", leaf.Path)
}

// NestedValue converts JSON transport values to the scalar types accepted by
// public Go APIs, preserving explicit NULL list members for dynamic coverage.
func NestedValue(leaf Leaf) (any, error) {
	_, field, lookup, err := nestedOperand(leaf)
	if err != nil {
		return nil, err
	}
	decode := func(raw json.RawMessage) (any, error) {
		if string(raw) == "null" {
			return nil, nil
		}
		if lookup == query.LookupIsNull || field.Kind() == query.FieldBoolean {
			var v bool
			err := json.Unmarshal(raw, &v)
			return v, err
		}
		switch field.Kind() {
		case query.FieldInteger:
			var v int64
			err := json.Unmarshal(raw, &v)
			return v, err
		case query.FieldDateTime:
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return time.Parse(time.RFC3339Nano, v)
		default:
			var v string
			err := json.Unmarshal(raw, &v)
			return v, err
		}
	}
	if lookup == query.LookupIn {
		var raw []json.RawMessage
		if err := json.Unmarshal(leaf.Value, &raw); err != nil {
			return nil, err
		}
		values := make([]any, len(raw))
		for i, item := range raw {
			value, err := decode(item)
			if err != nil {
				return nil, err
			}
			values[i] = value
		}
		return values, nil
	}
	return decode(leaf.Value)
}
func nestedValue(value any) (query.Value, error) {
	switch v := value.(type) {
	case nil:
		return query.Null(), nil
	case int64:
		return query.Integer(v), nil
	case string:
		return query.String(v), nil
	case bool:
		return query.Boolean(v), nil
	case time.Time:
		return query.DateTime(v), nil
	default:
		return query.Value{}, fmt.Errorf("unsupported fixture value %T", value)
	}
}
func NestedCondition(leaf Leaf) (query.Condition, error) {
	path, field, lookup, err := nestedOperand(leaf)
	if err != nil {
		return query.Condition{}, err
	}
	raw, err := NestedValue(leaf)
	if err != nil {
		return query.Condition{}, err
	}
	if lookup == query.LookupIn {
		items := raw.([]any)
		values := make([]query.Value, len(items))
		for i, item := range items {
			values[i], err = nestedValue(item)
			if err != nil {
				return query.Condition{}, err
			}
		}
		return query.NewRelatedInCondition(path, values)
	}
	value, err := nestedValue(raw)
	if err != nil {
		return query.Condition{}, err
	}
	if len(path.Hops()) == 0 {
		return query.NewCondition(field, lookup, value), nil
	}
	return query.NewRelatedCondition(path, lookup, value), nil
}
func NestedPlan(leaves map[string]Leaf, input NestedInput) (query.Plan, error) {
	where, err := expressionFromInputs(leaves, input.Expression, NestedCondition)
	if err != nil {
		return query.Plan{}, err
	}
	fields := SourceFields()
	plan, err := query.NewPlan("nested_reference_post", fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending)).WithWhere(where)
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
	models, err := nestedModels()
	if err != nil {
		return query.Plan{}, err
	}
	target := ir.ModelIdentity{AppLabel: "people", ModelName: "person"}
	columns := nestedFields(models[target])
	projections := make([]query.RelationProjection, len(input.Selected))
	for i, name := range input.Selected {
		var key query.FieldRef
		switch name {
		case "author":
			key = fields[2]
		case "reviewer":
			key = fields[3]
		default:
			return query.Plan{}, fmt.Errorf("unknown selected input %s", name)
		}
		projections[i], err = query.NewForwardRelationProjection(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, plan.Table(), key, target, models[target].DBTable, columns[0], columns)
		if err != nil {
			return query.Plan{}, err
		}
	}
	if len(projections) == 0 {
		return plan, nil
	}
	return plan.WithRelationProjections(projections...)
}

func EvaluateNested(ctx context.Context, backend db.Queryer, plan query.Plan) (int64, *NestedRow, []NestedRow, error) {
	return evaluateEagerRows(ctx, backend, plan, nestedRows)
}
func nestedRows(ctx context.Context, backend db.Queryer, plan query.Plan) ([]NestedRow, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := make([]NestedRow, 0)
	for rows.Next() {
		var id, author int64
		var title string
		var reviewer sql.NullInt64
		type targetCells struct {
			id, points, team, backup, manager sql.NullInt64
			name                              sql.NullString
		}
		projections := plan.RelationProjections()
		cells := make([]targetCells, len(projections))
		destinations := []any{&id, &title, &author, &reviewer}
		for i := range cells {
			c := &cells[i]
			destinations = append(destinations, &c.id, &c.name, &c.points, &c.team, &c.backup, &c.manager)
		}
		if err = rows.Scan(destinations...); err != nil {
			break
		}
		row := NestedRow{ID: id, Targets: make(map[string]*NestedTarget, len(cells))}
		for i, c := range cells {
			name := projections[i].TerminalHop().Field()
			if !c.id.Valid {
				if c.name.Valid || c.points.Valid || c.team.Valid || c.backup.Valid || c.manager.Valid {
					err = fmt.Errorf("partial absent target %s", name)
					break
				}
				row.Targets[name] = nil
				continue
			}
			if !c.name.Valid || !c.team.Valid {
				err = fmt.Errorf("partial present target %s", name)
				break
			}
			target := &NestedTarget{ID: c.id.Int64, Name: c.name.String, Team: c.team.Int64}
			if c.points.Valid {
				target.Points = &c.points.Int64
			}
			if c.backup.Valid {
				target.Backup = &c.backup.Int64
			}
			if c.manager.Valid {
				target.Manager = &c.manager.Int64
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
