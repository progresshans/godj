package godj

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

var gdj0047ExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
	dbState  bool
}{
	{id: "AUT-009", scenario: "godj.api_authentication.common_authentication_boundary", phase: protocol.PhaseConstruction},
	{id: "AUT-010", scenario: "godj.api_authentication.bounded_bearer_header", phase: protocol.PhaseEvaluation},
	{id: "AUT-011", scenario: "drf.api_authentication.missing_and_unsupported", phase: protocol.PhaseEvaluation},
	{id: "AUT-012", scenario: "drf.api_authentication.invalid_and_valid_token", phase: protocol.PhaseEvaluation},
	{id: "AUT-013", scenario: "drf.api_authentication.permission_denial", phase: protocol.PhaseEvaluation, dbState: true},
	{id: "AUT-014", scenario: "drf.api_authentication.unsafe_without_csrf", phase: protocol.PhaseCommit, dbState: true},
	{id: "AUT-015", scenario: "drf.api_authentication.profile_isolation", phase: protocol.PhaseEvaluation, dbState: true},
	{id: "AUT-016", scenario: "godj.api_authentication.secret_and_failure_boundary", phase: protocol.PhaseEvaluation},
	{id: "API-011", scenario: "godj.api_authentication.article_route_reuse", phase: protocol.PhaseCommit, dbState: true},
	{id: "API-012", scenario: "godj.api_authentication.denial_mutation_boundary", phase: protocol.PhaseEvaluation, dbState: true},
}

func TestGDJ0047APIAuthenticationHandlersObserveRealProfilesAndSQLite(t *testing.T) {
	if len(gdj0047ScenarioRegistry) != len(gdj0047ExpectedRegistrations) {
		t.Fatalf("GDJ-0047 registry size = %d, want %d", len(gdj0047ScenarioRegistry), len(gdj0047ExpectedRegistrations))
	}
	for _, expected := range gdj0047ExpectedRegistrations {
		expected := expected
		t.Run(expected.id, func(t *testing.T) {
			handler, ok := gdj0047APIScenarioHandler(expected.scenario)
			if !ok {
				t.Fatalf("scenario %q is not registered", expected.scenario)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID: expected.id, Scenario: expected.scenario, Phase: expected.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != expected.id || observation.Status != protocol.StatusObserved || observation.Phase != expected.phase {
				t.Fatalf("observation envelope = %#v", observation)
			}
			if observation.Result == nil || observation.Metrics == nil || observation.Error != nil || (observation.DBState != nil) != expected.dbState {
				t.Fatalf("observation dimensions = %#v", observation)
			}
			for name, value := range map[string]*protocol.Value{
				"result": observation.Result, "db_state": observation.DBState, "metrics": observation.Metrics,
			} {
				if value != nil {
					if err := value.Validate(); err != nil {
						t.Fatalf("%s: %v", name, err)
					}
				}
			}
		})
	}
}

func TestGDJ0047MutationAndProfileBoundariesAreIndependentlyObserved(t *testing.T) {
	unsafe := gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[5])
	unsafeDelta := parameterRoutingTestObjectField(t, *unsafe.Metrics, "article_row_delta")
	if unsafeDelta.Text == nil || *unsafeDelta.Text != "1" {
		t.Fatalf("unsafe Bearer row delta = %#v", unsafeDelta)
	}

	denied := gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[9])
	totalMutations := parameterRoutingTestObjectField(t, *denied.Metrics, "total_mutations")
	if totalMutations.Text == nil || *totalMutations.Text != "0" {
		t.Fatalf("denial mutations = %#v", totalMutations)
	}
	rowsChanged := parameterRoutingTestObjectField(t, *denied.DBState, "article_rows_changed")
	if rowsChanged.Text == nil || *rowsChanged.Text != "0" {
		t.Fatalf("denial row change = %#v", rowsChanged)
	}
	handlerInvocations := parameterRoutingTestObjectField(t, *denied.Result, "handler_invocations")
	if handlerInvocations.Text == nil || *handlerInvocations.Text != "0" {
		t.Fatalf("denial handler invocations = %#v", handlerInvocations)
	}

	isolation := gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[6])
	fallbacks := parameterRoutingTestObjectField(t, *isolation.Metrics, "fallback_authentications")
	if fallbacks.Text == nil || *fallbacks.Text != "0" {
		t.Fatalf("cross-profile fallback count = %#v", fallbacks)
	}

	first := gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[3])
	second := gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[3])
	if !reflect.DeepEqual(first, second) {
		t.Fatal("independent AUT-012 databases produced nondeterministic observations")
	}
}

