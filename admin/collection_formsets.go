package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CollectionFormSetConfig declares one bounded model cohort, without a parent
// object. Load returns the complete authorized cohort, including unchanged rows;
// it must reject overflow rather than silently truncate. Related reads only
// that snapshot. Run rebinds the sealed submission with BindCurrent in its own
// write scope, then owns revision-fenced persistence, collections, deletion,
// audit and the terminal commit outcome. All callbacks must be concurrency safe.
type CollectionFormSetConfig[M any] struct {
	Name, Label           string
	Project               orm.ProjectBinding
	Source                ir.ModelIdentity
	Manager               orm.Manager[M]
	Permissions           Permissions
	AdditionalPermissions []auth.Permission
	Form                  FormConfig
	Set                   forms.SetConfig
	Validators            []forms.SetValidator
	RevisionField         string
	Load                  func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[M], error)
	// Validate is an optional read-only database check. It is not called by
	// BindCurrent and cannot replace the writer's checks in its own scope.
	Validate func(context.Context, auth.Principal, formmodel.InstanceSet[M]) error
	Run      func(context.Context, auth.Principal, CollectionSetSubmission) (CollectionSetResult, error)
}

// CollectionFormSet is the immutable view of a typed declaration. Keep the
// ModelCollectionFormSet for the writer and register View on the same source.
type CollectionFormSet struct {
	token             *collectionSetToken
	model             ir.Model
	identity          ir.ModelIdentity
	choiceSources     []RelatedChoices
	choicePermissions []auth.Permission
	name, label       string
	primary, revision string
	permissions       Permissions
	additional        []auth.Permission
	fields            []forms.Field
	config            forms.SetConfig
	rules             inputRules
	files             map[string]bool
	bind              func(context.Context, auth.Principal, CollectionSetAccess, *forms.Data) (collectionFormSetBound, error)
	searchOrigin      func(context.Context, auth.Principal, CollectionSetAccess, int64) (ChoiceOrigin, error)
}

type collectionSetToken struct{ marker byte }

type collectionFormSetBound struct {
	choiceViews map[int]map[string]choiceFieldView
	set         forms.Set
	empty       *forms.SetForm
	identities  []forms.Value
	revisions   []int64
	access      CollectionSetAccess
	policy      forms.SetConfig
	run         func(context.Context) (CollectionSetResult, error)
}

type CollectionFormSetDescriptor struct {
	Name, Label, RevisionField string
	Source                     ir.ModelIdentity
	Permissions                Permissions
	AdditionalPermissions      []auth.Permission
	FormFields                 []forms.Field
	Config                     forms.SetConfig
}

