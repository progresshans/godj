// Package timezone provides explicit, immutable IANA time zone names. Its
// pinned data is embedded in the binary and does not use ambient host settings.
package timezone

import (
	"encoding/json"
	"time"

	"github.com/progresshans/godj/internal/zonerules"
)

const (
	DataVersion      = zonerules.Version
	MaximumNameBytes = zonerules.MaximumNameBytes
)

var ErrInvalid = zonerules.ErrInvalid

// Zone is a comparable name from the pinned IANA database. The zero value is
// invalid; UTC is explicit. No caller-owned location or byte slice is retained.
type Zone struct{ name string }

func Parse(name string) (Zone, error) {
	if _, err := zonerules.Borrow(name); err != nil {
		return Zone{}, err
	}
	return Zone{name: name}, nil
}

func UTC() Zone { return Zone{name: "UTC"} }

func (value Zone) Name() string { return value.name }
func (value Zone) Valid() bool  { return value.Validate() == nil }
func (value Zone) Validate() error {
	_, err := zonerules.Borrow(value.name)
	return err
}

func (value Zone) String() string {
	if !value.Valid() {
		return "invalid time zone"
	}
	return value.name
}

// Location returns an owned location. Replacing the pointed-to value cannot
// change this Zone, another caller's location or the shared timezone catalog.
// The unexported transition tables in time.Location are read-only.
func (value Zone) Location() (*time.Location, error) {
	location, err := zonerules.Borrow(value.name)
	if err != nil {
		return nil, err
	}
	owned := *location
	return &owned, nil
}

func Names() ([]string, error) { return zonerules.Names() }

func (value Zone) MarshalText() ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return []byte(value.name), nil
}

func (value *Zone) UnmarshalText(raw []byte) error {
	if value == nil || len(raw) > MaximumNameBytes {
		return ErrInvalid
	}
	parsed, err := Parse(string(raw))
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func (value Zone) MarshalJSON() ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value.name)
}

func (value *Zone) UnmarshalJSON(raw []byte) error {
	if value == nil || len(raw) > MaximumNameBytes*6+2 {
		return ErrInvalid
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return ErrInvalid
	}
	return value.UnmarshalText([]byte(name))
}
