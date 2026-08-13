package migrationrelation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

func TestFaultCandidateSchemaCopyRecorderAndRevisionFailuresRollbackAtomically(t *testing.T) {
	stages := []faultStage{
		faultStageRevisionClaim,
		faultStageCreate,
		faultStageCopy,
		faultStageDrop,
		faultStageRename,
		faultStageForeignKey,
		faultStageRecorder,
		faultStageRevisionVerify,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			database, backend, step := faultRelationRemoveFixture(t)
			defer database.Close()
			before := faultDurableState(t, database)
			cause := fmt.Errorf("%s sentinel", stage)
			backend.faults = faultNewPlan(stage, cause)

			result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{step})
			if !errors.Is(err, cause) {
				t.Fatalf("faultExecutePlan() error = %v, want cause identity %v", err, cause)
			}
			if result.ConfirmedSteps != 0 || result.Attempts[0] != 1 {
				t.Fatalf("result = %+v, want no confirmed step and one attempt", result)
			}
			after := faultDurableState(t, database)
			if after != before {
				t.Fatalf("fault %s changed durable state\nbefore:\n%s\nafter:\n%s", stage, before, after)
			}
			if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 {
				t.Fatalf(
					"lifecycle counts begin=%d rollback=%d commit=%d, want begin=1 rollback=1 commit=0",
					backend.beginCalls, backend.rollbackCalls, backend.commitCalls,
				)
			}
			if sqliteRelationColumnExists(t, database, "article", "editor_id") == false {
				t.Fatal("rollback lost editor relation column")
			}
			if sqliteRelationRecorderCount(t, database, "blog", "0002") != 1 || sqliteRelationRevision(t, database) != 2 {
				t.Fatal("rollback changed recorder or revision")
			}
		})
	}
}

func TestFaultCandidatePreservesPrimaryAndRollbackCauseIdentity(t *testing.T) {
	database, backend, step := faultRelationRemoveFixture(t)
	defer database.Close()
	primary := errors.New("copy primary sentinel")
	rollback := errors.New("rollback secondary sentinel")
	backend.faults = faultNewPlan(faultStageCopy, primary)
	backend.faults.rollbackCause = rollback

	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{step})
	if !errors.Is(err, primary) || !errors.Is(err, rollback) {
		t.Fatalf("joined error = %v, want primary and rollback cause identity", err)
	}
	if result.ConfirmedSteps != 0 || result.Attempts[0] != 1 {
		t.Fatalf("result = %+v, want stopped first attempt", result)
	}
	if sqliteRelationRevision(t, database) != 2 || !sqliteRelationColumnExists(t, database, "article", "editor_id") {
		t.Fatal("rollback-cause reporting changed durable database state")
	}
	if backend.discardCalls != 1 || database.Stats().InUse != 0 {
		t.Fatalf("failed rollback discard=%d in_use=%d, want 1/0", backend.discardCalls, database.Stats().InUse)
	}

	t.Run("canceled request uses bounded detached session cleanup exactly once", func(t *testing.T) {
		stages := []faultCleanupFailureStage{
			faultCleanupFailBegin,
			faultCleanupFailBeginPartial,
			faultCleanupFailApply,
			faultCleanupFailRecord,
			faultCleanupFailCommit,
		}
		for _, stage := range stages {
			t.Run(string(stage), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				primary := errors.New(string(stage) + " primary sentinel")
				cleanup := errors.New(string(stage) + " cleanup sentinel")
				testBackend := faultNewCleanupBackend(stage, cancel, primary, cleanup)
				intent := relationBackendArticleCreateIntent()
				result, err := faultExecutePlan(ctx, testBackend, []faultExecutorStep{{
					Transition: relationBackendTransition{
						App: intent.App, Name: intent.Name, Direction: relationBackendApply,
						FromRevision: 0, ToRevision: 1,
					},
					Intent: intent,
				}})
				if !errors.Is(err, primary) || !errors.Is(err, cleanup) {
					t.Fatalf("%s failure = %v, want primary and cleanup cause identity", stage, err)
				}
				if result.ConfirmedSteps != 0 || !reflect.DeepEqual(result.Attempts, []int{1}) {
					t.Fatalf("%s result = %+v, want one unconfirmed attempt", stage, result)
				}
				session := testBackend.session
				if session == nil || session.closeCalls != 1 || session.held {
					t.Fatalf("%s session cleanup = session:%#v, want exactly one releasing Close", stage, session)
				}
				if session.closeContextErr != nil || !session.closeDeadlineOK {
					t.Fatalf(
						"%s Close context = err:%v deadline:%t, want live bounded detached context",
						stage, session.closeContextErr, session.closeDeadlineOK,
					)
				}
				if session.closeDeadlineRemaining <= 0 || session.closeDeadlineRemaining > faultCleanupTimeout {
					t.Fatalf("%s Close deadline remaining = %s, want (0,%s]", stage, session.closeDeadlineRemaining, faultCleanupTimeout)
				}
				wantRollbacks := 1
				if stage == faultCleanupFailBegin || stage == faultCleanupFailCommit {
					wantRollbacks = 0
				}
				if testBackend.transaction.rollbackCalls != wantRollbacks {
					t.Fatalf("%s rollback calls = %d, want %d", stage, testBackend.transaction.rollbackCalls, wantRollbacks)
				}
				if wantRollbacks == 1 {
					transaction := testBackend.transaction
					if transaction.rollbackContextErr != nil || !transaction.rollbackDeadlineOK ||
						transaction.rollbackDeadlineRemaining <= 0 || transaction.rollbackDeadlineRemaining > faultCleanupTimeout {
						t.Fatalf(
							"%s rollback context = err:%v deadline:%t remaining:%s, want live bounded detached context",
							stage, transaction.rollbackContextErr, transaction.rollbackDeadlineOK,
							transaction.rollbackDeadlineRemaining,
						)
					}
				}
			})
		}
	})

	t.Run("cancellation between open begin apply record and commit stops at the next boundary", func(t *testing.T) {
		stages := []faultCleanupFailureStage{
			faultCleanupCancelAfterOpen,
			faultCleanupCancelAfterBegin,
			faultCleanupCancelAfterApply,
			faultCleanupCancelAfterRecord,
		}
		for _, stage := range stages {
			t.Run(string(stage), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				backend := faultNewCleanupBackend(stage, cancel, nil, nil)
				intent := relationBackendArticleCreateIntent()
				result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
					Transition: relationBackendTransition{
						App: intent.App, Name: intent.Name, Direction: relationBackendApply,
						FromRevision: 0, ToRevision: 1,
					},
					Intent: intent,
				}})
				if !errors.Is(err, context.Canceled) || result.ConfirmedSteps != 0 ||
					!reflect.DeepEqual(result.Attempts, []int{1}) {
					t.Fatalf("%s cancellation = result:%+v error:%v", stage, result, err)
				}
				if backend.session == nil || backend.session.closeCalls != 1 || backend.session.held ||
					backend.session.closeContextErr != nil || !backend.session.closeDeadlineOK {
					t.Fatalf("%s session cleanup = %#v", stage, backend.session)
				}
				wantBegin, wantApply, wantRecord, wantRollback := 0, 0, 0, 0
				switch stage {
				case faultCleanupCancelAfterOpen:
				case faultCleanupCancelAfterBegin:
					wantBegin, wantRollback = 1, 1
				case faultCleanupCancelAfterApply:
					wantBegin, wantApply, wantRollback = 1, 1, 1
				case faultCleanupCancelAfterRecord:
					wantBegin, wantApply, wantRecord, wantRollback = 1, len(intent.Changes), 1, 1
				}
				transaction := backend.transaction
				if backend.beginCalls != wantBegin || transaction.applyCalls != wantApply ||
					transaction.recordCalls != wantRecord || transaction.rollbackCalls != wantRollback ||
					transaction.commitCalls != 0 {
					t.Fatalf(
						"%s calls begin/apply/record/rollback/commit=%d/%d/%d/%d/%d want %d/%d/%d/%d/0",
						stage, backend.beginCalls, transaction.applyCalls, transaction.recordCalls,
						transaction.rollbackCalls, transaction.commitCalls,
						wantBegin, wantApply, wantRecord, wantRollback,
					)
				}
				if wantRollback == 1 && (transaction.rollbackContextErr != nil || !transaction.rollbackDeadlineOK) {
					t.Fatalf("%s rollback context = err:%v deadline:%t", stage, transaction.rollbackContextErr, transaction.rollbackDeadlineOK)
				}
			})
		}
	})
}

