package orm

import (
	"context"
	"reflect"
	"slices"

	"github.com/progresshans/godj/query"
)

// AnnotationSource is a sealed row source. M owns the expression vocabulary;
// R is the original model or projection result preserved alongside named cells.
type AnnotationSource[M, R any] interface {
	annotationSource(M, R) (annotationInput[M, R], error)
}

type namedBinding struct {
	reference query.AnnotationRef
	newCell   func() scalarCell[any]
	publish   bool
}

type annotationInput[M, R any] struct {
	origin     resultOrigin
	plan       query.Plan
	base       []query.ResultExpression
	newDecoder func() resultDecoder[R]
	bindings   []namedBinding
	bound      *BoundModel[M]
}

// NamedRow keeps the original result and separately owned named cells. Values
// are retrieved through a bound NamedColumn; a matching string from another
// query never grants access. Each retrieval uses that column's original codec.
type NamedRow[M, R any] struct {
	Value R
	cells map[query.AnnotationRef]any
	order []query.AnnotationRef
}

type NamedValues[M any] interface {
	namedValue(M, query.AnnotationRef) (any, bool)
}

func (row NamedRow[M, R]) namedValue(_ M, reference query.AnnotationRef) (any, bool) {
	value, found := row.cells[reference]
	return copyNamedRaw(value), found
}

func (row NamedRow[M, R]) Names() []string {
	result := make([]string, len(row.order))
	for index, reference := range row.order {
		result[index] = reference.Name()
	}
	return result
}

// MapNamedRow changes the caller-owned base value without rebinding any named
// cell. Generated model facades use this to attach their ordinary model object.
func MapNamedRow[M, A, B any](row NamedRow[M, A], build func(A) (B, error)) (NamedRow[M, B], error) {
	if row.cells == nil || build == nil {
		return NamedRow[M, B]{}, invalidResultBuilder("named row or builder is uninitialized")
	}
	value, err := build(row.Value)
	if err != nil {
		return NamedRow[M, B]{}, err
	}
	return NamedRow[M, B]{Value: value, cells: row.cells, order: row.order}, nil
}

// NamedQuery retains the original result builder and all immutable definitions.
// Copies share successful private scans; every refinement starts a fresh cache.
type NamedQuery[M, R any] struct {
	input annotationInput[M, R]
	rows  ResultQuery[M, NamedRow[M, R]]
	err   error
}

func Names[M, R any](source AnnotationSource[M, R]) (NamedQuery[M, R], error) {
	input, err := namedInput(source)
	if err != nil {
		return NamedQuery[M, R]{}, err
	}
	return newNamedQuery(input)
}

func NamesIn[M, R any](source AnnotationSource[M, R], binding BoundModel[M]) (NamedQuery[M, R], error) {
	input, err := namedInput(source)
	if err != nil {
		return NamedQuery[M, R]{}, err
	}
	input.bound = &binding
	if _, err := input.dynamicBinding(); err != nil {
		return NamedQuery[M, R]{}, err
	}
	if err := input.validateNames(); err != nil {
		return NamedQuery[M, R]{}, err
	}
	return newNamedQuery(input)
}

func namedInput[M, R any](source AnnotationSource[M, R]) (annotationInput[M, R], error) {
	if interfaceIsNil(source) {
		return annotationInput[M, R]{}, invalidResultBuilder("annotation source is nil")
	}
	return source.annotationSource(*new(M), *new(R))
}

