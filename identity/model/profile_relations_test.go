package model_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	fixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestProfileRelationRequiresExplicitCanonicalPolicyBeforeIO(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	config := accountmodel.ProfileRelationConfig[access.Team]{
		Choices: formmodel.ModelChoiceConfig[access.Team]{Label: func(row access.Team) (string, error) { calls++; return row.Title, nil }},
		Filter: func(context.Context, db.Queryer, auth.Principal, accountmodel.ProfileRelationOrigin) ([]orm.Predicate[access.Team], error) {
			calls++
			return nil, nil
		},
	}
	relation, err := accountmodel.NewProfileRelation(binding, "sponsor", access.TeamObjects, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"", "missing", "contact", "secret", "subject", "version", "teams", "grants", "leader"} {
		if _, err := accountmodel.NewProfileRelation(binding, field, access.TeamObjects, config); err == nil {
			t.Fatal("invalid relation/target accepted", field)
		}
	}
	missing := config
	missing.Filter = nil
	if _, err := accountmodel.NewProfileRelation(binding, "sponsor", access.TeamObjects, missing); err == nil {
		t.Fatal("implicit unrestricted policy accepted")
	}
	missing = config
	missing.Choices.Label = nil
	if _, err := accountmodel.NewProfileRelation(binding, "sponsor", access.TeamObjects, missing); err == nil {
		t.Fatal("unbounded/unpresentable choice declaration accepted")
	}
	backend := &untouchedProfileBackend{}
	store, err := accountmodel.NewCredentialStore(backend, binding, access.TeamDescriptor{}, access.GrantDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	policy := accountmodel.ProfileConfig[people.Member]{Fields: []string{"sponsor"}, Relations: []accountmodel.ProfileRelation[people.Member]{relation}, ChangePermission: "account.change"}
	owner, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, policy)
	if err != nil || owner == nil {
		t.Fatal("explicit typed relation policy rejected", err)
	}
	for _, configured := range [][]accountmodel.ProfileRelation[people.Member]{nil, {{}}, {relation, relation}} {
		invalid := policy
		invalid.Relations = configured
		if _, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, invalid); err == nil {
			t.Fatal("missing, unbound or duplicate relation policy accepted")
		}
	}
	invalid := policy
	invalid.Fields = []string{"region"}
	if _, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, invalid); err == nil {
		t.Fatal("unselected relation policy silently changed exposure")
	}
	// Same account, role and target metadata in a different host graph must not
	// supply relation policy to this store. No descriptor fields are forged.
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var schemas []ir.Schema
	for _, app := range spec.Apps {
		schemas = append(schemas, app.Schema)
	}
	extra, err := schema.Build(schema.Definition{AppLabel: "extra", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.CharField("title", "Title", 24)}}}})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := accountmodel.Bind(*spec.Account, people.MemberDescriptor{}, append(schemas, extra)...)
	if err != nil {
		t.Fatal(err)
	}
	other, err := accountmodel.NewProfileRelation(foreign, "sponsor", access.TeamObjects, config)
	if err != nil {
		t.Fatal(err)
	}
	invalid = policy
	invalid.Relations = []accountmodel.ProfileRelation[people.Member]{other}
	if _, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, invalid); err == nil {
		t.Fatal("foreign project relation policy accepted")
	}
	if calls != 0 || backend.calls != 0 || backend.writes != 0 {
		t.Fatal("pure relation construction invoked a policy or native owner")
	}
	if text := fmt.Sprintf("%+v %#v", relation, accountmodel.ProfileRelationOrigin{}); strings.Contains(text, "sponsor") || strings.Contains(text, "access") {
		t.Fatal("relation diagnostics exposed declaration state")
	}
}
