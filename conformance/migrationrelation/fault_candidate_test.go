package migrationrelation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

const faultCleanupTimeout = 2 * time.Second

type faultStage string

const (
	faultStagePragmaEnable   faultStage = "pragma_enable"
	faultStagePragmaRead     faultStage = "pragma_read"
	faultStageBegin          faultStage = "begin"
	faultStageRevisionClaim  faultStage = "revision_claim"
	faultStageCreate         faultStage = "create"
	faultStageCopy           faultStage = "copy"
	faultStageDrop           faultStage = "drop"
	faultStageRename         faultStage = "rename"
	faultStageForeignKey     faultStage = "foreign_key_check"
	faultStageRecorder       faultStage = "recorder"
	faultStageRevisionVerify faultStage = "revision_verify"
	faultStageCommit         faultStage = "commit"
	faultStageRollback       faultStage = "rollback"
)

type faultCommitMode uint8

const (
	faultCommitNone faultCommitMode = iota
	faultCommitRolledBack
	faultCommitCommitted
	faultCommitUnknown
)

type faultPlan struct {
	mu            sync.Mutex
	stage         faultStage
	cause         error
	rollbackCause error
	commitMode    faultCommitMode
	calls         map[faultStage]int
}

func faultNewPlan(stage faultStage, cause error) *faultPlan {
	return &faultPlan{stage: stage, cause: cause, calls: make(map[faultStage]int)}
}

func faultNewCommitPlan(mode faultCommitMode, cause error) *faultPlan {
	return &faultPlan{
		stage: faultStageCommit, cause: cause, commitMode: mode,
		calls: make(map[faultStage]int),
	}
}

