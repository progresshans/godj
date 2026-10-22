package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const (
	MaximumInlines    = 8
	MaximumInlineRows = 100
)

// InlineAccess is the request's admitted operation policy, including the
// Site's deny overlay. It is a snapshot, not proof of current stored authority.
type InlineAccess struct {
	View, Add, Change, Delete bool
	choiceAccess              *inlineChoiceAccess
}

func (access InlineAccess) visible() bool {
	return access.View || access.Add || access.Change || access.Delete
}
func (access InlineAccess) read() bool { return access.View || access.Change }

// InlineSnapshot is loaded after admission. Related must read only this
// snapshot; it cannot hide I/O in form binding. Load must bound its query to the
// configured allocation cap and reject overflow, never truncate silently.
type InlineSnapshot[C any] struct {
	Current []C
	Related func(C, ir.ManyToManyField) ([]int64, bool)
}

// InlineConfig links one canonical FK to an Admin parent. ParentWithID is the
// generated parent's pure constructor; its result is checked against the route
// identity before loading children. A new parent uses the type's zero value.
// Every callback must be safe for concurrent calls and keep request data local.
type InlineConfig[P, C any] struct {
	Project       orm.ProjectBinding
	Parent        orm.Manager[P]
	Child         orm.Manager[C]
	ParentWithID  func(int64) P
	ForeignKey    string
	Label         string
	Form          FormConfig
	Set           forms.SetConfig
	Validators    []forms.SetValidator
	Permissions   Permissions
	RevisionField string
	Load          func(context.Context, auth.Principal, int64) (InlineSnapshot[C], error)
	// Validate is an optional read-only database check. Return a direct
	// RejectInline only for confirmed input diagnostics; execution errors stay
	// errors. The parent writer still owns final checks in its transaction.
	Validate func(context.Context, auth.Principal, formmodel.InlineSet[P, C]) error
}

// Inline is an immutable, type-erased view adapter. It owns no transaction or
// writer. The parent's Create/Update callback saves the complete submission.
type Inline struct {
	token                         *inlineToken
	parent, child                 ir.Model
	parentIdentity, childIdentity ir.ModelIdentity
	prefix, label, primary        string
	revision                      string
	fields                        []forms.Field
	inputs                        map[string]bool
	config                        forms.SetConfig
	permissions                   Permissions
	choicePermissions             []auth.Permission
	choiceSources                 []RelatedChoices
	searchOrigin                  func(context.Context, auth.Principal, int64, int64, InlineAccess) (ChoiceOrigin, error)
	bind                          func(context.Context, auth.Principal, int64, InlineAccess, *forms.Data) (inlineBound, error)
}
type inlineToken struct{ marker byte }
type inlineBound struct {
	definition  Inline
	access      InlineAccess
	parentID    int64
	set         forms.Set
	identities  []forms.Value
	revisions   []int64
	empty       *forms.SetForm
	policy      forms.SetConfig
	choiceViews map[int]map[string]choiceFieldView
}

