package serializers_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func BenchmarkJSONEncoding(b *testing.B) {
	for _, rows := range []int{1, 100} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			values := make([]serializers.Value, rows)
			for row := range values {
				members := make([]serializers.Member, 10)
				for column := range members {
					members[column] = serializers.MemberOf(fmt.Sprintf("field_%d", column), serializers.String("한글 <tag> & quote\"\n"))
				}
				object, err := serializers.NewObject(members...)
				if err != nil {
					b.Fatal(err)
				}
				values[row] = object.Value()
			}
			value, err := serializers.NewList(values...)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := serializers.Encode(value, serializers.Limits{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkJSONNestedConstruction(b *testing.B) {
	for _, depth := range []int{8, 64} {
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			leaf := serializers.String(strings.Repeat("x", 128))
			b.ReportAllocs()
			for b.Loop() {
				value := leaf
				for level := 0; level < depth; level++ {
					object, err := serializers.NewObject(serializers.MemberOf("child", value))
					if err != nil {
						b.Fatal(err)
					}
					value = object.Value()
				}
			}
		})
	}
}
