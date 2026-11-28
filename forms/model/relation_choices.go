package model

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const (
	MaximumModelChoiceLimit      = 100
	MaximumModelChoiceOffset     = 1_000_000
	MaximumModelChoiceTermBytes  = 256
	MaximumModelChoiceKeys       = 1024
	MaximumModelChoiceLabelBytes = 4096
)

// ChoiceRequest is one bounded search page. Limit must be explicit and positive;
// Offset is zero-based. Term is literal input for the configured search policy.
type ChoiceRequest struct {
	Term          string
	Offset, Limit int
}

// ModelChoiceConfig owns pure presentation and search policy. Callbacks must
// be safe for concurrent use and must not perform I/O or retain model values.
// A nil Search permits only an empty term. Order is copied and receives a
// primary-key tie-breaker when it does not already order by that key.
type ModelChoiceConfig[M any] struct {
	Label  func(M) (string, error)
	Search func(string) ([]orm.Predicate[M], error)
	Order  []orm.Ordering[M]
}

// ModelChoiceBindingKind separates target-only selection from a canonical
// source relation. The zero value identifies no declaration.
type ModelChoiceBindingKind string

const (
	ModelChoiceTarget   ModelChoiceBindingKind = "target"
	ModelChoiceRelation ModelChoiceBindingKind = "relation"
)

// ModelChoiceMetadata identifies a canonical target model and binding kind.
// A target-only declaration has no Source, SourceModel or Field; its Multiple
// value has no meaning. A relation declaration additionally identifies its
// canonical source field and multiplicity. Accessors return detached metadata.
// This describes binding, never current actor authority or permission to write.
type ModelChoiceMetadata struct {
	Kind        ModelChoiceBindingKind
	Source      ir.ModelIdentity
	SourceModel ir.Model
	Field       string
	Target      ir.ModelIdentity
	TargetModel ir.Model
	Multiple    bool
}

func (metadata ModelChoiceMetadata) clone() ModelChoiceMetadata {
	metadata.SourceModel = metadata.SourceModel.Clone()
	metadata.TargetModel = metadata.TargetModel.Clone()
	return metadata
}

// ModelChoices is an immutable target-only or canonical FK, OneToOne or M2M
// selection declaration. It retains neither a backend, actor, borrowed session, QuerySet nor
// evaluated rows. Filter derives a request-local scope; Search and Resolve are
// the explicit I/O boundary. Callers authorize both operations using current
// stored state and repeat selected-key resolution in their final write scope.
type ModelChoices[M any] struct {
	metadata ModelChoiceMetadata
	target   orm.Manager[M]
	primary  orm.AutoField[M]
	label    func(M) (string, error)
	search   func(string) ([]orm.Predicate[M], error)
	order    []orm.Ordering[M]
	filters  []orm.Predicate[M]
	valid    bool
}

// NewModelChoices declares a target model for a command or other selection
// with no source relation. It performs the same complete metadata, read-only
// primary-key capability and ordering checks as NewRelationChoices. Input
// multiplicity belongs to the consuming form, not this target-only declaration.
// Construction performs no I/O and invokes neither callback.
func NewModelChoices[M any](project orm.ProjectBinding, targetIdentity ir.ModelIdentity, target orm.Manager[M], config ModelChoiceConfig[M]) (ModelChoices[M], error) {
	return newModelChoices(project, ModelChoiceMetadata{Kind: ModelChoiceTarget, Target: targetIdentity}, target, config)
}

