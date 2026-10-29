package orm_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	hostproject "github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Deliberately exposes only the read-snapshot capability set.
type collectionObservationReader struct{ scope *collectionPreparationScope }

func (reader *collectionObservationReader) ValidateSession(ctx context.Context) error {
	return reader.scope.ValidateSession(ctx)
}
func (reader *collectionObservationReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return reader.scope.Query(ctx, plan)
}

type observedLinkInput struct{ build func() error }

func (input observedLinkInput) BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[models.UserGroupsLink] {
	if err := input.build(); err != nil {
		return orm.InvalidMutation[models.UserGroupsLink](err)
	}
	return (models.UserGroupsLinkCreate{}).BuildManyToManyCreate(source, target, sourceKey, targetKey)
}

func TestCollectionObservationRejectsInvalidReadOwnershipBeforeIO(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	change, err := collections.IdentityUserGroups.SetKeysChange(models.NewUserWithID(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil_context", "canceled", "nil_reader", "typed_nil_reader", "root_reader", "write_scope", "relation_write", "conflict_write", "bulk_insert", "bulk_update", "query_update", "savepoint", "snapshot", "atomic", "coordinated", "relation_atomic", "coordinated_relation", "expired", "empty", "unbound", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, scope := t.Context(), &collectionPreparationScope{}
			var reader db.Queryer = &collectionObservationReader{scope: scope}
			changes := []orm.CollectionChange{change}
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_reader":
				reader = nil
			case "typed_nil_reader":
				reader = (*collectionObservationReader)(nil)
			case "root_reader":
				reader = &struct{ db.Queryer }{}
			case "write_scope":
				reader = scope
			case "relation_write":
				reader = struct {
					*collectionObservationReader
					db.RelationMutator
				}{&collectionObservationReader{scope}, nil}
			case "conflict_write":
				reader = struct {
					*collectionObservationReader
					db.ConflictInserter
				}{&collectionObservationReader{scope}, nil}
			case "bulk_insert":
				reader = struct {
					*collectionObservationReader
					db.BulkInserter
				}{&collectionObservationReader{scope}, nil}
			case "bulk_update":
				reader = struct {
					*collectionObservationReader
					db.BulkUpdater
				}{&collectionObservationReader{scope}, nil}
			case "query_update":
				reader = struct {
					*collectionObservationReader
					db.QueryUpdater
				}{&collectionObservationReader{scope}, nil}
			case "savepoint":
				reader = struct {
					*collectionObservationReader
					db.Savepointer
				}{&collectionObservationReader{scope}, nil}
			case "snapshot":
				reader = struct {
					*collectionObservationReader
					db.SnapshotReader
				}{&collectionObservationReader{scope}, nil}
			case "atomic":
				reader = struct {
					*collectionObservationReader
					db.Atomic
				}{&collectionObservationReader{scope}, nil}
			case "coordinated":
				reader = struct {
					*collectionObservationReader
					db.CoordinatedAtomic
				}{&collectionObservationReader{scope}, nil}
			case "relation_atomic":
				reader = struct {
					*collectionObservationReader
					db.RelationAtomic
				}{&collectionObservationReader{scope}, nil}
			case "coordinated_relation":
				reader = struct {
					*collectionObservationReader
					db.CoordinatedRelationAtomic
				}{&collectionObservationReader{scope}, nil}
			case "expired":
				scope.expired = errors.New("expired observation snapshot")
			case "empty":
				changes = nil
			case "unbound":
				changes = []orm.CollectionChange{{}}
			case "duplicate":
				changes = append(changes, change)
			}
			observed, err := orm.ObserveCollectionChangesInReadScope(ctx, reader, changes...)
			if err == nil || scope.queries != 0 || scope.writes != 0 || len(observed.Effects().Insertions()) != 0 {
				t.Fatal("invalid observation reached I/O or published a result", err)
			}
			if _, err := observed.PrepareInSession(t.Context(), scope, change); err == nil || scope.queries != 0 {
				t.Fatal("failed observation became executable", err)
			}
		})
	}
}

