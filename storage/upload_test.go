package storage_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/web"
)

// This isolated route verifies transport-to-storage ownership. Production
// routes still own authorization/CSRF and the separate DB reference commit.
func TestUploadPersistsBeyondHTTPRequestAndBackendReopen(t *testing.T) {
	root := t.TempDir()
	backend := openStorage(t, root, storage.FilesystemConfig{})
	field, err := forms.FileField("document")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	config := uploads.DefaultConfig()
	config.MemoryBytes = 0
	config.TempDir = t.TempDir()
	configured, err := settings.New(settings.Definition{ProjectName: "storage_upload", InstalledApps: []apps.Config{{Name: "storage_upload", Label: "files"}}})
	if err != nil {
		t.Fatal(err)
	}
	var incoming uploads.File
	var saved storage.Info
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "files:receive", Method: http.MethodPost, Path: "/files/", Handler: func(request *web.Request) (web.Response, error) {
		parsed, err := request.Multipart(config)
		if err != nil {
			return web.Response{}, err
		}
		bound, err := spec.Bind(request.Context(), forms.NewDataWithFiles(parsed.Values(), parsed.Files()), nil)
		if err != nil {
			return web.Response{}, err
		}
		if !bound.Valid() {
			return web.NewResponse(400, nil, []byte("invalid"))
		}
		value, _ := bound.Cleaned().File("document")
		incoming, _ = value.Upload()
		saved, err = storage.SaveUpload(request.Context(), backend, "documents/"+incoming.Name(), incoming, storage.SaveOptions{MaxLength: 100})
		if err != nil {
			return web.Response{}, err
		}
		return web.NewResponse(201, nil, []byte(saved.Name()))
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("document", "report.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(part, "binary\x00payload"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/files/", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 201 || response.Body.String() != saved.Name() {
		t.Fatal("upload not stored", response.Code, response.Body.String())
	}
	if _, err = incoming.Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("request upload still live", err)
	}
	if entries, err := os.ReadDir(config.TempDir); err != nil || len(entries) != 0 {
		t.Fatal("request staging leaked", err)
	}
	if err = backend.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStorage(t, root, storage.FilesystemConfig{})
	if readStorage(t, reopened, saved.Name()) != "binary\x00payload" {
		t.Fatal("persistent content lost on request release")
	}
	stageEmpty(t, root)
}
