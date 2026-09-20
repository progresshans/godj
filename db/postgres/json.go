package postgres

import (
	"database/sql"

	"github.com/progresshans/godj/jsonvalue"
)

// Native JSONB reaches database/sql as text/bytes. Keep SQL NULL separate
// from the JSON null document and restore exact JSON values before ORM scans.
type jsonDestination struct{ destination any }

func (target jsonDestination) Scan(raw any) error {
	fail := func(err error) error {
		if scanner, ok := target.destination.(sql.Scanner); ok {
			_ = scanner.Scan(nil)
		}
		if destination, ok := target.destination.(*any); ok && destination != nil {
			*destination = nil
		}
		return err
	}
	var canonical any
	if raw != nil {
		var text string
		switch value := raw.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		default:
			return fail(jsonvalue.ErrInvalid)
		}
		value, err := jsonvalue.Parse([]byte(text))
		if err != nil {
			return fail(jsonvalue.ErrInvalid)
		}
		canonical = value
	}
	switch destination := target.destination.(type) {
	case sql.Scanner:
		return destination.Scan(canonical)
	case *any:
		if destination == nil {
			return jsonvalue.ErrInvalid
		}
		*destination = canonical
		return nil
	default:
		return jsonvalue.ErrInvalid
	}
}
