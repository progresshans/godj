package web_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/web"
)

type seekFileProbe struct {
	*openedFileProbe
	seeker io.Seeker
	seek   func(int64, int) (int64, error)
	seeks  int
}

func (p *seekFileProbe) Seek(offset int64, whence int) (int64, error) {
	p.seeks++
	if p.seek != nil {
		return p.seek(offset, whence)
	}
	return p.seeker.Seek(offset, whence)
}

func fileHTTPInfo(t *testing.T, size int64, metadata storage.ContentMetadata) storage.Info {
	t.Helper()
	info, err := storage.NewInfo("file.bin", size)
	if err == nil {
		info, err = info.WithContentMetadata(metadata)
	}
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func fileHTTPApplication(t *testing.T, backend storage.Backend, limit int64, middleware ...web.Middleware) (*web.Application, web.Response) {
	t.Helper()
	response, err := web.FileResponse(backend, "file.bin", web.FileOptions{ContentType: "text/plain; charset=utf-8"})
	if err != nil {
		t.Fatal(err)
	}
	header := response.Header()
	header.Set("Set-Cookie", "file-test=preserved")
	response, err = response.WithHeaders(header)
	if err != nil {
		t.Fatal(err)
	}
	var routes []web.Route
	for _, method := range []string{"GET", "HEAD", "POST"} {
		routes = append(routes, web.Route{Name: "articles:" + strings.ToLower(method), Method: method, Path: "/", Handler: func(*web.Request) (web.Response, error) { return response, nil }})
	}
	return newTestApplication(t, web.Config{MaxStreamBytes: limit, Routes: routes, Middleware: middleware}), response
}

func fileHTTPRequest(t *testing.T, application http.Handler, method string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://example.test/", nil)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	if failure := streamCall(application, recorder, request); failure != nil {
		t.Fatal("unexpected transport abort", failure)
	}
	return recorder
}

