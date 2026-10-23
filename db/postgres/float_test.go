package postgres

import (
	"math"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresFloatNativeParametersAndPhysicalCatalog(t *testing.T) {
	for _, number := range []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1)} {
		value := query.Float(number)
		want, _ := value.Float()
		raw, err := postgresValue(value)
		actual, ok := raw.(float64)
		if err != nil || !ok || math.Float64bits(actual) != math.Float64bits(want) {
			t.Fatal("native double parameter changed bits")
		}
	}
	field := ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldFloat, Nullable: true}
	ddl, err := compilePostgresMigrationColumn(field)
	if err != nil || ddl != `"amount" DOUBLE PRECISION NULL` {
		t.Fatalf("Float DDL: %s %v", ddl, err)
	}
	model := postgresMigrationTestAuthorModel()
	model.Fields = append(model.Fields, field)
	exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
	if err := assertPostgresMigrationModelCatalog(exact, "product_schema", model, nil); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"float4", "numeric", "int8", "text"} {
		bad := clonePostgresMigrationTestCatalog(exact)
		bad.columns[2].typeName = kind
		if err := assertPostgresMigrationModelCatalog(bad, "product_schema", model, nil); err == nil {
			t.Fatal("Float physical precision/type drift accepted")
		}
	}
}
