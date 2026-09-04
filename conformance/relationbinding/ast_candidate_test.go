package relationbinding

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type relationDirection string

const (
	directionForward relationDirection = "forward"
	directionReverse relationDirection = "reverse"
)

type lookupKind string

const (
	lookupExact   lookupKind = "exact"
	lookupIsNull  lookupKind = "isnull"
	lookupRelated lookupKind = "related"
)

type relationHop struct {
	Edge        relationIdentity  `json:"edge"`
	Source      modelKey          `json:"source"`
	Target      modelKey          `json:"target"`
	Column      string            `json:"column"`
	Direction   relationDirection `json:"direction"`
	Cardinality string            `json:"cardinality"`
	Nullable    bool              `json:"nullable"`
}

type relationPath struct {
	Root          modelKey      `json:"root"`
	Hops          []relationHop `json:"hops"`
	TerminalField string        `json:"terminal_field,omitempty"`
	Lookup        lookupKind    `json:"lookup"`
}

func (p relationPath) CanonicalBytes() []byte {
	encoded, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("test-only relation AST cannot serialize: %v", err))
	}
	return encoded
}

type typedSelector struct {
	Root          modelKey
	Relation      string
	Direction     relationDirection
	TerminalField string
	Lookup        lookupKind
}

type relationPathError struct {
	Code      string
	Root      modelKey
	Segment   string
	Lookup    lookupKind
	Operation planOperation
}

func (e *relationPathError) Error() string {
	return fmt.Sprintf("%s root=%s segment=%s lookup=%s operation=%s", e.Code, e.Root, e.Segment, e.Lookup, e.Operation)
}

func buildTypedPath(set bindingSet, selector typedSelector) (relationPath, error) {
	if selector.Direction != directionForward && selector.Direction != directionReverse {
		return relationPath{}, &relationPathError{Code: "invalid_direction", Root: selector.Root, Segment: selector.Relation, Lookup: selector.Lookup}
	}
	return resolveRelationPath(set, selector.Root, selector.Relation, selector.Direction, selector.TerminalField, selector.Lookup)
}

func buildDynamicPath(set bindingSet, root modelKey, expression string) (relationPath, error) {
	parts := strings.Split(expression, "__")
	if len(parts) < 2 || len(parts) > 3 {
		return relationPath{}, &relationPathError{Code: "invalid_dynamic_path", Root: root, Segment: expression}
	}
	relationName := parts[0]
	terminal := parts[1]
	lookup := lookupExact
	if len(parts) == 2 && terminal == string(lookupIsNull) {
		terminal = ""
		lookup = lookupIsNull
	} else if len(parts) == 3 {
		lookup = lookupKind(parts[2])
	}
	if lookup != lookupExact && lookup != lookupIsNull {
		return relationPath{}, &relationPathError{Code: "unsupported_lookup", Root: root, Segment: expression, Lookup: lookup}
	}
	if lookup == lookupIsNull && terminal != "" {
		return relationPath{}, &relationPathError{Code: "invalid_isnull_path", Root: root, Segment: expression, Lookup: lookup}
	}

	if _, exists := findForward(set, root, relationName); exists {
		return resolveRelationPath(set, root, relationName, directionForward, terminal, lookup)
	}
	if _, exists := findReverse(set, root, relationName); exists {
		return resolveRelationPath(set, root, relationName, directionReverse, terminal, lookup)
	}
	return relationPath{}, &relationPathError{Code: "unknown_relation", Root: root, Segment: relationName, Lookup: lookup}
}

