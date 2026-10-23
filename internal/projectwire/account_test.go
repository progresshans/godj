package projectwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestAccountWireOwnsSelectionConstantsAndExactEscapedBudget(t *testing.T) {
	for _, email := range []string{"", "email<\u2028한글>"} {
		role := modeldef.AccountRole()
		role.Email = email
		staff := false
		role.Staff = ir.AccountBoolean{Constant: &staff}
		input := codegen.ProjectSpec{Apps: []codegen.AppSpec{}, Account: &role}
		for _, spec := range []Spec{Snapshot(input), View(input)} {
			wire, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, maximum := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
				sizer := wirejson.NewSizer(maximum)
				ok := Measure(sizer, spec)
				if ok != (maximum >= len(wire)) || ok && sizer.Size() != len(wire) {
					t.Fatal("account role bypassed exact escaped wire budget", maximum, sizer.Size(), len(wire))
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(wire))
			decoder.UseNumber()
			if err := Scan(decoder); err != nil {
				t.Fatal(err)
			}
			var decoded Spec
			if err := json.Unmarshal(wire, &decoded); err != nil || !reflect.DeepEqual(decoded.Account, &role) {
				t.Fatal("wire changed account selection", err)
			}
			if !reflect.DeepEqual(Declaration(decoded).Account, &role) {
				t.Fatal("declaration dropped account selection")
			}
		}
		copy, cloned := Snapshot(input), Clone(input)
		staff = true
		role.Model.ModelName = "changed"
		if *copy.Account.Staff.Constant || *cloned.Account.Staff.Constant || copy.Account.Model.ModelName != "user" || cloned.Account.Model.ModelName != "user" {
			t.Fatal("account snapshot shares mutable role ownership")
		}
		*copy.Account.Staff.Constant = true
		if *cloned.Account.Staff.Constant {
			t.Fatal("independent account copies alias constants")
		}
	}
}

func TestAccountWireRejectsOpenShapesAndExhaustedNodeBudget(t *testing.T) {
	role := modeldef.AccountRole()
	staff := false
	role.Staff = ir.AccountBoolean{Constant: &staff}
	wire, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	scan := func(raw string, nodes uint64) error {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		return parseAccountRole(decoder, &specBudget{nodes: nodes})
	}
	valid := string(wire)
	if err := scan(valid, projectspec.MaxAggregateNodes-8); err != nil {
		t.Fatal("valid exact-budget account binding", err)
	}
	if err := scan(valid, projectspec.MaxAggregateNodes-7); err == nil {
		t.Fatal("account references bypassed aggregate node budget")
	}
	for _, invalid := range []string{
		`null`, `[]`, `{}`, strings.Replace(valid, `"login":"username",`, ``, 1),
		strings.Replace(valid, `"login":"username"`, `"login":null`, 1),
		strings.Replace(valid, `"login":"username"`, `"login":"username","login":"email"`, 1),
		strings.Replace(valid, `"login":"username"`, `"login":"`+strings.Repeat("x", projectspec.MaxSchemaStringBytes+1)+`"`, 1),
		strings.Replace(valid, `"constant":false`, `"constant":null`, 1),
		strings.Replace(valid, `"constant":false`, `"constant":"false"`, 1),
		strings.Replace(valid, `"constant":false`, `"constant":false,"field":"staff"`, 1),
		strings.Replace(valid, `"constant":false`, ``, 1),
		strings.Replace(valid, `"constant":false`, `"constant":false,"callback":"trusted"`, 1),
		strings.Replace(valid, `"group_permissions":"permissions"`, `"group_permissions":[]`, 1),
		strings.Replace(valid, `"group_permissions":"permissions"`, `"group_permissions":"permissions","sql":"SELECT"`, 1),
	} {
		if err := scan(invalid, 0); err == nil {
			t.Fatal("open or malformed account wire accepted", invalid)
		}
	}
}

func TestAccountWireValidationRetainsSchemaAndResourceChecks(t *testing.T) {
	app, err := modeldef.AppSpec()
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	input := codegen.ProjectSpec{Apps: []codegen.AppSpec{app}, Account: &role}
	if err := Validate(input); err != nil {
		t.Fatal(err)
	}
	role.Login = "missing"
	if err := Validate(input); err == nil {
		t.Fatal("wire validation dropped selected-model semantics")
	}
	role.Login = strings.Repeat("x", projectspec.MaxSchemaStringBytes+1)
	var resource *projectspec.ResourceError
	if err := Validate(input); !errors.As(err, &resource) || resource.Path != "account.login" {
		t.Fatal("account strings were not bounded before normalization", err)
	}
}
