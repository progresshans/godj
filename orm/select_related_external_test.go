package orm_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type publicAuthorProjectionScan struct {
	id   sql.NullInt64
	name sql.NullString
}

func (publicAuthorObjectDescriptor) NewProjectionScan() orm.ProjectionScan[relationQueryAuthor] {
	return &publicAuthorProjectionScan{}
}

func (scan *publicAuthorProjectionScan) Destinations() []any {
	return []any{&scan.id, &scan.name}
}

func (scan *publicAuthorProjectionScan) Decode() (relationQueryAuthor, query.Value, orm.ProjectionPresence) {
	switch {
	case !scan.id.Valid && !scan.name.Valid:
		return relationQueryAuthor{}, query.Null(), orm.ProjectionAbsent
	case !scan.id.Valid || !scan.name.Valid:
		return relationQueryAuthor{}, query.Value{}, orm.ProjectionInvalid
	default:
		return relationQueryAuthor{ID: scan.id.Int64, Name: scan.name.String}, query.Integer(scan.id.Int64), orm.ProjectionPresent
	}
}

type publicPostProjectionScan struct {
	id         sql.NullInt64
	authorID   sql.NullInt64
	reviewerID sql.NullInt64
}

func (publicPostObjectDescriptor) NewProjectionScan() orm.ProjectionScan[relationQueryPost] {
	return &publicPostProjectionScan{}
}

func (scan *publicPostProjectionScan) Destinations() []any {
	return []any{&scan.id, &scan.authorID, &scan.reviewerID}
}

func (scan *publicPostProjectionScan) Decode() (relationQueryPost, query.Value, orm.ProjectionPresence) {
	if !scan.id.Valid || !scan.authorID.Valid {
		return relationQueryPost{}, query.Value{}, orm.ProjectionInvalid
	}
	value := relationQueryPost{ID: scan.id.Int64, AuthorID: scan.authorID.Int64}
	if scan.reviewerID.Valid {
		reviewerID := scan.reviewerID.Int64
		value.ReviewerID = &reviewerID
	}
	return value, query.Integer(scan.id.Int64), orm.ProjectionPresent
}

func TestPublicForwardSelectSurfaceCompilesAndWarmsRelatedObject(t *testing.T) {
	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	author, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		publicAuthorObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	post, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		publicPostObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	relation, err := orm.BindRequiredForwardObject(post, "author", author)
	if err != nil {
		t.Fatalf("BindRequiredForwardObject() error = %v", err)
	}
	path, err := orm.ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatalf("ResolveForwardSelectPath() error = %v", err)
	}
	selection, err := orm.BindRequiredForwardSelect(path, relation)
	if err != nil {
		t.Fatalf("BindRequiredForwardSelect() error = %v", err)
	}

	backend := &publicSelectRelatedBackend{}
	source := orm.NewManager[relationQueryPost](publicPostObjectDescriptor{}).Using(backend)
	selected := selection.Select(source)
	if selected.Backend() != backend {
		t.Fatalf("Backend() = %T, want *publicSelectRelatedBackend", selected.Backend())
	}
	projection, ok := selected.Plan().RelationProjection()
	if !ok || projection.Hop().Field() != "author" || projection.Hop().Nullable() {
		t.Fatalf("Plan().RelationProjection() = (%#v, %v)", projection, ok)
	}
	values, err := selected.All(context.Background())
	if err != nil || len(values) != 1 {
		t.Fatalf("All() = (%#v, %v)", values, err)
	}
	sourceValue, err := values[0].Source()
	if err != nil || sourceValue != (relationQueryPost{ID: 10, AuthorID: 1}) {
		t.Fatalf("Source() = (%#v, %v)", sourceValue, err)
	}
	ready, err := values[0].Related()
	if err != nil {
		t.Fatalf("Related() error = %v", err)
	}
	target, present, err := ready.Get(context.Background())
	if err != nil || !present || target != (relationQueryAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("ready Get() = (%#v, %v, %v)", target, present, err)
	}
	if backend.calls != 1 {
		t.Fatalf("All + ready Get backend calls = %d, want 1", backend.calls)
	}
}

type publicSelectRelatedBackend struct {
	calls int
}

func (backend *publicSelectRelatedBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.calls++
	return &publicSelectRelatedRows{}, nil
}

type publicSelectRelatedRows struct {
	position int
}

func (rows *publicSelectRelatedRows) Next() bool { return rows.position == 0 }

func (rows *publicSelectRelatedRows) Scan(destinations ...any) error {
	if len(destinations) != 5 {
		return fmt.Errorf("destination count = %d, want 5", len(destinations))
	}
	postID, ok0 := destinations[0].(*sql.NullInt64)
	authorID, ok1 := destinations[1].(*sql.NullInt64)
	reviewerID, ok2 := destinations[2].(*sql.NullInt64)
	targetID, ok3 := destinations[3].(*sql.NullInt64)
	targetName, ok4 := destinations[4].(*sql.NullString)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 {
		return fmt.Errorf("unexpected destination types")
	}
	rows.position++
	*postID = sql.NullInt64{Int64: 10, Valid: true}
	*authorID = sql.NullInt64{Int64: 1, Valid: true}
	*reviewerID = sql.NullInt64{}
	*targetID = sql.NullInt64{Int64: 1, Valid: true}
	*targetName = sql.NullString{String: "Ada", Valid: true}
	return nil
}

func (*publicSelectRelatedRows) Err() error   { return nil }
func (*publicSelectRelatedRows) Close() error { return nil }
