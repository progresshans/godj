package output

import (
	"net/http"
	"strings"
)

// NoContent declares an empty 204 response. Prepare retains the same exact
// declaration ownership as JSON outputs, without inventing a JSON schema.
// The unit Go value carries no response data. This output has no JSON encoding.
func NoContent() Output[struct{}] {
	return Output[struct{}]{owner: &responseOwner{noContent: true}}
}

// IsNoContent identifies the closed no-content representation. A zero Output
// is not a valid declaration; Components and response preparation reject it.
func (o Output[T]) IsNoContent() bool { return o.owner != nil && o.owner.noContent }

func validateNoContentHeaders(header http.Header) error {
	for name := range header {
		switch strings.ToLower(name) {
		case "content-length", "transfer-encoding", "trailer":
			return responseError("no-content output cannot declare message framing headers", nil)
		}
	}
	return nil
}
