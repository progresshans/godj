package apps_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/apps"
)

func TestRegistrySnapshotsOrderAndLookup(t *testing.T) {
	definitions := []apps.Config{
		{Name: "example.com/project/articles", Label: "articles"},
		{Name: "example.com/project/accounts", Label: "accounts"},
	}
	registry, err := apps.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Label = "mutated"

	got := registry.All()
	want := []apps.Config{
		{Name: "example.com/project/articles", Label: "articles"},
		{Name: "example.com/project/accounts", Label: "accounts"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("All() = %#v, want %#v", got, want)
	}
	got[0].Label = "also_mutated"
	if app, ok := registry.Lookup("articles"); !ok || app != want[0] {
		t.Fatalf("Lookup(articles) = %#v, %t", app, ok)
	}
	if _, ok := registry.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) unexpectedly succeeded")
	}
}

func TestRegistryRejectsInvalidAndDuplicateDefinitions(t *testing.T) {
	tests := []struct {
		name  string
		input []apps.Config
		code  apps.ErrorCode
	}{
		{name: "empty name", input: []apps.Config{{Label: "articles"}}, code: apps.CodeInvalidConfig},
		{name: "invalid label", input: []apps.Config{{Name: "articles", Label: "article-posts"}}, code: apps.CodeInvalidConfig},
		{name: "duplicate name", input: []apps.Config{{Name: "articles", Label: "articles"}, {Name: "articles", Label: "other"}}, code: apps.CodeDuplicateName},
		{name: "duplicate label", input: []apps.Config{{Name: "articles", Label: "articles"}, {Name: "other", Label: "articles"}}, code: apps.CodeDuplicateLabel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := apps.New(test.input)
			if !errors.Is(err, &apps.Error{Code: test.code}) {
				t.Fatalf("New() error = %v, want code %q", err, test.code)
			}
		})
	}
}

func TestEmptyRegistryIsUsable(t *testing.T) {
	registry, err := apps.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.All(); len(got) != 0 {
		t.Fatalf("All() length = %d, want 0", len(got))
	}
}
