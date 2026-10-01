package helpdesk

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

type payloadLockBackend struct {
	Backend
	written chan struct{}
	resume  <-chan struct{}
}
type payloadLockSession struct {
	db.RelationSession
	owner     *payloadLockBackend
	validator db.SessionValidator
	conflict  db.ConflictInserter
}

func (b *payloadLockBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	return b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		validator, ok := session.(db.SessionValidator)
		conflict, capable := session.(db.ConflictInserter)
		if !ok || !capable {
			return errors.New("native binary concurrency session capabilities missing")
		}
		return callback(payloadLockSession{session, b, validator, conflict})
	})
}
func (s payloadLockSession) ValidateSession(ctx context.Context) error {
	return s.validator.ValidateSession(ctx)
}
func (s payloadLockSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	return s.conflict.InsertOnConflict(ctx, plan)
}
func (s payloadLockSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	rows, err := s.RelationSession.Update(ctx, plan)
	if err != nil {
		return rows, err
	}
	for _, assignment := range plan.Assignments() {
		if assignment.Field().Name() == "subject" {
			close(s.owner.written)
			select {
			case <-s.owner.resume:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	}
	return rows, nil
}

// The two Applications deliberately use the native backend directly, without
// systemstate's wider cooperative fence. Observe PostgreSQL's actual lock wait,
// then prove both publications and the final row identify their stored payload.
func verifyPayloadDigestPostgresConcurrency(t *testing.T, b formSaveDatabase, observer *pgx.Conn, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	category, err := models.CategoryObjects.Create(ctx, b, models.NewCategoryCreate("Binary concurrent"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := jsonvalue.Parse([]byte(`{"a":1e0}`))
	if err != nil {
		t.Fatal(err)
	}
	after, err := jsonvalue.Parse([]byte(`{"a":2e0}`))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := models.TicketObjects.Create(ctx, b, models.NewTicketCreate("Binary initial", category.ID).WithExternalPayload(before))
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "binary-writer", Active: true, Permissions: []auth.Permission{ChangeTicket, ViewLabel}})
	if err != nil {
		t.Fatal(err)
	}
	resume := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	wrapper := &payloadLockBackend{Backend: b, written: make(chan struct{}), resume: resume}
	first, err := New(wrapper, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(b, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		row ticketRecord
		err error
	}
	left, right := make(chan result, 1), make(chan result, 1)
	var workers sync.WaitGroup
	defer func() { release(); cancel(); workers.Wait() }()
	workers.Add(1)
	go func() {
		defer workers.Done()
		row, _, err := first.updatePatch(ctx, actor, initial.ID, models.TicketPatch{}.WithSubject("Binary locked"), nil)
		left <- result{row, err}
	}()
	select {
	case <-wrapper.written:
	case answer := <-left:
		t.Fatal("first binary writer did not acquire row", answer.err)
	case <-ctx.Done():
		t.Fatal("first binary writer timed out")
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		row, _, err := second.updatePatch(ctx, actor, initial.ID, models.TicketPatch{}.WithExternalPayload(after), nil)
		right <- result{row, err}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := observer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid <> pg_catalog.pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1 AND pg_catalog.cardinality(pg_catalog.pg_blocking_pids(pid)) > 0)`, "%"+pgx.Identifier{schema, "helpdesk_ticket"}.Sanitize()+"%").Scan(&blocked)
		if err != nil {
			t.Fatal("observe binary PostgreSQL lock wait")
		}
		if blocked {
			break
		}
		select {
		case answer := <-right:
			t.Fatal("concurrent writer escaped held row", answer.err)
		case <-ctx.Done():
			t.Fatal("native binary row lock was not observed")
		case <-ticker.C:
		}
	}
	release()
	receive := func(ch <-chan result) ticketRecord {
		t.Helper()
		select {
		case answer := <-ch:
			if answer.err != nil {
				t.Fatal(answer.err)
			}
			return answer.row
		case <-ctx.Done():
			t.Fatal("binary writers did not finish")
			return ticketRecord{}
		}
	}
	firstRow, secondRow := receive(left), receive(right)
	check := func(row models.Ticket, want string) {
		t.Helper()
		if row.Subject != "Binary locked" || row.ExternalPayload == nil || row.ExternalPayload.Text != want || row.ExternalPayloadDigest == nil {
			t.Fatal("binary concurrent publication changed row")
		}
		sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
		if row.ExternalPayloadDigest.Data != string(sum[:]) {
			t.Fatal("binary digest used another transaction's JSON")
		}
	}
	check(firstRow.Ticket, `{"a":1}`)
	check(secondRow.Ticket, `{"a":2}`)
	stored, found, err := models.TicketObjects.Using(b).Filter(models.TicketFields.ID.Exact(initial.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	check(stored, `{"a":2}`)
}
