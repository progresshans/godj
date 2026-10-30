package model

import (
	"context"
	"slices"

	"github.com/progresshans/godj/auth"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uploads"
)

// FormDefinition retains the selected model, normalization and explicit file
// inspection policy. It performs no I/O and grants no current write authority.
func (creator *Creator[M]) FormDefinition() (formmodel.Definition, error) {
	if creator == nil || creator.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	return creator.state.binding.formDefinition(creator.state.fields, profileFileOverrides(creator.state.files))
}

func (creator *Creator[M]) FileFields() []string {
	if creator == nil || creator.state == nil {
		return nil
	}
	return profileFileNames(creator.state.files)
}

// CreateForm keeps uploads within the caller's current form/request lifetime.
// Current admission precedes publication; actual stored names are validated
// before hashing and again in the final native transaction. Every attempted
// publication is returned even if validation, hashing, commit or cleanup fails.
// Storage and the database are separate outcomes; neither is retried or undone.
func (creator *Creator[M]) CreateForm(ctx context.Context, actor auth.Principal, form formmodel.BoundForm, password string) (Creation, []formmodel.FilePublication, error) {
	return creator.createForm(ctx, actor, form, password, false)
}

// CreateUnusableForm uses the same publication and transaction contract without
// password hashing or strength validation. It creates an unusable credential.
func (creator *Creator[M]) CreateUnusableForm(ctx context.Context, actor auth.Principal, form formmodel.BoundForm) (Creation, []formmodel.FilePublication, error) {
	return creator.createForm(ctx, actor, form, "", true)
}

func (creator *Creator[M]) createForm(ctx context.Context, actor auth.Principal, form formmodel.BoundForm, password string, unusable bool) (Creation, []formmodel.FilePublication, error) {
	prepared, publications, err := creator.prepare(ctx, actor, orm.ModelInput[M]{}, password, unusable, &form)
	if err != nil {
		return Creation{}, publications, err
	}
	result, err := creator.Commit(ctx, actor, prepared)
	return result, publications, err
}

func (definition FirstAccountDefinition[M]) FormDefinition() (formmodel.Definition, error) {
	if definition.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	state := definition.state.creation
	return state.binding.formDefinition(state.fields, profileFileOverrides(state.files))
}

func (owner *FirstAccount[M]) FormDefinition() (formmodel.Definition, error) {
	if owner == nil || owner.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	state := owner.state.creation
	return state.binding.formDefinition(state.fields, profileFileOverrides(state.files))
}

// CreateForm provisions a fresh account/domain using an explicit operation ID
// retained by the caller before any publication. It can be reconciled through
// domain.Lookup after an unknown database outcome. Uploads never escape this
// invocation, and every attempted publication survives any later error.
func (owner *FirstAccount[M]) CreateForm(ctx context.Context, operationID string, form formmodel.BoundForm, password string) (FirstAccountCreation, []formmodel.FilePublication, error) {
	if !validFirstAccountDigest(operationID) {
		return FirstAccountCreation{}, nil, managementError(CodeInvalidInput, nil)
	}
	prepared, publications, err := owner.prepare(ctx, operationID, orm.ModelInput[M]{}, password, &form)
	if err != nil {
		return FirstAccountCreation{}, publications, err
	}
	result, err := owner.Commit(ctx, prepared)
	return result, publications, err
}

func (state *creationDefinition[M]) checkFileForm(bound formmodel.BoundForm) error {
	if !bound.Model().Equal(state.metadata) || !bound.Form().Bound() || bound.Form().ReadOnly() || !bound.Form().Valid() {
		return managementError(CodeInvalidInput, nil)
	}
	input, err := bound.Input()
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	for _, field := range state.metadata.Fields {
		if field.PrimaryKey {
			key, present := bound.Candidate().Get(field.Name)
			if !present || !key.IsNull() {
				return managementError(CodeInvalidInput, nil)
			}
		}
		if _, present := input.Get(field.Name); present && !state.allowed[field.Name] && !state.allowed[state.dimensions[field.Name]] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, field := range state.metadata.ManyToMany {
		if _, present := input.Get(field.Name); present && !state.allowed[field.Name] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, name := range bound.FieldNames() {
		if _, found := namedField(state.metadata, name); found && (!state.allowed[name] || state.dimensions[name] != "") {
			return managementError(CodeInvalidInput, nil)
		}
		for _, collection := range state.metadata.ManyToMany {
			if name == collection.Name && !state.allowed[name] {
				return managementError(CodeInvalidInput, nil)
			}
		}
	}
	for _, policy := range state.files {
		field := policy.state.field
		value, present := input.Get(field.Name)
		if !present || value.IsNull() {
			continue
		}
		file, isFile := value.AsFile()
		_, pending := file.Upload()
		if pending {
			if field.Choices != nil {
				return managementError(CodeInvalidInput, nil)
			}
			if _, inspected := file.Image(); field.Kind == ir.FieldImage && !inspected {
				return managementError(CodeInvalidInput, nil)
			}
			continue
		}
		name, textual := value.AsString()
		if isFile {
			name, textual = file.Name(), true
		}
		if !textual {
			return managementError(CodeInvalidInput, nil)
		}
		if field.Choices != nil {
			if name != "" && !slices.ContainsFunc(field.Choices, func(choice ir.Choice) bool { return choice.Value.String == name }) {
				return managementError(CodeInvalidInput, nil)
			}
			if _, inspected := file.Image(); name != "" && field.Kind == ir.FieldImage && !inspected {
				return managementError(CodeInvalidInput, nil)
			}
		} else if name != "" && (field.Default == nil || name != field.Default.String) {
			// A fabricated initial reference is not an upload or an IR default.
			return managementError(CodeInvalidInput, nil)
		}
	}
	return nil
}

func (state *creationState[M]) publishFiles(ctx context.Context, candidate *creationCandidate[M]) ([]formmodel.FilePublication, error) {
	if candidate == nil || candidate.form == nil || len(candidate.pendingFiles) == 0 {
		return nil, managementError(CodeInvalidInput, nil)
	}
	objects := state.binding.state.objects
	prepared, err := formmodel.PrepareInstance(objects, *candidate.form, &candidate.row)
	if err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	if !slices.Equal(prepared.PendingFiles(), candidate.pendingFiles) {
		return nil, managementError(CodeInvalidInput, nil)
	}
	savers := profileFileSavers(state.files, candidate.pendingFiles)
	for i := range savers {
		name := savers[i].Name
		savers[i].Name = func(_ M, upload uploads.File) (string, error) {
			// Native normalization and server-owned values have already been
			// prepared. The policy itself detaches and redacts this candidate.
			return name(candidate.row, upload)
		}
	}
	resolved, publications, err := prepared.SaveFiles(ctx, savers...)
	if err != nil {
		return publications, err
	}
	input, err := resolved.ModelInput()
	if err != nil {
		return publications, managementError(CodeInvalidInput, err)
	}
	actual := make(map[string]query.Value)
	for name, value := range input.Values() {
		field, found := namedField(state.metadata, name)
		if found && (field.Kind.IsFile() || state.dimensions[name] != "") {
			actual[name] = value
		}
	}
	// Preserve normalized scalar input and server identity; rebinding a generic
	// form must not overwrite them with an earlier unnormalized form candidate.
	candidate.row, err = objects.ApplyValues(candidate.row, actual)
	if err != nil {
		return publications, managementError(CodeInvalidInput, err)
	}
	candidate.pendingFiles, candidate.form = nil, nil
	return publications, nil
}