// NewRelationChoices resolves the field through the canonical project rather
// than trusting caller-supplied relation metadata. The complete target manager
// metadata must match. The target descriptor must provide the read-only
// orm.PrimaryKeyDescriptor capability; write support is not required. The
// canonical project currently supports AutoField primary keys.
// Construction performs no I/O and invokes neither callback.
func NewRelationChoices[M any](project orm.ProjectBinding, source ir.ModelIdentity, field string, target orm.Manager[M], config ModelChoiceConfig[M]) (ModelChoices[M], error) {
	var zero ModelChoices[M]
	sourceModel, present := project.Model(source)
	if !present {
		return zero, &Error{Path: "model_choices.source", Code: "not_in_project"}
	}
	metadata := ModelChoiceMetadata{Kind: ModelChoiceRelation, Source: source, SourceModel: sourceModel, Field: field}
	matches := 0
	if relation, found := project.Relation(source, field); found {
		if !relation.Cardinality.SingleValued() {
			return zero, &Error{Path: "model_choices.field", Code: "unsupported_relation"}
		}
		metadata.Target = relation.Target
		matches++
	}
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == source && relation.Field == field {
			metadata.Target, metadata.Multiple = relation.Target, true
			matches++
		}
	}
	if matches != 1 {
		return zero, &Error{Path: "model_choices.field", Code: "missing_or_ambiguous"}
	}
	return newModelChoices(project, metadata, target, config)
}

func newModelChoices[M any](project orm.ProjectBinding, metadata ModelChoiceMetadata, target orm.Manager[M], config ModelChoiceConfig[M]) (ModelChoices[M], error) {
	var zero ModelChoices[M]
	if config.Label == nil {
		return zero, &Error{Path: "model_choices.label", Code: "missing"}
	}
	var present bool
	metadata.TargetModel, present = project.Model(metadata.Target)
	if !present {
		return zero, &Error{Path: "model_choices.target", Code: "not_in_project"}
	}
	targetModel, err := target.Metadata()
	if err != nil {
		return zero, err
	}
	if !targetModel.Equal(metadata.TargetModel) {
		return zero, &Error{Path: "model_choices.target", Code: "model_mismatch"}
	}
	primary, err := target.PrimaryKeyField()
	if err != nil {
		return zero, err
	}
	key := orm.NewAutoField[M](primary)
	order := slices.Clone(config.Order)
	ordered, err := target.Using(nil).OrderBy(order...).Limit(1)
	if err != nil {
		return zero, err
	}
	if err := ordered.Plan().ValidateOrderings(); err != nil {
		return zero, err
	}
	hasKeyOrder := false
	for _, ordering := range ordered.Plan().Orderings() {
		expression := ordering.Expression()
		_, related := expression.RelationPath()
		// Field() also exposes a related expression's terminal field. An
		// identically named target key does not uniquely order these root rows.
		if expression.Kind() == query.ResultField && !related && ordering.Field().Name() == primary.Name && ordering.Field().Column() == primary.Column {
			hasKeyOrder = true
		}
	}
	if !hasKeyOrder {
		order = append(order, key.Asc())
	}
	return ModelChoices[M]{metadata: metadata, target: target, primary: key, label: config.Label, search: config.Search, order: order, valid: true}, nil
}

func (choices ModelChoices[M]) Initialized() bool { return choices.valid }
func (choices ModelChoices[M]) Metadata() ModelChoiceMetadata {
	return choices.metadata.clone()
}

// Filter narrows this declaration with typed target predicates. It copies the
// predicate slice and leaves the original declaration unchanged. Scope is never
// inferred from a submitted source/parent key. Invalid predicates fail during
// the next explicit operation before any query or label callback.
func (choices ModelChoices[M]) Filter(predicates ...orm.Predicate[M]) ModelChoices[M] {
	choices.filters = append(slices.Clone(choices.filters), predicates...)
	return choices
}

// ChoicePage is a detached search result. Its items do not authorize a submitted
// selection; use Resolve with the submitted keys and the current write scope.
type ChoicePage struct {
	metadata ModelChoiceMetadata
	items    []forms.Choice
	offset   int
	limit    int
	more     bool
	valid    bool
}

