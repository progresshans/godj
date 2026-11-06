package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

func TestFunctionZoneIsImmutableIdentityAndRequiresAnInstant(t *testing.T) {
	seoul, err := timezone.Parse("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	newYork, err := timezone.Parse("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for function := query.FunctionDate; function <= query.FunctionTruncSecond; function++ {
		instant := temporalNull(t, query.FieldDateTime)
		arguments := []query.ScalarExpression{instant}
		local, err := query.FunctionExpressionInZone(function, seoul, arguments...)
		if err != nil {
			t.Fatal(function, err)
		}
		arguments[0] = query.ScalarExpression{}
		zone, explicit := local.FunctionZone()
		if !explicit || zone != seoul || !local.Nullable() || local.Depth() != 2 || local.NodeCount() != 2 {
			t.Fatal("explicit zone changed operand ownership, nullability or budget")
		}
		if err := zone.UnmarshalText([]byte("UTC")); err != nil {
			t.Fatal(err)
		}
		retained, _ := local.FunctionZone()
		if retained != seoul {
			t.Fatal("zone accessor exposed AST storage")
		}
		copy, err := query.FunctionExpressionInZone(function, seoul, instant)
		if err != nil || !copy.Equal(local) {
			t.Fatal("same zone operation differs", err)
		}
		other, err := query.FunctionExpressionInZone(function, newYork, instant)
		if err != nil || local.Equal(other) {
			t.Fatal("time zone was omitted from identity", err)
		}
		defaultUTC := calendarFunction(t, function, instant)
		explicitUTC, err := query.FunctionExpressionInZone(function, timezone.UTC(), instant)
		if err != nil || local.Equal(defaultUTC) || local.Equal(explicitUTC) || defaultUTC.Equal(explicitUTC) {
			t.Fatal("explicit operation collapsed into another calendar frame", err)
		}
		if _, explicit := defaultUTC.FunctionZone(); explicit {
			t.Fatal("default operation acquired explicit state")
		}
		for _, kind := range []query.FieldKind{query.FieldDate, query.FieldTime, query.FieldDuration, query.FieldInteger, query.FieldString} {
			if _, err := query.FunctionExpressionInZone(function, seoul, temporalNull(t, kind)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatalf("function %d inferred an instant for %s: %v", function, kind, err)
			}
		}
		if _, err := query.FunctionExpressionInZone(function, timezone.Zone{}, instant); !errors.Is(err, timezone.ErrInvalid) {
			t.Fatal("zero zone silently used UTC", err)
		}
	}
	for _, value := range []query.ScalarExpression{{}, temporalNull(t, query.FieldDateTime)} {
		if _, explicit := value.FunctionZone(); explicit {
			t.Fatal("non-function exposed zone metadata")
		}
	}
	instant := temporalNull(t, query.FieldDateTime)
	for _, arguments := range [][]query.ScalarExpression{nil, {instant, instant}, {{}}} {
		if _, err := query.FunctionExpressionInZone(query.FunctionYear, seoul, arguments...); err == nil {
			t.Fatal("explicit zone bypassed arity/input checks")
		}
	}
	if _, err := query.FunctionExpressionInZone(255, seoul, instant); err == nil {
		t.Fatal("explicit zone accepted unknown function")
	}
}
