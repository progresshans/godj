package migrations

import (
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestLoadedImageDimensionsConsumePrecloneBudget(t *testing.T) {
	for _, height := range []bool{false, true} {
		field := ir.Field{}
		if height {
			field.HeightField = "ab"
		} else {
			field.WidthField = "ab"
		}
		budget := loadedResourceBudget{bytes: maxLoadedDefinitionSetBytes - 2}
		loadedScanFieldResource(&budget, Migration{App: "images", Name: "0001_initial"}, 0, "AddField", "field", field)
		if budget.byteOverflow || budget.bytes != maxLoadedDefinitionSetBytes {
			t.Fatal("exact loaded image budget", budget.bytes, budget.byteOverflow)
		}
		budget = loadedResourceBudget{bytes: maxLoadedDefinitionSetBytes - 1}
		loadedScanFieldResource(&budget, Migration{App: "images", Name: "0001_initial"}, 0, "AddField", "field", field)
		if !budget.byteOverflow {
			t.Fatal("image references bypassed loaded definition cap")
		}
	}
}