func (source QuerySet[M]) annotationSource(M, M) (annotationInput[M, M], error) {
	var zero annotationInput[M, M]
	if source.configurationErr != nil {
		return zero, source.configurationErr
	}
	if source.prepared == nil || source.evaluation == nil || descriptorIsNil(source.descriptor) {
		return zero, invalidResultBuilder("annotation model source is uninitialized")
	}
	if source.materialization != nil || len(source.plan.RelationProjections()) != 0 || source.plan.ResultShape().Kind() != query.ResultModel {
		return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "named model results require a source without eager or prefetch materialization"}
	}
	factory, err := namedModelDecoder(source)
	if err != nil {
		return zero, err
	}
	fields := source.plan.SourceFields()
	base := make([]query.ResultExpression, len(fields))
	for index, field := range fields {
		base[index] = query.FieldResult(field)
	}
	return annotationInput[M, M]{origin: resultOriginFor(source), plan: source.plan, base: base, newDecoder: factory}, nil
}

func (source ResultQuery[M, R]) annotationSource(M, R) (annotationInput[M, R], error) {
	if err := source.validatePlan(); err != nil {
		return annotationInput[M, R]{}, err
	}
	if source.plan.ResultShape().Kind() != query.ResultProjection {
		return annotationInput[M, R]{}, invalidResultBuilder("annotating an aggregate result requires an explicit result stage")
	}
	return annotationInput[M, R]{origin: source.origin, plan: source.plan, base: source.plan.ResultShape().Expressions(),
		newDecoder: source.newDecoder, bindings: source.named, bound: source.bound}, nil
}

func (source ResultSource[M]) annotationSource(ResultOf[M], []query.Value) (annotationInput[ResultOf[M], []query.Value], error) {
	var zero annotationInput[ResultOf[M], []query.Value]
	if err := source.validate(); err != nil {
		return zero, err
	}
	values := source.plan.ResultShape().Expressions()
	factory, err := dynamicGroupDecoder(values)
	if err != nil {
		return zero, err
	}
	return annotationInput[ResultOf[M], []query.Value]{origin: source.origin, plan: source.plan, base: values, newDecoder: factory}, nil
}

func (source NamedQuery[M, R]) annotationSource(M, R) (annotationInput[M, R], error) {
	if err := source.ConfigurationError(); err != nil {
		return annotationInput[M, R]{}, err
	}
	return source.input, nil
}

func namedModelDecoder[M any](source QuerySet[M]) (func() resultDecoder[M], error) {
	projection, supported := source.descriptor.(ProjectionDescriptor[M])
	keyer, keyed := source.descriptor.(interface{ PrimaryKey(M) (query.Value, bool) })
	if !supported || !keyed || descriptorIsNil(projection) {
		return nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported,
			Detail: "named model results require projection scanning and a model primary key; select a scalar projection for other descriptors"}
	}
	count := len(source.plan.SourceFields())
	factory := func() resultDecoder[M] {
		scan := projection.NewProjectionScan()
		if interfaceIsNil(scan) {
			return resultDecoder[M]{err: invalidResultBuilder("named model descriptor returned a nil projection scan")}
		}
		destinations := scan.Destinations()
		if !validProjectionDestinations(destinations, count) {
			return resultDecoder[M]{err: invalidResultBuilder("named model projection destinations differ from source fields")}
		}
		var snapshot M
		return resultDecoder[M]{destinations: slices.Clone(destinations), validate: func() error {
			value, key, presence := scan.Decode()
			actual, present := keyer.PrimaryKey(value)
			_, integer := key.Integer()
			if presence != ProjectionPresent || !integer || key.IsNull() || !present || !actual.Equal(key) {
				return invalidResultBuilder("named model projection did not decode one present model with its own primary key")
			}
			snapshot = projection.CloneModel(value)
			return nil
		}, decode: func() M { return projection.CloneModel(snapshot) }}
	}
	if probe := factory(); probe.err != nil {
		return nil, probe.err
	}
	return factory, nil
}

func Annotate[M, R, V any](source AnnotationSource[M, R], name string, value ScalarField[M, V]) (NamedQuery[M, R], NamedColumn[M, V], error) {
	expression, factory, err := scalarResult(value)
	return annotate(source, name, expression, factory, err, true)
}

