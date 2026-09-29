package web_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

func TestMultipartHTTPFormsetAndRequestLifetime(t *testing.T) {
	for _, mode := range []string{"success", "invalid_form", "handler_error", "panic", "limit"} {
		t.Run(mode, func(t *testing.T) {
			config := uploads.DefaultConfig()
			config.TempDir = t.TempDir()
			config.MemoryBytes = 1
			file, err := forms.FileField("document", forms.WithMaxLength(12))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{file})
			if err != nil {
				t.Fatal(err)
			}
			policy := forms.DefaultSetConfig()
			policy.Prefix = "attachments"
			policy.MaxForms = 2
			policy.AbsoluteMax = 3
			setSpec, err := forms.NewSetSpec(spec, policy)
			if err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			_ = writer.WriteField("attachments-TOTAL_FORMS", "2")
			_ = writer.WriteField("attachments-INITIAL_FORMS", "0")
			name := "report.txt"
			if mode == "invalid_form" {
				name = "filename-that-is-too-long.txt"
			}
			part, err := writer.CreateFormFile("attachments-0-document", name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(part, "actual binary content\x00\xff")
			_ = writer.Close()
			if mode == "limit" {
				config.MaxBodyBytes = int64(body.Len() - 1)
				config.MaxFileBytes = 32
				config.MemoryBytes = 1
				config.MaxValueBytes = 32
				config.MaxValueTotalBytes = 64
			}
			type observed struct {
				request *web.Request
				file    uploads.File
				reader  *uploads.Reader
				saved   bool
			}
			result := make(chan observed, 1)
			app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:upload", Method: "POST", Path: "/upload/", Handler: func(request *web.Request) (web.Response, error) {
				got := observed{request: request}
				defer func() { result <- got }()
				parsed, err := request.Multipart(config)
				if mode == "limit" {
					if !errors.Is(err, &uploads.Error{Code: "body_too_large"}) {
						t.Error(err)
					}
					return web.NewResponse(413, nil, []byte("too large"))
				}
				if err != nil {
					return web.Response{}, err
				}
				again, err := request.Multipart(config)
				if err != nil || again != parsed {
					t.Error("body parsed more than once", err)
				}
				changed := config
				changed.MemoryBytes++
				if _, err := request.Multipart(changed); !errors.Is(err, &uploads.Error{Code: "config_mismatch"}) {
					t.Error("changed parser budget", err)
				}
				got.file = parsed.Files()["attachments-0-document"][0]
				got.reader, err = got.file.Open(request.Context())
				if err != nil {
					return web.Response{}, err
				}
				if mode == "handler_error" {
					return web.Response{}, errors.New("fixture handler failure")
				}
				if mode == "panic" {
					panic("fixture handler panic")
				}
				set, err := setSpec.Bind(forms.NewDataWithFiles(parsed.Values(), parsed.Files()), nil)
				if err != nil {
					return web.Response{}, err
				}
				if !set.Valid() {
					return web.NewResponse(400, nil, []byte("invalid file"))
				}
				selected, err := set.ActiveForms()
				if err != nil {
					return web.Response{}, err
				}
				if len(selected) != 1 || len(selected[0].Form().Changed()) != 1 {
					t.Error("file-only extra row did not activate")
				}
				value, ok := selected[0].Form().Cleaned().File("document")
				if !ok {
					t.Error("file binding lost")
				}
				upload, ok := value.Upload()
				if !ok || !upload.Equal(got.file) {
					t.Error("parsed file replaced")
				}
				r, err := upload.Open(request.Context())
				if err != nil {
					return web.Response{}, err
				}
				defer r.Close()
				hash := sha256.New()
				if _, err = io.Copy(hash, r); err != nil {
					return web.Response{}, err
				}
				got.saved = true
				return web.NewResponse(200, nil, []byte(fmt.Sprintf("%x", hash.Sum(nil))))
			}}}})
			server := httptest.NewServer(app)
			defer server.Close()
			response, err := server.Client().Post(server.URL+"/upload/", writer.FormDataContentType(), &body)
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			got := <-result
			status := 200
			if mode == "invalid_form" {
				status = 400
			}
			if mode == "handler_error" || mode == "panic" {
				status = 500
			}
			if mode == "limit" {
				status = 413
			}
			if response.StatusCode != status || got.saved != (mode == "success") {
				t.Fatal("wrong terminal result", response.StatusCode, got.saved)
			}
			if mode == "success" && string(content) != fmt.Sprintf("%x", sha256.Sum256([]byte("actual binary content\x00\xff"))) {
				t.Fatal("payload was changed")
			}
			if entries, err := os.ReadDir(config.TempDir); err != nil || len(entries) != 0 {
				t.Fatal("request did not clean temporary files", entries, err)
			}
			if _, err := got.request.Multipart(config); !errors.Is(err, &web.Error{Code: web.CodeInvalidRequest}) {
				t.Fatal("retained request allowed parsing", err)
			}
			if got.file.Valid() {
				if _, err := got.file.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Fatal("retained upload opened", err)
				}
			}
			if got.reader != nil {
				if _, err := got.reader.Read(make([]byte, 1)); !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Fatal("retained cursor remained active", err)
				}
			}
		})
	}
}

func TestMultipartFailedReadIsNotRetried(t *testing.T) {
	reads := 0
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:upload", Method: "POST", Path: "/upload/", Handler: func(r *web.Request) (web.Response, error) {
		config := uploads.DefaultConfig()
		first, err := r.Multipart(config)
		if first != nil || err == nil {
			t.Error("malformed body accepted")
		}
		before := reads
		second, again := r.Multipart(config)
		if second != nil || again != err || reads != before {
			t.Error("failed parse consumed body twice")
		}
		return web.NewResponse(400, nil, nil)
	}}}})
	raw := httptest.NewRequest("POST", "/upload/", &multipartCountingReader{Reader: strings.NewReader("malformed"), reads: &reads})
	raw.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, raw)
	if response.Code != 400 || reads == 0 {
		t.Fatal("no actual malformed parse")
	}
}

type multipartCountingReader struct {
	io.Reader
	reads *int
}

func (r *multipartCountingReader) Read(p []byte) (int, error) { *r.reads++; return r.Reader.Read(p) }
