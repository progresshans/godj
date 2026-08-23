// Package adminapp connects the bounded Article Admin experience to the
// generated Article model without introducing reflection or a second model
// schema. HTTP, forms, authentication, and rendering are composed separately.
package adminapp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
)

const (
	DefaultPageSize    = 20
	MaximumPageSize    = 100
	MaximumActionIDs   = 100
	MaximumSearchBytes = 256
)

// Backend is the exact capability required by Article Admin. The generated
// model uses Queryer and Mutator; multi-row actions additionally require the
// public transaction boundary. SQLite and PostgreSQL implement all three.
type Backend interface {
	db.Queryer
	db.Mutator
	db.Atomic
}

type ErrorCode string

const (
	CodeInvalidInput ErrorCode = "invalid_input"
	CodeNotFound     ErrorCode = "not_found"
)

// Error is a secret-free application integration error. Database errors,
// including commit-outcome-unknown, are returned unchanged as its Cause so
// callers can preserve their structured ownership and no-retry policy.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "article admin: <nil>"
	}
	message := "article admin: " + string(e.Code)
	if e.Field != "" {
		message += ": " + e.Field
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Article is the explicit immutable Admin representation. Generated model
// key state and ORM caches never cross this boundary.
type Article struct {
	ID        int64
	Title     string
	Published bool
	Summary   *string
}

// Input is already-cleaned Article form data. Repository validation remains
// defensive so bypassing an HTTP Form cannot write invalid scalar values.
type Input struct {
	Title     string
	Published bool
	Summary   *string
}

type ListOptions struct {
	Search string
	Offset int
	Limit  int
}

type Page struct {
	Articles []Article
	Total    int64
	Offset   int
	Limit    int
}

type PublishResult struct {
	MatchedIDs []int64
}

func (r PublishResult) Matched() int { return len(r.MatchedIDs) }

// Repository is immutable after construction and safe for concurrent use
// when its backend is safe for concurrent use.
type Repository struct {
	backend Backend
}

func NewRepository(backend Backend) (Repository, error) {
	if interfaceNil(backend) {
		return Repository{}, invalid("backend", "backend is nil")
	}
	return Repository{backend: backend}, nil
}

func (r Repository) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := validateContext(ctx); err != nil {
		return Page{}, err
	}
	if interfaceNil(r.backend) {
		return Page{}, invalid("backend", "repository is zero or invalid")
	}
	if err := validateListOptions(options); err != nil {
		return Page{}, err
	}
	if options.Limit == 0 {
		options.Limit = DefaultPageSize
	}

	querySet := articlemodels.ArticleObjects.Using(r.backend)
	if options.Search != "" {
		querySet = querySet.Filter(orm.Or(
			articlemodels.ArticleFields.Title.IContains(options.Search),
			articlemodels.ArticleFields.Summary.IContains(options.Search),
		))
	}
	total, err := querySet.Count(ctx)
	if err != nil {
		return Page{}, fmt.Errorf("article admin list count: %w", err)
	}
	pageQuery, err := querySet.
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		Offset(options.Offset)
	if err != nil {
		return Page{}, fmt.Errorf("article admin list offset: %w", err)
	}
	pageQuery, err = pageQuery.Limit(options.Limit)
	if err != nil {
		return Page{}, fmt.Errorf("article admin list limit: %w", err)
	}
	models, err := pageQuery.All(ctx)
	if err != nil {
		return Page{}, fmt.Errorf("article admin list rows: %w", err)
	}
	articles := make([]Article, len(models))
	for index := range models {
		articles[index] = snapshot(models[index])
	}
	return Page{
		Articles: articles,
		Total:    total,
		Offset:   options.Offset,
		Limit:    options.Limit,
	}, nil
}

func (r Repository) Get(ctx context.Context, id int64) (Article, bool, error) {
	if err := validateContext(ctx); err != nil {
		return Article{}, false, err
	}
	if interfaceNil(r.backend) {
		return Article{}, false, invalid("backend", "repository is zero or invalid")
	}
	model, found, err := getModel(ctx, r.backend, id)
	if err != nil || !found {
		return Article{}, found, err
	}
	return snapshot(model), true, nil
}

func (r Repository) Create(ctx context.Context, input Input) (Article, error) {
	if err := r.validateWrite(ctx, input); err != nil {
		return Article{}, err
	}
	var created articlemodels.Article
	err := r.backend.Atomic(ctx, func(session db.Session) error {
		create := articlemodels.NewArticleCreate(input.Title).WithPublished(input.Published)
		if input.Summary == nil {
			create = create.WithSummaryNull()
		} else {
			create = create.WithSummary(*input.Summary)
		}
		value, err := articlemodels.ArticleObjects.Create(ctx, session, create)
		if err != nil {
			return err
		}
		created = value
		return nil
	})
	if err != nil {
		return Article{}, fmt.Errorf("article admin create: %w", err)
	}
	return snapshot(created), nil
}

func (r Repository) Update(ctx context.Context, id int64, input Input) (Article, []string, error) {
	if err := r.validateWrite(ctx, input); err != nil {
		return Article{}, nil, err
	}
	if id <= 0 {
		return Article{}, nil, invalid("id", "id must be positive")
	}
	var updated articlemodels.Article
	var changed []string
	err := r.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := getModel(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return notFound(id)
		}
		changed = changedFields(current, input)
		if len(changed) == 0 {
			updated = current
			return nil
		}
		patch := (articlemodels.ArticlePatch{}).
			WithTitle(input.Title).
			WithPublished(input.Published)
		if input.Summary == nil {
			patch = patch.WithSummaryNull()
		} else {
			patch = patch.WithSummary(*input.Summary)
		}
		value, err := articlemodels.ArticleObjects.Update(ctx, session, current, patch)
		if err != nil {
			return err
		}
		updated = value
		return nil
	})
	if err != nil {
		return Article{}, nil, fmt.Errorf("article admin update: %w", err)
	}
	return snapshot(updated), append([]string(nil), changed...), nil
}

