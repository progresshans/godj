package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
	"github.com/progresshans/godj/schema/ir"
)

func TestExecutorMigrateValidatesOuterInputsBeforeIO(t *testing.T) {
	t.Parallel()

	t.Run("nil context wins", func(t *testing.T) {
		fake := newLifecycleTestBackend(nil)
		_, err := (Executor{Backend: fake}).Migrate(nil, []Migration{{}}, LifecycleRequest{})
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if fake.openCount != 0 || fake.legacyBeginCount != 0 {
			t.Fatalf("backend calls = open:%d legacy:%d, want 0", fake.openCount, fake.legacyBeginCount)
		}
	})

	t.Run("canceled context wins", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fake := newLifecycleTestBackend(nil)
		_, err := (Executor{Backend: fake}).Migrate(ctx, []Migration{{}}, LifecycleRequest{})
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || fake.openCount != 0 {
			t.Fatalf("Migrate() error = %v, open calls = %d", err, fake.openCount)
		}
	})

	t.Run("zero request wins over definitions and capability", func(t *testing.T) {
		fake := newLifecycleTestBackend(nil)
		_, err := (Executor{Backend: fake}).Migrate(context.Background(), []Migration{{}}, LifecycleRequest{})
		assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
		if fake.openCount != 0 || fake.legacyBeginCount != 0 {
			t.Fatalf("backend calls = open:%d legacy:%d, want 0", fake.openCount, fake.legacyBeginCount)
		}
	})

	t.Run("invalid definitions win before capability", func(t *testing.T) {
		legacy := &fakeBackend{transaction: newFakeTransaction()}
		_, err := (Executor{Backend: legacy}).Migrate(
			context.Background(),
			[]Migration{{App: "", Name: "0001"}},
			LatestLifecycleRequest(),
		)
		assertPlanningError(t, err, CategoryGraph, CodeInvalidNode, MigrationKey{Name: "0001"}, MigrationKey{})
		if legacy.beginCount != 0 {
			t.Fatalf("legacy BeginMigration() calls = %d, want 0", legacy.beginCount)
		}
	})

	t.Run("legacy-only backend never falls back", func(t *testing.T) {
		legacy := &fakeBackend{transaction: newFakeTransaction()}
		_, err := (Executor{Backend: legacy}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryCapability, CodeRevisionFenceUnsupported, NoOperation, "")
		if legacy.beginCount != 0 {
			t.Fatalf("legacy BeginMigration() calls = %d, want 0", legacy.beginCount)
		}
	})

	t.Run("typed nil backend is unsupported", func(t *testing.T) {
		var fake *lifecycleTestBackend
		_, err := (Executor{Backend: fake}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryCapability, CodeRevisionFenceUnsupported, NoOperation, "")
	})

	t.Run("typed nil session is begin failure", func(t *testing.T) {
		var session *lifecycleTestSession
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
		if fake.openCount != 1 {
			t.Fatalf("OpenRevisionFencedSession() calls = %d, want 1", fake.openCount)
		}
	})

	t.Run("typed nil transaction is begin failure and session closes", func(t *testing.T) {
		var transaction *lifecycleTestTransaction
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
		if session.beginCount != 1 || session.closeCount != 1 {
			t.Fatalf("calls = begin:%d close:%d, want 1 each", session.beginCount, session.closeCount)
		}
	})

	t.Run("corrupt tagged request is rejected", func(t *testing.T) {
		fake := newLifecycleTestBackend(nil)
		request := LifecycleRequest{kind: lifecycleRequestLatest, targets: []Target{}}
		_, err := (Executor{Backend: fake}).Migrate(context.Background(), lifecycleTestDefinitions(), request)
		assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
		if fake.openCount != 0 {
			t.Fatalf("OpenRevisionFencedSession() calls = %d, want 0", fake.openCount)
		}
	})
}

func TestExecutorMigrateRejectsRelationDefinitionsBeforeBackendOrRecorderIO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition Migration
		kind       string
	}{
		{
			name: "CreateModel",
			definition: Migration{App: "blog", Name: "0001_post", Operations: []Operation{CreateModel{
				AppLabel: "blog",
				Model:    relationMigrationSchema().Models[0],
			}}},
			kind: "CreateModel",
		},
		{
			name: "AddField",
			definition: Migration{App: "blog", Name: "0002_author", Operations: []Operation{AddField{
				AppLabel: "blog", ModelName: "post", Field: relationMigrationField(),
			}}},
			kind: "AddField",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := newLifecycleTestSession(nil, nil)
			fake := newLifecycleTestBackend(session)
			state, err := (Executor{Backend: fake}).Migrate(
				context.Background(), []Migration{test.definition}, LatestLifecycleRequest(),
			)
			assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
			var migrationError *Error
			if !errors.As(err, &migrationError) {
				t.Fatalf("Migrate relation error = %#v, want *Error", err)
			}
			var capabilityError *backend.CapabilityError
			if !errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" ||
				!strings.Contains(capabilityError.Error(), "loader definition handoff is missing") {
				t.Fatalf("Migrate relation capability cause = %#v", capabilityError)
			}
			if len(state.Apps()) != 0 || fake.openCount != 0 || fake.legacyBeginCount != 0 ||
				session.readCount != 0 || session.beginCount != 0 || session.closeCount != 0 {
				t.Fatalf(
					"relation lifecycle published or touched I/O: apps=%v open=%d legacy=%d read=%d begin=%d close=%d",
					state.Apps(), fake.openCount, fake.legacyBeginCount, session.readCount, session.beginCount, session.closeCount,
				)
			}
		})
	}

	t.Run("request and context precedence", func(t *testing.T) {
		definition := tests[0].definition
		fake := newLifecycleTestBackend(newLifecycleTestSession(nil, nil))
		corrupt := LifecycleRequest{kind: lifecycleRequestLatest, targets: []Target{}}
		_, err := (Executor{Backend: fake}).Migrate(context.Background(), []Migration{definition}, corrupt)
		assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = (Executor{Backend: fake}).Migrate(ctx, []Migration{definition}, corrupt)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || fake.openCount != 0 {
			t.Fatalf("relation precedence = error:%v open:%d", err, fake.openCount)
		}
	})

	t.Run("cancellation after raw relation scan beats capability", func(t *testing.T) {
		definition := tests[0].definition
		ctx := &stagedRawCancellationContext{Context: context.Background(), cancelAt: 2}
		fake := newLifecycleTestBackend(newLifecycleTestSession(nil, nil))
		_, err := (Executor{Backend: fake}).Migrate(ctx, []Migration{definition}, LatestLifecycleRequest())
		var capability *backend.CapabilityError
		if !errors.Is(err, context.Canceled) || errors.As(err, &capability) || fake.openCount != 0 || ctx.calls.Load() < 2 {
			t.Fatalf("Migrate post-scan cancellation = error:%v capability:%#v open:%d calls:%d", err, capability, fake.openCount, ctx.calls.Load())
		}
	})
}

func TestExecutorMigrateLoadedRelationRoutesByActualHistory(t *testing.T) {
	t.Parallel()

	definitions := lifecycleLoadedRelationCreateDefinitions()
	allCapabilities := backend.RelationMigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKeyByTableRemake:     true,
	}
	tests := []struct {
		name                string
		records             []backend.AppliedMigration
		transactionCount    int
		wantCapabilityCalls int
		wantScalarBegins    int
		wantRelationBegins  int
		wantRelationState   bool
	}{
		{
			name:                "fresh history selects relation plan",
			transactionCount:    3,
			wantCapabilityCalls: 1,
			wantScalarBegins:    2,
			wantRelationBegins:  1,
			wantRelationState:   true,
		},
		{
			name: "applied relation leaves scalar plan",
			records: lifecycleRecords(
				MigrationKey{App: "authors", Name: "0001_author"},
				MigrationKey{App: "blog", Name: "0001_post"},
			),
			transactionCount:  1,
			wantScalarBegins:  1,
			wantRelationState: true,
		},
		{
			name: "fully applied history is no-op",
			records: lifecycleRecords(
				MigrationKey{App: "authors", Name: "0001_author"},
				MigrationKey{App: "blog", Name: "0001_post"},
				MigrationKey{App: "zeta", Name: "0001_note"},
			),
			wantRelationState: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := newLifecycleTestSession(test.records, lifecycleCommittedTransactions(test.transactionCount))
			fake := newLifecycleTestBackend(session)
			fake.relationCapabilities = allCapabilities
			state, err := (Executor{Backend: fake}).Migrate(
				lifecycleLoadedContext(t, definitions),
				definitions,
				LatestLifecycleRequest(),
			)
			if err != nil {
				t.Fatalf("Migrate(loaded relation): %v", err)
			}
			if session.readCount != 1 || fake.relationCapabilityCount != test.wantCapabilityCalls ||
				session.beginCount != test.wantScalarBegins || session.relationBeginCount != test.wantRelationBegins {
				t.Fatalf(
					"calls = read:%d capability:%d scalar:%d relation:%d, want 1/%d/%d/%d",
					session.readCount, fake.relationCapabilityCount, session.beginCount,
					session.relationBeginCount, test.wantCapabilityCalls,
					test.wantScalarBegins, test.wantRelationBegins,
				)
			}
			if test.wantRelationState && state.FormatVersion() != RelationStateFormatVersion {
				t.Fatalf("loaded result format = %d, want relation state", state.FormatVersion())
			}
			if test.wantRelationBegins == 1 {
				if len(session.relationIntents) != 1 || len(session.relationIntents[0].Operations) != 1 {
					t.Fatalf("relation intents = %#v", session.relationIntents)
				}
				operation := session.relationIntents[0].Operations[0]
				if operation.OperationIndex != 0 || operation.Kind != backend.RelationMigrationCreateModel ||
					len(operation.Targets) != 1 || operation.Targets[0].TargetModel.Name != "author" ||
					operation.Targets[0].TargetKey.Kind != ir.FieldAuto {
					t.Fatalf("relation CreateModel intent = %#v", operation)
				}
			}
		})
	}
}

