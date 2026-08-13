package migrationrelation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

func TestRelationBackendCandidateLegacyScalarPortsRemainSourceCompatible(t *testing.T) {
	legacy := &relationBackendLegacyScalarBackend{}
	var scalar migrationbackend.RevisionFencedBackend = legacy
	if scalar == nil {
		t.Fatal("legacy scalar backend unexpectedly nil")
	}
	if _, widened := any(legacy).(relationBackendOptionalBackend); widened {
		t.Fatal("legacy scalar backend unexpectedly satisfies optional relation port")
	}
}

func TestRelationBackendCandidateUnsupportedCapabilityDoesNoIO(t *testing.T) {
	legacy := &relationBackendLegacyScalarBackend{}
	_, err := relationBackendOpenStep(
		context.Background(),
		legacy,
		relationBackendTransition{App: "blog", Name: "0001", Direction: relationBackendApply, ToRevision: 1},
		relationBackendArticleCreateIntent(),
	)
	if !errors.Is(err, relationBackendErrUnsupported) {
		t.Fatalf("relationBackendOpenStep() error = %v, want unsupported", err)
	}
	if legacy.openCalls != 0 {
		t.Fatalf("legacy OpenRevisionFencedSession() calls = %d, want 0", legacy.openCalls)
	}
}

func TestRelationBackendCandidateIntentIsDeepCopiedBeforeSessionAndBegin(t *testing.T) {
	backend := &relationBackendTraceBackend{
		capabilities: relationBackendCapabilities{
			Profile: 1, CreateModel: true, NullableAddField: true,
			EmptyRequiredAddField: true, BoundedRemake: true,
		},
	}
	intent := relationBackendArticleCreateIntent()
	originalTable := intent.Changes[1].After.Table
	originalTarget := intent.Changes[1].After.Relations[0].TargetTable

	opened, err := relationBackendOpenStep(
		context.Background(),
		backend,
		relationBackendTransition{App: "blog", Name: "0001", Direction: relationBackendApply, ToRevision: 1},
		intent,
	)
	if err != nil {
		t.Fatalf("relationBackendOpenStep(): %v", err)
	}
	defer func() { _ = opened.Session.Close(context.Background()) }()
	if len(backend.session.pinned.Changes) < 2 || len(backend.session.pinned.Changes[1].After.Relations) == 0 {
		t.Fatalf("pinned intent lost nested changes: %#v", backend.session.pinned)
	}

	intent.Changes[1].After.Table = "mutated"
	intent.Changes[1].After.Relations[0].TargetTable = "mutated_target"
	if backend.session.pinned.Changes[1].After.Table != originalTable {
		t.Fatalf("pinned table = %q, want %q", backend.session.pinned.Changes[1].After.Table, originalTable)
	}
	if backend.session.pinned.Changes[1].After.Relations[0].TargetTable != originalTarget {
		t.Fatalf("pinned target = %q, want %q", backend.session.pinned.Changes[1].After.Relations[0].TargetTable, originalTarget)
	}
	if got, want := backend.events, []string{"capabilities", "open", "begin"}; !relationBackendStringSlicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}

	t.Run("begin_failure_closes_with_detached_bounded_context", func(t *testing.T) {
		beginCause := errors.New("begin failure sentinel")
		closeCause := errors.New("close failure sentinel")
		ctx, cancel := context.WithCancel(context.Background())
		failureBackend := &relationBackendTraceBackend{
			capabilities: relationBackendCapabilities{
				Profile: 1, CreateModel: true, NullableAddField: true,
				EmptyRequiredAddField: true, BoundedRemake: true,
			},
			beginErr:    beginCause,
			beginCancel: cancel,
			closeErr:    closeCause,
		}
		intent := relationBackendArticleCreateIntent()
		_, err := relationBackendOpenStep(
			ctx,
			failureBackend,
			relationBackendTransition{
				App: intent.App, Name: intent.Name,
				Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
			},
			intent,
		)
		if !errors.Is(err, beginCause) || !errors.Is(err, closeCause) {
			t.Fatalf("begin/close joined error = %v, want both sentinels", err)
		}
		if failureBackend.closeCalls != 1 {
			t.Fatalf("begin failure Close calls = %d, want 1", failureBackend.closeCalls)
		}
		if failureBackend.closeContextErr != nil || !failureBackend.closeHadDeadline {
			t.Fatalf(
				"begin failure Close context err=%v deadline=%t, want live bounded context",
				failureBackend.closeContextErr,
				failureBackend.closeHadDeadline,
			)
		}
	})
}

