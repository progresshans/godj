package nullableforwardproduct

import (
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"time"
)

// LookupPlan builds the broader nullable-target fixture from input values.
// Expected row IDs, counts and SQL never participate in its construction.
func LookupPlan(leaves map[string]Leaf, root Node) (query.Plan, error) {
	expression, err := expressionFromInputs(leaves, root, LookupCondition)
	if err != nil {
		return query.Plan{}, err
	}
	fields := SourceFields()
	return query.NewPlan("forward_lookup_post", fields).WithOrderings(query.NewOrdering(fields[0], query.Ascending)).WithWhere(expression)
}

func LookupInput(leaf Leaf) (query.FieldRef, query.Lookup, any, error) {
	parts := strings.Split(leaf.Path, "__")
	var field query.FieldRef
	lookup := query.LookupExact
	if len(parts) == 1 && parts[0] == "title" {
		field = SourceFields()[1]
	} else {
		if len(parts) != 3 || (parts[0] != "author" && parts[0] != "reviewer") {
			return field, lookup, nil, fmt.Errorf("invalid lookup input %q", leaf.Path)
		}
		name := parts[1]
		lookup = query.Lookup(parts[2])
		switch name {
		case "name", "nickname", "bio":
			field = query.NewFieldRef(name, name, query.FieldString, name != "name")
		case "score":
			field = query.NewFieldRef(name, name, query.FieldInteger, true)
		case "seen_at":
			field = query.NewFieldRef(name, name, query.FieldDateTime, true)
		case "active":
			field = query.NewFieldRef(name, name, query.FieldBoolean, false)
		default:
			return field, lookup, nil, fmt.Errorf("invalid target input %q", name)
		}
	}
	if lookup == query.LookupIn {
		var items []json.RawMessage
		if err := json.Unmarshal(leaf.Value, &items); err != nil {
			return field, lookup, nil, err
		}
		values := make([]any, len(items))
		for index, item := range items {
			if string(item) == "null" {
				continue
			}
			value, err := lookupScalar(field.Kind(), item)
			if err != nil {
				return field, lookup, nil, err
			}
			values[index] = value
		}
		return field, lookup, values, nil
	}
	kind := field.Kind()
	if lookup == query.LookupIsNull {
		kind = query.FieldBoolean
	}
	value, err := lookupScalar(kind, leaf.Value)
	return field, lookup, value, err
}

func lookupScalar(kind query.FieldKind, raw json.RawMessage) (any, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, fmt.Errorf("NULL scalar input requires an explicit membership item")
	}
	switch kind {
	case query.FieldInteger:
		var value int64
		err := json.Unmarshal(raw, &value)
		return value, err
	case query.FieldBoolean:
		var value bool
		err := json.Unmarshal(raw, &value)
		return value, err
	case query.FieldString:
		var value string
		err := json.Unmarshal(raw, &value)
		return value, err
	case query.FieldDateTime:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return time.Parse(time.RFC3339Nano, value)
	}
	return nil, fmt.Errorf("invalid input kind %q", kind)
}

func lookupValue(raw any) (query.Value, error) {
	switch value := raw.(type) {
	case nil:
		return query.Null(), nil
	case string:
		return query.String(value), nil
	case int64:
		return query.Integer(value), nil
	case bool:
		return query.Boolean(value), nil
	case time.Time:
		return query.DateTime(value), nil
	}
	return query.Value{}, fmt.Errorf("unsupported scalar input %T", raw)
}

func LookupCondition(leaf Leaf) (query.Condition, error) {
	field, lookup, raw, err := LookupInput(leaf)
	if err != nil {
		return query.Condition{}, err
	}
	if leaf.Path == "title" {
		value, err := lookupValue(raw)
		if err != nil {
			return query.Condition{}, err
		}
		return query.NewCondition(field, lookup, value), nil
	}
	relation, _, _ := strings.Cut(leaf.Path, "__")
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "forward_lookup", ModelName: "post"}, "forward_lookup_post", relation, relation+"_id",
		ir.ModelIdentity{AppLabel: "forward_lookup", ModelName: "person"}, "forward_lookup_person", "id", relation == "reviewer", field,
	)
	if err != nil {
		return query.Condition{}, err
	}
	if lookup == query.LookupIn {
		items := raw.([]any)
		values := make([]query.Value, len(items))
		for index, item := range items {
			value, err := lookupValue(item)
			if err != nil {
				return query.Condition{}, err
			}
			values[index] = value
		}
		return query.NewRelatedInCondition(path, values)
	}
	value, err := lookupValue(raw)
	if err != nil {
		return query.Condition{}, err
	}
	return query.NewRelatedCondition(path, lookup, value), nil
}
