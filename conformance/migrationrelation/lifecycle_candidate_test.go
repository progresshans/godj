package migrationrelation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const lifecycleRelationCleanupTimeout = 2 * time.Second

var (
	lifecycleRelationErrCapability = errors.New("revision-fenced session has no relation migration capability")
	lifecycleRelationErrIntent     = errors.New("relation lifecycle intent is invalid")
)

// lifecycleRelationFencedSession is an additive candidate port. Embedding the
// existing session preserves every scalar-only implementation: old sessions
// continue to satisfy migrationbackend.RevisionFencedSession without being
// forced to implement this optional method. The complete relation intent is an
// argument to the begin call, so a SQLite implementation can pin a connection,
// enable/read back foreign_keys before BEGIN, then run physical preflight after
// BEGIN and before the first DDL statement.
type lifecycleRelationFencedSession interface {
	migrationbackend.RevisionFencedSession
	BeginRelationFencedMigration(
		context.Context,
		migrationbackend.HistoryTransition,
		relationBackendStepIntent,
	) (lifecycleRelationFencedTransaction, error)
}

// lifecycleRelationFencedTransaction extends, rather than replaces, the
// existing transaction. Scalar schema edits, relation edits, the one recorder
// transition, and CommitFenced therefore share one transaction identity.
type lifecycleRelationFencedTransaction interface {
	migrationbackend.RevisionFencedTransaction
	ApplyRelationChange(context.Context, relationBackendChange) error
}

type lifecycleMixedOperationKind uint8

const (
	lifecycleMixedScalarAdd lifecycleMixedOperationKind = iota + 1
	lifecycleMixedRelationChange
)

type lifecycleMixedOperation struct {
	Kind     lifecycleMixedOperationKind
	Model    ir.Model
	Field    ir.Field
	Relation relationBackendChange
}

type lifecycleMixedResult struct {
	Outcome   migrationbackend.CommitOutcome
	Committed bool
}

