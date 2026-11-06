//go:build darwin || linux

package attestation

import (
	"testing"

	"github.com/progresshans/godj/conformance/internal/sourceaudit"
)

func TestSourceBindingOwnsNativeProductDependencies(t *testing.T) {
	sourceaudit.RequireDependencies(t, sourcePathOwned, sourceSymlinkMayHideOwnedSource,
		"./conformance/systemstate/worker/cmd", "./conformance/systemstate/multiruntimeworker/cmd", "./examples/article/cmd/site")
}
