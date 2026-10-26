package sessiondbtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/sessions/database/models"
)

var injected = errors.New("private native session failure marker")

type faultBackend struct {
	Backend
	mode                    string
	calls, inserts, deletes int
	cancel                  context.CancelFunc
}

func (owner *faultBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	owner.calls++
	switch owner.mode {
	case "skipped":
		return nil
	case "root":
		return read(owner.Backend)
	case "nil":
		return read(nil)
	case "typed_nil":
		return read((*faultReader)(nil))
	}
	return owner.Backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
		reader := &faultReader{Queryer: scope, mode: owner.mode}
		err := read(reader)
		switch owner.mode {
		case "repeated":
			return errors.Join(err, read(reader))
		case "swallowed":
			return nil
		case "cleanup":
			return errors.Join(err, injected)
		case "cancel":
			owner.cancel()
			return errors.Join(err, ctx.Err())
		default:
			return err
		}
	})
}

type faultReader struct {
	db.Queryer
	mode string
}

func (reader *faultReader) ValidateSession(ctx context.Context) error {
	return reader.Queryer.(db.SessionValidator).ValidateSession(ctx)
}
func (reader *faultReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := reader.Queryer.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	switch reader.mode {
	case "query", "swallowed":
		return rows, injected
	case "close":
		return &faultRows{Rows: rows}, nil
	default:
		return rows, nil
	}
}

type faultRows struct{ db.Rows }

func (rows *faultRows) Close() error { return errors.Join(rows.Rows.Close(), injected) }

func (owner *faultBackend) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	owner.calls++
	switch owner.mode {
	case "skipped":
		return nil
	case "root":
		return write(owner.Backend)
	case "nil":
		return write(nil)
	case "typed_nil":
		return write((*faultSession)(nil))
	}
	owned := ctx
	if owner.mode == "cancel_rollback" {
		owned = context.WithoutCancel(ctx)
	}
	err := owner.Backend.CoordinatedAtomic(owned, func(scope db.Session) error {
		decorated := &faultSession{Session: scope, owner: owner}
		err := write(decorated)
		switch owner.mode {
		case "repeated":
			return errors.Join(err, write(decorated))
		case "swallowed":
			return nil
		case "cleanup", "unknown_rollback":
			return errors.Join(err, injected)
		case "wrapped":
			if err != nil {
				return fmt.Errorf("owner rollback: %w", err)
			}
			return nil
		case "cancel_rollback":
			owner.cancel()
			return errors.Join(err, ctx.Err())
		default:
			return err
		}
	})
	switch owner.mode {
	case "unknown_commit":
		if err == nil {
			return &query.Error{Code: query.CodeCommitOutcomeUnknown, Cause: injected}
		}
	case "unknown_rollback":
		return &query.Error{Code: query.CodeTransactionOutcomeUnknown, Cause: errors.Join(err, injected)}
	case "late_cancel":
		if err == nil {
			owner.cancel()
		}
	}
	return err
}

type faultSession struct {
	db.Session
	owner *faultBackend
}

func (scope *faultSession) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *faultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	id, err := scope.Session.Insert(ctx, plan)
	if err == nil {
		scope.owner.inserts++
	}
	if scope.owner.mode == "insert_failure" || scope.owner.mode == "swallowed" {
		return id, errors.Join(err, injected)
	}
	return id, err
}
func (scope *faultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.Session.Delete(ctx, plan)
	if err == nil {
		scope.owner.deletes++
	}
	if scope.owner.mode == "delete_failure" {
		return count, errors.Join(err, injected)
	}
	return count, err
}

func readCompletion(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	value := record(t, 1, nil, instant)
	create(t, store(t, backend, "read", sessiondb.Config{}), value)
	for _, mode := range []string{"skipped", "root", "nil", "typed_nil", "repeated", "query", "close", "swallowed", "cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			for _, operation := range []string{"load", "inspect"} {
				t.Run(operation, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					probe := &faultBackend{Backend: backend, mode: mode, cancel: cancel}
					bound := store(t, probe, "read", sessiondb.Config{})
					var err error
					if operation == "load" {
						var result sessions.Record
						var present bool
						result, present, err = bound.Load(ctx, value.ID())
						if present || result.ID().Valid() {
							t.Fatal("failed read published a record")
						}
					} else {
						var count int
						count, err = bound.Inspect(ctx)
						if count != 0 {
							t.Fatal("failed inspection published inventory")
						}
					}
					if err == nil || probe.calls != 1 {
						t.Fatal("read completion failure was accepted", err, probe.calls)
					}
					if (mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup") && !errors.Is(err, injected) {
						t.Fatal("private completion cause was discarded", err)
					}
				})
			}
		})
	}
}