func TestRelationBackendCandidateOpenAndBeginFailuresCleanUpWithoutPublication(t *testing.T) {
	openCause := errors.New("open partial-session failure sentinel")
	beginCause := errors.New("begin failure sentinel")
	rollbackCause := errors.New("rollback failure sentinel")
	closeCause := errors.New("close failure sentinel")
	tests := []struct {
		name             string
		config           relationBackendOpenBoundaryConfig
		wantEvents       []string
		wantBeginCalls   int
		wantRollbackCall int
		wantCauses       []error
		wantText         string
	}{
		{
			name: "open_partial_session_and_error",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession: true,
				openErr:            openCause,
				cancelDuringOpen:   true,
				closeErr:           closeCause,
			},
			wantEvents: []string{"capabilities", "open", "close"},
			wantCauses: []error{openCause, closeCause},
		},
		{
			name: "open_success_then_request_cancel",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession: true,
				cancelDuringOpen:   true,
				closeErr:           closeCause,
			},
			wantEvents: []string{"capabilities", "open", "close"},
			wantCauses: []error{context.Canceled, closeCause},
		},
		{
			name: "begin_partial_transaction_and_error",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession:      true,
				beginReturnsTransaction: true,
				beginErr:                beginCause,
				cancelDuringBegin:       true,
				rollbackErr:             rollbackCause,
				closeErr:                closeCause,
			},
			wantEvents:       []string{"capabilities", "open", "begin", "rollback", "close"},
			wantBeginCalls:   1,
			wantRollbackCall: 1,
			wantCauses:       []error{beginCause, rollbackCause, closeCause},
		},
		{
			name: "begin_error_and_nil_transaction",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession: true,
				beginErr:           beginCause,
				cancelDuringBegin:  true,
				closeErr:           closeCause,
			},
			wantEvents:     []string{"capabilities", "open", "begin", "close"},
			wantBeginCalls: 1,
			wantCauses:     []error{beginCause, closeCause},
		},
		{
			name: "begin_success_with_nil_transaction",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession: true,
				cancelDuringBegin:  true,
				closeErr:           closeCause,
			},
			wantEvents:     []string{"capabilities", "open", "begin", "close"},
			wantBeginCalls: 1,
			wantCauses:     []error{closeCause},
			wantText:       "begin relation migration returned nil transaction",
		},
		{
			name: "begin_success_then_request_cancel",
			config: relationBackendOpenBoundaryConfig{
				openReturnsSession:      true,
				beginReturnsTransaction: true,
				cancelDuringBegin:       true,
				rollbackErr:             rollbackCause,
				closeErr:                closeCause,
			},
			wantEvents:       []string{"capabilities", "open", "begin", "rollback", "close"},
			wantBeginCalls:   1,
			wantRollbackCall: 1,
			wantCauses:       []error{context.Canceled, rollbackCause, closeCause},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &relationBackendOpenBoundaryBackend{config: test.config, cancel: cancel}
			intent := relationBackendArticleCreateIntent()
			opened, err := relationBackendOpenStep(
				ctx,
				backend,
				relationBackendTransition{
					App: intent.App, Name: intent.Name,
					Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
				},
				intent,
			)
			if err == nil {
				t.Fatal("relationBackendOpenStep() error = nil, want failure")
			}
			if opened.Session != nil || opened.Transaction != nil {
				t.Fatalf("relationBackendOpenStep() published %#v, want zero opened step", opened)
			}
			for _, cause := range test.wantCauses {
				if !errors.Is(err, cause) {
					t.Fatalf("relationBackendOpenStep() error = %v, want cause %v", err, cause)
				}
			}
			if test.wantText != "" && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("relationBackendOpenStep() error = %v, want text %q", err, test.wantText)
			}
			if ctx.Err() != context.Canceled {
				t.Fatalf("request context error = %v, want canceled by boundary fake", ctx.Err())
			}
			if got := backend.events; !relationBackendStringSlicesEqual(got, test.wantEvents) {
				t.Fatalf("events = %#v, want %#v", got, test.wantEvents)
			}
			if backend.capabilityCalls != 1 || backend.openCalls != 1 || backend.unexpectedCalls != 0 {
				t.Fatalf(
					"backend calls = capabilities:%d open:%d unexpected:%d, want 1/1/0",
					backend.capabilityCalls,
					backend.openCalls,
					backend.unexpectedCalls,
				)
			}
			if backend.session == nil {
				t.Fatal("boundary fake did not retain its returned session")
			}
			session := backend.session
			if session.beginCalls != test.wantBeginCalls || session.closeCalls != 1 {
				t.Fatalf(
					"session calls = begin:%d close:%d, want %d/1",
					session.beginCalls,
					session.closeCalls,
					test.wantBeginCalls,
				)
			}
			if session.closeContextErr != nil || !session.closeHadDeadline {
				t.Fatalf(
					"Close context = err:%v deadline:%t, want cancellation-stripped live deadline",
					session.closeContextErr,
					session.closeHadDeadline,
				)
			}
			if test.wantRollbackCall == 0 {
				if session.transaction != nil && session.transaction.rollbackCalls != 0 {
					t.Fatalf("Rollback calls = %d, want 0", session.transaction.rollbackCalls)
				}
				return
			}
			if session.transaction == nil {
				t.Fatal("boundary fake did not retain its returned transaction")
			}
			transaction := session.transaction
			if transaction.rollbackCalls != test.wantRollbackCall {
				t.Fatalf("Rollback calls = %d, want %d", transaction.rollbackCalls, test.wantRollbackCall)
			}
			if transaction.rollbackContextErr != nil || !transaction.rollbackHadDeadline {
				t.Fatalf(
					"Rollback context = err:%v deadline:%t, want cancellation-stripped live deadline",
					transaction.rollbackContextErr,
					transaction.rollbackHadDeadline,
				)
			}
		})
	}
}

func TestRelationBackendCandidateSelfAndCycleRejectBeforeIO(t *testing.T) {
	tests := []struct {
		name   string
		intent relationBackendStepIntent
		want   error
	}{
		{name: "self", intent: relationBackendSelfIntent(), want: relationBackendErrSelf},
		{name: "cycle", intent: relationBackendCycleIntent(), want: relationBackendErrCycle},
		{name: "transient_self_add_then_remove", intent: relationBackendTransientSelfIntent(), want: relationBackendErrSelf},
		{name: "transient_cycle_add_then_remove", intent: relationBackendTransientCycleIntent(), want: relationBackendErrCycle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &relationBackendTraceBackend{
				capabilities: relationBackendCapabilities{
					Profile: 1, CreateModel: true, NullableAddField: true,
					EmptyRequiredAddField: true, BoundedRemake: true,
				},
			}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: "blog", Name: "bad", Direction: relationBackendApply, ToRevision: 1},
				test.intent,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("relationBackendOpenStep() error = %v, want %v", err, test.want)
			}
			if len(backend.events) != 0 {
				t.Fatalf("backend events = %#v, want no capability/session I/O", backend.events)
			}
		})
	}
}

