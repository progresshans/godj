package model_test

import (
	"testing"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
)

func TestLoginInputOwnsSelectedModelPolicyBeforeIO(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	role.LoginFormNormalization = ir.AccountExact
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	input, err := binding.LoginInput()
	if err != nil || input.Label() != "Username" || input.MaxLength() != 256 || input.Normalization() != auth.LoginExact {
		t.Fatal("binding inferred default form normalization", err)
	}
	role.LoginFormNormalization = ir.AccountUnicode16NFKC
	again, err := binding.LoginInput()
	if err != nil || again != input {
		t.Fatal("binding form declaration is mutable", err)
	}
	backend := &untouchedProfileBackend{}
	store, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	declared, err := store.LoginInput()
	if err != nil || declared.Normalization() != auth.LoginUnicode16NFKC || backend.calls != 0 || backend.writes != 0 {
		t.Fatal("generated source performed I/O or lost its form declaration", err)
	}
	var zero accountmodel.Binding[models.User]
	if input, err := zero.LoginInput(); err == nil || input != (auth.LoginInput{}) {
		t.Fatal("unbound account inferred input")
	}
}