func NewInline[P, C any](config InlineConfig[P, C]) (ModelInline[P, C], error) {
	if config.ParentWithID == nil || config.Load == nil || !validBoundedText(config.Label, MaximumDisplayBytes) {
		return ModelInline[P, C]{}, &ConfigError{Path: "inline", Code: "invalid"}
	}
	if config.Set.AbsoluteMax < 1 || config.Set.AbsoluteMax > MaximumInlineRows {
		return ModelInline[P, C]{}, &ConfigError{Path: "inline.rows", Code: "limit_exceeded"}
	}
	if err := validatePermissions(config.Permissions); err != nil {
		return ModelInline[P, C]{}, err
	}
	parent, err := config.Parent.Metadata()
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	child, err := config.Child.Metadata()
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	form, err := prepareModelForm(child, config.Form)
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	validators := slices.Clone(config.Validators)
	rows, err := forms.NewSetSpec(form, config.Set, validators...)
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	spec, err := formmodel.NewInlineSpec(config.Project, config.Parent, config.Child, config.ForeignKey, rows)
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	rows = spec.SetSpec()
	var childIdentity ir.ModelIdentity
	for _, relation := range config.Project.ForwardRelations() {
		current, present := config.Project.Model(relation.Source)
		if present && current.Equal(child) && relation.Field == config.ForeignKey {
			childIdentity = relation.Source
		}
	}
	_, choices, err := prepareRelatedChoices(child, childIdentity, rows.FormSpec(), config.Form.RelatedChoices, false)
	if err != nil {
		return ModelInline[P, C]{}, err
	}
	postClean := config.Form.Definition.PostClean.Clone()
	if slices.Contains(postClean.Fields, config.ForeignKey) {
		return ModelInline[P, C]{}, &ConfigError{Path: "inline.post_clean", Code: "parent_is_server_owned"}
	}
	definition := Inline{token: &inlineToken{}, parent: parent, child: child, prefix: rows.Config().Prefix, label: config.Label, fields: rows.FormSpec().Fields(), config: rows.Config(), permissions: config.Permissions, choicePermissions: choices, choiceSources: slices.Clone(config.Form.RelatedChoices)}
	for _, field := range definition.fields {
		if field.Name() == "ORDER" || field.Name() == "DELETE" {
			return ModelInline[P, C]{}, &ConfigError{Path: "inline.form", Code: "reserved"}
		}
	}
	if config.RevisionField != "" {
		found := false
		for _, field := range child.Fields {
			if field.Name == config.RevisionField {
				found = field.Kind == ir.FieldInteger && !field.Nullable && !field.PrimaryKey
			}
		}
		if !found || slices.Contains(postClean.Fields, config.RevisionField) || slices.ContainsFunc(definition.fields, func(field forms.Field) bool {
			return field.Name() == config.RevisionField || field.Name() == "expected_revision"
		}) {
			return ModelInline[P, C]{}, &ConfigError{Path: "inline.revision", Code: "invalid"}
		}
		definition.revision = config.RevisionField
	}
	for _, relation := range config.Project.ForwardRelations() {
		model, present := config.Project.Model(relation.Source)
		if present && model.Equal(child) && relation.Field == config.ForeignKey {
			definition.parentIdentity, definition.childIdentity = relation.Target, relation.Source
		}
	}
	for _, field := range child.Fields {
		if field.PrimaryKey {
			definition.primary = field.Name
		}
	}
	definition.inputs = inlineInputNames(definition)
	parentKey := ""
	for _, field := range parent.Fields {
		if field.PrimaryKey {
			parentKey = field.Name
		}
	}
	validateParent := func(owner P, parentID int64) error {
		values, err := config.Parent.ModelValues(owner)
		if err != nil {
			return err
		}
		key := values[parentKey]
		integer, present := key.Integer()
		if parentID == 0 && !key.IsNull() || parentID != 0 && (!present || integer != parentID) {
			return &ConfigError{Path: "inline.parent", Code: "identity_mismatch"}
		}
		return nil
	}
	bindSnapshot := func(ctx context.Context, parentID int64, access InlineAccess, owner P, snapshot InlineSnapshot[C], currentForm forms.Spec, submitted *forms.Data, resolve func(context.Context, formmodel.InlinePreflight[P, C]) (forms.RowModelChoices, error), selections *forms.RowModelChoices) (formmodel.InlineSet[P, C], inlineBound, error) {
		if err := validateParent(owner, parentID); err != nil {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, err
		}
		if len(snapshot.Current) > rows.Config().AbsoluteMax {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, &ConfigError{Path: "inline.current", Code: "limit_exceeded"}
		}
		if len(snapshot.Current) != 0 && (parentID == 0 || !access.read()) {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, &ConfigError{Path: "inline.current", Code: "not_readable"}
		}
		policy := rows.Config()
		policy.ReadOnlyInitial = policy.ReadOnlyInitial || !access.Change
		policy.CanDelete = policy.CanDelete && access.Delete
		policy.CanOrder = policy.CanOrder && access.Change
		if !access.Add {
			policy.ExtraForms = 0
			if submitted == nil {
				policy.MinForms = 0
			}
		}
		requestRows, err := forms.NewSetSpec(currentForm, policy, validators...)
		if err != nil {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, err
		}
		if selections != nil {
			requestRows, err = requestRows.WithRowModelChoices(*selections)
			if err != nil {
				return formmodel.InlineSet[P, C]{}, inlineBound{}, err
			}
		}
		requestSpec, err := formmodel.NewInlineSpec(config.Project, config.Parent, config.Child, config.ForeignKey, requestRows)
		if err != nil {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, err
		}
		var related []func(C, ir.ManyToManyField) ([]int64, bool)
		if snapshot.Related != nil {
			related = append(related, snapshot.Related)
		}
		var bound formmodel.InlineSet[P, C]
		if submitted == nil {
			bound, err = requestSpec.Unbound(owner, snapshot.Current, related...)
		} else if resolve == nil {
			bound, err = requestSpec.Bind(ctx, *submitted, owner, snapshot.Current, postClean, related...)
		} else {
			input, preflightErr := requestSpec.Preflight(ctx, *submitted, owner, snapshot.Current, postClean, related...)
			if preflightErr != nil {
				return formmodel.InlineSet[P, C]{}, inlineBound{}, preflightErr
			}
			var selections forms.RowModelChoices
			if input.AdmissionValid() {
				selections, err = resolve(ctx, input)
				if contextErr := ctx.Err(); contextErr != nil {
					err = errors.Join(err, contextErr)
				}
				if err != nil {
					return formmodel.InlineSet[P, C]{}, inlineBound{}, err
				}
			}
			bound, err = input.Bind(ctx, selections)
		}
		if err != nil {
			return formmodel.InlineSet[P, C]{}, inlineBound{}, err
		}
		result := inlineBound{definition: definition, access: access, parentID: parentID, set: bound.FormSet(), policy: policy}
		if access.Add {
			empty, err := requestSpec.EmptyForm(owner)
			if err != nil {
				return formmodel.InlineSet[P, C]{}, inlineBound{}, err
			}
			result.empty = &empty
		}
		for _, row := range result.set.Forms() {
			identity, _ := bound.Identity(row.Index())
			result.identities = append(result.identities, identity)
			var revision int64
			if definition.revision != "" {
				current, present, currentErr := bound.Current(row.Index())
				if currentErr != nil {
					return formmodel.InlineSet[P, C]{}, inlineBound{}, currentErr
				}
				if present {
					values, valueErr := config.Child.ModelValues(current)
					if valueErr != nil {
						return formmodel.InlineSet[P, C]{}, inlineBound{}, valueErr
					}
					revision, present = values[definition.revision].Integer()
					if !present || revision < 1 {
						return formmodel.InlineSet[P, C]{}, inlineBound{}, &ConfigError{Path: "inline.revision", Code: "invalid_result"}
					}
				}
				if submitted != nil {
					raw, exists := submitted.Get(row.Prefix() + "-expected_revision")
					if revision != 0 && (!exists || len(raw) != 1 || raw[0] != strconv.FormatInt(revision, 10)) || revision == 0 && exists && (len(raw) != 1 || raw[0] != "") {
						return formmodel.InlineSet[P, C]{}, inlineBound{}, NewOperationError(OperationConflict, nil)
					}
				}
			}
			result.revisions = append(result.revisions, revision)
			if submitted != nil {
				if !access.Delete {
					if raw, present := submitted.Get(row.Prefix() + "-DELETE"); present && len(raw) != 0 {
						return formmodel.InlineSet[P, C]{}, inlineBound{}, NewOperationError(OperationDenied, nil)
					}
				}
				if !access.Change {
					if raw, present := submitted.Get(row.Prefix() + "-ORDER"); present && len(raw) != 0 {
						return formmodel.InlineSet[P, C]{}, inlineBound{}, NewOperationError(OperationDenied, nil)
					}
				}
				if !access.Add && row.Index() >= result.set.InitialForms() && len(row.Form().Changed()) != 0 {
					return formmodel.InlineSet[P, C]{}, inlineBound{}, NewOperationError(OperationDenied, nil)
				}
			}
		}
		return bound, result, nil
	}
	definition.bind = func(ctx context.Context, actor auth.Principal, parentID int64, access InlineAccess, submitted *forms.Data) (inlineBound, error) {
		if err := validateInlineAccess(ctx, actor, parentID, access, definition); err != nil {
			return inlineBound{}, err
		}
		var owner P
		if parentID != 0 {
			owner = config.ParentWithID(parentID)
		}
		if err := validateParent(owner, parentID); err != nil {
			return inlineBound{}, err
		}
		currentForm := rows.FormSpec()
		var err error
		var snapshot InlineSnapshot[C]
		if parentID != 0 && access.read() {
			snapshot, err = config.Load(ctx, actor, parentID)
			if err != nil {
				return inlineBound{}, err
			}
			if err := ctx.Err(); err != nil {
				return inlineBound{}, err
			}
		}

		admitted := submitted == nil
		resolve := func(work context.Context, input formmodel.InlinePreflight[P, C]) (forms.RowModelChoices, error) {
			admitted = true
			selections := make(map[int]map[string][]forms.Choice, input.TotalForms())
			for index := 0; index < input.TotalForms(); index++ {
				identity, _ := input.Identity(index)
				rowID, _ := identity.AsInteger()
				readOnly, err := input.ReadOnly(index)
				if err != nil {
					return forms.RowModelChoices{}, err
				}
				origin := definition.choiceOrigin(parentID, rowID)
				selected, err := resolveRowChoices(work, actor, origin, definition.choiceSources, readOnly, access.allowedChoices(), func(name string) ([]int64, bool, error) { return input.ChoiceKeys(index, name) })
				if err != nil {
					return forms.RowModelChoices{}, err
				}
				selections[index] = selected
			}
			return forms.NewRowModelChoices(selections, nil), nil
		}
		bound, result, err := bindSnapshot(ctx, parentID, access, owner, snapshot, currentForm, submitted, resolve, nil)
		if err != nil {
			return inlineBound{}, err
		}

		if admitted && len(definition.choiceSources) != 0 {
			rowChoices := make(map[int]map[string][]forms.Choice)
			result.choiceViews = make(map[int]map[string]choiceFieldView)
			for _, row := range result.set.Forms() {
				index := row.Index()
				identity, _ := bound.Identity(index)
				rowID, _ := identity.AsInteger()
				origin := definition.choiceOrigin(parentID, rowID)
				if submitted == nil {
					var initial map[string]forms.Value
					if rowID != 0 {
						// Formset ORDER/DELETE controls are not model input.
						// Project only the server values selected by this form.
						initial = make(map[string]forms.Value, len(currentForm.Fields()))
						for _, field := range currentForm.Fields() {
							if value, present := row.Form().Initial().Get(field.Name()); present {
								initial[field.Name()] = value
							}
						}
					}
					keys, keyErr := modelChoiceKeys(child, currentForm, forms.Data{}, initial, true)
					if keyErr != nil {
						return inlineBound{}, keyErr
					}
					selected, resolveErr := resolveRowChoices(ctx, actor, origin, definition.choiceSources, row.Form().ReadOnly(), access.allowedChoices(), keys)
					if resolveErr != nil {
						return inlineBound{}, resolveErr
					}
					rowChoices[index] = selected
				}
				views, viewErr := choiceViews(ctx, actor, origin, definition.choiceSources, "", row.Form().ReadOnly(), submitted == nil)
				if viewErr != nil {
					return inlineBound{}, viewErr
				}
				result.choiceViews[index] = views
			}
			var prototype map[string][]forms.Choice
			if access.Add {
				origin := definition.choiceOrigin(parentID, 0)
				keys, keyErr := modelChoiceKeys(child, currentForm, forms.Data{}, nil, true)
				if keyErr != nil {
					return inlineBound{}, keyErr
				}
				prototype, err = resolveRowChoices(ctx, actor, origin, definition.choiceSources, false, access.allowedChoices(), keys)
				if err != nil {
					return inlineBound{}, err
				}
				views, viewErr := choiceViews(ctx, actor, origin, definition.choiceSources, "", false, submitted == nil)
				if viewErr != nil {
					return inlineBound{}, viewErr
				}
				result.choiceViews[-1] = views
			}
			if submitted == nil {
				selections := forms.NewRowModelChoices(rowChoices, prototype)
				views := result.choiceViews
				bound, result, err = bindSnapshot(ctx, parentID, access, owner, snapshot, currentForm, nil, nil, &selections)
				if err != nil {
					return inlineBound{}, err
				}
				result.choiceViews = views
			} else if access.Add {
				prototypeRows, prototypeErr := rows.WithConfig(result.policy)
				if prototypeErr != nil {
					return inlineBound{}, prototypeErr
				}
				prototypeRows, prototypeErr = prototypeRows.WithRowModelChoices(forms.NewRowModelChoices(nil, prototype))
				if prototypeErr != nil {
					return inlineBound{}, prototypeErr
				}
				prototypeSpec, prototypeErr := formmodel.NewInlineSpec(config.Project, config.Parent, config.Child, config.ForeignKey, prototypeRows)
				if prototypeErr != nil {
					return inlineBound{}, prototypeErr
				}
				empty, prototypeErr := prototypeSpec.EmptyForm(owner)
				if prototypeErr != nil {
					return inlineBound{}, prototypeErr
				}
				result.empty = &empty
			}
		}
		if submitted != nil && config.Validate != nil && bound.FormSet().NonFormErrors().Empty() {
			if err := config.Validate(ctx, actor, bound); err != nil {
				if ctx.Err() != nil {
					return inlineBound{}, errors.Join(err, ctx.Err())
				}
				rejection, ok := err.(*InlineRejection)
				if !ok || rejection == nil || rejection.prefix != definition.prefix {
					return inlineBound{}, err
				}
				result, err = applyInlineRejection(result, rejection)
				if err != nil {
					return inlineBound{}, err
				}
			} else if err := ctx.Err(); err != nil {
				return inlineBound{}, err
			}
		}
		return result, nil
	}

	definition.searchOrigin = func(ctx context.Context, actor auth.Principal, parentID, rowID int64, access InlineAccess) (ChoiceOrigin, error) {
		if err := validateInlineAccess(ctx, actor, parentID, access, definition); err != nil {
			return ChoiceOrigin{}, err
		}
		if rowID == 0 {
			if !access.Add {
				return ChoiceOrigin{}, NewOperationError(OperationDenied, nil)
			}
			return definition.choiceOrigin(parentID, 0), nil
		}
		if parentID == 0 || !access.Change || config.Set.ReadOnlyInitial {
			return ChoiceOrigin{}, NewOperationError(OperationDenied, nil)
		}
		owner := config.ParentWithID(parentID)
		if err := validateParent(owner, parentID); err != nil {
			return ChoiceOrigin{}, err
		}
		snapshot, err := config.Load(ctx, actor, parentID)
		if err != nil {
			return ChoiceOrigin{}, err
		}
		if err := ctx.Err(); err != nil {
			return ChoiceOrigin{}, err
		}
		if len(snapshot.Current) > rows.Config().AbsoluteMax {
			return ChoiceOrigin{}, &ConfigError{Path: "inline.current", Code: "limit_exceeded"}
		}
		var readers []func(C, ir.ManyToManyField) ([]int64, bool)
		if snapshot.Related != nil {
			readers = append(readers, snapshot.Related)
		}
		current, err := spec.Unbound(owner, snapshot.Current, readers...)
		if err != nil {
			return ChoiceOrigin{}, err
		}
		for _, row := range current.FormSet().Forms() {
			identity, present := current.Identity(row.Index())
			key, valid := identity.AsInteger()
			if present && valid && key == rowID {
				return definition.choiceOrigin(parentID, rowID), nil
			}
		}
		return ChoiceOrigin{}, ErrObjectNotFound
	}
	return ModelInline[P, C]{view: definition, bindCurrent: func(ctx context.Context, parentID int64, access InlineAccess, data forms.Data, current InlineCurrent[P, C]) (formmodel.InlineSet[P, C], error) {
		resolve := func(work context.Context, input formmodel.InlinePreflight[P, C]) (forms.RowModelChoices, error) {
			if current.ResolveChoices != nil {
				return current.ResolveChoices(work, input)
			}
			if access.Add || access.Change {
				for _, field := range rows.FormSpec().Fields() {
					if field.ModelChoice() {
						return forms.RowModelChoices{}, &ConfigError{Path: "inline.current.choices", Code: "missing"}
					}
				}
			}
			return forms.RowModelChoices{}, nil
		}
		bound, _, err := bindSnapshot(ctx, parentID, access, current.Parent, current.Children, rows.FormSpec(), &data, resolve, nil)
		return bound, err
	}}, nil
}

