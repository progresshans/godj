package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/db/internal/migrationhistory"
	"github.com/progresshans/godj/internal/irresource"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const (
	sqliteRelationMaxOperations     = 2_048
	sqliteRelationMaxFields         = 2_048
	sqliteRelationMaxTargets        = 2_048
	sqliteRelationMaxStringBytes    = 1 << 20
	sqliteRelationMaxAggregateBytes = 16 << 20
	sqliteRelationMaxNodes          = 262_144
)

var (
	errSQLiteRelationForeignKeysOff = errors.New("SQLite foreign key enforcement is not enabled on the pinned migration connection")
	errSQLiteRelationPhysicalDrift  = errors.New("declared relation model differs from the physical SQLite schema")
	errSQLiteRelationForeignKey     = errors.New("SQLite foreign_key_check reported a violation")
)

// MigrationCapabilities advertises only the bounded relation DDL that
// this slice implements. Required Add is bounded to empty tables and relation
// Remove is bounded to the sealed table-remake path.
func (*Backend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return migrationbackend.MigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKey:                  true,
		AlterFieldChoices:                 true,
	}
}

type sqliteRelationIntentSeal struct {
	intent    migrationbackend.MigrationIntent
	digest    [sha256.Size]byte
	graphPlan migrationbackend.MigrationGraphPlan
}

type sqliteRelationFencedState struct {
	seal          sqliteRelationIntentSeal
	remakes       map[int]sqliteRelationRemakePlan
	remakeDigest  [sha256.Size]byte
	cursor        int
	finalVerified bool
}

type sqliteRelationSchemaObject struct {
	schema   string
	kind     string
	name     string
	owner    string
	sql      string
	nameKey  string
	ownerKey string
	tokens   []sqliteRelationSQLToken
}

type sqliteRelationSQLToken struct {
	value   string
	quoted  bool
	literal bool
}

type sqliteRelationCatalog struct {
	objects      []sqliteRelationSchemaObject
	byName       map[sqliteRelationCatalogNameKey][]int
	byObjectKind map[sqliteRelationCatalogObjectKey][]int
	sequences    map[string]sqliteRelationSequenceSnapshot
}

type sqliteRelationCatalogNameKey struct {
	schema string
	name   string
}

type sqliteRelationCatalogObjectKey struct {
	schema string
	name   string
	kind   string
}

type sqliteRelationBeginCheckpoint uint8

const (
	sqliteRelationCheckpointForeignKeysSet sqliteRelationBeginCheckpoint = iota + 1
	sqliteRelationCheckpointForeignKeysRead
	sqliteRelationCheckpointTransactionBegun
	sqliteRelationCheckpointPhysicalPreflightComplete
	sqliteRelationCheckpointRevisionClaimStarting
	sqliteRelationCheckpointRevisionClaimed
	sqliteRelationCheckpointForeignKeysSuspended
)

func (session *sqliteRevisionFencedSession) notifyRelationBeginCheckpoint(checkpoint sqliteRelationBeginCheckpoint) {
	if session.relationBeginCheckpoint != nil {
		session.relationBeginCheckpoint(checkpoint)
	}
}

func (session *sqliteRevisionFencedSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	if intent.Operations == nil {
		return nil, relationIntentIntegrity("migration intent operations are missing")
	}
	if session == nil {
		return nil, errors.New("begin SQLite relation revision-fenced migration: session is nil")
	}
	if ctx == nil {
		return nil, errors.New("begin SQLite relation revision-fenced migration: context is nil")
	}
	if err := session.backend.validateBackendContext(ctx); err != nil {
		if shouldPoisonRevisionSessionForBackendEntryError(err) {
			session.mu.Lock()
			if session.state == revisionSessionReady {
				session.state = revisionSessionPoisoned
			}
			session.mu.Unlock()
		}
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err)
	}
	if transition.Migration.App == "" || transition.Migration.Name == "" {
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("history transition requires a non-empty app and migration name"),
		)
	}
	if transition.Kind != migrationbackend.HistoryTransitionApply && transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("history transition kind %d is invalid", transition.Kind),
		)
	}

	seal, err := validateAndSealSQLiteRelationIntent(transition, intent)
	if err != nil {
		return nil, err
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != revisionSessionReady {
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: session state %d is not ready", session.state)
	}
	if err := session.backend.validateBackendContext(ctx); err != nil {
		if shouldPoisonRevisionSessionForBackendEntryError(err) {
			session.state = revisionSessionPoisoned
		}
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err)
	}

	successorRecords, err := migrationHistorySuccessor(session.records, transition)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, err
	}
	successorToken := session.token
	successorToken.initialized = true
	successorToken.fingerprint = migrationhistory.Fingerprint(successorRecords)
	if session.token.initialized {
		if session.token.revision == math.MaxInt64 {
			session.state = revisionSessionPoisoned
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("SQLite migration revision is exhausted"),
			)
		}
		successorToken.revision = session.token.revision + 1
	} else {
		if transition.Kind != migrationbackend.HistoryTransitionApply {
			session.state = revisionSessionPoisoned
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("an uninitialized history cannot begin with an unapply transition"),
			)
		}
		if _, err := rand.Read(successorToken.epoch[:]); err != nil {
			session.state = revisionSessionPoisoned
			return nil, fmt.Errorf("generate SQLite migration revision epoch: %w", err)
		}
		successorToken.revision = 1
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err)
	}

	suspendForeignKeys := sqliteMigrationSuspendsForeignKeys(seal.intent)
	var admission *relationTransactionAdmission
	if suspendForeignKeys {
		admission, err = session.backend.relationRetention.acquire(ctx)
		if err != nil {
			session.state = revisionSessionPoisoned
			return nil, err
		}
	}
	connection, err := session.backend.database.Conn(ctx)
	if err != nil {
		admission.release()
		session.state = revisionSessionPoisoned
		return nil, classifyRevisionIO("acquire pinned relation migration connection", err)
	}
	var connectionBoundary migrationPinnedConnection = connection
	if session.relationConnectionHook != nil {
		connectionBoundary = session.relationConnectionHook(connectionBoundary)
		if connectionBoundary == nil {
			_ = connection.Close()
			admission.release()
			session.state = revisionSessionPoisoned
			return nil, errors.New("begin SQLite relation revision-fenced migration: relation connection hook returned nil")
		}
	}
	restoreForeignKeys := false
	closeBeforeBegin := func(primary error) error {
		return errors.Join(primary, releaseFencedMigrationConnection(ctx, connectionBoundary, restoreForeignKeys, admission))
	}
	if _, err := connectionBoundary.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		session.state = revisionSessionPoisoned
		return nil, closeBeforeBegin(classifyRevisionIO("enable pinned SQLite foreign keys", err))
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointForeignKeysSet)
	var foreignKeys int
	if err := connectionBoundary.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		session.state = revisionSessionPoisoned
		return nil, closeBeforeBegin(classifyRevisionIO("read pinned SQLite foreign keys", err))
	}
	if foreignKeys != 1 {
		session.state = revisionSessionPoisoned
		return nil, closeBeforeBegin(sqliteRelationForeignKeysCapabilityError(foreignKeys))
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointForeignKeysRead)
	if suspendForeignKeys {
		// This connection is private and admitted to retention before the
		// first mode change. Any uncertain change is discarded or retained;
		// it must never be returned to the application pool with checks off.
		restoreForeignKeys = true
		if _, err := connectionBoundary.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			session.state = revisionSessionPoisoned
			return nil, errors.Join(fmt.Errorf("suspend pinned SQLite foreign keys: %w", err), discardOrRetainMigrationConnection(connectionBoundary, admission))
		}
		var readback int
		if err := connectionBoundary.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&readback); err != nil {
			session.state = revisionSessionPoisoned
			return nil, errors.Join(fmt.Errorf("read suspended SQLite foreign keys: %w", err), discardOrRetainMigrationConnection(connectionBoundary, admission))
		}
		if readback != 0 {
			session.state = revisionSessionPoisoned
			return nil, errors.Join(relationIntentUnsupported("SQLite foreign keys cannot be suspended outside the migration transaction"), discardOrRetainMigrationConnection(connectionBoundary, admission))
		}
		session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointForeignKeysSuspended)
	}
	if err := ctx.Err(); err != nil {
		session.state = revisionSessionPoisoned
		return nil, closeBeforeBegin(fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err))
	}
	if _, err := connectionBoundary.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		discardErr := discardOrRetainMigrationConnection(connectionBoundary, admission)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(classifyRevisionIO("begin immediate relation migration transaction", err), discardErr)
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointTransactionBegun)

	transaction := &sqliteRevisionFencedTransaction{
		connection:         connectionBoundary,
		restoreForeignKeys: restoreForeignKeys,
		admission:          admission,
		session:            session,
		transition:         transition,
		expectedRecords:    migrationhistory.Clone(session.records),
		successorRecords:   migrationhistory.Clone(successorRecords),
		expectedToken:      session.token,
		successorToken:     successorToken,
		bootstrap:          !session.token.initialized,
		relation:           &sqliteRelationFencedState{seal: seal},
	}
	remakes, remakeDigest, err := preflightSQLiteRelationIntent(
		ctx,
		transaction.connection,
		transition,
		&transaction.relation.seal,
	)
	if err != nil {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(err, cleanupErr)
	}
	transaction.relation.remakes = remakes
	transaction.relation.remakeDigest = remakeDigest
	if len(transaction.relation.seal.intent.Operations) == 0 {
		if err := verifySQLiteRelationFinalState(ctx, transaction.connection, &transaction.relation.seal); err != nil {
			cleanupErr := transaction.rollbackWithoutSession(ctx)
			session.state = revisionSessionPoisoned
			return nil, errors.Join(err, cleanupErr)
		}
		transaction.relation.finalVerified = true
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointPhysicalPreflightComplete)
	if err := ctx.Err(); err != nil {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err), cleanupErr)
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointRevisionClaimStarting)
	if err := transaction.claimRevision(ctx); err != nil {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(err, cleanupErr)
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointRevisionClaimed)
	if err := ctx.Err(); err != nil {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err), cleanupErr)
	}
	session.active = transaction
	session.state = revisionSessionActive
	return transaction, nil
}

