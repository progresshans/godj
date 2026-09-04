// Package adminapp connects the bounded Article Admin experience to the
// presentation-neutral Article application boundary. HTTP, forms,
// authentication, and rendering are composed separately.
package adminapp

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/article/articleapp"
)

const (
	DefaultPageSize    = articleapp.DefaultPageSize
	MaximumPageSize    = articleapp.MaximumPageSize
	MaximumActionIDs   = articleapp.MaximumActionIDs
	MaximumSearchBytes = articleapp.MaximumSearchBytes
)

type Backend = articleapp.Backend
type ErrorCode string

const (
	CodeInvalidInput ErrorCode = "invalid_input"
	CodeNotFound     ErrorCode = "not_found"
)

// Error preserves the existing Admin application error surface while the
// persistence implementation lives in articleapp.
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

func (e *Error) Is(target error) bool {
	return e != nil && e.Code == CodeNotFound && target == admin.ErrObjectNotFound
}

type Article = articleapp.Article
type Input = articleapp.Input
type ListOptions = articleapp.ListOptions
type Page = articleapp.Page
type PublishResult = articleapp.PublishResult

// Repository preserves the Admin-facing persistence API while delegating all
// Article I/O and transaction ownership to articleapp.Repository.
type Repository struct {
	core  articleapp.Repository
	valid bool
}

func NewRepository(backend Backend) (Repository, error) {
	core, err := articleapp.NewRepository(backend)
	if err != nil {
		return Repository{}, adminRepositoryError(err)
	}
	return Repository{core: core, valid: true}, nil
}

func (r Repository) List(ctx context.Context, options ListOptions) (Page, error) {
	page, err := r.core.List(ctx, options)
	return page, adminRepositoryError(err)
}

func (r Repository) Get(ctx context.Context, id int64) (Article, bool, error) {
	article, found, err := r.core.Get(ctx, id)
	return article, found, adminRepositoryError(err)
}

func (r Repository) Create(ctx context.Context, input Input) (Article, error) {
	article, err := r.core.Create(ctx, input)
	return article, adminRepositoryError(err)
}

func (r Repository) Update(ctx context.Context, id int64, input Input) (Article, []string, error) {
	article, changed, err := r.core.Update(ctx, id, input)
	return article, changed, adminRepositoryError(err)
}

func (r Repository) Delete(ctx context.Context, id int64) (Article, error) {
	article, err := r.core.Delete(ctx, id)
	return article, adminRepositoryError(err)
}

func (r Repository) withMutationHook(hook articleapp.MutationHook) Repository {
	r.core = r.core.WithMutationHook(hook)
	return r
}

func (r Repository) Publish(ctx context.Context, ids []int64) (PublishResult, error) {
	result, err := r.core.Publish(ctx, ids)
	return result, adminRepositoryError(err)
}

func (r Repository) validState() bool { return r.valid }

func invalid(field, detail string) error {
	return &Error{Code: CodeInvalidInput, Field: field, Detail: detail}
}

func IsCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}

type adminPersistenceError struct {
	cause error
}

func (e adminPersistenceError) Error() string { return e.cause.Error() }
func (e adminPersistenceError) Unwrap() error { return e.cause }
func (e adminPersistenceError) Is(target error) bool {
	return target == admin.ErrObjectNotFound && articleapp.IsCode(e.cause, articleapp.CodeNotFound)
}
func (e adminPersistenceError) As(target any) bool {
	adminError, ok := target.(**Error)
	if !ok {
		return false
	}
	var articleError *articleapp.Error
	if !errors.As(e.cause, &articleError) {
		return false
	}
	*adminError = adminErrorFrom(articleError)
	return true
}

func adminRepositoryError(err error) error {
	if err == nil {
		return err
	}
	var articleError *articleapp.Error
	if !errors.As(err, &articleError) {
		return err
	}
	if err == articleError {
		return adminErrorFrom(articleError)
	}
	return adminPersistenceError{cause: err}
}

func adminErrorFrom(err *articleapp.Error) *Error {
	return &Error{
		Code:   ErrorCode(err.Code),
		Field:  err.Field,
		Detail: err.Detail,
		Cause:  err.Cause,
	}
}