func writeCompletion(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	for index, mode := range []string{"skipped", "root", "nil", "typed_nil", "repeated", "insert_failure", "swallowed", "cleanup", "cancel_rollback", "unknown_rollback", "unknown_commit", "late_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := &faultBackend{Backend: backend, mode: mode, cancel: cancel}
			namespace := "write-" + mode
			bound := store(t, probe, namespace, sessiondb.Config{})
			value := record(t, index+1, nil, instant)
			created, err := bound.Create(ctx, value)
			if mode == "late_cancel" {
				if !created || err != nil || ctx.Err() == nil {
					t.Fatal("confirmed commit was downgraded after cancellation", created, err)
				}
			} else if created || err == nil {
				t.Fatal("failed write published creation", created, err)
			}
			if probe.calls != 1 {
				t.Fatal("write automatically retried", probe.calls)
			}
			if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("unknown commit lost", err)
			}
			if (mode == "skipped" || mode == "repeated" || mode == "swallowed" || mode == "unknown_rollback") && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
				t.Fatal("unknown callback outcome lost", err)
			}
			if (mode == "insert_failure" || mode == "swallowed" || mode == "cleanup" || mode == "unknown_commit" || mode == "unknown_rollback") && !errors.Is(err, injected) {
				t.Fatal("private write cause lost", err)
			}
			if mode == "insert_failure" && probe.inserts != 1 {
				t.Fatal("failure did not follow native insert")
			}
			requireRecord(t, store(t, backend, namespace, sessiondb.Config{}), value, mode == "unknown_commit" || mode == "swallowed" || mode == "late_cancel")
		})
	}
	t.Run("policy_completion", func(t *testing.T) {
		original := record(t, 90, nil, instant)
		base := store(t, backend, "policy", sessiondb.Config{})
		create(t, base, original)
		for _, mode := range []string{"direct", "wrapped", "cleanup", "unknown_rollback", "swallowed"} {
			t.Run(mode, func(t *testing.T) {
				probe := &faultBackend{Backend: backend, mode: mode}
				manager, err := sessions.NewManager(store(t, probe, "policy", sessiondb.Config{}), sessions.Config{Clock: func() time.Time { return time.Time{} }})
				if err != nil {
					t.Fatal(err)
				}
				result, present, err := manager.Load(t.Context(), original.ID())
				classified, ok := err.(*sessions.Error)
				want := sessions.CodeStoreFailure
				if mode == "direct" {
					want = sessions.CodeInvalidConfig
				}
				if !ok || classified.Code != want || present || result.ID().Valid() {
					t.Fatal("policy error hid storage completion", err)
				}
				if (mode == "cleanup" || mode == "unknown_rollback") && !errors.Is(err, injected) {
					t.Fatal("policy completion cause lost", err)
				}
				requireRecord(t, base, original, true)
			})
		}
	})
	t.Run("delete_failure", func(t *testing.T) {
		original := record(t, 91, nil, instant)
		base := store(t, backend, "delete", sessiondb.Config{})
		create(t, base, original)
		probe := &faultBackend{Backend: backend, mode: "delete_failure"}
		if err := store(t, probe, "delete", sessiondb.Config{}).Delete(t.Context(), original.ID()); !errors.Is(err, injected) || probe.deletes != 1 {
			t.Fatal("native delete failure not reached", err)
		}
		requireRecord(t, base, original, true)
	})
}

func storedIntegrity(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	base := store(t, backend, "integrity", sessiondb.Config{})
	value := record(t, 1, nil, instant)
	create(t, base, value)
	row, present, err := models.SessionObjects.Using(backend).OrderBy(models.SessionFields.ID.Asc()).First(t.Context())
	if err != nil || !present {
		t.Fatal(err)
	}
	for _, mode := range []string{"payload", "digest", "namespace"} {
		t.Run(mode, func(t *testing.T) {
			patch := models.SessionPatch{}
			switch mode {
			case "payload":
				patch = patch.WithPayload("invalid-payload")
			case "digest":
				patch = patch.WithDigest("invalid-digest")
			case "namespace":
				patch = patch.WithNamespace("")
			}
			if _, err := models.SessionObjects.Patch(t.Context(), backend, row, patch); err != nil {
				t.Fatal("corrupt fixture", err)
			}
			if count, err := base.Inspect(t.Context()); count != 0 || err == nil {
				t.Fatal("corrupt inventory accepted", count, err)
			}
			if count, err := base.DeleteAll(t.Context()); count != 0 || err == nil {
				t.Fatal("corruption silently cleaned", count, err)
			}
			if mode == "payload" {
				if value, present, err := base.Load(t.Context(), value.ID()); value.ID().Valid() || present || err == nil {
					t.Fatal("corrupt bearer payload accepted")
				}
			}
			if _, err := models.SessionObjects.Patch(t.Context(), backend, row, models.SessionPatch{}.WithPayload(row.Payload).WithDigest(row.Digest).WithNamespace(row.Namespace)); err != nil {
				t.Fatal(err)
			}
			requireRecord(t, base, value, true)
		})
	}
	if _, err := models.SessionObjects.Create(t.Context(), backend, models.NewSessionCreate("foreign", strings.Repeat("b", 64), row.Payload)); err != nil {
		t.Fatal(err)
	}
	limited := store(t, backend, "integrity", sessiondb.Config{MaxRecords: 1})
	if count, err := limited.Inspect(t.Context()); count != 0 || !errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFull}) {
		t.Fatal("over-capacity stored inventory accepted", count, err)
	}
}

func concurrentCapacity(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	left, right := store(t, backend, "left", sessiondb.Config{MaxRecords: 1}), store(t, reopen(t), "right", sessiondb.Config{MaxRecords: 1})
	start := make(chan struct{})
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 2)
	var joined sync.WaitGroup
	for index, value := range []*sessiondb.Store{left, right} {
		candidate := record(t, index+1, nil, instant)
		joined.Add(1)
		go func() {
			defer joined.Done()
			<-start
			created, err := value.Create(t.Context(), candidate)
			results <- result{created, err}
		}()
	}
	close(start)
	joined.Wait()
	close(results)
	winners, full := 0, 0
	for result := range results {
		if result.created && result.err == nil {
			winners++
		} else if !result.created && errors.Is(result.err, &sessions.Error{Code: sessions.CodeStoreFull}) {
			full++
		} else {
			t.Fatal("concurrent capacity result", result.created, result.err)
		}
	}
	if winners != 1 || full != 1 {
		t.Fatal("capacity fence failed", winners, full)
	}
	if count, err := left.Inspect(t.Context()); count != 1 || err != nil {
		t.Fatal("global capacity exceeded", count, err)
	}
}
