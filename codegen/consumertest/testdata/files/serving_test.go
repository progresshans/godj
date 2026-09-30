package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-files/models"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type servingStorage struct {
	storage.Backend
	saves, opens, stats, reads, seeks, closes, largestRead atomic.Int64
}

func (s *servingStorage) Save(ctx context.Context, name string, reader io.Reader, options storage.SaveOptions) (storage.Info, error) {
	s.saves.Add(1)
	return s.Backend.Save(ctx, name, reader, options)
}
func (s *servingStorage) Stat(ctx context.Context, name string) (storage.Info, error) {
	s.stats.Add(1)
	return s.Backend.Stat(ctx, name)
}
func (s *servingStorage) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	s.opens.Add(1)
	reader, err := s.Backend.Open(ctx, name)
	if err != nil {
		return reader, err
	}
	sized, ok := reader.(storage.SeekableReader)
	if !ok {
		_ = reader.Close()
		return nil, errors.New("storage lost opened-handle metadata or seeking")
	}
	return &servingReader{SeekableReader: sized, storage: s}, nil
}

type servingReader struct {
	storage.SeekableReader
	storage *servingStorage
}

func (r *servingReader) Read(p []byte) (int, error) {
	r.storage.reads.Add(1)
	for previous := r.storage.largestRead.Load(); int64(len(p)) > previous; previous = r.storage.largestRead.Load() {
		if r.storage.largestRead.CompareAndSwap(previous, int64(len(p))) {
			break
		}
	}
	return r.SeekableReader.Read(p)
}
func (r *servingReader) Seek(offset int64, whence int) (int64, error) {
	r.storage.seeks.Add(1)
	return r.SeekableReader.Seek(offset, whence)
}
func (r *servingReader) Close() error { r.storage.closes.Add(1); return r.SeekableReader.Close() }

