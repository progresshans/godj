package output

import (
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

// ModelView derives output from the same model source as a selected read view.
// The serializer remains an explicit output allowlist. A different source,
// invalid view or absent selected value fails before any response is published;
// it never falls back to a model's hidden field, zero value or input default.
func ModelView[M any](manager orm.Manager[M], spec serializers.Spec) Shape[orm.ModelView[M]] {
	model, err := manager.Metadata()
	if err != nil {
		return Shape[orm.ModelView[M]]{node: &node{err: err}}
	}
	encoder, err := serializers.NewModelEncoder(spec, model, func(view orm.ModelView[M], field ir.Field) (query.Value, bool) {
		return view.ReadField(field)
	})
	if err != nil {
		return Shape[orm.ModelView[M]]{node: &node{err: err}}
	}
	shape := Model(encoder)
	shape.project = func(view orm.ModelView[M]) (serializers.Projection, error) {
		source, valid := view.Model()
		if !valid || !source.Equal(model) {
			return serializers.Projection{}, responseError("model view source does not match output declaration", nil)
		}
		return encoder.Project(view), nil
	}
	return shape
}
