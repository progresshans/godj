package protocol

import (
	"path/filepath"
	"testing"
)

func TestQueryCacheArtifactsHaveExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadQueryCacheArtifacts(t)
	if len(manifest.Contracts) != 11 {
		t.Fatalf("query-cache manifest has %d contracts, want 11", len(manifest.Contracts))
	}
	if manifest.Contracts[0].ID != "QRY-011" || manifest.Contracts[10].ID != "QRY-021" {
		t.Fatalf("query-cache manifest boundary IDs = %s..%s, want QRY-011..QRY-021", manifest.Contracts[0].ID, manifest.Contracts[10].ID)
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django query-cache oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj query-cache not-implemented baseline does not validate: %v", err)
	}
	for index, contract := range manifest.Contracts {
		if contract.Status != ContractOracleLocked {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractOracleLocked)
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

func TestQueryCacheOraclePayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadQueryCacheArtifacts(t)
	for _, contract := range manifest.Contracts {
		contract := contract
		t.Run(contract.ID+" query count", func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := queryCacheObservation(t, &actual, contract.ID)
			steps := objectField(t, observation.Metrics, "steps")
			if steps.Type != ValueList || len(steps.Items) == 0 {
				t.Fatalf("%s metric steps must be a non-empty list: %#v", contract.ID, steps)
			}
			queryCount := objectField(t, &steps.Items[0], "query_count")
			changed := "999999"
			queryCount.Text = &changed
			assertQueryCacheMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
		})
	}

	resultMutations := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "repeated evaluation cached rows",
			contractID: "QRY-011",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "second_full_evaluation") = List()
			},
		},
		{
			name:       "empty evaluation remains empty after insert",
			contractID: "QRY-012",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "same_queryset_after_matching_insert") = List(String("unexpected row"))
			},
		},
		{
			name:       "evaluated source keeps stale snapshot",
			contractID: "QRY-013",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "source_after_insert") = List()
			},
		},
		{
			name:       "derived queryset sees independent result",
			contractID: "QRY-014",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "derived_after_insert") = List()
			},
		},
		{
			name:       "count reuses full cache value",
			contractID: "QRY-015",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "count_from_full_cache") = Integer("4")
			},
		},
		{
			name:       "exists reuses full cache value",
			contractID: "QRY-016",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "exists_from_full_cache") = Boolean(false)
			},
		},
		{
			name:       "iterator sees new row",
			contractID: "QRY-017",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "iterator_after_insert") = List()
			},
		},
		{
			name:       "index reuses the full cache after second insert",
			contractID: "QRY-018",
			mutate: func(t *testing.T, observation *Observation) {
				row := queryCacheResultStepValue(t, observation, "index_from_full_cache_after_second_insert")
				primaryKey := objectField(t, row, "id")
				changed := "6"
				primaryKey.Nested.Text = &changed
			},
		},
		{
			name:       "all clone sees new row",
			contractID: "QRY-020",
			mutate: func(t *testing.T, observation *Observation) {
				*queryCacheResultStepValue(t, observation, "clone_after_insert") = List()
			},
		},
		{
			name:       "first reuses the full cache after second insert",
			contractID: "QRY-021",
			mutate: func(t *testing.T, observation *Observation) {
				row := queryCacheResultStepValue(t, observation, "first_from_full_cache_after_second_insert")
				primaryKey := objectField(t, row, "id")
				changed := "6"
				primaryKey.Nested.Text = &changed
			},
		},
	}
	for _, test := range resultMutations {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := queryCacheObservation(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertQueryCacheMutationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}

	t.Run("failed evaluation retry error code", func(t *testing.T) {
		actual := cloneSuite(t, oracle)
		observation := queryCacheObservation(t, &actual, "QRY-019")
		step := queryCacheResultStep(t, observation, "failed_evaluation")
		errorValue := objectField(t, step, "error")
		code := objectField(t, errorValue, "code")
		changed := "different_error"
		code.Text = &changed
		assertQueryCacheMutationDiffers(t, profile, manifest, oracle, actual, "QRY-019")
	})
}

func TestQueryCacheArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadQueryCacheArtifacts(t)
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

func loadQueryCacheArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-cache-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-query-cache-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func queryCacheObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("query-cache observation %s is missing", contractID)
	return nil
}

func assertQueryCacheMutationDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("query-cache payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func queryCacheResultStep(t *testing.T, observation *Observation, name string) *Value {
	t.Helper()
	steps := objectField(t, observation.Result, "steps")
	if steps.Type != ValueList {
		t.Fatalf("query-cache result steps = %#v, want list", steps)
	}
	for index := range steps.Items {
		step := &steps.Items[index]
		stepName := objectField(t, step, "name")
		if stepName.Type == ValueString && stepName.Text != nil && *stepName.Text == name {
			return step
		}
	}
	t.Fatalf("query-cache result step %q is missing", name)
	return nil
}

func queryCacheResultStepValue(t *testing.T, observation *Observation, name string) *Value {
	t.Helper()
	return objectField(t, queryCacheResultStep(t, observation, name), "value")
}
