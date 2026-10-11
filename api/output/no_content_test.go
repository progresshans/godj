package output_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
)

func TestNoContentOwnsAnEmptyResponseWithoutJSONSchema(t *testing.T) {
	empty := output.NoContent()
	if !empty.IsNoContent() || openapi.ValidateSchema(empty.Schema()) == nil {
		t.Fatal("no-content output invented a JSON schema")
	}
	definitions, err := output.Components(empty.Declaration())
	if err != nil || len(definitions) != 0 {
		t.Fatal("no-content output invented components", err)
	}
	prepared, err := empty.Prepare(t.Context(), http.StatusNoContent, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	copy := empty
	response, err := copy.Response(prepared)
	if err != nil || response.Status() != 204 || len(response.Header()) != 0 || response.Streaming() {
		t.Fatal("empty response ownership or representation", response, err)
	}
	if body, err := response.Body(); err != nil || len(body) != 0 {
		t.Fatal("empty response has content", err)
	}
	header := http.Header{"Etag": {`"revision-1"`}, "Content-Type": {"application/json"}}
	withMetadata, err := prepared.WithHeaders(header)
	if err != nil {
		t.Fatal(err)
	}
	header["Etag"][0] = "changed"
	response, err = empty.Response(withMetadata)
	if err != nil || response.Header().Get("Etag") != `"revision-1"` || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("resource metadata was discarded or retained mutable input", err)
	}
	if original, err := empty.Response(prepared); err != nil || len(original.Header()) != 0 {
		t.Fatal("metadata mutation reached original", err)
	}
	if _, err := output.NoContent().Response(prepared); err == nil {
		t.Fatal("independent output accepted the response")
	}
	jsonOutput, err := output.New(output.Object(output.Field("present", output.Boolean(), func(struct{}) bool { return true })), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	jsonPrepared, err := jsonOutput.Prepare(t.Context(), 200, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Response(jsonPrepared); err == nil {
		t.Fatal("same Go type confused JSON and no-content owners")
	}
	if _, err := jsonOutput.Response(prepared); err == nil {
		t.Fatal("JSON output accepted an empty response")
	}
	if _, err := output.Components(empty.Declaration(), jsonOutput.Declaration()); err != nil {
		t.Fatal("mixed endpoint representations cannot collect components", err)
	}
}

func TestNoContentRejectsInvalidPreparationAndMessageFraming(t *testing.T) {
	empty := output.NoContent()
	for _, status := range []int{0, 200, 201, 205, 304, 400, 500} {
		value, err := empty.Prepare(t.Context(), status, struct{}{})
		if !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) {
			t.Fatal("invalid no-content status", status, err)
		}
		if _, err := empty.Response(value); err == nil {
			t.Fatal("invalid preparation published a response")
		}
	}
	if _, err := empty.Encode(t.Context(), struct{}{}); err == nil {
		t.Fatal("no-content output encoded a fake JSON value")
	}
	if _, err := empty.JSON(t.Context(), 204, struct{}{}); err == nil {
		t.Fatal("JSON helper accepted a no-content declaration")
	}
	if _, err := empty.Prepare(nil, 204, struct{}{}); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := empty.Prepare(ctx, 204, struct{}{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	} else if _, err := empty.Response(value); err == nil {
		t.Fatal("cancellation published a response")
	}
	prepared, err := empty.Prepare(t.Context(), 204, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Content-Length", "content-length", "Transfer-Encoding", "TRANSFER-ENCODING", "Trailer"} {
		for _, values := range [][]string{nil, {}, {"0"}} {
			if _, err := prepared.WithHeaders(http.Header{field: values}); err == nil {
				t.Fatal("no-content response accepted framing", field)
			}
		}
	}
	var zero output.Output[struct{}]
	if zero.IsNoContent() {
		t.Fatal("zero output acquired no-content capability")
	}
	if _, err := output.Components(zero.Declaration()); err == nil {
		t.Fatal("zero declaration accepted")
	}
	if _, err := zero.Prepare(t.Context(), 204, struct{}{}); err == nil {
		t.Fatal("zero output prepared a response")
	}
}
