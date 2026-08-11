// Package relationdeleteproduct executes the checked-in generated REL-007/008
// project against fresh, manually provisioned SQLite fixtures. Every result is
// derived from generated code and the live database.
package relationdeleteproduct

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationdeleteproduct/authors"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/blog"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

const (
	OperationQuery           = "QUERY"
	OperationRelationSetNull = "UPDATE"
	OperationDelete          = "DELETE"
)

type AuthorRow struct {
	ID   int64
	Name string
}

type PostRow struct {
	ID         int64
	Title      string
	AuthorID   int64
	ReviewerID *int64
}

type DatabaseState struct {
	Authors []AuthorRow
	Posts   []PostRow
}

type CallerState struct {
	ID         int64
	Name       string
	KeyPresent bool
}

type ForeignKeyShape struct {
	From     string
	ToTable  string
	ToColumn string
	OnDelete string
}

type PhysicalSchema struct {
	ForeignKeysEnabled int64
	ForeignKeys        []ForeignKeyShape
	AuthorNullable     bool
	ReviewerNullable   bool
	TriggerCount       int64
}

type MutationRow struct {
	Kind         string
	AffectedRows int64
}

type DeleteMetrics struct {
	TransactionCount     int64
	QueryCount           int64
	RelationSetNullCount int64
	DeleteCount          int64
	OperationOrder       []string
	MutationOrder        []string
	MutationRows         []MutationRow
	RelationSetNullRows  []int64
	DeleteRows           []int64
}

type DeleteObservation struct {
	Returned     int64
	Err          error
	CallerBefore CallerState
	CallerAfter  CallerState
	Before       DatabaseState
	After        DatabaseState
	Metrics      DeleteMetrics
	Schema       PhysicalSchema
}

type Observation struct {
	Protect DeleteObservation
	SetNull DeleteObservation
}

type fixtureConfig struct {
	authorDeleteAction     string
	reviewerDeleteAction   string
	omitAuthorForeignKey   bool
	omitReviewerForeignKey bool
	addBlogTrigger         bool
	addExternalProtect     bool
}

type relationFixture struct {
	backend   *sqlite.Backend
	inspector *sql.DB
}

type recordedOperation struct {
	kind string
	rows int64
}

type recordingRelationAtomic struct {
	backend      db.RelationAtomic
	mu           sync.Mutex
	transactions int64
	operations   []recordedOperation
}

type recordingRelationSession struct {
	db.RelationSession
	recorder *recordingRelationAtomic
}

var databaseSequence atomic.Uint64

var _ db.RelationAtomic = (*recordingRelationAtomic)(nil)
var _ db.RelationSession = (*recordingRelationSession)(nil)

// Observe runs REL-007 and REL-008 against separate fresh databases.
func Observe(ctx context.Context) (Observation, error) {
	if ctx == nil {
		return Observation{}, fmt.Errorf("observe REL-007/008: context is nil")
	}
	protect, err := observeDelete(ctx, 1, defaultFixtureConfig())
	if err != nil {
		return Observation{}, fmt.Errorf("observe REL-007: %w", err)
	}
	setNull, err := observeDelete(ctx, 2, defaultFixtureConfig())
	if err != nil {
		return Observation{}, fmt.Errorf("observe REL-008: %w", err)
	}
	return Observation{Protect: protect, SetNull: setNull}, nil
}

func observeDelete(ctx context.Context, targetID int64, config fixtureConfig) (DeleteObservation, error) {
	fixture, err := openFixture(ctx)
	if err != nil {
		return DeleteObservation{}, err
	}
	observation, observeErr := observeWithFixture(ctx, fixture, targetID, config)
	closeErr := fixture.close()
	if observeErr != nil {
		return DeleteObservation{}, errors.Join(observeErr, closeErr)
	}
	if closeErr != nil {
		return DeleteObservation{}, closeErr
	}
	return observation, nil
}

