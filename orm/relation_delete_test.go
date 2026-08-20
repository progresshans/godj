package orm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const relationDeleteTestFingerprint = "eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58"

var (
	errRelationDeleteQuery     = errors.New("relation delete query failure")
	errRelationDeleteScan      = errors.New("relation delete scan failure")
	errRelationDeleteRows      = errors.New("relation delete rows failure")
	errRelationDeleteClose     = errors.New("relation delete close failure")
	errRelationDeleteSetNull   = errors.New("relation delete set-null failure")
	errRelationDeleteTarget    = errors.New("relation delete target failure")
	errRelationDeleteAtomic    = errors.New("relation delete atomic failure")
	errRelationDeleteSecondary = errors.New("relation delete secondary failure")
)

type relationDeleteTestAuthor struct {
	ID                int64
	Name              string
	primaryKeyPresent bool
}

type relationDeleteTestAuthorDescriptor struct{}

func (relationDeleteTestAuthorDescriptor) Metadata() ir.Model {
	return relationDeleteTestAuthorModel()
}

func (relationDeleteTestAuthorDescriptor) Scan(db.Row) (relationDeleteTestAuthor, error) {
	return relationDeleteTestAuthor{}, errors.New("author descriptor scan is outside relation delete tests")
}

func (relationDeleteTestAuthorDescriptor) CloneModel(value relationDeleteTestAuthor) relationDeleteTestAuthor {
	return value
}

func (relationDeleteTestAuthorDescriptor) PrimaryKey(value relationDeleteTestAuthor) (query.Value, bool) {
	return query.Integer(value.ID), value.primaryKeyPresent
}

func (relationDeleteTestAuthorDescriptor) SetPrimaryKey(value *relationDeleteTestAuthor, key int64) {
	value.ID = key
	value.primaryKeyPresent = true
}

func (relationDeleteTestAuthorDescriptor) ClearPrimaryKey(value *relationDeleteTestAuthor) {
	value.ID = 0
	value.primaryKeyPresent = false
}

func (relationDeleteTestAuthorDescriptor) CloneWriteModel(value relationDeleteTestAuthor) relationDeleteTestAuthor {
	return value
}

func (relationDeleteTestAuthorDescriptor) WriteFieldValue(
	value relationDeleteTestAuthor,
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

type relationDeleteWrongMetadataDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteWrongMetadataDescriptor) Metadata() ir.Model {
	model := relationDeleteTestAuthorModel()
	model.DBTable = "wrong_author"
	return model
}

type relationDeleteOutgoingTargetDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteOutgoingTargetDescriptor) Metadata() ir.Model {
	model := relationDeleteTestAuthorModel()
	model.Fields = append(model.Fields, ir.Field{
		Name: "organization", GoName: "OrganizationID", Column: "organization_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "organizations", ModelName: "organization"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "authors"},
			OnDelete:    ir.DeleteProtect,
		},
	})
	return model
}

type relationDeleteStatefulDescriptor struct {
	relationDeleteTestAuthorDescriptor
	state byte
}

type relationDeleteMissingKeyDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteMissingKeyDescriptor) PrimaryKey(value relationDeleteTestAuthor) (query.Value, bool) {
	return query.Integer(value.ID), false
}

type relationDeleteNonIntegerKeyDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteNonIntegerKeyDescriptor) PrimaryKey(relationDeleteTestAuthor) (query.Value, bool) {
	return query.String("1"), true
}

type relationDeleteNullKeyDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteNullKeyDescriptor) PrimaryKey(relationDeleteTestAuthor) (query.Value, bool) {
	return query.Null(), true
}

type relationDeleteCloneMissingKeyDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteCloneMissingKeyDescriptor) CloneWriteModel(value relationDeleteTestAuthor) relationDeleteTestAuthor {
	value.primaryKeyPresent = false
	return value
}

type relationDeleteCloneKeyTypeDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteCloneKeyTypeDescriptor) CloneWriteModel(value relationDeleteTestAuthor) relationDeleteTestAuthor {
	value.Name = "clone-key-type"
	return value
}

func (relationDeleteCloneKeyTypeDescriptor) PrimaryKey(value relationDeleteTestAuthor) (query.Value, bool) {
	if value.Name == "clone-key-type" {
		return query.String("1"), value.primaryKeyPresent
	}
	return query.Integer(value.ID), value.primaryKeyPresent
}

type relationDeleteCloneKeyDriftDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteCloneKeyDriftDescriptor) CloneWriteModel(value relationDeleteTestAuthor) relationDeleteTestAuthor {
	value.ID++
	return value
}

type relationDeleteClearNoopDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteClearNoopDescriptor) ClearPrimaryKey(*relationDeleteTestAuthor) {}

type relationDeleteClearResidueDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteClearResidueDescriptor) ClearPrimaryKey(value *relationDeleteTestAuthor) {
	value.primaryKeyPresent = false
}

type relationDeleteClearMutationDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteClearMutationDescriptor) ClearPrimaryKey(value *relationDeleteTestAuthor) {
	value.ID = 0
	value.Name = "mutated"
	value.primaryKeyPresent = false
}

type relationDeleteUnreadableFieldDescriptor struct {
	relationDeleteTestAuthorDescriptor
}

func (relationDeleteUnreadableFieldDescriptor) WriteFieldValue(
	value relationDeleteTestAuthor,
	field ir.Field,
) (query.Value, bool) {
	if field.Name == "name" {
		return query.Value{}, false
	}
	return relationDeleteTestAuthorDescriptor{}.WriteFieldValue(value, field)
}

