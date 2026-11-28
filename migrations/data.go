package migrations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/internal/datascope"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// DataKey identifies one version of a project's historical data functions.
// Publish a new version when changing their meaning. Neither executable
// addresses nor the currently selected runtime model identify past data.
type DataKey struct {
	App, Name string
	Version   uint32
}

type DataCallback func(context.Context, HistoricalModels) error

// HistoricalModels provides model-scoped operations. The underlying session,
// transaction control and recorder remain with the executing backend.
type HistoricalModels datascope.Scope

func (models HistoricalModels) Model(ctx context.Context, app, name string) (orm.RecordSet, error) {
	session, model, err := datascope.Model(ctx, datascope.Scope(models), app, name)
	if err != nil {
		return orm.RecordSet{}, err
	}
	return orm.BindRecords(model, session)
}

type DataRegistration struct {
	Key              DataKey
	Forward, Reverse DataCallback
}

// DataRegistry is an immutable, project-owned registration snapshot.
type DataRegistry struct{ entries map[DataKey]DataRegistration }

func NewDataRegistry(entries ...DataRegistration) (DataRegistry, error) {
	result := DataRegistry{entries: make(map[DataKey]DataRegistration, len(entries))}
	for _, entry := range entries {
		if !entry.Key.valid() || entry.Forward == nil && entry.Reverse == nil {
			return DataRegistry{}, errors.New("data registration needs a valid versioned key and a callback")
		}
		if _, exists := result.entries[entry.Key]; exists {
			return DataRegistry{}, fmt.Errorf("duplicate data registration %s.%s version %d", entry.Key.App, entry.Key.Name, entry.Key.Version)
		}
		result.entries[entry.Key] = entry
	}
	return result, nil
}

func (key DataKey) valid() bool {
	return key.Version > 0 && identifiers.SQL(key.App) && identifiers.SQL(key.Name)
}

// DataAction is explicit in both directions. Irreversible is valid only
// for a reverse action, and differs from a successful no-op.
type DataAction string

const (
	DataCall         DataAction = "call"
	DataNoop         DataAction = "noop"
	DataIrreversible DataAction = "irreversible"
)

type RunData struct {
	Key              DataKey
	Forward, Reverse DataAction
	Models           []ir.ModelIdentity
}

func (RunData) operation()     {}
func (RunData) Kind() string   { return "RunData" }
func (op RunData) App() string { return op.Key.App }

// Normalized returns a detached operation with its model roots in identity order.
func (op RunData) Normalized() (RunData, error) {
	if !op.Key.valid() || op.Forward != DataCall && op.Forward != DataNoop ||
		op.Reverse != DataCall && op.Reverse != DataNoop && op.Reverse != DataIrreversible {
		return RunData{}, errors.New("invalid data operation identity, version or direction")
	}
	if len(op.Models) > maxLoadedDefinitions {
		return RunData{}, errors.New("data operation model count exceeds the definition limit")
	}
	op.Models = slices.Clone(op.Models)
	slices.SortFunc(op.Models, compareDataModel)
	for i, model := range op.Models {
		if !identifiers.SQL(model.AppLabel) || !identifiers.SQL(model.ModelName) || i > 0 && model == op.Models[i-1] {
			return RunData{}, errors.New("invalid or duplicate data operation model")
		}
	}
	return op, nil
}

func compareDataModel(a, b ir.ModelIdentity) int {
	if a.AppLabel != b.AppLabel {
		return strings.Compare(a.AppLabel, b.AppLabel)
	}
	return strings.Compare(a.ModelName, b.ModelName)
}

func (RunData) stateForward(state ProjectState) (ProjectState, error)  { return state, nil }
func (RunData) stateBackward(state ProjectState) (ProjectState, error) { return state, nil }
func (RunData) databaseForward(context.Context, backend.SchemaEditor, ProjectState, ProjectState) error {
	return backend.NewCapabilityError("historical_data", "RunData requires a loaded, revision-fenced transaction", nil)
}
func (op RunData) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return op.databaseForward(ctx, editor, from, to)
}

func (registry DataRegistry) validate(migration Migration) error {
	for index, operation := range migration.Operations {
		op, ok := operationValue(operation).(RunData)
		if !ok {
			continue
		}
		entry := registry.entries[op.Key]
		if op.Forward == DataCall && entry.Forward == nil || op.Reverse == DataCall && entry.Reverse == nil {
			return migrationError(CategoryState, CodeMissingDataCallback, "", migration, index, op.Kind(),
				fmt.Errorf("missing data function for %s.%s version %d", op.Key.App, op.Key.Name, op.Key.Version))
		}
	}
	return nil
}

func (registry DataRegistry) execute(ctx context.Context, tx migrationBodyTransaction, index int, op RunData, direction Direction) error {
	port, ok := tx.(backend.HistoricalDataTransaction)
	if !ok {
		return backend.NewCapabilityError("historical_data", "transaction has no historical row scope", nil)
	}
	action, callback := op.Forward, registry.entries[op.Key].Forward
	if direction == DirectionBackward {
		action, callback = op.Reverse, registry.entries[op.Key].Reverse
	}
	if action == DataNoop {
		callback = func(ctx context.Context, _ HistoricalModels) error { return ctx.Err() }
	}
	if callback == nil || action == DataIrreversible {
		return errors.New("historical data action has no executable direction")
	}
	return port.RunData(ctx, index, func(ctx context.Context, scope backend.DataScope) error {
		return callback(ctx, HistoricalModels(scope))
	})
}
