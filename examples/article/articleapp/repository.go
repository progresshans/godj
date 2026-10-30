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

	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
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
		return "article: <nil>"
	}
	message := "article: " + string(e.Code)
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

type Page struct {
	Articles []articlemodels.Article
	Total    int64
	Offset   int
	Limit    int
}

type PublishResult struct {
	MatchedIDs []int64
}

func (r PublishResult) Matched() int { return len(r.MatchedIDs) }

// MutationOperation is the closed set of Article write kernels. Presentation
// layers may use it to translate one confirmed semantic mutation into
// transaction-local side effects without duplicating Article CRUD.
type MutationOperation string

const (
	MutationCreate     MutationOperation = "create"
	MutationBulkCreate MutationOperation = "bulk_create"
	MutationUpdate     MutationOperation = "update"
	MutationPatch      MutationOperation = "patch"
	MutationDelete     MutationOperation = "delete"
	MutationPublish    MutationOperation = "publish"
)

// MutationItem is a detached semantic Article mutation. ChangedFields uses
// the app-declared field order and is empty only where the operation itself
// carries the semantic action, such as create or delete.
type MutationItem struct {
	Article       articlemodels.Article
	ChangedFields []string
}

func (item MutationItem) Clone() MutationItem {
	item.Article = cloneArticle(item.Article)
	item.ChangedFields = append([]string(nil), item.ChangedFields...)
	return item
}

// MutationResult owns detached generated models and no transaction
// session. A hook may retain this value, but must not retain the borrowed
// db.Session passed alongside it.
type MutationResult struct {
	Operation MutationOperation
	Items     []MutationItem
}

func (result MutationResult) Clone() MutationResult {
	cloned := MutationResult{
		Operation: result.Operation,
		Items:     make([]MutationItem, len(result.Items)),
	}
	for index := range result.Items {
		cloned.Items[index] = result.Items[index].Clone()
	}
	return cloned
}

// MutationHook runs after all Article DML for one repository call and before
// its commit. session is borrowed only for this callback. Returning an error
// rolls back the Article DML and every side effect written through session.
type MutationHook func(context.Context, db.Session, MutationResult) error

// Repository is immutable after construction and safe for concurrent use
// when its backend is safe for concurrent use.
type Repository struct {
	backend      Backend
	resource     crud.Resource[articlemodels.Article]
	mutationHook MutationHook
}

func NewRepository(backend Backend) (Repository, error) {
	if interfaceNil(backend) {
		return Repository{}, invalid("backend", "backend is nil")
	}
	resource, err := crud.New(articlemodels.ArticleObjects, crud.Config{Fields: WritableFields()})
	if err != nil {
		return Repository{}, err
	}
	return Repository{backend: backend, resource: resource}, nil
}

// WritableFields is the owned Article input selection shared by its service
// and Admin adapter. Scalar type, NULL and defaults remain in generated IR.
func WritableFields() []string { return []string{"title", "published", "summary", "slug"} }

// ModelResource shares the repository's immutable field/source policy with
// presentation adapters. Current authorization and Article business validation
// still belong to the application write methods, not this configuration value.
func (r Repository) ModelResource() crud.Resource[articlemodels.Article] { return r.resource }