func relationBackendTransientSelfIntent() relationBackendStepIntent {
	before := relationBackendModel{
		Table:   "node",
		Columns: []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
	}
	parent := relationBackendRelation{
		Name: "parent", Column: "parent_id", TargetTable: "node", TargetColumn: "id",
		Nullable: true, OnDelete: relationBackendSetNull, Position: 2,
	}
	after := before.relationBackendClone()
	after.Relations = []relationBackendRelation{parent}
	return relationBackendStepIntent{
		App: "blog", Name: "transient_self",
		Changes: []relationBackendChange{
			{Kind: relationBackendAddField, Before: before, After: after, Relation: parent},
			{Kind: relationBackendRemoveField, Before: after, After: before, Relation: parent},
		},
	}
}

func relationBackendTransientCycleIntent() relationBackendStepIntent {
	left := relationBackendModel{
		Table:   "left_model",
		Columns: []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
		Relations: []relationBackendRelation{{
			Name: "right", Column: "right_id", TargetTable: "right_model", TargetColumn: "id",
			OnDelete: relationBackendProtect, Position: 2,
		}},
	}
	right := relationBackendModel{
		Table:   "right_model",
		Columns: []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
	}
	leftRelation := relationBackendRelation{
		Name: "left", Column: "left_id", TargetTable: "left_model", TargetColumn: "id",
		OnDelete: relationBackendProtect, Position: 2,
	}
	rightWithLeft := right.relationBackendClone()
	rightWithLeft.Relations = []relationBackendRelation{leftRelation}
	return relationBackendStepIntent{
		App: "blog", Name: "transient_cycle",
		Changes: []relationBackendChange{
			{Kind: relationBackendCreateModel, After: left},
			{Kind: relationBackendCreateModel, After: right},
			{Kind: relationBackendAddField, Before: right, After: rightWithLeft, Relation: leftRelation},
			{Kind: relationBackendRemoveField, Before: rightWithLeft, After: right, Relation: leftRelation},
		},
	}
}

func TestRelationBackendCandidateMalformedExactDeltaRejectsBeforeIO(t *testing.T) {
	validAdd := relationBackendNullableAddIntent()
	validRemove := relationBackendNullableRemoveIntent()
	tests := []struct {
		name   string
		intent relationBackendStepIntent
	}{
		{
			name: "add_changes_scalar_column",
			intent: func() relationBackendStepIntent {
				intent := validAdd.relationBackendClone()
				intent.Changes[0].After.Columns[1].NotNull = false
				return intent
			}(),
		},
		{
			name: "add_declares_different_relation",
			intent: func() relationBackendStepIntent {
				intent := validAdd.relationBackendClone()
				intent.Changes[0].Relation.TargetColumn = "other_id"
				return intent
			}(),
		},
		{
			name: "add_includes_two_relations",
			intent: func() relationBackendStepIntent {
				intent := validAdd.relationBackendClone()
				intent.Changes[0].After.Relations = append(
					intent.Changes[0].After.Relations,
					relationBackendRelation{
						Name: "reviewer", Column: "reviewer_id", TargetTable: "author",
						TargetColumn: "id", Nullable: true, OnDelete: relationBackendSetNull, Position: 5,
					},
				)
				return intent
			}(),
		},
		{
			name: "remove_changes_retained_relation",
			intent: func() relationBackendStepIntent {
				intent := validRemove.relationBackendClone()
				intent.Changes[0].After.Relations[0].TargetColumn = "other_id"
				return intent
			}(),
		},
		{
			name: "remove_declares_different_relation",
			intent: func() relationBackendStepIntent {
				intent := validRemove.relationBackendClone()
				intent.Changes[0].Relation.Column = "other_editor_id"
				return intent
			}(),
		},
		{
			name: "remove_reorders_retained_relations",
			intent: func() relationBackendStepIntent {
				before := relationBackendArticleModel(true)
				reviewer := relationBackendRelation{
					Name: "reviewer", Column: "reviewer_id", TargetTable: "author",
					TargetColumn: "id", Nullable: true, OnDelete: relationBackendSetNull, Position: 5,
				}
				before.Relations = append(before.Relations, reviewer)
				after := before.relationBackendClone()
				after.Relations = []relationBackendRelation{reviewer, before.Relations[0]}
				return relationBackendStepIntent{
					App: "blog", Name: "bad_remove_order",
					Changes: []relationBackendChange{{
						Kind: relationBackendRemoveField, Before: before, After: after,
						Relation: before.Relations[1],
					}},
				}
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &relationBackendTraceBackend{
				capabilities: relationBackendCapabilities{
					Profile: 1, CreateModel: true, NullableAddField: true,
					EmptyRequiredAddField: true, BoundedRemake: true,
				},
			}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{
					App: test.intent.App, Name: test.intent.Name,
					Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
				},
				test.intent,
			)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("relationBackendOpenStep() error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("backend events = %#v, want malformed intent rejection before capability/I/O", backend.events)
			}
		})
	}
}

func TestRelationBackendCandidateCreateDeleteUnionPayloadRejectsBeforeIO(t *testing.T) {
	relation := relationBackendRelation{
		Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
		OnDelete: relationBackendProtect, Position: 3,
	}
	tests := []struct {
		name   string
		change relationBackendChange
	}{
		{
			name: "create_model_relation_payload",
			change: relationBackendChange{
				Kind: relationBackendCreateModel, After: relationBackendAuthorModel(), Relation: relation,
			},
		},
		{
			name: "delete_model_relation_payload",
			change: relationBackendChange{
				Kind: relationBackendDeleteModel, Before: relationBackendAuthorModel(), Relation: relation,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := relationBackendStepIntent{
				App: "blog", Name: test.name, Changes: []relationBackendChange{test.change},
			}
			backend := &relationBackendTraceBackend{capabilities: relationBackendCapabilities{
				Profile: 1, CreateModel: true, NullableAddField: true,
				EmptyRequiredAddField: true, BoundedRemake: true,
			}}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
				intent,
			)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("non-closed union error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("non-closed union backend events = %#v, want zero capability/session I/O", backend.events)
			}
		})
	}
}

