package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const externalRelationDeleteFingerprint = "eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58"

type externalRelationDeleteAuthor struct {
	ID      int64
	Name    string
	present bool
}

type externalRelationDeleteAuthorDescriptor struct{}

func (externalRelationDeleteAuthorDescriptor) Metadata() ir.Model {
	return externalRelationDeleteAuthorModel()
}

func (externalRelationDeleteAuthorDescriptor) Scan(db.Row) (externalRelationDeleteAuthor, error) {
	return externalRelationDeleteAuthor{}, errors.New("external relation delete scan is unused")
}

func (externalRelationDeleteAuthorDescriptor) CloneModel(value externalRelationDeleteAuthor) externalRelationDeleteAuthor {
	return value
}

func (externalRelationDeleteAuthorDescriptor) PrimaryKey(value externalRelationDeleteAuthor) (query.Value, bool) {
	return query.Integer(value.ID), value.present
}

func (externalRelationDeleteAuthorDescriptor) SetPrimaryKey(value *externalRelationDeleteAuthor, key int64) {
	value.ID = key
	value.present = true
}

func (externalRelationDeleteAuthorDescriptor) ClearPrimaryKey(value *externalRelationDeleteAuthor) {
	value.ID = 0
	value.present = false
}

func (externalRelationDeleteAuthorDescriptor) CloneWriteModel(value externalRelationDeleteAuthor) externalRelationDeleteAuthor {
	return value
}

func (externalRelationDeleteAuthorDescriptor) WriteFieldValue(
	value externalRelationDeleteAuthor,
	field ir.Field,
) (query.Value, bool) {
	switch field.Name {
	case "id":
		return query.Integer(value.ID), true
	case "name":
		return query.String(value.Name), true
	default:
		return query.Value{}, false
	}
}

type externalRelationDeleteRows struct {
	closed bool
}

func (*externalRelationDeleteRows) Next() bool        { return false }
func (*externalRelationDeleteRows) Scan(...any) error { return errors.New("empty rows cannot scan") }
func (*externalRelationDeleteRows) Err() error        { return nil }
func (rows *externalRelationDeleteRows) Close() error {
	rows.closed = true
	return nil
}

type externalRelationDeleteSession struct {
	rows       *externalRelationDeleteRows
	setNull    []query.RelationSetNullPlan
	deletes    []query.DeletePlan
	queryPlans []query.Plan
}

func (session *externalRelationDeleteSession) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	session.queryPlans = append(session.queryPlans, plan)
	return session.rows, nil
}

func (*externalRelationDeleteSession) Insert(context.Context, query.InsertPlan) (int64, error) {
	return 0, errors.New("unexpected insert")
}

func (*externalRelationDeleteSession) Update(context.Context, query.UpdatePlan) (int64, error) {
	return 0, errors.New("unexpected update")
}

func (session *externalRelationDeleteSession) Delete(_ context.Context, plan query.DeletePlan) (int64, error) {
	session.deletes = append(session.deletes, plan)
	return 1, nil
}

func (session *externalRelationDeleteSession) RelationSetNull(
	_ context.Context,
	plan query.RelationSetNullPlan,
) (int64, error) {
	session.setNull = append(session.setNull, plan)
	return 2, nil
}

type externalRelationDeleteBackend struct {
	session *externalRelationDeleteSession
	calls   int
}

func (backend *externalRelationDeleteBackend) AtomicRelation(
	_ context.Context,
	callback func(db.RelationSession) error,
) error {
	backend.calls++
	return callback(backend.session)
}

var (
	_ orm.WriteDescriptor[externalRelationDeleteAuthor] = externalRelationDeleteAuthorDescriptor{}
	_ db.RelationSession                                = (*externalRelationDeleteSession)(nil)
	_ db.RelationAtomic                                 = (*externalRelationDeleteBackend)(nil)
)

func TestExternalConsumerBindsAndExecutesRelationDeleter(t *testing.T) {
	t.Parallel()

	authors, blog := externalRelationDeleteSchemas()
	binding, err := orm.BindProject(blog, authors)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	deleter, err := orm.BindRelationDeleter[externalRelationDeleteAuthor](
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		externalRelationDeleteAuthorDescriptor{},
		externalRelationDeleteFingerprint,
	)
	if err != nil {
		t.Fatalf("BindRelationDeleter() error = %v", err)
	}

	rows := &externalRelationDeleteRows{}
	session := &externalRelationDeleteSession{rows: rows}
	backend := &externalRelationDeleteBackend{session: session}
	target := externalRelationDeleteAuthor{ID: 2, Name: "Bob", present: true}
	deleted, err := deleter.Delete(context.Background(), backend, &target)
	if err != nil || deleted != 1 {
		t.Fatalf("Delete() = (%d, %v)", deleted, err)
	}
	if target.ID != 0 || target.present || target.Name != "Bob" || backend.calls != 1 || !rows.closed {
		t.Fatalf("external committed state = target %#v backend calls %d rows closed %v", target, backend.calls, rows.closed)
	}
	if len(session.queryPlans) != 1 || len(session.setNull) != 1 || len(session.deletes) != 1 {
		t.Fatalf("external plan counts = query %d set-null %d delete %d", len(session.queryPlans), len(session.setNull), len(session.deletes))
	}
}

func externalRelationDeleteAuthorModel() ir.Model {
	return ir.Model{
		Name:    "author",
		GoName:  "Author",
		DBTable: "authors_author",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 100},
		},
	}
}

func externalRelationDeleteSchemas() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models:        []ir.Model{externalRelationDeleteAuthorModel()},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "post",
			GoName:  "Post",
			DBTable: "blog_post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
				{
					Name: "reviewer", GoName: "ReviewerID", Column: "reviewer_id", Kind: ir.FieldForeignKey,
					Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "reviewed_posts"},
						OnDelete:    ir.DeleteSetNull,
					},
				},
			},
		}},
	}
	return authors, blog
}
