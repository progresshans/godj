package migrations

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	alpha1 = MigrationKey{App: "alpha", Name: "0001_initial"}
	alpha2 = MigrationKey{App: "alpha", Name: "0002_second"}
	alpha3 = MigrationKey{App: "alpha", Name: "0003_third"}
	beta1  = MigrationKey{App: "beta", Name: "0001_initial"}
	beta2  = MigrationKey{App: "beta", Name: "0002_second"}
	gamma1 = MigrationKey{App: "gamma", Name: "0001_initial"}
	shared = MigrationKey{App: "shared", Name: "0001_initial"}
)

func TestPlannerMigrationPlanningContracts(t *testing.T) {
	t.Parallel()

	linear := mustPlanner(t,
		migration(alpha1),
		migration(alpha2, alpha1),
		migration(alpha3, alpha2),
	)
	cross := mustPlanner(t,
		migration(alpha1),
		migration(alpha2, alpha1),
		migration(beta1, alpha2),
		migration(beta2, beta1),
	)

	t.Run("MIG-005 linear forward", func(t *testing.T) {
		assertPlan(t, plan(t, linear, mustApplied(t), NamedTarget(alpha3)),
			forward(alpha1), forward(alpha2), forward(alpha3))
	})

	t.Run("MIG-006 applied pruning", func(t *testing.T) {
		assertPlan(t, plan(t, linear, mustApplied(t, alpha1), NamedTarget(alpha3)),
			forward(alpha2), forward(alpha3))
		assertPlan(t, plan(t, linear, mustApplied(t, alpha1, alpha2, alpha3), NamedTarget(alpha3)))
	})

	t.Run("MIG-007 missing target", func(t *testing.T) {
		missing := MigrationKey{App: "alpha", Name: "9999_missing"}
		_, err := linear.Plan(mustApplied(t), NamedTarget(missing))
		assertPlanningError(t, err, CategoryPlan, CodeTargetNotFound, missing, MigrationKey{})
	})

	t.Run("MIG-008 prior target rollback", func(t *testing.T) {
		assertPlan(t, plan(t, linear, mustApplied(t, alpha1, alpha2, alpha3), NamedTarget(alpha1)),
			backward(alpha3), backward(alpha2))
	})

	t.Run("MIG-009 zero with cross app dependents", func(t *testing.T) {
		assertPlan(t, plan(t, cross, mustApplied(t, alpha1, alpha2, beta1, beta2), ZeroTarget("alpha")),
			backward(beta2), backward(beta1), backward(alpha2), backward(alpha1))
	})

	t.Run("MIG-010 cross app forward", func(t *testing.T) {
		assertPlan(t, plan(t, cross, mustApplied(t), NamedTarget(beta2)),
			forward(alpha1), forward(alpha2), forward(beta1), forward(beta2))
	})

	t.Run("MIG-011 cross app backward", func(t *testing.T) {
		assertPlan(t, plan(t, cross, mustApplied(t, alpha1, alpha2, beta1, beta2), NamedTarget(alpha1)),
			backward(beta2), backward(beta1), backward(alpha2))
	})

	t.Run("MIG-012 ordered targets and shared dependency", func(t *testing.T) {
		planner := mustPlanner(t, migration(shared), migration(alpha1, shared), migration(beta1, shared))
		assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(alpha1), NamedTarget(beta1)),
			forward(shared), forward(alpha1), forward(beta1))
	})

	t.Run("MIG-013 retain unrelated branches", func(t *testing.T) {
		planner := mustPlanner(t,
			migration(alpha1), migration(alpha2, alpha1), migration(alpha3, alpha2),
			migration(beta1, alpha1), migration(gamma1),
		)
		assertPlan(t, plan(t, planner, mustApplied(t, alpha1, alpha2, alpha3, beta1, gamma1), NamedTarget(alpha1)),
			backward(alpha3), backward(alpha2))
	})

	t.Run("MIG-014 inconsistent history preflight", func(t *testing.T) {
		_, err := linear.Plan(mustApplied(t, alpha2))
		assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)
	})

	t.Run("MIG-015 missing dependency", func(t *testing.T) {
		missing := MigrationKey{App: "alpha", Name: "0001_missing"}
		_, err := NewPlanner(migration(alpha2, missing))
		assertPlanningError(t, err, CategoryGraph, CodeDependencyNotFound, alpha2, missing)
	})

	t.Run("MIG-016 dependency cycle", func(t *testing.T) {
		_, err := NewPlanner(migration(alpha1, beta1), migration(beta1, alpha1))
		planningError := assertPlanningError(t, err, CategoryGraph, CodeDependencyCycle, MigrationKey{}, MigrationKey{})
		if got, want := planningError.Members(), []MigrationKey{alpha1, beta1}; !reflect.DeepEqual(got, want) {
			t.Fatalf("cycle members = %v, want %v", got, want)
		}
	})
}

