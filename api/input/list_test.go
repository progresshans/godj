package input_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func listItem(t *testing.T, set func()) (serializers.Spec, input.Body[patchDTO]) {
	t.Helper()
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{
		f(serializers.StringField("name", serializers.WithMaxLength(8))),
		f(serializers.BooleanField("enabled", serializers.WithDefault(serializers.Boolean(false)))),
		f(serializers.StringField("memo", serializers.WithNullable(), serializers.WithRequired(false))),
		f(serializers.IntegerListField("tags", serializers.WithDefault(serializers.Integers(2, 3)))),
	}))
	body := require[input.Body[patchDTO]](t)(input.New(spec, api.ParserConfig{MaxBodyBytes: 32},
		input.Field("name", input.String(), func(v *patchDTO, p input.Presence[string]) {
			if set != nil {
				set()
			}
			v.Name = p
		}),
		input.Field("enabled", input.Boolean(), func(v *patchDTO, p input.Presence[bool]) { v.Enabled = p }),
		input.Field("memo", input.Nullable(input.String()), func(v *patchDTO, p input.Presence[*string]) { v.Memo = p }),
		input.Field("tags", input.Integers(), func(v *patchDTO, p input.Presence[[]int64]) { v.Tags = p }),
	))
	return spec, body
}

func TestListValidationCollectsRowsWithoutPublishingPartialValues(t *testing.T) {
	sets := 0
	spec, body := listItem(t, func() { sets++ })
	var checked []string
	list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MinItems: 1, MaxItems: 4}, func(_ context.Context, value patchDTO) (validation.Errors, error) {
		name, _ := value.Name.Get()
		checked = append(checked, name)
		if name == "blocked" {
			return validation.NewErrors(validation.New("name", "unavailable")), nil
		}
		return validation.Errors{}, nil
	}))
	rows := require[[]serializers.Object](t)(spec.DecodeList([]byte(`[{"name":" "},{"name":"yes","tags":[null,"x"]},{"name":"blocked"},{"name":"valid"}]`), serializers.Limits{}))
	values, failures, err := list.Bind(t.Context(), rows, serializers.ModeFull)
	want := []string{"name/blank/index=0", "tags/null/item_index=0/index=1", "tags/type/item_index=1/index=1", "name/unavailable/index=2"}
	if err != nil || values != nil || !slices.Equal(codes(failures), want) || sets != 2 || !slices.Equal(checked, []string{"blocked", "valid"}) {
		t.Fatal("row validation, indexes or publication differ", values, codes(failures), checked, sets, err)
	}
	_, single, err := body.Bind(t.Context(), rows[1], serializers.ModeFull)
	if err != nil || !slices.Equal(codes(single), []string{"tags/null/index=0", "tags/type/index=1"}) {
		t.Fatal("list changed the source body's field positions", codes(single), err)
	}
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		schema := require[openapi.Schema](t)(list.Schema(mode))
		item := require[openapi.Schema](t)(body.Schema(mode))
		want := require[openapi.Schema](t)(openapi.ArrayRange(item, 1, 4))
		if !reflect.DeepEqual(schema.Value(), want.Value()) {
			t.Fatal("list schema differs from its item mode or bounds")
		}
	}
}

func TestListOwnsDefaultsNullsAndConcurrentRows(t *testing.T) {
	spec, body := listItem(t, nil)
	list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MinItems: 0, MaxItems: 3}))
	rows := require[[]serializers.Object](t)(spec.DecodeList([]byte(`[{"name":" one ","memo":"text"},{"name":"two","memo":null},{"name":"three","tags":[]}]`), serializers.Limits{}))
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			values, failures, err := list.Bind(t.Context(), rows, serializers.ModeFull)
			if err != nil || !failures.Empty() || len(values) != 3 {
				t.Error("valid list failed", err)
				return
			}
			name, present := values[0].Name.Get()
			memo, _ := values[0].Memo.Get()
			null, hasNull := values[1].Memo.Get()
			first, _ := values[0].Tags.Get()
			second, _ := values[1].Tags.Get()
			empty, _ := values[2].Tags.Get()
			enabled, hasEnabled := values[0].Enabled.Get()
			if name != "one" || !present || memo == nil || *memo != "text" || !hasNull || null != nil || enabled || !hasEnabled || empty == nil || len(empty) != 0 || !slices.Equal(first, []int64{2, 3}) || !slices.Equal(second, []int64{2, 3}) {
				t.Error("presence/default policy changed")
				return
			}
			first[0], *memo = 90, "changed"
			if second[0] != 2 {
				t.Error("row slices share mutable storage")
			}
		})
	}
	group.Wait()
	partial, failures, err := list.Bind(t.Context(), []serializers.Object{object(t, spec, `{}`)}, serializers.ModePartial)
	if err != nil || !failures.Empty() || len(partial) != 1 || !reflect.DeepEqual(partial[0], patchDTO{}) {
		t.Fatal("partial omission applied defaults", partial, err)
	}
	empty, failures, err := list.Bind(t.Context(), nil, serializers.ModeFull)
	if err != nil || !failures.Empty() || empty == nil || len(empty) != 0 {
		t.Fatal("valid empty list is not an owned empty result", empty, err)
	}
}

