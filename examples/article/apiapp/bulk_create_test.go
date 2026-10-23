package apiapp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestArticleBulkHTTPValidatesEveryRowAndCommitsTheCompleteOrderedArray(t *testing.T) {
	for _, mode := range []string{"created", "forty", "invalid second", "missing second", "unknown second", "duplicate field", "duplicate slug", "duplicate empty slug", "invalid slug", "nul text", "empty", "too many", "object", "scalar item", "query", "media", "whole bytes", "compact item"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			token, csrf := h.csrf(t, h.allSession, apiapp.ListPath)
			body := `[{"title":"  first  ","summary":"summary","published":true},{"title":"second","summary":null}]`
			path, media, status := apiapp.BulkCreatePath, api.JSONContentType, 400
			switch mode {
			case "created":
				status = 201
				body = strings.Repeat(" ", 5000) + body
			case "forty", "too many":
				count := 40
				if mode == "too many" {
					count = 41
				} else {
					status = 201
				}
				rows := make([]string, count)
				for i := range rows {
					rows[i] = fmt.Sprintf(`{"title":"row %02d"}`, i)
				}
				body = "[" + strings.Join(rows, ",") + "]"
			case "invalid second":
				body = `[{"title":"first"},{"title":false}]`
			case "missing second":
				body = `[{"title":"first"},{}]`
			case "unknown second":
				body = `[{"title":"first"},{"title":"second","id":55}]`
			case "duplicate field":
				body = `[{"title":"first"},{"title":"second","title":"third"}]`
			case "duplicate slug":
				body = `[{"title":"first","slug":"same"},{"title":"second","slug":"same"}]`
			case "duplicate empty slug":
				body = `[{"title":"first","slug":""},{"title":"second","slug":""}]`
			case "invalid slug":
				body = `[{"title":"first"},{"title":"second","slug":"bad/slug"}]`
			case "nul text":
				body = `[{"title":"first"},{"title":"bad\u0000text"}]`
			case "empty":
				body = `[]`
			case "object":
				body = `{"title":"first"}`
			case "scalar item":
				body = `[{"title":"first"},true]`
			case "query":
				path += "?published=true"
			case "media":
				media = "text/plain"
				status = 415
			case "whole bytes":
				body = strings.Repeat(" ", 40*4096) + body
				status = 413
			case "compact item":
				body = `[{"title":"first"},{"title":"second"`
				for i := range 5 {
					body += fmt.Sprintf(`,"u%d":"%s"`, i, strings.Repeat("a", 1000))
				}
				body += `}]`
			}
			result := h.do(t, http.MethodPost, path, requestOptions{body: body, contentType: media, session: h.allSession, token: token, csrf: csrf})
			if result.status != status {
				t.Fatal("bulk response", result.status, result.body)
			}
			page, err := h.repository.List(context.Background(), articleapp.ListOptions{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if status != 201 {
				if page.Total != 0 {
					t.Fatal("invalid later row committed an earlier row", page)
				}
				if mode == "compact item" && (!strings.Contains(result.body, `"code":"limit_exceeded"`) || !strings.Contains(result.body, `"key":"index","value":"1"`)) {
					t.Fatal("compact item budget lost", result.body)
				}
				if mode == "nul text" && result.body != `{"code":"parse_error","errors":[]}` {
					t.Fatal("NUL changed the closed JSON parser's rejection", result.body)
				}
				if strings.Contains(mode, "second") || strings.HasPrefix(mode, "duplicate slug") || mode == "duplicate empty slug" || mode == "invalid slug" {
					if !strings.Contains(result.body, `"key":"index","value":"1"`) {
						t.Fatal("bulk rejection lost original row index", result.body)
					}
				}
				return
			}
			var rows []struct {
				ID            int64
				Title         string
				Published     bool
				Summary, Slug *string
			}
			if err := json.Unmarshal([]byte(result.body), &rows); err != nil {
				t.Fatal(err)
			}
			want := 2
			if mode == "forty" {
				want = 40
			}
			if len(rows) != want || page.Total != int64(want) || len(page.Articles) != want {
				t.Fatal("partial bulk result", len(rows), page.Total)
			}
			for i, row := range rows {
				stored := page.Articles[i]
				if row.ID <= 0 || row.ID != stored.ID || row.Title != stored.Title || row.Published != stored.Published || row.Slug != nil || i > 0 && rows[i-1].ID >= row.ID {
					t.Fatal("bulk output order or persistence", row, stored)
				}
				if mode == "forty" && row.Title != fmt.Sprintf("row %02d", i) {
					t.Fatal("bulk input order changed")
				}
			}
			if mode == "created" && (rows[0].Title != "first" || !rows[0].Published || rows[0].Summary == nil || *rows[0].Summary != "summary" || rows[1].Published || rows[1].Summary != nil) {
				t.Fatal("single-body defaults/null/cleaning diverged in the array", rows)
			}
		})
	}
}

type articleBulkUnreadBody struct{ reads, closes int }

func (body *articleBulkUnreadBody) Read([]byte) (int, error) {
	body.reads++
	return 0, io.ErrUnexpectedEOF
}
func (body *articleBulkUnreadBody) Close() error { body.closes++; return nil }

func TestArticleBulkAuthenticationCSRFAuthorizationAndQueryPrecedeBody(t *testing.T) {
	h := newHarness(t)
	allToken, allCSRF := h.csrf(t, h.allSession, apiapp.ListPath)
	viewToken, viewCSRF := h.csrf(t, h.viewSession, apiapp.ListPath)
	for _, mode := range []string{"anonymous", "csrf", "permission", "query"} {
		t.Run(mode, func(t *testing.T) {
			body := &articleBulkUnreadBody{}
			path := apiapp.BulkCreatePath
			if mode == "query" {
				path += "?unknown=true"
			}
			request := httptest.NewRequest(http.MethodPost, "http://example.test"+path, body)
			request.Header.Set("Content-Type", api.JSONContentType)
			if mode != "anonymous" {
				session, csrf, token := h.allSession, allCSRF, allToken
				if mode == "permission" {
					session, csrf, token = h.viewSession, viewCSRF, viewToken
				}
				request.AddCookie(session)
				if mode != "csrf" {
					request.AddCookie(csrf)
					request.Header.Set(websessionauth.DefaultCSRFHeader, token)
				}
			}
			recorder := httptest.NewRecorder()
			h.application.ServeHTTP(recorder, request)
			status := 403
			if mode == "query" {
				status = 400
			}
			if recorder.Code != status || body.reads != 0 || body.closes != 0 {
				t.Fatal("admission consumed/closed borrowed input", recorder.Code, body)
			}
			page, err := h.repository.List(context.Background(), articleapp.ListOptions{})
			if err != nil || page.Total != 0 {
				t.Fatal("denied bulk changed data", err)
			}
		})
	}
}
