package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMigrationArtifactsHaveExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "write-migration-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "write-migration-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-write-migration-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, actual); err != nil {
		t.Fatalf("GoDj not-implemented baseline does not validate: %v", err)
	}
	readManifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	readOracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		manifest Manifest
		suite    ObservationSuite
	}{
		{name: "read manifest with write oracle", manifest: readManifest, suite: oracle},
		{name: "write manifest with read oracle", manifest: manifest, suite: readOracle},
	} {
		if err := ValidateSuiteAgainst(profile, test.manifest, test.suite); err == nil || !strings.Contains(err.Error(), "position") {
			t.Fatalf("%s: expected checked-in cross-set rejection, got %v", test.name, err)
		}
	}
	for _, test := range []struct {
		name  string
		suite ObservationSuite
	}{
		{name: "observed oracle", suite: cloneSuite(t, oracle)},
		{name: "not-implemented baseline", suite: cloneSuite(t, actual)},
	} {
		test.suite.Contracts[0].Phase = PhaseEnvironment
		if err := ValidateSuiteAgainst(profile, manifest, test.suite); err == nil || !strings.Contains(err.Error(), "manifest phase") {
			t.Fatalf("%s: expected checked-in phase mutation rejection, got %v", test.name, err)
		}
	}
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(manifest.Contracts) {
		t.Fatalf("got %d differences, want one for each of %d contracts: %#v", len(differences), len(manifest.Contracts), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d does not preserve manifest order or explicit not-implemented status: %#v", index, difference)
		}
	}

	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *ObservationSuite)
	}{
		{
			name:       "write result",
			contractID: "MOD-001",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				primaryKey := objectField(t, suite.Contracts[0].Result, "pk")
				changed := "99"
				primaryKey.Nested.Text = &changed
			},
		},
		{
			name:       "transaction error",
			contractID: "MOD-007",
			mutate: func(_ *testing.T, suite *ObservationSuite) {
				suite.Contracts[6].Error.Code = "changed_rollback"
			},
		},
		{
			name:       "transaction rollback sentinel",
			contractID: "MOD-007",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				articles := objectField(t, suite.Contracts[6].DBState, "articles")
				articles.Items = []Value{}
			},
		},
		{
			name:       "transaction rollback restores prior value",
			contractID: "MOD-007",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				articles := objectField(t, suite.Contracts[6].DBState, "articles")
				title := objectField(t, &articles.Items[0], "title")
				changed := "Mutated inside transaction"
				title.Text = &changed
			},
		},
		{
			name:       "migration schema state",
			contractID: "MIG-002",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				schema := objectField(t, suite.Contracts[8].DBState, "schema")
				columns := objectField(t, schema, "columns")
				summary := &columns.Items[len(columns.Items)-1]
				nullable := objectField(t, summary, "nullable")
				changed := !*nullable.Bool
				nullable.Bool = &changed
			},
		},
		{
			name:       "migration recovery metrics",
			contractID: "MIG-004",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				recovery := objectField(t, suite.Contracts[10].Metrics, "connection_recovery")
				queryResult := objectField(t, recovery, "subsequent_query_result")
				changed := "9"
				queryResult.Text = &changed
			},
		},
		{
			name:       "migration managed table inventory",
			contractID: "MIG-004",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				managedTables := objectField(t, suite.Contracts[10].DBState, "managed_tables")
				managedTables.Items = append(managedTables.Items, String("godj_failure_broken__leftover"))
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			test.mutate(t, &actual)
			differences, err := Compare(profile, manifest, oracle, actual)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) == 0 {
				t.Fatal("artifact mutation produced a false green")
			}
			if differences[0].ContractID != test.contractID {
				t.Fatalf("mutation reported against %q, want %q: %#v", differences[0].ContractID, test.contractID, differences)
			}
		})
	}
}

