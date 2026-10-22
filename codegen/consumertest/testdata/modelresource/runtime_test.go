package consumer

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"example.com/godj-project-bundle/directory"
	"example.com/godj-project-bundle/inventory"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/api"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestModelResourceAssembly(t *testing.T) {
	withResourceBackends(t, func(t *testing.T, backend resourceBackend) {
		t.Run("form_json_defaults_and_fresh_update", func(t *testing.T) { verifyFormJSON(t, backend) })
		t.Run("source_exposure_and_target_rejection", func(t *testing.T) { verifySources(t, backend) })
		t.Run("scope_uniqueness_rollback_and_cross_app_delete", func(t *testing.T) { verifyTransactions(t, backend) })
		t.Run("http_permission_response_and_schema", func(t *testing.T) { verifyHTTP(t, backend) })
		t.Run("admin_form_permission_and_csrf", func(t *testing.T) { verifyAdmin(t, backend) })
		t.Run("same_change_manual_and_model_assembly", func(t *testing.T) { verifyComparison(t, backend) })
	})
}

func verifyFormJSON(t *testing.T, backend resourceBackend) {
	a := newAssembly(t, backend, "form-json")
	jsonInput := a.bindJSON(t, `{"code":"from-json"}`, serializers.ModeFull)
	created, err := a.create(t.Context(), jsonInput)
	check(t, err)
	if !created.Enabled || created.Note != nil || created.Stamp != "server" || created.WorkspaceID != a.workspace.ID {
		t.Fatal("IR defaults, nullable omission or server binding differ", created)
	}
	values, err := inventory.AssetObjects.ModelValues(created)
	check(t, err)
	if priority, present := values["priority"]; present != evolved || evolved && !priority.Equal(query.Integer(5)) {
		t.Fatal("added field default was not generated and prepared", values)
	}
	formInput := a.bindForm(t, map[string][]string{"code": {"from-form"}, "enabled": {"on"}}, nil)
	fromForm, err := a.create(t.Context(), formInput)
	check(t, err)
	formValues, err := inventory.AssetObjects.ModelValues(fromForm)
	check(t, err)
	for _, name := range []string{"id", "code"} {
		delete(values, name)
		delete(formValues, name)
	}
	if !reflect.DeepEqual(values, formValues) {
		t.Fatal("Form and JSON have different model/default preparation", values, formValues)
	}
	// A stale Form edits only code. The write must reread and preserve excluded
	// data, including non-editable fields; the bound ID still identifies the row.
	a.form, err = formmodel.NewSpecForFields((inventory.AssetDescriptor{}).Metadata(), []string{"code"})
	check(t, err)
	formUpdate := a.bindForm(t, map[string][]string{"code": {"fresh-form"}}, &fromForm)
	fromForm.Note, fromForm.Stamp = new("fresh note"), "fresh stamp"
	check(t, inventory.AssetObjects.Save(t.Context(), backend, &fromForm))
	changed, err := a.update(t.Context(), fromForm.ID, formUpdate)
	check(t, err)
	if changed.Value.Note == nil || *changed.Value.Note != "fresh note" || changed.Value.Stamp != "fresh stamp" || !slices.Equal(changed.ChangedFields, []string{"code"}) {
		t.Fatal("stale Form overwrote excluded current values", changed)
	}
	patch := a.bindJSON(t, `{"enabled":false,"note":null}`, serializers.ModePartial)
	changed, err = a.update(t.Context(), fromForm.ID, patch)
	check(t, err)
	if changed.Value.Enabled || changed.Value.Note != nil || !slices.Equal(changed.ChangedFields, []string{"enabled", "note"}) {
		t.Fatal("explicit zero or NULL lost presence", changed)
	}
	empty := a.bindJSON(t, `{}`, serializers.ModePartial)
	unchanged, err := a.update(t.Context(), fromForm.ID, empty)
	check(t, err)
	if len(unchanged.ChangedFields) != 0 || !reflect.DeepEqual(changed.Value, unchanged.Value) {
		t.Fatal("empty update changed the row", unchanged)
	}
	if evolved {
		input := a.bindJSON(t, `{"priority":9}`, serializers.ModePartial)
		changed, err := a.update(t.Context(), fromForm.ID, input)
		check(t, err)
		values, err := inventory.AssetObjects.ModelValues(changed.Value)
		check(t, err)
		if !values["priority"].Equal(query.Integer(9)) || !slices.Equal(changed.ChangedFields, []string{"priority"}) {
			t.Fatal("new field required a handwritten converter", values, changed.ChangedFields)
		}
		object, err := a.spec.DecodeObject([]byte(`{"priority":2}`), serializers.Limits{})
		check(t, err)
		input, diagnostics, err := a.body.Bind(t.Context(), object, serializers.ModePartial)
		check(t, err)
		if diagnostics.Empty() || !reflect.DeepEqual(input, orm.ModelInput[inventory.Asset]{}) {
			t.Fatal("new field choices were not enforced atomically")
		}
	}
}

