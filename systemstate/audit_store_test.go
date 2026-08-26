package systemstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

func TestRuntimeAuditHistoryPersistsPrunesAndContinuesSequenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "durable-audit.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	firstBackend := openSessionStoreBackend(t, ctx, dataSourceName)
	explicitlyMigrateSystemState(t, ctx, firstBackend)
	firstRuntime := mustAuditRuntime(t, ctx, firstBackend, 3)

	first := mustPreparedAuditEvent(t, "operator", 7, admin.ActionAdd, nil, "First")
	secondChanged := []string{"title"}
	second := mustPreparedAuditEvent(t, "operator", 7, admin.ActionChange, secondChanged, "Second")
	secondChanged[0] = "forged"
	third := mustPreparedAuditEvent(t, "publisher", 7, admin.ActionPublish, []string{"published"}, "Second")
	fourth := mustPreparedAuditEvent(t, "operator", 8, admin.ActionDelete, nil, "Other")
	for _, event := range []admin.PreparedEvent{first, second, third, fourth} {
		if err := appendRuntimeAudit(ctx, firstRuntime, event); err != nil {
			t.Fatalf("append audit event: %v", err)
		}
	}
	// Capacity is global. The first sequence was pruned after four confirmed
	// appends, while object 7 retains its two newest events in ascending order.
	history, err := firstRuntime.AuditHistory(ctx, "godj_conformance.article", 7, 3)
	if err != nil {
		t.Fatalf("AuditHistory(first runtime): %v", err)
	}
	if got := auditSequences(history); !reflect.DeepEqual(got, []uint64{2, 3}) {
		t.Fatalf("first runtime sequences = %v, want [2 3]", got)
	}
	if !reflect.DeepEqual(history[0].ChangedFields, []string{"title"}) ||
		!reflect.DeepEqual(history[1].ChangedFields, []string{"published"}) {
		t.Fatalf("first runtime changed fields = %#v", history)
	}
	history[0].ChangedFields[0] = "mutated"
	again, err := firstRuntime.AuditHistory(ctx, "godj_conformance.article", 7, 3)
	if err != nil || !reflect.DeepEqual(again[0].ChangedFields, []string{"title"}) {
		t.Fatalf("detached history = (%#v,%v)", again, err)
	}
	if err := firstBackend.Close(); err != nil {
		t.Fatalf("close first audit backend: %v", err)
	}

	secondBackend := openSessionStoreBackend(t, ctx, dataSourceName)
	secondRuntime := mustAuditRuntime(t, ctx, secondBackend, 3)
	restarted, err := secondRuntime.AuditHistory(ctx, "godj_conformance.article", 7, 3)
	if err != nil || !reflect.DeepEqual(auditSequences(restarted), []uint64{2, 3}) {
		_ = secondBackend.Close()
		t.Fatalf("restarted history = (%#v,%v)", restarted, err)
	}
	fifth := mustPreparedAuditEvent(t, "operator", 7, admin.ActionChange, []string{"summary"}, "Final")
	if err := appendRuntimeAudit(ctx, secondRuntime, fifth); err != nil {
		_ = secondBackend.Close()
		t.Fatalf("append after restart: %v", err)
	}
	continued, err := secondRuntime.AuditHistory(ctx, "godj_conformance.article", 7, 3)
	if err != nil || !reflect.DeepEqual(auditSequences(continued), []uint64{3, 5}) {
		_ = secondBackend.Close()
		t.Fatalf("continued history = (%#v,%v), want sequences [3 5]", continued, err)
	}
	if err := secondBackend.Close(); err != nil {
		t.Fatalf("close second audit backend: %v", err)
	}
}