func TestPlannerValidationPrecedenceAndDeterministicDiagnostics(t *testing.T) {
	t.Parallel()

	invalidNode := MigrationKey{App: "", Name: "bad"}
	invalidParent := MigrationKey{App: "alpha", Name: ""}
	missing := MigrationKey{App: "missing", Name: "0001"}

	tests := []struct {
		name       string
		migrations []Migration
		category   ErrorCategory
		code       ErrorCode
		node       MigrationKey
		related    MigrationKey
	}{
		{
			name: "invalid node before duplicate node and invalid edge",
			migrations: []Migration{
				migration(alpha1), migration(alpha1), migration(invalidNode, invalidParent),
			},
			category: CategoryGraph, code: CodeInvalidNode, node: invalidNode,
		},
		{
			name: "duplicate node before invalid edge",
			migrations: []Migration{
				migration(alpha1), migration(alpha1), migration(alpha2, invalidParent),
			},
			category: CategoryGraph, code: CodeDuplicateNode, node: alpha1,
		},
		{
			name: "invalid dependency before duplicate dependency",
			migrations: []Migration{
				migration(alpha1), migration(alpha2, alpha1, alpha1, invalidParent),
			},
			category: CategoryGraph, code: CodeInvalidDependency, node: alpha2, related: invalidParent,
		},
		{
			name: "duplicate dependency before missing dependency",
			migrations: []Migration{
				migration(alpha1), migration(alpha2, alpha1, alpha1, missing),
			},
			category: CategoryGraph, code: CodeDuplicateDependency, node: alpha2, related: alpha1,
		},
		{
			name: "missing dependency before cycle",
			migrations: []Migration{
				migration(alpha1, beta1), migration(beta1, alpha1), migration(alpha2, missing),
			},
			category: CategoryGraph, code: CodeDependencyNotFound, node: alpha2, related: missing,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, migrations := range [][]Migration{test.migrations, reversedMigrations(test.migrations)} {
				_, err := NewPlanner(migrations...)
				assertPlanningError(t, err, test.category, test.code, test.node, test.related)
			}
		})
	}
}

func TestPlannerValidationDiagnosticsAreLexicographicWithinClass(t *testing.T) {
	t.Parallel()

	invalidA := MigrationKey{App: "", Name: "a"}
	invalidZ := MigrationKey{App: "", Name: "z"}
	tests := []struct {
		name       string
		migrations []Migration
		code       ErrorCode
		node       MigrationKey
		related    MigrationKey
	}{
		{
			name:       "invalid node",
			migrations: []Migration{migration(invalidZ), migration(invalidA)},
			code:       CodeInvalidNode,
			node:       invalidA,
		},
		{
			name:       "duplicate node",
			migrations: []Migration{migration(beta1), migration(alpha1), migration(beta1), migration(alpha1)},
			code:       CodeDuplicateNode,
			node:       alpha1,
		},
		{
			name: "invalid dependency",
			migrations: []Migration{
				migration(alpha2, invalidZ, invalidA),
			},
			code:    CodeInvalidDependency,
			node:    alpha2,
			related: invalidA,
		},
		{
			name: "duplicate dependency",
			migrations: []Migration{
				migration(alpha1), migration(beta1),
				migration(alpha2, beta1, beta1, alpha1, alpha1),
			},
			code:    CodeDuplicateDependency,
			node:    alpha2,
			related: alpha1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, input := range [][]Migration{test.migrations, reversedMigrations(test.migrations)} {
				_, err := NewPlanner(input...)
				assertPlanningError(t, err, CategoryGraph, test.code, test.node, test.related)
			}
		})
	}
}

func TestAppliedStateValidationPrecedenceAndPermutation(t *testing.T) {
	t.Parallel()

	invalidA := MigrationKey{App: "", Name: "z"}
	invalidB := MigrationKey{App: "a", Name: ""}
	for _, keys := range [][]MigrationKey{
		{alpha2, alpha2, invalidB, invalidA},
		{invalidA, invalidB, alpha2, alpha2},
	} {
		_, err := NewAppliedState(keys...)
		assertPlanningError(t, err, CategoryHistory, CodeInvalidAppliedState, invalidA, MigrationKey{})
	}
	for _, keys := range [][]MigrationKey{{alpha2, alpha1, alpha2, alpha1}, {alpha1, alpha2, alpha1, alpha2}} {
		_, err := NewAppliedState(keys...)
		assertPlanningError(t, err, CategoryHistory, CodeDuplicateApplied, alpha1, MigrationKey{})
	}
}

func TestPlannerTargetAndHistoryPrecedence(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	inconsistent := mustApplied(t, alpha2)
	missing := MigrationKey{App: "alpha", Name: "9999_missing"}

	_, err := planner.Plan(inconsistent, Target{}, NamedTarget(missing))
	assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})

	_, err = planner.Plan(inconsistent, NamedTarget(missing))
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)

	_, err = planner.Plan(inconsistent)
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)

	_, err = planner.Plan(mustApplied(t), NamedTarget(missing))
	assertPlanningError(t, err, CategoryPlan, CodeTargetNotFound, missing, MigrationKey{})
}

func TestPlannerCheckHistoryIsExplicitAndPreservesPlanValidation(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	inconsistent := mustApplied(t, alpha2)

	err := planner.CheckHistory(inconsistent)
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)

	// Plan retains its independent history defense even when the caller does
	// not invoke the explicit startup check.
	_, err = planner.Plan(inconsistent, NamedTarget(alpha2))
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)

	// Explicit callers can check history before target validation/planning.
	err = planner.CheckHistory(inconsistent)
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, alpha2, alpha1)
	_, err = planner.Plan(mustApplied(t), Target{})
	assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
}

