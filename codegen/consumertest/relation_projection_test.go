package codegen_test

import (
	"testing"

	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedRelationProjectionPresenceAndPrivatePrimaryKeyCompile(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-projection"
	directory := writeGeneratedRelationProjectionApps(t, modulePath, authors, blog, true)
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation projection apps did not compile or pass: %v\n%s", err, output)
	}
}

func writeGeneratedRelationProjectionApps(
	t *testing.T,
	modulePath string,
	authors, blog ir.Schema,
	includeTests bool,
) string {
	t.Helper()
	directory := newGeneratedModule(t, modulePath)
	writeGeneratedAppFixture(t, directory, "authors", "authors", authors, appFixtureFeatures{object: true, projection: true})
	writeGeneratedAppFixture(t, directory, "blog", "blog", blog, appFixtureFeatures{object: true, projection: true})
	if includeTests {
		writeGeneratedTestFile(t, directory, "authors/relation_projection_test.go", generatedAuthorsProjectionTest())
		writeGeneratedTestFile(t, directory, "blog/relation_projection_test.go", generatedBlogProjectionTest())
	}
	return directory
}

func generatedAuthorsProjectionTest() []byte {
	return []byte(`package authors

import (
	"database/sql"
	"testing"

	"github.com/progresshans/godj/orm"
)

func TestProjectionPresenceAndPrimaryKey(t *testing.T) {
	scan := (AuthorDescriptor{}).NewProjectionScan()
	destinations := scan.Destinations()
	if len(destinations) != 2 {
		t.Fatalf("destinations = %d, want 2", len(destinations))
	}
	*destinations[0].(*sql.NullInt64) = sql.NullInt64{Int64: 1, Valid: true}
	*destinations[1].(*sql.NullString) = sql.NullString{String: "Ada", Valid: true}
	value, key, presence := scan.Decode()
	if presence != orm.ProjectionPresent || value.ID != 1 || value.Name != "Ada" {
		t.Fatalf("present decode = %#v, %#v, %v", value, key, presence)
	}
	if integer, ok := key.Integer(); !ok || integer != 1 {
		t.Fatalf("key = %#v", key)
	}
	if primary, ok := (AuthorDescriptor{}).PrimaryKey(value); !ok || !primary.Equal(key) {
		t.Fatalf("private primary-key presence was not restored: %#v, %v", primary, ok)
	}

	absent := (AuthorDescriptor{}).NewProjectionScan()
	_, absentKey, presence := absent.Decode()
	if presence != orm.ProjectionAbsent || !absentKey.IsNull() {
		t.Fatalf("absent decode = %#v, %v", absentKey, presence)
	}

	partial := (AuthorDescriptor{}).NewProjectionScan()
	*partial.Destinations()[1].(*sql.NullString) = sql.NullString{String: "Ada", Valid: true}
	_, _, presence = partial.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("partial presence = %v, want invalid", presence)
	}

	var nilScan *authorProjectionScan
	if nilScan.Destinations() != nil {
		t.Fatal("nil scan returned destinations")
	}
	_, _, presence = nilScan.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("nil scan presence = %v, want invalid", presence)
	}
}
`)
}

func generatedBlogProjectionTest() []byte {
	return []byte(`package blog

import (
	"database/sql"
	"testing"

	"github.com/progresshans/godj/orm"
)

func TestProjectionNullableFieldAndPresence(t *testing.T) {
	scan := (PostDescriptor{}).NewProjectionScan()
	destinations := scan.Destinations()
	if len(destinations) != 4 {
		t.Fatalf("destinations = %d, want 4", len(destinations))
	}
	*destinations[0].(*sql.NullInt64) = sql.NullInt64{Int64: 10, Valid: true}
	*destinations[1].(*sql.NullString) = sql.NullString{String: "Alpha", Valid: true}
	*destinations[2].(*sql.NullInt64) = sql.NullInt64{Int64: 1, Valid: true}
	value, key, presence := scan.Decode()
	if presence != orm.ProjectionPresent || value.ID != 10 || value.Title != "Alpha" || value.AuthorID != 1 || value.ReviewerID != nil {
		t.Fatalf("present nullable decode = %#v, %#v, %v", value, key, presence)
	}
	*destinations[3].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
	value, _, presence = scan.Decode()
	if presence != orm.ProjectionPresent || value.ReviewerID == nil || *value.ReviewerID != 2 {
		t.Fatalf("present reviewer decode = %#v, %v", value, presence)
	}

	absent := (PostDescriptor{}).NewProjectionScan()
	_, absentKey, presence := absent.Decode()
	if presence != orm.ProjectionAbsent || !absentKey.IsNull() {
		t.Fatalf("absent decode = %#v, %v", absentKey, presence)
	}

	partial := (PostDescriptor{}).NewProjectionScan()
	*partial.Destinations()[3].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
	_, _, presence = partial.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("nullable-only partial presence = %v, want invalid", presence)
	}
}
`)
}
