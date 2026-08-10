// Package relationobjectproduct executes the checked-in generated REL-003/006
// project against a manually provisioned SQLite fixture. It does not import
// relation oracle or not-implemented artifacts.
package relationobjectproduct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationobjectproduct/authors"
	"github.com/progresshans/godj/conformance/relationobjectproduct/blog"
	"github.com/progresshans/godj/conformance/relationobjectproduct/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type QueryMetrics struct {
	QueryCount         int64
	StatementKinds     []string
	JoinKinds          []string
	InnerJoinCount     int64
	LeftOuterJoinCount int64
}

type AccessStep struct {
	Name    string
	Metrics QueryMetrics
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

type ForwardCacheObservation struct {
	Cold  AuthorRow
	Warm  AuthorRow
	Steps []AccessStep
}

type NullableObservation struct {
	Reviewer           *AuthorRow
	IsNullPostIDs      []int64
	NullAccess         QueryMetrics
	IsNullConstruction QueryMetrics
	IsNullEvaluation   QueryMetrics
}

type Observation struct {
	Forward  ForwardCacheObservation
	Nullable NullableObservation
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
	authors      []authorSeed
	posts        []postSeed
	isNullValue  bool
	descending   bool
	loadPostID   int64
	nullablePost int64
}

type recordingQueryer struct {
	backend    *sqlite.Backend
	mu         sync.Mutex
	statements []string
}

var databaseSequence atomic.Uint64

func Observe(ctx context.Context) (Observation, error) {
	return observe(ctx, defaultFixtureConfig())
}

func observe(ctx context.Context, config fixtureConfig) (Observation, error) {
	if ctx == nil {
		return Observation{}, fmt.Errorf("observe REL-003/006: context is nil")
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-rel003-006-%d", databaseSequence.Add(1)))
	if err != nil {
		return Observation{}, fmt.Errorf("open REL-003/006 SQLite fixture: %w", err)
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
		return Observation{}, fmt.Errorf("bind generated REL-003/006 objects: %w", err)
	}
	recorder := &recordingQueryer{backend: backend}

	post, err := loadPost(ctx, recorder, config.loadPostID)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-003 source post: %w", err)
	}
	object, err := objects.BlogPost.From(recorder, post)
	if err != nil {
		return Observation{}, fmt.Errorf("wrap REL-003 source post: %w", err)
	}
	// Mutating the caller's source after From must not redirect either the
	// generated object snapshot or its related-object loaders.
	post.AuthorID = 3
	if post.ReviewerID != nil {
		*post.ReviewerID = 3
	}

	coldStart := recorder.mark()
	cold, err := object.Author(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("cold REL-003 author access: %w", err)
	}
	coldMetrics := recorder.metricsSince(coldStart)
	coldRow := AuthorRow{ID: cold.ID, Name: cold.Name}
	// A returned model is a clone. This mutation must not alter the canonical
	// QuerySet cache observed by the warm call.
	cold.Name = "caller-mutated"

	warmStart := recorder.mark()
	warm, err := object.Author(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("warm REL-003 author access: %w", err)
	}
	warmMetrics := recorder.metricsSince(warmStart)
	forward := ForwardCacheObservation{
		Cold: coldRow,
		Warm: AuthorRow{ID: warm.ID, Name: warm.Name},
		Steps: []AccessStep{
			{Name: "cold_access", Metrics: coldMetrics},
			{Name: "warm_access", Metrics: warmMetrics},
		},
	}

	nullablePost, err := loadPost(ctx, recorder, config.nullablePost)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-006 source post: %w", err)
	}
	nullableObject, err := objects.BlogPost.From(recorder, nullablePost)
	if err != nil {
		return Observation{}, fmt.Errorf("wrap REL-006 source post: %w", err)
	}
	nullStart := recorder.mark()
	reviewer, reviewerOK, err := nullableObject.Reviewer(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("REL-006 nullable reviewer access: %w", err)
	}
	var reviewerRow *AuthorRow
	if reviewerOK {
		value := AuthorRow{ID: reviewer.ID, Name: reviewer.Name}
		reviewerRow = &value
	}
	nullMetrics := recorder.metricsSince(nullStart)

	constructionStart := recorder.mark()
	typedPredicate := objects.BlogPost.Reviewer.IsNull(config.isNullValue)
	dynamicPredicates, err := objects.BlogPost.ParseDynamic(nil, []orm.LookupInput{
		{Key: "reviewer__isnull", Value: config.isNullValue},
	})
	if err != nil {
		return Observation{}, fmt.Errorf("build dynamic REL-006 isnull: %w", err)
	}
	typedQuery := blog.PostObjects.Using(recorder).Filter(typedPredicate)
	dynamicQuery := blog.PostObjects.Using(recorder).Filter(dynamicPredicates...)
	if config.descending {
		typedQuery = typedQuery.OrderBy(blog.PostFields.ID.Desc())
		dynamicQuery = dynamicQuery.OrderBy(blog.PostFields.ID.Desc())
	} else {
		typedQuery = typedQuery.OrderBy(blog.PostFields.ID.Asc())
		dynamicQuery = dynamicQuery.OrderBy(blog.PostFields.ID.Asc())
	}
	if !typedQuery.Plan().Equal(dynamicQuery.Plan()) {
		return Observation{}, fmt.Errorf("typed and dynamic REL-006 plans differ")
	}
	conditions := typedQuery.Plan().Conditions()
	if len(conditions) != 1 {
		return Observation{}, fmt.Errorf("REL-006 plan has %d conditions, want one", len(conditions))
	}
	path, related := conditions[0].RelationPath()
	if !related || path.TerminalScope() != query.RelationTerminalSourceKey || len(path.Hops()) != 1 || !path.Hops()[0].Nullable() {
		return Observation{}, fmt.Errorf("REL-006 plan lost nullable source-key relation provenance")
	}
	constructionMetrics := recorder.metricsSince(constructionStart)

	evaluationStart := recorder.mark()
	posts, err := typedQuery.All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("evaluate REL-006 isnull: %w", err)
	}
	evaluationMetrics := recorder.metricsSince(evaluationStart)
	identifiers := make([]int64, len(posts))
	for index := range posts {
		identifiers[index] = posts[index].ID
	}

	// Exercise the positive nullable loader path independently of the oracle
	// payload. The non-null reviewer must resolve through the same bounded
	// object loader, not through a manually constructed Author literal.
	positivePost, err := loadPost(ctx, recorder, 10)
	if err != nil {
		return Observation{}, fmt.Errorf("load positive nullable source: %w", err)
	}
	positiveObject, err := objects.BlogPost.From(recorder, positivePost)
	if err != nil {
		return Observation{}, fmt.Errorf("wrap positive nullable source: %w", err)
	}
	positiveReviewer, ok, err := positiveObject.Reviewer(ctx)
	if err != nil || !ok || positiveReviewer.ID != 2 {
		return Observation{}, fmt.Errorf("positive nullable reviewer = (%#v, %t, %v), want author 2", positiveReviewer, ok, err)
	}

	state, err := readState(ctx, backend)
	if err != nil {
		return Observation{}, err
	}
	return Observation{
		Forward: forward,
		Nullable: NullableObservation{
			Reviewer:           reviewerRow,
			IsNullPostIDs:      identifiers,
			NullAccess:         nullMetrics,
			IsNullConstruction: constructionMetrics,
			IsNullEvaluation:   evaluationMetrics,
		},
		DBState: state,
	}, nil
}

