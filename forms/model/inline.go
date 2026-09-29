package model

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// InlineSpec binds child form policy to one canonical project ForeignKey.
// Binding consumes already-authorized parent/child snapshots and performs no
// I/O. The caller owns queries, current authorization and the write transaction.
type InlineSpec[P, C any] struct {
	parent     orm.Manager[P]
	child      orm.Manager[C]
	row        forms.SetSpec
	foreignKey string
	parentKey  string
	valid      bool
}

// NewInlineSpec resolves the child's declaring model and target from the
// project, and checks both typed managers against that canonical metadata.
// ForeignKey input is replaced by a hidden server-parent field when bound.
// Prefix/count policy remains explicit; a unique FK caps display MaxForms at 1.
func NewInlineSpec[P, C any](project orm.ProjectBinding, parent orm.Manager[P], child orm.Manager[C], foreignKey string, row forms.SetSpec) (InlineSpec[P, C], error) {
	parentModel, err := parent.Metadata()
	if err != nil {
		return InlineSpec[P, C]{}, err
	}
	childModel, err := child.Metadata()
	if err != nil {
		return InlineSpec[P, C]{}, err
	}
	matches := 0
	for _, relation := range project.ForwardRelations() {
		if relation.Field != foreignKey {
			continue
		}
		source, found := project.Model(relation.Source)
		if !found || !source.Equal(childModel) {
			continue
		}
		target, found := project.Model(relation.Target)
		if !found || !target.Equal(parentModel) {
			return InlineSpec[P, C]{}, &Error{Path: "inline.parent", Code: "model_mismatch"}
		}
		matches++
	}
	if matches != 1 {
		return InlineSpec[P, C]{}, &Error{Path: "inline.relation", Code: "missing_or_ambiguous"}
	}
	var field ir.Field
	for _, candidate := range childModel.Fields {
		if candidate.Name == foreignKey {
			field = candidate
		}
	}
	if field.Kind != ir.FieldForeignKey || field.PrimaryKey || field.Relation == nil || !field.Relation.Cardinality.SingleValued() {
		return InlineSpec[P, C]{}, &Error{Path: "inline.relation", Code: "unsupported"}
	}
	key := ""
	for _, candidate := range parentModel.Fields {
		if candidate.PrimaryKey {
			if key != "" || candidate.Kind != ir.FieldAuto && candidate.Kind != ir.FieldInteger {
				return InlineSpec[P, C]{}, &Error{Path: "inline.parent", Code: "unsupported_primary_key"}
			}
			key = candidate.Name
		}
	}
	if key == "" {
		return InlineSpec[P, C]{}, &Error{Path: "inline.parent", Code: "missing_primary_key"}
	}
	if field.Unique {
		config := row.Config()
		config.MaxForms = min(config.MaxForms, 1)
		row, err = row.WithConfig(config)
		if err != nil {
			return InlineSpec[P, C]{}, err
		}
	}
	parentField, err := forms.InlineParentField(foreignKey, forms.Null())
	if err != nil {
		return InlineSpec[P, C]{}, err
	}
	row, err = row.WithFormField(parentField)
	if err != nil {
		return InlineSpec[P, C]{}, err
	}
	if _, _, _, err := setSnapshots(child, row, nil, nil); err != nil {
		return InlineSpec[P, C]{}, err
	}
	return InlineSpec[P, C]{parent: parent, child: child, row: row, foreignKey: foreignKey, parentKey: key, valid: true}, nil
}

// InlineSet owns the parent snapshot and the matching child identity cohort.
// It can validate children before a new parent's key is assigned, but no child
// can be prepared with a missing parent, even when the FK column is nullable.
type InlineSet[P, C any] struct {
	spec   InlineSpec[P, C]
	parent P
	key    forms.Value
	set    InstanceSet[C]
}

func (spec InlineSpec[P, C]) Unbound(parent P, current []C, related ...func(C, ir.ManyToManyField) ([]int64, bool)) (InlineSet[P, C], error) {
	result, row, err := spec.start(parent, current)
	if err != nil {
		return InlineSet[P, C]{}, err
	}
	result.set, err = UnboundSet(spec.child, row, current, related...)
	return result, err
}

func (spec InlineSpec[P, C]) Bind(data forms.Data, parent P, current []C, postClean PostClean, related ...func(C, ir.ManyToManyField) ([]int64, bool)) (InlineSet[P, C], error) {
	result, row, err := spec.start(parent, current)
	if err != nil {
		return InlineSet[P, C]{}, err
	}
	if slices.Contains(postClean.Fields, spec.foreignKey) {
		return InlineSet[P, C]{}, &Error{Path: "inline.post_clean", Code: "parent_is_server_owned"}
	}
	result.set, err = bindSet(spec.child, row, data, current, postClean, related, spec.foreignKey)
	if err != nil {
		return InlineSet[P, C]{}, err
	}
	// Parent mismatch is admission, not deletable row data. Check even rows
	// which skipped cleaning because they are unchanged optional extras.
	expected := ""
	if key, present := result.key.AsInteger(); present {
		expected = strconv.FormatInt(key, 10)
	}
	var failures []validation.Violation
	for _, item := range result.set.FormSet().Forms() {
		name := item.Prefix() + "-" + spec.foreignKey
		if raw, present := data.Get(name); present && (len(raw) != 1 || raw[0] != "" && (result.key.IsNull() || raw[0] != expected)) {
			failures = append(failures, validation.New(validation.NonField, "invalid_parent", validation.NewParam("index", strconv.Itoa(item.Index()))))
		}
	}
	if len(failures) != 0 {
		result.set, err = result.set.WithErrors(validation.NewErrors(failures...))
	}
	return result, err
}

