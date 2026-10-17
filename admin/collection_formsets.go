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
	model       ir.Model
	name, label string
	permissions []auth.Permission
	fields      []forms.Field
	config      forms.SetConfig
	rules       inputRules
	bind        func(context.Context, auth.Principal, *forms.Data) (collectionFormSetBound, error)
}

type collectionFormSetBound struct {
	set   forms.Set
	empty forms.SetForm
	run   func(context.Context) ([]int64, error)
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
	formFor, choices, err := prepareRelatedChoices(form, config.Form.RelatedChoices)
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
	definition := CollectionFormSet{model: model, name: config.Name, label: config.Label, permissions: permissions, fields: fields, config: policy, rules: rules}
	definition.bind = func(ctx context.Context, actor auth.Principal, submitted *forms.Data) (collectionFormSetBound, error) {
		for _, permission := range permissions {
			if err := validatePrincipalPermission(ctx, actor, permission); err != nil {
				return collectionFormSetBound{}, err
			}
		}
		current, err := formFor(ctx, actor)
		if err != nil {
			return collectionFormSetBound{}, err
		}
		spec, err := forms.NewSetSpec(current, policy, validators...)
		if err != nil {
			return collectionFormSetBound{}, err
		}
		var typed formmodel.InstanceSet[M]
		if submitted == nil {
			typed, err = formmodel.UnboundSet(config.Manager, spec, nil)
		} else {
			typed, err = formmodel.BindSet(ctx, config.Manager, spec, *submitted, nil, postClean)
		}
		if err != nil {
			return collectionFormSetBound{}, err
		}
		empty, err := spec.EmptyForm()
		if err != nil {
			return collectionFormSetBound{}, err
		}
		bound := collectionFormSetBound{set: typed.FormSet(), empty: empty}
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

func prepareCollectionFormSets(input []CollectionFormSet, model ir.Model) ([]CollectionFormSet, error) {
	if len(input) > MaximumActions {
		return nil, &ConfigError{Path: "model.collection_formsets", Code: "limit_exceeded"}
	}
	seen := make(map[string]bool, len(input))
	for _, definition := range input {
		if definition.bind == nil || !definition.model.Equal(model) || seen[definition.name] {
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