func TestGDJ0047ActualArtifactsAndDiagnosticsContainNoRawBearer(t *testing.T) {
	indices := []int{3, 6, 7, 8, 9}
	observations := make([]protocol.Observation, 0, len(indices))
	for _, index := range indices {
		observations = append(observations, gdj0047RunTestScenario(t, gdj0047ExpectedRegistrations[index]))
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{
		"unknown", "inactive", "active", "profile-valid", "profile-unknown",
		"format", "invalid", "infra", "verifier-cancel", "verifier-deadline",
		"authorizer", "authorizer-cancel", "authorizer-deadline",
		"route-reuse", "invalid-denial", "denied",
	}
	for _, label := range labels {
		if strings.Contains(string(encoded), gdj0047RawToken(label)) {
			t.Fatalf("raw Bearer from label %q escaped into actual artifact", label)
		}
	}

	files := []string{"gdj0047_api_authentication_fixture.go", "gdj0047_api_authentication_scenarios.go"}
	for _, name := range files {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"conformance/oracles/", "api-authentication-oracle.json", "api-authentication-not-implemented.json"} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("oracle-blind actual source %s contains %q", name, forbidden)
			}
		}
	}
}

func TestGDJ0047RegistrationRejectsWrongIdentityAndUnknownScenario(t *testing.T) {
	handler, ok := gdj0047APIScenarioHandler(gdj0047ExpectedRegistrations[0].scenario)
	if !ok {
		t.Fatal("known GDJ-0047 scenario is missing")
	}
	if _, err := handler(context.Background(), protocol.Contract{
		ID: "AUT-999", Scenario: gdj0047ExpectedRegistrations[0].scenario, Phase: protocol.PhaseConstruction,
	}); err == nil {
		t.Fatal("GDJ-0047 handler accepted a wrong contract id")
	}
	if _, err := handler(context.Background(), protocol.Contract{
		ID: "AUT-009", Scenario: gdj0047ExpectedRegistrations[0].scenario, Phase: protocol.PhaseEvaluation,
	}); err == nil {
		t.Fatal("GDJ-0047 handler accepted a wrong phase")
	}
	if unknown, found := gdj0047APIScenarioHandler("godj.api_authentication.unknown"); found || unknown != nil {
		t.Fatalf("unknown GDJ-0047 handler = %v, %t", unknown, found)
	}

	registered := make([]string, 0, len(gdj0047ScenarioRegistry))
	for _, expected := range gdj0047ExpectedRegistrations {
		if _, found := gdj0047ScenarioRegistry[expected.scenario]; found {
			registered = append(registered, expected.id)
		}
	}
	want := []string{"AUT-009", "AUT-010", "AUT-011", "AUT-012", "AUT-013", "AUT-014", "AUT-015", "AUT-016", "API-011", "API-012"}
	if !slices.Equal(registered, want) {
		t.Fatalf("registered contract ids = %v, want %v", registered, want)
	}
}

func gdj0047RunTestScenario(t *testing.T, expected struct {
	id       string
	scenario string
	phase    protocol.Phase
	dbState  bool
}) protocol.Observation {
	t.Helper()
	handler, ok := gdj0047APIScenarioHandler(expected.scenario)
	if !ok {
		t.Fatalf("scenario %q is not registered", expected.scenario)
	}
	observation, err := handler(context.Background(), protocol.Contract{
		ID: expected.id, Scenario: expected.scenario, Phase: expected.phase,
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
