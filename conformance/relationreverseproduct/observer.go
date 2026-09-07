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

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/authors"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type Observation struct {
	AccessorPostIDs []int64
	LookupAuthorIDs []int64
	Accessor        relationstate.QueryMetrics
	Lookup          relationstate.QueryMetrics
	DBState         relationstate.DatabaseState
}

type fixtureConfig struct {
	authors            []relationstate.AuthorRow
	posts              []relationstate.PostRow
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
	if err := relationstate.Provision(ctx, backend, "REL-005", config.authors, config.posts); err != nil {
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

	state, err := relationstate.Read(ctx, backend, "REL-005")
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

func (r *recordingQueryer) metricsSince(start int) relationstate.QueryMetrics {
	r.mu.Lock()
	statements := append([]string(nil), r.statements[start:]...)
	r.mu.Unlock()
	return classifyStatements(statements)
}

func classifyStatements(statements []string) relationstate.QueryMetrics {
	metrics := relationstate.QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
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

func defaultFixtureConfig() fixtureConfig {
	seed := relationstate.Seed()
	return fixtureConfig{
		authors:          seed.Authors,
		posts:            seed.Posts,
		accessorAuthorID: 1,
		lookupTitle:      "Alpha",
	}
}
