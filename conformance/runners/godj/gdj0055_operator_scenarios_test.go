//go:build darwin || linux

package godj

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0055SystemStateAPIInspectionDetectsCallableAliasesAndNestedSecrets(t *testing.T) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "systemstate.go", `package systemstate

type CredentialPolicy struct { PasswordHasher interface{} }
type NestedRuntimeSecret struct { APISecret []byte }
type RuntimeConfig struct {
	CredentialPolicy CredentialPolicy
	Nested NestedRuntimeSecret
}
type ProvisionOperatorConfig struct{}
type BootstrapConfig = RuntimeConfig

func ProvisionOperator(context int, backend int, config ProvisionOperatorConfig) error { return nil }
var ProvisionAgain = ProvisionOperator
func OpenExisting(context int, backend int, config RuntimeConfig) (int, error) { return 0, nil }
var Open = OpenExisting
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := (&types.Config{GoVersion: "go1.26"}).Check(
		"example.test/systemstate",
		fileSet,
		[]*ast.File{parsed},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := gdj0055InspectSystemStatePackage(loaded)
	if err != nil {
		t.Fatal(err)
	}
	wantAPI := []string{"Open", "OpenExisting", "ProvisionAgain", "ProvisionOperator"}
	if !reflect.DeepEqual(facts.currentAPI, wantAPI) || facts.compatibilityShims != 3 ||
		facts.implicitBootstrapEntrypoints != 2 || facts.provisionEntrypoints != 2 ||
		facts.rawSecretInputsToOpenExisting != 1 {
		t.Fatalf("synthetic system-state API facts=%+v, want API=%v shims=3 implicit=2 provision=2 raw=1", facts, wantAPI)
	}
}

func TestGDJ0055ScenarioHandlersAreGloballyRegisteredAndMatchPublishedObservations(t *testing.T) {
	manifest, err := protocol.LoadManifest(filepath.Join("..", "..", "contracts", "system-state-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(filepath.Join("..", "..", "oracles", "django-6.1-sqlite-darwin-arm64", "system-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	contracts := make(map[string]protocol.Contract)
	for _, contract := range manifest.Contracts {
		contracts[contract.ID] = contract
	}
	expected := make(map[string]protocol.Observation)
	for _, observation := range oracle.Contracts {
		expected[observation.ID] = observation
	}
	postgres := gdj0055PassingBackendFacts("postgresql_17_10")
	sqlite := gdj0055PassingBackendFacts("sqlite")
	inputs := GDJ0055Inputs{
		PostgreSQLOperatorBackend: &postgres,
		SQLiteOperatorBackend:     &sqlite,
	}

	identifiers := make([]string, 0, len(gdj0055SystemStateScenarioRegistry))
	for scenario, registration := range gdj0055SystemStateScenarioRegistry {
		identifiers = append(identifiers, registration.id)
		if _, globallyRegistered := lookupScenarioHandler(scenario); !globallyRegistered {
			t.Fatalf("GDJ-0055 scenario %q is absent from the global runner", scenario)
		}
	}
	sort.Strings(identifiers)
	wantIDs := []string{"SYS-021", "SYS-022", "SYS-023", "SYS-024", "SYS-025", "SYS-026", "SYS-027", "SYS-028", "SYS-029", "SYS-030"}
	if !reflect.DeepEqual(identifiers, wantIDs) {
		t.Fatalf("GDJ-0055 local registry ids=%v, want %v", identifiers, wantIDs)
	}
	required, err := RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != len(manifest.Contracts) || !reflect.DeepEqual(required[len(required)-len(wantIDs):], wantIDs) {
		t.Fatalf("required registry ids=%v, want all manifest contracts ending in %v", required, wantIDs)
	}

	for _, identifier := range wantIDs {
		identifier := identifier
		t.Run(identifier, func(t *testing.T) {
			contract, ok := contracts[identifier]
			if !ok {
				t.Fatalf("manifest contract %s is missing", identifier)
			}
			handler, ok := lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{
				ProjectOperatorPostgreSQL: inputs.PostgreSQLOperatorBackend,
				ProjectOperatorSQLite:     inputs.SQLiteOperatorBackend,
			})
			if !ok {
				t.Fatalf("global handler %s is missing", identifier)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			actual, err := handler(ctx, contract)
			if err != nil {
				t.Fatal(err)
			}
			if want := expected[identifier]; !reflect.DeepEqual(actual, want) {
				t.Fatalf("%s observation differs\nactual: %#v\nwant:   %#v", identifier, actual, want)
			}
		})
	}
}

func TestGDJ0055BackendEvidenceFailsClosedBeforeObservation(t *testing.T) {
	contract := protocol.Contract{
		ID:       "SYS-029",
		Scenario: gdj0055BackendRestartScenario,
		Phase:    protocol.PhaseEnvironment,
	}
	handler, ok := lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{})
	if !ok {
		t.Fatal("SYS-029 global handler is missing")
	}
	_, err := handler(context.Background(), contract)
	if err == nil || err.Error() != "GDJ-0055 operator backend scenario: verified PostgreSQL evidence is missing" {
		t.Fatalf("missing PostgreSQL evidence error=%v", err)
	}

	postgres := gdj0055PassingBackendFacts("postgresql_17_10")
	sqlite := gdj0055PassingBackendFacts("sqlite")
	handler, _ = lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{ProjectOperatorPostgreSQL: &postgres})
	_, err = handler(context.Background(), contract)
	if err == nil || err.Error() != "GDJ-0055 operator backend scenario: verified SQLite evidence is missing" {
		t.Fatalf("missing SQLite evidence error=%v", err)
	}

	malformed := gdj0055PassingBackendFacts("postgresql_17_10")
	malformed.DistinctProcesses = -1
	handler, _ = lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{
		ProjectOperatorPostgreSQL: &malformed,
		ProjectOperatorSQLite:     &sqlite,
	})
	_, err = handler(context.Background(), contract)
	if err == nil || err.Error() != "GDJ-0055 operator backend scenario: PostgreSQL evidence is malformed" {
		t.Fatalf("malformed PostgreSQL evidence error=%v", err)
	}

	malformed = gdj0055PassingBackendFacts("sqlite")
	malformed.ProvisionCalls = -1
	handler, _ = lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{
		ProjectOperatorPostgreSQL: &postgres,
		ProjectOperatorSQLite:     &malformed,
	})
	_, err = handler(context.Background(), contract)
	if err == nil || err.Error() != "GDJ-0055 operator backend scenario: SQLite evidence is malformed" {
		t.Fatalf("malformed SQLite evidence error=%v", err)
	}
}

func TestGDJ0055GlobalRegistryRejectsUnpublishedOrMismatchedManifestEntries(t *testing.T) {
	manifest, err := protocol.LoadManifest(filepath.Join("..", "..", "contracts", "system-state-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for index := range manifest.Contracts {
		if manifest.Contracts[index].ID != "SYS-029" {
			continue
		}
		locked := manifest
		locked.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
		locked.Contracts[index].Status = protocol.ContractOracleLocked
		if _, err := RequiredObservedContractIDs(locked); err == nil || !strings.Contains(err.Error(), "registered scenario") {
			t.Fatalf("locked globally registered SYS-029 error=%v", err)
		}

		wrongID := manifest.Contracts[index]
		wrongID.ID = "SYS-999"
		handler, ok := lookupScenarioHandler(wrongID.Scenario)
		if !ok {
			t.Fatal("SYS-029 global handler is missing")
		}
		if _, err := handler(context.Background(), wrongID); err == nil || !strings.Contains(err.Error(), "contract id") {
			t.Fatalf("wrong global SYS-029 identity error=%v", err)
		}
		return
	}
	t.Fatal("SYS-029 manifest contract is missing")
}

func TestGDJ0055GlobalRunnerSnapshotsBothFailureShapedBackendInputs(t *testing.T) {
	contract := protocol.Contract{
		ID:       "SYS-029",
		Scenario: gdj0055BackendRestartScenario,
		Phase:    protocol.PhaseEnvironment,
	}
	postgres := gdj0055PassingBackendFacts("postgresql_17_10")
	postgres.APIAuthenticated = false
	postgres.RestartStateLoss = 2
	postgres.RawSecretOccurrences = 3
	sqlite := gdj0055PassingBackendFacts("sqlite")
	sqlite.AdminAuthenticated = false
	sqlite.RestartRawSecretInput = true

	handler, ok := lookupScenarioHandlerWithInputs(contract.Scenario, Inputs{
		ProjectOperatorPostgreSQL: &postgres,
		ProjectOperatorSQLite:     &sqlite,
	})
	if !ok {
		t.Fatal("SYS-029 global handler is missing")
	}
	postgres.APIAuthenticated = true
	postgres.RestartStateLoss = 0
	postgres.RawSecretOccurrences = 0
	sqlite.AdminAuthenticated = true
	sqlite.RestartRawSecretInput = false

	observation, err := handler(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	login := systemStateTestField(t, *observation.Result, "django_login_semantics_reused")
	if login.Bool == nil || *login.Bool {
		t.Fatalf("snapshot login fact=%#v, want false", login)
	}
	restartSecret := systemStateTestField(t, *observation.Result, "restart_raw_secret_input")
	if restartSecret.Bool == nil || !*restartSecret.Bool {
		t.Fatalf("snapshot restart secret fact=%#v, want true", restartSecret)
	}
	if got := systemStateTestInteger(t, *observation.DBState, "restart_state_loss"); got != 2 {
		t.Fatalf("snapshot restart loss=%d, want 2", got)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "raw_secret_occurrences"); got != 3 {
		t.Fatalf("snapshot raw-secret occurrences=%d, want 3", got)
	}
}

func TestGDJ0055PTYDrainFailsClosedWhenObservationCannotStart(t *testing.T) {
	if document, err := gdj0055ReadAvailablePTY(nil); err == nil || document != nil {
		t.Fatalf("nil PTY drain = (%q, %v), want nil/error", document, err)
	}
	closed, err := os.CreateTemp(t.TempDir(), "closed-pty-")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if document, err := gdj0055ReadAvailablePTY(closed); err == nil || document != nil {
		t.Fatalf("closed PTY drain = (%q, %v), want nil/error", document, err)
	}
}

func TestGDJ0055PTYDrainReturnsImmediatelyWhenNoBytesAreAvailable(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()

	type result struct {
		document []byte
		err      error
	}
	ready := make(chan result, 1)
	go func() {
		document, err := gdj0055ReadAvailablePTY(master)
		ready <- result{document: document, err: err}
	}()

	select {
	case got := <-ready:
		if got.err != nil || len(got.document) != 0 {
			t.Fatalf("empty PTY drain = (%q, %v), want empty/nil", got.document, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("empty PTY drain blocked")
	}
}

func TestGDJ0055BackendObservationExposesEveryPostgreSQLCountDisagreement(t *testing.T) {
	contract := protocol.Contract{ID: "SYS-029", Phase: protocol.PhaseEnvironment}
	sqliteFacts := gdj0055PassingBackendFacts("sqlite")
	baseline := gdj0055PassingBackendFacts("postgresql_17_10")
	tests := []struct {
		name    string
		field   string
		dbState bool
		mutate  func(*GDJ0055OperatorBackendFacts)
	}{
		{name: "credential rows", field: "credential_rows_per_backend", dbState: true, mutate: func(facts *GDJ0055OperatorBackendFacts) { facts.CredentialRows++ }},
		{name: "distinct processes", field: "distinct_processes_per_backend", mutate: func(facts *GDJ0055OperatorBackendFacts) { facts.DistinctProcesses++ }},
		{name: "provision calls", field: "provision_calls_per_backend", mutate: func(facts *GDJ0055OperatorBackendFacts) { facts.ProvisionCalls++ }},
		{name: "provision processes", field: "provision_processes_per_backend", mutate: func(facts *GDJ0055OperatorBackendFacts) { facts.ProvisionProcesses++ }},
		{name: "runtime processes", field: "runtime_processes_per_backend", mutate: func(facts *GDJ0055OperatorBackendFacts) { facts.RuntimeProcesses++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			postgres := baseline
			test.mutate(&postgres)
			observation, err := gdj0055OperatorBackendObservation(contract, sqliteFacts, postgres)
			if err != nil {
				t.Fatal(err)
			}
			container := observation.Metrics
			if test.dbState {
				container = observation.DBState
			}
			if got := systemStateTestInteger(t, *container, test.field); got != -1 {
				t.Fatalf("%s disagreement=%d, want -1", test.field, got)
			}
		})
	}
}

func TestGDJ0055BackendObservationCombinesBothBackendFailureFacts(t *testing.T) {
	contract := protocol.Contract{ID: "SYS-029", Phase: protocol.PhaseEnvironment}
	sqliteFacts := gdj0055PassingBackendFacts("sqlite")
	postgres := gdj0055PassingBackendFacts("postgresql_17_10")
	postgres.RestartRawSecretInput = true
	postgres.RestartStateLoss = 2
	postgres.SchemaDrift = true
	postgres.RawSecretOccurrences = 3
	postgres.APIAuthenticated = false
	observation, err := gdj0055OperatorBackendObservation(contract, sqliteFacts, postgres)
	if err != nil {
		t.Fatal(err)
	}
	if got := systemStateTestInteger(t, *observation.DBState, "restart_state_loss"); got != 2 {
		t.Fatalf("restart loss=%d, want 2", got)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "raw_secret_occurrences"); got != 3 {
		t.Fatalf("raw secret occurrences=%d, want 3", got)
	}
	for _, field := range []struct {
		value protocol.Value
		name  string
	}{
		{value: *observation.Result, name: "restart_raw_secret_input"},
		{value: *observation.DBState, name: "schema_drift"},
	} {
		got := systemStateTestField(t, field.value, field.name)
		if got.Bool == nil || !*got.Bool {
			t.Fatalf("%s=%#v, want true", field.name, got)
		}
	}
	login := systemStateTestField(t, *observation.Result, "django_login_semantics_reused")
	if login.Bool == nil || *login.Bool {
		t.Fatalf("django_login_semantics_reused=%#v, want false", login)
	}
}

func TestGDJ0055HandlersRejectMismatchedContractIdentityAndPhase(t *testing.T) {
	for scenario, registration := range gdj0055SystemStateScenarioRegistry {
		handler, ok := gdj0055SystemStateScenarioHandler(scenario, GDJ0055Inputs{})
		if !ok {
			t.Fatalf("handler %q is missing", scenario)
		}
		_, err := handler(context.Background(), protocol.Contract{ID: "SYS-999", Scenario: scenario, Phase: registration.phase})
		if err == nil || !strings.Contains(err.Error(), "contract id") {
			t.Fatalf("handler %q wrong-id error=%v", scenario, err)
		}
		_, err = handler(context.Background(), protocol.Contract{ID: registration.id, Scenario: scenario, Phase: protocol.PhaseRollback})
		if registration.phase != protocol.PhaseRollback && (err == nil || !strings.Contains(err.Error(), "phase")) {
			t.Fatalf("handler %q wrong-phase error=%v", scenario, err)
		}
	}
}

func TestGDJ0055HandlerSourcesDoNotReadExpectedArtifacts(t *testing.T) {
	entries, err := filepath.Glob("gdj0055_*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(document)
		for _, forbidden := range []string{"conformance/oracles", "godj-system-state-not-implemented", "system_state_decisions.py"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("handler source %s contains expected-artifact path %q", path, forbidden)
			}
		}
	}
}

func gdj0055PassingBackendFacts(backend string) GDJ0055OperatorBackendFacts {
	return GDJ0055OperatorBackendFacts{
		Backend: backend, ProvisionProcesses: 1, RuntimeProcesses: 2,
		DistinctProcesses: 3, ProvisionCalls: 1, CredentialRows: 1,
		Provisioned: true, AdminAuthenticated: true, APIAuthenticated: true,
		DistinctProcessRestart: true, ProvisionProcessDistinctFromRuntime: true,
		RestartRawSecretInput: false, RestartStateLoss: 0, SchemaDrift: false,
		RawSecretOccurrences: 0,
	}
}
