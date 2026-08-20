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
		RemoveForeignKeyByTableRemake:     true,
	}
}

type sqliteRelationIntentSeal struct {
	intent                 migrationbackend.MigrationIntent
	digest                 [sha256.Size]byte
	externalTargets        map[string]sqliteRelationExternalTarget
	targetOperationByTable map[string][]int
}

type sqliteRelationExternalTarget struct {
	app      string
	model    string
	snapshot ir.Model
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

type sqliteRelationResourceBudget struct {
	nodes uint64
	bytes uint64
}

type sqliteRelationReverseOwnerIndex struct {
	byApp      map[string]*sqliteRelationReverseAppOwners
	appLookups int
}

type sqliteRelationReverseAppOwners struct {
	byModel      map[string]map[string]sqliteRelationReverseOwner
	modelLookups int
}

type sqliteRelationReverseOwner struct {
	model string
	field string
}

func newSQLiteRelationReverseOwnerIndex() *sqliteRelationReverseOwnerIndex {
	return &sqliteRelationReverseOwnerIndex{
		byApp: make(map[string]*sqliteRelationReverseAppOwners),
	}
}

func (index *sqliteRelationReverseOwnerIndex) app(app string) *sqliteRelationReverseAppOwners {
	index.appLookups++
	owners := index.byApp[app]
	if owners == nil {
		owners = &sqliteRelationReverseAppOwners{byModel: make(map[string]map[string]sqliteRelationReverseOwner)}
		index.byApp[app] = owners
	}
	return owners
}

func (index *sqliteRelationReverseAppOwners) register(
	model,
	reverse string,
	owner sqliteRelationReverseOwner,
) (sqliteRelationReverseOwner, bool) {
	modelKey := sqliteRelationIdentifierKey(model)
	index.modelLookups++
	owners := index.byModel[modelKey]
	if owners == nil {
		owners = make(map[string]sqliteRelationReverseOwner)
		index.byModel[modelKey] = owners
	}
	reverseKey := sqliteRelationIdentifierKey(reverse)
	previous, exists := owners[reverseKey]
	if !exists {
		owners[reverseKey] = owner
	}
	return previous, exists
}

func (index *sqliteRelationReverseAppOwners) firstFieldCollision(
	model string,
	fields []ir.Field,
) (ir.Field, sqliteRelationReverseOwner, bool) {
	modelKey := sqliteRelationIdentifierKey(model)
	index.modelLookups++
	owners := index.byModel[modelKey]
	if len(owners) == 0 {
		return ir.Field{}, sqliteRelationReverseOwner{}, false
	}
	for fieldIndex := range fields {
		field := fields[fieldIndex]
		if owner, exists := owners[sqliteRelationIdentifierKey(field.Name)]; exists {
			return field, owner, true
		}
	}
	return ir.Field{}, sqliteRelationReverseOwner{}, false
}

func registerSQLiteInitialRelationReverseOwners(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
	initial map[string]ir.Model,
	owners *sqliteRelationReverseOwnerIndex,
) error {
	for position := range intent.Operations {
		operation := intent.Operations[position]
		if reflect.DeepEqual(operation.Before, ir.Model{}) {
			continue
		}
		relationFields := relationFieldsInModel(operation.Before)
		for targetIndex := range relationFields {
			field := relationFields[targetIndex]
			if field.Relation == nil {
				return relationIntentIntegrity(
					"relation operation %d initial source field %q has no relation metadata",
					operation.OperationIndex,
					field.Name,
				)
			}
			if targetIndex >= len(operation.Targets) ||
				!reflect.DeepEqual(operation.Targets[targetIndex].SourceField, field) {
				return relationIntentIntegrity(
					"relation operation %d initial source target %d is not in exact field order",
					operation.OperationIndex,
					targetIndex,
				)
			}
			target := &operation.Targets[targetIndex]
			if target.TargetModel.Name != field.Relation.Target.ModelName {
				return relationIntentIntegrity(
					"relation operation %d initial source field %q lacks exact sealed target metadata",
					operation.OperationIndex,
					field.Name,
				)
			}
			reverse := field.Relation.Reverse.Name
			if reverse == "" {
				continue
			}
			targetSnapshot := target.TargetModel
			if field.Relation.Target.AppLabel == transition.Migration.App {
				if visible, exists := initial[sqliteRelationIdentifierKey(field.Relation.Target.ModelName)]; exists {
					targetSnapshot = visible
				}
			}
			for fieldIndex := range targetSnapshot.Fields {
				if targetSnapshot.Fields[fieldIndex].Name == reverse {
					return relationIntentIntegrity(
						"relation operation %d initial target field %q collides with reverse name registered by %s.%s",
						operation.OperationIndex,
						reverse,
						operation.Before.Name,
						field.Name,
					)
				}
			}
			owner := sqliteRelationReverseOwner{model: operation.Before.Name, field: field.Name}
			targetOwners := owners.app(field.Relation.Target.AppLabel)
			if previous, duplicate := targetOwners.register(target.TargetModel.Name, reverse, owner); duplicate && previous != owner {
				return relationIntentIntegrity(
					"relation reverse name %q on %s.%s is duplicated by %s.%s",
					reverse,
					field.Relation.Target.AppLabel,
					target.TargetModel.Name,
					previous.model,
					previous.field,
				)
			}
		}
	}
	return nil
}

type sqliteRelationBeginCheckpoint uint8

const (
	sqliteRelationCheckpointForeignKeysSet sqliteRelationBeginCheckpoint = iota + 1
	sqliteRelationCheckpointForeignKeysRead
	sqliteRelationCheckpointTransactionBegun
	sqliteRelationCheckpointPhysicalPreflightComplete
	sqliteRelationCheckpointRevisionClaimStarting
	sqliteRelationCheckpointRevisionClaimed
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
	if err := ctx.Err(); err != nil {
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
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != revisionSessionReady {
		return nil, fmt.Errorf("begin SQLite relation revision-fenced migration: session state %d is not ready", session.state)
	}

	successorRecords, err := migrationHistorySuccessor(session.records, transition)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, err
	}
	successorToken := session.token
	successorToken.initialized = true
	successorToken.fingerprint = fingerprintMigrationHistory(successorRecords)
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

	connection, err := session.backend.database.Conn(ctx)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, classifyRevisionIO("acquire pinned relation migration connection", err)
	}
	var connectionBoundary migrationPinnedConnection = connection
	if session.relationConnectionHook != nil {
		connectionBoundary = session.relationConnectionHook(connectionBoundary)
		if connectionBoundary == nil {
			_ = connection.Close()
			session.state = revisionSessionPoisoned
			return nil, errors.New("begin SQLite relation revision-fenced migration: relation connection hook returned nil")
		}
	}
	closeBeforeBegin := func(primary error) error {
		return errors.Join(primary, closeOrDiscardMigrationConnection(connectionBoundary))
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
	if err := ctx.Err(); err != nil {
		session.state = revisionSessionPoisoned
		return nil, closeBeforeBegin(fmt.Errorf("begin SQLite relation revision-fenced migration: %w", err))
	}
	if _, err := connectionBoundary.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		discardErr := discardMigrationConnection(connectionBoundary)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(classifyRevisionIO("begin immediate relation migration transaction", err), discardErr)
	}
	session.notifyRelationBeginCheckpoint(sqliteRelationCheckpointTransactionBegun)

	transaction := &sqliteRevisionFencedTransaction{
		connection:       connectionBoundary,
		session:          session,
		transition:       transition,
		expectedRecords:  cloneAppliedMigrations(session.records),
		successorRecords: cloneAppliedMigrations(successorRecords),
		expectedToken:    session.token,
		successorToken:   successorToken,
		bootstrap:        !session.token.initialized,
		relation:         &sqliteRelationFencedState{seal: seal},
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
	pinned := cloneSQLiteRelationIntent(intent)
	externalTargets, err := validateSQLiteRelationIntent(transition, pinned)
	if err != nil {
		return sqliteRelationIntentSeal{}, err
	}
	// ForeignKey Add/Remove validation expands the sealed private copy from
	// the public changed-field target to a complete source-field-ordered target
	// list. Re-scan the derived representation so cloning that authority cannot
	// evade the original aggregate resource envelope.
	if err := scanSQLiteRelationIntentResources(transition, pinned); err != nil {
		return sqliteRelationIntentSeal{}, err
	}
	digest, err := hashSQLiteRelationIntent(pinned)
	if err != nil {
		return sqliteRelationIntentSeal{}, relationIntentIntegrity("seal relation migration intent: %v", err)
	}
	targetOperationByTable := make(map[string][]int)
	for index := range pinned.Operations {
		operation := pinned.Operations[index]
		if len(operation.Targets) == 0 {
			continue
		}
		model := operation.After
		if operation.Kind == migrationbackend.MigrationDeleteModel {
			model = operation.Before
		}
		tableKey := sqliteRelationIdentifierKey(model.DBTable)
		targetOperationByTable[tableKey] = append(targetOperationByTable[tableKey], index)
	}
	return sqliteRelationIntentSeal{
		intent:                 pinned,
		digest:                 digest,
		externalTargets:        externalTargets,
		targetOperationByTable: targetOperationByTable,
	}, nil
}

func scanSQLiteRelationIntentResources(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) error {
	if len(intent.Operations) > sqliteRelationMaxOperations {
		return relationIntentIntegrity("relation intent has %d operations, maximum %d", len(intent.Operations), sqliteRelationMaxOperations)
	}
	budget := sqliteRelationResourceBudget{}
	if err := budget.consumeNodes("transition", 1); err != nil {
		return err
	}
	if err := budget.consumeString("transition.migration.app", transition.Migration.App); err != nil {
		return err
	}
	if err := budget.consumeString("transition.migration.name", transition.Migration.Name); err != nil {
		return err
	}
	if err := budget.consumeNodes("operations", len(intent.Operations)); err != nil {
		return err
	}
	for operationIndex := range intent.Operations {
		operation := intent.Operations[operationIndex]
		prefix := fmt.Sprintf("operations[%d]", operationIndex)
		if err := budget.scanModel(prefix+".before", operation.Before); err != nil {
			return err
		}
		if err := budget.scanModel(prefix+".after", operation.After); err != nil {
			return err
		}
		if len(operation.Targets) > sqliteRelationMaxTargets {
			return relationIntentIntegrity("%s has %d targets, maximum %d", prefix, len(operation.Targets), sqliteRelationMaxTargets)
		}
		if err := budget.consumeNodes(prefix+".targets", len(operation.Targets)); err != nil {
			return err
		}
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			targetPrefix := fmt.Sprintf("%s.targets[%d]", prefix, targetIndex)
			if err := budget.scanField(targetPrefix+".source_field", target.SourceField); err != nil {
				return err
			}
			if err := budget.scanModel(targetPrefix+".target_model", target.TargetModel); err != nil {
				return err
			}
			if err := budget.scanField(targetPrefix+".target_key", target.TargetKey); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *sqliteRelationResourceBudget) scanModel(path string, model ir.Model) error {
	if err := budget.consumeNodes(path, 1); err != nil {
		return err
	}
	for _, value := range []string{model.Name, model.GoName, model.DBTable} {
		if err := budget.consumeString(path, value); err != nil {
			return err
		}
	}
	if len(model.Fields) > sqliteRelationMaxFields {
		return relationIntentIntegrity("%s has %d fields, maximum %d", path, len(model.Fields), sqliteRelationMaxFields)
	}
	if err := budget.consumeNodes(path+".fields", len(model.Fields)); err != nil {
		return err
	}
	for index := range model.Fields {
		if err := budget.scanField(fmt.Sprintf("%s.fields[%d]", path, index), model.Fields[index]); err != nil {
			return err
		}
	}
	return nil
}

func (budget *sqliteRelationResourceBudget) scanField(path string, field ir.Field) error {
	if err := budget.consumeNodes(path, 1); err != nil {
		return err
	}
	for _, value := range []string{field.Name, field.GoName, field.Column, string(field.Kind)} {
		if err := budget.consumeString(path, value); err != nil {
			return err
		}
	}
	if field.Default != nil {
		if err := budget.consumeNodes(path+".default", 1); err != nil {
			return err
		}
		for _, value := range []string{string(field.Default.Kind), field.Default.String} {
			if err := budget.consumeString(path+".default", value); err != nil {
				return err
			}
		}
	}
	if field.Relation != nil {
		if err := budget.consumeNodes(path+".relation", 1); err != nil {
			return err
		}
		for _, value := range []string{
			field.Relation.Target.AppLabel,
			field.Relation.Target.ModelName,
			string(field.Relation.Cardinality),
			field.Relation.Reverse.Name,
			string(field.Relation.OnDelete),
		} {
			if err := budget.consumeString(path+".relation", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *sqliteRelationResourceBudget) consumeNodes(path string, count int) error {
	if count < 0 || uint64(count) > sqliteRelationMaxNodes-budget.nodes {
		return relationIntentIntegrity("%s exceeds the aggregate relation intent node limit %d", path, sqliteRelationMaxNodes)
	}
	budget.nodes += uint64(count)
	return nil
}

func (budget *sqliteRelationResourceBudget) consumeString(path, value string) error {
	if len(value) > sqliteRelationMaxStringBytes {
		return relationIntentIntegrity("%s contains a string of %d bytes, maximum %d", path, len(value), sqliteRelationMaxStringBytes)
	}
	if uint64(len(value)) > sqliteRelationMaxAggregateBytes-budget.bytes {
		return relationIntentIntegrity("%s exceeds the aggregate relation intent byte limit %d", path, sqliteRelationMaxAggregateBytes)
	}
	budget.bytes += uint64(len(value))
	return nil
}

func cloneSQLiteRelationIntent(intent migrationbackend.MigrationIntent) migrationbackend.MigrationIntent {
	clone := migrationbackend.MigrationIntent{}
	if intent.Operations == nil {
		return clone
	}
	clone.Operations = make([]migrationbackend.MigrationOperation, len(intent.Operations))
	for operationIndex := range intent.Operations {
		operation := intent.Operations[operationIndex]
		operation.Before = cloneSQLiteRelationModel(operation.Before)
		operation.After = cloneSQLiteRelationModel(operation.After)
		if operation.Targets != nil {
			operation.Targets = make([]migrationbackend.MigrationTarget, len(operation.Targets))
			for targetIndex := range intent.Operations[operationIndex].Targets {
				target := intent.Operations[operationIndex].Targets[targetIndex]
				target.SourceField = target.SourceField.Clone()
				target.TargetModel = cloneSQLiteRelationModel(target.TargetModel)
				target.TargetKey = target.TargetKey.Clone()
				operation.Targets[targetIndex] = target
			}
		}
		clone.Operations[operationIndex] = operation
	}
	return clone
}

func cloneSQLiteRelationModel(model ir.Model) ir.Model {
	clone := model
	if model.Fields != nil {
		clone.Fields = make([]ir.Field, len(model.Fields))
		for index := range model.Fields {
			clone.Fields[index] = model.Fields[index].Clone()
		}
	}
	return clone
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
	writeRelationBool(hash, field.Default != nil)
	if field.Default != nil {
		writeRelationString(hash, string(field.Default.Kind))
		writeRelationString(hash, field.Default.String)
		writeRelationBool(hash, field.Default.Boolean)
		writeRelationInt64(hash, field.Default.Integer)
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
) (map[string]sqliteRelationExternalTarget, error) {
	if intent.Operations == nil {
		return nil, relationIntentIntegrity("migration intent operations are missing")
	}
	if len(intent.Operations) == 0 {
		return map[string]sqliteRelationExternalTarget{}, nil
	}

	type modelIdentity struct {
		name  string
		table string
	}
	identities := make(map[string]modelIdentity)
	tableOwners := make(map[string]string)
	goNameOwners := make(map[string]string)
	beforeModels := make([]ir.Model, len(intent.Operations))
	afterModels := make([]ir.Model, len(intent.Operations))
	expectedRelationFields := make([][]ir.Field, len(intent.Operations))
	relationMutations := make(map[string]int)
	for position := range intent.Operations {
		operation := intent.Operations[position]
		wantIndex := position
		if transition.Kind == migrationbackend.HistoryTransitionUnapply {
			wantIndex = len(intent.Operations) - 1 - position
		}
		if operation.OperationIndex != wantIndex {
			return nil, relationIntentIntegrity("relation operation position %d has original index %d, want %d", position, operation.OperationIndex, wantIndex)
		}
		if transition.Kind == migrationbackend.HistoryTransitionApply &&
			(operation.Kind == migrationbackend.MigrationDeleteModel || operation.Kind == migrationbackend.MigrationRemoveField) ||
			transition.Kind == migrationbackend.HistoryTransitionUnapply &&
				(operation.Kind == migrationbackend.MigrationCreateModel || operation.Kind == migrationbackend.MigrationAddField) {
			return nil, relationIntentIntegrity("relation operation %d kind %d does not match history transition %d", operation.OperationIndex, operation.Kind, transition.Kind)
		}

		var before, after ir.Model
		switch operation.Kind {
		case migrationbackend.MigrationCreateModel:
			if !reflect.DeepEqual(operation.Before, ir.Model{}) {
				return nil, relationIntentIntegrity("relation CreateModel operation %d has non-zero Before model", operation.OperationIndex)
			}
			after = operation.After
			if err := validateExactNormalizedRelationModel(after); err != nil {
				return nil, relationIntentIntegrity("relation operation %d After model is not exact normalized IR: %v", operation.OperationIndex, err)
			}
			expectedRelationFields[position] = relationFieldsInModel(after)
		case migrationbackend.MigrationAddField:
			before, after = operation.Before, operation.After
			field, err := validateSQLiteRelationAddDelta(before, after)
			if err != nil {
				return nil, relationIntentIntegrity("relation AddField operation %d: %v", operation.OperationIndex, err)
			}
			if field.Kind == ir.FieldForeignKey || field.Relation != nil {
				sourceKey := sqliteRelationIdentifierKey(after.Name)
				relationMutations[sourceKey]++
				if relationMutations[sourceKey] > 1 {
					return nil, relationIntentUnsupported(
						"relation AddField/RemoveField permits at most one relation mutation per source model %q in a migration step",
						after.Name,
					)
				}
				derived, err := deriveSQLiteRelationMutationTargets(operation, field, after)
				if err != nil {
					return nil, err
				}
				intent.Operations[position].Targets = derived
				operation = intent.Operations[position]
				expectedRelationFields[position] = relationFieldsInModel(after)
			} else {
				// Scalar mutations on relation-bearing models carry the complete
				// retained relation target list. The metadata is authority for
				// preflight/final-shape verification; the scalar DDL compiler does
				// not consume it.
				expectedRelationFields[position] = relationFieldsInModel(after)
			}
		case migrationbackend.MigrationDeleteModel:
			if !reflect.DeepEqual(operation.After, ir.Model{}) {
				return nil, relationIntentIntegrity("relation DeleteModel operation %d has non-zero After model", operation.OperationIndex)
			}
			before = operation.Before
			if err := validateExactNormalizedRelationModel(before); err != nil {
				return nil, relationIntentIntegrity("relation operation %d Before model is not exact normalized IR: %v", operation.OperationIndex, err)
			}
			expectedRelationFields[position] = relationFieldsInModel(before)
		case migrationbackend.MigrationRemoveField:
			before, after = operation.Before, operation.After
			field, err := validateSQLiteRelationRemoveDelta(before, after)
			if err != nil {
				return nil, relationIntentIntegrity("relation RemoveField operation %d: %v", operation.OperationIndex, err)
			}
			if field.Kind == ir.FieldForeignKey || field.Relation != nil {
				sourceKey := sqliteRelationIdentifierKey(before.Name)
				relationMutations[sourceKey]++
				if relationMutations[sourceKey] > 1 {
					return nil, relationIntentUnsupported(
						"relation AddField/RemoveField permits at most one relation mutation per source model %q in a migration step",
						before.Name,
					)
				}
				derived, err := deriveSQLiteRelationMutationTargets(operation, field, before)
				if err != nil {
					return nil, err
				}
				intent.Operations[position].Targets = derived
				operation = intent.Operations[position]
				expectedRelationFields[position] = relationFieldsInModel(before)
			} else {
				expectedRelationFields[position] = relationFieldsInModel(before)
			}
		default:
			return nil, relationIntentIntegrity("relation operation %d has invalid kind %d", operation.OperationIndex, operation.Kind)
		}
		beforeModels[position], afterModels[position] = before, after
		model := after
		if reflect.DeepEqual(model, ir.Model{}) {
			model = before
		}
		if relationReservedTable(model.DBTable) {
			return nil, relationIntentIntegrity("relation operation %d uses reserved SQLite migration table %q", operation.OperationIndex, model.DBTable)
		}
		nameKey := sqliteRelationIdentifierKey(model.Name)
		tableKey := sqliteRelationIdentifierKey(model.DBTable)
		if identity, exists := identities[nameKey]; exists {
			if identity.table != tableKey {
				return nil, relationIntentIntegrity("relation model %q changes table identity from %q to %q", model.Name, identity.table, model.DBTable)
			}
		} else {
			identities[nameKey] = modelIdentity{name: model.Name, table: tableKey}
		}
		if owner, exists := tableOwners[tableKey]; exists && owner != nameKey {
			return nil, relationIntentIntegrity("relation models %q and %q collide on SQLite table %q", identities[owner].name, model.Name, model.DBTable)
		}
		tableOwners[tableKey] = nameKey
		if owner, exists := goNameOwners[model.GoName]; exists && owner != nameKey {
			return nil, relationIntentIntegrity("relation models %q and %q duplicate Go name %q", identities[owner].name, model.Name, model.GoName)
		}
		goNameOwners[model.GoName] = nameKey
	}

	current := make(map[string]ir.Model, len(identities))
	initialized := make(map[string]bool, len(identities))
	for position := range intent.Operations {
		model := afterModels[position]
		if reflect.DeepEqual(model, ir.Model{}) {
			model = beforeModels[position]
		}
		nameKey := sqliteRelationIdentifierKey(model.Name)
		if initialized[nameKey] {
			continue
		}
		initialized[nameKey] = true
		if !reflect.DeepEqual(beforeModels[position], ir.Model{}) {
			current[nameKey] = beforeModels[position].Clone()
		}
	}

	externalTargets := make(map[string]sqliteRelationExternalTarget)
	externalIdentityTables := make(map[struct {
		app   string
		model string
	}]string)
	reverseOwners := newSQLiteRelationReverseOwnerIndex()
	localReverseOwners := reverseOwners.app(transition.Migration.App)
	if err := registerSQLiteInitialRelationReverseOwners(
		transition,
		intent,
		current,
		reverseOwners,
	); err != nil {
		return nil, err
	}
	for position := range intent.Operations {
		operation := intent.Operations[position]
		before, after := beforeModels[position], afterModels[position]
		model := after
		if reflect.DeepEqual(model, ir.Model{}) {
			model = before
		}
		nameKey := sqliteRelationIdentifierKey(model.Name)
		actual, exists := current[nameKey]
		if reflect.DeepEqual(before, ir.Model{}) {
			if exists {
				return nil, relationIntentIntegrity("relation operation %d CreateModel source already exists in the ordered intent", operation.OperationIndex)
			}
		} else if !exists || !reflect.DeepEqual(actual, before) {
			return nil, relationIntentIntegrity("relation operation %d Before model is discontinuous with the ordered intent", operation.OperationIndex)
		}

		relationFields := expectedRelationFields[position]
		if len(operation.Targets) != len(relationFields) {
			return nil, relationIntentIntegrity("relation operation %d has %d targets, want %d in exact field order", operation.OperationIndex, len(operation.Targets), len(relationFields))
		}
		for targetIndex := range operation.Targets {
			if operation.Kind != migrationbackend.MigrationCreateModel &&
				operation.Kind != migrationbackend.MigrationDeleteModel &&
				operation.Kind != migrationbackend.MigrationAddField &&
				operation.Kind != migrationbackend.MigrationRemoveField {
				return nil, relationIntentUnsupported("SQLite relation target metadata is unsupported on operation %d kind %d", operation.OperationIndex, operation.Kind)
			}
			target := operation.Targets[targetIndex]
			sourceField := relationFields[targetIndex]
			if !reflect.DeepEqual(target.SourceField, sourceField) {
				return nil, relationIntentIntegrity("relation operation %d target %d source field does not match declared field order", operation.OperationIndex, targetIndex)
			}
			if sourceField.Relation == nil {
				return nil, relationIntentIntegrity("relation operation %d target %d source field has no relation", operation.OperationIndex, targetIndex)
			}
			if err := validateExactNormalizedRelationModel(target.TargetModel); err != nil {
				return nil, relationIntentIntegrity("relation operation %d target %d model is not exact normalized IR: %v", operation.OperationIndex, targetIndex, err)
			}
			if relationReservedTable(target.TargetModel.DBTable) {
				return nil, relationIntentIntegrity("relation operation %d target %d uses reserved SQLite migration table %q", operation.OperationIndex, targetIndex, target.TargetModel.DBTable)
			}
			if sourceField.Relation.Target.ModelName != target.TargetModel.Name {
				return nil, relationIntentIntegrity("relation operation %d target %d model name %q does not match source declaration %q", operation.OperationIndex, targetIndex, target.TargetModel.Name, sourceField.Relation.Target.ModelName)
			}
			primaryKey, err := exactRelationTargetPrimaryKey(target.TargetModel)
			if err != nil {
				return nil, relationIntentIntegrity("relation operation %d target %d: %v", operation.OperationIndex, targetIndex, err)
			}
			if !reflect.DeepEqual(target.TargetKey, primaryKey) {
				return nil, relationIntentIntegrity("relation operation %d target %d key is not the exact historical AutoField primary key", operation.OperationIndex, targetIndex)
			}
			if reverse := sourceField.Relation.Reverse.Name; reverse != "" {
				for fieldIndex := range target.TargetModel.Fields {
					if target.TargetModel.Fields[fieldIndex].Name == reverse {
						return nil, relationIntentIntegrity("relation operation %d reverse name %q collides with target field", operation.OperationIndex, reverse)
					}
				}
				owner := sqliteRelationReverseOwner{model: model.Name, field: sourceField.Name}
				targetReverseOwners := localReverseOwners
				if sourceField.Relation.Target.AppLabel != transition.Migration.App {
					targetReverseOwners = reverseOwners.app(sourceField.Relation.Target.AppLabel)
				}
				if previous, exists := targetReverseOwners.register(
					target.TargetModel.Name,
					reverse,
					owner,
				); exists && previous != owner {
					return nil, relationIntentIntegrity(
						"relation reverse name %q on %s.%s is duplicated by %s.%s",
						reverse,
						sourceField.Relation.Target.AppLabel,
						target.TargetModel.Name,
						previous.model,
						previous.field,
					)
				}
			}

			targetNameKey := sqliteRelationIdentifierKey(target.TargetModel.Name)
			targetTableKey := sqliteRelationIdentifierKey(target.TargetModel.DBTable)
			if sourceField.Relation.Target.AppLabel == transition.Migration.App {
				if targetNameKey == nameKey {
					return nil, relationIntentIntegrity("relation operation %d contains a self relation", operation.OperationIndex)
				}
				if _, participates := identities[targetNameKey]; participates {
					visible, visibleNow := current[targetNameKey]
					if !visibleNow || !reflect.DeepEqual(visible, target.TargetModel) {
						return nil, relationIntentIntegrity("relation operation %d target %d is not visible with the exact scheduled state", operation.OperationIndex, targetIndex)
					}
					continue
				}
			}
			if owner, collision := tableOwners[targetTableKey]; collision {
				return nil, relationIntentIntegrity("external relation target %s.%s collides with local model %q on table %q", sourceField.Relation.Target.AppLabel, target.TargetModel.Name, identities[owner].name, target.TargetModel.DBTable)
			}
			if len(relationFieldsInModel(target.TargetModel)) != 0 {
				return nil, relationIntentUnsupported("external relation target %s.%s contains relation fields without nested sealed target metadata", sourceField.Relation.Target.AppLabel, target.TargetModel.Name)
			}
			targetIdentity := struct {
				app   string
				model string
			}{app: sourceField.Relation.Target.AppLabel, model: target.TargetModel.Name}
			if previousTable, exists := externalIdentityTables[targetIdentity]; exists && previousTable != targetTableKey {
				return nil, relationIntentIntegrity("external relation target %s.%s maps to conflicting SQLite tables %q and %q", targetIdentity.app, targetIdentity.model, previousTable, target.TargetModel.DBTable)
			}
			if existing, exists := externalTargets[targetTableKey]; exists {
				if existing.app != targetIdentity.app || existing.model != targetIdentity.model {
					return nil, relationIntentIntegrity("external relation target identities %s.%s and %s.%s collide on SQLite table %q", existing.app, existing.model, targetIdentity.app, targetIdentity.model, target.TargetModel.DBTable)
				}
				if !reflect.DeepEqual(existing.snapshot, target.TargetModel) {
					return nil, relationIntentIntegrity("relation target table %q has conflicting historical model snapshots", target.TargetModel.DBTable)
				}
			}
			externalIdentityTables[targetIdentity] = targetTableKey
			externalTargets[targetTableKey] = sqliteRelationExternalTarget{
				app:      targetIdentity.app,
				model:    targetIdentity.model,
				snapshot: target.TargetModel.Clone(),
			}
		}
		if !reflect.DeepEqual(after, ir.Model{}) {
			if field, owner, exists := localReverseOwners.firstFieldCollision(
				after.Name,
				after.Fields,
			); exists {
				return nil, relationIntentIntegrity(
					"relation operation %d field %q collides with reverse name registered by %s.%s",
					operation.OperationIndex,
					field.Name,
					owner.model,
					owner.field,
				)
			}
		}
		if err := validateSQLiteRelationStaticOperation(operation, before, after); err != nil {
			return nil, err
		}
		if reflect.DeepEqual(after, ir.Model{}) {
			delete(current, nameKey)
		} else {
			current[nameKey] = after.Clone()
		}
	}
	return externalTargets, nil
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
		field := after.Fields[len(after.Fields)-1]
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
		field := before.Fields[len(before.Fields)-1]
		if field.PrimaryKey {
			return relationIntentUnsupported("relation-step RemoveField operation %d must be non-primary-key", operation.OperationIndex)
		}
		if field.Kind == ir.FieldForeignKey {
			if len(operation.Targets) == 0 {
				return relationIntentIntegrity("relation RemoveField operation %d lacks target metadata", operation.OperationIndex)
			}
			retainedTargets := operation.Targets[:len(operation.Targets)-1]
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
	if !sqliteRelationSameModelIdentity(before, after) || len(after.Fields) != len(before.Fields)+1 ||
		!reflect.DeepEqual(before.Fields, after.Fields[:len(before.Fields)]) {
		return ir.Field{}, errors.New("After must append exactly one field to the same model")
	}
	return after.Fields[len(after.Fields)-1], nil
}

// deriveSQLiteRelationMutationTargets independently enforces the same
// closed authority universe as migrations core. The public intent supplies
// target metadata only for the changed field; SQLite may derive bindings for
// pre-existing source relations solely by reusing that sealed snapshot when
// every symbolic target is exactly identical. It never consults the physical
// catalog or a current runtime registry to fill missing historical metadata.
// The enclosing validator also permits at most one relation Add or Remove per
// source model in a migration step so the initial/final target prefix is
// unambiguous.
func deriveSQLiteRelationMutationTargets(
	operation migrationbackend.MigrationOperation,
	field ir.Field,
	relationBoundary ir.Model,
) ([]migrationbackend.MigrationTarget, error) {
	if field.Kind != ir.FieldForeignKey || field.Relation == nil || field.PrimaryKey || field.Default != nil ||
		(!field.Nullable && field.Relation.OnDelete != ir.DeleteProtect) {
		return nil, relationIntentUnsupported(
			"relation AddField operation %d requires a non-primary-key ForeignKey with no migration default; required fields must use PROTECT",
			operation.OperationIndex,
		)
	}
	if len(operation.Targets) != 1 || !reflect.DeepEqual(operation.Targets[0].SourceField, field) {
		return nil, relationIntentIntegrity(
			"relation operation %d requires exactly the changed-field target snapshot",
			operation.OperationIndex,
		)
	}
	changed := operation.Targets[0]
	if err := validateExactNormalizedRelationModel(changed.TargetModel); err != nil {
		return nil, relationIntentIntegrity(
			"relation AddField operation %d target model is not exact normalized IR: %v",
			operation.OperationIndex,
			err,
		)
	}
	if changed.TargetModel.Name != field.Relation.Target.ModelName {
		return nil, relationIntentIntegrity(
			"relation AddField operation %d target model name %q does not match source declaration %q",
			operation.OperationIndex,
			changed.TargetModel.Name,
			field.Relation.Target.ModelName,
		)
	}
	primaryKey, err := exactRelationTargetPrimaryKey(changed.TargetModel)
	if err != nil {
		return nil, relationIntentIntegrity("relation AddField operation %d target: %v", operation.OperationIndex, err)
	}
	if !reflect.DeepEqual(changed.TargetKey, primaryKey) {
		return nil, relationIntentIntegrity(
			"relation AddField operation %d target key is not the exact historical AutoField primary key",
			operation.OperationIndex,
		)
	}
	if len(relationFieldsInModel(changed.TargetModel)) != 0 {
		return nil, relationIntentUnsupported(
			"relation AddField operation %d target model contains nested relation fields outside the sealed target universe",
			operation.OperationIndex,
		)
	}

	relationFields := relationFieldsInModel(relationBoundary)
	if err := validateSQLiteDerivedTargetExpansionResources(relationFields, changed); err != nil {
		return nil, err
	}
	derived := make([]migrationbackend.MigrationTarget, len(relationFields))
	for index := range relationFields {
		source := relationFields[index]
		if source.Relation == nil || source.Relation.Target != field.Relation.Target {
			return nil, relationIntentUnsupported(
				"relation operation %d source contains a relation with a different symbolic target",
				operation.OperationIndex,
			)
		}
		derived[index] = migrationbackend.MigrationTarget{
			SourceField: source.Clone(),
			// These values belong to the already-cloned private intent. Reuse the
			// one immutable target snapshot across the derived bindings so an
			// R-field source and T-field target never allocate O(R*T) model
			// copies before the aggregate scanner can reject them.
			TargetModel: changed.TargetModel,
			TargetKey:   changed.TargetKey,
		}
	}
	return derived, nil
}

func validateSQLiteDerivedTargetExpansionResources(
	sources []ir.Field,
	changed migrationbackend.MigrationTarget,
) error {
	derived := sqliteRelationResourceBudget{}
	if err := derived.consumeNodes("derived relation targets", len(sources)); err != nil {
		return err
	}
	for index := range sources {
		if err := derived.scanField(fmt.Sprintf("derived relation targets[%d].source_field", index), sources[index]); err != nil {
			return err
		}
	}
	perTarget := sqliteRelationResourceBudget{}
	if err := perTarget.scanModel("derived relation target.model", changed.TargetModel); err != nil {
		return err
	}
	if err := perTarget.scanField("derived relation target.key", changed.TargetKey); err != nil {
		return err
	}
	count := uint64(len(sources))
	if perTarget.nodes != 0 && count > (sqliteRelationMaxNodes-derived.nodes)/perTarget.nodes {
		return relationIntentIntegrity("derived relation targets exceed the aggregate relation intent node limit %d", sqliteRelationMaxNodes)
	}
	if perTarget.bytes != 0 && count > (sqliteRelationMaxAggregateBytes-derived.bytes)/perTarget.bytes {
		return relationIntentIntegrity("derived relation targets exceed the aggregate relation intent byte limit %d", sqliteRelationMaxAggregateBytes)
	}
	return nil
}

func validateSQLiteRelationRemoveDelta(before, after ir.Model) (ir.Field, error) {
	if err := validateExactNormalizedRelationModel(before); err != nil {
		return ir.Field{}, fmt.Errorf("Before model is not exact normalized IR: %w", err)
	}
	if err := validateExactNormalizedRelationModel(after); err != nil {
		return ir.Field{}, fmt.Errorf("After model is not exact normalized IR: %w", err)
	}
	if !sqliteRelationSameModelIdentity(before, after) || len(before.Fields) != len(after.Fields)+1 ||
		!reflect.DeepEqual(after.Fields, before.Fields[:len(after.Fields)]) {
		return ir.Field{}, errors.New("After must remove exactly the final field from the same model")
	}
	return before.Fields[len(before.Fields)-1], nil
}

func sqliteRelationSameModelIdentity(left, right ir.Model) bool {
	return left.Name == right.Name && left.GoName == right.GoName && left.DBTable == right.DBTable
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
		wantField := operation.After.Fields[len(operation.After.Fields)-1]
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
		wantField := operation.Before.Fields[len(operation.Before.Fields)-1]
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
	changed := targets[len(targets)-1]
	if !reflect.DeepEqual(changed.SourceField, field) {
		return "", relationIntentIntegrity("relation AddField %q changed target does not match the added field", model.DBTable)
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
	relevant := make(map[string]struct{}, len(seal.intent.Operations)+len(seal.externalTargets)+2)
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
	for key := range seal.externalTargets {
		relevant[key] = struct{}{}
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
		targets, known := sqliteRelationTargetsForModel(seal, state.model)
		if !known && len(relationFieldsInModel(state.model)) != 0 {
			return relationIntentUnsupported("scalar-touched relation model %q has no exact same-step target metadata", state.model.DBTable)
		}
		if err := assertSQLiteRelationModelShape(ctx, executor, state.model, targets, known, validationCache); err != nil {
			return fmt.Errorf("preflight relation input model %q: %w", state.model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, state.model, targets, known, tableObject.sql); err != nil {
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
			field := operation.After.Fields[len(operation.After.Fields)-1]
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
			field := operation.Before.Fields[len(operation.Before.Fields)-1]
			if owner, referenced := removeDependencies.owner(operation.Before.DBTable, field.Column); referenced {
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("column %s.%s is referenced by foreign key on table %s", operation.Before.DBTable, field.Column, owner),
					nil,
				)
			}
		}
	}

	externalTables := make([]string, 0, len(seal.externalTargets))
	for tableKey := range seal.externalTargets {
		externalTables = append(externalTables, tableKey)
	}
	sort.Strings(externalTables)
	for _, tableKey := range externalTables {
		model := seal.externalTargets[tableKey].snapshot
		if err := assertSQLiteRelationNamespace(ctx, executor, catalog, model.DBTable, true); err != nil {
			return err
		}
		tableObject, exists, err := catalog.object("main", model.DBTable, "table")
		if err != nil {
			return err
		}
		if !exists || tableObject.name != model.DBTable {
			return relationPhysicalDrift("external relation target table %q is missing or differs by SQLite identifier spelling", model.DBTable)
		}
		targets, known := sqliteRelationTargetsForModel(seal, model)
		if err := assertSQLiteRelationModelShape(ctx, executor, model, targets, known, validationCache); err != nil {
			return fmt.Errorf("preflight external relation target %q: %w", model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, model, targets, known, tableObject.sql); err != nil {
			return fmt.Errorf("preflight external relation target %q SQL: %w", model.DBTable, err)
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
	for index := range seal.intent.Operations {
		operation := seal.intent.Operations[index]
		model := operation.After
		if reflect.DeepEqual(model, ir.Model{}) {
			model = operation.Before
		}
		key := sqliteRelationIdentifierKey(model.DBTable)
		if _, exists := initial[key]; !exists {
			initial[key] = sqliteRelationBoundaryModel{
				model:   modelForRelationBoundary(operation.Before, model),
				present: !reflect.DeepEqual(operation.Before, ir.Model{}),
			}
		}
		final[key] = sqliteRelationBoundaryModel{
			model:   modelForRelationBoundary(operation.After, model),
			present: !reflect.DeepEqual(operation.After, ir.Model{}),
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
) ([]migrationbackend.MigrationTarget, bool) {
	relationFields := relationFieldsInModel(model)
	if len(relationFields) == 0 {
		return nil, true
	}
	candidates := seal.targetOperationByTable[sqliteRelationIdentifierKey(model.DBTable)]
	if len(candidates) == 0 {
		return nil, false
	}
	var selected []migrationbackend.MigrationTarget
	matches := 0
	for _, operationIndex := range candidates {
		if operationIndex < 0 || operationIndex >= len(seal.intent.Operations) {
			return nil, false
		}
		operation := seal.intent.Operations[operationIndex]
		targets := operation.Targets
		if len(targets) != len(relationFields) {
			// A bounded relation Add/Remove seals the complete relation-bearing
			// boundary target list. The opposite boundary contains the exact
			// field-order prefix ending just before the changed ForeignKey; no
			// other subset is authoritative.
			if (operation.Kind != migrationbackend.MigrationAddField &&
				operation.Kind != migrationbackend.MigrationRemoveField) ||
				len(targets) != len(relationFields)+1 ||
				!sqliteRelationOperationChangesForeignKey(operation) {
				continue
			}
			targets = targets[:len(relationFields)]
		}
		matchesFields := true
		for targetIndex := range relationFields {
			if !reflect.DeepEqual(targets[targetIndex].SourceField, relationFields[targetIndex]) {
				matchesFields = false
				break
			}
		}
		if !matchesFields {
			continue
		}
		if matches != 0 && !reflect.DeepEqual(selected, targets) {
			return nil, false
		}
		matches++
		selected = targets
	}
	// Consecutive operations may expose the same model boundary (for example,
	// CreateModel followed by a scalar AddField). Identical sealed target
	// snapshots are one authority; any disagreement above fails closed.
	if matches == 0 {
		return nil, false
	}
	return selected, true
}

func sqliteRelationOperationChangesForeignKey(operation migrationbackend.MigrationOperation) bool {
	model := operation.After
	if operation.Kind == migrationbackend.MigrationRemoveField {
		model = operation.Before
	}
	return len(model.Fields) != 0 && model.Fields[len(model.Fields)-1].Kind == ir.FieldForeignKey
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
				if source == target {
					return relationPhysicalDrift("relation CreateModel table %q has a physical self relation", model.DBTable)
				}
				graph.add(source, target)
			}
		case migrationbackend.MigrationDeleteModel:
			if inbound := graph.incoming[source]; len(inbound) != 0 {
				owners := make([]string, 0, len(inbound))
				for owner := range inbound {
					owners = append(owners, owner)
				}
				sort.Strings(owners)
				return relationPhysicalDrift("relation DeleteModel table %q has inbound foreign key from %q", model.DBTable, owners[0])
			}
			graph.remove(source)
		case migrationbackend.MigrationAddField:
			if len(operation.Targets) == 0 || len(operation.After.Fields) == 0 ||
				operation.After.Fields[len(operation.After.Fields)-1].Kind != ir.FieldForeignKey {
				continue
			}
			target := sqliteRelationIdentifierKey(operation.Targets[len(operation.Targets)-1].TargetModel.DBTable)
			if source == target {
				return relationPhysicalDrift("relation AddField table %q has a physical self relation", model.DBTable)
			}
			if !graph.hasEdge(source, target) && len(graph.outgoing[target]) != 0 {
				// The sealed Add authority requires a relation-free target and its
				// exact physical-shape preflight has already enforced zero foreign
				// keys. This constant-time defensive check closes any future path
				// that could turn the new edge into a cycle without rescanning an
				// unrelated existing graph for every Add.
				return relationPhysicalDrift("relation AddField target %q has an outgoing physical relation that could create a cycle", operation.Targets[len(operation.Targets)-1].TargetModel.DBTable)
			}
			graph.add(source, target)
		case migrationbackend.MigrationRemoveField:
			if len(operation.Targets) == 0 || !sqliteRelationOperationChangesForeignKey(operation) {
				continue
			}
			if inbound := graph.incoming[source]; len(inbound) != 0 {
				owners := make([]string, 0, len(inbound))
				for owner := range inbound {
					owners = append(owners, owner)
				}
				sort.Strings(owners)
				return relationPhysicalDrift("relation RemoveField table %q has inbound foreign key from %q", model.DBTable, owners[0])
			}
			graph.removeOutgoing(source)
			for targetIndex := 0; targetIndex < len(operation.Targets)-1; targetIndex++ {
				graph.add(source, sqliteRelationIdentifierKey(operation.Targets[targetIndex].TargetModel.DBTable))
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
		targets, known := sqliteRelationTargetsForModel(seal, state.model)
		if !known && len(relationFieldsInModel(state.model)) != 0 {
			return relationIntentUnsupported("final relation model %q has no exact sealed target metadata", state.model.DBTable)
		}
		if err := assertSQLiteRelationModelShape(ctx, executor, state.model, targets, known, validationCache); err != nil {
			return fmt.Errorf("verify final relation model %q: %w", state.model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, state.model, targets, known, tableObject.sql); err != nil {
			return fmt.Errorf("verify final relation model %q SQL: %w", state.model.DBTable, err)
		}
	}

	externalTables := make([]string, 0, len(seal.externalTargets))
	for tableKey := range seal.externalTargets {
		externalTables = append(externalTables, tableKey)
	}
	sort.Strings(externalTables)
	for _, tableKey := range externalTables {
		model := seal.externalTargets[tableKey].snapshot
		if err := assertSQLiteRelationNamespace(ctx, executor, catalog, model.DBTable, true); err != nil {
			return err
		}
		tableObject, exists, err := catalog.object("main", model.DBTable, "table")
		if err != nil {
			return err
		}
		if !exists || tableObject.name != model.DBTable {
			return relationPhysicalDrift("final external relation target %q is missing or differs by SQLite identifier spelling", model.DBTable)
		}
		targets, known := sqliteRelationTargetsForModel(seal, model)
		if !known && len(relationFieldsInModel(model)) != 0 {
			return relationIntentUnsupported("final external relation target %q has no exact sealed target metadata", model.DBTable)
		}
		if err := assertSQLiteRelationModelShape(ctx, executor, model, targets, known, validationCache); err != nil {
			return fmt.Errorf("verify final external relation target %q: %w", model.DBTable, err)
		}
		if err := assertSQLiteRelationCanonicalTableSQL(ctx, executor, model, targets, known, tableObject.sql); err != nil {
			return fmt.Errorf("verify final external relation target %q SQL: %w", model.DBTable, err)
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
}

type sqliteRelationAutoKey struct {
	table  string
	column string
}

func newSQLiteRelationPhysicalValidationCache() *sqliteRelationPhysicalValidationCache {
	return &sqliteRelationPhysicalValidationCache{autoKeys: make(map[sqliteRelationAutoKey]error)}
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
	for index := range model.Fields {
		field := model.Fields[index]
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
		column := columns[index]
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
	return nil
}

func assertSQLiteRelationCanonicalTableSQL(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
	targetsKnown bool,
	actualSQL string,
) error {
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
	case ir.FieldChar:
		return fmt.Sprintf("VARCHAR(%d)", field.MaxLength), nil
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