func validateAndSealSQLiteRelationIntent(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (sqliteRelationIntentSeal, error) {
	if err := scanSQLiteRelationIntentResources(transition, intent); err != nil {
		return sqliteRelationIntentSeal{}, err
	}
	if err := validateSQLiteRelationZeroSentinels(transition, intent); err != nil {
		return sqliteRelationIntentSeal{}, err
	}
	pinned := intent.Clone()
	graphPlan, err := migrationbackend.ResolveMigrationGraphPlan(transition, pinned)
	if err != nil {
		return sqliteRelationIntentSeal{}, relationIntentIntegrity("%v", err)
	}
	if err := validateSQLiteRelationIntent(transition, pinned, graphPlan); err != nil {
		return sqliteRelationIntentSeal{}, err
	}
	digest, err := hashSQLiteRelationIntent(pinned)
	if err != nil {
		return sqliteRelationIntentSeal{}, relationIntentIntegrity("seal relation migration intent: %v", err)
	}
	return sqliteRelationIntentSeal{
		intent:    pinned,
		digest:    digest,
		graphPlan: graphPlan,
	}, nil
}

func scanSQLiteRelationIntentResources(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = relationIntentIntegrity("%v", resultErr)
		}
	}()
	if len(intent.Operations) > sqliteRelationMaxOperations {
		return fmt.Errorf("relation intent has %d operations, maximum %d", len(intent.Operations), sqliteRelationMaxOperations)
	}
	budget := irresource.New(irresource.Limits{Fields: sqliteRelationMaxFields, StringBytes: sqliteRelationMaxStringBytes, Nodes: sqliteRelationMaxNodes, Bytes: sqliteRelationMaxAggregateBytes})
	if err := budget.ConsumeNodes("transition", 1); err != nil {
		return err
	}
	if err := budget.ConsumeString("transition.migration.app", transition.Migration.App); err != nil {
		return err
	}
	if err := budget.ConsumeString("transition.migration.name", transition.Migration.Name); err != nil {
		return err
	}
	if err := budget.ConsumeNodes("operations", len(intent.Operations)); err != nil {
		return err
	}
	for operationIndex := range intent.Operations {
		operation := intent.Operations[operationIndex]
		prefix := fmt.Sprintf("operations[%d]", operationIndex)
		if err := budget.ScanModel(prefix+".before", operation.Before); err != nil {
			return err
		}
		if err := budget.ScanModel(prefix+".after", operation.After); err != nil {
			return err
		}
		if len(operation.Targets) > sqliteRelationMaxTargets {
			return fmt.Errorf("%s has %d targets, maximum %d", prefix, len(operation.Targets), sqliteRelationMaxTargets)
		}
		if err := budget.ConsumeNodes(prefix+".targets", len(operation.Targets)); err != nil {
			return err
		}
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			targetPrefix := fmt.Sprintf("%s.targets[%d]", prefix, targetIndex)
			if err := budget.ScanField(targetPrefix+".source_field", target.SourceField); err != nil {
				return err
			}
			if err := budget.ScanModel(targetPrefix+".target_model", target.TargetModel); err != nil {
				return err
			}
			if err := budget.ScanField(targetPrefix+".target_key", target.TargetKey); err != nil {
				return err
			}
		}
		if len(operation.RelatedModels) > sqliteRelationMaxTargets {
			return fmt.Errorf("%s has too many transitive relation models", prefix)
		}
		if err := budget.ConsumeNodes(prefix+".related_models", len(operation.RelatedModels)); err != nil {
			return err
		}
		for index, model := range operation.RelatedModels {
			path := fmt.Sprintf("%s.related_models[%d]", prefix, index)
			if err := budget.ConsumeString(path+".app", model.AppLabel); err != nil {
				return err
			}
			if err := budget.ScanModel(path+".model", model.Model); err != nil {
				return err
			}
		}
	}
	return nil
}

func hashSQLiteRelationIntent(intent migrationbackend.MigrationIntent) ([sha256.Size]byte, error) {
	hash := sha256.New()
	writeRelationSliceHeader(hash, intent.Operations == nil, len(intent.Operations))
	for operationIndex := range intent.Operations {
		operation := intent.Operations[operationIndex]
		writeRelationInt(hash, operation.OperationIndex)
		writeRelationInt(hash, int(operation.Kind))
		writeRelationModel(hash, operation.Before)
		writeRelationModel(hash, operation.After)
		writeRelationSliceHeader(hash, operation.Targets == nil, len(operation.Targets))
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			writeRelationField(hash, target.SourceField)
			writeRelationModel(hash, target.TargetModel)
			writeRelationField(hash, target.TargetKey)
		}
		writeRelationSliceHeader(hash, operation.RelatedModels == nil, len(operation.RelatedModels))
		for _, model := range operation.RelatedModels {
			writeRelationString(hash, model.AppLabel)
			writeRelationModel(hash, model.Model)
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

type sqliteRelationHashWriter interface {
	Write([]byte) (int, error)
}

func writeRelationModel(hash sqliteRelationHashWriter, model ir.Model) {
	writeRelationString(hash, model.Name)
	writeRelationString(hash, model.GoName)
	writeRelationString(hash, model.DBTable)
	writeRelationSliceHeader(hash, model.Fields == nil, len(model.Fields))
	for index := range model.Fields {
		writeRelationField(hash, model.Fields[index])
	}
}

func writeRelationField(hash sqliteRelationHashWriter, field ir.Field) {
	writeRelationString(hash, field.Name)
	writeRelationString(hash, field.GoName)
	writeRelationString(hash, field.Column)
	writeRelationString(hash, string(field.Kind))
	writeRelationBool(hash, field.PrimaryKey)
	writeRelationBool(hash, field.Nullable)
	writeRelationInt(hash, field.MaxLength)
	if field.Decimal != nil {
		writeRelationString(hash, "decimal")
		writeRelationInt(hash, field.Decimal.MaxDigits)
		writeRelationInt(hash, field.Decimal.DecimalPlaces)
	}
	writeRelationBool(hash, field.Default != nil)
	if field.Default != nil {
		writeRelationScalar(hash, *field.Default)
	}
	writeRelationSliceHeader(hash, field.Choices == nil, len(field.Choices))
	for _, choice := range field.Choices {
		writeRelationScalar(hash, choice.Value)
		writeRelationString(hash, choice.Label)
	}
	writeRelationBool(hash, field.Relation != nil)
	if field.Relation != nil {
		writeRelationString(hash, field.Relation.Target.AppLabel)
		writeRelationString(hash, field.Relation.Target.ModelName)
		writeRelationString(hash, string(field.Relation.Cardinality))
		writeRelationString(hash, field.Relation.Reverse.Name)
		writeRelationBool(hash, field.Relation.Reverse.Disabled)
		writeRelationString(hash, string(field.Relation.OnDelete))
	}
}

func writeRelationScalar(hash sqliteRelationHashWriter, scalar ir.Scalar) {
	writeRelationString(hash, string(scalar.Kind))
	writeRelationString(hash, scalar.String)
	writeRelationString(hash, scalar.DateTime)
	if scalar.Kind == ir.ScalarDecimal {
		writeRelationString(hash, scalar.Decimal)
	}
	if scalar.Kind == ir.ScalarFloat {
		writeRelationString(hash, scalar.FloatBits)
	}
	if scalar.Kind == ir.ScalarDuration {
		writeRelationString(hash, scalar.Duration)
	}
	if scalar.Kind == ir.ScalarTime {
		writeRelationString(hash, scalar.Time)
	}
	if scalar.Kind == ir.ScalarDate {
		writeRelationString(hash, scalar.Date)
	}
	writeRelationBool(hash, scalar.Boolean)
	writeRelationInt64(hash, scalar.Integer)
}

func writeRelationSliceHeader(hash sqliteRelationHashWriter, nilSlice bool, length int) {
	writeRelationBool(hash, nilSlice)
	writeRelationInt(hash, length)
}

func writeRelationString(hash sqliteRelationHashWriter, value string) {
	writeRelationInt(hash, len(value))
	_, _ = hash.Write([]byte(value))
}

func writeRelationBool(hash sqliteRelationHashWriter, value bool) {
	byteValue := byte(0)
	if value {
		byteValue = 1
	}
	_, _ = hash.Write([]byte{byteValue})
}

func writeRelationInt(hash sqliteRelationHashWriter, value int) {
	writeRelationInt64(hash, int64(value))
}

func writeRelationInt64(hash sqliteRelationHashWriter, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = hash.Write(encoded[:])
}

func validateSQLiteRelationZeroSentinels(
	_ migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) error {
	for position := range intent.Operations {
		operation := intent.Operations[position]
		if operation.Kind == migrationbackend.MigrationCreateModel &&
			!reflect.DeepEqual(operation.Before, ir.Model{}) {
			return relationIntentIntegrity("relation CreateModel operation %d has non-zero Before model", operation.OperationIndex)
		}
		if operation.Kind == migrationbackend.MigrationDeleteModel &&
			!reflect.DeepEqual(operation.After, ir.Model{}) {
			return relationIntentIntegrity("relation DeleteModel operation %d has non-zero After model", operation.OperationIndex)
		}
	}
	return nil
}

func validateSQLiteRelationIntent(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
	graphPlan migrationbackend.MigrationGraphPlan,
) error {
	// SQLite folds identifier spelling. The shared historical graph keeps
	// semantic app/model identities exact; this backend additionally rejects
	// physical table/column aliases and reverse-name collisions.
	tableOwners := make(map[string]ir.ModelIdentity)
	modelNames := make(map[struct{ app, name string }]ir.ModelIdentity)
	reverseOwners := make(map[ir.ModelIdentity]map[string]struct {
		source ir.ModelIdentity
		field  string
	})
	checkModel := func(snapshot migrationbackend.MigrationModel) error {
		identity := snapshot.Identity()
		model := snapshot.Model
		if relationReservedTable(model.DBTable) {
			return relationIntentIntegrity("relation model %s.%s uses a reserved SQLite table", identity.AppLabel, identity.ModelName)
		}
		tableKey := sqliteRelationIdentifierKey(model.DBTable)
		if owner, exists := tableOwners[tableKey]; exists && owner != identity {
			return relationIntentIntegrity("different relation models share SQLite table %q", model.DBTable)
		}
		tableOwners[tableKey] = identity
		modelKey := struct{ app, name string }{identity.AppLabel, sqliteRelationIdentifierKey(identity.ModelName)}
		if owner, exists := modelNames[modelKey]; exists && owner != identity {
			return relationIntentIntegrity("relation model names collide under SQLite identifier folding")
		}
		modelNames[modelKey] = identity
		columns := make(map[string]bool, len(model.Fields))
		for _, field := range model.Fields {
			column := sqliteRelationIdentifierKey(field.Column)
			if columns[column] {
				return relationIntentIntegrity("relation model %q repeats SQLite column %q", model.Name, field.Column)
			}
			columns[column] = true
			if field.Relation == nil || field.Relation.Reverse.Name == "" {
				continue
			}
			owners := reverseOwners[field.Relation.Target]
			if owners == nil {
				owners = make(map[string]struct {
					source ir.ModelIdentity
					field  string
				})
				reverseOwners[field.Relation.Target] = owners
			}
			name := sqliteRelationIdentifierKey(field.Relation.Reverse.Name)
			owner := struct {
				source ir.ModelIdentity
				field  string
			}{identity, field.Name}
			if previous, exists := owners[name]; exists && previous != owner {
				return relationIntentIntegrity("relation reverse name %q has multiple owners", field.Relation.Reverse.Name)
			}
			owners[name] = owner
		}
		return nil
	}
	for position, operation := range intent.Operations {
		if transition.Kind == migrationbackend.HistoryTransitionApply &&
			(operation.Kind == migrationbackend.MigrationDeleteModel || operation.Kind == migrationbackend.MigrationRemoveField) ||
			transition.Kind == migrationbackend.HistoryTransitionUnapply &&
				(operation.Kind == migrationbackend.MigrationCreateModel || operation.Kind == migrationbackend.MigrationAddField) {
			return relationIntentIntegrity("relation operation %d kind does not match history transition", operation.OperationIndex)
		}
		before, after := operation.Before, operation.After
		switch operation.Kind {
		case migrationbackend.MigrationCreateModel:
			if !reflect.DeepEqual(before, ir.Model{}) {
				return relationIntentIntegrity("CreateModel has a non-zero Before model")
			}
		case migrationbackend.MigrationDeleteModel:
			if !reflect.DeepEqual(after, ir.Model{}) {
				return relationIntentIntegrity("DeleteModel has a non-zero After model")
			}
		case migrationbackend.MigrationAddField:
			if _, err := validateSQLiteRelationAddDelta(before, after); err != nil {
				return relationIntentIntegrity("relation AddField operation %d: %v", operation.OperationIndex, err)
			}
		case migrationbackend.MigrationRemoveField:
			if _, err := validateSQLiteRelationRemoveDelta(before, after); err != nil {
				return relationIntentIntegrity("relation RemoveField operation %d: %v", operation.OperationIndex, err)
			}
		case migrationbackend.MigrationAlterField:
			if err := validateSQLiteChoiceDelta(before, after); err != nil {
				return relationIntentIntegrity("AlterField operation %d has an invalid choices delta: %v", operation.OperationIndex, err)
			}
		default:
			return relationIntentIntegrity("relation operation %d has invalid kind %d", operation.OperationIndex, operation.Kind)
		}
		graph, exists := graphPlan.Operation(position)
		if !exists {
			return relationIntentIntegrity("relation operation has no sealed graph")
		}
		for _, snapshot := range graph.Models() {
			if err := checkModel(snapshot); err != nil {
				return err
			}
		}
		if err := validateSQLiteRelationStaticOperation(operation, before, after); err != nil {
			return err
		}
	}
	for _, snapshot := range append(graphPlan.InitialModels(), graphPlan.FinalModels()...) {
		if err := checkModel(snapshot); err != nil {
			return err
		}
		owners := reverseOwners[snapshot.Identity()]
		for _, field := range snapshot.Model.Fields {
			if _, exists := owners[sqliteRelationIdentifierKey(field.Name)]; exists {
				return relationIntentIntegrity("field %s.%s.%s collides with a relation reverse name", snapshot.AppLabel, snapshot.Model.Name, field.Name)
			}
		}
	}
	return nil
}

func validateSQLiteRelationStaticOperation(
	operation migrationbackend.MigrationOperation,
	before,
	after ir.Model,
) error {
	switch operation.Kind {
	case migrationbackend.MigrationCreateModel:
		if _, err := compileSQLiteRelationCreateModel(after, operation.Targets); err != nil {
			return relationIntentUnsupported("relation CreateModel operation %d cannot compile safely: %v", operation.OperationIndex, err)
		}
	case migrationbackend.MigrationDeleteModel:
		if _, err := compileMigrationDeleteModel(before); err != nil {
			return relationIntentUnsupported("relation-step DeleteModel operation %d cannot compile safely: %v", operation.OperationIndex, err)
		}
	case migrationbackend.MigrationAddField:
		field, err := operation.ChangedField()
		if err != nil {
			return relationIntentIntegrity("invalid AddField delta: %v", err)
		}
		if field.Kind == ir.FieldForeignKey {
			if _, err := compileSQLiteRelationAddField(before, field, operation.Targets); err != nil {
				return relationIntentUnsupported("relation AddField operation %d cannot compile safely: %v", operation.OperationIndex, err)
			}
		} else {
			if field.PrimaryKey {
				return relationIntentUnsupported("relation-step AddField operation %d must be non-primary-key", operation.OperationIndex)
			}
			if _, err := compileMigrationAddField(before, field); err != nil {
				return relationIntentUnsupported("relation-step AddField operation %d cannot compile safely: %v", operation.OperationIndex, err)
			}
		}
	case migrationbackend.MigrationRemoveField:
		field, err := operation.ChangedField()
		if err != nil {
			return relationIntentIntegrity("invalid RemoveField delta: %v", err)
		}
		if field.PrimaryKey {
			return relationIntentUnsupported("relation-step RemoveField operation %d must be non-primary-key", operation.OperationIndex)
		}
		if field.Kind == ir.FieldForeignKey {
			if len(operation.Targets) == 0 {
				return relationIntentIntegrity("relation RemoveField operation %d lacks target metadata", operation.OperationIndex)
			}
			retainedTargets, err := sqliteRelationTargetsForFields(after, operation.Targets)
			if err != nil {
				return err
			}
			if _, err := compileSQLiteRelationCreateModel(after, retainedTargets); err != nil {
				return relationIntentUnsupported("relation RemoveField operation %d cannot compile bounded remake: %v", operation.OperationIndex, err)
			}
			break
		}
		if _, err := compileMigrationRemoveField(before, field); err != nil {
			return relationIntentUnsupported("relation-step RemoveField operation %d cannot compile safely: %v", operation.OperationIndex, err)
		}
	}
	return nil
}

func validateSQLiteRelationAddDelta(before, after ir.Model) (ir.Field, error) {
	if err := validateExactNormalizedRelationModel(before); err != nil {
		return ir.Field{}, fmt.Errorf("Before model is not exact normalized IR: %w", err)
	}
	if err := validateExactNormalizedRelationModel(after); err != nil {
		return ir.Field{}, fmt.Errorf("After model is not exact normalized IR: %w", err)
	}
	return (migrationbackend.MigrationOperation{Kind: migrationbackend.MigrationAddField, Before: before, After: after}).ChangedField()
}

func validateSQLiteRelationRemoveDelta(before, after ir.Model) (ir.Field, error) {
	if err := validateExactNormalizedRelationModel(before); err != nil {
		return ir.Field{}, fmt.Errorf("Before model is not exact normalized IR: %w", err)
	}
	if err := validateExactNormalizedRelationModel(after); err != nil {
		return ir.Field{}, fmt.Errorf("After model is not exact normalized IR: %w", err)
	}
	return (migrationbackend.MigrationOperation{Kind: migrationbackend.MigrationRemoveField, Before: before, After: after}).ChangedField()
}

func validateExactNormalizedRelationModel(model ir.Model) error {
	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_relation_intent",
		Models:        []ir.Model{model.Clone()},
	})
	if err != nil {
		return err
	}
	if len(normalized.Models) != 1 || !reflect.DeepEqual(normalized.Models[0], model) {
		return errors.New("normalization changes the model snapshot")
	}
	return nil
}

