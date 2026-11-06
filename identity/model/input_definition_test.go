package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func inputPolicyBinding(t *testing.T, role ir.AccountRole) accountmodel.Binding[models.User] {
	t.Helper()
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestSelectedAccountInputDefinitionsShareRolePolicyWithoutChangingSource(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
		login, email    ir.AccountNormalization
		shared, noEmail bool
	}{
		{"separate_nfkc", "Ｆｒｅｄ", "Fred", ir.AccountUnicode16NFKC, ir.AccountUnicode16EmailDomain, false, false},
		{"exact", "Ｆｒｅｄ", "Ｆｒｅｄ", ir.AccountExact, ir.AccountExact, false, false},
		{"shared_roles", "Ｌｏｃａｌ＠ＥＸＡＭＰＬＥ．ＴＥＳＴ", "Local@example.test", ir.AccountUnicode16NFKC, ir.AccountUnicode16EmailDomain, true, false},
		{"shared_domain_only", "Local@EXAMPLE.TEST", "Local@example.test", ir.AccountExact, ir.AccountUnicode16EmailDomain, true, false},
		{"no_email", "Ｆｒｅｄ", "Fred", ir.AccountUnicode16NFKC, "", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			role := modeldef.AccountRole()
			role.WriteLoginNormalization = test.login
			role.WriteEmailNormalization = test.email
			if test.shared {
				role.Email = role.Login
			}
			if test.noEmail {
				role.Email = ""
			}
			binding := inputPolicyBinding(t, role)
			selected := []serializers.ModelField{{Name: role.Login}}
			fields := []string{role.Login}
			raw := map[string][]string{role.Login: {test.raw}}
			members := []serializers.Member{serializers.MemberOf(role.Login, serializers.String(test.raw))}
			if role.Email != "" && role.Email != role.Login {
				selected = append(selected, serializers.ModelField{Name: role.Email, AllowEmpty: true})
				fields = append(fields, role.Email)
				raw[role.Email] = []string{"Local@EXAMPLE.TEST"}
				members = append(members, serializers.MemberOf(role.Email, serializers.String("Local@EXAMPLE.TEST")))
			}
			spec, err := binding.InputSpec(selected...)
			if err != nil {
				t.Fatal(err)
			}
			definition, err := binding.FormDefinition(fields)
			if err != nil {
				t.Fatal(err)
			}
			selected[0].Name = "encoded_password"
			fields[0] = "encoded_password"
			metadata := (models.UserDescriptor{}).Metadata()
			source, found := spec.Model()
			if !found || !source.Equal(metadata) || definition.Fields[0] != role.Login {
				t.Fatal("policy weakened source or retained caller selection")
			}
			objects, err := binding.Objects()
			if err != nil {
				t.Fatal(err)
			}
			body, err := apiinput.NewModel(objects, spec, api.ParserConfig{})
			if err != nil {
				t.Fatal(err)
			}
			object, err := serializers.NewObject(members...)
			if err != nil {
				t.Fatal(err)
			}
			value, failures, err := body.Bind(t.Context(), object, serializers.ModeFull)
			if err != nil || !failures.Empty() {
				t.Fatal("typed role input", err, failures)
			}
			login, _ := value.Get(role.Login)
			actual, _ := login.String()
			if actual != test.want {
				t.Fatal("JSON role normalization differs", actual)
			}
			form, err := definition.Spec(metadata)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := form.Bind(t.Context(), forms.NewData(raw), nil)
			actual, _ = bound.Cleaned().String(role.Login)
			if err != nil || !bound.Valid() || actual != test.want {
				t.Fatal("Form role policy differs", err, bound.Errors())
			}
			for _, field := range spec.Fields() {
				if field.Name() == role.Login && field.MaxInputBytes() != auth.MaximumUsernameBytes {
					t.Fatal("JSON raw login bound lost")
				}
			}
			for _, field := range form.Fields() {
				if field.Name() == role.Login && field.MaxInputBytes() != auth.MaximumUsernameBytes {
					t.Fatal("Form raw login bound lost")
				}
			}
			if role.Email != "" && role.Email != role.Login {
				email, _ := value.Get(role.Email)
				actual, _ = email.String()
				want := "Local@EXAMPLE.TEST"
				if test.email == ir.AccountUnicode16EmailDomain {
					want = "Local@example.test"
				}
				if actual != want {
					t.Fatal("independent email normalization lost")
				}
				actual, _ = bound.Cleaned().String(role.Email)
				if actual != want {
					t.Fatal("Form independent email differs")
				}
			}
		})
	}
}

func TestSelectedAccountInputPolicyRejectsWideningAndRawByteBypass(t *testing.T) {
	role := modeldef.AccountRole()
	binding := inputPolicyBinding(t, role)
	for name, selection := range map[string]serializers.ModelField{
		"conflicting_normalization": {Name: role.Login, Normalization: serializers.StringUnicode16EmailDomain},
		"login_bytes":               {Name: role.Login, MaxInputBytes: auth.MaximumUsernameBytes + 1},
		"email_bytes":               {Name: role.Email, MaxInputBytes: 4097},
		"negative_bytes":            {Name: role.Login, MaxInputBytes: -1},
		"missing_field":             {Name: "missing"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := binding.InputSpec(selection); err == nil {
				t.Fatal("invalid or broader role policy accepted")
			}
		})
	}
	narrow, err := binding.InputSpec(serializers.ModelField{Name: role.Login, MaxInputBytes: 12})
	if err != nil || narrow.Fields()[0].MaxInputBytes() != 12 {
		t.Fatal("explicit narrower bound lost", err)
	}
	readOnly, err := binding.InputSpec(serializers.ModelField{Name: role.Login, ReadOnly: true})
	if err != nil || readOnly.Fields()[0].StringNormalization() != serializers.StringExact || readOnly.Fields()[0].MaxInputBytes() != 0 {
		t.Fatal("read-only input was normalized", err)
	}
	spec, err := binding.InputSpec(serializers.ModelField{Name: role.Login})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := binding.FormDefinition([]string{role.Login})
	if err != nil {
		t.Fatal(err)
	}
	form, err := definition.Spec((models.UserDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Repeat(" ", auth.MaximumUsernameBytes) + "Fred"
	object, err := serializers.NewObject(serializers.MemberOf(role.Login, serializers.String(raw)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := spec.Bind(object, serializers.ModeFull)
	if err != nil || result.Valid() || result.Errors().All()[0].Code() != "max_bytes" {
		t.Fatal("JSON normalization hid raw credential bytes", err)
	}
	bound, err := form.Bind(t.Context(), forms.NewData(map[string][]string{role.Login: {raw}}), nil)
	if err != nil || bound.Valid() || bound.Errors().All()[0].Code() != "max_bytes" {
		t.Fatal("Form normalization hid raw credential bytes", err)
	}
	var zero accountmodel.Binding[models.User]
	if _, err := zero.InputSpec(serializers.ModelField{Name: role.Login}); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
		t.Fatal("zero input binding", err)
	}
	if _, err := zero.FormDefinition([]string{role.Login}); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
		t.Fatal("zero form binding", err)
	}
}
