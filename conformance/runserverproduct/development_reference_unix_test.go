//go:build darwin || linux

package runserverproduct_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type developmentInputs struct {
	Kind    string
	Notes   struct{ Before, After, Summary string }
	Article struct{ Title, Group string }
	Command struct {
		SavedTitle string `json:"saved_title"`
	}
	Versions struct{ Initial, Changed, Field, Edited string }
	Cases    []struct{ Name, Owner, Comparison, Reason string }
}

type developmentObserver struct {
	inputs           developmentInputs
	owner, inputHash string
	observations     map[string]any
}

// Only shared authored inputs cross the language boundary. Actual Go results
// come from these tests' public CLI/HTTP/DB consumers; no Django output is read.
func newDevelopmentObserver(t *testing.T, owner string) *developmentObserver {
	t.Helper()
	data, err := os.ReadFile("../runners/django/development_reference/input.json")
	if err != nil {
		t.Fatal(err)
	}
	observer := &developmentObserver{owner: owner, observations: make(map[string]any)}
	if err := json.Unmarshal(data, &observer.inputs); err != nil || observer.inputs.Kind != "project-development-inputs-v1" {
		t.Fatal("invalid shared development inputs", err)
	}
	if len(observer.inputs.Cases) != 9 || observer.inputs.Notes.Before == "" || observer.inputs.Notes.After == "" ||
		observer.inputs.Notes.Summary == "" || observer.inputs.Article.Title == "" || observer.inputs.Article.Group == "" || observer.inputs.Command.SavedTitle == "" ||
		observer.inputs.Versions.Initial == "" || observer.inputs.Versions.Changed == "" || observer.inputs.Versions.Field == "" || observer.inputs.Versions.Edited == "" {
		t.Fatal("incomplete shared development inputs")
	}
	seen := make(map[string]bool)
	for _, entry := range observer.inputs.Cases {
		if seen[entry.Name] || entry.Name == "" || entry.Reason == "" ||
			(entry.Owner != "notes" && entry.Owner != "article" && entry.Owner != "command") ||
			(entry.Comparison != "shared" && entry.Comparison != "shared_boundary" && entry.Comparison != "native_difference") {
			t.Fatal("invalid development case inventory")
		}
		seen[entry.Name] = true
	}
	digest := sha256.Sum256(data)
	observer.inputHash = hex.EncodeToString(digest[:])
	return observer
}

func (observer *developmentObserver) record(t *testing.T, name string, value any) {
	t.Helper()
	if _, duplicate := observer.observations[name]; duplicate {
		t.Fatal("duplicate development observation", name)
	}
	for _, entry := range observer.inputs.Cases {
		if entry.Name == name && entry.Owner == observer.owner {
			observer.observations[name] = value
			return
		}
	}
	t.Fatal("development observation has the wrong owner", name)
}

func (observer *developmentObserver) publish(t *testing.T) {
	t.Helper()
	var cases []map[string]any
	for _, entry := range observer.inputs.Cases {
		if entry.Owner != observer.owner {
			continue
		}
		value, found := observer.observations[entry.Name]
		if !found {
			t.Fatal("missing actual development observation", entry.Name)
		}
		cases = append(cases, map[string]any{"name": entry.Name, "owner": entry.Owner, "comparison": entry.Comparison, "reason": entry.Reason, "observed": value})
	}
	if len(cases) == 0 || len(cases) != len(observer.observations) {
		t.Fatal("incomplete development owner")
	}
	data, err := json.Marshal(map[string]any{"kind": "godj-development-observations-v1", "owner": observer.owner, "input_sha256": observer.inputHash, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("GODJ_DEVELOPMENT_OBSERVATION " + string(data))
}

func observedNote(t *testing.T, state reloadNotesState, title string) map[string]any {
	t.Helper()
	var matches []reloadNote
	for _, row := range state.Notes {
		if row.Title == title {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		t.Fatal("observed Note is missing or duplicated")
	}
	return map[string]any{"title": matches[0].Title, "summary": matches[0].Summary}
}
