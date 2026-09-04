// Package relationreverseproduct executes the checked-in generated REL-005
// project against a manually provisioned SQLite fixture. It does not import
// relation oracle or not-implemented artifacts.
package relationreverseproduct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationreverseproduct/authors"
	"github.com/progresshans/godj/conformance/relationreverseproduct/blog"
	"github.com/progresshans/godj/conformance/relationreverseproduct/project"
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

type Observation struct {
	AccessorPostIDs []int64
	LookupAuthorIDs []int64
	Accessor        QueryMetrics
	Lookup          QueryMetrics
	DBState         DatabaseState
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
	authors            []authorSeed
	posts              []postSeed
	accessorAuthorID   int64
	lookupTitle        string
	accessorDescending bool
	lookupDescending   bool
	repeatLookup       bool
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
		return Observation{}, fmt.Errorf("observe REL-005: context is nil")
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-rel005-%d", databaseSequence.Add(1)))
	if err != nil {
		return Observation{}, fmt.Errorf("open REL-005 SQLite fixture: %w", err)
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
	reverseObjects, err := project.BindReverseObjects()
	if err != nil {
		return Observation{}, fmt.Errorf("bind generated REL-005 reverse objects: %w", err)
	}
	reverseRelations, err := project.BindReverseRelations()
	if err != nil {
		return Observation{}, fmt.Errorf("bind generated REL-005 reverse relations: %w", err)
	}
	recorder := &recordingQueryer{backend: backend}

	author, err := loadAuthor(ctx, recorder, config.accessorAuthorID)
	if err != nil {
		return Observation{}, fmt.Errorf("freshly load REL-005 author: %w", err)
	}
	constructionStart := recorder.mark()
	object, err := reverseObjects.AuthorsAuthor.From(recorder, author)
	if err != nil {
		return Observation{}, fmt.Errorf("wrap REL-005 author: %w", err)
	}
	set, err := object.Posts()
	if err != nil {
		return Observation{}, fmt.Errorf("select REL-005 posts accessor: %w", err)
	}
	if config.accessorDescending {
		set, err = set.OrderBy(blog.PostFields.ID.Desc())
	} else {
		set, err = set.OrderBy(blog.PostFields.ID.Asc())
	}
	if err != nil {
		return Observation{}, fmt.Errorf("order REL-005 posts accessor: %w", err)
	}
	if metrics := recorder.metricsSince(constructionStart); metrics.QueryCount != 0 {
		return Observation{}, fmt.Errorf("REL-005 accessor construction executed %d queries", metrics.QueryCount)
	}
	accessorStart := recorder.mark()
	posts, err := set.All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("evaluate REL-005 posts accessor: %w", err)
	}
	accessorMetrics := recorder.metricsSince(accessorStart)
	accessorPostIDs := make([]int64, len(posts))
	for index := range posts {
		accessorPostIDs[index] = posts[index].ID
	}

	lookupConstructionStart := recorder.mark()
	typedPredicate := reverseRelations.AuthorsAuthor.Posts.Title.Exact(config.lookupTitle)
	dynamicPredicates, err := reverseRelations.AuthorsAuthor.ParseDynamic(nil, []orm.LookupInput{
		{Key: "posts__title", Value: config.lookupTitle},
	})
	if err != nil {
		return Observation{}, fmt.Errorf("build dynamic REL-005 reverse lookup: %w", err)
	}
	typedQuery := authors.AuthorObjects.Using(recorder).Filter(typedPredicate)
	dynamicQuery := authors.AuthorObjects.Using(recorder).Filter(dynamicPredicates...)
	if config.lookupDescending {
		typedQuery = typedQuery.OrderBy(authors.AuthorFields.ID.Desc())
		dynamicQuery = dynamicQuery.OrderBy(authors.AuthorFields.ID.Desc())
	} else {
		typedQuery = typedQuery.OrderBy(authors.AuthorFields.ID.Asc())
		dynamicQuery = dynamicQuery.OrderBy(authors.AuthorFields.ID.Asc())
	}
	if !typedQuery.Plan().Equal(dynamicQuery.Plan()) {
		return Observation{}, fmt.Errorf("typed and dynamic REL-005 reverse lookup plans differ")
	}
	if metrics := recorder.metricsSince(lookupConstructionStart); metrics.QueryCount != 0 {
		return Observation{}, fmt.Errorf("REL-005 lookup construction executed %d queries", metrics.QueryCount)
	}
	lookupStart := recorder.mark()
	authorsResult, err := typedQuery.All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("evaluate REL-005 reverse lookup: %w", err)
	}
	if config.repeatLookup {
		if _, err := dynamicQuery.All(ctx); err != nil {
			return Observation{}, fmt.Errorf("repeat dynamic REL-005 reverse lookup: %w", err)
		}
	}
	lookupMetrics := recorder.metricsSince(lookupStart)
	lookupAuthorIDs := make([]int64, len(authorsResult))
	for index := range authorsResult {
		lookupAuthorIDs[index] = authorsResult[index].ID
	}

	state, err := readState(ctx, backend)
	if err != nil {
		return Observation{}, err
	}
	return Observation{
		AccessorPostIDs: accessorPostIDs,
		LookupAuthorIDs: lookupAuthorIDs,
		Accessor:        accessorMetrics,
		Lookup:          lookupMetrics,
		DBState:         state,
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
	metrics := QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
	metrics.QueryCount = int64(len(statements))
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

func loadAuthor(ctx context.Context, backend db.Queryer, identifier int64) (authors.Author, error) {
	models, err := authors.AuthorObjects.Using(backend).
		Filter(authors.AuthorFields.ID.Exact(identifier)).
		All(ctx)
	if err != nil {
		return authors.Author{}, err
	}
	if len(models) != 1 {
		return authors.Author{}, fmt.Errorf("author %d row count = %d, want one", identifier, len(models))
	}
	return models[0], nil
}

func provision(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) error {
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(200) NOT NULL)`,
		`CREATE TABLE "blog_post" ("id" INTEGER NOT NULL PRIMARY KEY, "title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE RESTRICT, "reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE SET NULL)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision REL-005 schema: %w", err)
		}
	}
	for _, author := range config.authors {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`, author.id, author.name); err != nil {
			return fmt.Errorf("provision REL-005 author %d: %w", author.id, err)
		}
	}
	for _, post := range config.posts {
		var reviewer any
		if post.reviewerID != nil {
			reviewer = *post.reviewerID
		}
		if _, err := backend.ExecContext(ctx, `INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`, post.id, post.title, post.authorID, reviewer); err != nil {
			return fmt.Errorf("provision REL-005 post %d: %w", post.id, err)
		}
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).
		OrderBy(authors.AuthorFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-005 authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-005 posts: %w", err)
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
		accessorAuthorID: 1,
		lookupTitle:      "Alpha",
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
