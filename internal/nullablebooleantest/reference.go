// Package nullablebooleantest shares independent Django/DRF observations with
// product consumers. The reference runner does not read GoDj implementations.
package nullablebooleantest

import (
	_ "embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/django61.json
var raw []byte

type Reference struct {
	Django, DRF, Python string
	Form                []struct {
		Required bool
		Input    map[string]string
		Valid    bool
		Cleaned  map[string]*bool
		Errors   map[string][]string
		Changed  map[string]bool
	}
	Database struct {
		AfterAdd    [][]any `json:"after_add"`
		Reopened    [][]any
		Queries     map[string][]string
		Relations   map[string][]string
		AfterUpdate [][]any  `json:"after_update"`
		AfterRemove []string `json:"after_remove"`
	}
}

func Load(t testing.TB) Reference {
	t.Helper()
	var reference Reference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Python != "3.14.3" || len(reference.Form) != 30 || len(reference.Database.Queries) != 6 || len(reference.Database.Relations) != 6 {
		t.Fatal("nullable Boolean reference version or roster is incomplete")
	}
	return reference
}
