package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// Keep the native scope, transaction, conflict insertion and lifetime checks.
// Inject only the optional IExact lookup, after real current authorization.
type usernameLookupBackend struct {
	Backend
	phase   string
	fault   error
	cleanup bool
	hits    int
}

func (backend *usernameLookupBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	err := backend.Backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
		if backend.phase == "read" || backend.phase == "cleanup" {
			validator, valid := scope.(db.SessionValidator)
			if !valid {
				return errors.New("username read lost native scope validation")
			}
			scope = &usernameLookupReader{Queryer: scope, validator: validator, owner: backend}
		}
		return read(scope)
	})
	if backend.cleanup {
		return errors.Join(err, backend.fault)
	}
	return err
}

func (backend *usernameLookupBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	return backend.Backend.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
		if backend.phase == "write" {
			conflict, valid := scope.(db.ConflictInserter)
			if !valid {
				return errors.New("username write lost native conflict insertion")
			}
			validator, valid := scope.(db.SessionValidator)
			if !valid {
				return errors.New("username write lost native scope validation")
			}
			scope = &usernameLookupSession{RelationSession: scope, validator: validator, conflict: conflict, owner: backend}
		}
		return write(scope)
	})
}

func (backend *usernameLookupBackend) query(ctx context.Context, scope db.Queryer, plan query.Plan) (db.Rows, error) {
	for _, condition := range plan.Conditions() {
		if condition.Lookup() == query.LookupIExact {
			backend.hits++
			if backend.phase != "cleanup" {
				return nil, backend.fault
			}
		}
	}
	return scope.Query(ctx, plan)
}

type usernameLookupReader struct {
	db.Queryer
	validator db.SessionValidator
	owner     *usernameLookupBackend
}

func (reader *usernameLookupReader) ValidateSession(ctx context.Context) error {
	return reader.validator.ValidateSession(ctx)
}
func (reader *usernameLookupReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return reader.owner.query(ctx, reader.Queryer, plan)
}

type usernameLookupSession struct {
	db.RelationSession
	validator db.SessionValidator
	conflict  db.ConflictInserter
	owner     *usernameLookupBackend
}

func (scope *usernameLookupSession) ValidateSession(ctx context.Context) error {
	return scope.validator.ValidateSession(ctx)
}
func (scope *usernameLookupSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return scope.owner.query(ctx, scope.RelationSession, plan)
}
func (scope *usernameLookupSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	return scope.conflict.InsertOnConflict(ctx, plan)
}