func TestRelationBackendCandidateKnownLocalTargetMustBeExactAutoIntegerPrimaryKey(t *testing.T) {
	tests := []struct {
		name         string
		targetColumn string
	}{
		{name: "missing_target_column", targetColumn: "legacy_id"},
		{name: "non_primary_target_column", targetColumn: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			author := relationBackendAuthorModel()
			article := relationBackendArticleModel(false)
			article.Relations[0].TargetColumn = test.targetColumn
			intent := relationBackendStepIntent{
				App: "blog", Name: test.name,
				Changes: []relationBackendChange{
					{Kind: relationBackendCreateModel, After: author},
					{Kind: relationBackendCreateModel, After: article},
				},
			}
			backend := &relationBackendTraceBackend{capabilities: relationBackendCapabilities{
				Profile: 1, CreateModel: true, NullableAddField: true,
				EmptyRequiredAddField: true, BoundedRemake: true,
			}}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
				intent,
			)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("known local target error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("known local target backend events = %#v, want zero capability/session I/O", backend.events)
			}
		})
	}
}

func TestRelationBackendCandidateMiddlePhysicalRelationRemovalCompactsLaterPositions(t *testing.T) {
	before := relationBackendModel{
		Table: "article",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 3},
		},
		Relations: []relationBackendRelation{
			{
				Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			},
			{
				Name: "editor", Column: "editor_id", TargetTable: "author", TargetColumn: "id",
				Nullable: true, OnDelete: relationBackendSetNull, Position: 4,
			},
		},
	}
	after := before.relationBackendClone()
	after.Columns[1].Position = 2
	after.Relations = append([]relationBackendRelation(nil), before.Relations[1:]...)
	after.Relations[0].Position = 3
	intent := relationBackendStepIntent{
		App: "blog", Name: "middle_remove",
		Changes: []relationBackendChange{{
			Kind: relationBackendRemoveField, Before: before, After: after, Relation: before.Relations[0],
		}},
	}
	backend := &relationBackendTraceBackend{capabilities: relationBackendCapabilities{
		Profile: 1, CreateModel: true, NullableAddField: true,
		EmptyRequiredAddField: true, BoundedRemake: true,
	}}
	opened, err := relationBackendOpenStep(
		context.Background(), backend,
		relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
		intent,
	)
	if err != nil {
		t.Fatalf("middle-position relation removal: %v", err)
	}
	defer func() { _ = opened.Session.Close(context.Background()) }()
	if got, want := backend.events, []string{"capabilities", "open", "begin"}; !relationBackendStringSlicesEqual(got, want) {
		t.Fatalf("middle-position relation removal events = %#v, want %#v", got, want)
	}
	if got := backend.session.pinned.Changes[0].After; !relationBackendModelsEqual(got, after) {
		t.Fatalf("pinned compacted after-model = %#v, want %#v", got, after)
	}

	t.Run("unrelated_mutation_is_rejected", func(t *testing.T) {
		malformed := intent.relationBackendClone()
		malformed.Changes[0].After.Columns[1].MaxLength++
		if err := relationBackendValidateIntent(malformed); !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("middle removal with unrelated mutation error = %v, want invalid intent", err)
		}
	})

	t.Run("later_positions_must_all_shift", func(t *testing.T) {
		malformed := intent.relationBackendClone()
		malformed.Changes[0].After.Relations[0].Position = 4
		if err := relationBackendValidateIntent(malformed); !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("middle removal without full position compaction error = %v, want invalid intent", err)
		}
	})
}

func TestRelationBackendCandidateOrderedVirtualStateRequiresExactChainContinuity(t *testing.T) {
	base := relationBackendArticleModel(false)
	withEditor := relationBackendArticleModel(true)
	reviewer := relationBackendRelation{
		Name: "reviewer", Column: "reviewer_id", TargetTable: "author", TargetColumn: "id",
		Nullable: true, OnDelete: relationBackendSetNull, Position: 5,
	}
	withReviewer := withEditor.relationBackendClone()
	withReviewer.Relations = append(withReviewer.Relations, reviewer)
	valid := relationBackendStepIntent{
		App: "blog", Name: "ordered",
		Changes: []relationBackendChange{
			{Kind: relationBackendAddField, Before: base, After: withEditor, Relation: withEditor.Relations[1]},
			{Kind: relationBackendAddField, Before: withEditor, After: withReviewer, Relation: reviewer},
		},
	}
	if err := relationBackendValidateIntent(valid); err != nil {
		t.Fatalf("valid ordered relation chain: %v", err)
	}

	tests := []struct {
		name   string
		intent relationBackendStepIntent
	}{
		{
			name: "stale_second_before",
			intent: func() relationBackendStepIntent {
				intent := valid.relationBackendClone()
				intent.Changes[1].Before = base.relationBackendClone()
				return intent
			}(),
		},
		{
			name: "change_after_delete",
			intent: relationBackendStepIntent{
				App: "blog", Name: "deleted",
				Changes: []relationBackendChange{
					{Kind: relationBackendDeleteModel, Before: base},
					{Kind: relationBackendAddField, Before: base, After: withEditor, Relation: withEditor.Relations[1]},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &relationBackendTraceBackend{capabilities: relationBackendCapabilities{
				Profile: 1, CreateModel: true, NullableAddField: true,
				EmptyRequiredAddField: true, BoundedRemake: true,
			}}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: test.intent.App, Name: test.intent.Name, Direction: relationBackendApply, ToRevision: 1},
				test.intent,
			)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("ordered virtual state error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("ordered virtual state backend events = %#v, want zero I/O", backend.events)
			}
		})
	}

	t.Run("remove_only_inbound_then_delete_target_is_safe", func(t *testing.T) {
		target := relationBackendAuthorModel()
		before := relationBackendModel{
			Table: "audit_log",
			Columns: []relationBackendColumn{
				{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			},
			Relations: []relationBackendRelation{{
				Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			}},
		}
		after := before.relationBackendClone()
		after.Relations = nil
		intent := relationBackendStepIntent{
			App: "blog", Name: "remove_then_delete",
			Changes: []relationBackendChange{
				{Kind: relationBackendRemoveField, Before: before, After: after, Relation: before.Relations[0]},
				{Kind: relationBackendDeleteModel, Before: target},
			},
		}
		if err := relationBackendValidateIntent(intent); err != nil {
			t.Fatalf("RemoveField followed by safe target DeleteModel: %v", err)
		}
	})

	t.Run("created_inbound_then_delete_target_is_rejected", func(t *testing.T) {
		intent := relationBackendArticleCreateIntent()
		intent.Name = "create_source_then_delete_target"
		intent.Changes = append(intent.Changes, relationBackendChange{
			Kind: relationBackendDeleteModel, Before: relationBackendAuthorModel(),
		})
		backend := &relationBackendTraceBackend{capabilities: relationBackendCapabilities{
			Profile: 1, CreateModel: true, NullableAddField: true,
			EmptyRequiredAddField: true, BoundedRemake: true,
		}}
		_, err := relationBackendOpenStep(
			context.Background(), backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("created inbound followed by target deletion error = %v, want invalid intent", err)
		}
		if len(backend.events) != 0 {
			t.Fatalf("unsafe ordered deletion backend events = %#v, want zero I/O", backend.events)
		}
	})

	t.Run("case_folded_deleted_target_tombstone_is_rejected", func(t *testing.T) {
		source := relationBackendArticleModel(false)
		source.Relations[0].TargetTable = "AuThOr"
		intent := relationBackendStepIntent{
			App: "blog", Name: "folded_tombstone",
			Changes: []relationBackendChange{
				{Kind: relationBackendDeleteModel, Before: relationBackendAuthorModel()},
				{Kind: relationBackendCreateModel, After: source},
			},
		}
		if err := relationBackendValidateIntent(intent); !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("case-folded deleted target error = %v, want invalid intent", err)
		}
	})

	t.Run("case_folded_table_collision_is_rejected", func(t *testing.T) {
		intent := relationBackendArticleCreateIntent()
		colliding := relationBackendAuthorModel()
		colliding.Table = "AUTHOR"
		intent.Changes = append(intent.Changes, relationBackendChange{Kind: relationBackendCreateModel, After: colliding})
		if err := relationBackendValidateIntent(intent); !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("case-folded table collision error = %v, want invalid intent", err)
		}
	})
}

