// Package articleapp owns the Article application's typed persistence boundary.
// It is independent of Admin and can be reused by other presentation layers.
package articleapp

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

// Backend is the exact capability required by Article persistence. The
// generated model uses Queryer and Mutator; grouped writes additionally use
// the public transaction boundary. SQLite and PostgreSQL implement all three.
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

// ErrNotFound is the presentation-neutral missing Article marker.
var ErrNotFound = errors.New("article: not found")

// Error is a secret-free Article integration error. Database errors,
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
	// Preserve the existing Admin-facing error bytes while persistence moves to
	// this neutral package. Presentation layers must not serialize this prose.
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

func (e *Error) Is(target error) bool {
	return e != nil && e.Code == CodeNotFound && target == ErrNotFound
}

// Article is an explicit persistence snapshot. Generated model key state and
// ORM caches never cross this boundary.
type Article struct {
	ID        int64
	Title     string
	Published bool
	Summary   *string
}

// Input is already-cleaned Article data. Repository validation remains
// defensive so bypassing a presentation-layer validator cannot write invalid
// scalar values.
type Input struct {
	Title     string
	Published bool
	Summary   *string
}

// PublishedFilter is the closed Article publication-state filter. Its zero
// value preserves the existing behavior and includes both published and
// unpublished rows.
type PublishedFilter uint8

const (
	PublishedAny PublishedFilter = iota
	PublishedOnly
	UnpublishedOnly
)

// IDOrdering is the closed deterministic Article list ordering. Its zero
// value preserves the existing ascending primary-key order.
type IDOrdering uint8

const (
	IDAscending IDOrdering = iota
	IDDescending
)

// SearchScope is the closed set of Article fields searched by List. Its zero
// value preserves the existing title-or-summary search behavior.
type SearchScope uint8

const (
	SearchTitleAndSummary SearchScope = iota
	SearchTitleOnly
)

type ListOptions struct {
	Search      string
	Offset      int
	Limit       int
	Published   PublishedFilter
	Ordering    IDOrdering
	SearchScope SearchScope
}

// Patch is an immutable, closed Article partial-update value. The zero value
// is an empty patch. Value-receiver builders return a copy, so omitted fields,
// explicit summary NULL, and an explicit empty summary remain distinct.
type Patch struct {
	title     patchString
	published patchBool
	summary   patchNullableString
}

type patchString struct {
	supplied bool
	value    string
}

type patchBool struct {
	supplied bool
	value    bool
}

type patchNullableString struct {
	supplied bool
	null     bool
	value    string
}

func (patch Patch) WithTitle(value string) Patch {
	patch.title = patchString{supplied: true, value: value}
	return patch
}

func (patch Patch) WithPublished(value bool) Patch {
	patch.published = patchBool{supplied: true, value: value}
	return patch
}

func (patch Patch) WithSummary(value string) Patch {
	patch.summary = patchNullableString{supplied: true, value: value}
	return patch
}

func (patch Patch) WithSummaryNull() Patch {
	patch.summary = patchNullableString{supplied: true, null: true}
	return patch
}

func (patch Patch) Empty() bool {
	return !patch.title.supplied && !patch.published.supplied && !patch.summary.supplied
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
		switch options.SearchScope {
		case SearchTitleAndSummary:
			querySet = querySet.Filter(orm.Or(
				articlemodels.ArticleFields.Title.IContains(options.Search),
				articlemodels.ArticleFields.Summary.IContains(options.Search),
			))
		case SearchTitleOnly:
			querySet = querySet.Filter(articlemodels.ArticleFields.Title.IContains(options.Search))
		}
	}
	switch options.Published {
	case PublishedOnly:
		querySet = querySet.Filter(articlemodels.ArticleFields.Published.Exact(true))
	case UnpublishedOnly:
		querySet = querySet.Filter(articlemodels.ArticleFields.Published.Exact(false))
	}
	total, err := querySet.Count(ctx)
	if err != nil {
		return Page{}, fmt.Errorf("article admin list count: %w", err)
	}
	ordering := articlemodels.ArticleFields.ID.Asc()
	if options.Ordering == IDDescending {
		ordering = articlemodels.ArticleFields.ID.Desc()
	}
	pageQuery, err := querySet.OrderBy(ordering).Offset(options.Offset)
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

