// Package relationprefetchproduct executes the checked-in generated REL-012
// project against a manually provisioned SQLite fixture. It does not import
// relation oracle or not-implemented artifacts.
package relationprefetchproduct

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationprefetchproduct/authors"
	"github.com/progresshans/godj/conformance/relationprefetchproduct/blog"
	"github.com/progresshans/godj/conformance/relationprefetchproduct/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

type QueryMetrics struct {
	QueryCount                int64
	StatementKinds            []string
	JoinKinds                 []string
	InnerJoinCount            int64
	LeftOuterJoinCount        int64
	PrimaryQueryCount         int64
	BatchQueryCount           int64
	BatchPredicateColumn      string
	BatchKeyCount             int64
	RelatedAccessExtraQueries int64
}

type AuthorPosts struct {
	AuthorID int64
	PostIDs  []int64
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
	Authors []AuthorPosts
	Metrics QueryMetrics
	DBState DatabaseState
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
	authors          []authorSeed
	posts            []postSeed
	ownersDescending bool
	repeatPrimary    bool
	forceColdAccess  bool
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
		return Observation{}, fmt.Errorf("observe REL-012: context is nil")
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-rel012-%d", databaseSequence.Add(1)))
	if err != nil {
		return Observation{}, fmt.Errorf("open REL-012 SQLite fixture: %w", err)
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
	prefetches, err := project.BindReversePrefetches()
	if err != nil {
		return Observation{}, fmt.Errorf("bind generated REL-012 reverse prefetches: %w", err)
	}
	recorder := &recordingQueryer{backend: backend}

	prefetchStart := recorder.mark()
	ownersQuery := authors.AuthorObjects.Using(recorder)
	if config.ownersDescending {
		ownersQuery = ownersQuery.OrderBy(authors.AuthorFields.ID.Desc())
	} else {
		ownersQuery = ownersQuery.OrderBy(authors.AuthorFields.ID.Asc())
	}
	owners, err := ownersQuery.All(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("load REL-012 owners: %w", err)
	}
	if config.repeatPrimary {
		if _, err := ownersQuery.Fresh().All(ctx); err != nil {
			return Observation{}, fmt.Errorf("repeat REL-012 primary query: %w", err)
		}
	}
	objects, err := prefetches.AuthorsAuthor.Posts(ctx, recorder, owners)
	if err != nil {
		return Observation{}, fmt.Errorf("prefetch REL-012 posts: %w", err)
	}
	prefetchRecords := recorder.recordsSince(prefetchStart)

	accessStart := recorder.mark()
	result := make([]AuthorPosts, len(objects))
	for index, object := range objects {
		owner, err := object.Model()
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-012 owner %d: %w", index, err)
		}
		set, err := object.Posts()
		if err != nil {
			return Observation{}, fmt.Errorf("read REL-012 posts set %d: %w", index, err)
		}
		if config.forceColdAccess && index == 0 {
			set, err = set.Fresh()
			if err != nil {
				return Observation{}, fmt.Errorf("freshen REL-012 posts set: %w", err)
			}
		}
		posts, err := set.All(ctx)
		if err != nil {
			return Observation{}, fmt.Errorf("consume REL-012 posts set %d: %w", index, err)
		}
		postIDs := make([]int64, len(posts))
		for postIndex := range posts {
			postIDs[postIndex] = posts[postIndex].ID
		}
		result[index] = AuthorPosts{AuthorID: owner.ID, PostIDs: postIDs}
	}
	accessRecords := recorder.recordsSince(accessStart)

	metrics, err := prefetchMetrics(prefetchRecords, accessRecords)
	if err != nil {
		return Observation{}, err
	}
	if err := validateSuccessfulTrace(prefetchRecords, accessRecords, result); err != nil {
		return Observation{}, err
	}
	state, err := readState(ctx, backend)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Authors: result, Metrics: metrics, DBState: state}, nil
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
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]recordedQuery, len(r.records)-start)
	for index, record := range r.records[start:] {
		result[index] = recordedQuery{
			statement: record.statement,
			arguments: append([]any(nil), record.arguments...),
			plan:      record.plan,
		}
	}
	return result
}

