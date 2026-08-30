package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0052DEV0002PolicySelectsExactManifestContractSet(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	lifecycle, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	lifecyclePolicy, err := deviationPolicyForProduct("DEV-0002", lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	legacyPolicy, err := deviationPolicyForDecision("DEV-0002")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lifecyclePolicy, legacyPolicy) {
		t.Fatalf("manifest-aware MIG-052 policy = %#v, want unchanged legacy policy %#v", lifecyclePolicy, legacyPolicy)
	}

	targetPlan, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-target-plan-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	targetPolicy, err := deviationPolicyForProduct("DEV-0002", targetPlan)
	if err != nil {
		t.Fatal(err)
	}
	assertGDJ0052TargetPlanPolicy(t, targetPolicy)
}

func TestGDJ0052TargetPlanPolicyAndFixtureOwnOnlyFirstThreePlanRows(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	policy := migrationTargetPlanDeviationPolicy()
	assertGDJ0052TargetPlanPolicy(t, policy)
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-target-plan-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if expectation.Decision != policy.Decision || len(expectation.Contracts) != len(policy.Contracts) {
		t.Fatalf("MIG-122 fixture/policy shape = %#v / %#v", expectation, policy)
	}
	fixtureContract := expectation.Contracts[0]
	policyContract := policy.Contracts[0]
	if fixtureContract.ID != policyContract.ID || len(fixtureContract.Changes) != len(policyContract.Changes) {
		t.Fatalf("MIG-122 fixture contract = %#v, want policy %#v", fixtureContract, policyContract)
	}
	for index, selector := range policyContract.Changes {
		change := fixtureContract.Changes[index]
		if change.Dimension != selector.Dimension || change.Path != selector.Path || change.Operation != selector.Operation {
			t.Fatalf("MIG-122 fixture selector %d = %#v, want %#v", index, change, selector)
		}
	}

	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-target-plan-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertGDJ0052TargetPlanManifestPublication(t, manifest)
	reference, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-target-plan-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	effective, product, err := protocol.PrepareDeviationExpectation(profile, manifest, reference, expectation, policy)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Contracts[3].ID != "MIG-122" || effective.Contracts[3].Status != protocol.ContractDeviation {
		t.Fatalf("effective MIG-122 contract = %#v", effective.Contracts[3])
	}
	referenceObservation := gdj0052Observation(t, reference, "MIG-122")
	productObservation := gdj0052Observation(t, product, "MIG-122")
	if !reflect.DeepEqual(referenceObservation.DBState, productObservation.DBState) || !reflect.DeepEqual(referenceObservation.Metrics, productObservation.Metrics) {
		t.Fatal("MIG-122 sparse plan deviation changed state or metrics")
	}
	for _, field := range referenceObservation.Result.Fields {
		if field.Name == "plan" {
			continue
		}
		if !reflect.DeepEqual(field.Value, *gdj0052ObjectField(t, productObservation.Result, field.Name)) {
			t.Fatalf("MIG-122 result field %q changed outside plan[0..2]", field.Name)
		}
	}
	referencePlan := gdj0052ObjectField(t, referenceObservation.Result, "plan")
	productPlan := gdj0052ObjectField(t, productObservation.Result, "plan")
	if len(referencePlan.Items) != 4 || len(productPlan.Items) != 4 {
		t.Fatalf("MIG-122 plan lengths = %d/%d, want 4/4", len(referencePlan.Items), len(productPlan.Items))
	}
	if !reflect.DeepEqual(referencePlan.Items[3], productPlan.Items[3]) {
		t.Fatal("MIG-122 policy changed unowned result.plan[3]")
	}
	want := []string{
		"alpha.0003_third/backward",
		"alpha.0002_second/backward",
		"beta.0001_direct_dependent/backward",
		"alpha.0001_initial/backward",
	}
	for index := range want {
		if got := gdj0052PlanIdentity(t, &productPlan.Items[index]); got != want[index] {
			t.Fatalf("MIG-122 product plan[%d] = %q, want %q", index, got, want[index])
		}
	}
}

