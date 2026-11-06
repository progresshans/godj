package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/storage"
)

var errStoredFileMissing = errors.New("web: stored file not found")

// FileOptions controls the response representation, not access policy.
// Downloads default to attachment, application/octet-stream, no-store and
// nosniff. Inline rendering and a different media type must be intentional.
type FileOptions struct {
	Filename    string
	ContentType string
	Inline      bool
}

func (FileOptions) Format(s fmt.State, _ rune) { fmt.Fprint(s, "web.FileOptions{redacted}") }

// FileResponse serves one explicitly authorized storage reference. Resolve
// current principal/object permissions before calling it; a valid name is not
// admission. It creates no public directory route and never looks up a path or
// model from untrusted request text. Backend is borrowed for application life.
//
// Open runs after middleware succeeds. Metadata from storage.Reader belongs
// to that same opened handle; other readers stream without Content-Length.
// No racy Stat-before-Open or implicit MIME sniffing is performed. Register a
// HEAD route explicitly if needed. GET/HEAD preconditions use the opened
// metadata; GET byte ranges require storage.SeekableReader. Conditional
// responses do not enable caching or act as preconditions for a handler's writes.
// A missing file returns 404 after admission; other backend failures remain 500.
func FileResponse(backend storage.Backend, name string, options FileOptions) (Response, error) {
	if nilStreamValue(backend) {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "file", Detail: "storage backend is nil"}
	}
	if _, err := storage.NewInfo(name, 0); err != nil {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "file", Detail: "storage name is invalid", Cause: err}
	}
	filename := options.Filename
	if filename == "" {
		filename = path.Base(name)
	}
	if !utf8.ValidString(filename) || len(filename) > 1024 || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) || strings.ContainsFunc(filename, unicode.IsControl) {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "filename", Detail: "download filename is invalid"}
	}
	contentType := options.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || !validHeaderValue(contentType) || !strings.Contains(mediaType, "/") {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "content_type", Detail: "download media type is invalid"}
	}
	contentType = mime.FormatMediaType(mediaType, parameters)
	disposition := "attachment"
	if options.Inline {
		disposition = "inline"
	}
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	response, err := NewStreamResponse(http.StatusOK, header, func(ctx context.Context) (stream Stream, err error) {
		stream.Reader, err = backend.Open(ctx, name)
		if err != nil {
			if nilStreamValue(stream.Reader) && errors.Is(err, fs.ErrNotExist) && ctx.Err() == nil {
				return stream, errStoredFileMissing
			}
			return stream, err // Application owns any returned reader, even on error.
		}
		stream.Size = -1
		return stream, nil
	})
	if err == nil {
		response.stream.fileName = name
	}
	return response, err
}