func (spec InlineSpec[P, C]) start(parent P, current []C) (InlineSet[P, C], forms.SetSpec, error) {
	if !spec.valid {
		return InlineSet[P, C]{}, forms.SetSpec{}, &Error{Path: "inline", Code: "uninitialized"}
	}
	if len(current) > spec.row.Config().AbsoluteMax {
		return InlineSet[P, C]{}, forms.SetSpec{}, &Error{Path: "inline.current", Code: "absolute_max"}
	}
	snapshot, err := spec.parent.ApplyValues(parent, nil)
	if err != nil {
		return InlineSet[P, C]{}, forms.SetSpec{}, err
	}
	values, err := spec.parent.ModelValues(snapshot)
	if err != nil {
		return InlineSet[P, C]{}, forms.SetSpec{}, err
	}
	key, present := formValue(values[spec.parentKey])
	if !present || key.IsNull() && len(current) != 0 {
		return InlineSet[P, C]{}, forms.SetSpec{}, &Error{Path: "inline.current", Code: "unsaved_parent"}
	}
	for _, child := range current {
		values, err := spec.child.ModelValues(child)
		if err != nil {
			return InlineSet[P, C]{}, forms.SetSpec{}, err
		}
		foreign, present := formValue(values[spec.foreignKey])
		if !present || !foreign.Equal(key) {
			return InlineSet[P, C]{}, forms.SetSpec{}, &Error{Path: "inline.current", Code: "parent_mismatch"}
		}
	}
	field, err := forms.InlineParentField(spec.foreignKey, key)
	if err != nil {
		return InlineSet[P, C]{}, forms.SetSpec{}, err
	}
	row, err := spec.row.WithFormField(field)
	return InlineSet[P, C]{spec: spec, parent: snapshot, key: key}, row, err
}

func (set InlineSet[P, C]) FormSet() forms.Set { return set.set.FormSet() }
func (set InlineSet[P, C]) Valid() bool        { return set.set.Valid() }
func (set InlineSet[P, C]) Instance(index int) (InstanceForm[C], bool) {
	return set.set.Instance(index)
}
func (set InlineSet[P, C]) Current(index int) (C, bool, error) {
	return set.set.Current(index)
}
func (set InlineSet[P, C]) IdentityName(index int) (string, error) {
	return set.set.IdentityName(index)
}
func (set InlineSet[P, C]) Identity(index int) (forms.Value, bool) {
	return set.set.Identity(index)
}
func (set InlineSet[P, C]) ParentIdentity() forms.Value { return set.key }
func (set InlineSet[P, C]) WithErrors(failures validation.Errors) (InlineSet[P, C], error) {
	var err error
	set.set, err = set.set.WithErrors(failures)
	return set, err
}
func (set InlineSet[P, C]) WithRowErrors(index int, failures validation.Errors, rejectedFields ...string) (InlineSet[P, C], error) {
	var err error
	set.set, err = set.set.WithRowErrors(index, failures, rejectedFields...)
	return set, err
}

func (set InlineSet[P, C]) Prepare() (PreparedSet[C], error) {
	return set.PrepareWithParent(set.parent)
}

// PrepareWithParent supplies the key assigned by the caller's parent save.
// An existing parent's identity cannot change. This performs no I/O or clean
// callbacks and does not prove a commit; save parent and all children in the
// same authorized transaction. Its failure must leave that transaction.
func (set InlineSet[P, C]) PrepareWithParent(parent P) (PreparedSet[C], error) {
	if !set.spec.valid || !set.Valid() {
		return PreparedSet[C]{}, &Error{Path: "inline", Code: "not_bound_valid"}
	}
	values, err := set.spec.parent.ModelValues(parent)
	if err != nil {
		return PreparedSet[C]{}, err
	}
	key, present := formValue(values[set.spec.parentKey])
	if !present || key.IsNull() {
		return PreparedSet[C]{}, &Error{Path: "inline.parent", Code: "unsaved_parent"}
	}
	if !set.key.IsNull() && !set.key.Equal(key) {
		return PreparedSet[C]{}, &Error{Path: "inline.parent", Code: "identity_changed"}
	}
	prepared := set.set
	prepared.instances = make(map[int]InstanceForm[C], len(set.set.instances))
	for index, instance := range set.set.instances {
		candidate := map[string]forms.Value{}
		for _, value := range instance.bound.candidate.All() {
			candidate[value.Name()] = value.Value()
		}
		candidate[set.spec.foreignKey] = key
		instance.bound.candidate = forms.NewValues(candidate)
		prepared.instances[index] = instance
	}
	return prepared.Prepare()
}

func (InlineSpec[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.InlineSpec{redacted}")
}
func (InlineSet[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.InlineSet{redacted}")
}
