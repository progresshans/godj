package backend

import (
	"context"
	"github.com/progresshans/godj/schema/ir"
)

// ManyToManySchemaEditor consumes the sealed, columnless before/after models.
// Support is negotiated separately from stored scalar/FK column operations.
type ManyToManySchemaEditor interface {
	AlterManyToMany(context.Context, ir.Model, ir.Model) error
}