func TestPlannerCheckHistoryAcceptsZeroAndUnknownAppliedRecords(t *testing.T) {
	t.Parallel()

	var zeroPlanner Planner
	if err := zeroPlanner.CheckHistory(AppliedState{}); err != nil {
		t.Fatalf("zero Planner.CheckHistory() error = %v", err)
	}

	unknown := MigrationKey{App: "legacy", Name: "0009_removed"}
	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	if err := planner.CheckHistory(mustApplied(t, unknown)); err != nil {
		t.Fatalf("CheckHistory(unknown) error = %v", err)
	}
	assertPlan(t, plan(t, planner, mustApplied(t, unknown), NamedTarget(alpha2)), forward(alpha1), forward(alpha2))
}

func TestPlannerCheckHistoryRepeatedAndConcurrentCallsAreImmutable(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1), migration(alpha3, alpha2))
	applied := mustApplied(t, alpha1, alpha2)
	for iteration := 0; iteration < 100; iteration++ {
		if err := planner.CheckHistory(applied); err != nil {
			t.Fatalf("CheckHistory() iteration %d error = %v", iteration, err)
		}
	}

	const workers = 64
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				if err := planner.CheckHistory(applied); err != nil {
					errorsChannel <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
}

func TestPlannerHistoryDiagnosticsChooseLexicographicChildParentAcrossPermutations(t *testing.T) {
	t.Parallel()

	parentA := MigrationKey{App: "alpha", Name: "0001_parent"}
	childA := MigrationKey{App: "alpha", Name: "0002_child"}
	parentB := MigrationKey{App: "beta", Name: "0001_parent"}
	childB := MigrationKey{App: "beta", Name: "0002_child"}
	base := []Migration{
		migration(parentA),
		migration(parentB),
		migration(childA, parentB, parentA),
		migration(childB, parentB),
	}

	for _, graphInput := range [][]Migration{base, reversedMigrations(base)} {
		for _, appliedInput := range [][]MigrationKey{{childA, childB}, {childB, childA}} {
			planner := mustPlanner(t, graphInput...)
			_, err := planner.Plan(mustApplied(t, appliedInput...))
			assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, childA, parentA)
		}
	}
}

func TestPlannerRejectsInvalidTargetRepresentationsInCallerOrder(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t, migration(alpha1))
	tests := []struct {
		name   string
		target Target
		node   MigrationKey
	}{
		{name: "zero value", target: Target{}},
		{name: "empty named app", target: NamedTarget(MigrationKey{Name: "0001"}), node: MigrationKey{Name: "0001"}},
		{name: "empty named name", target: NamedTarget(MigrationKey{App: "alpha"}), node: MigrationKey{App: "alpha"}},
		{name: "empty zero app", target: ZeroTarget("")},
		{name: "corrupt named tag", target: Target{kind: targetNamed, key: alpha1, app: "alpha"}, node: alpha1},
		{name: "corrupt zero tag", target: Target{kind: targetZero, key: alpha1, app: "alpha"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := planner.Plan(mustApplied(t), test.target)
			assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, test.node, MigrationKey{})
		})
	}

	_, err := planner.Plan(mustApplied(t), NamedTarget(alpha1), Target{}, ZeroTarget(""))
	assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
}

func TestPlannerCycleDiagnosticsUseLexicographicSCCAndCloneMembers(t *testing.T) {
	t.Parallel()

	cycleA1 := MigrationKey{App: "a", Name: "0001"}
	cycleA2 := MigrationKey{App: "a", Name: "0002"}
	cycleB1 := MigrationKey{App: "b", Name: "0001"}
	cycleB2 := MigrationKey{App: "b", Name: "0002"}
	downstream := MigrationKey{App: "a", Name: "9999_downstream"}
	migrations := []Migration{
		migration(cycleB2, cycleB1),
		migration(downstream, cycleA2),
		migration(cycleA2, cycleA1),
		migration(cycleB1, cycleB2),
		migration(cycleA1, cycleA2),
	}

	for _, input := range [][]Migration{migrations, reversedMigrations(migrations)} {
		_, err := NewPlanner(input...)
		planningError := assertPlanningError(t, err, CategoryGraph, CodeDependencyCycle, MigrationKey{}, MigrationKey{})
		want := []MigrationKey{cycleA1, cycleA2}
		members := planningError.Members()
		if !reflect.DeepEqual(members, want) {
			t.Fatalf("Members() = %v, want %v", members, want)
		}
		members[0] = downstream
		if got := planningError.Members(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Members() after caller mutation = %v, want %v", got, want)
		}
	}
}

func TestPlannerSelfDependencyIsSingletonCycle(t *testing.T) {
	t.Parallel()

	_, err := NewPlanner(migration(alpha1, alpha1))
	planningError := assertPlanningError(t, err, CategoryGraph, CodeDependencyCycle, MigrationKey{}, MigrationKey{})
	if got, want := planningError.Members(), []MigrationKey{alpha1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Members() = %v, want %v", got, want)
	}
}

