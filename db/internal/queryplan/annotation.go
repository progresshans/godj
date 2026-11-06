package queryplan

import (
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// AnnotationDialect shares named-query planning across both physical backends.
// Value renders a row operand; Aggregate completes one call at its owning SQL
// level. CompleteAggregates adapts backend aggregate state before dependencies
// consume it (including PostgreSQL's exact Duration fraction accumulator).
type AnnotationDialect struct {
	BackendName        string
	ParameterLimit     int
	JoinLimit          int
	QuoteTable         func(string) (string, error)
	QuoteIdentifier    func(string) (string, error)
	Qualify            func(string, string) (string, error)
	Value              func(query.ResultExpression, ScalarSource, *[]any) (string, error)
	Aggregate          func(query.ResultExpression, string, string) (string, int, error)
	CompleteAggregates func(string, []query.ResultExpression) (string, error)
	Predicate          func(query.Expression, ScalarSource, *[]any) (string, error)
	OrderValue         func(query.FieldKind, string) string
	Pagination         func(*strings.Builder, *[]any, query.Plan) error
	RowLock            func(*strings.Builder, query.Plan, string, map[RelationKey]Join) error
	GroupLiteral       func(query.Value, *[]any) (string, error)
	GroupComparison    func(query.ResultExpression, string) (string, error)
}

type annotationPhysicalKey struct{ alias, column string }
type annotationGroupKey struct {
	value    query.ResultExpression
	physical annotationPhysicalKey
	field    query.FieldRef
}
type annotationAggregate struct {
	value query.ResultExpression
	owner query.AnnotationRef
}
type annotationPredicate struct {
	value   query.Expression
	indices []int
}
type annotationRead struct {
	plan       query.Plan
	dialect    AnnotationDialect
	joins      Joins
	keys       []annotationGroupKey
	atoms      []annotationAggregate
	physical   map[annotationPhysicalKey]int
	inputAlias string
	groupAlias string
	scope      query.ResultScope
}

// CompileAnnotated preserves a definition's source and JOIN binding while
// lowering dependencies. Grouped values use one aggregate relation followed by
// a value projection. This is a physical lowering within the same query stage:
// it does not change cache ownership, slicing, grouping or reference authority.
func CompileAnnotated(plan query.Plan, input string, inputArguments []any, dialect AnnotationDialect) (string, []any, error) {
	if !plan.HasAnnotations() || dialect.ParameterLimit <= 0 || dialect.Value == nil || dialect.Aggregate == nil ||
		dialect.Predicate == nil || dialect.QuoteTable == nil || dialect.QuoteIdentifier == nil || dialect.Qualify == nil || dialect.Pagination == nil {
		return "", nil, invalidPlan("annotated compiler has an invalid source or dialect")
	}
	if err := plan.ValidateAnnotations(); err != nil {
		return "", nil, err
	}
	if err := plan.ValidateGrouping(); err != nil {
		return "", nil, err
	}
	if err := plan.ValidateOrderings(); err != nil {
		return "", nil, err
	}
	if _, err := plan.WithResultShape(plan.ResultShape()); err != nil {
		return "", nil, err
	}
	r := annotationRead{plan: plan, dialect: dialect, physical: make(map[annotationPhysicalKey]int)}
	var prefix string
	if _, scope, derived := plan.DerivedSource(); derived {
		if input == "" || len(input) > 4<<20 {
			return "", nil, invalidPlan("annotated result stage requires its compiled input")
		}
		r.scope = scope
		r.inputAlias = unusedSQLIdentifier("godj_annotation_input", input)
		var columns strings.Builder
		appendGroupColumns(&columns, scope.ColumnCount(), "")
		var err error
		prefix, err = ScalarSQL(`WITH "`, r.inputAlias, `" (`, columns.String(), `) AS (`, input, `)`)
		if err != nil {
			return "", nil, err
		}
	} else {
		if input != "" || len(inputArguments) != 0 {
			return "", nil, invalidPlan("model annotation cannot borrow a result input")
		}
		var err error
		r.joins, err = PrepareJoins(plan, dialect.BackendName)
		if err != nil {
			return "", nil, err
		}
		if dialect.JoinLimit > 0 && len(r.joins.Keys) > dialect.JoinLimit {
			return "", nil, invalidPlan("annotated query exceeds the backend JOIN budget")
		}
	}
	selected := plan.ResultShape().Expressions()
	if plan.ResultShape().Kind() == query.ResultModel {
		var err error
		selected, err = SelectedRows(plan)
		if err != nil {
			return "", nil, err
		}
	}
	if len(selected) == 0 {
		return "", nil, invalidPlan("annotated read has no selected values")
	}
	hidden, err := HiddenOrderings(plan, selected)
	if err != nil {
		return "", nil, err
	}
	allSelected := append(slices.Clone(selected), hidden...)
	where, _ := plan.Where()
	before, after, err := splitAnnotationPredicate(where)
	if err != nil {
		return "", nil, err
	}
	initialKeys, grouped := plan.AnnotationGroupKeys()
	if plan.ResultShape().Kind() == query.ResultGrouped {
		initialKeys, grouped = plan.ResultShape().GroupKeys(), true
	}
	if plan.ResultShape().Kind() == query.ResultAggregate {
		if _, limited := plan.Limit(); limited || plan.Distinct() {
			return "", nil, invalidPlan("aggregate a sliced or distinct named query through an explicit result stage")
		}
		if _, offset := plan.Offset(); offset {
			return "", nil, invalidPlan("aggregate a sliced named query through an explicit result stage")
		}
		grouped = true
	}
	if after.value.Kind() != 0 && !grouped {
		return "", nil, invalidPlan("aggregate predicate has no grouping source")
	}
	arguments := slices.Clone(inputArguments)
	var base strings.Builder
	if grouped {
		for _, key := range initialKeys {
			if err := r.collectValue(key, query.AnnotationRef{}); err != nil {
				return "", nil, err
			}
		}
		for _, value := range allSelected {
			if err := r.collectValue(value, query.AnnotationRef{}); err != nil {
				return "", nil, err
			}
		}
		for _, order := range plan.Orderings() {
			if plan.ResultShape().Kind() == query.ResultAggregate {
				break
			}
			if err := r.collectValue(order.Expression(), query.AnnotationRef{}); err != nil {
				return "", nil, err
			}
		}
		if err := r.collectPredicate(after); err != nil {
			return "", nil, err
		}
		if len(r.keys)+len(r.atoms) == 0 {
			r.atoms = append(r.atoms, annotationAggregate{value: query.CountAllResult()})
		}
		if len(r.keys)+len(r.atoms) > query.MaxProjectionExpressions {
			return "", nil, invalidPlan("annotation groups exceed their selected-cell budget")
		}
		base.WriteString("SELECT ")
		var producers []query.ResultExpression
		for index, key := range r.keys {
			if index != 0 {
				base.WriteString(", ")
			}
			var value string
			if key.field != (query.FieldRef{}) {
				value, err = dialect.Qualify(key.physical.alias, key.physical.column)
				producers = append(producers, query.FieldResult(key.field))
			} else {
				value, err = r.render(key.value, query.AnnotationRef{}, false, &arguments)
				producers = append(producers, key.value)
			}
			if err != nil {
				return "", nil, err
			}
			if base.Len()+len(value) > 4<<20 {
				return "", nil, invalidPlan("annotation groups exceed their SQL budget")
			}
			base.WriteString(value + ` AS "c` + strconv.Itoa(index) + `"`)
		}
		for index, atom := range r.atoms {
			if len(r.keys)+index != 0 {
				base.WriteString(", ")
			}
			value, err := r.render(atom.value, atom.owner, false, &arguments)
			if err != nil {
				return "", nil, err
			}
			if base.Len()+len(value) > 4<<20 {
				return "", nil, invalidPlan("annotation groups exceed their SQL budget")
			}
			base.WriteString(value + ` AS "c` + strconv.Itoa(len(r.keys)+index) + `"`)
			producers = append(producers, atom.value)
		}
		if err := r.appendSource(&base); err != nil {
			return "", nil, err
		}
		if err := r.appendPredicate(&base, " WHERE ", before, false, &arguments); err != nil {
			return "", nil, err
		}
		if len(r.keys) != 0 {
			base.WriteString(" GROUP BY ")
			for index := range r.keys {
				if index != 0 {
					base.WriteString(", ")
				}
				base.WriteString(strconv.Itoa(index + 1))
			}
		}
		completed := base.String()
		if dialect.CompleteAggregates != nil {
			completed, err = dialect.CompleteAggregates(completed, producers)
			if err != nil {
				return "", nil, err
			}
		}
		r.groupAlias = unusedSQLIdentifier("godj_annotation_groups", input+completed)
		if prefix == "" {
			prefix = "WITH "
		} else {
			prefix += ", "
		}
		prefix, err = ScalarSQL(prefix, `"`, r.groupAlias, `" AS (`, completed, `)`)
		if err != nil {
			return "", nil, err
		}
	}
	var statement strings.Builder
	if prefix != "" {
		statement.WriteString(prefix + " ")
	}
	statement.WriteString("SELECT ")
	if plan.Distinct() {
		statement.WriteString("DISTINCT ")
	}
	for index, value := range allSelected {
		if index != 0 {
			statement.WriteString(", ")
		}
		rendered, err := r.render(value, query.AnnotationRef{}, grouped, &arguments)
		if err != nil {
			return "", nil, err
		}
		if statement.Len()+len(rendered) > 4<<20 {
			return "", nil, invalidPlan("annotation selection exceeds its SQL budget")
		}
		statement.WriteString(rendered + ` AS "c` + strconv.Itoa(index) + `"`)
	}
	if grouped {
		statement.WriteString(` FROM "` + r.groupAlias + `"`)
		if err := r.appendPredicate(&statement, " WHERE ", after, true, &arguments); err != nil {
			return "", nil, err
		}
	} else {
		if err := r.appendSource(&statement); err != nil {
			return "", nil, err
		}
		if err := r.appendPredicate(&statement, " WHERE ", before, false, &arguments); err != nil {
			return "", nil, err
		}
	}
	if plan.ResultShape().Kind() == query.ResultGrouped {
		if dialect.GroupLiteral == nil {
			return "", nil, invalidPlan("named group requires a comparison dialect")
		}
		completed, err := FinishGroups(statement.String(), plan, func(value query.Value) (string, error) {
			return dialect.GroupLiteral(value, &arguments)
		}, dialect.GroupComparison, func(sql *strings.Builder) error { return dialect.Pagination(sql, &arguments, plan) })
		if err != nil {
			return "", nil, err
		}
		statement.Reset()
		statement.WriteString(completed)
	} else {
		for index, order := range plan.Orderings() {
			if plan.ResultShape().Kind() == query.ResultAggregate {
				break
			}
			if index == 0 {
				statement.WriteString(" ORDER BY ")
			} else {
				statement.WriteString(", ")
			}
			value := order.Expression()
			if selectedIndex := ExpressionIndex(allSelected, value); selectedIndex >= 0 && value.ResultValueKind() != query.FieldJSON {
				statement.WriteString(strconv.Itoa(selectedIndex + 1))
			} else {
				rendered, err := r.render(value, query.AnnotationRef{}, grouped, &arguments)
				if err != nil {
					return "", nil, err
				}
				if dialect.OrderValue != nil {
					rendered = dialect.OrderValue(value.ResultValueKind(), rendered)
				}
				if statement.Len()+len(rendered) > 4<<20 {
					return "", nil, invalidPlan("annotation ordering exceeds its SQL budget")
				}
				statement.WriteString(rendered)
			}
			AppendOrderDirection(&statement, order.Direction(), order.Nulls())
		}
		if err := dialect.Pagination(&statement, &arguments, plan); err != nil {
			return "", nil, err
		}
		if dialect.RowLock != nil {
			if err := dialect.RowLock(&statement, plan, "t0", r.joins.ByKey); err != nil {
				return "", nil, err
			}
		}
	}
	output := statement.String()
	if len(hidden) != 0 {
		var outer strings.Builder
		outer.WriteString("SELECT ")
		appendGroupColumns(&outer, len(selected), `"godj_annotation_rows".`)
		outer.WriteString(" FROM (" + output + `) AS "godj_annotation_rows"`)
		for index, order := range plan.Orderings() {
			if index == 0 {
				outer.WriteString(" ORDER BY ")
			} else {
				outer.WriteString(", ")
			}
			column := `"godj_annotation_rows"."c` + strconv.Itoa(ExpressionIndex(allSelected, order.Expression())) + `"`
			if dialect.OrderValue != nil {
				column = dialect.OrderValue(order.Expression().ResultValueKind(), column)
			}
			outer.WriteString(column)
			AppendOrderDirection(&outer, order.Direction(), order.Nulls())
		}
		output = outer.String()
	}
	if len(arguments) > dialect.ParameterLimit || len(output) > 4<<20 {
		return "", nil, invalidPlan("annotated query exceeds its SQL or parameter budget")
	}
	return output, arguments, nil
}

func (r *annotationRead) appendSource(statement *strings.Builder) error {
	if r.inputAlias != "" {
		statement.WriteString(` FROM "` + r.inputAlias + `" AS "t0"`)
		return nil
	}
	return AppendGroupSource(statement, r.plan, r.joins, r.dialect.QuoteTable, r.dialect.QuoteIdentifier, r.dialect.Qualify)
}

func (r *annotationRead) groupColumn(index int) string {
	return `"` + r.groupAlias + `"."c` + strconv.Itoa(index) + `"`
}

func (r *annotationRead) source(owner query.AnnotationRef, outer, aggregate bool) ScalarSource {
	plan, joins := r.plan, r.joins.ByKey
	if owner.Valid() {
		plan, _ = owner.Source()
		joins = r.joins.Annotations[owner]
	} else if aggregate {
		joins = r.joins.ResultByKey
	}
	source := ScalarSource{Plan: plan, Alias: "t0", Joins: joins, Aggregate: aggregate, QuoteTable: r.dialect.QuoteTable}
	source.Selected = func(ref query.ResultRef) (string, error) {
		index, valid := ref.Index()
		if !valid || !ref.Scope().Equal(r.scope) {
			return "", invalidPlan("named result reference belongs to another input")
		}
		return `"t0"."c` + strconv.Itoa(index) + `"`, nil
	}
	source.Annotation = func(ref query.AnnotationRef, arguments *[]any) (string, error) {
		if !r.plan.OwnsAnnotation(ref) {
			return "", invalidPlan("annotation compiler received a foreign reference")
		}
		value, _ := ref.Expression()
		return r.render(value, ref, outer, arguments)
	}
	if outer {
		source.Field = func(field query.FieldRef) (string, error) {
			return r.physicalColumn(annotationPhysicalKey{"t0", field.Column()})
		}
		source.Related = func(path query.RelationPath) (string, error) {
			alias := "t0"
			if key, joined := ConditionJoinKey(path); joined {
				entry, found := r.joins.ByKey[key]
				if !found {
					return "", invalidPlan("group predicate relation has no owned JOIN")
				}
				alias = entry.Alias
			}
			return r.physicalColumn(annotationPhysicalKey{alias, path.Terminal().Column()})
		}
		source.Reference = func(value query.ScalarExpression) (string, bool, error) {
			for index, key := range r.keys {
				if scalar, present := key.value.Scalar(); present && scalar.Equal(value) {
					return r.groupColumn(index), true, nil
				}
			}
			return "", false, nil
		}
		source.ExistsQualify = func(alias, column string) (string, error) {
			if alias == "t0" || slices.ContainsFunc(r.joins.Keys, func(key RelationKey) bool { return r.joins.ByKey[key].Alias == alias }) {
				return r.physicalColumn(annotationPhysicalKey{alias, column})
			}
			return r.dialect.Qualify(alias, column)
		}
	}
	return source
}

func (r *annotationRead) physicalColumn(key annotationPhysicalKey) (string, error) {
	index, present := r.physical[key]
	if !present {
		return "", invalidPlan("group predicate has an unselected source dependency")
	}
	return r.groupColumn(index), nil
}

func (r *annotationRead) render(value query.ResultExpression, owner query.AnnotationRef, outer bool, arguments *[]any) (string, error) {
	// A named field is a scalar operand. Preserve its exact physical checks
	// instead of letting the new name bypass Decimal precision or temporal
	// source validation that FieldExpression would normally provide.
	if owner.Valid() && value.Kind() == query.ResultField {
		var scalar query.ScalarExpression
		var err error
		if path, related := value.RelationPath(); related {
			scalar, err = query.RelatedExpression(path)
		} else {
			field, _ := value.Field()
			scalar, err = query.FieldExpression(field)
		}
		if err != nil {
			return "", err
		}
		value, err = query.ScalarResult(scalar)
		if err != nil {
			return "", err
		}
	}
	if outer {
		for index, key := range r.keys {
			if key.field == (query.FieldRef{}) && key.value.Equal(value) {
				return r.groupColumn(index), nil
			}
		}
		if value.IsAggregateCall() {
			for index, atom := range r.atoms {
				if atom.owner.Equal(owner) && atom.value.Equal(value) {
					return r.groupColumn(len(r.keys) + index), nil
				}
			}
			return "", invalidPlan("aggregate dependency has no completed group cell")
		}
	}
	source := r.source(owner, outer, value.IsAggregateCall())
	if !value.IsAggregateCall() {
		return r.dialect.Value(value, source, arguments)
	}
	start := len(*arguments)
	operand, filter := "", ""
	var err error
	if value.Kind() != query.ResultCountAll {
		operand, err = r.dialect.Value(value, source, arguments)
		if err != nil {
			return "", err
		}
	}
	if predicate, present := value.Filter(); present {
		condition, err := r.dialect.Predicate(predicate, source, arguments)
		if err != nil {
			return "", err
		}
		filter = " FILTER (WHERE " + condition + ")"
	}
	statement, copies, err := r.dialect.Aggregate(value, operand, filter)
	if err != nil {
		return "", err
	}
	if copies < 1 || copies > 2 {
		return "", invalidPlan("aggregate compiler returned an invalid parameter traversal")
	}
	if copies == 2 {
		*arguments = append(*arguments, (*arguments)[start:]...)
	}
	if len(*arguments) > r.dialect.ParameterLimit {
		return "", invalidPlan("named aggregate exceeds its parameter budget")
	}
	return ScalarSQL(statement)
}

func (r *annotationRead) appendPredicate(statement *strings.Builder, keyword string, predicate annotationPredicate, outer bool, arguments *[]any) error {
	if predicate.value.Kind() == 0 {
		return nil
	}
	source := r.source(query.AnnotationRef{}, outer, false)
	source.Exists = make(map[int]CollectionExists)
	for index, original := range predicate.indices {
		if exists, present := r.joins.Exists[original]; present {
			source.Exists[index] = exists
		}
	}
	value, err := r.dialect.Predicate(predicate.value, source, arguments)
	if err != nil {
		return err
	}
	if statement.Len()+len(value) > 4<<20 {
		return invalidPlan("annotation predicate exceeds its SQL budget")
	}
	statement.WriteString(keyword + value)
	return nil
}
