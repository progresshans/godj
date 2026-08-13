package migrationrelation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const relationBackendCleanupTimeout = 2 * time.Second

var (
	relationBackendErrUnsupported = errors.New("relation migration backend capability is unsupported")
	relationBackendErrIntent      = errors.New("relation migration intent is invalid")
	relationBackendErrSelf        = errors.New("self relation migration is unsupported")
	relationBackendErrCycle       = errors.New("cyclic relation migration is unsupported")
	relationBackendErrMismatch    = errors.New("relation migration change does not match pinned intent")
)

type relationBackendChangeKind uint8

const (
	relationBackendCreateModel relationBackendChangeKind = iota + 1
	relationBackendDeleteModel
	relationBackendAddField
	relationBackendRemoveField
)

type relationBackendColumn struct {
	Name          string
	Type          string
	MaxLength     int
	Nullable      bool
	NotNull       bool
	PrimaryKey    bool
	AutoIncrement bool
	Position      int
}

type relationBackendDeletePolicy uint8

const (
	relationBackendProtect relationBackendDeletePolicy = iota + 1
	relationBackendSetNull
)

type relationBackendRelation struct {
	Name         string
	Column       string
	TargetTable  string
	TargetColumn string
	Nullable     bool
	OnDelete     relationBackendDeletePolicy
	Position     int
}

type relationBackendModel struct {
	Table     string
	Columns   []relationBackendColumn
	Relations []relationBackendRelation
}

func (model relationBackendModel) relationBackendClone() relationBackendModel {
	clone := model
	if model.Columns != nil {
		clone.Columns = make([]relationBackendColumn, len(model.Columns))
		copy(clone.Columns, model.Columns)
	}
	if model.Relations != nil {
		clone.Relations = make([]relationBackendRelation, len(model.Relations))
		copy(clone.Relations, model.Relations)
	}
	return clone
}

type relationBackendChange struct {
	Kind     relationBackendChangeKind
	Before   relationBackendModel
	After    relationBackendModel
	Relation relationBackendRelation
}

func (change relationBackendChange) relationBackendClone() relationBackendChange {
	clone := change
	clone.Before = change.Before.relationBackendClone()
	clone.After = change.After.relationBackendClone()
	return clone
}

type relationBackendStepIntent struct {
	App     string
	Name    string
	Changes []relationBackendChange
}

func (intent relationBackendStepIntent) relationBackendClone() relationBackendStepIntent {
	clone := intent
	if intent.Changes != nil {
		clone.Changes = make([]relationBackendChange, len(intent.Changes))
		for index := range intent.Changes {
			clone.Changes[index] = intent.Changes[index].relationBackendClone()
		}
	}
	return clone
}

type relationBackendDirection uint8

const (
	relationBackendApply relationBackendDirection = iota + 1
	relationBackendUnapply
)

type relationBackendTransition struct {
	App          string
	Name         string
	Direction    relationBackendDirection
	FromRevision int64
	ToRevision   int64
}

type relationBackendCapabilities struct {
	Profile               uint8
	CreateModel           bool
	NullableAddField      bool
	EmptyRequiredAddField bool
	BoundedRemake         bool
}

