package definition

import (
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestImageDimensionEncodingBudgetRunsBeforeCloning(t *testing.T) {
	for _, height := range []bool{false, true} {
		field := ir.Field{Kind: ir.FieldImage}
		dimension := "width_field"
		if height {
			field.HeightField = "dimension"
			dimension = "height_field"
		} else {
			field.WidthField = "dimension"
		}
		scanner := encodingSizeScanner{}
		if err := scanner.scanField("field", field); err != nil {
			t.Fatal(err)
		}
		empty := encodingSizeScanner{}
		if err := empty.scanField("field", ir.Field{Kind: ir.FieldImage}); err != nil {
			t.Fatal(err)
		}
		if scanner.lowerBound-empty.lowerBound != uint64(len(dimension)+6+len("dimension")) {
			t.Fatal("image references not charged in preclone wire admission")
		}
		exact := encodingSizeScanner{lowerBound: MaxDocumentBytes - scanner.lowerBound}
		if err := exact.scanField("field", field); err != nil {
			t.Fatal("exact reference budget rejected", err)
		}
		short := encodingSizeScanner{lowerBound: MaxDocumentBytes - scanner.lowerBound + 1}
		if err := short.scanField("field", field); err == nil {
			t.Fatal("reference bypassed encoding preflight")
		}
	}
}
