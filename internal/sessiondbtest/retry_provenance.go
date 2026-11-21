package sessiondbtest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/sessions/database/models"
)

type retryBackend struct {
	Backend
	mode             string
	fault            error
	calls, faults    int
	inserts, deletes int64
}

func (owner *retryBackend) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	owner.calls++
	err := owner.Backend.CoordinatedAtomic(ctx, func(scope db.Session) error {
		return write(&retryScope{Session: scope, owner: owner})
	})
	if owner.mode == "collision_cleanup" {
		return errors.Join(err, owner.fault)
	}
	if owner.mode == "collision_unknown" {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Cause: owner.fault})
	}
	return err
}

type retryScope struct {
	db.Session
	owner *retryBackend
}

func (scope *retryScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *retryScope) fail() error { scope.owner.faults++; return scope.owner.fault }
func (scope *retryScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if scope.owner.mode == "query" {
		return nil, scope.fail()
	}
	return scope.Session.Query(ctx, plan)
}
func (scope *retryScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if scope.owner.mode == "before_insert" {
		return 0, scope.fail()
	}
	id, err := scope.Session.Insert(ctx, plan)
	if err == nil {
		if id <= 0 {
			return 0, errors.New("retry probe did not allocate a native row")
		}
		scope.owner.inserts++
		if scope.owner.mode == "after_insert" {
			return 0, scope.fail()
		}
	}
	return id, err
}
func (scope *retryScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.Session.Delete(ctx, plan)
	if err == nil {
		if count != 1 {
			return 0, errors.New("retry probe did not delete exactly one native row")
		}
		scope.owner.deletes += count
		if scope.owner.mode == "after_delete" {
			return 0, scope.fail()
		}
	}
	return count, err
}

func retryRows(t *testing.T, backend Backend) []models.Session {
	t.Helper()
	rows, err := models.SessionObjects.Using(backend).OrderBy(models.SessionFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// A backend can return any error type. Only the store's actual collision
// observation, followed by confirmed rollback, may replay a manager command.
func retryProvenance(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	for _, operation := range []string{"rotate", "replace"} {
		t.Run(operation, func(t *testing.T) {
			for _, mode := range []string{"query", "before_insert", "after_insert", "after_delete", "collision_cleanup", "collision_unknown"} {
				t.Run(mode, func(t *testing.T) {
					namespace := operation + "-" + mode
					base := store(t, backend, namespace, sessiondb.Config{})
					previous := record(t, 1, map[string]string{"private": "preserved"}, instant)
					blocker := record(t, 2, map[string]string{"collision": "preserved"}, instant)
					create(t, base, previous)
					create(t, base, blocker)
					candidate := record(t, 3, nil, instant)
					collision := strings.HasPrefix(mode, "collision_")
					if collision {
						candidate = blocker
					}
					entropy, err := base64.RawURLEncoding.DecodeString(candidate.ID().Encoded())
					if err != nil {
						t.Fatal(err)
					}
					tail, err := base64.RawURLEncoding.DecodeString(record(t, 4, nil, instant).ID().Encoded())
					if err != nil {
						t.Fatal(err)
					}
					source := bytes.NewReader(append(entropy, tail...))
					const private = "private adapter entropy-shaped failure"
					var fault error = &sessions.Error{Code: sessions.CodeEntropy, Detail: private}
					if collision {
						fault = errors.New(private)
					}
					owner := &retryBackend{Backend: backend, mode: mode, fault: fault}
					manager, err := sessions.NewManager(store(t, owner, namespace, sessiondb.Config{}), sessions.Config{Clock: func() time.Time { return instant.Add(time.Second) }, Random: source})
					if err != nil {
						t.Fatal(err)
					}
					before := retryRows(t, reopen(t))
					var result sessions.Record
					if operation == "rotate" {
						result, err = manager.Rotate(t.Context(), previous)
					} else {
						result, err = manager.Replace(t.Context(), previous, map[string]string{"owner": "new"})
					}
					classified, ok := err.(*sessions.Error)
					if !ok || classified.Code != sessions.CodeStoreFailure || !errors.Is(err, fault) || !errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) || strings.Contains(fmt.Sprintf("%+v", err), private) || !reflect.DeepEqual(result, sessions.Record{}) {
						t.Fatal("native failure became a public collision or lost its cause", err)
					}
					if owner.calls != 1 || source.Len() != 32 || owner.faults != 1 && !collision || collision && owner.faults != 0 {
						t.Fatal("unconfirmed replacement retried or missed its error boundary")
					}
					var inserts, deletes int64
					if mode == "before_insert" || mode == "after_insert" || mode == "after_delete" {
						deletes = 1
					}
					if mode == "after_insert" {
						inserts = 1
					}
					if owner.inserts != inserts || owner.deletes != deletes {
						t.Fatal("native failure did not follow the expected write prefix")
					}
					if mode == "collision_unknown" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
						t.Fatal("collision outcome uncertainty was lost", err)
					}
					if !reflect.DeepEqual(before, retryRows(t, reopen(t))) {
						t.Fatal("unconfirmed replacement changed native session rows")
					}
					requireRecord(t, base, previous, true)
					requireRecord(t, base, blocker, true)
				})
			}
		})
	}
}