func resolveRelationPath(set bindingSet, root modelKey, relationName string, direction relationDirection, terminal string, lookup lookupKind) (relationPath, error) {
	var hop relationHop
	switch direction {
	case directionForward:
		forward, exists := findForward(set, root, relationName)
		if !exists {
			return relationPath{}, &relationPathError{Code: "unknown_forward_relation", Root: root, Segment: relationName, Lookup: lookup}
		}
		hop = relationHop{
			Edge: forward.Identity, Source: root, Target: forward.Target,
			Column: forward.Column, Direction: directionForward, Cardinality: "many_to_one", Nullable: forward.Nullable,
		}
	case directionReverse:
		reverse, exists := findReverse(set, root, relationName)
		if !exists {
			return relationPath{}, &relationPathError{Code: "unknown_reverse_relation", Root: root, Segment: relationName, Lookup: lookup}
		}
		forward, exists := findForward(set, reverse.Source, reverse.SourceField)
		if !exists {
			return relationPath{}, &relationPathError{Code: "binding_integrity", Root: root, Segment: relationName, Lookup: lookup}
		}
		hop = relationHop{
			Edge: forward.Identity, Source: root, Target: reverse.Source,
			Column: forward.Column, Direction: directionReverse, Cardinality: "one_to_many", Nullable: forward.Nullable,
		}
	default:
		return relationPath{}, &relationPathError{Code: "invalid_direction", Root: root, Segment: relationName, Lookup: lookup}
	}

	if lookup != lookupExact && lookup != lookupIsNull && lookup != lookupRelated {
		return relationPath{}, &relationPathError{Code: "unsupported_lookup", Root: root, Segment: relationName, Lookup: lookup}
	}
	if lookup == lookupIsNull || lookup == lookupRelated {
		if terminal != "" {
			return relationPath{}, &relationPathError{Code: "invalid_relation_only_path", Root: root, Segment: terminal, Lookup: lookup}
		}
	} else {
		fields, exists := set.modelFields(hop.Target)
		if !exists {
			return relationPath{}, &relationPathError{Code: "binding_integrity", Root: root, Segment: hop.Target.String(), Lookup: lookup}
		}
		position := sort.SearchStrings(fields, terminal)
		if terminal == "" || position == len(fields) || fields[position] != terminal {
			return relationPath{}, &relationPathError{Code: "unknown_target_field", Root: root, Segment: terminal, Lookup: lookup}
		}
	}
	return relationPath{Root: root, Hops: []relationHop{hop}, TerminalField: terminal, Lookup: lookup}, nil
}

func findForward(set bindingSet, source modelKey, field string) (boundRelation, bool) {
	for _, relation := range set.forward {
		if relation.Identity.Source == source && relation.Identity.Field == field {
			return relation, true
		}
	}
	return boundRelation{}, false
}

func findReverse(set bindingSet, owner modelKey, name string) (reverseBinding, bool) {
	for _, relation := range set.reverse {
		if relation.Owner == owner && relation.Name == name {
			return relation, true
		}
	}
	return reverseBinding{}, false
}

type planOperation string

const (
	operationPredicate     planOperation = "predicate"
	operationSelectRelated planOperation = "select_related"
	operationPrefetch      planOperation = "prefetch_related"
)

type joinKind string

const (
	joinInner     joinKind = "inner"
	joinLeftOuter joinKind = "left_outer"
)

type relationJoin struct {
	Edge      relationIdentity  `json:"edge"`
	Direction relationDirection `json:"direction"`
	Kind      joinKind          `json:"kind"`
}

type fetchStage struct {
	Kind       string   `json:"kind"`
	Source     modelKey `json:"source"`
	Target     modelKey `json:"target,omitempty"`
	ForeignKey string   `json:"foreign_key,omitempty"`
	Keys       []int64  `json:"keys,omitempty"`
}

type relationPlan struct {
	Operation planOperation  `json:"operation"`
	Paths     []relationPath `json:"paths"`
	Joins     []relationJoin `json:"joins"`
	Stages    []fetchStage   `json:"stages,omitempty"`
}

func (p relationPlan) CanonicalBytes() []byte {
	encoded, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("test-only relation plan cannot serialize: %v", err))
	}
	return encoded
}

