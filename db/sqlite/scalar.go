package sqlite

import (
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
)

func sqliteValue(value query.Value) (any, error) {
	if instant, ok := value.DateTime(); ok {
		return instant.Format(temporal.SQLiteLayout), nil
	}
	return value.DatabaseValue()
}
