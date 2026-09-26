package queryplan

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// PackIntegerMembership preserves an integer set as one bound parameter. SQL
// syntax and the enclosing array representation remain dialect-owned. Small
// lists keep their ordinary scalar parameters; other kinds use their existing
// compilation paths. NULL handling belongs to MembershipValues before this.
func PackIntegerMembership(values []query.Value, open, close byte) (string, bool) {
	if len(values) <= 999 {
		return "", false
	}
	var encoded strings.Builder
	encoded.WriteByte(open)
	for i, value := range values {
		integer, ok := value.Integer()
		if !ok || value.IsNull() {
			return "", false
		}
		if i > 0 {
			encoded.WriteByte(',')
		}
		encoded.WriteString(strconv.FormatInt(integer, 10))
	}
	encoded.WriteByte(close)
	return encoded.String(), true
}