func Alias[M, R, V any](source AnnotationSource[M, R], name string, value ScalarField[M, V]) (NamedQuery[M, R], NamedColumn[M, V], error) {
	expression, factory, err := scalarResult(value)
	return annotate(source, name, expression, factory, err, false)
}

func AnnotateAggregate[M, R, V any](source AnnotationSource[M, R], name string, value AggregateExpression[M, V]) (NamedQuery[M, R], NamedColumn[M, V], error) {
	return annotate(source, name, value.expression, value.newCell, value.err, true)
}

func AliasAggregate[M, R, V any](source AnnotationSource[M, R], name string, value AggregateExpression[M, V]) (NamedQuery[M, R], NamedColumn[M, V], error) {
	return annotate(source, name, value.expression, value.newCell, value.err, false)
}

func annotate[M, R, V any](source AnnotationSource[M, R], name string, expression query.ResultExpression, factory func() scalarCell[V], valueErr error, selected bool) (NamedQuery[M, R], NamedColumn[M, V], error) {
	var emptyQuery NamedQuery[M, R]
	var emptyColumn NamedColumn[M, V]
	input, err := namedInput(source)
	if err = firstError(err, valueErr); err != nil {
		return emptyQuery, emptyColumn, err
	}
	input, column, err := bindNamedValue(input, name, expression, factory, selected)
	if err != nil {
		return emptyQuery, emptyColumn, err
	}
	result, err := newNamedQuery(input)
	if err != nil {
		return emptyQuery, emptyColumn, err
	}
	return result, column, nil
}

func bindNamedValue[M, R, V any](input annotationInput[M, R], name string, expression query.ResultExpression, factory func() scalarCell[V], selected bool) (annotationInput[M, R], NamedColumn[M, V], error) {
	var column NamedColumn[M, V]
	if factory == nil {
		return annotationInput[M, R]{}, column, invalidResultBuilder("named value has no decoder")
	}
	if err := input.validateName(name); err != nil {
		return annotationInput[M, R]{}, column, err
	}
	plan := input.plan
	if _, grouped := plan.AnnotationGroupKeys(); expression.IsAggregate() && !grouped {
		// Capture the actual output before grouping. A rebind replaces its
		// automatically published scalar, but an explicitly selected older
		// definition in the base projection retains its own identity.
		values, _, _, err := input.selection(name)
		if err != nil {
			return annotationInput[M, R]{}, column, err
		}
		shape, err := query.NewProjectionResult(values...)
		if err != nil {
			return annotationInput[M, R]{}, column, err
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			return annotationInput[M, R]{}, column, err
		}
	}
	plan, ref, err := plan.WithAnnotation(name, expression, selected)
	if err != nil {
		return annotationInput[M, R]{}, column, err
	}
	scalar, err := query.AnnotationExpression(ref)
	if err != nil {
		return annotationInput[M, R]{}, column, err
	}
	column = NamedColumn[M, V]{reference: ref, column: ResultColumn[M, V]{value: ScalarExpression[M, V]{expression: scalar}, newCell: factory}}
	binding := namedBinding{reference: ref, publish: selected, newCell: func() scalarCell[any] {
		cell := factory()
		return scalarCell[any]{destination: cell.destination, value: func() any { return cell.value() }}
	}}
	bindings := slices.Clone(input.bindings)
	index := slices.IndexFunc(bindings, func(value namedBinding) bool { return value.reference.Name() == name })
	if index < 0 {
		bindings = append(bindings, binding)
	} else {
		bindings[index] = binding
	}
	input.plan, input.bindings = plan, bindings
	return input, column, nil
}