func TestFileHTTPPreconditionsUseOpenedMetadataAndPrecedence(t *testing.T) {
	modified := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	info := fileHTTPInfo(t, 10, storage.ContentMetadata{Version: "revision-one", Modified: modified, ModifiedStrong: true})
	var probe *seekFileProbe
	backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) {
		content := strings.NewReader("0123456789")
		probe = &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: info}, seeker: content}
		return probe, nil
	}}
	application, description := fileHTTPApplication(t, backend, 0)
	initial := fileHTTPRequest(t, application, "GET", nil)
	etag := initial.Header().Get("ETag")
	if initial.Code != 200 || etag == "" || strings.HasPrefix(etag, "W/") || initial.Header().Get("Last-Modified") != modified.Format(http.TimeFormat) {
		t.Fatal("opened validators unavailable", initial.Code, initial.Header())
	}
	old, fresh := modified.Add(-time.Hour).Format(http.TimeFormat), modified.Add(time.Hour).Format(http.TimeFormat)
	for _, item := range []struct {
		name, method string
		header       http.Header
		status       int
		body         string
	}{
		{"none_match", "GET", http.Header{"If-None-Match": {etag}}, 304, ""},
		{"weak_none_match", "GET", http.Header{"If-None-Match": {"W/" + etag}}, 304, ""},
		{"list_none_match", "GET", http.Header{"If-None-Match": {`"other"`, ", W/" + etag + ", \t"}}, 304, ""},
		{"wildcard_none_match", "GET", http.Header{"If-None-Match": {"*"}}, 304, ""},
		{"none_match_precedes_modified", "GET", http.Header{"If-None-Match": {`"other"`}, "If-Modified-Since": {fresh}}, 200, "0123456789"},
		{"match", "GET", http.Header{"If-Match": {etag}}, 200, "0123456789"},
		{"weak_match_rejected", "GET", http.Header{"If-Match": {"W/" + etag}}, 412, ""},
		{"mismatch_precedes_none_match", "GET", http.Header{"If-Match": {`"other"`}, "If-None-Match": {etag}}, 412, ""},
		{"match_precedes_unmodified", "GET", http.Header{"If-Match": {etag}, "If-Unmodified-Since": {old}}, 200, "0123456789"},
		{"wildcard_match", "GET", http.Header{"If-Match": {"*"}, "If-Unmodified-Since": {old}}, 200, "0123456789"},
		{"unmodified_fails", "GET", http.Header{"If-Unmodified-Since": {old}}, 412, ""},
		{"unmodified_passes", "GET", http.Header{"If-Unmodified-Since": {fresh}}, 200, "0123456789"},
		{"modified_same", "GET", http.Header{"If-Modified-Since": {modified.Format(http.TimeFormat)}}, 304, ""},
		{"modified_future", "GET", http.Header{"If-Modified-Since": {fresh}}, 304, ""},
		{"modified_older", "GET", http.Header{"If-Modified-Since": {old}}, 200, "0123456789"},
		{"invalid_date_ignored", "GET", http.Header{"If-Modified-Since": {"invalid date"}}, 200, "0123456789"},
		{"condition_precedes_bad_range", "GET", http.Header{"If-None-Match": {etag}, "Range": {"bytes=99-"}}, 304, ""},
		{"if_range_match", "GET", http.Header{"If-Range": {etag}, "Range": {"bytes=2-4"}}, 206, "234"},
		{"if_range_mismatch", "GET", http.Header{"If-Range": {`"other"`}, "Range": {"bytes=2-4"}}, 200, "0123456789"},
		{"if_range_weak", "GET", http.Header{"If-Range": {"W/" + etag}, "Range": {"bytes=2-4"}}, 200, "0123456789"},
		{"if_range_date", "GET", http.Header{"If-Range": {modified.Format(http.TimeFormat)}, "Range": {"bytes=2-4"}}, 206, "234"},
		{"if_range_date_mismatch", "GET", http.Header{"If-Range": {fresh}, "Range": {"bytes=2-4"}}, 200, "0123456789"},
		{"if_range_invalid", "GET", http.Header{"If-Range": {etag + `,"other"`}, "Range": {"bytes=2-4"}}, 200, "0123456789"},
		{"invalid_none_match", "GET", http.Header{"If-None-Match": {etag + ",invalid"}}, 400, ""},
		{"invalid_match", "GET", http.Header{"If-Match": {`*, "other"`}}, 400, ""},
		{"head_ignores_range", "HEAD", http.Header{"Range": {"bytes=2-4"}}, 200, ""},
		{"head_not_modified", "HEAD", http.Header{"If-None-Match": {etag}}, 304, ""},
		{"unsafe_method_not_guarded_after_handler", "POST", http.Header{"If-Match": {`"other"`}, "If-None-Match": {"*"}, "Range": {"bytes=2-4"}}, 200, "0123456789"},
		{"bounded_headers", "GET", http.Header{"If-None-Match": {strings.Repeat("x", (8<<10)+1)}}, 431, ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			result := fileHTTPRequest(t, application, item.method, item.header)
			if result.Code != item.status || result.Body.String() != item.body {
				t.Fatal("conditional selection", result.Code, result.Body.String())
			}
			if probe.closes.Load() != 1 || backend.stats != 0 || result.Header().Get("Set-Cookie") != "file-test=preserved" || result.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("selection lost reader ownership or response policy")
			}
			if item.status != 200 && item.status != 206 || item.method == "HEAD" {
				if probe.reads.Load() != 0 || probe.seeks != 0 {
					t.Fatal("bodyless result read or sought content")
				}
			}
			if item.status == 304 && (result.Header().Get("Content-Length") != "" || result.Header().Get("Content-Type") != "" || result.Header().Get("ETag") != etag) {
				t.Fatal("304 framing or validator changed")
			}
			if item.method == "HEAD" && item.status == 200 && result.Header().Get("Content-Length") != "10" {
				t.Fatal("HEAD used a partial length")
			}
		})
	}
	if description.Status() != 200 || description.Header().Get("ETag") != "" || description.Header().Get("Content-Range") != "" {
		t.Fatal("request selection mutated a reusable response")
	}
}

