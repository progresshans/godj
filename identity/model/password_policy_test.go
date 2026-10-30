package model_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
)

func TestSelectedPasswordPolicyUsesDeclaredAttributesAndRawPassword(t *testing.T) {
	binding := credentialBinding(t, modeldef.AccountRole())
	row := credentialRow()
	row.Username, row.Email, row.FirstName = "abcdefgh", "person@example.test", "Synthetic-Profile-Character-Gamma"
	before := row
	attributes := []accountmodel.PasswordAttribute{{Field: "first_name", Label: "display name"}}
	selected, err := binding.DefaultPasswordValidator(attributes...)
	if err != nil {
		t.Fatal(err)
	}
	attributes[0] = accountmodel.PasswordAttribute{Field: "email", Label: "private label"}
	diagnostics, err := selected(t.Context(), row.FirstName, row)
	if err != nil || len(diagnostics.All()) != 1 || diagnostics.All()[0].Code() != "password_too_similar" ||
		len(diagnostics.All()[0].Params()) != 1 || diagnostics.All()[0].Params()[0].Value() != "display name" {
		t.Fatal("typed policy lost its selected field or copied declaration", err, diagnostics)
	}
	defaultPolicy, err := binding.DefaultPasswordValidator()
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics, err := defaultPolicy(t.Context(), row.FirstName, row); err != nil || !diagnostics.Empty() {
		t.Fatal("unselected profile became an implicit policy", err)
	}
	// Passwords are not NFKC-normalized even though selected login writes are.
	if diagnostics, err := defaultPolicy(t.Context(), "ａｂｃｄｅｆｇｈ", row); err != nil || !diagnostics.Empty() {
		t.Fatal("password was normalized before strength checks", err)
	}
	if diagnostics, err := defaultPolicy(t.Context(), row.Username, row); err != nil || diagnostics.Empty() {
		t.Fatal("actual login role was omitted", err)
	}
	withoutAttributes, err := binding.DefaultPasswordValidator([]accountmodel.PasswordAttribute{}...)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics, err := withoutAttributes(t.Context(), row.FirstName, row); err != nil || !diagnostics.Empty() {
		t.Fatal("explicit empty attribute policy changed", err)
	}
	for _, raw := range []string{"123", "password", "１２３４５６７８", "\xff"} {
		if diagnostics, err := withoutAttributes(t.Context(), raw, row); err != nil || diagnostics.Empty() {
			t.Fatal("default strength rule disappeared", err)
		}
	}
	if !reflect.DeepEqual(row, before) {
		t.Fatal("password policy mutated its typed profile")
	}
	if _, err := selected(nil, row.FirstName, row); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if diagnostics, err := selected(ctx, row.FirstName, row); !errors.Is(err, context.Canceled) || !diagnostics.Empty() {
		t.Fatal("cancellation became password diagnostics", err)
	}
}

func TestSelectedPasswordPolicyRejectsSecretAndInvalidAttributesBeforeUse(t *testing.T) {
	binding := credentialBinding(t, modeldef.AccountRole())
	for _, test := range []struct {
		name   string
		fields []accountmodel.PasswordAttribute
	}{
		{"credential", []accountmodel.PasswordAttribute{{Field: "encoded_password", Label: "credential"}}},
		{"principal", []accountmodel.PasswordAttribute{{Field: "principal_id", Label: "principal"}}},
		{"revision", []accountmodel.PasswordAttribute{{Field: "revision", Label: "revision"}}},
		{"server_time", []accountmodel.PasswordAttribute{{Field: "date_joined", Label: "time"}}},
		{"wrong_kind", []accountmodel.PasswordAttribute{{Field: "active", Label: "active"}}},
		{"unknown", []accountmodel.PasswordAttribute{{Field: "private-unknown", Label: "unknown"}}},
		{"duplicate", []accountmodel.PasswordAttribute{{Field: "username", Label: "login"}, {Field: "username", Label: "copy"}}},
		{"empty_label", []accountmodel.PasswordAttribute{{Field: "username"}}},
		{"control_label", []accountmodel.PasswordAttribute{{Field: "username", Label: "private\nlabel"}}},
		{"invalid_label", []accountmodel.PasswordAttribute{{Field: "username", Label: "\xff"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, err := binding.DefaultPasswordValidator(test.fields...)
			if err == nil || policy != nil || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid attribute policy was published or disclosed")
			}
		})
	}
	var zero accountmodel.Binding[models.User]
	if policy, err := zero.DefaultPasswordValidator(); policy != nil || err == nil {
		t.Fatal("unbound policy accepted")
	}
}