// Same Go type and table, changed IR: a stale generation is not a valid source.
type foreignAssetDescriptor struct{ inventory.AssetDescriptor }

func (foreignAssetDescriptor) Metadata() ir.Model {
	model := (inventory.AssetDescriptor{}).Metadata()
	for index := range model.Fields {
		if model.Fields[index].Name == "code" {
			model.Fields[index].MaxLength++
		}
	}
	return model
}

// Nil embedded storage makes any accidental database call fail the test. Only
// configuration/source/target rejection is permitted to reach this fixture.
type forbiddenSession struct{ db.Session }

func verifySources(t *testing.T, backend resourceBackend) {
	a := newAssembly(t, backend, "sources")
	foreign := orm.NewManager[inventory.Asset](foreignAssetDescriptor{})
	foreignSpec, err := serializers.FromModel(foreignAssetDescriptor{}.Metadata(), serializers.ModelField{Name: "code"})
	check(t, err)
	if _, err := bodyinput.NewResource(a.resource, foreignSpec, api.ParserConfig{}); err == nil {
		t.Fatal("foreign IR source accepted")
	}
	input, err := foreign.Input(map[string]query.Value{"code": query.String("foreign")})
	check(t, err)
	if _, err := a.resource.CreateInSession(t.Context(), forbiddenSession{}, input, a.saveOptions(true)); err == nil {
		t.Fatal("foreign model input accepted")
	}
	lookalike, err := serializers.NewSpec(a.spec.Fields())
	check(t, err)
	if _, err := bodyinput.NewResource(a.resource, lookalike, api.ParserConfig{}); err == nil {
		t.Fatal("generic lookalike acquired model source")
	}
	for _, raw := range []string{`{"id":1}`, `{"stamp":"caller"}`, `{"workspace":7}`} {
		object, err := a.spec.DecodeObject([]byte(raw), serializers.Limits{})
		check(t, err)
		input, failures, err := a.body.Bind(t.Context(), object, serializers.ModePartial)
		check(t, err)
		if failures.Empty() || !reflect.DeepEqual(input, orm.ModelInput[inventory.Asset]{}) {
			t.Fatal("hidden/read-only input accepted", raw)
		}
	}
	current := inventory.NewAssetWithID(7)
	current.Code = "bound"
	input = a.bindForm(t, map[string][]string{"code": {"bound"}, "enabled": {"on"}}, &current)
	if _, err := a.resource.UpdateInSession(t.Context(), forbiddenSession{}, 8, input, crud.SaveOptions[inventory.Asset]{}); err == nil {
		t.Fatal("Form target binding was discarded")
	}
	readOnly, err := crud.New(inventory.AssetObjects, crud.Config{ReadOnly: true})
	check(t, err)
	valid := a.bindJSON(t, `{"code":"readonly"}`, serializers.ModeFull)
	if _, err := readOnly.CreateInSession(t.Context(), forbiddenSession{}, valid, crud.SaveOptions[inventory.Asset]{}); !errors.Is(err, crud.ErrReadOnly) {
		t.Fatal("read-only resource reached storage", err)
	}
	if _, err := bodyinput.NewResource(readOnly, a.spec, api.ParserConfig{}); err == nil {
		t.Fatal("writable JSON widened read-only resource")
	}
	if evolved {
		// Adding a stored field never widens an existing public selection.
		previous, err := crud.New(inventory.AssetObjects, crud.Config{Fields: []string{"code", "enabled", "note"}})
		check(t, err)
		if _, err := bodyinput.NewResource(previous, a.spec, api.ParserConfig{}); err == nil {
			t.Fatal("new field silently widened existing resource")
		}
		spec, err := formmodel.NewSpecForFields((inventory.AssetDescriptor{}).Metadata(), previous.Fields())
		check(t, err)
		bound, err := formmodel.BindInstance(t.Context(), inventory.AssetObjects, spec, forms.NewData(map[string][]string{"code": {"hidden-new"}, "enabled": {"on"}, "priority": {"9"}}), nil, formmodel.PostClean{})
		check(t, err)
		prepared, err := formmodel.PrepareInput(inventory.AssetObjects, bound.BoundForm())
		check(t, err)
		if _, present := prepared.Get("priority"); present {
			t.Fatal("unselected new field entered Form input")
		}
	}
}