func TestExecutorMigrateLoadedRelationDryValidatesCapabilitiesBeforeEveryBegin(t *testing.T) {
	t.Parallel()

	definitions := lifecycleLoadedRelationAddDefinitions()
	session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(len(definitions)))
	fake := newLifecycleTestBackend(session)
	state, err := (Executor{Backend: fake}).Migrate(
		lifecycleLoadedContext(t, definitions),
		definitions,
		LatestLifecycleRequest(),
	)
	assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
	var capability *backend.CapabilityError
	if !errors.As(err, &capability) || capability.Feature != "relation_migration" ||
		!strings.Contains(capability.Detail, "AddRequiredForeignKeyToEmptyTable") {
		t.Fatalf("loaded AddField capability error = %#v", err)
	}
	if len(state.Apps()) != 0 || session.readCount != 1 || fake.relationCapabilityCount != 1 ||
		session.beginCount != 0 || session.relationBeginCount != 0 || len(session.transitions) != 0 {
		t.Fatalf(
			"unsupported tail touched partial lifecycle: apps=%v read=%d capability=%d scalar=%d relation=%d transitions=%v",
			state.Apps(), session.readCount, fake.relationCapabilityCount,
			session.beginCount, session.relationBeginCount, session.transitions,
		)
	}
}

func TestExecutorMigrateLoadedRelationPassesCompleteForwardAndReverseIntents(t *testing.T) {
	t.Parallel()

	definitions := lifecycleLoadedCompleteIntentDefinitions()
	allCapabilities := backend.RelationMigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKeyByTableRemake:     true,
	}

	t.Run("forward", func(t *testing.T) {
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(2))
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = allCapabilities
		_, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		if err != nil {
			t.Fatalf("Migrate(complete forward): %v", err)
		}
		if len(session.relationIntents) != 1 {
			t.Fatalf("forward relation intents = %#v", session.relationIntents)
		}
		operations := session.relationIntents[0].Operations
		wantKinds := []backend.RelationMigrationOperationKind{
			backend.RelationMigrationCreateModel,
			backend.RelationMigrationAddField,
			backend.RelationMigrationAddField,
		}
		if len(operations) != 3 {
			t.Fatalf("forward operations = %#v", operations)
		}
		for index := range operations {
			if operations[index].OperationIndex != index || operations[index].Kind != wantKinds[index] {
				t.Fatalf("forward operation[%d] = %#v", index, operations[index])
			}
		}
		if len(operations[0].Targets) != 0 || len(operations[1].Targets) != 0 || len(operations[2].Targets) != 1 {
			t.Fatalf("forward target membership = %d/%d/%d", len(operations[0].Targets), len(operations[1].Targets), len(operations[2].Targets))
		}
	})

	t.Run("reverse", func(t *testing.T) {
		records := lifecycleRecords(
			MigrationKey{App: "authors", Name: "0001_author"},
			MigrationKey{App: "blog", Name: "0001_post"},
		)
		session := newLifecycleTestSession(records, lifecycleCommittedTransactions(2))
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = allCapabilities
		state, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions),
			definitions,
			TargetedLifecycleRequest(ZeroTarget("authors")),
		)
		if err != nil {
			t.Fatalf("Migrate(complete reverse): %v", err)
		}
		if len(state.Apps()) != 0 || len(session.relationIntents) != 1 {
			t.Fatalf("reverse result = apps:%v intents:%#v", state.Apps(), session.relationIntents)
		}
		operations := session.relationIntents[0].Operations
		wantIndices := []int{2, 1, 0}
		wantKinds := []backend.RelationMigrationOperationKind{
			backend.RelationMigrationRemoveField,
			backend.RelationMigrationRemoveField,
			backend.RelationMigrationDeleteModel,
		}
		if len(operations) != 3 {
			t.Fatalf("reverse operations = %#v", operations)
		}
		for index := range operations {
			if operations[index].OperationIndex != wantIndices[index] || operations[index].Kind != wantKinds[index] {
				t.Fatalf("reverse operation[%d] = %#v", index, operations[index])
			}
		}
		if len(operations[0].Targets) != 1 || len(operations[1].Targets) != 0 || len(operations[2].Targets) != 0 {
			t.Fatalf("reverse target membership = %d/%d/%d", len(operations[0].Targets), len(operations[1].Targets), len(operations[2].Targets))
		}
	})
}

