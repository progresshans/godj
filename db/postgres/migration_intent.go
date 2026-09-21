package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/irresource"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const (
	postgresMigrationMaxOperations     = 2_048
	postgresMigrationMaxFields         = 2_048
	postgresMigrationMaxTargets        = 2_048
	postgresMigrationMaxStringBytes    = 1 << 20
	postgresMigrationMaxAggregateBytes = 16 << 20
	postgresMigrationMaxNodes          = 262_144
	postgresMigrationRecorderMaxChars  = 255
	postgresMigrationRecorderMaxBytes  = postgresMigrationRecorderMaxChars * utf8.UTFMax
	// PostgreSQL's absolute attribute-slot limit is 1,600, while the usable
	// logical column count can be much lower for wider types. The current
	// bounded profile uses the documented conservative floor instead of
	// treating the absolute slot count as generally usable for logical fields.
	postgresMigrationMaxModelFields    = 250
	postgresMigrationMaxAttributeSlots = 1_600
	postgresMigrationMaxVarcharChars   = 10_485_760
)

type postgresMigrationSchema struct {
	transition       migrationbackend.HistoryTransition
	intent           migrationbackend.MigrationIntent
	digest           [sha256.Size]byte
	operationDigests [][sha256.Size]byte
	namespace        string
	initial          postgresMigrationBoundary
	final            postgresMigrationBoundary
	cursor           int
	preflight        bool
	verified         bool
}

type postgresMigrationBoundary struct {
	models  map[string]ir.Model
	targets map[string][]migrationbackend.MigrationTarget
}

type preparedPostgresMigrationIntent struct {
	intent  migrationbackend.MigrationIntent
	digest  [sha256.Size]byte
	initial postgresMigrationBoundary
	final   postgresMigrationBoundary
}

type postgresMigrationSealPayload struct {
	Transition migrationbackend.HistoryTransition `json:"transition"`
	Intent     migrationbackend.MigrationIntent   `json:"intent"`
}

func postgresMigrationIntentIntegrity(detail string, cause error) error {
	if cause == nil {
		cause = errors.New(detail)
	} else {
		cause = fmt.Errorf("%s: %w", detail, cause)
	}
	return &migrationbackend.RevisionFenceError{
		Kind:  migrationbackend.RevisionFenceFailureIntegrity,
		Cause: cause,
	}
}

