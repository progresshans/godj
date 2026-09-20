package sqlite

import (
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
)

func sqliteValue(value query.Value) (any, error) {
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
