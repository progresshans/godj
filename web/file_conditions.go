package web

import (
	"context"
	"encoding/base64"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/progresshans/godj/storage"
)

const maxFileRequestHeaderBytes = 8 << 10

func validateFileHeaders(header http.Header) error {
	contentTypes := 0
	for name, values := range header {
		switch strings.ToLower(name) {
		case "etag", "last-modified", "accept-ranges", "content-range", "content-encoding":
			return &Error{Code: CodeInvalidResponse, Field: "header", Detail: "file representation headers belong to opened content"}
		case "content-type":
			contentTypes += len(values)
			for _, value := range values {
				mediaType, _, err := mime.ParseMediaType(value)
				if err != nil || !strings.Contains(mediaType, "/") {
					return &Error{Code: CodeInvalidResponse, Field: "content_type", Detail: "download media type is invalid"}
				}
			}
		}
	}
	if contentTypes > 1 {
		return &Error{Code: CodeInvalidResponse, Field: "content_type", Detail: "file requires one media type"}
	}
	return nil
}

// prepareFileStream runs only after application admission and Open. It keeps
// the original reader owned by writeStream on every error/panic path, replacing
// it only with a wrapper that owns that same reader. It never opens by name or
// retains the borrowed Web request. Response is this request's private copy.
func prepareFileStream(ctx context.Context, request *http.Request, response *Response, stream *Stream, limit int64) (bodyless bool, err error) {
	var metadata storage.ContentMetadata
	if reader, ok := stream.Reader.(storage.Reader); ok {
		info := reader.Info()
		if !info.Valid() || info.Name() != response.stream.fileName {
			return false, streamFailure("metadata", nil)
		}
		stream.Size = info.Size()
		metadata = info.ContentMetadata()
		if metadata.Modified.Year() < 1 || metadata.Modified.Year() > 9999 {
			return false, streamFailure("metadata", nil)
		}
	}
	// Canonicalize a fresh header map: middleware may use lower-case keys, and
	// range/conditional selection must not mutate a reusable Response snapshot.
	header := make(http.Header, len(response.header)+3)
	copyResponseHeaders(header, response.header)
	response.header = header
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/octet-stream")
	}
	etag := ""
	if metadata.Version != "" {
		etag = `"` + base64.RawURLEncoding.EncodeToString([]byte(metadata.Version)) + `"`
		header.Set("ETag", etag)
	}
	modified := metadata.Modified.UTC().Truncate(time.Second)
	if !metadata.Modified.IsZero() {
		now := time.Now().UTC().Truncate(time.Second)
		if modified.After(now) {
			modified, metadata.ModifiedStrong = now, false
		}
		header.Set("Last-Modified", modified.Format(http.TimeFormat))
	}
	seekable, canSeek := stream.Reader.(storage.SeekableReader)
	if canSeek && stream.Size >= 0 {
		header.Set("Accept-Ranges", "bytes")
	} else {
		header.Set("Accept-Ranges", "none")
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false, nil // A response cannot guard writes the handler already made.
	}
	fields, valid := fileRequestHeaders(request.Header)
	if !valid {
		return emptyFileResponse(response, stream, http.StatusRequestHeaderFieldsTooLarge), nil
	}
	status := filePreconditionStatus(fields, etag, modified)
	if status != http.StatusOK {
		return emptyFileResponse(response, stream, status), nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// RFC 9110 defines ranges only for GET. HEAD describes the full GET body.
	if request.Method != http.MethodGet || !canSeek || stream.Size < 0 {
		return false, nil
	}
	if !fileIfRangeMatches(fields.Get("If-Range"), etag, modified, metadata.ModifiedStrong) {
		return false, nil
	}
	ranges, rejected := parseFileRanges(fields.Get("Range"), stream.Size)
	if rejected {
		header.Set("Content-Range", unsatisfiedFileRange(stream.Size))
		return emptyFileResponse(response, stream, http.StatusRequestedRangeNotSatisfiable), nil
	}
	if len(ranges) == 0 {
		return false, nil
	}
	return false, selectFileRanges(response, stream, seekable, ranges, limit)
}

func emptyFileResponse(response *Response, stream *Stream, status int) bool {
	response.status, stream.Size = status, 0
	response.header.Del("Content-Type")
	response.header.Del("Content-Disposition")
	return true
}

// Bound the fields before joining repeated values. These are the only request
// headers retained for selection, and no client-controlled value is reflected.
func fileRequestHeaders(header http.Header) (http.Header, bool) {
	result := make(http.Header)
	remaining, count := maxFileRequestHeaderBytes, 64
	for name, values := range header {
		key := http.CanonicalHeaderKey(name)
		switch key {
		case "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Range", "Range":
		default:
			continue
		}
		for _, value := range values {
			if len(value) > remaining || count == 0 {
				return nil, false
			}
			remaining -= len(value)
			count--
			result[key] = append(result[key], value)
		}
	}
	for name, values := range result {
		result[name] = []string{strings.Join(values, ",")}
	}
	return result, true
}

// Preconditions follow RFC 9110 section 13.2.2. Invalid entity-tag syntax is a
// bad request; invalid/unknown dates are ignored. Empty fields are absent.
func filePreconditionStatus(fields http.Header, etag string, modified time.Time) int {
	if value := fields.Get("If-Match"); value != "" {
		matched, valid := matchFileETags(value, etag, true)
		if !valid {
			return http.StatusBadRequest
		}
		if !matched {
			return http.StatusPreconditionFailed
		}
	} else if date, err := http.ParseTime(fields.Get("If-Unmodified-Since")); err == nil && !modified.IsZero() && modified.After(date) {
		return http.StatusPreconditionFailed
	}
	if value := fields.Get("If-None-Match"); value != "" {
		matched, valid := matchFileETags(value, etag, false)
		if !valid {
			return http.StatusBadRequest
		}
		if matched {
			return http.StatusNotModified
		}
	} else if date, err := http.ParseTime(fields.Get("If-Modified-Since")); err == nil && !modified.IsZero() && !modified.After(date) {
		return http.StatusNotModified
	}
	return http.StatusOK
}

func matchFileETags(value, current string, strong bool) (matched, valid bool) {
	value = strings.Trim(value, " \t")
	if value == "*" {
		return true, true // Open has already established that the file exists.
	}
	tags := 0
	for segments := 0; value != "" && segments < 64; segments++ {
		value = strings.TrimLeft(value, " \t")
		if strings.HasPrefix(value, ",") {
			value = value[1:]
			continue
		}
		weak := strings.HasPrefix(value, "W/")
		if weak {
			value = value[2:]
		}
		if len(value) == 0 || value[0] != '"' {
			return false, false
		}
		end := strings.IndexByte(value[1:], '"') + 1
		if end == 0 {
			return false, false
		}
		for i := 1; i < end; i++ {
			if value[i] < 0x21 || value[i] == 0x7f {
				return false, false
			}
		}
		tags++
		matched = matched || (!strong || !weak) && value[:end+1] == current
		value = strings.TrimLeft(value[end+1:], " \t")
		if value != "" {
			if value[0] != ',' {
				return false, false
			}
			value = value[1:]
		}
	}
	return matched, tags > 0 && strings.Trim(value, " \t") == ""
}

func fileIfRangeMatches(value, etag string, modified time.Time, modifiedStrong bool) bool {
	value = strings.Trim(value, " \t")
	if value == "" {
		return true
	}
	if etag != "" && value == etag {
		return true
	}
	if !modifiedStrong || modified.IsZero() {
		return false
	}
	date, err := http.ParseTime(value)
	return err == nil && modified.Equal(date)
}
