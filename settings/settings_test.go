package settings_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/settings"
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