func TestPlannerDeepCopiesDependencySlicesAndIgnoresOperations(t *testing.T) {
	t.Parallel()

	dependencies := []MigrationKey{alpha1}
	migrationWithAlias := migration(alpha2, dependencies...)
	migrationWithAlias.Dependencies = dependencies
	planner := mustPlanner(t, migration(alpha1), migrationWithAlias)
	dependencies[0] = beta1
	migrationWithAlias.Dependencies = append(migrationWithAlias.Dependencies, beta1)
	migrationWithAlias.Operations = []Operation{nil}

	assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(alpha2)), forward(alpha1), forward(alpha2))
}

func TestAppliedStateCopiesCallerKeySlice(t *testing.T) {
	t.Parallel()

	keys := []MigrationKey{alpha1}
	applied, err := NewAppliedState(keys...)
	if err != nil {
		t.Fatalf("NewAppliedState() error = %v", err)
	}
	keys[0] = beta1
	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	assertPlan(t, plan(t, planner, applied, NamedTarget(alpha2)), forward(alpha2))
}

func TestPlannerInputPermutationsProduceSamePlanAndError(t *testing.T) {
	t.Parallel()

	base := []Migration{
		migration(shared),
		migration(alpha1, shared),
		migration(beta1, shared),
		migration(alpha2, alpha1, beta1),
	}
	want := []PlanStep{forward(shared), forward(alpha1), forward(beta1), forward(alpha2)}
	random := rand.New(rand.NewSource(4217))
	for iteration := 0; iteration < 100; iteration++ {
		input := cloneMigrations(base)
		random.Shuffle(len(input), func(left, right int) { input[left], input[right] = input[right], input[left] })
		for index := range input {
			random.Shuffle(len(input[index].Dependencies), func(left, right int) {
				input[index].Dependencies[left], input[index].Dependencies[right] = input[index].Dependencies[right], input[index].Dependencies[left]
			})
		}
		planner := mustPlanner(t, input...)
		assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(alpha2)), want...)
	}

	missingA := MigrationKey{App: "missing", Name: "0001"}
	missingB := MigrationKey{App: "missing", Name: "0002"}
	for _, input := range [][]Migration{
		{migration(beta1, missingB), migration(alpha2, missingA)},
		{migration(alpha2, missingA), migration(beta1, missingB)},
	} {
		_, err := NewPlanner(input...)
		assertPlanningError(t, err, CategoryGraph, CodeDependencyNotFound, alpha2, missingA)
	}
}

func TestPlannerCanonicalSiblingOrderAndCallerTargetOrder(t *testing.T) {
	t.Parallel()

	parentA := MigrationKey{App: "a", Name: "0001"}
	parentB := MigrationKey{App: "b", Name: "0001"}
	target := MigrationKey{App: "z", Name: "0001"}
	planner := mustPlanner(t, migration(target, parentB, parentA), migration(parentB), migration(parentA))
	assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(target)),
		forward(parentA), forward(parentB), forward(target))

	sharedPlanner := mustPlanner(t, migration(shared), migration(alpha1, shared), migration(beta1, shared))
	assertPlan(t, plan(t, sharedPlanner, mustApplied(t), NamedTarget(beta1), NamedTarget(alpha1)),
		forward(shared), forward(beta1), forward(alpha1))
}

func TestPlannerReevaluatesCanonicalReadySetAfterEveryStep(t *testing.T) {
	t.Parallel()

	unlockingParent := MigrationKey{App: "a", Name: "0001"}
	newlyReady := MigrationKey{App: "b", Name: "0001"}
	alreadyReady := MigrationKey{App: "z", Name: "0001"}
	target := MigrationKey{App: "zz", Name: "0001"}
	planner := mustPlanner(t,
		migration(unlockingParent),
		migration(newlyReady, unlockingParent),
		migration(alreadyReady),
		migration(target, newlyReady, alreadyReady),
	)
	assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(target)),
		forward(unlockingParent), forward(newlyReady), forward(alreadyReady), forward(target))
}

func TestPlannerReevaluatesCanonicalBackwardReadySetAfterEveryStep(t *testing.T) {
	t.Parallel()

	root := MigrationKey{App: "core", Name: "0001_root"}
	unlockedAfterFirstLeaf := MigrationKey{App: "a", Name: "0001"}
	firstLeaf := MigrationKey{App: "b", Name: "0001"}
	alreadyReadyLeaf := MigrationKey{App: "z", Name: "0001"}
	planner := mustPlanner(t,
		migration(root),
		migration(unlockedAfterFirstLeaf, root),
		migration(firstLeaf, unlockedAfterFirstLeaf),
		migration(alreadyReadyLeaf, root),
	)
	applied := mustApplied(t, root, unlockedAfterFirstLeaf, firstLeaf, alreadyReadyLeaf)
	assertPlan(t, plan(t, planner, applied, ZeroTarget("core")),
		backward(firstLeaf), backward(unlockedAfterFirstLeaf), backward(alreadyReadyLeaf), backward(root))
}

func TestPlannerNamedBackwardSeedsUseSequentialClosures(t *testing.T) {
	t.Parallel()

	root := MigrationKey{App: "core", Name: "0000_root"}
	child1 := MigrationKey{App: "core", Name: "0001_first"}
	child2 := MigrationKey{App: "core", Name: "0002_second"}
	firstDescendant := MigrationKey{App: "zeta", Name: "0001"}
	secondDescendant := MigrationKey{App: "aardvark", Name: "0001"}
	planner := mustPlanner(t,
		migration(root),
		migration(child1, root),
		migration(child2, root),
		migration(firstDescendant, child1),
		migration(secondDescendant, child2),
	)
	applied := mustApplied(t, root, child1, child2, firstDescendant, secondDescendant)
	assertPlan(t, plan(t, planner, applied, NamedTarget(root)),
		backward(firstDescendant), backward(child1), backward(secondDescendant), backward(child2))
}