func newNamedQuery[M, R any](input annotationInput[M, R]) (NamedQuery[M, R], error) {
	var zero NamedQuery[M, R]
	if input.origin.validate == nil || input.newDecoder == nil || len(input.base) == 0 {
		return zero, invalidResultBuilder("named query input is uninitialized")
	}
	if err := input.validateNames(); err != nil {
		return zero, err
	}
	selected, bindings, indices, err := input.selection("")
	if err != nil {
		return zero, err
	}
	shape, err := query.NewProjectionResult(selected...)
	if err != nil {
		return zero, err
	}
	plan, err := input.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	factory := func() resultDecoder[NamedRow[M, R]] {
		base := input.newDecoder()
		if base.err != nil {
			return resultDecoder[NamedRow[M, R]]{err: base.err}
		}
		if len(base.destinations) != len(input.base) {
			return resultDecoder[NamedRow[M, R]]{err: invalidResultBuilder("named base decoder differs from its selected columns")}
		}
		raw := make([]any, len(selected))
		destinations := make([]any, len(raw))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		return resultDecoder[NamedRow[M, R]]{destinations: destinations, validate: func() error {
			for index, destination := range base.destinations {
				if err := scanGroupValue(destination, raw[index]); err != nil {
					return err
				}
			}
			if err := base.validateScanned(); err != nil {
				return err
			}
			for index, binding := range bindings {
				cell := binding.newCell()
				if err := scanGroupValue(cell.destination, raw[indices[index]]); err != nil {
					return err
				}
			}
			for index := range raw {
				raw[index] = copyNamedRaw(raw[index])
			}
			return nil
		}, decode: func() NamedRow[M, R] {
			row := NamedRow[M, R]{Value: base.decode(), cells: make(map[query.AnnotationRef]any, len(bindings)), order: make([]query.AnnotationRef, len(bindings))}
			for index, expression := range selected {
				if value, computed := expression.Scalar(); computed {
					if ref, named := value.AnnotationReference(); named {
						row.cells[ref] = raw[index]
					}
				}
			}
			for index, binding := range bindings {
				row.cells[binding.reference] = raw[indices[index]]
				row.order[index] = binding.reference
			}
			return row
		}}
	}
	if probe := factory(); probe.err != nil {
		return zero, probe.err
	}
	return NamedQuery[M, R]{input: input, rows: newResultQuery[M](input.origin, plan, factory)}, nil
}

func (input annotationInput[M, R]) selection(replacing string) ([]query.ResultExpression, []namedBinding, []int, error) {
	selected := slices.Clone(input.base)
	var bindings []namedBinding
	var indices []int
	for _, binding := range input.bindings {
		if !binding.publish || binding.reference.Name() == replacing {
			continue
		}
		value, err := query.AnnotationExpression(binding.reference)
		if err != nil {
			return nil, nil, nil, err
		}
		expression, err := query.ScalarResult(value)
		if err != nil {
			return nil, nil, nil, err
		}
		index := slices.IndexFunc(selected, expression.Equal)
		if index < 0 {
			index = len(selected)
			selected = append(selected, expression)
		}
		indices = append(indices, index)
		bindings = append(bindings, binding)
	}
	return selected, bindings, indices, nil
}

func copyNamedRaw(value any) any {
	if bytes, ok := value.([]byte); ok {
		return slices.Clone(bytes)
	}
	return value
}

