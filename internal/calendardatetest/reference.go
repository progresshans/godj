// Package calendardatetest loads independent Django/DRF date observations.
package calendardatetest

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
		Cleaned  map[string]*string
		Errors   map[string][]string
		Changed  map[string]bool
		Widget   string
	}
	Serializer []struct {
		Serializer     string
		Partial, Valid bool
		Input          map[string]struct {
			Type  string
			Value json.RawMessage
		}
		Validated map[string]*string
		Errors    map[string][]string
	}
	Database struct {
		AfterAdd           [][]any `json:"after_add"`
		Reopened           [][]any
		Queries, Relations map[string][]string
		Aggregates         map[string]string
		AfterUpdate        [][]any  `json:"after_update"`
		AfterRemove        []string `json:"after_remove"`
	}
}

func Load(t testing.TB) Reference {
	t.Helper()
	var result Reference
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Django != "6.1" || result.DRF != "3.18.0" || result.Python != "3.14.3" || len(result.Form) != 120 || len(result.Serializer) != 272 || len(result.Database.Queries) != 9 || len(result.Database.Relations) != 9 {
		t.Fatal("calendar date reference version or roster is incomplete")
	}
	return result
}