func lifecyclePrepareMixedStep(
	transition migrationbackend.HistoryTransition,
	intent relationBackendStepIntent,
	operations []lifecycleMixedOperation,
) (relationBackendStepIntent, []lifecycleMixedOperation, error) {
	if err := relationBackendValidateResourceShape(intent); err != nil {
		return relationBackendStepIntent{}, nil, fmt.Errorf("%w: %w", lifecycleRelationErrIntent, err)
	}
	pinned := intent.relationBackendClone()
	if err := relationBackendValidateIntent(pinned); err != nil {
		return relationBackendStepIntent{}, nil, fmt.Errorf("%w: %w", lifecycleRelationErrIntent, err)
	}
	if err := lifecycleValidateTransition(transition, pinned); err != nil {
		return relationBackendStepIntent{}, nil, err
	}
	if len(operations) > profileMaxOperations {
		return relationBackendStepIntent{}, nil, fmt.Errorf(
			"%w: mixed operation resource limit exceeded: %d > %d",
			lifecycleRelationErrIntent,
			len(operations),
			profileMaxOperations,
		)
	}
	resourceBudget := lifecycleScalarResourceBudget{}
	for index, operation := range operations {
		if err := lifecycleConsumeScalarResourceShape(&resourceBudget, operation.Model, operation.Field); err != nil {
			return relationBackendStepIntent{}, nil, fmt.Errorf(
				"%w: mixed operation %d scalar resource limit: %w",
				lifecycleRelationErrIntent,
				index,
				err,
			)
		}
	}

	prepared := make([]lifecycleMixedOperation, len(operations))
	nextRelation := 0
	for index, operation := range operations {
		switch operation.Kind {
		case lifecycleMixedScalarAdd:
			if len(operation.Model.Fields) >= profileMaxFields {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed scalar operation %d field resource limit exceeded: %d >= %d",
					lifecycleRelationErrIntent,
					index,
					len(operation.Model.Fields),
					profileMaxFields,
				)
			}
			if !reflect.DeepEqual(operation.Relation, relationBackendChange{}) {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed scalar operation %d carries a relation arm",
					lifecycleRelationErrIntent,
					index,
				)
			}
			if operation.Field.Kind == ir.FieldForeignKey || operation.Field.Relation != nil {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed scalar operation %d carries a relation field",
					lifecycleRelationErrIntent,
					index,
				)
			}
			after := operation.Model.Clone()
			after.Fields = append(after.Fields, operation.Field.Clone())
			normalized, err := ir.Normalize(ir.Schema{
				FormatVersion: ir.FormatVersion,
				AppLabel:      "candidate",
				Models:        []ir.Model{after},
			})
			if err != nil || len(normalized.Models) != 1 || !reflect.DeepEqual(normalized.Models[0], after) {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed scalar operation %d is not normalized: %v",
					lifecycleRelationErrIntent,
					index,
					err,
				)
			}
		case lifecycleMixedRelationChange:
			if !reflect.DeepEqual(operation.Model, ir.Model{}) || !reflect.DeepEqual(operation.Field, ir.Field{}) {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed relation operation %d carries a scalar arm",
					lifecycleRelationErrIntent,
					index,
				)
			}
			if nextRelation >= len(pinned.Changes) {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed operation sequence has an extra relation change at %d",
					lifecycleRelationErrIntent,
					index,
				)
			}
			if !relationBackendChangesEqual(operation.Relation, pinned.Changes[nextRelation]) {
				return relationBackendStepIntent{}, nil, fmt.Errorf(
					"%w: mixed relation operation %d is missing, reordered, or mismatched",
					lifecycleRelationErrIntent,
					index,
				)
			}
			nextRelation++
		default:
			return relationBackendStepIntent{}, nil, fmt.Errorf(
				"%w: mixed migration operation %d has invalid kind %d",
				lifecycleRelationErrIntent,
				index,
				operation.Kind,
			)
		}
		prepared[index] = lifecycleMixedOperation{
			Kind:     operation.Kind,
			Model:    operation.Model.Clone(),
			Field:    operation.Field.Clone(),
			Relation: operation.Relation.relationBackendClone(),
		}
	}
	if nextRelation != len(pinned.Changes) {
		return relationBackendStepIntent{}, nil, fmt.Errorf(
			"%w: mixed operation sequence stages %d relation changes, want %d",
			lifecycleRelationErrIntent,
			nextRelation,
			len(pinned.Changes),
		)
	}
	return pinned, prepared, nil
}

type lifecycleScalarResourceBudget struct {
	bytes int
	nodes int
}