func observeWithFixture(
	ctx context.Context,
	fixture *relationFixture,
	targetID int64,
	config fixtureConfig,
) (DeleteObservation, error) {
	if err := provision(ctx, fixture.backend, config); err != nil {
		return DeleteObservation{}, err
	}
	physical, err := inspectPhysicalSchema(ctx, fixture.inspector)
	if err != nil {
		return DeleteObservation{}, err
	}
	if err := validatePhysicalSchema(physical); err != nil {
		return DeleteObservation{}, err
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		return DeleteObservation{}, fmt.Errorf("bind generated relation deleters: %w", err)
	}
	before, err := readState(ctx, fixture.backend)
	if err != nil {
		return DeleteObservation{}, err
	}
	target, found, err := authors.AuthorObjects.Using(fixture.backend).
		Filter(authors.AuthorFields.ID.Exact(targetID)).
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil {
		return DeleteObservation{}, fmt.Errorf("load relation-delete author %d: %w", targetID, err)
	}
	if !found {
		return DeleteObservation{}, fmt.Errorf("load relation-delete author %d: row not found", targetID)
	}
	callerBefore := callerState(target)
	recorder := &recordingRelationAtomic{backend: fixture.backend}
	returned, deleteErr := deleters.AuthorsAuthor.Delete(ctx, recorder, &target)
	after, err := readState(ctx, fixture.backend)
	if err != nil {
		return DeleteObservation{}, err
	}
	return DeleteObservation{
		Returned:     returned,
		Err:          deleteErr,
		CallerBefore: callerBefore,
		CallerAfter:  callerState(target),
		Before:       before,
		After:        after,
		Metrics:      recorder.snapshot(),
		Schema:       physical,
	}, nil
}

func (recorder *recordingRelationAtomic) AtomicRelation(
	ctx context.Context,
	callback func(db.RelationSession) error,
) error {
	recorder.mu.Lock()
	recorder.transactions++
	recorder.mu.Unlock()
	if callback == nil {
		return recorder.backend.AtomicRelation(ctx, nil)
	}
	return recorder.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		return callback(&recordingRelationSession{RelationSession: session, recorder: recorder})
	})
}

func (session *recordingRelationSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	session.recorder.begin(OperationQuery)
	return session.RelationSession.Query(ctx, plan)
}

func (session *recordingRelationSession) RelationSetNull(
	ctx context.Context,
	plan query.RelationSetNullPlan,
) (int64, error) {
	position := session.recorder.begin(OperationRelationSetNull)
	rows, err := session.RelationSession.RelationSetNull(ctx, plan)
	session.recorder.finish(position, rows)
	return rows, err
}

func (session *recordingRelationSession) Delete(
	ctx context.Context,
	plan query.DeletePlan,
) (int64, error) {
	position := session.recorder.begin(OperationDelete)
	rows, err := session.RelationSession.Delete(ctx, plan)
	session.recorder.finish(position, rows)
	return rows, err
}

func (recorder *recordingRelationAtomic) begin(kind string) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	position := len(recorder.operations)
	recorder.operations = append(recorder.operations, recordedOperation{kind: kind})
	return position
}

func (recorder *recordingRelationAtomic) finish(position int, rows int64) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if position >= 0 && position < len(recorder.operations) {
		recorder.operations[position].rows = rows
	}
}

func (recorder *recordingRelationAtomic) snapshot() DeleteMetrics {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	metrics := DeleteMetrics{
		TransactionCount:    recorder.transactions,
		OperationOrder:      make([]string, 0, len(recorder.operations)),
		MutationOrder:       []string{},
		MutationRows:        []MutationRow{},
		RelationSetNullRows: []int64{},
		DeleteRows:          []int64{},
	}
	for _, operation := range recorder.operations {
		metrics.OperationOrder = append(metrics.OperationOrder, operation.kind)
		switch operation.kind {
		case OperationQuery:
			metrics.QueryCount++
		case OperationRelationSetNull:
			metrics.RelationSetNullCount++
			metrics.MutationOrder = append(metrics.MutationOrder, operation.kind)
			metrics.MutationRows = append(metrics.MutationRows, MutationRow{Kind: operation.kind, AffectedRows: operation.rows})
			metrics.RelationSetNullRows = append(metrics.RelationSetNullRows, operation.rows)
		case OperationDelete:
			metrics.DeleteCount++
			metrics.MutationOrder = append(metrics.MutationOrder, operation.kind)
			metrics.MutationRows = append(metrics.MutationRows, MutationRow{Kind: operation.kind, AffectedRows: operation.rows})
			metrics.DeleteRows = append(metrics.DeleteRows, operation.rows)
		}
	}
	return metrics
}