func (plan *faultPlan) faultHit(stage faultStage) error {
	if plan == nil {
		return nil
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	if plan.calls == nil {
		plan.calls = make(map[faultStage]int)
	}
	plan.calls[stage]++
	if stage == faultStageRollback && plan.rollbackCause != nil {
		return plan.rollbackCause
	}
	if plan.stage == stage && stage != faultStageCommit {
		return plan.cause
	}
	return nil
}

func (plan *faultPlan) faultCommit() (faultCommitMode, error) {
	if plan == nil {
		return faultCommitNone, nil
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	if plan.calls == nil {
		plan.calls = make(map[faultStage]int)
	}
	plan.calls[faultStageCommit]++
	if plan.stage != faultStageCommit {
		return faultCommitNone, nil
	}
	return plan.commitMode, plan.cause
}

func (plan *faultPlan) faultCalls(stage faultStage) int {
	if plan == nil {
		return 0
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	return plan.calls[stage]
}

type faultExecutorStep struct {
	CatalogOrder int
	Transition   relationBackendTransition
	Intent       relationBackendStepIntent
}

type faultExecutorResult struct {
	ConfirmedSteps int
	Attempts       []int
	Outcome        migrationbackend.CommitDurability
}

type faultCandidateLocalRestartReport struct {
	Revision       int64
	RecorderReads  int
	RecordedSteps  []faultMigrationKey
	Reconstructed  int
	AlreadyApplied int
}

type faultMigrationKey struct {
	App  string
	Name string
}

type faultCandidateError struct {
	Category string
	Code     string
	Stage    string
	Reason   string
	Cause    error
}

func (e *faultCandidateError) Error() string {
	if e == nil {
		return "migration relation fault candidate error"
	}
	return fmt.Sprintf("%s/%s stage=%s reason=%s: %v", e.Category, e.Code, e.Stage, e.Reason, e.Cause)
}

func (e *faultCandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var faultErrInvalidCandidateLocalCatalog = errors.New("invalid candidate-local linear restart catalog")

type faultCandidateLocalRestartSnapshot struct {
	Revision      int64
	RecordedSteps []faultMigrationKey
}

type faultCandidateLocalRestartReader interface {
	faultReadCandidateLocalRestartSnapshot(context.Context) (faultCandidateLocalRestartSnapshot, error)
}

type faultSQLCandidateLocalRestartReader struct {
	database          *sql.DB
	afterRevisionRead func() error
}

func (reader faultSQLCandidateLocalRestartReader) faultReadCandidateLocalRestartSnapshot(
	ctx context.Context,
) (result faultCandidateLocalRestartSnapshot, resultErr error) {
	if reader.database == nil {
		return faultCandidateLocalRestartSnapshot{}, errors.New("candidate-local restart snapshot database is nil")
	}
	transaction, err := reader.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("begin candidate-local restart read transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, transaction.Rollback())
		}
	}()

	revisionRows, err := transaction.QueryContext(
		ctx,
		`SELECT typeof("singleton"), `+
			`CASE WHEN typeof("singleton") = 'integer' THEN "singleton" ELSE NULL END, `+
			`typeof("revision"), `+
			`CASE WHEN typeof("revision") = 'integer' THEN "revision" ELSE NULL END `+
			`FROM "main"."`+sqliteRelationRevisionTable+`" LIMIT 2`,
	)
	if err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("read candidate-local restart revision: %w", err)
	}
	revisionCount := 0
	for revisionRows.Next() {
		var singletonType, revisionType string
		var singleton, revision sql.NullInt64
		if err := revisionRows.Scan(&singletonType, &singleton, &revisionType, &revision); err != nil {
			_ = revisionRows.Close()
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("scan candidate-local restart revision: %w", err)
		}
		revisionCount++
		if revisionCount > 1 || singletonType != "integer" || !singleton.Valid || singleton.Int64 != 1 ||
			revisionType != "integer" || !revision.Valid || revision.Int64 < 0 {
			_ = revisionRows.Close()
			return faultCandidateLocalRestartSnapshot{}, errors.New("candidate-local restart revision is outside the closed storage shape")
		}
		result.Revision = revision.Int64
	}
	if err := revisionRows.Err(); err != nil {
		_ = revisionRows.Close()
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("iterate candidate-local restart revision: %w", err)
	}
	if err := revisionRows.Close(); err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("close candidate-local restart revision: %w", err)
	}
	if revisionCount != 1 {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("candidate-local restart revision row count=%d, want 1", revisionCount)
	}
	if reader.afterRevisionRead != nil {
		if err := reader.afterRevisionRead(); err != nil {
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("candidate-local restart inter-read hook: %w", err)
		}
	}
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT `+
			`typeof("app"), `+
			`typeof("name"), `+
			`COALESCE(length(CAST("app" AS BLOB)), -1), `+
			`COALESCE(length(CAST("name" AS BLOB)), -1), `+
			`substr(CAST("app" AS BLOB), 1, ?), `+
			`substr(CAST("name" AS BLOB), 1, ?) `+
			`FROM "main"."`+sqliteRelationRecorderTable+`" LIMIT ?`,
		migrationdefinition.MaxSourceIDBytes+1,
		migrationdefinition.MaxSourceIDBytes+1,
		profileMaxDocuments+1,
	)
	if err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("read candidate-local restart recorder: %w", err)
	}
	for rows.Next() {
		var appType, nameType string
		var appBytes, nameBytes int64
		var appPrefix, namePrefix []byte
		if err := rows.Scan(&appType, &nameType, &appBytes, &nameBytes, &appPrefix, &namePrefix); err != nil {
			_ = rows.Close()
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("scan candidate-local restart recorder: %w", err)
		}
		if len(result.RecordedSteps) >= profileMaxDocuments {
			_ = rows.Close()
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf(
				"candidate-local restart recorder resource limit exceeded: more than %d rows",
				profileMaxDocuments,
			)
		}
		if appType != "text" || nameType != "text" {
			_ = rows.Close()
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf(
				"candidate-local restart recorder storage class is invalid: app=%s name=%s",
				appType,
				nameType,
			)
		}
		if appBytes < 0 || nameBytes < 0 ||
			appBytes > int64(migrationdefinition.MaxSourceIDBytes) ||
			nameBytes > int64(migrationdefinition.MaxSourceIDBytes) {
			_ = rows.Close()
			return faultCandidateLocalRestartSnapshot{}, fmt.Errorf(
				"candidate-local restart recorder identity resource limit exceeded: app_bytes=%d name_bytes=%d",
				appBytes,
				nameBytes,
			)
		}
		key := faultMigrationKey{App: string(appPrefix), Name: string(namePrefix)}
		result.RecordedSteps = append(result.RecordedSteps, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("iterate candidate-local restart recorder: %w", err)
	}
	if err := rows.Close(); err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("close candidate-local restart recorder: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return faultCandidateLocalRestartSnapshot{}, fmt.Errorf("commit candidate-local restart read transaction: %w", err)
	}
	return result, nil
}

// faultReconstructCandidateLocalLinearPlan is intentionally only quiescent,
// candidate-local evidence. It reads the spike's private revision/recorder
// tables and a caller-supplied linear catalog; it does not exercise GoDj's
// product epoch/fingerprint token, migration DAG, state reconstructor, or
// lifecycle session. The SQL reader nevertheless pins its two reads in one
// transaction so even this narrow evidence cannot publish a torn snapshot.
func faultReconstructCandidateLocalLinearPlan(
	ctx context.Context,
	reader faultCandidateLocalRestartReader,
	catalog []faultExecutorStep,
) ([]faultExecutorStep, faultCandidateLocalRestartReport, error) {
	if ctx == nil {
		return nil, faultCandidateLocalRestartReport{}, errors.New("candidate-local restart reconstruction requires context")
	}
	snapshots, err := faultPrepareCandidateLocalLinearCatalog(catalog)
	if err != nil {
		return nil, faultCandidateLocalRestartReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, faultCandidateLocalRestartReport{}, fmt.Errorf("candidate-local restart reconstruction context: %w", err)
	}
	if reader == nil {
		return nil, faultCandidateLocalRestartReport{}, errors.New("candidate-local restart reconstruction requires snapshot reader")
	}
	durable, err := reader.faultReadCandidateLocalRestartSnapshot(ctx)
	report := faultCandidateLocalRestartReport{RecorderReads: 1}
	if err != nil {
		return nil, report, fmt.Errorf("read candidate-local restart snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, report, fmt.Errorf("candidate-local restart reconstruction context after snapshot read: %w", err)
	}
	if durable.Revision < 0 {
		return nil, report, errors.New("candidate-local restart revision is negative")
	}
	if len(durable.RecordedSteps) > profileMaxDocuments {
		return nil, report, fmt.Errorf(
			"candidate-local restart recorder resource limit exceeded: %d > %d",
			len(durable.RecordedSteps),
			profileMaxDocuments,
		)
	}
	if err := faultValidateRestartKeyResources(durable.RecordedSteps); err != nil {
		return nil, report, err
	}
	report.Revision = durable.Revision
	report.RecordedSteps = append([]faultMigrationKey(nil), durable.RecordedSteps...)
	sort.Slice(report.RecordedSteps, func(left, right int) bool {
		return faultMigrationKeyLess(report.RecordedSteps[left], report.RecordedSteps[right])
	})
	recorded := make(map[faultMigrationKey]struct{}, len(report.RecordedSteps))
	for _, key := range report.RecordedSteps {
		if key.App == "" || key.Name == "" {
			return nil, report, errors.New("candidate-local restart recorder contains invalid migration identity")
		}
		if _, exists := recorded[key]; exists {
			return nil, report, errors.New("candidate-local restart recorder contains duplicate migration")
		}
		recorded[key] = struct{}{}
	}
	if durable.Revision < int64(len(recorded)) {
		return nil, report, fmt.Errorf(
			"candidate-local restart revision %d is behind %d durable recorder rows",
			durable.Revision,
			len(recorded),
		)
	}
	if (durable.Revision-int64(len(recorded)))%2 != 0 {
		return nil, report, fmt.Errorf(
			"candidate-local restart revision %d has impossible parity for %d durable recorder rows",
			durable.Revision,
			len(recorded),
		)
	}

	missing := make([]faultExecutorStep, 0, len(snapshots))
	seenMissing := false
	for _, step := range snapshots {
		key := faultMigrationKey{App: step.Transition.App, Name: step.Transition.Name}
		_, exists := recorded[key]
		if exists {
			if seenMissing {
				return nil, report, fmt.Errorf("candidate-local restart recorder has non-prefix applied step %s.%s", key.App, key.Name)
			}
			report.AlreadyApplied++
			continue
		}
		seenMissing = true
		missing = append(missing, step)
	}
	if len(recorded) != report.AlreadyApplied {
		return nil, report, errors.New("candidate-local restart recorder contains unknown migration")
	}
	nextRevision := report.Revision
	for index := range missing {
		if nextRevision == int64(1<<63-1) {
			return nil, report, errors.New("candidate-local restart revision exhausted")
		}
		missing[index].Transition.FromRevision = nextRevision
		nextRevision++
		missing[index].Transition.ToRevision = nextRevision
	}
	if err := ctx.Err(); err != nil {
		return nil, report, fmt.Errorf("candidate-local restart reconstruction context before publication: %w", err)
	}
	report.Reconstructed = len(missing)
	return missing, report, nil
}

func faultValidateRestartKeyResources(keys []faultMigrationKey) error {
	totalBytes := 0
	for index, key := range keys {
		for _, value := range []struct {
			name string
			text string
		}{{name: "app", text: key.App}, {name: "name", text: key.Name}} {
			if len(value.text) > migrationdefinition.MaxSourceIDBytes {
				return fmt.Errorf(
					"candidate-local restart recorder %s resource limit exceeded at row %d: %d > %d",
					value.name,
					index,
					len(value.text),
					migrationdefinition.MaxSourceIDBytes,
				)
			}
			if len(value.text) > migrationdefinition.MaxBatchBytes-totalBytes {
				return fmt.Errorf(
					"candidate-local restart recorder aggregate identity bytes exceed %d",
					migrationdefinition.MaxBatchBytes,
				)
			}
			totalBytes += len(value.text)
		}
	}
	return nil
}

func faultPrepareCandidateLocalLinearCatalog(catalog []faultExecutorStep) ([]faultExecutorStep, error) {
	if len(catalog) == 0 {
		return nil, fmt.Errorf("%w: catalog is empty", faultErrInvalidCandidateLocalCatalog)
	}
	if len(catalog) > profileMaxDocuments {
		return nil, fmt.Errorf(
			"%w: catalog resource limit exceeded: %d > %d",
			faultErrInvalidCandidateLocalCatalog,
			len(catalog),
			profileMaxDocuments,
		)
	}
	if err := faultValidateAggregateIntentResources(catalog); err != nil {
		return nil, fmt.Errorf("%w: catalog resources: %v", faultErrInvalidCandidateLocalCatalog, err)
	}
	snapshots := make([]faultExecutorStep, len(catalog))
	seen := make(map[faultMigrationKey]struct{}, len(catalog))
	for index, step := range catalog {
		snapshot := faultCloneExecutorStep(step)
		if snapshot.CatalogOrder != index {
			return nil, fmt.Errorf("%w: step %d has order %d", faultErrInvalidCandidateLocalCatalog, index, snapshot.CatalogOrder)
		}
		if snapshot.Transition.App == "" || snapshot.Transition.Name == "" ||
			snapshot.Transition.Direction != relationBackendApply ||
			snapshot.Transition.App != snapshot.Intent.App || snapshot.Transition.Name != snapshot.Intent.Name ||
			snapshot.Transition.FromRevision < 0 || snapshot.Transition.FromRevision == math.MaxInt64 ||
			snapshot.Transition.ToRevision != snapshot.Transition.FromRevision+1 {
			return nil, fmt.Errorf("%w: step %d identity, direction, or declared fence is invalid", faultErrInvalidCandidateLocalCatalog, index)
		}
		key := faultMigrationKey{App: snapshot.Transition.App, Name: snapshot.Transition.Name}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate migration %s.%s", faultErrInvalidCandidateLocalCatalog, key.App, key.Name)
		}
		seen[key] = struct{}{}
		if err := relationBackendValidateIntent(snapshot.Intent); err != nil {
			return nil, fmt.Errorf("%w: step %d intent: %v", faultErrInvalidCandidateLocalCatalog, index, err)
		}
		snapshots[index] = snapshot
	}
	if err := faultValidatePlanStateContinuity(snapshots); err != nil {
		return nil, fmt.Errorf("%w: catalog state continuity: %v", faultErrInvalidCandidateLocalCatalog, err)
	}
	return snapshots, nil
}

func faultCloneExecutorStep(step faultExecutorStep) faultExecutorStep {
	return faultExecutorStep{
		CatalogOrder: step.CatalogOrder,
		Transition:   step.Transition,
		Intent:       step.Intent.relationBackendClone(),
	}
}

func faultMigrationKeyLess(left, right faultMigrationKey) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	return left.Name < right.Name
}

func faultExecutePlan(
	ctx context.Context,
	candidate any,
	steps []faultExecutorStep,
) (faultExecutorResult, error) {
	result := faultExecutorResult{}
	if ctx == nil {
		return result, fmt.Errorf("%w: context is nil", relationBackendErrIntent)
	}
	if len(steps) > profileMaxDocuments {
		return result, fmt.Errorf(
			"%w: plan resource limit exceeded: %d > %d",
			relationBackendErrIntent,
			len(steps),
			profileMaxDocuments,
		)
	}
	result.Attempts = make([]int, len(steps))
	prepared, backend, err := faultPrepareExecutorPlan(candidate, steps)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for index, step := range prepared {
		result.Attempts[index]++
		opened, err := faultOpenPreparedStep(ctx, backend, step.Transition, step.Intent)
		if err != nil {
			return result, err
		}
		closeSession := func() error { return faultCloseSession(ctx, opened.Session) }
		for _, change := range step.Intent.Changes {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(err, faultRollbackTransaction(ctx, opened.Transaction), closeSession())
			}
			if err := opened.Transaction.ApplyRelationChange(ctx, change); err != nil {
				rollbackErr := faultRollbackTransaction(ctx, opened.Transaction)
				return result, errors.Join(
					fmt.Errorf("apply relation change: %w", err),
					rollbackErr,
					closeSession(),
				)
			}
			if err := ctx.Err(); err != nil {
				return result, errors.Join(err, faultRollbackTransaction(ctx, opened.Transaction), closeSession())
			}
		}
		if err := ctx.Err(); err != nil {
			return result, errors.Join(err, faultRollbackTransaction(ctx, opened.Transaction), closeSession())
		}
		if err := opened.Transaction.RecordRelationTransition(ctx); err != nil {
			rollbackErr := faultRollbackTransaction(ctx, opened.Transaction)
			return result, errors.Join(
				fmt.Errorf("record relation transition: %w", err),
				rollbackErr,
				closeSession(),
			)
		}
		if err := ctx.Err(); err != nil {
			return result, errors.Join(err, faultRollbackTransaction(ctx, opened.Transaction), closeSession())
		}
		outcome, commitErr := opened.Transaction.CommitRelationFenced(ctx)
		result.Outcome = outcome.Durability
		closeErr := closeSession()
		switch outcome.Durability {
		case migrationbackend.CommitCommitted:
			result.ConfirmedSteps++
			if commitErr != nil || closeErr != nil {
				return result, faultCommitFailure("commit_cleanup_failed", "committed_cleanup_error", errors.Join(commitErr, closeErr))
			}
		case migrationbackend.CommitRolledBack:
			if commitErr == nil {
				commitErr = errors.New("commit reported rolled back without a cause")
			}
			return result, faultCommitFailure("commit_failed", "rolled_back", errors.Join(commitErr, closeErr))
		case migrationbackend.CommitUnknown:
			if commitErr == nil {
				commitErr = errors.New("commit durability is unknown")
			}
			return result, faultCommitFailure("commit_outcome_unknown", "unknown", errors.Join(commitErr, closeErr))
		default:
			return result, faultCommitFailure(
				"commit_outcome_unknown",
				"invalid_durability",
				errors.Join(errors.New("commit returned invalid durability"), commitErr, closeErr),
			)
		}
	}
	return result, nil
}

func faultPrepareExecutorPlan(
	candidate any,
	steps []faultExecutorStep,
) ([]faultExecutorStep, relationBackendOptionalBackend, error) {
	if len(steps) > profileMaxDocuments {
		return nil, nil, fmt.Errorf(
			"%w: plan resource limit exceeded: %d > %d",
			relationBackendErrIntent,
			len(steps),
			profileMaxDocuments,
		)
	}
	if err := faultValidateAggregateIntentResources(steps); err != nil {
		return nil, nil, fmt.Errorf("prepare relation plan resources: %w", err)
	}
	prepared := make([]faultExecutorStep, len(steps))
	for index := range steps {
		prepared[index] = faultCloneExecutorStep(steps[index])
	}
	for index := range prepared {
		step := prepared[index]
		if err := relationBackendValidateIntent(step.Intent); err != nil {
			return nil, nil, fmt.Errorf("prepare relation plan step %d intent: %w", index, err)
		}
		if err := relationBackendValidateTransition(step.Transition, step.Intent); err != nil {
			return nil, nil, fmt.Errorf("prepare relation plan step %d transition: %w", index, err)
		}
		if index > 0 && prepared[index-1].Transition.ToRevision != step.Transition.FromRevision {
			return nil, nil, fmt.Errorf(
				"%w: relation plan step %d fence %d does not follow prior successor %d",
				relationBackendErrIntent,
				index,
				step.Transition.FromRevision,
				prepared[index-1].Transition.ToRevision,
			)
		}
	}
	if err := faultValidatePlanStateContinuity(prepared); err != nil {
		return nil, nil, fmt.Errorf("prepare relation plan state continuity: %w", err)
	}
	backend, ok := candidate.(relationBackendOptionalBackend)
	if !ok || relationBackendNilInterface(backend) {
		return nil, nil, relationBackendErrUnsupported
	}
	capabilities := backend.RelationMigrationCapabilities()
	for index := range prepared {
		if !capabilities.relationBackendSupports(prepared[index].Intent) {
			return nil, nil, fmt.Errorf("prepare relation plan step %d: %w", index, relationBackendErrUnsupported)
		}
	}
	return prepared, backend, nil
}

func faultValidatePlanStateContinuity(steps []faultExecutorStep) error {
	type knownModel struct {
		present bool
		model   relationBackendModel
	}
	models := make(map[string]knownModel)
	graphWork := 0
	for stepIndex, step := range steps {
		for changeIndex, change := range step.Intent.Changes {
			table := change.After.Table
			if change.Kind == relationBackendDeleteModel {
				table = change.Before.Table
			}
			key := relationBackendIdentifierKey(table)
			prior, known := models[key]
			if known {
				switch change.Kind {
				case relationBackendCreateModel:
					if prior.present {
						return fmt.Errorf(
							"%w: step %d change %d creates already-present table %q",
							relationBackendErrIntent, stepIndex, changeIndex, table,
						)
					}
				case relationBackendDeleteModel, relationBackendAddField, relationBackendRemoveField:
					if !prior.present || !relationBackendModelsEqual(prior.model, change.Before) {
						return fmt.Errorf(
							"%w: step %d change %d before-state is not the prior plan state for %q",
							relationBackendErrIntent, stepIndex, changeIndex, table,
						)
					}
				}
			}
			if change.Kind == relationBackendDeleteModel {
				models[key] = knownModel{present: false}
			} else {
				models[key] = knownModel{present: true, model: change.After.relationBackendClone()}
			}
			effective := make(map[string]relationBackendModel, len(models))
			nodes := 0
			for modelKey, state := range models {
				if !state.present {
					continue
				}
				members := 1 + len(state.model.Columns) + len(state.model.Relations)
				if members > migrationdefinition.MaxJSONValues-nodes {
					return fmt.Errorf("%w: cross-step graph nodes exceed %d", relationBackendErrIntent, migrationdefinition.MaxJSONValues)
				}
				nodes += members
				effective[modelKey] = state.model
			}
			for targetKey, state := range models {
				if state.present {
					continue
				}
				if source, inbound := relationBackendEffectiveInbound(effective, targetKey); inbound {
					return fmt.Errorf(
						"%w: step %d change %d leaves inbound relation from %q to a known-deleted table %q",
						relationBackendErrIntent,
						stepIndex,
						changeIndex,
						source,
						targetKey,
					)
				}
			}
			if nodes > migrationdefinition.MaxJSONValues-graphWork {
				return fmt.Errorf("%w: cross-step graph work exceeds %d", relationBackendErrIntent, migrationdefinition.MaxJSONValues)
			}
			graphWork += nodes
			if err := relationBackendValidateEffectiveGraph(effective); err != nil {
				return fmt.Errorf("%w: step %d change %d cross-step graph: %w", relationBackendErrIntent, stepIndex, changeIndex, err)
			}
		}
	}
	return nil
}

// faultValidateAggregateIntentResources repeats the raw loader's batch-wide
// work bounds before any plan/catalog deep clone. Per-intent validation alone
// would otherwise allow profileMaxDocuments individually maximal documents to
// compose into gigabytes of slice copying and graph traversal.
func faultValidateAggregateIntentResources(steps []faultExecutorStep) error {
	totalBytes := 0
	totalNodes := 0
	consumeText := func(value string) error {
		if len(value) > migrationdefinition.MaxBatchBytes-totalBytes {
			return fmt.Errorf("aggregate intent bytes exceed %d", migrationdefinition.MaxBatchBytes)
		}
		totalBytes += len(value)
		return nil
	}
	consumeNodes := func(count int) error {
		if count > migrationdefinition.MaxJSONValues-totalNodes {
			return fmt.Errorf("aggregate intent nodes exceed %d", migrationdefinition.MaxJSONValues)
		}
		totalNodes += count
		return nil
	}
	consumeRelation := func(relation relationBackendRelation) error {
		for _, value := range []string{relation.Name, relation.Column, relation.TargetTable, relation.TargetColumn} {
			if err := consumeText(value); err != nil {
				return err
			}
		}
		return consumeNodes(1)
	}
	consumeModel := func(model relationBackendModel) error {
		if err := consumeText(model.Table); err != nil {
			return err
		}
		if err := consumeNodes(1 + len(model.Columns)); err != nil {
			return err
		}
		for _, column := range model.Columns {
			if err := consumeText(column.Name); err != nil {
				return err
			}
			if err := consumeText(column.Type); err != nil {
				return err
			}
		}
		for _, relation := range model.Relations {
			if err := consumeRelation(relation); err != nil {
				return err
			}
		}
		return nil
	}

	for index := range steps {
		intent := steps[index].Intent
		if err := relationBackendValidateResourceShape(intent); err != nil {
			return fmt.Errorf("step %d: %w", index, err)
		}
		if err := consumeText(intent.App); err != nil {
			return fmt.Errorf("step %d: %w", index, err)
		}
		if err := consumeText(intent.Name); err != nil {
			return fmt.Errorf("step %d: %w", index, err)
		}
		if err := consumeNodes(1 + len(intent.Changes)); err != nil {
			return fmt.Errorf("step %d: %w", index, err)
		}
		for _, change := range intent.Changes {
			if err := consumeRelation(change.Relation); err != nil {
				return fmt.Errorf("step %d: %w", index, err)
			}
			if err := consumeModel(change.Before); err != nil {
				return fmt.Errorf("step %d: %w", index, err)
			}
			if err := consumeModel(change.After); err != nil {
				return fmt.Errorf("step %d: %w", index, err)
			}
		}
	}
	return nil
}

func faultOpenPreparedStep(
	ctx context.Context,
	backend relationBackendOptionalBackend,
	transition relationBackendTransition,
	intent relationBackendStepIntent,
) (relationBackendOpenedStep, error) {
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, err
	}
	session, err := backend.OpenRelationMigrationSession(ctx)
	if err != nil {
		return relationBackendOpenedStep{}, errors.Join(
			fmt.Errorf("open relation migration session: %w", err),
			faultCloseSession(ctx, session),
		)
	}
	if relationBackendNilInterface(session) {
		return relationBackendOpenedStep{}, errors.New("open relation migration session returned nil session")
	}
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, errors.Join(err, faultCloseSession(ctx, session))
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, intent)
	if err != nil {
		return relationBackendOpenedStep{}, errors.Join(
			fmt.Errorf("begin relation migration: %w", err),
			faultRollbackTransaction(ctx, transaction),
			faultCloseSession(ctx, session),
		)
	}
	if relationBackendNilInterface(transaction) {
		return relationBackendOpenedStep{}, errors.Join(
			errors.New("begin relation migration returned nil transaction"),
			faultCloseSession(ctx, session),
		)
	}
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, errors.Join(
			err,
			faultRollbackTransaction(ctx, transaction),
			faultCloseSession(ctx, session),
		)
	}
	return relationBackendOpenedStep{Session: session, Transaction: transaction}, nil
}

func faultRollbackTransaction(ctx context.Context, transaction relationBackendTransaction) error {
	if relationBackendNilInterface(transaction) {
		return nil
	}
	cleanupCtx, cancel := faultDetachedCleanupContext(ctx)
	defer cancel()
	return transaction.RollbackRelation(cleanupCtx)
}

func faultCloseSession(ctx context.Context, session relationBackendOptionalSession) error {
	if relationBackendNilInterface(session) {
		return nil
	}
	cleanupCtx, cancel := faultDetachedCleanupContext(ctx)
	defer cancel()
	return session.Close(cleanupCtx)
}

func faultDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), faultCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), faultCleanupTimeout)
}

func faultCommitFailure(code, reason string, cause error) *faultCandidateError {
	return &faultCandidateError{
		Category: "migration_relation_lifecycle_candidate_error",
		Code:     code,
		Stage:    "commit",
		Reason:   reason,
		Cause:    cause,
	}
}

type faultRecorderSemantics string

const (
	// faultDjangoRecorderObservation is an observation from the pinned Django
	// oracle: schema-editor exit can commit DDL before recorder insertion fails.
	faultDjangoRecorderObservation faultRecorderSemantics = "django_observed_schema_durable_record_absent"
	// faultGoDjAtomicProposal is the separate GoDj candidate policy measured by
	// this spike: schema, recorder, and revision share one transaction.
	faultGoDjAtomicProposal faultRecorderSemantics = "godj_proposed_schema_record_revision_atomic"
)
