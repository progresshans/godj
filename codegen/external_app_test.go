package codegen

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestExternalAppSeparatesFileOwnershipFromProjectRelations(t *testing.T) {
	spec := projectBundleTestSpec()
	spec.Apps[0].External = true
	spec.Apps[0].Package.Directory = ""
	bundle, err := GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range bundle.Files() {
		if file.Owner == "app:"+spec.Apps[0].Schema.AppLabel {
			t.Fatal("host attempted to own external app files")
		}
	}
	var bridge []byte
	for _, file := range bundle.Files() {
		if strings.HasSuffix(file.Path, "/zz_godj_bindings.go") {
			bridge = file.Source()
		}
	}
	prepared, err := prepareSchema(spec.Apps[0].Schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range AppSnapshotMarkers(prepared.hash) {
		if !bytes.Contains(bridge, []byte(spec.Apps[0].Alias+"."+marker)) {
			t.Fatal("external companion is not required by host compilation")
		}
	}
	if bytes.Contains(bridge, []byte(spec.Apps[0].Alias+".GoDjProjectSnapshot_")) {
		t.Fatal("external app is tied to host project snapshot")
	}
	if !bytes.Contains(bridge, []byte(spec.Apps[1].Alias+".GoDjProjectSnapshot_")) {
		t.Fatal("owned app lost whole-project seal")
	}
	before := spec.Apps[0].Schema.Clone()
	permuted := spec
	permuted.Apps = append([]AppSpec(nil), spec.Apps...)
	permuted.Apps[0], permuted.Apps[1] = permuted.Apps[1], permuted.Apps[0]
	again, err := GenerateProject(permuted)
	if err != nil || !bytes.Equal(bundle.Manifest(), again.Manifest()) {
		t.Fatal("external app ordering changed publication identity", err)
	}
	if !reflect.DeepEqual(before, spec.Apps[0].Schema) {
		t.Fatal("generation modified library declaration")
	}
}

func TestExternalAppRejectsAHostDirectoryAndOwnedAppRequiresOne(t *testing.T) {
	for _, directory := range []string{".", "models", "../dependency", "/tmp/dependency"} {
		spec := projectBundleTestSpec()
		spec.Apps[0].External = true
		spec.Apps[0].Package.Directory = directory
		bundle, err := GenerateProject(spec)
		if err == nil || len(bundle.Files()) != 0 || len(bundle.Manifest()) != 0 {
			t.Fatalf("external app claimed directory %q", directory)
		}
	}
	spec := projectBundleTestSpec()
	spec.Apps[0].Package.Directory = ""
	if _, err := GenerateProject(spec); err == nil {
		t.Fatal("owned app accepted absent directory")
	}
}

func TestAppCompanionSealsAreIndependentOfHostAndTrackSchema(t *testing.T) {
	spec := projectBundleTestSpec()
	app := spec.Apps[0]
	prepared, err := prepareSchema(app.Schema)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderAppSources(app.Package.PackageName, prepared, appProjection)
	if err != nil {
		t.Fatal(err)
	}
	markers := AppSnapshotMarkers(prepared.hash)
	for index, file := range files {
		if !bytes.Contains(file.source, []byte("type "+markers[index]+" struct{}")) {
			t.Fatal("companion does not declare its intrinsic seal")
		}
	}
	changed := app.Schema.Clone()
	changed.Models[0].DBTable += "_renamed"
	reprepared, err := prepareSchema(changed)
	if err != nil {
		t.Fatal(err)
	}
	if AppSnapshotMarkers(reprepared.hash) == markers {
		t.Fatal("app seal ignored model semantics")
	}
}