// lifecycleConsumeScalarResourceShape bounds every caller-owned scalar arm
// before Model.Clone or ir.Normalize can walk it. One shared budget spans the
// complete mixed operation sequence, matching a single migration document
// instead of incorrectly granting every operation a fresh document allowance.
func lifecycleConsumeScalarResourceShape(
	budget *lifecycleScalarResourceBudget,
	model ir.Model,
	added ir.Field,
) error {
	if budget == nil {
		return errors.New("scalar resource budget is nil")
	}
	fieldNodes := len(model.Fields) + 1
	if fieldNodes > migrationdefinition.MaxJSONValues-budget.nodes {
		return fmt.Errorf("aggregate scalar nodes exceed %d", migrationdefinition.MaxJSONValues)
	}
	budget.nodes += fieldNodes
	consume := func(label, value string, maximum int) error {
		if len(value) > maximum {
			return fmt.Errorf("%s bytes %d exceed %d", label, len(value), maximum)
		}
		if len(value) > migrationdefinition.MaxDocumentBytes-budget.bytes {
			return fmt.Errorf("aggregate scalar bytes exceed %d", migrationdefinition.MaxDocumentBytes)
		}
		budget.bytes += len(value)
		return nil
	}
	consumeField := func(label string, field ir.Field) error {
		for _, value := range []struct {
			label string
			text  string
		}{
			{label + ".name", field.Name},
			{label + ".go_name", field.GoName},
			{label + ".column", field.Column},
		} {
			if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
				return err
			}
		}
		if err := consume(label+".kind", string(field.Kind), migrationdefinition.MaxSourceIDBytes); err != nil {
			return err
		}
		if field.Default != nil {
			if err := consume(label+".default.kind", string(field.Default.Kind), migrationdefinition.MaxSourceIDBytes); err != nil {
				return err
			}
			if err := consume(label+".default.string", field.Default.String, migrationdefinition.MaxDocumentBytes); err != nil {
				return err
			}
		}
		if field.Relation != nil {
			for _, value := range []struct {
				label string
				text  string
			}{
				{label + ".relation.target.app", field.Relation.Target.AppLabel},
				{label + ".relation.target.model", field.Relation.Target.ModelName},
				{label + ".relation.cardinality", string(field.Relation.Cardinality)},
				{label + ".relation.reverse.name", field.Relation.Reverse.Name},
				{label + ".relation.on_delete", string(field.Relation.OnDelete)},
			} {
				if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, value := range []struct {
		label string
		text  string
	}{
		{"model.name", model.Name},
		{"model.go_name", model.GoName},
		{"model.db_table", model.DBTable},
	} {
		if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
			return err
		}
	}
	for index, field := range model.Fields {
		if err := consumeField(fmt.Sprintf("model.fields[%d]", index), field); err != nil {
			return err
		}
	}
	return consumeField("added_field", added)
}

func lifecycleBeginRelationFenced(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	transition migrationbackend.HistoryTransition,
	intent relationBackendStepIntent,
) (lifecycleRelationFencedTransaction, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lifecycleNilInterface(session) {
		return nil, lifecycleRelationErrCapability
	}

	if err := relationBackendValidateResourceShape(intent); err != nil {
		return nil, fmt.Errorf("%w: %w", lifecycleRelationErrIntent, err)
	}
	pinned := intent.relationBackendClone()
	if err := relationBackendValidateIntent(pinned); err != nil {
		return nil, fmt.Errorf("%w: %w", lifecycleRelationErrIntent, err)
	}
	if err := lifecycleValidateTransition(transition, pinned); err != nil {
		return nil, err
	}

	relationSession, ok := session.(lifecycleRelationFencedSession)
	if !ok || lifecycleNilInterface(relationSession) {
		// Do not fall back to BeginFencedMigration: by then a SQLite
		// implementation has already issued BEGIN IMMEDIATE and it never
		// received the relation intent needed for connection-local preflight.
		return nil, lifecycleRelationErrCapability
	}
	transaction, err := relationSession.BeginRelationFencedMigration(ctx, transition, pinned)
	if err != nil {
		if lifecycleNilInterface(transaction) {
			return nil, err
		}
		return nil, lifecycleRollbackMixed(ctx, transaction, err)
	}
	if lifecycleNilInterface(transaction) {
		return nil, errors.New("relation-capable session returned a nil fenced transaction")
	}
	if err := ctx.Err(); err != nil {
		return nil, lifecycleRollbackMixed(ctx, transaction, err)
	}
	return transaction, nil
}

func lifecycleValidateTransition(
	transition migrationbackend.HistoryTransition,
	intent relationBackendStepIntent,
) error {
	if transition.Migration.App != intent.App || transition.Migration.Name != intent.Name {
		return fmt.Errorf("%w: history transition does not match relation intent", lifecycleRelationErrIntent)
	}
	if transition.Kind != migrationbackend.HistoryTransitionApply &&
		transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return fmt.Errorf("%w: history transition kind is invalid", lifecycleRelationErrIntent)
	}
	return nil
}