func NewCollectionFormSet[M any](config CollectionFormSetConfig[M]) (ModelCollectionFormSet[M], error) {
	fail := func(path, code string) (ModelCollectionFormSet[M], error) {
		return ModelCollectionFormSet[M]{}, &ConfigError{Path: "collection_formset." + path, Code: code}
	}
	if !validSlug(config.Name) || !validBoundedText(config.Label, MaximumDisplayBytes) || config.Run == nil || config.Load == nil {
		return fail("config", "invalid")
	}
	if err := validatePermissions(config.Permissions); err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	policy := config.Set
	if !validIdentifier(policy.Prefix, MaximumModelBytes) || !policy.ValidateMin || !policy.ValidateMax || policy.AbsoluteMax > MaximumInlineRows || policy.AbsoluteMax < 1 {
		return fail("set", "unsupported_policy")
	}
	model, err := config.Manager.Metadata()
	if err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	canonical, present := config.Project.Model(config.Source)
	if !present || !canonical.Equal(model) {
		return fail("source", "binding_mismatch")
	}
	form, err := prepareModelForm(model, config.Form)
	if err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	validators := slices.Clone(config.Validators)
	rows, err := forms.NewSetSpec(form, policy, validators...)
	if err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	if _, err := formmodel.UnboundSet(config.Manager, rows, nil); err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	_, choices, err := prepareRelatedChoices(model, config.Source, form, config.Form.RelatedChoices, false)
	if err != nil {
		return ModelCollectionFormSet[M]{}, err
	}
	additional := slices.Clone(config.AdditionalPermissions)
	for _, permission := range additional {
		if err := validatePermission("collection_formset.additional_permissions", permission); err != nil {
			return ModelCollectionFormSet[M]{}, err
		}
	}
	postClean := config.Form.Definition.PostClean.Clone()
	definition := CollectionFormSet{
		token: &collectionSetToken{}, model: model, identity: config.Source, name: config.Name, label: config.Label,
		permissions: config.Permissions, additional: additional, fields: form.Fields(), config: policy,
		choiceSources: slices.Clone(config.Form.RelatedChoices), choicePermissions: choices,
	}
	for _, field := range model.Fields {
		if field.PrimaryKey {
			definition.primary = field.Name
		}
	}
	for _, field := range definition.fields {
		if field.Name() == "ORDER" || field.Name() == "DELETE" || field.Name() == "expected_revision" {
			return fail("form", "reserved")
		}
	}
	if config.RevisionField != "" {
		valid := slices.ContainsFunc(model.Fields, func(field ir.Field) bool {
			return field.Name == config.RevisionField && field.Kind == ir.FieldInteger && !field.Nullable && !field.PrimaryKey
		})
		if !valid || slices.Contains(postClean.Fields, config.RevisionField) || slices.ContainsFunc(definition.fields, func(field forms.Field) bool { return field.Name() == config.RevisionField }) {
			return fail("revision", "invalid")
		}
		definition.revision = config.RevisionField
	}
	definition.rules, definition.files = collectionSetInputRules(definition)
	if len(definition.rules) > MaximumInputValues {
		return fail("form", "limit_exceeded")
	}

	bindSnapshot := func(ctx context.Context, access CollectionSetAccess, snapshot CollectionSetSnapshot[M], submitted *forms.Data, resolve func(context.Context, formmodel.SetPreflight[M]) (forms.RowModelChoices, error), selections *forms.RowModelChoices) (formmodel.InstanceSet[M], collectionFormSetBound, error) {
		var zero formmodel.InstanceSet[M]
		if len(snapshot.Current) > policy.AbsoluteMax {
			return zero, collectionFormSetBound{}, &ConfigError{Path: "collection_formset.current", Code: "limit_exceeded"}
		}
		if len(snapshot.Current) != 0 && !access.read() {
			return zero, collectionFormSetBound{}, NewOperationError(OperationDenied, nil)
		}
		requestPolicy := policy
		requestPolicy.ReadOnlyInitial = policy.ReadOnlyInitial || !access.Change
		requestPolicy.CanDelete = policy.CanDelete && access.Delete
		requestPolicy.CanOrder = policy.CanOrder && access.Change
		if !access.Add {
			requestPolicy.ExtraForms = 0
			if submitted == nil {
				requestPolicy.MinForms = 0
			}
		}
		requestRows, err := forms.NewSetSpec(form, requestPolicy, validators...)
		if err != nil {
			return zero, collectionFormSetBound{}, err
		}
		if selections != nil {
			requestRows, err = requestRows.WithRowModelChoices(*selections)
			if err != nil {
				return zero, collectionFormSetBound{}, err
			}
		}
		var related []func(M, ir.ManyToManyField) ([]int64, bool)
		if snapshot.Related != nil {
			related = append(related, snapshot.Related)
		}
		var typed formmodel.InstanceSet[M]
		if submitted == nil {
			typed, err = formmodel.UnboundSet(config.Manager, requestRows, snapshot.Current, related...)
		} else {
			input, preflightErr := formmodel.PreflightSet(ctx, config.Manager, requestRows, *submitted, snapshot.Current, postClean, related...)
			if preflightErr != nil {
				return zero, collectionFormSetBound{}, preflightErr
			}
			var choices forms.RowModelChoices
			if input.AdmissionValid() {
				// Identity, current revision and operation admission precede every
				// target read. A DELETE row cannot suppress any of these checks.
				for index := 0; index < input.TotalForms(); index++ {
					current, present, currentErr := input.Current(index)
					if currentErr != nil {
						return zero, collectionFormSetBound{}, currentErr
					}
					_, revisionErr := collectionSetRevision(config.Manager, definition, current, present, *submitted, index, true)
					if revisionErr != nil {
						return zero, collectionFormSetBound{}, revisionErr
					}
					for _, control := range []struct {
						name    string
						allowed bool
					}{{"DELETE", requestPolicy.CanDelete}, {"ORDER", requestPolicy.CanOrder}} {
						if raw, found := submitted.Get(policy.Prefix + "-" + strconv.Itoa(index) + "-" + control.name); !control.allowed && found && len(raw) != 0 {
							return zero, collectionFormSetBound{}, NewOperationError(OperationDenied, nil)
						}
					}
				}
				choices, err = resolve(ctx, input)
				if contextErr := ctx.Err(); contextErr != nil {
					err = errors.Join(err, contextErr)
				}
				if err != nil {
					return zero, collectionFormSetBound{}, err
				}
			}
			typed, err = input.Bind(ctx, choices)
		}
		if err != nil {
			return zero, collectionFormSetBound{}, err
		}
		result := collectionFormSetBound{set: typed.FormSet(), access: access, policy: requestPolicy}
		if access.Add {
			empty, err := requestRows.EmptyForm()
			if err != nil {
				return zero, collectionFormSetBound{}, err
			}
			result.empty = &empty
		}
		for _, row := range result.set.Forms() {
			identity, present := typed.Identity(row.Index())
			if key, integer := identity.AsInteger(); present && (!integer || key <= 0) {
				return zero, collectionFormSetBound{}, &ConfigError{Path: "collection_formset.current", Code: "invalid_identity"}
			}
			result.identities = append(result.identities, identity)
			current, present, currentErr := typed.Current(row.Index())
			if currentErr != nil {
				return zero, collectionFormSetBound{}, currentErr
			}
			revision, revisionErr := collectionSetRevision(config.Manager, definition, current, present, forms.Data{}, row.Index(), false)
			if revisionErr != nil {
				return zero, collectionFormSetBound{}, revisionErr
			}
			result.revisions = append(result.revisions, revision)
			if submitted != nil && !access.Add && row.Index() >= result.set.InitialForms() && len(row.Form().Changed()) != 0 {
				return zero, collectionFormSetBound{}, NewOperationError(OperationDenied, nil)
			}
		}
		return typed, result, nil
	}
	definition.bind = func(ctx context.Context, actor auth.Principal, access CollectionSetAccess, submitted *forms.Data) (collectionFormSetBound, error) {
		if err := validateCollectionSetAccess(ctx, actor, access, definition); err != nil {
			return collectionFormSetBound{}, err
		}
		snapshot, err := config.Load(ctx, actor, access)
		if contextErr := ctx.Err(); contextErr != nil {
			err = errors.Join(err, contextErr)
		}
		if err != nil {
			return collectionFormSetBound{}, err
		}
		admitted := submitted == nil
		resolve := func(ctx context.Context, input formmodel.SetPreflight[M]) (forms.RowModelChoices, error) {
			admitted = true
			selections := make(map[int]map[string][]forms.Choice)
			for index := 0; index < input.TotalForms(); index++ {
				if index >= input.InitialForms() && !access.Add {
					continue
				}
				identity, _ := input.Identity(index)
				key, _ := identity.AsInteger()
				readonly, err := input.ReadOnly(index)
				if err != nil {
					return forms.RowModelChoices{}, err
				}
				selections[index], err = resolveRowChoices(ctx, actor, definition.choiceOrigin(key), definition.choiceSources, readonly, access.allowedChoices(), func(name string) ([]int64, bool, error) { return input.ChoiceKeys(index, name) })
				if err != nil {
					return forms.RowModelChoices{}, err
				}
			}
			return forms.NewRowModelChoices(selections, nil), nil
		}
		typed, bound, err := bindSnapshot(ctx, access, snapshot, submitted, resolve, nil)
		if err != nil {
			return collectionFormSetBound{}, err
		}
		if admitted && len(definition.choiceSources) != 0 {
			selections := make(map[int]map[string][]forms.Choice)
			views := make(map[int]map[string]choiceFieldView)
			for _, row := range bound.set.Forms() {
				index := row.Index()
				key, _ := bound.identities[index].AsInteger()
				origin := definition.choiceOrigin(key)
				if submitted == nil {
					var initial map[string]forms.Value
					if key != 0 {
						initial = make(map[string]forms.Value)
						for _, field := range form.Fields() {
							if value, present := row.Form().Initial().Get(field.Name()); present {
								initial[field.Name()] = value
							}
						}
					}
					keys, keyErr := modelChoiceKeys(model, form, forms.Data{}, initial, true)
					if keyErr != nil {
						return collectionFormSetBound{}, keyErr
					}
					selections[index], err = resolveRowChoices(ctx, actor, origin, definition.choiceSources, row.Form().ReadOnly(), access.allowedChoices(), keys)
					if err != nil {
						return collectionFormSetBound{}, err
					}
				}
				views[index], err = choiceViews(ctx, actor, origin, definition.choiceSources, "", row.Form().ReadOnly(), submitted == nil)
				if err != nil {
					return collectionFormSetBound{}, err
				}
			}
			var prototype map[string][]forms.Choice
			if access.Add {
				keys, keyErr := modelChoiceKeys(model, form, forms.Data{}, nil, true)
				if keyErr != nil {
					return collectionFormSetBound{}, keyErr
				}
				prototype, err = resolveRowChoices(ctx, actor, definition.choiceOrigin(0), definition.choiceSources, false, access.allowedChoices(), keys)
				if err != nil {
					return collectionFormSetBound{}, err
				}
				views[-1], err = choiceViews(ctx, actor, definition.choiceOrigin(0), definition.choiceSources, "", false, submitted == nil)
				if err != nil {
					return collectionFormSetBound{}, err
				}
			}
			if submitted == nil {
				choices := forms.NewRowModelChoices(selections, prototype)
				typed, bound, err = bindSnapshot(ctx, access, snapshot, nil, nil, &choices)
				if err != nil {
					return collectionFormSetBound{}, err
				}
			} else if access.Add {
				prototypeRows, err := rows.WithRowModelChoices(forms.NewRowModelChoices(nil, prototype))
				if err != nil {
					return collectionFormSetBound{}, err
				}
				empty, err := prototypeRows.EmptyForm()
				if err != nil {
					return collectionFormSetBound{}, err
				}
				bound.empty = &empty
			}
			bound.choiceViews = views
		}
		if submitted != nil && config.Validate != nil && bound.set.NonFormErrors().Empty() {
			if err := config.Validate(ctx, actor, typed); err != nil {
				return bound.withRejection(ctx, err)
			}
			if err := ctx.Err(); err != nil {
				return collectionFormSetBound{}, err
			}
		}
		bound.run = func(ctx context.Context) (CollectionSetResult, error) {
			if err := validateCollectionSetAccess(ctx, actor, access, definition); err != nil {
				return CollectionSetResult{}, err
			}
			if !bound.set.Valid() || !access.writable() {
				return CollectionSetResult{}, &ConfigError{Path: "collection_formset.submission", Code: "not_bound_valid"}
			}
			submission := CollectionSetSubmission{declaration: definition.token, owner: &collectionSetToken{}, actor: actor.ID(), access: access, data: bound.set.Submitted(), set: bound.set}
			result, err := config.Run(ctx, actor, submission)
			if err != nil {
				return CollectionSetResult{}, err
			}
			if result.owner != submission.owner || result.declaration != definition.token || result.current == nil {
				return CollectionSetResult{}, reconciliationError("collection formset "+config.Name, &ConfigError{Path: "collection_formset.result", Code: "binding_mismatch"})
			}
			return result, nil
		}
		return bound, nil
	}
	definition.searchOrigin = func(ctx context.Context, actor auth.Principal, access CollectionSetAccess, key int64) (ChoiceOrigin, error) {
		if err := validateCollectionSetAccess(ctx, actor, access, definition); err != nil {
			return ChoiceOrigin{}, err
		}
		if key == 0 {
			if !access.Add {
				return ChoiceOrigin{}, NewOperationError(OperationDenied, nil)
			}
			return definition.choiceOrigin(0), nil
		}
		if key < 0 || !access.Change || policy.ReadOnlyInitial {
			return ChoiceOrigin{}, NewOperationError(OperationDenied, nil)
		}
		snapshot, err := config.Load(ctx, actor, access)
		if contextErr := ctx.Err(); contextErr != nil {
			err = errors.Join(err, contextErr)
		}
		if err != nil {
			return ChoiceOrigin{}, err
		}
		_, bound, err := bindSnapshot(ctx, access, snapshot, nil, nil, nil)
		if err != nil {
			return ChoiceOrigin{}, err
		}
		for _, identity := range bound.identities {
			if current, present := identity.AsInteger(); present && current == key {
				return definition.choiceOrigin(key), nil
			}
		}
		return ChoiceOrigin{}, ErrObjectNotFound
	}
	return ModelCollectionFormSet[M]{view: definition, bindCurrent: func(ctx context.Context, access CollectionSetAccess, data forms.Data, current CollectionSetCurrent[M]) (formmodel.InstanceSet[M], error) {
		resolve := func(ctx context.Context, input formmodel.SetPreflight[M]) (forms.RowModelChoices, error) {
			if current.ResolveChoices != nil {
				return current.ResolveChoices(ctx, input)
			}
			if (access.Add || access.Change) && len(definition.choiceSources) != 0 {
				return forms.RowModelChoices{}, &ConfigError{Path: "collection_formset.current.choices", Code: "missing"}
			}
			return forms.RowModelChoices{}, nil
		}
		typed, _, err := bindSnapshot(ctx, access, current.Rows, &data, resolve, nil)
		return typed, err
	}}, nil
}