func TestFileHTTPRangesMatchIndependentByteSelections(t *testing.T) {
	for _, item := range []struct {
		name, rangeHeader, body, contentRange string
		status                                int
		compareStandard                       bool
	}{
		{"middle", "bytes=2-4", "234", "bytes 2-4/10", 206, true},
		{"suffix", "bytes=-3", "789", "bytes 7-9/10", 206, true},
		{"open_ended", "bytes=7-", "789", "bytes 7-9/10", 206, true},
		{"clamped", "bytes=8-999", "89", "bytes 8-9/10", 206, true},
		{"oversized_suffix", "bytes=-999", "0123456789", "bytes 0-9/10", 206, true},
		{"mixed_unsatisfiable", "bytes=99-100,1-2", "12", "bytes 1-2/10", 206, true},
		{"one_byte", "bytes=0-0", "0", "bytes 0-0/10", 206, true},
		{"leading_zero", "bytes=0001-0002", "12", "bytes 1-2/10", 206, true},
		{"large_end", "bytes=8-999999999999999999999999999", "89", "bytes 8-9/10", 206, false},
		{"large_suffix", "bytes=-999999999999999999999999999", "0123456789", "bytes 0-9/10", 206, false},
		{"case_insensitive_unit", "BYTES=2-3", "23", "bytes 2-3/10", 206, false},
		{"unknown_unit", "items=0-1", "0123456789", "", 200, false},
		{"non_ascii_unit", "byteſ=0-1", "0123456789", "", 200, false},
		{"amplification", "bytes=0-8,1-9", "0123456789", "", 200, true},
		{"no_overlap", "bytes=10-", "", "bytes */10", 416, true},
		{"huge_start", "bytes=999999999999999999999999-", "", "bytes */10", 416, false},
		{"zero_suffix", "bytes=-0", "", "bytes */10", 416, false},
		{"reversed", "bytes=7-2", "", "bytes */10", 416, false},
		{"huge_reversed", "bytes=9999999999999999999999999-999999999999999999999999", "", "bytes */10", 416, false},
		{"negative", "bytes=-1-2", "", "bytes */10", 416, false},
		{"positive_sign", "bytes=+1-2", "", "bytes */10", 416, false},
		{"missing_dash", "bytes=12", "", "bytes */10", 416, false},
		{"empty_set", "bytes=, ,", "", "bytes */10", 416, false},
		{"bounded_count", "bytes=" + strings.Repeat("0-0,", 16) + "0-0", "", "bytes */10", 416, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			content := strings.NewReader("0123456789")
			probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, 10, storage.ContentMetadata{})}, seeker: content}
			backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return probe, nil }}
			application, _ := fileHTTPApplication(t, backend, 0)
			result := fileHTTPRequest(t, application, "GET", http.Header{"Range": {item.rangeHeader}})
			if result.Code != item.status || result.Body.String() != item.body || result.Header().Get("Content-Range") != item.contentRange || result.Header().Get("Content-Length") != strconv.Itoa(len(item.body)) {
				t.Fatal("range selection", result.Code, result.Header(), result.Body.String())
			}
			if backend.stats != 0 || probe.closes.Load() != 1 || probe.maxRead.Load() > 32<<10 || result.Header().Get("Accept-Ranges") != "bytes" {
				t.Fatal("range escaped its opened content/transfer bounds")
			}
			if item.status == 416 && (probe.reads.Load() != 0 || probe.seeks != 0) {
				t.Fatal("rejected ranges consumed content")
			}
			if item.compareStandard {
				request := httptest.NewRequest("GET", "http://example.test/", nil)
				request.Header.Set("Range", item.rangeHeader)
				native := httptest.NewRecorder()
				native.Header().Set("Content-Type", "text/plain; charset=utf-8")
				http.ServeContent(native, request, "file.bin", time.Time{}, strings.NewReader("0123456789"))
				if native.Code != result.Code || native.Header().Get("Content-Range") != result.Header().Get("Content-Range") || item.status != 416 && native.Body.String() != result.Body.String() {
					t.Fatal("standard library range result differs", native.Code, native.Header())
				}
			}
		})
	}
}

func TestFileHTTPMultipartRangesKeepOrderBoundReadsAndCountFraming(t *testing.T) {
	payload := strings.Repeat("0123456789", 14000)
	content := strings.NewReader(payload)
	probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, int64(len(payload)), storage.ContentMetadata{})}, seeker: content}
	backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return probe, nil }}
	application, _ := fileHTTPApplication(t, backend, 0)
	result := fileHTTPRequest(t, application, "GET", http.Header{"Range": {"bytes=90000-139999, 0-39999"}})
	media, params, err := mime.ParseMediaType(result.Header().Get("Content-Type"))
	if err != nil || result.Code != 206 || media != "multipart/byteranges" || params["boundary"] == "" || result.Header().Get("Content-Range") != "" || result.Header().Get("Content-Length") != strconv.Itoa(result.Body.Len()) {
		t.Fatal("multipart framing", result.Code, result.Header(), err)
	}
	parts := multipart.NewReader(bytes.NewReader(result.Body.Bytes()), params["boundary"])
	for _, item := range []struct{ start, end int }{{90000, 139999}, {0, 39999}} {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil || string(body) != payload[item.start:item.end+1] || part.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", item.start, item.end, len(payload)) || part.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatal("multipart selected a different sequence", err)
		}
	}
	if _, err := parts.NextPart(); err != io.EOF || probe.seeks != 2 || probe.closes.Load() != 1 || probe.maxRead.Load() > 32<<10 || backend.stats != 0 {
		t.Fatal("multipart length, cursor or cleanup changed", err, probe.seeks, probe.closes.Load())
	}
}