func TestExecutorMigrateLoadedRelationCancellationAndOptionalPortBoundaries(t *testing.T) {
	definitions := lifecycleLoadedRelationCreateDefinitions()[:2]

	t.Run("Open cancellation closes without reading", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		session := newLifecycleTestSession(nil, nil)
		fake := newLifecycleTestBackend(session)
		fake.openHook = cancel
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || session.readCount != 0 || session.closeCount != 1 ||
			fake.relationCapabilityCount != 0 || session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("Open cancellation = err:%v read:%d close:%d capability:%d scalar:%d relation:%d", err, session.readCount, session.closeCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("Open cancellation wins over a nil session", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		fake := newLifecycleTestBackend(nil)
		fake.openHook = cancel
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || fake.openCount != 1 || fake.relationCapabilityCount != 0 {
			t.Fatalf("Open nil-session cancellation = err:%v open:%d capability:%d", err, fake.openCount, fake.relationCapabilityCount)
		}
	})

	t.Run("Read cancellation wins before applied-state interpretation", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		session := newLifecycleTestSession([]backend.AppliedMigration{{Name: "malformed"}}, nil)
		session.readHook = cancel
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		var planning *PlanningError
		if !errors.Is(err, context.Canceled) || errors.As(err, &planning) || session.readCount != 1 ||
			session.closeCount != 1 || fake.relationCapabilityCount != 0 || session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("Read cancellation = err:%v planning:%#v read:%d close:%d capability:%d scalar:%d relation:%d", err, planning, session.readCount, session.closeCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("capability cancellation wins over a false capability", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		session := newLifecycleTestSession(nil, nil)
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilityHook = cancel
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		var capability *backend.CapabilityError
		if !errors.Is(err, context.Canceled) || errors.As(err, &capability) || session.readCount != 1 ||
			fake.relationCapabilityCount != 1 || session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("capability cancellation = err:%v capability:%#v read:%d calls:%d scalar:%d relation:%d", err, capability, session.readCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("missing relation backend follows exact history and dry validation", func(t *testing.T) {
		session := newLifecycleTestSession(nil, nil)
		delegate := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: &lifecycleNoRelationBackend{delegate: delegate}}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		if session.readCount != 1 || session.closeCount != 1 || session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("missing relation backend calls = read:%d close:%d scalar:%d relation:%d", session.readCount, session.closeCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("missing relation session follows capability selection", func(t *testing.T) {
		delegate := newLifecycleTestSession(nil, nil)
		session := &lifecycleNoRelationSession{delegate: delegate}
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		_, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		if delegate.readCount != 1 || delegate.closeCount != 1 || fake.relationCapabilityCount != 1 ||
			delegate.beginCount != 0 || delegate.relationBeginCount != 0 {
			t.Fatalf("missing relation session calls = read:%d close:%d capability:%d scalar:%d relation:%d", delegate.readCount, delegate.closeCount, fake.relationCapabilityCount, delegate.beginCount, delegate.relationBeginCount)
		}
	})

	t.Run("relation Begin nonnil error rolls back", func(t *testing.T) {
		cause := errors.New("relation begin sentinel")
		transaction := newLifecycleTestTransaction()
		session := newLifecycleTestSession(
			lifecycleRecords(MigrationKey{App: "authors", Name: "0001_author"}),
			[]backend.RevisionFencedTransaction{transaction},
		)
		session.beginErrors = []error{cause}
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		state, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
		if !errors.Is(err, cause) || session.relationBeginCount != 1 || session.beginCount != 0 ||
			!reflect.DeepEqual(transaction.calls, []string{"rollback"}) {
			t.Fatalf("relation Begin error = err:%v scalar:%d relation:%d calls:%v", err, session.beginCount, session.relationBeginCount, transaction.calls)
		}
		if _, exists := state.Model("authors", "author"); !exists {
			t.Fatal("relation Begin error lost reconstructed durable history")
		}
	})

	t.Run("post-Begin cancellation rolls back before editor", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		transaction := newLifecycleTestTransaction()
		session := newLifecycleTestSession(
			lifecycleRecords(MigrationKey{App: "authors", Name: "0001_author"}),
			[]backend.RevisionFencedTransaction{transaction},
		)
		session.relationBeginHook = cancel
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(transaction.calls, []string{"rollback"}) {
			t.Fatalf("post-Begin cancellation = err:%v calls:%v", err, transaction.calls)
		}
	})

	t.Run("post-operation cancellation rolls back before recorder", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		transaction := newLifecycleTestTransaction()
		transaction.hooks["create_model"] = cancel
		session := newLifecycleTestSession(
			lifecycleRecords(MigrationKey{App: "authors", Name: "0001_author"}),
			[]backend.RevisionFencedTransaction{transaction},
		)
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "CreateModel")
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(transaction.calls, []string{"create_model", "rollback"}) {
			t.Fatalf("post-operation cancellation = err:%v calls:%v", err, transaction.calls)
		}
	})

	t.Run("post-recorder cancellation rolls back before commit", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		transaction := newLifecycleTestTransaction()
		transaction.hooks["record_applied"] = cancel
		session := newLifecycleTestSession(
			lifecycleRecords(MigrationKey{App: "authors", Name: "0001_author"}),
			[]backend.RevisionFencedTransaction{transaction},
		)
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		_, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		want := []string{"create_model", "record_applied:blog.0001_post", "rollback"}
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(transaction.calls, want) {
			t.Fatalf("post-recorder cancellation = err:%v calls:%v, want %v", err, transaction.calls, want)
		}
	})
}

func TestExecutorMigrateLoadedRelationResourceValidationPrecedesCapabilitiesAndBegins(t *testing.T) {
	t.Run("loader-authorized scalar targets retain loaded resource bounds", func(t *testing.T) {
		definitions := lifecycleTestDefinitions()[:1]
		request := LifecycleRequest{
			kind:    lifecycleRequestTargeted,
			targets: make([]Target, definitionhandoff.MaxDefinitions+1),
		}
		session := newLifecycleTestSession(nil, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(lifecycleLoadedContext(t, definitions), definitions, request)
		assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
		if fake.openCount != 0 || session.readCount != 0 || session.beginCount != 0 {
			t.Fatalf("loader-authorized scalar oversized targets touched IO: open:%d read:%d begin:%d", fake.openCount, session.readCount, session.beginCount)
		}
	})

	t.Run("loader-authorized scalar history retains loaded resource bounds", func(t *testing.T) {
		definitions := lifecycleTestDefinitions()[:1]
		records := make([]backend.AppliedMigration, definitionhandoff.MaxDefinitions+1)
		for index := range records {
			records[index] = backend.AppliedMigration{App: "unknown", Name: fmt.Sprintf("%04d", index)}
		}
		session := newLifecycleTestSession(records, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
		if session.readCount != 1 || session.closeCount != 1 || session.beginCount != 0 {
			t.Fatalf("loader-authorized scalar oversized history calls = read:%d close:%d begin:%d", session.readCount, session.closeCount, session.beginCount)
		}
	})

	t.Run("oversized target request fails before copy and IO", func(t *testing.T) {
		definitions := lifecycleLoadedRelationCreateDefinitions()[:2]
		request := LifecycleRequest{
			kind:    lifecycleRequestTargeted,
			targets: make([]Target, definitionhandoff.MaxDefinitions+1),
		}
		session := newLifecycleTestSession(nil, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(lifecycleLoadedContext(t, definitions), definitions, request)
		assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
		if fake.openCount != 0 || session.readCount != 0 || fake.relationCapabilityCount != 0 ||
			session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("oversized targets touched IO: open:%d read:%d capability:%d scalar:%d relation:%d", fake.openCount, session.readCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("oversized exact history fails before key allocation and capability", func(t *testing.T) {
		definitions := lifecycleLoadedRelationCreateDefinitions()[:2]
		records := make([]backend.AppliedMigration, definitionhandoff.MaxDefinitions+1)
		for index := range records {
			records[index] = backend.AppliedMigration{App: "unknown", Name: fmt.Sprintf("%04d", index)}
		}
		session := newLifecycleTestSession(records, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
		if session.readCount != 1 || session.closeCount != 1 || fake.relationCapabilityCount != 0 ||
			session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("oversized history calls = read:%d close:%d capability:%d scalar:%d relation:%d", session.readCount, session.closeCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("semantic target identity may exceed source ID limit", func(t *testing.T) {
		definitions := lifecycleLoadedRelationCreateDefinitions()[:2]
		longName := strings.Repeat("m", definitionhandoff.MaxSourceIDBytes+1)
		definitions[1].Name = longName
		session := newLifecycleTestSession(nil, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions),
			definitions,
			TargetedLifecycleRequest(NamedTarget(MigrationKey{App: "blog", Name: longName})),
		)
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		if fake.openCount != 1 || session.readCount != 1 || fake.relationCapabilityCount != 1 ||
			session.beginCount != 0 || session.relationBeginCount != 0 {
			t.Fatalf("long semantic identity calls = open:%d read:%d capability:%d scalar:%d relation:%d", fake.openCount, session.readCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount)
		}
	})

	t.Run("oversized later supported relation intent prevents scalar prefix", func(t *testing.T) {
		definitions := lifecycleLoadedOversizedRelationIntentDefinitions()
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(len(definitions)))
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		state, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		var migrationError *Error
		if !errors.As(err, &migrationError) || migrationError.Category != CategoryState ||
			migrationError.Code != CodeInvalidState || !strings.Contains(migrationError.Cause.Error(), "derived relation intent resource limit") {
			t.Fatalf("oversized derived intent error = %#v", err)
		}
		if len(state.Apps()) != 0 || session.readCount != 1 || fake.relationCapabilityCount != 0 ||
			session.beginCount != 0 || session.relationBeginCount != 0 || len(session.transitions) != 0 {
			t.Fatalf("oversized later intent touched partial lifecycle: apps:%v read:%d capability:%d scalar:%d relation:%d transitions:%v", state.Apps(), session.readCount, fake.relationCapabilityCount, session.beginCount, session.relationBeginCount, session.transitions)
		}
	})
}

func TestExecutorMigrateLoadedRelationCommitOutcomesPublishOnlyDurableState(t *testing.T) {
	definitions := lifecycleLoadedRelationCreateDefinitions()[:2]
	records := lifecycleRecords(MigrationKey{App: "authors", Name: "0001_author"})

	t.Run("committed cleanup and session close errors retain after state", func(t *testing.T) {
		cleanupCause := errors.New("relation commit cleanup")
		closeCause := errors.New("relation session close")
		transaction := newLifecycleTestTransaction()
		transaction.commitErr = cleanupCause
		session := newLifecycleTestSession(records, []backend.RevisionFencedTransaction{transaction})
		session.closeErr = closeCause
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		state, err := (Executor{Backend: fake}).Migrate(
			lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeCommitCleanupFailed, NoOperation, "")
		if !errors.Is(err, cleanupCause) || !errors.Is(err, closeCause) {
			t.Fatalf("committed cleanup error = %v, want commit and close causes", err)
		}
		if _, exists := state.Model("blog", "post"); !exists {
			t.Fatal("committed cleanup error lost relation after state")
		}
		want := []string{"create_model", "record_applied:blog.0001_post", "commit_fenced"}
		if !reflect.DeepEqual(transaction.calls, want) {
			t.Fatalf("committed cleanup calls = %v, want %v", transaction.calls, want)
		}
	})

	for _, test := range []struct {
		name       string
		durability backend.CommitDurability
		code       ErrorCode
	}{
		{name: "rolled back", durability: backend.CommitRolledBack, code: CodeCommitFailed},
		{name: "unknown", durability: backend.CommitUnknown, code: CodeCommitOutcomeUnknown},
	} {
		test := test
		t.Run(test.name+" preserves before state", func(t *testing.T) {
			cause := errors.New("relation " + test.name)
			transaction := newLifecycleTestTransaction()
			transaction.outcome = backend.CommitOutcome{Durability: test.durability}
			transaction.commitErr = cause
			session := newLifecycleTestSession(records, []backend.RevisionFencedTransaction{transaction})
			fake := newLifecycleTestBackend(session)
			fake.relationCapabilities = lifecycleAllRelationCapabilities()
			state, err := (Executor{Backend: fake}).Migrate(
				lifecycleLoadedContext(t, definitions), definitions, LatestLifecycleRequest(),
			)
			assertMigrationError(t, err, CategoryTransaction, test.code, NoOperation, "")
			if !errors.Is(err, cause) {
				t.Fatalf("%s error = %v, want cause", test.name, err)
			}
			if _, exists := state.Model("authors", "author"); !exists {
				t.Fatalf("%s lost durable before state", test.name)
			}
			if _, exists := state.Model("blog", "post"); exists {
				t.Fatalf("%s published non-durable relation state", test.name)
			}
		})
	}

	t.Run("cancellation after final committed relation step is success", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := lifecycleLoadedContextFrom(t, base, definitions)
		transaction := newLifecycleTestTransaction()
		transaction.commitHook = cancel
		session := newLifecycleTestSession(records, []backend.RevisionFencedTransaction{transaction})
		fake := newLifecycleTestBackend(session)
		fake.relationCapabilities = lifecycleAllRelationCapabilities()
		state, err := (Executor{Backend: fake}).Migrate(ctx, definitions, LatestLifecycleRequest())
		if err != nil {
			t.Fatalf("final committed relation cancellation error = %v", err)
		}
		if _, exists := state.Model("blog", "post"); !exists {
			t.Fatal("final committed relation cancellation lost after state")
		}
		assertDetachedBoundedCleanup(t, session.closeContextCanceled, session.closeDeadline)
	})
}

func TestExecutorMigrateCarrierResourceScanPrecedesRelationInspectionAndCloning(t *testing.T) {
	t.Parallel()

	handoff, err := definitionhandoff.New([]definitionhandoff.Record{{
		SourceID: "sealed", Producer: definitionhandoff.Producer{Name: "test", Version: "1"},
		Profile:    definitionhandoff.Compatibility{DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 1, SchemaIR: 2},
		Definition: definitionhandoff.Definition{App: "sealed", Name: "0001_initial"},
	}})
	if err != nil {
		t.Fatalf("definitionhandoff.New(): %v", err)
	}
	oversizedFields := make([]ir.Field, definitionhandoff.MaxFieldsPerCreateModel+1)
	definition := Migration{App: "blog", Name: "0001_oversized", Operations: []Operation{CreateModel{
		AppLabel: "blog", Model: ir.Model{Name: "post", GoName: "Post", DBTable: "blog_post", Fields: oversizedFields},
	}}}
	session := newLifecycleTestSession(nil, nil)
	fake := newLifecycleTestBackend(session)
	ctx := definitionhandoff.WithContext(context.Background(), handoff)
	_, err = (Executor{Backend: fake}).Migrate(ctx, []Migration{definition}, LatestLifecycleRequest())
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 0, "CreateModel")
	var migrationError *Error
	if !errors.As(err, &migrationError) {
		t.Fatalf("carrier resource error = %#v, want *Error", err)
	}
	if !strings.Contains(migrationError.Cause.Error(), "field_count") {
		t.Fatalf("carrier resource error = %v", migrationError.Cause)
	}
	if fake.openCount != 0 || fake.legacyBeginCount != 0 || session.readCount != 0 || session.beginCount != 0 {
		t.Fatalf("oversized carrier touched I/O: open=%d legacy=%d read=%d begin=%d", fake.openCount, fake.legacyBeginCount, session.readCount, session.beginCount)
	}
}

func TestExecutorMigrateHistoryAndTargetPrecedence(t *testing.T) {
	t.Parallel()

	t.Run("known inconsistent history precedes contained invalid target", func(t *testing.T) {
		session := newLifecycleTestSession(lifecycleRecords(lifecycleAlpha2), nil)
		fake := newLifecycleTestBackend(session)
		state, err := (Executor{Backend: fake}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			TargetedLifecycleRequest(Target{}),
		)
		assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, lifecycleAlpha2, lifecycleAlpha1)
		if len(state.Apps()) != 0 || session.beginCount != 0 || session.readCount != 1 || session.closeCount != 1 {
			t.Fatalf("state/calls = apps:%v read:%d begin:%d close:%d", state.Apps(), session.readCount, session.beginCount, session.closeCount)
		}
	})

	t.Run("valid history exposes contained invalid target", func(t *testing.T) {
		session := newLifecycleTestSession(lifecycleRecords(lifecycleAlpha1), nil)
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			TargetedLifecycleRequest(Target{}),
		)
		assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("planning error did not return the reconstructed durable state")
		}
		if session.beginCount != 0 || session.readCount != 1 || session.closeCount != 1 {
			t.Fatalf("calls = read:%d begin:%d close:%d", session.readCount, session.beginCount, session.closeCount)
		}
	})

	t.Run("invalid snapshot record is semantic history error", func(t *testing.T) {
		session := newLifecycleTestSession([]backend.AppliedMigration{{Name: "0001"}}, nil)
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		assertPlanningError(t, err, CategoryHistory, CodeInvalidAppliedState, MigrationKey{Name: "0001"}, MigrationKey{})
		if session.beginCount != 0 || session.closeCount != 1 {
			t.Fatalf("calls = begin:%d close:%d", session.beginCount, session.closeCount)
		}
	})

	t.Run("generic snapshot failure remains recorder read failure", func(t *testing.T) {
		cause := errors.New("read sentinel")
		session := newLifecycleTestSession(nil, nil)
		session.readErr = cause
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		var recorderError *RecorderError
		if !errors.As(err, &recorderError) || recorderError.Code != CodeReadFailed || !errors.Is(err, cause) {
			t.Fatalf("Migrate() error = %#v, want recorder read failure", err)
		}
	})

	t.Run("malformed raw snapshot failure is integrity", func(t *testing.T) {
		cause := errors.New("malformed fence")
		session := newLifecycleTestSession(nil, nil)
		session.readErr = &backend.RevisionFenceError{Cause: cause}
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryHistory, CodeHistoryRevisionIntegrity, NoOperation, "")
		if !errors.Is(err, cause) {
			t.Fatalf("Migrate() error = %v, want cause", err)
		}
	})

	t.Run("fully applied latest is read-only no-op", func(t *testing.T) {
		session := newLifecycleTestSession(lifecycleRecords(lifecycleAlpha1, lifecycleAlpha2, lifecycleAlpha3, lifecycleBeta1), nil)
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		if err != nil {
			t.Fatalf("Migrate(latest no-op) error = %v", err)
		}
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("latest no-op did not reconstruct applied state")
		}
		if session.readCount != 1 || session.beginCount != 0 || session.closeCount != 1 {
			t.Fatalf("calls = read:%d begin:%d close:%d", session.readCount, session.beginCount, session.closeCount)
		}
	})
}

func TestExecutorMigrateLatestAndAppZeroUseCanonicalPlans(t *testing.T) {
	t.Parallel()

	t.Run("fresh latest", func(t *testing.T) {
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(4))
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		if err != nil {
			t.Fatalf("Migrate(latest) error = %v", err)
		}
		assertLifecycleTransitions(t, session.transitions,
			lifecycleTransition(lifecycleAlpha1, backend.HistoryTransitionApply),
			lifecycleTransition(lifecycleAlpha2, backend.HistoryTransitionApply),
			lifecycleTransition(lifecycleAlpha3, backend.HistoryTransitionApply),
			lifecycleTransition(lifecycleBeta1, backend.HistoryTransitionApply),
		)
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("latest state is missing alpha.article")
		}
		if _, exists := state.Model("beta", "entry"); !exists {
			t.Fatal("latest state is missing beta.entry")
		}
	})

	t.Run("alpha zero keeps Go canonical incomparable sibling order", func(t *testing.T) {
		session := newLifecycleTestSession(
			lifecycleRecords(lifecycleAlpha1, lifecycleAlpha2, lifecycleAlpha3, lifecycleBeta1),
			lifecycleCommittedTransactions(4),
		)
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(),
			lifecycleTestDefinitions(),
			TargetedLifecycleRequest(ZeroTarget("alpha")),
		)
		if err != nil {
			t.Fatalf("Migrate(alpha zero) error = %v", err)
		}
		assertLifecycleTransitions(t, session.transitions,
			lifecycleTransition(lifecycleAlpha3, backend.HistoryTransitionUnapply),
			lifecycleTransition(lifecycleAlpha2, backend.HistoryTransitionUnapply),
			lifecycleTransition(lifecycleBeta1, backend.HistoryTransitionUnapply),
			lifecycleTransition(lifecycleAlpha1, backend.HistoryTransitionUnapply),
		)
		if len(state.Apps()) != 0 {
			t.Fatalf("alpha zero state apps = %v, want empty", state.Apps())
		}
	})
}

func TestExecutorMigrateSnapshotsDefinitionsAndTargetsBeforeIO(t *testing.T) {
	t.Parallel()

	definitions := lifecycleTestDefinitions()
	create := definitions[0].Operations[0].(CreateModel)
	create.Model.Fields = append(create.Model.Fields, ir.Field{
		Name:      "title",
		GoName:    "Title",
		Column:    "title",
		Kind:      ir.FieldChar,
		MaxLength: 64,
		Default:   &ir.ScalarDefault{Kind: ir.ScalarString, String: "original"},
	})
	createPointer := &create
	definitions[0].Operations[0] = createPointer
	add := definitions[1].Operations[0].(AddField)
	addPointer := &add
	definitions[1].Operations[0] = addPointer

	rest := []Target{NamedTarget(lifecycleBeta1)}
	request := TargetedLifecycleRequest(NamedTarget(lifecycleAlpha3), rest...)
	rest[0] = Target{}

	session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(4))
	session.readHook = func() {
		definitions[0].App = "mutated"
		definitions[1].Dependencies[0] = lifecycleBeta1
		definitions[2].Dependencies[0] = lifecycleBeta1
		createPointer.Model.Name = "mutated"
		createPointer.Model.Fields[0].Name = "mutated"
		createPointer.Model.Fields[1].Default.String = "mutated"
		createPointer.Model.Fields = nil
		addPointer.ModelName = "missing"
		addPointer.Field.Default.Boolean = true
		definitions[0].Operations = nil
		definitions[1].Operations[0] = AddField{AppLabel: "mutated"}
	}
	state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
		context.Background(), definitions, request,
	)
	if err != nil {
		t.Fatalf("Migrate(snapshot aliases) error = %v", err)
	}
	assertLifecycleTransitions(t, session.transitions,
		lifecycleTransition(lifecycleAlpha1, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleAlpha2, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleAlpha3, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleBeta1, backend.HistoryTransitionApply),
	)
	model, exists := state.Model("alpha", "article")
	if !exists || model.Name != "article" || len(model.Fields) != 3 ||
		model.Fields[1].Name != "title" || model.Fields[1].Default == nil ||
		model.Fields[1].Default.String != "original" {
		t.Fatalf("snapshotted CreateModel nested IR = %#v, exists=%t", model, exists)
	}
	field, exists := findField(state, "alpha", "article", "published")
	if !exists || field.Default == nil || field.Default.Boolean {
		t.Fatalf("snapshotted pointer AddField = %#v, exists=%t", field, exists)
	}
}

