// Package siteappconformance exposes narrow observations of the Article
// example's real startup composition to repository conformance tests. It is
// not a second application builder and does not reproduce siteapp decisions.
package siteappconformance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
)

// CompositionObservation contains only route-publication facts obtained from
// one call to the real Article siteapp.New composition boundary.
type CompositionObservation struct {
	ApplicationCreated bool
	ArticlePublished   bool
	AdminPublished     bool
	APIPublished       bool
}

// ProvisionPolicyMismatch installs a valid encoded credential with the exact
// Article hash profile and permissions but a different immutable principal.
// It is fixture setup for proving that siteapp.New propagates the real policy
// mismatch instead of downgrading it to public-only mode.
func ProvisionPolicyMismatch(
	ctx context.Context,
	backend systemstate.Backend,
	username string,
	password string,
) error {
	canonical, err := operatorconfig.CredentialPolicy()
	if err != nil {
		return fmt.Errorf("load canonical Article operator policy: %w", err)
	}
	mismatch, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          operatorconfig.PrincipalID + "-mismatch",
		Active:      canonical.Principal.Active(),
		Permissions: canonical.Principal.Permissions(),
	})
	if err != nil {
		return fmt.Errorf("construct mismatched Article operator principal: %w", err)
	}
	return systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{
		Username: username,
		Password: password,
		CredentialPolicy: systemstate.CredentialPolicy{
			Principal:      mismatch,
			PasswordHasher: canonical.PasswordHasher,
		},
	})
}

// PublicOnly reports whether the real composition published precisely the
// public Article surface and withheld both authenticated surfaces.
func (observation CompositionObservation) PublicOnly() bool {
	return observation.ApplicationCreated && observation.ArticlePublished &&
		!observation.AdminPublished && !observation.APIPublished
}

// ObserveComposition calls the real Article startup composition. Startup
// failures are returned unchanged so the caller can verify the stable
// system-state classification rather than inferring it from a synthetic flag.
func ObserveComposition(
	ctx context.Context,
	backend systemstate.Backend,
) (CompositionObservation, error) {
	application, err := siteapp.New(ctx, siteapp.NewConfig(backend))
	observation := CompositionObservation{ApplicationCreated: application != nil}
	if err != nil {
		return observation, err
	}
	if application == nil {
		return CompositionObservation{}, errors.New("Article site composition returned a nil application")
	}

	articlePath, err := application.Reverse(webapp.ArticleListRoute)
	if err != nil {
		return CompositionObservation{}, fmt.Errorf("reverse public Article route: %w", err)
	}
	observation.ArticlePublished = articlePath == webapp.ArticleListPath
	if _, err := application.Reverse("admin:index"); err == nil {
		observation.AdminPublished = true
	} else if !errors.Is(err, &web.Error{Code: web.CodeReverseNotFound}) {
		return CompositionObservation{}, fmt.Errorf("inspect Admin route publication: %w", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test"+apiapp.ListPath, nil).WithContext(ctx)
	application.ServeHTTP(recorder, request)
	observation.APIPublished = recorder.Code != http.StatusNotFound
	return observation, nil
}