func TestFaultCandidateSnapshotsAndPrevalidatesCompletePlanBeforeIO(t *testing.T) {
	firstIntent := relationBackendArticleCreateIntent()
	secondIntent := relationBackendNullableAddIntent()
	steps := []faultExecutorStep{
		{
			Transition: relationBackendTransition{
				App: firstIntent.App, Name: firstIntent.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: firstIntent,
		},
		{
			Transition: relationBackendTransition{
				App: secondIntent.App, Name: secondIntent.Name, Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: secondIntent,
		},
	}

	t.Run("later invalid step leaves every attempt and lifecycle count at zero", func(t *testing.T) {
		invalid := []faultExecutorStep{faultCloneExecutorStep(steps[0]), faultCloneExecutorStep(steps[1])}
		invalid[1].Intent.Changes[0].After.Columns[0].Position = 0
		backend := faultNewCleanupBackend("", nil, nil, nil)
		result, err := faultExecutePlan(context.Background(), backend, invalid)
		if !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("later invalid plan error = %v, want intent failure", err)
		}
		if !reflect.DeepEqual(result.Attempts, []int{0, 0}) || result.ConfirmedSteps != 0 || result.Outcome != 0 {
			t.Fatalf("later invalid plan result = %+v, want zero publication/attempts", result)
		}
		if backend.openCalls != 0 || backend.session != nil || backend.transaction.rollbackCalls != 0 {
			t.Fatalf(
				"later invalid plan touched backend: open=%d session=%#v rollback=%d",
				backend.openCalls, backend.session, backend.transaction.rollbackCalls,
			)
		}
	})

	t.Run("later duplicate create leaves every attempt and lifecycle count at zero", func(t *testing.T) {
		duplicate := []faultExecutorStep{faultCloneExecutorStep(steps[0]), faultCloneExecutorStep(steps[0])}
		duplicate[1].Transition.Name = "0002_duplicate"
		duplicate[1].Transition.FromRevision = 1
		duplicate[1].Transition.ToRevision = 2
		duplicate[1].Intent.Name = "0002_duplicate"
		backend := faultNewCleanupBackend("", nil, nil, nil)
		result, err := faultExecutePlan(context.Background(), backend, duplicate)
		if !errors.Is(err, relationBackendErrIntent) || !strings.Contains(err.Error(), "already-present table") {
			t.Fatalf("duplicate create plan error = %v, want cross-step state failure", err)
		}
		if !reflect.DeepEqual(result.Attempts, []int{0, 0}) || result.ConfirmedSteps != 0 ||
			backend.openCalls != 0 || backend.session != nil {
			t.Fatalf("duplicate create touched lifecycle: result=%+v open=%d session=%#v", result, backend.openCalls, backend.session)
		}
	})

	t.Run("later-created target that closes a cycle leaves every attempt at zero", func(t *testing.T) {
		a := relationBackendModel{
			Table: "a",
			Columns: []relationBackendColumn{{
				Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
			}},
			Relations: []relationBackendRelation{{
				Name: "b", Column: "b_id", TargetTable: "b", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			}},
		}
		b := relationBackendModel{
			Table: "b",
			Columns: []relationBackendColumn{{
				Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
			}},
			Relations: []relationBackendRelation{{
				Name: "a", Column: "a_id", TargetTable: "a", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			}},
		}
		cycle := []faultExecutorStep{
			{
				Transition: relationBackendTransition{App: "blog", Name: "0001_a", Direction: relationBackendApply, ToRevision: 1},
				Intent: relationBackendStepIntent{App: "blog", Name: "0001_a", Changes: []relationBackendChange{{
					Kind: relationBackendCreateModel, After: a,
				}}},
			},
			{
				Transition: relationBackendTransition{App: "blog", Name: "0002_b", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
				Intent: relationBackendStepIntent{App: "blog", Name: "0002_b", Changes: []relationBackendChange{{
					Kind: relationBackendCreateModel, After: b,
				}}},
			},
		}
		backend := faultNewCleanupBackend("", nil, nil, nil)
		result, err := faultExecutePlan(context.Background(), backend, cycle)
		if !errors.Is(err, relationBackendErrCycle) || !errors.Is(err, relationBackendErrIntent) ||
			!reflect.DeepEqual(result.Attempts, []int{0, 0}) || backend.openCalls != 0 || backend.session != nil {
			t.Fatalf("cross-step cycle = result:%+v open:%d session:%#v error:%v", result, backend.openCalls, backend.session, err)
		}
	})

	t.Run("later-deleted target with a planned inbound relation leaves every attempt at zero", func(t *testing.T) {
		target := relationBackendAuthorModel()
		source := relationBackendArticleModel(false)
		plan := []faultExecutorStep{
			{
				Transition: relationBackendTransition{App: "blog", Name: "0001_author", Direction: relationBackendApply, ToRevision: 1},
				Intent: relationBackendStepIntent{App: "blog", Name: "0001_author", Changes: []relationBackendChange{{
					Kind: relationBackendCreateModel, After: target,
				}}},
			},
			{
				Transition: relationBackendTransition{App: "blog", Name: "0002_article", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
				Intent: relationBackendStepIntent{App: "blog", Name: "0002_article", Changes: []relationBackendChange{{
					Kind: relationBackendCreateModel, After: source,
				}}},
			},
			{
				Transition: relationBackendTransition{App: "blog", Name: "0003_delete_author", Direction: relationBackendApply, FromRevision: 2, ToRevision: 3},
				Intent: relationBackendStepIntent{App: "blog", Name: "0003_delete_author", Changes: []relationBackendChange{{
					Kind: relationBackendDeleteModel, Before: target,
				}}},
			},
		}
		backend := faultNewCleanupBackend("", nil, nil, nil)
		result, err := faultExecutePlan(context.Background(), backend, plan)
		if !errors.Is(err, relationBackendErrIntent) || !strings.Contains(err.Error(), "known-deleted table") ||
			!reflect.DeepEqual(result.Attempts, []int{0, 0, 0}) || backend.openCalls != 0 || backend.session != nil {
			t.Fatalf("cross-step deleted target = result:%+v open:%d session:%#v error:%v", result, backend.openCalls, backend.session, err)
		}
	})

	t.Run("caller mutation during first open cannot alter prepared tail", func(t *testing.T) {
		preparedInput := []faultExecutorStep{faultCloneExecutorStep(steps[0]), faultCloneExecutorStep(steps[1])}
		backend := faultNewCleanupBackend("", nil, nil, nil)
		backend.openHook = func() {
			backend.openHook = nil
			preparedInput[1].Transition.Name = "mutated_transition"
			preparedInput[1].Intent.Name = "mutated_intent"
			preparedInput[1].Intent.Changes[0].After.Columns[0].Name = "mutated_column"
		}
		result, err := faultExecutePlan(context.Background(), backend, preparedInput)
		if err != nil {
			t.Fatalf("snapshotted plan execution: %v", err)
		}
		if result.ConfirmedSteps != 2 || !reflect.DeepEqual(result.Attempts, []int{1, 1}) || backend.openCalls != 2 {
			t.Fatalf("snapshotted plan result=%+v open=%d, want two prepared steps", result, backend.openCalls)
		}
	})

	t.Run("aggregate plan nodes fail before cloning or backend IO", func(t *testing.T) {
		columns := make([]relationBackendColumn, profileMaxFields)
		intent := relationBackendStepIntent{
			App: "blog", Name: "bulk",
			Changes: []relationBackendChange{{
				Kind:   relationBackendAddField,
				Before: relationBackendModel{Table: "article", Columns: columns},
				After:  relationBackendModel{Table: "article", Columns: columns},
			}},
		}
		perStepNodes := 1 + 1 + 1 + (1 + len(columns)) + (1 + len(columns))
		stepCount := migrationdefinition.MaxJSONValues/perStepNodes + 1
		oversized := make([]faultExecutorStep, stepCount)
		for index := range oversized {
			oversized[index].Intent = intent
		}
		backend := faultNewCleanupBackend("", nil, nil, nil)
		result, err := faultExecutePlan(context.Background(), backend, oversized)
		if err == nil || !strings.Contains(err.Error(), "aggregate intent nodes exceed") ||
			backend.openCalls != 0 || backend.session != nil || result.ConfirmedSteps != 0 {
			t.Fatalf(
				"aggregate plan = result:%+v open:%d session:%#v error:%v",
				result, backend.openCalls, backend.session, err,
			)
		}
		for index, attempts := range result.Attempts {
			if attempts != 0 {
				t.Fatalf("aggregate plan attempt[%d] = %d, want 0", index, attempts)
			}
		}
	})
}

func TestFaultCandidateCommitOutcomeMatrixNoRetryAndTailStop(t *testing.T) {
	tests := []struct {
		name                string
		mode                faultCommitMode
		injectFault         bool
		wantOutcome         migrationbackend.CommitDurability
		wantConfirmed       int
		wantRevision        int64
		wantEditor          bool
		wantRecorder        int
		wantCode            string
		wantReason          string
		wantDurableOnReopen bool
	}{
		{
			name: "clean_success", mode: faultCommitNone,
			wantOutcome: migrationbackend.CommitCommitted, wantConfirmed: 1,
			wantRevision: 3, wantEditor: false, wantRecorder: 0,
			wantDurableOnReopen: true,
		},
		{
			name: "rolled_back", mode: faultCommitRolledBack, injectFault: true,
			wantOutcome:  migrationbackend.CommitRolledBack,
			wantRevision: 2, wantEditor: true, wantRecorder: 1,
			wantCode: "commit_failed", wantReason: "rolled_back",
		},
		{
			name: "committed_with_cleanup_error", mode: faultCommitCommitted, injectFault: true,
			wantOutcome: migrationbackend.CommitCommitted, wantConfirmed: 1,
			wantRevision: 3, wantEditor: false, wantRecorder: 0,
			wantCode: "commit_cleanup_failed", wantReason: "committed_cleanup_error",
			wantDurableOnReopen: true,
		},
		{
			name: "unknown_external_commit", mode: faultCommitUnknown, injectFault: true,
			wantOutcome:  migrationbackend.CommitUnknown,
			wantRevision: 3, wantEditor: false, wantRecorder: 0,
			wantCode: "commit_outcome_unknown", wantReason: "unknown",
			wantDurableOnReopen: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, step := faultRelationRemoveFixture(t)
			path := faultDatabasePath(t, database)
			var cause error
			if test.injectFault {
				cause = errors.New(test.name + " commit sentinel")
				backend.faults = faultNewCommitPlan(test.mode, cause)
			}
			tail := faultExecutorStep{
				Transition: relationBackendTransition{
					App: "blog", Name: "tail", Direction: relationBackendApply,
					FromRevision: 3, ToRevision: 4,
				},
				Intent: relationBackendStepIntent{App: "blog", Name: "tail", Changes: []relationBackendChange{{
					Kind: relationBackendCreateModel,
					After: relationBackendModel{
						Table: "tail_table",
						Columns: []relationBackendColumn{{
							Name: "id", Type: "INTEGER", NotNull: true,
							PrimaryKey: true, AutoIncrement: true, Position: 1,
						}},
					},
				}}},
			}

			steps := []faultExecutorStep{step}
			if test.injectFault {
				steps = append(steps, tail)
			}
			result, err := faultExecutePlan(context.Background(), backend, steps)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("faultExecutePlan() clean error = %v, want nil", err)
				}
			} else {
				var failure *faultCandidateError
				if !errors.As(err, &failure) || failure.Category != "migration_relation_lifecycle_candidate_error" ||
					failure.Code != test.wantCode || failure.Stage != "commit" || failure.Reason != test.wantReason {
					t.Fatalf("structured commit failure = %#v, want %s/%s", err, test.wantCode, test.wantReason)
				}
				if !errors.Is(err, cause) {
					t.Fatalf("faultExecutePlan() error = %v, want commit cause identity", err)
				}
			}
			if result.Outcome != test.wantOutcome || result.ConfirmedSteps != test.wantConfirmed {
				t.Fatalf("result = %+v, want outcome=%d confirmed=%d", result, test.wantOutcome, test.wantConfirmed)
			}
			if result.Attempts[0] != 1 || (test.injectFault && result.Attempts[1] != 0) {
				t.Fatalf("attempts = %#v, want first=1 and injected tail=0 (no retry/tail stop)", result.Attempts)
			}
			if backend.commitCalls != 1 {
				t.Fatalf("commit calls = %d, want exactly 1", backend.commitCalls)
			}
			if got := sqliteRelationRevision(t, database); got != test.wantRevision {
				t.Fatalf("revision = %d, want %d", got, test.wantRevision)
			}
			if got := sqliteRelationColumnExists(t, database, "article", "editor_id"); got != test.wantEditor {
				t.Fatalf("editor column exists = %t, want %t", got, test.wantEditor)
			}
			if got := sqliteRelationRecorderCount(t, database, "blog", "0002"); got != test.wantRecorder {
				t.Fatalf("0002 recorder rows = %d, want %d", got, test.wantRecorder)
			}
			if test.injectFault && sqliteRelationColumnExists(t, database, "tail_table", "id") {
				t.Fatal("tail step executed after non-successful commit return")
			}

			if err := database.Close(); err != nil {
				t.Fatalf("close candidate database: %v", err)
			}
			reopened := faultOpenExistingDatabase(t, path)
			defer reopened.Close()
			if got := sqliteRelationRevision(t, reopened); got != test.wantRevision {
				t.Fatalf("fresh reopen revision = %d, want %d", got, test.wantRevision)
			}
			if got := sqliteRelationColumnExists(t, reopened, "article", "editor_id"); got != test.wantEditor {
				t.Fatalf("fresh reopen editor exists = %t, want %t", got, test.wantEditor)
			}
			if test.wantDurableOnReopen && result.ConfirmedSteps == 0 && test.mode == faultCommitUnknown {
				// This is the deliberate distinction: the executor retained its last
				// confirmed state, while a fresh process can observe a durable successor.
				if sqliteRelationRevision(t, reopened) != 3 {
					t.Fatal("fresh restart did not expose durable unknown successor")
				}
			}
		})
	}
}

