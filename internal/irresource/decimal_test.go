package irresource_test

import (
	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"testing"
)

func TestNumericPayloadBudgetsIncludeActiveAndInactiveArms(t *testing.T) {
	for _, kind := range []ir.ScalarKind{ir.ScalarDecimal, ir.ScalarFloat, ir.ScalarString} {
		for _, arm := range []string{"decimal", "float"} {
			for _, choice := range []bool{false, true} {
				scalar := ir.Scalar{Kind: kind}
				if arm == "decimal" {
					scalar.Decimal = strings.Repeat("1", 16)
				} else {
					scalar.FloatBits = strings.Repeat("1", 16)
				}
				field := ir.Field{}
				if choice {
					field.Choices = []ir.Choice{{Value: scalar}}
				} else {
					field.Default = &scalar
				}
				limits := irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: 1000}
				base := irresource.New(limits)
				if err := base.ScanField("field", field); err != nil {
					t.Fatal(err)
				}
				_, charged := base.Counts()
				if charged < 16 {
					t.Fatal("numeric payload not charged")
				}
				limits.Bytes = charged - 1
				limited := irresource.New(limits)
				if err := limited.ScanField("field", field); err == nil {
					t.Fatal("numeric payload escaped aggregate budget")
				}
				limits.Bytes = charged
				exact := irresource.New(limits)
				if err := exact.ScanField("field", field); err != nil {
					t.Fatal(err)
				}
				limits.StringBytes = 15
				short := irresource.New(limits)
				if err := short.ScanField("field", field); err == nil {
					t.Fatal("numeric payload escaped per-string budget")
				}
			}
		}
	}
	budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 1, Bytes: 1000})
	if err := budget.ScanField("field", ir.Field{Decimal: &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}}); err == nil {
		t.Fatal("decimal facet bypassed node cap")
	}
}
