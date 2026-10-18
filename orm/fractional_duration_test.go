package orm

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
	"github.com/progresshans/godj/query"
)

func TestFractionalDurationScannerRetainsResultAndFailureOwnership(t *testing.T) {
	value, err := durationcalc.FromMicroseconds(3).Divide(durationcalc.IntegerFactor(2))
	if err != nil {
		t.Fatal(err)
	}
	var scanner fractionalDurationScanner
	key := value.Key()
	if err := scanner.Scan(key); err != nil || !scanner.valid || scanner.value != duration.FromMicroseconds(2) {
		t.Fatal("fractional result did not round at its final scanner", scanner, err)
	}
	for index := range key {
		key[index] = 0
	}
	if scanner.value != duration.FromMicroseconds(2) {
		t.Fatal("scanner retained driver-owned bytes")
	}
	if err := scanner.Scan(key); !errors.Is(err, durationcalc.ErrKey) || scanner.valid || scanner.value != (duration.Duration{}) {
		t.Fatal("failed scan exposed the previous row", scanner, err)
	}
	if err := scanner.Scan(nil); err != nil || scanner.valid {
		t.Fatal("NULL became a zero Duration", err)
	}
	if err := scanner.Scan(int64(1)); !errors.Is(err, durationcalc.ErrKey) || scanner.valid {
		t.Fatal("fractional scanner accepted an untagged physical value", err)
	}
	var ordinary DurationScanner
	if err := ordinary.Scan(value.Key()); err == nil {
		t.Fatal("model Duration accepted an internal query fraction")
	}
}

func TestComputedDurationScalingUsesItsFractionalScanner(t *testing.T) {
	period, err := query.LiteralExpression(query.Duration(duration.FromMicroseconds(1)))
	if err != nil {
		t.Fatal(err)
	}
	divisor, err := query.LiteralExpression(query.Integer(3))
	if err != nil {
		t.Fatal(err)
	}
	scalar, err := query.Arithmetic(query.ArithmeticDivide, period, divisor)
	if err != nil {
		t.Fatal(err)
	}
	result, err := query.ScalarResult(scalar)
	if err != nil {
		t.Fatal(err)
	}
	factory, err := computedCell[duration.Duration](result, true)
	if err != nil {
		t.Fatal(err)
	}
	cell := factory()
	fraction, err := durationcalc.FromMicroseconds(1).Divide(durationcalc.IntegerFactor(3))
	if err != nil {
		t.Fatal(err)
	}
	if err := scanGroupValue(cell.destination, fraction.Key()); err != nil {
		t.Fatal("computed Duration could not decode its fractional result", err)
	}
	retained := cell.value()
	if value, present := retained.Get(); !present || value != duration.FromMicroseconds(0) {
		t.Fatal("computed fractional zero lost its presence or final rounding", value, present)
	}
	for _, invalid := range []any{[]byte("invalid"), int64(1)} {
		if err := scanGroupValue(cell.destination, fraction.Key()); err != nil {
			t.Fatal(err)
		}
		if err := scanGroupValue(cell.destination, invalid); !errors.Is(err, durationcalc.ErrKey) || cell.value().Valid() {
			t.Fatal("failed computed scan exposed the previous result", cell.value(), err)
		}
	}
	if err := scanGroupValue(cell.destination, fraction.Key()); err != nil {
		t.Fatal(err)
	}
	if err := scanGroupValue(cell.destination, nil); err != nil || cell.value().Valid() {
		t.Fatal("computed NULL became a zero Duration", cell.value(), err)
	}
	if value, present := retained.Get(); !present || value != duration.FromMicroseconds(0) {
		t.Fatal("later scans changed the previously published result", value, present)
	}
	requiredFactory, err := computedCell[duration.Duration](result, false)
	if err != nil {
		t.Fatal(err)
	}
	required := requiredFactory()
	if err := scanGroupValue(required.destination, fraction.Key()); err != nil {
		t.Fatal(err)
	}
	if err := scanGroupValue(required.destination, nil); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || required.value().Valid() {
		t.Fatal("required computed result accepted NULL or exposed its previous value", required.value(), err)
	}
}
