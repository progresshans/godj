package definitionhandoff

import (
	"strings"
	"testing"
	"time"
)

func TestHandoffResourceLimitsPrecedeCloningAndSemanticValidation(t *testing.T) {
	t.Parallel()

	tooMany := make([]Record, MaxDefinitions+1)
	if _, err := New(tooMany); err == nil || !strings.Contains(err.Error(), "definition_count") {
		t.Fatalf("New(over-limit records) error = %v", err)
	}

	valid := relationRecord()
	handoff, err := New([]Record{valid})
	if err != nil {
		t.Fatalf("New(valid): %v", err)
	}
	visible := make([]Definition, MaxDefinitions+1)
	if err := handoff.ValidateVisible(visible); err == nil || !strings.Contains(err.Error(), "definition_count") {
		t.Fatalf("ValidateVisible(over-limit) error = %v", err)
	}

	oversized := valid
	oversized.SourceID = strings.Repeat("s", MaxSourceIDBytes+1)
	if _, err := New([]Record{oversized}); err == nil || !strings.Contains(err.Error(), "source_id_bytes") {
		t.Fatalf("New(oversized source ID) error = %v", err)
	}

	longSemantic := valid
	longSemantic.Definition.App = strings.Repeat("a", MaxSourceIDBytes+1)
	longSemantic.Definition.Operations[0].AppLabel = longSemantic.Definition.App
	if _, err := New([]Record{longSemantic}); err != nil {
		t.Fatalf("New(loader-valid long semantic identifier): %v", err)
	}
}

func TestHandoffResourceLimitsAcceptExactStructuralMaximum(t *testing.T) {
	t.Parallel()

	definitions := make([]Definition, MaxDefinitions)
	for index := range definitions {
		definitions[index] = Definition{App: "a", Name: "m"}
	}
	// The exact count ceiling must pass its own guard. The semantic count
	// mismatch against a one-record carrier then proves validation continued.
	handoff, err := New([]Record{relationRecord()})
	if err != nil {
		t.Fatalf("New(valid): %v", err)
	}
	err = handoff.ValidateVisible(definitions)
	if err == nil || strings.Contains(err.Error(), "resource limit") || !strings.Contains(err.Error(), "definition count") {
		t.Fatalf("ValidateVisible(exact count maximum) error = %v", err)
	}
}

func TestHandoffAggregateNodeExhaustionStopsSharedAliasTraversal(t *testing.T) {
	fields := make([]Field, MaxFieldsPerCreateModel)
	operations := make([]Operation, MaxOperations)
	for index := range operations {
		operations[index] = Operation{Kind: "create_model", HasModel: true, Model: Model{Fields: fields}}
	}
	definitions := make([]Definition, MaxDefinitions)
	for index := range definitions {
		definitions[index] = Definition{App: "a", Name: "m", Operations: operations}
	}

	done := make(chan error, 1)
	go func() { done <- ValidateDefinitionResources(definitions) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "aggregate_nodes") {
			t.Fatalf("shared-alias aggregate scan error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shared-alias aggregate scan did not stop after node exhaustion")
	}
}