func TestCheckedInOracleChecksumsMatchArtifacts(t *testing.T) {
	t.Parallel()

	oracleDirectory := filepath.Join(conformanceRepositoryRoot(t), "conformance", "oracles", "django-6.1-sqlite-darwin-arm64")
	checksumBytes, err := os.ReadFile(filepath.Join(oracleDirectory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(checksumBytes)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("SHA256SUMS line %d is malformed: %q", lineNumber+1, line)
		}
		if _, exists := entries[fields[1]]; exists {
			t.Fatalf("SHA256SUMS contains duplicate path %q", fields[1])
		}
		entries[fields[1]] = fields[0]
	}
	wantedPaths := []string{"migration-execution-oracle.json", "migration-lifecycle-oracle.json", "migration-planning-oracle.json", "migration-restart-oracle.json", "migration-state-reconstruction-oracle.json", "oracle.json", "query-cache-oracle.json", "query-breadth-oracle.json", "save-lifecycle-oracle.json", "write-migration-oracle.json", "migration-definition-source-oracle.json", "migration-project-check-oracle.json", "migration-command-oracle.json", "migration-writer-oracle.json", "migration-status-oracle.json", "migration-target-plan-oracle.json", "migration-sql-rendering-oracle.json", "relation-oracle.json", "migration-relation-oracle.json", "query-expression-oracle.json", "template-form-oracle.json", "auth-session-oracle.json", "article-admin-oracle.json", "system-state.json"}
	if len(entries) != len(wantedPaths) {
		t.Fatalf("SHA256SUMS has %d entries, want %d: %#v", len(entries), len(wantedPaths), entries)
	}
	for _, name := range wantedPaths {
		contents, err := os.ReadFile(filepath.Join(oracleDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(contents))
		if entries[name] != actual {
			t.Fatalf("%s checksum = %q, want %q", name, entries[name], actual)
		}
	}
	for name, want := range map[string]string{
		"migration-planning-oracle.json":      "9586075f25edca8eb82ab553c09416060d992978f0282d730c9d4232178c6938",
		"oracle.json":                         "ba99ad6b7c1ccf68c92e272caf717d56c6aec43c0ef0f8ee4903e85d2a6b74fd",
		"query-cache-oracle.json":             "8887677d4b89cae722313d90542b0916b364bccf9c9031261ecbd71c12978104",
		"query-breadth-oracle.json":           "b1b4a981a83df9705f60e2570847353f3f216e30dedc899b995cac0b76968141",
		"query-expression-oracle.json":        "463b2b0619b6e0d19030285b2fe268877aec0693ec35ffdfb877adc4dabc634c",
		"template-form-oracle.json":           "ec3fe1dc73f919387b718dd3ee0ffea3b3a2ab7215dda5827a579109b9c2a8ab",
		"auth-session-oracle.json":            "90f8fe223bde62cc3959945cfbf00fd0df02a5d89da4026f28372e703c819d68",
		"article-admin-oracle.json":           "e718f31177bfa81b4cfe810f7cc13633b5d3465094be33e412c5ba6c3c2a6c2e",
		"system-state.json":                   "fb9370af22749c949e85b5929772144ecd03890653b76e440c56b0e459503959",
		"save-lifecycle-oracle.json":          "3d2327edc32f83f740f9dd8b5e2f64b7887ad10f07a36e51ca860213be10a4b8",
		"write-migration-oracle.json":         "04f398468251850059f85aa0550d03cd91d001056a33c2ad41e2d85361ba176f",
		"migration-status-oracle.json":        "38e121058b00b6d28303d91bbf9f0d0a5efcd434c7c0dec6bf0e7d9d462f7d20",
		"migration-target-plan-oracle.json":   "0c104b05d7882dc0d740b011b8a4e63de7cb22af6a9b0cce047cd380f2e37473",
		"migration-sql-rendering-oracle.json": "d59cff1aa58e4f8c485c3a7a087c839396626aabcddda5d9d687d4ad1c977617",
	} {
		if entries[name] != want {
			t.Fatalf("locked %s checksum changed to %q, want %q", name, entries[name], want)
		}
	}
}

func TestHistorical34ArtifactsAndFirstFiveSet57PassingStatusesRemainPinned(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	// The first three sets are the historical 34 passing contracts and remain
	// byte-for-byte immutable. Query-cache and migration-planning locked oracles
	// and static baselines also remain immutable; only each manifest hash changes
	// when its product adapter reaches 0-diff and status moves to passing.

	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets := []struct {
		name          string
		manifestName  string
		oracleName    string
		fixtureName   string
		contractCount int
	}{
		{name: "read", manifestName: "manifest.json", oracleName: "oracle.json", fixtureName: "godj-not-implemented.json", contractCount: 11},
		{name: "write-migration", manifestName: "write-migration-manifest.json", oracleName: "write-migration-oracle.json", fixtureName: "godj-write-migration-not-implemented.json", contractCount: 11},
		{name: "save-lifecycle", manifestName: "save-lifecycle-manifest.json", oracleName: "save-lifecycle-oracle.json", fixtureName: "godj-save-lifecycle-not-implemented.json", contractCount: 12},
		{name: "query-cache", manifestName: "query-cache-manifest.json", oracleName: "query-cache-oracle.json", fixtureName: "godj-query-cache-not-implemented.json", contractCount: 11},
		{name: "migration-planning", manifestName: "migration-planning-manifest.json", oracleName: "migration-planning-oracle.json", fixtureName: "godj-migration-planning-not-implemented.json", contractCount: 12},
	}
	totalContracts := 0
	for _, set := range sets {
		set := set
		totalContracts += set.contractCount
		t.Run(set.name, func(t *testing.T) {
			manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifestName))
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", set.oracleName))
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", set.fixtureName))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
				t.Fatalf("oracle does not validate: %v", err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, fixture); err != nil {
				t.Fatalf("not-implemented fixture does not validate: %v", err)
			}
			if len(manifest.Contracts) != set.contractCount {
				t.Fatalf("manifest contract count = %d, want %d", len(manifest.Contracts), set.contractCount)
			}
			for index, contract := range manifest.Contracts {
				if contract.Status != ContractPassing {
					t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
				}
				if oracle.Contracts[index].Status != StatusObserved {
					t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
				}
				if fixture.Contracts[index].Status != StatusNotImplemented {
					t.Fatalf("fixture contract %s status = %q, want %q", contract.ID, fixture.Contracts[index].Status, StatusNotImplemented)
				}
			}
		})
	}
	if totalContracts != 57 {
		t.Fatalf("pinned passing contract count = %d, want 57", totalContracts)
	}
}

