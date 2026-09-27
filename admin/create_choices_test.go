package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestCreateAndChangeChoicesKeepIndependentActionAuthority(t *testing.T) {
	builder, config := editableRelationConfig(t)
	var createLoads, changeLoads, writes int
	var createFailure error
	available := true
	config.RelatedChoices[0] = RelatedChoices{Field: "category", Permission: config.Permissions.Change, Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
		changeLoads++
		return []forms.Choice{{Value: forms.Integer(9), Label: "Change selection"}}, nil
	}}
	config.CreateForm = &FormConfig{Fields: []string{"category"}, RelatedChoices: []RelatedChoices{{Field: "category", Permission: config.Permissions.Add, Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
		createLoads++
		if createFailure != nil || !available {
			return nil, createFailure
		}
		return []forms.Choice{{Value: forms.Integer(7), Label: "Create selection"}}, nil
	}}}}
	create := config.Create
	config.Create = func(ctx context.Context, p auth.Principal, v forms.Values) (selectionTicket, error) {
		writes++
		return create(ctx, p, v)
	}
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, _ := builder.Build()
	model := registry.models[0]
	creator := mustPrincipalWithPermissions(t, config.Permissions.Add)
	changer := mustPrincipalWithPermissions(t, config.Permissions.Change)
	spec, err := model.forCreate().formFor(context.Background(), creator)
	if err != nil || createLoads != 1 || changeLoads != 0 {
		t.Fatal("create invoked the change loader")
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"category": {"7"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatal("create choice rejected")
	}
	if _, err := model.create(context.Background(), creator, bound); err != nil || writes != 1 || changeLoads != 0 {
		t.Fatal("add-only action required unrelated view/change authority", err)
	}
	if _, err := model.formFor(context.Background(), creator); err == nil || changeLoads != 0 {
		t.Fatal("creator queried change-only labels")
	}
	if _, err := model.formFor(context.Background(), changer); err != nil || changeLoads != 1 {
		t.Fatal("change choice scope failed", err)
	}
	available = false
	if _, err := model.create(context.Background(), creator, bound); err == nil {
		t.Fatal("stale create choice reached mutation")
	} else if diagnostics, ok := validation.Rejected(err); !ok || diagnostics.All()[0].Code() != "invalid_choice" || writes != 1 {
		t.Fatal("stale create choice was not a confirmed rejection")
	}
	createFailure = NewOperationError(OperationDenied, nil)
	if _, err := model.createFormFor(context.Background(), creator); err != createFailure {
		t.Fatal("loader lost its definite outer classification")
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancellation still takes precedence over a permission-shaped result.
	cancel()
	if _, err := model.createFormFor(ctx, creator); !errors.Is(err, context.Canceled) {
		t.Fatal("loader lost cancellation")
	}
}
