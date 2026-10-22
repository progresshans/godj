package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
)

type adminChoiceHTTPProof struct {
	GetStatus  int                   `json:"get_status"`
	PostStatus *int                  `json:"post_status"`
	Options    []adminChoiceOption   `json:"rendered_options"`
	Disabled   bool                  `json:"disabled"`
	Before     adminChoiceSource     `json:"stored_before"`
	After      adminChoiceSource     `json:"stored_after"`
	Writer     adminChoiceWriteStats `json:"writer"`
}
type adminChoiceNativeCase struct {
	adminChoiceCase
	Django         json.RawMessage          `json:"django_result"`
	Search         *adminChoiceSearchResult `json:"search,omitempty"`
	Form           *adminChoiceFormProof    `json:"form,omitempty"`
	HTTP           *adminChoiceHTTPProof    `json:"http,omitempty"`
	Compared       bool                     `json:"shared_comparison_passed"`
	DomainRestored bool                     `json:"domain_restored"`
}

func adminChoiceCompareSearch(t *testing.T, got, want adminChoiceSearchResult) {
	t.Helper()
	if got.Status != want.Status || !reflect.DeepEqual(got.Items, want.Items) || !reflect.DeepEqual(got.More, want.More) {
		t.Fatalf("Admin search differs from independent Django response\ngot: %+v\nwant: %+v", got, want)
	}
}
func (native *adminChoiceNative) observeSearch(t *testing.T, c adminChoiceCase) adminChoiceSearchResult {
	t.Helper()
	var input adminChoiceSearchInput
	if err := adminChoiceDecode(c.Input, &input); err != nil {
		t.Fatal(err)
	}
	page, err := strconv.Atoi(input.Page)
	if err != nil || page < 1 {
		t.Fatal("unsupported shared fixture page")
	}
	client, stats := native.client(t, c)
	query := url.Values{"id": {"1"}, "q": {input.Term}, "offset": {strconv.Itoa((page - 1) * 20)}, "limit": {"20"}}
	response := client.do(http.MethodGet, "/admin/sources/change/choices/"+c.Relation+"/?"+query.Encode(), nil)
	result := adminChoiceSearchResult{Status: response.Code, Items: []adminChoiceItem{}, Native: json.RawMessage("null")}
	allowed := slices.Contains(c.Actor.Source, "change") && slices.Contains(c.Actor.Target, "view")
	if allowed {
		if response.Code != http.StatusOK {
			t.Fatalf("native search expected admission, got %d %s", response.Code, response.Body.String())
		}
		var wire struct {
			Items []struct {
				Value string `json:"value"`
				Label string `json:"label"`
			} `json:"items"`
			Offset int  `json:"offset"`
			Limit  int  `json:"limit"`
			More   bool `json:"more"`
		}
		if err := adminChoiceDecode(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Offset != (page-1)*20 || wire.Limit != 20 || len(wire.Items) > 20 || wire.Items == nil {
			t.Fatal("native search lost its page bound")
		}
		for _, item := range wire.Items {
			if !adminChoiceKey(item.Value) {
				t.Fatal("native search coerced signed or large identity")
			}
			result.Items = append(result.Items, adminChoiceItem{item.Value, item.Label})
		}
		result.More = &wire.More
		result.Native = append(json.RawMessage(nil), response.Body.Bytes()...)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("choice response can be cached across current authorization")
		}
	} else if response.Code != http.StatusForbidden {
		t.Fatalf("native source/target admission returned %d", response.Code)
	}
	if *stats != (adminChoiceWriteStats{}) {
		t.Fatal("search invoked a writer")
	}
	return result
}

