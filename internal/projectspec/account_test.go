package projectspec

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestAccountLoginFormPolicyIsBoundedBeforeNormalization(t *testing.T) {
	role := ir.AccountRole{LoginFormNormalization: ir.AccountNormalization(strings.Repeat("x", MaxSchemaStringBytes+1))}
	err := ValidateAccountRoleResources(&role)
	var resource *ResourceError
	if !errors.As(err, &resource) || resource.Path != "account.login_form_normalization" || resource.Limit != "schema_string_bytes" {
		t.Fatal("form policy bypassed project wire allocation bound", err)
	}
	role.LoginFormNormalization = "\xff"
	err = ValidateAccountRoleResources(&role)
	if !errors.As(err, &resource) || resource.Path != "account.login_form_normalization" || resource.Limit != "schema_string_utf8" {
		t.Fatal("form policy bypassed project UTF-8 bound", err)
	}
}