func TestExecutorMigratePreflightsCompletePlanBeforeFirstTransaction(t *testing.T) {
	t.Parallel()

	t.Run("invalid tail state", func(t *testing.T) {
		definitions := lifecycleTestDefinitions()
		definitions[1].Operations = []Operation{AddField{
			AppLabel:  "alpha",
			ModelName: "missing",
			Field:     lifecyclePublishedField(),
		}}
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(4))
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), definitions, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryState, CodeInvalidState, 0, "AddField")
		if len(state.Apps()) != 0 || session.beginCount != 0 {
			t.Fatalf("invalid tail state/calls = apps:%v begin:%d", state.Apps(), session.beginCount)
		}
	})

	t.Run("mixed direction", func(t *testing.T) {
		gamma := MigrationKey{App: "gamma", Name: "0001_initial"}
		definitions := append(
			lifecycleTestDefinitions(),
			Migration{App: gamma.App, Name: gamma.Name},
		)
		session := newLifecycleTestSession(lifecycleRecords(lifecycleAlpha1, lifecycleAlpha2), lifecycleCommittedTransactions(3))
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(),
			definitions,
			TargetedLifecycleRequest(ZeroTarget("alpha"), NamedTarget(gamma)),
		)
		assertMigrationError(t, err, CategoryExecution, CodeMixedDirections, NoOperation, "")
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("mixed plan did not return reconstructed durable state")
		}
		if session.beginCount != 0 {
			t.Fatalf("BeginFencedMigration() calls = %d, want 0", session.beginCount)
		}
	})

	t.Run("inner cancellation gate", func(t *testing.T) {
		definitions := lifecycleTestDefinitions()[:1]
		operations, after, err := preflight(EmptyProjectState(), definitions[0], DirectionForward)
		if err != nil {
			t.Fatal(err)
		}
		prepared := preparedPlanStep{
			step:       PlanStep{Key: lifecycleAlpha1, Direction: DirectionForward},
			migration:  definitions[0],
			operations: operations,
			after:      after,
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(1))
		state, err := executeFencedMigration(ctx, session, EmptyProjectState(), prepared)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) || session.beginCount != 0 || len(state.Apps()) != 0 {
			t.Fatalf("gate result = err:%v begin:%d apps:%v", err, session.beginCount, state.Apps())
		}
	})
}

