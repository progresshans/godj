package serializers_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func BenchmarkJSONRejectedString(b *testing.B) {
	value := serializers.String(strings.Repeat("\x01", 64<<10))
	b.ReportAllocs()
	for b.Loop() {
		if result, err := serializers.Encode(value, serializers.Limits{MaxDocumentBytes: 64}); result != nil || err == nil {
			b.Fatal("oversized JSON output was accepted")
		}
	}
}

func BenchmarkSerializerUnknownErrors(b *testing.B) {
	field, err := serializers.StringField("allowed", serializers.WithRequired(false))
	if err != nil {
		b.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{16, 256, 1024} {
		b.Run(fmt.Sprintf("fields_%d", count), func(b *testing.B) {
			members := make([]serializers.Member, count)
			for index := range members {
				members[index] = serializers.MemberOf(fmt.Sprintf("unknown_%04d", index), serializers.Boolean(true))
			}
			object, err := serializers.NewObject(members...)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				result, err := spec.Bind(object, serializers.ModeFull)
				if err != nil || result.Valid() || result.Errors().Len() != count {
					b.Fatalf("bind: %v", err)
				}
			}
		})
	}
}

func BenchmarkJSONDecoding(b *testing.B) {
	for _, count := range []int{16, 256} {
		b.Run(fmt.Sprintf("members_%d", count), func(b *testing.B) {
			var document strings.Builder
			document.WriteByte('{')
			for index := range count {
				if index != 0 {
					document.WriteByte(',')
				}
				fmt.Fprintf(&document, `"field_%d":["한글",true,123]`, index)
			}
			document.WriteByte('}')
			input := []byte(document.String())
			b.ReportAllocs()
			for b.Loop() {
				result, err := serializers.DecodeObject(input, serializers.Limits{})
				if err != nil || result.Len() != count {
					b.Fatalf("decode: %v", err)
				}
			}
		})
	}
}