func TestFileHTTPUnknownValidatorsCapabilitiesEmptyFilesAndTransferLimits(t *testing.T) {
	for _, mode := range []string{"unknown", "weak_date", "empty", "small_slice", "multipart_limit", "future_date", "backend_failure", "version_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			payload := "0123456789"
			if mode == "empty" {
				payload = ""
			}
			modified := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
			metadata := storage.ContentMetadata{}
			if mode == "weak_date" {
				metadata.Modified = modified
			}
			if mode == "future_date" {
				metadata.Modified, metadata.ModifiedStrong = time.Now().Add(24*time.Hour), true
			}
			content := strings.NewReader(payload)
			probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, int64(len(payload)), metadata)}, seeker: content}
			backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) {
				if mode == "backend_failure" {
					return nil, &storage.Error{Code: "not_found", Cause: errors.ErrUnsupported}
				}
				if mode == "unknown" {
					return probe.streamProbe, nil
				}
				return probe, nil
			}}
			limit := int64(0)
			header := http.Header{"Range": {"bytes=2-4"}}
			wantStatus, wantBody := 200, payload
			switch mode {
			case "weak_date":
				header.Set("If-Range", modified.Format(http.TimeFormat))
			case "small_slice":
				limit, wantStatus, wantBody = 3, 206, "234"
			case "multipart_limit":
				limit, wantStatus, wantBody = 10, 500, "Internal Server Error\n"
				header.Set("Range", "bytes=0-0,9-9")
			case "future_date":
				header.Set("If-Range", metadata.Modified.UTC().Format(http.TimeFormat))
			case "backend_failure":
				header.Set("If-None-Match", "*")
				wantStatus, wantBody = 500, "Internal Server Error\n"
			case "version_unavailable":
				header.Set("If-Match", `"unknown"`)
				wantStatus, wantBody = 412, ""
			}
			application, _ := fileHTTPApplication(t, backend, limit)
			result := fileHTTPRequest(t, application, "GET", header)
			if result.Code != wantStatus || result.Body.String() != wantBody {
				t.Fatal("capability/limit selection", result.Code, result.Body.String())
			}
			if mode != "backend_failure" && probe.closes.Load() != 1 || backend.stats != 0 {
				t.Fatal("opened resource escaped cleanup")
			}
			if mode == "unknown" && result.Header().Get("Accept-Ranges") != "none" {
				t.Fatal("unknown stream advertised range support")
			}
			if mode == "multipart_limit" && (probe.reads.Load() != 0 || probe.seeks != 0) {
				t.Fatal("framing limit applied after content consumption")
			}
			if mode == "future_date" {
				date, err := http.ParseTime(result.Header().Get("Last-Modified"))
				if err != nil || date.After(time.Now()) {
					t.Fatal("future modification date emitted", err)
				}
			}
		})
	}
}

func TestFileHTTPSeekAndPartialReadFailuresCloseAndAbortIncompleteBodies(t *testing.T) {
	for _, mode := range []string{"seek_error", "wrong_position", "seek_panic", "short", "joined_eof", "late_seek", "read_panic"} {
		t.Run(mode, func(t *testing.T) {
			content := strings.NewReader("0123456789")
			probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, 10, storage.ContentMetadata{})}, seeker: content}
			if strings.HasPrefix(mode, "seek_") || mode == "wrong_position" || mode == "late_seek" {
				probe.seek = func(offset int64, whence int) (int64, error) {
					if mode == "seek_panic" {
						panic("private seek details")
					}
					if mode == "wrong_position" {
						return offset + 1, nil
					}
					if mode == "seek_error" || mode == "late_seek" && probe.seeks > 1 {
						return 0, errors.New("private seek failure")
					}
					return content.Seek(offset, whence)
				}
			}
			if mode == "short" {
				probe.reader = strings.NewReader("short")
			}
			if mode == "joined_eof" || mode == "read_panic" {
				probe.read = func(buffer []byte) (int, error) {
					if mode == "read_panic" {
						panic("private read details")
					}
					return copy(buffer, "bad"), errors.Join(io.EOF, errors.New("private read failure"))
				}
			}
			backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return probe, nil }}
			application, _ := fileHTTPApplication(t, backend, 0)
			request := httptest.NewRequest("GET", "http://example.test/", nil)
			request.Header.Set("Range", "bytes=0-7")
			if mode == "late_seek" {
				request.Header.Set("Range", "bytes=0-1,8-9")
			}
			result := httptest.NewRecorder()
			failure := streamCall(application, result, request)
			wantAbort := mode == "short" || mode == "late_seek"
			if wantAbort {
				if failure != http.ErrAbortHandler || result.Code != 206 || strconv.Itoa(result.Body.Len()) == result.Header().Get("Content-Length") {
					t.Fatal("partial failure looked complete", result.Code, failure, result.Body.Len())
				}
			} else if failure != nil || result.Code != 500 {
				t.Fatal("pre-header failure escaped fixed response", result.Code, failure)
			}
			if probe.closes.Load() != 1 || strings.Contains(result.Body.String(), "private") {
				t.Fatal("range failure leaked resource or diagnostics")
			}
		})
	}
}