func (q NamedQuery[M, R]) Plan() query.Plan { return q.rows.plan }
func (q NamedQuery[M, R]) ConfigurationError() error {
	if q.err != nil {
		return q.err
	}
	return q.rows.validatePlan()
}
func (q NamedQuery[M, R]) All(ctx context.Context) ([]NamedRow[M, R], error) {
	if q.err != nil {
		return nil, q.err
	}
	return q.rows.All(ctx)
}
func (q NamedQuery[M, R]) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	return q.rows.Count(ctx)
}
func (q NamedQuery[M, R]) Page(ctx context.Context, limit, offset int) (ResultPage[NamedRow[M, R]], error) {
	if q.err != nil {
		return ResultPage[NamedRow[M, R]]{}, q.err
	}
	return q.rows.Page(ctx, limit, offset)
}
func (q NamedQuery[M, R]) Exists(ctx context.Context) (bool, error) {
	if err := q.ConfigurationError(); err != nil {
		return false, err
	}
	if err := q.rows.validate(ctx); err != nil {
		return false, err
	}
	if values, ready := q.rows.evaluation.cachedValues(); ready {
		return sessionReadResult(ctx, q.rows.origin.backend, len(values) != 0, nil)
	}
	limit := 1
	if current, sliced := q.rows.plan.Limit(); sliced {
		limit = min(current, limit)
	}
	rows, err := q.rows.Limit(limit)
	if err != nil {
		return false, err
	}
	count, err := rows.Count(ctx)
	return count != 0, err
}
func (q NamedQuery[M, R]) Fresh() NamedQuery[M, R] {
	q.rows = q.rows.Fresh()
	return q
}
func (q NamedQuery[M, R]) resultInput(M) (resultOrigin, query.Plan, error) {
	return q.rows.origin, q.rows.plan, q.ConfigurationError()
}

func (q NamedQuery[M, R]) refine(plan query.Plan, err error) NamedQuery[M, R] {
	if q.err != nil {
		return q
	}
	if err != nil {
		q.err = err
		return q
	}
	input := q.input
	input.plan = plan
	result, err := newNamedQuery(input)
	if err != nil {
		q.err = err
		return q
	}
	return result
}
func (q NamedQuery[M, R]) Filter(predicates ...Predicate[M]) NamedQuery[M, R] {
	if err := q.ConfigurationError(); err != nil {
		q.err = err
		return q
	}
	if q.rows.sliced() {
		q.err = invalidResultBuilder("filtering a sliced named query requires an explicit result stage")
		return q
	}
	if len(predicates) > query.MaximumScalarNodes {
		q.err = invalidResultBuilder("too many named query predicates")
		return q
	}
	var expressions []query.Expression
	for _, predicate := range predicates {
		if predicate.err != nil {
			q.err = predicate.err
			return q
		}
		expressions = append(expressions, predicate.expression)
	}
	if len(expressions) == 0 {
		return q.refine(q.input.plan.WithoutCollectionFilterReuse(), nil)
	}
	where := expressions[0]
	var err error
	if len(expressions) > 1 {
		where, err = query.AndExpressions(where, expressions[1], expressions[2:]...)
		if err != nil {
			return q.refine(query.Plan{}, err)
		}
	}
	plan, err := q.input.plan.WithWhere(where)
	return q.refine(plan, err)
}
func (q NamedQuery[M, R]) OrderBy(orderings ...Ordering[M]) NamedQuery[M, R] {
	if err := q.ConfigurationError(); err != nil {
		q.err = err
		return q
	}
	if q.rows.sliced() || len(orderings) > query.MaxProjectionExpressions {
		q.err = invalidResultBuilder("named ordering requires an unsliced source and a bounded list")
		return q
	}
	values := make([]query.Ordering, len(orderings))
	for index, order := range orderings {
		if order.err != nil {
			q.err = order.err
			return q
		}
		values[index] = order.ordering
	}
	plan := q.input.plan.WithOrderings(values...)
	return q.refine(plan, plan.ValidateOrderings())
}
func (q NamedQuery[M, R]) Distinct() NamedQuery[M, R] {
	if q.rows.sliced() {
		q.err = invalidResultBuilder("DISTINCT cannot follow a named query slice")
		return q
	}
	return q.refine(q.input.plan.WithDistinct(), q.ConfigurationError())
}
func (q NamedQuery[M, R]) Limit(limit int) (NamedQuery[M, R], error) {
	if err := q.ConfigurationError(); err != nil {
		return NamedQuery[M, R]{}, err
	}
	plan, err := q.input.plan.WithLimit(limit)
	result := q.refine(plan, err)
	return result, result.ConfigurationError()
}
func (q NamedQuery[M, R]) Offset(offset int) (NamedQuery[M, R], error) {
	if err := q.ConfigurationError(); err != nil {
		return NamedQuery[M, R]{}, err
	}
	plan, err := q.input.plan.WithOffset(offset)
	result := q.refine(plan, err)
	return result, result.ConfigurationError()
}