func TestPlannerZeroSeedsUseSequentialClosures(t *testing.T) {
	t.Parallel()

	root1 := MigrationKey{App: "core", Name: "0001_first"}
	root2 := MigrationKey{App: "core", Name: "0002_second"}
	firstDescendant := MigrationKey{App: "zeta", Name: "0001"}
	secondDescendant := MigrationKey{App: "aardvark", Name: "0001"}
	planner := mustPlanner(t,
		migration(root1), migration(root2),
		migration(firstDescendant, root1), migration(secondDescendant, root2),
	)
	applied := mustApplied(t, root1, root2, firstDescendant, secondDescendant)
	assertPlan(t, plan(t, planner, applied, ZeroTarget("core")),
		backward(firstDescendant), backward(root1), backward(secondDescendant), backward(root2))
}

func TestPlannerUnappliedDependentDoesNotBlockRollback(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1), migration(beta1, alpha2))
	assertPlan(t, plan(t, planner, mustApplied(t, alpha1, alpha2), NamedTarget(alpha1)), backward(alpha2))
}

func TestPlannerUnknownAppliedRecordsArePreservedAndSkippedByHistoryCheck(t *testing.T) {
	t.Parallel()

	unknown := MigrationKey{App: "legacy", Name: "0009_removed"}
	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	applied := mustApplied(t, unknown)
	assertPlan(t, plan(t, planner, applied, NamedTarget(alpha2)), forward(alpha1), forward(alpha2))
	assertPlan(t, plan(t, planner, applied, ZeroTarget("legacy")))
}

func TestPlannerZeroValuesEmptyTargetsDuplicatesAndMixedPlans(t *testing.T) {
	t.Parallel()

	var zeroPlanner Planner
	var zeroApplied AppliedState
	assertPlan(t, plan(t, zeroPlanner, zeroApplied))
	assertPlan(t, plan(t, zeroPlanner, zeroApplied, ZeroTarget("unknown")))

	emptyPlanner := mustPlanner(t)
	emptyApplied := mustApplied(t)
	assertPlan(t, plan(t, emptyPlanner, emptyApplied))
	assertPlan(t, plan(t, emptyPlanner, emptyApplied, ZeroTarget("unknown")))

	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1))
	assertPlan(t, plan(t, planner, mustApplied(t), NamedTarget(alpha2), NamedTarget(alpha2)),
		forward(alpha1), forward(alpha2))
	assertPlan(t, plan(t, planner, mustApplied(t, alpha1), ZeroTarget("alpha"), NamedTarget(alpha2)),
		backward(alpha1), forward(alpha1), forward(alpha2))
}

func TestPlannerRandomDAGPrecedence(t *testing.T) {
	t.Parallel()

	random := rand.New(rand.NewSource(20260808))
	for iteration := 0; iteration < 100; iteration++ {
		const nodeCount = 18
		keys := make([]MigrationKey, nodeCount)
		migrations := make([]Migration, nodeCount)
		for index := range keys {
			keys[index] = MigrationKey{App: "random", Name: leftPaddedNumber(index)}
		}
		for child := range keys {
			dependencies := make([]MigrationKey, 0)
			for parent := 0; parent < child; parent++ {
				if random.Intn(5) == 0 {
					dependencies = append(dependencies, keys[parent])
				}
			}
			migrations[child] = migration(keys[child], dependencies...)
		}
		planner := mustPlanner(t, migrations...)

		forwardPlan := plan(t, planner, mustApplied(t), NamedTarget(keys[len(keys)-1]))
		seen := make(map[MigrationKey]bool)
		for _, step := range forwardPlan {
			for _, parent := range dependencyMap(migrations)[step.Key] {
				if !seen[parent] {
					t.Fatalf("iteration %d forward emitted %v before parent %v: %v", iteration, step.Key, parent, forwardPlan)
				}
			}
			seen[step.Key] = true
		}

		backwardPlan := plan(t, planner, mustApplied(t, keys...), ZeroTarget("random"))
		positions := make(map[MigrationKey]int, len(backwardPlan))
		for index, step := range backwardPlan {
			positions[step.Key] = index
		}
		if len(positions) != len(keys) {
			t.Fatalf("iteration %d backward plan length = %d, want %d", iteration, len(positions), len(keys))
		}
		for _, migration := range migrations {
			for _, parent := range migration.Dependencies {
				if positions[migration.Key()] >= positions[parent] {
					t.Fatalf("iteration %d backward parent %v preceded child %v: %v", iteration, parent, migration.Key(), backwardPlan)
				}
			}
		}
	}
}