func TestRelationBackendCandidateEndpointDeltasAndNilEmptySnapshotsAreSemantic(t *testing.T) {
	base := relationBackendArticleModel(false)
	editor := relationBackendRelation{
		Name: "editor", Column: "editor_id", TargetTable: "author", TargetColumn: "id",
		Nullable: true, OnDelete: relationBackendSetNull, Position: 4,
	}
	addFirstAfter := base.relationBackendClone()
	addFirstAfter.Relations = append([]relationBackendRelation{editor}, addFirstAfter.Relations...)
	if err := relationBackendValidateChange(relationBackendChange{
		Kind: relationBackendAddField, Before: base, After: addFirstAfter, Relation: editor,
	}); err != nil {
		t.Fatalf("first-position exact AddField: %v", err)
	}
	if err := relationBackendValidateChange(relationBackendChange{
		Kind: relationBackendRemoveField, Before: addFirstAfter, After: base, Relation: editor,
	}); err != nil {
		t.Fatalf("first-position exact RemoveField: %v", err)
	}
	removeLastBefore := base.relationBackendClone()
	removeLastBefore.Relations = append(removeLastBefore.Relations, editor)
	if err := relationBackendValidateChange(relationBackendChange{
		Kind: relationBackendRemoveField, Before: removeLastBefore, After: base, Relation: editor,
	}); err != nil {
		t.Fatalf("last-position exact RemoveField: %v", err)
	}

	nilSlices := relationBackendModel{Table: "empty"}
	emptySlices := relationBackendModel{
		Table: "empty", Columns: []relationBackendColumn{}, Relations: []relationBackendRelation{},
	}
	if !relationBackendModelsEqual(nilSlices, emptySlices) {
		t.Fatal("nil and non-nil empty snapshots are not semantically equal")
	}
	clone := emptySlices.relationBackendClone()
	if clone.Columns == nil || clone.Relations == nil {
		t.Fatalf("clone lost non-nil empty slice shape: %#v", clone)
	}
}

func TestRelationBackendCandidateRequiredAddCapabilityIsEmptyTableOnly(t *testing.T) {
	intent := relationBackendRequiredAddIntent()
	backend := &relationBackendTraceBackend{
		capabilities: relationBackendCapabilities{
			Profile: 1, CreateModel: true, NullableAddField: true, BoundedRemake: true,
		},
	}
	_, err := relationBackendOpenStep(
		context.Background(), backend,
		relationBackendTransition{
			App: intent.App, Name: intent.Name,
			Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
		},
		intent,
	)
	if !errors.Is(err, relationBackendErrUnsupported) {
		t.Fatalf("relationBackendOpenStep() error = %v, want unsupported empty-required capability", err)
	}
	if got, want := backend.events, []string{"capabilities"}; !relationBackendStringSlicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v (no session/open I/O)", got, want)
	}
}

func TestRelationBackendCandidateMalformedTransitionRejectsBeforeCapabilityAndIO(t *testing.T) {
	intent := relationBackendNullableAddIntent()
	tests := []struct {
		name       string
		transition relationBackendTransition
	}{
		{name: "identity", transition: relationBackendTransition{App: "other", Name: intent.Name, Direction: relationBackendApply, FromRevision: 0, ToRevision: 1}},
		{name: "direction_zero", transition: relationBackendTransition{App: intent.App, Name: intent.Name, FromRevision: 0, ToRevision: 1}},
		{name: "direction_unknown", transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: 9, FromRevision: 0, ToRevision: 1}},
		{name: "negative_revision", transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, FromRevision: -1, ToRevision: 0}},
		{name: "non_successor", transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, FromRevision: 1, ToRevision: 3}},
		{name: "successor_overflow", transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, FromRevision: math.MaxInt64, ToRevision: math.MinInt64}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &relationBackendTraceBackend{
				capabilities: relationBackendCapabilities{
					Profile: 1, CreateModel: true, NullableAddField: true,
					EmptyRequiredAddField: true, BoundedRemake: true,
				},
			}
			_, err := relationBackendOpenStep(context.Background(), backend, test.transition, intent)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("relationBackendOpenStep() error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("backend events = %#v, want malformed transition zero capability/session I/O", backend.events)
			}
		})
	}
}

