// Package storage owns named file content independently of request uploads.
// A stored name is metadata, never permission to read, delete, or serve a file.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

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
	name     string
	size     int64
	metadata ContentMetadata
}

// ContentMetadata describes the same content as an Info snapshot. Version is
// an optional, public opaque token that changes whenever the representation's
// bytes change. Backends must not infer it from a name, byte count or timestamp.
// Modified is optional. ModifiedStrong additionally asserts that different
// content cannot share its whole-second timestamp; ordinary filesystems do not
// provide that guarantee. Neither metadata field grants access to the content.
type ContentMetadata struct {
	Version        string
	Modified       time.Time
	ModifiedStrong bool
}

func (ContentMetadata) Format(s fmt.State, _ rune) {
	fmt.Fprint(s, "storage.ContentMetadata{redacted}")
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

// WithContentMetadata returns a new snapshot without performing I/O. Empty
// metadata means unknown. An Open implementation must obtain this information
// from the returned handle, not from a separate lookup of a reusable name.
func (i Info) WithContentMetadata(metadata ContentMetadata) (Info, error) {
	if !i.Valid() || len(metadata.Version) > 1024 || metadata.Modified.Year() < 1 || metadata.Modified.Year() > 9999 || metadata.ModifiedStrong && metadata.Modified.IsZero() {
		return Info{}, &Error{Code: "invalid_metadata", Outcome: NotPublished}
	}
	metadata.Modified = metadata.Modified.Round(0).UTC()
	i.metadata = metadata
	return i, nil
}

func (i Info) ContentMetadata() ContentMetadata { return i.metadata }

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

// Reader optionally supplies metadata from the same opened content handle.
// This avoids a separate Stat/Open race when a HTTP response declares length.
// Info is an immutable snapshot, not a promise that external writers cannot
// alter the content. Readers without this capability have unknown length.
type Reader interface {
	io.ReadCloser
	Info() Info
}

// SeekableReader supports bounded partial reads from the same opened handle.
// Seek and Read share one cursor. Implementations honor the Open context and
// serialize cursor operations and Close; callers own the operation sequence.
type SeekableReader interface {
	Reader
	io.Seeker
}

// SaveUpload opens a request capability while it is alive, consumes it through
// the explicitly chosen backend/name, and closes only that new reader. It does
// not commit a database reference, delete an old file, or prolong the upload.
func SaveUpload(ctx context.Context, backend Backend, name string, file uploads.File, options SaveOptions) (info Info, err error) {
	if nilValue(backend) {
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
	if nilValue(ctx) {
		return &Error{Code: "invalid_context", Outcome: NotPublished}
	}
	if err := ctx.Err(); err != nil {
		return &Error{Code: "canceled", Outcome: NotPublished, Cause: err}
	}
	return nil
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
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
	mu     sync.Mutex
	ctx    context.Context
	reader interface {
		io.ReadCloser
		io.Seeker
	}
	info     Info
	closed   bool
	closeErr error
}

func (r *fileReader) Info() Info {
	if r == nil || r.state == nil {
		return Info{}
	}
	return r.state.info
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
func (r *fileReader) Seek(offset int64, whence int) (int64, error) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.closed {
		return 0, &Error{Code: "closed"}
	}
	if err := r.state.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	position, err := r.state.reader.Seek(offset, whence)
	if err != nil {
		return position, &Error{Code: "seek_failed", Cause: err}
	}
	return position, nil
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