func TestPlannerRepeatedAndConcurrentPlanIsImmutable(t *testing.T) {
	t.Parallel()

	planner := mustPlanner(t,
		migration(shared), migration(alpha1, shared), migration(beta1, shared), migration(alpha2, alpha1, beta1),
	)
	applied := mustApplied(t)
	targets := []Target{NamedTarget(alpha2)}
	want := plan(t, planner, applied, targets...)
	mutated := plan(t, planner, applied, targets...)
	mutated[0] = backward(gamma1)
	assertPlan(t, plan(t, planner, applied, targets...), want...)

	for iteration := 0; iteration < 100; iteration++ {
		assertPlan(t, plan(t, planner, applied, targets...), want...)
	}

	const workers = 64
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				got, err := planner.Plan(applied, targets...)
				if err != nil {
					errorsChannel <- err
					return
				}
				if !reflect.DeepEqual(got, want) {
					errorsChannel <- errors.New("concurrent plan differed")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
}

func TestPlannerGraphAppLeavesIncludeNodeWithOnlyCrossAppChildren(t *testing.T) {
	t.Parallel()

	alphaRoot := MigrationKey{App: "alpha", Name: "0001_root"}
	alphaLeaf := MigrationKey{App: "alpha", Name: "0002_leaf"}
	betaChild := MigrationKey{App: "beta", Name: "0001_child"}
	graph, err := newPlannerGraph([]Migration{
		migration(alphaRoot),
		migration(alphaLeaf, alphaRoot),
		migration(betaChild, alphaRoot),
	})
	if err != nil {
		t.Fatalf("newPlannerGraph() error = %v", err)
	}
	want := []MigrationKey{alphaLeaf, betaChild}
	if got := graph.appLeaves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("appLeaves() = %v, want %v", got, want)
	}

	graph, err = newPlannerGraph([]Migration{
		migration(alphaRoot),
		migration(betaChild, alphaRoot),
	})
	if err != nil {
		t.Fatalf("newPlannerGraph() cross-app-only error = %v", err)
	}
	want = []MigrationKey{alphaRoot, betaChild}
	if got := graph.appLeaves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cross-app-only appLeaves() = %v, want %v", got, want)
	}
}

func TestPlannerHeapSelectionMatchesCanonicalScanExhaustively(t *testing.T) {
	keys := []MigrationKey{
		{App: "alpha", Name: "0001"},
		{App: "alpha", Name: "0002"},
		{App: "beta", Name: "0001"},
		{App: "beta", Name: "0002"},
	}
	edges := make([]dependencyEdge, 0, 6)
	for child := 1; child < len(keys); child++ {
		for parent := 0; parent < child; parent++ {
			edges = append(edges, dependencyEdge{child: keys[child], parent: keys[parent]})
		}
	}
	targetChoices := []Target{
		NamedTarget(keys[0]),
		NamedTarget(keys[1]),
		NamedTarget(keys[2]),
		NamedTarget(keys[3]),
		ZeroTarget("alpha"),
		ZeroTarget("beta"),
	}
	targetSequences := make([][]Target, 0, len(targetChoices)+len(targetChoices)*len(targetChoices))
	for _, first := range targetChoices {
		targetSequences = append(targetSequences, []Target{first})
		for _, second := range targetChoices {
			targetSequences = append(targetSequences, []Target{first, second})
		}
	}

	for graphMask := 0; graphMask < 1<<len(edges); graphMask++ {
		definitions := make([]Migration, len(keys))
		for index, key := range keys {
			definitions[index] = migration(key)
		}
		for edgeIndex, edge := range edges {
			if graphMask&(1<<edgeIndex) != 0 {
				for index := range definitions {
					if definitions[index].Key() == edge.child {
						definitions[index].Dependencies = append(definitions[index].Dependencies, edge.parent)
						break
					}
				}
			}
		}
		planner := mustPlanner(t, definitions...)
		for appliedMask := 0; appliedMask < 1<<len(keys); appliedMask++ {
			appliedKeys := make([]MigrationKey, 0, len(keys))
			for index, key := range keys {
				if appliedMask&(1<<index) != 0 {
					appliedKeys = append(appliedKeys, key)
				}
			}
			applied := mustApplied(t, appliedKeys...)
			for sequenceIndex, targets := range targetSequences {
				got, gotErr := planner.Plan(applied, targets...)
				want, wantErr := referencePlannerPlan(planner.graph, applied, targets...)
				if !reflect.DeepEqual(got, want) || !samePlanningError(gotErr, wantErr) {
					t.Fatalf(
						"graph mask %06b applied mask %04b target sequence %d: Plan() = (%v, %v), canonical scan = (%v, %v)",
						graphMask, appliedMask, sequenceIndex, got, gotErr, want, wantErr,
					)
				}
			}
		}
	}
}

