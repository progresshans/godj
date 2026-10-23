// Package uploads parses bounded multipart input and owns temporary upload
// content. A Form must be closed; web.Request does so at the handler boundary.
package uploads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Error never includes client filenames, field values or temporary paths in
// ordinary formatting. Unwrap preserves explicit I/O and cancellation checks.
type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "uploads: " + e.Code
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && (other.Code == "" || other.Code == e.Code)
}
func (e Error) Format(state fmt.State, _ rune) { fmt.Fprint(state, e.Error()) }

// File is immutable metadata and a capability to open the received content.
// Name is a sanitized client basename, never a storage path. ContentType is
// untrusted client metadata, not a verified content type.
type File struct{ state *fileState }
type fileState struct {
	owner                   *owner
	name, contentType, path string
	size                    int64
	data                    []byte
}
type owner struct {
	mu        sync.Mutex
	closed    bool
	closeErr  error
	directory string
	readers   map[*readerState]struct{}
	files     []*fileState
}

// Valid reports constructed metadata. Open still checks lifetime and context;
// metadata validation cannot prove that file content is currently readable.
func (f File) Valid() bool { return f.state != nil }
func (f File) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f File) ContentType() string {
	if f.state == nil {
		return ""
	}
	return f.state.contentType
}
func (f File) Size() int64 {
	if f.state == nil {
		return 0
	}
	return f.state.size
}
func (f File) Equal(other File) bool        { return f.state != nil && f.state == other.state }
func (File) Format(state fmt.State, _ rune) { fmt.Fprint(state, "uploads.File{redacted}") }

// NewFile snapshots in-memory content for non-HTTP callers. It has no temporary
// resources and does not require a Form owner. Limits on HTTP input belong to Parse.
func NewFile(name, contentType string, content []byte) (File, error) {
	name, err := sanitizeName(name)
	if err != nil {
		return File{}, err
	}
	if !validMetadata(contentType) {
		return File{}, &Error{Code: "invalid_content_type"}
	}
	return File{&fileState{name: name, contentType: contentType, size: int64(len(content)), data: bytes.Clone(content)}}, nil
}

// Reader owns a fresh cursor. Read observes both its context and its Form's
// lifetime. Close may be called concurrently with reads and is idempotent.
// Copying a Reader retains the same cursor and lifetime, never copies a lock.
type Reader struct{ state *readerState }
type readerState struct {
	mu       sync.Mutex
	ctx      context.Context
	owner    *owner
	reader   io.Reader
	closer   io.Closer
	closed   bool
	closeErr error
}

func (f File) Open(ctx context.Context) (*Reader, error) {
	if ctx == nil || f.state == nil {
		return nil, &Error{Code: "invalid_file"}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Code: "canceled", Cause: err}
	}
	s := f.state
	if s.owner != nil {
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		if s.owner.closed {
			return nil, &Error{Code: "closed"}
		}
	}
	r := &readerState{ctx: ctx, owner: s.owner, reader: bytes.NewReader(s.data)}
	if s.path != "" {
		file, err := os.Open(s.path)
		if err != nil {
			return nil, &Error{Code: "open_failed", Cause: err}
		}
		r.reader, r.closer = file, file
	}
	if s.owner != nil {
		s.owner.readers[r] = struct{}{}
	}
	return &Reader{state: r}, nil
}

func (r *Reader) Read(target []byte) (int, error) {
	if r == nil || r.state == nil {
		return 0, &Error{Code: "closed"}
	}
	return r.state.read(target)
}
func (r *readerState) read(target []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, &Error{Code: "closed"}
	}
	if err := r.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	n, err := r.reader.Read(target)
	if err != nil && !errors.Is(err, io.EOF) {
		err = &Error{Code: "read_failed", Cause: err}
	}
	return n, err
}

func (r *Reader) Close() error {
	if r == nil || r.state == nil {
		return nil
	}
	state := r.state
	if state.owner != nil {
		state.owner.mu.Lock()
		defer state.owner.mu.Unlock()
		delete(state.owner.readers, state)
	}
	return state.close()
}
func (r *readerState) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		if r.closer != nil {
			if err := r.closer.Close(); err != nil {
				r.closeErr = &Error{Code: "close_failed", Cause: err}
			}
		}
		r.reader, r.closer = nil, nil
	}
	return r.closeErr
}
func (Reader) Format(state fmt.State, _ rune) { fmt.Fprint(state, "uploads.Reader{redacted}") }

func (o *owner) close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.closeErr
	}
	o.closed = true
	var failures []error
	for reader := range o.readers {
		failures = append(failures, reader.close())
	}
	o.readers = nil
	for _, file := range o.files {
		file.data = nil
	}
	o.files = nil
	if o.directory != "" {
		if err := os.RemoveAll(o.directory); err != nil {
			failures = append(failures, &Error{Code: "cleanup_failed", Cause: err})
		}
	}
	o.closeErr = errors.Join(failures...)
	return o.closeErr
}
