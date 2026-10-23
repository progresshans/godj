package codegen_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema/ir"
)

func TestAccountSelectionOwnsGeneratedBindingAndProjectSnapshot(t *testing.T) {
	spec, err := modeldef.ProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	staff := false
	role.Staff = ir.AccountBoolean{Constant: &staff}
	spec.Account = &role
	first, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	var source []byte
	for _, file := range first.Files() {
		if file.Path == "project/zz_godj_account.go" {
			source = file.Source()
		}
	}
	for _, required := range []string{
		"func BindAccount() (_godjaccountmodel.Binding[identity.User], error)",
		"identity.UserDescriptor{}", "identity.GoDjRelationSchema()", "Constant: &_staff", "_staff := false",
		"func NewAccountCredentialStore(", "func NewAccountAuthenticator(",
		"identity.GroupDescriptor{}", "identity.PermissionDescriptor{}",
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("generated account binding is missing %q", required)
		}
	}
	staff = true
	second, err := codegen.GenerateProject(spec)
	if err != nil || first.SnapshotSHA256() == second.SnapshotSHA256() {
		t.Fatal("account policy change did not invalidate generated snapshot", err)
	}
	for _, file := range first.Files() {
		if file.Path == "project/zz_godj_account.go" && !bytes.Equal(source, file.Source()) {
			t.Fatal("declaration mutation changed a published candidate")
		}
	}
	spec.Account = nil
	unselected, err := codegen.GenerateProject(spec)
	if err != nil || unselected.SnapshotSHA256() == first.SnapshotSHA256() {
		t.Fatal("unselected and selected account snapshots collided", err)
	}
	for _, file := range unselected.Files() {
		if file.Path == "project/zz_godj_account.go" && bytes.Contains(file.Source(), []byte("func BindAccount")) {
			t.Fatal("installing an app implicitly selected its account")
		}
	}
	role.Login = "foreign"
	spec.Account = &role
	rejected, err := codegen.GenerateProject(spec)
	if err == nil || rejected.SnapshotSHA256() != "" || len(rejected.Files()) != 0 || len(rejected.Manifest()) != 0 {
		t.Fatal("invalid role published a partial generated candidate", err)
	}
}
