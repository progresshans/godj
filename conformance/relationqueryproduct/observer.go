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

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db/sqlite"
)

type CaseObservation struct {
	Name         string
	PostIDs      []int64
	Construction relationstate.QueryMetrics
	Evaluation   relationstate.QueryMetrics
}

type Observation struct {
	Cases   []CaseObservation
	DBState relationstate.DatabaseState
}

type fixtureConfig struct {
	authors                  []relationstate.AuthorRow
	posts                    []relationstate.PostRow
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

	state, err := relationstate.Read(ctx, backend, "REL-004")
	if err != nil {
		return Observation{}, err
	}
	return Observation{Cases: cases, DBState: state}, nil
}

func provision(ctx context.Context, backend *sqlite.Backend, config fixtureConfig) error {
	if err := relationstate.CreateSchema(ctx, backend, "REL-004"); err != nil {
		return err
	}
	if err := relationstate.InsertAuthors(ctx, backend, "REL-004", config.authors); err != nil {
		return err
	}
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, NULL)`,
		int64(999), "orphan", int64(999),
	); err == nil {
		return fmt.Errorf("REL-004 fixture accepted orphan required foreign key")
	}
	return relationstate.InsertPosts(ctx, backend, "REL-004", config.posts)
}

func classifyStatement(statement string, queryCount uint64) relationstate.QueryMetrics {
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
	return relationstate.QueryMetrics{
		QueryCount:         int64(queryCount),
		StatementKinds:     statementKinds,
		JoinKinds:          joinKinds,
		InnerJoinCount:     int64(innerCount),
		LeftOuterJoinCount: int64(leftCount),
	}
}

func emptyQueryMetrics(queryCount uint64) relationstate.QueryMetrics {
	return relationstate.QueryMetrics{
		QueryCount:     int64(queryCount),
		StatementKinds: []string{},
		JoinKinds:      []string{},
	}
}

func defaultFixtureConfig() fixtureConfig {
	seed := relationstate.Seed()
	return fixtureConfig{
		authors:           seed.Authors,
		posts:             seed.Posts,
		predicateName:     "Ada",
		predicateAuthorID: 1,
	}
}