func TestGDJ0052DEV0002ManifestSelectionFailsClosed(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	lifecycle, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-target-plan-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		manifest  protocol.Manifest
		wantError string
	}{
		{name: "neither registered surface", manifest: protocol.Manifest{}, wantError: "neither MIG-052 nor MIG-122"},
		{name: "partial lifecycle set", manifest: protocol.Manifest{Contracts: append([]protocol.Contract(nil), lifecycle.Contracts[:9]...)}, wantError: "exact MIG-047..MIG-056"},
		{name: "partial target-plan set", manifest: protocol.Manifest{Contracts: append([]protocol.Contract(nil), targetPlan.Contracts[:9]...)}, wantError: "exact MIG-119..MIG-128"},
		{name: "duplicate lifecycle identity", manifest: gdj0052ManifestWithDuplicate(lifecycle, 5), wantError: "exact MIG-047..MIG-056"},
		{name: "both registered surfaces", manifest: gdj0052ManifestWithContract(lifecycle, targetPlan.Contracts[3]), wantError: "ambiguous"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := deviationPolicyForProduct("DEV-0002", test.manifest); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestGDJ0052ManifestAwareDispatchLeavesOtherDecisionsUnchanged(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	for _, test := range []struct {
		decision string
		manifest string
	}{
		{decision: "DEV-0001", manifest: "migration-execution-manifest.json"},
		{decision: "DEV-0003", manifest: "template-form-manifest.json"},
		{decision: "DEV-0004", manifest: "auth-session-manifest.json"},
		{decision: "DEV-0005", manifest: "article-admin-manifest.json"},
		{decision: "DEV-0006", manifest: "parameter-routing-manifest.json"},
		{decision: "DEV-0007", manifest: "article-api-manifest.json"},
		{decision: "DEV-0008", manifest: "system-state-manifest.json"},
		{decision: "DEV-0009", manifest: "api-authentication-manifest.json"},
		{decision: "DEV-0010", manifest: "migration-writer-manifest.json"},
	} {
		test := test
		t.Run(test.decision, func(t *testing.T) {
			t.Parallel()
			manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", test.manifest))
			if err != nil {
				t.Fatal(err)
			}
			got, err := deviationPolicyForProduct(test.decision, manifest)
			if err != nil {
				t.Fatal(err)
			}
			want, err := deviationPolicyForDecision(test.decision)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s manifest-aware policy = %#v, want existing policy %#v", test.decision, got, want)
			}
		})
	}
}

func assertGDJ0052TargetPlanPolicy(t *testing.T, policy protocol.DeviationPolicy) {
	t.Helper()
	wantPaths := []string{"plan[0]", "plan[1]", "plan[2]"}
	if policy.Decision != "DEV-0002" || len(policy.Contracts) != 1 || policy.Contracts[0].ID != "MIG-122" {
		t.Fatalf("MIG-122 policy = %#v", policy)
	}
	if len(policy.Contracts[0].Changes) != len(wantPaths) {
		t.Fatalf("MIG-122 policy changes = %#v, want exactly %v", policy.Contracts[0].Changes, wantPaths)
	}
	for index, change := range policy.Contracts[0].Changes {
		if change.Dimension != protocol.DeviationResult || change.Path != wantPaths[index] || change.Operation != protocol.DeviationReplace {
			t.Fatalf("MIG-122 policy change %d = %#v", index, change)
		}
	}
}

func assertGDJ0052TargetPlanManifestPublication(t *testing.T, manifest protocol.Manifest) {
	t.Helper()
	for index := range manifest.Contracts {
		contract := manifest.Contracts[index]
		if contract.ID != "MIG-122" {
			if contract.Status != protocol.ContractPassing {
				t.Fatalf("%s status = %q, want passing", contract.ID, contract.Status)
			}
			for _, provenance := range contract.Provenance {
				if provenance.Kind == "decision" {
					t.Fatalf("passing contract %s carries decision provenance %#v", contract.ID, provenance)
				}
			}
			continue
		}
		if contract.Status != protocol.ContractDeviation {
			t.Fatalf("MIG-122 status = %q, want deviation", contract.Status)
		}
		decisionCount := 0
		for _, provenance := range contract.Provenance {
			if provenance.Kind == "decision" {
				if provenance.Reference != "DEV-0002" || provenance.Derived == nil || *provenance.Derived {
					t.Fatalf("unexpected MIG-122 decision provenance = %#v", provenance)
				}
				decisionCount++
			}
		}
		if decisionCount != 1 {
			t.Fatalf("MIG-122 decision provenance count = %d, want 1", decisionCount)
		}
		return
	}
	t.Fatal("MIG-122 is absent from target-plan manifest")
}

func gdj0052ManifestWithContract(manifest protocol.Manifest, contract protocol.Contract) protocol.Manifest {
	cloned := manifest
	cloned.Contracts = append(append([]protocol.Contract(nil), manifest.Contracts...), contract)
	return cloned
}

func gdj0052ManifestWithDuplicate(manifest protocol.Manifest, index int) protocol.Manifest {
	cloned := manifest
	cloned.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
	cloned.Contracts[len(cloned.Contracts)-1] = manifest.Contracts[index]
	return cloned
}

func gdj0052Observation(t *testing.T, suite protocol.ObservationSuite, id string) *protocol.Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == id {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("observation %s is absent", id)
	return nil
}

func gdj0052ObjectField(t *testing.T, value *protocol.Value, name string) *protocol.Value {
	t.Helper()
	if value == nil || value.Type != protocol.ValueObject {
		t.Fatalf("value for field %q = %#v, want object", name, value)
	}
	for index := range value.Fields {
		if value.Fields[index].Name == name {
			return &value.Fields[index].Value
		}
	}
	t.Fatalf("field %q is absent", name)
	return nil
}

func gdj0052PlanIdentity(t *testing.T, value *protocol.Value) string {
	t.Helper()
	app := gdj0052ObjectField(t, value, "app")
	name := gdj0052ObjectField(t, value, "name")
	direction := gdj0052ObjectField(t, value, "direction")
	if app.Type != protocol.ValueString || name.Type != protocol.ValueString || direction.Type != protocol.ValueString {
		t.Fatalf("plan row contains non-string identity = %#v", value)
	}
	return *app.Text + "." + *name.Text + "/" + *direction.Text
}