func relationDeleteTestAuthorModel() ir.Model {
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

func relationDeleteTestSchemas() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models:        []ir.Model{relationDeleteTestAuthorModel()},
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

func relationDeleteTestBinding(t *testing.T) ProjectBinding {
	t.Helper()
	authors, blog := relationDeleteTestSchemas()
	binding, err := BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	return binding
}

func relationDeleteTestActualFingerprint(t *testing.T, binding ProjectBinding) string {
	t.Helper()
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	model, ok := binding.Model(target)
	if !ok {
		t.Fatal("target model missing")
	}
	primaryKey, ok := relationDeleteTargetKey(model)
	if !ok {
		t.Fatal("target primary key invalid")
	}
	edges, err := relationDeleteIncomingEdges(binding.snapshot, target)
	if err != nil {
		t.Fatalf("relationDeleteIncomingEdges() error = %v", err)
	}
	return relationDeletePolicyFingerprint(target, model, primaryKey, edges)
}

func relationDeleteTestDeleterWithDescriptor[D WriteDescriptor[relationDeleteTestAuthor]](
	t *testing.T,
	descriptor D,
) RelationDeleter[relationDeleteTestAuthor] {
	t.Helper()
	deleter, err := BindRelationDeleter[relationDeleteTestAuthor](
		relationDeleteTestBinding(t),
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		descriptor,
		relationDeleteTestFingerprint,
	)
	if err != nil {
		t.Fatalf("BindRelationDeleter() error = %v", err)
	}
	return deleter
}

type relationDeleteTestRows struct {
	values     [][2]any
	index      int
	nextCalls  int
	scanCalls  int
	errCalls   int
	closeCalls int
	scanErr    error
	rowsErr    error
	closeErr   error
	onNext     func(int)
}

func (rows *relationDeleteTestRows) Next() bool {
	rows.nextCalls++
	if rows.onNext != nil {
		rows.onNext(rows.nextCalls)
	}
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *relationDeleteTestRows) Scan(destinations ...any) error {
	rows.scanCalls++
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 2 || rows.index == 0 || rows.index > len(rows.values) {
		return fmt.Errorf("unexpected relation delete scan shape")
	}
	left, leftOK := destinations[0].(*any)
	right, rightOK := destinations[1].(*any)
	if !leftOK || !rightOK {
		return fmt.Errorf("relation delete scan destinations are not *any")
	}
	*left = rows.values[rows.index-1][0]
	*right = rows.values[rows.index-1][1]
	return nil
}

func (rows *relationDeleteTestRows) Err() error {
	rows.errCalls++
	return rows.rowsErr
}

func (rows *relationDeleteTestRows) Close() error {
	rows.closeCalls++
	return rows.closeErr
}

type relationDeleteTypedNilRows struct{}

func (*relationDeleteTypedNilRows) Next() bool { panic("typed nil rows Next must not be called") }
func (*relationDeleteTypedNilRows) Scan(...any) error {
	panic("typed nil rows Scan must not be called")
}
func (*relationDeleteTypedNilRows) Err() error   { panic("typed nil rows Err must not be called") }
func (*relationDeleteTypedNilRows) Close() error { panic("typed nil rows Close must not be called") }

type relationDeleteTestSession struct {
	mu               sync.Mutex
	queryRows        []db.Rows
	queryErrs        []error
	queryPlans       []query.Plan
	setNullCounts    []int64
	setNullErrs      []error
	setNullPlans     []query.RelationSetNullPlan
	deleteCount      int64
	deleteErr        error
	deletePlans      []query.DeletePlan
	mutationOrder    []string
	onSetNull        func()
	forbiddenMutator int
}

func (session *relationDeleteTestSession) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	index := len(session.queryPlans)
	session.queryPlans = append(session.queryPlans, plan)
	var rows db.Rows
	if index < len(session.queryRows) {
		rows = session.queryRows[index]
	}
	var err error
	if index < len(session.queryErrs) {
		err = session.queryErrs[index]
	}
	return rows, err
}

func (session *relationDeleteTestSession) Insert(context.Context, query.InsertPlan) (int64, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.forbiddenMutator++
	return 0, errors.New("unexpected relation delete Insert")
}

func (session *relationDeleteTestSession) Update(context.Context, query.UpdatePlan) (int64, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.forbiddenMutator++
	return 0, errors.New("unexpected relation delete Update")
}

func (session *relationDeleteTestSession) RelationSetNull(
	_ context.Context,
	plan query.RelationSetNullPlan,
) (int64, error) {
	session.mu.Lock()
	index := len(session.setNullPlans)
	session.setNullPlans = append(session.setNullPlans, plan)
	session.mutationOrder = append(session.mutationOrder, "UPDATE")
	hook := session.onSetNull
	var count int64
	if index < len(session.setNullCounts) {
		count = session.setNullCounts[index]
	}
	var err error
	if index < len(session.setNullErrs) {
		err = session.setNullErrs[index]
	}
	session.mu.Unlock()
	if hook != nil {
		hook()
	}
	return count, err
}

func (session *relationDeleteTestSession) Delete(_ context.Context, plan query.DeletePlan) (int64, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.deletePlans = append(session.deletePlans, plan)
	session.mutationOrder = append(session.mutationOrder, "DELETE")
	return session.deleteCount, session.deleteErr
}

type relationDeleteTestBackend struct {
	mu     sync.Mutex
	calls  int
	invoke func(context.Context, func(db.RelationSession) error) error
}

func (backend *relationDeleteTestBackend) AtomicRelation(
	ctx context.Context,
	callback func(db.RelationSession) error,
) error {
	backend.mu.Lock()
	backend.calls++
	invoke := backend.invoke
	backend.mu.Unlock()
	if invoke == nil {
		return errors.New("relation delete test backend has no callback strategy")
	}
	return invoke(ctx, callback)
}

func relationDeleteConformingBackend(session db.RelationSession) *relationDeleteTestBackend {
	return &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
		return callback(session)
	}}
}

func relationDeleteTestAuthorValue(id int64) relationDeleteTestAuthor {
	return relationDeleteTestAuthor{ID: id, Name: "Ada", primaryKeyPresent: true}
}

