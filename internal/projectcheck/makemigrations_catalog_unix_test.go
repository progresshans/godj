//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureMakemigrationsFilesystemCatalogIsCanonicalAndFresh(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	root := filepath.Join(fixture.project, "migrations")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	alpha := []byte("alpha")
	zeta := []byte("zeta")
	if err := os.WriteFile(filepath.Join(root, "z.godj.json"), zeta, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.godj.json"), alpha, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	selected := selectMakemigrationsCatalogProject(t, fixture.project)
	defer func() { _ = selected.close() }()
	first, err := captureMakemigrationsFilesystemCatalog(context.Background(), selected, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].SourceID != "migrations/a.godj.json" || first[1].SourceID != "migrations/z.godj.json" || !bytes.Equal(first[0].Document, alpha) || !bytes.Equal(first[1].Document, zeta) {
		t.Fatalf("catalog = %#v", first)
	}

	changed := []byte("ALPHA")
	if err := os.WriteFile(filepath.Join(root, "a.godj.json"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := captureMakemigrationsFilesystemCatalog(context.Background(), selected, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second[0].Document, changed) || bytes.Equal(first[0].Document, second[0].Document) {
		t.Fatalf("fresh catalog did not observe in-place bytes: first=%q second=%q", first[0].Document, second[0].Document)
	}
	second[0].Document[0] = 'x'
	if bytes.Equal(first[0].Document, second[0].Document) {
		t.Fatal("separate captures alias document memory")
	}
}

func TestCaptureMakemigrationsFilesystemCatalogRejectsUnsafeAuthority(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	realRoot := filepath.Join(fixture.project, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(fixture.project, "migrations")); err != nil {
		t.Fatal(err)
	}
	selected := selectMakemigrationsCatalogProject(t, fixture.project)
	defer func() { _ = selected.close() }()
	if _, err := captureMakemigrationsFilesystemCatalog(context.Background(), selected, "migrations"); err == nil {
		t.Fatal("symlink writer root was accepted")
	}

	if err := os.Remove(filepath.Join(fixture.project, "migrations")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fixture.project, "migrations"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(fixture.project, "target.godj.json")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../target.godj.json", filepath.Join(fixture.project, "migrations", "source.godj.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := captureMakemigrationsFilesystemCatalog(context.Background(), selected, "migrations"); err == nil {
		t.Fatal("symlink definition source was accepted")
	}
}

func TestCaptureMakemigrationsFilesystemCatalogPreflightAndCancellation(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	if err := os.Mkdir(filepath.Join(fixture.project, "migrations"), 0o700); err != nil {
		t.Fatal(err)
	}
	selected := selectMakemigrationsCatalogProject(t, fixture.project)
	defer func() { _ = selected.close() }()

	for _, root := range []string{"", "/absolute", "a/../b", "a\\b", "a//b"} {
		if _, err := captureMakemigrationsFilesystemCatalog(context.Background(), selected, root); err == nil {
			t.Errorf("invalid root %q was accepted", root)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := captureMakemigrationsFilesystemCatalog(canceled, selected, "migrations"); err == nil {
		t.Fatal("canceled capture succeeded")
	}
}

func selectMakemigrationsCatalogProject(t *testing.T, root string) retainedProject {
	t.Helper()
	report := Report{}
	selected, failure := selectProject(root, commandArguments{}, &report)
	if failure != nil {
		t.Fatalf("selectProject() failure = %+v", failure)
	}
	return selected
}
