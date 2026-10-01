package protocol

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQueryExpressionReferenceBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "query-expression")
	wantScenarios := []string{
		"django.query.expression.scalar_exact_or",
		"django.query.expression.escaped_ascii_icontains_or",
		"django.query.expression.grouped_or_and_reuse",
		"django.query.expression.nonnull_scalar_not",
		"django.query.expression.nullable_negation_truth_table",
		"django.query.expression.implicit_filter_and",
		"django.query.expression.nested_connector_order_and_source_independence",
		"django.query.expression.composite_distinct_stable_page",
		"django.query.expression.projection_outside_predicate",
		"django.query.expression.composite_count_max",
		"django.query.expression.integer_gt_literal_boundary",
		"django.query.expression.integer_gte_literal_boundary",
		"django.query.expression.integer_lt_literal_boundary",
		"django.query.expression.integer_lte_literal_boundary",
		"django.query.expression.range_composition_negation_and_reuse",
		"django.query.expression.same_field_reference_boundaries",
		"django.query.expression.same_model_field_reference_and_nullable_negation",
		"django.query.expression.nullable_ordering_negation_truth_table",
		"django.query.expression.field_reference_stable_projection",
		"django.query.expression.field_reference_count_max",
	}
	wantComparison := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	if len(manifest.Contracts) != len(wantScenarios) || len(oracle.Contracts) != len(wantScenarios) || len(baseline.Contracts) != len(wantScenarios) {
		t.Fatalf("query-expression lengths = manifest %d/oracle %d/static %d, want %d", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts), len(wantScenarios))
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("QRY-%03d", index+34)
		if contract.ID != wantID || contract.Scenario != wantScenarios[index] {
			t.Fatalf("contract %d = %s/%s, want %s/%s", index, contract.ID, contract.Scenario, wantID, wantScenarios[index])
		}
		if contract.Status != ContractPassing {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if contract.Phase != PhaseEvaluation {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, PhaseEvaluation)
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
		}
		assertQueryExpressionProvenance(t, index, contract)

		observation := oracle.Contracts[index]
		if observation.ID != wantID || observation.Status != StatusObserved || observation.Phase != PhaseEvaluation {
			t.Fatalf("oracle contract %d = %#v, want %s observed/evaluation", index, observation, wantID)
		}
		if observation.Result == nil || observation.DBState == nil || observation.Metrics == nil || observation.Error != nil {
			t.Fatalf("oracle contract %s does not have exactly result/db_state/metrics payloads: %#v", observation.ID, observation)
		}
		static := baseline.Contracts[index]
		if static.ID != wantID || static.Status != StatusNotImplemented || static.Phase != PhaseEvaluation {
			t.Fatalf("static contract %d = %#v, want %s not_implemented/evaluation", index, static, wantID)
		}
		if static.Result != nil || static.Error != nil || static.DBState != nil || static.Metrics != nil {
			t.Fatalf("static contract %s contains product payloads: %#v", static.ID, static)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("query-expression oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("query-expression static fixture does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(manifest.Contracts) {
		t.Fatalf("oracle/static differences = %d, want %d: %#v", len(differences), len(manifest.Contracts), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("difference %d = %#v, want ordered observed/not_implemented status mismatch", index, difference)
		}
	}
}

func TestGDJ0043ProductPublicationYieldsCurrentStatusAggregate(t *testing.T) {
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
		"query-breadth-manifest.json",
		"query-expression-manifest.json",
		"migration-relation-manifest.json",
		"template-form-manifest.json",
		"auth-session-manifest.json",
		"article-admin-manifest.json",
	}
	passing, deviations, oracleLocked := 0, 0, 0
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
				t.Fatalf("manifest %s contract %s has unexpected status %q", name, contract.ID, contract.Status)
			}
		}
	}
	if passing != 179 || deviations != 10 || oracleLocked != 12 {
		t.Fatalf("current reference statuses = %d passing + %d deviation + %d oracle_locked, want 179 + 10 + 12", passing, deviations, oracleLocked)
	}
}

func TestQueryExpressionDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "query-expression")
	for index, contract := range manifest.Contracts {
		index, contract := index, contract
		for _, dimension := range contract.Comparison {
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				observation := &actual.Contracts[index]
				var changed bool
				switch dimension {
				case CompareResult:
					changed = mutateFirstScalar(observation.Result)
				case CompareDBState:
					changed = mutateFirstScalar(observation.DBState)
				case CompareMetrics:
					changed = mutateFirstScalar(observation.Metrics)
				}
				if !changed {
					t.Fatalf("contract %s declared %s without a mutable payload", contract.ID, dimension)
				}
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestQueryExpressionArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "query-expression")
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
				t.Fatal("query-expression contract reordering produced a false green")
			}
		})
		t.Run(artifact.name+" phase", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Phase = PhaseConstruction
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-expression phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-expression profile mutation produced a false green")
			}
		})
	}
}

type queryExpressionProvenanceSpec struct {
	kind      string
	reference string
	license   string
}

func assertQueryExpressionProvenance(t *testing.T, index int, contract Contract) {
	t.Helper()
	wanted := [][]queryExpressionProvenanceSpec{
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#complex-lookups-with-q", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/or_lookups/tests.py::OrLookupsTests.test_filter_or", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#icontains", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/lookup/tests.py::LookupTests.test_escaping", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#complex-lookups-with-q", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/or_lookups/tests.py::OrLookupsTests.test_q_negated", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#complex-lookups-with-q", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/or_lookups/tests.py::OrLookupsTests.test_q_negated", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/sql/query.py::Query.build_filter", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/lookup/tests.py::LookupTests.test_isnull_textfield", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#complex-lookups-with-q", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/or_lookups/tests.py::OrLookupsTests.test_q_and", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/query_utils.py::Q._combine", license: "BSD-3-Clause"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filtered-querysets-are-unique", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#distinct", license: "BSD-3-Clause"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#limiting-querysets", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#values", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/lookup/tests.py::LookupTests.test_values_list_filter_and_no_fields", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0040"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/aggregation.txt#generating-aggregates-over-a-queryset", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/aggregation/tests.py::AggregateTestCase.test_multiple_aggregates", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#gt", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/lookups.py::GreaterThan", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#gte", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/lookups.py::GreaterThanOrEqual", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#lt", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/lookups.py::LessThan", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#lte", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/lookups.py::LessThanOrEqual", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#complex-lookups-with-q", license: "BSD-3-Clause"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filtered-querysets-are-unique", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/or_lookups/tests.py::OrLookupsTests.test_q_and", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filters-can-reference-fields-on-the-model", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/expressions/tests.py::BasicExpressionsTests.test_filter_inter_attribute", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filters-can-reference-fields-on-the-model", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/sql/query.py::Query.build_filter", license: "BSD-3-Clause"},
			{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/queries/tests.py::ExcludeTests.test_exclude_nullable_fields", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#gt", license: "BSD-3-Clause"},
			{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/models/sql/query.py::Query.build_filter", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filters-can-reference-fields-on-the-model", license: "BSD-3-Clause"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/ref/models/querysets.txt#values", license: "BSD-3-Clause"},
		},
		{
			{kind: "decision", reference: "ADR-0041"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/aggregation.txt#generating-aggregates-over-a-queryset", license: "BSD-3-Clause"},
			{kind: "documentation", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:docs/topics/db/queries.txt#filters-can-reference-fields-on-the-model", license: "BSD-3-Clause"},
		},
	}
	want := wanted[index]
	if len(contract.Provenance) != len(want) {
		t.Fatalf("contract %s provenance length = %d, want %d", contract.ID, len(contract.Provenance), len(want))
	}
	for provenanceIndex, got := range contract.Provenance {
		spec := want[provenanceIndex]
		if got.Kind != spec.kind || got.Reference != spec.reference || got.License != spec.license || got.Derived == nil || *got.Derived {
			t.Fatalf("contract %s provenance %d = %#v, want kind=%q reference=%q license=%q derived=false", contract.ID, provenanceIndex, got, spec.kind, spec.reference, spec.license)
		}
	}
}
