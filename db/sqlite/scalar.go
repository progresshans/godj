package sqlite

import (
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
	"math"
)

func sqliteValue(value query.Value) (any, error) {
	if identifier, ok := value.UUID(); ok {
		return identifier.Hex(), nil
	}
	if number, ok := value.Decimal(); ok {
		return decimalstorage.Encode(number)
	}
	if number, ok := value.Float(); ok && math.IsNaN(number) {
		return nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Detail: "SQLite cannot store or compare NaN without losing its value"}
	}
	if elapsed, ok := value.Duration(); ok {
		micros, err := elapsed.TotalMicroseconds()
		if err != nil {
			return nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Detail: "duration exceeds SQLite signed int64 microseconds"}
		}
		return micros, nil
	}
	if instant, ok := value.DateTime(); ok {
		return instant.Format(temporal.SQLiteLayout), nil
	}
	return value.DatabaseValue()
}
