package consumer

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings/database"
)

// Both independent generated account types use the real Form/JSON ModelInput
// transfer and the generated profile service factory. No hand-written SQL,
// default User copy, direct row patch or custom authorization adapter is used.
func assertProfileEditor[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], editor *accountmodel.ProfileEditor[M], reader *accountmodel.ProfileReader[M], authenticator *accountmodel.Authenticator[M], sessionStore *accountmodel.SessionStore[M], actor auth.Principal, row M, field, desired string, revoke func(bool) error) {
	t.Helper()
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := objects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	key, present, err := objects.PrimaryKey(row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		t.Fatal("typed profile has no stored key", err)
	}
	role, err := binding.Role()
	if err != nil {
		t.Fatal(err)
	}
	form, err := formmodel.NewSpecForFields(metadata, editor.Fields())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := reader.Get(t.Context(), actor, id)
	if err != nil || profile.Revision() != 1 {
		t.Fatal("generated profile reader", err)
	}
	bound, err := formmodel.BindView(t.Context(), objects, form, forms.NewData(map[string][]string{field: {"  " + desired + "  "}}), profile.View(), formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := formmodel.PrepareInput(objects, bound)
	if err != nil {
		t.Fatal(err)
	}
	change, err := editor.Update(t.Context(), actor, id, 1, prepared)
	if err != nil || change.ID != id || change.Revision != 2 || !reflect.DeepEqual(change.ChangedFields, []string{field}) {
		t.Fatal("typed model form profile edit", change, err)
	}
	output, err := json.Marshal(change)
	if err != nil || strings.Contains(string(output), desired) || strings.Contains(string(output), role.Password) || strings.Contains(string(output), actor.ID()) {
		t.Fatal("profile result exposed model values", err)
	}
	spec := profileJSONSpec(t, metadata, field)
	body, err := apiinput.NewModel(objects, spec, api.ParserConfig{})
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw string) orm.ModelInput[M] {
		object, err := spec.DecodeObject([]byte(raw), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		input, failures, err := body.Bind(t.Context(), object, serializers.ModePartial)
		if err != nil || !failures.Empty() {
			t.Fatal("typed JSON profile intent", failures, err)
		}
		return input
	}
	// A JSON omission and an equivalent cleaned Form value both preserve the
	// current revision/audit. A real JSON edit advances both; stale Form input
	// cannot overwrite that freshly committed result.
	change, err = editor.Update(t.Context(), actor, id, 2, decode(`{}`))
	if err != nil || change.Revision != 2 || len(change.ChangedFields) != 0 {
		t.Fatal("JSON omission was not a no-op", change, err)
	}
	wire, err := json.Marshal(map[string]string{field: "  " + desired + "  "})
	if err != nil {
		t.Fatal(err)
	}
	change, err = editor.Update(t.Context(), actor, id, 2, decode(string(wire)))
	if err != nil || change.Revision != 2 || len(change.ChangedFields) != 0 {
		t.Fatal("Form/JSON cleaning semantics diverged", change, err)
	}
	wire, err = json.Marshal(map[string]string{field: "  " + desired + " JSON  "})
	if err != nil {
		t.Fatal(err)
	}
	change, err = editor.Update(t.Context(), actor, id, 2, decode(string(wire)))
	if err != nil || change.Revision != 3 || !reflect.DeepEqual(change.ChangedFields, []string{field}) {
		t.Fatal("JSON profile edit did not advance revision", change, err)
	}
	if change, err = editor.Update(t.Context(), actor, id, 2, prepared); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) || change.Revision != 0 {
		t.Fatal("stale typed profile accepted", err)
	}
	for _, name := range []string{role.Password, role.PrincipalID, role.Revision, role.LastLogin, role.Active.Field, role.Superuser.Field} {
		if name == "" {
			continue
		}
		raw, err := json.Marshal(map[string]string{name: "forged"})
		if err != nil {
			t.Fatal(err)
		}
		object, err := spec.DecodeObject(raw, serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if invalid, failures, err := body.Bind(t.Context(), object, serializers.ModePartial); err != nil || failures.Empty() || invalid.Values() != nil {
			t.Fatal("JSON exposed a reserved account field", name, err)
		}
	}
	wrong, err := prepared.WithTargetKey(query.Integer(id + 1))
	if err == nil {
		t.Fatal("prepared form lost immutable target", wrong)
	}
	entries, err := reader.History(t.Context(), actor, id, 100)
	if err != nil || len(entries) != 2 || entries[0].ActorID() != actor.ID() || entries[1].ActorID() != actor.ID() || !reflect.DeepEqual(entries[0].ChangedFields(), []string{field}) || !reflect.DeepEqual(entries[1].ChangedFields(), []string{field}) {
		t.Fatal("typed profile read audit differs", err)
	}
	assertProfileHTTP(t, backend, binding, editor, reader, authenticator, sessionStore, actor, row, field, desired, revoke)
}

// This consumer deliberately uses the model form's blank policy for its one
// exposed JSON field. An empty default is valid only when Blank permits it;
// the custom model's required profile field keeps its nonempty policy.
func profileJSONSpec(t *testing.T, model ir.Model, name string) serializers.Spec {
	t.Helper()
	for _, field := range model.Fields {
		if field.Name != name {
			continue
		}
		spec, err := serializers.FromModel(model, serializers.ModelField{Name: name, AllowEmpty: field.Blank})
		if err != nil {
			t.Fatal(err)
		}
		return spec
	}
	t.Fatal("selected profile field is absent from its model")
	return serializers.Spec{}
}
