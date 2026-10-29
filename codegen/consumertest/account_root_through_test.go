package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/root_through_test.go.tmpl
var accountRootThroughConsumer string

func TestGeneratedAccountRootThroughCreation(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			for _, kind := range []string{"account", "group", "permission"} {
				t.Run(kind, func(t *testing.T) {
					root := writeAccountRootThroughConsumer(t, custom, kind)
					required := []string{"TestRootThroughCreation"}
					for _, operation := range []string{"ordinary", "first"} {
						required = append(required, "TestRootThroughCreation/"+operation)
						for _, mode := range []string{"create", "empty", "limit", "endpoint", "denied_add", "denied_related", "revoked", "snapshot_cleanup", "hash_failure", "late_validator", "late_payload", "rollback", "unknown", "restart", "zero_target", "negative_target", "generated_source_key", "configuration", "canceled"} {
							required = append(required, "TestRootThroughCreation/"+operation+"/"+mode)
						}
					}
					runAccountConsumerDatabases(t, root, required, "TestRootThroughCreation")
				})
			}
		})
	}
}

func writeAccountRootThroughConsumer(t *testing.T, custom bool, kind string) string {
	t.Helper()
	var root string
	var spec codegen.ProjectSpec
	if kind == "account" {
		root, spec = writeAccountCreationGraphConsumerWithSpec(t, custom, "profile")
	} else {
		root, spec = writeAccountCatalogThroughConsumerWithSpec(t, custom, kind)
	}
	types := make(map[ir.ModelIdentity]string)
	var imports []string
	for i, app := range spec.Apps {
		alias, used := fmt.Sprintf("model%d", i), false
		for _, model := range app.Schema.Models {
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			types[identity] = alias + "." + model.GoName
			used = used || identity == spec.Account.Model || identity == spec.Account.Catalog.Group || identity == spec.Account.Catalog.Permission
		}
		if used {
			imports = append(imports, fmt.Sprintf("%s %q", alias, app.Package.ImportPath))
		}
	}
	profile, prefix, groups, permissions := "first_name", "IdentityUser", "Groups", "Permissions"
	if custom {
		profile, prefix, groups, permissions = "region", "PeopleMember", "Teams", "Grants"
	}
	field, collection, source, target := "catalog_links", "CatalogLinks", "link_owner", "link_target"
	through := spec.Account.Catalog.Group
	factory := "NewGroupThroughCreation"
	if kind == "permission" {
		through, factory = spec.Account.Catalog.Permission, "NewPermissionThroughCreation"
	}
	if kind == "account" {
		through, field, collection, source, target = spec.Account.Model, "delegates", "Delegates", "link_parent", "catalog_permission"
	}
	policy := `
 config := accountmodel.CatalogThroughConfig[@@THROUGH@@]{Creation:accountmodel.CatalogModelConfig[@@THROUGH@@]{ReadFields:childFields,WriteFields:childFields,Permissions:accountmodel.CatalogPermissions{View:"nested.view",Add:"nested.add",Change:"nested.change",Delete:"nested.delete"},Validate:validateNested},RelatedPermissions:nestedRelated,
 Prepare:func(ctx context.Context,pair accountmodel.ThroughEndpoints)(orm.ModelInput[@@THROUGH@@],error){return prepareNested(ctx,pair)}}
 policy,err:=accountmodel.@@FACTORY@@(binding,@@THROUGH@@Descriptor{},overlay,config);check(err)
 `
	if kind == "account" {
		policy = `
 sourcePolicy,err:=accountmodel.NewProfileRelation(binding,"@@SOURCE@@",objects,accountmodel.ProfileRelationConfig[@@ACCOUNT@@]{Choices:formmodel.ModelChoiceConfig[@@ACCOUNT@@]{Label:func(@@ACCOUNT@@)(string,error){return "Parent",nil}},Filter:func(context.Context,db.Queryer,auth.Principal,accountmodel.ProfileRelationOrigin)([]orm.Predicate[@@ACCOUNT@@],error){return nil,nil}});check(err)
 targetPolicy,err:=accountmodel.NewProfileRelation(binding,"@@TARGET@@",permissions,accountmodel.ProfileRelationConfig[@@PERMISSION@@]{Choices:formmodel.ModelChoiceConfig[@@PERMISSION@@]{Label:func(@@PERMISSION@@)(string,error){return "Target",nil}},Filter:func(context.Context,db.Queryer,auth.Principal,accountmodel.ProfileRelationOrigin)([]orm.Predicate[@@PERMISSION@@],error){return nil,nil}});check(err)
 policy,err:=accountmodel.NewAccountThroughCreation(binding,collections.@@PREFIX@@@@GROUPS@@,collections.@@PREFIX@@@@PERMISSIONS@@,overlay,hasher,accountmodel.CreationConfig[@@ACCOUNT@@]{CreationPolicy:accountmodel.CreationPolicy[@@ACCOUNT@@]{Fields:append(slices.Clone(childFields),"@@SOURCE@@","@@TARGET@@"),Relations:[]accountmodel.ProfileRelation[@@ACCOUNT@@]{sourcePolicy,targetPolicy},RelatedPermissions:nestedRelated,Validate:func(ctx context.Context,reader db.Queryer,row @@ACCOUNT@@,_ accountmodel.AccountMemberships)(validation.Errors,error){return validateNested(ctx,reader,nil,row)}},AddPermission:"nested.add",ChangePermission:"nested.change"},accountmodel.AccountThroughConfig[@@ACCOUNT@@]{Prepare:func(ctx context.Context,pair accountmodel.ThroughEndpoints)(accountmodel.AccountThroughInput[@@ACCOUNT@@],error){input,e:=prepareNested(ctx,pair);return accountmodel.AccountThroughInput[@@ACCOUNT@@]{Input:input,Password:accountPasswordText},e}});check(err)
 `
	}
	sourceText := strings.ReplaceAll(accountRootThroughConsumer, "@@POLICY@@", policy)
	sourceText = strings.NewReplacer("@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@IMPORTS@@", strings.Join(imports, "\n"), "@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission], "@@THROUGH@@", types[through], "@@KIND@@", kind, "@@FIELD@@", field, "@@COLLECTION@@", collection, "@@SOURCE@@", source, "@@TARGET@@", target, "@@PROFILE@@", profile, "@@PREFIX@@", prefix, "@@GROUPS@@", groups, "@@PERMISSIONS@@", permissions, "@@FACTORY@@", factory).Replace(sourceText)
	formatted, err := format.Source([]byte(sourceText))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/root_through_test.go", formatted)
	return root
}
