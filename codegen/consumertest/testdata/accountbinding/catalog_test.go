package consumer

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	apioutput "github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func catalogConsumerConfig[G, P any](role ir.AccountRole, groupExtra, permissionExtra string) accountmodel.CatalogConfig[G, P] {
	policy := accountmodel.CatalogPermissions{View: "account.shared", Add: "account.shared", Change: "account.team", Delete: "account.shared"}
	groupFields := []string{role.Catalog.GroupName, role.Catalog.GroupPermissions}
	permissionFields := []string{role.Catalog.PermissionCode, role.Catalog.PermissionName}
	if groupExtra != "" {
		groupFields = append(groupFields, groupExtra)
	}
	if permissionExtra != "" {
		permissionFields = append(permissionFields, permissionExtra)
	}
	return accountmodel.CatalogConfig[G, P]{
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.shared", Change: "account.team"},
		Group:              accountmodel.CatalogModelConfig[G]{ReadFields: groupFields, WriteFields: groupFields, Permissions: policy},
		Permission:         accountmodel.CatalogModelConfig[P]{ReadFields: permissionFields, WriteFields: permissionFields, Permissions: policy},
	}
}

func catalogConsumerSpec[M any](t *testing.T, resource accountmodel.CatalogResource[M], names []string, readOnly bool) serializers.Spec {
	t.Helper()
	model, err := resource.Manager().Metadata()
	if err != nil {
		t.Fatal(err)
	}
	fields := make([]serializers.ModelField, 0, len(names))
	for _, name := range names {
		selection := serializers.ModelField{Name: name, ReadOnly: readOnly}
		for _, field := range model.Fields {
			if field.Name == name {
				selection.AllowEmpty = field.Blank && (field.Kind == ir.FieldChar || field.Kind == ir.FieldText || field.Kind == ir.FieldEmail)
			}
		}
		fields = append(fields, selection)
	}
	spec, err := serializers.FromModel(model, fields...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func catalogConsumerJSON[M any](t *testing.T, resource accountmodel.CatalogResource[M], data map[string]any) orm.ModelInput[M] {
	t.Helper()
	spec := catalogConsumerSpec(t, resource, resource.WriteFields(), false)
	body, err := apiinput.NewModel(resource.Manager(), spec, api.ParserConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	object, err := spec.DecodeObject(wire, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	input, failures, err := body.Bind(t.Context(), object, serializers.ModePartial)
	if err != nil || !failures.Empty() {
		t.Fatal("catalog typed JSON binding", failures, err)
	}
	return input
}

// The same Form/API consumer is instantiated with unrelated generated Group
// and Permission types. Custom scalar fields and NoReverse relations remain
// part of their actual source; there is no fixed DTO or hidden default User.
func assertCatalogConsumer[G, P any](t *testing.T, catalog *accountmodel.Catalog[G, P], actor auth.Principal, role ir.AccountRole, groupExtra, permissionExtra string) {
	t.Helper()
	groups, permissions := catalog.Groups(), catalog.Permissions()
	permissionModel, err := permissions.Manager().Metadata()
	if err != nil {
		t.Fatal(err)
	}
	permissionForm, err := formmodel.NewSpecForFields(permissionModel, permissions.WriteFields())
	if err != nil {
		t.Fatal(err)
	}
	permissionData := map[string][]string{role.Catalog.PermissionCode: {"  documents.inspect  "}, role.Catalog.PermissionName: {"  Inspect documents  "}}
	if permissionExtra != "" {
		permissionData[permissionExtra] = []string{"  Generated custom description  "}
	}
	boundPermission, err := formmodel.BindInstance(t.Context(), permissions.Manager(), permissionForm, forms.NewData(permissionData), nil, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	permissionInput, err := formmodel.PrepareInput(permissions.Manager(), boundPermission.BoundForm())
	if err != nil {
		t.Fatal(err)
	}
	permission, err := permissions.Create(t.Context(), actor, permissionInput)
	if err != nil || permission.Revision != 1 {
		t.Fatal("generated permission form create", err)
	}
	permissionView, err := permissions.Get(t.Context(), actor, permission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if permissionExtra != "" {
		if value, present := permissionView.View().Get(permissionExtra); !present || !value.Equal(query.String("Generated custom description")) {
			t.Fatal("custom typed permission field lost")
		}
	}
	groupModel, err := groups.Manager().Metadata()
	if err != nil {
		t.Fatal(err)
	}
	groupForm, err := formmodel.NewSpecForFields(groupModel, groups.WriteFields())
	if err != nil {
		t.Fatal(err)
	}
	groupForm, err = groupForm.WithModelChoices(role.Catalog.GroupPermissions, forms.Choice{Value: forms.Integer(permission.ID), Label: "Inspect documents"})
	if err != nil {
		t.Fatal(err)
	}
	groupData := map[string][]string{role.Catalog.GroupName: {"  Reviewers  "}, role.Catalog.GroupPermissions: {strconv.FormatInt(permission.ID, 10)}}
	if groupExtra != "" {
		groupData[groupExtra] = []string{"7"}
	}
	boundGroup, err := formmodel.BindInstance(t.Context(), groups.Manager(), groupForm, forms.NewData(groupData), nil, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	groupInput, err := formmodel.PrepareInput(groups.Manager(), boundGroup.BoundForm())
	if err != nil {
		t.Fatal(err)
	}
	group, err := groups.Create(t.Context(), actor, groupInput)
	if err != nil || group.Revision != 1 {
		t.Fatal("generated group form create", err)
	}
	view, err := groups.Get(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys, loaded := view.View().Collection(role.Catalog.GroupPermissions); !loaded || len(keys) != 1 || keys[0] != permission.ID {
		t.Fatal("generated selected group grants missing")
	}
	if groupExtra != "" {
		if value, present := view.View().Get(groupExtra); !present || !value.Equal(query.Integer(7)) {
			t.Fatal("custom typed integer field lost")
		}
	}
	for _, name := range []string{"id", role.Catalog.GroupRevision} {
		if _, exposed := view.View().Get(name); exposed {
			t.Fatal("catalog made server field implicit output", name)
		}
	}
	outputSpec := catalogConsumerSpec(t, groups, groups.ReadFields(), true)
	output, err := apioutput.New(apioutput.ModelView(groups.Manager(), outputSpec), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := output.Encode(t.Context(), view.View())
	if err != nil || !strings.Contains(string(wire), "Reviewers") || !strings.Contains(string(wire), role.Catalog.GroupPermissions) {
		t.Fatal("selected catalog API output", err)
	}
	groupData[role.Catalog.GroupName] = []string{"  Edited reviewers  "}
	boundEdit, err := formmodel.BindView(t.Context(), groups.Manager(), groupForm, forms.NewData(groupData), view.View(), formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	editInput, err := formmodel.PrepareInput(groups.Manager(), boundEdit)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 1, editInput); err != nil || result.Revision != 2 {
		t.Fatal("catalog form edit from loaded view", err)
	}
	jsonData := map[string]any{role.Catalog.GroupName: "  JSON reviewers  "}
	if groupExtra != "" {
		jsonData[groupExtra] = 11
	}
	jsonInput := catalogConsumerJSON(t, groups, jsonData)
	if result, err := groups.Update(t.Context(), actor, group.ID, 2, jsonInput); err != nil || result.Revision != 3 {
		t.Fatal("catalog typed JSON update", err)
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 2, editInput); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) || result.Revision != 0 {
		t.Fatal("stale catalog form overwrote JSON", err)
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 3, catalogConsumerJSON(t, groups, map[string]any{})); err != nil || result.Revision != 3 || len(result.ChangedFields) != 0 {
		t.Fatal("catalog JSON omission changed state", err)
	}
	current, err := groups.Get(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys, loaded := current.View().Collection(role.Catalog.GroupPermissions); !loaded || len(keys) != 1 || keys[0] != permission.ID {
		t.Fatal("catalog partial JSON cleared grants")
	}
	if groupExtra != "" {
		if value, _ := current.View().Get(groupExtra); !value.Equal(query.Integer(11)) {
			t.Fatal("typed JSON custom integer lost")
		}
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 3, catalogConsumerJSON(t, groups, map[string]any{role.Catalog.GroupPermissions: []int64{}})); err != nil || result.Revision != 4 {
		t.Fatal("catalog JSON explicit grant clear", err)
	}
	if entries, err := groups.History(t.Context(), actor, group.ID, 100); err != nil || len(entries) != 4 {
		t.Fatal("catalog form and JSON audit", err)
	}
	if result, err := permissions.Update(t.Context(), actor, permission.ID, 1, catalogConsumerJSON(t, permissions, map[string]any{role.Catalog.PermissionCode: "documents.review"})); err != nil || result.ID != permission.ID || result.Revision != 2 {
		t.Fatal("typed permission rename", err)
	}
	if result, err := groups.Delete(t.Context(), actor, group.ID, 4); err != nil || result.DeletedRows != 1 {
		t.Fatal("typed catalog group delete", err)
	}
	if result, err := permissions.Delete(t.Context(), actor, permission.ID, 2); err != nil || result.DeletedRows != 1 {
		t.Fatal("typed catalog permission delete", err)
	}
}
