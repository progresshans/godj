package operatorconfig

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/internal/articlepermissions"
)

func TestCredentialPolicyIsTheCanonicalSecretFreeArticleOperatorProfile(t *testing.T) {
	first, err := CredentialPolicy()
	if err != nil {
		t.Fatal(err)
	}
	second, err := CredentialPolicy()
	if err != nil {
		t.Fatal(err)
	}
	wantPermissions := []auth.Permission{
		admin.DefaultAccessPermission,
		articlepermissions.ArticleViewPermission,
		articlepermissions.ArticleAddPermission,
		articlepermissions.ArticleChangePermission,
		articlepermissions.ArticleDeletePermission,
	}
	for index, policy := range []struct {
		name      string
		principal auth.Principal
		hasher    auth.PasswordHasher
	}{
		{name: "first", principal: first.Principal, hasher: first.PasswordHasher},
		{name: "second", principal: second.Principal, hasher: second.PasswordHasher},
	} {
		if policy.principal.ID() != PrincipalID || !policy.principal.Active() ||
			!reflect.DeepEqual(policy.principal.Permissions(), wantPermissions) {
			t.Fatalf("policy %d principal = id %q active %t permissions %v", index, policy.principal.ID(), policy.principal.Active(), policy.principal.Permissions())
		}
		encoded, err := policy.hasher.Hash(context.Background(), "article-operator-profile-probe")
		if err != nil {
			t.Fatalf("%s policy Hash(): %v", policy.name, err)
		}
		if err := first.PasswordHasher.ValidateEncoded(encoded); err != nil {
			t.Fatalf("first policy rejects %s encoded profile: %v", policy.name, err)
		}
		if err := second.PasswordHasher.ValidateEncoded(encoded); err != nil {
			t.Fatalf("second policy rejects %s encoded profile: %v", policy.name, err)
		}
	}
}

func TestRuntimeConfigReusesCanonicalCredentialPolicyWithoutRawSecret(t *testing.T) {
	config, err := RuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CredentialPolicy.Principal.ID() != PrincipalID || config.CredentialPolicy.PasswordHasher == nil ||
		config.MaxSessions != maximumSessions || config.AuditCapacity != maximumAuditItems {
		t.Fatalf("RuntimeConfig() = %v", config)
	}
	if got := config.String(); got != "systemstate.RuntimeConfig{redacted}" {
		t.Fatalf("RuntimeConfig diagnostic = %q", got)
	}
}
