package accountstoretest

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
)

type catalogExpectedEvent struct {
	model  string
	id     int64
	action admin.Action
	fields []string
}

// Bind newly allocated through keys to the exact independently requested
// source/target pairs. Existing link IDs remain part of the full comparison.
func catalogExpectedGrantAdditions(t *testing.T, before, after []models.GroupPermissionsLink, group int64, targets []int64) []models.GroupPermissionsLink {
	t.Helper()
	known, pending := make(map[int64]bool), make(map[int64]bool)
	for _, row := range before {
		known[row.ID] = true
	}
	for _, id := range targets {
		if pending[id] {
			t.Fatal("duplicate expected catalog grant")
		}
		pending[id] = true
	}
	expected := slices.Clone(before)
	for _, row := range after {
		if known[row.ID] {
			continue
		}
		if row.ID <= 0 || row.SourceID != group || !pending[row.TargetID] {
			t.Fatal("catalog mutation inserted an unexpected grant")
		}
		delete(pending, row.TargetID)
		expected = append(expected, row)
	}
	if len(pending) != 0 {
		t.Fatal("catalog mutation omitted requested grants")
	}
	slices.SortFunc(expected, func(a, b models.GroupPermissionsLink) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return expected
}

// Expected rows come from the independent before-state and fixture intent.
// Only new audit keys are generated; their complete semantic values and the
// unchanged historical prefix are checked before comparing every other row.
func assertCatalogState(t *testing.T, backend Backend, expected accountNativeState, actor auth.Principal, events ...catalogExpectedEvent) {
	t.Helper()
	actual := accountNativeSnapshot(t, backend)
	old := len(expected.audits)
	if len(actual.audits) != old+len(events) || old > 0 && !reflect.DeepEqual(expected.audits, actual.audits[:old]) {
		t.Fatal("catalog mutation changed historical audit or added an unexpected event")
	}
	domain, bound := actor.CredentialDomain()
	if !bound {
		t.Fatal("catalog observation lost its expected actor domain")
	}
	var last int64
	if old != 0 {
		last = expected.audits[old-1].ID
	}
	for index, want := range events {
		event := actual.audits[old+index]
		var fields []string
		if err := json.Unmarshal([]byte(event.ChangedFields), &fields); err != nil {
			t.Fatal(err)
		}
		if event.ID <= last || event.Domain != domain.Identifier() || event.ActorID != actor.ID() || event.Model != "godj_identity."+want.model ||
			event.ObjectID != want.id || event.Action != string(want.action) || !slices.Equal(fields, want.fields) || !event.RecordedAt.Equal(joined) {
			t.Fatal("catalog audit differs from the exact authorized effect")
		}
		last = event.ID
	}
	actual.audits = expected.audits
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("catalog mutation changed unexpected account/catalog/through/session/domain state")
	}
}