func TestRuntimeAuditAppendRollbackAndPruneFailureAreAtomic(t *testing.T) {
	ctx := context.Background()
	delegate := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "audit-rollback.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = delegate.Close() })
	explicitlyMigrateSystemState(t, ctx, delegate)
	backend := &auditFaultBackend{Backend: delegate}
	runtime := mustAuditRuntime(t, ctx, backend, 2)

	callbackFailure := errors.New("injected article mutation failure")
	event := mustPreparedAuditEvent(t, "operator", 1, admin.ActionAdd, nil, "Article")
	err := runtime.Atomic(ctx, func(session db.Session) error {
		if err := runtime.AppendAudit(ctx, session, event); err != nil {
			return err
		}
		return callbackFailure
	})
	if !errors.Is(err, callbackFailure) {
		t.Fatalf("Atomic(callback failure) error = %v", err)
	}
	if history, err := runtime.AuditHistory(ctx, "godj_conformance.article", 1, 2); err != nil || len(history) != 0 {
		t.Fatalf("history after callback rollback = (%#v,%v)", history, err)
	}

	for index := int64(1); index <= 2; index++ {
		if err := appendRuntimeAudit(ctx, runtime, mustPreparedAuditEvent(t, "operator", index, admin.ActionAdd, nil, fmt.Sprintf("Article %d", index))); err != nil {
			t.Fatalf("seed audit %d: %v", index, err)
		}
	}
	pruneFailure := errors.New("injected audit prune delete failure")
	backend.armDeleteFailure(pruneFailure)
	third := mustPreparedAuditEvent(t, "operator", 3, admin.ActionAdd, nil, "Article 3")
	if err := appendRuntimeAudit(ctx, runtime, third); !errors.Is(err, pruneFailure) {
		t.Fatalf("append with prune failure error = %v", err)
	}
	rows, err := inspectAllAuditRows(ctx, delegate, 3)
	if err != nil || len(rows) != 2 || rows[0].id != 1 || rows[1].id != 2 {
		t.Fatalf("rows after prune rollback = (%#v,%v), want sequences [1 2]", rows, err)
	}
}

func TestRuntimeOpenRejectsCorruptAndOverCapacityAuditWithoutMutation(t *testing.T) {
	ctx := context.Background()
	t.Run("corrupt later row", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "audit-corrupt.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		runtime := mustAuditRuntime(t, ctx, backend, 4)
		if err := appendRuntimeAudit(ctx, runtime, mustPreparedAuditEvent(t, "operator", 1, admin.ActionAdd, nil, "Valid")); err != nil {
			t.Fatalf("append valid seed: %v", err)
		}
		if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
			auditTableName,
			[]query.Assignment{
				query.NewAssignment(auditActorIDRef, query.String("operator")),
				query.NewAssignment(auditModelRef, query.String("godj_conformance.article")),
				query.NewAssignment(auditObjectIDRef, query.String("2")),
				query.NewAssignment(auditActionRef, query.String(string(admin.ActionAdd))),
				query.NewAssignment(auditChangedFieldsRef, query.String("v9.corrupt-marker")),
				query.NewAssignment(auditDisplayLabelRef, query.String("Corrupt")),
			},
			auditIDRef,
		)); err != nil {
			t.Fatalf("insert corrupt audit seed: %v", err)
		}
		before, err := inspectAllAuditRows(ctx, backend, 5)
		if err != nil {
			t.Fatalf("inspect corrupt seeds: %v", err)
		}
		opened, err := Open(ctx, backend, auditBootstrapConfig(t, 4))
		if err == nil || opened != nil {
			t.Fatalf("Open(corrupt audit) = (%v,%v), want nil/error", opened, err)
		}
		var classified *Error
		if !errors.As(err, &classified) || classified.Code != CodeCorruptState || strings.Contains(err.Error(), "corrupt-marker") {
			t.Fatalf("Open(corrupt audit) error = %#v", err)
		}
		after, inspectErr := inspectAllAuditRows(ctx, backend, 5)
		if inspectErr != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("Open(corrupt audit) mutated rows: before=%#v after=%#v err=%v", before, after, inspectErr)
		}
	})

	t.Run("over capacity", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "audit-over-capacity.sqlite3"))+"?mode=rwc")
		t.Cleanup(func() { _ = backend.Close() })
		explicitlyMigrateSystemState(t, ctx, backend)
		runtime := mustAuditRuntime(t, ctx, backend, 3)
		for index := int64(1); index <= 3; index++ {
			if err := appendRuntimeAudit(ctx, runtime, mustPreparedAuditEvent(t, "operator", index, admin.ActionAdd, nil, "Article")); err != nil {
				t.Fatalf("append seed %d: %v", index, err)
			}
		}
		// A non-cooperating writer exceeds the configured bound.
		fields, _ := encodeAuditChangedFields(nil)
		if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
			auditTableName,
			[]query.Assignment{
				query.NewAssignment(auditActorIDRef, query.String("operator")),
				query.NewAssignment(auditModelRef, query.String("godj_conformance.article")),
				query.NewAssignment(auditObjectIDRef, query.String("4")),
				query.NewAssignment(auditActionRef, query.String(string(admin.ActionAdd))),
				query.NewAssignment(auditChangedFieldsRef, query.String(fields)),
				query.NewAssignment(auditDisplayLabelRef, query.String("Article")),
			},
			auditIDRef,
		)); err != nil {
			t.Fatalf("insert over-capacity seed: %v", err)
		}
		opened, err := Open(ctx, backend, auditBootstrapConfig(t, 3))
		if err == nil || opened != nil {
			t.Fatalf("Open(over-capacity) = (%v,%v), want nil/error", opened, err)
		}
		var classified *Error
		if !errors.As(err, &classified) || classified.Code != CodeCardinality {
			t.Fatalf("Open(over-capacity) error = %#v", err)
		}
	})
}

