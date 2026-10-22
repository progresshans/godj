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

// CollectionFormSetConfig declares creation of several instances without an
// existing selected object. Run owns fresh scope/uniqueness checks, one write
// transaction and any required audit. Return ordered IDs only after commit.
// The typed formset carries the evaluated candidates; do not bind/clean again.
// Callbacks must keep request data local and be safe for concurrent use.
type CollectionFormSetConfig[M any] struct {
	Name, Label           string
	Manager               orm.Manager[M]
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Form                  FormConfig
	Set                   forms.SetConfig
	Validators            []forms.SetValidator
	Run                   func(context.Context, auth.Principal, formmodel.InstanceSet[M]) ([]int64, error)
}

// CollectionFormSet is a sealed view/binding adapter for a bounded create-only
// model formset. It owns no transaction and does not infer a bulk save policy.
// Ordering/deletion controls and uploaded files are currently unsupported;
// configuration fails explicitly instead of ignoring those policies.
type CollectionFormSet struct {
	model         ir.Model
	identity      ir.ModelIdentity
	choiceSources []RelatedChoices
	name, label   string
	permissions   []auth.Permission
	fields        []forms.Field
	config        forms.SetConfig
	rules         inputRules
	bind          func(context.Context, auth.Principal, *forms.Data) (collectionFormSetBound, error)
}

type collectionFormSetBound struct {
	choiceViews map[int]map[string]choiceFieldView
	set         forms.Set
	empty       forms.SetForm
	run         func(context.Context) ([]int64, error)
}

type CollectionFormSetDescriptor struct {
	Name, Label string
	Permissions []auth.Permission
	FormFields  []forms.Field
	Config      forms.SetConfig
}

