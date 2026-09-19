package irresource_test

import (
	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"testing"
)

func TestDatePayloadConsumesPerStringAndAggregateBudgets(t *testing.T) {
	for _, kind := range []ir.ScalarKind{ir.ScalarDate, ir.ScalarInteger} {
		for _, choice := range []bool{false, true} {
			scalar := ir.Scalar{Kind: kind, Date: "2000-02-29"}
			field := ir.Field{}
			if choice {
				field.Choices = []ir.Choice{{Value: scalar}}
			} else {
				field.Default = &scalar
			}
			base := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: 1000})
			if err := base.ScanField("field", field); err != nil {
				t.Fatal(err)
			}
			_, bytes := base.Counts()
			if bytes < uint64(len(scalar.Date)) {
				t.Fatal("date payload not charged")
			}
			for _, cap := range []uint64{bytes - 1, bytes} {
				budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: cap})
				if err := budget.ScanField("field", field); (err == nil) != (cap == bytes) {
					t.Fatalf("aggregate date cap %d: %v", cap, err)
				}
			}
			if choice {
				field.Choices[0].Value.Date = strings.Repeat("x", 17)
			} else {
				scalar.Date = strings.Repeat("x", 17)
			}
			budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: 1000})
			if err := budget.ScanField("field", field); err == nil || !strings.Contains(err.Error(), "string of 17 bytes") {
				t.Fatalf("date payload escaped string budget: %v", err)
			}
		}
	}
}