func TestRuntimeAuditHistoryValidatesRequestAndPreparedEvent(t *testing.T) {
	ctx := context.Background()
	backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "audit-validation.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)
	runtime := mustAuditRuntime(t, ctx, backend, 2)
	if err := runtime.Atomic(ctx, func(session db.Session) error {
		return runtime.AppendAudit(ctx, session, admin.PreparedEvent{})
	}); err == nil {
		t.Fatal("AppendAudit(zero event) error = nil")
	}
	for _, request := range []struct {
		model string
		id    int64
		limit int
	}{
		{model: "bad model", id: 1, limit: 1},
		{model: "godj_conformance.article", id: 0, limit: 1},
		{model: "godj_conformance.article", id: 1, limit: 0},
		{model: "godj_conformance.article", id: 1, limit: admin.MaximumHistoryEntries + 1},
	} {
		if entries, err := runtime.AuditHistory(ctx, request.model, request.id, request.limit); err == nil || entries != nil {
			t.Fatalf("AuditHistory(%q,%d,%d) = (%v,%v), want nil/error", request.model, request.id, request.limit, entries, err)
		}
	}
}

type auditFaultBackend struct {
	*sqlite.Backend
	mu                sync.Mutex
	nextDeleteFailure error
}

func (backend *auditFaultBackend) armDeleteFailure(err error) {
	backend.mu.Lock()
	backend.nextDeleteFailure = err
	backend.mu.Unlock()
}

func (backend *auditFaultBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.mu.Lock()
	failure := backend.nextDeleteFailure
	backend.nextDeleteFailure = nil
	backend.mu.Unlock()
	return backend.Backend.Atomic(ctx, func(session db.Session) error {
		return callback(&auditFaultSession{Session: session, deleteFailure: failure})
	})
}

func (backend *auditFaultBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.mu.Lock()
	failure := backend.nextDeleteFailure
	backend.nextDeleteFailure = nil
	backend.mu.Unlock()
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		return callback(&auditFaultSession{Session: session, deleteFailure: failure})
	})
}

type auditFaultSession struct {
	db.Session
	deleteFailure error
}

func (session *auditFaultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if session.deleteFailure != nil && plan.Table() == auditTableName {
		err := session.deleteFailure
		session.deleteFailure = nil
		return 0, err
	}
	return session.Session.Delete(ctx, plan)
}

func mustAuditRuntime(t *testing.T, ctx context.Context, backend Backend, capacity int) *Runtime {
	t.Helper()
	runtime, err := Open(ctx, backend, auditBootstrapConfig(t, capacity))
	if err != nil {
		t.Fatalf("systemstate.Open(): %v", err)
	}
	return runtime
}

func auditBootstrapConfig(t *testing.T, capacity int) BootstrapConfig {
	t.Helper()
	permission, err := auth.NewPermission("admin.site.access")
	if err != nil {
		t.Fatalf("auth.NewPermission(): %v", err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
	})
	if err != nil {
		t.Fatalf("auth.NewPBKDF2(): %v", err)
	}
	return BootstrapConfig{
		Username:       "admin",
		Password:       "restart-secret-password",
		PrincipalID:    "admin-principal",
		Active:         true,
		Permissions:    []auth.Permission{permission},
		PasswordHasher: hasher,
		MaxSessions:    8,
		AuditCapacity:  capacity,
	}
}

func mustPreparedAuditEvent(
	t *testing.T,
	actor string,
	objectID int64,
	action admin.Action,
	changed []string,
	display string,
) admin.PreparedEvent {
	t.Helper()
	event, err := admin.PrepareEvent(actor, "godj_conformance.article", objectID, action, changed, display)
	if err != nil {
		t.Fatalf("admin.PrepareEvent(): %v", err)
	}
	return event
}

func appendRuntimeAudit(ctx context.Context, runtime *Runtime, event admin.PreparedEvent) error {
	return runtime.Atomic(ctx, func(session db.Session) error {
		return runtime.AppendAudit(ctx, session, event)
	})
}

func auditSequences(entries []admin.AuditEntry) []uint64 {
	result := make([]uint64, len(entries))
	for index := range entries {
		result[index] = entries[index].Sequence
	}
	return result
}

func inspectAllAuditRows(ctx context.Context, queryer db.Queryer, limit int) ([]persistedAuditRow, error) {
	return queryAuditRows(ctx, queryer, "", 0, limit, query.Ascending)
}

var _ migrationbackend.AppliedMigrationReader = (*auditFaultBackend)(nil)