func usernameDeniedBeforeLookup(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionUsernamePolicy(t, backend, "member")
	probe := &usernameLookupBackend{Backend: backend, phase: "read", fault: errors.New("duplicate lookup preceded current admission")}
	owner := newUsernameCreationOwner(t, probe, f, true)
	credential, err := f.runtime.Authenticator().Resolve(t.Context(), f.existing.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	before := userReferenceSnapshot(t, reopen(t))
	candidate, err := owner.runtime.Accounts().Creator().Prepare(t.Context(), credential.Principal(), creationInput(t, "MEMBER"), userReferencePassword)
	requireProfileCode(t, err, accountmodel.CodePermission)
	if !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) || owner.hasher.hashes.Load() != 0 || owner.boundary.calls != 0 || probe.hits != 0 {
		t.Fatal("denied username creation reached duplicate lookup, hash or write")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}

func usernameCleanupFailure(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionUsernamePolicy(t, backend, "member")
	fault := errors.New("private username read cleanup failure")
	probe := &usernameLookupBackend{Backend: backend, phase: "cleanup", fault: fault, cleanup: true}
	owner := newUsernameCreationOwner(t, probe, f, true)
	before := userReferenceSnapshot(t, reopen(t))
	candidate, err := owner.runtime.Accounts().Creator().Prepare(t.Context(), f.actor.Principal(), creationInput(t, "MEMBER"), userReferencePassword)
	requireProfileCode(t, err, accountmodel.CodePersistence)
	if !errors.Is(err, fault) || probe.hits != 1 || !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) || owner.hasher.hashes.Load() != 0 || owner.boundary.calls != 0 {
		t.Fatal("duplicate cleanup failure published or performed password/write work", err)
	}
	if _, rejected := validation.Rejected(err); rejected {
		t.Fatal("cleanup failure became a username rejection")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}

func usernameLookupFailure(t *testing.T, backend Backend, reopen func(*testing.T) Backend, phase string) {
	f := provisionUsernamePolicy(t, backend, "member")
	fault := errors.New("private selected username lookup failure")
	probe := &usernameLookupBackend{Backend: backend, phase: phase, fault: fault}
	owner := newUsernameCreationOwner(t, probe, f, true)
	before := userReferenceSnapshot(t, reopen(t))
	creator := owner.runtime.Accounts().Creator()
	candidate, err := creator.Prepare(t.Context(), f.actor.Principal(), creationInput(t, "candidate"), userReferencePassword)
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	if phase == "write" {
		if err != nil || owner.hasher.hashes.Load() != 1 || probe.hits != 0 {
			t.Fatal("write lookup fault ran before preparation", err)
		}
		result, failure := creator.Commit(t.Context(), f.actor.Principal(), candidate)
		err = failure
		if !reflect.DeepEqual(result, accountmodel.Creation{}) {
			t.Fatal("failed username write lookup published metadata")
		}
	} else if !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) {
		t.Fatal("failed username read published a candidate")
	}
	wantHashes, wantWrites := int64(0), 0
	if phase == "write" {
		wantHashes, wantWrites = 1, 1
	}
	requireProfileCode(t, err, accountmodel.CodePersistence)
	if !errors.Is(err, fault) || probe.hits != 1 || owner.hasher.hashes.Load() != wantHashes || owner.boundary.calls != wantWrites {
		t.Fatal("selected username lookup failure lost cause, phase or single attempt", err)
	}
	if _, rejected := validation.Rejected(err); rejected || strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
		t.Fatal("lookup failure disclosed its cause or became validation")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}

func usernameWriteFailure(t *testing.T, backend Backend, reopen func(*testing.T) Backend, mode string) {
	f := provisionUsernamePolicy(t, backend, "member")
	owner := newUsernameCreationOwner(t, backend, f, true)
	fault := errors.New("private selected username audit failure")
	effects := map[string]int{}
	owner.boundary.mode = mode
	if mode == "audit_failure" {
		owner.boundary.mode = "username_audit_failure"
		owner.boundary.decorate = func(scope db.Session) db.Session { return newRoleFaultScope(t, scope, "audit_failure", fault, effects) }
	}
	before := userReferenceSnapshot(t, reopen(t))
	creator := owner.runtime.Accounts().Creator()
	candidate, err := creator.Prepare(t.Context(), f.actor.Principal(), creationInput(t, "username-outcome"), userReferencePassword)
	if err != nil || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 0 {
		t.Fatal("username failure fixture did not prepare once", err)
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	result, err := creator.Commit(t.Context(), f.actor.Principal(), candidate)
	if !reflect.DeepEqual(result, accountmodel.Creation{}) || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 1 {
		t.Fatal("failed username creation published or repeated work")
	}
	if mode == "audit_failure" {
		requireProfileCode(t, err, accountmodel.CodePersistence)
		if !errors.Is(err, fault) || effects["godj_identity_user"] != 1 || effects["godj_account_audit_event"] != 1 {
			t.Fatal("username audit fault did not follow the actual user and audit inserts", err)
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	} else {
		requireProfileCode(t, err, accountmodel.CodeOutcomeUnknown)
		if owner.boundary.nativeError != nil || !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatal("username unknown outcome lost the committed native result", err)
		}
		usernameCommitted(t, reopen(t), before, f, owner, "username-outcome")
	}
	after := userReferenceSnapshot(t, reopen(t))
	result, err = creator.Commit(t.Context(), f.actor.Principal(), candidate)
	requireProfileCode(t, err, accountmodel.CodeConflict)
	if !reflect.DeepEqual(result, accountmodel.Creation{}) || owner.boundary.calls != 1 || owner.hasher.hashes.Load() != 1 {
		t.Fatal("failed or unknown username candidate was reusable")
	}
	assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}
