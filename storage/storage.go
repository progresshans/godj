// Package storage owns named file content independently of request uploads.
// A stored name is metadata, never permission to read, delete, or serve a file.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/progresshans/godj/uploads"
)

// Outcome describes Save publication only, not a database commit or power-loss
// durability. Unknown errors must not trigger automatic retries or deletion.
type Outcome string

const (
	Uncertain    Outcome = ""
	NotPublished Outcome = "not_published"
	Published    Outcome = "published"
)

type Error struct {
	Code    string
	Outcome Outcome
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "storage: " + e.Code
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && (other.Code == "" || other.Code == e.Code) && (other.Outcome == Uncertain || other.Outcome == e.Outcome)
}
func (e Error) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.Error()) }

// Info contains an explicit storage-relative name and byte count. Constructing
// it does not perform I/O or grant authority. A successful Save returns the
// actual name, which may differ from the proposed name after a collision.
type Info struct {
	name string
	size int64
}

func NewInfo(name string, size int64) (Info, error) {
	if err := validateName(name); err != nil {
		return Info{}, err
	}
	if size < 0 {
		return Info{}, &Error{Code: "invalid_size", Outcome: NotPublished}
	}
	return Info{name: name, size: size}, nil
}
func (i Info) Valid() bool              { return i.name != "" }
func (i Info) Name() string             { return i.name }
func (i Info) Size() int64              { return i.size }
func (Info) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.Info{redacted}") }

// SaveOptions.MaxLength tightens the maximum complete name length in Unicode
// characters (for example a model FileField's limit). Zero uses backend policy.
type SaveOptions struct{ MaxLength int }

// Backend never owns or closes the reader passed to Save. Open returns an
// independently owned reader which the caller must close. Delete is idempotent
// for missing names. All operations require application authorization.
// Save may return nonzero Info with an error after publication; inspect its
// Error.Outcome and reconcile instead of retrying or deleting blindly.
type Backend interface {
	Save(context.Context, string, io.Reader, SaveOptions) (Info, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Stat(context.Context, string) (Info, error)
	Delete(context.Context, string) error
}

// SaveUpload opens a request capability while it is alive, consumes it through
// the explicitly chosen backend/name, and closes only that new reader. It does
// not commit a database reference, delete an old file, or prolong the upload.
func SaveUpload(ctx context.Context, backend Backend, name string, file uploads.File, options SaveOptions) (info Info, err error) {
	if backend == nil {
		return Info{}, &Error{Code: "invalid_backend", Outcome: NotPublished}
	}
	reader, err := file.Open(ctx)
	if err != nil {
		return Info{}, &Error{Code: "input_open_failed", Outcome: NotPublished, Cause: err}
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			outcome := Uncertain
			if err == nil {
				outcome = Published
			} else if failure, ok := err.(*Error); ok {
				outcome = failure.Outcome
			}
			err = &Error{Code: "input_close_failed", Outcome: outcome, Cause: errors.Join(err, closeErr)}
		}
	}()
	info, err = backend.Save(ctx, name, reader, options)
	if err == nil && (!info.Valid() || info.Size() != file.Size()) {
		err = &Error{Code: "invalid_result", Outcome: Uncertain}
	}
	return info, err
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return &Error{Code: "invalid_context", Outcome: NotPublished}
	}
	if err := ctx.Err(); err != nil {
		return &Error{Code: "canceled", Outcome: NotPublished, Cause: err}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// The facade shares a cursor and lock when copied, and never formats paths.
type fileReader struct{ state *fileReaderState }
type fileReaderState struct {
	mu       sync.Mutex
	ctx      context.Context
	reader   io.ReadCloser
	closed   bool
	closeErr error
}

func (r *fileReader) Read(p []byte) (int, error) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.closed {
		return 0, &Error{Code: "closed"}
	}
	if err := r.state.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	n, err := r.state.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = &Error{Code: "read_failed", Cause: err}
	}
	return n, err
}
func (r *fileReader) Close() error {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if !r.state.closed {
		r.state.closed = true
		if err := r.state.reader.Close(); err != nil {
			r.state.closeErr = &Error{Code: "close_failed", Cause: err}
		}
	}
	return r.state.closeErr
}
func (fileReader) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.Reader{redacted}") }