func newPostgresMigrationSchema(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (*postgresMigrationSchema, error) {
	if err := validatePostgresMigrationRecorderIdentity(transition.Migration); err != nil {
		return nil, err
	}
	prepared, err := preparePostgresMigrationIntent(transition, intent)
	if err != nil {
		return nil, err
	}
	operationDigests := make([][sha256.Size]byte, len(prepared.intent.Operations))
	for index, operation := range prepared.intent.Operations {
		operationDigests[index], err = hashPostgresMigrationOperation(transition, operation)
		if err != nil {
			return nil, postgresMigrationIntentIntegrity("seal PostgreSQL migration operation", err)
		}
	}
	return &postgresMigrationSchema{
		transition:       transition,
		intent:           prepared.intent,
		digest:           prepared.digest,
		operationDigests: operationDigests,
		initial:          prepared.initial,
		final:            prepared.final,
	}, nil
}

// preparePostgresMigrationIntent owns the immutable, database-free intent
// validation shared by execution and SQL projection. Recorder-specific
// identity limits stay with newPostgresMigrationSchema; a renderer follows the
// loader-owned definition identity bounds instead.
func preparePostgresMigrationIntent(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (preparedPostgresMigrationIntent, error) {
	if transition.Kind != migrationbackend.HistoryTransitionApply &&
		transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return preparedPostgresMigrationIntent{}, postgresMigrationIntentIntegrity(fmt.Sprintf("history transition kind %d is invalid", transition.Kind), nil)
	}
	if intent.Operations == nil {
		return preparedPostgresMigrationIntent{}, postgresMigrationIntentIntegrity("migration intent operations are missing", nil)
	}
	if err := scanPostgresMigrationResources(transition, intent); err != nil {
		return preparedPostgresMigrationIntent{}, err
	}
	cloned := intent.Clone()
	initial, final, err := validatePostgresMigrationIntent(transition, &cloned)
	if err != nil {
		return preparedPostgresMigrationIntent{}, err
	}
	if err := scanPostgresMigrationResources(transition, cloned); err != nil {
		return preparedPostgresMigrationIntent{}, err
	}
	if err := validatePostgresConstraintNames(cloned); err != nil {
		return preparedPostgresMigrationIntent{}, err
	}
	digest, err := hashPostgresMigrationIntent(transition, cloned)
	if err != nil {
		return preparedPostgresMigrationIntent{}, postgresMigrationIntentIntegrity("seal PostgreSQL migration intent", err)
	}
	return preparedPostgresMigrationIntent{
		intent:  cloned,
		digest:  digest,
		initial: initial,
		final:   final,
	}, nil
}

func validatePostgresMigrationRecorderIdentity(identity migrationbackend.AppliedMigration) error {
	for _, value := range []struct {
		name string
		text string
	}{
		{name: "app", text: identity.App},
		{name: "migration name", text: identity.Name},
	} {
		if value.text == "" {
			return postgresMigrationIntentIntegrity("history transition requires non-empty app and migration name", nil)
		}
		if len(value.text) > postgresMigrationRecorderMaxBytes {
			return postgresMigrationIntentIntegrity(
				fmt.Sprintf(
					"history transition %s has %d bytes, maximum %d",
					value.name,
					len(value.text),
					postgresMigrationRecorderMaxBytes,
				),
				nil,
			)
		}
		if !utf8.ValidString(value.text) {
			return postgresMigrationIntentIntegrity("history transition "+value.name+" is not valid UTF-8", nil)
		}
		if strings.IndexByte(value.text, 0) >= 0 {
			return postgresMigrationIntentIntegrity("history transition "+value.name+" contains NUL", nil)
		}
		if count := utf8.RuneCountInString(value.text); count > postgresMigrationRecorderMaxChars {
			return postgresMigrationIntentIntegrity(
				fmt.Sprintf(
					"history transition %s has %d characters, maximum %d",
					value.name,
					count,
					postgresMigrationRecorderMaxChars,
				),
				nil,
			)
		}
	}
	return nil
}

func (schema *postgresMigrationSchema) verifySeal() error {
	if schema == nil {
		return postgresMigrationIntentIntegrity("migration schema is nil", nil)
	}
	digest, err := hashPostgresMigrationIntent(schema.transition, schema.intent)
	if err != nil {
		return postgresMigrationIntentIntegrity("hash sealed PostgreSQL migration intent", err)
	}
	if digest != schema.digest {
		return postgresMigrationIntentIntegrity("sealed PostgreSQL migration intent changed after validation", nil)
	}
	return nil
}

// verifyOperationSeal checks only the operation about to reach the database.
// The entire detached intent is checked before preflight and again before
// recording success. This keeps N operations linear in their total encoded
// size instead of serializing all N operations before every SQL statement.
func (schema *postgresMigrationSchema) verifyOperationSeal(operation migrationbackend.MigrationOperation) error {
	if len(schema.operationDigests) != len(schema.intent.Operations) || schema.cursor < 0 || schema.cursor >= len(schema.operationDigests) {
		return postgresMigrationIntentIntegrity("sealed PostgreSQL migration operation inventory changed", nil)
	}
	digest, err := hashPostgresMigrationOperation(schema.transition, operation)
	if err != nil {
		return postgresMigrationIntentIntegrity("hash sealed PostgreSQL migration operation", err)
	}
	if digest != schema.operationDigests[schema.cursor] {
		return postgresMigrationIntentIntegrity("sealed PostgreSQL migration operation changed after validation", nil)
	}
	return nil
}

func (schema *postgresMigrationSchema) validateOperationContext(ctx context.Context) error {
	if schema == nil {
		return postgresMigrationIntentIntegrity("migration schema is nil", nil)
	}
	if ctx == nil {
		return errors.New("execute PostgreSQL migration schema operation: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !schema.preflight || schema.namespace == "" {
		return postgresMigrationIntentIntegrity("migration schema physical preflight has not completed", nil)
	}
	if schema.verified {
		return postgresMigrationIntentIntegrity("migration schema is already verified complete", nil)
	}
	return nil
}

func validatePostgresMigrationIntent(
	transition migrationbackend.HistoryTransition,
	intent *migrationbackend.MigrationIntent,
) (postgresMigrationBoundary, postgresMigrationBoundary, error) {
	graphPlan, err := migrationbackend.ResolveMigrationGraphPlan(transition, *intent)
	if err != nil {
		return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity("invalid historical relation graph", err)
	}
	for position, operation := range intent.Operations {
		if transition.Kind == migrationbackend.HistoryTransitionApply &&
			(operation.Kind == migrationbackend.MigrationDeleteModel || operation.Kind == migrationbackend.MigrationRemoveField) ||
			transition.Kind == migrationbackend.HistoryTransitionUnapply &&
				(operation.Kind == migrationbackend.MigrationCreateModel || operation.Kind == migrationbackend.MigrationAddField) {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity("operation kind does not match history transition", nil)
		}
		if _, _, _, err := validatePostgresMigrationOperation(operation); err != nil {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
		}
		graph, exists := graphPlan.Operation(position)
		if !exists {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity("operation graph is missing", nil)
		}
		for _, snapshot := range graph.Models() {
			if postgresMigrationReservedTable(snapshot.Model.DBTable) {
				return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity("relation graph uses a reserved PostgreSQL table", nil)
			}
			if err := validateExactPostgresMigrationModel(snapshot.Model); err != nil {
				return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
			}
		}
	}
	boundary := func(models []migrationbackend.MigrationModel, final bool) (postgresMigrationBoundary, error) {
		result := postgresMigrationBoundary{models: make(map[string]ir.Model, len(models)), targets: make(map[string][]migrationbackend.MigrationTarget, len(models))}
		for _, snapshot := range models {
			targets, err := graphPlan.BoundaryTargets(snapshot.Model, final)
			if err != nil {
				return postgresMigrationBoundary{}, postgresMigrationIntentIntegrity("relation graph boundary lacks exact targets", err)
			}
			result.models[snapshot.Model.DBTable] = snapshot.Model
			result.targets[snapshot.Model.DBTable] = targets
		}
		return result, nil
	}
	initial, err := boundary(graphPlan.InitialModels(), false)
	if err != nil {
		return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
	}
	final, err := boundary(graphPlan.FinalModels(), true)
	if err != nil {
		return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
	}
	return initial, final, nil
}

func validatePostgresMigrationOperation(operation migrationbackend.MigrationOperation) (ir.Model, ir.Model, ir.Field, error) {
	var before, after ir.Model
	var changed ir.Field
	switch operation.Kind {
	case migrationbackend.MigrationCreateModel:
		if !reflect.DeepEqual(operation.Before, ir.Model{}) {
			return before, after, changed, postgresMigrationIntentIntegrity("CreateModel has a non-zero Before model", nil)
		}
		after = operation.After
		if err := validateExactPostgresMigrationModel(after); err != nil {
			return before, after, changed, err
		}
	case migrationbackend.MigrationDeleteModel:
		if !reflect.DeepEqual(operation.After, ir.Model{}) {
			return before, after, changed, postgresMigrationIntentIntegrity("DeleteModel has a non-zero After model", nil)
		}
		before = operation.Before
		if err := validateExactPostgresMigrationModel(before); err != nil {
			return before, after, changed, err
		}
	case migrationbackend.MigrationAddField:
		before, after = operation.Before, operation.After
		if err := validateExactPostgresMigrationModel(before); err != nil {
			return before, after, changed, err
		}
		if err := validateExactPostgresMigrationModel(after); err != nil {
			return before, after, changed, err
		}
		var err error
		changed, err = operation.ChangedField()
		if err != nil {
			return before, after, changed, postgresMigrationIntentIntegrity("invalid AddField delta", err)
		}
		if changed.PrimaryKey {
			return before, after, changed, postgresMigrationIntentIntegrity("AddField cannot add a primary key", nil)
		}
	case migrationbackend.MigrationRemoveField:
		before, after = operation.Before, operation.After
		if err := validateExactPostgresMigrationModel(before); err != nil {
			return before, after, changed, err
		}
		if err := validateExactPostgresMigrationModel(after); err != nil {
			return before, after, changed, err
		}
		var err error
		changed, err = operation.ChangedField()
		if err != nil {
			return before, after, changed, postgresMigrationIntentIntegrity("invalid RemoveField delta", err)
		}
		if changed.PrimaryKey {
			return before, after, changed, postgresMigrationIntentIntegrity("RemoveField cannot remove a primary key", nil)
		}
	case migrationbackend.MigrationAddConstraint, migrationbackend.MigrationRemoveConstraint:
		before, after = operation.Before, operation.After
		if err := validateExactPostgresMigrationModel(before); err != nil {
			return before, after, changed, err
		}
		if err := validateExactPostgresMigrationModel(after); err != nil {
			return before, after, changed, err
		}
		if _, err := operation.ChangedConstraint(); err != nil {
			return before, after, changed, postgresMigrationIntentIntegrity("invalid named constraint delta", err)
		}
	case migrationbackend.MigrationAlterField:
		before, after = operation.Before, operation.After
		if err := validateExactPostgresMigrationModel(before); err != nil {
			return before, after, changed, err
		}
		if err := validateExactPostgresMigrationModel(after); err != nil {
			return before, after, changed, err
		}
		_, field, _, err := migrationbackend.ChangedField(before, after)
		if err != nil {
			return before, after, changed, postgresMigrationIntentIntegrity("AlterField requires an exact supported field delta", err)
		}
		changed = field
	default:
		return before, after, changed, postgresMigrationIntentIntegrity(fmt.Sprintf("operation kind %d is invalid", operation.Kind), nil)
	}
	return before, after, changed, nil
}

func validateExactPostgresMigrationModel(model ir.Model) error {
	if len(model.UniqueConstraints) != 0 {
		return migrationbackend.NewCapabilityError("named_unique_constraints", "named model constraints require native constraint ownership before migration execution", nil)
	}
	if reflect.DeepEqual(model, ir.Model{}) {
		return postgresMigrationIntentIntegrity("migration model is zero", nil)
	}
	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_postgres_intent",
		Models:        []ir.Model{model.Clone()},
	})
	if err != nil {
		return postgresMigrationIntentIntegrity("migration model is not valid normalized Schema IR", err)
	}
	if len(normalized.Models) != 1 || !reflect.DeepEqual(normalized.Models[0], model) {
		return postgresMigrationIntentIntegrity("normalization changes the migration model snapshot", nil)
	}
	if err := validateIdentifier(model.DBTable); err != nil {
		return postgresMigrationIntentIntegrity("model table is invalid for PostgreSQL", err)
	}
	if len(model.Fields) > postgresMigrationMaxModelFields {
		return postgresMigrationCapability(
			fmt.Sprintf(
				"model %q has %d fields, the current PostgreSQL profile supports at most %d",
				model.Name,
				len(model.Fields),
				postgresMigrationMaxModelFields,
			),
			nil,
		)
	}
	for index := range model.Fields {
		field := model.Fields[index]
		if err := validateIdentifier(field.Column); err != nil {
			return postgresMigrationIntentIntegrity(fmt.Sprintf("field %q column is invalid for PostgreSQL", field.Name), err)
		}
		if field.Kind == ir.FieldChar && field.MaxLength > postgresMigrationMaxVarcharChars {
			return postgresMigrationCapability(
				fmt.Sprintf(
					"field %q max_length %d exceeds the PostgreSQL VARCHAR limit %d",
					field.Name,
					field.MaxLength,
					postgresMigrationMaxVarcharChars,
				),
				nil,
			)
		}
	}
	return nil
}

