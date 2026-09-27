// Package iexacttest compares native consumers with independently observed
// Django 6.1 results. Inputs contain no hand-authored expected query results.
package iexacttest

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

type queryCase struct {
	Name, Model, Field, Value, Mode string
}

type CreationCase struct {
	Stored, Candidate string
}

type CreationResult struct {
	Stored   string
	Accepted bool
	Username *string
	Codes    []string
}

type inputs struct {
	Rows []struct {
		ID        int64
		Title     string
		Summary   *string
		Published bool
	}
	Posts []struct {
		ID       int64
		Title    string
		Author   int64
		Reviewer *int64
	}
	Queries  []queryCase
	Creation []CreationCase
}

type observation struct {
	Name   string
	IDs    []int64
	Count  int64
	Exists bool
}

type reference struct {
	Django, Python, Backend string
	DatabaseVersion         string `json:"database_version"`
	InputSHA256             string `json:"input_sha256"`
	Collation               []string
	Queries                 []observation
	Creation                []CreationResult
}

func load(t *testing.T, dialect string) (inputs, reference) {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var input inputs
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	encoded, err := fixtures.ReadFile("testdata/django61-" + dialect + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var want reference
	if err := json.Unmarshal(encoded, &want); err != nil {
		t.Fatal(err)
	}
	backend := map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[dialect]
	if backend == "" || want.Backend != backend || want.Django != "6.1" || want.Python != "3.14.3" || want.InputSHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatal("reference version, backend or input binding changed")
	}
	if dialect == "postgres" && (want.DatabaseVersion != "170010" || !reflect.DeepEqual(want.Collation, []string{"C", "C", "c"})) {
		t.Fatal("reference PostgreSQL profile changed")
	}
	if dialect == "sqlite" && (want.DatabaseVersion != "3.50.4" || len(want.Collation) != 0) {
		t.Fatal("reference SQLite profile changed")
	}
	if len(input.Rows) != 32 || len(input.Posts) != 10 || len(input.Queries) != 233 || len(want.Queries) != 233 || len(input.Creation) != 11 || len(want.Creation) != 11 {
		t.Fatal("incomplete reference roster")
	}
	seen := map[string]bool{}
	for index, c := range input.Queries {
		if c.Name == "" || seen[c.Name] || c.Name != want.Queries[index].Name {
			t.Fatal("query roster mismatch", c.Name)
		}
		seen[c.Name] = true
	}
	return input, want
}

func CreationReference(t *testing.T, dialect string) ([]CreationCase, []CreationResult) {
	t.Helper()
	input, want := load(t, dialect)
	return input.Creation, want.Creation
}