func TestListCountItemAndDiagnosticBudgets(t *testing.T) {
	spec, body := listItem(t, nil)
	raw := `{"name":"ok"}`
	row := object(t, spec, raw)
	for _, maximum := range []int{len(raw) - 1, len(raw)} {
		list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MinItems: 1, MaxItems: 2, ItemLimits: serializers.Limits{MaxDocumentBytes: maximum}}))
		values, failures, err := list.Bind(t.Context(), []serializers.Object{row}, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		if maximum == len(raw) && (len(values) != 1 || !failures.Empty()) {
			t.Fatal("exact item limit failed", codes(failures))
		}
		if maximum < len(raw) && (values != nil || !slices.Equal(codes(failures), []string{"__all__/limit_exceeded/index=0"})) {
			t.Fatal("item limit lost its row", values, codes(failures))
		}
		for _, rows := range [][]serializers.Object{nil, {row, row, row}} {
			values, failures, err := list.Bind(t.Context(), rows, serializers.ModeFull)
			if err != nil || values != nil || !slices.Equal(codes(failures), []string{"__all__/invalid_count/min=1/max=2"}) {
				t.Fatal("row count did not reject the complete input", values, failures, err)
			}
		}
	}
	violations := make([]validation.Violation, 8193)
	for i := range violations {
		violations[i] = validation.New("name", "invalid")
	}
	list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MaxItems: 3}, func(context.Context, patchDTO) (validation.Errors, error) {
		return validation.NewErrors(violations...), nil
	}))
	values, failures, err := list.Bind(t.Context(), []serializers.Object{row, row, row}, serializers.ModeFull)
	if err != nil || values != nil || !slices.Equal(codes(failures), []string{"__all__/too_many_errors/count=24579"}) {
		t.Fatal("overflow published a truncated prefix", values, codes(failures), err)
	}
}

func TestListCancellationAndValidatorFailuresRemainInternal(t *testing.T) {
	private := &api.Error{Code: api.FailureInvalidRequest, Detail: "private validator cause"}
	for _, cancelAt := range []string{"before", "setter", "validator", "failure"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sets, checks := 0, 0
			spec, body := listItem(t, func() {
				sets++
				if cancelAt == "setter" {
					cancel()
				}
			})
			list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MaxItems: 2}, func(context.Context, patchDTO) (validation.Errors, error) {
				checks++
				if cancelAt == "validator" {
					cancel()
				}
				return validation.Errors{}, private
			}))
			if cancelAt == "before" {
				cancel()
			}
			row := object(t, spec, `{"name":"ok"}`)
			values, failures, err := list.Bind(ctx, []serializers.Object{row, row}, serializers.ModeFull)
			if values != nil || !failures.Empty() || err == nil || sets > 1 || checks > 1 {
				t.Fatal("failure published values or continued rows", values, failures, sets, checks, err)
			}
			if cancelAt != "failure" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if cancelAt == "validator" || cancelAt == "failure" {
				if !errors.Is(err, private) {
					t.Fatal("validator cause lost", err)
				}
				if _, handled, _ := api.RequestErrorResponse(err); handled {
					t.Fatal("validator impersonated a parser error")
				}
			}
			if cancelAt == "failure" && (strings.Contains(err.Error(), "private") || strings.Contains(fmt.Sprintf("%#v", err), "private")) {
				t.Fatal("validator secret escaped")
			}
		})
	}
}

func TestListRejectsInvalidOrAmbiguousDiagnosticsEvenAfterOverflow(t *testing.T) {
	spec, body := listItem(t, nil)
	row := object(t, spec, `{"name":"ok"}`)
	for _, mode := range []string{"invalid field", "invalid parameter", "repeated index", "repeated item index", "ambiguous indexes"} {
		t.Run(mode, func(t *testing.T) {
			violations := make([]validation.Violation, 16385)
			for index := range violations {
				violations[index] = validation.New("name", "invalid")
			}
			switch mode {
			case "invalid field":
				violations[len(violations)-1] = validation.New("private\x00field", "invalid")
			case "invalid parameter":
				violations[len(violations)-1] = validation.New("name", "invalid", validation.NewParam("value", "private\x00value"))
			case "repeated index":
				violations[len(violations)-1] = validation.New("name", "invalid", validation.NewParam("index", "0"), validation.NewParam("index", "1"))
			case "repeated item index":
				violations[len(violations)-1] = validation.New("name", "invalid", validation.NewParam("item_index", "0"), validation.NewParam("item_index", "1"))
			case "ambiguous indexes":
				violations[len(violations)-1] = validation.New("name", "invalid", validation.NewParam("index", "0"), validation.NewParam("item_index", "1"))
			}
			list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MaxItems: 1}, func(context.Context, patchDTO) (validation.Errors, error) {
				return validation.NewErrors(violations...), nil
			}))
			values, failures, err := list.Bind(t.Context(), []serializers.Object{row}, serializers.ModeFull)
			if err == nil || values != nil || !failures.Empty() || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid callback diagnostics became a public summary", values, failures, err)
			}
		})
	}
}

