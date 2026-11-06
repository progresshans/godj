package codegen

import "testing"

func TestGeneratedBundleAccessorsReturnCallerOwnedViews(t *testing.T) {
	bundle := GeneratedBundle{
		snapshotSHA256: "snapshot",
		files: []GeneratedFile{{
			Path:   "app/zz_godj_generated.go",
			SHA256: "source",
			source: []byte("package app\n"),
		}},
		manifest: []byte("{}\n"),
	}

	files := bundle.Files()
	files[0] = GeneratedFile{Path: "changed"}
	if got := bundle.Files()[0].Path; got != "app/zz_godj_generated.go" {
		t.Fatalf("bundle file inventory changed through returned slice: %q", got)
	}

	source := bundle.Files()[0].Source()
	source[0] = 'X'
	if got := string(bundle.Files()[0].Source()); got != "package app\n" {
		t.Fatalf("bundle source changed through returned bytes: %q", got)
	}

	manifest := bundle.Manifest()
	manifest[0] = '['
	if got := string(bundle.Manifest()); got != "{}\n" {
		t.Fatalf("bundle manifest changed through returned bytes: %q", got)
	}
}
