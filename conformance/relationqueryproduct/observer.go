// Package relationqueryproduct executes the checked-in generated REL-004
// project against a manually provisioned SQLite fixture. It does not import
// relation oracle or not-implemented artifacts.
package relationqueryproduct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/conformance/relationqueryproduct/authors"
	"github.com/progresshans/godj/conformance/relationqueryproduct/blog"
	"github.com/progresshans/godj/conformance/relationqueryproduct/project"
	"github.com/progresshans/godj/db/sqlite"
)

type QueryMetrics struct {
	QueryCount         int64
	StatementKinds     []string
	JoinKinds          []string
	InnerJoinCount     int64
	LeftOuterJoinCount int64
}

type CaseObservation struct {
	Name         string
	PostIDs      []int64
	Construction QueryMetrics
	Evaluation   QueryMetrics
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
	Cases   []CaseObservation
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
	authors                  []authorSeed
	posts                    []postSeed
	predicateName            string
	predicateAuthorID        int64
	firstPredicateTerminalID bool
	descending               bool
}

var databaseSequence atomic.Uint64

func Observe(ctx context.Context) (Observation, error) {
	return observe(ctx, defaultFixtureConfig())
}

func observe(ctx context.Context, config fixtureConfig) (Observation, error) {
	if ctx == nil {
		return Observation{}, fmt.Errorf("observe REL-004: context is nil")
	}
	backend, err := sqlite.OpenMemory(ctx, fmt.Sprintf("godj-rel004-%d", databaseSequence.Add(1)))
	if err != nil {
		return Observation{}, fmt.Errorf("open REL-004 SQLite fixture: %w", err)
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
	relations, err := project.BindRelations()
	if err != nil {
		return Observation{}, fmt.Errorf("bind generated REL-004 relations: %w", err)
	}

	cases := make([]CaseObservation, 0, 2)
	for index, name := range []string{"one_predicate", "two_predicates"} {
		beforeConstruction := backend.QueryCount()
		base := blog.PostObjects.Using(backend)
		querySet := base.Filter(relations.BlogPost.Author.Name.Exact(config.predicateName))
		if config.firstPredicateTerminalID {
			querySet = base.Filter(relations.BlogPost.Author.ID.Exact(config.predicateAuthorID + 1))
		}
		if index == 1 {
			querySet = querySet.Filter(relations.BlogPost.Author.ID.Exact(config.predicateAuthorID))
		}
		if config.descending {
			querySet = querySet.OrderBy(blog.PostFields.ID.Desc())
		} else {
			querySet = querySet.OrderBy(blog.PostFields.ID.Asc())
		}
		constructionCount := backend.QueryCount() - beforeConstruction
		if constructionCount != 0 {
			return Observation{}, fmt.Errorf("REL-004 %s construction issued %d queries", name, constructionCount)
		}

		statement, _, err := sqlite.Compile(querySet.Plan())
		if err != nil {
			return Observation{}, fmt.Errorf("compile REL-004 %s plan: %w", name, err)
		}
		beforeEvaluation := backend.QueryCount()
		posts, err := querySet.All(ctx)
		if err != nil {
			return Observation{}, fmt.Errorf("evaluate REL-004 %s: %w", name, err)
		}
		evaluationCount := backend.QueryCount() - beforeEvaluation
		metrics := classifyStatement(statement, evaluationCount)
		if metrics.QueryCount != 1 || metrics.InnerJoinCount != 1 || metrics.LeftOuterJoinCount != 0 {
			return Observation{}, fmt.Errorf("REL-004 %s query shape = %#v", name, metrics)
		}
		identifiers := make([]int64, len(posts))
		for postIndex := range posts {
			identifiers[postIndex] = posts[postIndex].ID
		}
		cases = append(cases, CaseObservation{
			Name:         name,
			PostIDs:      identifiers,
			Construction: emptyQueryMetrics(constructionCount),
			Evaluation:   metrics,
		})
	}

	state, err := readState(ctx, backend)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Cases: cases, DBState: state}, nil
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
			return fmt.Errorf("provision REL-004 schema: %w", err)
		}
	}
	for _, author := range config.authors {
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`,
			author.id,
			author.name,
		); err != nil {
			return fmt.Errorf("provision REL-004 author %d: %w", author.id, err)
		}
	}
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, NULL)`,
		int64(999), "orphan", int64(999),
	); err == nil {
		return fmt.Errorf("REL-004 fixture accepted orphan required foreign key")
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
			return fmt.Errorf("provision REL-004 post %d: %w", post.id, err)
		}
	}
	return nil
}

func readState(ctx context.Context, backend *sqlite.Backend) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).
		OrderBy(authors.AuthorFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-004 authors: %w", err)
	}
	postModels, err := blog.PostObjects.Using(backend).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read REL-004 posts: %w", err)
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

func classifyStatement(statement string, queryCount uint64) QueryMetrics {
	innerCount := strings.Count(statement, " INNER JOIN ")
	leftCount := strings.Count(statement, " LEFT OUTER JOIN ")
	joinKinds := make([]string, 0, innerCount+leftCount)
	for index := 0; index < innerCount; index++ {
		joinKinds = append(joinKinds, "INNER")
	}
	for index := 0; index < leftCount; index++ {
		joinKinds = append(joinKinds, "LEFT OUTER")
	}
	statementKinds := []string{}
	if strings.HasPrefix(strings.TrimSpace(statement), "SELECT ") {
		statementKinds = append(statementKinds, "SELECT")
	}
	return QueryMetrics{
		QueryCount:         int64(queryCount),
		StatementKinds:     statementKinds,
		JoinKinds:          joinKinds,
		InnerJoinCount:     int64(innerCount),
		LeftOuterJoinCount: int64(leftCount),
	}
}

func emptyQueryMetrics(queryCount uint64) QueryMetrics {
	return QueryMetrics{
		QueryCount:     int64(queryCount),
		StatementKinds: []string{},
		JoinKinds:      []string{},
	}
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
		predicateName:     "Ada",
		predicateAuthorID: 1,
	}
}

func cloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