func TestListPreparationRejectsInvalidDeclarations(t *testing.T) {
	_, body := listItem(t, func() { t.Error("startup called setter") })
	for _, config := range []input.ListConfig{{}, {MinItems: -1, MaxItems: 1}, {MinItems: 2, MaxItems: 1}, {MaxItems: 1025}, {MaxItems: 1, Parser: api.ParserConfig{MaxBodyBytes: -1}}, {MaxItems: 1, ItemLimits: serializers.Limits{MaxDepth: -1}}} {
		if _, err := input.NewList(body, config); err == nil {
			t.Fatal("invalid list config accepted", config)
		}
	}
	validator := func(context.Context, patchDTO) (validation.Errors, error) {
		t.Error("startup called validator")
		return validation.Errors{}, nil
	}
	for _, validators := range [][]input.ListValidator[patchDTO]{{nil}, {validator, validator}} {
		if _, err := input.NewList(body, input.ListConfig{MaxItems: 1}, validators...); err == nil {
			t.Fatal("invalid validators accepted")
		}
	}
	if _, err := input.NewList(input.Body[patchDTO]{}, input.ListConfig{MaxItems: 1}); err == nil {
		t.Fatal("zero body accepted")
	}
	for _, list := range []input.ListBody[patchDTO]{{}, require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MaxItems: 1}))} {
		if _, err := list.Schema(0); err == nil {
			t.Fatal("invalid schema mode accepted")
		}
		if value, failures, err := list.Bind(nil, nil, serializers.ModeFull); err == nil || value != nil || !failures.Empty() {
			t.Fatal("invalid Bind accepted")
		}
	}
}

func TestListActualRequestUsesOneBorrowedParser(t *testing.T) {
	for _, test := range []struct {
		name, raw, media string
		code             api.FailureCode
		valid            bool
	}{
		{"valid", `[{"name":"one"},{"name":"two"}]`, api.JSONContentType, "", true},
		{"object root", `{"name":"one"}`, api.JSONContentType, api.FailureInvalidRequest, false},
		{"scalar row", `[{} , 1]`, api.JSONContentType, api.FailureInvalidRequest, false},
		{"duplicate member", `[{"name":"one","name":"two"}]`, api.JSONContentType, api.FailureInvalidRequest, false},
		{"media before read", `[]`, "text/plain", api.FailureUnsupportedMedia, false},
		{"combined request budget", `[{"name":"one"}]` + strings.Repeat(" ", 128), api.JSONContentType, api.FailureBodyTooLarge, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			_, body := listItem(t, func() { calls.Add(1) })
			list := require[input.ListBody[patchDTO]](t)(input.NewList(body, input.ListConfig{MinItems: 1, MaxItems: 4, Parser: api.ParserConfig{MaxBodyBytes: 128}}))
			var values []patchDTO
			var failures validation.Errors
			var failure error
			configured := require[settings.Settings](t)(settings.New(settings.Definition{ProjectName: "list_test", InstalledApps: []apps.Config{{Name: "example.test/list", Label: "list"}}}))
			app := require[*web.Application](t)(web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "list:parse", Method: "POST", Path: "/", Handler: func(request *web.Request) (web.Response, error) {
				values, failures, failure = list.Parse(request, serializers.ModeFull)
				return web.NewResponse(204, nil, nil)
			}}}}))
			reader := &borrowedBody{reader: strings.NewReader(test.raw)}
			request := httptest.NewRequest(http.MethodPost, "http://example.test/", nil)
			request.Body = reader
			request.ContentLength = -1
			request.Header.Set("Content-Type", test.media)
			recorder := httptest.NewRecorder()
			app.ServeHTTP(recorder, request)
			if recorder.Code != 204 || reader.closes != 0 {
				t.Fatal("borrowed lifetime changed", recorder.Code, reader.closes)
			}
			if test.valid {
				if len(values) != 2 || failure != nil || !failures.Empty() || calls.Load() != 2 {
					t.Fatal("array reader used the scalar body or failed", values, failure)
				}
				return
			}
			if values != nil || calls.Load() != 0 || !failures.Empty() || !errors.Is(failure, &api.Error{Code: test.code}) {
				t.Fatal("parser failed after row work or changed classification", values, calls.Load(), failure)
			}
			if test.media != "application/json" && reader.reads != 0 {
				t.Fatal("media rejection read the body")
			}
		})
	}
}