func TestRelationBackendCandidateCanceledAndOversizedInputRejectBeforeCloneCapabilityAndIO(t *testing.T) {
	capabilities := relationBackendCapabilities{
		Profile: 1, CreateModel: true, NullableAddField: true,
		EmptyRequiredAddField: true, BoundedRemake: true,
	}

	t.Run("already canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := &relationBackendTraceBackend{capabilities: capabilities}
		intent := relationBackendArticleCreateIntent()
		_, err := relationBackendOpenStep(
			ctx,
			backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if !errors.Is(err, context.Canceled) || len(backend.events) != 0 {
			t.Fatalf("canceled open = events:%#v error:%v, want context cause and zero capability/I/O", backend.events, err)
		}
	})

	t.Run("operation count", func(t *testing.T) {
		backend := &relationBackendTraceBackend{capabilities: capabilities}
		intent := relationBackendStepIntent{
			App: "blog", Name: "oversized",
			Changes: make([]relationBackendChange, profileMaxOperations+1),
		}
		_, err := relationBackendOpenStep(
			context.Background(),
			backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if !errors.Is(err, relationBackendErrIntent) || len(backend.events) != 0 {
			t.Fatalf("oversized operations = events:%#v error:%v, want bounded pre-clone rejection", backend.events, err)
		}
	})

	t.Run("model field count", func(t *testing.T) {
		backend := &relationBackendTraceBackend{capabilities: capabilities}
		intent := relationBackendStepIntent{
			App: "blog", Name: "oversized_model",
			Changes: []relationBackendChange{{
				Kind: relationBackendCreateModel,
				After: relationBackendModel{
					Table:   "oversized_model",
					Columns: make([]relationBackendColumn, profileMaxFields+1),
				},
			}},
		}
		_, err := relationBackendOpenStep(
			context.Background(),
			backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if !errors.Is(err, relationBackendErrIntent) || len(backend.events) != 0 {
			t.Fatalf("oversized model = events:%#v error:%v, want bounded pre-clone rejection", backend.events, err)
		}
	})

	t.Run("aggregate graph validation work", func(t *testing.T) {
		backend := &relationBackendTraceBackend{capabilities: capabilities}
		intent := relationBackendStepIntent{
			App: "blog", Name: "oversized_graph_work",
			Changes: make([]relationBackendChange, profileMaxOperations),
		}
		for index := range intent.Changes {
			columns := make([]relationBackendColumn, profileMaxFields)
			columns[0] = relationBackendColumn{
				Name: "id", Type: "INTEGER", NotNull: true,
				PrimaryKey: true, AutoIncrement: true, Position: 1,
			}
			for fieldIndex := 1; fieldIndex < len(columns); fieldIndex++ {
				columns[fieldIndex] = relationBackendColumn{
					Name: fmt.Sprintf("field_%03d", fieldIndex), Type: "VARCHAR",
					MaxLength: 200, NotNull: true, Position: fieldIndex + 1,
				}
			}
			intent.Changes[index] = relationBackendChange{
				Kind: relationBackendCreateModel,
				After: relationBackendModel{
					Table:   fmt.Sprintf("model_%03d", index),
					Columns: columns,
				},
			}
		}
		_, err := relationBackendOpenStep(
			context.Background(), backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if !errors.Is(err, relationBackendErrIntent) ||
			!strings.Contains(fmt.Sprint(err), "aggregate graph validation resource limit") ||
			len(backend.events) != 0 {
			t.Fatalf("oversized aggregate graph work = events:%#v error:%v, want bounded pre-clone rejection", backend.events, err)
		}
	})
}

func TestRelationBackendCandidateNilOptionalPortsFailClosedWithoutPanic(t *testing.T) {
	intent := relationBackendArticleCreateIntent()
	transition := relationBackendTransition{
		App: intent.App, Name: intent.Name,
		Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
	}
	capabilities := relationBackendCapabilities{Profile: 1, CreateModel: true}

	t.Run("typed nil backend", func(t *testing.T) {
		var backend *relationBackendTraceBackend
		_, err := relationBackendOpenStep(context.Background(), backend, transition, intent)
		if !errors.Is(err, relationBackendErrUnsupported) {
			t.Fatalf("typed-nil backend error = %v, want unsupported", err)
		}
	})

	t.Run("typed nil session", func(t *testing.T) {
		backend := &relationBackendTraceBackend{capabilities: capabilities, typedNilSession: true}
		_, err := relationBackendOpenStep(context.Background(), backend, transition, intent)
		if err == nil || backend.closeCalls != 0 {
			t.Fatalf("typed-nil session = error:%v close:%d, want fail-closed without method call", err, backend.closeCalls)
		}
	})

	t.Run("typed nil transaction", func(t *testing.T) {
		backend := &relationBackendTraceBackend{capabilities: capabilities, typedNilTransaction: true}
		_, err := relationBackendOpenStep(context.Background(), backend, transition, intent)
		if err == nil || backend.closeCalls != 1 || backend.closeContextErr != nil || !backend.closeHadDeadline {
			t.Fatalf(
				"typed-nil transaction = error:%v close:%d context:%v deadline:%t",
				err, backend.closeCalls, backend.closeContextErr, backend.closeHadDeadline,
			)
		}
	})
}

func TestRelationBackendCandidateUnknownDeletePolicyAndColumnTypeRejectBeforeIO(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*relationBackendStepIntent)
	}{
		{name: "unknown_delete_policy", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[1].After.Relations[0].OnDelete = 0
		}},
		{name: "injected_column_type", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Columns[1].Type = `TEXT); DROP TABLE "author"; --`
		}},
		{name: "empty_relation_name", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[1].After.Relations[0].Name = ""
		}},
		{name: "quoted_table_identifier", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Table = `author"archive`
		}},
		{name: "comment_like_target_identifier", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[1].After.Relations[0].TargetTable = "author--shadow"
		}},
		{name: "text_primary_key", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Columns[0].Type = "TEXT"
		}},
		{name: "non_auto_primary_key", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Columns[0].AutoIncrement = false
		}},
		{name: "auto_non_primary_key", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Columns[1].AutoIncrement = true
		}},
		{name: "case_folded_column_collision", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[0].After.Columns[1].Name = "ID"
		}},
		{name: "case_folded_relation_name_collision", mutate: func(intent *relationBackendStepIntent) {
			intent.Changes[1].After.Relations = append(intent.Changes[1].After.Relations, relationBackendRelation{
				Name: "AUTHOR", Column: "reviewer_id", TargetTable: "author", TargetColumn: "id",
				Nullable: true, OnDelete: relationBackendSetNull, Position: 4,
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := relationBackendArticleCreateIntent()
			test.mutate(&intent)
			backend := &relationBackendTraceBackend{
				capabilities: relationBackendCapabilities{Profile: 1, CreateModel: true},
			}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{
					App: intent.App, Name: intent.Name,
					Direction: relationBackendApply, FromRevision: 0, ToRevision: 1,
				}, intent,
			)
			if !errors.Is(err, relationBackendErrIntent) {
				t.Fatalf("relationBackendOpenStep() error = %v, want invalid intent", err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("backend events = %#v, want zero capability/session I/O", backend.events)
			}
		})
	}
}

