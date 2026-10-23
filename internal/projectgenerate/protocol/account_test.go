package protocol

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema/ir"
)

func TestProjectAccountSelectionSurvivesCanonicalProtocolAndOwnsConstants(t *testing.T) {
	spec, err := modeldef.ProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	staff := false
	spec.Account.Staff = ir.AccountBoolean{Constant: &staff}
	document, err := EncodeResponse(Response{OK: true, ProjectSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	result, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !result.OK || !reflect.DeepEqual(result.ProjectSpec.Account, spec.Account) {
		t.Fatal("project account was dropped or changed in transport", failure)
	}
	staff = true
	if *result.ProjectSpec.Account.Staff.Constant {
		t.Fatal("decoded project account retained caller ownership")
	}
	encoded, err := EncodeResponse(result)
	if err != nil || !bytes.Equal(document, encoded) {
		t.Fatal("project account round trip changed canonical bytes", err)
	}
	for _, invalid := range [][]byte{
		bytes.Replace(document, []byte(`"constant":false`), []byte(`"constant":null`), 1),
		bytes.Replace(document, []byte(`"constant":false`), []byte(`"field":"staff","constant":false`), 1),
		bytes.Replace(document, []byte(`"login":"username"`), []byte(`"login":"encoded_password"`), 1),
	} {
		if _, _, rejected := ParseResponse(invalid, true); !rejected {
			t.Fatal("malformed or unbound selected account accepted")
		}
	}
	if _, err := EncodeResponse(Response{ProjectSpec: codegen.ProjectSpec{Account: spec.Account}, Failure: Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}}); err == nil {
		t.Fatal("failure response silently retained an account selection")
	}
}