func TestRelationDeletePolicyFingerprintGoldenCanonicalAndLengthDelimited(t *testing.T) {
	t.Parallel()

	binding := relationDeleteTestBinding(t)
	actual := relationDeleteTestActualFingerprint(t, binding)
	if actual != relationDeleteTestFingerprint {
		t.Fatalf("relation delete fingerprint = %q, want %q", actual, relationDeleteTestFingerprint)
	}

	authors, blog := relationDeleteTestSchemas()
	blog.Models[0].Fields[2], blog.Models[0].Fields[3] = blog.Models[0].Fields[3], blog.Models[0].Fields[2]
	reordered, err := BindProject(blog, authors)
	if err != nil {
		t.Fatalf("BindProject(reordered) error = %v", err)
	}
	if got := relationDeleteTestActualFingerprint(t, reordered); got != actual {
		t.Fatalf("reordered fingerprint = %q, want %q", got, actual)
	}

	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	model, _ := binding.Model(target)
	primaryKey, _ := relationDeleteTargetKey(model)
	edges, edgeErr := relationDeleteIncomingEdges(binding.snapshot, target)
	if edgeErr != nil {
		t.Fatal(edgeErr)
	}
	changed := append([]relationDeleteEdge(nil), edges...)
	changed[0] = cloneRelationDeleteEdge(changed[0])
	changed[0].metadata.OnDelete = ir.DeleteSetNull
	if relationDeletePolicyFingerprint(target, model, primaryKey, changed) == actual {
		t.Fatal("policy change did not change relation delete fingerprint")
	}

	left := sha256.New()
	writeRelationDeleteFingerprintValue(left, "ab")
	writeRelationDeleteFingerprintValue(left, "c")
	right := sha256.New()
	writeRelationDeleteFingerprintValue(right, "a")
	writeRelationDeleteFingerprintValue(right, "bc")
	if reflect.DeepEqual(left.Sum(nil), right.Sum(nil)) {
		t.Fatal("length-delimited fingerprint values collided")
	}
}

func TestBindRelationDeleterValidatesDescriptorBindingAndFingerprint(t *testing.T) {
	t.Parallel()

	binding := relationDeleteTestBinding(t)
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	if _, err := BindRelationDeleter[relationDeleteTestAuthor](
		binding,
		target,
		relationDeleteTestAuthorDescriptor{},
		relationDeleteTestFingerprint,
	); err != nil {
		t.Fatalf("BindRelationDeleter(valid) error = %v", err)
	}
	authorsV3, blogV3 := relationDeleteTestSchemas()
	authorsV3.FormatVersion = ir.CurrentFormatVersion
	v3Binding, err := BindProject(authorsV3, blogV3)
	if err != nil {
		t.Fatalf("BindProject(v3 scalar target) error = %v", err)
	}
	if got := relationDeleteTestActualFingerprint(t, v3Binding); got != relationDeleteTestFingerprint {
		t.Fatalf("direct v3 scalar target fingerprint = %q", got)
	}
	if _, err := BindRelationDeleter[relationDeleteTestAuthor](
		v3Binding,
		target,
		relationDeleteTestAuthorDescriptor{},
		relationDeleteTestFingerprint,
	); err != nil {
		t.Fatalf("direct BindRelationDeleter(v3 scalar target) error = %v", err)
	}

	var typedNil *relationDeleteTestAuthorDescriptor
	tests := []struct {
		name        string
		binding     ProjectBinding
		identity    ir.ModelIdentity
		descriptor  WriteDescriptor[relationDeleteTestAuthor]
		fingerprint string
	}{
		{name: "zero binding", identity: target, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: relationDeleteTestFingerprint},
		{name: "wrong identity", binding: binding, identity: ir.ModelIdentity{AppLabel: "authors", ModelName: "missing"}, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: relationDeleteTestFingerprint},
		{name: "typed nil descriptor", binding: binding, identity: target, descriptor: typedNil, fingerprint: relationDeleteTestFingerprint},
		{name: "pointer descriptor", binding: binding, identity: target, descriptor: &relationDeleteTestAuthorDescriptor{}, fingerprint: relationDeleteTestFingerprint},
		{name: "stateful descriptor", binding: binding, identity: target, descriptor: relationDeleteStatefulDescriptor{}, fingerprint: relationDeleteTestFingerprint},
		{name: "wrong metadata", binding: binding, identity: target, descriptor: relationDeleteWrongMetadataDescriptor{}, fingerprint: relationDeleteTestFingerprint},
		{name: "empty fingerprint", binding: binding, identity: target, descriptor: relationDeleteTestAuthorDescriptor{}},
		{name: "uppercase fingerprint", binding: binding, identity: target, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{name: "short fingerprint", binding: binding, identity: target, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: strings.Repeat("0", 63)},
		{name: "nonhex fingerprint", binding: binding, identity: target, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"},
		{name: "stale fingerprint", binding: binding, identity: target, descriptor: relationDeleteTestAuthorDescriptor{}, fingerprint: "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deleter, err := BindRelationDeleter[relationDeleteTestAuthor](
				test.binding,
				test.identity,
				test.descriptor,
				test.fingerprint,
			)
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("BindRelationDeleter() error = %v, want query_error/invalid_plan", err)
			}
			if deleter.state.valid {
				t.Fatal("failed binder published a valid deleter")
			}
		})
	}

	authors, _ := relationDeleteTestSchemas()
	zeroIncoming, err := BindProject(authors)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindRelationDeleter[relationDeleteTestAuthor](
		zeroIncoming,
		target,
		relationDeleteTestAuthorDescriptor{},
		"0000000000000000000000000000000000000000000000000000000000000000",
	); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero-incoming BindRelationDeleter() error = %v", err)
	}

	authorsOutgoing, blogOutgoing := relationDeleteTestSchemas()
	authorsOutgoing.FormatVersion = ir.CurrentFormatVersion
	authorsOutgoing.Models[0] = relationDeleteOutgoingTargetDescriptor{}.Metadata()
	organizations := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "organizations",
		Models: []ir.Model{{
			Name: "organization", GoName: "Organization", DBTable: "organizations_organization",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	outgoingBinding, err := BindProject(authorsOutgoing, blogOutgoing, organizations)
	if err != nil {
		t.Fatalf("BindProject(outgoing target) error = %v", err)
	}
	if _, err := BindRelationDeleter[relationDeleteTestAuthor](
		outgoingBinding,
		target,
		relationDeleteOutgoingTargetDescriptor{},
		"0000000000000000000000000000000000000000000000000000000000000000",
	); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("outgoing-target BindRelationDeleter() error = %v", err)
	}
}