// InlineSubmission carries the server-admitted inline inputs of one parent
// mutation. The writer must re-read/rebind in its transaction and intersect
// this access snapshot with current authority. It must not commit each child
// separately or infer a commit from any prepared value.
type InlineSubmission struct {
	writerActor string
	owner       *inlineToken
	parentID    int64
	entries     []inlineBound
}

func (submission InlineSubmission) Lookup(prefix string) (forms.Data, InlineAccess, bool) {
	for _, entry := range submission.entries {
		if entry.definition.prefix == prefix {
			return entry.set.Submitted(), entry.access, true
		}
	}
	return forms.Data{}, InlineAccess{}, false
}
func (submission InlineSubmission) FormSet(prefix string) (forms.Set, bool) {
	for _, entry := range submission.entries {
		if entry.definition.prefix == prefix {
			return entry.set, true
		}
	}
	return forms.Set{}, false
}
func (submission InlineSubmission) valid() bool {
	for _, entry := range submission.entries {
		if !entry.set.Valid() {
			return false
		}
	}
	return true
}

func (Inline) Format(state fmt.State, _ rune) { fmt.Fprint(state, "admin.Inline{redacted}") }
func (InlineSubmission) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.InlineSubmission{redacted}")
}

func inlineInputNames(definition Inline) map[string]bool {
	names := map[string]bool{definition.primary: true}
	for _, field := range definition.fields {
		names[field.Name()] = true
		if field.IsFile() && field.Widget() == forms.ClearableFileInput {
			names[field.Name()+"-clear"] = true
		}
	}
	if definition.config.CanDelete {
		names["DELETE"] = true
	}
	if definition.config.CanOrder {
		names["ORDER"] = true
	}
	if definition.revision != "" {
		names["expected_revision"] = true
	}
	return names
}

