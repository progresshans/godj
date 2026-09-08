package protocol

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPassingSaveLifecycleArtifactsKeepExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-save-lifecycle-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django save lifecycle oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj save lifecycle not-implemented baseline does not validate: %v", err)
	}
	for index, contract := range manifest.Contracts {
		if contract.Status != ContractPassing {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
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
}

func TestSaveLifecycleOraclePayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *ObservationSuite)
	}{
		{
			name:       "default save concurrent database value",
			contractID: "MOD-009",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-009")
				articles := objectField(t, observation.DBState, "articles")
				summary := objectField(t, &articles.Items[0], "summary")
				changed := "Concurrent database value"
				summary.Text = &changed
			},
		},
		{
			name:       "partial save preserves object and database divergence",
			contractID: "MOD-010",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-010")
				instance := objectField(t, observation.Result, "instance_after")
				summary := objectField(t, instance, "summary")
				changed := "Database preserved"
				summary.Text = &changed
			},
		},
		{
			name:       "empty update fields performs zero queries",
			contractID: "MOD-011",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-011")
				queryCount := objectField(t, observation.Metrics, "query_count")
				changed := "1"
				queryCount.Text = &changed
			},
		},
		{
			name:       "primary key update field error code",
			contractID: "MOD-012",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-012")
				observation.Error.Code = "changed_primary_key_error"
			},
		},
		{
			name:       "force insert conflict preserves existing row",
			contractID: "MOD-013",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-013")
				articles := objectField(t, observation.DBState, "articles")
				title := objectField(t, &articles.Items[0], "title")
				changed := "Conflicting value leaked"
				title.Text = &changed
			},
		},
		{
			name:       "missing force update executes update",
			contractID: "MOD-015",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-015")
				statements := objectField(t, observation.Metrics, "statement_kinds")
				changed := "INSERT"
				statements.Items[0].Text = &changed
			},
		},
		{
			name:       "existing explicit primary key uses update",
			contractID: "MOD-017",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-017")
				statements := objectField(t, observation.Metrics, "statement_kinds")
				changed := "INSERT"
				statements.Items[0].Text = &changed
			},
		},
		{
			name:       "missing explicit primary key updates then inserts",
			contractID: "MOD-018",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-018")
				statements := objectField(t, observation.Metrics, "statement_kinds")
				statements.Items[0], statements.Items[1] = statements.Items[1], statements.Items[0]
			},
		},
		{
			name:       "rollback leaves assigned object primary key",
			contractID: "MOD-019",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				observation := observationByID(t, suite, "MOD-019")
				created := objectField(t, observation.Result, "created_instance_after")
				primaryKey := objectField(t, created, "pk")
				changed := "99"
				primaryKey.Nested.Text = &changed
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
				t.Fatal("save lifecycle payload mutation produced a false green")
			}
			for _, difference := range differences {
				if difference.ContractID != test.contractID {
					t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, test.contractID, differences)
				}
			}
		})
	}
}

func TestSaveLifecycleArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-save-lifecycle-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, artifact := range []struct {
		name  string
		suite ObservationSuite
	}{
		{name: "oracle", suite: oracle},
		{name: "not-implemented baseline", suite: baseline},
	} {
		artifact := artifact
		t.Run(artifact.name+" order", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0], changed.Contracts[1] = changed.Contracts[1], changed.Contracts[0]
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("contract reordering produced a false green")
			}
		})
		t.Run(artifact.name+" phase", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Phase = differentPhase(changed.Contracts[0].Phase)
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("profile mutation produced a false green")
			}
		})
	}
}

func conformanceRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate protocol test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}

func differentPhase(phase Phase) Phase {
	if phase == PhaseEnvironment {
		return PhaseMetadata
	}
	return PhaseEnvironment
}
