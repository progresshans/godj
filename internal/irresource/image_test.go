package irresource_test

import (
	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"testing"
)

func TestImageDimensionsConsumeStringAndAggregateBudgets(t *testing.T) {
	for _, kind := range []ir.FieldKind{ir.FieldImage, ir.FieldChar} {
		for _, height := range []bool{false, true} {
			field := ir.Field{Kind: kind}
			if height {
				field.HeightField = strings.Repeat("x", 16)
			} else {
				field.WidthField = strings.Repeat("x", 16)
			}
			limits := irresource.Limits{Fields: 10, Nodes: 10, Bytes: 1000, StringBytes: 16}
			budget := irresource.New(limits)
			if err := budget.ScanField("field", field); err != nil {
				t.Fatal(err)
			}
			_, size := budget.Counts()
			if size != uint64(16+len(kind)) {
				t.Fatal("dimension reference omitted from resource admission")
			}
			limits.Bytes = size - 1
			short := irresource.New(limits)
			if err := short.ScanField("field", field); err == nil {
				t.Fatal("dimension bypassed aggregate cap")
			}
			limits.Bytes = size
			limits.StringBytes = 15
			short = irresource.New(limits)
			if err := short.ScanField("field", field); err == nil {
				t.Fatal("dimension bypassed string cap")
			}
		}
	}
}
