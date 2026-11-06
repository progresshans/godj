package model_test

import (
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/serializers"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func TestManagementJSONInputPreservesOperationExposureAndSelectedPolicy(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountManagement(store, verifier, auth.PrincipalAuthorizer{}, unitManagementConfig())
	if err != nil {
		t.Fatal(err)
	}
	fields := []serializers.ModelField{{Name: "username"}, {Name: "email", AllowEmpty: true}, {Name: "groups", Optional: true}}
	spec, err := owner.InputSpec(admin.ActionAdd, fields...)
	if err != nil {
		t.Fatal(err)
	}
	fields[0].Name = "principal_id"
	metadata, present := spec.Model()
	if !present || !metadata.Equal((models.UserDescriptor{}).Metadata()) || spec.Fields()[0].Name() != "username" || spec.Fields()[0].StringNormalization() != serializers.StringUnicode16NFKC || spec.Fields()[0].MaxInputBytes() != auth.MaximumUsernameBytes {
		t.Fatal("operation input lost source, ownership or role policy")
	}
	for _, test := range []struct {
		name   string
		action admin.Action
		field  serializers.ModelField
	}{
		{"creation_only", admin.ActionChange, serializers.ModelField{Name: "username"}},
		{"edit_only", admin.ActionAdd, serializers.ModelField{Name: "first_name"}},
		{"protected", admin.ActionAdd, serializers.ModelField{Name: "principal_id"}},
		{"readonly", admin.ActionAdd, serializers.ModelField{Name: "username", ReadOnly: true}},
		{"widened_bytes", admin.ActionAdd, serializers.ModelField{Name: "username", MaxInputBytes: 1025}},
		{"conflicting_normalization", admin.ActionAdd, serializers.ModelField{Name: "username", Normalization: serializers.StringUnicode16EmailDomain}},
		{"invalid_action", admin.ActionDelete, serializers.ModelField{Name: "username"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := owner.InputSpec(test.action, test.field); err == nil {
				t.Fatal("invalid management input published")
			}
		})
	}
	for _, owner := range []*accountmodel.AccountManagement[models.User]{nil, {}} {
		if _, err := owner.InputSpec(admin.ActionAdd, serializers.ModelField{Name: "username"}); err == nil {
			t.Fatal("zero owner published input")
		}
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("input declaration performed I/O")
	}
}
