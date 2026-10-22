package admin

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

type testChoiceRead func(context.Context, auth.Principal) ([]forms.Choice, error)

// These fixtures exercise the public declaration and bounded query path. The
// in-memory backend implements only the predicates the fixture declares, and
// applies selected-key membership, ordering, offset and the SQL row limit.
func testRelationChoices(t *testing.T, model ir.Model, app, field string, permission auth.Permission, read testChoiceRead) RelatedChoices {
	t.Helper()
	var definition ir.Schema
	var err error
	if app == "helpdesk" {
		definition, err = modeldef.Schema()
		for index := range definition.Models {
			if definition.Models[index].Name == model.Name {
				definition.Models[index] = model
			}
		}
	} else {
		definition, err = schema.Build(schema.Definition{AppLabel: app, Models: []schema.Model{{Name: "label", GoName: "Label", Fields: []schema.Field{schema.CharField("name", "Name", 100)}}}})
		definition.Models = append(definition.Models, model)
	}
	if err != nil {
		t.Fatal(err)
	}
	project, err := orm.BindProject(definition)
	if err != nil {
		t.Fatal(err)
	}
	source := ir.ModelIdentity{AppLabel: app, ModelName: model.Name}
	var target ir.ModelIdentity
	if relation, found := project.Relation(source, field); found {
		target = relation.Target
	}
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == source && relation.Field == field {
			target = relation.Target
		}
	}
	metadata, found := project.Model(target)
	if !found {
		t.Fatal("test relation target is absent")
	}
	choices, err := formmodel.NewRelationChoices(project, source, field, orm.NewManager[forms.Choice](testChoiceDescriptor{metadata}), testChoicePolicy(metadata))
	if err != nil {
		t.Fatal(err)
	}
	return testChoiceAdapter(t, field, permission, choices, read)
}

func testTargetChoices(t *testing.T, field string, permission auth.Permission, read testChoiceRead) RelatedChoices {
	t.Helper()
	definition, err := schema.Build(schema.Definition{AppLabel: "choice_targets", Models: []schema.Model{{Name: "row", GoName: "Row", Fields: []schema.Field{schema.CharField("name", "Name", 100)}}}})
	if err != nil {
		t.Fatal(err)
	}
	project, err := orm.BindProject(definition)
	if err != nil {
		t.Fatal(err)
	}
	metadata := definition.Models[0]
	choices, err := formmodel.NewModelChoices(project, ir.ModelIdentity{AppLabel: definition.AppLabel, ModelName: metadata.Name}, orm.NewManager[forms.Choice](testChoiceDescriptor{metadata}), testChoicePolicy(metadata))
	if err != nil {
		t.Fatal(err)
	}
	return testChoiceAdapter(t, field, permission, choices, read)
}

func testChoicePolicy(metadata ir.Model) formmodel.ModelChoiceConfig[forms.Choice] {
	var name ir.Field
	for _, field := range metadata.Fields {
		if field.Name == "name" {
			name = field
		}
	}
	return formmodel.ModelChoiceConfig[forms.Choice]{Label: func(row forms.Choice) (string, error) { return row.Label, nil }, Search: func(term string) ([]orm.Predicate[forms.Choice], error) {
		if term == "" {
			return nil, nil
		}
		return []orm.Predicate[forms.Choice]{orm.NewStringField[forms.Choice](name).IContains(term)}, nil
	}}
}

