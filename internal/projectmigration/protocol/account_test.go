package protocol

import (
	"context"
	"testing"

	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema/ir"
)

func TestProjectAccountPolicyOwnsSourceDigestWithoutRewritingSchema(t *testing.T) {
	input, err := modeldef.ProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	staff := false
	input.Account.Staff = ir.AccountBoolean{Constant: &staff}
	beforeSchema, err := ir.Hash(input.Apps[0].Schema)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ProjectSpecDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeProjectSpec(input)
	if err != nil || normalized.Account == nil || *normalized.Account.Staff.Constant {
		t.Fatal("migration source lost selected account policy", err)
	}
	staff = true
	if *normalized.Account.Staff.Constant {
		t.Fatal("normalized migration source aliases account policy")
	}
	changed, err := ProjectSpecDigest(input)
	if err != nil || changed == before {
		t.Fatal("changed account authority retained old source digest", err)
	}
	preserved, err := ProjectSpecDigest(normalized)
	if err != nil || preserved != before {
		t.Fatal("normalized snapshot changed after caller mutation", err)
	}
	afterSchema, err := ir.Hash(input.Apps[0].Schema)
	if err != nil || afterSchema != beforeSchema {
		t.Fatal("account policy implicitly rewrote physical model declarations", err)
	}
	input.Account = nil
	unselected, err := ProjectSpecDigest(input)
	if err != nil || unselected == before || unselected == changed {
		t.Fatal("absent and selected account source identities collided", err)
	}
}