func TestFaultCandidateLocalQuiescentLinearFileReopenWithoutReplay(t *testing.T) {
	// This exercises only the isolated candidate's private tables and explicit
	// two-step linear catalog. It is not actual GoDj restart, DAG planning,
	// historical-state reconstruction, or epoch/fingerprint evidence.
	database, backend, path := sqliteRelationOpenCandidate(t)
	sqliteRelationApplyInitialArticle(t, backend)
	if _, err := database.ExecContext(
		context.Background(),
		`INSERT INTO "author" ("id", "name") VALUES (5, 'Ada');
		 INSERT INTO "article" ("id", "title", "author_id") VALUES (8, 'two', 5)`,
	); err != nil {
		t.Fatalf("seed file restart rows: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close file database: %v", err)
	}

	reopened := faultOpenExistingDatabase(t, path)
	if sqliteRelationRevision(t, reopened) != 1 || sqliteRelationRecorderCount(t, reopened, "blog", "0001") != 1 {
		t.Fatal("fresh restart lost revision or recorder")
	}
	foreignKeys := sqliteRelationForeignKeysFromDatabase(t, reopened, "article")
	if len(foreignKeys) != 1 || foreignKeys[0].SourceColumn != "author_id" || foreignKeys[0].OnDelete != "NO ACTION" {
		t.Fatalf("fresh restart relation state = %#v, want author NO ACTION", foreignKeys)
	}
	var rows int
	if err := reopened.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM "article" WHERE "id" = 8`).Scan(&rows); err != nil {
		t.Fatalf("read restart rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("restart rows = %d, want 1", rows)
	}

	catalog := faultForwardCatalog()
	reconstructed, report, err := faultReconstructCandidateLocalLinearPlan(
		context.Background(), faultSQLCandidateLocalRestartReader{database: reopened}, catalog,
	)
	if err != nil {
		t.Fatalf("faultReconstructCandidateLocalLinearPlan(): %v", err)
	}
	if report.Revision != 1 || report.RecorderReads != 1 || report.AlreadyApplied != 1 || report.Reconstructed != 1 ||
		!reflect.DeepEqual(report.RecordedSteps, []faultMigrationKey{{App: "blog", Name: "0001"}}) {
		t.Fatalf("restart reconstruction report = %+v", report)
	}
	if len(reconstructed) != 1 || reconstructed[0].Transition.Name != "0002" {
		t.Fatalf("restart reconstructed plan = %#v, want only 0002", reconstructed)
	}
	if reconstructed[0].Transition.FromRevision != 1 || reconstructed[0].Transition.ToRevision != 2 {
		t.Fatalf("pending fence = %d->%d, want durable revision successor 1->2", reconstructed[0].Transition.FromRevision, reconstructed[0].Transition.ToRevision)
	}

	// The catalog's declared fence is deliberately unrelated to catalog index
	// and durable revision. Mutation after reconstruction must not alias the
	// returned transition, intent, changes, model columns, or relation slices.
	catalog[1].Transition.Name = "mutated_transition"
	catalog[1].Intent.Name = "mutated_intent"
	catalog[1].Intent.Changes[0].Before.Columns[0].Name = "mutated_column"
	catalog[1].Intent.Changes[0].After.Relations[1].Name = "mutated_relation"
	catalog[1].Intent.Changes[0].Relation.Name = "mutated_change_relation"
	if reconstructed[0].Transition.Name != "0002" || reconstructed[0].Intent.Name != "0002" ||
		reconstructed[0].Intent.Changes[0].Before.Columns[0].Name != "id" ||
		reconstructed[0].Intent.Changes[0].After.Relations[1].Name != "editor" ||
		reconstructed[0].Intent.Changes[0].Relation.Name != "editor" {
		t.Fatalf("reconstructed plan retained caller aliases: %#v", reconstructed)
	}

	freshBackend := &sqliteRelationBackend{database: reopened}
	result, err := faultExecutePlan(context.Background(), freshBackend, reconstructed)
	if err != nil {
		t.Fatalf("execute reconstructed restart plan: %v", err)
	}
	if result.ConfirmedSteps != 1 || !reflect.DeepEqual(result.Attempts, []int{1}) || freshBackend.openCalls != 1 || freshBackend.beginCalls != 1 {
		t.Fatalf("restart result=%+v open=%d begin=%d, want one pending step and no 0001 replay", result, freshBackend.openCalls, freshBackend.beginCalls)
	}
	if sqliteRelationRevision(t, reopened) != 2 || sqliteRelationRecorderCount(t, reopened, "blog", "0001") != 1 ||
		sqliteRelationRecorderCount(t, reopened, "blog", "0002") != 1 || !sqliteRelationColumnExists(t, reopened, "article", "editor_id") {
		t.Fatal("reconstructed restart plan did not durably apply only the pending 0002 relation")
	}
	var nullEditors int
	if err := reopened.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM "article" WHERE "editor_id" IS NULL`).Scan(&nullEditors); err != nil {
		t.Fatalf("read reconstructed nullable relation rows: %v", err)
	}
	if nullEditors != 1 {
		t.Fatalf("reconstructed restart NULL editor rows = %d, want 1", nullEditors)
	}

	fullyApplied, report, err := faultReconstructCandidateLocalLinearPlan(
		context.Background(), faultSQLCandidateLocalRestartReader{database: reopened}, faultForwardCatalog(),
	)
	if err != nil || len(fullyApplied) != 0 || report.Revision != 2 || report.AlreadyApplied != 2 || report.Reconstructed != 0 {
		t.Fatalf("fully applied reconstruction = plan:%#v report:%+v error:%v", fullyApplied, report, err)
	}

	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	unapply := faultExecutorStep{
		Transition: relationBackendTransition{
			App: "blog", Name: "0002", Direction: relationBackendUnapply,
			FromRevision: 2, ToRevision: 3,
		},
		Intent: relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
			Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
		}}},
	}
	if result, err := faultExecutePlan(context.Background(), freshBackend, []faultExecutorStep{unapply}); err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("unapply before second restart = result:%+v error:%v", result, err)
	}
	if sqliteRelationRevision(t, reopened) != 3 || sqliteRelationRecorderCount(t, reopened, "blog", "0002") != 0 ||
		sqliteRelationColumnExists(t, reopened, "article", "editor_id") {
		t.Fatal("unapply did not leave monotonic revision 3 with 0002 pending")
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close after unapply: %v", err)
	}

	reopened = faultOpenExistingDatabase(t, path)
	reapply, report, err := faultReconstructCandidateLocalLinearPlan(
		context.Background(), faultSQLCandidateLocalRestartReader{database: reopened}, faultForwardCatalog(),
	)
	if err != nil || len(reapply) != 1 || reapply[0].Transition.Name != "0002" ||
		reapply[0].Transition.FromRevision != 3 || reapply[0].Transition.ToRevision != 4 ||
		report.Revision != 3 || report.AlreadyApplied != 1 || report.Reconstructed != 1 {
		t.Fatalf("post-unapply reconstruction = plan:%#v report:%+v error:%v", reapply, report, err)
	}
	reapplyBackend := &sqliteRelationBackend{database: reopened}
	if result, err := faultExecutePlan(context.Background(), reapplyBackend, reapply); err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("reapply reconstructed plan = result:%+v error:%v", result, err)
	}
	if sqliteRelationRevision(t, reopened) != 4 || sqliteRelationRecorderCount(t, reopened, "blog", "0002") != 1 ||
		!sqliteRelationColumnExists(t, reopened, "article", "editor_id") {
		t.Fatal("reapply did not preserve recorder membership with monotonic revision 4")
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close after reapply: %v", err)
	}

	reopened = faultOpenExistingDatabase(t, path)
	defer reopened.Close()
	fullyApplied, report, err = faultReconstructCandidateLocalLinearPlan(
		context.Background(), faultSQLCandidateLocalRestartReader{database: reopened}, faultForwardCatalog(),
	)
	if err != nil || len(fullyApplied) != 0 || report.Revision != 4 || report.AlreadyApplied != 2 || report.Reconstructed != 0 {
		t.Fatalf("reopened fully applied reconstruction = plan:%#v report:%+v error:%v", fullyApplied, report, err)
	}
}