func collectionSetRevision[M any](manager orm.Manager[M], definition CollectionFormSet, current M, present bool, data forms.Data, index int, submitted bool) (int64, error) {
	if definition.revision == "" {
		return 0, nil
	}
	var revision int64
	if present {
		values, err := manager.ModelValues(current)
		if err != nil {
			return 0, err
		}
		var valid bool
		revision, valid = values[definition.revision].Integer()
		if !valid || revision < 1 {
			return 0, &ConfigError{Path: "collection_formset.revision", Code: "invalid_result"}
		}
	}
	if submitted {
		raw, exists := data.Get(definition.config.Prefix + "-" + strconv.Itoa(index) + "-expected_revision")
		if revision != 0 && (!exists || len(raw) != 1 || raw[0] != strconv.FormatInt(revision, 10)) || revision == 0 && exists && (len(raw) != 1 || raw[0] != "") {
			return 0, NewOperationError(OperationConflict, nil)
		}
	}
	return revision, nil
}

func collectionSetInputRules(definition CollectionFormSet) (inputRules, map[string]bool) {
	rules, files := inputRules{"csrfmiddlewaretoken": MaximumInputValues}, make(map[string]bool)
	for _, name := range []string{"TOTAL_FORMS", "INITIAL_FORMS", "MIN_NUM_FORMS", "MAX_NUM_FORMS"} {
		rules[definition.config.Prefix+"-"+name] = MaximumInputValues
	}
	for index := 0; index < definition.config.AbsoluteMax; index++ {
		prefix := definition.config.Prefix + "-" + strconv.Itoa(index) + "-"
		rules[prefix+definition.primary] = -MaximumInputValues
		if definition.revision != "" {
			rules[prefix+"expected_revision"] = -MaximumInputValues
		}
		if definition.config.CanOrder {
			rules[prefix+"ORDER"] = -MaximumInputValues
		}
		if definition.config.CanDelete {
			rules[prefix+"DELETE"] = -MaximumInputValues
		}
		for _, field := range definition.fields {
			rules[prefix+field.Name()] = -MaximumInputValues
			if field.AcceptsUpload() {
				files[prefix+field.Name()] = true
			}
			if field.IsFile() && field.Widget() == forms.ClearableFileInput {
				rules[prefix+field.Name()+"-clear"] = -MaximumInputValues
			}
		}
	}
	return rules, files
}