func inlineInputRule(definition Inline, name string) (int, bool) {
	prefix := definition.prefix + "-"
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(name, prefix)
	for _, management := range []string{"TOTAL_FORMS", "INITIAL_FORMS", "MIN_NUM_FORMS", "MAX_NUM_FORMS"} {
		if rest == management {
			return MaximumInputValues, true
		}
	}
	index, field, found := strings.Cut(rest, "-")
	parsed, err := strconv.Atoi(index)
	if !found || err != nil || parsed < 0 || parsed >= definition.config.AbsoluteMax || strconv.Itoa(parsed) != index || !definition.inputs[field] {
		return 0, false
	}
	return -MaximumInputValues, true
}

// RejectInline identifies one confirmed validation rejection after no mutation
// or a confirmed rollback. index -1 is a whole-set rejection, required for
// protected deletion and request admission so DELETE cannot hide the error.
// Wrapped/joined errors do not qualify for redisplay.
func RejectInline(prefix string, index int, failures validation.Errors, cause error) error {
	if prefix == "" || index < -1 || failures.Empty() {
		return &ConfigError{Path: "inline.rejection", Code: "invalid"}
	}
	return &InlineRejection{prefix: prefix, index: index, failures: failures, cause: cause}
}

type InlineRejection struct {
	prefix   string
	index    int
	failures validation.Errors
	cause    error
}