func TestFaultCandidateLocalRestartSnapshotIsPinnedMainQualifiedAndFailClosed(t *testing.T) {
	t.Run("SQL recorder stream stops at the accepted migration bound", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		if _, err := database.ExecContext(context.Background(), `
			WITH RECURSIVE sequence(value) AS (
				VALUES(0)
				UNION ALL
				SELECT value + 1 FROM sequence WHERE value < ?
			)
			INSERT INTO "main"."`+sqliteRelationRecorderTable+`" ("app", "name")
			SELECT 'bulk', printf('m%04d', value) FROM sequence
		`, profileMaxDocuments); err != nil {
			t.Fatalf("seed oversized candidate-local recorder: %v", err)
		}
		snapshot, err := (faultSQLCandidateLocalRestartReader{database: database}).
			faultReadCandidateLocalRestartSnapshot(context.Background())
		if err == nil || !reflect.DeepEqual(snapshot, faultCandidateLocalRestartSnapshot{}) ||
			!strings.Contains(err.Error(), "recorder resource limit exceeded") {
			t.Fatalf("oversized SQL recorder snapshot = %+v error:%v", snapshot, err)
		}
	})

	t.Run("SQL recorder identity is bounded before publication", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		if _, err := database.ExecContext(
			context.Background(),
			`INSERT INTO "main"."`+sqliteRelationRecorderTable+`" ("app", "name") VALUES (?, '0001')`,
			strings.Repeat("a", migrationdefinition.MaxSourceIDBytes+1),
		); err != nil {
			t.Fatalf("seed oversized candidate-local recorder identity: %v", err)
		}
		snapshot, err := (faultSQLCandidateLocalRestartReader{database: database}).
			faultReadCandidateLocalRestartSnapshot(context.Background())
		if err == nil || !reflect.DeepEqual(snapshot, faultCandidateLocalRestartSnapshot{}) ||
			!strings.Contains(err.Error(), "identity resource limit exceeded") {
			t.Fatalf("oversized SQL recorder identity snapshot = %+v error:%v", snapshot, err)
		}
	})

	t.Run("SQL recorder BLOB aliases are rejected before publication", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		if _, err := database.ExecContext(
			context.Background(),
			`INSERT INTO "main"."`+sqliteRelationRecorderTable+`" ("app", "name") VALUES (CAST('blog' AS BLOB), CAST('0001' AS BLOB))`,
		); err != nil {
			t.Fatalf("seed BLOB candidate-local recorder identity: %v", err)
		}
		snapshot, err := (faultSQLCandidateLocalRestartReader{database: database}).
			faultReadCandidateLocalRestartSnapshot(context.Background())
		if err == nil || !reflect.DeepEqual(snapshot, faultCandidateLocalRestartSnapshot{}) ||
			!strings.Contains(err.Error(), "storage class is invalid") {
			t.Fatalf("BLOB SQL recorder identity snapshot = %+v error:%v", snapshot, err)
		}
	})

	t.Run("temporary shadow tables cannot replace main candidate history", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		if _, err := database.ExecContext(context.Background(), `
			CREATE TEMP TABLE "`+sqliteRelationRevisionTable+`" (
				"singleton" INTEGER PRIMARY KEY,
				"revision" INTEGER NOT NULL
			);
			INSERT INTO "`+sqliteRelationRevisionTable+`" ("singleton", "revision") VALUES (1, 99);
			CREATE TEMP TABLE "`+sqliteRelationRecorderTable+`" (
				"app" TEXT NOT NULL,
				"name" TEXT NOT NULL
			);
			INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('shadow', 'rogue');
		`); err != nil {
			t.Fatalf("create candidate-local temporary shadows: %v", err)
		}
		snapshot, err := (faultSQLCandidateLocalRestartReader{database: database}).
			faultReadCandidateLocalRestartSnapshot(context.Background())
		if err != nil {
			t.Fatalf("read main-qualified candidate-local snapshot: %v", err)
		}
		wantRecords := []faultMigrationKey{{App: "blog", Name: "0001"}}
		if snapshot.Revision != 1 || !reflect.DeepEqual(snapshot.RecordedSteps, wantRecords) {
			t.Fatalf("main-qualified snapshot = %+v, want revision 1 and %#v", snapshot, wantRecords)
		}
	})

	t.Run("concurrent successor between reads cannot tear pinned snapshot", func(t *testing.T) {
		database, backend, path := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		var journalMode string
		if err := database.QueryRowContext(context.Background(), `PRAGMA journal_mode = WAL`).Scan(&journalMode); err != nil {
			t.Fatalf("enable WAL for pinned snapshot proof: %v", err)
		}
		writer := faultOpenExistingDatabase(t, path)
		defer writer.Close()
		reader := faultSQLCandidateLocalRestartReader{
			database: database,
			afterRevisionRead: func() error {
				transaction, err := writer.BeginTx(context.Background(), nil)
				if err != nil {
					return err
				}
				if _, err := transaction.ExecContext(
					context.Background(),
					`UPDATE "main"."`+sqliteRelationRevisionTable+`" SET "revision" = 2 WHERE "singleton" = 1`,
				); err != nil {
					_ = transaction.Rollback()
					return err
				}
				if _, err := transaction.ExecContext(
					context.Background(),
					`INSERT INTO "main"."`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('blog', '0002')`,
				); err != nil {
					_ = transaction.Rollback()
					return err
				}
				return transaction.Commit()
			},
		}
		snapshot, err := reader.faultReadCandidateLocalRestartSnapshot(context.Background())
		if err != nil {
			t.Fatalf("read pinned candidate-local snapshot across concurrent successor: %v", err)
		}
		wantOld := []faultMigrationKey{{App: "blog", Name: "0001"}}
		if snapshot.Revision != 1 || !reflect.DeepEqual(snapshot.RecordedSteps, wantOld) {
			t.Fatalf("pinned snapshot tore across successor: %+v", snapshot)
		}
		if sqliteRelationRevision(t, database) != 2 || sqliteRelationRecorderCount(t, database, "blog", "0002") != 1 {
			t.Fatal("concurrent successor did not become durable after pinned read completed")
		}
	})

	t.Run("corrupt durable revision fails without plan publication", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		if _, err := database.ExecContext(
			context.Background(),
			`UPDATE "main"."`+sqliteRelationRevisionTable+`" SET "revision" = -1 WHERE "singleton" = 1`,
		); err != nil {
			t.Fatalf("corrupt candidate-local revision: %v", err)
		}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(
			context.Background(), faultSQLCandidateLocalRestartReader{database: database}, faultForwardCatalog(),
		)
		if err == nil || plan != nil || !reflect.DeepEqual(report, faultCandidateLocalRestartReport{RecorderReads: 1}) {
			t.Fatalf("corrupt candidate-local snapshot = plan:%#v report:%+v error:%v", plan, report, err)
		}
	})

	t.Run("BLOB durable revision fails before snapshot publication", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		if _, err := database.ExecContext(
			context.Background(),
			`UPDATE "main"."`+sqliteRelationRevisionTable+`" SET "revision" = CAST('0' AS BLOB) WHERE "singleton" = 1`,
		); err != nil {
			t.Fatalf("corrupt candidate-local revision storage class: %v", err)
		}
		snapshot, err := (faultSQLCandidateLocalRestartReader{database: database}).
			faultReadCandidateLocalRestartSnapshot(context.Background())
		if err == nil || !reflect.DeepEqual(snapshot, faultCandidateLocalRestartSnapshot{}) ||
			!strings.Contains(err.Error(), "closed storage shape") {
			t.Fatalf("BLOB candidate-local revision snapshot = %+v error:%v", snapshot, err)
		}
	})
}

func TestFaultCandidateLocalLinearCatalogRejectsBeforeReaderIO(t *testing.T) {
	base := faultForwardCatalog()
	tests := []struct {
		name   string
		mutate func([]faultExecutorStep)
	}{
		{name: "explicit_order", mutate: func(catalog []faultExecutorStep) {
			catalog[0], catalog[1] = catalog[1], catalog[0]
		}},
		{name: "identity", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Transition.Name = "other"
		}},
		{name: "direction", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Transition.Direction = relationBackendUnapply
		}},
		{name: "duplicate_identity", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Transition.App = catalog[0].Transition.App
			catalog[1].Transition.Name = catalog[0].Transition.Name
			catalog[1].Intent.App = catalog[0].Intent.App
			catalog[1].Intent.Name = catalog[0].Intent.Name
		}},
		{name: "declared_fence", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Transition.ToRevision = catalog[1].Transition.FromRevision
		}},
		{name: "declared_fence_overflow", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Transition.FromRevision = math.MaxInt64
			catalog[1].Transition.ToRevision = math.MinInt64
		}},
		{name: "intent", mutate: func(catalog []faultExecutorStep) {
			catalog[1].Intent.Changes = nil
		}},
	}
	t.Run("empty", func(t *testing.T) {
		readerCause := errors.New("reader must not be called")
		reader := &faultCountingCandidateLocalRestartReader{err: readerCause}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, nil)
		if !errors.Is(err, faultErrInvalidCandidateLocalCatalog) || errors.Is(err, readerCause) || reader.calls != 0 ||
			plan != nil || !reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
			t.Fatalf("empty catalog result = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
	t.Run("catalog resource limit precedes clone and reader", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{err: errors.New("reader must not be called")}
		catalog := make([]faultExecutorStep, profileMaxDocuments+1)
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, catalog)
		if !errors.Is(err, faultErrInvalidCandidateLocalCatalog) || reader.calls != 0 || plan != nil ||
			!reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
			t.Fatalf("oversized catalog = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
	t.Run("nested catalog resource limit precedes nested clone and reader", func(t *testing.T) {
		catalog := faultForwardCatalog()
		catalog[0].Intent.Changes[0].After.Columns = make([]relationBackendColumn, profileMaxFields+1)
		reader := &faultCountingCandidateLocalRestartReader{err: errors.New("reader must not be called")}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, catalog)
		if !errors.Is(err, faultErrInvalidCandidateLocalCatalog) || reader.calls != 0 || plan != nil ||
			!reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
			t.Fatalf("nested oversized catalog = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
	t.Run("reader snapshot resource limit precedes clone and publication", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{snapshot: faultCandidateLocalRestartSnapshot{
			Revision:      1,
			RecordedSteps: make([]faultMigrationKey, profileMaxDocuments+1),
		}}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, faultForwardCatalog())
		if err == nil || reader.calls != 1 || plan != nil ||
			!reflect.DeepEqual(report, faultCandidateLocalRestartReport{RecorderReads: 1}) {
			t.Fatalf("oversized snapshot = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
	t.Run("reader snapshot identity bytes precede clone and publication", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{snapshot: faultCandidateLocalRestartSnapshot{
			Revision: 1,
			RecordedSteps: []faultMigrationKey{{
				App:  strings.Repeat("a", (1<<10)+1),
				Name: "0001",
			}},
		}}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, faultForwardCatalog())
		if err == nil || reader.calls != 1 || plan != nil ||
			!reflect.DeepEqual(report, faultCandidateLocalRestartReport{RecorderReads: 1}) {
			t.Fatalf("oversized snapshot identity = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := make([]faultExecutorStep, len(base))
			for index := range base {
				catalog[index] = faultCloneExecutorStep(base[index])
			}
			test.mutate(catalog)
			readerCause := errors.New("reader must not be called")
			reader := &faultCountingCandidateLocalRestartReader{err: readerCause}
			plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, catalog)
			if !errors.Is(err, faultErrInvalidCandidateLocalCatalog) || errors.Is(err, readerCause) {
				t.Fatalf("catalog error = %v, want pure invalid-catalog failure", err)
			}
			if reader.calls != 0 || plan != nil || !reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
				t.Fatalf("invalid catalog touched reader/published result: calls=%d plan=%#v report=%+v", reader.calls, plan, report)
			}
		})
	}

	t.Run("invalid catalog precedes canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := &faultCountingCandidateLocalRestartReader{err: errors.New("reader must not be called")}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(ctx, reader, nil)
		if !errors.Is(err, faultErrInvalidCandidateLocalCatalog) || errors.Is(err, context.Canceled) ||
			reader.calls != 0 || plan != nil || !reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
			t.Fatalf("invalid/canceled precedence = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})

	t.Run("canceled context rejects before reader without publication", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := &faultCountingCandidateLocalRestartReader{snapshot: faultCandidateLocalRestartSnapshot{Revision: 9}}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(ctx, reader, faultForwardCatalog())
		if !errors.Is(err, context.Canceled) || reader.calls != 0 || plan != nil || !reflect.DeepEqual(report, faultCandidateLocalRestartReport{}) {
			t.Fatalf("canceled restart = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})

	t.Run("reader context cause remains discoverable", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{err: context.DeadlineExceeded}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, faultForwardCatalog())
		if !errors.Is(err, context.DeadlineExceeded) || reader.calls != 1 || plan != nil ||
			!reflect.DeepEqual(report, faultCandidateLocalRestartReport{RecorderReads: 1}) {
			t.Fatalf("reader context failure = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})

	t.Run("revision cannot trail durable recorder membership", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{snapshot: faultCandidateLocalRestartSnapshot{
			Revision:      0,
			RecordedSteps: []faultMigrationKey{{App: "blog", Name: "0001"}},
		}}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, faultForwardCatalog())
		if err == nil || !strings.Contains(err.Error(), "revision 0 is behind 1 durable recorder rows") ||
			reader.calls != 1 || plan != nil || report.RecorderReads != 1 || report.Revision != 0 ||
			!reflect.DeepEqual(report.RecordedSteps, []faultMigrationKey{{App: "blog", Name: "0001"}}) {
			t.Fatalf("impossible restart snapshot = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})

	t.Run("revision parity must match durable recorder membership", func(t *testing.T) {
		reader := &faultCountingCandidateLocalRestartReader{snapshot: faultCandidateLocalRestartSnapshot{
			Revision:      2,
			RecordedSteps: []faultMigrationKey{{App: "blog", Name: "0001"}},
		}}
		plan, report, err := faultReconstructCandidateLocalLinearPlan(context.Background(), reader, faultForwardCatalog())
		if err == nil || !strings.Contains(err.Error(), "revision 2 has impossible parity") ||
			reader.calls != 1 || plan != nil || report.RecorderReads != 1 || report.Revision != 2 {
			t.Fatalf("impossible restart parity = calls:%d plan:%#v report:%+v error:%v", reader.calls, plan, report, err)
		}
	})
}

type faultCleanupFailureStage string

const (
	faultCleanupFailBegin         faultCleanupFailureStage = "begin"
	faultCleanupFailBeginPartial  faultCleanupFailureStage = "begin_partial"
	faultCleanupFailApply         faultCleanupFailureStage = "apply"
	faultCleanupFailRecord        faultCleanupFailureStage = "record"
	faultCleanupFailCommit        faultCleanupFailureStage = "commit"
	faultCleanupCancelAfterOpen   faultCleanupFailureStage = "cancel_after_open"
	faultCleanupCancelAfterBegin  faultCleanupFailureStage = "cancel_after_begin"
	faultCleanupCancelAfterApply  faultCleanupFailureStage = "cancel_after_apply"
	faultCleanupCancelAfterRecord faultCleanupFailureStage = "cancel_after_record"
)

type faultCleanupBackend struct {
	stage       faultCleanupFailureStage
	cancel      context.CancelFunc
	primary     error
	cleanup     error
	session     *faultCleanupSession
	transaction *faultCleanupTransaction
	openCalls   int
	beginCalls  int
	openHook    func()
}

func faultNewCleanupBackend(
	stage faultCleanupFailureStage,
	cancel context.CancelFunc,
	primary error,
	cleanup error,
) *faultCleanupBackend {
	backend := &faultCleanupBackend{stage: stage, cancel: cancel, primary: primary, cleanup: cleanup}
	backend.transaction = &faultCleanupTransaction{backend: backend}
	return backend
}

func (*faultCleanupBackend) RelationMigrationCapabilities() relationBackendCapabilities {
	return relationBackendCapabilities{
		Profile: 1, CreateModel: true, NullableAddField: true,
		EmptyRequiredAddField: true, BoundedRemake: true,
	}
}

func (backend *faultCleanupBackend) OpenRelationMigrationSession(context.Context) (relationBackendOptionalSession, error) {
	backend.openCalls++
	if backend.openHook != nil {
		backend.openHook()
	}
	backend.session = &faultCleanupSession{backend: backend, held: true}
	if backend.stage == faultCleanupCancelAfterOpen {
		backend.cancel()
	}
	return backend.session, nil
}

type faultCleanupSession struct {
	backend                *faultCleanupBackend
	held                   bool
	closeCalls             int
	closeContextErr        error
	closeDeadlineOK        bool
	closeDeadlineRemaining time.Duration
}

func (session *faultCleanupSession) BeginRelationFencedMigration(
	context.Context,
	relationBackendTransition,
	relationBackendStepIntent,
) (relationBackendTransaction, error) {
	session.backend.beginCalls++
	if session.backend.stage == faultCleanupFailBegin {
		session.backend.cancel()
		return nil, session.backend.primary
	}
	if session.backend.stage == faultCleanupFailBeginPartial {
		session.backend.cancel()
		return session.backend.transaction, session.backend.primary
	}
	if session.backend.stage == faultCleanupCancelAfterBegin {
		session.backend.cancel()
	}
	return session.backend.transaction, nil
}

func (session *faultCleanupSession) Close(ctx context.Context) error {
	session.closeCalls++
	session.closeContextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	session.closeDeadlineOK = ok
	if ok {
		session.closeDeadlineRemaining = time.Until(deadline)
	}
	if session.closeContextErr != nil {
		return session.closeContextErr
	}
	session.held = false
	return session.backend.cleanup
}

type faultCleanupTransaction struct {
	backend                   *faultCleanupBackend
	rollbackCalls             int
	rollbackContextErr        error
	rollbackDeadlineOK        bool
	rollbackDeadlineRemaining time.Duration
	applyCalls                int
	recordCalls               int
	commitCalls               int
}

func (transaction *faultCleanupTransaction) ApplyRelationChange(context.Context, relationBackendChange) error {
	transaction.applyCalls++
	if transaction.backend.stage == faultCleanupFailApply {
		transaction.backend.cancel()
		return transaction.backend.primary
	}
	if transaction.backend.stage == faultCleanupCancelAfterApply {
		transaction.backend.cancel()
	}
	return nil
}

func (transaction *faultCleanupTransaction) RecordRelationTransition(context.Context) error {
	transaction.recordCalls++
	if transaction.backend.stage == faultCleanupFailRecord {
		transaction.backend.cancel()
		return transaction.backend.primary
	}
	if transaction.backend.stage == faultCleanupCancelAfterRecord {
		transaction.backend.cancel()
	}
	return nil
}

func (transaction *faultCleanupTransaction) CommitRelationFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.commitCalls++
	if transaction.backend.stage == faultCleanupFailCommit {
		transaction.backend.cancel()
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, transaction.backend.primary
	}
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil
}

func (transaction *faultCleanupTransaction) RollbackRelation(ctx context.Context) error {
	transaction.rollbackCalls++
	transaction.rollbackContextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	transaction.rollbackDeadlineOK = ok
	if ok {
		transaction.rollbackDeadlineRemaining = time.Until(deadline)
	}
	return nil
}

type faultCountingCandidateLocalRestartReader struct {
	calls    int
	snapshot faultCandidateLocalRestartSnapshot
	err      error
}

func (reader *faultCountingCandidateLocalRestartReader) faultReadCandidateLocalRestartSnapshot(
	context.Context,
) (faultCandidateLocalRestartSnapshot, error) {
	reader.calls++
	return reader.snapshot, reader.err
}

func faultForwardCatalog() []faultExecutorStep {
	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	return []faultExecutorStep{
		{
			CatalogOrder: 0,
			Transition: relationBackendTransition{
				App: "blog", Name: "0001", Direction: relationBackendApply,
				FromRevision: 40, ToRevision: 41,
			},
			Intent: relationBackendArticleCreateIntent(),
		},
		{
			CatalogOrder: 1,
			Transition: relationBackendTransition{
				App: "blog", Name: "0002", Direction: relationBackendApply,
				FromRevision: 70, ToRevision: 71,
			},
			Intent: relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
				Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
			}}},
		},
	}
}

func TestFaultCandidateDjangoRecorderObservationIsSeparateFromGoDjAtomicPolicy(t *testing.T) {
	if faultDjangoRecorderObservation == faultGoDjAtomicProposal {
		t.Fatal("Django recorder observation and GoDj atomic proposal must remain distinct")
	}
	if !strings.Contains(string(faultDjangoRecorderObservation), "schema_durable_record_absent") {
		t.Fatalf("Django observation = %q, want schema durable/record absent wording", faultDjangoRecorderObservation)
	}
	if !strings.Contains(string(faultGoDjAtomicProposal), "schema_record_revision_atomic") {
		t.Fatalf("GoDj policy = %q, want schema/record/revision atomic wording", faultGoDjAtomicProposal)
	}

	database, backend, step := faultRelationRemoveFixture(t)
	defer database.Close()
	backend.faults = faultNewPlan(faultStageRecorder, errors.New("GoDj recorder sentinel"))
	_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{step})
	if err == nil {
		t.Fatal("GoDj recorder fault returned nil")
	}
	if sqliteRelationRevision(t, database) != 2 ||
		sqliteRelationRecorderCount(t, database, "blog", "0002") != 1 ||
		!sqliteRelationColumnExists(t, database, "article", "editor_id") {
		t.Fatal("GoDj recorder fault did not atomically roll back schema, record, and revision")
	}
}

func faultRelationRemoveFixture(t *testing.T) (*sql.DB, *sqliteRelationBackend, faultExecutorStep) {
	t.Helper()
	database, backend, _ := sqliteRelationOpenCandidate(t)
	sqliteRelationApplyInitialArticle(t, backend)
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO "author" ("id", "name") VALUES (5, 'Ada');
		 INSERT INTO "article" ("id", "title", "author_id") VALUES (3, 'one', 5), (8, 'two', 5)`,
	); err != nil {
		_ = database.Close()
		t.Fatalf("seed fault fixture: %v", err)
	}
	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
	}}}
	if _, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
		Intent:     add,
	}}); err != nil {
		_ = database.Close()
		t.Fatalf("add fault fixture editor: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `UPDATE "article" SET "editor_id" = 5 WHERE "id" = 8`); err != nil {
		_ = database.Close()
		t.Fatalf("seed fault fixture editor value: %v", err)
	}
	backend.beginCalls = 0
	backend.commitCalls = 0
	backend.rollbackCalls = 0
	backend.trace = nil
	remove := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
	}}}
	return database, backend, faultExecutorStep{
		Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendUnapply, FromRevision: 2, ToRevision: 3},
		Intent:     remove,
	}
}