func TestExecutorMigrateMapsRevisionFenceErrorsAtEveryStage(t *testing.T) {
	t.Parallel()

	classifications := []struct {
		name     string
		kind     backend.RevisionFenceFailureKind
		category ErrorCategory
		code     ErrorCode
	}{
		{name: "adoption", kind: backend.RevisionFenceFailureAdoptionRequired, category: CategoryCapability, code: CodeRevisionFenceAdoptionRequired},
		{name: "stale", kind: backend.RevisionFenceFailureStale, category: CategoryConflict, code: CodeStaleHistoryRevision},
		{name: "contended", kind: backend.RevisionFenceFailureContended, category: CategoryTransaction, code: CodeHistoryRevisionContended},
		{name: "integrity", kind: backend.RevisionFenceFailureIntegrity, category: CategoryHistory, code: CodeHistoryRevisionIntegrity},
		{name: "zero", kind: 0, category: CategoryHistory, code: CodeHistoryRevisionIntegrity},
		{name: "unknown", kind: backend.RevisionFenceFailureKind(255), category: CategoryHistory, code: CodeHistoryRevisionIntegrity},
	}

	stages := []string{"open", "read", "begin", "schema", "recorder", "commit"}
	for _, stage := range stages {
		stage := stage
		for _, classification := range classifications {
			classification := classification
			t.Run(stage+"/"+classification.name, func(t *testing.T) {
				cause := errors.New(stage + " " + classification.name)
				raw := fmt.Errorf("wrapped raw fence failure: %w", &backend.RevisionFenceError{
					Kind:  classification.kind,
					Cause: cause,
				})
				transaction := newLifecycleTestTransaction()
				session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
				fake := newLifecycleTestBackend(session)
				definitions := lifecycleTestDefinitions()[:1]
				operationIndex := NoOperation
				operation := ""

				switch stage {
				case "open":
					fake.openErr = raw
				case "read":
					session.readErr = raw
				case "begin":
					session.beginErrors = []error{raw}
				case "schema":
					transaction.failures["create_model"] = raw
					operationIndex = 0
					operation = "CreateModel"
				case "recorder":
					definitions = []Migration{{App: "alpha", Name: "0001_empty"}}
					transaction.failures["record_applied"] = raw
				case "commit":
					transaction.outcome = backend.CommitOutcome{Durability: backend.CommitRolledBack}
					transaction.commitErr = raw
				default:
					t.Fatalf("unknown stage %q", stage)
				}

				_, err := (Executor{Backend: fake}).Migrate(
					context.Background(), definitions, LatestLifecycleRequest(),
				)
				assertMigrationError(t, err, classification.category, classification.code, operationIndex, operation)
				if !errors.Is(err, cause) {
					t.Fatalf("Migrate(%s/%s) error = %v, want raw cause", stage, classification.name, err)
				}
				if session.closeCount != 1 {
					t.Fatalf("Migrate(%s/%s) Close() calls = %d, want 1", stage, classification.name, session.closeCount)
				}
			})
		}
	}

	t.Run("generic recorder capability preserves recorder taxonomy", func(t *testing.T) {
		cause := backend.NewCapabilityError("migration_recorder", "recorder write unsupported", nil)
		transaction := newLifecycleTestTransaction()
		transaction.failures["record_applied"] = cause
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), []Migration{{App: "alpha", Name: "0001_empty"}}, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryRecorder, CodeRecordFailed, NoOperation, "")
		if !errors.Is(err, cause) {
			t.Fatalf("Migrate() error = %v, want generic recorder capability cause", err)
		}
	})

	t.Run("typed nil raw carrier is integrity", func(t *testing.T) {
		var raw *backend.RevisionFenceError
		transaction := newLifecycleTestTransaction()
		transaction.failures["record_applied"] = raw
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), []Migration{{App: "alpha", Name: "0001_empty"}}, LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryHistory, CodeHistoryRevisionIntegrity, NoOperation, "")
	})
}

