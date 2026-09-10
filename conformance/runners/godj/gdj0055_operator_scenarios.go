//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/systemstate"
)

const (
	gdj0055ExplicitProvisionScenario = "godj.system_state.explicit_operator_provisioning"
	gdj0055ArgvScenario              = "godj.system_state.createsuperuser_argv_and_pre_io"
	gdj0055TTYScenario               = "godj.system_state.tty_secret_transport"
	gdj0055OwnershipScenario         = "godj.system_state.project_provision_ownership"
	gdj0055CardinalityScenario       = "godj.system_state.operator_provision_cardinality"
	gdj0055OutcomeScenario           = "godj.system_state.provision_outcome_ownership"
	gdj0055OpenExistingScenario      = "godj.system_state.open_existing_authenticator"
	gdj0055PublicOnlyScenario        = "godj.system_state.credential_absent_public_only"
	gdj0055BackendRestartScenario    = "godj.system_state.operator_backend_login_restart"
	gdj0055CleanupScenario           = "godj.system_state.sensitive_child_cleanup"
)

type gdj0055Registration struct {
	id      string
	phase   protocol.Phase
	handler func(context.Context, protocol.Contract, GDJ0055Inputs) (protocol.Observation, error)
}

// GDJ0055Inputs contains only externally collected product facts whose owning
// environment is unavailable to the portable runner. In particular, SYS-029
// cannot publish a required PostgreSQL case from a local SQLite substitute.
type GDJ0055Inputs struct {
	PostgreSQLOperatorBackend *GDJ0055OperatorBackendFacts
	SQLiteOperatorBackend     *GDJ0055OperatorBackendFacts
}

func (inputs GDJ0055Inputs) snapshot() GDJ0055Inputs {
	var snapshot GDJ0055Inputs
	if inputs.PostgreSQLOperatorBackend != nil {
		facts := *inputs.PostgreSQLOperatorBackend
		snapshot.PostgreSQLOperatorBackend = &facts
	}
	if inputs.SQLiteOperatorBackend != nil {
		facts := *inputs.SQLiteOperatorBackend
		snapshot.SQLiteOperatorBackend = &facts
	}
	return snapshot
}

// GDJ0055OperatorBackendFacts is the oracle-independent result of one required
// backend's provision/login/restart product run. False and non-zero failure
// facts remain representable and are never coerced by the scenario adapter.
type GDJ0055OperatorBackendFacts struct {
	Backend                             string
	ProvisionProcesses                  int
	RuntimeProcesses                    int
	DistinctProcesses                   int
	ProvisionCalls                      int
	CredentialRows                      int
	Provisioned                         bool
	AdminAuthenticated                  bool
	APIAuthenticated                    bool
	DistinctProcessRestart              bool
	ProvisionProcessDistinctFromRuntime bool
	RestartRawSecretInput               bool
	RestartStateLoss                    int
	SchemaDrift                         bool
	RawSecretOccurrences                int
}

var gdj0055SystemStateScenarioRegistry = map[string]gdj0055Registration{
	gdj0055ExplicitProvisionScenario: {id: "SYS-021", phase: protocol.PhaseConstruction, handler: gdj0055ExplicitOperatorProvisioning},
	gdj0055ArgvScenario:              {id: "SYS-022", phase: protocol.PhaseEnvironment, handler: gdj0055CreatesuperuserArgv},
	gdj0055TTYScenario:               {id: "SYS-023", phase: protocol.PhaseEnvironment, handler: gdj0055TTYSecretTransport},
	gdj0055OwnershipScenario:         {id: "SYS-024", phase: protocol.PhaseEnvironment, handler: gdj0055ProjectProvisionOwnership},
	gdj0055CardinalityScenario:       {id: "SYS-025", phase: protocol.PhaseCommit, handler: gdj0055OperatorProvisionCardinality},
	gdj0055OutcomeScenario:           {id: "SYS-026", phase: protocol.PhaseCommit, handler: gdj0055ProvisionOutcomeOwnership},
	gdj0055OpenExistingScenario:      {id: "SYS-027", phase: protocol.PhaseEvaluation, handler: gdj0055OpenExistingAuthenticator},
	gdj0055PublicOnlyScenario:        {id: "SYS-028", phase: protocol.PhaseEnvironment, handler: gdj0055CredentialAbsentPublicOnly},
	gdj0055BackendRestartScenario:    {id: "SYS-029", phase: protocol.PhaseEnvironment, handler: gdj0055OperatorBackendLoginRestart},
	gdj0055CleanupScenario:           {id: "SYS-030", phase: protocol.PhaseEnvironment, handler: gdj0055SensitiveChildCleanup},
}