func faultDurableState(t *testing.T, database *sql.DB) string {
	t.Helper()
	var builder strings.Builder
	builder.WriteString(sqliteRelationDumpState(t, database))
	recorderRows, err := database.QueryContext(context.Background(),
		`SELECT "app", "name" FROM "`+sqliteRelationRecorderTable+`" ORDER BY "app", "name"`,
	)
	if err != nil {
		t.Fatalf("read durable recorder snapshot: %v", err)
	}
	for recorderRows.Next() {
		var app, name string
		if err := recorderRows.Scan(&app, &name); err != nil {
			_ = recorderRows.Close()
			t.Fatalf("scan durable recorder snapshot: %v", err)
		}
		fmt.Fprintf(&builder, "record:%s:%s\n", app, name)
	}
	if err := recorderRows.Close(); err != nil {
		t.Fatalf("close durable recorder snapshot: %v", err)
	}
	if err := recorderRows.Err(); err != nil {
		t.Fatalf("iterate durable recorder snapshot: %v", err)
	}
	articleRows, err := database.QueryContext(context.Background(),
		`SELECT "id", "title", "author_id", "editor_id" FROM "article" ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("read durable article snapshot: %v", err)
	}
	for articleRows.Next() {
		var id, authorID int64
		var title string
		var editorID sql.NullInt64
		if err := articleRows.Scan(&id, &title, &authorID, &editorID); err != nil {
			_ = articleRows.Close()
			t.Fatalf("scan durable article snapshot: %v", err)
		}
		fmt.Fprintf(&builder, "article:%d:%s:%d:%v:%d\n", id, title, authorID, editorID.Valid, editorID.Int64)
	}
	if err := articleRows.Close(); err != nil {
		t.Fatalf("close durable article snapshot: %v", err)
	}
	if err := articleRows.Err(); err != nil {
		t.Fatalf("iterate durable article snapshot: %v", err)
	}
	sequenceRows, err := database.QueryContext(context.Background(),
		`SELECT "name", "seq" FROM "sqlite_sequence" ORDER BY "name"`,
	)
	if err != nil {
		t.Fatalf("read durable sequence snapshot: %v", err)
	}
	for sequenceRows.Next() {
		var name string
		var sequence int64
		if err := sequenceRows.Scan(&name, &sequence); err != nil {
			_ = sequenceRows.Close()
			t.Fatalf("scan durable sequence snapshot: %v", err)
		}
		fmt.Fprintf(&builder, "sequence:%s:%d\n", name, sequence)
	}
	if err := sequenceRows.Close(); err != nil {
		t.Fatalf("close durable sequence snapshot: %v", err)
	}
	if err := sequenceRows.Err(); err != nil {
		t.Fatalf("iterate durable sequence snapshot: %v", err)
	}
	return builder.String()
}

func faultDatabasePath(t *testing.T, database *sql.DB) string {
	t.Helper()
	var path string
	rows, err := database.QueryContext(context.Background(), `PRAGMA database_list`)
	if err != nil {
		t.Fatalf("read database_list: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, file string
		if err := rows.Scan(&sequence, &name, &file); err != nil {
			t.Fatalf("scan database_list: %v", err)
		}
		if name == "main" {
			path = file
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate database_list: %v", err)
	}
	if path == "" {
		t.Fatal("database_list did not return main file")
	}
	return path
}

func faultOpenExistingDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("reopen SQLite candidate: %v", err)
	}
	database.SetMaxOpenConns(1)
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("ping reopened SQLite candidate: %v", err)
	}
	return database
}
