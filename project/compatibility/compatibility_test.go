package compatibility

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/version"
)

func testInfo() Info {
	info := Current()
	info.Framework = version.Info{Module: version.Module, Origin: version.Development}
	return info
}

func TestCompatibilityChecksEveryContractAndEffectiveVersion(t *testing.T) {
	tool := testInfo()
	if err := Check(tool, tool); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Info){
		func(i *Info) { i.Contract.Schema++ }, func(i *Info) { i.Contract.Bundle++ }, func(i *Info) {
			i.Contract.Generator = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		func(i *Info) { i.Contract.MigrationCheck++ }, func(i *Info) { i.Contract.Generate++ }, func(i *Info) { i.Contract.MakeMigrations++ }, func(i *Info) { i.Contract.Migrate++ }, func(i *Info) { i.Contract.ShowMigrations++ }, func(i *Info) { i.Contract.SQLMigrate++ }, func(i *Info) { i.Contract.CreateSuperuser++ }, func(i *Info) { i.Contract.Commands++ },
		func(i *Info) { i.Framework.Origin = version.Unknown }, func(i *Info) { i.Framework.GoVersion = "/private/path" }, func(i *Info) { i.FormatVersion++ },
	} {
		linked := tool
		mutate(&linked)
		if Check(tool, linked) == nil {
			t.Fatal("mismatch accepted", linked.Contract)
		}
	}
	linked := tool
	linked.Framework.Origin = version.LocalReplacement
	linked.Framework.RequestedVersion = "v0.0.0"
	if Check(tool, linked) != nil {
		t.Fatal("explicit development contracts differ")
	}
	linked.Framework = version.Info{Module: version.Module, Origin: version.ModuleVersion, Version: "v0.1.0"}
	if Check(tool, linked) == nil {
		t.Fatal("mixed development and fixed versions accepted")
	}
	tool.Framework = linked.Framework
	if Check(tool, linked) != nil {
		t.Fatal("same fixed dependency rejected")
	}
	linked.Framework.Version = "v0.1.1"
	if Check(tool, linked) == nil {
		t.Fatal("different fixed dependency accepted")
	}
}

func TestCompatibilityProbeRejectsMalformedOrAmbiguousDocuments(t *testing.T) {
	document, err := json.Marshal(testInfo())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(document); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{
		append(append([]byte(nil), document...), document...),
		bytes.Replace(document, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1),
		bytes.Replace(document, []byte(`"format_version":1`), []byte(`"format_version":1,"private":"secret"`), 1),
		bytes.Replace(document, []byte(`"format_version":1`), []byte(`"format_version":null`), 1),
		bytes.Repeat([]byte("x"), MaxBytes+1), []byte("null"),
	} {
		if _, err := Parse(input); err == nil {
			t.Fatal("malformed response accepted")
		}
	}
	var output bytes.Buffer
	if handled, err := Serve(t.Context(), []string{"other"}, &output); handled || err != nil || output.Len() != 0 {
		t.Fatal("probe consumed another command")
	}
}
