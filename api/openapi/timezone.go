package openapi

import (
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/timezone"
)

// TimeZone describes every name and alias in the pinned IANA database. The
// data identity and exact enum are shared by parameters and computed outputs.
func TimeZone() (Schema, error) { return timeZoneSchema(0, nil, "timezone") }

func QueryTimeZone(fallback *timezone.Zone) (Schema, error) {
	return timeZoneSchema(0, fallback, "query")
}

// A header must accommodate the entire name domain; a smaller budget must not
// silently publish an enum whose values the transport would reject.
func HeaderTimeZone(maximumBytes int, fallback *timezone.Zone) (Schema, error) {
	if maximumBytes < 1 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.timezone", "timezone header budget must be between 1 byte and 1 MiB")
	}
	return timeZoneSchema(maximumBytes, fallback, "header")
}

func timeZoneSchema(maximumBytes int, fallback *timezone.Zone, source string) (Schema, error) {
	names, err := timezone.Names()
	if err != nil {
		return Schema{}, err
	}
	var name *string
	if fallback != nil {
		if !fallback.Valid() {
			return Schema{}, schemaConfigError(source+".default", "timezone default must be a name in the pinned IANA database")
		}
		name = new(fallback.Name())
	}
	base, err := stringChoicesSchema(names, maximumBytes, name, source)
	if err != nil {
		return Schema{}, err
	}
	identity, err := serializers.NewObject(
		serializers.MemberOf("database", serializers.String("IANA")),
		serializers.MemberOf("version", serializers.String(timezone.DataVersion)),
		serializers.MemberOf("sha256", serializers.String(timezone.DataSHA256)),
	)
	if err != nil {
		return Schema{}, err
	}
	return schemaAnnotate(base, serializers.MemberOf("x-godj-timezone", identity.Value()))
}