func (r *recordingQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	statement, _, err := sqlite.Compile(plan)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.statements = append(r.statements, statement)
	r.mu.Unlock()
	return r.backend.Query(ctx, plan)
}

func (r *recordingQueryer) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.statements)
}

func (r *recordingQueryer) metricsSince(start int) QueryMetrics {
	r.mu.Lock()
	statements := append([]string(nil), r.statements[start:]...)
	r.mu.Unlock()
	return classifyStatements(statements)
}

func classifyStatements(statements []string) QueryMetrics {
	metrics := QueryMetrics{QueryCount: int64(len(statements))}
	for _, statement := range statements {
		if strings.HasPrefix(strings.TrimSpace(statement), "SELECT ") {
			metrics.StatementKinds = append(metrics.StatementKinds, "SELECT")
		}
		innerCount := strings.Count(statement, " INNER JOIN ")
		leftCount := strings.Count(statement, " LEFT OUTER JOIN ")
		metrics.InnerJoinCount += int64(innerCount)
		metrics.LeftOuterJoinCount += int64(leftCount)
		for index := 0; index < innerCount; index++ {
			metrics.JoinKinds = append(metrics.JoinKinds, "INNER")
		}
		for index := 0; index < leftCount; index++ {
			metrics.JoinKinds = append(metrics.JoinKinds, "LEFT OUTER")
		}
	}
	return metrics
}

func loadPost(ctx context.Context, backend db.Queryer, identifier int64) (blog.Post, error) {
	posts, err := blog.PostObjects.Using(backend).
		Filter(blog.PostFields.ID.Exact(identifier)).
		All(ctx)
	if err != nil {
		return blog.Post{}, err
	}
	if len(posts) != 1 {
		return blog.Post{}, fmt.Errorf("post %d row count = %d, want one", identifier, len(posts))
	}
	return posts[0], nil
}

func provision(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "name" VARCHAR(200) NOT NULL
)`,
		`CREATE TABLE "blog_post" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE RESTRICT,
  "reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE SET NULL
)`,
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision REL-003/006 schema: %w", err)
		}
	}
	for _, author := range config.authors {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`, author.id, author.name); err != nil {
			return fmt.Errorf("provision REL-003/006 author %d: %w", author.id, err)
		}
	}
	for _, post := range config.posts {
		var reviewer any
		if post.reviewerID != nil {
			reviewer = *post.reviewerID
		}
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`,
			post.id,
			post.title,
			post.authorID,
			reviewer,
		); err != nil {
			return fmt.Errorf("provision REL-003/006 post %d: %w", post.id, err)
		}
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).
		OrderBy(authors.AuthorFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-003/006 authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-003/006 posts: %w", err)
	}
	authorRows := make([]AuthorRow, len(authorModels))
	for index := range authorModels {
		authorRows[index] = AuthorRow{ID: authorModels[index].ID, Name: authorModels[index].Name}
	}
	postRows := make([]PostRow, len(postModels))
	for index := range postModels {
		postRows[index] = PostRow{
			ID:         postModels[index].ID,
			Title:      postModels[index].Title,
			AuthorID:   postModels[index].AuthorID,
			ReviewerID: cloneIntegerPointer(postModels[index].ReviewerID),
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
		isNullValue:  true,
		loadPostID:   10,
		nullablePost: 11,
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