func TestRelationDeleterSetNullThenDeleteSuccess(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	protectRows := &relationDeleteTestRows{}
	session := &relationDeleteTestSession{
		queryRows:     []db.Rows{protectRows},
		setNullCounts: []int64{2},
		deleteCount:   1,
	}
	backend := relationDeleteConformingBackend(session)
	target := relationDeleteTestAuthorValue(2)
	rows, err := deleter.Delete(context.Background(), backend, &target)
	if err != nil || rows != 1 {
		t.Fatalf("Delete() = (%d, %v)", rows, err)
	}
	if target.ID != 0 || target.primaryKeyPresent || target.Name != "Ada" {
		t.Fatalf("committed target = %#v", target)
	}
	if backend.calls != 1 || protectRows.closeCalls != 1 || protectRows.errCalls != 1 || protectRows.scanCalls != 0 {
		t.Fatalf("transaction/rows metrics = backend %d close %d err %d scan %d", backend.calls, protectRows.closeCalls, protectRows.errCalls, protectRows.scanCalls)
	}
	if got, want := session.mutationOrder, []string{"UPDATE", "DELETE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mutation order = %#v, want %#v", got, want)
	}
	assertRelationDeletePlans(t, session, 2)
}

func TestRelationDeleterProtectsAllDistinctSourceRowsWithoutMutation(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	protectRows := &relationDeleteTestRows{values: [][2]any{
		{int64(10), int64(1)},
		{int64(11), int64(1)},
		{int64(10), int64(1)},
	}}
	session := &relationDeleteTestSession{queryRows: []db.Rows{protectRows}, deleteCount: 1}
	backend := relationDeleteConformingBackend(session)
	target := relationDeleteTestAuthorValue(1)
	before := target
	rows, err := deleter.Delete(context.Background(), backend, &target)
	if rows != 0 {
		t.Fatalf("Delete(PROTECT) rows = %d", rows)
	}
	var protected *query.ProtectedForeignKeyError
	if !errors.As(err, &protected) || protected.ProtectedSourceRows() != 2 ||
		!errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("Delete(PROTECT) error = %T %v", err, err)
	}
	if target != before || len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 || len(session.mutationOrder) != 0 {
		t.Fatalf("protected delete mutated state: target=%#v session=%#v", target, session)
	}
	if protectRows.nextCalls != 4 || protectRows.scanCalls != 3 || protectRows.errCalls != 1 || protectRows.closeCalls != 1 {
		t.Fatalf("protected rows metrics = next %d scan %d err %d close %d", protectRows.nextCalls, protectRows.scanCalls, protectRows.errCalls, protectRows.closeCalls)
	}
}