// gdj0055SystemStateScenarioHandler is also registered by the global runner.
// SYS-029 remains fail-closed there unless both verified backend inputs exist.
func gdj0055SystemStateScenarioHandler(scenario string, inputs GDJ0055Inputs) (scenarioHandler, bool) {
	registration, ok := gdj0055SystemStateScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	inputs = inputs.snapshot()
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if ctx == nil {
			return protocol.Observation{}, errors.New("GDJ-0055 system-state scenario context is nil")
		}
		if err := ctx.Err(); err != nil {
			return protocol.Observation{}, err
		}
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("GDJ-0055 scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("GDJ-0055 scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("GDJ-0055 scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract, inputs)
	}, true
}

type gdj0055StateFixture struct {
	directory string
	dsn       string
	raw       *sqlite.Backend
	observed  *systemStateObservedBackend
	config    systemStateConfig
}

func newGDJ0055StateFixture(ctx context.Context, withArticle bool) (*gdj0055StateFixture, error) {
	directory, err := os.MkdirTemp("", "godj-gdj0055-system-state-")
	if err != nil {
		return nil, fmt.Errorf("create GDJ-0055 state directory: %w", err)
	}
	fixture := &gdj0055StateFixture{
		directory: directory,
		dsn:       "file:" + filepath.ToSlash(filepath.Join(directory, "system-state.sqlite3")) + "?mode=rwc&_busy_timeout=5000&_pragma=foreign_keys(1)",
		config:    systemStateFixtureConfig(0x55),
	}
	fixture.config.Username = "gdj0055-operator"
	fixture.config.Password = "gdj0055-operator-password"
	raw, err := sqlite.Open(ctx, fixture.dsn)
	if err != nil {
		fixture.cleanup()
		return nil, fmt.Errorf("open GDJ-0055 SQLite state: %w", err)
	}
	fixture.raw = raw
	fixture.observed = &systemStateObservedBackend{Backend: raw}
	if _, err := systemStateMigrate(ctx, fixture.observed, withArticle); err != nil {
		fixture.cleanup()
		return nil, err
	}
	fixture.observed.resetDML()
	return fixture, nil
}

func (fixture *gdj0055StateFixture) cleanup() {
	if fixture == nil {
		return
	}
	if fixture.raw != nil {
		_ = fixture.raw.Close()
		fixture.raw = nil
	}
	if fixture.directory != "" {
		base := filepath.Base(fixture.directory)
		if strings.HasPrefix(base, "godj-gdj0055-system-state-") && filepath.Dir(fixture.directory) != fixture.directory {
			_ = os.RemoveAll(fixture.directory)
		}
		fixture.directory = ""
	}
}

func (fixture *gdj0055StateFixture) resetDML() {
	fixture.observed.resetDML()
}

func (fixture *gdj0055StateFixture) writes() int64 {
	return fixture.observed.inserts.Load() + fixture.observed.updates.Load() + fixture.observed.deletes.Load()
}

type gdj0055ObservedHasher struct {
	auth.PasswordHasher
	hashCalls     atomic.Int64
	verifyCalls   atomic.Int64
	validateCalls atomic.Int64
}

func (hasher *gdj0055ObservedHasher) Hash(ctx context.Context, password string) (string, error) {
	hasher.hashCalls.Add(1)
	return hasher.PasswordHasher.Hash(ctx, password)
}

func (hasher *gdj0055ObservedHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	hasher.verifyCalls.Add(1)
	return hasher.PasswordHasher.Verify(ctx, password, encoded)
}

func (hasher *gdj0055ObservedHasher) ValidateEncoded(encoded string) error {
	hasher.validateCalls.Add(1)
	return hasher.PasswordHasher.ValidateEncoded(encoded)
}

const gdj0055ModulePath = "github.com/progresshans/godj"

type gdj0055SystemStateAPIFacts struct {
	currentAPI                    []string
	compatibilityShims            int
	implicitBootstrapEntrypoints  int
	provisionEntrypoints          int
	rawSecretInputsToOpenExisting int
}

// gdj0055SourceImporter loads the checked-out module through go/types rather
// than trusting runtime function values. Package-level function variables and
// wrapper entrypoints therefore remain visible to the current-only ABI probe.
type gdj0055SourceImporter struct {
	repositoryRoot string
	fileSet        *token.FileSet
	standard       types.Importer
	packages       map[string]*types.Package
	loading        map[string]bool
}

func (source *gdj0055SourceImporter) Import(path string) (*types.Package, error) {
	return source.ImportFrom(path, "", 0)
}

func (source *gdj0055SourceImporter) ImportFrom(path, _ string, _ types.ImportMode) (*types.Package, error) {
	if loaded := source.packages[path]; loaded != nil {
		return loaded, nil
	}
	if !strings.HasPrefix(path, gdj0055ModulePath+"/") {
		var loaded *types.Package
		var err error
		if from, ok := source.standard.(types.ImporterFrom); ok {
			loaded, err = from.ImportFrom(path, "", 0)
		} else {
			loaded, err = source.standard.Import(path)
		}
		if err != nil {
			return nil, err
		}
		source.packages[path] = loaded
		return loaded, nil
	}
	if source.loading[path] {
		return nil, fmt.Errorf("GDJ-0055 source type import cycle at %q", path)
	}
	source.loading[path] = true
	defer delete(source.loading, path)

	relative := strings.TrimPrefix(path, gdj0055ModulePath+"/")
	directory := filepath.Join(source.repositoryRoot, filepath.FromSlash(relative))
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read GDJ-0055 source package %q: %w", path, err)
	}
	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		matched, err := build.Default.MatchFile(directory, name)
		if err != nil {
			return nil, fmt.Errorf("match GDJ-0055 source file %s: %w", filepath.Join(relative, name), err)
		}
		if !matched {
			continue
		}
		parsed, err := parser.ParseFile(source.fileSet, filepath.Join(directory, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse GDJ-0055 source file %s: %w", filepath.Join(relative, name), err)
		}
		files = append(files, parsed)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("GDJ-0055 source package %q has no current files", path)
	}
	configuration := types.Config{
		Importer:  source,
		GoVersion: "go1.26",
		Sizes:     types.SizesFor("gc", runtime.GOARCH),
	}
	loaded, err := configuration.Check(path, source.fileSet, files, nil)
	if err != nil {
		return nil, fmt.Errorf("type-check GDJ-0055 source package %q: %w", path, err)
	}
	source.packages[path] = loaded
	return loaded, nil
}