func verifyTransactions(t *testing.T, backend resourceBackend) {
	a := newAssembly(t, backend, "transactions")
	other := newAssembly(t, backend, "other-scope")
	first, err := a.create(t.Context(), a.bindJSON(t, `{"code":"scoped-one"}`, serializers.ModeFull))
	check(t, err)
	_, err = other.create(t.Context(), other.bindJSON(t, `{"code":"scoped-two"}`, serializers.ModeFull))
	check(t, err)
	page, err := a.resource.List(t.Context(), backend, crud.ListOptions[inventory.Asset]{Limit: 1})
	check(t, err)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatal("scoped list leaked another workspace", page)
	}
	if _, found, err := other.resource.Get(t.Context(), backend, first.ID); err != nil || found {
		t.Fatal("scoped get leaked another workspace", err)
	}
	if _, err := other.update(t.Context(), first.ID, other.bindJSON(t, `{"code":"forbidden"}`, serializers.ModePartial)); err != crud.ErrNotFound {
		t.Fatal("scoped write reached another workspace", err)
	}
	duplicate, err := a.create(t.Context(), a.bindJSON(t, `{"code":"scoped-one"}`, serializers.ModeFull))
	if err == nil || !reflect.DeepEqual(duplicate, inventory.Asset{}) {
		t.Fatal("unique failure published a model", duplicate, err)
	}
	a.workspace.Writable = false
	check(t, directory.WorkspaceObjects.Save(t.Context(), backend, &a.workspace))
	if value, err := a.update(t.Context(), first.ID, a.bindJSON(t, `{"code":"forbidden"}`, serializers.ModePartial)); !errors.Is(err, deniedWorkspace) || !reflect.DeepEqual(value, crud.Mutation[inventory.Asset]{}) {
		t.Fatal("fresh relation policy was not checked", value, err)
	}
	a.workspace.Writable = true
	check(t, directory.WorkspaceObjects.Save(t.Context(), backend, &a.workspace))
	prepareFailure := errors.New("response preparation failed")
	value, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (inventory.Asset, error) {
		mutation, err := a.resource.CreateInSession(ctx, session, a.bindJSON(t, `{"code":"rollback"}`, serializers.ModeFull), a.saveOptions(true))
		if err != nil {
			return inventory.Asset{}, err
		}
		return mutation.Value, prepareFailure
	})
	if !errors.Is(err, prepareFailure) || !reflect.DeepEqual(value, inventory.Asset{}) {
		t.Fatal("failed output published a provisional model", value, err)
	}
	count, err := inventory.AssetObjects.Using(backend).Filter(inventory.AssetFields.Code.Exact("rollback")).Count(t.Context())
	check(t, err)
	if count != 0 {
		t.Fatal("failed output committed storage")
	}
	deleters, err := project.BindRelationDeleters()
	check(t, err)
	workspace, err := crud.New(directory.WorkspaceObjects, crud.Config{Fields: []string{"name", "writable"}})
	check(t, err)
	deleteWorkspace := func(ctx context.Context, session db.Session, value directory.Workspace) (int64, error) {
		return deleters.DirectoryWorkspace.DeleteInSession(ctx, session.(db.RelationSession), value)
	}
	if removed, err := db.RelationAtomicResult(t.Context(), backend, func(ctx context.Context, session db.RelationSession) (directory.Workspace, error) {
		return workspace.DeleteInSession(ctx, session, a.workspace.ID, crud.DeleteOptions[directory.Workspace]{Delete: deleteWorkspace})
	}); err == nil || !reflect.DeepEqual(removed, directory.Workspace{}) {
		t.Fatal("cross-app PROTECT was bypassed", removed, err)
	}
	removed, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (inventory.Asset, error) {
		return a.resource.DeleteInSession(ctx, session, first.ID, crud.DeleteOptions[inventory.Asset]{Delete: func(ctx context.Context, session db.Session, value inventory.Asset) (int64, error) {
			return inventory.AssetObjects.Delete(ctx, session, &value)
		}})
	})
	check(t, err)
	if removed.ID != first.ID {
		t.Fatal("wrong scoped object deleted")
	}
	_, err = db.RelationAtomicResult(t.Context(), backend, func(ctx context.Context, session db.RelationSession) (directory.Workspace, error) {
		return workspace.DeleteInSession(ctx, session, a.workspace.ID, crud.DeleteOptions[directory.Workspace]{Delete: deleteWorkspace})
	})
	check(t, err)
	if _, found, err := workspace.Get(t.Context(), backend, a.workspace.ID); err != nil || found {
		t.Fatal("unreferenced workspace was not deleted", err)
	}
}
