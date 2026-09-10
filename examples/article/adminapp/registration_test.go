package adminapp

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/db/sqlite"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestRegisterArticlePublishesIRDerivedTypedConfiguration(t *testing.T) {
	service := testRegistrationService(t)
	installed, err := apps.New([]apps.Config{{
		Name:  "github.com/progresshans/godj/examples/article/models",
		Label: "godj_conformance",
	}})
	if err != nil {
		t.Fatalf("apps.New() error = %v", err)
	}
	builder := admin.NewBuilder(installed)
	if err := RegisterArticle(builder, service); err != nil {
		t.Fatalf("RegisterArticle() error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	descriptor, ok := registry.Lookup("godj_conformance", "article")
	if !ok {
		t.Fatal("Lookup() ok = false")
	}
	if descriptor.Slug != "articles" || descriptor.Model.DBTable != "godj_conformance_article" ||
		!reflect.DeepEqual(descriptor.ListFields, []string{"id", "title", "published", "summary"}) ||
		!reflect.DeepEqual(descriptor.SearchFields, []string{"title", "summary"}) ||
		len(descriptor.FormFields) != 3 || descriptor.FormFields[0].Name() != "title" ||
		len(descriptor.Actions) != 1 || descriptor.Actions[0].Name != "publish" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if descriptor.Permissions.View != ArticleViewPermission || descriptor.Actions[0].Permission != ArticleChangePermission {
		t.Fatalf("permissions = %#v, actions = %#v", descriptor.Permissions, descriptor.Actions)
	}
}

func TestRegisterArticleRejectsInvalidOrDuplicateStartupService(t *testing.T) {
	installed, err := apps.New([]apps.Config{{Name: "article", Label: "godj_conformance"}})
	if err != nil {
		t.Fatalf("apps.New() error = %v", err)
	}
	if err := RegisterArticle(admin.NewBuilder(installed), Service{}); err == nil {
		t.Fatal("RegisterArticle(zero service) error = nil")
	}
	builder := admin.NewBuilder(installed)
	service := testRegistrationService(t)
	if err := RegisterArticle(builder, service); err != nil {
		t.Fatalf("first RegisterArticle() error = %v", err)
	}
	if err := RegisterArticle(builder, service); err == nil {
		t.Fatal("second RegisterArticle() error = nil")
	}
}

func TestArticleInputAcceptsOnlyCompleteTypedCleanedValues(t *testing.T) {
	spec, err := formmodel.NewSpec((articlemodels.ArticleDescriptor{}).Metadata())
	if err != nil {
		t.Fatalf("formmodel.NewSpec() error = %v", err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{
		"title":     {"  GoDj  "},
		"published": {"on"},
		"summary":   {""},
	}), nil)
	if err != nil || !bound.Valid() {
		t.Fatalf("Bind() = %#v, %v", bound, err)
	}
	input, err := articleInput(bound.Cleaned())
	if err != nil {
		t.Fatalf("articleInput() error = %v", err)
	}
	if input.Title != "GoDj" || !input.Published || input.Summary != nil {
		t.Fatalf("articleInput() = %#v", input)
	}

	invalid, err := spec.Bind(forms.NewData(map[string][]string{"published": {"on"}}), nil)
	if err != nil || invalid.Valid() {
		t.Fatalf("invalid Bind() = %#v, %v", invalid, err)
	}
	if _, err := articleInput(invalid.Cleaned()); err == nil {
		t.Fatal("articleInput(incomplete cleaned data) error = nil")
	}
}

func testRegistrationService(t *testing.T) Service {
	t.Helper()
	backend, err := sqlite.OpenMemory(context.Background(), "article-admin-registration")
	if err != nil {
		t.Fatalf("sqlite.OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	audit, err := admin.NewAuditLog(16)
	if err != nil {
		t.Fatalf("admin.NewAuditLog() error = %v", err)
	}
	service, err := NewService(backend, audit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}
