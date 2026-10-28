// Package accountdomaintest exercises the account-domain protocol on native
// SQLite and PostgreSQL connections. It creates no default account tables.
package accountdomaintest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	accounts "github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

type Backend interface {
	db.Session
	db.CoordinatedAtomic
	db.SnapshotReader
	mb.RevisionFencedBackend
}

type Open func(*testing.T) (Backend, func(*testing.T) Backend)

func Run(t *testing.T, open Open) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, Backend, func(*testing.T) Backend)
	}{
		{"bootstrap_rollback", bootstrapRollback},
		{"transition_history_and_reopen", transitionHistory},
		{"concurrent_transition", concurrentTransition},
		{"coherent_snapshot", coherentSnapshot},
		{"read_contract_and_cleanup", readContract},
		{"expired_scope", expiredScope},
		{"invalid_history", invalidHistory},
		{"unknown_outcome_reconciliation", unknownOutcome},
		{"inspect_scope", inspectScope},
		{"lookup_contract", lookupContract},
		{"mapping_history", mappingHistory},
		{"mapping_pages", mappingPages},
		{"mapping_failures", mappingFailures},
		{"mapping_write_readback", mappingWriteReadback},
		{"mapping_corruption", mappingCorruption},
		{"catalog_mapping_history", catalogMappingHistory},
		{"catalog_mapping_failures", catalogMappingFailures},
		{"catalog_mapping_corruption", catalogMappingCorruption},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reopen := open(t)
			loaded, _, err := definition.Load(domain.MigrationSources()...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			// The independently migratable domain must not create a hidden User.
			if _, err := accounts.UserObjects.Using(backend).Exists(t.Context()); err == nil {
				t.Fatal("domain migration installed a default account")
			}
			test.run(t, backend, reopen)
		})
	}
	inspectLostUniqueness(t, open)
	mappingUpgrade(t, open)
	mappingLostConstraint(t, open)
}

var instant = time.Date(2026, 10, 24, 1, 2, 3, 456789000, time.UTC)
var roleA, roleB, evidence = fingerprint("role-a"), fingerprint("role-b"), fingerprint("reviewed mapping")

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func prepare(t *testing.T, role string) domain.Change {
	t.Helper()
	change, err := domain.Prepare(t.Context(), role, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func initialize(t *testing.T, backend Backend) domain.Receipt {
	t.Helper()
	change := prepare(t, roleA)
	var receipt domain.Receipt
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		var err error
		receipt, err = domain.InitializeIn(t.Context(), transaction, change, instant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func transition(ctx context.Context, backend Backend, previous domain.Receipt, change domain.Change) (domain.Receipt, error) {
	var candidate domain.Receipt
	err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		var err error
		candidate, err = domain.TransitionIn(ctx, transaction, previous, change, instant)
		return err
	})
	if err != nil {
		return domain.Receipt{}, err
	}
	return candidate, nil
}

func bootstrapRollback(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	ctx := t.Context()
	if got, err := domain.Read(ctx, backend, roleA); !errors.Is(err, domain.ErrUninitialized) || got.Sequence() != 0 {
		t.Fatal("uninitialized", err)
	}
	change := prepare(t, roleA)
	if err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		broken := &firstReadFailure{Session: transaction}
		got, err := domain.InitializeIn(ctx, broken, change, instant)
		if !errors.Is(err, domain.ErrUninitialized) || got.Sequence() != 0 || broken.inserts != 0 {
			return errors.New("failed observation was treated as empty history")
		}
		for _, bad := range []time.Time{time.Time{}, time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600))} {
			if got, err := domain.InitializeIn(ctx, transaction, change, bad); !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
				return errors.New("invalid normalized timestamp accepted")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.InitializeIn(ctx, backend, change, instant); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("unfenced root backend accepted", err)
	}
	fault := errors.New("provisioning failed after domain insert")
	if err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		got, err := domain.InitializeIn(ctx, transaction, change, instant)
		if err != nil {
			return err
		}
		if got.OperationID() != change.OperationID() || got.Sequence() != 1 {
			return errors.New("invalid provisional receipt")
		}
		return fault
	}); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if got, err := domain.Lookup(ctx, reopen(t), change.OperationID()); !errors.Is(err, domain.ErrUninitialized) || got.Sequence() != 0 {
		t.Fatal("rollback left a receipt", err)
	}
	// Explicit retry after confirmed rollback can use the same preparation.
	if err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		_, err := domain.InitializeIn(ctx, transaction, change, instant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replacement := prepare(t, roleB)
	if err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		_, err := domain.InitializeIn(ctx, transaction, replacement, instant)
		return err
	}); !errors.Is(err, domain.ErrStale) {
		t.Fatal("bootstrap replaced existing ownership", err)
	}
}