func TestRelationDeleterQueriesEveryProtectEdgeAndDistinctsBySourceIdentityAndKey(t *testing.T) {
	t.Parallel()

	authors, blog := relationDeleteTestSchemas()
	blog.Models[0].Fields = append(blog.Models[0].Fields, ir.Field{
		Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "edited_posts"},
			OnDelete:    ir.DeleteProtect,
		},
	})
	archive := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "archive",
		Models: []ir.Model{{
			Name: "entry", GoName: "Entry", DBTable: "archive_entry",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "archive_entries"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
	binding, err := BindProject(blog, authors, archive)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := relationDeleteTestActualFingerprint(t, binding)
	deleter, err := BindRelationDeleter[relationDeleteTestAuthor](
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		relationDeleteTestAuthorDescriptor{},
		fingerprint,
	)
	if err != nil {
		t.Fatal(err)
	}
	rowSets := []*relationDeleteTestRows{
		{values: [][2]any{{int64(10), int64(1)}}},
		{values: [][2]any{{int64(10), int64(1)}, {int64(11), int64(1)}}},
		{values: [][2]any{{int64(10), int64(1)}}},
	}
	session := &relationDeleteTestSession{queryRows: []db.Rows{rowSets[0], rowSets[1], rowSets[2]}, deleteCount: 1}
	target := relationDeleteTestAuthorValue(1)
	before := target
	rows, deleteErr := deleter.Delete(context.Background(), relationDeleteConformingBackend(session), &target)
	var protected *query.ProtectedForeignKeyError
	if rows != 0 || !errors.As(deleteErr, &protected) || protected.ProtectedSourceRows() != 3 || target != before {
		t.Fatalf("Delete(global distinct) = (%d, %v), protected %#v target %#v", rows, deleteErr, protected, target)
	}
	if len(session.queryPlans) != 3 || len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 {
		t.Fatalf("global distinct calls = query %d set-null %d delete %d", len(session.queryPlans), len(session.setNullPlans), len(session.deletePlans))
	}
	if got := []string{session.queryPlans[0].Table(), session.queryPlans[1].Columns()[1].Name(), session.queryPlans[2].Columns()[1].Name()}; !reflect.DeepEqual(got, []string{"archive_entry", "author", "editor"}) {
		t.Fatalf("canonical PROTECT order = %#v", got)
	}
	for index, rows := range rowSets {
		if rows.closeCalls != 1 || rows.errCalls != 1 {
			t.Fatalf("PROTECT rows[%d] lifecycle = close %d err %d", index, rows.closeCalls, rows.errCalls)
		}
	}
}

func TestBindRelationDeleterRejectsCorruptStructuralBindingBeforeFingerprint(t *testing.T) {
	t.Parallel()

	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	tests := []struct {
		name    string
		corrupt func(ProjectBinding)
	}{
		{name: "unsupported policy", corrupt: func(binding ProjectBinding) {
			binding.snapshot.forward[0].OnDelete = ir.DeletePolicy("cascade")
		}},
		{name: "set-null without nullable", corrupt: func(binding ProjectBinding) {
			binding.snapshot.forward[1].Nullable = false
		}},
		{name: "source missing AutoField", corrupt: func(binding ProjectBinding) {
			source := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
			model := binding.snapshot.models[source]
			model.Fields[0].Kind = ir.FieldChar
			binding.snapshot.models[source] = model
		}},
		{name: "source field metadata mismatch", corrupt: func(binding ProjectBinding) {
			binding.snapshot.forward[0].Column = "wrong_author_id"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := relationDeleteTestBinding(t)
			test.corrupt(binding)
			deleter, err := BindRelationDeleter[relationDeleteTestAuthor](
				binding,
				target,
				relationDeleteTestAuthorDescriptor{},
				"0000000000000000000000000000000000000000000000000000000000000000",
			)
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || deleter.state.valid {
				t.Fatalf("BindRelationDeleter(corrupt) = (%#v, %v)", deleter, err)
			}
		})
	}
}

func TestRelationDeleterPreflightRejectsDescriptorDriftBeforeAtomicIO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		descriptor WriteDescriptor[relationDeleteTestAuthor]
		target     relationDeleteTestAuthor
	}{
		{name: "missing key", descriptor: relationDeleteMissingKeyDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "noninteger key", descriptor: relationDeleteNonIntegerKeyDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "NULL key", descriptor: relationDeleteNullKeyDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clone missing key", descriptor: relationDeleteCloneMissingKeyDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clone key type drift", descriptor: relationDeleteCloneKeyTypeDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clone key drift", descriptor: relationDeleteCloneKeyDriftDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clear no-op", descriptor: relationDeleteClearNoopDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clear key residue", descriptor: relationDeleteClearResidueDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "clear non-PK mutation", descriptor: relationDeleteClearMutationDescriptor{}, target: relationDeleteTestAuthorValue(1)},
		{name: "unreadable non-PK", descriptor: relationDeleteUnreadableFieldDescriptor{}, target: relationDeleteTestAuthorValue(1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, test.descriptor)
			backend := &relationDeleteTestBackend{invoke: func(context.Context, func(db.RelationSession) error) error {
				t.Fatal("AtomicRelation called after failed descriptor preflight")
				return nil
			}}
			before := test.target
			rows, err := deleter.Delete(context.Background(), backend, &test.target)
			if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("Delete() = (%d, %v), want (0, query invalid_plan)", rows, err)
			}
			if test.target != before || backend.calls != 0 {
				t.Fatalf("cold preflight changed state: target=%#v backend calls=%d", test.target, backend.calls)
			}
		})
	}
}

func TestRelationDeleterRejectsInvalidArgumentsBeforeAtomicIO(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	backend := &relationDeleteTestBackend{invoke: func(context.Context, func(db.RelationSession) error) error {
		t.Fatal("AtomicRelation called for invalid argument")
		return nil
	}}
	target := relationDeleteTestAuthorValue(1)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var typedNilBackend *relationDeleteTestBackend
	tests := []struct {
		name    string
		ctx     context.Context
		backend db.RelationAtomic
		target  *relationDeleteTestAuthor
		want    error
	}{
		{name: "nil context", backend: backend, target: &target, want: &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}},
		{name: "canceled context", ctx: canceled, backend: backend, target: &target, want: context.Canceled},
		{name: "nil backend", ctx: context.Background(), target: &target, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "typed nil backend", ctx: context.Background(), backend: typedNilBackend, target: &target, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "nil target", ctx: context.Background(), backend: backend, want: &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}},
		{name: "zero deleter", ctx: context.Background(), backend: backend, target: &target, want: &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := deleter
			if test.name == "zero deleter" {
				current = RelationDeleter[relationDeleteTestAuthor]{}
			}
			rows, err := current.Delete(test.ctx, test.backend, test.target)
			if rows != 0 || !errors.Is(err, test.want) {
				t.Fatalf("Delete() = (%d, %v), want error %v", rows, err, test.want)
			}
		})
	}
	if backend.calls != 0 {
		t.Fatalf("invalid arguments reached backend %d times", backend.calls)
	}
}

