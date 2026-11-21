package orm

import (
	"context"
	"errors"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

type manyCancelNilSession struct {
	db.RelationSession
	cancel context.CancelFunc
}

func (s manyCancelNilSession) Query(context.Context, query.Plan) (db.Rows, error) {
	s.cancel()
	return nil, nil
}
func TestManyToManyPreservesCancellationWhenBackendReturnsNilRows(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state := &manyToManyState{model: ir.Model{DBTable: "links"}, key: ir.Field{Name: "id", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}, source: ir.Field{Name: "source", Column: "source_id", Kind: ir.FieldForeignKey}, target: ir.Field{Name: "target", Column: "target_id", Kind: ir.FieldForeignKey}}
	result, err := state.rows(ctx, manyCancelNilSession{cancel: cancel}, 0)
	if result != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("nil row cancellation cause lost", result, err)
	}
}