func TestPlannerHeapSelectionHandlesDenseResourceValidGraphs(t *testing.T) {
	const (
		denseNodes = 1851
		extraNodes = 2048 - denseNodes
		earlyCount = 150
		chainCount = 1700
	)

	t.Run("forward", func(t *testing.T) {
		definitions := make([]Migration, 0, denseNodes+extraNodes)
		chain := make([]MigrationKey, chainCount)
		for index := range chain {
			chain[index] = MigrationKey{App: "zchain", Name: fmt.Sprintf("%04d", index)}
			dependencies := []MigrationKey(nil)
			if index != 0 {
				dependencies = []MigrationKey{chain[index-1]}
			}
			definitions = append(definitions, migration(chain[index], dependencies...))
		}
		early := make([]MigrationKey, earlyCount)
		for index := range early {
			early[index] = MigrationKey{App: "alpha", Name: fmt.Sprintf("%04d", index)}
			definitions = append(definitions, migration(early[index], chain...))
		}
		leaf := MigrationKey{App: "zzleaf", Name: "0001"}
		definitions = append(definitions, migration(leaf, early...))
		extraTargets := make([]Target, 0, extraNodes)
		for index := 0; index < extraNodes; index++ {
			key := MigrationKey{App: "extra", Name: fmt.Sprintf("%04d", index)}
			definitions = append(definitions, migration(key))
			extraTargets = append(extraTargets, NamedTarget(key))
		}

		planner := mustPlanner(t, definitions...)
		targets := append([]Target{NamedTarget(leaf)}, extraTargets...)
		steps := plan(t, planner, mustApplied(t), targets...)
		if got, want := len(steps), len(definitions); got != want {
			t.Fatalf("dense forward plan length = %d, want %d", got, want)
		}
		for index, step := range steps {
			if step.Direction != DirectionForward {
				t.Fatalf("dense forward step %d direction = %v", index, step.Direction)
			}
		}
	})

	t.Run("backward", func(t *testing.T) {
		root := MigrationKey{App: "root", Name: "0001"}
		definitions := make([]Migration, 0, denseNodes+extraNodes)
		definitions = append(definitions, migration(root))
		early := make([]MigrationKey, earlyCount)
		for index := range early {
			early[index] = MigrationKey{App: "alpha", Name: fmt.Sprintf("%04d", index)}
			definitions = append(definitions, migration(early[index], root))
		}
		chain := make([]MigrationKey, chainCount)
		for index := range chain {
			chain[index] = MigrationKey{App: "zchain", Name: fmt.Sprintf("%04d", index)}
			dependencies := append([]MigrationKey(nil), early...)
			if index+1 < len(chain) {
				dependencies = append(dependencies, MigrationKey{App: "zchain", Name: fmt.Sprintf("%04d", index+1)})
			}
			definitions = append(definitions, migration(chain[index], dependencies...))
		}
		extraKeys := make([]MigrationKey, 0, extraNodes)
		for index := 0; index < extraNodes; index++ {
			key := MigrationKey{App: "extra", Name: fmt.Sprintf("%04d", index)}
			definitions = append(definitions, migration(key))
			extraKeys = append(extraKeys, key)
		}

		planner := mustPlanner(t, definitions...)
		appliedKeys := append([]MigrationKey{root}, early...)
		appliedKeys = append(appliedKeys, chain...)
		appliedKeys = append(appliedKeys, extraKeys...)
		steps := plan(t, planner, mustApplied(t, appliedKeys...), ZeroTarget(root.App))
		if got, want := len(steps), denseNodes; got != want {
			t.Fatalf("dense backward plan length = %d, want %d", got, want)
		}
		for index, step := range steps {
			if step.Direction != DirectionBackward {
				t.Fatalf("dense backward step %d direction = %v", index, step.Direction)
			}
		}
	})
}

func referencePlannerPlan(graph *plannerGraph, applied AppliedState, targets ...Target) ([]PlanStep, error) {
	for _, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, err
		}
	}
	working := cloneAppliedKeys(applied.keys)
	if err := graph.validateAppliedHistory(working); err != nil {
		return nil, err
	}

	var result []PlanStep
	for _, target := range targets {
		switch target.kind {
		case targetNamed:
			if !graph.contains(target.key) {
				return nil, newPlanningError(CategoryPlan, CodeTargetNotFound, target.key, MigrationKey{}, nil)
			}
			if _, exists := working[target.key]; !exists {
				steps, err := referencePlanForward(graph, target.key, working)
				if err != nil {
					return nil, err
				}
				result = append(result, steps...)
				continue
			}
			for _, child := range graph.children[target.key] {
				if child.App != target.key.App {
					continue
				}
				steps, err := referencePlanBackward(graph, child, working)
				if err != nil {
					return nil, err
				}
				result = append(result, steps...)
			}
		case targetZero:
			for _, root := range graph.appRoots(target.app) {
				steps, err := referencePlanBackward(graph, root, working)
				if err != nil {
					return nil, err
				}
				result = append(result, steps...)
			}
		}
	}
	return result, nil
}

func referencePlanForward(graph *plannerGraph, target MigrationKey, applied map[MigrationKey]struct{}) ([]PlanStep, error) {
	candidates := make(map[MigrationKey]struct{})
	stack := []MigrationKey{target}
	for len(stack) != 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := applied[key]; exists {
			continue
		}
		if _, exists := candidates[key]; exists {
			continue
		}
		candidates[key] = struct{}{}
		stack = append(stack, graph.parents[key]...)
	}

	steps := make([]PlanStep, 0, len(candidates))
	for len(candidates) != 0 {
		next, exists := referenceFirstForwardReady(graph, candidates, applied)
		if !exists {
			return nil, internalCyclePlanningError(candidates)
		}
		steps = append(steps, PlanStep{Key: next, Direction: DirectionForward})
		applied[next] = struct{}{}
		delete(candidates, next)
	}
	return steps, nil
}