type firstReadFailure struct {
	db.Session
	reads, inserts int
}

func (session *firstReadFailure) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *firstReadFailure) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	session.reads++
	if session.reads == 1 {
		return nil, domain.ErrUninitialized
	}
	return session.Session.Query(ctx, plan)
}
func (session *firstReadFailure) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.inserts++
	return session.Session.Insert(ctx, plan)
}

func transitionHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	ctx := t.Context()
	first := initialize(t, backend)
	change := prepare(t, roleB)
	second, err := transition(ctx, backend, first, change)
	if err != nil || second.Sequence() != 2 || second.Generation() == first.Generation() {
		t.Fatal("transition", err)
	}
	if got, err := domain.Read(ctx, reopen(t), roleA); !errors.Is(err, domain.ErrMismatch) || got.Sequence() != 0 {
		t.Fatal("old role survived reopen", err)
	}
	if got, err := domain.Read(ctx, reopen(t), roleB); err != nil || got.OperationID() != second.OperationID() || !got.At().Equal(instant) || got.EvidenceSHA256() != evidence {
		t.Fatal("new ownership missing", err)
	}
	if _, err := transition(ctx, backend, first, prepare(t, roleA)); !errors.Is(err, domain.ErrStale) {
		t.Fatal("stale receipt accepted", err)
	}
	if _, err := transition(ctx, backend, second, change); !errors.Is(err, domain.ErrStale) {
		t.Fatal("committed change replayed", err)
	}
	third, err := transition(ctx, backend, second, prepare(t, roleA))
	if err != nil || third.Sequence() != 3 || third.Generation() == first.Generation() || third.Generation() == second.Generation() {
		t.Fatal("returning model reused generation", err)
	}
	for _, want := range []domain.Receipt{first, second, third} {
		got, err := domain.Lookup(ctx, reopen(t), want.OperationID())
		if err != nil || got.Sequence() != want.Sequence() || got.Generation() != want.Generation() || got.RoleFingerprint() != want.RoleFingerprint() {
			t.Fatal("immutable history changed", err)
		}
	}
	fault := errors.New("mapping failed after new receipt")
	failedChange := prepare(t, roleB)
	if err := backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		_, err := domain.TransitionIn(ctx, transaction, third, failedChange, instant)
		if err != nil {
			return err
		}
		return fault
	}); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if got, err := domain.Read(ctx, reopen(t), roleA); err != nil || got.OperationID() != third.OperationID() {
		t.Fatal("failed mapping changed domain", err)
	}
	oldest, found, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.Sequence.Exact(1)).OrderBy(models.TransitionFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := models.TransitionObjects.Delete(ctx, backend, &oldest); err == nil {
		t.Fatal("predecessor deletion was not protected")
	}
}

func concurrentTransition(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	previous := initialize(t, backend)
	other := reopen(t)
	changes := []domain.Change{prepare(t, roleA), prepare(t, roleB)}
	backends := []Backend{backend, other}
	var receipts [2]domain.Receipt
	var errs [2]error
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			receipts[i], errs[i] = transition(t.Context(), backends[i], previous, changes[i])
		}()
	}
	close(start)
	workers.Wait()
	successes, stale := 0, 0
	for i, err := range errs {
		if err == nil {
			successes++
			if receipts[i].Sequence() != 2 {
				t.Fatal("winner did not advance once")
			}
		} else if errors.Is(err, domain.ErrStale) {
			stale++
			if receipts[i].Sequence() != 0 {
				t.Fatal("loser published receipt")
			}
			if _, lookupErr := domain.Lookup(t.Context(), reopen(t), changes[i].OperationID()); !errors.Is(lookupErr, domain.ErrUninitialized) {
				t.Fatal("loser persisted", lookupErr)
			}
		} else {
			t.Fatal("unexpected transition result", err)
		}
	}
	if successes != 1 || stale != 1 {
		t.Fatal("transition did not select one winner", successes, stale)
	}
	if count, err := models.TransitionObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
		t.Fatal("wrong durable history count", count, err)
	}
}

func coherentSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	writer := reopen(t)
	change := prepare(t, roleB)
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		before, err := domain.RequireIn(t.Context(), reader, roleA)
		if err != nil || before.OperationID() != first.OperationID() {
			return errors.Join(err, errors.New("initial snapshot differs"))
		}
		if _, err := transition(t.Context(), writer, first, change); err != nil {
			return err
		}
		after, err := domain.RequireIn(t.Context(), reader, roleA)
		if err != nil || after.OperationID() != before.OperationID() {
			return errors.Join(err, errors.New("mixed account ownership snapshot"))
		}
		if _, err := domain.RequireIn(t.Context(), reader, roleB); !errors.Is(err, domain.ErrMismatch) {
			return errors.New("uncommitted-to-snapshot role admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := domain.Read(t.Context(), backend, roleB); err != nil || got.OperationID() != change.OperationID() {
		t.Fatal("fresh snapshot retained stale domain", err)
	}
}