func relationFieldsInModel(model ir.Model) []ir.Field {
	fields := make([]ir.Field, 0)
	for index := range model.Fields {
		if model.Fields[index].Kind == ir.FieldForeignKey {
			fields = append(fields, model.Fields[index])
		}
	}
	return fields
}

func exactRelationTargetPrimaryKey(model ir.Model) (ir.Field, error) {
	var primaryKey ir.Field
	count := 0
	for index := range model.Fields {
		field := model.Fields[index]
		if field.PrimaryKey {
			count++
			primaryKey = field
		}
	}
	if count != 1 || primaryKey.Kind != ir.FieldAuto || primaryKey.Nullable {
		return ir.Field{}, fmt.Errorf("target model %q requires exactly one non-nullable AutoField primary key", model.Name)
	}
	return primaryKey, nil
}

func relationReservedTable(table string) bool {
	key := sqliteRelationIdentifierKey(table)
	return key == sqliteRelationIdentifierKey(migrationRevisionTable) ||
		key == sqliteRelationIdentifierKey(migrationRecorderTable) ||
		strings.HasPrefix(key, "sqlite_")
}

func sqliteRelationIdentifierKey(identifier string) string {
	return sqliteIdentifierKey(identifier)
}

func relationIntentUnsupported(format string, arguments ...any) error {
	return migrationbackend.NewCapabilityError("sqlite_relation_migration", fmt.Sprintf(format, arguments...), nil)
}

func sqliteRelationForeignKeysCapabilityError(readback int) error {
	return migrationbackend.NewCapabilityError(
		"sqlite_relation_migration",
		fmt.Sprintf("%v: readback=%d", errSQLiteRelationForeignKeysOff, readback),
		nil,
	)
}

func relationIntentIntegrity(format string, arguments ...any) error {
	return fmt.Errorf("invalid SQLite relation migration intent: "+format, arguments...)
}

func (transaction *sqliteRevisionFencedTransaction) executeRelationCreateModel(ctx context.Context, model ir.Model) error {
	return transaction.execute(ctx, "create relation model", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected relation CreateModel after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationCreateModel || !reflect.DeepEqual(model, operation.After) {
			return relationIntentIntegrity("relation CreateModel does not match sealed operation at cursor %d", state.cursor)
		}
		statement, err := compileSQLiteRelationCreateModel(operation.After, operation.Targets)
		if err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create SQLite relation model %q: %w", operation.After.DBTable, err)
		}
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *sqliteRevisionFencedTransaction) executeRelationDeleteModel(ctx context.Context, model ir.Model) error {
	return transaction.execute(ctx, "delete relation model", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected relation DeleteModel after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationDeleteModel || !reflect.DeepEqual(model, operation.Before) {
			return relationIntentIntegrity("relation DeleteModel does not match sealed operation at cursor %d", state.cursor)
		}
		statement, err := compileMigrationDeleteModel(operation.Before)
		if err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("delete SQLite relation model %q: %w", operation.Before.DBTable, err)
		}
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *sqliteRevisionFencedTransaction) executeRelationAddField(
	ctx context.Context,
	model ir.Model,
	field ir.Field,
) error {
	return transaction.execute(ctx, "add field in relation step", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected relation-step AddField after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationAddField || len(operation.After.Fields) == 0 {
			return relationIntentIntegrity("relation-step AddField does not match sealed operation kind at cursor %d", state.cursor)
		}
		wantField, deltaErr := operation.ChangedField()
		if deltaErr != nil {
			return relationIntentIntegrity("invalid sealed AddField delta: %v", deltaErr)
		}
		if !reflect.DeepEqual(model, operation.Before) || !reflect.DeepEqual(field, wantField) {
			return relationIntentIntegrity("relation-step AddField does not match sealed model/field at cursor %d", state.cursor)
		}
		var statement string
		var err error
		if field.Kind == ir.FieldForeignKey {
			statement, err = compileSQLiteRelationAddField(operation.Before, wantField, operation.Targets)
		} else {
			if field.PrimaryKey {
				return relationIntentUnsupported("SQLite relation-step AddField must be non-primary-key")
			}
			statement, err = compileMigrationAddField(operation.Before, wantField)
		}
		if err != nil {
			return err
		}
		if field.Kind != ir.FieldForeignKey && (field.Default != nil || !field.Nullable) {
			empty, err := sqliteTableEmpty(ctx, executor, operation.Before.DBTable)
			if err != nil {
				return err
			}
			if !empty && field.Default != nil {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding field %s with a migration default requires one-time backfill or table rebuild", operation.Before.DBTable, field.Column),
					nil,
				)
			}
			if !empty {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding non-null field %s requires table rebuild", operation.Before.DBTable, field.Column),
					nil,
				)
			}
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add SQLite field %s.%s in relation step: %w", operation.Before.DBTable, field.Column, err)
		}
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *sqliteRevisionFencedTransaction) executeRelationRemoveField(
	ctx context.Context,
	model ir.Model,
	field ir.Field,
) error {
	return transaction.execute(ctx, "remove field in relation step", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected relation-step RemoveField after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationRemoveField || len(operation.Before.Fields) == 0 {
			return relationIntentIntegrity("relation-step RemoveField does not match sealed operation kind at cursor %d", state.cursor)
		}
		wantField, deltaErr := operation.ChangedField()
		if deltaErr != nil {
			return relationIntentIntegrity("invalid sealed RemoveField delta: %v", deltaErr)
		}
		if !reflect.DeepEqual(model, operation.Before) || !reflect.DeepEqual(field, wantField) {
			return relationIntentIntegrity("relation-step RemoveField does not match sealed model/field at cursor %d", state.cursor)
		}
		if field.PrimaryKey {
			return relationIntentUnsupported("SQLite relation-step RemoveField must be non-primary-key")
		}
		if field.Kind == ir.FieldForeignKey {
			if err := verifySQLiteRelationRemakePlans(state.remakes, state.remakeDigest); err != nil {
				return err
			}
			plan, exists := state.remakes[operation.OperationIndex]
			if !exists || plan.operationIndex != operation.OperationIndex ||
				!reflect.DeepEqual(plan.before, operation.Before) ||
				!reflect.DeepEqual(plan.after, operation.After) {
				return relationIntentIntegrity(
					"relation-step RemoveField lacks its sealed physical remake plan at cursor %d",
					state.cursor,
				)
			}
			if err := func() error {
				if err := executeSQLiteRelationRemake(ctx, executor, plan); err != nil {
					return err
				}
				state.cursor++
				return transaction.completeRelationOperationIfLast(ctx, executor)
			}(); err != nil {
				return newSQLiteRelationRemakeExecutionError(
					fmt.Sprintf("remove SQLite relation field %s.%s by bounded remake", operation.Before.DBTable, field.Column),
					err,
				)
			}
			return nil
		}
		statement, err := compileMigrationRemoveField(operation.Before, wantField)
		if err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			if sqliteDropColumnCapabilityFailure(err) {
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("SQLite rejected native DROP COLUMN for %s.%s; table rebuild is disabled", operation.Before.DBTable, field.Column),
					err,
				)
			}
			return fmt.Errorf("remove SQLite scalar field %s.%s in relation step: %w", operation.Before.DBTable, field.Column, err)
		}
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *sqliteRevisionFencedTransaction) completeRelationOperationIfLast(
	ctx context.Context,
	executor migrationSQLExecutor,
) error {
	state := transaction.relation
	if state == nil || state.cursor != len(state.seal.intent.Operations) {
		return nil
	}
	if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
		return err
	}
	if err := verifySQLiteRelationFinalState(ctx, executor, &state.seal); err != nil {
		return err
	}
	state.finalVerified = true
	return nil
}