func (definition CollectionFormSet) choiceOrigin(key int64) ChoiceOrigin {
	return ChoiceOrigin{source: definition.identity, operation: ChoiceCollectionSet, objectID: key, rowID: key, name: definition.name, valid: true}
}

func prepareCollectionFormSets(input []CollectionFormSet, model ir.Model, identity ir.ModelIdentity) ([]CollectionFormSet, error) {
	if len(input) > MaximumActions {
		return nil, &ConfigError{Path: "model.collection_formsets", Code: "limit_exceeded"}
	}
	seen := make(map[string]bool, len(input))
	for _, definition := range input {
		if definition.token == nil || definition.bind == nil || !definition.model.Equal(model) || definition.identity != identity || seen[definition.name] {
			return nil, &ConfigError{Path: "model.collection_formsets", Code: "invalid_or_duplicate"}
		}
		seen[definition.name] = true
	}
	return slices.Clone(input), nil
}

func collectionFormSetDescriptors(input []CollectionFormSet) []CollectionFormSetDescriptor {
	result := make([]CollectionFormSetDescriptor, len(input))
	for index, definition := range input {
		result[index] = CollectionFormSetDescriptor{Name: definition.name, Label: definition.label, RevisionField: definition.revision, Source: definition.identity, Permissions: definition.permissions, AdditionalPermissions: slices.Clone(definition.additional), FormFields: slices.Clone(definition.fields), Config: definition.config}
	}
	return result
}