type relationBackendOpenBoundaryConfig struct {
	openReturnsSession      bool
	openErr                 error
	cancelDuringOpen        bool
	beginReturnsTransaction bool
	beginErr                error
	cancelDuringBegin       bool
	rollbackErr             error
	closeErr                error
}

type relationBackendOpenBoundaryBackend struct {
	config          relationBackendOpenBoundaryConfig
	cancel          context.CancelFunc
	events          []string
	capabilityCalls int
	openCalls       int
	unexpectedCalls int
	session         *relationBackendOpenBoundarySession
}

func (backend *relationBackendOpenBoundaryBackend) RelationMigrationCapabilities() relationBackendCapabilities {
	backend.capabilityCalls++
	backend.events = append(backend.events, "capabilities")
	return relationBackendCapabilities{Profile: 1, CreateModel: true}
}

func (backend *relationBackendOpenBoundaryBackend) OpenRelationMigrationSession(context.Context) (relationBackendOptionalSession, error) {
	backend.openCalls++
	backend.events = append(backend.events, "open")
	if backend.config.openReturnsSession {
		backend.session = &relationBackendOpenBoundarySession{backend: backend}
	}
	if backend.config.cancelDuringOpen {
		backend.cancel()
	}
	return backend.session, backend.config.openErr
}

type relationBackendOpenBoundarySession struct {
	backend          *relationBackendOpenBoundaryBackend
	transaction      *relationBackendOpenBoundaryTransaction
	beginCalls       int
	closeCalls       int
	closeContextErr  error
	closeHadDeadline bool
}

func (session *relationBackendOpenBoundarySession) BeginRelationFencedMigration(
	context.Context,
	relationBackendTransition,
	relationBackendStepIntent,
) (relationBackendTransaction, error) {
	session.beginCalls++
	session.backend.events = append(session.backend.events, "begin")
	if session.backend.config.beginReturnsTransaction {
		session.transaction = &relationBackendOpenBoundaryTransaction{session: session}
	}
	if session.backend.config.cancelDuringBegin {
		session.backend.cancel()
	}
	return session.transaction, session.backend.config.beginErr
}

func (session *relationBackendOpenBoundarySession) Close(ctx context.Context) error {
	session.closeCalls++
	session.backend.events = append(session.backend.events, "close")
	session.closeContextErr = ctx.Err()
	_, session.closeHadDeadline = ctx.Deadline()
	return session.backend.config.closeErr
}

type relationBackendOpenBoundaryTransaction struct {
	session             *relationBackendOpenBoundarySession
	rollbackCalls       int
	rollbackContextErr  error
	rollbackHadDeadline bool
}

func (transaction *relationBackendOpenBoundaryTransaction) ApplyRelationChange(context.Context, relationBackendChange) error {
	transaction.session.backend.unexpectedCalls++
	return errors.New("unexpected ApplyRelationChange call")
}

func (transaction *relationBackendOpenBoundaryTransaction) RecordRelationTransition(context.Context) error {
	transaction.session.backend.unexpectedCalls++
	return errors.New("unexpected RecordRelationTransition call")
}

func (transaction *relationBackendOpenBoundaryTransaction) CommitRelationFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.session.backend.unexpectedCalls++
	return migrationbackend.CommitOutcome{}, errors.New("unexpected CommitRelationFenced call")
}

func (transaction *relationBackendOpenBoundaryTransaction) RollbackRelation(ctx context.Context) error {
	transaction.rollbackCalls++
	transaction.session.backend.events = append(transaction.session.backend.events, "rollback")
	transaction.rollbackContextErr = ctx.Err()
	_, transaction.rollbackHadDeadline = ctx.Deadline()
	return transaction.session.backend.config.rollbackErr
}

type relationBackendTraceBackend struct {
	capabilities        relationBackendCapabilities
	events              []string
	session             *relationBackendTraceSession
	beginErr            error
	beginCancel         context.CancelFunc
	closeErr            error
	closeCalls          int
	closeContextErr     error
	closeHadDeadline    bool
	typedNilSession     bool
	typedNilTransaction bool
}