func stateCursor(state *sqliteRelationFencedState) int {
	if state == nil {
		return -1
	}
	return state.cursor
}

func compileSQLiteRelationCreateModel(
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
) (string, error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite relation CreateModel table: %w", err)
	}
	if len(model.Fields) == 0 {
		return "", fmt.Errorf("compile SQLite relation CreateModel %q: fields are empty", model.DBTable)
	}
	parts := make([]string, 0, len(model.Fields)+len(targets))
	targetIndex := 0
	for fieldIndex := range model.Fields {
		field := model.Fields[fieldIndex]
		if field.Kind != ir.FieldForeignKey {
			column, err := compileMigrationColumn(field)
			if err != nil {
				return "", fmt.Errorf("compile SQLite relation CreateModel %q field %d: %w", model.DBTable, fieldIndex, err)
			}
			parts = append(parts, column)
			continue
		}
		if targetIndex >= len(targets) || !reflect.DeepEqual(targets[targetIndex].SourceField, field) {
			return "", relationIntentIntegrity("relation CreateModel %q target metadata does not match field %d", model.DBTable, fieldIndex)
		}
		column, err := quoteIdentifier(field.Column)
		if err != nil {
			return "", fmt.Errorf("compile SQLite relation column: %w", err)
		}
		declaration := column + " INTEGER"
		if field.Nullable {
			declaration += " NULL"
		} else {
			declaration += " NOT NULL"
		}
		parts = append(parts, declaration)
		targetIndex++
	}
	if targetIndex != len(targets) {
		return "", relationIntentIntegrity("relation CreateModel %q has %d unused targets", model.DBTable, len(targets)-targetIndex)
	}
	for index := range targets {
		target := targets[index]
		sourceColumn, err := quoteIdentifier(target.SourceField.Column)
		if err != nil {
			return "", fmt.Errorf("compile SQLite relation source column: %w", err)
		}
		targetTable, err := quoteIdentifier(target.TargetModel.DBTable)
		if err != nil {
			return "", fmt.Errorf("compile SQLite relation target table: %w", err)
		}
		targetColumn, err := quoteIdentifier(target.TargetKey.Column)
		if err != nil {
			return "", fmt.Errorf("compile SQLite relation target column: %w", err)
		}
		parts = append(parts, "FOREIGN KEY ("+sourceColumn+") REFERENCES "+targetTable+" ("+targetColumn+") ON DELETE NO ACTION")
	}
	return "CREATE TABLE " + table + " (" + strings.Join(parts, ", ") + ")", nil
}

func compileSQLiteRelationAddField(
	model ir.Model,
	field ir.Field,
	targets []migrationbackend.MigrationTarget,
) (string, error) {
	if field.Kind != ir.FieldForeignKey || field.Relation == nil || field.PrimaryKey || field.Default != nil ||
		(!field.Nullable && field.Relation.OnDelete != ir.DeleteProtect) {
		return "", errors.New("relation AddField requires a non-primary-key ForeignKey with no migration default; required fields must use PROTECT")
	}
	relationFields := relationFieldsInModel(model)
	if len(targets) != len(relationFields)+1 {
		return "", relationIntentIntegrity(
			"relation AddField %q has %d targets, want %d",
			model.DBTable,
			len(targets),
			len(relationFields)+1,
		)
	}
	changed, err := sqliteRelationTargetForField(field, targets)
	if err != nil {
		return "", err
	}
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite relation AddField table: %w", err)
	}
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", fmt.Errorf("compile SQLite relation AddField column: %w", err)
	}
	targetTable, err := quoteIdentifier(changed.TargetModel.DBTable)
	if err != nil {
		return "", fmt.Errorf("compile SQLite relation AddField target table: %w", err)
	}
	targetColumn, err := quoteIdentifier(changed.TargetKey.Column)
	if err != nil {
		return "", fmt.Errorf("compile SQLite relation AddField target column: %w", err)
	}
	nullability := " NOT NULL"
	if field.Nullable {
		nullability = " NULL"
	}
	return "ALTER TABLE \"main\"." + table + " ADD COLUMN " + column + " INTEGER" + nullability + " REFERENCES " +
		targetTable + " (" + targetColumn + ") ON DELETE NO ACTION", nil
}

func preflightSQLiteRelationIntent(
	ctx context.Context,
	executor migrationSQLExecutor,
	transition migrationbackend.HistoryTransition,
	seal *sqliteRelationIntentSeal,
) (map[int]sqliteRelationRemakePlan, [sha256.Size]byte, error) {
	if err := verifySQLiteRelationIntentSeal(seal); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	catalog, err := loadSQLiteRelationCatalog(ctx, executor)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	if err := validateSQLiteRelationCatalogHazards(catalog, seal); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	if err := preflightSQLiteRelationModels(ctx, executor, transition, seal, catalog); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return preflightSQLiteRelationRemakes(ctx, executor, transition, seal, catalog)
}

func loadSQLiteRelationCatalog(ctx context.Context, executor migrationSQLExecutor) (sqliteRelationCatalog, error) {
	catalog := sqliteRelationCatalog{
		objects:   make([]sqliteRelationSchemaObject, 0),
		sequences: make(map[string]sqliteRelationSequenceSnapshot),
	}
	aggregateCatalogBytes := 0
	aggregateCatalogTokens := 0
	hasMainSequence := false
	for _, schema := range []string{"main", "temp"} {
		remaining := sqliteRelationMaxNodes - len(catalog.objects)
		statement := `SELECT ` +
			`COALESCE(length(CAST("type" AS BLOB)), -1), ` +
			`COALESCE(length(CAST("name" AS BLOB)), -1), ` +
			`COALESCE(length(CAST("tbl_name" AS BLOB)), -1), ` +
			`COALESCE(length(CAST("sql" AS BLOB)), 0), ` +
			`substr(CAST("type" AS BLOB), 1, ?), ` +
			`substr(CAST("name" AS BLOB), 1, ?), ` +
			`substr(CAST("tbl_name" AS BLOB), 1, ?), ` +
			`COALESCE(substr(CAST("sql" AS BLOB), 1, ?), X'') ` +
			`FROM ` + schema + `.sqlite_schema LIMIT ?`
		if err := func() (resultErr error) {
			rows, err := executor.QueryContext(
				ctx,
				statement,
				sqliteRelationMaxStringBytes+1,
				sqliteRelationMaxStringBytes+1,
				sqliteRelationMaxStringBytes+1,
				sqliteRelationMaxStringBytes+1,
				remaining+1,
			)
			if err != nil {
				return classifyRevisionIO("load SQLite "+schema+" relation catalog", err)
			}
			defer func() {
				resultErr = errors.Join(resultErr, classifyRevisionIO("close SQLite "+schema+" relation catalog", rows.Close()))
			}()
			for rows.Next() {
				var (
					typeBytes  int64
					nameBytes  int64
					ownerBytes int64
					sqlBytes   int64
					typeValue  []byte
					nameValue  []byte
					ownerValue []byte
					sqlValue   []byte
				)
				if err := rows.Scan(
					&typeBytes,
					&nameBytes,
					&ownerBytes,
					&sqlBytes,
					&typeValue,
					&nameValue,
					&ownerValue,
					&sqlValue,
				); err != nil {
					return classifyRevisionIO("scan SQLite "+schema+" relation catalog", err)
				}
				if typeBytes < 0 || nameBytes < 0 || ownerBytes < 0 || sqlBytes < 0 ||
					typeBytes > sqliteRelationMaxStringBytes || nameBytes > sqliteRelationMaxStringBytes ||
					ownerBytes > sqliteRelationMaxStringBytes || sqlBytes > sqliteRelationMaxStringBytes {
					return relationPhysicalDrift("SQLite %s catalog object exceeds the bounded string/SQL envelope", schema)
				}
				entryBytes := int(typeBytes + nameBytes + ownerBytes + sqlBytes)
				if len(catalog.objects) >= sqliteRelationMaxNodes || entryBytes > sqliteRelationMaxAggregateBytes-aggregateCatalogBytes {
					return relationPhysicalDrift("SQLite physical catalog exceeds the bounded object/SQL envelope")
				}
				object := sqliteRelationSchemaObject{
					schema: schema,
					kind:   string(typeValue),
					name:   string(nameValue),
					owner:  string(ownerValue),
					sql:    string(sqlValue),
				}
				object.nameKey = sqliteRelationIdentifierKey(object.name)
				object.ownerKey = sqliteRelationIdentifierKey(object.owner)
				if object.schema == "main" && object.kind == "table" && object.nameKey == "sqlite_sequence" {
					hasMainSequence = true
				}
				if strings.ContainsRune(object.kind, 0) || strings.ContainsRune(object.name, 0) ||
					strings.ContainsRune(object.owner, 0) || strings.ContainsRune(object.sql, 0) {
					return relationPhysicalDrift("SQLite %s catalog contains a NUL byte", schema)
				}
				object.tokens, err = tokenizeSQLiteRelationSQL(object.sql, sqliteRelationMaxNodes-aggregateCatalogTokens)
				if err != nil {
					return err
				}
				aggregateCatalogTokens += len(object.tokens)
				aggregateCatalogBytes += entryBytes
				catalog.objects = append(catalog.objects, object)
			}
			return classifyRevisionIO("iterate SQLite "+schema+" relation catalog", rows.Err())
		}(); err != nil {
			return sqliteRelationCatalog{}, err
		}
	}
	if hasMainSequence {
		remaining := sqliteRelationMaxNodes - len(catalog.objects)
		if err := func() (resultErr error) {
			rows, err := executor.QueryContext(
				ctx,
				`SELECT `+
					`typeof("name"), `+
					`COALESCE(length(CAST("name" AS BLOB)), -1), `+
					`substr(CAST("name" AS BLOB), 1, ?), `+
					`typeof("seq"), `+
					`CASE WHEN typeof("seq") = 'integer' THEN "seq" ELSE NULL END `+
					`FROM main.sqlite_sequence LIMIT ?`,
				sqliteRelationMaxStringBytes+1,
				remaining+1,
			)
			if err != nil {
				return classifyRevisionIO("load SQLite relation sequence catalog", err)
			}
			defer func() {
				resultErr = errors.Join(resultErr, classifyRevisionIO("close SQLite relation sequence catalog", rows.Close()))
			}()
			sequenceRows := 0
			for rows.Next() {
				var (
					nameType  string
					nameBytes int64
					nameValue []byte
					seqType   string
					sequence  sql.NullInt64
				)
				if err := rows.Scan(&nameType, &nameBytes, &nameValue, &seqType, &sequence); err != nil {
					return classifyRevisionIO("scan SQLite relation sequence catalog", err)
				}
				if nameType != "text" || nameBytes <= 0 || nameBytes > sqliteRelationMaxStringBytes ||
					int64(len(nameValue)) != nameBytes || !utf8.Valid(nameValue) ||
					seqType != "integer" || !sequence.Valid || sequence.Int64 < 0 ||
					nameBytes > int64(sqliteRelationMaxAggregateBytes-aggregateCatalogBytes) ||
					sequenceRows >= remaining {
					return relationPhysicalDrift("SQLite sequence catalog exceeds the bounded node/byte envelope")
				}
				name := string(nameValue)
				if strings.ContainsRune(name, 0) {
					return relationPhysicalDrift("SQLite sequence catalog contains a NUL byte")
				}
				aggregateCatalogBytes += int(nameBytes)
				sequenceRows++
				key := sqliteRelationIdentifierKey(name)
				if previous, exists := catalog.sequences[key]; exists {
					return relationPhysicalDrift("SQLite sequence catalog duplicates case-folded rows %q and %q", previous.name, name)
				}
				catalog.sequences[key] = sqliteRelationSequenceSnapshot{
					present: true,
					name:    name,
					value:   sequence.Int64,
				}
			}
			return classifyRevisionIO("iterate SQLite relation sequence catalog", rows.Err())
		}(); err != nil {
			return sqliteRelationCatalog{}, err
		}
	}
	sort.Slice(catalog.objects, func(left, right int) bool {
		if catalog.objects[left].schema != catalog.objects[right].schema {
			return catalog.objects[left].schema < catalog.objects[right].schema
		}
		if catalog.objects[left].kind != catalog.objects[right].kind {
			return catalog.objects[left].kind < catalog.objects[right].kind
		}
		return catalog.objects[left].nameKey < catalog.objects[right].nameKey
	})
	catalog.byName = make(map[sqliteRelationCatalogNameKey][]int, len(catalog.objects))
	catalog.byObjectKind = make(map[sqliteRelationCatalogObjectKey][]int, len(catalog.objects))
	for index := range catalog.objects {
		object := catalog.objects[index]
		nameKey := sqliteRelationCatalogNameKey{schema: object.schema, name: object.nameKey}
		catalog.byName[nameKey] = append(catalog.byName[nameKey], index)
		objectKey := sqliteRelationCatalogObjectKey{schema: object.schema, name: object.nameKey, kind: object.kind}
		catalog.byObjectKind[objectKey] = append(catalog.byObjectKind[objectKey], index)
	}
	return catalog, nil
}

