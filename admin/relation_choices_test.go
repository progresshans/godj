package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func editableRelationConfig(t *testing.T) (*Builder, ModelConfig[selectionTicket]) {
	t.Helper()
	builder, config := ticketSelectionConfig(t)
	config.ReadOnly = false
	config.FormFields = []string{"category"}
	config.Permissions = Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"}
	config.Get = func(context.Context, auth.Principal, int64) (selectionTicket, bool, error) {
		return selectionTicket{1, "Printer", 7}, true, nil
	}
	config.Initial = func(row selectionTicket) (map[string]forms.Value, error) {
		return map[string]forms.Value{"category": forms.Integer(row.category)}, nil
	}
	config.Create = func(_ context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (MutationResult, error) {
		values, inputErr := bound.Input()
		if inputErr != nil {
			return MutationResult{}, inputErr
		}

		if _, ok := values.Integer("category"); !ok {
			return MutationResult{}, errors.New("missing category")
		}
		return MutationResult{ID: 1}, nil
	}
	config.Update = func(_ context.Context, _ auth.Principal, mutation Mutation, bound formmodel.BoundForm, _ InlineSubmission) (MutationResult, []string, error) {
		values, inputErr := bound.Input()
		if inputErr != nil {
			return MutationResult{}, nil, inputErr
		}

		id := mutation.ID
		if _, ok := values.Integer("category"); !ok {
			return MutationResult{}, nil, errors.New("missing category")
		}
		return MutationResult{ID: id}, []string{"category"}, nil
	}
	config.Delete = func(context.Context, auth.Principal, Mutation) (MutationResult, error) {
		return MutationResult{ID: 1}, nil
	}
	config.RelatedChoices = []RelatedChoices{testRelationChoices(t, config.Model, config.AppLabel, "category", "helpdesk.view_category", func(context.Context, auth.Principal) ([]forms.Choice, error) {
		return []forms.Choice{{Value: forms.Integer(7), Label: "Visible"}}, nil
	})}
	return builder, config
}

func TestRelatedChoicesRegistrationRequiresExactExplicitSourcesWithoutIO(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ModelConfig[selectionTicket])
	}{
		{"missing", func(c *ModelConfig[selectionTicket]) { c.RelatedChoices = nil }},
		{"duplicate", func(c *ModelConfig[selectionTicket]) {
			c.RelatedChoices = append(c.RelatedChoices, c.RelatedChoices[0])
		}},
		{"scalar", func(c *ModelConfig[selectionTicket]) {
			c.RelatedChoices[0] = testChoicesWithField(c.RelatedChoices[0], "subject")
		}},
		{"unknown", func(c *ModelConfig[selectionTicket]) {
			c.RelatedChoices[0] = testChoicesWithField(c.RelatedChoices[0], "unknown")
		}},
		{"missing callback", func(c *ModelConfig[selectionTicket]) {
			c.RelatedChoices[0] = testChoicesWithoutScope(t, c.RelatedChoices[0])
		}},
		{"missing permission", func(c *ModelConfig[selectionTicket]) {
			c.RelatedChoices[0] = testChoicesWithPermission(c.RelatedChoices[0], "")
		}},
		{"read only", func(c *ModelConfig[selectionTicket]) { c.ReadOnly = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder, config := editableRelationConfig(t)
			config.RelatedChoices[0] = testChoicesWithRead(config.RelatedChoices[0], func(context.Context, auth.Principal) ([]forms.Choice, error) {
				t.Fatal("registration queried choices")
				return nil, nil
			})
			test.change(&config)
			if err := RegisterModel(builder, config); err == nil {
				t.Fatal("invalid choice source accepted")
			}
		})
	}
	builder, config := editableRelationConfig(t)
	config.RelatedChoices[0] = testChoicesWithRead(config.RelatedChoices[0], func(context.Context, auth.Principal) ([]forms.Choice, error) {
		t.Fatal("registration queried choices")
		return nil, nil
	})
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatal(err)
	}
}

