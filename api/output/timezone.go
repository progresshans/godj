package output

import (
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/timezone"
)

// TimeZone preserves the selected IANA name, including aliases. A zero Zone
// fails before any response is published; it is never replaced with UTC.
func TimeZone() Shape[timezone.Zone] {
	schema, err := openapi.TimeZone()
	return Shape[timezone.Zone]{node: &node{schema: schema, err: err}, project: func(value timezone.Zone) (serializers.Projection, error) {
		if !value.Valid() {
			return serializers.Projection{}, invalidValue("timezone", "output is not a name in the pinned IANA database")
		}
		return serializers.ValueProjection(serializers.String(value.Name())), nil
	}}
}