func (native *adminChoiceNative) observeHTTPForm(t *testing.T, c adminChoiceCase, want adminChoiceFormResult) adminChoiceHTTPProof {
	t.Helper()
	var input adminChoiceFormInput
	if err := adminChoiceDecode(c.Input, &input); err != nil {
		t.Fatal(err)
	}
	before, err := native.domain(t.Context(), native.database)
	if err != nil {
		t.Fatal(err)
	}
	client, stats := native.client(t, c)
	path := "/admin/sources/change/?id=1"
	if input.Mode == "add" {
		path = "/admin/sources/add/"
	}
	response := client.do(http.MethodGet, path, nil)
	proof := adminChoiceHTTPProof{GetStatus: response.Code, Options: []adminChoiceOption{}, Before: before.Source}
	if response.Code == http.StatusOK {
		proof.Options, proof.Disabled, err = adminChoiceOptions(response.Body.String(), c.Relation)
		if err != nil {
			t.Fatal(err)
		}
	}
	afterGet, err := native.domain(t.Context(), native.database)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, afterGet) || *stats != (adminChoiceWriteStats{}) {
		t.Fatal("GET changed database state")
	}
	if c.Comparison == "shared" && response.Code != http.StatusOK {
		t.Fatalf("shared form GET status %d: %s", response.Code, response.Body.String())
	}
	if !input.Bound && c.Comparison == "shared" {
		selected := []adminChoiceItem{}
		for _, option := range proof.Options {
			if option.Selected && option.Key != "" {
				selected = append(selected, adminChoiceItem{option.Key, option.Label})
			}
		}
		// Selected M2M options are a membership set. Keep both raw orders in the
		// capture, and normalize only this comparison; search order stays exact.
		byKey := func(items []adminChoiceItem) map[string]string {
			out := map[string]string{}
			for _, item := range items {
				if _, duplicate := out[item.Key]; duplicate {
					t.Fatal("duplicate selected option")
				}
				out[item.Key] = item.Label
			}
			return out
		}
		if !reflect.DeepEqual(byKey(selected), byKey(want.Selected)) {
			t.Fatalf("initial selection or label lost: got %+v want %+v", selected, want.Selected)
		}
		if len(proof.Options) > 23 {
			t.Fatal("initial Admin form rendered the whole target collection")
		}
	}
	if c.Name == c.Relation+"/no_source_permission" && response.Code != http.StatusForbidden {
		t.Fatal("native form admitted a source without view or change")
	}
	if c.Name == c.Relation+"/view_only" && (response.Code != http.StatusOK || !proof.Disabled) {
		t.Fatal("native viewer did not receive a disabled choice control")
	}
	if input.Bound {
		// Revocation follows the actual GET, so a label previously displayed by
		// the browser cannot authorize a later POST or leak through its errors.
		if len(c.Setup.Revoke) > 0 {
			if err := native.database.AtomicRelation(t.Context(), func(session db.RelationSession) error { return native.revoke(t.Context(), session, c.Setup.Revoke) }); err != nil {
				t.Fatal(err)
			}
		}
		dashboard := client.do(http.MethodGet, "/admin/", nil)
		if dashboard.Code != http.StatusOK {
			t.Fatal("fixture dashboard unavailable")
		}
		values := url.Values{}
		for name, items := range input.Data {
			values[name] = slices.Clone(items)
		}
		values.Set("csrfmiddlewaretoken", siteCSRFToken(t, dashboard.Body.String()))
		response = client.do(http.MethodPost, path, values)
		status := response.Code
		proof.PostStatus = &status
		allowed := slices.Contains(c.Actor.Source, "change") && slices.Contains(c.Actor.Target, "view")
		if !allowed {
			if response.Code != http.StatusForbidden || *stats != (adminChoiceWriteStats{}) {
				t.Fatal("denied submission reached the final writer")
			}
		} else if c.Comparison == "shared" {
			if want.Valid != nil && *want.Valid {
				if response.Code != http.StatusFound || stats.Calls != 1 || stats.Commits != 1 {
					t.Fatalf("valid native POST did not commit exactly once: %d %+v %s", response.Code, *stats, response.Body.String())
				}
			} else {
				if response.Code != http.StatusOK || *stats != (adminChoiceWriteStats{}) {
					t.Fatalf("invalid native POST wrote or lost its errors: %d %+v", response.Code, *stats)
				}
				for _, codes := range want.Codes {
					for _, code := range codes {
						if !strings.Contains(response.Body.String(), `data-error-code="`+code+`"`) {
							t.Fatalf("native POST missing validation code %q", code)
						}
					}
				}
			}
		}
		if response.Code == http.StatusOK {
			proof.Options, proof.Disabled, err = adminChoiceOptions(response.Body.String(), c.Relation)
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range c.Setup.Revoke {
			for _, target := range before.Targets {
				if target.Key == key && strings.Contains(response.Body.String(), target.Label) {
					t.Fatal("revoked label survived bound error rendering")
				}
			}
		}
	}
	after, err := native.domain(t.Context(), native.database)
	if err != nil {
		t.Fatal(err)
	}
	proof.After = after.Source
	proof.Writer = *stats
	expected := before.Source
	if input.Bound && c.Comparison == "shared" {
		expected = want.After
	}
	if !reflect.DeepEqual(proof.After, expected) {
		t.Fatalf("HTTP durable readback differs: got %+v want %+v", proof.After, expected)
	}
	if err := native.restore(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestAdminChoiceMatchesIndependentDjango(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			directory := os.Getenv("GODJ_ADMIN_CHOICE_REFERENCE_DIR")
			if directory == "" {
				if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
					t.Fatal("required independent Admin reference absent")
				}
				t.Skip("independent Admin observations execute in the native PostgreSQL core owner, including its SQLite comparison")
			}
			inputRaw := adminChoiceBytes(t, adminChoiceInputPath)
			observerRaw := adminChoiceBytes(t, "../conformance/runners/django/admin_choice_reference.py")
			verificationRaw := adminChoiceBytes(t, "../conformance/runners/django/admin_choice_fixture/source-verification.json")
			raw := adminChoiceBytes(t, filepath.Join(directory, "django61-"+backend+".json"))
			reference, inputs, err := adminChoiceReadReference(raw, inputRaw, observerRaw, verificationRaw, backend)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("reference_negative_controls", func(t *testing.T) {
				adminChoiceReferenceNegatives(t, raw, inputRaw, observerRaw, verificationRaw, backend)
			})
			native := adminChoiceOpen(t, backend, inputs)
			original, err := native.domain(t.Context(), native.database)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original.Source, inputs.Source) {
				t.Fatal("native source seed differs from shared input")
			}
			declaredTargets := slices.Clone(inputs.Targets)
			slices.SortFunc(declaredTargets, func(a, b adminChoiceTarget) int {
				av, bv := adminChoiceInt(t, a.Key), adminChoiceInt(t, b.Key)
				if av < bv {
					return -1
				}
				if av > bv {
					return 1
				}
				return 0
			})
			if !reflect.DeepEqual(original.Targets, declaredTargets) {
				t.Fatal("native target seed differs from shared input")
			}
			observations := []adminChoiceNativeCase{}
			shared, nativeOnly := 0, 0
			for i, c := range inputs.Cases {
				t.Run(c.Name, func(t *testing.T) {
					observation := adminChoiceNativeCase{adminChoiceCase: c, Django: reference.Cases[i].Result}
					if c.Kind == "autocomplete" {
						search := native.observeSearch(t, c)
						observation.Search = &search
						if c.Comparison == "shared" {
							var expected adminChoiceSearchResult
							if err := adminChoiceDecode(reference.Cases[i].Result, &expected); err != nil {
								t.Fatal(err)
							}
							adminChoiceCompareSearch(t, search, expected)
							observation.Compared = true
						}
					} else {
						var expected adminChoiceFormResult
						if err := adminChoiceDecode(reference.Cases[i].Result, &expected); err != nil {
							t.Fatal(err)
						}
						if c.Comparison == "shared" {
							form := native.observeForm(t, c)
							adminChoiceCompareForm(t, form, expected)
							observation.Form = &form
							observation.Compared = true
						}
						httpProof := native.observeHTTPForm(t, c, expected)
						observation.HTTP = &httpProof
					}
					current, err := native.domain(t.Context(), native.database)
					if err != nil {
						t.Fatal(err)
					}
					observation.DomainRestored = reflect.DeepEqual(original, current)
					if !observation.DomainRestored {
						t.Fatal("case changed domain state after observation cleanup")
					}
					if c.Comparison == "shared" {
						shared++
					} else {
						nativeOnly++
					}
					observations = append(observations, observation)
				})
			}
			if t.Failed() || len(observations) != len(inputs.Cases) {
				t.Fatal("Admin observation inventory incomplete")
			}
			final, err := native.domain(t.Context(), native.database)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, final) {
				t.Fatal("native domain not restored")
			}
			if err := native.cleanup(); err != nil {
				t.Fatal(err)
			}
			sources := map[string]string{}
			for _, path := range []string{"choice_reference_test.go", "choice_reference_native_test.go", "choice_reference_wire_test.go", "testdata/choicereferencefixture/models.go", "relation_choices.go", "site_choices.go", "site_render.go", "site_assets/choices.js", "../forms/model/relation_choices.go", "../forms/model/choice_input.go"} {
				sources[path] = adminChoiceSHA(adminChoiceBytes(t, path))
			}
			originalBytes, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			finalBytes, err := json.Marshal(final)
			if err != nil {
				t.Fatal(err)
			}
			capture := map[string]any{"kind": "godj-admin-choice-native-v1", "schema_version": 1, "backend": backend, "django_commit": reference.Commit, "input_sha256": adminChoiceSHA(inputRaw), "reference_sha256": adminChoiceSHA(raw), "native_sources_sha256": sources, "cases": observations, "shared_cases": shared, "native_only_cases": nativeOnly,
				"profile":        native.profile,
				"initial_domain": original, "final_domain": final,
				"comparison_policy": map[string]string{"search": "Exact HTTP status, ordered key/label tuples and more flag.", "form": "Validity, diagnostic codes, cleaned keys, stored relations and rollback. M2M cleaned keys compare as membership sets; raw order is retained.", "http": "Separate native GET/POST requests and writer commit counters, followed by fresh durable reads; rollback observations never stand in for a commit."},
				"database":          map[string]any{"initial_sha256": adminChoiceSHA(originalBytes), "final_sha256": adminChoiceSHA(finalBytes), "domain_restored": true, "owned_resource_removed": true}}
			adminChoiceWriteCapture(t, backend, capture)
			t.Logf("ADMIN_CHOICE_REFERENCE_VERIFIED backend=%s cases=%d shared=%d native_only=%d cleanup=true", backend, len(observations), shared, nativeOnly)
		})
	}
}
