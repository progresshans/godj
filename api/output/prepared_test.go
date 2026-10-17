package output_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
)

func TestPreparedResponseOwnsBytesHeadersAndExactDeclaration(t *testing.T) {
	type payload struct{ Values []int64 }
	var reads atomic.Int64
	shape := output.Object(output.Field("values", output.Array(output.Int64(), 0, 3), func(value payload) []int64 {
		reads.Add(1)
		return value.Values
	}))
	encoder := prepare(t, shape, serializers.Limits{})
	copy := encoder
	value := payload{Values: []int64{1, 2}}
	response, err := encoder.Prepare(t.Context(), 201, value)
	if err != nil || reads.Load() != 1 {
		t.Fatal("prepare must encode once", err, reads.Load())
	}
	value.Values[0] = 99
	header := http.Header{"Content-Type": {api.JSONContentType}, "X-Revision": {"1"}}
	derived, err := response.WithHeaders(header)
	if err != nil {
		t.Fatal(err)
	}
	header["X-Revision"][0] = "caller mutation"
	for _, test := range []struct {
		name    string
		encoder output.Output[payload]
		accept  bool
	}{
		{"owner", encoder, true}, {"copy", copy, true},
		{"independent same shape and budget", prepare(t, shape, serializers.Limits{}), false},
		{"independent tighter budget", prepare(t, shape, serializers.Limits{MaxDocumentBytes: 2}), false},
		{"zero", output.Output[payload]{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := test.encoder.Response(derived)
			if !test.accept {
				if !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) || actual.Status() != 0 {
					t.Fatal("accepted another output's prepared bytes", err, actual.Status())
				}
				return
			}
			body, bodyErr := actual.Body()
			if err != nil || bodyErr != nil || actual.Status() != 201 || string(body) != `{"values":[1,2]}` || actual.Header().Get("X-Revision") != "1" {
				t.Fatal("prepared snapshot changed", err, bodyErr, string(body), actual.Header())
			}
			body[0] = '!'
			actual.Header().Set("X-Revision", "returned mutation")
		})
	}
	original, err := encoder.Response(response)
	if err != nil || original.Header().Get("X-Revision") != "" || reads.Load() != 1 {
		t.Fatal("derivation changed original or re-evaluated DTO", err, reads.Load())
	}
	if invalid, err := response.WithHeaders(http.Header{"X-Test": {"bad\nheader"}}); err == nil {
		t.Fatal("accepted invalid header", invalid)
	}
	if invalid, err := (output.Prepared[payload]{}).WithHeaders(header); err == nil {
		t.Fatal("accepted zero prepared response", invalid)
	}
	if actual, err := encoder.Response(output.Prepared[payload]{}); err == nil || actual.Status() != 0 {
		t.Fatal("accepted zero prepared response", err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 8 {
				actual, err := copy.Response(derived)
				body, bodyErr := actual.Body()
				if err != nil || bodyErr != nil || string(body) != `{"values":[1,2]}` || actual.Header().Get("X-Revision") != "1" {
					t.Error("concurrent read changed snapshot", err, bodyErr, string(body))
					return
				}
				body[0] = '!'
			}
		})
	}
	workers.Wait()
	if reads.Load() != 1 {
		t.Fatal("response conversion invoked getter", reads.Load())
	}
}

func TestPreparedResponseFailureDoesNotPublish(t *testing.T) {
	for _, mode := range []string{"before", "during", "nil context", "budget", "invalid value", "invalid status"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			encoder := prepare(t, output.Object(output.Field("value", output.String(), func(value string) string {
				reads++
				if mode == "during" {
					cancel()
				}
				return value
			})), serializers.Limits{MaxStringBytes: 16})
			value, status, expectedReads := "ok", 200, 1
			switch mode {
			case "before":
				cancel()
				expectedReads = 0
			case "nil context":
				ctx = nil
				expectedReads = 0
			case "budget":
				value = strings.Repeat("x", 17)
			case "invalid value":
				value = "private\x00value"
			case "invalid status":
				status = 999
			}
			prepared, err := encoder.Prepare(ctx, status, value)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) || reads != expectedReads {
				t.Fatal("expected preparation failure", err, reads)
			}
			if (mode == "before" || mode == "during") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation cause lost", err)
			}
			if response, err := encoder.Response(prepared); err == nil || response.Status() != 0 {
				t.Fatal("failed preparation published a result", err)
			}
		})
	}
}