func gdj0055InspectSystemStateAPI() (gdj0055SystemStateAPIFacts, error) {
	repositoryRoot, err := systemStateRepositoryRoot()
	if err != nil {
		return gdj0055SystemStateAPIFacts{}, err
	}
	loader := &gdj0055SourceImporter{
		repositoryRoot: repositoryRoot,
		fileSet:        token.NewFileSet(),
		standard:       importer.Default(),
		packages:       make(map[string]*types.Package),
		loading:        make(map[string]bool),
	}
	loaded, err := loader.Import(gdj0055ModulePath + "/systemstate")
	if err != nil {
		return gdj0055SystemStateAPIFacts{}, err
	}
	return gdj0055InspectSystemStatePackage(loaded)
}

func gdj0055InspectSystemStatePackage(loaded *types.Package) (gdj0055SystemStateAPIFacts, error) {
	if loaded == nil {
		return gdj0055SystemStateAPIFacts{}, errors.New("GDJ-0055 system-state type package is nil")
	}
	scope := loaded.Scope()
	provision := scope.Lookup("ProvisionOperator")
	open := scope.Lookup("OpenExisting")
	if provision == nil || open == nil {
		return gdj0055SystemStateAPIFacts{}, errors.New("GDJ-0055 current system-state entrypoints are missing")
	}
	provisionSignature, provisionOK := provision.Type().Underlying().(*types.Signature)
	openSignature, openOK := open.Type().Underlying().(*types.Signature)
	if !provisionOK || !openOK {
		return gdj0055SystemStateAPIFacts{}, errors.New("GDJ-0055 current system-state entrypoints are not callable")
	}

	facts := gdj0055SystemStateAPIFacts{}
	shimNames := make(map[string]struct{})
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if object == nil || !object.Exported() {
			continue
		}
		signature, callable := object.Type().Underlying().(*types.Signature)
		if callable && (types.Identical(signature, provisionSignature) || types.Identical(signature, openSignature)) {
			facts.currentAPI = append(facts.currentAPI, name)
			if name != "ProvisionOperator" && name != "OpenExisting" {
				shimNames[name] = struct{}{}
			}
		}
		if callable && types.Identical(signature, provisionSignature) {
			facts.provisionEntrypoints++
		}
		if name == "Open" && callable {
			shimNames[name] = struct{}{}
			facts.implicitBootstrapEntrypoints++
		}
		if name == "BootstrapConfig" {
			shimNames[name] = struct{}{}
			facts.implicitBootstrapEntrypoints++
		}
	}
	sort.Strings(facts.currentAPI)
	facts.compatibilityShims = len(shimNames)
	facts.rawSecretInputsToOpenExisting = gdj0055RawSecretInputs(openSignature, loaded.Path())
	return facts, nil
}

