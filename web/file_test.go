package web_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/web"
)

type fileBackendProbe struct {
	storage.Backend
	open         func(context.Context, string) (io.ReadCloser, error)
	opens, stats int
}

func (b *fileBackendProbe) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	b.opens++
	return b.open(ctx, name)
}
func (b *fileBackendProbe) Stat(context.Context, string) (storage.Info, error) {
	b.stats++
	return storage.Info{}, errors.New("separate Stat is forbidden")
}

type openedFileProbe struct {
	*streamProbe
	info      storage.Info
	infoPanic bool
}

func (p *openedFileProbe) Info() storage.Info {
	if p.infoPanic {
		panic("private metadata panic")
	}
	return p.info
}

func TestFileResponsesAgainstPinnedDjango(t *testing.T) {
	raw, err := os.ReadFile("../storage/testdata/serving-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python string
		Files          []struct {
			Name, Filename  string
			Inline          bool
			ContentType     string `json:"content_type"`
			Disposition     string
			HeaderFilename  string `json:"header_filename"`
			Length          *string
			ReadsBefore     int    `json:"reads_before"`
			ContentError    string `json:"content_error"`
			Hex             string `json:"payload_hex"`
			OpenedAfterRead bool   `json:"opened_after_read"`
			Closes          int
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Files) != 6 {
		t.Fatal("native file fixture", err)
	}
	for _, native := range reference.Files {
		t.Run(native.Name, func(t *testing.T) {
			payload, err := hex.DecodeString(native.Hex)
			if err != nil {
				t.Fatal(err)
			}
			probe := &streamProbe{reader: bytes.NewReader(payload)}
			if native.Name == "offset" {
				reader := bytes.NewReader(append([]byte("prefix-"), payload...))
				if _, err := reader.Seek(7, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				probe.reader = reader
			}
			backend := &fileBackendProbe{open: func(ctx context.Context, name string) (io.ReadCloser, error) {
				if ctx.Err() != nil || name != "objects/stored.bin" {
					return nil, errors.New("lost context/name")
				}
				if native.Length == nil {
					return probe, nil
				}
				info, err := storage.NewInfo(name, int64(len(payload)))
				if err != nil {
					return nil, err
				}
				return &openedFileProbe{streamProbe: probe, info: info}, nil
			}}
			response, err := web.FileResponse(backend, "objects/stored.bin", web.FileOptions{Filename: native.Filename, ContentType: native.ContentType, Inline: native.Inline})
			if err != nil {
				t.Fatal(err)
			}
			if backend.opens != 0 || backend.stats != 0 || probe.reads.Load() != 0 || native.ReadsBefore != 0 {
				t.Fatal("file description performed I/O")
			}
			if _, err := response.Body(); !errors.Is(err, &web.Error{Code: web.CodeBodyNotBuffered}) || native.ContentError != "AttributeError" {
				t.Fatal("file exposed buffered body", err)
			}
			app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:file", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }}}})
			recorder := httptest.NewRecorder()
			app.ServeHTTP(recorder, httptest.NewRequest("GET", "http://example.test/", nil))
			disposition, parameters, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
			if err != nil {
				t.Fatal(err)
			}
			wantLength := ""
			if native.Length != nil {
				wantLength = *native.Length
			}
			if recorder.Code != 200 || !bytes.Equal(recorder.Body.Bytes(), payload) || recorder.Header().Get("Content-Length") != wantLength || recorder.Header().Get("Content-Type") != native.ContentType || disposition != native.Disposition || parameters["filename"] != native.HeaderFilename {
				t.Fatal("native file response differs", recorder.Code, recorder.Header())
			}
			if backend.opens != 1 || backend.stats != 0 || probe.closes.Load() != int64(native.Closes) || !native.OpenedAfterRead {
				t.Fatal("owned file lifetime", backend.opens, backend.stats, probe.closes.Load())
			}
		})
	}
}

func TestFileResponseUsesSafeDefaultsAndOpenedHandleMetadata(t *testing.T) {
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	content := "<script>private content</script>"
	if _, err := root.Save(t.Context(), "private/page.html", strings.NewReader(content), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	backend := &fileBackendProbe{open: root.Open}
	response, err := web.FileResponse(backend, "private/page.html", web.FileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if backend.opens != 0 {
		t.Fatal("file opened before middleware")
	}
	handler := func(*web.Request) (web.Response, error) { return response, nil }
	app := newTestApplication(t, web.Config{MaxResponseBytes: 1, Routes: []web.Route{{Name: "articles:get", Method: "GET", Path: "/", Handler: handler}, {Name: "articles:head", Method: "HEAD", Path: "/", Handler: handler}}})
	for _, method := range []string{"GET", "HEAD"} {
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(method, "http://example.test/", nil))
		disposition, parameters, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
		if err != nil {
			t.Fatal(err)
		}
		if recorder.Code != 200 || recorder.Header().Get("Content-Type") != "application/octet-stream" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Cache-Control") != "no-store" || disposition != "attachment" || parameters["filename"] != "page.html" {
			t.Fatal("default download policy", recorder.Code, recorder.Header())
		}
		if method == "GET" && recorder.Body.String() != content || method == "HEAD" && recorder.Body.Len() != 0 {
			t.Fatal("GET/HEAD payload changed")
		}
	}
	if backend.opens != 2 || backend.stats != 0 {
		t.Fatal("file did a racy Stat/Open")
	}
}

func TestFileResponseRejectsConfigurationAndInvalidOpenedMetadata(t *testing.T) {
	never := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("unexpected") }}
	for _, name := range []string{"", "../escape", "/absolute", "nested\\file", ".godj-staging/private"} {
		if _, err := web.FileResponse(never, name, web.FileOptions{}); err == nil {
			t.Fatal("invalid stored name accepted", name)
		}
	}
	for _, backend := range []storage.Backend{nil, (*fileBackendProbe)(nil)} {
		if _, err := web.FileResponse(backend, "valid.txt", web.FileOptions{}); err == nil {
			t.Fatal("nil storage accepted")
		}
	}
	for _, options := range []web.FileOptions{{Filename: "../escape"}, {Filename: "folder\\file"}, {Filename: "."}, {Filename: "line\r\ninjected"}, {Filename: string([]byte{0xff})}, {Filename: strings.Repeat("x", 1025)}, {ContentType: "invalid"}, {ContentType: "text/plain\r\ninjected"}} {
		if _, err := web.FileResponse(never, "valid.txt", options); err == nil {
			t.Fatal("invalid file representation accepted")
		}
	}
	if never.opens != 0 || never.stats != 0 {
		t.Fatal("invalid configuration touched backend")
	}
	for _, mode := range []string{"zero_info", "wrong_name", "metadata_panic", "missing", "backend_failure", "error_with_reader"} {
		t.Run(mode, func(t *testing.T) {
			probe := &streamProbe{reader: strings.NewReader("private")}
			info := storage.Info{}
			if mode == "wrong_name" {
				info, _ = storage.NewInfo("different.txt", 7)
			}
			backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) {
				switch mode {
				case "missing":
					return nil, fs.ErrNotExist
				case "backend_failure":
					return nil, errors.New("private backend details")
				case "error_with_reader":
					return probe, fs.ErrNotExist
				}
				return &openedFileProbe{streamProbe: probe, info: info, infoPanic: mode == "metadata_panic"}, nil
			}}
			response, err := web.FileResponse(backend, "valid.txt", web.FileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:file", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }}}})
			recorder := httptest.NewRecorder()
			app.ServeHTTP(recorder, httptest.NewRequest("GET", "http://example.test/", nil))
			wantStatus, wantClose := 500, int64(1)
			if mode == "missing" {
				wantStatus, wantClose = 404, 0
			}
			if mode == "backend_failure" {
				wantClose = 0
			}
			if recorder.Code != wantStatus || probe.closes.Load() != wantClose || probe.reads.Load() != 0 || strings.Contains(recorder.Body.String(), "private") {
				t.Fatal("invalid/missing file escaped pre-header boundary", recorder.Code, probe.closes.Load())
			}
		})
	}
}
