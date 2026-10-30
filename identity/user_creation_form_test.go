package identity

import (
	"testing"

	"github.com/progresshans/godj/identity/models"
)

func TestUninitializedCreationFormCannotExposeAllModelFields(t *testing.T) {
	for _, form := range []*UserCreationForm{nil, {}} {
		definition := form.Definition()
		if definition.Fields == nil || len(definition.Fields) != 0 {
			t.Fatal("invalid form selected all User fields")
		}
		if _, err := definition.Spec(models.UserDescriptor{}.Metadata()); err == nil {
			t.Fatal("invalid identity form projected private storage fields")
		}
		if _, err := form.Spec().Unbound(nil); err == nil {
			t.Fatal("invalid form published a spec")
		}
	}
	for _, factory := range []func(*Manager) (*UserCreationForm, error){NewUserCreationForm, NewAdminUserCreationForm} {
		for _, manager := range []*Manager{nil, {}} {
			if form, err := factory(manager); err == nil || form != nil {
				t.Fatal("uninitialized manager was accepted")
			}
		}
	}
}
