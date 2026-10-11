package admin

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestInlineDynamicPrototypeRetainsOnlyServerParentAndDefaults(t *testing.T) {
	config, actor := reportInlineFixture(t)
	config.Set.ExtraForms = 0
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{0, 7} {
		entry, err := inline.bind(t.Context(), actor, id, InlineAccess{View: true, Add: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if entry.empty == nil || entry.empty.Index() != -1 || entry.empty.Prefix() != "reports-__prefix__" || entry.empty.Form().Bound() || entry.empty.Form().ReadOnly() {
			t.Fatal("missing editable inert prototype")
		}
		if value, _ := entry.empty.Form().Initial().String("summary"); value != "" {
			t.Fatal("prototype copied current child")
		}
		parentFound := false
		for _, field := range entry.empty.Fields() {
			if key, found := field.InlineParent(); found {
				parentFound = true
				number, present := key.AsInteger()
				if id == 0 && !key.IsNull() || id != 0 && (!present || number != id) {
					t.Fatal("wrong prototype parent")
				}
			}
		}
		if !parentFound {
			t.Fatal("prototype omitted server parent")
		}
		if _, present := entry.empty.Form().Initial().Get("id"); present {
			t.Fatal("prototype adopted child identity")
		}
		if _, err := inlineRowContext(entry, *entry.empty, forms.Null(), 0); err != nil {
			t.Fatal(err)
		}
		view, err := inlineContext(InlineSubmission{entries: []inlineBound{entry}})
		if err != nil {
			t.Fatal(err)
		}
		groups, ok := view.Items()
		if !ok || len(groups) != 1 {
			t.Fatal("missing zero-extra group")
		}
	}
	entry, err := inline.bind(t.Context(), actor, 7, InlineAccess{View: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if entry.empty != nil {
		t.Fatal("no-add policy published a new-row prototype")
	}
}

func TestInlineAssetIsVersionedPublicAndNotALoginDestination(t *testing.T) {
	harness := newSiteApplicationHarness(t, 2)
	site := harness.site
	path := site.inlineScriptPath()
	if !strings.Contains(path, fmt.Sprintf("%x", sha256.Sum256(inlineScriptBytes))) || site.auth.AllowsNext(path) {
		t.Fatal("asset identity or login destination")
	}
	for _, route := range site.Routes() {
		if route.Path == path {
			t.Fatal("registry without inlines added asset route")
		}
	}
	configured, err := settings.New(settings.Definition{ProjectName: "inline_asset", InstalledApps: mustApps(t).All()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "godj_conformance:asset", Method: http.MethodGet, Path: path, Handler: site.inlineScript}}})
	if err != nil {
		t.Fatal(err)
	}
	before := harness.store.counts()
	for _, item := range []struct {
		method, suffix string
		status         int
	}{{"GET", "", 200}, {"GET", "?unexpected=1", 400}, {"POST", "", 405}} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(item.method, "http://example.test"+path+item.suffix, nil))
		if response.Code != item.status {
			t.Fatal("asset status", response.Code, item.status)
		}
		if item.status == 200 {
			if !bytes.Equal(response.Body.Bytes(), inlineScriptBytes) || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("X-Content-Type-Options") != "nosniff" || len(response.Result().Cookies()) != 0 {
				t.Fatal("asset payload/cache/security changed")
			}
		}
	}
	if after := harness.store.counts(); after != before {
		t.Fatal("asset touched a session")
	}
}