func lifecycleExecuteMixedStep(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	transition migrationbackend.HistoryTransition,
	intent relationBackendStepIntent,
	operations []lifecycleMixedOperation,
) (lifecycleMixedResult, error) {
	if ctx == nil {
		return lifecycleMixedResult{}, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	pinned, prepared, err := lifecyclePrepareMixedStep(transition, intent, operations)
	if err != nil {
		return lifecycleMixedResult{}, err
	}
	// Session lifetime belongs to the outer lifecycle, which may execute many
	// sequential fenced steps from one atomic history snapshot. This helper owns
	// only the transaction it begins and therefore must not close the session.
	transaction, err := lifecycleBeginRelationFenced(ctx, session, transition, pinned)
	if err != nil {
		return lifecycleMixedResult{}, err
	}

	for _, operation := range prepared {
		if err := ctx.Err(); err != nil {
			return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
		}
		switch operation.Kind {
		case lifecycleMixedScalarAdd:
			err = transaction.AddField(ctx, operation.Model.Clone(), operation.Field.Clone())
		case lifecycleMixedRelationChange:
			err = transaction.ApplyRelationChange(ctx, operation.Relation.relationBackendClone())
		}
		if err != nil {
			return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
		}
	}
	// A context-insensitive backend operation may return nil after canceling the
	// request. Recheck before staging recorder state so the final operation
	// cannot turn cancellation into a durable migration.
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}

	switch transition.Kind {
	case migrationbackend.HistoryTransitionApply:
		err = transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name)
	case migrationbackend.HistoryTransitionUnapply:
		err = transaction.RecordUnapplied(ctx, transition.Migration.App, transition.Migration.Name)
	}
	if err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}
	// The recorder is also an external port. If it returns nil while the request
	// becomes canceled, roll the staged transition back instead of attempting a
	// commit with an already-canceled context.
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}

	outcome, commitErr := transaction.CommitFenced(ctx)
	result := lifecycleMixedResult{Outcome: outcome}
	switch outcome.Durability {
	case migrationbackend.CommitCommitted:
		result.Committed = true
		return result, commitErr
	case migrationbackend.CommitRolledBack:
		if commitErr == nil {
			commitErr = errors.New("relation transaction reported rollback without an error")
		}
		return result, commitErr
	case migrationbackend.CommitUnknown:
		if commitErr == nil {
			commitErr = errors.New("relation transaction reported unknown outcome without an error")
		}
		return result, commitErr
	default:
		return result, fmt.Errorf("relation transaction returned invalid commit durability %d: %w", outcome.Durability, commitErr)
	}
}

func lifecycleRollbackMixed(
	ctx context.Context,
	transaction lifecycleRelationFencedTransaction,
	primary error,
) error {
	cleanupBase := context.Background()
	if ctx != nil {
		cleanupBase = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(cleanupBase, lifecycleRelationCleanupTimeout)
	defer cancel()
	return errors.Join(primary, transaction.Rollback(cleanupCtx))
}

func lifecycleNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// lifecycleScalarOnlySession deliberately implements only the existing port.
// Its compile-time assertion is the source-compatibility proof for legacy
// session implementations.
type lifecycleScalarOnlySession struct {
	beginCalls int
}

var _ migrationbackend.RevisionFencedSession = (*lifecycleScalarOnlySession)(nil)

func (*lifecycleScalarOnlySession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return []migrationbackend.AppliedMigration{}, nil
}

func (session *lifecycleScalarOnlySession) BeginFencedMigration(
	context.Context,
	migrationbackend.HistoryTransition,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.beginCalls++
	return nil, errors.New("legacy begin should not be used for relation intent")
}

func (*lifecycleScalarOnlySession) Close(context.Context) error { return nil }

type lifecycleTraceSession struct {
	mu sync.Mutex

	records []migrationbackend.AppliedMigration
	events  []string

	legacyBeginCalls   int
	relationBeginCalls int
	closeCalls         int

	outcome        migrationbackend.CommitOutcome
	commitErr      error
	unknownDurable bool
	relationErr    error
	cancelRelation context.CancelFunc
	cancelRecorder context.CancelFunc
	cancelBegin    context.CancelFunc
	beginErr       error
	beginHook      func()
	preCommitHook  func(*lifecycleTraceTransaction)

	lastTransaction *lifecycleTraceTransaction
}

var _ lifecycleRelationFencedSession = (*lifecycleTraceSession)(nil)

func (session *lifecycleTraceSession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]migrationbackend.AppliedMigration(nil), session.records...), nil
}

func (session *lifecycleTraceSession) BeginFencedMigration(
	context.Context,
	migrationbackend.HistoryTransition,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.legacyBeginCalls++
	return nil, errors.New("legacy begin unexpectedly selected")
}

