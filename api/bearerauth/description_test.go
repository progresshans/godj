package bearerauth

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
)

func TestDescribeAuthenticationDoesNotVerifyOrAuthorize(t *testing.T) {
	t.Parallel()
	runtime, err := New(Config{
		Verifier: verifierFunc(func(context.Context, Token) (auth.Principal, error) {
			t.Fatal("description verified a credential")
			return auth.Principal{}, nil
		}),
		Authorizer: authorizerFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
			t.Fatal("description authorized a principal")
			return false, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := api.AuthenticationDescription{Kind: api.AuthenticationBearer}
	for range 2 {
		got, err := runtime.DescribeAuthentication()
		if err != nil || got != want {
			t.Fatalf("description = %#v, %v; want %#v", got, err, want)
		}
		got.CSRFHeader = "caller mutation"
	}
	for _, invalid := range []*Runtime{nil, {}} {
		if _, err := invalid.DescribeAuthentication(); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "runtime"}) {
			t.Fatalf("invalid runtime description = %v", err)
		}
	}
}