func TestRelatedChoicesRevalidateBeforeMutationAndKeepExecutionFailures(t *testing.T) {
	builder, config := editableRelationConfig(t)
	choices := []forms.Choice{{Value: forms.Integer(7), Label: "Visible"}}
	var loads, mutations int
	var failure error
	config.RelatedChoices[0] = testChoicesWithRead(config.RelatedChoices[0], func(context.Context, auth.Principal) ([]forms.Choice, error) { loads++; return choices, failure })
	create, update := config.Create, config.Update
	config.Create = func(ctx context.Context, p auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (MutationResult, error) {

		mutations++
		return create(ctx, p, bound, InlineSubmission{})
	}
	config.Update = func(ctx context.Context, p auth.Principal, mutation Mutation, bound formmodel.BoundForm, _ InlineSubmission) (MutationResult, []string, error) {

		mutations++
		return update(ctx, p, mutation, bound, InlineSubmission{})
	}
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	model := registry.models[0]
	principal := mustPrincipalWithPermissions(t, config.Permissions.Add, config.Permissions.Change, "helpdesk.view_category")
	spec, err := model.resolveForm(context.Background(), principal, model.origin(ChoiceChange, 1), forms.NewData(map[string][]string{"category": {"7"}}), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"category": {"7"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatal(err, bound.Errors())
	}
	for _, update := range []bool{false, true} {
		run := func(form forms.Form) error {
			if update {
				_, _, err := model.update(context.Background(), principal, Mutation{ID: 1}, form, InlineSubmission{})
				return err
			}
			_, err := model.create(context.Background(), principal, form, InlineSubmission{})
			return err
		}
		before := loads
		if err := run(forms.Form{}); errorCode(err) != "not_bound_valid" || loads != before {
			t.Fatal("invalid internal form queried choices", err, loads, before)
		}
		choices = nil
		err := run(bound)
		rejected, ok := validation.Rejected(err)
		if !ok || len(rejected.All()) != 1 || rejected.All()[0].Field() != "category" || rejected.All()[0].Code() != "invalid_choice" || mutations != 0 {
			t.Fatal("stale choice reached mutation", err, mutations)
		}
		choices = []forms.Choice{{Value: forms.Integer(7), Label: "Visible"}}
		failure = errors.New("choice query failed")
		if err := run(bound); !errors.Is(err, failure) {
			t.Fatal("query failure lost", err)
		} else if _, ok := validation.Rejected(err); ok {
			t.Fatal("execution failure became input rejection")
		}
		failure = nil
	}
	if _, err := model.create(context.Background(), principal, bound, InlineSubmission{}); err != nil || mutations != 1 {
		t.Fatal("valid snapshot could not recover", err, mutations)
	}
	denied := mustPrincipalWithPermissions(t, config.Permissions.Add)
	before := loads
	if _, err := model.create(context.Background(), denied, bound, InlineSubmission{}); err == nil || loads != before || mutations != 1 {
		t.Fatal("missing target permission performed IO", err)
	}
}

func TestRelatedChoiceSnapshotsAuthorizeAllSourcesAndIsolateConcurrentScopes(t *testing.T) {
	first, _ := forms.ModelChoiceField("first")
	second, _ := forms.ModelChoiceField("second")
	spec, err := forms.NewSpec([]forms.Field{first, second})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	type scopeKey struct{}
	load := func(ctx context.Context, _ auth.Principal) ([]forms.Choice, error) {
		calls.Add(1)
		key := ctx.Value(scopeKey{}).(int64)
		return []forms.Choice{{Value: forms.Integer(key), Label: fmt.Sprint(key)}}, nil
	}
	sources := []RelatedChoices{testTargetChoices(t, "second", "rows.second", load), testTargetChoices(t, "first", "rows.first", load)}
	resolve, _, err := prepareRelatedChoices(ir.Model{}, ir.ModelIdentity{}, spec, sources, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), scopeKey{}, int64(7))
	origin := ChoiceOrigin{operation: ChoiceCollection, valid: true}
	keys := func(key int64) choiceKeys {
		return func(string) ([]int64, bool, error) { return []int64{key}, true, nil }
	}
	if _, err := resolve(ctx, mustPrincipalWithPermissions(t, "rows.first"), origin, keys(7)); err == nil || calls.Load() != 0 {
		t.Fatal("partly authorized source queried", err)
	}
	principal := mustPrincipalWithPermissions(t, "rows.first", "rows.second")
	var wait sync.WaitGroup
	failures := make(chan error, 20)
	for key := int64(1); key <= 20; key++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := resolve(context.WithValue(context.Background(), scopeKey{}, key), principal, origin, keys(key))
			if err != nil {
				failures <- err
				return
			}
			for _, field := range value.Fields() {
				choices := field.Choices()
				got, _ := choices[0].Value.AsInteger()
				if got != key {
					failures <- fmt.Errorf("scope %d received %d", key, got)
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	for _, field := range spec.Fields() {
		if len(field.Choices()) != 0 {
			t.Fatal("shared base spec was changed")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	sources[1] = testChoicesWithRead(sources[1], func(context.Context, auth.Principal) ([]forms.Choice, error) {
		cancel()
		return []forms.Choice{{Value: forms.Integer(7), Label: "Visible"}}, nil
	})
	resolve, _, err = prepareRelatedChoices(ir.Model{}, ir.ModelIdentity{}, spec, sources, true)
	if err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if result, err := resolve(canceled, principal, origin, keys(7)); !errors.Is(err, context.Canceled) || len(result.Fields()) != 0 || calls.Load() != before {
		t.Fatal("canceled partial snapshot published or next source queried", err)
	}
	sources[1] = testChoicesWithRead(sources[1], func(context.Context, auth.Principal) ([]forms.Choice, error) {
		return []forms.Choice{{Value: forms.String("7"), Label: "bad"}}, nil
	})
	resolve, _, _ = prepareRelatedChoices(ir.Model{}, ir.ModelIdentity{}, spec, sources, true)
	if result, err := resolve(ctx, principal, origin, keys(7)); err != nil {
		var failure *query.Error
		if !errors.As(err, &failure) || failure.Category != query.CategoryField || failure.Code != query.CodeInvalidValue || failure.Field != "id" || len(result.Fields()) != 0 {
			t.Fatal("invalid provider result lost its failure or published a partial form", err)
		}
	} else {
		t.Fatal("invalid provider result accepted")
	}
}