func TestExecutorMigrateCommitOutcomeStateMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		outcome     backend.CommitDurability
		commitErr   error
		category    ErrorCategory
		code        ErrorCode
		wantPost    bool
		wantSuccess bool
	}{
		{name: "committed", outcome: backend.CommitCommitted, wantPost: true, wantSuccess: true},
		{name: "committed cleanup error", outcome: backend.CommitCommitted, commitErr: errors.New("cleanup"), category: CategoryTransaction, code: CodeCommitCleanupFailed, wantPost: true},
		{name: "rolled back error", outcome: backend.CommitRolledBack, commitErr: errors.New("rolled back"), category: CategoryTransaction, code: CodeCommitFailed},
		{name: "rolled back nil error", outcome: backend.CommitRolledBack, category: CategoryTransaction, code: CodeCommitFailed},
		{name: "unknown error", outcome: backend.CommitUnknown, commitErr: errors.New("unknown"), category: CategoryTransaction, code: CodeCommitOutcomeUnknown},
		{name: "unknown nil error", outcome: backend.CommitUnknown, category: CategoryTransaction, code: CodeCommitOutcomeUnknown},
		{name: "zero outcome", category: CategoryTransaction, code: CodeCommitOutcomeUnknown},
		{name: "invalid outcome", outcome: backend.CommitDurability(255), commitErr: errors.New("invalid"), category: CategoryTransaction, code: CodeCommitOutcomeUnknown},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			transaction := newLifecycleTestTransaction()
			transaction.outcome = backend.CommitOutcome{Durability: test.outcome}
			transaction.commitErr = test.commitErr
			session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
			state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
				context.Background(), lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
			)
			if test.wantSuccess {
				if err != nil {
					t.Fatalf("Migrate() error = %v", err)
				}
			} else {
				assertMigrationError(t, err, test.category, test.code, NoOperation, "")
				if test.commitErr != nil && !errors.Is(err, test.commitErr) {
					t.Fatalf("Migrate() error = %v, want commit cause", err)
				}
			}
			_, post := state.Model("alpha", "article")
			if post != test.wantPost {
				t.Fatalf("returned post-step state = %t, want %t", post, test.wantPost)
			}
			if reflect.DeepEqual(transaction.calls, []string{}) || transaction.calls[len(transaction.calls)-1] != "commit_fenced" {
				t.Fatalf("transaction calls = %v, want terminal commit_fenced", transaction.calls)
			}
			for _, call := range transaction.calls {
				if call == "rollback" {
					t.Fatalf("commit outcome triggered an extra rollback: %v", transaction.calls)
				}
			}
		})
	}

	t.Run("rolled back raw stale preserves conflict", func(t *testing.T) {
		cause := errors.New("commit stale")
		transaction := newLifecycleTestTransaction()
		transaction.outcome = backend.CommitOutcome{Durability: backend.CommitRolledBack}
		transaction.commitErr = &backend.RevisionFenceError{Kind: backend.RevisionFenceFailureStale, Cause: cause}
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryConflict, CodeStaleHistoryRevision, NoOperation, "")
		if !errors.Is(err, cause) || len(state.Apps()) != 0 {
			t.Fatalf("Migrate() = apps:%v err:%v", state.Apps(), err)
		}
	})

	t.Run("multi-step noncommitted outcomes preserve prefix stop tail and outrank close", func(t *testing.T) {
		tests := []struct {
			name       string
			durability backend.CommitDurability
			code       ErrorCode
		}{
			{name: "rolled back", durability: backend.CommitRolledBack, code: CodeCommitFailed},
			{name: "unknown", durability: backend.CommitUnknown, code: CodeCommitOutcomeUnknown},
			{name: "zero", durability: 0, code: CodeCommitOutcomeUnknown},
		}
		for _, test := range tests {
			test := test
			t.Run(test.name, func(t *testing.T) {
				commitCause := errors.New(test.name + " commit sentinel")
				closeCause := errors.New(test.name + " close sentinel")
				first := newLifecycleTestTransaction()
				second := newLifecycleTestTransaction()
				second.outcome = backend.CommitOutcome{Durability: test.durability}
				second.commitErr = commitCause
				third := newLifecycleTestTransaction()
				session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{first, second, third})
				session.closeErr = closeCause

				state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
					context.Background(), lifecycleTestDefinitions()[:3], LatestLifecycleRequest(),
				)
				assertMigrationError(t, err, CategoryTransaction, test.code, NoOperation, "")
				if !errors.Is(err, commitCause) || !errors.Is(err, closeCause) {
					t.Fatalf("Migrate(%s) error = %v, want commit and close causes", test.name, err)
				}
				if _, exists := state.Model("alpha", "article"); !exists {
					t.Fatalf("Migrate(%s) lost the first committed prefix", test.name)
				}
				if _, exists := findField(state, "alpha", "article", "published"); exists {
					t.Fatalf("Migrate(%s) returned the noncommitted second step", test.name)
				}
				if session.beginCount != 2 || len(third.calls) != 0 {
					t.Fatalf("Migrate(%s) begin calls = %d tail calls = %v, want 2 and none", test.name, session.beginCount, third.calls)
				}
				if session.closeCount != 1 {
					t.Fatalf("Migrate(%s) Close() calls = %d, want 1", test.name, session.closeCount)
				}
			})
		}
	})

	t.Run("committed cleanup error advances state and stops tail", func(t *testing.T) {
		cleanupCause := errors.New("post-commit connection cleanup")
		closeCause := errors.New("session close after commit cleanup")
		first := newLifecycleTestTransaction()
		first.outcome = backend.CommitOutcome{Durability: backend.CommitCommitted}
		first.commitErr = cleanupCause
		second := newLifecycleTestTransaction()
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{first, second})
		session.closeErr = closeCause
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions()[:2], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeCommitCleanupFailed, NoOperation, "")
		if !errors.Is(err, cleanupCause) || !errors.Is(err, closeCause) {
			t.Fatalf("Migrate() error = %v, want commit cleanup and session close causes", err)
		}
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("committed cleanup error lost post-step durable state")
		}
		if _, exists := findField(state, "alpha", "article", "published"); exists {
			t.Fatal("committed cleanup error executed the tail migration")
		}
		if session.beginCount != 1 {
			t.Fatalf("BeginFencedMigration() calls = %d, want 1", session.beginCount)
		}
	})
}

func TestExecutorMigratePreservesLastDurableStateAndStopsTail(t *testing.T) {
	t.Parallel()

	first := newLifecycleTestTransaction()
	second := newLifecycleTestTransaction()
	failure := errors.New("A2 add failure")
	second.failures["add_field"] = failure
	third := newLifecycleTestTransaction()
	session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{first, second, third})
	state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
		context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
	)
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "AddField")
	if !errors.Is(err, failure) {
		t.Fatalf("Migrate() error = %v, want operation cause", err)
	}
	if _, exists := state.Model("alpha", "article"); !exists {
		t.Fatal("middle failure lost first committed migration state")
	}
	if _, exists := findField(state, "alpha", "article", "published"); exists {
		t.Fatal("middle failure returned rolled-back A2 state")
	}
	if _, exists := state.Model("beta", "entry"); exists {
		t.Fatal("middle failure executed beta tail")
	}
	if session.beginCount != 2 {
		t.Fatalf("BeginFencedMigration() calls = %d, want 2", session.beginCount)
	}
	assertCalls(t, second.calls, "add_field", "rollback")
	if len(third.calls) != 0 {
		t.Fatalf("tail transaction calls = %v, want empty", third.calls)
	}
}

func TestExecutorMigrateSessionCloseAndRollbackErrorPriority(t *testing.T) {
	t.Parallel()

	t.Run("close-only failure preserves resulting state", func(t *testing.T) {
		closeCause := errors.New("close sentinel")
		session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(1))
		session.closeErr = closeCause
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeSessionCloseFailed, NoOperation, "")
		if !errors.Is(err, closeCause) {
			t.Fatalf("Migrate() error = %v, want close cause", err)
		}
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("session close failure lost committed resulting state")
		}
		assertDetachedBoundedCleanup(t, session.closeContextCanceled, session.closeDeadline)
	})

	t.Run("primary operation then rollback then close", func(t *testing.T) {
		operationCause := errors.New("operation sentinel")
		rollbackCause := errors.New("rollback sentinel")
		closeCause := errors.New("close sentinel")
		transaction := newLifecycleTestTransaction()
		transaction.failures["create_model"] = operationCause
		transaction.failures["rollback"] = rollbackCause
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		session.closeErr = closeCause
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			context.Background(), lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "CreateModel")
		var primary *Error
		if !errors.As(err, &primary) {
			t.Fatalf("Migrate() error = %#v, want primary *Error", err)
		}
		if primary.RollbackCause != rollbackCause {
			t.Fatalf("primary RollbackCause = %#v, want rollback sentinel only", primary.RollbackCause)
		}
		if !errors.Is(err, operationCause) || !errors.Is(err, rollbackCause) || !errors.Is(err, closeCause) {
			t.Fatalf("joined Migrate() error = %v, want operation+rollback+close causes", err)
		}
		assertDetachedBoundedCleanup(t, transaction.rollbackContextCanceled, transaction.rollbackDeadline)
		assertDetachedBoundedCleanup(t, session.closeContextCanceled, session.closeDeadline)
	})

	t.Run("non-nil session returned with open error is still closed", func(t *testing.T) {
		openCause := errors.New("open sentinel")
		closeCause := errors.New("close sentinel")
		session := newLifecycleTestSession(nil, nil)
		session.closeErr = closeCause
		fake := newLifecycleTestBackend(session)
		fake.openErr = openCause
		_, err := (Executor{Backend: fake}).Migrate(
			context.Background(), lifecycleTestDefinitions(), LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
		if !errors.Is(err, openCause) || !errors.Is(err, closeCause) || session.closeCount != 1 || session.readCount != 0 {
			t.Fatalf("Migrate(open+close) = err:%v read:%d close:%d", err, session.readCount, session.closeCount)
		}
	})
}

func TestExecutorMigrateCancellationUsesDurableBoundariesAndDetachedCleanup(t *testing.T) {
	t.Parallel()

	t.Run("cancellation after final commit remains success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		transaction := newLifecycleTestTransaction()
		transaction.commitHook = cancel
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			ctx, lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		if err != nil {
			t.Fatalf("Migrate(final cancellation) error = %v", err)
		}
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("final cancellation lost committed state")
		}
		assertDetachedBoundedCleanup(t, session.closeContextCanceled, session.closeDeadline)
	})

	t.Run("cancellation between steps returns first commit and stops", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		first := newLifecycleTestTransaction()
		first.commitHook = cancel
		second := newLifecycleTestTransaction()
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{first, second})
		state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			ctx, lifecycleTestDefinitions()[:2], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Migrate() error = %v, want context.Canceled", err)
		}
		if _, exists := state.Model("alpha", "article"); !exists {
			t.Fatal("between-step cancellation lost first commit")
		}
		if _, exists := findField(state, "alpha", "article", "published"); exists {
			t.Fatal("between-step cancellation executed second step")
		}
		if session.beginCount != 1 {
			t.Fatalf("BeginFencedMigration() calls = %d, want 1", session.beginCount)
		}
	})

	t.Run("operation cancellation rolls back with detached deadline", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		operationCause := errors.New("operation after cancellation")
		transaction := newLifecycleTestTransaction()
		transaction.hooks["create_model"] = cancel
		transaction.failures["create_model"] = operationCause
		session := newLifecycleTestSession(nil, []backend.RevisionFencedTransaction{transaction})
		_, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
			ctx, lifecycleTestDefinitions()[:1], LatestLifecycleRequest(),
		)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "CreateModel")
		if !errors.Is(err, operationCause) {
			t.Fatalf("Migrate() error = %v, want operation cause", err)
		}
		assertDetachedBoundedCleanup(t, transaction.rollbackContextCanceled, transaction.rollbackDeadline)
		assertDetachedBoundedCleanup(t, session.closeContextCanceled, session.closeDeadline)
	})
}