func (capabilities relationBackendCapabilities) relationBackendSupports(intent relationBackendStepIntent) bool {
	if capabilities.Profile != 1 {
		return false
	}
	for _, change := range intent.Changes {
		switch change.Kind {
		case relationBackendCreateModel, relationBackendDeleteModel:
			if !capabilities.CreateModel {
				return false
			}
		case relationBackendAddField:
			if change.Relation.Nullable && !capabilities.NullableAddField {
				return false
			}
			if !change.Relation.Nullable && !capabilities.EmptyRequiredAddField {
				return false
			}
		case relationBackendRemoveField:
			if !capabilities.BoundedRemake {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type relationBackendOptionalBackend interface {
	RelationMigrationCapabilities() relationBackendCapabilities
	OpenRelationMigrationSession(context.Context) (relationBackendOptionalSession, error)
}

type relationBackendOptionalSession interface {
	BeginRelationFencedMigration(
		context.Context,
		relationBackendTransition,
		relationBackendStepIntent,
	) (relationBackendTransaction, error)
	Close(context.Context) error
}

type relationBackendTransaction interface {
	ApplyRelationChange(context.Context, relationBackendChange) error
	RecordRelationTransition(context.Context) error
	CommitRelationFenced(context.Context) (migrationbackend.CommitOutcome, error)
	RollbackRelation(context.Context) error
}

type relationBackendOpenedStep struct {
	Session     relationBackendOptionalSession
	Transaction relationBackendTransaction
}

func relationBackendOpenStep(
	ctx context.Context,
	candidate any,
	transition relationBackendTransition,
	intent relationBackendStepIntent,
) (relationBackendOpenedStep, error) {
	if ctx == nil {
		return relationBackendOpenedStep{}, fmt.Errorf("%w: context is nil", relationBackendErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, err
	}
	if err := relationBackendValidateResourceShape(intent); err != nil {
		return relationBackendOpenedStep{}, err
	}
	prepared := intent.relationBackendClone()
	if err := relationBackendValidateIntent(prepared); err != nil {
		return relationBackendOpenedStep{}, err
	}
	if err := relationBackendValidateTransition(transition, prepared); err != nil {
		return relationBackendOpenedStep{}, err
	}
	backend, ok := candidate.(relationBackendOptionalBackend)
	if !ok || relationBackendNilInterface(backend) || !backend.RelationMigrationCapabilities().relationBackendSupports(prepared) {
		return relationBackendOpenedStep{}, relationBackendErrUnsupported
	}
	session, err := backend.OpenRelationMigrationSession(ctx)
	if err != nil {
		primary := fmt.Errorf("open relation migration session: %w", err)
		if relationBackendNilInterface(session) {
			return relationBackendOpenedStep{}, primary
		}
		return relationBackendOpenedStep{}, relationBackendCloseSession(ctx, session, primary)
	}
	if relationBackendNilInterface(session) {
		return relationBackendOpenedStep{}, errors.New("open relation migration session returned nil session")
	}
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, relationBackendCloseSession(ctx, session, err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, prepared)
	if err != nil {
		primary := fmt.Errorf("begin relation migration: %w", err)
		if relationBackendNilInterface(transaction) {
			return relationBackendOpenedStep{}, relationBackendCloseSession(ctx, session, primary)
		}
		return relationBackendOpenedStep{}, relationBackendRollbackAndClose(ctx, session, transaction, primary)
	}
	if relationBackendNilInterface(transaction) {
		return relationBackendOpenedStep{}, relationBackendCloseSession(
			ctx,
			session,
			errors.New("begin relation migration returned nil transaction"),
		)
	}
	if err := ctx.Err(); err != nil {
		return relationBackendOpenedStep{}, relationBackendRollbackAndClose(ctx, session, transaction, err)
	}
	return relationBackendOpenedStep{Session: session, Transaction: transaction}, nil
}

func relationBackendCloseSession(
	ctx context.Context,
	session relationBackendOptionalSession,
	primary error,
) error {
	cleanupCtx, cancel := relationBackendDetachedCleanupContext(ctx)
	defer cancel()
	return errors.Join(primary, session.Close(cleanupCtx))
}

func relationBackendRollbackAndClose(
	ctx context.Context,
	session relationBackendOptionalSession,
	transaction relationBackendTransaction,
	primary error,
) error {
	rollbackCtx, cancelRollback := relationBackendDetachedCleanupContext(ctx)
	rollbackErr := transaction.RollbackRelation(rollbackCtx)
	cancelRollback()

	cleanupCtx, cancel := relationBackendDetachedCleanupContext(ctx)
	defer cancel()
	return errors.Join(
		primary,
		rollbackErr,
		session.Close(cleanupCtx),
	)
}

func relationBackendNilInterface(value any) bool {
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

func relationBackendDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), relationBackendCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), relationBackendCleanupTimeout)
}

func relationBackendValidateTransition(
	transition relationBackendTransition,
	intent relationBackendStepIntent,
) error {
	if transition.App != intent.App || transition.Name != intent.Name {
		return fmt.Errorf("%w: transition identity does not match intent", relationBackendErrIntent)
	}
	if transition.Direction != relationBackendApply && transition.Direction != relationBackendUnapply {
		return fmt.Errorf("%w: transition direction is invalid", relationBackendErrIntent)
	}
	if transition.FromRevision < 0 || transition.FromRevision == math.MaxInt64 ||
		transition.ToRevision != transition.FromRevision+1 {
		return fmt.Errorf("%w: transition revision successor is invalid", relationBackendErrIntent)
	}
	return nil
}

func relationBackendValidateIntent(intent relationBackendStepIntent) error {
	if err := relationBackendValidateResourceShape(intent); err != nil {
		return err
	}
	if intent.App == "" || intent.Name == "" || len(intent.Changes) == 0 {
		return fmt.Errorf("%w: missing identity or changes", relationBackendErrIntent)
	}
	type virtualModel struct {
		model   relationBackendModel
		present bool
	}
	effectiveModels := make(map[string]relationBackendModel)
	orderedModels := make(map[string]virtualModel)
	deletedTables := make(map[string]bool)
	tableSpellings := make(map[string]string)
	for index, change := range intent.Changes {
		if err := relationBackendValidateChange(change); err != nil {
			return fmt.Errorf("%w: change %d: %w", relationBackendErrIntent, index, err)
		}
		table := change.After.Table
		if change.Kind == relationBackendDeleteModel {
			table = change.Before.Table
		}
		tableKey := relationBackendIdentifierKey(table)
		if spelling, known := tableSpellings[tableKey]; known && spelling != table {
			return fmt.Errorf(
				"%w: change %d table %q case-fold collides with %q",
				relationBackendErrIntent, index, table, spelling,
			)
		}
		tableSpellings[tableKey] = table
		switch change.Kind {
		case relationBackendCreateModel:
			if prior, known := orderedModels[tableKey]; known && prior.present {
				return fmt.Errorf("%w: change %d creates present table %q", relationBackendErrIntent, index, change.After.Table)
			}
			orderedModels[tableKey] = virtualModel{model: change.After.relationBackendClone(), present: true}
			effectiveModels[tableKey] = change.After.relationBackendClone()
			delete(deletedTables, tableKey)
		case relationBackendDeleteModel:
			if prior, known := orderedModels[tableKey]; known {
				if !prior.present || !relationBackendModelsEqual(prior.model, change.Before) {
					return fmt.Errorf("%w: change %d before-state is not the ordered state for %q", relationBackendErrIntent, index, change.Before.Table)
				}
			}
			if source, inbound := relationBackendEffectiveInbound(effectiveModels, tableKey); inbound {
				return fmt.Errorf(
					"%w: change %d deletes table %q while current source %q still references it",
					relationBackendErrIntent, index, change.Before.Table, source,
				)
			}
			orderedModels[tableKey] = virtualModel{present: false}
			delete(effectiveModels, tableKey)
			deletedTables[tableKey] = true
		case relationBackendAddField, relationBackendRemoveField:
			if prior, known := orderedModels[tableKey]; known {
				if !prior.present || !relationBackendModelsEqual(prior.model, change.Before) {
					return fmt.Errorf("%w: change %d before-state is not the ordered state for %q", relationBackendErrIntent, index, change.Before.Table)
				}
			}
			orderedModels[tableKey] = virtualModel{model: change.After.relationBackendClone(), present: true}
			effectiveModels[tableKey] = change.After.relationBackendClone()
		}
		if err := relationBackendValidateEffectiveGraph(effectiveModels); err != nil {
			return fmt.Errorf("%w: change %d: %w", relationBackendErrIntent, index, err)
		}
		if source, target, dangling := relationBackendDeletedTargetReference(effectiveModels, deletedTables); dangling {
			return fmt.Errorf(
				"%w: change %d source %q references deleted target %q",
				relationBackendErrIntent, index, source, target,
			)
		}
	}
	return nil
}

// The product source decoder accepts at most profileMaxOperations operations
// per migration and profileMaxFields fields per CreateModel. The downstream
// backend candidate repeats those bounds before cloning caller-owned slices so
// an in-memory or external implementation cannot bypass the raw decoder and
// turn validation into an unbounded allocation or graph walk.
func relationBackendValidateResourceShape(intent relationBackendStepIntent) error {
	if len(intent.Changes) > profileMaxOperations {
		return fmt.Errorf(
			"%w: changes resource limit exceeded: %d > %d",
			relationBackendErrIntent,
			len(intent.Changes),
			profileMaxOperations,
		)
	}
	identifierBytes := 0
	addIdentifier := func(value, location string) error {
		if len(value) > migrationdefinition.MaxSourceIDBytes {
			return fmt.Errorf(
				"%w: %s byte resource limit exceeded: %d > %d",
				relationBackendErrIntent,
				location,
				len(value),
				migrationdefinition.MaxSourceIDBytes,
			)
		}
		if len(value) > migrationdefinition.MaxDocumentBytes-identifierBytes {
			return fmt.Errorf(
				"%w: aggregate identifier byte resource limit exceeded: > %d",
				relationBackendErrIntent,
				migrationdefinition.MaxDocumentBytes,
			)
		}
		identifierBytes += len(value)
		return nil
	}
	if err := addIdentifier(intent.App, "app identity"); err != nil {
		return err
	}
	if err := addIdentifier(intent.Name, "migration identity"); err != nil {
		return err
	}

	// Validation intentionally checks the effective relation graph after every
	// ordered change so a relation that is added and later removed cannot hide a
	// transient self-edge or cycle. Account for that repeated walk up front. The
	// cap keeps the semantic guarantee while preventing individually valid model
	// snapshots from composing into quadratic, unbounded validation work.
	effectiveMembers := make(map[string]int, len(intent.Changes))
	effectiveNodeCount := 0
	aggregateSnapshotNodes := 0
	aggregateGraphNodes := 0
	for index := range intent.Changes {
		change := &intent.Changes[index]
		if aggregateSnapshotNodes == migrationdefinition.MaxJSONValues {
			return fmt.Errorf(
				"%w: aggregate snapshot node resource limit exceeded: > %d",
				relationBackendErrIntent,
				migrationdefinition.MaxJSONValues,
			)
		}
		aggregateSnapshotNodes++
		if err := addIdentifier(change.Relation.Name, fmt.Sprintf("change %d relation name", index)); err != nil {
			return err
		}
		if err := addIdentifier(change.Relation.Column, fmt.Sprintf("change %d relation column", index)); err != nil {
			return err
		}
		if err := addIdentifier(change.Relation.TargetTable, fmt.Sprintf("change %d relation target table", index)); err != nil {
			return err
		}
		if err := addIdentifier(change.Relation.TargetColumn, fmt.Sprintf("change %d relation target column", index)); err != nil {
			return err
		}
		for _, snapshot := range []struct {
			name  string
			model *relationBackendModel
		}{
			{name: "before", model: &change.Before},
			{name: "after", model: &change.After},
		} {
			if len(snapshot.model.Columns) > profileMaxFields ||
				len(snapshot.model.Relations) > profileMaxFields-len(snapshot.model.Columns) {
				members := len(snapshot.model.Columns) + len(snapshot.model.Relations)
				return fmt.Errorf(
					"%w: change %d %s-model field resource limit exceeded: %d > %d",
					relationBackendErrIntent,
					index,
					snapshot.name,
					members,
					profileMaxFields,
				)
			}
			members := len(snapshot.model.Columns) + len(snapshot.model.Relations)
			if members > migrationdefinition.MaxJSONValues-aggregateSnapshotNodes {
				return fmt.Errorf(
					"%w: aggregate snapshot node resource limit exceeded: > %d",
					relationBackendErrIntent,
					migrationdefinition.MaxJSONValues,
				)
			}
			aggregateSnapshotNodes += members
			if err := addIdentifier(snapshot.model.Table, fmt.Sprintf("change %d %s table", index, snapshot.name)); err != nil {
				return err
			}
			for memberIndex := range snapshot.model.Columns {
				column := &snapshot.model.Columns[memberIndex]
				if err := addIdentifier(column.Name, fmt.Sprintf("change %d %s column %d name", index, snapshot.name, memberIndex)); err != nil {
					return err
				}
				if err := addIdentifier(column.Type, fmt.Sprintf("change %d %s column %d type", index, snapshot.name, memberIndex)); err != nil {
					return err
				}
			}
			for memberIndex := range snapshot.model.Relations {
				relation := &snapshot.model.Relations[memberIndex]
				for _, identifier := range []struct {
					name  string
					value string
				}{
					{name: "name", value: relation.Name},
					{name: "column", value: relation.Column},
					{name: "target table", value: relation.TargetTable},
					{name: "target column", value: relation.TargetColumn},
				} {
					if err := addIdentifier(
						identifier.value,
						fmt.Sprintf("change %d %s relation %d %s", index, snapshot.name, memberIndex, identifier.name),
					); err != nil {
						return err
					}
				}
			}
		}

		table := change.After.Table
		members := len(change.After.Columns) + len(change.After.Relations)
		if change.Kind == relationBackendDeleteModel {
			table = change.Before.Table
			members = 0
		}
		tableKey := relationBackendIdentifierKey(table)
		priorMembers, known := effectiveMembers[tableKey]
		if known {
			effectiveNodeCount -= priorMembers + 1
		}
		if change.Kind == relationBackendDeleteModel {
			delete(effectiveMembers, tableKey)
		} else {
			effectiveMembers[tableKey] = members
			if members > migrationdefinition.MaxJSONValues-effectiveNodeCount-1 {
				return fmt.Errorf(
					"%w: effective graph node resource limit exceeded: > %d",
					relationBackendErrIntent,
					migrationdefinition.MaxJSONValues,
				)
			}
			effectiveNodeCount += members + 1
		}
		if effectiveNodeCount > migrationdefinition.MaxJSONValues-aggregateGraphNodes {
			return fmt.Errorf(
				"%w: aggregate graph validation resource limit exceeded: > %d",
				relationBackendErrIntent,
				migrationdefinition.MaxJSONValues,
			)
		}
		aggregateGraphNodes += effectiveNodeCount
	}
	return nil
}

func relationBackendDeletedTargetReference(
	effectiveModels map[string]relationBackendModel,
	deletedTables map[string]bool,
) (string, string, bool) {
	sources := make([]string, 0, len(effectiveModels))
	for sourceKey := range effectiveModels {
		sources = append(sources, sourceKey)
	}
	sort.Strings(sources)
	for _, sourceKey := range sources {
		model := effectiveModels[sourceKey]
		for _, relation := range model.Relations {
			if deletedTables[relationBackendIdentifierKey(relation.TargetTable)] {
				return model.Table, relation.TargetTable, true
			}
		}
	}
	return "", "", false
}

func relationBackendEffectiveInbound(
	effectiveModels map[string]relationBackendModel,
	targetKey string,
) (string, bool) {
	sources := make([]string, 0, len(effectiveModels))
	for sourceKey := range effectiveModels {
		sources = append(sources, sourceKey)
	}
	sort.Strings(sources)
	for _, sourceKey := range sources {
		if sourceKey == targetKey {
			continue
		}
		model := effectiveModels[sourceKey]
		for _, relation := range model.Relations {
			if relationBackendIdentifierKey(relation.TargetTable) == targetKey {
				return model.Table, true
			}
		}
	}
	return "", false
}

func relationBackendValidateEffectiveGraph(effectiveModels map[string]relationBackendModel) error {
	tables := make([]string, 0, len(effectiveModels))
	for table := range effectiveModels {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, source := range tables {
		model := effectiveModels[source]
		for _, relation := range model.Relations {
			if relationBackendIdentifierKey(relation.TargetTable) == source {
				return fmt.Errorf("%w: table %q", relationBackendErrSelf, source)
			}
			target, local := effectiveModels[relationBackendIdentifierKey(relation.TargetTable)]
			if !local {
				continue
			}
			if err := relationBackendValidateLocalTargetColumn(target, relation.TargetColumn); err != nil {
				return fmt.Errorf(
					"relation %q on table %q has invalid local target %q.%q: %w",
					relation.Name,
					model.Table,
					relation.TargetTable,
					relation.TargetColumn,
					err,
				)
			}
		}
	}

	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) error
	visit = func(table string) error {
		if visiting[table] {
			return fmt.Errorf("%w: table %q", relationBackendErrCycle, table)
		}
		if visited[table] {
			return nil
		}
		visiting[table] = true
		model := effectiveModels[table]
		for _, relation := range model.Relations {
			targetKey := relationBackendIdentifierKey(relation.TargetTable)
			if _, local := effectiveModels[targetKey]; local {
				if err := visit(targetKey); err != nil {
					return err
				}
			}
		}
		visiting[table] = false
		visited[table] = true
		return nil
	}
	for _, table := range tables {
		if err := visit(table); err != nil {
			return err
		}
	}
	return nil
}

func relationBackendValidateLocalTargetColumn(model relationBackendModel, targetColumn string) error {
	targetKey := relationBackendIdentifierKey(targetColumn)
	for _, column := range model.Columns {
		if relationBackendIdentifierKey(column.Name) != targetKey {
			continue
		}
		if column.Type != "INTEGER" || column.MaxLength != 0 || column.Nullable || !column.NotNull ||
			!column.PrimaryKey || !column.AutoIncrement {
			return errors.New("target column is not the exact Auto INTEGER primary-key shape")
		}
		return nil
	}
	return errors.New("target column does not exist")
}

func relationBackendValidateChange(change relationBackendChange) error {
	switch change.Kind {
	case relationBackendCreateModel:
		if change.After.Table == "" || change.Before.Table != "" {
			return errors.New("create-model snapshots are invalid")
		}
		if change.Relation != (relationBackendRelation{}) {
			return errors.New("create-model carries a relation-field union payload")
		}
		return relationBackendValidateModel(change.After)
	case relationBackendDeleteModel:
		if change.Before.Table == "" || change.After.Table != "" {
			return errors.New("delete-model snapshots are invalid")
		}
		if change.Relation != (relationBackendRelation{}) {
			return errors.New("delete-model carries a relation-field union payload")
		}
		return relationBackendValidateModel(change.Before)
	case relationBackendAddField, relationBackendRemoveField:
		if change.Before.Table == "" || change.Before.Table != change.After.Table {
			return errors.New("field-change snapshots are invalid")
		}
		if err := relationBackendValidateModel(change.Before); err != nil {
			return err
		}
		if err := relationBackendValidateModel(change.After); err != nil {
			return err
		}
		if change.Relation.Column == "" || change.Relation.TargetTable == "" || change.Relation.TargetColumn == "" {
			return errors.New("relation target is incomplete")
		}
		if change.Relation.OnDelete != relationBackendProtect && change.Relation.OnDelete != relationBackendSetNull {
			return errors.New("relation delete policy is invalid")
		}
		if change.Relation.OnDelete == relationBackendSetNull && !change.Relation.Nullable {
			return errors.New("SET_NULL relation must be nullable")
		}
		return relationBackendValidateExactDelta(change)
	default:
		return errors.New("change kind is invalid")
	}
}

func relationBackendValidateExactDelta(change relationBackendChange) error {
	switch change.Kind {
	case relationBackendAddField:
		if !relationBackendColumnsEqual(change.Before.Columns, change.After.Columns) {
			return errors.New("AddField modified non-relation columns")
		}
		source := change.After.Relations
		retained := change.Before.Relations
		if len(source) != len(retained)+1 {
			return errors.New("AddField must add exactly one relation")
		}
		for _, column := range change.Before.Columns {
			if change.Relation.Position <= column.Position {
				return errors.New("native relation AddField must append after every retained field")
			}
		}
		for _, relation := range change.Before.Relations {
			if change.Relation.Position <= relation.Position {
				return errors.New("native relation AddField must append after every retained field")
			}
		}
		matches := 0
		for index := range source {
			if !reflect.DeepEqual(source[index], change.Relation) {
				continue
			}
			withoutDeclared := make([]relationBackendRelation, 0, len(source)-1)
			withoutDeclared = append(withoutDeclared, source[:index]...)
			withoutDeclared = append(withoutDeclared, source[index+1:]...)
			if relationBackendRelationsEqual(withoutDeclared, retained) {
				matches++
			}
		}
		if matches != 1 {
			return errors.New("field change is not the exact declared relation delta")
		}
		return nil
	case relationBackendRemoveField:
		if len(change.Before.Relations) != len(change.After.Relations)+1 {
			return errors.New("RemoveField must remove exactly one relation")
		}
		matches := 0
		for index := range change.Before.Relations {
			if !reflect.DeepEqual(change.Before.Relations[index], change.Relation) {
				continue
			}
			expected := change.Before.relationBackendClone()
			expected.Columns = append([]relationBackendColumn(nil), change.Before.Columns...)
			for columnIndex := range expected.Columns {
				if expected.Columns[columnIndex].Position > change.Relation.Position {
					expected.Columns[columnIndex].Position--
				}
			}
			expected.Relations = make([]relationBackendRelation, 0, len(change.Before.Relations)-1)
			expected.Relations = append(expected.Relations, change.Before.Relations[:index]...)
			expected.Relations = append(expected.Relations, change.Before.Relations[index+1:]...)
			for relationIndex := range expected.Relations {
				if expected.Relations[relationIndex].Position > change.Relation.Position {
					expected.Relations[relationIndex].Position--
				}
			}
			if relationBackendModelsEqual(expected, change.After) {
				matches++
			}
		}
		if matches != 1 {
			return errors.New("field change is not the exact declared relation delta with deterministic position compaction")
		}
		return nil
	default:
		return errors.New("exact relation delta requires AddField or RemoveField")
	}
}

func relationBackendValidateModel(model relationBackendModel) error {
	if !relationBackendValidIdentifier(model.Table) {
		return fmt.Errorf("model table %q is not a normalized database identifier", model.Table)
	}
	seen := make(map[string]struct{}, len(model.Columns)+len(model.Relations))
	seenPositions := make(map[int]struct{}, len(model.Columns)+len(model.Relations))
	seenRelationNames := make(map[string]struct{}, len(model.Relations))
	fieldCount := len(model.Columns) + len(model.Relations)
	primaryKeys := 0
	validAutoPrimaryKey := false
	for _, column := range model.Columns {
		if !relationBackendValidIdentifier(column.Name) {
			return fmt.Errorf("model column %q is not a normalized database identifier", column.Name)
		}
		if column.Position <= 0 || column.Position > fieldCount {
			return fmt.Errorf("model column %q has invalid physical position %d", column.Name, column.Position)
		}
		if _, duplicate := seenPositions[column.Position]; duplicate {
			return fmt.Errorf("duplicate physical position %d", column.Position)
		}
		seenPositions[column.Position] = struct{}{}
		if column.Type != "INTEGER" && column.Type != "VARCHAR" && column.Type != "BOOLEAN" {
			return fmt.Errorf("model column %q has unsupported type %q", column.Name, column.Type)
		}
		columnKey := relationBackendIdentifierKey(column.Name)
		if _, duplicate := seen[columnKey]; duplicate {
			return fmt.Errorf("duplicate column %q", column.Name)
		}
		seen[columnKey] = struct{}{}
		switch column.Type {
		case "INTEGER":
			if !column.PrimaryKey || !column.AutoIncrement || !column.NotNull || column.Nullable || column.MaxLength != 0 {
				return fmt.Errorf("INTEGER column %q is not the closed AutoField shape", column.Name)
			}
		case "VARCHAR":
			if column.PrimaryKey || column.AutoIncrement || column.MaxLength <= 0 || column.NotNull == column.Nullable {
				return fmt.Errorf("VARCHAR column %q is not the closed CharField shape", column.Name)
			}
		case "BOOLEAN":
			if column.PrimaryKey || column.AutoIncrement || column.MaxLength != 0 || column.Nullable || !column.NotNull {
				return fmt.Errorf("BOOLEAN column %q is not the closed BooleanField shape", column.Name)
			}
		}
		if column.PrimaryKey {
			primaryKeys++
			validAutoPrimaryKey = column.Type == "INTEGER" && column.AutoIncrement && column.NotNull
		}
		if column.AutoIncrement && !column.PrimaryKey {
			return fmt.Errorf("column %q autoincrements without primary key", column.Name)
		}
	}
	if primaryKeys != 1 {
		return fmt.Errorf("model has %d primary keys", primaryKeys)
	}
	if !validAutoPrimaryKey {
		return errors.New("model primary key must be INTEGER AUTOINCREMENT")
	}
	for _, relation := range model.Relations {
		if !relationBackendValidIdentifier(relation.Name) ||
			!relationBackendValidIdentifier(relation.Column) ||
			!relationBackendValidIdentifier(relation.TargetTable) ||
			!relationBackendValidIdentifier(relation.TargetColumn) {
			return fmt.Errorf("model relation %q has a non-normalized database identifier", relation.Name)
		}
		if relation.OnDelete != relationBackendProtect && relation.OnDelete != relationBackendSetNull {
			return fmt.Errorf("relation %q delete policy is invalid", relation.Name)
		}
		if relation.Position <= 0 || relation.Position > fieldCount {
			return fmt.Errorf("relation %q has invalid physical position %d", relation.Name, relation.Position)
		}
		if _, duplicate := seenPositions[relation.Position]; duplicate {
			return fmt.Errorf("duplicate physical position %d", relation.Position)
		}
		seenPositions[relation.Position] = struct{}{}
		columnKey := relationBackendIdentifierKey(relation.Column)
		if _, duplicate := seen[columnKey]; duplicate {
			return fmt.Errorf("duplicate column %q", relation.Column)
		}
		seen[columnKey] = struct{}{}
		relationKey := relationBackendIdentifierKey(relation.Name)
		if _, duplicate := seenRelationNames[relationKey]; duplicate {
			return fmt.Errorf("duplicate relation name %q", relation.Name)
		}
		seenRelationNames[relationKey] = struct{}{}
		if relation.OnDelete == relationBackendSetNull && !relation.Nullable {
			return fmt.Errorf("relation %q uses SET_NULL without nullability", relation.Name)
		}
	}
	return nil
}

// relationBackendValidIdentifier mirrors the normalized Schema IR database
// identifier language. Keeping this boundary closed before capability/session
// I/O also makes the conservative SQLite schema-object scan sound: quoted
// escapes, comments, control bytes, and alternate identifier spellings cannot
// enter a candidate intent and evade table/view hazard matching.
func relationBackendValidIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for index := 0; index < len(identifier); index++ {
		value := identifier[index]
		if index == 0 {
			if value != '_' && (value < 'a' || value > 'z') {
				return false
			}
			continue
		}
		if value != '_' && (value < 'a' || value > 'z') && (value < '0' || value > '9') {
			return false
		}
	}
	return true
}

// SQLite resolves unquoted and quoted identifiers with ASCII case-insensitive
// equality. Keep original spelling for emitted SQL, but use this key for every
// identity/collision decision so the candidate cannot describe two physical
// objects that SQLite treats as one.
func relationBackendIdentifierKey(identifier string) string {
	bytes := []byte(identifier)
	for index, value := range bytes {
		if value >= 'A' && value <= 'Z' {
			bytes[index] = value + ('a' - 'A')
		}
	}
	return string(bytes)
}

func relationBackendChangesEqual(left, right relationBackendChange) bool {
	return left.Kind == right.Kind &&
		relationBackendModelsEqual(left.Before, right.Before) &&
		relationBackendModelsEqual(left.After, right.After) &&
		reflect.DeepEqual(left.Relation, right.Relation)
}

func relationBackendModelsEqual(left, right relationBackendModel) bool {
	return left.Table == right.Table &&
		relationBackendColumnsEqual(left.Columns, right.Columns) &&
		relationBackendRelationsEqual(left.Relations, right.Relations)
}

func relationBackendColumnsEqual(left, right []relationBackendColumn) bool {
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

func relationBackendRelationsEqual(left, right []relationBackendRelation) bool {
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

// relationBackendLegacyScalarBackend intentionally implements only the
// already-published scalar revision-fenced ports. Candidate relation support is
// additive: this fake must continue to compile without any relation method.
type relationBackendLegacyScalarBackend struct {
	openCalls int
}

var _ migrationbackend.RevisionFencedBackend = (*relationBackendLegacyScalarBackend)(nil)

func (backend *relationBackendLegacyScalarBackend) OpenRevisionFencedSession(context.Context) (migrationbackend.RevisionFencedSession, error) {
	backend.openCalls++
	return &relationBackendLegacyScalarSession{}, nil
}

type relationBackendLegacyScalarSession struct{}

var _ migrationbackend.RevisionFencedSession = (*relationBackendLegacyScalarSession)(nil)

func (*relationBackendLegacyScalarSession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return nil, nil
}

func (*relationBackendLegacyScalarSession) BeginFencedMigration(context.Context, migrationbackend.HistoryTransition) (migrationbackend.RevisionFencedTransaction, error) {
	return &relationBackendLegacyScalarTransaction{}, nil
}

func (*relationBackendLegacyScalarSession) Close(context.Context) error { return nil }

type relationBackendLegacyScalarTransaction struct{}

var _ migrationbackend.RevisionFencedTransaction = (*relationBackendLegacyScalarTransaction)(nil)

func (*relationBackendLegacyScalarTransaction) CreateModel(context.Context, ir.Model) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) DeleteModel(context.Context, ir.Model) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) RecordApplied(context.Context, string, string) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) RecordUnapplied(context.Context, string, string) error {
	return nil
}
func (*relationBackendLegacyScalarTransaction) CommitFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil
}
func (*relationBackendLegacyScalarTransaction) Rollback(context.Context) error { return nil }