func testChoiceAdapter(t *testing.T, field string, permission auth.Permission, choices formmodel.ModelChoices[forms.Choice], read testChoiceRead) RelatedChoices {
	t.Helper()
	result, err := NewRelatedChoices(RelatedChoiceConfig[forms.Choice]{Field: field, Choices: choices, Permission: permission, Scope: testChoiceScope(read)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testChoiceScope(read testChoiceRead) func(context.Context, auth.Principal, ChoiceOrigin, func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
	return func(_ context.Context, actor auth.Principal, _ ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[forms.Choice]) error) error {
		return visit(testChoiceQueryer{actor: actor, read: read}, nil)
	}
}

func testChoicesWithRead(source RelatedChoices, read testChoiceRead) RelatedChoices {
	config := source.(relatedChoices[forms.Choice]).config
	config.Scope = testChoiceScope(read)
	return relatedChoices[forms.Choice]{config}
}

type testChoiceDeclaration struct {
	RelatedChoices
	field      string
	permission auth.Permission
}

func (source testChoiceDeclaration) choiceField() string               { return source.field }
func (source testChoiceDeclaration) choicePermission() auth.Permission { return source.permission }
func testChoicesWithField(source RelatedChoices, field string) RelatedChoices {
	return testChoiceDeclaration{RelatedChoices: source, field: field, permission: source.choicePermission()}
}
func testChoicesWithPermission(source RelatedChoices, permission auth.Permission) RelatedChoices {
	return testChoiceDeclaration{RelatedChoices: source, field: source.choiceField(), permission: permission}
}
func testChoicesWithoutScope(t *testing.T, source RelatedChoices) RelatedChoices {
	t.Helper()
	config := source.(relatedChoices[forms.Choice]).config
	config.Scope = nil
	result, err := NewRelatedChoices(config)
	if err == nil || result != nil {
		t.Fatal("choice constructor accepted a missing scope callback")
	}
	return result
}

type testChoiceDescriptor struct{ metadata ir.Model }

func (descriptor testChoiceDescriptor) Metadata() ir.Model            { return descriptor.metadata.Clone() }
func (testChoiceDescriptor) CloneModel(row forms.Choice) forms.Choice { return row }
func (testChoiceDescriptor) PrimaryKey(row forms.Choice) (query.Value, bool) {
	if key, valid := row.Value.AsInteger(); valid {
		return query.Integer(key), true
	}
	return query.String("invalid fixture key"), true
}
func (testChoiceDescriptor) Scan(row db.Row) (forms.Choice, error) {
	var result forms.Choice
	err := row.Scan(&result)
	return result, err
}

type testChoiceQueryer struct {
	actor auth.Principal
	read  testChoiceRead
}

func (backend testChoiceQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, bounded := plan.Limit()
	if !bounded || limit < 1 || backend.read == nil {
		return nil, errors.New("unbounded or uninitialized test choice query")
	}
	items, err := backend.read(ctx, backend.actor)
	if err != nil {
		return nil, err
	}
	items = slices.Clone(items)
	for _, condition := range plan.Conditions() {
		if condition.Lookup() == query.LookupIn && condition.Field().Name() == "id" {
			values, valid := condition.Values()
			if !valid {
				return nil, errors.New("invalid test key membership query")
			}
			items = slices.DeleteFunc(items, func(item forms.Choice) bool {
				key, integer := item.Value.AsInteger()
				if !integer {
					return false
				} // retain malformed backend rows for the service's rejection
				return !slices.ContainsFunc(values, func(value query.Value) bool { requested, _ := value.Integer(); return key == requested })
			})
		} else if condition.Lookup() == query.LookupIContains && condition.Field().Name() == "name" {
			term, _ := condition.Value().String()
			items = slices.DeleteFunc(items, func(item forms.Choice) bool {
				return !strings.Contains(strings.ToLower(item.Label), strings.ToLower(term))
			})
		} else {
			return nil, errors.New("unsupported test choice predicate")
		}
	}
	slices.SortStableFunc(items, func(left, right forms.Choice) int {
		one, _ := left.Value.AsInteger()
		two, _ := right.Value.AsInteger()
		if one < two {
			return -1
		}
		if one > two {
			return 1
		}
		return 0
	})
	offset, _ := plan.Offset()
	offset = min(offset, len(items))
	items = items[offset:min(offset+limit, len(items))]
	return &testChoiceRows{items: items, index: -1}, nil
}

type testChoiceRows struct {
	items []forms.Choice
	index int
}

func (rows *testChoiceRows) Next() bool { rows.index++; return rows.index < len(rows.items) }
func (*testChoiceRows) Err() error      { return nil }
func (*testChoiceRows) Close() error    { return nil }
func (rows *testChoiceRows) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected test choice projection")
	}
	result, valid := destinations[0].(*forms.Choice)
	if !valid {
		return errors.New("unexpected test choice destination")
	}
	*result = rows.items[rows.index]
	return nil
}