func NewCollectionFormSet[M any](config CollectionFormSetConfig[M]) (CollectionFormSet, error) {
	if !validSlug(config.Name) || !validBoundedText(config.Label, MaximumDisplayBytes) || config.Run == nil {
		return CollectionFormSet{}, &ConfigError{Path: "collection_formset", Code: "invalid"}
	}
	policy := config.Set
	if policy.MinForms < 1 || !policy.ValidateMin || !policy.ValidateMax || policy.AbsoluteMax > MaximumInlineRows || policy.AbsoluteMax < 1 || policy.CanOrder || policy.CanDelete || policy.ReadOnlyInitial {
		return CollectionFormSet{}, &ConfigError{Path: "collection_formset.set", Code: "unsupported_policy"}
	}
	model, err := config.Manager.Metadata()
	if err != nil {
		return CollectionFormSet{}, err
	}
	form, err := prepareModelForm(model, config.Form)
	if err != nil {
		return CollectionFormSet{}, err
	}
	if form.IsMultipart() || slices.ContainsFunc(form.Fields(), func(field forms.Field) bool { return field.IsFile() }) {
		return CollectionFormSet{}, &ConfigError{Path: "collection_formset.form", Code: "unsupported_file"}
	}
	validators := slices.Clone(config.Validators)
	rows, err := forms.NewSetSpec(form, policy, validators...)
	if err != nil {
		return CollectionFormSet{}, err
	}
	if _, err := formmodel.UnboundSet(config.Manager, rows, nil); err != nil {
		return CollectionFormSet{}, err
	}
	var identity ir.ModelIdentity
	if len(config.Form.RelatedChoices) > 0 && config.Form.RelatedChoices[0] != nil {
		identity = config.Form.RelatedChoices[0].choiceMetadata().Source
	}
	_, choices, err := prepareRelatedChoices(model, identity, form, config.Form.RelatedChoices, false)
	if err != nil {
		return CollectionFormSet{}, err
	}
	permissions, err := additionalPermissions(config.Permission, config.AdditionalPermissions)
	if err != nil {
		return CollectionFormSet{}, err
	}
	for _, permission := range choices {
		if !slices.Contains(permissions, permission) {
			permissions = append(permissions, permission)
		}
	}
	permissions, err = additionalPermissions(permissions[0], permissions[1:])
	if err != nil {
		return CollectionFormSet{}, err
	}
	fields := form.Fields()
	if policy.AbsoluteMax*len(fields)+5 > MaximumInputValues {
		return CollectionFormSet{}, &ConfigError{Path: "collection_formset.form", Code: "limit_exceeded"}
	}
	rules := inputRules{"csrfmiddlewaretoken": MaximumInputValues}
	for _, name := range []string{"TOTAL_FORMS", "INITIAL_FORMS", "MIN_NUM_FORMS", "MAX_NUM_FORMS"} {
		rules[policy.Prefix+"-"+name] = MaximumInputValues
	}
	for index := 0; index < policy.AbsoluteMax; index++ {
		for _, field := range fields {
			rules[policy.Prefix+"-"+strconv.Itoa(index)+"-"+field.Name()] = -MaximumInputValues
		}
	}
	postClean := config.Form.Definition.PostClean.Clone()
	definition := CollectionFormSet{model: model, identity: identity, choiceSources: slices.Clone(config.Form.RelatedChoices), name: config.Name, label: config.Label, permissions: permissions, fields: fields, config: policy, rules: rules}
	definition.bind = func(ctx context.Context, actor auth.Principal, submitted *forms.Data) (collectionFormSetBound, error) {
		for _, permission := range permissions {
			if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
				return collectionFormSetBound{}, err
			}
		}

		origin := ChoiceOrigin{source: identity, operation: ChoiceCollectionSet, name: config.Name, valid: true}
		spec := rows
		var typed formmodel.InstanceSet[M]
		var input formmodel.SetPreflight[M]
		var err error
		views := make(map[int]map[string]choiceFieldView)
		selections := make(map[int]map[string][]forms.Choice)
		admitted := submitted == nil
		if submitted == nil {
			typed, err = formmodel.UnboundSet(config.Manager, spec, nil)
		} else {
			input, err = formmodel.PreflightSet(ctx, config.Manager, spec, *submitted, nil, postClean)
			admitted = input.AdmissionValid()
		}
		if err != nil {
			return collectionFormSetBound{}, err
		}
		count := typed.FormSet().TotalForms()
		if submitted != nil {
			count = input.TotalForms()
		}
		var prototype map[string][]forms.Choice
		if admitted {
			initialKeys, keyErr := modelChoiceKeys(model, form, forms.Data{}, nil, true)
			if keyErr != nil {
				return collectionFormSetBound{}, keyErr
			}
			for index := 0; index < count; index++ {
				keys := initialKeys
				if submitted != nil {
					keys = func(name string) ([]int64, bool, error) { return input.ChoiceKeys(index, name) }
				}
				selections[index], err = resolveRowChoices(ctx, actor, origin, definition.choiceSources, false, nil, keys)
				if err != nil {
					return collectionFormSetBound{}, err
				}
				views[index], err = choiceViews(ctx, actor, origin, definition.choiceSources, "", false, submitted == nil)
				if err != nil {
					return collectionFormSetBound{}, err
				}
			}
			prototype, err = resolveRowChoices(ctx, actor, origin, definition.choiceSources, false, nil, initialKeys)
			if err != nil {
				return collectionFormSetBound{}, err
			}
			views[-1], err = choiceViews(ctx, actor, origin, definition.choiceSources, "", false, submitted == nil)
			if err != nil {
				return collectionFormSetBound{}, err
			}
		}
		choices := forms.NewRowModelChoices(selections, prototype)
		if submitted == nil {
			spec, err = spec.WithRowModelChoices(choices)
			if err != nil {
				return collectionFormSetBound{}, err
			}
			typed, err = formmodel.UnboundSet(config.Manager, spec, nil)
		} else {
			typed, err = input.Bind(ctx, choices)
		}
		if err != nil {
			return collectionFormSetBound{}, err
		}
		prototypeSpec, err := spec.WithRowModelChoices(forms.NewRowModelChoices(nil, prototype))
		if err != nil {
			return collectionFormSetBound{}, err
		}
		empty, err := prototypeSpec.EmptyForm()
		if err != nil {
			return collectionFormSetBound{}, err
		}
		bound := collectionFormSetBound{set: typed.FormSet(), empty: empty, choiceViews: views}
		bound.run = func(ctx context.Context) ([]int64, error) {
			for _, permission := range permissions {
				if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
					return nil, err
				}
			}
			if !typed.Valid() || typed.FormSet().InitialForms() != 0 {
				return nil, &ConfigError{Path: "collection_formset.submission", Code: "not_bound_valid"}
			}
			active, err := typed.FormSet().ActiveForms()
			if err != nil {
				return nil, err
			}

			// Re-resolve the original submitted/default keys without re-running clean
			// hooks or using any post-clean rewrite as a lookup request.
			for _, row := range active {
				index := row.Index()
				keys := func(name string) ([]int64, bool, error) { return input.ChoiceKeys(index, name) }
				selections, resolveErr := resolveRowChoices(ctx, actor, origin, definition.choiceSources, false, nil, keys)
				if resolveErr != nil {
					return nil, resolveErr
				}
				for _, source := range definition.choiceSources {
					requested, valid, keyErr := keys(source.choiceField())
					if keyErr != nil {
						return nil, keyErr
					}
					if !valid {
						return nil, &ConfigError{Path: "collection_formset.keys", Code: "invalid"}
					}
					for _, key := range requested {
						if !slices.ContainsFunc(selections[source.choiceField()], func(choice forms.Choice) bool {
							value, present := choice.Value.AsInteger()
							return present && value == key
						}) {
							return nil, RejectCollectionFormSet(map[int]validation.Errors{index: validation.NewErrors(validation.New(validation.Field(source.choiceField()), "invalid_choice"))}, validation.Errors{}, nil)
						}
					}
				}
			}
			identities, err := config.Run(ctx, actor, typed)
			if err != nil {
				return nil, err
			}
			identities = slices.Clone(identities)
			seen := make(map[int64]bool, len(identities))
			valid := len(identities) == len(active) && len(identities) >= policy.MinForms && len(identities) <= policy.MaxForms
			for _, identity := range identities {
				valid = valid && identity > 0 && !seen[identity]
				seen[identity] = true
			}
			if !valid {
				return nil, reconciliationError("collection formset "+config.Name, &ConfigError{Path: "collection_formset.result", Code: "invalid_identities"})
			}
			return identities, nil
		}
		return bound, nil
	}
	return definition, nil
}

