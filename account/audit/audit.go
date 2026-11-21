// Package audit persists semantic account-management events under the exact
// credential domain that owns both actor and target. It never authenticates a
// caller or grants history access. Owners authorize and mutate in the same
// borrowed scope, and publish results only after confirmed cleanup or commit.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

var (
	ErrInvalid = errors.New("account audit: invalid event, history selection or scope")
	ErrCorrupt = errors.New("account audit: stored event is invalid")
)

// Event has no field values, password, session material or display label. Its
// actor and target belong to one explicit account domain. An event is semantic
// data, not a credential or proof that its actor currently has permission.
type Event struct{ state *eventState }

type eventState struct {
	domain auth.CredentialDomain
	data   admin.PreparedEvent
	at     time.Time
}

func (Event) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("account.audit.Event{redacted}"))
}

// Prepare uses the CURRENT actor returned by scoped account authorization.
// The owner chooses at on the server. Event time is normalized to UTC and the
// shared database microsecond precision. Integer object keys include 0 and
// negative values; they are never used as missing-object sentinels.
func Prepare(actor auth.Principal, model string, objectID int64, action admin.Action, changedFields []string, at time.Time) (Event, error) {
	domain, bound := actor.CredentialDomain()
	if !actor.Authenticated() || !bound {
		return Event{}, ErrInvalid
	}
	return prepare(domain, actor.ID(), model, objectID, action, changedFields, at)
}

func prepare(domain auth.CredentialDomain, actor, model string, objectID int64, action admin.Action, fields []string, at time.Time) (Event, error) {
	at = at.UTC().Truncate(time.Microsecond)
	if domain.Identifier() == "" || at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return Event{}, ErrInvalid
	}
	data, err := admin.PrepareEvent(actor, model, objectID, action, fields, "")
	if err != nil {
		return Event{}, ErrInvalid
	}
	return Event{state: &eventState{domain: domain, data: data, at: at}}, nil
}

// Entry is a detached durable-row observation. AppendIn's result is provisional
// until its caller confirms commit; a sequence alone is not a command receipt.
type Entry struct {
	event Event
	id    int64
}

func (Entry) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("account.audit.Entry{redacted}"))
}
func (entry Entry) Sequence() int64 { return entry.id }
func (entry Entry) Domain() auth.CredentialDomain {
	if entry.event.state == nil {
		return auth.CredentialDomain{}
	}
	return entry.event.state.domain
}
func (entry Entry) ActorID() string         { return entry.data().ActorID() }
func (entry Entry) Model() string           { return entry.data().Model() }
func (entry Entry) ObjectID() int64         { return entry.data().ObjectID() }
func (entry Entry) Action() admin.Action    { return entry.data().Action() }
func (entry Entry) ChangedFields() []string { return entry.data().ChangedFields() }
func (entry Entry) At() time.Time {
	if entry.event.state == nil {
		return time.Time{}
	}
	return entry.event.state.at
}
func (entry Entry) data() admin.PreparedEvent {
	if entry.event.state == nil {
		return admin.PreparedEvent{}
	}
	return entry.event.state.data
}

// AppendIn must share the authorized target mutation's coordinated transaction.
// It neither opens a nested transaction nor prunes or rewrites previous events.
// Failure must be returned to the scope owner. Unknown outcomes are not retried.
func AppendIn(ctx context.Context, session db.Session, event Event) (Entry, error) {
	if event.state == nil {
		return Entry{}, ErrInvalid
	}
	if err := checkScope(ctx, session); err != nil {
		return Entry{}, err
	}
	data := event.state.data
	fields := data.ChangedFields()
	if fields == nil {
		fields = []string{}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return Entry{}, ErrInvalid
	}
	row, err := models.EventObjects.Create(ctx, session, models.NewEventCreate(
		event.state.domain.Identifier(), data.ActorID(), data.Model(), data.ObjectID(),
		string(data.Action()), string(encoded), event.state.at,
	))
	if err = errors.Join(err, checkScope(ctx, session)); err != nil {
		return Entry{}, failure(err)
	}
	return fromRow(row)
}

// HistoryIn returns the newest bounded selection in ascending event order.
// Domain is mandatory, even when model, principal and row IDs are unchanged.
// A caller may deliberately inspect a historical domain after authorizing it;
// this function never substitutes the current domain or combines generations.
func HistoryIn(ctx context.Context, reader db.Queryer, domain auth.CredentialDomain, model string, objectID int64, limit int) ([]Entry, error) {
	if domain.Identifier() == "" || limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, ErrInvalid
	}
	if _, err := admin.PrepareEvent("account-audit-history", model, objectID, admin.ActionChange, nil, ""); err != nil {
		return nil, ErrInvalid
	}
	if err := checkScope(ctx, reader); err != nil {
		return nil, err
	}
	selected, err := models.EventObjects.Using(reader).Filter(
		models.EventFields.Domain.Exact(domain.Identifier()), models.EventFields.Model.Exact(model),
		models.EventFields.ObjectID.Exact(objectID),
	).OrderBy(models.EventFields.ID.Desc()).Limit(limit)
	if err != nil {
		return nil, failure(err)
	}
	rows, err := selected.All(ctx)
	if err = errors.Join(err, checkScope(ctx, reader)); err != nil {
		return nil, failure(err)
	}
	if len(rows) > limit {
		return nil, ErrCorrupt
	}
	result := make([]Entry, len(rows))
	for index, row := range rows {
		if row.Domain != domain.Identifier() || row.Model != model || row.ObjectID != objectID || index > 0 && row.ID >= rows[index-1].ID {
			return nil, ErrCorrupt
		}
		entry, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		result[len(rows)-index-1] = entry
	}
	return result, nil
}

func fromRow(row models.Event) (Entry, error) {
	domain, err := auth.NewCredentialDomain(row.Domain)
	if err != nil || row.ID <= 0 || len(row.ChangedFields) > 32768 || row.RecordedAt.Nanosecond()%1000 != 0 {
		return Entry{}, ErrCorrupt
	}
	var fields []string
	if err := json.Unmarshal([]byte(row.ChangedFields), &fields); err != nil || fields == nil {
		return Entry{}, ErrCorrupt
	}
	encoded, err := json.Marshal(fields)
	if err != nil || string(encoded) != row.ChangedFields {
		return Entry{}, ErrCorrupt
	}
	event, err := prepare(domain, row.ActorID, row.Model, row.ObjectID, admin.Action(row.Action), fields, row.RecordedAt)
	if err != nil {
		return Entry{}, ErrCorrupt
	}
	return Entry{event: event, id: row.ID}, nil
}

func checkScope(ctx context.Context, reader db.Queryer) error {
	if isNil(ctx) || isNil(reader) {
		return ErrInvalid
	}
	validator, ok := reader.(db.SessionValidator)
	if !ok || isNil(validator) {
		return ErrInvalid
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return failure(err)
	}
	return nil
}
func isNil(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}

type storageError struct{ cause error }

func (storageError) Error() string                    { return "account audit: storage operation failed" }
func (e storageError) Unwrap() error                  { return e.cause }
func (e storageError) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(e.Error())) }
func failure(err error) error                         { return storageError{cause: err} }
