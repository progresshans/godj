package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteMigrationArtifactsHaveExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate protocol test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
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

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate protocol test source")
	}
	oracleDirectory := filepath.Join(filepath.Dir(source), "..", "..", "oracles", "django-6.1-sqlite-darwin-arm64")
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
	wantedPaths := []string{"migration-execution-oracle.json", "migration-lifecycle-oracle.json", "migration-planning-oracle.json", "migration-restart-oracle.json", "migration-state-reconstruction-oracle.json", "oracle.json", "query-cache-oracle.json", "save-lifecycle-oracle.json", "write-migration-oracle.json", "migration-definition-source-oracle.json", "migration-project-check-oracle.json", "relation-oracle.json"}
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
		"migration-planning-oracle.json": "7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474",
		"oracle.json":                    "e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869",
		"query-cache-oracle.json":        "d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682",
		"save-lifecycle-oracle.json":     "05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb",
		"write-migration-oracle.json":    "35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac",
	} {
		if entries[name] != want {
			t.Fatalf("existing %s checksum changed to %q, want immutable baseline %q", name, entries[name], want)
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
	expectedHashes := map[string]string{
		"conformance/contracts/manifest.json":                                            "e395fc862d357b7d45f94fa7d2d15f5a5dfdf8c353db958adc280fd64870b874",
		"conformance/contracts/query-cache-manifest.json":                                "35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2",
		"conformance/contracts/save-lifecycle-manifest.json":                             "6f215f6aee153954dee84d0571cc28529c2d50ee31ee2b9755733db3f9762905",
		"conformance/contracts/write-migration-manifest.json":                            "b0ba235cb8b83e9b595b2ad3230ea7440d8b6ea74789de27c8a1f6625ecd05bb",
		"conformance/fixtures/godj-not-implemented.json":                                 "f02ea4e01e0ffcc9195d56d69129c5def0591cbcdcb5b07a62d2ec7395fa7874",
		"conformance/fixtures/godj-query-cache-not-implemented.json":                     "5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87",
		"conformance/fixtures/godj-save-lifecycle-not-implemented.json":                  "5ece667fe6babef5d01059ba4166e1243946176f9672119ae45f4c39c440c726",
		"conformance/fixtures/godj-write-migration-not-implemented.json":                 "c565c877278032637b75f99c9490c5e7e02169c8730628069533f16da6d8e707",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json":                 "e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json":     "d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json":  "05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json": "35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac",
	}
	for name, want := range expectedHashes {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("existing artifact %s checksum changed to %q, want immutable baseline %q", name, got, want)
		}
	}

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

func TestTwelveSetProductStatusesAre111Passing5ReviewedDeviationsAnd11OracleLocked(t *testing.T) {
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
	if passing != 111 || deviations != 5 || oracleLocked != 11 {
		t.Fatalf("twelve-set product statuses = %d passing + %d deviation + %d oracle_locked, want 111 + 5 + 11", passing, deviations, oracleLocked)
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
