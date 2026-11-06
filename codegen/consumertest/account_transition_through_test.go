package codegen_test

import (
	_ "embed"
	"encoding/json"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/transition_through_test.go.tmpl
var accountTransitionThroughConsumer string

func transitionThroughCases(kind string) []string {
	result := []string{"create", "signed_keys", "limit", "count_limit", "duplicate", "endpoint", "target_key", "denied_add", "denied_related", "revoked", "source_changed", "read_cleanup", "insert_failure", "mapping_failure", "late_payload", "rollback", "unknown_commit", "unknown_rollback", "owned_manifest", "restart", "payload_corruption", "configuration", "canceled"}
	if kind == "account" {
		result = append(result, "hash_failure", "retained_identity", "late_membership")
	}
	if kind != "permission" {
		result = append(result, "staged")
	}
	return result
}

func TestGeneratedAccountTransitionThroughCreation(t *testing.T) {
	for _, kind := range []string{"account", "group", "permission"} {
		t.Run(kind, func(t *testing.T) {
			root := writeAccountTransitionThroughConsumer(t, kind)
			required := []string{"TestTransitionThroughCreation"}
			for _, name := range transitionThroughCases(kind) {
				required = append(required, "TestTransitionThroughCreation/"+name)
			}
			runAccountConsumerDatabases(t, root, required, "TestTransitionThroughCreation")
		})
	}
}

func writeAccountTransitionThroughConsumer(t *testing.T, kind string) string {
	t.Helper()
	spec, role := accountCatalogTransitionSpec(t)
	spec.Apps = slices.DeleteFunc(spec.Apps, func(app codegen.AppSpec) bool { return app.Schema.AppLabel == "host" })
	through, owner, target, field := role.Model, role.Model, role.Catalog.Group, role.Groups
	typeName, natural, code := "members.Member", "handle", ""
	if kind == "group" {
		through, owner, target, field = role.Catalog.Group, role.Catalog.Group, role.Catalog.Permission, role.Catalog.GroupPermissions
		typeName, natural = "catalog.Team", "title"
	}
	if kind == "permission" {
		through, owner, target, field = role.Catalog.Permission, role.Model, spec.Account.Catalog.Permission, "interests"
		typeName, natural, code = "catalog.Grant", "label", "symbol"
	}
	for i := range spec.Apps {
		app := &spec.Apps[i]
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			model.ManyToMany = slices.DeleteFunc(model.ManyToMany, func(f ir.ManyToManyField) bool {
				return f.Name == "topics" || strings.HasPrefix(f.Name, "legacy_") || f.Name == "new_permissions"
			})
			if identity == owner {
				for k := range model.ManyToMany {
					if model.ManyToMany[k].Name == field {
						model.ManyToMany[k].Through = &ir.ThroughModel{Model: through, SourceField: "link_owner", TargetField: "link_target"}
					}
				}
			}
			if identity == through {
				for _, endpoint := range []struct {
					name, goName string
					target       ir.ModelIdentity
				}{{"link_owner", "LinkOwnerID", owner}, {"link_target", "LinkTargetID", target}} {
					model.Fields = append(model.Fields, ir.Field{Name: endpoint.name, GoName: endpoint.goName, Column: endpoint.name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Relation: &ir.ForeignKeyRelation{Target: endpoint.target, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
				}
			}
		}
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	encoded, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	for _, name := range transitionThroughCases(kind) {
		cases = append(cases, strconv.Quote(name))
	}
	policy := `policy,err:=accountmodel.NewGroupThroughCreation(binding,catalog.TeamDescriptor{},overlay,accountmodel.CatalogThroughConfig[catalog.Team]{Creation:accountmodel.CatalogModelConfig[catalog.Team]{ReadFields:childFields,WriteFields:childFields,Permissions:accountmodel.CatalogPermissions{View:"nested.view",Add:"nested.add",Change:"nested.change",Delete:"nested.delete"}},RelatedPermissions:related,Prepare:prepareNested});nuMust(t,err)`
	if kind == "permission" {
		policy = strings.ReplaceAll(policy, "NewGroupThroughCreation", "NewPermissionThroughCreation")
		policy = strings.ReplaceAll(policy, "catalog.Team", "catalog.Grant")
	}
	if kind == "account" {
		policy = `
 sourceChoice,err:=accountmodel.NewProfileRelation(binding,"link_owner",members.MemberObjects,accountmodel.ProfileRelationConfig[members.Member]{Choices:formmodel.ModelChoiceConfig[members.Member]{Label:func(members.Member)(string,error){return "Owner",nil}},Filter:func(context.Context,db.Queryer,auth.Principal,accountmodel.ProfileRelationOrigin)([]orm.Predicate[members.Member],error){return nil,nil}});nuMust(t,err)
 targetChoice,err:=accountmodel.NewProfileRelation(binding,"link_target",catalog.TeamObjects,accountmodel.ProfileRelationConfig[catalog.Team]{Choices:formmodel.ModelChoiceConfig[catalog.Team]{Label:func(catalog.Team)(string,error){return "Target",nil}},Filter:func(context.Context,db.Queryer,auth.Principal,accountmodel.ProfileRelationOrigin)([]orm.Predicate[catalog.Team],error){return nil,nil}});nuMust(t,err)
 policy,err:=accountmodel.NewAccountThroughCreation(binding,collections.MembersMemberGroups,collections.MembersMemberPermissions,overlay,hasher,accountmodel.CreationConfig[members.Member]{CreationPolicy:accountmodel.CreationPolicy[members.Member]{Fields:[]string{"handle","link_owner","link_target"},Relations:[]accountmodel.ProfileRelation[members.Member]{sourceChoice,targetChoice},RelatedPermissions:related},AddPermission:"nested.add",ChangePermission:"nested.change"},accountmodel.AccountThroughConfig[members.Member]{Prepare:func(ctx context.Context,pair accountmodel.ThroughEndpoints)(accountmodel.AccountThroughInput[members.Member],error){input,err:=prepareNested(ctx,pair);return accountmodel.AccountThroughInput[members.Member]{Input:input,Password:accountPasswordText},err}});nuMust(t,err)
 targetOptions.Fields=append(targetOptions.Fields,"link_owner","link_target");targetOptions.Relations=[]accountmodel.ProfileRelation[members.Member]{sourceChoice,targetChoice}
 `
	}
	targetConfig := `targetOptions.ThroughCreations=map[string]accountmodel.ThroughCreation{"groups":policy};catalogOptions.AccountThroughCreations=map[string]accountmodel.ThroughCreation{"groups":policy}`
	if kind == "group" {
		targetConfig = `catalogOptions.GroupThroughCreations=map[string]accountmodel.ThroughCreation{"grants":policy}`
	}
	if kind == "permission" {
		targetConfig = `
 choice,err:=accountmodel.NewProfileCollection(binding,"interests",collections.MembersMemberInterests,models.PermissionObjects,accountmodel.ProfileCollectionConfig[models.Permission]{MaximumItems:4,Choices:formmodel.ModelChoiceConfig[models.Permission]{Label:func(row models.Permission)(string,error){return row.Code,nil}},Filter:func(context.Context,db.Queryer,auth.Principal,accountmodel.ProfileRelationOrigin)([]orm.Predicate[models.Permission],error){return nil,nil}});nuMust(t,err)
 targetOptions.Fields=append(targetOptions.Fields,"interests");targetOptions.Collections=[]accountmodel.ProfileCollection[members.Member]{choice};targetOptions.ThroughCreations=map[string]accountmodel.ThroughCreation{"interests":policy}
 `
	}
	var snapshots []string
	for _, entry := range []struct{ kind, name, objects string }{{"account", "member_groups", "members.MemberGroupsLinkObjects"}, {"group", "group_grants", "catalog.TeamGrantsLinkObjects"}, {"permission", "profile", "members.MemberInterestsLinkObjects"}} {
		if entry.kind != kind {
			snapshots = append(snapshots, strconv.Quote(entry.name)+":nuRows(t,scope,"+entry.objects+"),")
		}
	}
	formImport := ""
	if kind != "group" {
		formImport = `formmodel "github.com/progresshans/godj/forms/model"`
	}
	source := strings.NewReplacer("@@FORMIMPORT@@", formImport, "@@ROLE@@", string(encoded), "@@KIND@@", kind, "@@THROUGH@@", typeName, "@@NATURAL@@", natural, "@@CODE@@", code, "@@CASES@@", strings.Join(cases, ","), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec), "@@POLICY@@", policy, "@@TARGETCONFIG@@", targetConfig, "@@SNAPSHOTS@@", strings.Join(snapshots, "\n")).Replace(accountTransitionThroughConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_through_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
