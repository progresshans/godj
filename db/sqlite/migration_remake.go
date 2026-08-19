package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// sqliteRelationSequenceSnapshot is captured after BEGIN IMMEDIATE and before
// the revision claim. The exact spelling is part of the sealed physical input
// and must equal the declared table spelling; a successful remake preserves
// that exact name and the nonnegative high-water value.
type sqliteRelationSequenceSnapshot struct {
	present bool
	name    string
	value   int64
}

type sqliteRelationRemakePlan struct {
	operationIndex int
	before         ir.Model
	after          ir.Model
	targets        []migrationbackend.RelationMigrationTarget
	temporary      string
	primaryKey     ir.Field
	sequence       sqliteRelationSequenceSnapshot
}

type sqliteRelationRemakeSealPlan struct {
	OperationIndex int                                        `json:"operation_index"`
	Before         ir.Model                                   `json:"before"`
	After          ir.Model                                   `json:"after"`
	Targets        []migrationbackend.RelationMigrationTarget `json:"targets"`
	Temporary      string                                     `json:"temporary"`
	PrimaryKey     ir.Field                                   `json:"primary_key"`
	Sequence       sqliteRelationRemakeSealSequence           `json:"sequence"`
}

type sqliteRelationRemakeSealSequence struct {
	Present bool   `json:"present"`
	Name    string `json:"name"`
	Value   int64  `json:"value"`
}

// sqliteRelationRemakeExecutionError deliberately preserves errors.Is without
// exposing backend capability/revision/sqlite classification through
// errors.As. Once the remake stream starts, the public owner is the original
// AddField operation; a late SQLITE_BUSY or defensive final-shape failure must
// not be reclassified as a preclaim capability/fence error.
type sqliteRelationRemakeExecutionError struct {
	stage string
	cause error
}

func (e sqliteRelationRemakeExecutionError) Error() string {
	return e.stage + ": " + e.cause.Error()
}

