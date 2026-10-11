package auth_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestRequiredPermissionsOwnsBoundedDistinctSnapshot(t *testing.T) {
	extra := []auth.Permission{"links.ticket", "links.label"}
	snapshot, err := auth.RequiredPermissions("links.add", extra...)
	if err != nil {
		t.Fatal(err)
	}
	extra[0] = "links.changed"
	if !slices.Equal(snapshot, []auth.Permission{"links.add", "links.ticket", "links.label"}) {
		t.Fatal("requirements retained caller slice")
	}
	for _, input := range [][]auth.Permission{{""}, {"Links.View"}, {"links.view", ""}, {"links.view", "links.view"}, {"links.view", "links.label", "links.label"}} {
		if got, err := auth.RequiredPermissions(input[0], input[1:]...); err == nil || got != nil {
			t.Fatal("invalid/duplicate requirements accepted", input)
		}
	}
	limit := make([]auth.Permission, 256)
	for i := range limit {
		limit[i] = auth.Permission(fmt.Sprintf("links.permission%d", i))
	}
	if got, err := auth.RequiredPermissions(limit[0], limit[1:]...); err != nil || !slices.Equal(got, limit) {
		t.Fatal("maximum supported requirements", err)
	}
	if got, err := auth.RequiredPermissions("links.extra", limit...); err == nil || got != nil {
		t.Fatal("unbounded requirements accepted")
	}
}
