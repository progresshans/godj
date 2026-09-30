package model_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/validation"
)

func databaseBoundForm(t *testing.T) formmodel.BoundForm {
	t.Helper()
	metadata := blankModel(t, schema.CharField("key", "Key", 8, schema.Unique()), schema.CharField("other", "Other", 20), schema.IntegerField("counter", "Counter"))
	bound, err := (formmodel.Definition{}).Bind(t.Context(), metadata, forms.NewData(map[string][]string{"key": {"used"}, "other": {"valid"}, "counter": {"1"}}), nil)
	if err != nil || !bound.Form().Valid() {
		t.Fatal("valid model form", err)
	}
	return bound
}

func TestModelDatabaseChecksRecomputeExclusionsAndKeepOriginalForm(t *testing.T) {
	bound, err := databaseBoundForm(t).WithErrors(validation.NewErrors(validation.New("other", "field_failure")))
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	result, err := bound.CheckDatabase(t.Context(), formmodel.DatabaseChecks{
		UniqueFields: func(_ context.Context, values map[string]query.Value) (validation.Errors, error) {
			stages = append(stages, "unique")
			if len(values) != 2 || !values["key"].Equal(query.String("used")) || !values["counter"].Equal(query.Integer(1)) {
				t.Fatal("unrelated field failure suppressed a valid candidate")
			}
			values["counter"] = query.Integer(99)
			return validation.NewErrors(validation.New("key", validation.CodeUnique)), nil
		},
		Constraints: func(_ context.Context, values map[string]query.Value) (validation.Errors, error) {
			stages = append(stages, "constraints")
			if len(values) != 1 || !values["counter"].Equal(query.Integer(1)) {
				t.Fatal("constraint candidate retained prior errors or callback mutations")
			}
			return validation.NewErrors(validation.New(validation.NonField, validation.CodeUniqueTogether)), nil
		},
	})
	if err != nil || result.Len() != 2 || !reflect.DeepEqual(stages, []string{"unique", "constraints"}) {
		t.Fatal("stage result", err, stages, result)
	}
	if bound.Form().Errors().Len() != 1 {
		t.Fatal("checking mutated the caller's form")
	}
	if key, _ := bound.Form().Cleaned().String("key"); key != "used" {
		t.Fatal("checking lost original cleaned input")
	}
	checked, err := bound.WithErrors(result)
	if err != nil || checked.Form().Errors().Len() != 3 {
		t.Fatal("new diagnostics duplicated initial errors", err)
	}
	if _, present := checked.Form().Cleaned().Get("key"); present {
		t.Fatal("unique failure kept a cleaned field")
	}
	metadata := bound.Model()
	metadata.Fields[0].Name = "forged"
	if bound.Model().Fields[0].Name != "id" {
		t.Fatal("model metadata getter leaked an alias")
	}
}

func TestModelDatabaseChecksRejectMissingContextCallbacksAndUnboundBeforeChecks(t *testing.T) {
	for _, mode := range []string{"nil_context", "canceled", "missing_unique", "missing_constraints", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			calls := 0
			check := func(context.Context, map[string]query.Value) (validation.Errors, error) {
				calls++
				return validation.Errors{}, nil
			}
			checks := formmodel.DatabaseChecks{UniqueFields: check, Constraints: check}
			bound := databaseBoundForm(t)
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "missing_unique":
				checks.UniqueFields = nil
			case "missing_constraints":
				checks.Constraints = nil
			case "unbound":
				bound = formmodel.BoundForm{}
			}
			result, err := bound.CheckDatabase(ctx, checks)
			if err == nil || !result.Empty() || calls != 0 {
				t.Fatal("invalid request reached checks", err, calls)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
		})
	}
}

func TestModelDatabaseExecutionFailureDiscardsPartialDiagnosticsAndRedactsCause(t *testing.T) {
	secret := errors.New("private-model-db-detail")
	for _, mode := range []string{"unique_failure", "constraint_failure", "unique_cancel", "constraint_cancel", "rejection_as_error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			check := func(context.Context, map[string]query.Value) (validation.Errors, error) {
				calls++
				failures := validation.NewErrors(validation.New("key", validation.CodeUnique))
				if mode == "unique_failure" || mode == "constraint_failure" && calls == 2 {
					return failures, secret
				}
				if mode == "unique_cancel" || mode == "constraint_cancel" && calls == 2 {
					cancel()
				}
				if mode == "rejection_as_error" {
					return failures, validation.Reject(failures, secret)
				}
				return failures, nil
			}
			result, err := databaseBoundForm(t).CheckDatabase(ctx, formmodel.DatabaseChecks{UniqueFields: check, Constraints: check})
			if err == nil || !result.Empty() {
				t.Fatal("execution failure published partial diagnostics")
			}
			wantCalls := 1
			if strings.HasPrefix(mode, "constraint_") {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatal("execution continued after a failed stage", calls)
			}
			if _, renderable := validation.Rejected(err); renderable {
				t.Fatal("execution error was downgraded to form rejection")
			}
			if strings.HasSuffix(mode, "cancel") {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost")
				}
			} else if !errors.Is(err, secret) {
				t.Fatal("private cause lost")
			}
			for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
				if strings.Contains(fmt.Sprintf(format, err), secret.Error()) {
					t.Fatal("private database cause appeared in formatting")
				}
			}
		})
	}
}

func TestModelDatabaseChecksRejectUnownedDiagnosticsBeforeNextStage(t *testing.T) {
	calls := 0
	check := func(context.Context, map[string]query.Value) (validation.Errors, error) {
		calls++
		return validation.NewErrors(validation.New("hidden", "unique")), nil
	}
	result, err := databaseBoundForm(t).CheckDatabase(t.Context(), formmodel.DatabaseChecks{UniqueFields: check, Constraints: check})
	if err == nil || calls != 1 || !result.Empty() {
		t.Fatal("unowned diagnostic reached next stage", err, calls)
	}
}