// Patch updates only supplied fields. It reads and conditionally updates the
// row inside one backend transaction. Empty and value-equivalent patches are
// no-op writes but still require the target row to exist.
func (r Repository) Patch(ctx context.Context, id int64, patch Patch) (Article, []string, error) {
	if err := validateContext(ctx); err != nil {
		return Article{}, nil, err
	}
	if interfaceNil(r.backend) {
		return Article{}, nil, invalid("backend", "repository is zero or invalid")
	}
	if id <= 0 {
		return Article{}, nil, invalid("id", "id must be positive")
	}
	if err := validatePatch(patch); err != nil {
		return Article{}, nil, err
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
		changed = patchChangedFields(current, patch)
		if len(changed) == 0 {
			updated = current
			return nil
		}

		modelPatch := articlemodels.ArticlePatch{}
		if patch.title.supplied && current.Title != patch.title.value {
			modelPatch = modelPatch.WithTitle(patch.title.value)
		}
		if patch.published.supplied && current.Published != patch.published.value {
			modelPatch = modelPatch.WithPublished(patch.published.value)
		}
		if patch.summary.supplied && !patchSummaryEquals(current.Summary, patch.summary) {
			if patch.summary.null {
				modelPatch = modelPatch.WithSummaryNull()
			} else {
				modelPatch = modelPatch.WithSummary(patch.summary.value)
			}
		}
		value, err := articlemodels.ArticleObjects.Update(ctx, session, current, modelPatch)
		if err != nil {
			return err
		}
		updated = value
		return nil
	})
	if err != nil {
		return Article{}, nil, fmt.Errorf("article admin patch: %w", err)
	}
	return snapshot(updated), append([]string(nil), changed...), nil
}

func (r Repository) Delete(ctx context.Context, id int64) (Article, error) {
	return r.DeletePrepared(ctx, id, nil)
}

// DeletePrepared runs prepare after loading the current row and before the
// delete in the same transaction. A preparation failure rolls the delete back.
// It exists for the Admin audit boundary; ordinary callers should use Delete.
func (r Repository) DeletePrepared(ctx context.Context, id int64, prepare func(Article) error) (Article, error) {
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
		if prepare != nil {
			if err := prepare(snapshot(current)); err != nil {
				return err
			}
		}
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

// ValidateInput applies the scalar validation used immediately before Article
// writes. It performs no I/O.
func ValidateInput(input Input) error {
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

func (r Repository) validateWrite(ctx context.Context, input Input) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if interfaceNil(r.backend) {
		return invalid("backend", "repository is zero or invalid")
	}
	return ValidateInput(input)
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
	if options.Published > UnpublishedOnly {
		return invalid("published", "published filter is unsupported")
	}
	if options.Ordering > IDDescending {
		return invalid("ordering", "ordering is unsupported")
	}
	if options.SearchScope > SearchTitleOnly {
		return invalid("search_scope", "search scope is unsupported")
	}
	return nil
}

func validatePatch(patch Patch) error {
	if patch.title.supplied {
		if err := validateText("title", patch.title.value, false, 200); err != nil {
			return err
		}
	}
	if patch.summary.supplied && !patch.summary.null {
		if err := validateText("summary", patch.summary.value, true, 200); err != nil {
			return err
		}
	}
	return nil
}

func validateText(field, value string, allowEmpty bool, maximumRunes int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return invalid(field, "value must be valid UTF-8 without NUL")
	}
	for _, character := range value {
		if character == '\t' || character == '\n' || character == '\r' {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return invalid(field, "value contains an unsupported control character")
		}
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

func patchChangedFields(current articlemodels.Article, patch Patch) []string {
	changed := make([]string, 0, 3)
	if patch.title.supplied && current.Title != patch.title.value {
		changed = append(changed, "title")
	}
	if patch.published.supplied && current.Published != patch.published.value {
		changed = append(changed, "published")
	}
	if patch.summary.supplied && !patchSummaryEquals(current.Summary, patch.summary) {
		changed = append(changed, "summary")
	}
	return changed
}

func patchSummaryEquals(current *string, patch patchNullableString) bool {
	if patch.null {
		return current == nil
	}
	return current != nil && *current == patch.value
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