func planRelations(operation planOperation, paths []relationPath, prefetchKeys []int64) (relationPlan, error) {
	plan := relationPlan{Operation: operation, Paths: cloneRelationPaths(paths)}
	joins := make(map[string]relationJoin)
	for _, path := range plan.Paths {
		if len(path.Hops) != 1 {
			return relationPlan{}, &relationPathError{Code: "unsupported_hop_count", Root: path.Root, Operation: operation}
		}
		hop := path.Hops[0]
		joinKey := hop.Edge.String() + ":" + string(hop.Direction)
		switch operation {
		case operationPredicate:
			if path.Lookup == lookupRelated {
				return relationPlan{}, &relationPathError{Code: "missing_predicate_lookup", Root: path.Root, Segment: hop.Edge.String(), Operation: operation}
			}
			// A relation-level isnull predicate can be evaluated from the FK
			// storage column on the root table, so it must not create a join.
			if path.Lookup == lookupIsNull && hop.Direction == directionForward {
				continue
			}
			joins[joinKey] = relationJoin{Edge: hop.Edge, Direction: hop.Direction, Kind: joinInner}
		case operationSelectRelated:
			if path.Lookup != lookupRelated {
				return relationPlan{}, &relationPathError{Code: "invalid_select_related_path", Root: path.Root, Segment: hop.Edge.String(), Operation: operation}
			}
			if hop.Direction == directionReverse || hop.Cardinality != "many_to_one" {
				return relationPlan{}, &relationPathError{Code: "multi_valued_select_related", Root: path.Root, Segment: hop.Edge.String(), Operation: operation}
			}
			kind := joinInner
			if hop.Nullable {
				kind = joinLeftOuter
			}
			joins[joinKey] = relationJoin{Edge: hop.Edge, Direction: hop.Direction, Kind: kind}
		case operationPrefetch:
			if path.Lookup != lookupRelated {
				return relationPlan{}, &relationPathError{Code: "invalid_prefetch_path", Root: path.Root, Segment: hop.Edge.String(), Operation: operation}
			}
			if hop.Direction != directionReverse || hop.Cardinality != "one_to_many" {
				return relationPlan{}, &relationPathError{Code: "unsupported_prefetch_path", Root: path.Root, Segment: hop.Edge.String(), Operation: operation}
			}
			keys := append([]int64(nil), prefetchKeys...)
			sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
			keys = compactInt64(keys)
			plan.Stages = []fetchStage{
				{Kind: "primary", Source: path.Root},
				{Kind: "foreign_key_batch", Source: hop.Target, Target: path.Root, ForeignKey: hop.Column, Keys: keys},
			}
		default:
			return relationPlan{}, &relationPathError{Code: "unsupported_operation", Root: path.Root, Operation: operation}
		}
	}
	joinKeys := make([]string, 0, len(joins))
	for key := range joins {
		joinKeys = append(joinKeys, key)
	}
	sort.Strings(joinKeys)
	plan.Joins = make([]relationJoin, 0, len(joinKeys))
	for _, key := range joinKeys {
		plan.Joins = append(plan.Joins, joins[key])
	}
	return plan, nil
}

func cloneRelationPaths(paths []relationPath) []relationPath {
	cloned := make([]relationPath, len(paths))
	for index, path := range paths {
		cloned[index] = path
		cloned[index].Hops = append([]relationHop(nil), path.Hops...)
	}
	return cloned
}

func compactInt64(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	output := values[:1]
	for _, value := range values[1:] {
		if value != output[len(output)-1] {
			output = append(output, value)
		}
	}
	return output
}

type ioProbe struct {
	CompilerCalls int
	DatabaseCalls int
}

func compileAndEvaluateCandidate(operation planOperation, paths []relationPath, prefetchKeys []int64, probe *ioProbe) (relationPlan, error) {
	plan, err := planRelations(operation, paths, prefetchKeys)
	if err != nil {
		return relationPlan{}, err
	}
	probe.CompilerCalls++
	probe.DatabaseCalls++
	return plan, nil
}

func compileDynamicCandidate(set bindingSet, root modelKey, expression string, operation planOperation, probe *ioProbe) (relationPlan, error) {
	path, err := buildDynamicPath(set, root, expression)
	if err != nil {
		return relationPlan{}, err
	}
	return compileAndEvaluateCandidate(operation, []relationPath{path}, nil, probe)
}
