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
	transition migrationbackend.HistoryTransition
	intent     migrationbackend.MigrationIntent
	digest     [sha256.Size]byte
	namespace  string
	initial    postgresMigrationBoundary
	final      postgresMigrationBoundary
	cursor     int
	preflight  bool
	verified   bool
}

type postgresMigrationBoundary struct {
	models  map[string]ir.Model
	targets map[string][]migrationbackend.MigrationTarget
}

type postgresMigrationSealPayload struct {
	Transition migrationbackend.HistoryTransition `json:"transition"`
	Intent     migrationbackend.MigrationIntent   `json:"intent"`
}

type postgresMigrationResourceBudget struct {
	nodes uint64
	bytes uint64
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
	if transition.Kind != migrationbackend.HistoryTransitionApply &&
		transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return nil, postgresMigrationIntentIntegrity(fmt.Sprintf("history transition kind %d is invalid", transition.Kind), nil)
	}
	if intent.Operations == nil {
		return nil, postgresMigrationIntentIntegrity("migration intent operations are missing", nil)
	}
	if err := scanPostgresMigrationResources(transition, intent); err != nil {
		return nil, err
	}
	cloned := clonePostgresMigrationIntent(intent)
	initial, final, err := validatePostgresMigrationIntent(transition, &cloned)
	if err != nil {
		return nil, err
	}
	if err := scanPostgresMigrationResources(transition, cloned); err != nil {
		return nil, err
	}
	if err := validatePostgresConstraintNames(cloned); err != nil {
		return nil, err
	}
	digest, err := hashPostgresMigrationIntent(transition, cloned)
	if err != nil {
		return nil, postgresMigrationIntentIntegrity("seal PostgreSQL migration intent", err)
	}
	return &postgresMigrationSchema{
		transition: transition,
		intent:     cloned,
		digest:     digest,
		initial:    initial,
		final:      final,
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
	return schema.verifySeal()
}

func validatePostgresMigrationIntent(
	transition migrationbackend.HistoryTransition,
	intent *migrationbackend.MigrationIntent,
) (postgresMigrationBoundary, postgresMigrationBoundary, error) {
	initial := postgresMigrationBoundary{
		models:  make(map[string]ir.Model),
		targets: make(map[string][]migrationbackend.MigrationTarget),
	}
	current := postgresMigrationBoundary{
		models:  make(map[string]ir.Model),
		targets: make(map[string][]migrationbackend.MigrationTarget),
	}
	initialSeen := make(map[string]struct{})
	tableOwners := make(map[string]string)

	for position := range intent.Operations {
		operation := &intent.Operations[position]
		wantIndex := position
		if transition.Kind == migrationbackend.HistoryTransitionUnapply {
			wantIndex = len(intent.Operations) - 1 - position
		}
		if operation.OperationIndex != wantIndex {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
				fmt.Sprintf("operation position %d has original index %d, want %d", position, operation.OperationIndex, wantIndex), nil,
			)
		}
		if transition.Kind == migrationbackend.HistoryTransitionApply &&
			(operation.Kind == migrationbackend.MigrationDeleteModel || operation.Kind == migrationbackend.MigrationRemoveField) ||
			transition.Kind == migrationbackend.HistoryTransitionUnapply &&
				(operation.Kind == migrationbackend.MigrationCreateModel || operation.Kind == migrationbackend.MigrationAddField) {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
				fmt.Sprintf("operation %d kind %d does not match history transition %d", operation.OperationIndex, operation.Kind, transition.Kind), nil,
			)
		}

		before, after, changed, err := validatePostgresMigrationOperation(*operation)
		if err != nil {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
		}
		boundary := after
		// DeleteModel and RemoveField must validate relation authority against
		// the physical source shape that exists before the operation. In
		// particular, a ForeignKey RemoveField's After snapshot no longer
		// contains the relation whose constraint preflight must accept and whose
		// target metadata must remain sealed through the DROP.
		if operation.Kind == migrationbackend.MigrationDeleteModel ||
			operation.Kind == migrationbackend.MigrationRemoveField {
			boundary = before
		}
		if postgresMigrationReservedTable(boundary.DBTable) {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
				fmt.Sprintf("operation %d uses reserved migration table %q", operation.OperationIndex, boundary.DBTable), nil,
			)
		}
		identity := boundary.Name
		if previous, exists := tableOwners[boundary.DBTable]; exists && previous != identity {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
				fmt.Sprintf("models %q and %q collide on PostgreSQL table %q", previous, identity, boundary.DBTable), nil,
			)
		}
		tableOwners[boundary.DBTable] = identity

		actual, exists := current.models[identity]
		_, initialAlreadySeen := initialSeen[identity]
		firstIdentityOperation := !initialAlreadySeen
		if firstIdentityOperation {
			initialSeen[identity] = struct{}{}
		}
		if firstIdentityOperation && !reflect.DeepEqual(before, ir.Model{}) {
			initial.models[identity] = before.Clone()
		}
		if reflect.DeepEqual(before, ir.Model{}) {
			if exists {
				return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
					fmt.Sprintf("CreateModel operation %d source already exists", operation.OperationIndex), nil,
				)
			}
		} else if exists && !reflect.DeepEqual(actual, before) {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, postgresMigrationIntentIntegrity(
				fmt.Sprintf("operation %d Before model is discontinuous", operation.OperationIndex), nil,
			)
		} else if !exists {
			current.models[identity] = before.Clone()
		}

		expanded, err := validateAndExpandPostgresMigrationTargets(transition, *operation, boundary, changed)
		if err != nil {
			return postgresMigrationBoundary{}, postgresMigrationBoundary{}, err
		}
		operation.Targets = expanded
		if firstIdentityOperation && reflect.DeepEqual(before, ir.Model{}) {
			initial.targets[identity] = nil
		} else if firstIdentityOperation {
			initial.targets[identity] = targetsForPostgresBoundary(before, expanded)
		}
		if reflect.DeepEqual(after, ir.Model{}) {
			delete(current.models, identity)
			delete(current.targets, identity)
		} else {
			current.models[identity] = after.Clone()
			current.targets[identity] = targetsForPostgresBoundary(after, expanded)
		}
	}

	final := clonePostgresMigrationBoundary(current)
	for identity, model := range initial.models {
		if _, exists := final.models[identity]; !exists && !postgresBoundaryDeleted(intent.Operations, identity) {
			final.models[identity] = model.Clone()
			final.targets[identity] = clonePostgresMigrationTargets(initial.targets[identity])
		}
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
		if !postgresMigrationSameModel(before, after) || len(after.Fields) != len(before.Fields)+1 ||
			!reflect.DeepEqual(before.Fields, after.Fields[:len(before.Fields)]) {
			return before, after, changed, postgresMigrationIntentIntegrity("AddField must append exactly one field to the same model", nil)
		}
		changed = after.Fields[len(after.Fields)-1]
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
		if !postgresMigrationSameModel(before, after) || len(before.Fields) != len(after.Fields)+1 ||
			!reflect.DeepEqual(after.Fields, before.Fields[:len(after.Fields)]) {
			return before, after, changed, postgresMigrationIntentIntegrity("RemoveField must remove exactly the final field from the same model", nil)
		}
		changed = before.Fields[len(before.Fields)-1]
		if changed.PrimaryKey {
			return before, after, changed, postgresMigrationIntentIntegrity("RemoveField cannot remove a primary key", nil)
		}
	default:
		return before, after, changed, postgresMigrationIntentIntegrity(fmt.Sprintf("operation kind %d is invalid", operation.Kind), nil)
	}
	return before, after, changed, nil
}

