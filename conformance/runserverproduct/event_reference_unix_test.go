//go:build darwin || linux

package runserverproduct_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/progresshans/godj/events"
)

// These independently executed Go observations use the same synthetic inputs
// as the pinned Django observer, not its generated results. The shared boundary
// is call order, stop/continue and removal; Python return values/exceptions and
// native-only differences are explicitly outside this projection.
func TestEventSynchronousReferenceBoundaries(t *testing.T) {
	payload, err := os.ReadFile("../runners/django/event_reference/input.json")
	if err != nil {
		t.Fatal(err)
	}
	var inputs struct {
		Kind  string                                            `json:"kind"`
		Cases []struct{ Name, Kind, Comparison, Reason string } `json:"cases"`
	}
	if err := json.Unmarshal(payload, &inputs); err != nil || inputs.Kind != "django-event-inputs-v1" {
		t.Fatal("invalid independent input", err)
	}
	type call struct {
		Name  string
		Value int
	}
	want := map[string][]call{
		"order":             {{"alpha", 1}, {"beta", 1}, {"gamma", 1}},
		"fail_fast":         {{"alpha", 1}, {"beta", 1}},
		"robust":            {{"alpha", 1}, {"beta", 1}, {"gamma", 1}},
		"disconnect_before": {{"alpha", 1}, {"gamma", 1}},
		"connect_during":    {{"alpha", 1}, {"beta", 1}, {"alpha", 2}, {"beta", 2}, {"gamma", 2}},
		"nested":            {{"alpha", 1}, {"alpha", 2}, {"beta", 2}, {"beta", 1}},
	}
	var observations []map[string]any
	seen := make(map[string]bool)
	for _, input := range inputs.Cases {
		if input.Comparison == "native_difference" {
			continue
		}
		if seen[input.Kind] || want[input.Kind] == nil {
			t.Fatal("unexpected shared input", input.Kind)
		}
		seen[input.Kind] = true
		e, err := events.New[int](events.Config{Name: "reference"})
		if err != nil {
			t.Fatal(err)
		}
		var calls []call
		disconnected := []bool{}
		var callbackErr error
		registered := false
		gamma := func(_ context.Context, value int) error { calls = append(calls, call{"gamma", value}); return nil }
		_, err = e.Subscribe("alpha", func(ctx context.Context, value int) error {
			calls = append(calls, call{"alpha", value})
			if input.Kind == "connect_during" && !registered {
				_, callbackErr = e.Subscribe("gamma", gamma)
				registered = true
			}
			if input.Kind == "nested" && value == 1 {
				_, callbackErr = e.Send(ctx, 2)
			}
			return callbackErr
		})
		if err != nil {
			t.Fatal(err)
		}
		beta, err := e.Subscribe("beta", func(_ context.Context, value int) error {
			calls = append(calls, call{"beta", value})
			if input.Kind == "fail_fast" || input.Kind == "robust" {
				return errors.New("synthetic private receiver error")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if input.Kind == "order" || input.Kind == "fail_fast" || input.Kind == "robust" || input.Kind == "disconnect_before" {
			if _, err := e.Subscribe("gamma", gamma); err != nil {
				t.Fatal(err)
			}
		}
		if input.Kind == "disconnect_before" {
			disconnected = append(disconnected, beta.Unsubscribe(), beta.Unsubscribe())
		}
		var results []events.Result
		if input.Kind == "robust" {
			results, err = e.SendRobust(t.Context(), 1)
		} else {
			results, err = e.Send(t.Context(), 1)
		}
		if input.Kind == "connect_during" {
			_, err = e.Send(t.Context(), 2)
		}
		if callbackErr != nil || (err != nil) != (input.Kind == "fail_fast") || !reflect.DeepEqual(calls, want[input.Kind]) {
			t.Fatal(input.Name, calls, err, callbackErr)
		}
		if input.Kind == "robust" && (len(results) != 3 || results[0].Err != nil || results[1].Err == nil || results[2].Err != nil) {
			t.Fatal("robust result boundary", results)
		}
		if input.Kind == "disconnect_before" && !reflect.DeepEqual(disconnected, []bool{true, false}) {
			t.Fatal(disconnected)
		}
		e.Close()
		if err := e.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		projected := make([][]any, len(calls))
		for i, entry := range calls {
			projected[i] = []any{entry.Name, entry.Value}
		}
		observations = append(observations, map[string]any{"name": input.Name, "comparison": input.Comparison, "calls": projected, "disconnected": disconnected})
	}
	if len(seen) != len(want) {
		t.Fatal("shared event reference inventory is incomplete")
	}
	digest := sha256.Sum256(payload)
	document, err := json.Marshal(map[string]any{"kind": "godj-event-observations-v1", "input_sha256": hex.EncodeToString(digest[:]), "cases": observations})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("GODJ_EVENT_OBSERVATION " + string(document))
}