func (page ChoicePage) Initialized() bool             { return page.valid }
func (page ChoicePage) Metadata() ModelChoiceMetadata { return page.metadata.clone() }
func (page ChoicePage) Items() []forms.Choice         { return slices.Clone(page.items) }
func (page ChoicePage) Offset() int                   { return page.offset }
func (page ChoicePage) Limit() int                    { return page.limit }
func (page ChoicePage) More() bool                    { return page.more }

// ChoiceSelection contains the authorized rows found for a bounded distinct
// key request. Keys preserves first-requested order; Items follows the declared
// target ordering. Missing or unauthorized keys are absent from Items so pure
// form binding reports invalid_choice. Even an empty selection retains its
// canonical binding. This snapshot is not a durable authorization grant.
type ChoiceSelection struct {
	metadata ModelChoiceMetadata
	keys     []int64
	items    []forms.Choice
	valid    bool
}

func (selection ChoiceSelection) Initialized() bool             { return selection.valid }
func (selection ChoiceSelection) Metadata() ModelChoiceMetadata { return selection.metadata.clone() }
func (selection ChoiceSelection) Keys() []int64                 { return slices.Clone(selection.keys) }
func (selection ChoiceSelection) Items() []forms.Choice         { return slices.Clone(selection.items) }

// Search reads at most Limit+1 rows in one fresh query. The extra row determines
// More without an unbounded enumeration or a separate count. A backend that
// violates that row bound, returns duplicate keys or fails while closing its
// cursor publishes no partial page. The entire operation ends synchronously.
func (choices ModelChoices[M]) Search(ctx context.Context, backend db.Queryer, request ChoiceRequest) (ChoicePage, error) {
	if err := choices.validateCall(ctx, backend); err != nil {
		return ChoicePage{}, err
	}
	if request.Limit < 1 || request.Limit > MaximumModelChoiceLimit || request.Offset < 0 || request.Offset > MaximumModelChoiceOffset {
		return ChoicePage{}, &Error{Path: "model_choices.page", Code: "out_of_bounds"}
	}
	if !validChoiceText(request.Term, MaximumModelChoiceTermBytes) {
		return ChoicePage{}, &Error{Path: "model_choices.term", Code: "invalid"}
	}
	var predicates []orm.Predicate[M]
	if choices.search == nil {
		if request.Term != "" {
			return ChoicePage{}, &Error{Path: "model_choices.search", Code: "unavailable"}
		}
	} else {
		var err error
		predicates, err = choices.search(request.Term)
		if err = choiceContextError(ctx, err); err != nil {
			return ChoicePage{}, err
		}
	}
	objects, err := choices.query(backend, predicates, request.Offset, request.Limit+1)
	if err != nil {
		return ChoicePage{}, err
	}
	items, err := choices.read(ctx, objects, request.Limit+1, nil)
	if err != nil {
		return ChoicePage{}, err
	}
	more := len(items) > request.Limit
	if more {
		items = items[:request.Limit]
	}
	return ChoicePage{metadata: choices.metadata, items: items, offset: request.Offset, limit: request.Limit, more: more, valid: true}, nil
}

// Resolve reads only the requested keys within the caller's current Filter
// scope. It does not apply a search term, so selections outside a search page
// remain resolvable. Repeated requested keys are deduplicated without changing
// the original raw data's independent Form cleaning and Changed semantics.
func (choices ModelChoices[M]) Resolve(ctx context.Context, backend db.Queryer, keys []int64) (ChoiceSelection, error) {
	if err := choices.validateCall(ctx, backend); err != nil {
		return ChoiceSelection{}, err
	}
	requested := make(map[int64]bool, min(len(keys), MaximumModelChoiceKeys))
	owned := make([]int64, 0, min(len(keys), MaximumModelChoiceKeys))
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return ChoiceSelection{}, err
		}
		if requested[key] {
			continue
		}
		if len(owned) == MaximumModelChoiceKeys {
			return ChoiceSelection{}, &Error{Path: "model_choices.keys", Code: "limit_exceeded"}
		}
		requested[key] = true
		owned = append(owned, key)
	}
	objects, err := choices.query(backend, []orm.Predicate[M]{choices.primary.In(owned...)}, 0, len(owned)+1)
	if err != nil {
		return ChoiceSelection{}, err
	}
	var items []forms.Choice
	if len(owned) != 0 {
		items, err = choices.read(ctx, objects, len(owned), requested)
		if err != nil {
			return ChoiceSelection{}, err
		}
	}
	if err := choices.validateCall(ctx, backend); err != nil {
		return ChoiceSelection{}, err
	}
	return ChoiceSelection{metadata: choices.metadata, keys: owned, items: items, valid: true}, nil
}

