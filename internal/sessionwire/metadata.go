package sessionwire

import (
	"fmt"
	"strings"
	"time"

	"github.com/progresshans/godj/sessions"
)

// Metadata is a decoded value/expiry observation without a bearer identity.
// It cannot be passed to a Store as a valid authenticated session record.
type Metadata struct{ state *metadataState }
type metadataState struct {
	values         map[string]string
	absolute, idle time.Time
}

func (Metadata) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("sessionwire.Metadata{redacted}"))
}
func (metadata Metadata) Value(name string) (string, bool) {
	if metadata.state == nil {
		return "", false
	}
	value, present := metadata.state.values[name]
	return value, present
}
func (metadata Metadata) Expired(at time.Time) bool {
	return metadata.state == nil || !metadata.state.absolute.After(at) || !metadata.state.idle.After(at)
}

func Inspect(payload string, limits sessions.Limits) (Metadata, error) {
	placeholder, err := sessions.ParseID(strings.Repeat("A", 43))
	if err != nil {
		return Metadata{}, err
	}
	record, err := Decode(payload, placeholder, limits)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{state: &metadataState{values: record.Values(), absolute: record.AbsoluteExpiresAt(), idle: record.IdleExpiresAt()}}, nil
}
