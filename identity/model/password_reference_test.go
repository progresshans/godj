package model_test

import (
	"reflect"
	"sync"
	"testing"

	passwordreference "github.com/progresshans/godj/auth/passwordpolicy/testdata"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func selectedPasswordDiagnostics(failures validation.Errors) []passwordreference.Diagnostic {
	result := []passwordreference.Diagnostic{}
	for _, item := range failures.All() {
		params := map[string]string{}
		for _, parameter := range item.Params() {
			params[parameter.Key()] = parameter.Value()
		}
		result = append(result, passwordreference.Diagnostic{Code: string(item.Code()), Params: params})
	}
	return result
}

// This consumer tests the default four rules over typed advisory profile
// inputs. General input/IR storage validation belongs to the account services;
// no database row or credential acceptance is inferred from these pure calls.
func TestSelectedPasswordPolicyMatchesPinnedDjango(t *testing.T) {
	inputs, reference, err := passwordreference.Load()
	if err != nil {
		t.Fatal(err)
	}
	binding := credentialBinding(t, modeldef.AccountRole())
	count := 0
	for index, input := range inputs.Cases {
		// Custom dictionaries, thresholds and lengths are consumed by the
		// model-independent policy suite, which compares all 156 cases.
		if input.Minimum != 8 || input.MaximumSimilarity != .7 || input.Dictionary != nil {
			continue
		}
		count++
		t.Run(input.Name, func(t *testing.T) {
			declared := input.ProfileAttributes()
			attributes := make([]accountmodel.PasswordAttribute, len(declared))
			for i, attribute := range declared {
				attributes[i] = accountmodel.PasswordAttribute{Field: attribute.Field, Label: attribute.Label}
			}
			policy, err := binding.DefaultPasswordValidator(attributes...)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]query.Value{}
			for name, value := range input.Profile {
				values[name] = query.String(value)
			}
			typed, err := models.UserObjects.Input(values)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := models.UserObjects.ViewInput(typed)
			if err != nil {
				t.Fatal(err)
			}
			before := profile.Values()
			failures, err := policy(t.Context(), input.Password, profile)
			if err != nil {
				t.Fatal("selected password reference became an execution error", err)
			}
			for _, item := range failures.All() {
				if item.Field() != "password" {
					t.Fatal("selected policy lost the password diagnostic field")
				}
			}
			if !reflect.DeepEqual(selectedPasswordDiagnostics(failures), reference.Cases[index].Errors["combined"]) {
				t.Fatal("selected declared policy differs from independent Django")
			}
			if !reflect.DeepEqual(before, profile.Values()) {
				t.Fatal("selected policy changed its caller's advisory profile")
			}
		})
	}
	if count != 41 {
		t.Fatal("selected default policy reference coverage changed", count)
	}
}

func TestSelectedPasswordPolicyDeclarationOwnershipAndConcurrency(t *testing.T) {
	binding := credentialBinding(t, modeldef.AccountRole())
	attributes := []accountmodel.PasswordAttribute{{Field: "first_name", Label: "display name"}}
	policy, err := binding.DefaultPasswordValidator(attributes...)
	if err != nil {
		t.Fatal(err)
	}
	attributes[0] = accountmodel.PasswordAttribute{Field: "unknown", Label: "private replacement"}
	row := credentialRow()
	row.FirstName = "Synthetic-Profile-Character-Gamma"
	matching, err := models.UserObjects.View(row, "first_name")
	if err != nil {
		t.Fatal(err)
	}
	row.FirstName = "zzzzzzzzzzzzzzzzzz"
	other, err := models.UserObjects.View(row, "first_name")
	if err != nil {
		t.Fatal(err)
	}
	want := []passwordreference.Diagnostic{{Code: "password_too_similar", Params: map[string]string{"verbose_name": "display name"}}}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 50 {
				failures, err := policy(t.Context(), "Synthetic-Profile-Character-Gamma", matching)
				if err != nil || !reflect.DeepEqual(selectedPasswordDiagnostics(failures), want) {
					t.Error("selected policy shared or lost its original attribute declaration", err)
				}
				failures, err = policy(t.Context(), "Synthetic-Profile-Character-Gamma", other)
				if err != nil || !failures.Empty() {
					t.Error("selected policy retained another invocation's profile", err)
				}
			}
		})
	}
	workers.Wait()
}