func gdj0055RawSecretInputs(signature *types.Signature, packagePath string) int {
	if signature == nil {
		return 0
	}
	count := 0
	visiting := make(map[types.Type]bool)
	var inspect func(types.Type, string)
	inspect = func(candidate types.Type, name string) {
		if candidate == nil {
			return
		}
		if gdj0055SensitiveScalar(name, candidate) {
			count++
			return
		}
		switch typed := candidate.(type) {
		case *types.Pointer:
			inspect(typed.Elem(), name)
		case *types.Alias:
			inspect(types.Unalias(typed), name)
		case *types.Named:
			object := typed.Obj()
			if object == nil || object.Pkg() == nil || object.Pkg().Path() != packagePath || visiting[typed] {
				return
			}
			visiting[typed] = true
			inspect(typed.Underlying(), name)
			delete(visiting, typed)
		case *types.Struct:
			for index := 0; index < typed.NumFields(); index++ {
				field := typed.Field(index)
				inspect(field.Type(), field.Name())
			}
		case *types.Array:
			inspect(typed.Elem(), name)
		case *types.Slice:
			inspect(typed.Elem(), name)
		case *types.Map:
			inspect(typed.Elem(), name)
		}
	}
	parameters := signature.Params()
	for index := 0; index < parameters.Len(); index++ {
		parameter := parameters.At(index)
		inspect(parameter.Type(), parameter.Name())
	}
	return count
}

func gdj0055SensitiveScalar(name string, candidate types.Type) bool {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "password") && !strings.Contains(lower, "secret") &&
		!strings.Contains(lower, "bearer") && !strings.Contains(lower, "token") {
		return false
	}
	underlying := candidate.Underlying()
	if basic, ok := underlying.(*types.Basic); ok {
		return basic.Kind() == types.String
	}
	slice, ok := underlying.(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := slice.Elem().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Byte
}