func SelectNamed[M, R, S any](source NamedQuery[M, R], projection Projection[M, S]) (ResultQuery[M, S], error) {
	if err := firstError(source.ConfigurationError(), projection.err); err != nil {
		return ResultQuery[M, S]{}, err
	}
	if projection.newDecoder == nil {
		return ResultQuery[M, S]{}, invalidResultBuilder("named projection has no decoder")
	}
	shape, err := query.NewProjectionResult(projection.expressions...)
	if err != nil {
		return ResultQuery[M, S]{}, err
	}
	plan, err := source.input.plan.WithResultShape(shape)
	if err != nil {
		return ResultQuery[M, S]{}, err
	}
	result := newResultQuery[M](source.input.origin, plan, projection.newDecoder)
	result.bound = source.input.bound
	result.named = slices.Clone(source.input.bindings)
	for index := range result.named {
		binding := &result.named[index]
		binding.publish = slices.ContainsFunc(projection.expressions, func(value query.ResultExpression) bool {
			if scalar, computed := value.Scalar(); computed {
				ref, named := scalar.AnnotationReference()
				return named && ref.Equal(binding.reference)
			}
			return false
		})
	}
	return result, nil
}

func (input annotationInput[M, R]) dynamicBinding() (dynamicBinding, error) {
	if _, scope, derived := input.plan.DerivedSource(); derived {
		if input.bound != nil {
			return dynamicBinding{}, invalidResultBuilder("model relation bindings cannot cross a result stage")
		}
		return dynamicBinding{scope: scope, annotations: input.plan}, nil
	}
	fields := make(map[string]query.FieldRef)
	for _, field := range input.plan.SourceFields() {
		fields[field.Name()] = field
	}
	result := dynamicBinding{fields: fields, annotations: input.plan}
	if input.bound != nil {
		binding := *input.bound
		if err := validateObjectBoundModel(binding); err != nil {
			return dynamicBinding{}, err
		}
		if input.origin.metadata == nil || input.origin.descriptor != reflect.TypeOf(binding.objectDescriptor) ||
			!reflect.DeepEqual(input.origin.metadata.metadata, binding.model) || input.plan.Table() != binding.model.DBTable ||
			!reflect.DeepEqual(input.plan.SourceFields(), modelFieldReferences(binding.model)) {
			return dynamicBinding{}, invalidResultBuilder("named relation binding differs from its original model source")
		}
		result.related = func(name string) (query.ScalarExpression, error) {
			path, err := dynamicValueRelation(binding, name)
			if err != nil {
				return query.ScalarExpression{}, err
			}
			return query.RelatedExpression(path)
		}
	}
	return result, nil
}

func (input annotationInput[M, R]) validateName(name string) error {
	if input.bound != nil {
		model := *input.bound
		field, found := findField(model.model.Fields, name)
		if found && field.Relation != nil || hasQueryRelation(model.snapshot, model.identity, name) {
			return invalidResultBuilder("annotation name conflicts with a declared relation accessor")
		}
	}
	return nil
}
func (input annotationInput[M, R]) validateNames() error {
	refs := input.plan.Annotations()
	if len(refs) != len(input.bindings) {
		return invalidResultBuilder("annotation definitions and decoders have different ownership")
	}
	for index, binding := range input.bindings {
		if binding.newCell == nil || !refs[index].Equal(binding.reference) || !input.plan.OwnsAnnotation(binding.reference) || binding.publish && !binding.reference.Selected() {
			return invalidResultBuilder("named decoder belongs to another definition")
		}
		if err := input.validateName(binding.reference.Name()); err != nil {
			return err
		}
	}
	return nil
}
