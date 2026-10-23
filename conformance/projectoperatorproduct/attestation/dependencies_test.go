//go:build darwin || linux

package attestation

import (
	"testing"

	"github.com/progresshans/godj/conformance/internal/sourceaudit"
)

func TestSourceBindingOwnsNativeProductDependencies(t *testing.T) {
	// The external fixture imports these reusable packages and the global CLI.
	// Its own generated source is already owned by the captured harness files.
	sourceaudit.RequireDependencies(t, sourcePathOwned, sourceSymlinkMayHideOwnedSource,
		"./cmd/godj", "./examples/article/cmd/projectrunner", "./examples/article/cmd/site", "./identity/modeldef")
}
