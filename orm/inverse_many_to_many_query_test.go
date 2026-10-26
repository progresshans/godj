package orm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestInverseCollectionQueryRetainsDeclarationAndOwner(t *testing.T) {
	graph, err := orm.BindProject(models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	user, err := orm.BindModel(graph, ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "user"}, models.UserDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := orm.BindModel(graph, ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "permission"}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	group, err := orm.BindModel(graph, ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "group"}, models.GroupDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := orm.BindInverseManyToManyQuery(user, "permissions", permission)
	if err != nil {
		t.Fatal(err)
	}
	published, err := orm.BindQueryRelation(permission, "users", user)
	if err != nil {
		t.Fatal(err)
	}
	left, err := inverse.Integer(models.UserFields.ID)
	if err != nil {
		t.Fatal(err)
	}
	right, err := published.Integer(models.UserFields.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models.PermissionObjects.Using(nil).Filter(left.Exact(0)).Plan(), models.PermissionObjects.Using(nil).Filter(right.Exact(0)).Plan()) {
		t.Fatal("explicit inverse and published reverse did not use the same Query AST")
	}
	foreignGraph, err := orm.BindProject(models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := orm.BindModel(foreignGraph, ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "permission"}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []orm.BoundModel[models.Permission]{foreign, {}} {
		if _, err := orm.BindInverseManyToManyQuery(user, "permissions", target); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("foreign or zero endpoint admitted", err)
		}
	}
	for _, name := range []string{"missing", "users", "username", "groups"} {
		if _, err := orm.BindInverseManyToManyQuery(user, name, permission); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid declaration admitted", name, err)
		}
	}
	if _, err := orm.BindInverseManyToManyQuery(user, "permissions", group); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("wrong typed endpoint admitted", err)
	}
	if _, err := orm.BindInverseManyToManyQuery(orm.BoundModel[models.User]{}, "permissions", permission); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("zero owner admitted", err)
	}
}