func (choices ModelChoices[M]) query(backend db.Queryer, predicates []orm.Predicate[M], offset, limit int) (orm.QuerySet[M], error) {
	objects := choices.target.Using(backend).Filter(choices.filters...).Filter(predicates...).OrderBy(choices.order...)
	objects, err := objects.Offset(offset)
	if err != nil {
		return orm.QuerySet[M]{}, err
	}
	objects, err = objects.Limit(limit)
	if err != nil {
		return orm.QuerySet[M]{}, err
	}
	plan := objects.Plan()
	if err := plan.ValidateOrderings(); err != nil {
		return orm.QuerySet[M]{}, err
	}
	if where, present := plan.Where(); present {
		if err := plan.ValidatePredicate(where); err != nil {
			return orm.QuerySet[M]{}, err
		}
	}
	return objects, nil
}

func (choices ModelChoices[M]) read(ctx context.Context, objects orm.QuerySet[M], maximum int, requested map[int64]bool) ([]forms.Choice, error) {
	items := make([]forms.Choice, 0, maximum)
	seen := make(map[int64]bool, maximum)
	err := objects.Iterate(ctx, func(row M) error {
		if len(items) == maximum {
			return &Error{Path: "model_choices.result", Code: "limit_exceeded"}
		}
		value, present, err := choices.target.PrimaryKey(row)
		if err != nil {
			return err
		}
		key, valid := value.Integer()
		if !present || !valid {
			return &Error{Path: "model_choices.result", Code: "invalid_primary_key"}
		}
		if seen[key] {
			return &Error{Path: "model_choices.result", Code: "duplicate"}
		}
		if requested != nil && !requested[key] {
			return &Error{Path: "model_choices.result", Code: "unrequested_key"}
		}
		label, err := choices.label(row)
		if err = choiceContextError(ctx, err); err != nil {
			return err
		}
		if !validChoiceText(label, MaximumModelChoiceLabelBytes) {
			return &Error{Path: "model_choices.result", Code: "invalid_label"}
		}
		seen[key] = true
		items = append(items, forms.Choice{Value: forms.Integer(key), Label: label})
		return nil
	})
	if err = choiceContextError(ctx, err); err != nil {
		return nil, err
	}
	return items, nil
}

func (choices ModelChoices[M]) validateCall(ctx context.Context, backend db.Queryer) error {
	if nilSaveValue(ctx) {
		return &Error{Path: "model_choices.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !choices.valid {
		return &Error{Path: "model_choices", Code: "uninitialized"}
	}
	if nilSaveValue(backend) {
		return &Error{Path: "model_choices.backend", Code: "nil"}
	}
	if session, borrowed := backend.(db.SessionValidator); borrowed {
		return choiceContextError(ctx, session.ValidateSession(ctx))
	}
	return nil
}

func validChoiceText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func choiceContextError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil && !errors.Is(err, contextErr) {
		if err != nil {
			return errors.Join(err, contextErr)
		}
		return contextErr
	}
	return err
}

func (ModelChoices[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.ModelChoices{redacted}")
}
func (ModelChoiceMetadata) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.ModelChoiceMetadata{redacted}")
}
func (ChoicePage) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.ChoicePage{redacted}")
}
func (ChoiceSelection) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.ChoiceSelection{redacted}")
}
