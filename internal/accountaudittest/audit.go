// Package accountaudittest exercises domain-qualified audit on both native DBs.
package accountaudittest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
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
		{"domain_keys_and_reopen", domainKeys},
		{"rollback_and_storage_failures", rollback},
		{"coherent_snapshot", coherentSnapshot},
		{"invalid_rows_and_scopes", invalidRows},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reopen := open(t)
			loaded, _, err := definition.Load(audit.MigrationSources()...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			if _, err := accounts.UserObjects.Using(backend).Exists(t.Context()); err == nil {
				t.Fatal("audit migration installed a hidden account")
			}
			test.run(t, backend, reopen)
		})
	}
}

var instant = time.Date(2026, 10, 24, 2, 3, 4, 567890000, time.UTC)

const target = "people.member"

func actor(t *testing.T, domainByte string) auth.Principal {
	t.Helper()
	domain, err := auth.NewCredentialDomain(strings.Repeat(domainByte, 64))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "reused-principal", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(domain, "same-login", "!same-unusable-marker", principal)
	if err != nil {
		t.Fatal(err)
	}
	return credential.Principal()
}

func event(t *testing.T, principal auth.Principal, model string, id int64, field string) audit.Event {
	t.Helper()
	fields := []string{field}
	prepared, err := audit.Prepare(principal, model, id, admin.ActionChange, fields, instant)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = "mutated_input"
	return prepared
}

func appendEvent(t *testing.T, backend Backend, prepared audit.Event) audit.Entry {
	t.Helper()
	var entry audit.Entry
	if err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
		var err error
		entry, err = audit.AppendIn(t.Context(), session, prepared)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return entry
}

func history(t *testing.T, backend Backend, principal auth.Principal, model string, id int64, limit int) []audit.Entry {
	t.Helper()
	domain, _ := principal.CredentialDomain()
	var entries []audit.Entry
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		var err error
		entries, err = audit.HistoryIn(t.Context(), reader, domain, model, id, limit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}

func domainKeys(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first, second := actor(t, "a"), actor(t, "b")
	var all []audit.Entry
	for _, key := range []int64{0, -17, 1} {
		all = append(all, appendEvent(t, backend, event(t, first, target, key, "region")))
	}
	other := appendEvent(t, backend, event(t, second, target, 0, "timezone"))
	appendEvent(t, backend, event(t, first, "other.member", 0, "label"))
	for index, key := range []int64{0, -17, 1} {
		got := history(t, reopen(t), first, target, key, 100)
		if len(got) != 1 || got[0].Sequence() != all[index].Sequence() || got[0].ActorID() != first.ID() ||
			got[0].Model() != target || got[0].ObjectID() != key || !got[0].At().Equal(instant) ||
			got[0].Action() != admin.ActionChange || !reflect.DeepEqual(got[0].ChangedFields(), []string{"region"}) {
			t.Fatal("domain/model/key history differs after reopen", index, got)
		}
		got[0].ChangedFields()[0] = "mutated_output"
	}
	if got := history(t, backend, second, target, 0, 100); len(got) != 1 || got[0].Sequence() != other.Sequence() || !reflect.DeepEqual(got[0].ChangedFields(), []string{"timezone"}) {
		t.Fatal("reused IDs inherited historical domain events", got)
	}
	newer := appendEvent(t, backend, event(t, first, target, 0, "region"))
	latest := appendEvent(t, backend, event(t, first, target, 0, "timezone"))
	got := history(t, backend, first, target, 0, 2)
	if len(got) != 2 || got[0].Sequence() != newer.Sequence() || got[1].Sequence() != latest.Sequence() {
		t.Fatal("history did not return newest bounded entries in ascending order", got)
	}
}

func rollback(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	principal := actor(t, "a")
	prepared := event(t, principal, target, 0, "region")
	fault := errors.New("private-storage-fault")
	err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
		if _, err := audit.AppendIn(t.Context(), session, prepared); err != nil {
			return err
		}
		return fault
	})
	if !errors.Is(err, fault) || len(history(t, reopen(t), principal, target, 0, 100)) != 0 {
		t.Fatal("callback rollback retained audit", err)
	}
	err = backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
		entry, err := audit.AppendIn(t.Context(), &faultSession{Session: session, insertFailure: fault}, prepared)
		if entry.Sequence() != 0 || !errors.Is(err, fault) || strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
			return errors.New("audit exposed a failed entry or private storage cause")
		}
		return err
	})
	if !errors.Is(err, fault) || len(history(t, reopen(t), principal, target, 0, 100)) != 0 {
		t.Fatal("insert failure did not roll back actual insert", err)
	}
	appendEvent(t, backend, prepared)
	domain, _ := principal.CredentialDomain()
	for _, phase := range []string{"query", "close"} {
		if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
			wrapped := &faultReader{Queryer: reader, failure: fault, close: phase == "close"}
			entries, err := audit.HistoryIn(t.Context(), wrapped, domain, target, 0, 100)
			if entries != nil || !errors.Is(err, fault) || strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
				return errors.New("failed read published entries or lost redaction")
			}
			return nil
		}); err != nil {
			t.Fatal(phase, err)
		}
	}
}

func coherentSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	principal := actor(t, "a")
	domain, _ := principal.CredentialDomain()
	first := appendEvent(t, backend, event(t, principal, target, 0, "region"))
	writer := reopen(t)
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		before, err := audit.HistoryIn(t.Context(), reader, domain, target, 0, 100)
		if err != nil || len(before) != 1 || before[0].Sequence() != first.Sequence() {
			return errors.Join(err, errors.New("initial history snapshot differs"))
		}
		appendEvent(t, writer, event(t, principal, target, 0, "timezone"))
		after, err := audit.HistoryIn(t.Context(), reader, domain, target, 0, 100)
		if err != nil || len(after) != 1 || after[0].Sequence() != before[0].Sequence() {
			return errors.Join(err, errors.New("history mixed committed snapshots"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(history(t, backend, principal, target, 0, 100)) != 2 {
		t.Fatal("fresh history retained old snapshot")
	}
}

func invalidRows(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	principal := actor(t, "a")
	domain, _ := principal.CredentialDomain()
	prepared := event(t, principal, target, 0, "region")
	if entry, err := audit.AppendIn(t.Context(), backend, prepared); !errors.Is(err, audit.ErrInvalid) || entry.Sequence() != 0 {
		t.Fatal("root write backend accepted", err)
	}
	if entries, err := audit.HistoryIn(t.Context(), backend, domain, target, 0, 1); !errors.Is(err, audit.ErrInvalid) || entries != nil {
		t.Fatal("root read backend accepted", err)
	}
	var expired db.Session
	if err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error { expired = session; return nil }); err != nil {
		t.Fatal(err)
	}
	if entry, err := audit.AppendIn(t.Context(), expired, prepared); err == nil || entry.Sequence() != 0 {
		t.Fatal("expired write accepted", err)
	}
	if entries, err := audit.HistoryIn(t.Context(), expired, domain, target, 0, 1); err == nil || entries != nil {
		t.Fatal("expired history accepted", err)
	}
	entry := appendEvent(t, backend, prepared)
	row, found, err := models.EventObjects.Using(backend).Filter(models.EventFields.ID.Exact(entry.Sequence())).OrderBy(models.EventFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	for _, patch := range []models.EventPatch{
		(models.EventPatch{}).WithChangedFields(`null`),
		(models.EventPatch{}).WithChangedFields(`["region", "region"]`),
		(models.EventPatch{}).WithChangedFields(`["region",1]`),
		(models.EventPatch{}).WithChangedFields(`[ "region" ]`),
		(models.EventPatch{}).WithActorID("bad\nactor"),
		(models.EventPatch{}).WithAction("unknown"),
	} {
		bad, err := models.EventObjects.Patch(t.Context(), backend, row, patch)
		if err != nil {
			t.Fatal(err)
		}
		err = backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
			entries, err := audit.HistoryIn(t.Context(), reader, domain, target, 0, 100)
			if entries != nil || !errors.Is(err, audit.ErrCorrupt) {
				return errors.New("corrupt stored audit accepted")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		row, err = models.EventObjects.Patch(t.Context(), backend, bad, (models.EventPatch{}).WithChangedFields(`["region"]`).WithActorID(principal.ID()).WithAction("change"))
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		cancel()
		if entries, err := audit.HistoryIn(ctx, reader, domain, target, 0, 100); !errors.Is(err, context.Canceled) || entries != nil {
			return errors.New("canceled audit history accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type faultSession struct {
	db.Session
	insertFailure error
}

func (session *faultSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *faultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if _, err := session.Session.Insert(ctx, plan); err != nil {
		return 0, err
	}
	return 0, session.insertFailure
}

type faultReader struct {
	db.Queryer
	failure error
	close   bool
}

func (reader *faultReader) ValidateSession(ctx context.Context) error {
	return reader.Queryer.(db.SessionValidator).ValidateSession(ctx)
}
func (reader *faultReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if !reader.close {
		return nil, reader.failure
	}
	rows, err := reader.Queryer.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	return &faultRows{Rows: rows, failure: reader.failure}, nil
}

type faultRows struct {
	db.Rows
	failure error
}

func (rows *faultRows) Close() error { return errors.Join(rows.Rows.Close(), rows.failure) }
