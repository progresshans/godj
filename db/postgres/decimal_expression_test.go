package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
)

func TestNumericResultAdaptsWideDisplayScaleWithoutWideningInputPolicy(t *testing.T) {
	for _, text := range []string{"1.23" + strings.Repeat("0", 5000), "-0." + strings.Repeat("0", 9000), "0.125" + strings.Repeat("0", 16000)} {
		if _, err := decimal.Parse(text); !errors.Is(err, decimal.ErrRange) {
			t.Fatal("public input byte limit was weakened", err)
		}
		expected, err := decimal.Parse(strings.TrimRight(text, "0"))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := parseNumericResult(text)
		if err != nil || !actual.Equal(expected) {
			t.Fatal("native display scale changed a canonical value", actual, err)
		}
		var cell any
		if err := (decimalDestination{destination: &cell}).Scan([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if value, ok := cell.(decimal.Decimal); !ok || !value.Equal(expected) {
			t.Fatal("native row adapter lost the Decimal type")
		}
	}
	for _, text := range []string{
		"1." + strings.Repeat("0", 4094) + ".", "1.." + strings.Repeat("0", 5000),
		"1e0." + strings.Repeat("0", 5000), "NaN." + strings.Repeat("0", 5000),
		"1." + strings.Repeat("0", 18000), strings.Repeat("9", 1001) + "." + strings.Repeat("0", 5000),
		"1.234" + strings.Repeat("0", 5000),
	} {
		cell := orm.NewNullableDecimalScanner(5, 2)
		cell.Valid, cell.Decimal = true, decimal.Decimal{Coefficient: "1"}
		if err := (decimalDestination{destination: &cell}).Scan(text); err == nil || cell.Valid || cell.Decimal != (decimal.Decimal{}) {
			t.Fatal("malformed, wide or out-of-field value was accepted or retained old state")
		}
	}
}