func TestCollectionObservationDetachesInputsAndRechecksOrderedSelections(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	host, err := hostproject.Bind()
	if err != nil {
		t.Fatal(err)
	}
	hostCollections, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	owner, group := models.NewUserWithID(0), models.NewGroupWithID(-7)
	firstCalls := 0
	first := observedLinkInput{build: func() error { firstCalls++; return nil }}
	forward, err := collections.IdentityUserGroups.SetKeysChange(owner, []int64{-7}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: first})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := collections.IdentityGroupUsers.SetKeysChange(group, []int64{0}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: first})
	if err != nil {
		t.Fatal(err)
	}
	read := &collectionPreparationScope{}
	observed, err := orm.ObserveCollectionChangesInReadScope(t.Context(), &collectionObservationReader{read}, forward, reverse)
	if err != nil || observed.ValidateSource(binding) != nil || observed.ValidateSource(host) == nil {
		t.Fatal("observation lost complete source binding", err)
	}
	if firstCalls != 1 || read.queries != 2 || read.closes != 2 || read.writes != 0 || len(observed.Effects().Insertions()) != 1 {
		t.Fatal("read-only overlapping selection did not coalesce its actual typed input")
	}
	expected := observed.Effects()
	inserts := expected.Insertions()
	inserts[0].Values["source"] = query.Integer(999)
	inserts[0].Model.ModelName = "caller_changed"
	if !reflect.DeepEqual(expected, observed.Effects()) || !strings.Contains(fmt.Sprintf("%#v", observed), "redacted") {
		t.Fatal("observation exposed or aliased its owned evidence")
	}
	// The old input and read scope become unusable. Final preparation must use
	// only new inputs and the new write session, after the full state recheck.
	read.expired = errors.New("read snapshot has closed")
	finalCalls := 0
	finalInput := observedLinkInput{build: func() error { finalCalls++; return nil }}
	final, err := collections.IdentityUserGroups.SetKeysChange(owner, []int64{-7}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: finalInput})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := collections.IdentityUserGroups.SetKeysChange(owner, []int64{42}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: finalInput})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := hostCollections.IdentityUserGroups.SetKeysChange(owner, []int64{-7}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: finalInput})
	if err != nil {
		t.Fatal(err)
	}
	for _, changes := range [][]orm.CollectionChange{{changed, reverse}, {reverse, final}, {final}, {foreign, reverse}} {
		scope := &collectionPreparationScope{}
		if _, err := observed.PrepareInSession(t.Context(), scope, changes...); err == nil || scope.queries != 0 || finalCalls != 0 || firstCalls != 1 {
			t.Fatal("mismatched selection reached I/O or input preparation", err)
		}
	}
	write := &collectionPreparationScope{}
	prepared, err := observed.PrepareInSession(t.Context(), write, final, reverse)
	if err != nil || prepared.ValidateSource(binding) != nil || finalCalls != 1 || firstCalls != 1 || read.queries != 2 || write.queries != 2 || write.closes != 2 || write.writes != 0 {
		t.Fatal("final preparation reused the reader/input or lost typed coalescing", err)
	}
}

func TestCollectionObservationDiscardsCandidateOnInputOrLifetimeFailure(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"input", "expiry", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			scope := &collectionPreparationScope{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private candidate failure")
			calls := 0
			input := observedLinkInput{build: func() error {
				calls++
				switch mode {
				case "input":
					return fault
				case "expiry":
					scope.expired = fault
				case "cancel":
					cancel()
				}
				return nil
			}}
			change, err := collections.IdentityUserGroups.SetKeysChange(models.NewUserWithID(0), []int64{-7}, orm.ManyToManySetOptions[models.UserGroupsLink]{ThroughDefaults: input})
			if err != nil {
				t.Fatal(err)
			}
			observed, err := orm.ObserveCollectionChangesInReadScope(ctx, &collectionObservationReader{scope}, change)
			cause := fault
			if mode == "cancel" {
				cause = context.Canceled
			}
			if !errors.Is(err, cause) || calls != 1 || observed.ValidateSource(binding) == nil || len(observed.Effects().Insertions()) != 0 || scope.queries != 1 || scope.closes != 1 || scope.writes != 0 {
				t.Fatal("failed observation published partial evidence or lost its cause", err)
			}
		})
	}
}

func TestCollectionObservationFinalScopeRefusals(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	change, err := collections.IdentityUserGroups.SetKeysChange(models.NewUserWithID(-7), nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := orm.ObserveCollectionChangesInReadScope(t.Context(), &collectionObservationReader{&collectionPreparationScope{}}, change)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil_context", "canceled", "nil_session", "typed_nil_session", "root_session", "expired"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			scope := &collectionPreparationScope{}
			var session db.RelationSession = scope
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_session":
				session = nil
			case "typed_nil_session":
				session = (*collectionPreparationScope)(nil)
			case "root_session":
				session = &struct{ db.RelationSession }{}
			case "expired":
				scope.expired = errors.New("expired final collection session")
			}
			prepared, err := observed.PrepareInSession(ctx, session, change)
			if err == nil || scope.queries != 0 || scope.writes != 0 {
				t.Fatal("observation bypassed final scope ownership", err)
			}
			if _, err := prepared.Apply(t.Context(), nil); err == nil {
				t.Fatal("scope refusal published an executable plan")
			}
		})
	}
}