type snapshotFunc func(context.Context, func(db.Queryer) error) error

func (function snapshotFunc) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return function(ctx, callback)
}

func readContract(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	initialize(t, backend)
	fault := errors.New("private read cleanup fault")
	for _, scenario := range []string{"cleanup", "ignored_callback_error", "twice", "zero", "nil", "cancel"} {
		ctx, cancel := context.WithCancel(t.Context())
		reader := snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
			switch scenario {
			case "zero":
				return nil
			case "nil":
				_ = callback(nil)
				return nil
			case "ignored_callback_error":
				_ = backend.ReadSnapshot(ctx, callback)
				return nil
			default:
				if err := backend.ReadSnapshot(ctx, callback); err != nil {
					return err
				}
				switch scenario {
				case "cleanup":
					return fault
				case "twice":
					_ = backend.ReadSnapshot(ctx, callback)
				case "cancel":
					cancel()
				}
				return nil
			}
		})
		role := roleA
		if scenario == "ignored_callback_error" {
			role = roleB
		}
		got, err := domain.Read(ctx, reader, role)
		cancel()
		if err == nil || got.Sequence() != 0 {
			t.Fatal(scenario, "published an invalid read")
		}
		if scenario == "cleanup" && (!errors.Is(err, fault) || fmt.Sprintf("%+v", err) != "account domain: storage operation failed") {
			t.Fatal("cleanup cause or redaction lost", err)
		}
	}
}

func expiredScope(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	current := initialize(t, backend)
	var expired db.Session
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error { expired = transaction; return nil }); err != nil {
		t.Fatal(err)
	}
	if got, err := domain.TransitionIn(t.Context(), expired, current, prepare(t, roleB), instant); err == nil || got.Sequence() != 0 {
		t.Fatal("expired write scope accepted")
	}
	if got, err := domain.RequireIn(t.Context(), expired, roleA); err == nil || got.Sequence() != 0 {
		t.Fatal("expired read scope accepted")
	}
}

func invalidHistory(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	first := initialize(t, backend)
	if _, err := transition(t.Context(), backend, first, prepare(t, roleB)); err != nil {
		t.Fatal(err)
	}
	row, found, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.Sequence.Exact(2)).OrderBy(models.TransitionFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	for _, patch := range []models.TransitionPatch{
		models.TransitionPatch{}.WithPreviousIDNull(),
		models.TransitionPatch{}.WithSequence(4),
		models.TransitionPatch{}.WithRoleFingerprint("malformed"),
	} {
		bad, err := models.TransitionObjects.Patch(t.Context(), backend, row, patch)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := domain.Read(t.Context(), backend, roleB); !errors.Is(err, domain.ErrCorrupt) || got.Sequence() != 0 {
			t.Fatal("corrupt history admitted", err)
		}
		row, err = models.TransitionObjects.Patch(t.Context(), backend, bad, models.TransitionPatch{}.WithPreviousID(*row.PreviousID).WithSequence(2).WithRoleFingerprint(roleB))
		if err != nil {
			t.Fatal(err)
		}
	}
}

type unknownBackend struct {
	Backend
	commit bool
	calls  int
}

func (backend *unknownBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	backend.calls++
	unknown := &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Detail: "injected commit acknowledgement loss"}
	err := backend.Backend.CoordinatedAtomic(ctx, func(transaction db.Session) error {
		if err := callback(transaction); err != nil {
			return err
		}
		if !backend.commit {
			return unknown
		}
		return nil
	})
	if err != nil {
		return err
	}
	return unknown
}

func unknownOutcome(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	for _, commit := range []bool{false, true} {
		change := prepare(t, roleB)
		unknown := &unknownBackend{Backend: backend, commit: commit}
		got, err := transition(t.Context(), unknown, first, change)
		if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || got.Sequence() != 0 || unknown.calls != 1 {
			t.Fatal("unknown outcome published or retried", err)
		}
		reconciled, lookupErr := domain.Lookup(t.Context(), reopen(t), change.OperationID())
		if commit {
			if lookupErr != nil || reconciled.Sequence() != 2 || reconciled.EvidenceSHA256() != evidence {
				t.Fatal("committed unknown operation missing", lookupErr)
			}
		} else if !errors.Is(lookupErr, domain.ErrUninitialized) || reconciled.Sequence() != 0 {
			t.Fatal("rolled back unknown operation appeared", lookupErr)
		}
	}
}
