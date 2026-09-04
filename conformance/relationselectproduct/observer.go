// Package relationselectproduct executes the checked-in generated
// REL-009/010/011 project against a manually provisioned SQLite fixture. It
// does not import relation oracle or not-implemented artifacts.
package relationselectproduct

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationselectproduct/authors"
	"github.com/progresshans/godj/conformance/relationselectproduct/blog"
	"github.com/progresshans/godj/conformance/relationselectproduct/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type QueryMetrics struct {
	QueryCount         int64
	StatementKinds     []string
	JoinKinds          []string
	InnerJoinCount     int64
	LeftOuterJoinCount int64
	AccessExtraQueries int64
	MutationCount      int64
}

type PostRelatedRow struct {
	PostID int64
	Name   *string
}

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

type RequiredObservation struct {
	Plain        []PostRelatedRow
	Eager        []PostRelatedRow
	PlainMetrics QueryMetrics
	EagerMetrics QueryMetrics
}

type NullableObservation struct {
	Rows    []PostRelatedRow
	Metrics QueryMetrics
}

type InvalidObservation struct {
	Err     error
	Metrics QueryMetrics
}

type Observation struct {
	Required RequiredObservation
	Nullable NullableObservation
	Invalid  InvalidObservation
	DBState  DatabaseState
}

type authorSeed struct {
	id   int64
	name string
}

type postSeed struct {
	id         int64
	title      string
	authorID   int64
	reviewerID *int64
}

type fixtureConfig struct {
	authors               []authorSeed
	posts                 []postSeed
	postsDescending       bool
	repeatRequiredEager   bool
	forceRequiredCold     bool
	forceNullableCold     bool
	invalidPath           string
	allowInvalidPathQuery bool
}

type recordedQuery struct {
	statement string
	arguments []any
	plan      query.Plan
}

type recordingQueryer struct {
	backend *sqlite.Backend
	mu      sync.Mutex
	records []recordedQuery
}

var databaseSequence atomic.Uint64

func Observe(ctx context.Context) (Observation, error) {
	return observe(ctx, defaultFixtureConfig())
}