func referenceFirstForwardReady(graph *plannerGraph, candidates, applied map[MigrationKey]struct{}) (MigrationKey, bool) {
	for _, key := range graph.nodes {
		if _, exists := candidates[key]; !exists {
			continue
		}
		ready := true
		for _, parent := range graph.parents[key] {
			if _, exists := applied[parent]; !exists {
				ready = false
				break
			}
		}
		if ready {
			return key, true
		}
	}
	return MigrationKey{}, false
}

func referencePlanBackward(graph *plannerGraph, seed MigrationKey, applied map[MigrationKey]struct{}) ([]PlanStep, error) {
	candidates := make(map[MigrationKey]struct{})
	stack := []MigrationKey{seed}
	visited := make(map[MigrationKey]struct{})
	for len(stack) != 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := visited[key]; exists {
			continue
		}
		visited[key] = struct{}{}
		if _, exists := applied[key]; exists {
			candidates[key] = struct{}{}
		}
		stack = append(stack, graph.children[key]...)
	}

	steps := make([]PlanStep, 0, len(candidates))
	for len(candidates) != 0 {
		next, exists := referenceFirstBackwardReady(graph, candidates)
		if !exists {
			return nil, internalCyclePlanningError(candidates)
		}
		steps = append(steps, PlanStep{Key: next, Direction: DirectionBackward})
		delete(applied, next)
		delete(candidates, next)
	}
	return steps, nil
}

func referenceFirstBackwardReady(graph *plannerGraph, candidates map[MigrationKey]struct{}) (MigrationKey, bool) {
	for _, key := range graph.nodes {
		if _, exists := candidates[key]; !exists {
			continue
		}
		ready := true
		for _, child := range graph.children[key] {
			if _, exists := candidates[child]; exists {
				ready = false
				break
			}
		}
		if ready {
			return key, true
		}
	}
	return MigrationKey{}, false
}

func samePlanningError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftPlanning, leftOK := left.(*PlanningError)
	rightPlanning, rightOK := right.(*PlanningError)
	if !leftOK || !rightOK {
		return left.Error() == right.Error()
	}
	return leftPlanning.Category == rightPlanning.Category &&
		leftPlanning.Code == rightPlanning.Code &&
		leftPlanning.Node == rightPlanning.Node &&
		leftPlanning.Related == rightPlanning.Related &&
		reflect.DeepEqual(leftPlanning.Members(), rightPlanning.Members())
}

func TestPlannerSourceHasNoDatabaseOrBackendImports(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"planner.go", "planner_graph.go", "reconstructor.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", imported.Path.Value, filename, err)
			}
			if path == "database/sql" || strings.HasSuffix(path, "/db") || strings.Contains(path, "/db/") || strings.Contains(path, "migrations/backend") {
				t.Fatalf("%s imports I/O/backend package %q", filename, path)
			}
		}
	}
}

func migration(key MigrationKey, dependencies ...MigrationKey) Migration {
	return Migration{App: key.App, Name: key.Name, Dependencies: append([]MigrationKey(nil), dependencies...)}
}

func mustPlanner(t *testing.T, migrations ...Migration) Planner {
	t.Helper()
	planner, err := NewPlanner(migrations...)
	if err != nil {
		t.Fatalf("NewPlanner() error = %v", err)
	}
	return planner
}

func mustApplied(t *testing.T, keys ...MigrationKey) AppliedState {
	t.Helper()
	applied, err := NewAppliedState(keys...)
	if err != nil {
		t.Fatalf("NewAppliedState() error = %v", err)
	}
	return applied
}

func plan(t *testing.T, planner Planner, applied AppliedState, targets ...Target) []PlanStep {
	t.Helper()
	steps, err := planner.Plan(applied, targets...)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return steps
}

func forward(key MigrationKey) PlanStep {
	return PlanStep{Key: key, Direction: DirectionForward}
}

func backward(key MigrationKey) PlanStep {
	return PlanStep{Key: key, Direction: DirectionBackward}
}

func assertPlan(t *testing.T, got []PlanStep, want ...PlanStep) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
}

func assertPlanningError(t *testing.T, err error, category ErrorCategory, code ErrorCode, node, related MigrationKey) *PlanningError {
	t.Helper()
	var planningError *PlanningError
	if !errors.As(err, &planningError) {
		t.Fatalf("error = %#v, want *PlanningError", err)
	}
	if planningError.Category != category || planningError.Code != code || planningError.Node != node || planningError.Related != related {
		t.Fatalf("planning error = %#v, want category=%s code=%s node=%v related=%v", planningError, category, code, node, related)
	}
	return planningError
}

func reversedMigrations(input []Migration) []Migration {
	result := cloneMigrations(input)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func cloneMigrations(input []Migration) []Migration {
	result := append([]Migration(nil), input...)
	for index := range result {
		result[index].Dependencies = append([]MigrationKey(nil), result[index].Dependencies...)
		result[index].Operations = append([]Operation(nil), result[index].Operations...)
	}
	return result
}

func dependencyMap(migrations []Migration) map[MigrationKey][]MigrationKey {
	result := make(map[MigrationKey][]MigrationKey, len(migrations))
	for _, migration := range migrations {
		result[migration.Key()] = migration.Dependencies
	}
	return result
}

func leftPaddedNumber(value int) string {
	const digits = "0000000000"
	rendered := strconv.Itoa(value)
	return digits[:4-len(rendered)] + rendered
}