func validateSQLiteRelationCatalogHazards(catalog sqliteRelationCatalog, seal *sqliteRelationIntentSeal) error {
	controls := map[string]struct{}{
		sqliteRelationIdentifierKey(migrationRevisionTable): {},
		sqliteRelationIdentifierKey(migrationRecorderTable): {},
	}
	touched := make(map[string]struct{}, len(seal.intent.Operations))
	relevant := make(map[string]struct{}, len(seal.intent.Operations)+2)
	mutationHazards := make(map[string]struct{}, len(seal.intent.Operations)+2)
	for key := range controls {
		relevant[key] = struct{}{}
		mutationHazards[key] = struct{}{}
	}
	for index := range seal.intent.Operations {
		operation := seal.intent.Operations[index]
		model := operation.After
		if reflect.DeepEqual(model, ir.Model{}) {
			model = operation.Before
		}
		key := sqliteRelationIdentifierKey(model.DBTable)
		touched[key] = struct{}{}
		relevant[key] = struct{}{}
		mutationHazards[key] = struct{}{}
	}
	for _, snapshot := range append(seal.graphPlan.InitialModels(), seal.graphPlan.FinalModels()...) {
		relevant[sqliteRelationIdentifierKey(snapshot.Model.DBTable)] = struct{}{}
	}

	for _, object := range catalog.objects {
		nameKey := object.nameKey
		ownerKey := object.ownerKey
		_, nameRelevant := relevant[nameKey]
		_, ownerTouched := touched[ownerKey]
		_, ownerControl := controls[ownerKey]
		_, nameControl := controls[nameKey]
		if object.schema == "temp" && object.kind != "trigger" && nameRelevant {
			return relationPhysicalDrift("SQLite TEMP %s %q shadows a relation/control identifier", object.kind, object.name)
		}
		if object.schema == "main" && object.kind != "trigger" && nameControl {
			canonicalControl := object.name == migrationRevisionTable || object.name == migrationRecorderTable
			if object.kind != "table" || !canonicalControl {
				return relationPhysicalDrift("SQLite main %s %q aliases a migration control identifier", object.kind, object.name)
			}
			wantSQL := createMigrationRevisionTableSQL
			if object.name == migrationRecorderTable {
				wantSQL = migrationRecorderTableDefinitionSQL
			}
			if object.sql != wantSQL {
				return relationPhysicalDrift("SQLite migration control table %q differs from its canonical schema", object.name)
			}
		}
		if object.kind == "index" && object.sql != "" && (ownerTouched || ownerControl) {
			return relationPhysicalDrift("SQLite index %s.%q is undeclared on touched/control table %q", object.schema, object.name, object.owner)
		}
		if object.kind == "trigger" {
			hazard := ownerTouched || ownerControl
			if !hazard && sqliteRelationSQLReferencesAny(object.kind, object.tokens, object.ownerKey, mutationHazards) {
				hazard = true
			}
			if hazard {
				return relationPhysicalDrift("SQLite trigger %s.%q owns or references a relation/control table", object.schema, object.name)
			}
		}
		if object.kind == "view" {
			if sqliteRelationSQLReferencesAny(object.kind, object.tokens, object.ownerKey, mutationHazards) {
				return relationPhysicalDrift("SQLite view %s.%q references a touched relation/control table", object.schema, object.name)
			}
		}
		if object.kind == "table" && sqliteRelationIsVirtualTable(object.tokens) && !ownerTouched {
			if sqliteRelationSQLReferencesAny(object.kind, object.tokens, object.ownerKey, mutationHazards) {
				return relationPhysicalDrift("SQLite virtual table %s.%q references a touched relation/control table", object.schema, object.name)
			}
		}
	}
	return nil
}

type sqliteRelationBoundaryModel struct {
	model   ir.Model
	present bool
}

func preflightSQLiteRelationModels(
	ctx context.Context,
	executor migrationSQLExecutor,
	_ migrationbackend.HistoryTransition,
	seal *sqliteRelationIntentSeal,
	catalog sqliteRelationCatalog,
) error {
	initial, _ := sqliteRelationBoundaryStates(seal)
	physicalGraph, err := buildSQLiteRelationPhysicalGraph(catalog)
	if err != nil {
		return err
	}
	validationCache := newSQLiteRelationPhysicalValidationCache()
	tables := sortedSQLiteRelationBoundaryTables(initial)
	for _, tableKey := range tables {
		state := initial[tableKey]
		if err := assertSQLiteRelationNamespace(ctx, executor, catalog, state.model.DBTable, state.present); err != nil {
			return err
		}
		if !state.present {
			continue
		}
		tableObject, exists, err := catalog.object("main", state.model.DBTable, "table")
		if err != nil {
			return err
		}
		if !exists || tableObject.name != state.model.DBTable {
			return relationPhysicalDrift("relation input table %q is missing or differs by SQLite identifier spelling", state.model.DBTable)
		}
		targets, known := sqliteRelationTargetsForModel(seal, state.model, false)
		if !known && len(relationFieldsInModel(state.model)) != 0 {
			return relationIntentUnsupported("scalar-touched relation model %q has no exact same-step target metadata", state.model.DBTable)
		}
		if err := assertSQLiteRelationModelShape(ctx, executor, state.model, targets, known, validationCache); err != nil {
			return fmt.Errorf("preflight relation input model %q: %w", state.model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, state.model, targets, known, tableObject.sql, validationCache); err != nil {
			return fmt.Errorf("preflight relation input model %q SQL: %w", state.model.DBTable, err)
		}
	}
	emptyTables := make(map[string]bool)
	checkedEmpty := make(map[string]bool)
	removeTables := make(map[string]struct{})
	for operationIndex := range seal.intent.Operations {
		operation := seal.intent.Operations[operationIndex]
		if operation.Kind == migrationbackend.MigrationRemoveField {
			removeTables[sqliteRelationIdentifierKey(operation.Before.DBTable)] = struct{}{}
		}
	}
	removeDependencies, err := buildSQLiteRelationRemoveDependencyIndex(ctx, executor, catalog, physicalGraph, removeTables)
	if err != nil {
		return err
	}
	for operationIndex := range seal.intent.Operations {
		operation := seal.intent.Operations[operationIndex]
		switch operation.Kind {
		case migrationbackend.MigrationAddField:
			field, err := operation.ChangedField()
			if err != nil {
				return relationIntentIntegrity("invalid sealed AddField delta: %v", err)
			}
			if field.Default == nil && field.Nullable {
				continue
			}
			tableKey := sqliteRelationIdentifierKey(operation.Before.DBTable)
			empty := true
			if initialState := initial[tableKey]; initialState.present {
				if !checkedEmpty[tableKey] {
					var err error
					emptyTables[tableKey], err = sqliteTableEmpty(ctx, executor, operation.Before.DBTable)
					if err != nil {
						return err
					}
					checkedEmpty[tableKey] = true
				}
				empty = emptyTables[tableKey]
			}
			if !empty && field.Default != nil {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding field %s with a migration default requires one-time backfill or table rebuild", operation.Before.DBTable, field.Column),
					nil,
				)
			}
			if !empty {
				feature := "sqlite_add_field"
				if field.Kind == ir.FieldForeignKey {
					feature = "sqlite_relation_migration"
				}
				return migrationbackend.NewCapabilityError(
					feature,
					fmt.Sprintf("table %s contains rows; adding non-null field %s requires table rebuild", operation.Before.DBTable, field.Column),
					nil,
				)
			}
		case migrationbackend.MigrationRemoveField:
			field, err := operation.ChangedField()
			if err != nil {
				return relationIntentIntegrity("invalid sealed RemoveField delta: %v", err)
			}
			if owner, referenced := removeDependencies.owner(operation.Before.DBTable, field.Column); referenced {
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("column %s.%s is referenced by foreign key on table %s", operation.Before.DBTable, field.Column, owner),
					nil,
				)
			}
		}
	}

	return validateSQLiteRelationPhysicalGraph(seal, physicalGraph)
}

type sqliteRelationRemoveDependencyIndex struct {
	owners           map[sqliteRelationRemoveDependencyKey]string
	ownerVisits      int
	foreignKeyVisits int
}

type sqliteRelationRemoveDependencyKey struct {
	table  string
	column string
}

func buildSQLiteRelationRemoveDependencyIndex(
	ctx context.Context,
	executor migrationSQLExecutor,
	catalog sqliteRelationCatalog,
	graph sqliteRelationPhysicalGraph,
	removeTables map[string]struct{},
) (sqliteRelationRemoveDependencyIndex, error) {
	index := sqliteRelationRemoveDependencyIndex{owners: make(map[sqliteRelationRemoveDependencyKey]string)}
	ownerSet := make(map[string]struct{})
	for table := range removeTables {
		for owner := range graph.incoming[table] {
			ownerSet[owner] = struct{}{}
		}
	}
	owners := make([]string, 0, len(ownerSet))
	for owner := range ownerSet {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		index.ownerVisits++
		object, exists, err := catalog.object("main", owner, "table")
		if err != nil {
			return sqliteRelationRemoveDependencyIndex{}, err
		}
		if !exists {
			return sqliteRelationRemoveDependencyIndex{}, relationPhysicalDrift("SQLite inbound foreign-key owner %q is absent from the bounded catalog", owner)
		}
		foreignKeys, err := readSQLiteRelationForeignKeys(ctx, executor, object.name, -1)
		if err != nil {
			return sqliteRelationRemoveDependencyIndex{}, err
		}
		for _, foreignKey := range foreignKeys {
			index.foreignKeyVisits++
			table := sqliteRelationIdentifierKey(foreignKey.table)
			if _, relevant := removeTables[table]; !relevant {
				continue
			}
			key := sqliteRelationRemoveDependencyKey{table: table, column: sqliteRelationIdentifierKey(foreignKey.to)}
			if _, exists := index.owners[key]; !exists {
				index.owners[key] = owner
			}
		}
	}
	return index, nil
}