func observe(ctx context.Context, config fixtureConfig) (Observation, error) {
	if ctx == nil {
		return Observation{}, fmt.Errorf("observe REL-009/010/011: context is nil")
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-rel009-011-%d", databaseSequence.Add(1)))
	if err != nil {
		return Observation{}, fmt.Errorf("open REL-009/010/011 SQLite fixture: %w", err)
	}
	observation, observeErr := observeWithBackend(ctx, backend, config)
	closeErr := backend.Close()
	if observeErr != nil {
		return Observation{}, errors.Join(observeErr, closeErr)
	}
	if closeErr != nil {
		return Observation{}, closeErr
	}
	return observation, nil
}

func observeWithBackend(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) (Observation, error) {
	if err := provision(ctx, backend, config); err != nil {
		return Observation{}, err
	}
	objects, err := project.BindObjects()
	if err != nil {
		return Observation{}, fmt.Errorf("bind generated select-related objects: %w", err)
	}
	recorder := &recordingQueryer{backend: backend}

	plainStart := recorder.mark()
	plainModels, err := orderedPosts(recorder, config.postsDescending).All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-009 plain posts: %w", err)
	}
	plainAccessStart := recorder.mark()
	plain := make([]PostRelatedRow, len(plainModels))
	for index, model := range plainModels {
		object, err := objects.BlogPost.From(recorder, model)
		if err != nil {
			return Observation{}, fmt.Errorf("wrap REL-009 plain post %d: %w", index, err)
		}
		author, err := object.Author(ctx)
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-009 plain author %d: %w", index, err)
		}
		name := author.Name
		plain[index] = PostRelatedRow{PostID: model.ID, Name: &name}
	}
	plainRecords := recorder.recordsSince(plainStart)
	plainAccessRecords := recorder.recordsSince(plainAccessStart)
	plainMetrics := metricsFor(plainRecords, plainAccessRecords)
	if err := validatePlainTrace(plainRecords, plainAccessRecords); err != nil {
		return Observation{}, err
	}

	requiredStart := recorder.mark()
	requiredQuery := orderedPosts(recorder, config.postsDescending)
	required := objects.BlogPost.SelectRelated(requiredQuery).Author()
	requiredObjects, err := required.All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-009 eager posts: %w", err)
	}
	if config.repeatRequiredEager {
		freshRequired := objects.BlogPost.SelectRelated(requiredQuery.Fresh()).Author()
		if _, err := freshRequired.All(ctx); err != nil {
			return Observation{}, fmt.Errorf("repeat REL-009 eager posts: %w", err)
		}
	}
	requiredAccessStart := recorder.mark()
	eager := make([]PostRelatedRow, len(requiredObjects))
	for index, object := range requiredObjects {
		model, err := object.Model()
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-009 eager source %d: %w", index, err)
		}
		if config.forceRequiredCold && index == 0 {
			object, err = object.Fresh()
			if err != nil {
				return Observation{}, fmt.Errorf("fresh REL-009 eager object: %w", err)
			}
		}
		author, err := object.Author(ctx)
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-009 eager author %d: %w", index, err)
		}
		name := author.Name
		eager[index] = PostRelatedRow{PostID: model.ID, Name: &name}
	}
	requiredRecords := recorder.recordsBetween(requiredStart, requiredAccessStart)
	requiredAccessRecords := recorder.recordsSince(requiredAccessStart)
	requiredMetrics := metricsFor(append(append([]recordedQuery(nil), requiredRecords...), requiredAccessRecords...), requiredAccessRecords)
	if err := validateEagerTrace(requiredRecords, requiredAccessRecords, "author", false); err != nil {
		return Observation{}, err
	}

	nullableStart := recorder.mark()
	nullableQuery := orderedPosts(recorder, config.postsDescending)
	nullableObjects, err := objects.BlogPost.SelectRelated(nullableQuery).Reviewer().All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-010 eager posts: %w", err)
	}
	nullableAccessStart := recorder.mark()
	nullable := make([]PostRelatedRow, len(nullableObjects))
	for index, object := range nullableObjects {
		model, err := object.Model()
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-010 eager source %d: %w", index, err)
		}
		if config.forceNullableCold && index == 0 {
			object, err = object.Fresh()
			if err != nil {
				return Observation{}, fmt.Errorf("fresh REL-010 eager object: %w", err)
			}
		}
		reviewer, ok, err := object.Reviewer(ctx)
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-010 eager reviewer %d: %w", index, err)
		}
		var name *string
		if ok {
			value := reviewer.Name
			name = &value
		}
		nullable[index] = PostRelatedRow{PostID: model.ID, Name: name}
	}
	nullableRecords := recorder.recordsBetween(nullableStart, nullableAccessStart)
	nullableAccessRecords := recorder.recordsSince(nullableAccessStart)
	nullableMetrics := metricsFor(append(append([]recordedQuery(nil), nullableRecords...), nullableAccessRecords...), nullableAccessRecords)
	if err := validateEagerTrace(nullableRecords, nullableAccessRecords, "reviewer", true); err != nil {
		return Observation{}, err
	}

	invalidStart := recorder.mark()
	invalidErr := observeInvalidReverse(config.invalidPath)
	invalidRecords := recorder.recordsSince(invalidStart)
	if config.allowInvalidPathQuery {
		if _, err := orderedPosts(recorder, false).Fresh().All(ctx); err != nil {
			return Observation{}, fmt.Errorf("forced REL-011 query: %w", err)
		}
		invalidRecords = recorder.recordsSince(invalidStart)
	}
	if err := validateInvalidTrace(invalidErr, config.invalidPath, invalidRecords); err != nil {
		return Observation{}, err
	}

	state, err := readState(ctx, backend)
	if err != nil {
		return Observation{}, err
	}
	return Observation{
		Required: RequiredObservation{
			Plain:        plain,
			Eager:        eager,
			PlainMetrics: plainMetrics,
			EagerMetrics: requiredMetrics,
		},
		Nullable: NullableObservation{Rows: nullable, Metrics: nullableMetrics},
		Invalid: InvalidObservation{
			Err:     invalidErr,
			Metrics: metricsFor(invalidRecords, nil),
		},
		DBState: state,
	}, nil
}

func observeInvalidReverse(path string) error {
	binding, err := project.Bind()
	if err != nil {
		return fmt.Errorf("bind REL-011 negative project: %w", err)
	}
	source, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		authors.AuthorDescriptor{},
	)
	if err != nil {
		return fmt.Errorf("bind REL-011 negative source: %w", err)
	}
	_, err = orm.ResolveForwardSelectPath(source, path)
	return err
}

func orderedPosts(backend db.Queryer, descending bool) orm.QuerySet[blog.Post] {
	querySet := blog.PostObjects.Using(backend)
	if descending {
		return querySet.OrderBy(blog.PostFields.ID.Desc())
	}
	return querySet.OrderBy(blog.PostFields.ID.Asc())
}

func (r *recordingQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.records = append(r.records, recordedQuery{
		statement: statement,
		arguments: append([]any(nil), arguments...),
		plan:      plan,
	})
	r.mu.Unlock()
	return r.backend.Query(ctx, plan)
}

func (r *recordingQueryer) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *recordingQueryer) recordsSince(start int) []recordedQuery {
	return r.recordsBetween(start, r.mark())
}

func (r *recordingQueryer) recordsBetween(start, end int) []recordedQuery {
	r.mu.Lock()
	defer r.mu.Unlock()
	if start < 0 || end < start || end > len(r.records) {
		return nil
	}
	result := make([]recordedQuery, end-start)
	for index, record := range r.records[start:end] {
		result[index] = recordedQuery{
			statement: record.statement,
			arguments: append([]any(nil), record.arguments...),
			plan:      record.plan,
		}
	}
	return result
}

