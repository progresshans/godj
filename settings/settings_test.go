package settings_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
)

func TestSettingsSnapshot(t *testing.T) {
	definition := settings.Definition{
		ProjectName: " article_site ",
		InstalledApps: []apps.Config{{
			Name:  "example.com/article",
			Label: "articles",
		}},
	}
	configured, err := settings.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.InstalledApps[0].Label = "mutated"
	if got := configured.ProjectName(); got != "article_site" {
		t.Fatalf("ProjectName() = %q", got)
	}
	if app, ok := configured.Apps().Lookup("articles"); !ok || app.Name != "example.com/article" {
		t.Fatalf("Apps().Lookup() = %#v, %t", app, ok)
	}
}

func TestSettingsRejectsInvalidDefinition(t *testing.T) {
	if _, err := settings.New(settings.Definition{}); !errors.Is(err, &settings.Error{Field: "project_name"}) {
		t.Fatalf("empty project error = %v", err)
	}
	_, err := settings.New(settings.Definition{
		ProjectName:   "article_site",
		InstalledApps: []apps.Config{{Name: "article", Label: "not-valid"}},
	})
	var settingsErr *settings.Error
	if !errors.As(err, &settingsErr) || settingsErr.Field != "installed_apps" {
		t.Fatalf("invalid app error = %v", err)
	}
}

func TestSettingsStorageRegistryHasNoImplicitDefaultOrGlobalLifetime(t *testing.T) {
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	stores, err := storage.NewRegistry(storage.Registration{Alias: storage.DefaultAlias, Backend: root})
	if err != nil {
		t.Fatal(err)
	}
	definition := settings.Definition{ProjectName: "private-project", Storages: stores}
	configured, err := settings.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Storages = storage.Registry{}
	if backend, err := configured.Storages().Default(); err != nil || backend != root {
		t.Fatal("settings lost explicit registry", err)
	}
	empty, err := settings.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Storages().Default(); !errors.Is(err, &storage.Error{Code: "unknown_alias"}) {
		t.Fatal("settings supplied a global default", err)
	}
	if _, err := configured.Storages().URL(t.Context(), storage.DefaultAlias, "file.txt"); !errors.Is(err, &storage.Error{Code: "url_unavailable"}) {
		t.Fatal("settings implicitly published storage URL", err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, configured), "private-project") {
			t.Fatal("settings formatting exposed configuration")
		}
	}
}
