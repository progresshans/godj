package codegen

import (
	"fmt"
	"strconv"

	"github.com/progresshans/godj/schema/ir"
)

func queryFieldReferenceLiteral(field ir.Field) string {
	if field.Kind == ir.FieldDecimal && field.Decimal != nil {
		return fmt.Sprintf("query.NewDecimalFieldRef(%s, %s, %t, %d, %d)", strconv.Quote(field.Name), strconv.Quote(field.Column), field.Nullable, field.Decimal.MaxDigits, field.Decimal.DecimalPlaces)
	}
	return fmt.Sprintf("query.NewFieldRef(%s, %s, query.Field%s, %t)",
		strconv.Quote(field.Name), strconv.Quote(field.Column), fieldRenderKind(field.Kind).queryValue, field.Nullable)
}

// fieldRenderSpec is the shared lowering of one normalized IR kind into Go
// storage, query values, typed fields and joined-row scan storage. Root scans
// and eager scans consume the same mapping; nullability stays a field property.
type fieldRenderSpec struct {
	goType           string
	queryValue       string
	ormField         string
	nullableORMField string
	sqlHolder        string
	sqlValue         string
	irKind           string
}

func fieldRenderKind(kind ir.FieldKind) fieldRenderSpec {
	switch kind {
	case ir.FieldAuto:
		return fieldRenderSpec{"int64", "Integer", "AutoField", "", "sql.NullInt64", "Int64", "ir.FieldAuto"}
	case ir.FieldInteger:
		return fieldRenderSpec{"int64", "Integer", "IntegerField", "NullableIntegerField", "sql.NullInt64", "Int64", "ir.FieldInteger"}
	case ir.FieldChar:
		return fieldRenderSpec{"string", "String", "StringField", "NullableStringField", "sql.NullString", "String", "ir.FieldChar"}
	case ir.FieldUUID:
		return fieldRenderSpec{"_godjuuid.UUID", "UUID", "UUIDField", "NullableUUIDField", "orm.NullableUUIDScanner", "UUID", "ir.FieldUUID"}
	case ir.FieldDecimal:
		return fieldRenderSpec{"_godjdecimal.Decimal", "Decimal", "DecimalField", "NullableDecimalField", "orm.NullableDecimalScanner", "Decimal", "ir.FieldDecimal"}
	case ir.FieldFloat:
		return fieldRenderSpec{"float64", "Float", "FloatField", "NullableFloatField", "orm.NullableFloatScanner", "Float", "ir.FieldFloat"}
	case ir.FieldDuration:
		return fieldRenderSpec{"_godjduration.Duration", "Duration", "DurationField", "NullableDurationField", "orm.NullableDurationScanner", "Duration", "ir.FieldDuration"}
	case ir.FieldTime:
		return fieldRenderSpec{"_godjclock.Time", "Time", "TimeField", "NullableTimeField", "orm.NullableTimeScanner", "Time", "ir.FieldTime"}
	case ir.FieldDate:
		return fieldRenderSpec{"_godjcalendar.Date", "Date", "DateField", "NullableDateField", "orm.NullableDateScanner", "Date", "ir.FieldDate"}
	case ir.FieldDateTime:
		return fieldRenderSpec{"_godjtime.Time", "DateTime", "DateTimeField", "NullableDateTimeField", "orm.NullableDateTimeScanner", "Time", "ir.FieldDateTime"}
	case ir.FieldText:
		return fieldRenderSpec{"string", "String", "StringField", "NullableStringField", "sql.NullString", "String", "ir.FieldText"}
	case ir.FieldBoolean:
		return fieldRenderSpec{"bool", "Boolean", "BooleanField", "NullableBooleanField", "sql.NullBool", "Bool", "ir.FieldBoolean"}
	case ir.FieldForeignKey:
		return fieldRenderSpec{"int64", "Integer", "", "", "sql.NullInt64", "Int64", "ir.FieldForeignKey"}
	default:
		// Public generators reject unsupported kinds through ir.Normalize before
		// rendering. Keep this fallback invalid rather than guessing a type.
		return fieldRenderSpec{goType: "struct{}", sqlHolder: "struct{}", irKind: strconv.Quote(string(kind))}
	}
}

func (spec fieldRenderSpec) fieldType(nullable bool) string {
	if nullable {
		return spec.nullableORMField
	}
	return spec.ormField
}
