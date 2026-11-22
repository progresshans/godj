package migrations

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// DeleteModel drops the exact historical model and its owned automatic
// intermediary tables. Reversing it recreates empty tables in their original
// schema position. BeforeModel names the next declared model, or is empty
// when the dropped model was last.
type DeleteModel struct {
	AppLabel    string
	Model       ir.Model
	BeforeModel string
}

func (DeleteModel) operation()     {}
func (DeleteModel) Kind() string   { return "DeleteModel" }
func (op DeleteModel) App() string { return op.AppLabel }

func modelInsertionPosition(order []string, before string) (int, error) {
	if before == "" {
		return len(order), nil
	}
	if i := slices.Index(order, before); i >= 0 {
		return i, nil
	}
	return 0, fmt.Errorf("historical model anchor %q is missing", before)
}

func modelRemovalPosition(order []string, name, before string) (int, error) {
	end, err := modelInsertionPosition(order, before)
	if err != nil {
		return 0, err
	}
	if end == 0 || order[end-1] != name {
		return 0, fmt.Errorf("historical model %q is not immediately before %q", name, before)
	}
	return end - 1, nil
}

func modelOrder(models []ir.Model) []string {
	order := make([]string, len(models))
	for i, model := range models {
		order[i] = model.Name
	}
	return order
}

func (op DeleteModel) stateForward(state ProjectState) (ProjectState, error) {
	s, _ := state.Schema(op.AppLabel)
	if _, err := modelRemovalPosition(modelOrder(s.Models), op.Model.Name, op.BeforeModel); err != nil {
		return state, err
	}
	return (CreateModel{AppLabel: op.AppLabel, Model: op.Model}).stateBackward(state)
}

func (op DeleteModel) stateBackward(state ProjectState) (ProjectState, error) {
	return (CreateModel{AppLabel: op.AppLabel, Model: op.Model}).insertState(state, op.BeforeModel)
}

func (op DeleteModel) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return (CreateModel{AppLabel: op.AppLabel, Model: op.Model}).databaseBackward(ctx, editor, from, to)
}

func (op DeleteModel) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return (CreateModel{AppLabel: op.AppLabel, Model: op.Model}).databaseForward(ctx, editor, from, to)
}