func TestRelationDeleterRowsFailuresCloseExactlyOnceAndNeverMutate(t *testing.T) {
	t.Parallel()

	var typedNil *relationDeleteTypedNilRows
	tests := []struct {
		name       string
		rows       db.Rows
		queryErr   error
		want       error
		closeCalls int
	}{
		{name: "nil rows nil error", want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "typed nil rows nil error", rows: typedNil, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "nil rows primary error", queryErr: errRelationDeleteQuery, want: errRelationDeleteQuery},
		{name: "typed nil rows primary error", rows: typedNil, queryErr: errRelationDeleteQuery, want: errRelationDeleteQuery},
		{name: "genuine rows primary error", rows: &relationDeleteTestRows{}, queryErr: errRelationDeleteQuery, want: errRelationDeleteQuery, closeCalls: 1},
		{name: "genuine rows primary plus close error", rows: &relationDeleteTestRows{closeErr: errRelationDeleteClose}, queryErr: errRelationDeleteQuery, want: errRelationDeleteClose, closeCalls: 1},
		{name: "scan failure", rows: &relationDeleteTestRows{values: [][2]any{{int64(1), int64(1)}}, scanErr: errRelationDeleteScan}, want: errRelationDeleteScan, closeCalls: 1},
		{name: "rows error", rows: &relationDeleteTestRows{rowsErr: errRelationDeleteRows}, want: errRelationDeleteRows, closeCalls: 1},
		{name: "close error", rows: &relationDeleteTestRows{closeErr: errRelationDeleteClose}, want: errRelationDeleteClose, closeCalls: 1},
		{name: "NULL primary key", rows: &relationDeleteTestRows{values: [][2]any{{nil, int64(1)}}}, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}, closeCalls: 1},
		{name: "NULL foreign key", rows: &relationDeleteTestRows{values: [][2]any{{int64(1), nil}}}, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}, closeCalls: 1},
		{name: "foreign key mismatch", rows: &relationDeleteTestRows{values: [][2]any{{int64(1), int64(2)}}}, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}, closeCalls: 1},
		{name: "noninteger values", rows: &relationDeleteTestRows{values: [][2]any{{"1", "1"}}}, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}, closeCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
			session := &relationDeleteTestSession{queryRows: []db.Rows{test.rows}, queryErrs: []error{test.queryErr}, deleteCount: 1}
			backend := relationDeleteConformingBackend(session)
			target := relationDeleteTestAuthorValue(1)
			before := target
			rows, err := deleter.Delete(context.Background(), backend, &target)
			if rows != 0 || !errors.Is(err, test.want) {
				t.Fatalf("Delete() = (%d, %v), want error %v", rows, err, test.want)
			}
			if test.queryErr != nil && !errors.Is(err, test.queryErr) {
				t.Fatalf("Delete() error = %v, lost primary query error %v", err, test.queryErr)
			}
			if target != before || len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 {
				t.Fatalf("rows failure mutated state: target=%#v session=%#v", target, session)
			}
			if genuine, ok := test.rows.(*relationDeleteTestRows); ok && genuine.closeCalls != test.closeCalls {
				t.Fatalf("Close calls = %d, want %d", genuine.closeCalls, test.closeCalls)
			}
		})
	}
}

func TestRelationDeleterMutationFailuresReturnZeroAndPreserveCaller(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setNullCount  int64
		setNullErr    error
		deleteCount   int64
		deleteErr     error
		want          error
		wantDeleteRun bool
	}{
		{name: "zero set-null permitted", setNullCount: 0, deleteCount: 1, wantDeleteRun: true},
		{name: "negative set-null rejected", setNullCount: -1, deleteCount: 1, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "minimum set-null rejected", setNullCount: math.MinInt64, deleteCount: 1, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}},
		{name: "set-null error", setNullCount: 2, setNullErr: errRelationDeleteSetNull, deleteCount: 1, want: errRelationDeleteSetNull},
		{name: "target zero rows", setNullCount: 2, deleteCount: 0, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}, wantDeleteRun: true},
		{name: "target two rows", setNullCount: 2, deleteCount: 2, want: &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}, wantDeleteRun: true},
		{name: "target error", setNullCount: 2, deleteCount: 1, deleteErr: errRelationDeleteTarget, want: errRelationDeleteTarget, wantDeleteRun: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
			session := &relationDeleteTestSession{
				queryRows:     []db.Rows{&relationDeleteTestRows{}},
				setNullCounts: []int64{test.setNullCount},
				setNullErrs:   []error{test.setNullErr},
				deleteCount:   test.deleteCount,
				deleteErr:     test.deleteErr,
			}
			backend := relationDeleteConformingBackend(session)
			target := relationDeleteTestAuthorValue(2)
			before := target
			rows, err := deleter.Delete(context.Background(), backend, &target)
			if test.want == nil {
				if err != nil || rows != 1 || target.ID != 0 || target.primaryKeyPresent {
					t.Fatalf("Delete() = (%d, %v), target %#v", rows, err, target)
				}
			} else {
				if rows != 0 || !errors.Is(err, test.want) || target != before {
					t.Fatalf("Delete() = (%d, %v), target %#v, want error %v unchanged %#v", rows, err, target, test.want, before)
				}
			}
			if got := len(session.deletePlans) != 0; got != test.wantDeleteRun {
				t.Fatalf("target Delete ran = %v, want %v", got, test.wantDeleteRun)
			}
		})
	}
}

func TestRelationDeleterCancellationInsideCallbackPreventsLaterMutation(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	ctx, cancel := context.WithCancel(context.Background())
	session := &relationDeleteTestSession{
		queryRows:     []db.Rows{&relationDeleteTestRows{}},
		setNullCounts: []int64{2},
		deleteCount:   1,
		onSetNull:     cancel,
	}
	backend := relationDeleteConformingBackend(session)
	target := relationDeleteTestAuthorValue(2)
	before := target
	rows, err := deleter.Delete(ctx, backend, &target)
	if rows != 0 || !errors.Is(err, context.Canceled) || target != before {
		t.Fatalf("Delete(canceled in callback) = (%d, %v), target %#v", rows, err, target)
	}
	if len(session.setNullPlans) != 1 || len(session.deletePlans) != 0 {
		t.Fatalf("canceled mutation calls = set-null %d delete %d", len(session.setNullPlans), len(session.deletePlans))
	}
}

func TestRelationDeleterCancellationWhileDrainingRowsClosesAndDoesNotMutate(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	ctx, cancel := context.WithCancel(context.Background())
	protectRows := &relationDeleteTestRows{values: [][2]any{{int64(10), int64(1)}}}
	protectRows.onNext = func(call int) {
		if call == 1 {
			cancel()
		}
	}
	session := &relationDeleteTestSession{queryRows: []db.Rows{protectRows}, deleteCount: 1}
	target := relationDeleteTestAuthorValue(1)
	before := target
	rows, err := deleter.Delete(ctx, relationDeleteConformingBackend(session), &target)
	if rows != 0 || !errors.Is(err, context.Canceled) || target != before {
		t.Fatalf("Delete(canceled rows) = (%d, %v), target %#v", rows, err, target)
	}
	if protectRows.scanCalls != 0 || protectRows.errCalls != 1 || protectRows.closeCalls != 1 ||
		len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 {
		t.Fatalf("canceled rows lifecycle = scan %d err %d close %d set-null %d delete %d", protectRows.scanCalls, protectRows.errCalls, protectRows.closeCalls, len(session.setNullPlans), len(session.deletePlans))
	}
}