// WithMutationHook returns an immutable repository view whose writes invoke
// hook inside their existing transaction. A nil hook preserves the ordinary
// repository API and creates no nested transaction boundary.
func (r Repository) WithMutationHook(hook MutationHook) Repository {
	r.mutationHook = hook
	return r
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

	var filters []orm.Predicate[articlemodels.Article]
	if options.Search != "" {
		switch options.SearchScope {
		case SearchTitleAndSummary:
			filters = append(filters, orm.Or(
				articlemodels.ArticleFields.Title.IContains(options.Search),
				articlemodels.ArticleFields.Summary.IContains(options.Search),
			))
		case SearchTitleOnly:
			filters = append(filters, articlemodels.ArticleFields.Title.IContains(options.Search))
		}
	}
	switch options.Published {
	case PublishedOnly:
		filters = append(filters, articlemodels.ArticleFields.Published.Exact(true))
	case UnpublishedOnly:
		filters = append(filters, articlemodels.ArticleFields.Published.Exact(false))
	}
	ordering := articlemodels.ArticleFields.ID.Asc()
	if options.Ordering == IDDescending {
		ordering = articlemodels.ArticleFields.ID.Desc()
	}
	page, err := r.resource.List(ctx, r.backend, crud.ListOptions[articlemodels.Article]{Filter: filters, Ordering: []orm.Ordering[articlemodels.Article]{ordering}, Offset: options.Offset, Limit: options.Limit})
	if err != nil {
		return Page{}, fmt.Errorf("article list: %w", err)
	}
	return Page{Articles: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, nil
}

func (r Repository) Get(ctx context.Context, id int64) (articlemodels.Article, bool, error) {
	if err := validateContext(ctx); err != nil {
		return articlemodels.Article{}, false, err
	}
	if interfaceNil(r.backend) {
		return articlemodels.Article{}, false, invalid("backend", "repository is zero or invalid")
	}
	model, found, err := r.getIn(ctx, r.backend, id)
	if err != nil || !found {
		return articlemodels.Article{}, found, err
	}
	return cloneArticle(model), true, nil
}

func (r Repository) Create(ctx context.Context, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, error) {
	return CreateAndPrepare(ctx, r, input, func(_ context.Context, article articlemodels.Article) (articlemodels.Article, error) {
		return article, nil
	})
}

// Update replaces the submitted scalar values and returns the confirmed row.
func (r Repository) Update(ctx context.Context, id int64, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, []string, error) {
	result, err := UpdateAndPrepare(ctx, r, id, input, ownUpdatedArticle)
	return result.article, result.changed, err
}

// Patch updates only supplied fields in one transaction. Empty and equivalent
// inputs still require the row to exist and return its confirmed current value.
func (r Repository) Patch(ctx context.Context, id int64, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, []string, error) {
	result, err := PatchAndPrepare(ctx, r, id, input, ownUpdatedArticle)
	return result.article, result.changed, err
}

type updatedArticle struct {
	article articlemodels.Article
	changed []string
}

func ownUpdatedArticle(_ context.Context, article articlemodels.Article, changed []string) (updatedArticle, error) {
	return updatedArticle{article: article, changed: changed}, nil
}

func (r Repository) Delete(ctx context.Context, id int64) (articlemodels.Article, error) {
	if err := validateContext(ctx); err != nil {
		return articlemodels.Article{}, err
	}
	if interfaceNil(r.backend) {
		return articlemodels.Article{}, invalid("backend", "repository is zero or invalid")
	}
	if id <= 0 {
		return articlemodels.Article{}, invalid("id", "id must be positive")
	}
	return preparedAtomic(ctx, r, MutationDelete, func(work context.Context, session db.Session) (articlemodels.Article, error) {
		deleted, err := r.resource.DeleteInSession(work, session, id, crud.DeleteOptions[articlemodels.Article]{Delete: func(ctx context.Context, session db.Session, value articlemodels.Article) (int64, error) {
			return articlemodels.ArticleObjects.Delete(ctx, session, &value)
		}})
		if err == crud.ErrNotFound {
			return articlemodels.Article{}, notFound(id)
		}
		if err != nil {
			return articlemodels.Article{}, err
		}
		if r.mutationHook != nil {
			if err := r.mutationHook(work, session, mutationResult(MutationDelete, deleted, nil)); err != nil {
				return articlemodels.Article{}, err
			}
		}
		return deleted, nil
	})
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
	return preparedAtomic(ctx, r, MutationPublish, func(work context.Context, session db.Session) (PublishResult, error) {
		matched := make([]int64, 0, len(canonical))
		items := make([]MutationItem, 0, len(canonical))
		for _, id := range canonical {
			current, found, err := r.getIn(work, session, id)
			if err != nil {
				return PublishResult{}, err
			}
			if !found {
				continue
			}
			// Publishing is an explicit action, including already-published rows.
			updated, err := articlemodels.ArticleObjects.Patch(work, session, current, (articlemodels.ArticlePatch{}).WithPublished(true))
			if err != nil {
				return PublishResult{}, err
			}
			matched = append(matched, id)
			items = append(items, MutationItem{Article: cloneArticle(updated), ChangedFields: []string{"published"}})
		}
		if r.mutationHook != nil && len(items) != 0 {
			if err := r.mutationHook(work, session, MutationResult{Operation: MutationPublish, Items: items}.Clone()); err != nil {
				return PublishResult{}, err
			}
		}
		return PublishResult{MatchedIDs: matched}, nil
	})
}

func mutationResult(operation MutationOperation, article articlemodels.Article, changedFields []string) MutationResult {
	return MutationResult{
		Operation: operation,
		Items: []MutationItem{{
			Article:       cloneArticle(article),
			ChangedFields: append([]string(nil), changedFields...),
		}},
	}
}

// ValidateInput applies Article's defensive business policy to supplied
// scalars without I/O. ModelInput owns type, NULL and source identity; the
// transport owns trimming, required-input policy and its explicit allowlist.
func ValidateInput(input orm.ModelInput[articlemodels.Article]) error {
	model, valid := input.Model()
	if !valid || !model.Equal((articlemodels.ArticleDescriptor{}).Metadata()) {
		return invalid("input", "model input source does not match Article")
	}
	if value, supplied := input.Get("slug"); supplied && !value.IsNull() {
		slug, _ := value.String()
		if slug != "" && !ValidAddressSlug(slug) {
			return invalid("slug", "slug is outside the model input policy")
		}
	}
	for _, name := range []string{"title", "summary"} {
		if value, supplied := input.Get(name); supplied && !value.IsNull() {
			text, _ := value.String()
			if err := validateModelText(name, text, name == "summary"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r Repository) validateWrite(ctx context.Context, input orm.ModelInput[articlemodels.Article]) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if interfaceNil(r.backend) {
		return invalid("backend", "repository is zero or invalid")
	}
	return ValidateInput(input)
}

func (r Repository) getIn(ctx context.Context, backend db.Queryer, id int64) (articlemodels.Article, bool, error) {
	if id <= 0 {
		return articlemodels.Article{}, false, invalid("id", "id must be positive")
	}
	value, found, err := r.resource.Get(ctx, backend, id)
	if err != nil {
		return articlemodels.Article{}, false, fmt.Errorf("article get: %w", err)
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

func validateModelText(name, value string, allowEmpty bool) error {
	for _, field := range (articlemodels.ArticleDescriptor{}).Metadata().Fields {
		if field.Name == name {
			return validateText(name, value, allowEmpty, field.MaxLength)
		}
	}
	return invalid(name, "model metadata does not contain the field")
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
	if interfaceNil(ctx) {
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

func cloneArticle(article articlemodels.Article) articlemodels.Article {
	return (articlemodels.ArticleDescriptor{}).CloneModel(article)
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

// ValidAddressSlug follows the model's length and character policy. It performs
// no trimming, case folding or Unicode normalization and permits no empty path.
func ValidAddressSlug(value string) bool {
	for _, field := range (articlemodels.ArticleDescriptor{}).Metadata().Fields {
		if field.Name == "slug" {
			return utf8.RuneCountInString(value) <= field.MaxLength && validation.ValidSlug(value, field.AllowUnicode)
		}
	}
	return false
}

// Translate only the direct DML conflict inside the owning transaction. A
// failed rollback or unknown commit never becomes an input rejection.
func writeRejection(err error) error {
	conflict, ok := err.(*query.Error)
	if !ok || conflict.Category != query.CategoryIntegrity || conflict.Code != query.CodeUniqueConstraint {
		return err
	}
	return validation.Reject(validation.NewErrors(validation.New(validation.NonField, validation.CodeUnique)), err)
}

func mutationError(ctx context.Context, operation string, err error) error {
	if !interfaceNil(ctx) {
		if contextErr := ctx.Err(); contextErr != nil {
			return errors.Join(err, contextErr)
		}
	}
	if _, rejected := validation.Rejected(err); rejected {
		return err
	}
	// A confirmed rollback preserves the direct application rejection. Keep
	// joined/wrapped infrastructure failures distinct from this expected miss.
	if missing, ok := err.(*Error); ok && missing != nil && missing.Code == CodeNotFound && missing.Cause == nil {
		return err
	}
	if err == ErrNotFound {
		return err
	}
	return fmt.Errorf("article %s: %w", operation, err)
}