func gdj0055ExplicitOperatorProvisioning(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	fixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()

	beforeDefinition := systemstate.InitialDefinitionSource().Document
	fixture.resetDML()
	runtimeBefore, absentErr := systemStateOpenExisting(ctx, fixture.observed, fixture.config)
	openBeforeWrites := fixture.writes()
	if runtimeBefore != nil || !errors.Is(absentErr, &systemstate.Error{Code: systemstate.CodeCredentialAbsent}) || openBeforeWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("OpenExisting on migrated clean state = runtime %v error %v writes %d", runtimeBefore, absentErr, openBeforeWrites)
	}

	fixture.resetDML()
	if err := systemStateProvisionOperator(ctx, fixture.observed, fixture.config); err != nil {
		return protocol.Observation{}, fmt.Errorf("explicitly provision operator: %w", err)
	}
	if fixture.observed.inserts.Load() != 1 || fixture.observed.updates.Load() != 0 || fixture.observed.deletes.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf("explicit provision DML = insert %d update %d delete %d", fixture.observed.inserts.Load(), fixture.observed.updates.Load(), fixture.observed.deletes.Load())
	}

	fixture.resetDML()
	runtimeAfter, err := systemStateOpenExisting(ctx, fixture.observed, fixture.config)
	if err != nil || runtimeAfter == nil || runtimeAfter.Authenticator() == nil {
		return protocol.Observation{}, fmt.Errorf("OpenExisting after provision = runtime %v error %v", runtimeAfter, err)
	}
	openAfterWrites := fixture.writes()
	if openAfterWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("OpenExisting after provision performed %d writes", openAfterWrites)
	}
	afterDefinition := systemstate.InitialDefinitionSource().Document

	apiFacts, err := gdj0055InspectSystemStateAPI()
	if err != nil {
		return protocol.Observation{}, err
	}
	implicitBootstrap := "removed"
	if apiFacts.implicitBootstrapEntrypoints != 0 {
		implicitBootstrap = "present"
	}
	currentOnly := apiFacts.compatibilityShims == 0 && apiFacts.provisionEntrypoints == 1 &&
		apiFacts.rawSecretInputsToOpenExisting == 0 && len(apiFacts.currentAPI) == 2 &&
		apiFacts.currentAPI[0] == "OpenExisting" && apiFacts.currentAPI[1] == "ProvisionOperator"
	currentAPI := make([]protocol.Value, len(apiFacts.currentAPI))
	for index, name := range apiFacts.currentAPI {
		currentAPI[index] = protocol.String(name)
	}

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"current_api":                    protocol.List(currentAPI...),
			"current_only":                   protocol.Boolean(currentOnly),
			"implicit_bootstrap_open":        protocol.String(implicitBootstrap),
			"open_existing_raw_secret_input": protocol.Boolean(apiFacts.rawSecretInputsToOpenExisting != 0),
			"provision_intent":               protocol.String("explicit"),
		}),
		protocol.Object(map[string]protocol.Value{
			"open_existing_writes":              systemStateInt64(openBeforeWrites + openAfterWrites),
			"schema_or_migration_bytes_changed": protocol.Boolean(!bytes.Equal(beforeDefinition, afterDefinition)),
			"startup_credential_inserts":        systemStateInt64(fixture.observed.inserts.Load()),
		}),
		protocol.Object(map[string]protocol.Value{
			"compatibility_shims":                systemStateInt(apiFacts.compatibilityShims),
			"provision_entrypoints":              systemStateInt(apiFacts.provisionEntrypoints),
			"raw_secret_inputs_to_open_existing": systemStateInt(apiFacts.rawSecretInputsToOpenExisting),
		}),
	)
}

type gdj0055ForbiddenBuildBackend struct{ calls atomic.Int64 }

func (backend *gdj0055ForbiddenBuildBackend) Execute(
	context.Context,
	<-chan struct{},
	productcheck.ProcessStage,
	productcheck.Command,
) productcheck.ProcessResult {
	backend.calls.Add(1)
	return productcheck.ProcessResult{}
}

