package postgres

import (
	"database/sql"

	"github.com/progresshans/godj/uuid"
)

// Native UUID is exposed as canonical driver text by database/sql. Keep the
// PostgreSQL representation at this boundary; ORM scanners receive a UUID value.
type uuidDestination struct{ destination any }

func (target uuidDestination) Scan(raw any) error {
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
			return fail(uuid.ErrInvalid)
		}
		value, err := uuid.Parse(text)
		if err != nil || value.String() != text {
			return fail(uuid.ErrInvalid)
		}
		canonical = value
	}
	switch destination := target.destination.(type) {
	case sql.Scanner:
		return destination.Scan(canonical)
	case *any:
		if destination == nil {
			return uuid.ErrInvalid
		}
		*destination = canonical
		return nil
	default:
		return uuid.ErrInvalid
	}
}
