package uploads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/url"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Config separately bounds the complete wire body, file payloads, retained file
// bytes in memory, text values and part counts. MemoryBytes is a shared payload
// budget, not a per-file allowance. Zero MemoryBytes forces nonempty files to disk.
type Config struct {
	MaxBodyBytes, MaxFileBytes, MemoryBytes int64
	MaxValueBytes, MaxValueTotalBytes       int64
	MaxParts, MaxFiles                      int
	TempDir                                 string
}

func DefaultConfig() Config {
	return Config{MaxBodyBytes: 32 << 20, MaxFileBytes: 16 << 20, MemoryBytes: 5 << 19, MaxValueBytes: 64 << 10, MaxValueTotalBytes: 1 << 20, MaxParts: 1000, MaxFiles: 100}
}
func (c Config) Validate() error {
	if c.MaxBodyBytes <= 0 || c.MaxBodyBytes == math.MaxInt64 || c.MaxFileBytes <= 0 || c.MaxFileBytes > c.MaxBodyBytes || c.MemoryBytes < 0 || c.MemoryBytes > c.MaxBodyBytes || c.MaxValueBytes <= 0 || c.MaxValueBytes > c.MaxValueTotalBytes || c.MaxValueTotalBytes > c.MaxBodyBytes || c.MaxParts <= 0 || c.MaxFiles < 0 || c.MaxFiles > c.MaxParts || strings.ContainsRune(c.TempDir, 0) {
		return &Error{Code: "invalid_config"}
	}
	return nil
}

// Form keeps text and repeated file parts separate. Accessors return detached
// containers; file capabilities expire at Close, including already-open readers.
type Form struct {
	owner  *owner
	values url.Values
	files  map[string][]File
}

func (f *Form) Values() url.Values {
	result := url.Values{}
	if f != nil {
		for key, values := range f.values {
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}
func (f *Form) Files() map[string][]File {
	result := map[string][]File{}
	if f != nil {
		for key, values := range f.files {
			result[key] = append([]File(nil), values...)
		}
	}
	return result
}
func (f *Form) Close() error {
	if f == nil || f.owner == nil {
		return nil
	}
	return f.owner.close()
}
func (Form) Format(state fmt.State, _ rune) { fmt.Fprint(state, "uploads.Form{redacted}") }

// Parse consumes one multipart/form-data body without closing the borrowed
// input. No partially parsed form is returned on failure. An over-limit body
// fails even when the multipart closing boundary precedes extra wire bytes.
func Parse(ctx context.Context, body io.Reader, contentType string, config Config) (_ *Form, err error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil || body == nil {
		return nil, &Error{Code: "invalid_input"}
	}
	media, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || parameters["boundary"] == "" || len(parameters["boundary"]) > 70 {
		return nil, &Error{Code: "invalid_content_type"}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Code: "canceled", Cause: err}
	}
	form := &Form{owner: &owner{readers: make(map[*readerState]struct{})}, values: url.Values{}, files: make(map[string][]File)}
	published := false
	defer func() {
		if !published {
			if closeErr := form.Close(); closeErr != nil {
				err = &Error{Code: "cleanup_failed", Cause: errors.Join(err, closeErr)}
			}
		}
	}()
	wire := &boundedReader{ctx: ctx, reader: body, remaining: config.MaxBodyBytes, end: terminator{marker: []byte("--" + parameters["boundary"] + "--"), possible: true}}
	reader := multipart.NewReader(wire, parameters["boundary"])
	var memoryUsed, valueBytes int64
	fileCount := 0
	for count := 0; ; count++ {
		part, nextErr := reader.NextRawPart()
		// Only the parser's exact EOF proves a closing boundary. A wrapped
		// EOF is a malformed/truncated envelope, not an empty successful form.
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, classifyRead(nextErr)
		}
		if count >= config.MaxParts {
			return nil, &Error{Code: "too_many_parts"}
		}
		if len(part.Header.Values("Content-Disposition")) != 1 || len(part.Header.Values("Content-Type")) > 1 || len(part.Header.Values("Content-Transfer-Encoding")) != 0 {
			return nil, &Error{Code: "invalid_part"}
		}
		disposition, parameters, parseErr := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := parameters["name"]
		if parseErr != nil || disposition != "form-data" || name == "" || len(name) > 512 || !validMetadata(name) {
			return nil, &Error{Code: "invalid_part"}
		}
		filename, isFile := parameters["filename"]
		payload := partReader{ctx: ctx, reader: part}
		if !isFile {
			limit := min(config.MaxValueBytes, config.MaxValueTotalBytes-valueBytes)
			data, readErr := io.ReadAll(io.LimitReader(payload, limit+1))
			if readErr != nil {
				return nil, classifyRead(readErr)
			}
			if int64(len(data)) > limit {
				return nil, &Error{Code: "value_too_large"}
			}
			valueBytes += int64(len(data))
			form.values[name] = append(form.values[name], string(data))
			continue
		}
		fileCount++
		if fileCount > config.MaxFiles {
			return nil, &Error{Code: "too_many_files"}
		}
		if filename == "" {
			data, readErr := io.ReadAll(io.LimitReader(payload, 1))
			if readErr != nil {
				return nil, classifyRead(readErr)
			}
			if len(data) != 0 {
				return nil, &Error{Code: "invalid_filename"}
			}
			continue
		}
		filename, parseErr = sanitizeName(filename)
		if parseErr != nil {
			return nil, parseErr
		}
		typ := part.Header.Get("Content-Type")
		if !validMetadata(typ) {
			return nil, &Error{Code: "invalid_content_type"}
		}
		file, readErr := receiveFile(payload, form.owner, filename, typ, config, config.MemoryBytes-memoryUsed)
		if readErr != nil {
			return nil, readErr
		}
		memoryUsed += int64(len(file.state.data))
		form.files[name] = append(form.files[name], file)
		form.owner.files = append(form.owner.files, file.state)
	}
	// multipart.Reader may stop at its closing boundary without consuming an
	// epilogue. Its buffered bytes already consumed the wire budget; drain the
	// remaining input through that same budget and context.
	if _, err := io.Copy(io.Discard, wire); err != nil {
		return nil, classifyRead(err)
	}
	if !wire.end.complete {
		return nil, &Error{Code: "malformed_body"}
	}
	published = true
	return form, nil
}

