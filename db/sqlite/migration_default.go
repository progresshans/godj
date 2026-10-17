package sqlite

import (
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/progresshans/godj/internal/migrationdefault"
	"github.com/progresshans/godj/schema/ir"
)

// The same expression is used by execution and sqlmigrate. Encode delimiters
// and controls as bytes to preserve the semicolon/control-free SQL body contract.
func compileSQLiteMigrationDefault(field ir.Field) (string, error) {
	if field.Default == nil || field.PrimaryKey || field.Relation != nil {
		return "", errors.New("migration backfill requires a scalar default")
	}
	value, err := migrationdefault.Value(*field.Default)
	if err != nil {
		return "", err
	}
	stored, err := sqliteValue(value)
	if err != nil {
		return "", err
	}
	switch value := stored.(type) {
	case string:
		if strings.ContainsRune(value, ';') || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "CAST(X'" + hex.EncodeToString([]byte(value)) + "' AS TEXT)", nil
		}
		return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
	case []byte:
		return "X'" + hex.EncodeToString(value) + "'", nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case bool:
		if value {
			return "1", nil
		}
		return "0", nil
	case float64:
		if math.IsInf(value, 1) {
			return "9e999", nil
		}
		if math.IsInf(value, -1) {
			return "-9e999", nil
		}
		return "CAST('" + strconv.FormatFloat(value, 'g', -1, 64) + "' AS REAL)", nil
	default:
		return "", errors.New("unsupported SQLite migration default storage")
	}
}
