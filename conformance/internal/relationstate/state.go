// Package relationstate shares manually provisioned relation fixtures and actual DB snapshots.
// It never reads the fixed Django reference artifacts.
package relationstate

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/conformance/relationfixture/authors"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/db/sqlite"
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

type QueryMetrics struct {
	QueryCount         int64
	StatementKinds     []string
	JoinKinds          []string
	InnerJoinCount     int64
	LeftOuterJoinCount int64
}

func Read(ctx context.Context, backend *sqlite.Backend, label string) (DatabaseState, error) {
	authorModels, err := authors.AuthorObjects.Using(backend).
		OrderBy(authors.AuthorFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read %s authors: %w", label, err)
	}
	postModels, err := blog.PostObjects.Using(backend).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("read %s posts: %w", label, err)
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
			ReviewerID: CloneIntegerPointer(postModels[index].ReviewerID),
		}
	}
	return DatabaseState{Authors: authorRows, Posts: postRows}, nil
}

func CloneIntegerPointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// Seed returns fresh row slices and reviewer storage for each independent fixture.
func Seed() DatabaseState {
	reviewer := int64(2)
	return DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts:   []PostRow{{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer}, {ID: 11, Title: "Beta", AuthorID: 1}, {ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer}},
	}
}

func Provision(ctx context.Context, backend *sqlite.Backend, label string, authors []AuthorRow, posts []PostRow) error {
	if err := CreateSchema(ctx, backend, label); err != nil {
		return err
	}
	if err := InsertAuthors(ctx, backend, label, authors); err != nil {
		return err
	}
	return InsertPosts(ctx, backend, label, posts)
}

func CreateSchema(ctx context.Context, backend *sqlite.Backend, label string) error {
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
			return fmt.Errorf("provision %s schema: %w", label, err)
		}
	}
	return nil
}

func InsertAuthors(ctx context.Context, backend *sqlite.Backend, label string, authors []AuthorRow) error {
	for _, author := range authors {
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "authors_author" ("id", "name") VALUES (?, ?)`,
			author.ID,
			author.Name,
		); err != nil {
			return fmt.Errorf("provision %s author %d: %w", label, author.ID, err)
		}
	}
	return nil
}

func InsertPosts(ctx context.Context, backend *sqlite.Backend, label string, posts []PostRow) error {
	for _, post := range posts {
		var reviewer any
		if post.ReviewerID != nil {
			reviewer = *post.ReviewerID
		}
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (?, ?, ?, ?)`,
			post.ID,
			post.Title,
			post.AuthorID,
			reviewer,
		); err != nil {
			return fmt.Errorf("provision %s post %d: %w", label, post.ID, err)
		}
	}
	return nil
}
