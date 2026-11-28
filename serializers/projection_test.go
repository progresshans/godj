package serializers_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
)

func TestLazyProjectionSharesValueEncodingAndEveryBudget(t *testing.T) {
	document, err := jsonvalue.Parse([]byte(`{"a":[true,null]}`))
	if err != nil {
		t.Fatal(err)
	}
	children := []serializers.Value{serializers.Integer(-9), serializers.JSON(document)}
	list, err := serializers.NewList(children...)
	if err != nil {
		t.Fatal(err)
	}
	object, err := serializers.NewObject(
		serializers.MemberOf("label", serializers.String(" \u2028x")),
		serializers.MemberOf("values", list),
		serializers.MemberOf("tail", serializers.Integer(2)),
	)
	if err != nil {
		t.Fatal(err)
	}
	projection := serializers.ObjectProjection(3, func(_ context.Context, index int) (string, serializers.Projection, error) {
		if index == 1 {
			return "values", serializers.ArrayProjection(2, func(_ context.Context, child int) (serializers.Projection, error) {
				return serializers.ValueProjection(children[child]), nil
			}), nil
		}
		member := object.Members()[index]
		return member.Name(), serializers.ValueProjection(member.Value()), nil
	})
	const want = `{"label":" \u2028x","values":[-9,{"a":[true,null]}],"tail":2}`
	for name, limit := range map[string]struct{ exact, short serializers.Limits }{
		"document":     {serializers.Limits{MaxDocumentBytes: len(want)}, serializers.Limits{MaxDocumentBytes: len(want) - 1}},
		"depth":        {serializers.Limits{MaxDepth: 5}, serializers.Limits{MaxDepth: 4}},
		"values":       {serializers.Limits{MaxValues: 9}, serializers.Limits{MaxValues: 8}},
		"members":      {serializers.Limits{MaxObjectMembers: 3}, serializers.Limits{MaxObjectMembers: 2}},
		"items":        {serializers.Limits{MaxArrayItems: 2}, serializers.Limits{MaxArrayItems: 1}},
		"string bytes": {serializers.Limits{MaxStringBytes: 6}, serializers.Limits{MaxStringBytes: 5}},
		"number bytes": {serializers.Limits{MaxNumberBytes: 2}, serializers.Limits{MaxNumberBytes: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, eager := range []bool{false, true} {
				encode := func(limits serializers.Limits) ([]byte, error) {
					if eager {
						return serializers.Encode(object.Value(), limits)
					}
					return serializers.EncodeProjection(t.Context(), projection, limits)
				}
				body, err := encode(limit.exact)
				if err != nil || string(body) != want {
					t.Fatalf("eager=%v exact = %s, %v", eager, body, err)
				}
				body, err = encode(limit.short)
				if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) || body != nil {
					t.Fatalf("eager=%v short = %s, %v", eager, body, err)
				}
			}
		})
	}
}

func TestLazyProjectionRejectsBeforeReadingOversizedContainers(t *testing.T) {
	reads := 0
	read := func(context.Context, int) (serializers.Projection, error) {
		reads++
		return serializers.ValueProjection(serializers.Integer(0)), nil
	}
	for _, test := range []struct {
		size   int
		limits serializers.Limits
	}{
		{2, serializers.Limits{MaxArrayItems: 1}},
		{2, serializers.Limits{MaxValues: 2}},
		{1, serializers.Limits{MaxDepth: 1}},
		{1 << 30, serializers.Limits{}},
	} {
		body, err := serializers.EncodeProjection(t.Context(), serializers.ArrayProjection(test.size, read), test.limits)
		if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) || body != nil || reads != 0 {
			t.Fatalf("oversize visited %d: %s, %v", reads, body, err)
		}
	}
	// A nested child can spend the remaining budget. The next sibling must not
	// be read just to discover that no value slot remains.
	first, err := serializers.NewList(serializers.Integer(1), serializers.Integer(2))
	if err != nil {
		t.Fatal(err)
	}
	projection := serializers.ArrayProjection(2, func(_ context.Context, index int) (serializers.Projection, error) {
		reads++
		if index != 0 {
			t.Fatal("budget-exhausted sibling was read")
		}
		return serializers.ValueProjection(first), nil
	})
	body, err := serializers.EncodeProjection(t.Context(), projection, serializers.Limits{MaxValues: 4})
	if err == nil || body != nil || reads != 1 {
		t.Fatalf("shared slot bound: %s %v reads=%d", body, err, reads)
	}
}