func TestTwelveSetProductStatusesAre122Passing5ReviewedDeviationsAnd0OracleLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	manifestNames := []string{
		"manifest.json",
		"write-migration-manifest.json",
		"save-lifecycle-manifest.json",
		"query-cache-manifest.json",
		"migration-planning-manifest.json",
		"migration-execution-manifest.json",
		"migration-restart-manifest.json",
		"migration-state-reconstruction-manifest.json",
		"migration-lifecycle-manifest.json",
		"migration-definition-source-manifest.json",
		"migration-project-check-manifest.json",
		"relation-manifest.json",
	}
	passing := 0
	deviations := 0
	oracleLocked := 0
	for _, name := range manifestNames {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, contract := range manifest.Contracts {
			switch contract.Status {
			case ContractPassing:
				passing++
			case ContractDeviation:
				deviations++
			case ContractOracleLocked:
				oracleLocked++
			default:
				t.Fatalf("manifest %s contract %s has non-product status %q", name, contract.ID, contract.Status)
			}
		}
	}
	if passing != 122 || deviations != 5 || oracleLocked != 0 {
		t.Fatalf("twelve-set product statuses = %d passing + %d deviation + %d oracle_locked, want 122 + 5 + 0", passing, deviations, oracleLocked)
	}
}

func objectField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	if value == nil || value.Type != ValueObject {
		t.Fatalf("cannot select field %q from %#v", name, value)
	}
	for index := range value.Fields {
		if value.Fields[index].Name == name {
			return &value.Fields[index].Value
		}
	}
	available := make([]string, 0, len(value.Fields))
	for _, field := range value.Fields {
		available = append(available, field.Name)
	}
	t.Fatalf("field %q not found in %s", name, strings.Join(available, ", "))
	return nil
}