func (e sqliteRelationRemakeExecutionError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

func newSQLiteRelationRemakeExecutionError(stage string, cause error) error {
	if cause == nil {
		return nil
	}
	return sqliteRelationRemakeExecutionError{stage: stage, cause: cause}
}

func preflightSQLiteRelationRemakes(
	ctx context.Context,
	executor migrationSQLExecutor,
	transition migrationbackend.HistoryTransition,
	seal *sqliteRelationIntentSeal,
	catalog sqliteRelationCatalog,
) (map[int]sqliteRelationRemakePlan, [sha256.Size]byte, error) {
	plans := make(map[int]sqliteRelationRemakePlan)
	blocked := make(map[string]string)
	block := func(name, owner string) {
		key := sqliteRelationIdentifierKey(name)
		if _, exists := blocked[key]; !exists {
			blocked[key] = owner
		}
	}
	for _, object := range catalog.objects {
		block(object.name, object.schema+" "+object.kind)
	}
	for _, sequence := range catalog.sequences {
		block(sequence.name, "sqlite_sequence")
	}
	block(migrationRevisionTable, "migration control")
	block(migrationRecorderTable, "migration control")
	for index := range seal.intent.Operations {
		operation := seal.intent.Operations[index]
		model := operation.After
		if reflect.DeepEqual(model, ir.Model{}) {
			model = operation.Before
		}
		block(model.DBTable, fmt.Sprintf("operation %d", operation.OperationIndex))
	}
	for _, target := range seal.externalTargets {
		block(target.snapshot.DBTable, "external relation target")
	}

	for position := range seal.intent.Operations {
		operation := seal.intent.Operations[position]
		if operation.Kind != migrationbackend.RelationMigrationRemoveField ||
			!sqliteRelationOperationChangesForeignKey(operation) {
			continue
		}
		field := operation.Before.Fields[len(operation.Before.Fields)-1]
		if len(operation.Targets) == 0 ||
			!reflect.DeepEqual(operation.Targets[len(operation.Targets)-1].SourceField, field) {
			return nil, [sha256.Size]byte{}, relationIntentIntegrity(
				"relation RemoveField operation %d lacks exact changed-field target authority",
				operation.OperationIndex,
			)
		}
		retainedTargets := cloneSQLiteRelationTargets(operation.Targets[:len(operation.Targets)-1])
		primaryKey, err := exactRelationTargetPrimaryKey(operation.After)
		if err != nil {
			return nil, [sha256.Size]byte{}, relationIntentUnsupported(
				"relation RemoveField operation %d source: %v",
				operation.OperationIndex,
				err,
			)
		}
		if err := rejectSQLiteRelationRemakeIndexes(ctx, executor, operation.Before.DBTable); err != nil {
			return nil, [sha256.Size]byte{}, err
		}
		sequence := catalog.sequences[sqliteRelationIdentifierKey(operation.Before.DBTable)]
		if sequence.present && sequence.name != operation.Before.DBTable {
			return nil, [sha256.Size]byte{}, relationIntentUnsupported(
				"bounded relation remake sqlite_sequence name %q does not exactly match declared table %q",
				sequence.name,
				operation.Before.DBTable,
			)
		}
		temporary := sqliteRelationRemakeTemporary(transition, operation.OperationIndex, operation.Before.DBTable, field.Column)
		if owner, collision := blocked[sqliteRelationIdentifierKey(temporary)]; collision {
			return nil, [sha256.Size]byte{}, relationIntentUnsupported(
				"bounded relation remake temporary table %q collides with %s",
				temporary,
				owner,
			)
		}
		block(temporary, fmt.Sprintf("remake plan %d", operation.OperationIndex))
		if _, duplicate := plans[operation.OperationIndex]; duplicate {
			return nil, [sha256.Size]byte{}, relationIntentIntegrity(
				"relation remake plan index %d is duplicated",
				operation.OperationIndex,
			)
		}
		plan := sqliteRelationRemakePlan{
			operationIndex: operation.OperationIndex,
			before:         operation.Before.Clone(),
			after:          operation.After.Clone(),
			targets:        retainedTargets,
			temporary:      temporary,
			primaryKey:     primaryKey.Clone(),
			sequence:       sequence,
		}
		if _, err := compileSQLiteRelationRemakeCreate(plan); err != nil {
			return nil, [sha256.Size]byte{}, relationIntentUnsupported(
				"relation RemoveField operation %d cannot compile bounded remake: %v",
				operation.OperationIndex,
				err,
			)
		}
		plans[operation.OperationIndex] = plan
	}
	digest, err := hashSQLiteRelationRemakePlans(plans)
	if err != nil {
		return nil, [sha256.Size]byte{}, relationIntentIntegrity("seal relation remake plans: %v", err)
	}
	return plans, digest, nil
}

func rejectSQLiteRelationRemakeIndexes(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableName string,
) (resultErr error) {
	table, err := quoteIdentifier(tableName)
	if err != nil {
		return relationIntentUnsupported("bounded relation remake table identifier %q is invalid", tableName)
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA main.index_list(`+table+`)`)
	if err != nil {
		return classifyRevisionIO("list relation remake indexes "+tableName, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIO("close relation remake indexes "+tableName, rows.Close()))
	}()
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return classifyRevisionIO("scan relation remake index "+tableName, err)
		}
		if origin != "pk" {
			return relationIntentUnsupported(
				"bounded relation remake rejects index %q on table %q",
				name,
				tableName,
			)
		}
	}
	return classifyRevisionIO("iterate relation remake indexes "+tableName, rows.Err())
}

func sqliteRelationRemakeTemporary(
	transition migrationbackend.HistoryTransition,
	operationIndex int,
	table,
	column string,
) string {
	hash := sha256.New()
	for _, value := range []string{
		"godj/sqlite/relation-remake/v1",
		transition.Migration.App,
		transition.Migration.Name,
		strconv.FormatUint(uint64(transition.Kind), 10),
		strconv.Itoa(operationIndex),
		table,
		column,
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	digest := hash.Sum(nil)
	return fmt.Sprintf("__godj_relation_%x", digest[:16])
}

func cloneSQLiteRelationTargets(
	targets []migrationbackend.RelationMigrationTarget,
) []migrationbackend.RelationMigrationTarget {
	cloned := make([]migrationbackend.RelationMigrationTarget, len(targets))
	for index := range targets {
		cloned[index] = migrationbackend.RelationMigrationTarget{
			SourceField: targets[index].SourceField.Clone(),
			TargetModel: targets[index].TargetModel.Clone(),
			TargetKey:   targets[index].TargetKey.Clone(),
		}
	}
	return cloned
}

func hashSQLiteRelationRemakePlans(plans map[int]sqliteRelationRemakePlan) ([sha256.Size]byte, error) {
	indices := make([]int, 0, len(plans))
	for index := range plans {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	payload := make([]sqliteRelationRemakeSealPlan, len(indices))
	for position, index := range indices {
		plan := plans[index]
		payload[position] = sqliteRelationRemakeSealPlan{
			OperationIndex: plan.operationIndex,
			Before:         plan.before,
			After:          plan.after,
			Targets:        plan.targets,
			Temporary:      plan.temporary,
			PrimaryKey:     plan.primaryKey,
			Sequence: sqliteRelationRemakeSealSequence{
				Present: plan.sequence.present,
				Name:    plan.sequence.name,
				Value:   plan.sequence.value,
			},
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func verifySQLiteRelationRemakePlans(
	plans map[int]sqliteRelationRemakePlan,
	want [sha256.Size]byte,
) error {
	got, err := hashSQLiteRelationRemakePlans(plans)
	if err != nil {
		return fmt.Errorf("hash sealed relation remake plans: %w", err)
	}
	if got != want {
		return errors.New("sealed relation remake plans changed after physical preflight")
	}
	return nil
}

func compileSQLiteRelationRemakeCreate(plan sqliteRelationRemakePlan) (string, error) {
	model := plan.after.Clone()
	model.DBTable = plan.temporary
	statement, err := compileSQLiteRelationCreateModel(model, plan.targets)
	if err != nil {
		return "", err
	}
	return strings.Replace(statement, "CREATE TABLE ", `CREATE TABLE "main".`, 1), nil
}

func executeSQLiteRelationRemake(
	ctx context.Context,
	executor migrationSQLExecutor,
	plan sqliteRelationRemakePlan,
) error {
	var beforeRows int64
	if err := executor.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM `+qualifiedSQLiteRelationMain(plan.before.DBTable),
	).Scan(&beforeRows); err != nil {
		return fmt.Errorf("count rows before relation remake: %w", err)
	}
	createStatement, err := compileSQLiteRelationRemakeCreate(plan)
	if err != nil {
		return fmt.Errorf("compile sealed relation remake: %w", err)
	}
	if _, err := executor.ExecContext(ctx, createStatement); err != nil {
		return fmt.Errorf("create relation remake table %q: %w", plan.temporary, err)
	}
	columns := make([]string, len(plan.after.Fields))
	for index := range plan.after.Fields {
		quoted, err := quoteIdentifier(plan.after.Fields[index].Column)
		if err != nil {
			return fmt.Errorf("quote relation remake retained column: %w", err)
		}
		columns[index] = quoted
	}
	primaryKey, err := quoteIdentifier(plan.primaryKey.Column)
	if err != nil {
		return fmt.Errorf("quote relation remake primary key: %w", err)
	}
	copyStatement := `INSERT INTO ` + qualifiedSQLiteRelationMain(plan.temporary) +
		` (` + strings.Join(columns, ", ") + `) SELECT ` + strings.Join(columns, ", ") +
		` FROM ` + qualifiedSQLiteRelationMain(plan.before.DBTable) + ` ORDER BY ` + primaryKey
	copyResult, err := executor.ExecContext(ctx, copyStatement)
	if err != nil {
		return fmt.Errorf("copy retained rows during relation remake: %w", err)
	}
	copiedByResult, err := copyResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("count relation remake copied rows affected: %w", err)
	}
	var copiedRows int64
	if err := executor.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM `+qualifiedSQLiteRelationMain(plan.temporary),
	).Scan(&copiedRows); err != nil {
		return fmt.Errorf("count copied rows during relation remake: %w", err)
	}
	if copiedByResult != beforeRows || copiedRows != beforeRows {
		return fmt.Errorf(
			"relation remake copied rows affected=%d stored=%d, want %d",
			copiedByResult,
			copiedRows,
			beforeRows,
		)
	}
	if _, err := executor.ExecContext(ctx, `DROP TABLE `+qualifiedSQLiteRelationMain(plan.before.DBTable)); err != nil {
		return fmt.Errorf("drop relation remake source table %q: %w", plan.before.DBTable, err)
	}
	finalName, err := quoteIdentifier(plan.after.DBTable)
	if err != nil {
		return fmt.Errorf("quote relation remake final table: %w", err)
	}
	if _, err := executor.ExecContext(
		ctx,
		`ALTER TABLE `+qualifiedSQLiteRelationMain(plan.temporary)+` RENAME TO `+finalName,
	); err != nil {
		return fmt.Errorf("rename relation remake table %q: %w", plan.temporary, err)
	}
	if _, err := executor.ExecContext(
		ctx,
		`DELETE FROM "main"."sqlite_sequence" WHERE "name" COLLATE NOCASE IN (?, ?)`,
		plan.temporary,
		plan.after.DBTable,
	); err != nil {
		return fmt.Errorf("clear relation remake sequence rows: %w", err)
	}
	if plan.sequence.present {
		if _, err := executor.ExecContext(
			ctx,
			`INSERT INTO "main"."sqlite_sequence" ("name", "seq") VALUES (?, ?)`,
			plan.sequence.name,
			plan.sequence.value,
		); err != nil {
			return fmt.Errorf("restore relation remake sequence: %w", err)
		}
	}
	if err := verifySQLiteRelationRemakeSequence(ctx, executor, plan.after.DBTable, plan.sequence); err != nil {
		return err
	}
	return nil
}

func qualifiedSQLiteRelationMain(identifier string) string {
	quoted, err := quoteIdentifier(identifier)
	if err != nil {
		// Every caller uses a preflighted identifier and checks compilation before
		// mutation. Preserve an impossible value as invalid SQL rather than widen
		// this hot-path helper's return signature.
		return `"main".""`
	}
	return `"main".` + quoted
}

func verifySQLiteRelationRemakeSequence(
	ctx context.Context,
	executor migrationSQLExecutor,
	table string,
	want sqliteRelationSequenceSnapshot,
) (resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT typeof("name"), "name", typeof("seq"), `+
			`CASE WHEN typeof("seq") = 'integer' THEN "seq" ELSE NULL END `+
			`FROM "main"."sqlite_sequence" WHERE "name" COLLATE NOCASE = ? LIMIT 2`,
		table,
	)
	if err != nil {
		return fmt.Errorf("verify relation remake sequence: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close relation remake sequence: %w", closeErr))
		}
	}()
	count := 0
	for rows.Next() {
		var nameType, name, sequenceType string
		var sequence sql.NullInt64
		if err := rows.Scan(&nameType, &name, &sequenceType, &sequence); err != nil {
			return fmt.Errorf("scan relation remake sequence: %w", err)
		}
		count++
		if count > 1 || !want.present || nameType != "text" || name != table ||
			sequenceType != "integer" || !sequence.Valid || sequence.Int64 != want.value {
			return fmt.Errorf("restored sqlite_sequence for %q is outside the sealed shape", table)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation remake sequence: %w", err)
	}
	wantCount := 0
	if want.present {
		wantCount = 1
	}
	if count != wantCount {
		return fmt.Errorf("restored sqlite_sequence row count for %q=%d, want %d", table, count, wantCount)
	}
	return nil
}