func (index sqliteRelationRemoveDependencyIndex) owner(table, column string) (string, bool) {
	owner, exists := index.owners[sqliteRelationRemoveDependencyKey{
		table:  sqliteRelationIdentifierKey(table),
		column: sqliteRelationIdentifierKey(column),
	}]
	return owner, exists
}

func sqliteRelationBoundaryStates(
	seal *sqliteRelationIntentSeal,
) (map[string]sqliteRelationBoundaryModel, map[string]sqliteRelationBoundaryModel) {
	initial := make(map[string]sqliteRelationBoundaryModel)
	final := make(map[string]sqliteRelationBoundaryModel)
	for _, snapshot := range seal.graphPlan.InitialModels() {
		key := sqliteRelationIdentifierKey(snapshot.Model.DBTable)
		initial[key] = sqliteRelationBoundaryModel{model: snapshot.Model, present: true}
	}
	for _, snapshot := range seal.graphPlan.FinalModels() {
		key := sqliteRelationIdentifierKey(snapshot.Model.DBTable)
		final[key] = sqliteRelationBoundaryModel{model: snapshot.Model, present: true}
	}
	for _, operation := range seal.intent.Operations {
		if operation.Kind == migrationbackend.MigrationCreateModel {
			key := sqliteRelationIdentifierKey(operation.After.DBTable)
			if _, present := initial[key]; !present {
				initial[key] = sqliteRelationBoundaryModel{model: operation.After.Clone()}
			}
		}
		if operation.Kind == migrationbackend.MigrationDeleteModel {
			key := sqliteRelationIdentifierKey(operation.Before.DBTable)
			if _, present := final[key]; !present {
				final[key] = sqliteRelationBoundaryModel{model: operation.Before.Clone()}
			}
		}
	}
	return initial, final
}

func modelForRelationBoundary(candidate, fallback ir.Model) ir.Model {
	if reflect.DeepEqual(candidate, ir.Model{}) {
		return fallback.Clone()
	}
	return candidate.Clone()
}

