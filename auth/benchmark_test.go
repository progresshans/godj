package auth_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/progresshans/godj/auth"
)

func BenchmarkPrincipalResolve(b *testing.B) {
	for _, count := range []int{8, 128} {
		b.Run(fmt.Sprintf("permissions_%d", count), func(b *testing.B) {
			permissions := make([]auth.Permission, count)
			for index := range permissions {
				permissions[index] = auth.Permission(fmt.Sprintf("bench.action%d", index))
			}
			principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true, Permissions: permissions})
			if err != nil {
				b.Fatal(err)
			}
			hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
			if err != nil {
				b.Fatal(err)
			}
			encoded, err := hasher.Hash(context.Background(), "benchmark password")
			if err != nil {
				b.Fatal(err)
			}
			credential, err := auth.NewCredential("operator", encoded, principal)
			if err != nil {
				b.Fatal(err)
			}
			authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				value, err := authenticator.Resolve(context.Background(), "operator")
				if err != nil || !value.Has(permissions[count-1]) {
					b.Fatalf("resolve: %v", err)
				}
			}
		})
	}
}
