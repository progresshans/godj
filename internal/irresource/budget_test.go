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

func TestTimePayloadConsumesPerStringAndAggregateBudgets(t *testing.T) {
	for _, kind := range []ir.ScalarKind{ir.ScalarTime, ir.ScalarInteger} {
		for _, choice := range []bool{false, true} {
			scalar := ir.Scalar{Kind: kind, Time: "12:34:56.123456"}
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
			if bytes < uint64(len(scalar.Time)) {
				t.Fatal("time payload not charged")
			}
			for _, cap := range []uint64{bytes - 1, bytes} {
				budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: cap})
				if err := budget.ScanField("field", field); (err == nil) != (cap == bytes) {
					t.Fatalf("aggregate time cap %d: %v", cap, err)
				}
			}
			if choice {
				field.Choices[0].Value.Time = strings.Repeat("x", 17)
			} else {
				scalar.Time = strings.Repeat("x", 17)
			}
			budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: 1000})
			if err := budget.ScanField("field", field); err == nil || !strings.Contains(err.Error(), "string of 17 bytes") {
				t.Fatalf("time payload escaped string budget: %v", err)
			}
		}
	}
}

func TestDurationPayloadConsumesPerStringAndAggregateBudgets(t *testing.T) {
	for _, kind := range []ir.ScalarKind{ir.ScalarDuration, ir.ScalarInteger} {
		for _, choice := range []bool{false, true} {
			scalar := ir.Scalar{Kind: kind, Duration: "12:34:56.123456"}
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
			if bytes < uint64(len(scalar.Duration)) {
				t.Fatal("duration payload not charged")
			}
			for _, cap := range []uint64{bytes - 1, bytes} {
				budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: cap})
				if err := budget.ScanField("field", field); (err == nil) != (cap == bytes) {
					t.Fatalf("aggregate duration cap %d: %v", cap, err)
				}
			}
			if choice {
				field.Choices[0].Value.Duration = strings.Repeat("x", 17)
			} else {
				scalar.Duration = strings.Repeat("x", 17)
			}
			budget := irresource.New(irresource.Limits{Fields: 10, StringBytes: 16, Nodes: 100, Bytes: 1000})
			if err := budget.ScanField("field", field); err == nil || !strings.Contains(err.Error(), "string of 17 bytes") {
				t.Fatalf("duration payload escaped string budget: %v", err)
			}
		}
	}
}
