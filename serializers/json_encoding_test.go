package serializers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func TestEncodeStringEscapingAndOutputOwnership(t *testing.T) {
	values := []string{"", "<tag>&'", "quote\"\\/", "\b\t\n\f\r", "한글😀\u2028\u2029", strings.Repeat("long", 1024), "x"}
	for character := byte(1); character < 32; character++ {
		values = append(values, string([]byte{character}))
	}
	children := make([]serializers.Value, len(values))
	for index, value := range values {
		children[index] = serializers.String(value)
	}
	list, err := serializers.NewList(children...)
	if err != nil {
		t.Fatal(err)
	}
	var expected bytes.Buffer
	standard := json.NewEncoder(&expected)
	standard.SetEscapeHTML(false)
	if err := standard.Encode(values); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(expected.String(), "\n")
	first, err := serializers.Encode(list, serializers.Limits{})
	if err != nil || string(first) != want {
		t.Fatalf("JSON escaping mismatch: %v", err)
	}
	first[0] = '!'
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			got, err := serializers.Encode(list, serializers.Limits{})
			if err != nil || string(got) != want {
				t.Errorf("independent concurrent encode mismatch: %v", err)
				return
			}
			got[0] = '!'
		})
	}
	workers.Wait()
}

func TestEncodeImmutableSubtreesKeepsValueAndByteBudgets(t *testing.T) {
	value := serializers.Integer(1)
	// Shared children have tiny physical storage but an exponentially larger
	// logical JSON tree. Encode must enforce its own bounded traversal.
	for range 32 {
		var err error
		value, err = serializers.NewList(value, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if output, err := serializers.Encode(value, serializers.Limits{MaxDepth: 64, MaxValues: 64}); output != nil || !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatalf("shared subtree budget = %q, %v", output, err)
	}
	integers, err := serializers.NewList(serializers.Integer(math.MinInt64), serializers.Integer(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	want := "[-9223372036854775808,9223372036854775807]"
	if output, err := serializers.Encode(integers, serializers.Limits{MaxDocumentBytes: len(want)}); err != nil || string(output) != want {
		t.Fatalf("integer boundary = %q, %v", output, err)
	}
	if output, err := serializers.Encode(integers, serializers.Limits{MaxDocumentBytes: len(want) - 1}); output != nil || !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatalf("integer output cap = %q, %v", output, err)
	}
	for _, invalid := range []serializers.Value{{}, serializers.String("nul\x00"), serializers.String("\xff")} {
		if _, err := serializers.NewList(invalid); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
			t.Fatalf("invalid child accepted: %v", err)
		}
		if output, err := serializers.Encode(invalid, serializers.Limits{MaxStringBytes: 1}); output != nil || !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
			t.Fatalf("invalid root precedence = %q, %v", output, err)
		}
	}
}