func openFixture(ctx context.Context) (*relationFixture, error) {
	name := fmt.Sprintf("godj-rel007-008-%d", databaseSequence.Add(1))
	dsn := "file:" + url.PathEscape(name) + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	backend, err := sqlite.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open relation-delete SQLite backend: %w", err)
	}
	inspector, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("open relation-delete SQLite inspector: %w", err)
	}
	if err := inspector.PingContext(ctx); err != nil {
		return nil, errors.Join(
			fmt.Errorf("ping relation-delete SQLite inspector: %w", err),
			inspector.Close(),
			backend.Close(),
		)
	}
	return &relationFixture{backend: backend, inspector: inspector}, nil
}

func (fixture *relationFixture) close() error {
	if fixture == nil {
		return nil
	}
	var inspectorErr error
	if fixture.inspector != nil {
		inspectorErr = fixture.inspector.Close()
	}
	var backendErr error
	if fixture.backend != nil {
		backendErr = fixture.backend.Close()
	}
	return errors.Join(inspectorErr, backendErr)
}

func provision(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) error {
	columns := []string{
		`"id" INTEGER NOT NULL PRIMARY KEY`,
		`"title" VARCHAR(200) NOT NULL`,
		`"author_id" INTEGER NOT NULL`,
		`"reviewer_id" INTEGER NULL`,
	}
	if !config.omitAuthorForeignKey {
		columns = append(columns,
			fmt.Sprintf(`FOREIGN KEY ("author_id") REFERENCES "authors_author" ("id") ON DELETE %s`, config.authorDeleteAction),
		)
	}
	if !config.omitReviewerForeignKey {
		columns = append(columns,
			fmt.Sprintf(`FOREIGN KEY ("reviewer_id") REFERENCES "authors_author" ("id") ON DELETE %s`, config.reviewerDeleteAction),
		)
	}
	statements := []string{
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(200) NOT NULL)`,
		`CREATE TABLE "blog_post" (` + strings.Join(columns, ", ") + `)`,
	}
	if config.addBlogTrigger {
		statements = append(statements,
			`CREATE TRIGGER "blog_post_relation_side_effect" AFTER UPDATE ON "blog_post" BEGIN SELECT 1; END`,
		)
	}
	if config.addExternalProtect {
		statements = append(statements,
			`CREATE TABLE "external_author_hold" ("id" INTEGER NOT NULL PRIMARY KEY, "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE RESTRICT)`,
		)
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision relation-delete schema: %w", err)
		}
	}
	for _, author := range []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`, author.ID, author.Name); err != nil {
			return fmt.Errorf("provision relation-delete author %d: %w", author.ID, err)
		}
	}
	reviewer := int64(2)
	posts := []PostRow{
		{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
		{ID: 11, Title: "Beta", AuthorID: 1},
		{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
	}
	for _, post := range posts {
		var reviewerValue any
		if post.ReviewerID != nil {
			reviewerValue = *post.ReviewerID
		}
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`,
			post.ID,
			post.Title,
			post.AuthorID,
			reviewerValue,
		); err != nil {
			return fmt.Errorf("provision relation-delete post %d: %w", post.ID, err)
		}
	}
	if config.addExternalProtect {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "external_author_hold" ("id", "author_id") VALUES (1, 2)`); err != nil {
			return fmt.Errorf("provision relation-delete external hold: %w", err)
		}
	}
	return nil
}

func inspectPhysicalSchema(ctx context.Context, inspector *sql.DB) (PhysicalSchema, error) {
	if inspector == nil {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete schema: database is nil")
	}
	var result PhysicalSchema
	if err := inspector.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&result.ForeignKeysEnabled); err != nil {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete foreign_keys pragma: %w", err)
	}
	foreignRows, err := inspector.QueryContext(ctx, `PRAGMA foreign_key_list("blog_post")`)
	if err != nil {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete foreign keys: %w", err)
	}
	for foreignRows.Next() {
		var id, sequence int64
		var shape ForeignKeyShape
		var onUpdate, match string
		if err := foreignRows.Scan(
			&id,
			&sequence,
			&shape.ToTable,
			&shape.From,
			&shape.ToColumn,
			&onUpdate,
			&shape.OnDelete,
			&match,
		); err != nil {
			_ = foreignRows.Close()
			return PhysicalSchema{}, fmt.Errorf("scan relation-delete foreign key: %w", err)
		}
		result.ForeignKeys = append(result.ForeignKeys, shape)
	}
	foreignErr := foreignRows.Err()
	foreignCloseErr := foreignRows.Close()
	if foreignErr != nil || foreignCloseErr != nil {
		return PhysicalSchema{}, errors.Join(foreignErr, foreignCloseErr)
	}
	sort.Slice(result.ForeignKeys, func(left, right int) bool {
		return result.ForeignKeys[left].From < result.ForeignKeys[right].From
	})

	tableRows, err := inspector.QueryContext(ctx, `PRAGMA table_info("blog_post")`)
	if err != nil {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete table columns: %w", err)
	}
	var authorFound, reviewerFound bool
	for tableRows.Next() {
		var cid, notNull, primaryKey int64
		var name, fieldType string
		var defaultValue any
		if err := tableRows.Scan(&cid, &name, &fieldType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = tableRows.Close()
			return PhysicalSchema{}, fmt.Errorf("scan relation-delete table column: %w", err)
		}
		switch name {
		case "author_id":
			authorFound = true
			result.AuthorNullable = notNull == 0
		case "reviewer_id":
			reviewerFound = true
			result.ReviewerNullable = notNull == 0
		}
	}
	tableErr := tableRows.Err()
	tableCloseErr := tableRows.Close()
	if tableErr != nil || tableCloseErr != nil {
		return PhysicalSchema{}, errors.Join(tableErr, tableCloseErr)
	}
	if !authorFound || !reviewerFound {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete table: required FK columns are missing")
	}
	if err := inspector.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = 'blog_post'`,
	).Scan(&result.TriggerCount); err != nil {
		return PhysicalSchema{}, fmt.Errorf("inspect relation-delete triggers: %w", err)
	}
	return result, nil
}

