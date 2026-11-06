package uploads_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/uploads"
)

type part struct {
	name, filename, content string
	file                    bool
}

func body(t *testing.T, parts ...part) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	w := multipart.NewWriter(&buffer)
	for _, p := range parts {
		var writer io.Writer
		var err error
		if p.file {
			writer, err = w.CreateFormFile(p.name, p.filename)
		} else {
			writer, err = w.CreateFormField(p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(writer, p.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), w.FormDataContentType()
}
func config(t *testing.T) uploads.Config {
	c := uploads.DefaultConfig()
	c.MaxBodyBytes, c.MaxFileBytes, c.MemoryBytes = 2048, 128, 5
	c.MaxValueBytes, c.MaxValueTotalBytes, c.MaxParts, c.MaxFiles = 32, 64, 8, 4
	c.TempDir = t.TempDir()
	return c
}
func empty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary resources retained: %v %v", entries, err)
	}
}
func TestMultipartOwnsMemoryDiskAndIndependentReaders(t *testing.T) {
	c := config(t)
	input, typ := body(t, part{name: "title", content: "one"}, part{name: "title", content: "two"}, part{name: "document", filename: `C:\fakepath\first.txt`, content: "abc", file: true}, part{name: "document", filename: "../../second.txt", content: "defg", file: true})
	f, err := uploads.Parse(t.Context(), bytes.NewReader(input), typ, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	if got := f.Values()["title"]; len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatal(got)
	}
	files := f.Files()["document"]
	if len(files) != 2 || files[0].Name() != "first.txt" || files[1].Name() != "second.txt" || files[0].Size() != 3 || files[1].Size() != 4 {
		t.Fatal("metadata")
	}
	entries, err := os.ReadDir(c.TempDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("global memory budget did not spill", entries, err)
	}
	spools, err := os.ReadDir(filepath.Join(c.TempDir, entries[0].Name()))
	if err != nil || len(spools) != 1 {
		t.Fatal("unexpected disk payloads", spools, err)
	}
	info, err := spools[0].Info()
	if err != nil || info.Size() != 4 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("private file or spill size", info, err)
	}
	f.Values()["title"][0] = "changed"
	f.Files()["document"][0] = uploads.File{}
	if f.Values()["title"][0] != "one" || !f.Files()["document"][0].Equal(files[0]) {
		t.Fatal("borrowed containers")
	}
	left, err := files[1].Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	right, err := files[1].Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var first [1]byte
	if _, err := left.Read(first[:]); err != nil || first[0] != 'd' {
		t.Fatal(err)
	}
	data, err := io.ReadAll(right)
	if err != nil || string(data) != "defg" {
		t.Fatal("shared cursor", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	empty(t, c.TempDir)
	if _, err := left.Read(first[:]); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("retained reader", err)
	}
	if _, err := files[0].Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("retained memory capability", err)
	}
	if _, err := files[1].Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("retained disk capability", err)
	}
	if err := right.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMultipartRejectsLimitsMalformedAndCleansPartialSpills(t *testing.T) {
	for _, test := range []struct {
		name, code string
		parts      []part
		edit       func(*uploads.Config)
		mutate     func([]byte) []byte
	}{
		{name: "file", code: "file_too_large", parts: []part{{name: "f", filename: "private.txt", content: strings.Repeat("x", 129), file: true}}},
		{name: "value", code: "value_too_large", parts: []part{{name: "v", content: strings.Repeat("x", 33)}}},
		{name: "total_values", code: "value_too_large", parts: []part{{name: "v", content: strings.Repeat("x", 32)}, {name: "v", content: strings.Repeat("x", 32)}, {name: "v", content: "x"}}},
		{name: "parts", code: "too_many_parts", parts: []part{{name: "v", content: "1"}, {name: "v", content: "2"}}, edit: func(c *uploads.Config) { c.MaxParts, c.MaxFiles = 1, 1 }},
		{name: "files", code: "too_many_files", parts: []part{{name: "f", filename: "one", content: "123456", file: true}, {name: "f", filename: "two", content: "x", file: true}}, edit: func(c *uploads.Config) { c.MaxFiles = 1 }},
		{name: "wire_epilogue", code: "body_too_large", parts: []part{{name: "f", filename: "one", content: "123456", file: true}}, mutate: func(b []byte) []byte { return append(b, bytes.Repeat([]byte("x"), 2048)...) }},
		{name: "truncated", code: "malformed_body", parts: []part{{name: "f", filename: "one", content: "12345678", file: true}}, mutate: func(b []byte) []byte { return b[:len(b)-20] }},
		{name: "invalid_basename", code: "invalid_filename", parts: []part{{name: "f", filename: "../", file: true}}},
		{name: "nameless_payload", code: "invalid_filename", parts: []part{{name: "f", filename: "", content: "hidden", file: true}}},
		{name: "unknown_part", code: "invalid_part", parts: []part{{name: "", content: "hidden"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := config(t)
			if test.edit != nil {
				test.edit(&c)
			}
			input, typ := body(t, test.parts...)
			if test.mutate != nil {
				input = test.mutate(input)
			}
			f, err := uploads.Parse(t.Context(), bytes.NewReader(input), typ, c)
			if f != nil || !errors.Is(err, &uploads.Error{Code: test.code}) {
				t.Fatalf("form %v error %v, want %s", f, err, test.code)
			}
			empty(t, c.TempDir)
		})
	}
	c := config(t)
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	header := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="f"; filename="private.txt"`}, "Content-Transfer-Encoding": {"base64"}}
	p, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(p, "c2VjcmV0")
	_ = w.Close()
	if _, err := uploads.Parse(t.Context(), &b, w.FormDataContentType(), c); !errors.Is(err, &uploads.Error{Code: "invalid_part"}) {
		t.Fatal(err)
	}
	empty(t, c.TempDir)
}

type failureReader struct{ err error }

func (r failureReader) Read([]byte) (int, error) { return 0, r.err }

func TestMultipartRequiresACompleteEnvelope(t *testing.T) {
	config := config(t)
	for index, body := range []string{"", "malformed", "--fixture\r\n", "--fixture\r\nContent-Disposition: form-data; name=\"value\"\r\n\r\ntext", "--fixture--suffix\r\n", "--fixture--\r \n"} {
		form, err := uploads.Parse(t.Context(), strings.NewReader(body), "multipart/form-data; boundary=fixture", config)
		if form != nil || !errors.Is(err, &uploads.Error{Code: "malformed_body"}) {
			t.Fatal("incomplete multipart accepted", index, err)
		}
	}
	for _, ending := range []string{"", "\r\n", " \t\r\n", "\r\nepilogue"} {
		fragmented := &cancelReader{reader: bytes.NewReader([]byte("--fixture--" + ending))}
		form, err := uploads.Parse(t.Context(), fragmented, "multipart/form-data; boundary=fixture", config)
		if err != nil || len(form.Values()) != 0 || len(form.Files()) != 0 {
			t.Fatal("complete empty envelope rejected", err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type cancelReader struct {
	reader          *bytes.Reader
	consumed, after int
	cancel          func()
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if len(p) > 8 {
		p = p[:8]
	}
	n, err := r.reader.Read(p)
	r.consumed += n
	if r.consumed >= r.after && r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return n, err
}
func TestMultipartCancellationCleansPreviouslySpilledContent(t *testing.T) {
	c := config(t)
	input, typ := body(t, part{name: "f", filename: "first.txt", content: "first payload", file: true}, part{name: "f", filename: "second.txt", content: strings.Repeat("x", 64), file: true})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	spilled := false
	reader := &cancelReader{reader: bytes.NewReader(input), after: len(input) - 60, cancel: func() { entries, err := os.ReadDir(c.TempDir); spilled = err == nil && len(entries) > 0; cancel() }}
	form, err := uploads.Parse(ctx, reader, typ, c)
	if form != nil || !errors.Is(err, context.Canceled) || !spilled {
		t.Fatal("cancellation did not exercise a live spill", err, spilled)
	}
	empty(t, c.TempDir)
}

type panicReader struct {
	reader    *bytes.Reader
	directory string
	spilled   bool
}

func (r *panicReader) Read(p []byte) (int, error) {
	if entries, err := os.ReadDir(r.directory); err == nil && len(entries) > 0 {
		r.spilled = true
		panic("synthetic input-reader panic")
	}
	if len(p) > 8 {
		p = p[:8]
	}
	return r.reader.Read(p)
}
func TestMultipartReaderPanicCleansInProgressWriter(t *testing.T) {
	c := config(t)
	input, typ := body(t, part{name: "f", filename: "private.txt", content: strings.Repeat("x", 80), file: true})
	reader := &panicReader{reader: bytes.NewReader(input), directory: c.TempDir}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _ = uploads.Parse(t.Context(), reader, typ, c)
	}()
	if !panicked || !reader.spilled {
		t.Fatal("panic did not reach an in-progress spill")
	}
	empty(t, c.TempDir)
}
func TestMultipartContextIOAndNoDiskForSmallFiles(t *testing.T) {
	c := config(t)
	input, typ := body(t, part{name: "f", filename: "one", content: "small", file: true})
	c.TempDir = filepath.Join(c.TempDir, "unused-parent")
	f, err := uploads.Parse(t.Context(), bytes.NewReader(input), typ, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(c.TempDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("memory upload wrote to disk", err)
	}
	_ = f.Close()
	c = config(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if f, err = uploads.Parse(ctx, bytes.NewReader(input), typ, c); f != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	marker := errors.New("read failure with private contents")
	if f, err = uploads.Parse(t.Context(), io.MultiReader(bytes.NewReader(input[:len(input)-20]), failureReader{marker}), typ, c); f != nil || !errors.Is(err, marker) {
		t.Fatal(err)
	}
	empty(t, c.TempDir)
	c.MemoryBytes = 0
	sentinel := filepath.Join(c.TempDir, "keep")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	c.TempDir = sentinel
	if f, err = uploads.Parse(t.Context(), bytes.NewReader(input), typ, c); f != nil || !errors.Is(err, &uploads.Error{Code: "write_failed"}) {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(sentinel); err != nil || string(saved) != "keep" {
		t.Fatal("failed upload changed unrelated file", err)
	}
}

func TestFileSnapshotPrivacyAndConcurrentClose(t *testing.T) {
	payload := []byte("secret payload")
	f, err := uploads.NewFile("private-name.txt", "text/plain", payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	reader, err := f.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "secret payload" {
		t.Fatal("content alias", err)
	}
	_ = reader.Close()
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		for _, value := range []any{f, reader, *reader, &uploads.Error{Code: "read_failed", Cause: errors.New("private-name.txt")}, uploads.Error{Code: "read_failed", Cause: errors.New("private-name.txt")}} {
			if strings.Contains(fmt.Sprintf(verb, value), "private-name") || strings.Contains(fmt.Sprintf(verb, value), "secret payload") {
				t.Fatal("format disclosed payload")
			}
		}
	}
	c := config(t)
	input, typ := body(t, part{name: "title", content: "private-text"}, part{name: "f", filename: "private-name.txt", content: "123456789", file: true})
	form, err := uploads.Parse(t.Context(), bytes.NewReader(input), typ, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := form.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		for _, value := range []any{form, *form} {
			if strings.Contains(fmt.Sprintf(verb, value), "private-text") {
				t.Fatal("format disclosed parsed input")
			}
		}
	}
	file := form.Files()["f"][0]
	cursor, err := file.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	copy := *cursor
	var first, second [1]byte
	if _, err := cursor.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := copy.Read(second[:]); err != nil || first[0] != '1' || second[0] != '2' {
		t.Fatal("copied reader lost cursor ownership", err)
	}
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cursor.Read(first[:]); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("copy reopened a closed cursor", err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 10 {
				r, err := file.Open(t.Context())
				if err != nil {
					if !errors.Is(err, &uploads.Error{Code: "closed"}) {
						t.Error(err)
					}
					return
				}
				_, err = io.Copy(io.Discard, r)
				if err != nil && !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Error(err)
				}
				if err = r.Close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Go(func() {
		if err := form.Close(); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	empty(t, c.TempDir)
}

type uploadTerminalFailure struct{ err error }

func (r uploadTerminalFailure) Read([]byte) (int, error) { return 0, r.err }

func TestMultipartExactWireBudgetPreservesReaderFailure(t *testing.T) {
	input, typ := body(t, part{name: "document", filename: "a.txt", content: "abcdef", file: true})
	policy := config(t)
	policy.MaxBodyBytes = int64(len(input))
	policy.MemoryBytes = 0
	sentinel := errors.New("transport failed after exact wire budget")
	parsed, err := uploads.Parse(t.Context(), io.MultiReader(bytes.NewReader(input), uploadTerminalFailure{sentinel}), typ, policy)
	if parsed != nil || !errors.Is(err, sentinel) || !errors.Is(err, &uploads.Error{Code: "read_failed"}) {
		t.Fatal("transport failure became malformed input", err)
	}
	empty(t, policy.TempDir)
}