func (*InlineRejection) Error() string { return "admin: inline validation rejected" }
func (failure *InlineRejection) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}
func (InlineRejection) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.InlineRejection{redacted}")
}
func (InlineRejection) MarshalJSON() ([]byte, error) {
	return []byte(`"admin.InlineRejection{redacted}"`), nil
}

func applyInlineRejection(entry inlineBound, rejection *InlineRejection) (inlineBound, error) {
	if rejection == nil || rejection.prefix != entry.definition.prefix {
		return inlineBound{}, &ConfigError{Path: "inline.rejection", Code: "unknown_inline"}
	}
	var err error
	if rejection.index == -1 {
		entry.set, err = entry.set.WithErrors(rejection.failures)
	} else {
		entry.set, err = entry.set.WithFormErrors(rejection.index, rejection.failures)
	}
	return entry, err
}

func (definition Inline) choiceOrigin(parentID, rowID int64) ChoiceOrigin {
	return ChoiceOrigin{source: definition.childIdentity, operation: ChoiceInline, objectID: rowID, parent: definition.parentIdentity, parentID: parentID, rowID: rowID, name: definition.prefix, valid: true}
}
func resolveRowChoices(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, sources []RelatedChoices, readOnly bool, access map[string]bool, keys choiceKeys) (map[string][]forms.Choice, error) {
	if ctx == nil {
		return nil, &ConfigError{Path: "operation.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string][]forms.Choice, len(sources))
	for _, source := range sources {
		if readOnly {
			if access != nil && !access[source.choiceField()] {
				continue
			}
			if err := validatePrincipalPermission(ctx, actor, source.choicePermission()); err != nil {
				if denied, ok := err.(*ConfigError); ok && denied.Path == "operation.permission" && denied.Code == "denied" {
					continue
				}
				return nil, err
			}
		}
		selected, valid, err := keys(source.choiceField())
		if err != nil {
			return nil, err
		}
		if !valid {
			continue
		}
		result[source.choiceField()], err = source.resolve(ctx, actor, origin, selected)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

type inlineChoiceAccess struct{ allowed map[string]bool }

func (access InlineAccess) allowedChoices() map[string]bool {
	if access.choiceAccess == nil {
		return nil
	}
	return access.choiceAccess.allowed
}