func validateAndExpandPostgresMigrationTargets(
	transition migrationbackend.HistoryTransition,
	operation migrationbackend.MigrationOperation,
	boundary ir.Model,
	changed ir.Field,
) ([]migrationbackend.MigrationTarget, error) {
	relationFields := postgresMigrationRelationFields(boundary)
	if changed.Kind == ir.FieldForeignKey {
		if len(operation.Targets) != 1 || !migrationFieldsEqual(operation.Targets[0].SourceField, changed) {
			return nil, postgresMigrationIntentIntegrity(
				fmt.Sprintf("ForeignKey operation %d requires exactly its changed-field target", operation.OperationIndex), nil,
			)
		}
		changedTarget := operation.Targets[0]
		if err := validatePostgresMigrationTarget(changed, changedTarget); err != nil {
			return nil, err
		}
		if len(postgresMigrationRelationFields(changedTarget.TargetModel)) != 0 {
			return nil, postgresMigrationIntentIntegrity("ForeignKey Add/Remove target contains nested relation fields", nil)
		}
		expanded := make([]migrationbackend.MigrationTarget, len(relationFields))
		for index := range relationFields {
			field := relationFields[index]
			if field.Relation == nil || changed.Relation == nil || field.Relation.Target != changed.Relation.Target {
				return nil, postgresMigrationIntentIntegrity("ForeignKey Add/Remove source contains a different symbolic target", nil)
			}
			expanded[index] = migrationbackend.MigrationTarget{
				SourceField: field.Clone(),
				TargetModel: changedTarget.TargetModel,
				TargetKey:   changedTarget.TargetKey,
			}
		}
		return expanded, nil
	}
	if len(operation.Targets) != len(relationFields) {
		return nil, postgresMigrationIntentIntegrity(
			fmt.Sprintf("operation %d has %d relation targets, want %d", operation.OperationIndex, len(operation.Targets), len(relationFields)), nil,
		)
	}
	result := clonePostgresMigrationTargets(operation.Targets)
	for index := range result {
		if !migrationFieldsEqual(result[index].SourceField, relationFields[index]) {
			return nil, postgresMigrationIntentIntegrity("relation targets are not in exact source field order", nil)
		}
		if err := validatePostgresMigrationTarget(relationFields[index], result[index]); err != nil {
			return nil, err
		}
		if result[index].SourceField.Relation != nil &&
			result[index].SourceField.Relation.Target.AppLabel != transition.Migration.App &&
			len(postgresMigrationRelationFields(result[index].TargetModel)) != 0 {
			return nil, postgresMigrationIntentIntegrity("external relation target contains nested relation fields outside the sealed intent", nil)
		}
	}
	return result, nil
}