// RejectCollectionFormSet reports confirmed input diagnostics after no write or
// confirmed rollback. Whole-set errors use NonField; authorization/cohort and
// protected deletion failures must not be suppressible by a DELETE checkbox.
// Wrapped or joined errors never qualify for redisplay.
func RejectCollectionFormSet(rows map[int]validation.Errors, whole validation.Errors, cause error) error {
	owned := make(map[int]validation.Errors, len(rows))
	for index, failures := range rows {
		if index < 0 || failures.Empty() {
			return &ConfigError{Path: "collection_formset.rejection", Code: "invalid"}
		}
		owned[index] = failures
	}
	if len(owned) == 0 && whole.Empty() {
		return cause
	}
	return &collectionFormSetRejection{rows: owned, whole: whole, cause: cause}
}

type collectionFormSetRejection struct {
	rows  map[int]validation.Errors
	whole validation.Errors
	cause error
}

func (*collectionFormSetRejection) Error() string {
	return "admin: collection formset validation rejected"
}
func (failure *collectionFormSetRejection) Unwrap() error { return failure.cause }
func (collectionFormSetRejection) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.collectionFormSetRejection{redacted}")
}
func (CollectionFormSet) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "admin.CollectionFormSet{redacted}")
}

func (bound collectionFormSetBound) withRejection(ctx context.Context, failure error) (collectionFormSetBound, error) {
	if err := ctx.Err(); err != nil {
		return collectionFormSetBound{}, errors.Join(failure, err)
	}
	rejection, valid := failure.(*collectionFormSetRejection)
	if !valid || rejection == nil {
		return collectionFormSetBound{}, failure
	}
	var err error
	if !rejection.whole.Empty() {
		bound.set, err = bound.set.WithErrors(rejection.whole)
		if err != nil {
			return collectionFormSetBound{}, err
		}
	}
	indices := make([]int, 0, len(rejection.rows))
	for index := range rejection.rows {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	for _, index := range indices {
		bound.set, err = bound.set.WithFormErrors(index, rejection.rows[index])
		if err != nil {
			return collectionFormSetBound{}, err
		}
	}
	if bound.set.Valid() {
		return collectionFormSetBound{}, &ConfigError{Path: "collection_formset.rejection", Code: "suppressed_diagnostics"}
	}
	bound.run = nil
	return bound, nil
}