func TestRelationDeleterPreservesOutcomeUnknownMarkersAndCallerState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		code            string
		callbackFailure bool
	}{
		{name: "commit", code: query.CodeCommitOutcomeUnknown},
		{name: "transaction", code: query.CodeTransactionOutcomeUnknown, callbackFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
			session := &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}}, setNullCounts: []int64{2}, deleteCount: 1}
			if test.callbackFailure {
				session.setNullErrs = []error{errRelationDeleteSetNull}
			}
			var marker *query.Error
			backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
				callbackErr := callback(session)
				if test.callbackFailure {
					marker = &query.Error{
						Category: query.CategoryBackend,
						Code:     test.code,
						Cause:    errors.Join(callbackErr, errRelationDeleteAtomic),
					}
					return marker
				}
				if callbackErr != nil {
					return callbackErr
				}
				marker = &query.Error{Category: query.CategoryBackend, Code: test.code, Cause: errRelationDeleteAtomic}
				return marker
			}}
			target := relationDeleteTestAuthorValue(2)
			before := target
			rows, err := deleter.Delete(context.Background(), backend, &target)
			var classified *query.Error
			if rows != 0 || !errors.As(err, &classified) || classified != marker || !errors.Is(err, errRelationDeleteAtomic) || target != before {
				t.Fatalf("Delete(%s unknown) = (%d, %v), classified %#v target %#v", test.name, rows, err, classified, target)
			}
			if test.callbackFailure && !errors.Is(err, errRelationDeleteSetNull) {
				t.Fatalf("Delete(%s unknown) lost callback failure: %v", test.name, err)
			}
		})
	}
}

func TestRelationDeleterSuccessfulAtomicReturnRemainsAuthoritativeAfterContextTransition(t *testing.T) {
	t.Parallel()

	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	ctx, cancel := context.WithCancel(context.Background())
	session := &relationDeleteTestSession{
		queryRows:     []db.Rows{&relationDeleteTestRows{}},
		setNullCounts: []int64{2},
		deleteCount:   1,
	}
	backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
		if err := callback(session); err != nil {
			return err
		}
		cancel()
		return nil
	}}
	target := relationDeleteTestAuthorValue(2)
	rows, err := deleter.Delete(ctx, backend, &target)
	if rows != 1 || err != nil || target.ID != 0 || target.primaryKeyPresent || target.Name != "Ada" {
		t.Fatalf("Delete(post-callback context transition) = (%d, %v), target %#v", rows, err, target)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context state after AtomicRelation = %v, want canceled", ctx.Err())
	}
}

func TestRelationDeleteCallbackGuardRejectsFirstPostSealEntry(t *testing.T) {
	t.Parallel()

	guard := &relationDeleteCallbackGuard{}
	snapshot := guard.seal()
	mutations := 0
	err := guard.invoke(func() error {
		mutations++
		return nil
	})
	if snapshot.entries != 0 || snapshot.completed != 0 || snapshot.result != nil {
		t.Fatalf("initial sealed snapshot = %#v", snapshot)
	}
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || mutations != 0 {
		t.Fatalf("first post-seal entry = (%v, mutations %d), want invalid_plan and zero mutation", err, mutations)
	}
}

func TestRelationDeleterFirstCallbackRacingBackendReturnAndSealIsRaceSafe(t *testing.T) {
	const attempts = 100
	for attempt := range attempts {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		session := &relationDeleteTestSession{
			queryRows:     []db.Rows{&relationDeleteTestRows{}},
			setNullCounts: []int64{2},
			deleteCount:   1,
		}
		callbackResult := make(chan error, 1)
		release := make(chan struct{})
		backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
			go func() {
				<-release
				callbackResult <- callback(session)
			}()
			close(release)
			if attempt%2 == 0 {
				runtime.Gosched()
			}
			return nil
		}}
		target := relationDeleteTestAuthorValue(2)
		_, _ = deleter.Delete(context.Background(), backend, &target)
		callbackErr := <-callbackResult
		switch {
		case callbackErr == nil:
			if len(session.setNullPlans) != 1 || len(session.deletePlans) != 1 {
				t.Fatalf("attempt %d admitted callback mutation counts = %d/%d, want 1/1", attempt, len(session.setNullPlans), len(session.deletePlans))
			}
		case errors.Is(callbackErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}):
			if len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 {
				t.Fatalf("attempt %d rejected callback mutation counts = %d/%d, want 0/0", attempt, len(session.setNullPlans), len(session.deletePlans))
			}
		default:
			t.Fatalf("attempt %d racing first callback error = %v", attempt, callbackErr)
		}
	}
}