func TestExecutorMigrateRepeatedConcurrentCallsAreDeterministicAndImmutable(t *testing.T) {
	t.Parallel()

	definitions := lifecycleTestDefinitions()
	request := LatestLifecycleRequest()
	want := []backend.HistoryTransition{
		lifecycleTransition(lifecycleAlpha1, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleAlpha2, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleAlpha3, backend.HistoryTransitionApply),
		lifecycleTransition(lifecycleBeta1, backend.HistoryTransitionApply),
	}

	const workers = 24
	var wait sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session := newLifecycleTestSession(nil, lifecycleCommittedTransactions(4))
			state, err := (Executor{Backend: newLifecycleTestBackend(session)}).Migrate(
				context.Background(), definitions, request,
			)
			if err != nil {
				errorsByWorker <- err
				return
			}
			if !reflect.DeepEqual(session.transitions, want) {
				errorsByWorker <- fmt.Errorf("transitions = %v, want %v", session.transitions, want)
				return
			}
			if _, exists := state.Model("beta", "entry"); !exists {
				errorsByWorker <- errors.New("result missing beta.entry")
			}
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Error(err)
	}

	if definitions[0].App != "alpha" || definitions[1].Operations[0].(AddField).Field.Default.Boolean {
		t.Fatal("concurrent Migrate calls mutated caller-owned definitions")
	}
}

var (
	lifecycleAlpha1 = MigrationKey{App: "alpha", Name: "0001_initial"}
	lifecycleAlpha2 = MigrationKey{App: "alpha", Name: "0002_published"}
	lifecycleAlpha3 = MigrationKey{App: "alpha", Name: "0003_tail"}
	lifecycleBeta1  = MigrationKey{App: "beta", Name: "0001_entry"}
)

func lifecycleTestDefinitions() []Migration {
	return []Migration{
		{
			App:  lifecycleAlpha1.App,
			Name: lifecycleAlpha1.Name,
			Operations: []Operation{CreateModel{
				AppLabel: "alpha",
				Model: ir.Model{
					Name:    "article",
					GoName:  "Article",
					DBTable: "alpha_article",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					},
				},
			}},
		},
		{
			App:          lifecycleAlpha2.App,
			Name:         lifecycleAlpha2.Name,
			Dependencies: []MigrationKey{lifecycleAlpha1},
			Operations: []Operation{AddField{
				AppLabel:  "alpha",
				ModelName: "article",
				Field:     lifecyclePublishedField(),
			}},
		},
		{
			App:          lifecycleAlpha3.App,
			Name:         lifecycleAlpha3.Name,
			Dependencies: []MigrationKey{lifecycleAlpha2},
		},
		{
			App:          lifecycleBeta1.App,
			Name:         lifecycleBeta1.Name,
			Dependencies: []MigrationKey{lifecycleAlpha1},
			Operations: []Operation{CreateModel{
				AppLabel: "beta",
				Model: ir.Model{
					Name:    "entry",
					GoName:  "Entry",
					DBTable: "beta_entry",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					},
				},
			}},
		},
	}
}

func lifecycleLoadedRelationCreateDefinitions() []Migration {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	return []Migration{
		{
			App: targetKey.App, Name: targetKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "authors", Model: lifecycleLoadedAuthorModel()}},
		},
		{
			App: "blog", Name: "0001_post", Dependencies: []MigrationKey{targetKey},
			Operations: []Operation{CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "post", GoName: "Post", DBTable: "blog_post",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					lifecycleLoadedAuthorField(),
				},
			}}},
		},
		{
			App: "zeta", Name: "0001_note",
			Operations: []Operation{CreateModel{AppLabel: "zeta", Model: ir.Model{
				Name: "note", GoName: "Note", DBTable: "zeta_note",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
	}
}

func lifecycleLoadedRelationAddDefinitions() []Migration {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	sourceKey := MigrationKey{App: "blog", Name: "0001_post"}
	return []Migration{
		{
			App: targetKey.App, Name: targetKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "authors", Model: lifecycleLoadedAuthorModel()}},
		},
		{
			App: sourceKey.App, Name: sourceKey.Name, Dependencies: []MigrationKey{targetKey},
			Operations: []Operation{CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "post", GoName: "Post", DBTable: "blog_post",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
		{
			App: "blog", Name: "0002_post_author", Dependencies: []MigrationKey{sourceKey},
			Operations: []Operation{AddField{
				AppLabel: "blog", ModelName: "post", Field: lifecycleLoadedAuthorField(),
			}},
		},
	}
}

func lifecycleLoadedCompleteIntentDefinitions() []Migration {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	return []Migration{
		{
			App: targetKey.App, Name: targetKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "authors", Model: lifecycleLoadedAuthorModel()}},
		},
		{
			App: "blog", Name: "0001_post", Dependencies: []MigrationKey{targetKey},
			Operations: []Operation{
				CreateModel{AppLabel: "blog", Model: ir.Model{
					Name: "post", GoName: "Post", DBTable: "blog_post",
					Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
				}},
				AddField{AppLabel: "blog", ModelName: "post", Field: lifecyclePublishedField()},
				AddField{AppLabel: "blog", ModelName: "post", Field: lifecycleLoadedAuthorField()},
			},
		},
	}
}

func lifecycleLoadedOversizedRelationIntentDefinitions() []Migration {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	sourceKey := MigrationKey{App: "blog", Name: "0001_post"}
	prefixKey := MigrationKey{App: "blog", Name: "0002_prefix"}
	operations := make([]Operation, 0, 513)
	for index := 0; index < 512; index++ {
		name := fmt.Sprintf("field_%04d", index)
		operations = append(operations, AddField{
			AppLabel:  "blog",
			ModelName: "post",
			Field: ir.Field{
				Name: name, GoName: fmt.Sprintf("Field%04d", index), Column: name,
				Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean},
			},
		})
	}
	operations = append(operations, CreateModel{AppLabel: "blog", Model: ir.Model{
		Name: "comment", GoName: "Comment", DBTable: "blog_comment",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{
				Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
					Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Name: "comments"},
					OnDelete: ir.DeleteProtect,
				},
			},
		},
	}})
	return []Migration{
		{
			App: targetKey.App, Name: targetKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "authors", Model: lifecycleLoadedAuthorModel()}},
		},
		{
			App: sourceKey.App, Name: sourceKey.Name, Dependencies: []MigrationKey{targetKey},
			Operations: []Operation{CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "post", GoName: "Post", DBTable: "blog_post",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
		{
			App: prefixKey.App, Name: prefixKey.Name, Dependencies: []MigrationKey{sourceKey},
			Operations: []Operation{AddField{
				AppLabel: "blog", ModelName: "post",
				Field: ir.Field{Name: "prefix", GoName: "Prefix", Column: "prefix", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean}},
			}},
		},
		{
			App: "blog", Name: "0003_oversized_relation_create", Dependencies: []MigrationKey{prefixKey},
			Operations: operations,
		},
	}
}

func lifecycleAllRelationCapabilities() backend.RelationMigrationCapabilities {
	return backend.RelationMigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKeyByTableRemake:     true,
	}
}

func lifecycleLoadedAuthorModel() ir.Model {
	return ir.Model{
		Name: "author", GoName: "Author", DBTable: "authors_author",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
}

func lifecycleLoadedAuthorField() ir.Field {
	return ir.Field{
		Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "posts"},
			OnDelete:    ir.DeleteProtect,
		},
	}
}

func lifecycleLoadedContext(t *testing.T, definitions []Migration) context.Context {
	return lifecycleLoadedContextFrom(t, context.Background(), definitions)
}

func lifecycleLoadedContextFrom(t *testing.T, base context.Context, definitions []Migration) context.Context {
	t.Helper()
	records := make([]definitionhandoff.Record, len(definitions))
	for index := range definitions {
		definition, err := migrationHandoffDefinition(definitions[index])
		if err != nil {
			t.Fatalf("migrationHandoffDefinition(%d): %v", index, err)
		}
		profile := definitionhandoff.Compatibility{
			DefinitionFormat: 1,
			LoaderABI:        1,
			OperationCodec:   1,
			SchemaIR:         2,
		}
		if migrationContainsRelation(definitions[index]) {
			profile = definitionhandoff.Compatibility{
				DefinitionFormat: 1,
				LoaderABI:        2,
				OperationCodec:   2,
				SchemaIR:         3,
			}
		}
		records[index] = definitionhandoff.Record{
			SourceID:   fmt.Sprintf("loaded-%d", index),
			Producer:   definitionhandoff.Producer{Name: "lifecycle-test", Version: "1"},
			Profile:    profile,
			Definition: definition,
		}
	}
	handoff, err := definitionhandoff.New(records)
	if err != nil {
		t.Fatalf("definitionhandoff.New(): %v", err)
	}
	return definitionhandoff.WithContext(base, handoff)
}

func lifecyclePublishedField() ir.Field {
	return ir.Field{
		Name:     "published",
		GoName:   "Published",
		Column:   "published",
		Kind:     ir.FieldBoolean,
		Default:  &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
		Nullable: false,
	}
}

func lifecycleRecords(keys ...MigrationKey) []backend.AppliedMigration {
	records := make([]backend.AppliedMigration, len(keys))
	for index, key := range keys {
		records[index] = backend.AppliedMigration{App: key.App, Name: key.Name}
	}
	return records
}