func validatePostgresConstraintNames(intent migrationbackend.MigrationIntent) error {
	constraintOwners := make(map[string]string)
	relationOwners := map[string]string{
		postgresMigrationRecorderTable:      "migration recorder table",
		postgresMigrationRevisionTable:      "migration revision table",
		postgresMigrationRecorderPrimaryKey: "migration recorder primary index",
		postgresMigrationRevisionPrimaryKey: "migration revision primary index",
	}
	for _, operation := range intent.Operations {
		models := []ir.Model{operation.Before, operation.After}
		for _, target := range operation.Targets {
			models = append(models, target.TargetModel)
		}
		for _, related := range operation.RelatedModels {
			models = append(models, related.Model)
		}
		for _, model := range models {
			if reflect.DeepEqual(model, ir.Model{}) {
				continue
			}
			if err := registerPostgresModelDerivedNames(constraintOwners, relationOwners, model); err != nil {
				return err
			}
			for _, field := range model.Fields {
				if field.Kind != ir.FieldForeignKey {
					continue
				}
				name, err := postgresForeignKeyConstraintName(model.DBTable, field.Column)
				if err != nil {
					return postgresMigrationIntentIntegrity("derive PostgreSQL foreign key constraint", err)
				}
				if err := registerPostgresConstraintName(constraintOwners, name, model.DBTable+"."+field.Column); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func registerPostgresModelDerivedNames(
	constraintOwners,
	relationOwners map[string]string,
	model ir.Model,
) error {
	if err := registerPostgresRelationName(relationOwners, model.DBTable, model.DBTable+".<table>"); err != nil {
		return err
	}
	primaryKey, err := postgresMigrationPrimaryKey(model)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL identity object names", err)
	}
	primaryName, err := postgresPrimaryKeyConstraintName(model.DBTable)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL primary key constraint", err)
	}
	primaryOwner := model.DBTable + ".<primary-key>"
	if err := registerPostgresConstraintName(constraintOwners, primaryName, primaryOwner); err != nil {
		return err
	}
	if err := registerPostgresRelationName(relationOwners, primaryName, primaryOwner+" index"); err != nil {
		return err
	}
	for _, field := range model.Fields {
		if !field.Unique {
			continue
		}
		name, err := postgresUniqueConstraintName(model.DBTable, field.Column)
		if err != nil {
			return postgresMigrationIntentIntegrity("derive PostgreSQL unique constraint", err)
		}
		owner := model.DBTable + "." + field.Column + " unique"
		if err := registerPostgresConstraintName(constraintOwners, name, owner); err != nil {
			return err
		}
		if err := registerPostgresRelationName(relationOwners, name, owner+" index"); err != nil {
			return err
		}
	}
	sequenceName, err := postgresIdentitySequenceName(model.DBTable, primaryKey.Column)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL identity sequence", err)
	}
	return registerPostgresRelationName(
		relationOwners,
		sequenceName,
		model.DBTable+"."+primaryKey.Column+" identity sequence",
	)
}

func registerPostgresConstraintName(owners map[string]string, name, owner string) error {
	if previous, exists := owners[name]; exists && previous != owner {
		return postgresMigrationIntentIntegrity(
			fmt.Sprintf("derived constraint %q collides for %s and %s", name, previous, owner),
			nil,
		)
	}
	owners[name] = owner
	return nil
}

func registerPostgresRelationName(owners map[string]string, name, owner string) error {
	if previous, exists := owners[name]; exists && previous != owner {
		return postgresMigrationIntentIntegrity(
			fmt.Sprintf("derived PostgreSQL relation %q collides for %s and %s", name, previous, owner),
			nil,
		)
	}
	owners[name] = owner
	return nil
}

func scanPostgresMigrationResources(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = postgresMigrationIntentIntegrity("migration intent resource limit", resultErr)
		}
	}()
	if len(intent.Operations) > postgresMigrationMaxOperations {
		return fmt.Errorf("intent has %d operations, maximum %d", len(intent.Operations), postgresMigrationMaxOperations)
	}
	budget := irresource.New(irresource.Limits{
		Fields: postgresMigrationMaxFields, StringBytes: postgresMigrationMaxStringBytes,
		Nodes: postgresMigrationMaxNodes, Bytes: postgresMigrationMaxAggregateBytes,
	})
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
	for index, operation := range intent.Operations {
		prefix := fmt.Sprintf("operations[%d]", index)
		if err := budget.ScanModel(prefix+".before", operation.Before); err != nil {
			return err
		}
		if err := budget.ScanModel(prefix+".after", operation.After); err != nil {
			return err
		}
		if len(operation.Targets) > postgresMigrationMaxTargets {
			return fmt.Errorf("%s has too many targets", prefix)
		}
		if err := budget.ConsumeNodes(prefix+".targets", len(operation.Targets)); err != nil {
			return err
		}
		for targetIndex, target := range operation.Targets {
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
		if len(operation.RelatedModels) > postgresMigrationMaxTargets {
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

func hashPostgresMigrationIntent(
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(postgresMigrationSealPayload{Transition: transition, Intent: intent})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func hashPostgresMigrationOperation(
	transition migrationbackend.HistoryTransition,
	operation migrationbackend.MigrationOperation,
) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(struct {
		Transition migrationbackend.HistoryTransition
		Operation  migrationbackend.MigrationOperation
	}{transition, operation})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func postgresMigrationRelationFields(model ir.Model) []ir.Field {
	fields := make([]ir.Field, 0)
	for index := range model.Fields {
		if model.Fields[index].Kind == ir.FieldForeignKey {
			fields = append(fields, model.Fields[index])
		}
	}
	return fields
}

func postgresMigrationSameModel(left, right ir.Model) bool {
	return left.Name == right.Name && left.GoName == right.GoName && left.DBTable == right.DBTable
}

func postgresMigrationReservedTable(table string) bool {
	return table == postgresMigrationRecorderTable || table == postgresMigrationRevisionTable
}

func migrationFieldsEqual(left, right ir.Field) bool {
	return reflect.DeepEqual(left, right)
}