func (r Repository) Delete(ctx context.Context, id int64) (Article, error) {
	if err := validateContext(ctx); err != nil {
		return Article{}, err
	}
	if interfaceNil(r.backend) {
		return Article{}, invalid("backend", "repository is zero or invalid")
	}
	if id <= 0 {
		return Article{}, invalid("id", "id must be positive")
	}
	var deleted articlemodels.Article
	err := r.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := getModel(ctx, session, id)
		if err != nil {
			return err
		}
		if !found {
			return notFound(id)
		}
		deleted = current
		if _, err := articlemodels.ArticleObjects.Delete(ctx, session, &current); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Article{}, fmt.Errorf("article admin delete: %w", err)
	}
	return snapshot(deleted), nil
}

func (r Repository) Publish(ctx context.Context, ids []int64) (PublishResult, error) {
	if err := validateContext(ctx); err != nil {
		return PublishResult{}, err
	}
	if interfaceNil(r.backend) {
		return PublishResult{}, invalid("backend", "repository is zero or invalid")
	}
	canonical, err := canonicalIDs(ids)
	if err != nil {
		return PublishResult{}, err
	}
	matched := make([]int64, 0, len(canonical))
	err = r.backend.Atomic(ctx, func(session db.Session) error {
		for _, id := range canonical {
			current, found, err := getModel(ctx, session, id)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			patch := (articlemodels.ArticlePatch{}).WithPublished(true)
			if _, err := articlemodels.ArticleObjects.Update(ctx, session, current, patch); err != nil {
				return err
			}
			matched = append(matched, id)
		}
		return nil
	})
	if err != nil {
		return PublishResult{}, fmt.Errorf("article admin publish: %w", err)
	}
	return PublishResult{MatchedIDs: append([]int64(nil), matched...)}, nil
}

func (r Repository) validateWrite(ctx context.Context, input Input) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if interfaceNil(r.backend) {
		return invalid("backend", "repository is zero or invalid")
	}
	if err := validateText("title", input.Title, false, 200); err != nil {
		return err
	}
	if input.Summary != nil {
		if err := validateText("summary", *input.Summary, true, 200); err != nil {
			return err
		}
	}
	return nil
}

func getModel(ctx context.Context, backend db.Queryer, id int64) (articlemodels.Article, bool, error) {
	if id <= 0 {
		return articlemodels.Article{}, false, invalid("id", "id must be positive")
	}
	querySet := articlemodels.ArticleObjects.Using(backend).
		Filter(articlemodels.ArticleFields.ID.Exact(id)).
		OrderBy(articlemodels.ArticleFields.ID.Asc())
	value, found, err := querySet.First(ctx)
	if err != nil {
		return articlemodels.Article{}, false, fmt.Errorf("article admin get: %w", err)
	}
	return value, found, nil
}

func validateListOptions(options ListOptions) error {
	if options.Offset < 0 {
		return invalid("offset", "offset must not be negative")
	}
	if options.Limit < 0 || options.Limit > MaximumPageSize {
		return invalid("limit", "limit is outside the supported range")
	}
	if !utf8.ValidString(options.Search) || strings.ContainsRune(options.Search, '\x00') || len(options.Search) > MaximumSearchBytes {
		return invalid("search", "search is invalid or exceeds the supported limit")
	}
	return nil
}

func validateText(field, value string, allowEmpty bool, maximumRunes int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return invalid(field, "value must be valid UTF-8 without NUL")
	}
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return invalid(field, "value is required")
	}
	if utf8.RuneCountInString(value) > maximumRunes {
		return invalid(field, "value exceeds max length")
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return invalid("context", "context is nil")
	}
	return ctx.Err()
}

func canonicalIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, invalid("ids", "at least one id is required")
	}
	if len(ids) > MaximumActionIDs {
		return nil, invalid("ids", "selected id count exceeds the supported limit")
	}
	canonical := append([]int64(nil), ids...)
	for _, id := range canonical {
		if id <= 0 {
			return nil, invalid("ids", "selected ids must be positive")
		}
	}
	sort.Slice(canonical, func(left, right int) bool { return canonical[left] < canonical[right] })
	write := 0
	for _, id := range canonical {
		if write > 0 && canonical[write-1] == id {
			continue
		}
		canonical[write] = id
		write++
	}
	return canonical[:write], nil
}

func changedFields(current articlemodels.Article, input Input) []string {
	changed := make([]string, 0, 3)
	if current.Title != input.Title {
		changed = append(changed, "title")
	}
	if current.Published != input.Published {
		changed = append(changed, "published")
	}
	if !equalStringPointer(current.Summary, input.Summary) {
		changed = append(changed, "summary")
	}
	return changed
}

func equalStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func snapshot(value articlemodels.Article) Article {
	result := Article{
		ID:        value.ID,
		Title:     value.Title,
		Published: value.Published,
	}
	if value.Summary != nil {
		summary := *value.Summary
		result.Summary = &summary
	}
	return result
}

func invalid(field, detail string) error {
	return &Error{Code: CodeInvalidInput, Field: field, Detail: detail}
}

func notFound(id int64) error {
	return &Error{Code: CodeNotFound, Field: "id", Detail: fmt.Sprintf("article %d was not found", id)}
}

func interfaceNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func IsCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}