func metricsFor(records, accessRecords []recordedQuery) QueryMetrics {
	metrics := QueryMetrics{
		StatementKinds:     []string{},
		JoinKinds:          []string{},
		AccessExtraQueries: int64(len(accessRecords)),
	}
	for _, record := range records {
		if strings.HasPrefix(strings.TrimSpace(record.statement), "SELECT ") {
			metrics.QueryCount++
			metrics.StatementKinds = append(metrics.StatementKinds, "SELECT")
		}
		innerCount := strings.Count(record.statement, " INNER JOIN ")
		leftCount := strings.Count(record.statement, " LEFT OUTER JOIN ")
		metrics.InnerJoinCount += int64(innerCount)
		metrics.LeftOuterJoinCount += int64(leftCount)
		for index := 0; index < innerCount; index++ {
			metrics.JoinKinds = append(metrics.JoinKinds, "INNER")
		}
		for index := 0; index < leftCount; index++ {
			metrics.JoinKinds = append(metrics.JoinKinds, "LEFT_OUTER")
		}
	}
	return metrics
}

func validatePlainTrace(records, accessRecords []recordedQuery) error {
	if len(records) != 4 || len(accessRecords) != 3 {
		return fmt.Errorf("REL-009 plain trace query/access count = %d/%d, want 4/3", len(records), len(accessRecords))
	}
	for index, record := range records {
		if _, ok := record.plan.RelationProjection(); ok || strings.Contains(record.statement, " JOIN ") {
			return fmt.Errorf("REL-009 plain trace query %d contains eager projection/JOIN", index)
		}
	}
	return nil
}

func validateEagerTrace(records, accessRecords []recordedQuery, field string, nullable bool) error {
	if len(records) != 1 || len(accessRecords) != 0 {
		return fmt.Errorf("select-related %s trace query/access count = %d/%d, want 1/0", field, len(records), len(accessRecords))
	}
	projection, ok := records[0].plan.RelationProjection()
	if !ok || projection.Hop().Field() != field || projection.Hop().Nullable() != nullable {
		return fmt.Errorf("select-related %s trace has wrong or missing canonical projection", field)
	}
	wantJoin := " INNER JOIN "
	forbiddenJoin := " LEFT OUTER JOIN "
	if nullable {
		wantJoin, forbiddenJoin = forbiddenJoin, wantJoin
	}
	if strings.Count(records[0].statement, wantJoin) != 1 || strings.Contains(records[0].statement, forbiddenJoin) {
		return fmt.Errorf("select-related %s trace has wrong JOIN shape: %s", field, records[0].statement)
	}
	return nil
}

func validateInvalidTrace(err error, path string, records []recordedQuery) error {
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != query.CategoryField ||
		queryError.Code != query.CodeInvalidRelatedPath || queryError.Field != path {
		return fmt.Errorf("REL-011 error = %v, want field_error/invalid_related_path field=%q", err, path)
	}
	if len(records) != 0 {
		return fmt.Errorf("REL-011 reverse path executed %d query calls", len(records))
	}
	return nil
}

func provision(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) error {
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(200) NOT NULL)`,
		`CREATE TABLE "blog_post" ("id" INTEGER NOT NULL PRIMARY KEY, "title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE RESTRICT, "reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE SET NULL)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision select-related schema: %w", err)
		}
	}
	for _, author := range config.authors {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`, author.id, author.name); err != nil {
			return fmt.Errorf("provision select-related author %d: %w", author.id, err)
		}
	}
	for _, post := range config.posts {
		var reviewer any
		if post.reviewerID != nil {
			reviewer = *post.reviewerID
		}
		if _, err := backend.ExecContext(ctx, `INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`, post.id, post.title, post.authorID, reviewer); err != nil {
			return fmt.Errorf("provision select-related post %d: %w", post.id, err)
		}
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).OrderBy(authors.AuthorFields.ID.Asc()).All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read select-related authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).OrderBy(blog.PostFields.ID.Asc()).All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read select-related posts: %w", err)
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

func defaultFixtureConfig() fixtureConfig {
	reviewer := int64(2)
	return fixtureConfig{
		authors: []authorSeed{
			{id: 1, name: "Ada"},
			{id: 2, name: "Bob"},
			{id: 3, name: "Cleo"},
		},
		posts: []postSeed{
			{id: 10, title: "Alpha", authorID: 1, reviewerID: &reviewer},
			{id: 11, title: "Beta", authorID: 1},
			{id: 12, title: "Gamma", authorID: 3, reviewerID: &reviewer},
		},
		invalidPath: "posts",
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePostRelatedRows(rows []PostRelatedRow) []PostRelatedRow {
	result := make([]PostRelatedRow, len(rows))
	for index, row := range rows {
		result[index] = PostRelatedRow{PostID: row.PostID}
		if row.Name != nil {
			name := *row.Name
			result[index].Name = &name
		}
	}
	return result
}

func equalPostRelatedRows(left, right []PostRelatedRow) bool {
	return reflect.DeepEqual(clonePostRelatedRows(left), clonePostRelatedRows(right))
}
