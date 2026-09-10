package forms_test

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/forms"
)

func benchmarkFormSpec(b *testing.B, count int) forms.Spec {
	b.Helper()
	fields := make([]forms.Field, count)
	for index := range fields {
		field, err := forms.CharField(fmt.Sprintf("field_%d", index), forms.WithMaxLength(1))
		if err != nil {
			b.Fatal(err)
		}
		fields[index] = field
	}
	spec, err := forms.NewSpec(fields)
	if err != nil {
		b.Fatal(err)
	}
	return spec
}

func BenchmarkFormBindErrors(b *testing.B) {
	for _, count := range []int{16, 256} {
		b.Run(fmt.Sprintf("fields_%d", count), func(b *testing.B) {
			spec := benchmarkFormSpec(b, count)
			input := make(map[string][]string, count)
			for index := range count {
				input[fmt.Sprintf("field_%d", index)] = []string{"too long"}
			}
			data := forms.NewData(input)
			b.ReportAllocs()
			for b.Loop() {
				form, err := spec.Bind(data, nil)
				if err != nil || form.Valid() || form.Errors().Len() != count {
					b.Fatalf("bind: %v", err)
				}
			}
		})
	}
}

func BenchmarkFormResultAccess(b *testing.B) {
	spec := benchmarkFormSpec(b, 16)
	input := make(map[string][]string, 16)
	for index := range 16 {
		input[fmt.Sprintf("field_%d", index)] = []string{"x"}
	}
	form, err := spec.Bind(forms.NewData(input), nil)
	if err != nil || !form.Valid() {
		b.Fatalf("bind: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		value, present := form.Cleaned().String("field_0")
		initial, found := form.Initial().String("field_0")
		if !present || !found || value != "x" || initial != "" || !form.Errors().Empty() {
			b.Fatal("invalid form snapshot")
		}
	}
}
