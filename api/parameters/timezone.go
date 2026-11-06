package parameters

import (
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/timezone"
)

// TimeZone accepts an exact name or alias from the pinned IANA database. Empty
// text, ambient local time, offset strings, paths and unknown names are invalid.
// Omission follows Required, Optional or Default independently of this codec.
func TimeZone() Codec[timezone.Zone] {
	return Codec[timezone.Zone]{
		schema: openapi.QueryTimeZone, headerSchema: openapi.HeaderTimeZone,
		decode: func(raw string) (timezone.Zone, bool) {
			value, err := timezone.Parse(raw)
			return value, err == nil
		},
	}
}
