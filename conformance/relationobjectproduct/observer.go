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

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type AccessStep struct {
	Name    string
	Metrics relationstate.QueryMetrics
}

type ForwardCacheObservation struct {
	Cold  relationstate.AuthorRow
	Warm  relationstate.AuthorRow
	Steps []AccessStep
}

type NullableObservation struct {
	Reviewer           *relationstate.AuthorRow
	IsNullPostIDs      []int64
	NullAccess         relationstate.QueryMetrics
	IsNullConstruction relationstate.QueryMetrics
	IsNullEvaluation   relationstate.QueryMetrics
}

type Observation struct {
	Forward  ForwardCacheObservation
	Nullable NullableObservation
	DBState  relationstate.DatabaseState
}

type fixtureConfig struct {
	authors      []relationstate.AuthorRow
	posts        []relationstate.PostRow
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
	if err := relationstate.Provision(ctx, backend, "REL-003/006", config.authors, config.posts); err != nil {
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
	coldRow := relationstate.AuthorRow{ID: cold.ID, Name: cold.Name}
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
		Warm: relationstate.AuthorRow{ID: warm.ID, Name: warm.Name},
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
	var reviewerRow *relationstate.AuthorRow
	if reviewerOK {
		value := relationstate.AuthorRow{ID: reviewer.ID, Name: reviewer.Name}
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

	state, err := relationstate.Read(ctx, backend, "REL-003/006")
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

func (r *recordingQueryer) metricsSince(start int) relationstate.QueryMetrics {
	r.mu.Lock()
	statements := append([]string(nil), r.statements[start:]...)
	r.mu.Unlock()
	return classifyStatements(statements)
}

func classifyStatements(statements []string) relationstate.QueryMetrics {
	metrics := relationstate.QueryMetrics{QueryCount: int64(len(statements))}
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

func defaultFixtureConfig() fixtureConfig {
	seed := relationstate.Seed()
	return fixtureConfig{
		authors:      seed.Authors,
		posts:        seed.Posts,
		isNullValue:  true,
		loadPostID:   10,
		nullablePost: 11,
	}
}