func (backend *relationBackendTraceBackend) RelationMigrationCapabilities() relationBackendCapabilities {
	backend.events = append(backend.events, "capabilities")
	return backend.capabilities
}

func (backend *relationBackendTraceBackend) OpenRelationMigrationSession(context.Context) (relationBackendOptionalSession, error) {
	backend.events = append(backend.events, "open")
	if backend.typedNilSession {
		var session *relationBackendTraceSession
		return session, nil
	}
	backend.session = &relationBackendTraceSession{backend: backend}
	return backend.session, nil
}

type relationBackendTraceSession struct {
	backend *relationBackendTraceBackend
	pinned  relationBackendStepIntent
}

func (session *relationBackendTraceSession) BeginRelationFencedMigration(
	_ context.Context,
	_ relationBackendTransition,
	intent relationBackendStepIntent,
) (relationBackendTransaction, error) {
	session.backend.events = append(session.backend.events, "begin")
	session.pinned = intent.relationBackendClone()
	if session.backend.beginCancel != nil {
		session.backend.beginCancel()
	}
	if session.backend.beginErr != nil {
		return nil, session.backend.beginErr
	}
	if session.backend.typedNilTransaction {
		var transaction *relationBackendTraceTransaction
		return transaction, nil
	}
	return &relationBackendTraceTransaction{}, nil
}

func (session *relationBackendTraceSession) Close(ctx context.Context) error {
	session.backend.closeCalls++
	session.backend.closeContextErr = ctx.Err()
	_, session.backend.closeHadDeadline = ctx.Deadline()
	return session.backend.closeErr
}

type relationBackendTraceTransaction struct{}

func (*relationBackendTraceTransaction) ApplyRelationChange(context.Context, relationBackendChange) error {
	return nil
}
func (*relationBackendTraceTransaction) RecordRelationTransition(context.Context) error { return nil }
func (*relationBackendTraceTransaction) CommitRelationFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil
}
func (*relationBackendTraceTransaction) RollbackRelation(context.Context) error { return nil }

func relationBackendArticleCreateIntent() relationBackendStepIntent {
	author := relationBackendAuthorModel()
	article := relationBackendArticleModel(false)
	return relationBackendStepIntent{
		App: "blog", Name: "0001",
		Changes: []relationBackendChange{
			{Kind: relationBackendCreateModel, After: author},
			{Kind: relationBackendCreateModel, After: article},
		},
	}
}

func relationBackendNullableAddIntent() relationBackendStepIntent {
	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	return relationBackendStepIntent{
		App: "blog", Name: "0002",
		Changes: []relationBackendChange{{
			Kind: relationBackendAddField, Before: before, After: after,
			Relation: after.Relations[1],
		}},
	}
}

func relationBackendNullableRemoveIntent() relationBackendStepIntent {
	add := relationBackendNullableAddIntent()
	change := add.Changes[0]
	return relationBackendStepIntent{
		App: add.App, Name: add.Name,
		Changes: []relationBackendChange{{
			Kind: relationBackendRemoveField, Before: change.After, After: change.Before,
			Relation: change.Relation,
		}},
	}
}

func relationBackendRequiredAddIntent() relationBackendStepIntent {
	before := relationBackendArticleModel(false)
	after := before.relationBackendClone()
	reviewer := relationBackendRelation{
		Name: "reviewer", Column: "reviewer_id", TargetTable: "author", TargetColumn: "id",
		OnDelete: relationBackendProtect, Position: 4,
	}
	after.Relations = append(after.Relations, reviewer)
	return relationBackendStepIntent{
		App: "blog", Name: "0002_required",
		Changes: []relationBackendChange{{
			Kind: relationBackendAddField, Before: before, After: after, Relation: reviewer,
		}},
	}
}

func relationBackendSelfIntent() relationBackendStepIntent {
	model := relationBackendModel{
		Table:   "node",
		Columns: []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
		Relations: []relationBackendRelation{{
			Name: "parent", Column: "parent_id", TargetTable: "node", TargetColumn: "id",
			Nullable: true, OnDelete: relationBackendSetNull, Position: 2,
		}},
	}
	return relationBackendStepIntent{App: "blog", Name: "self", Changes: []relationBackendChange{{Kind: relationBackendCreateModel, After: model}}}
}

func relationBackendCycleIntent() relationBackendStepIntent {
	left := relationBackendModel{
		Table:     "left_model",
		Columns:   []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
		Relations: []relationBackendRelation{{Name: "right", Column: "right_id", TargetTable: "right_model", TargetColumn: "id", OnDelete: relationBackendProtect, Position: 2}},
	}
	right := relationBackendModel{
		Table:     "right_model",
		Columns:   []relationBackendColumn{{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1}},
		Relations: []relationBackendRelation{{Name: "left", Column: "left_id", TargetTable: "left_model", TargetColumn: "id", OnDelete: relationBackendProtect, Position: 2}},
	}
	return relationBackendStepIntent{
		App: "blog", Name: "cycle",
		Changes: []relationBackendChange{{Kind: relationBackendCreateModel, After: left}, {Kind: relationBackendCreateModel, After: right}},
	}
}

func relationBackendAuthorModel() relationBackendModel {
	return relationBackendModel{
		Table: "author",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "name", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 2},
		},
	}
}

func relationBackendArticleModel(withEditor bool) relationBackendModel {
	model := relationBackendModel{
		Table: "article",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 2},
		},
		Relations: []relationBackendRelation{{
			Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
			OnDelete: relationBackendProtect, Position: 3,
		}},
	}
	if withEditor {
		model.Relations = append(model.Relations, relationBackendRelation{
			Name: "editor", Column: "editor_id", TargetTable: "author", TargetColumn: "id",
			Nullable: true, OnDelete: relationBackendSetNull, Position: 4,
		})
	}
	return model
}

func relationBackendStringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