func TestFileHTTPRealClientObservesBodylessAndTruncatedResponses(t *testing.T) {
	backend := newFileHTTPMemory(t)
	if _, err := backend.Save(t.Context(), "file.bin", strings.NewReader("0123456789"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	application, _ := fileHTTPApplication(t, backend, 0)
	server := httptest.NewServer(application)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	request, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
	request.Header.Set("If-None-Match", "*")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != 304 || len(body) != 0 || readErr != nil || closeErr != nil || response.Header.Get("Content-Length") != "" {
		t.Fatal("wire 304 body/framing", response.StatusCode, readErr, closeErr)
	}
	truncated := strings.Repeat("x", 70<<10)
	content := strings.NewReader(truncated)
	probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, 100000, storage.ContentMetadata{})}, seeker: content}
	broken := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return probe, nil }}
	application, _ = fileHTTPApplication(t, broken, 0)
	completed := make(chan struct{})
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(completed)
		application.ServeHTTP(w, r)
	}))
	defer badServer.Close()
	request, _ = http.NewRequestWithContext(t.Context(), "GET", badServer.URL, nil)
	request.Header.Set("Range", "bytes=0-99998")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal("client did not receive the committed partial response", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 206 || response.ContentLength != 99999 || !errors.Is(err, io.ErrUnexpectedEOF) || string(body) != truncated {
		t.Fatal("real client accepted or obscured a truncated range", response.StatusCode, len(body), err)
	}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("aborted range did not finish cleanup")
	}
	if probe.closes.Load() != 1 {
		t.Fatal("wire abort retained opened content")
	}
}

