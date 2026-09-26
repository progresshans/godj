package postgres

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/progresshans/godj/internal/migrationdefault"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func compilePostgresMigrationDefault(field ir.Field) (string, error) {
	if field.Default == nil || field.PrimaryKey || field.Relation != nil {
		return "", errors.New("migration backfill requires a scalar default")
	}
	value, err := migrationdefault.Value(*field.Default)
	if err != nil {
		return "", err
	}
	// Preserve the ordinary write adapter's JSONB/range rejection.
	if _, err := postgresValue(value); err != nil {
		return "", err
	}
	quote := func(text string) (string, error) {
		if strings.ContainsRune(text, 0) {
			return "", errors.New("PostgreSQL migration text default contains NUL")
		}
		var literal strings.Builder
		literal.WriteString("E'")
		for _, character := range text {
			switch character {
			case '\\':
				literal.WriteString(`\\`)
			case '\'':
				literal.WriteString("''")
			case ';':
				literal.WriteString(`\073`)
			default:
				if unicode.IsControl(character) {
					fmt.Fprintf(&literal, `\u%04X`, character)
				} else {
					literal.WriteRune(character)
				}
			}
		}
		literal.WriteByte('\'')
		return literal.String(), nil
	}
	var text, cast string
	switch value.Kind() {
	case query.ValueInteger:
		number, _ := value.Integer()
		return strconv.FormatInt(number, 10), nil
	case query.ValueBoolean:
		boolean, _ := value.Boolean()
		return strconv.FormatBool(boolean), nil
	case query.ValueString:
		text, _ = value.String()
	case query.ValueFloat:
		number, _ := value.Float()
		text = strconv.FormatFloat(number, 'g', -1, 64)
		if math.IsInf(number, 1) {
			text = "Infinity"
		} else if math.IsInf(number, -1) {
			text = "-Infinity"
		}
		cast = "double precision"
	case query.ValueDecimal:
		number, _ := value.Decimal()
		text, cast = number.String(), "numeric"
	case query.ValueUUID:
		identifier, _ := value.UUID()
		text, cast = identifier.String(), "uuid"
	case query.ValueJSON:
		document, _ := value.JSON()
		text, cast = document.Text, "jsonb"
	case query.ValueDuration:
		elapsed, _ := value.Duration()
		text, cast = fmt.Sprintf("%d days %d microseconds", elapsed.Days, elapsed.Microseconds), "interval"
	case query.ValueDate:
		date, _ := value.Date()
		text, cast = date.String(), "date"
	case query.ValueTime:
		clock, _ := value.Time()
		text, cast = clock.String(), "time"
	case query.ValueDateTime:
		instant, _ := value.DateTime()
		text, cast = instant.Format(time.RFC3339Nano), "timestamp with time zone"
	default:
		return "", errors.New("unsupported PostgreSQL migration default storage")
	}
	literal, err := quote(text)
	if err != nil {
		return "", err
	}
	if cast != "" {
		literal += "::" + cast
	}
	return literal, nil
}