func sortedSQLiteRelationBoundaryTables(states map[string]sqliteRelationBoundaryModel) []string {
	tables := make([]string, 0, len(states))
	for table := range states {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func sqliteRelationTargetsForModel(
	seal *sqliteRelationIntentSeal,
	model ir.Model,
	final bool,
) ([]migrationbackend.MigrationTarget, bool) {
	targets, err := seal.graphPlan.BoundaryTargets(model, final)
	return targets, err == nil
}

func sqliteRelationOperationChangesForeignKey(operation migrationbackend.MigrationOperation) bool {
	field, err := operation.ChangedField()
	return err == nil && field.Kind == ir.FieldForeignKey
}

func sqliteRelationTargetForField(field ir.Field, targets []migrationbackend.MigrationTarget) (migrationbackend.MigrationTarget, error) {
	for _, target := range targets {
		if reflect.DeepEqual(target.SourceField, field) {
			return target, nil
		}
	}
	return migrationbackend.MigrationTarget{}, relationIntentIntegrity("relation field %q lacks exact target authority", field.Name)
}

// Select retained bindings in the supplied model's field order. Supplied targets
// have already passed graph admission; the result borrows immutable metadata.
func sqliteRelationTargetsForFields(model ir.Model, targets []migrationbackend.MigrationTarget) ([]migrationbackend.MigrationTarget, error) {
	byName := make(map[string]migrationbackend.MigrationTarget, len(targets))
	for _, target := range targets {
		if _, exists := byName[target.SourceField.Name]; exists {
			return nil, relationIntentIntegrity("duplicate relation target %q", target.SourceField.Name)
		}
		byName[target.SourceField.Name] = target
	}
	selected := make([]migrationbackend.MigrationTarget, 0, len(targets))
	for _, field := range model.Fields {
		if field.Kind != ir.FieldForeignKey {
			continue
		}
		target, exists := byName[field.Name]
		if !exists || !reflect.DeepEqual(target.SourceField, field) {
			return nil, relationIntentIntegrity("relation field %q lacks exact retained target authority", field.Name)
		}
		selected = append(selected, target)
	}
	return selected, nil
}

func assertSQLiteRelationNamespace(
	ctx context.Context,
	executor migrationSQLExecutor,
	catalog sqliteRelationCatalog,
	table string,
	wantPresent bool,
) error {
	key := sqliteRelationIdentifierKey(table)
	for _, schema := range []string{"temp", "main"} {
		indices := catalog.byName[sqliteRelationCatalogNameKey{schema: schema, name: key}]
		for _, index := range indices {
			object := catalog.objects[index]
			if object.kind == "trigger" {
				continue
			}
			if schema == "temp" {
				return relationPhysicalDrift("SQLite TEMP %s %q shadows relation table %q", object.kind, object.name, table)
			}
			if object.kind != "table" || !wantPresent {
				return relationPhysicalDrift("SQLite main %s %q collides with relation table %q", object.kind, object.name, table)
			}
		}
	}
	if !wantPresent {
		if err := assertSQLiteRelationSequenceAbsent(ctx, executor, catalog, table); err != nil {
			return err
		}
	}
	return nil
}

func assertSQLiteRelationSequenceAbsent(
	_ context.Context,
	_ migrationSQLExecutor,
	catalog sqliteRelationCatalog,
	table string,
) error {
	if sequence, exists := catalog.sequences[sqliteRelationIdentifierKey(table)]; exists {
		return relationPhysicalDrift("sqlite_sequence contains orphan row %q for absent relation table %q", sequence.name, table)
	}
	return nil
}

type sqliteRelationPhysicalGraph struct {
	outgoing map[string]map[string]struct{}
	incoming map[string]map[string]struct{}
}

func buildSQLiteRelationPhysicalGraph(catalog sqliteRelationCatalog) (sqliteRelationPhysicalGraph, error) {
	graph := sqliteRelationPhysicalGraph{
		outgoing: make(map[string]map[string]struct{}),
		incoming: make(map[string]map[string]struct{}),
	}
	edgeCount := 0
	for _, object := range catalog.objects {
		if object.schema != "main" || object.kind != "table" || sqliteRelationIdentifierKey(object.name) == "sqlite_sequence" {
			continue
		}
		source := sqliteRelationIdentifierKey(object.name)
		for index := 0; index+1 < len(object.tokens); index++ {
			if object.tokens[index].quoted || object.tokens[index].literal || object.tokens[index].value != "references" {
				continue
			}
			if edgeCount >= sqliteRelationMaxNodes {
				return sqliteRelationPhysicalGraph{}, relationPhysicalDrift("SQLite physical relation graph exceeds %d bounded edges", sqliteRelationMaxNodes)
			}
			edgeCount++
			graph.add(source, object.tokens[index+1].value)
		}
	}
	return graph, nil
}

func validateSQLiteRelationPhysicalGraph(
	seal *sqliteRelationIntentSeal,
	graph sqliteRelationPhysicalGraph,
) error {
	for _, control := range []string{migrationRevisionTable, migrationRecorderTable} {
		controlKey := sqliteRelationIdentifierKey(control)
		if len(graph.incoming[controlKey]) != 0 {
			return relationPhysicalDrift("SQLite migration control table %q has an inbound foreign key", control)
		}
	}

	for index := range seal.intent.Operations {
		operation := seal.intent.Operations[index]
		model := operation.After
		if reflect.DeepEqual(model, ir.Model{}) {
			model = operation.Before
		}
		source := sqliteRelationIdentifierKey(model.DBTable)
		switch operation.Kind {
		case migrationbackend.MigrationCreateModel:
			if inbound := graph.incoming[source]; len(inbound) != 0 {
				owners := make([]string, 0, len(inbound))
				for owner := range inbound {
					owners = append(owners, owner)
				}
				sort.Strings(owners)
				return relationPhysicalDrift("relation CreateModel table %q has pre-existing inbound foreign key from %q", model.DBTable, owners[0])
			}
			graph.removeOutgoing(source)
			for targetIndex := range operation.Targets {
				target := sqliteRelationIdentifierKey(operation.Targets[targetIndex].TargetModel.DBTable)
				graph.add(source, target)
			}
		case migrationbackend.MigrationDeleteModel:
			var owners []string
			for owner := range graph.incoming[source] {
				if owner != source {
					owners = append(owners, owner)
				}
			}
			if len(owners) != 0 {
				sort.Strings(owners)
				return relationPhysicalDrift("relation DeleteModel table %q has inbound foreign key from %q", model.DBTable, owners[0])
			}
			graph.remove(source)
		case migrationbackend.MigrationAddField:
			field, err := operation.ChangedField()
			if err != nil {
				return relationIntentIntegrity("invalid AddField delta: %v", err)
			}
			if field.Kind != ir.FieldForeignKey {
				continue
			}
			target, err := sqliteRelationTargetForField(field, operation.Targets)
			if err != nil {
				return err
			}
			graph.add(source, sqliteRelationIdentifierKey(target.TargetModel.DBTable))
		case migrationbackend.MigrationRemoveField:
			if len(operation.Targets) == 0 || !sqliteRelationOperationChangesForeignKey(operation) {
				continue
			}
			graph.removeOutgoing(source)
			retained, err := sqliteRelationTargetsForFields(operation.After, operation.Targets)
			if err != nil {
				return err
			}
			for _, target := range retained {
				graph.add(source, sqliteRelationIdentifierKey(target.TargetModel.DBTable))
			}
		}
	}
	return nil
}

func (graph *sqliteRelationPhysicalGraph) add(source, target string) {
	if graph.outgoing[source] == nil {
		graph.outgoing[source] = make(map[string]struct{})
	}
	if _, exists := graph.outgoing[source][target]; exists {
		return
	}
	graph.outgoing[source][target] = struct{}{}
	if graph.incoming[target] == nil {
		graph.incoming[target] = make(map[string]struct{})
	}
	graph.incoming[target][source] = struct{}{}
}

func (graph sqliteRelationPhysicalGraph) hasEdge(source, target string) bool {
	_, exists := graph.outgoing[source][target]
	return exists
}

func (graph *sqliteRelationPhysicalGraph) remove(source string) {
	graph.removeOutgoing(source)
	delete(graph.incoming, source)
}

func (graph *sqliteRelationPhysicalGraph) removeOutgoing(source string) {
	for target := range graph.outgoing[source] {
		delete(graph.incoming[target], source)
		if len(graph.incoming[target]) == 0 {
			delete(graph.incoming, target)
		}
	}
	delete(graph.outgoing, source)
}

func (catalog sqliteRelationCatalog) object(schema, name, kind string) (sqliteRelationSchemaObject, bool, error) {
	key := sqliteRelationIdentifierKey(name)
	var indices []int
	if kind == "" {
		indices = catalog.byName[sqliteRelationCatalogNameKey{schema: schema, name: key}]
	} else {
		indices = catalog.byObjectKind[sqliteRelationCatalogObjectKey{schema: schema, name: key, kind: kind}]
	}
	if len(indices) > 1 {
		return sqliteRelationSchemaObject{}, false, relationPhysicalDrift("SQLite %s catalog duplicates case-folded %s object %q", schema, kind, name)
	}
	if len(indices) == 0 {
		return sqliteRelationSchemaObject{}, false, nil
	}
	return catalog.objects[indices[0]], true, nil
}

func sqliteRelationSQLReferencesAny(
	kind string,
	tokens []sqliteRelationSQLToken,
	ownerKey string,
	hazards map[string]struct{},
) bool {
	start := 0
	switch kind {
	case "trigger":
		ownerFound := false
		for index, token := range tokens {
			if token.quoted || token.literal || token.value != "on" {
				continue
			}
			ownerIndex := index + 1
			if ownerIndex < len(tokens) && tokens[ownerIndex].value == ownerKey {
				start = ownerIndex + 1
				ownerFound = true
			} else if ownerIndex+1 < len(tokens) &&
				(tokens[ownerIndex].value == "main" || tokens[ownerIndex].value == "temp") &&
				tokens[ownerIndex+1].value == ownerKey {
				start = ownerIndex + 2
				ownerFound = true
			}
			break
		}
		if !ownerFound {
			return true
		}
	case "view":
		start = sqliteRelationSQLTokenAfterKeyword(tokens, "as")
	case "table":
		start = sqliteRelationSQLTokenAfterKeyword(tokens, "using")
	}
	for _, token := range tokens[start:] {
		if _, exists := hazards[token.value]; exists {
			return true
		}
	}
	return false
}

func sqliteRelationSQLTokenAfterKeyword(tokens []sqliteRelationSQLToken, keyword string) int {
	for index, token := range tokens {
		if !token.quoted && !token.literal && token.value == keyword {
			return index + 1
		}
	}
	return 0
}

func sqliteRelationIsVirtualTable(tokens []sqliteRelationSQLToken) bool {
	want := []string{"create", "virtual", "table"}
	matched := 0
	for _, token := range tokens {
		if token.quoted || token.literal {
			return false
		}
		if token.value != want[matched] {
			return false
		}
		matched++
		if matched == len(want) {
			return true
		}
	}
	return false
}

func tokenizeSQLiteRelationSQL(statement string, limit int) ([]sqliteRelationSQLToken, error) {
	tokens := make([]sqliteRelationSQLToken, 0)
	appendToken := func(value string, quoted, literal bool) error {
		if len(tokens) >= limit {
			return relationPhysicalDrift("SQLite physical catalog exceeds the bounded SQL-token envelope %d", sqliteRelationMaxNodes)
		}
		tokens = append(tokens, sqliteRelationSQLToken{value: sqliteRelationIdentifierKey(value), quoted: quoted, literal: literal})
		return nil
	}
	for index := 0; index < len(statement); {
		switch {
		case statement[index] == '\'':
			index++
			value := make([]byte, 0)
			for index < len(statement) {
				if statement[index] != '\'' {
					value = append(value, statement[index])
					index++
					continue
				}
				if index+1 < len(statement) && statement[index+1] == '\'' {
					value = append(value, '\'')
					index += 2
					continue
				}
				index++
				break
			}
			if len(value) != 0 {
				if err := appendToken(string(value), false, true); err != nil {
					return nil, err
				}
			}
		case statement[index] == '-' && index+1 < len(statement) && statement[index+1] == '-':
			index += 2
			for index < len(statement) && statement[index] != '\n' && statement[index] != '\r' {
				index++
			}
		case statement[index] == '/' && index+1 < len(statement) && statement[index+1] == '*':
			index += 2
			for index+1 < len(statement) && (statement[index] != '*' || statement[index+1] != '/') {
				index++
			}
			if index+1 < len(statement) {
				index += 2
			}
		case statement[index] == '"' || statement[index] == '`' || statement[index] == '[':
			opening := statement[index]
			closing := opening
			if opening == '[' {
				closing = ']'
			}
			index++
			value := make([]byte, 0)
			for index < len(statement) {
				if statement[index] != closing {
					value = append(value, statement[index])
					index++
					continue
				}
				if index+1 < len(statement) && statement[index+1] == closing {
					value = append(value, closing)
					index += 2
					continue
				}
				index++
				break
			}
			if len(value) != 0 {
				if err := appendToken(string(value), true, false); err != nil {
					return nil, err
				}
			}
		case sqliteRelationIdentifierByte(lowerASCII(statement[index])):
			start := index
			for index < len(statement) && sqliteRelationIdentifierByte(lowerASCII(statement[index])) {
				index++
			}
			value := make([]byte, index-start)
			for offset := range value {
				value[offset] = lowerASCII(statement[start+offset])
			}
			if err := appendToken(string(value), false, false); err != nil {
				return nil, err
			}
		default:
			index++
		}
	}
	return tokens, nil
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func sqliteRelationIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func (transaction *sqliteRevisionFencedTransaction) verifyRelationBeforeRecord(
	ctx context.Context,
	executor migrationSQLExecutor,
) error {
	state := transaction.relation
	if state == nil {
		return relationIntentIntegrity("migration transaction has no sealed intent state")
	}
	if state.cursor != len(state.seal.intent.Operations) {
		return relationIntentIntegrity("relation operation cursor consumed %d of %d sealed operations", state.cursor, len(state.seal.intent.Operations))
	}
	if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
		return err
	}
	if !state.finalVerified {
		return relationIntentIntegrity("relation final physical state was not verified by the last sealed operation")
	}
	return nil
}

func (transaction *sqliteRevisionFencedTransaction) verifyRelationCommitReady() error {
	if transaction.relation == nil {
		return relationIntentIntegrity("migration transaction has no sealed intent state")
	}
	if !transaction.relation.finalVerified || transaction.relation.cursor != len(transaction.relation.seal.intent.Operations) {
		return relationIntentIntegrity("relation migration cannot commit before exact operation and physical final-state verification")
	}
	return nil
}

func verifySQLiteRelationIntentSeal(seal *sqliteRelationIntentSeal) error {
	if seal == nil {
		return relationIntentIntegrity("relation migration intent seal is missing")
	}
	digest, err := hashSQLiteRelationIntent(seal.intent)
	if err != nil {
		return relationIntentIntegrity("hash sealed relation migration intent: %v", err)
	}
	if digest != seal.digest {
		return relationIntentIntegrity("sealed relation migration intent changed after validation")
	}
	return nil
}

func verifySQLiteRelationFinalState(
	ctx context.Context,
	executor migrationSQLExecutor,
	seal *sqliteRelationIntentSeal,
) error {
	catalog, err := loadSQLiteRelationCatalog(ctx, executor)
	if err != nil {
		return err
	}
	if err := validateSQLiteRelationCatalogHazards(catalog, seal); err != nil {
		return err
	}
	validationCache := newSQLiteRelationPhysicalValidationCache()
	_, final := sqliteRelationBoundaryStates(seal)
	for _, tableKey := range sortedSQLiteRelationBoundaryTables(final) {
		state := final[tableKey]
		if err := assertSQLiteRelationNamespace(ctx, executor, catalog, state.model.DBTable, state.present); err != nil {
			return err
		}
		if !state.present {
			continue
		}
		tableObject, exists, err := catalog.object("main", state.model.DBTable, "table")
		if err != nil {
			return err
		}
		if !exists || tableObject.name != state.model.DBTable {
			return relationPhysicalDrift("final relation table %q is missing or differs by SQLite identifier spelling", state.model.DBTable)
		}
		targets, known := sqliteRelationTargetsForModel(seal, state.model, true)
		if !known && len(relationFieldsInModel(state.model)) != 0 {
			return relationIntentUnsupported("final relation model %q has no exact sealed target metadata", state.model.DBTable)
		}
		if err := assertSQLiteRelationModelShape(ctx, executor, state.model, targets, known, validationCache); err != nil {
			return fmt.Errorf("verify final relation model %q: %w", state.model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, state.model, targets, known, tableObject.sql, validationCache); err != nil {
			return fmt.Errorf("verify final relation model %q SQL: %w", state.model.DBTable, err)
		}
	}

	return runSQLiteRelationForeignKeyCheck(ctx, executor)
}

type sqliteRelationPhysicalColumn struct {
	position     int
	name         string
	declaredType string
	notNull      int
	defaultValue sql.NullString
	primaryKey   int
	hidden       int
}

type sqliteRelationPhysicalForeignKey struct {
	id       int
	sequence int
	table    string
	from     string
	to       string
	onUpdate string
	onDelete string
	match    string
}

type sqliteRelationPhysicalValidationCache struct {
	autoKeys map[sqliteRelationAutoKey]error
	// Layouts are populated only after exact column and FK catalog validation.
	// They borrow sealed fields and live only within one boundary inspection.
	layouts map[string][]ir.Field
}

type sqliteRelationAutoKey struct {
	table  string
	column string
}

func newSQLiteRelationPhysicalValidationCache() *sqliteRelationPhysicalValidationCache {
	return &sqliteRelationPhysicalValidationCache{autoKeys: make(map[sqliteRelationAutoKey]error), layouts: make(map[string][]ir.Field)}
}

func assertSQLiteRelationModelShape(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
	targetsKnown bool,
	cache *sqliteRelationPhysicalValidationCache,
) (resultErr error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return relationPhysicalDrift("invalid table identifier %q: %v", model.DBTable, err)
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.table_xinfo(`+table+`)`)
	if err != nil {
		return classifyRevisionIO("inspect relation table columns "+model.DBTable, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close relation table columns "+model.DBTable, rows.Close()))
	}()
	columns := make([]sqliteRelationPhysicalColumn, 0, len(model.Fields))
	for rows.Next() {
		if len(columns) >= len(model.Fields)+1 || len(columns) >= sqliteRelationMaxFields+1 {
			return relationPhysicalDrift("table %q exceeds the bounded declared column shape", model.DBTable)
		}
		var column sqliteRelationPhysicalColumn
		if err := rows.Scan(
			&column.position,
			&column.name,
			&column.declaredType,
			&column.notNull,
			&column.defaultValue,
			&column.primaryKey,
			&column.hidden,
		); err != nil {
			return classifyRevisionIO("scan relation table column "+model.DBTable, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIO("iterate relation table columns "+model.DBTable, err)
	}
	if err := rows.Close(); err != nil {
		return classifyRevisionIO("close relation table columns "+model.DBTable, err)
	}
	if len(columns) != len(model.Fields) {
		return relationPhysicalDrift("table %q has %d columns, want %d", model.DBTable, len(columns), len(model.Fields))
	}
	fieldsByColumn := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		fieldsByColumn[field.Column] = field
	}
	layout := make([]ir.Field, len(columns))
	for index, column := range columns {
		field, exists := fieldsByColumn[column.name]
		if !exists {
			return relationPhysicalDrift("table %q has unexpected or duplicate column %q", model.DBTable, column.name)
		}
		delete(fieldsByColumn, column.name)
		layout[index] = field
		declaredType, err := sqliteRelationDeclaredType(field)
		if err != nil {
			return err
		}
		wantNotNull := 1
		if field.Nullable {
			wantNotNull = 0
		}
		wantPrimaryKey := 0
		if field.PrimaryKey {
			wantPrimaryKey = 1
		}
		if column.position != index || column.name != field.Column || column.declaredType != declaredType ||
			column.notNull != wantNotNull || column.defaultValue.Valid || column.primaryKey != wantPrimaryKey || column.hidden != 0 {
			return relationPhysicalDrift(
				"table %q column[%d]=(%q,%q,notnull=%d,default=%v,pk=%d,hidden=%d), want=(%q,%q,notnull=%d,default=NULL,pk=%d,hidden=0)",
				model.DBTable,
				index,
				column.name,
				column.declaredType,
				column.notNull,
				column.defaultValue,
				column.primaryKey,
				column.hidden,
				field.Column,
				declaredType,
				wantNotNull,
				wantPrimaryKey,
			)
		}
	}
	relationFields := relationFieldsInModel(model)
	foreignKeys, err := readSQLiteRelationForeignKeys(ctx, executor, model.DBTable, len(relationFields))
	if err != nil {
		return err
	}
	if len(foreignKeys) != len(relationFields) {
		return relationPhysicalDrift("table %q has %d foreign keys, want %d", model.DBTable, len(foreignKeys), len(relationFields))
	}
	bySource := make(map[string]sqliteRelationPhysicalForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		if _, exists := bySource[foreignKey.from]; exists {
			return relationPhysicalDrift("table %q duplicates physical foreign key source column %q", model.DBTable, foreignKey.from)
		}
		bySource[foreignKey.from] = foreignKey
	}
	for index := range relationFields {
		field := relationFields[index]
		foreignKey, exists := bySource[field.Column]
		if !exists || foreignKey.sequence != 0 || foreignKey.onUpdate != "NO ACTION" ||
			foreignKey.onDelete != "NO ACTION" || foreignKey.match != "NONE" {
			return relationPhysicalDrift("table %q foreign key for %q = %+v, want exact NO ACTION single-column relation", model.DBTable, field.Column, foreignKey)
		}
		if targetsKnown {
			if index >= len(targets) || !reflect.DeepEqual(targets[index].SourceField, field) ||
				foreignKey.table != targets[index].TargetModel.DBTable || foreignKey.to != targets[index].TargetKey.Column {
				return relationPhysicalDrift("table %q foreign key for %q = %+v, want sealed target metadata", model.DBTable, field.Column, foreignKey)
			}
		}
		if err := cache.assertAutoKey(ctx, executor, foreignKey.table, foreignKey.to); err != nil {
			return fmt.Errorf("table %q foreign key %q target: %w", model.DBTable, field.Column, err)
		}
	}
	cache.layouts[model.DBTable] = layout
	return nil
}

func assertSQLiteRelationCanonicalTableSQL(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
	targetsKnown bool,
	actualSQL string,
	cache *sqliteRelationPhysicalValidationCache,
) error {
	layout, exists := cache.layouts[model.DBTable]
	if !exists {
		return relationIntentIntegrity("canonical table SQL requires validated physical layout for %q", model.DBTable)
	}
	model.Fields = layout
	if targetsKnown {
		ordered, err := sqliteRelationTargetsForFields(model, targets)
		if err != nil {
			return err
		}
		targets = ordered
	}
	relationFields := relationFieldsInModel(model)
	if !targetsKnown {
		foreignKeys, err := readSQLiteRelationForeignKeys(ctx, executor, model.DBTable, len(relationFields))
		if err != nil {
			return err
		}
		bySource := make(map[string]sqliteRelationPhysicalForeignKey, len(foreignKeys))
		for _, foreignKey := range foreignKeys {
			bySource[foreignKey.from] = foreignKey
		}
		targets = make([]migrationbackend.MigrationTarget, len(relationFields))
		for index := range relationFields {
			foreignKey, exists := bySource[relationFields[index].Column]
			if !exists {
				return relationPhysicalDrift("table %q lacks canonical foreign key for %q", model.DBTable, relationFields[index].Column)
			}
			targets[index] = migrationbackend.MigrationTarget{
				SourceField: relationFields[index],
				TargetModel: ir.Model{DBTable: foreignKey.table},
				TargetKey:   ir.Field{Column: foreignKey.to},
			}
		}
	}
	matches, err := matchesSQLiteRelationCanonicalTableSQL(actualSQL, model, targets)
	if err != nil {
		return err
	}
	if !matches {
		return relationPhysicalDrift("table %q SQL differs from exact sealed/canonical declaration", model.DBTable)
	}
	return nil
}

// matchesSQLiteRelationCanonicalTableSQL consumes the exact CREATE TABLE
// grammar emitted by GoDj and SQLite's native ALTER ADD rewrite in one forward
// pass. Relation columns may carry their REFERENCES clause inline, while any
// relation column without an inline clause must have its exact table-level
// constraint after all columns in the same field order. No case folding,
// whitespace normalization, token subset, or trailing SQL is accepted.
func matchesSQLiteRelationCanonicalTableSQL(
	actual string,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
) (bool, error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return false, err
	}
	prefix := "CREATE TABLE " + table + " ("
	if !strings.HasPrefix(actual, prefix) {
		return false, nil
	}
	cursor := len(prefix)
	pending := make([]string, 0, len(targets))
	targetIndex := 0
	consumeDelimiter := func() bool {
		if cursor+2 > len(actual) || actual[cursor:cursor+2] != ", " {
			return false
		}
		cursor += 2
		return true
	}
	consume := func(fragment string) bool {
		if !strings.HasPrefix(actual[cursor:], fragment) {
			return false
		}
		cursor += len(fragment)
		return cursor == len(actual)-1 && actual[cursor] == ')' ||
			cursor+2 <= len(actual) && actual[cursor:cursor+2] == ", "
	}

	for fieldIndex := range model.Fields {
		if fieldIndex != 0 && !consumeDelimiter() {
			return false, nil
		}
		field := model.Fields[fieldIndex]
		if field.Kind != ir.FieldForeignKey {
			column, err := compileMigrationColumn(field)
			if err != nil {
				return false, err
			}
			if !consume(column) {
				return false, nil
			}
			continue
		}
		if targetIndex >= len(targets) || !reflect.DeepEqual(targets[targetIndex].SourceField, field) {
			return false, relationIntentIntegrity("canonical table %q target metadata does not match field %d", model.DBTable, fieldIndex)
		}
		target := targets[targetIndex]
		targetIndex++
		column, err := compileSQLiteRelationColumn(field)
		if err != nil {
			return false, err
		}
		constraint, err := compileSQLiteRelationConstraint(target)
		if err != nil {
			return false, err
		}
		inline := column + " REFERENCES " + constraint
		if consume(inline) {
			continue
		}
		if !consume(column) {
			return false, nil
		}
		quotedColumn, err := quoteIdentifier(field.Column)
		if err != nil {
			return false, err
		}
		pending = append(pending, "FOREIGN KEY ("+quotedColumn+") REFERENCES "+constraint)
	}
	if targetIndex != len(targets) {
		return false, relationIntentIntegrity("canonical table %q has %d unused targets", model.DBTable, len(targets)-targetIndex)
	}
	for index := range pending {
		if !consumeDelimiter() || !consume(pending[index]) {
			return false, nil
		}
	}
	return cursor == len(actual)-1 && actual[cursor] == ')', nil
}

func compileSQLiteRelationColumn(field ir.Field) (string, error) {
	column, err := quoteIdentifier(field.Column)
	if err != nil {
		return "", err
	}
	if field.Kind != ir.FieldForeignKey || field.Relation == nil || field.PrimaryKey || field.Default != nil {
		return "", errors.New("canonical relation column is not an exact ForeignKey")
	}
	declaration := column + " INTEGER"
	if field.Nullable {
		declaration += " NULL"
	} else {
		declaration += " NOT NULL"
	}
	return declaration, nil
}

// compileSQLiteRelationConstraint returns the portion following REFERENCES.
func compileSQLiteRelationConstraint(target migrationbackend.MigrationTarget) (string, error) {
	table, err := quoteIdentifier(target.TargetModel.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(target.TargetKey.Column)
	if err != nil {
		return "", err
	}
	return table + " (" + column + ") ON DELETE NO ACTION", nil
}

func (cache *sqliteRelationPhysicalValidationCache) assertAutoKey(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableName,
	columnName string,
) (resultErr error) {
	key := sqliteRelationAutoKey{
		table:  sqliteRelationIdentifierKey(tableName),
		column: sqliteRelationIdentifierKey(columnName),
	}
	if cached, exists := cache.autoKeys[key]; exists {
		return cached
	}
	defer func() {
		cache.autoKeys[key] = resultErr
	}()
	table, err := quoteIdentifier(tableName)
	if err != nil {
		return relationPhysicalDrift("invalid target table identifier %q", tableName)
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.table_xinfo(`+table+`)`)
	if err != nil {
		return classifyRevisionIO("inspect relation target AutoField "+tableName, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close relation target AutoField "+tableName, rows.Close()))
	}()
	found := 0
	for rows.Next() {
		var (
			position     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
			hidden       int
		)
		if err := rows.Scan(&position, &name, &declaredType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return classifyRevisionIO("scan relation target AutoField "+tableName, err)
		}
		if sqliteRelationIdentifierKey(name) == sqliteRelationIdentifierKey(columnName) {
			found++
			if name != columnName || declaredType != "INTEGER" || notNull != 1 || defaultValue.Valid || primaryKey != 1 || hidden != 0 {
				return relationPhysicalDrift("target %q.%q is not an exact INTEGER NOT NULL primary key", tableName, columnName)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIO("iterate relation target AutoField "+tableName, err)
	}
	if found != 1 {
		return relationPhysicalDrift("target %q.%q primary key count is %d, want 1", tableName, columnName, found)
	}
	return nil
}

func sqliteRelationDeclaredType(field ir.Field) (string, error) {
	switch field.Kind {
	case ir.FieldAuto, ir.FieldForeignKey:
		return "INTEGER", nil
	case ir.FieldInteger:
		return "BIGINT", nil
	case ir.FieldChar:
		return fmt.Sprintf("VARCHAR(%d)", field.MaxLength), nil
	case ir.FieldDecimal:
		return "BLOB", nil
	case ir.FieldFloat:
		return "REAL", nil
	case ir.FieldDuration:
		return "BIGINT", nil
	case ir.FieldTime:
		return "TIME", nil
	case ir.FieldDate:
		return "DATE", nil
	case ir.FieldDateTime:
		return "DATETIME", nil
	case ir.FieldText:
		return "TEXT", nil
	case ir.FieldBoolean:
		return "BOOLEAN", nil
	default:
		return "", relationPhysicalDrift("unsupported declared field kind %q", field.Kind)
	}
}

func readSQLiteRelationForeignKeys(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableName string,
	expected int,
) (foreignKeys []sqliteRelationPhysicalForeignKey, resultErr error) {
	table, err := quoteIdentifier(tableName)
	if err != nil {
		return nil, relationPhysicalDrift("invalid relation table identifier %q: %v", tableName, err)
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.foreign_key_list(`+table+`)`)
	if err != nil {
		return nil, classifyRevisionIO("inspect relation foreign keys "+tableName, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			foreignKeys = nil
			resultErr = errors.Join(resultErr, classifyRevisionIO("close relation foreign keys "+tableName, err))
		}
	}()
	rowLimit := sqliteRelationMaxTargets + 1
	if expected >= 0 && expected+1 < rowLimit {
		rowLimit = expected + 1
	}
	for rows.Next() {
		if len(foreignKeys) >= rowLimit {
			return nil, relationPhysicalDrift("table %q exceeds the bounded declared foreign-key shape", tableName)
		}
		var foreignKey sqliteRelationPhysicalForeignKey
		if err := rows.Scan(
			&foreignKey.id,
			&foreignKey.sequence,
			&foreignKey.table,
			&foreignKey.from,
			&foreignKey.to,
			&foreignKey.onUpdate,
			&foreignKey.onDelete,
			&foreignKey.match,
		); err != nil {
			return nil, classifyRevisionIO("scan relation foreign key "+tableName, err)
		}
		foreignKeys = append(foreignKeys, foreignKey)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRevisionIO("iterate relation foreign keys "+tableName, err)
	}
	return foreignKeys, nil
}

func runSQLiteRelationForeignKeyCheck(ctx context.Context, executor migrationSQLExecutor) (resultErr error) {
	rows, err := executor.QueryContext(ctx, `PRAGMA main.foreign_key_check`)
	if err != nil {
		return classifyRevisionIO("run relation foreign_key_check", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close relation foreign_key_check", rows.Close()))
	}()
	if rows.Next() {
		var (
			table        string
			rowID        sql.NullInt64
			parent       string
			foreignKeyID int
		)
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return classifyRevisionIO("scan relation foreign_key_check", err)
		}
		return fmt.Errorf("%w: table=%q rowid=%v parent=%q foreign_key_id=%d", errSQLiteRelationForeignKey, table, rowID, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIO("iterate relation foreign_key_check", err)
	}
	return nil
}

func relationPhysicalDrift(format string, arguments ...any) error {
	return migrationbackend.NewCapabilityError(
		"sqlite_relation_migration",
		fmt.Sprintf(format, arguments...),
		errSQLiteRelationPhysicalDrift,
	)
}