func (session *lifecycleTraceSession) BeginRelationFencedMigration(
	_ context.Context,
	transition migrationbackend.HistoryTransition,
	intent relationBackendStepIntent,
) (lifecycleRelationFencedTransaction, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.relationBeginCalls++
	session.events = append(session.events, "begin_relation")
	if session.beginHook != nil {
		session.beginHook()
	}
	transaction := &lifecycleTraceTransaction{
		session: session, transition: transition, intent: intent.relationBackendClone(),
		outcome: session.outcome, commitErr: session.commitErr, unknownDurable: session.unknownDurable,
		relationErr: session.relationErr, cancelRelation: session.cancelRelation,
		cancelRecorder: session.cancelRecorder,
		preCommitHook:  session.preCommitHook,
	}
	if transaction.outcome.Durability == 0 {
		transaction.outcome.Durability = migrationbackend.CommitCommitted
	}
	session.lastTransaction = transaction
	if session.cancelBegin != nil {
		session.cancelBegin()
	}
	return transaction, session.beginErr
}

func (session *lifecycleTraceSession) Close(context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closeCalls++
	session.events = append(session.events, "close")
	return nil
}

func (session *lifecycleTraceSession) appendEvent(event string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.events = append(session.events, event)
}

func (session *lifecycleTraceSession) snapshotEvents() []string {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]string(nil), session.events...)
}

type lifecycleTraceTransaction struct {
	session    *lifecycleTraceSession
	transition migrationbackend.HistoryTransition
	intent     relationBackendStepIntent
	nextChange int

	outcome        migrationbackend.CommitOutcome
	commitErr      error
	unknownDurable bool
	relationErr    error
	cancelRelation context.CancelFunc
	cancelRecorder context.CancelFunc
	preCommitHook  func(*lifecycleTraceTransaction)

	stagedRecords   []migrationbackend.AppliedMigration
	recordStaged    bool
	stagePublished  bool
	stageDiscarded  bool
	lastScalarModel ir.Model
	lastScalarField ir.Field

	recorderCalls int
	commitCalls   int
	rollbackCalls int
	rollbackErr   error
	rollbackCtx   error
	rollbackLimit bool
}

var _ lifecycleRelationFencedTransaction = (*lifecycleTraceTransaction)(nil)

func (transaction *lifecycleTraceTransaction) CreateModel(context.Context, ir.Model) error {
	transaction.session.appendEvent("scalar_create")
	return nil
}

func (transaction *lifecycleTraceTransaction) DeleteModel(context.Context, ir.Model) error {
	transaction.session.appendEvent("scalar_delete")
	return nil
}

func (transaction *lifecycleTraceTransaction) AddField(_ context.Context, model ir.Model, field ir.Field) error {
	transaction.session.appendEvent("scalar_add")
	transaction.lastScalarModel = model.Clone()
	transaction.lastScalarField = field.Clone()
	return nil
}

func (transaction *lifecycleTraceTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	transaction.session.appendEvent("scalar_remove")
	return nil
}

func (transaction *lifecycleTraceTransaction) ApplyRelationChange(
	_ context.Context,
	change relationBackendChange,
) error {
	transaction.session.appendEvent("relation_change")
	if transaction.cancelRelation != nil {
		transaction.cancelRelation()
	}
	if transaction.relationErr != nil {
		return transaction.relationErr
	}
	if transaction.nextChange >= len(transaction.intent.Changes) ||
		!relationBackendChangesEqual(change, transaction.intent.Changes[transaction.nextChange]) {
		return relationBackendErrMismatch
	}
	transaction.nextChange++
	return nil
}

func (transaction *lifecycleTraceTransaction) RecordApplied(_ context.Context, app, name string) error {
	return transaction.record(migrationbackend.HistoryTransitionApply, app, name)
}

func (transaction *lifecycleTraceTransaction) RecordUnapplied(_ context.Context, app, name string) error {
	return transaction.record(migrationbackend.HistoryTransitionUnapply, app, name)
}