func prefetchMetrics(prefetchRecords, accessRecords []recordedQuery) (QueryMetrics, error) {
	all := append(append([]recordedQuery(nil), prefetchRecords...), accessRecords...)
	metrics := classifyQueries(all)
	for _, record := range prefetchRecords {
		for _, condition := range record.plan.Conditions() {
			if condition.Lookup() != query.LookupIn {
				continue
			}
			values, ok := condition.Values()
			if !ok || metrics.BatchQueryCount != 0 {
				return QueryMetrics{}, fmt.Errorf("REL-012 trace has an invalid or duplicate batch condition")
			}
			metrics.BatchQueryCount = 1
			metrics.BatchPredicateColumn = condition.Field().Column()
			metrics.BatchKeyCount = int64(len(values))
		}
	}
	metrics.PrimaryQueryCount = int64(len(prefetchRecords)) - metrics.BatchQueryCount
	metrics.RelatedAccessExtraQueries = int64(len(accessRecords))
	return metrics, nil
}

func classifyQueries(records []recordedQuery) QueryMetrics {
	metrics := QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
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
			metrics.JoinKinds = append(metrics.JoinKinds, "LEFT OUTER")
		}
	}
	return metrics
}

func validateSuccessfulTrace(prefetchRecords, accessRecords []recordedQuery, result []AuthorPosts) error {
	if len(prefetchRecords) != 2 {
		return fmt.Errorf("REL-012 prefetch trace query count = %d, want 2", len(prefetchRecords))
	}
	if len(accessRecords) != 0 {
		return fmt.Errorf("REL-012 related access executed %d extra queries", len(accessRecords))
	}
	for _, record := range prefetchRecords {
		if !strings.HasPrefix(strings.TrimSpace(record.statement), "SELECT ") ||
			strings.Contains(record.statement, " JOIN ") {
			return fmt.Errorf("REL-012 trace is not mutation-free join-free SELECT: %s", record.statement)
		}
	}
	batch := prefetchRecords[1]
	conditions := batch.plan.Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupIn ||
		conditions[0].Field().Column() != "author_id" {
		return fmt.Errorf("REL-012 batch plan does not contain the canonical author_id IN condition")
	}
	values, ok := conditions[0].Values()
	if !ok || len(values) != 3 {
		return fmt.Errorf("REL-012 batch plan has an invalid key list")
	}
	wantArguments := []any{int64(1), int64(2), int64(3)}
	if !reflect.DeepEqual(batch.arguments, wantArguments) {
		return fmt.Errorf("REL-012 batch arguments = %#v, want %#v", batch.arguments, wantArguments)
	}
	orderings := batch.plan.Orderings()
	if len(orderings) != 1 || orderings[0].Field().Name() != "id" ||
		orderings[0].Field().Column() != "id" || orderings[0].Direction() != query.Ascending {
		return fmt.Errorf("REL-012 batch plan is not ordered by source primary key ascending")
	}
	for _, owner := range result {
		if !slices.IsSorted(owner.PostIDs) {
			return fmt.Errorf("REL-012 source group for owner %d is not primary-key ordered", owner.AuthorID)
		}
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
			return fmt.Errorf("provision REL-012 schema: %w", err)
		}
	}
	for _, author := range config.authors {
		if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`, author.id, author.name); err != nil {
			return fmt.Errorf("provision REL-012 author %d: %w", author.id, err)
		}
	}
	for _, post := range config.posts {
		var reviewer any
		if post.reviewerID != nil {
			reviewer = *post.reviewerID
		}
		if _, err := backend.ExecContext(ctx, `INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`, post.id, post.title, post.authorID, reviewer); err != nil {
			return fmt.Errorf("provision REL-012 post %d: %w", post.id, err)
		}
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).
		OrderBy(authors.AuthorFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-012 authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-012 posts: %w", err)
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
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
