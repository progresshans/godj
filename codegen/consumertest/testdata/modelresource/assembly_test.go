package consumer

import (
	"context"
	"errors"
	"testing"

	"example.com/godj-project-bundle/directory"
	"example.com/godj-project-bundle/inventory"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/api"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

// These are application policy, intentionally separate from generated scalar
// meaning. The same edit permission governs JSON admission and Admin changes.
const oldPermission auth.Permission = "inventory.change_asset"

func editPermission() auth.Permission {
	if evolved {
		return "inventory.manage_asset"
	}
	return oldPermission
}

func inputFields() []string {
	fields := []string{"code", "enabled", "note"}
	if evolved {
		fields = append(fields, "priority")
	}
	return fields
}

func responseFields() []string {
	if evolved {
		return []string{"id", "code", "enabled", "priority"}
	}
	return []string{"id", "code", "enabled", "note"}
}

type assembly struct {
	backend   resourceBackend
	workspace directory.Workspace
	resource  crud.Resource[inventory.Asset]
	body      bodyinput.Body[orm.ModelInput[inventory.Asset]]
	spec      serializers.Spec
	out       output.Output[inventory.Asset]
	form      forms.Spec
}

func newAssembly(t *testing.T, backend resourceBackend, name string) assembly {
	t.Helper()
	workspace := directory.Workspace{Name: name, Writable: true}
	check(t, directory.WorkspaceObjects.Save(t.Context(), backend, &workspace))
	resource, err := crud.New(inventory.AssetObjects, crud.Config{Fields: inputFields()})
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	resource = resource.Filter(relations.InventoryAsset.Workspace.ID.Exact(workspace.ID))
	model := (inventory.AssetDescriptor{}).Metadata()
	fields := []serializers.ModelField{{Name: "id"}}
	for _, name := range resource.Fields() {
		fields = append(fields, serializers.ModelField{Name: name, Optional: name == "note", AllowEmpty: name == "note"})
	}
	spec, err := serializers.FromModel(model, fields...)
	check(t, err)
	body, err := bodyinput.NewResource(resource, spec, api.ParserConfig{})
	check(t, err)
	var projection []serializers.ModelField
	for _, name := range responseFields() {
		projection = append(projection, serializers.ModelField{Name: name, ReadOnly: true, AllowEmpty: name == "note"})
	}
	response, err := serializers.FromModel(model, projection...)
	check(t, err)
	encoder, err := serializers.NewModelEncoder(response, model, (inventory.AssetDescriptor{}).WriteFieldValue)
	check(t, err)
	out, err := output.New(output.Named("Asset", output.Model(encoder)), serializers.Limits{})
	check(t, err)
	form, err := formmodel.NewSpecForFields(model, resource.Fields())
	check(t, err)
	return assembly{backend: backend, workspace: workspace, resource: resource, body: body, spec: spec, out: out, form: form}
}

var deniedWorkspace = errors.New("workspace no longer writable")

func (a assembly) saveOptions(create bool) crud.SaveOptions[inventory.Asset] {
	options := crud.SaveOptions[inventory.Asset]{Before: func(ctx context.Context, session db.Session, current, candidate *inventory.Asset) error {
		workspace, found, err := directory.WorkspaceObjects.Using(session).Filter(directory.WorkspaceFields.ID.Exact(a.workspace.ID)).OrderBy(directory.WorkspaceFields.ID.Asc()).First(ctx)
		if err != nil {
			return err
		}
		if !found || !workspace.Writable || candidate.WorkspaceID != workspace.ID || current != nil && current.WorkspaceID != workspace.ID {
			return deniedWorkspace
		}
		return nil
	}}
	if create {
		options.ServerValues = map[string]query.Value{"workspace": query.Integer(a.workspace.ID)}
	}
	return options
}

func (a assembly) create(ctx context.Context, input orm.ModelInput[inventory.Asset]) (inventory.Asset, error) {
	return db.AtomicResult(ctx, a.backend, func(work context.Context, session db.Session) (inventory.Asset, error) {
		mutation, err := a.resource.CreateInSession(work, session, input, a.saveOptions(true))
		return mutation.Value, err
	})
}

func (a assembly) update(ctx context.Context, id int64, input orm.ModelInput[inventory.Asset]) (crud.Mutation[inventory.Asset], error) {
	return db.AtomicResult(ctx, a.backend, func(work context.Context, session db.Session) (crud.Mutation[inventory.Asset], error) {
		return a.resource.UpdateInSession(work, session, id, input, a.saveOptions(false))
	})
}

func (a assembly) bindJSON(t *testing.T, raw string, mode serializers.Mode) orm.ModelInput[inventory.Asset] {
	t.Helper()
	object, err := a.spec.DecodeObject([]byte(raw), serializers.Limits{})
	check(t, err)
	input, diagnostics, err := a.body.Bind(t.Context(), object, mode)
	check(t, err)
	if !diagnostics.Empty() {
		t.Fatal("JSON validation failed", diagnostics)
	}
	return input
}

func (a assembly) bindForm(t *testing.T, values map[string][]string, current *inventory.Asset) orm.ModelInput[inventory.Asset] {
	t.Helper()
	bound, err := formmodel.BindInstance(t.Context(), inventory.AssetObjects, a.form, forms.NewData(values), current, formmodel.PostClean{})
	check(t, err)
	if !bound.BoundForm().Form().Valid() {
		t.Fatal("Form validation failed", bound.BoundForm().Form().Errors())
	}
	input, err := formmodel.PrepareInput(inventory.AssetObjects, bound.BoundForm())
	check(t, err)
	return input
}