func (transaction *lifecycleTraceTransaction) record(
	kind migrationbackend.HistoryTransitionKind,
	app,
	name string,
) error {
	transaction.session.appendEvent("record")
	transaction.recorderCalls++
	if transaction.cancelRecorder != nil {
		transaction.cancelRecorder()
	}
	if transaction.recorderCalls != 1 || transaction.nextChange != len(transaction.intent.Changes) ||
		kind != transaction.transition.Kind || app != transaction.transition.Migration.App ||
		name != transaction.transition.Migration.Name {
		return errors.New("recorder transition does not match the declared fenced transition")
	}
	transaction.session.mu.Lock()
	current := append([]migrationbackend.AppliedMigration(nil), transaction.session.records...)
	transaction.session.mu.Unlock()
	staged, err := lifecycleStageHistoryTransition(current, transaction.transition)
	if err != nil {
		return err
	}
	transaction.stagedRecords = staged
	transaction.recordStaged = true
	return nil
}

func (transaction *lifecycleTraceTransaction) CommitFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.session.appendEvent("commit_fenced")
	transaction.commitCalls++
	if transaction.preCommitHook != nil {
		transaction.preCommitHook(transaction)
	}
	if !transaction.recordStaged {
		return transaction.outcome, errors.Join(
			transaction.commitErr,
			errors.New("commit reached without a staged history transition"),
		)
	}
	// CommitCommitted publishes the staged history even when the returned error
	// is only terminal cleanup. CommitUnknown deliberately has two observations:
	// the same unknown result may hide either a durable or a nondurable outcome.
	publish := transaction.outcome.Durability == migrationbackend.CommitCommitted ||
		(transaction.outcome.Durability == migrationbackend.CommitUnknown && transaction.unknownDurable)
	if publish {
		transaction.session.mu.Lock()
		transaction.session.records = append(
			[]migrationbackend.AppliedMigration(nil),
			transaction.stagedRecords...,
		)
		transaction.session.mu.Unlock()
		transaction.stagePublished = true
	} else {
		transaction.stagedRecords = nil
		transaction.stageDiscarded = true
	}
	return transaction.outcome, transaction.commitErr
}

func (transaction *lifecycleTraceTransaction) Rollback(ctx context.Context) error {
	transaction.session.appendEvent("rollback")
	transaction.rollbackCalls++
	transaction.rollbackCtx = ctx.Err()
	_, transaction.rollbackLimit = ctx.Deadline()
	transaction.stagedRecords = nil
	transaction.stageDiscarded = transaction.recordStaged
	return transaction.rollbackErr
}

func lifecycleStageHistoryTransition(
	records []migrationbackend.AppliedMigration,
	transition migrationbackend.HistoryTransition,
) ([]migrationbackend.AppliedMigration, error) {
	staged := append([]migrationbackend.AppliedMigration(nil), records...)
	switch transition.Kind {
	case migrationbackend.HistoryTransitionApply:
		for _, record := range staged {
			if record == transition.Migration {
				return nil, errors.New("apply transition is already recorded")
			}
		}
		return append(staged, transition.Migration), nil
	case migrationbackend.HistoryTransitionUnapply:
		for index, record := range staged {
			if record != transition.Migration {
				continue
			}
			return append(staged[:index:index], staged[index+1:]...), nil
		}
		return nil, errors.New("unapply transition is not recorded")
	default:
		return nil, errors.New("history transition kind is invalid")
	}
}

func lifecycleMixedScalarModel() ir.Model {
	return ir.Model{
		Name: "article", GoName: "Article", DBTable: "article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
}

func lifecycleNullableEditorIntent() relationBackendStepIntent {
	before := relationBackendModel{
		Table: "article",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 2},
		},
	}
	after := before.relationBackendClone()
	editor := relationBackendRelation{
		Name: "editor", Column: "editor_id", TargetTable: "author", TargetColumn: "id",
		Nullable: true, OnDelete: relationBackendSetNull, Position: 3,
	}
	after.Relations = []relationBackendRelation{editor}
	return relationBackendStepIntent{
		App: "blog", Name: "0002_relation",
		Changes: []relationBackendChange{{
			Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
		}},
	}
}