// A generated model, real DB, cookie login, multipart publication and real HTTP
// response form one path. Admission uses a fresh model query for each request;
// storage names, URLs, query parameters and alias availability grant no access.
func runFileServing(t *testing.T, backend fileBackend, root storage.Backend, scope string) {
	t.Helper()
	ctx := t.Context()
	aliceOwner, bobOwner := scope+"-alice", scope+"-bob"
	titlePrefix, directory := scope+"-download-", scope+"/downloads/"
	files := &servingStorage{Backend: root}
	registry, err := storage.NewRegistry(storage.Registration{Alias: storage.DefaultAlias, Backend: files}, storage.Registration{Alias: "documents", Backend: files})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "file_serving", InstalledApps: []apps.Config{{Name: "files", Label: "files"}}, Storages: registry})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(ctx, "synthetic consumer password")
	if err != nil {
		t.Fatal(err)
	}
	view, add := auth.Permission("files.document.view"), auth.Permission("files.document.add")
	var credentials []auth.Credential
	for _, entry := range []struct {
		name        string
		permissions []auth.Permission
	}{{"alice", []auth.Permission{view, add}}, {"bob", []auth.Permission{view}}, {"denied", nil}} {
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: scope + "-" + entry.name, Active: true, Permissions: entry.permissions})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := auth.NewCredential(entry.name, encoded, principal)
		if err != nil {
			t.Fatal(err)
		}
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sessions.NewMemoryStore(128)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFHeader: "X-Test-CSRF", LoginPath: "/login/", FallbackPath: "/", AllowedNextPaths: []string{"/", "/login/", "/upload/"}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpecForFields((models.DocumentDescriptor{}).Metadata(), []string{"title", "file"})
	if err != nil {
		t.Fatal(err)
	}
	policy := uploads.DefaultConfig()
	policy.MemoryBytes = 0
	policy.TempDir = t.TempDir()
	loginGet := func(request *web.Request) (web.Response, error) {
		token, err := runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.NewResponse(200, nil, []byte(token.Value()))
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	}
	loginPost := func(request *web.Request) (web.Response, error) {
		if err := runtime.VerifyCSRF(request, nil); err != nil {
			return web.NewResponse(403, nil, nil)
		}
		change, err := runtime.Login(request, request.HTTP().Header.Get("X-Test-Username"), request.HTTP().Header.Get("X-Test-Password"))
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.NewResponse(204, nil, nil)
		if err != nil {
			return web.Response{}, err
		}
		return change.Apply(response)
	}
	upload := runtime.Require(add, func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if err := runtime.VerifyCSRF(request, nil); err != nil {
			return web.NewResponse(403, nil, nil)
		}
		parsed, err := request.Multipart(policy)
		if err != nil {
			return web.Response{}, err
		}
		bound, err := formmodel.BindInstance(request.Context(), models.DocumentObjects, spec, forms.NewDataWithFiles(parsed.Values(), parsed.Files()), nil, formmodel.PostClean{Fields: []string{"owner"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
			return forms.NewValues(map[string]forms.Value{"owner": forms.String(principal.ID())}), validation.Errors{}
		}})
		if err != nil {
			return web.Response{}, err
		}
		prepared, err := bound.Prepare()
		if err != nil {
			return web.NewResponse(400, nil, nil)
		}
		if len(prepared.PendingFiles()) != 1 {
			return web.NewResponse(400, nil, nil)
		}
		capability, err := request.Settings().Storages().Lookup("documents")
		if err != nil {
			return web.Response{}, err
		}
		stored, publications, err := prepared.SaveFiles(request.Context(), formmodel.FileSaver[models.Document]{Field: "file", Backend: capability, Name: func(_ models.Document, file uploads.File) (string, error) { return directory + file.Name(), nil }})
		if err != nil {
			return web.Response{}, err
		}
		if len(publications) != 1 || publications[0].Outcome() != storage.Published {
			return web.Response{}, errors.New("upload was not published")
		}
		value, err := stored.Model()
		if err != nil {
			return web.Response{}, err
		}
		if err := backend.AtomicRelation(request.Context(), func(tx db.RelationSession) error { return stored.Save(request.Context(), tx, &value) }); err != nil {
			return web.Response{}, err
		}
		location, err := request.ReverseWith("files:download", web.Int64Argument("id", value.ID))
		if err != nil {
			return web.Response{}, err
		}
		return web.NewResponse(201, http.Header{"Location": {location}}, []byte(strconv.FormatInt(value.ID, 10)))
	})
	download := runtime.Require(view, func(request *web.Request, principal auth.Principal) (web.Response, error) {
		id, ok := request.Int64Parameter("id")
		if !ok {
			return web.Response{}, errors.New("missing typed route key")
		}
		rows, err := models.DocumentObjects.Using(backend).Filter(models.DocumentFields.ID.Exact(id), models.DocumentFields.Owner.Exact(principal.ID())).All(request.Context())
		if err != nil {
			return web.Response{}, err
		}
		if len(rows) != 1 || rows[0].File == "" {
			return web.NewResponse(404, nil, nil)
		}
		capability, err := request.Settings().Storages().Lookup("documents")
		if err != nil {
			return web.Response{}, err
		}
		return web.FileResponse(capability, rows[0].File, web.FileOptions{})
	})
	application, err := web.NewApplication(web.Config{Settings: configured, MaxResponseBytes: 256, MaxStreamBytes: 2 << 20, Routes: []web.Route{
		{Name: "files:login", Method: "GET", Path: "/login/", Handler: loginGet},
		{Name: "files:login_post", Method: "POST", Path: "/login/", Handler: loginPost},
		{Name: "files:upload", Method: "POST", Path: "/upload/", Handler: upload},
		{Name: "files:download", Method: "GET", Path: "/documents/<int64:id>/", Handler: download},
		{Name: "files:download_head", Method: "HEAD", Path: "/documents/<int64:id>/", Handler: download},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Completion acknowledges reader cleanup, which can follow the client's last
	// Content-Length byte. Every request below is sequential and never redirects.
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { completed <- struct{}{} }()
		application.ServeHTTP(w, r)
	}))
	defer server.Close()
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	request := func(t *testing.T, client *http.Client, method, route string, body []byte, header http.Header) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+route, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if header != nil {
			req.Header = header.Clone()
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		select {
		case <-completed:
		case <-time.After(10 * time.Second):
			t.Fatal("HTTP request did not finish cleanup")
		}
		return response, payload
	}
	login := func(name string) (*http.Client, string) {
		t.Helper()
		client := newClient()
		issued, token := request(t, client, "GET", "/login/", nil, nil)
		if issued.StatusCode != 200 || len(token) == 0 {
			t.Fatal("CSRF token issuance failed")
		}
		result, _ := request(t, client, "POST", "/login/", nil, http.Header{"X-Test-CSRF": {string(token)}, "X-Test-Username": {name}, "X-Test-Password": {"synthetic consumer password"}})
		if result.StatusCode != 204 {
			t.Fatal("cookie login failed", result.StatusCode)
		}
		refreshed, fresh := request(t, client, "GET", "/login/", nil, nil)
		if refreshed.StatusCode != 200 || bytes.Equal(fresh, token) {
			t.Fatal("fresh login CSRF token unavailable")
		}
		return client, string(fresh)
	}
	alice, aliceCSRF := login("alice")
	bob, bobCSRF := login("bob")
	denied, _ := login("denied")
	anonymous := newClient()
	payload := bytes.Repeat([]byte("<html>private uploaded document</html>\x00"), 33000)
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	for key, value := range map[string]string{"title": titlePrefix + "alice", "owner": bobOwner} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", "private.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contentType := writer.FormDataContentType()
	for _, attempt := range []struct {
		client *http.Client
		token  string
	}{{alice, ""}, {bob, bobCSRF}} {
		response, _ := request(t, attempt.client, "POST", "/upload/", multipartBody.Bytes(), http.Header{"Content-Type": {contentType}, "X-Test-CSRF": {attempt.token}})
		if response.StatusCode != 403 || files.saves.Load() != 0 || files.opens.Load() != 0 {
			t.Fatal("failed admission touched storage", response.StatusCode)
		}
	}
	// A posted storage name cannot masquerade as an uploaded file.
	var forgedBody bytes.Buffer
	forgedWriter := multipart.NewWriter(&forgedBody)
	for key, value := range map[string]string{"title": "forged", "file": directory + "other.html"} {
		if err := forgedWriter.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := forgedWriter.Close(); err != nil {
		t.Fatal(err)
	}
	forged, _ := request(t, alice, "POST", "/upload/", forgedBody.Bytes(), http.Header{"Content-Type": {forgedWriter.FormDataContentType()}, "X-Test-CSRF": {aliceCSRF}})
	if forged.StatusCode != 400 || files.saves.Load() != 0 {
		t.Fatal("string reference accepted as upload", forged.StatusCode)
	}
	existing, err := root.Save(ctx, directory+"private.html", strings.NewReader("previous publication"), storage.SaveOptions{MaxLength: 40})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := request(t, alice, "POST", "/upload/", multipartBody.Bytes(), http.Header{"Content-Type": {contentType}, "X-Test-CSRF": {aliceCSRF}})
	if response.StatusCode != 201 || files.saves.Load() != 1 {
		t.Fatal("authenticated multipart publication failed", response.StatusCode)
	}
	location := response.Header.Get("Location")
	documents, err := models.DocumentObjects.Using(backend).Filter(models.DocumentFields.Title.Exact(titlePrefix + "alice")).All(ctx)
	if err != nil || len(documents) != 1 {
		t.Fatal("uploaded model missing", err)
	}
	document := documents[0]
	if document.Owner != aliceOwner || document.File == "" || document.File == existing.Name() || location != "/documents/"+strconv.FormatInt(document.ID, 10)+"/" {
		t.Fatal("posted owner or proposal replaced server state")
	}
	previous, err := root.Open(ctx, existing.Name())
	if err != nil {
		t.Fatal(err)
	}
	previousBody, readErr := io.ReadAll(previous)
	closeErr := previous.Close()
	if readErr != nil || closeErr != nil || string(previousBody) != "previous publication" {
		t.Fatal("upload collision overwrote existing file", readErr, closeErr)
	}
	if _, err := registry.URL(ctx, "documents", document.File); err == nil {
		t.Fatal("private alias became a public URL")
	}
	bobFile, err := root.Save(ctx, directory+"bob.txt", strings.NewReader("bob's private bytes"), storage.SaveOptions{MaxLength: 40})
	if err != nil {
		t.Fatal(err)
	}
	bobDocument, err := models.DocumentObjects.Create(ctx, backend, models.NewDocumentCreate(titlePrefix+"bob").WithFile(bobFile.Name()).WithOwner(bobOwner))
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		client *http.Client
		route  string
		status int
	}{{anonymous, location, 302}, {denied, location, 403}, {bob, location, 404}, {alice, "/documents/" + strconv.FormatInt(bobDocument.ID, 10) + "/", 404}, {alice, "/" + directory + "private.html", 404}} {
		response, _ := request(t, attempt.client, "GET", attempt.route, nil, http.Header{"If-None-Match": {"*"}, "Range": {"bytes=0-1"}})
		if response.StatusCode != attempt.status || files.opens.Load() != 0 {
			t.Fatal("download admission opened file", response.StatusCode, attempt.status, files.opens.Load())
		}
	}
	assertDownload := func(client *http.Client, method, route string) *http.Response {
		t.Helper()
		beforeOpen, beforeClose, beforeRead := files.opens.Load(), files.closes.Load(), files.reads.Load()
		response, body := request(t, client, method, route, nil, nil)
		disposition, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != path.Base(document.File) || response.StatusCode != 200 || response.ContentLength != int64(len(payload)) || response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("unsafe or incomplete file response", response.StatusCode, response.Header, err)
		}
		if files.opens.Load() != beforeOpen+1 || files.closes.Load() != beforeClose+1 || files.stats.Load() != 0 {
			t.Fatal("file response lost handle ownership or used racy Stat")
		}
		if method == "HEAD" {
			if len(body) != 0 || files.reads.Load() != beforeRead {
				t.Fatal("HEAD consumed content")
			}
		} else if !bytes.Equal(body, payload) || files.reads.Load() == beforeRead {
			t.Fatal("streamed upload changed bytes")
		}
		return response
	}
	downloaded := assertDownload(alice, "GET", location)
	assertDownload(alice, "HEAD", location)
	assertDownload(alice, "GET", location+"?alias=public&name="+url.QueryEscape(bobDocument.File))
	t.Run("ranges_and_conditionals", func(t *testing.T) {
		if downloaded.Header.Get("Accept-Ranges") != "bytes" || downloaded.Header.Get("Last-Modified") == "" || scope == "memory" && downloaded.Header.Get("ETag") == "" {
			t.Fatal("opened content validators or range capability unavailable")
		}
		partial, body := request(t, alice, "GET", location, nil, http.Header{"Range": {"bytes=2-17"}})
		if partial.StatusCode != 206 || partial.ContentLength != 16 || !bytes.Equal(body, payload[2:18]) || partial.Header.Get("Content-Range") != "bytes 2-17/"+strconv.Itoa(len(payload)) {
			t.Fatal("authorized range selected different bytes", partial.StatusCode)
		}
		partial, body = request(t, alice, "GET", location, nil, http.Header{"Range": {"bytes=0-7,-8"}})
		media, parameters, err := mime.ParseMediaType(partial.Header.Get("Content-Type"))
		if err != nil || partial.StatusCode != 206 || media != "multipart/byteranges" || partial.ContentLength != int64(len(body)) || partial.Header.Get("Content-Range") != "" {
			t.Fatal("authorized multipart framing", partial.StatusCode, err)
		}
		parts := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
		for _, expected := range [][]byte{payload[:8], payload[len(payload)-8:]} {
			part, err := parts.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			selected, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(selected, expected) || part.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatal("authorized multipart content", err)
			}
		}
		if _, err := parts.NextPart(); err != io.EOF {
			t.Fatal("multipart did not end exactly", err)
		}
		for _, attempt := range []struct {
			method string
			header http.Header
			status int
		}{
			{"GET", http.Header{"If-None-Match": {"*"}, "Range": {"bytes=999999999999-"}}, 304},
			{"GET", http.Header{"If-Modified-Since": {downloaded.Header.Get("Last-Modified")}}, 304},
			{"GET", http.Header{"If-Match": {`"different-generation"`}}, 412},
			{"GET", http.Header{"Range": {"bytes=" + strconv.Itoa(len(payload)) + "-"}}, 416},
			{"HEAD", http.Header{"Range": {"bytes=2-17"}}, 200},
		} {
			beforeOpen, beforeClose, beforeRead, beforeSeek := files.opens.Load(), files.closes.Load(), files.reads.Load(), files.seeks.Load()
			result, body := request(t, alice, attempt.method, location, nil, attempt.header)
			if result.StatusCode != attempt.status || len(body) != 0 || files.opens.Load() != beforeOpen+1 || files.closes.Load() != beforeClose+1 || files.reads.Load() != beforeRead || files.seeks.Load() != beforeSeek || result.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("bodyless selection consumed content or lost admission", result.StatusCode)
			}
			if attempt.status == 304 && result.Header.Get("Content-Length") != "" || attempt.method == "HEAD" && result.ContentLength != int64(len(payload)) {
				t.Fatal("conditional/HEAD length changed")
			}
		}
		if etag := downloaded.Header.Get("ETag"); etag != "" {
			result, body := request(t, alice, "GET", location, nil, http.Header{"If-Range": {etag}, "Range": {"bytes=2-17"}})
			if result.StatusCode != 206 || !bytes.Equal(body, payload[2:18]) {
				t.Fatal("strong version did not resume the authorized file")
			}
			before := files.reads.Load()
			result, body = request(t, alice, "GET", location, nil, http.Header{"If-None-Match": {"W/" + etag + ", \t"}})
			if result.StatusCode != 304 || len(body) != 0 || files.reads.Load() != before {
				t.Fatal("weak comparison did not reuse the same byte identity")
			}
		}
	})
	// Model ownership is resolved anew; an earlier successful download is not a
	// capability to read the file after it has moved to another owner.
	document.Owner = bobOwner
	if err := models.DocumentObjects.Save(ctx, backend, &document, models.DocumentUpdateFields(models.DocumentFields.Owner)); err != nil {
		t.Fatal(err)
	}
	beforeOpen := files.opens.Load()
	revoked, _ := request(t, alice, "GET", location, nil, http.Header{"If-None-Match": {"*"}, "Range": {"bytes=0-1"}})
	if revoked.StatusCode != 404 || files.opens.Load() != beforeOpen {
		t.Fatal("stale model ownership authorized download")
	}
	assertDownload(bob, "GET", location)
	if err := root.Delete(ctx, document.File); err != nil {
		t.Fatal(err)
	}
	beforeClose := files.closes.Load()
	missing, missingBody := request(t, bob, "GET", location, nil, http.Header{"If-None-Match": {"*"}, "Range": {"bytes=0-1"}})
	if missing.StatusCode != 404 || strings.Contains(string(missingBody), document.File) || files.closes.Load() != beforeClose {
		t.Fatal("missing authorized file leaked or closed nonexistent handle")
	}
	if entries, err := os.ReadDir(policy.TempDir); err != nil || len(entries) != 0 {
		t.Fatal("multipart staging leaked", err)
	}
	if files.largestRead.Load() > 32<<10 || files.opens.Load() != files.closes.Load()+1 {
		t.Fatal("stream was buffered or an opened reader leaked")
	}
}
