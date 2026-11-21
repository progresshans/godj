package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestCompileRelationSetNull(t *testing.T) {
	t.Parallel()

	plan := query.NewRelationSetNullPlan(
		`blog_post`,
		query.NewFieldRef("reviewer", `reviewer_id`, query.FieldInteger, true),
		query.Integer(42),
	)
	statement, arguments, err := compileRelationSetNull(plan)
	if err != nil {
		t.Fatalf("compileRelationSetNull() error = %v", err)
	}
	if want := `UPDATE "blog_post" SET "reviewer_id" = NULL WHERE "reviewer_id" = ?`; statement != want {
		t.Fatalf("statement = %q, want %q", statement, want)
	}
	if !reflect.DeepEqual(arguments, []any{int64(42)}) {
		t.Fatalf("arguments = %#v, want [42]", arguments)
	}

	quoted := query.NewRelationSetNullPlan(
		`odd"table`,
		query.NewFieldRef("reviewer", `odd"column`, query.FieldInteger, true),
		query.Integer(7),
	)
	statement, _, err = compileRelationSetNull(quoted)
	if err != nil {
		t.Fatalf("compile quoted plan: %v", err)
	}
	if want := `UPDATE "odd""table" SET "odd""column" = NULL WHERE "odd""column" = ?`; statement != want {
		t.Fatalf("quoted statement = %q, want %q", statement, want)
	}
}

func TestCompileRelationSetNullRejectsForgedPlans(t *testing.T) {
	t.Parallel()

	validField := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	tests := []struct {
		name string
		plan query.RelationSetNullPlan
	}{
		{name: "zero", plan: query.RelationSetNullPlan{}},
		{name: "empty_table", plan: query.NewRelationSetNullPlan("", validField, query.Integer(1))},
		{name: "table_nul", plan: query.NewRelationSetNullPlan("post\x00", validField, query.Integer(1))},
		{name: "empty_name", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("", "reviewer_id", query.FieldInteger, true), query.Integer(1))},
		{name: "name_nul", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("reviewer\x00", "reviewer_id", query.FieldInteger, true), query.Integer(1))},
		{name: "empty_column", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("reviewer", "", query.FieldInteger, true), query.Integer(1))},
		{name: "column_nul", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("reviewer", "reviewer\x00", query.FieldInteger, true), query.Integer(1))},
		{name: "non_integer", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("reviewer", "reviewer_id", query.FieldString, true), query.String("1"))},
		{name: "non_nullable", plan: query.NewRelationSetNullPlan("post", query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, false), query.Integer(1))},
		{name: "null_target", plan: query.NewRelationSetNullPlan("post", validField, query.Null())},
		{name: "string_target", plan: query.NewRelationSetNullPlan("post", validField, query.String("1"))},
		{name: "unknown_target", plan: query.NewRelationSetNullPlan("post", validField, query.Value{})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statement, arguments, err := compileRelationSetNull(test.plan)
			if statement != "" || arguments != nil {
				t.Fatalf("compileRelationSetNull() = (%q, %#v, %v), want empty result", statement, arguments, err)
			}
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("error = %v, want query_error/invalid_plan", err)
			}
		})
	}
}

func TestExecuteCompiledRelationSetNullRowsAffectedContract(t *testing.T) {
	t.Parallel()

	for _, rowsAffected := range []int64{0, 2} {
		executor := &relationMutationExecutor{result: relationMutationResult{rowsAffected: rowsAffected}}
		got, err := executeCompiledRelationSetNull(context.Background(), executor, "UPDATE x", []any{int64(1)})
		if err != nil || got != rowsAffected {
			t.Fatalf("rows %d: execute = (%d, %v)", rowsAffected, got, err)
		}
		if executor.statement != "UPDATE x" || !reflect.DeepEqual(executor.arguments, []any{int64(1)}) {
			t.Fatalf("executor observed (%q, %#v)", executor.statement, executor.arguments)
		}
	}

	negative := &relationMutationExecutor{result: relationMutationResult{rowsAffected: -1}}
	if rows, err := executeCompiledRelationSetNull(context.Background(), negative, "UPDATE x", nil); rows != 0 ||
		!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}) {
		t.Fatalf("negative rows = (%d, %v), want backend unexpected_rows", rows, err)
	}

	rowsErr := errors.New("rows affected failure")
	failingRows := &relationMutationExecutor{result: relationMutationResult{rowsErr: rowsErr}}
	if rows, err := executeCompiledRelationSetNull(context.Background(), failingRows, "UPDATE x", nil); rows != 0 || !errors.Is(err, rowsErr) {
		t.Fatalf("RowsAffected failure = (%d, %v)", rows, err)
	}

	execErr := errors.New("execute failure")
	failingExec := &relationMutationExecutor{err: execErr}
	if rows, err := executeCompiledRelationSetNull(context.Background(), failingExec, "UPDATE x", nil); rows != 0 || !errors.Is(err, execErr) {
		t.Fatalf("Exec failure = (%d, %v)", rows, err)
	}
}

type relationMutationExecutor struct {
	statement string
	arguments []any
	result    sql.Result
	err       error
}

func (executor *relationMutationExecutor) ExecContext(_ context.Context, statement string, arguments ...any) (sql.Result, error) {
	executor.statement = statement
	executor.arguments = append([]any(nil), arguments...)
	return executor.result, executor.err
}

type relationMutationResult struct {
	rowsAffected int64
	rowsErr      error
}

func (result relationMutationResult) LastInsertId() (int64, error) { return 0, fmt.Errorf("unused") }
func (result relationMutationResult) RowsAffected() (int64, error) {
	return result.rowsAffected, result.rowsErr
}
