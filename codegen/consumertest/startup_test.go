package codegen_test

import (
	"bytes"
	_ "embed"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/startup/consumer_test.go
var startupConsumerSource []byte

//go:embed testdata/startup/server_test.go
var startupServerConsumerSource []byte

//go:embed testdata/startup/command_test.go
var startupCommandConsumerSource []byte

func TestGeneratedAppsOwnOrderedStartupAndCleanup(t *testing.T) {
	root := writeStartupConsumerModule(t)
	output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-v", "./...").CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("--- PASS: TestInstalledAppsOwnLiveResourcesAndPreserveData")) ||
		!bytes.Contains(output, []byte("--- PASS: TestProjectServerOwnsGeneratedAppsThroughRequestDrain")) ||
		!bytes.Contains(output, []byte("--- PASS: TestGeneratedCommandCommitSurvivesEveryLoggerFailure")) {
		t.Fatalf("external app startup consumer did not execute: %v\n%s", err, output)
	}
	t.Logf("external app startup and HTTP lifetime execution:\n%s", output)
}

func writeStartupConsumerModule(t *testing.T) string {
	t.Helper()
	const module = "example.com/godj-project-bundle/"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "notes", Package: codegen.PackageSpec{PackageName: "notes", ImportPath: module + "notes", Directory: "notes"},
				Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "notes", Models: []ir.Model{{Name: "note", GoName: "Note", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 80}}}}}},
			{Alias: "storage", Package: codegen.PackageSpec{PackageName: "storage", ImportPath: module + "storage", Directory: "storage"},
				Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "_storage"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	writeGeneratedTestFile(t, root, "consumer/startup_test.go", startupConsumerSource)
	writeGeneratedTestFile(t, root, "consumer/server_test.go", startupServerConsumerSource)
	writeGeneratedTestFile(t, root, "consumer/command_test.go", startupCommandConsumerSource)
	return root
}
