package api_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
)

func TestPermissionPolicyOwnsBoundedDistinctRequirements(t *testing.T) {
	required, alternatives := []auth.Permission{"tests.scope"}, []auth.Permission{"tests.view", "tests.change"}
	policy, err := api.NewPermissionPolicy(required, alternatives)
	if err != nil || !policy.Valid() {
		t.Fatal(err)
	}
	required[0], alternatives[0] = "tests.other", "tests.other"
	policy.Required()[0], policy.Alternatives()[0] = "tests.other", "tests.other"
	if !slices.Equal(policy.Required(), []auth.Permission{"tests.scope"}) || !slices.Equal(policy.Alternatives(), []auth.Permission{"tests.view", "tests.change"}) {
		t.Fatal("mutable policy")
	}
	maximum := make([]auth.Permission, auth.MaximumPermissions)
	for index := range maximum {
		maximum[index] = auth.Permission(fmt.Sprintf("tests.permission%d", index))
	}
	if _, err := api.NewPermissionPolicy(maximum[:100], maximum[100:]); err != nil {
		t.Fatal("maximum valid policy", err)
	}
	for name, groups := range map[string][2][]auth.Permission{
		"empty": {}, "empty_name": {{""}, nil}, "noncanonical": {{"Tests.View"}, nil},
		"duplicate_required":    {{"tests.view", "tests.view"}, nil},
		"duplicate_alternative": {nil, {"tests.view", "tests.view"}},
		"cross_group_duplicate": {{"tests.view"}, {"tests.view"}},
		"union_bound":           {maximum, {"tests.extra"}}, "required_bound": {append(slices.Clone(maximum), "tests.extra"), nil},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := api.NewPermissionPolicy(groups[0], groups[1])
			if err == nil || value.Valid() {
				t.Fatal("invalid policy published")
			}
		})
	}
}

func TestPermissionPolicyRequiresAllCommonAndOneAlternative(t *testing.T) {
	policy, err := api.NewPermissionPolicy([]auth.Permission{"tests.scope", "tests.manage"}, []auth.Permission{"tests.view", "tests.change"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"first", "alternative", "missing_common", "missing_alternatives", "common_error", "alternative_error", "cancel_before", "cancel_common", "cancel_alternative"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel_before" {
				cancel()
			}
			failure := errors.New("authority failure")
			var calls []auth.Permission
			granted, err := policy.Check(ctx, func(_ context.Context, permission auth.Permission) (bool, error) {
				calls = append(calls, permission)
				if mode == "common_error" && permission == "tests.manage" || mode == "alternative_error" && permission == "tests.view" {
					return true, failure
				}
				if mode == "cancel_common" && permission == "tests.manage" || mode == "cancel_alternative" && permission == "tests.view" {
					cancel()
					return true, failure
				}
				if permission == "tests.scope" {
					return true, nil
				}
				if permission == "tests.manage" {
					return mode != "missing_common", nil
				}
				return mode == "first" || mode == "alternative" && permission == "tests.change", nil
			})
			want := []auth.Permission{"tests.scope", "tests.manage", "tests.view", "tests.change"}
			switch mode {
			case "cancel_before":
				want = nil
			case "missing_common", "common_error", "cancel_common":
				want = want[:2]
			case "first", "alternative_error", "cancel_alternative":
				want = want[:3]
			}
			if granted != (mode == "first" || mode == "alternative") || !slices.Equal(calls, want) {
				t.Fatal("policy outcome/order", granted, calls, err)
			}
			if mode == "common_error" || mode == "alternative_error" {
				if err != failure {
					t.Fatal("direct authority error identity lost", err)
				}
			} else if mode == "cancel_before" || mode == "cancel_common" || mode == "cancel_alternative" {
				if !errors.Is(err, context.Canceled) || mode != "cancel_before" && !errors.Is(err, failure) {
					t.Fatal("cancellation/cause lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, check := range map[string]func() (bool, error){
		"zero": func() (bool, error) {
			return (api.PermissionPolicy{}).Check(context.Background(), func(context.Context, auth.Permission) (bool, error) {
				t.Fatal("zero policy callback")
				return true, nil
			})
		},
		"nil_context": func() (bool, error) {
			return policy.Check(nil, func(context.Context, auth.Permission) (bool, error) {
				t.Fatal("nil context callback")
				return true, nil
			})
		},
		"nil_authority": func() (bool, error) { return policy.Check(context.Background(), nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if granted, err := check(); granted || err == nil {
				t.Fatal("invalid check granted", err)
			}
		})
	}
}