func TestRelationDeleterCallbackGuardRejectsPortViolations(t *testing.T) {
	t.Parallel()

	t.Run("begin failure without callback preserves primary", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		backend := &relationDeleteTestBackend{invoke: func(context.Context, func(db.RelationSession) error) error {
			return errRelationDeleteAtomic
		}}
		target := relationDeleteTestAuthorValue(2)
		before := target
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, errRelationDeleteAtomic) ||
			errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
			t.Fatalf("Delete(begin failure) = (%d, %v), target %#v", rows, err, target)
		}
	})

	t.Run("success without callback", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		backend := &relationDeleteTestBackend{invoke: func(context.Context, func(db.RelationSession) error) error { return nil }}
		target := relationDeleteTestAuthorValue(2)
		before := target
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
			t.Fatalf("Delete(no callback) = (%d, %v), target %#v", rows, err, target)
		}
	})

	t.Run("typed nil session", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		var session *relationDeleteTestSession
		backend := relationDeleteConformingBackend(session)
		target := relationDeleteTestAuthorValue(2)
		before := target
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
			t.Fatalf("Delete(typed nil session) = (%d, %v), target %#v", rows, err, target)
		}
	})

	t.Run("double callback rejects second invocation and outer result", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		session := &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}}, setNullCounts: []int64{2}, deleteCount: 1}
		var secondErr error
		backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
			firstErr := callback(session)
			secondErr = callback(session)
			return errors.Join(firstErr, secondErr)
		}}
		target := relationDeleteTestAuthorValue(2)
		before := target
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) ||
			!errors.Is(secondErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
			t.Fatalf("Delete(double callback) = (%d, %v), second=%v target=%#v", rows, err, secondErr, target)
		}
		if len(session.setNullPlans) != 1 || len(session.deletePlans) != 1 {
			t.Fatalf("rejected second callback mutated: set-null=%d delete=%d", len(session.setNullPlans), len(session.deletePlans))
		}
	})

	t.Run("swallowed callback error", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		session := &relationDeleteTestSession{queryErrs: []error{errRelationDeleteQuery}}
		backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
			_ = callback(session)
			return nil
		}}
		target := relationDeleteTestAuthorValue(1)
		before := target
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, errRelationDeleteQuery) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
			t.Fatalf("Delete(swallowed callback) = (%d, %v), target %#v", rows, err, target)
		}
	})

	t.Run("atomic error loses callback error", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		session := &relationDeleteTestSession{queryErrs: []error{errRelationDeleteQuery}}
		backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
			_ = callback(session)
			return errRelationDeleteSecondary
		}}
		target := relationDeleteTestAuthorValue(1)
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 0 || !errors.Is(err, errRelationDeleteQuery) || !errors.Is(err, errRelationDeleteSecondary) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("Delete(lost callback error) = (%d, %v)", rows, err)
		}
	})

	t.Run("late callback cannot revise committed result", func(t *testing.T) {
		deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
		session := &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}}, setNullCounts: []int64{2}, deleteCount: 1}
		var retained func(db.RelationSession) error
		backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
			retained = callback
			return callback(session)
		}}
		target := relationDeleteTestAuthorValue(2)
		rows, err := deleter.Delete(context.Background(), backend, &target)
		if rows != 1 || err != nil || target.ID != 0 || target.primaryKeyPresent {
			t.Fatalf("Delete(before late callback) = (%d, %v), target %#v", rows, err, target)
		}
		lateErr := retained(session)
		if !errors.Is(lateErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) ||
			len(session.setNullPlans) != 1 || len(session.deletePlans) != 1 || target.ID != 0 || target.primaryKeyPresent {
			t.Fatalf("late callback = %v, mutations update=%d delete=%d target=%#v", lateErr, len(session.setNullPlans), len(session.deletePlans), target)
		}
	})
}

func TestRelationDeleterConcurrentSecondCallbackIsRejectedRaceSafely(t *testing.T) {
	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	session := &relationDeleteTestSession{queryRows: []db.Rows{&relationDeleteTestRows{}}, setNullCounts: []int64{2}, deleteCount: 1}
	backend := &relationDeleteTestBackend{invoke: func(_ context.Context, callback func(db.RelationSession) error) error {
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				results <- callback(session)
			}()
		}
		close(start)
		return errors.Join(<-results, <-results)
	}}
	target := relationDeleteTestAuthorValue(2)
	before := target
	rows, err := deleter.Delete(context.Background(), backend, &target)
	if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || target != before {
		t.Fatalf("Delete(concurrent callbacks) = (%d, %v), target %#v", rows, err, target)
	}
	if len(session.setNullPlans) != 1 || len(session.deletePlans) != 1 {
		t.Fatalf("concurrent rejected callback mutated: set-null=%d delete=%d", len(session.setNullPlans), len(session.deletePlans))
	}
}

func assertRelationDeletePlans(t *testing.T, session *relationDeleteTestSession, targetKey int64) {
	t.Helper()
	if len(session.queryPlans) != 1 || len(session.setNullPlans) != 1 || len(session.deletePlans) != 1 {
		t.Fatalf("plan counts = query %d set-null %d delete %d", len(session.queryPlans), len(session.setNullPlans), len(session.deletePlans))
	}
	protect := session.queryPlans[0]
	columns := protect.Columns()
	conditions := protect.Conditions()
	if protect.Table() != "blog_post" || len(columns) != 2 || columns[0].Name() != "id" || columns[1].Name() != "author" ||
		len(conditions) != 1 || !conditions[0].Field().Equal(columns[1]) || conditions[0].Lookup() != query.LookupExact ||
		!conditions[0].Value().Equal(query.Integer(targetKey)) {
		t.Fatalf("protect plan = table %q columns %#v conditions %#v", protect.Table(), columns, conditions)
	}
	setNull := session.setNullPlans[0]
	if setNull.Table() != "blog_post" || setNull.ForeignKey().Name() != "reviewer" ||
		setNull.ForeignKey().Column() != "reviewer_id" || !setNull.ForeignKey().Nullable() ||
		!setNull.TargetKey().Equal(query.Integer(targetKey)) {
		t.Fatalf("SET_NULL plan = %#v", setNull)
	}
	deletePlan := session.deletePlans[0]
	if deletePlan.Table() != "authors_author" || deletePlan.KeyField().Name() != "id" ||
		!deletePlan.KeyValue().Equal(query.Integer(targetKey)) {
		t.Fatalf("delete plan = %#v", deletePlan)
	}
	if session.forbiddenMutator != 0 {
		t.Fatalf("unexpected ordinary mutator calls = %d", session.forbiddenMutator)
	}
}
