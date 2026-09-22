package codegen

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/internal/relationpolicy"
	"github.com/progresshans/godj/schema/ir"
)

type projectManyToMany struct {
	owner, target, through     *projectRelationModel
	name, surface, fingerprint string
	reverse                    bool
}

func buildProjectManyToMany(plan *relationProjectPlan) ([]projectManyToMany, int, error) {
	schemas := make([]ir.Schema, len(plan.apps))
	for i, app := range plan.apps {
		schemas[i] = app.schema
	}
	bindings, err := ir.ResolveManyToMany(schemas...)
	if err != nil {
		return nil, 0, err
	}
	deletes, err := plan.deleteSurface()
	if err != nil {
		return nil, 0, err
	}
	var result []projectManyToMany
	for _, binding := range bindings {
		owner, target, through := plan.byIdentity[binding.Source], plan.byIdentity[binding.Target], plan.byIdentity[binding.Through.Model]
		if owner == nil || target == nil || through == nil {
			return nil, 0, fmt.Errorf("collection models are missing from project storage")
		}
		key, ok := projectRelationObjectAutoPrimaryKey(through.model)
		if !ok {
			return nil, 0, fmt.Errorf("collection through requires an AutoField primary key")
		}
		fingerprint := relationpolicy.Fingerprint(relationpolicy.ModelKey{Identity: through.identity, Table: through.model.DBTable, PrimaryKeyName: key.Name, PrimaryKeyColumn: key.Column}, nil)
		for _, target := range deletes {
			if target.model == through {
				fingerprint = target.fingerprint
				break
			}
		}
		var selector string
		for _, field := range owner.model.ManyToMany {
			if field.Name == binding.Field {
				selector = field.GoName
				break
			}
		}
		if selector == "" {
			return nil, 0, fmt.Errorf("collection declaration is missing from its owner")
		}
		result = append(result, projectManyToMany{owner: owner, target: target, through: through, name: binding.Field, surface: owner.app.prefix + owner.model.GoName + selector, fingerprint: fingerprint})
		if !binding.Reverse.Disabled {
			selector, err := relationReverseSelector(binding.Reverse.Name)
			if err != nil {
				return nil, 0, err
			}
			result = append(result, projectManyToMany{owner: target, target: owner, through: through, name: binding.Reverse.Name, surface: target.app.prefix + target.model.GoName + selector, fingerprint: fingerprint, reverse: true})
		}
	}
	return result, len(bindings), nil
}

func renderProjectManyToMany(output *bytes.Buffer, plan *relationProjectPlan, relations []projectManyToMany, count int) {
	if len(relations) == 0 {
		return
	}
	fmt.Fprintln(output, "type Collections struct {")
	for _, relation := range relations {
		fmt.Fprintf(output, "\t%s orm.ManyToMany[%s.%s, %s.%s, %s.%s]\n", relation.surface, relation.owner.app.alias, relation.owner.model.GoName, relation.target.app.alias, relation.target.model.GoName, relation.through.app.alias, relation.through.model.GoName)
	}
	fmt.Fprintln(output, "}")
	fmt.Fprintln(output, "func BindCollections() (Collections, error) {")
	renderProjectModelBindings(output, plan.models, "Collections", nil)
	fmt.Fprintf(output, "\tif len(_binding.ManyToManyRelations()) != %d { return Collections{}, &query.Error{Category:query.CategoryQuery,Code:query.CodeInvalidPlan,Detail:\"generated collection set does not match project binding\"} }\n", count)
	used := make(map[int]bool)
	for index, relation := range relations {
		binder := "BindManyToMany"
		if relation.reverse {
			binder = "BindReverseManyToMany"
		}
		fmt.Fprintf(output, "\t_relation%d, _err := orm.%s(_model%d, %s, _model%d, _model%d, %s)\n", index, binder, relation.owner.bind, strconv.Quote(relation.name), relation.target.bind, relation.through.bind, strconv.Quote(relation.fingerprint))
		fmt.Fprintln(output, "\tif _err != nil { return Collections{}, _err }")
		used[relation.owner.bind], used[relation.target.bind], used[relation.through.bind] = true, true, true
	}
	for _, model := range plan.models {
		if !used[model.bind] {
			fmt.Fprintf(output, "\t_ = _model%d\n", model.bind)
		}
	}
	fmt.Fprintln(output, "\treturn Collections{")
	for index, relation := range relations {
		fmt.Fprintf(output, "\t\t%s: _relation%d,\n", relation.surface, index)
	}
	fmt.Fprintln(output, "\t}, nil")
	fmt.Fprintln(output, "}")
}
