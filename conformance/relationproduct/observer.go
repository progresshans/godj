// Package relationproduct exposes the checked-in generated REL-001 project
// binding to the GoDj conformance adapter. It deliberately imports only the
// generated project bridge and does not read reference oracles or static
// fixtures.
package relationproduct

import (
	"github.com/progresshans/godj/conformance/relationproduct/project"
	"github.com/progresshans/godj/orm"
)

func Binding() (orm.ProjectBinding, error) {
	return project.Bind()
}