func TestLazyProjectionFailuresAndCancellationDiscardOutput(t *testing.T) {
	sentinel := errors.New("reader failed")
	for name, projection := range map[string]serializers.Projection{
		"zero":              {},
		"zero value":        serializers.ValueProjection(serializers.Value{}),
		"negative object":   serializers.ObjectProjection(-1, nil),
		"negative array":    serializers.ArrayProjection(-1, nil),
		"nil object reader": serializers.ObjectProjection(0, nil),
		"nil array reader":  serializers.ArrayProjection(0, nil),
		"empty name": serializers.ObjectProjection(1, func(context.Context, int) (string, serializers.Projection, error) {
			return "", serializers.ValueProjection(serializers.Null()), nil
		}),
		"duplicate name": serializers.ObjectProjection(2, func(context.Context, int) (string, serializers.Projection, error) {
			return "same", serializers.ValueProjection(serializers.Null()), nil
		}),
		"invalid child": serializers.ArrayProjection(1, func(context.Context, int) (serializers.Projection, error) { return serializers.Projection{}, nil }),
		"reader error": serializers.ObjectProjection(2, func(_ context.Context, index int) (string, serializers.Projection, error) {
			if index == 1 {
				return "last", serializers.Projection{}, sentinel
			}
			return "first", serializers.ValueProjection(serializers.String("private body")), nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			body, err := serializers.EncodeProjection(t.Context(), projection, serializers.Limits{})
			if err == nil || body != nil {
				t.Fatalf("failed projection = %s, %v", body, err)
			}
			if name == "reader error" && (!errors.Is(err, sentinel) || !strings.Contains(err.Error(), `$["last"]`)) {
				t.Fatal("reader cause or path lost", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	reads := 0
	projection := serializers.ArrayProjection(2, func(context.Context, int) (serializers.Projection, error) {
		reads++
		cancel()
		return serializers.ValueProjection(serializers.String("not published")), nil
	})
	if body, err := serializers.EncodeProjection(ctx, projection, serializers.Limits{}); !errors.Is(err, context.Canceled) || body != nil || reads != 1 {
		t.Fatal("late cancellation published bytes", err, reads)
	}
	if body, err := serializers.EncodeProjection(ctx, projection, serializers.Limits{}); !errors.Is(err, context.Canceled) || body != nil || reads != 1 {
		t.Fatal("pre-cancellation read callback", err, reads)
	}
	if body, err := serializers.EncodeProjection(nil, projection, serializers.Limits{}); err == nil || body != nil || reads != 1 {
		t.Fatal("nil context read callback", err, reads)
	}
	for _, empty := range []serializers.Projection{
		serializers.ObjectProjection(0, func(context.Context, int) (string, serializers.Projection, error) {
			t.Fatal("empty object read")
			return "", serializers.Projection{}, nil
		}),
		serializers.ArrayProjection(0, func(context.Context, int) (serializers.Projection, error) {
			t.Fatal("empty array read")
			return serializers.Projection{}, nil
		}),
	} {
		body, err := serializers.EncodeProjection(t.Context(), empty, serializers.Limits{MaxValues: 1, MaxDepth: 1, MaxDocumentBytes: 2})
		if err != nil || !(bytes.Equal(body, []byte(`{}`)) || bytes.Equal(body, []byte(`[]`))) {
			t.Fatalf("empty = %s %v", body, err)
		}
	}
}