func validatePostgresMigrationTarget(field ir.Field, target migrationbackend.MigrationTarget) error {
	if field.Kind != ir.FieldForeignKey || field.Relation == nil || !migrationFieldsEqual(field, target.SourceField) {
		return postgresMigrationIntentIntegrity("relation target source field is not exact", nil)
	}
	if err := validateExactPostgresMigrationModel(target.TargetModel); err != nil {
		return err
	}
	if postgresMigrationReservedTable(target.TargetModel.DBTable) {
		return postgresMigrationIntentIntegrity("relation target uses a reserved PostgreSQL migration table", nil)
	}
	if target.TargetModel.Name != field.Relation.Target.ModelName {
		return postgresMigrationIntentIntegrity("relation target model does not match the symbolic declaration", nil)
	}
	primaryKey, err := postgresMigrationPrimaryKey(target.TargetModel)
	if err != nil {
		return postgresMigrationIntentIntegrity("relation target primary key is invalid", err)
	}
	if !migrationFieldsEqual(primaryKey, target.TargetKey) {
		return postgresMigrationIntentIntegrity("relation target key is not the exact historical AutoField", nil)
	}
	return nil
}

func validateExactPostgresMigrationModel(model ir.Model) error {
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
	for index := range intent.Operations {
		operation := intent.Operations[index]
		model := operation.After
		if reflect.DeepEqual(model, ir.Model{}) {
			model = operation.Before
		}
		if err := registerPostgresModelDerivedNames(constraintOwners, relationOwners, model); err != nil {
			return err
		}
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			if err := registerPostgresModelDerivedNames(constraintOwners, relationOwners, target.TargetModel); err != nil {
				return err
			}
			name, err := postgresForeignKeyConstraintName(model.DBTable, target.SourceField.Column)
			if err != nil {
				return postgresMigrationIntentIntegrity("derive PostgreSQL foreign key constraint", err)
			}
			owner := model.DBTable + "." + target.SourceField.Column
			if err := registerPostgresConstraintName(constraintOwners, name, owner); err != nil {
				return err
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
) error {
	if len(intent.Operations) > postgresMigrationMaxOperations {
		return postgresMigrationIntentIntegrity(fmt.Sprintf("intent has %d operations, maximum %d", len(intent.Operations), postgresMigrationMaxOperations), nil)
	}
	budget := postgresMigrationResourceBudget{}
	if err := budget.consumeNodes(1); err != nil {
		return err
	}
	for _, value := range []string{transition.Migration.App, transition.Migration.Name} {
		if err := budget.consumeString(value); err != nil {
			return err
		}
	}
	if err := budget.consumeNodes(len(intent.Operations)); err != nil {
		return err
	}
	for index := range intent.Operations {
		operation := intent.Operations[index]
		if err := budget.scanModel(operation.Before); err != nil {
			return err
		}
		if err := budget.scanModel(operation.After); err != nil {
			return err
		}
		if len(operation.Targets) > postgresMigrationMaxTargets {
			return postgresMigrationIntentIntegrity(fmt.Sprintf("operation %d has too many targets", operation.OperationIndex), nil)
		}
		if err := budget.consumeNodes(len(operation.Targets)); err != nil {
			return err
		}
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			if err := budget.scanField(target.SourceField); err != nil {
				return err
			}
			if err := budget.scanModel(target.TargetModel); err != nil {
				return err
			}
			if err := budget.scanField(target.TargetKey); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *postgresMigrationResourceBudget) scanModel(model ir.Model) error {
	if err := budget.consumeNodes(1); err != nil {
		return err
	}
	for _, value := range []string{model.Name, model.GoName, model.DBTable} {
		if err := budget.consumeString(value); err != nil {
			return err
		}
	}
	if len(model.Fields) > postgresMigrationMaxFields {
		return postgresMigrationIntentIntegrity(fmt.Sprintf("model %q has too many fields", model.Name), nil)
	}
	if err := budget.consumeNodes(len(model.Fields)); err != nil {
		return err
	}
	for index := range model.Fields {
		if err := budget.scanField(model.Fields[index]); err != nil {
			return err
		}
	}
	return nil
}

func (budget *postgresMigrationResourceBudget) scanField(field ir.Field) error {
	if err := budget.consumeNodes(1); err != nil {
		return err
	}
	for _, value := range []string{field.Name, field.GoName, field.Column, string(field.Kind)} {
		if err := budget.consumeString(value); err != nil {
			return err
		}
	}
	if field.Default != nil {
		if err := budget.consumeNodes(1); err != nil {
			return err
		}
		for _, value := range []string{string(field.Default.Kind), field.Default.String} {
			if err := budget.consumeString(value); err != nil {
				return err
			}
		}
	}
	if field.Relation != nil {
		if err := budget.consumeNodes(1); err != nil {
			return err
		}
		for _, value := range []string{
			field.Relation.Target.AppLabel,
			field.Relation.Target.ModelName,
			string(field.Relation.Cardinality),
			field.Relation.Reverse.Name,
			string(field.Relation.OnDelete),
		} {
			if err := budget.consumeString(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *postgresMigrationResourceBudget) consumeNodes(count int) error {
	if count < 0 || uint64(count) > postgresMigrationMaxNodes-budget.nodes {
		return postgresMigrationIntentIntegrity("intent exceeds the aggregate node limit", nil)
	}
	budget.nodes += uint64(count)
	return nil
}

func (budget *postgresMigrationResourceBudget) consumeString(value string) error {
	if len(value) > postgresMigrationMaxStringBytes {
		return postgresMigrationIntentIntegrity(fmt.Sprintf("intent contains a string of %d bytes", len(value)), nil)
	}
	if uint64(len(value)) > postgresMigrationMaxAggregateBytes-budget.bytes {
		return postgresMigrationIntentIntegrity("intent exceeds the aggregate byte limit", nil)
	}
	budget.bytes += uint64(len(value))
	return nil
}

func clonePostgresMigrationIntent(intent migrationbackend.MigrationIntent) migrationbackend.MigrationIntent {
	cloned := migrationbackend.MigrationIntent{}
	if intent.Operations == nil {
		return cloned
	}
	cloned.Operations = make([]migrationbackend.MigrationOperation, len(intent.Operations))
	for index := range intent.Operations {
		operation := intent.Operations[index]
		operation.Before = operation.Before.Clone()
		operation.After = operation.After.Clone()
		operation.Targets = clonePostgresMigrationTargets(operation.Targets)
		cloned.Operations[index] = operation
	}
	return cloned
}

func clonePostgresMigrationTargets(targets []migrationbackend.MigrationTarget) []migrationbackend.MigrationTarget {
	if targets == nil {
		return nil
	}
	cloned := make([]migrationbackend.MigrationTarget, len(targets))
	for index := range targets {
		cloned[index] = migrationbackend.MigrationTarget{
			SourceField: targets[index].SourceField.Clone(),
			TargetModel: targets[index].TargetModel.Clone(),
			TargetKey:   targets[index].TargetKey.Clone(),
		}
	}
	return cloned
}

func clonePostgresMigrationBoundary(boundary postgresMigrationBoundary) postgresMigrationBoundary {
	cloned := postgresMigrationBoundary{
		models:  make(map[string]ir.Model, len(boundary.models)),
		targets: make(map[string][]migrationbackend.MigrationTarget, len(boundary.targets)),
	}
	for identity, model := range boundary.models {
		cloned.models[identity] = model.Clone()
		cloned.targets[identity] = clonePostgresMigrationTargets(boundary.targets[identity])
	}
	return cloned
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

func postgresMigrationRelationFields(model ir.Model) []ir.Field {
	fields := make([]ir.Field, 0)
	for index := range model.Fields {
		if model.Fields[index].Kind == ir.FieldForeignKey {
			fields = append(fields, model.Fields[index])
		}
	}
	return fields
}

func targetsForPostgresBoundary(
	model ir.Model,
	candidates []migrationbackend.MigrationTarget,
) []migrationbackend.MigrationTarget {
	fields := postgresMigrationRelationFields(model)
	result := make([]migrationbackend.MigrationTarget, 0, len(fields))
	for index := range fields {
		for targetIndex := range candidates {
			if migrationFieldsEqual(fields[index], candidates[targetIndex].SourceField) {
				result = append(result, migrationbackend.MigrationTarget{
					SourceField: fields[index].Clone(),
					TargetModel: candidates[targetIndex].TargetModel.Clone(),
					TargetKey:   candidates[targetIndex].TargetKey.Clone(),
				})
				break
			}
		}
	}
	return result
}

func postgresBoundaryDeleted(operations []migrationbackend.MigrationOperation, identity string) bool {
	for index := range operations {
		if operations[index].Kind == migrationbackend.MigrationDeleteModel && operations[index].Before.Name == identity {
			return true
		}
	}
	return false
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