func prepareCollectionFormSets(input []CollectionFormSet, model ir.Model, identity ir.ModelIdentity) ([]CollectionFormSet, error) {
	if len(input) > MaximumActions {
		return nil, &ConfigError{Path: "model.collection_formsets", Code: "limit_exceeded"}
	}
	seen := make(map[string]bool, len(input))
	for _, definition := range input {
		if definition.bind == nil || !definition.model.Equal(model) || len(definition.choiceSources) > 0 && definition.identity != identity || seen[definition.name] {
			return nil, &ConfigError{Path: "model.collection_formsets", Code: "invalid_or_duplicate"}
		}
		seen[definition.name] = true
	}
	return slices.Clone(input), nil
}

func collectionFormSetDescriptors(input []CollectionFormSet) []CollectionFormSetDescriptor {
	result := make([]CollectionFormSetDescriptor, len(input))
	for index, definition := range input {
		result[index] = CollectionFormSetDescriptor{Name: definition.name, Label: definition.label, Permissions: slices.Clone(definition.permissions), FormFields: slices.Clone(definition.fields), Config: definition.config}
	}
	return result
}

// RejectCollectionFormSet reports confirmed validation errors for a complete
// create request. Rows uses zero-based submitted indices. Whole-set errors
// must use NonField. Only this direct error qualifies for HTML redisplay;
// wrapped errors and uncertain rollback outcomes remain execution failures.
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
	bound.run = nil
	return bound, nil
}