func validatePhysicalSchema(schema PhysicalSchema) error {
	if schema.ForeignKeysEnabled != 1 {
		return fmt.Errorf("relation-delete inspector foreign_keys = %d, want 1", schema.ForeignKeysEnabled)
	}
	if len(schema.ForeignKeys) != 2 {
		return fmt.Errorf("relation-delete physical FK count = %d, want 2", len(schema.ForeignKeys))
	}
	for _, expected := range []string{"author_id", "reviewer_id"} {
		position := sort.Search(len(schema.ForeignKeys), func(index int) bool {
			return schema.ForeignKeys[index].From >= expected
		})
		if position == len(schema.ForeignKeys) || schema.ForeignKeys[position].From != expected {
			return fmt.Errorf("relation-delete physical FK %s is missing", expected)
		}
		foreignKey := schema.ForeignKeys[position]
		if foreignKey.ToTable != "authors_author" || foreignKey.ToColumn != "id" ||
			(foreignKey.OnDelete != "NO ACTION" && foreignKey.OnDelete != "RESTRICT") {
			return fmt.Errorf("relation-delete physical FK %s = %#v, want authors_author.id NO ACTION/RESTRICT", expected, foreignKey)
		}
	}
	if schema.AuthorNullable || !schema.ReviewerNullable {
		return fmt.Errorf(
			"relation-delete physical nullability author/reviewer = %t/%t, want false/true",
			schema.AuthorNullable,
			schema.ReviewerNullable,
		)
	}
	if schema.TriggerCount != 0 {
		return fmt.Errorf("relation-delete physical trigger count = %d, want 0", schema.TriggerCount)
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).OrderBy(authors.AuthorFields.ID.Asc()).All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read relation-delete authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).OrderBy(blog.PostFields.ID.Asc()).All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read relation-delete posts: %w", err)
	}
	authorRows := make([]AuthorRow, len(authorModels))
	for index, author := range authorModels {
		authorRows[index] = AuthorRow{ID: author.ID, Name: author.Name}
	}
	postRows := make([]PostRow, len(postModels))
	for index, post := range postModels {
		postRows[index] = PostRow{
			ID:         post.ID,
			Title:      post.Title,
			AuthorID:   post.AuthorID,
			ReviewerID: cloneIntegerPointer(post.ReviewerID),
		}
	}
	return DatabaseState{Authors: authorRows, Posts: postRows}, nil
}

func callerState(author authors.Author) CallerState {
	_, present := (authors.AuthorDescriptor{}).PrimaryKey(author)
	return CallerState{ID: author.ID, Name: author.Name, KeyPresent: present}
}

func defaultFixtureConfig() fixtureConfig {
	return fixtureConfig{
		authorDeleteAction:   "RESTRICT",
		reviewerDeleteAction: "NO ACTION",
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
