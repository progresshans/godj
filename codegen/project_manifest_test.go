package codegen

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProjectManifestCanonicalSchemaAndFinalLF(t *testing.T) {
	bundle, err := GenerateProject(projectBundleTestSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	manifest := bundle.Manifest()
	if len(manifest) == 0 || manifest[len(manifest)-1] != '\n' || bytes.Count(manifest, []byte{'\n'}) != 1 {
		t.Fatalf("manifest is not compact canonical JSON with one final LF: %q", manifest)
	}
	var document projectManifestDocument
	if err := json.Unmarshal(manifest, &document); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if document.FormatVersion != ProjectBundleFormatVersion {
		t.Fatalf("format_version = %d, want %d", document.FormatVersion, ProjectBundleFormatVersion)
	}
	if document.SnapshotSHA256 != bundle.SnapshotSHA256() {
		t.Fatalf("snapshot_sha256 = %q, want %q", document.SnapshotSHA256, bundle.SnapshotSHA256())
	}
	if len(document.GeneratorABI) != 13 {
		t.Fatalf("len(generator_abi) = %d, want 13", len(document.GeneratorABI))
	}
	wantRoles := []string{
		"bundle",
		"app.main",
		"app.relation_metadata",
		"app.relation_object",
		"app.relation_projection",
		"project.bindings",
		"project.relation_query",
		"project.relation_object",
		"project.relation_reverse",
		"project.relation_prefetch",
		"project.relation_select_related",
		"project.relation_delete",
		"project.relation_facade",
	}
	for index, role := range wantRoles {
		if document.GeneratorABI[index].Role != role || document.GeneratorABI[index].Filename == "" ||
			document.GeneratorABI[index].Version == "" {
			t.Fatalf("generator_abi[%d] = %#v, want role %q with filename/version", index, document.GeneratorABI[index], role)
		}
	}
	if len(document.Apps) != 2 || document.Apps[0].AppLabel != "authors" || document.Apps[1].AppLabel != "blog" {
		t.Fatalf("manifest apps are not canonical: %#v", document.Apps)
	}
	files := bundle.Files()
	if len(document.Files) != len(files) {
		t.Fatalf("manifest files = %d, bundle files = %d", len(document.Files), len(files))
	}
	for index, file := range document.Files {
		if file.Path != files[index].Path || file.Owner != files[index].Owner || file.Mode != "0644" ||
			file.SHA256 != files[index].SHA256 {
			t.Fatalf("manifest file %d = %#v, bundle = %#v", index, file, files[index])
		}
	}
	reencoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("re-encode manifest: %v", err)
	}
	reencoded = append(reencoded, '\n')
	if !bytes.Equal(reencoded, manifest) {
		t.Fatal("manifest is not the canonical encoding of its frozen schema")
	}
}

func TestProjectManifestAccessorsExcludeCommitMarkerAndUseStableOwners(t *testing.T) {
	bundle, err := GenerateProject(projectBundleTestSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	owners := map[string]int{}
	for _, file := range bundle.Files() {
		owners[file.Owner]++
		if file.Path == GeneratedManifestPath {
			t.Fatal("manifest appears in GeneratedBundle.Files")
		}
	}
	if owners["app:authors"] != 4 || owners["app:blog"] != 4 || owners["project"] != 8 || len(owners) != 3 {
		t.Fatalf("owner counts = %#v, want app 4+4 and project 8", owners)
	}
}

func TestProjectManifestSizeLimitUsesExactBoundary(t *testing.T) {
	if err := validateProjectManifestSize(maxProjectManifestBytes); err != nil {
		t.Fatalf("maximum manifest size rejected: %v", err)
	}
	if err := validateProjectManifestSize(maxProjectManifestBytes + 1); err == nil {
		t.Fatal("manifest above maximum size was accepted")
	}
}