func receiveFile(part io.Reader, owned *owner, name, typ string, config Config, memory int64) (File, error) {
	data, err := io.ReadAll(io.LimitReader(part, min(memory, config.MaxFileBytes)+1))
	if err != nil {
		return File{}, classifyRead(err)
	}
	if int64(len(data)) > config.MaxFileBytes {
		return File{}, &Error{Code: "file_too_large"}
	}
	s := &fileState{owner: owned, name: name, contentType: typ, size: int64(len(data))}
	if s.size <= memory {
		s.data = data
		return File{s}, nil
	}
	if owned.directory == "" {
		owned.directory, err = os.MkdirTemp(config.TempDir, "godj-upload-")
		if err != nil {
			return File{}, &Error{Code: "write_failed", Cause: err}
		}
	}
	file, err := os.CreateTemp(owned.directory, "part-")
	if err != nil {
		return File{}, &Error{Code: "write_failed", Cause: err}
	}
	closed := false
	defer func() {
		// A caller-supplied reader can panic before the normal Close below.
		// Close the in-progress writer before Parse removes its directory.
		if !closed {
			_ = file.Close()
		}
	}()
	s.path = file.Name()
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	if writeErr == nil {
		var count int64
		count, writeErr = io.Copy(file, io.LimitReader(part, config.MaxFileBytes-s.size+1))
		s.size += count
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		closeErr = &Error{Code: "close_failed", Cause: closeErr}
	}
	if writeErr != nil {
		var inputErr *Error
		if errors.As(writeErr, &inputErr) {
			return File{}, errors.Join(writeErr, closeErr)
		}
		return File{}, &Error{Code: "write_failed", Cause: errors.Join(writeErr, closeErr)}
	}
	if closeErr != nil {
		return File{}, closeErr
	}
	if s.size > config.MaxFileBytes {
		return File{}, &Error{Code: "file_too_large"}
	}
	return File{s}, nil
}

type boundedReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
	end       terminator
}

type partReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r partReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = classifyRead(err)
	}
	return n, err
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var one [1]byte
		n, err := r.reader.Read(one[:])
		if n != 0 {
			return 0, &Error{Code: "body_too_large"}
		}
		if err == io.EOF {
			r.end.eof()
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	r.end.observe(p[:n])
	if err == io.EOF {
		r.end.eof()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = &Error{Code: "read_failed", Cause: err}
	}
	return n, err
}
func classifyRead(err error) error {
	var failure *Error
	if errors.As(err, &failure) {
		return failure
	}
	return &Error{Code: "malformed_body", Cause: err}
}
func validMetadata(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func sanitizeName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", &Error{Code: "invalid_filename"}
	}
	name = strings.ReplaceAll(html.UnescapeString(name), "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", &Error{Code: "invalid_filename"}
	}
	if utf8.RuneCountInString(name) > 255 {
		ext := []rune(path.Ext(name))
		if len(ext) > 255 {
			ext = ext[:255]
		}
		name = string([]rune(name)[:255-len(ext)]) + string(ext)
	}
	return name, nil
}