func gdj0055CreatesuperuserArgv(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	accepted := [][]string{
		{"createsuperuser"},
		{"createsuperuser", "--project", "PATH"},
	}
	acceptedValues := make([]protocol.Value, 0, len(accepted))
	for _, argv := range accepted {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		backend := &gdj0055ForbiddenBuildBackend{}
		var stdout, stderr bytes.Buffer
		report := productcheck.RunCreatesuperuser(productcheck.CreatesuperuserInvocation{
			Context: canceled,
			CWD:     filepath.Join(os.TempDir(), "gdj0055-accepted-argv-must-not-read"),
			Args:    append([]string(nil), argv...),
			Stdout:  &stdout,
			Stderr:  &stderr,
			Backend: backend,
		})
		if !report.HasCreatesuperuserFailure || report.CreatesuperuserFailure.Code != createsuperuserprotocol.CodeProjectCanceled ||
			report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 || report.BuildCalls != 0 ||
			report.RunnerCalls != 0 || report.TerminalChecks != 0 || backend.calls.Load() != 0 || stdout.Len() != 0 {
			return protocol.Observation{}, fmt.Errorf("accepted createsuperuser argv %q crossed canceled pre-I/O boundary: report=%+v backend=%d", argv, report, backend.calls.Load())
		}
		items := make([]protocol.Value, len(argv))
		for index, value := range argv {
			items[index] = protocol.String(value)
		}
		acceptedValues = append(acceptedValues, protocol.List(items...))
	}

	rejectedClasses := []struct {
		name  string
		forms [][]string
	}{
		{name: "identity_or_secret_flag", forms: [][]string{
			{"createsuperuser", "--username", "operator"},
			{"createsuperuser", "--password", "argv-secret-marker"},
			{"createsuperuser", "--password-file", "secret.txt"},
			{"createsuperuser", "--noinput"},
		}},
		{name: "noncanonical_permutation", forms: [][]string{
			{"createsuperuser", "--project=PATH"},
			{"createsuperuser", "--project", "PATH", "--noinput"},
		}},
		{name: "positional_identity", forms: [][]string{
			{"createsuperuser", "operator"},
		}},
	}
	var projectReads, builds, discoveries, terminalReads, childStarts, secretAccepted int
	classValues := make([]protocol.Value, 0, len(rejectedClasses))
	for _, class := range rejectedClasses {
		for _, argv := range class.forms {
			backend := &gdj0055ForbiddenBuildBackend{}
			var stdout, stderr bytes.Buffer
			report := productcheck.RunCreatesuperuser(productcheck.CreatesuperuserInvocation{
				Context: ctx,
				CWD:     filepath.Join(os.TempDir(), "gdj0055-rejected-argv-must-not-read"),
				Args:    append([]string(nil), argv...),
				Stdout:  &stdout,
				Stderr:  &stderr,
				Backend: backend,
			})
			wantFailure := createsuperuserprotocol.Failure{
				Category: createsuperuserprotocol.CategoryCommand,
				Code:     createsuperuserprotocol.CodeInvalidArguments,
			}
			if report.ExitCode != 2 || !report.HasCreatesuperuserFailure || report.CreatesuperuserFailure != wantFailure ||
				report.HasCreatesuperuserResult || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 ||
				report.BuildCalls != 0 || report.RunnerCalls != 0 || report.TerminalChecks != 0 || backend.calls.Load() != 0 || stdout.Len() != 0 {
				return protocol.Observation{}, fmt.Errorf("rejected createsuperuser argv %q crossed pre-I/O boundary: report=%+v backend=%d", argv, report, backend.calls.Load())
			}
			projectReads += report.DescriptorReads
			discoveries += report.AncestorDirectoriesInspected
			builds += report.BuildCalls
			terminalReads += report.TerminalChecks
			childStarts += report.RunnerCalls
			if strings.Contains(strings.Join(argv, "\x00"), "secret") && report.CreatesuperuserFailure.Code != createsuperuserprotocol.CodeInvalidArguments {
				secretAccepted++
			}
		}
		classValues = append(classValues, protocol.String(class.name))
	}

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"accepted_argv":    protocol.List(acceptedValues...),
			"invalid_forms":    protocol.String("rejected_before_io"),
			"rejected_classes": protocol.List(classValues...),
		}),
		protocol.Object(map[string]protocol.Value{
			"backend_opens_on_rejection": systemStateInt(0),
			"project_reads_on_rejection": systemStateInt(projectReads),
			"writes_on_rejection":        systemStateInt(0),
		}),
		protocol.Object(map[string]protocol.Value{
			"accepted_forms":                   systemStateInt(len(accepted)),
			"child_starts_on_rejection":        systemStateInt(childStarts),
			"project_builds_on_rejection":      systemStateInt(builds),
			"project_discoveries_on_rejection": systemStateInt(discoveries),
			"secret_bearing_forms_accepted":    systemStateInt(secretAccepted),
			"terminal_reads_on_rejection":      systemStateInt(terminalReads),
		}),
	)
}