func TestFileHTTPConditionalResultsAgainstPinnedDjango(t *testing.T) {
	raw, err := os.ReadFile("testdata/file-conditional-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Version, Etag string
		Modified                      int64
		Cases                         []struct {
			Name, Method string
			Headers      map[string]string
			Common       bool
			Status       int
			Hex          string `json:"body_hex"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Cases) != 22 {
		t.Fatal("conditional native reference", err)
	}
	common, deviations := 0, 0
	for _, item := range reference.Cases {
		t.Run(item.Name, func(t *testing.T) {
			metadata := storage.ContentMetadata{Version: reference.Version, Modified: time.Unix(reference.Modified, 0)}
			if item.Name == "native_unknown_etag_wildcard" {
				metadata.Version = ""
			}
			content := strings.NewReader("0123456789")
			probe := &seekFileProbe{openedFileProbe: &openedFileProbe{streamProbe: &streamProbe{reader: content}, info: fileHTTPInfo(t, 10, metadata)}, seeker: content}
			backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return probe, nil }}
			application, _ := fileHTTPApplication(t, backend, 0)
			header := make(http.Header)
			for name, value := range item.Headers {
				header.Set(name, value)
			}
			result := fileHTTPRequest(t, application, item.Method, header)
			if item.Common {
				common++
				body, err := hex.DecodeString(item.Hex)
				if err != nil || result.Code != item.Status || !bytes.Equal(result.Body.Bytes(), body) {
					t.Fatal("native condition differs", result.Code, item.Status, err)
				}
			} else {
				deviations++
				wantNative, wantGo := 200, 206
				switch item.Name {
				case "native_invalid_tag_suffix":
					wantNative, wantGo = 304, 400
				case "native_unknown_etag_wildcard":
					wantNative, wantGo = 200, 304
				case "native_range_unsupported":
				default:
					t.Fatal("unreviewed native difference")
				}
				if item.Status != wantNative || result.Code != wantGo {
					t.Fatal("explicit protocol difference changed", item.Status, result.Code)
				}
			}
			if probe.closes.Load() != 1 || backend.stats != 0 {
				t.Fatal("native comparison lost opened reader")
			}
		})
	}
	if common != 19 || deviations != 3 {
		t.Fatal("missing conditional observations", common, deviations)
	}
}

func TestFileHTTPAdmissionAndMissingFilesPrecedeConditionalResponses(t *testing.T) {
	backend := newFileHTTPMemory(t)
	if _, err := backend.Save(t.Context(), "file.bin", strings.NewReader("private"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	probe := &fileBackendProbe{open: backend.Open}
	deny := func(next web.Handler) web.Handler {
		return func(request *web.Request) (web.Response, error) {
			if _, err := next(request); err != nil {
				return web.Response{}, err
			}
			return web.NewResponse(403, nil, []byte("denied"))
		}
	}
	application, _ := fileHTTPApplication(t, probe, 0, deny)
	header := http.Header{"If-None-Match": {"*"}, "Range": {"bytes=0-1"}}
	result := fileHTTPRequest(t, application, "GET", header)
	if result.Code != 403 || probe.opens != 0 || result.Header().Get("ETag") != "" {
		t.Fatal("conditional response bypassed final admission")
	}
	if err := backend.Delete(t.Context(), "file.bin"); err != nil {
		t.Fatal(err)
	}
	application, _ = fileHTTPApplication(t, probe, 0)
	result = fileHTTPRequest(t, application, "GET", header)
	if result.Code != 404 || result.Header().Get("ETag") != "" {
		t.Fatal("conditional wildcard invented an absent representation", result.Code)
	}
}

func newFileHTTPMemory(t *testing.T) *storage.Memory {
	t.Helper()
	backend, err := storage.NewMemory(storage.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func TestFileHTTPNameReuseAndConcurrentResponseReuseDoNotShareValidatorsOrCursors(t *testing.T) {
	backend := newFileHTTPMemory(t)
	if _, err := backend.Save(t.Context(), "file.bin", strings.NewReader("old-bytes"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	application, response := fileHTTPApplication(t, backend, 0)
	first := fileHTTPRequest(t, application, "GET", nil)
	etag := first.Header().Get("ETag")
	if err := backend.Delete(t.Context(), "file.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Save(t.Context(), "file.bin", strings.NewReader("new-bytes"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, header := range []http.Header{{"If-None-Match": {etag}}, {"If-Range": {etag}, "Range": {"bytes=0-2"}}} {
		result := fileHTTPRequest(t, application, "GET", header)
		if result.Code != 200 || result.Body.String() != "new-bytes" || result.Header().Get("ETag") == etag {
			t.Fatal("cached metadata served a reused name", result.Code, result.Header())
		}
	}
	var failed atomic.Bool
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Go(func() {
			request := httptest.NewRequest("GET", "http://example.test/", nil)
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", i%9, i%9))
			result := httptest.NewRecorder()
			failure := streamCall(application, result, request)
			if failure != nil || result.Code != 206 || result.Body.String() != "new-bytes"[i%9:i%9+1] {
				failed.Store(true)
			}
		})
	}
	workers.Wait()
	if failed.Load() || response.Status() != 200 || response.Header().Get("ETag") != "" || response.Header().Get("Content-Range") != "" {
		t.Fatal("reusable file response shared mutable selection state")
	}
}

func TestFileHTTPRepresentationHeadersRemainBoundToOpenedContent(t *testing.T) {
	backend := &fileBackendProbe{open: func(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("unexpected I/O") }}
	response, err := web.FileResponse(backend, "file.bin", web.FileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ETag", "etag", "Last-Modified", "Accept-Ranges", "Content-Range", "Content-Encoding"} {
		header := response.Header()
		header[name] = []string{"forged"}
		if _, err := response.WithHeaders(header); err == nil {
			t.Fatal("headers forged file representation metadata", name)
		}
	}
	for _, header := range []http.Header{{"Content-Type": {"invalid"}}, {"Content-Type": {"text/plain"}, "content-type": {"application/json"}}} {
		if _, err := response.WithHeaders(header); err == nil {
			t.Fatal("ambiguous multipart media type accepted")
		}
	}
	if backend.opens != 0 {
		t.Fatal("header derivation opened content")
	}
}
