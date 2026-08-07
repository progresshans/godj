package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

type preparedPlanStep struct {
	step      PlanStep
	migration Migration
	after     ProjectState
}

// ExecutePlan runs an already-planned, single-direction sequence one
// migration transaction at a time. It validates the complete definition,
// plan, and historical state transition sequence before the first backend
// transaction starts.
func (e Executor) ExecutePlan(
	ctx context.Context,
	before ProjectState,
	definitions []Migration,
	plan []PlanStep,
) (ProjectState, error) {
	if ctx == nil {
		return before.Clone(), executionContextError(PlanStep{}, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(PlanStep{}, err)
	}
	if len(plan) == 0 {
		return before.Clone(), nil
	}

	// Snapshot caller-owned slices and the built-in operation values before
	// preflight. Operations are sealed by their package-private marker method;
	// the known built-ins are deep-copied below.
	definitionSnapshot := cloneMigrationDefinitions(definitions)
	planSnapshot := append([]PlanStep(nil), plan...)
	prepared, err := preflightPlan(ctx, before, definitionSnapshot, planSnapshot)
	if err != nil {
		return before.Clone(), err
	}

	working := before.Clone()
	for _, preparedStep := range prepared {
		// This is both the pre-step gate and the between-step gate. Apply and
		// Unapply repeat the check immediately before backend access.
		if err := ctx.Err(); err != nil {
			return working.Clone(), executionContextError(preparedStep.step, err)
		}

		if preparedStep.step.Direction == DirectionForward {
			_, err = e.Apply(ctx, working, preparedStep.migration)
		} else {
			_, err = e.Unapply(ctx, working, preparedStep.migration)
		}
		if err != nil {
			// Apply/Unapply return the input state on failure. ExecutePlan owns
			// the plan-level durable state and deliberately preserves the
			// original structured error and all of its causes.
			return working.Clone(), err
		}
		working = preparedStep.after.Clone()
	}

	// Do not re-check context here. A cancellation observed only after the
	// final migration commit cannot undo that durable success.
	return working.Clone(), nil
}

func preflightPlan(
	ctx context.Context,
	before ProjectState,
	definitions []Migration,
	plan []PlanStep,
) ([]preparedPlanStep, error) {
	byKey := make(map[MigrationKey]Migration, len(definitions))
	for index, migration := range definitions {
		key := migration.Key()
		if !validMigrationKey(key) {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				PlanStep{Key: key},
				fmt.Errorf("definition[%d] has an invalid migration key", index),
			)
		}
		if _, exists := byKey[key]; exists {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				PlanStep{Key: key},
				fmt.Errorf("definition[%d] duplicates migration %s.%s", index, key.App, key.Name),
			)
		}
		byKey[key] = migration
	}

	seen := make(map[MigrationKey]struct{}, len(plan))
	var direction Direction
	for index, step := range plan {
		if !validMigrationKey(step.Key) {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("plan[%d] has an invalid migration key", index),
			)
		}
		if step.Direction != DirectionForward && step.Direction != DirectionBackward {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("plan[%d] has invalid direction %q", index, step.Direction),
			)
		}
		if _, exists := seen[step.Key]; exists {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("plan[%d] duplicates migration %s.%s", index, step.Key.App, step.Key.Name),
			)
		}
		seen[step.Key] = struct{}{}
		if _, exists := byKey[step.Key]; !exists {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("plan[%d] has no definition for migration %s.%s", index, step.Key.App, step.Key.Name),
			)
		}
		if direction == "" {
			direction = step.Direction
			continue
		}
		if step.Direction != direction {
			return nil, executionPlanError(
				CodeMixedDirections,
				step,
				fmt.Errorf("plan[%d] direction %q differs from plan direction %q", index, step.Direction, direction),
			)
		}
	}

	working := before.Clone()
	prepared := make([]preparedPlanStep, 0, len(plan))
	for _, step := range plan {
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(step, err)
		}
		migration := byKey[step.Key]
		_, after, err := preflight(working, migration, step.Direction)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedPlanStep{
			step:      step,
			migration: migration,
			after:     after.Clone(),
		})
		working = after
	}
	return prepared, nil
}

func executionPlanError(code ErrorCode, step PlanStep, cause error) *Error {
	return migrationError(
		CategoryExecution,
		code,
		step.Direction,
		Migration{App: step.Key.App, Name: step.Key.Name},
		NoOperation,
		"",
		cause,
	)
}

func executionContextError(step PlanStep, cause error) *Error {
	return executionPlanError(CodeOperationFailed, step, cause)
}

func cloneMigrationDefinitions(definitions []Migration) []Migration {
	cloned := make([]Migration, len(definitions))
	for index, definition := range definitions {
		cloned[index] = definition
		cloned[index].Dependencies = append([]MigrationKey(nil), definition.Dependencies...)
		cloned[index].Operations = make([]Operation, len(definition.Operations))
		for operationIndex, operation := range definition.Operations {
			cloned[index].Operations[operationIndex] = cloneMigrationOperation(operation)
		}
	}
	return cloned
}

func cloneMigrationOperation(operation Operation) Operation {
	switch operation := operation.(type) {
	case CreateModel:
		operation.Model = operation.Model.Clone()
		return operation
	case *CreateModel:
		if operation == nil {
			return operation
		}
		cloned := *operation
		cloned.Model = operation.Model.Clone()
		return cloned
	case AddField:
		operation.Field = cloneMigrationField(operation.Field)
		return operation
	case *AddField:
		if operation == nil {
			return operation
		}
		cloned := *operation
		cloned.Field = cloneMigrationField(operation.Field)
		return cloned
	default:
		return operation
	}
}

func cloneMigrationField(field ir.Field) ir.Field {
	cloned := field
	if field.Default != nil {
		defaultValue := *field.Default
		cloned.Default = &defaultValue
	}
	return cloned
}
