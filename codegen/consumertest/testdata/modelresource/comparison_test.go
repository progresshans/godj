package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"example.com/godj-project-bundle/inventory"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

func verifyComparison(t *testing.T, backend resourceBackend) {
	a := newAssembly(t, backend, "comparison")
	manual, err := manualBody(a.spec)
	check(t, err)
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		before, err := manual.Schema(mode)
		check(t, err)
		after, err := a.body.Schema(mode)
		check(t, err)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("same scalar policy produced a different request schema")
		}
	}
	if manualPermission() != editPermission() || !slices.Equal(manualResponseFields(), responseFields()) {
		t.Fatal("comparison variants changed different application policies")
	}
	oldActor := principal(t, "comparison-old", oldPermission)
	for _, permission := range []auth.Permission{manualPermission(), editPermission()} {
		allowed, err := (auth.PrincipalAuthorizer{}).Allowed(t.Context(), oldActor, permission)
		check(t, err)
		if allowed == evolved {
			t.Fatal("before/after permission change was not effective")
		}
	}
	var fields []serializers.ModelField
	for _, name := range manualResponseFields() {
		fields = append(fields, serializers.ModelField{Name: name, ReadOnly: true, AllowEmpty: name == "note"})
	}
	model := (inventory.AssetDescriptor{}).Metadata()
	spec, err := serializers.FromModel(model, fields...)
	check(t, err)
	encoder, err := serializers.NewModelEncoder(spec, model, (inventory.AssetDescriptor{}).WriteFieldValue)
	check(t, err)
	manualOutput, err := output.New(output.Named("Asset", output.Model(encoder)), serializers.Limits{})
	check(t, err)
	if !reflect.DeepEqual(manualOutput.Schema(), a.out.Schema()) {
		t.Fatal("same response policy produced a different schema")
	}
	rollback := errors.New("comparison observation rollback")
	type observation struct {
		values   map[string]query.Value
		response map[string]json.RawMessage
	}
	observe := func(run func(context.Context, db.Session) (inventory.Asset, error), out output.Output[inventory.Asset]) observation {
		var observed observation
		result, err := db.AtomicResult(t.Context(), backend, func(ctx context.Context, session db.Session) (inventory.Asset, error) {
			value, err := run(ctx, session)
			if err != nil {
				return inventory.Asset{}, err
			}
			observed.values, err = inventory.AssetObjects.ModelValues(value)
			if err != nil {
				return inventory.Asset{}, err
			}
			delete(observed.values, "id")
			wire, err := out.Encode(ctx, value)
			if err != nil {
				return inventory.Asset{}, err
			}
			if err := json.Unmarshal(wire, &observed.response); err != nil {
				return inventory.Asset{}, err
			}
			delete(observed.response, "id")
			return value, rollback
		})
		if !errors.Is(err, rollback) || !reflect.DeepEqual(result, inventory.Asset{}) {
			t.Fatal("comparison did not roll back its observation", err)
		}
		return observed
	}
	for _, raw := range []string{`{"code":"compare"}`, `{"code":"compare","enabled":false,"note":null}`, `{"code":"compare","note":"value"}`} {
		if evolved {
			raw = raw[:len(raw)-1] + `,"priority":9}`
		}
		object, err := a.spec.DecodeObject([]byte(raw), serializers.Limits{})
		check(t, err)
		before, failures, err := manual.Bind(t.Context(), object, serializers.ModeFull)
		check(t, err)
		if !failures.Empty() {
			t.Fatal(failures)
		}
		after := a.bindJSON(t, raw, serializers.ModeFull)
		left := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
			return inventory.AssetObjects.Create(ctx, session, before.create(a.workspace.ID))
		}, manualOutput)
		right := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
			mutation, err := a.resource.CreateInSession(ctx, session, after, a.saveOptions(true))
			return mutation.Value, err
		}, a.out)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("same JSON change differed between manual/model assembly", left, right)
		}
	}
	formValues := map[string][]string{"code": {"compare"}, "enabled": {"on"}, "note": {"form value"}}
	if evolved {
		formValues["priority"] = []string{"9"}
	}
	bound, err := formmodel.BindInstance(t.Context(), inventory.AssetObjects, a.form, forms.NewData(formValues), nil, formmodel.PostClean{})
	check(t, err)
	before, err := manualFormCreate(bound.BoundForm(), a.workspace.ID)
	check(t, err)
	after, err := formmodel.PrepareInput(inventory.AssetObjects, bound.BoundForm())
	check(t, err)
	left := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
		return inventory.AssetObjects.Create(ctx, session, before)
	}, manualOutput)
	right := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
		mutation, err := a.resource.CreateInSession(ctx, session, after, a.saveOptions(true))
		return mutation.Value, err
	}, a.out)
	if !reflect.DeepEqual(left, right) {
		t.Fatal("same Form change differed between manual/model assembly", left, right)
	}
	current, err := a.create(t.Context(), a.bindJSON(t, `{"code":"patch-base","note":"initial"}`, serializers.ModeFull))
	check(t, err)
	for _, raw := range []string{`{"enabled":false}`, `{"note":null}`} {
		if evolved {
			raw = raw[:len(raw)-1] + `,"priority":1}`
		}
		object, err := a.spec.DecodeObject([]byte(raw), serializers.Limits{})
		check(t, err)
		before, failures, err := manual.Bind(t.Context(), object, serializers.ModePartial)
		check(t, err)
		if !failures.Empty() {
			t.Fatal(failures)
		}
		after := a.bindJSON(t, raw, serializers.ModePartial)
		left := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
			return inventory.AssetObjects.Patch(ctx, session, current, before.patch())
		}, manualOutput)
		right := observe(func(ctx context.Context, session db.Session) (inventory.Asset, error) {
			mutation, err := a.resource.UpdateInSession(ctx, session, current.ID, after, a.saveOptions(false))
			return mutation.Value, err
		}, a.out)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("same PATCH change differed between manual/model assembly", left, right)
		}
	}
	count, err := a.resource.Query(backend).Filter(inventory.AssetFields.Code.Exact("compare")).Count(t.Context())
	check(t, err)
	if count != 0 {
		t.Fatal("comparison observations changed persistent storage")
	}
}
