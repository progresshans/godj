package migrationautodetect

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectIdentityThroughHostCyclesResumeEveryPublicationPrefix(t *testing.T) {
	for _, required := range []bool{false, true} {
		name := "nullable"
		if required {
			name = "required"
		}
		t.Run(name, func(t *testing.T) {
			id := func(app, model string) ir.ModelIdentity { return ir.ModelIdentity{AppLabel: app, ModelName: model} }
			user, group, permission := id("identity", "user"), id("identity", "group"), id("identity", "permission")
			member, team, grant := id("members", "member"), id("catalog", "team"), id("catalog", "grant")
			bridge, hub := id("bridges", "bridge"), id("bridges", "hub")
			fk := func(name string, target ir.ModelIdentity) ir.Field {
				field := testForeignKey(name, !required, target.AppLabel, target.ModelName, "")
				field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
				return field
			}
			many := func(name string, target, through ir.ModelIdentity, source, endpoint string) ir.ManyToManyField {
				return ir.ManyToManyField{Name: name, GoName: testGoName(name), Target: target, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: through, SourceField: source, TargetField: endpoint}}
			}
			oldUser, oldGroup, oldPermission := testModel("user"), testModel("group"), testModel("permission")
			newMember, newTeam, newGrant := testModel("member"), testModel("team"), testModel("grant")
			link, host := testModel("bridge"), testModel("hub")
			for _, field := range []struct {
				name   string
				target ir.ModelIdentity
			}{{"old_user", user}, {"member", member}, {"old_group", group}, {"new_group", team}, {"old_permission", permission}, {"new_permission", grant}} {
				link.Fields = append(link.Fields, fk(field.name, field.target))
			}
			oldUser.ManyToMany = []ir.ManyToManyField{many("groups", group, bridge, "old_user", "old_group"), many("permissions", permission, bridge, "old_user", "old_permission")}
			oldGroup.ManyToMany = []ir.ManyToManyField{many("permissions", permission, bridge, "old_group", "old_permission")}
			newMember.ManyToMany = []ir.ManyToManyField{many("groups", team, bridge, "member", "new_group"), many("permissions", grant, bridge, "member", "new_permission")}
			newTeam.ManyToMany = []ir.ManyToManyField{many("permissions", grant, bridge, "new_group", "new_permission")}
			for _, row := range []struct {
				model    *ir.Model
				identity ir.ModelIdentity
			}{{&newMember, member}, {&newTeam, team}, {&newGrant, grant}} {
				row.model.Fields = append(row.model.Fields, fk("identity_source", user), fk("identity_group", group), fk("identity_permission", permission), fk("host_hub", hub))
				oldUser.ManyToMany = append(oldUser.ManyToMany, many(row.identity.ModelName+"_rows", permission, row.identity, "identity_source", "identity_permission"))
				oldGroup.ManyToMany = append(oldGroup.ManyToMany, many(row.identity.ModelName+"_grants", permission, row.identity, "identity_group", "identity_permission"))
				host.ManyToMany = append(host.ManyToMany, many(row.identity.ModelName+"_links", permission, row.identity, "host_hub", "identity_permission"))
			}
			desired := mustProjectState(t, testSchema("identity", oldUser, oldGroup, oldPermission), testSchema("members", newMember), testSchema("catalog", newTeam, newGrant), testSchema("bridges", link, host))
			request := Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}
			plan := mustDetect(t, request).Migrations()
			assertGeneratedState(t, request.Definitions, plan, desired)
			for prefix := 0; prefix <= len(plan); prefix++ {
				history := cloneMigrations(plan[:prefix])
				slices.Reverse(history)
				request.Definitions = mustLoadDefinitions(t, history...)
				remaining := mustDetect(t, request).Migrations()
				if len(remaining) != len(plan)-prefix {
					t.Fatal("identity through publication prefix lost work", prefix)
				}
				for i, migration := range remaining {
					actual, err := definition.Encode(testProducer, migration)
					if err != nil {
						t.Fatal(err)
					}
					want, err := definition.Encode(testProducer, plan[prefix+i])
					if err != nil {
						t.Fatal(err)
					}
					if string(actual) != string(want) {
						t.Fatal("identity through restart changed migration bytes", prefix, i)
					}
				}
			}
		})
	}
}