func lifecycleTransition(key MigrationKey, kind backend.HistoryTransitionKind) backend.HistoryTransition {
	return backend.HistoryTransition{
		Migration: backend.AppliedMigration{App: key.App, Name: key.Name},
		Kind:      kind,
	}
}

func assertLifecycleTransitions(t *testing.T, got []backend.HistoryTransition, want ...backend.HistoryTransition) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
}

func assertDetachedBoundedCleanup(t *testing.T, canceled bool, deadline time.Time) {
	t.Helper()
	if canceled {
		t.Fatal("cleanup context inherited caller cancellation")
	}
	if deadline.IsZero() {
		t.Fatal("cleanup context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > lifecycleCleanupTimeout+time.Second {
		t.Fatalf("cleanup deadline remaining = %s, want within %s", remaining, lifecycleCleanupTimeout)
	}
}

type lifecycleTestBackend struct {
	session                 backend.RevisionFencedSession
	openErr                 error
	openHook                func()
	openCount               int
	legacyBeginCount        int
	relationCapabilities    backend.RelationMigrationCapabilities
	relationCapabilityCount int
	relationCapabilityHook  func()
}

var _ backend.AtomicBackend = (*lifecycleTestBackend)(nil)
var _ backend.RevisionFencedBackend = (*lifecycleTestBackend)(nil)
var _ backend.RelationRevisionFencedBackend = (*lifecycleTestBackend)(nil)

func newLifecycleTestBackend(session backend.RevisionFencedSession) *lifecycleTestBackend {
	return &lifecycleTestBackend{session: session}
}

func (b *lifecycleTestBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	b.legacyBeginCount++
	return nil, errors.New("legacy migration path must not be used")
}

func (b *lifecycleTestBackend) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	b.openCount++
	if b.openHook != nil {
		b.openHook()
	}
	return b.session, b.openErr
}

func (b *lifecycleTestBackend) RelationMigrationCapabilities() backend.RelationMigrationCapabilities {
	b.relationCapabilityCount++
	if b.relationCapabilityHook != nil {
		b.relationCapabilityHook()
	}
	return b.relationCapabilities
}

type lifecycleNoRelationBackend struct {
	delegate *lifecycleTestBackend
}

var _ backend.AtomicBackend = (*lifecycleNoRelationBackend)(nil)
var _ backend.RevisionFencedBackend = (*lifecycleNoRelationBackend)(nil)

func (b *lifecycleNoRelationBackend) BeginMigration(ctx context.Context) (backend.Transaction, error) {
	return b.delegate.BeginMigration(ctx)
}

func (b *lifecycleNoRelationBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	return b.delegate.OpenRevisionFencedSession(ctx)
}

type lifecycleTestSession struct {
	records              []backend.AppliedMigration
	readErr              error
	readHook             func()
	readCount            int
	transactions         []backend.RevisionFencedTransaction
	beginErrors          []error
	beginCount           int
	relationBeginCount   int
	relationBeginHook    func()
	transitions          []backend.HistoryTransition
	relationIntents      []backend.RelationMigrationIntent
	closeErr             error
	closeCount           int
	closeContextCanceled bool
	closeDeadline        time.Time
}

var _ backend.RevisionFencedSession = (*lifecycleTestSession)(nil)
var _ backend.RelationRevisionFencedSession = (*lifecycleTestSession)(nil)

func newLifecycleTestSession(records []backend.AppliedMigration, transactions []backend.RevisionFencedTransaction) *lifecycleTestSession {
	return &lifecycleTestSession{records: records, transactions: transactions}
}

func (s *lifecycleTestSession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	s.readCount++
	if s.readHook != nil {
		s.readHook()
	}
	return s.records, s.readErr
}

func (s *lifecycleTestSession) BeginFencedMigration(_ context.Context, transition backend.HistoryTransition) (backend.RevisionFencedTransaction, error) {
	index := s.beginCount + s.relationBeginCount
	s.beginCount++
	s.transitions = append(s.transitions, transition)
	var transaction backend.RevisionFencedTransaction
	if index < len(s.transactions) {
		transaction = s.transactions[index]
	}
	var err error
	if index < len(s.beginErrors) {
		err = s.beginErrors[index]
	}
	return transaction, err
}

func (s *lifecycleTestSession) BeginRelationFencedMigration(
	_ context.Context,
	transition backend.HistoryTransition,
	intent backend.RelationMigrationIntent,
) (backend.RevisionFencedTransaction, error) {
	index := s.beginCount + s.relationBeginCount
	s.relationBeginCount++
	s.transitions = append(s.transitions, transition)
	s.relationIntents = append(s.relationIntents, cloneLifecycleRelationIntent(intent))
	if s.relationBeginHook != nil {
		s.relationBeginHook()
	}
	var transaction backend.RevisionFencedTransaction
	if index < len(s.transactions) {
		transaction = s.transactions[index]
	}
	var err error
	if index < len(s.beginErrors) {
		err = s.beginErrors[index]
	}
	return transaction, err
}

func (s *lifecycleTestSession) Close(ctx context.Context) error {
	s.closeCount++
	s.closeContextCanceled = ctx.Err() != nil
	s.closeDeadline, _ = ctx.Deadline()
	return s.closeErr
}

type lifecycleNoRelationSession struct {
	delegate *lifecycleTestSession
}

var _ backend.RevisionFencedSession = (*lifecycleNoRelationSession)(nil)

func (s *lifecycleNoRelationSession) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	return s.delegate.ReadAppliedMigrations(ctx)
}

func (s *lifecycleNoRelationSession) BeginFencedMigration(
	ctx context.Context,
	transition backend.HistoryTransition,
) (backend.RevisionFencedTransaction, error) {
	return s.delegate.BeginFencedMigration(ctx, transition)
}

func (s *lifecycleNoRelationSession) Close(ctx context.Context) error {
	return s.delegate.Close(ctx)
}

type lifecycleTestTransaction struct {
	calls                   []string
	failures                map[string]error
	hooks                   map[string]func()
	outcome                 backend.CommitOutcome
	commitErr               error
	commitHook              func()
	rollbackContextCanceled bool
	rollbackDeadline        time.Time
}

var _ backend.RevisionFencedTransaction = (*lifecycleTestTransaction)(nil)

func newLifecycleTestTransaction() *lifecycleTestTransaction {
	return &lifecycleTestTransaction{
		failures: make(map[string]error),
		hooks:    make(map[string]func()),
		outcome:  backend.CommitOutcome{Durability: backend.CommitCommitted},
	}
}

func lifecycleCommittedTransactions(count int) []backend.RevisionFencedTransaction {
	transactions := make([]backend.RevisionFencedTransaction, count)
	for index := range transactions {
		transactions[index] = newLifecycleTestTransaction()
	}
	return transactions
}

func cloneLifecycleRelationIntent(value backend.RelationMigrationIntent) backend.RelationMigrationIntent {
	cloned := backend.RelationMigrationIntent{
		Operations: make([]backend.RelationMigrationOperation, len(value.Operations)),
	}
	for operationIndex := range value.Operations {
		operation := value.Operations[operationIndex]
		operation.Before = operation.Before.Clone()
		operation.After = operation.After.Clone()
		operation.Targets = make([]backend.RelationMigrationTarget, len(value.Operations[operationIndex].Targets))
		for targetIndex := range value.Operations[operationIndex].Targets {
			target := value.Operations[operationIndex].Targets[targetIndex]
			target.SourceField = target.SourceField.Clone()
			target.TargetModel = target.TargetModel.Clone()
			target.TargetKey = target.TargetKey.Clone()
			operation.Targets[targetIndex] = target
		}
		cloned.Operations[operationIndex] = operation
	}
	return cloned
}

func (t *lifecycleTestTransaction) CreateModel(context.Context, ir.Model) error {
	return t.call("create_model")
}

func (t *lifecycleTestTransaction) DeleteModel(context.Context, ir.Model) error {
	return t.call("delete_model")
}

func (t *lifecycleTestTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	return t.call("add_field")
}

func (t *lifecycleTestTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return t.call("remove_field")
}

func (t *lifecycleTestTransaction) RecordApplied(_ context.Context, app, name string) error {
	t.calls = append(t.calls, fmt.Sprintf("record_applied:%s.%s", app, name))
	if hook := t.hooks["record_applied"]; hook != nil {
		hook()
	}
	return t.failures["record_applied"]
}

func (t *lifecycleTestTransaction) RecordUnapplied(_ context.Context, app, name string) error {
	t.calls = append(t.calls, fmt.Sprintf("record_unapplied:%s.%s", app, name))
	if hook := t.hooks["record_unapplied"]; hook != nil {
		hook()
	}
	return t.failures["record_unapplied"]
}

func (t *lifecycleTestTransaction) CommitFenced(context.Context) (backend.CommitOutcome, error) {
	t.calls = append(t.calls, "commit_fenced")
	if t.commitHook != nil {
		t.commitHook()
	}
	return t.outcome, t.commitErr
}

func (t *lifecycleTestTransaction) Rollback(ctx context.Context) error {
	t.rollbackContextCanceled = ctx.Err() != nil
	t.rollbackDeadline, _ = ctx.Deadline()
	return t.call("rollback")
}

func (t *lifecycleTestTransaction) call(name string) error {
	t.calls = append(t.calls, name)
	if hook := t.hooks[name]; hook != nil {
		hook()
	}
	if err, exists := t.failures[name]; exists {
		return err
	}
	return nil
}
