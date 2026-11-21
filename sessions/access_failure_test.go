package sessions_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

type failedPolicyCompletionStore struct {
	sessions.Store
	mode  string
	fault error
}

func (store failedPolicyCompletionStore) Access(ctx context.Context, id sessions.ID, policy sessions.AccessPolicy) (sessions.Record, sessions.AccessStatus, error) {
	current, present, err := store.Store.Load(ctx, id)
	if err != nil || !present {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	_, _, err = policy.Apply(id, current)
	switch store.mode {
	case "wrapped":
		err = fmt.Errorf("private completion wrapper: %w", err)
	case "joined", "unknown", "canceled":
		err = errors.Join(err, store.fault)
	}
	return sessions.Record{}, sessions.AccessMissing, err
}

func TestManagerAccessPolicyCannotHideStorageCompletionFailure(t *testing.T) {
	for _, mode := range []string{"direct", "wrapped", "joined", "unknown", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(4)
			if err != nil {
				t.Fatal(err)
			}
			clock := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return clock }})
			if err != nil {
				t.Fatal(err)
			}
			record, err := manager.Create(t.Context(), map[string]string{"secret": "private payload"})
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("private cleanup cause")
			if mode == "unknown" {
				fault = &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown}
			}
			if mode == "canceled" {
				fault = context.Canceled
			}
			reader, err := manager.WithStore(failedPolicyCompletionStore{Store: store, mode: mode, fault: fault})
			if err != nil {
				t.Fatal(err)
			}
			clock = time.Time{}
			value, found, err := reader.Load(t.Context(), record.ID())
			var classified *sessions.Error
			if found || value.ID().Valid() || !errors.As(err, &classified) {
				t.Fatal("failed access published a session", err)
			}
			if mode == "direct" {
				if classified.Code != sessions.CodeInvalidConfig || classified.Field != "clock" {
					t.Fatal("direct policy rejection lost authority", err)
				}
			} else {
				if classified.Code != sessions.CodeStoreFailure {
					t.Fatal("policy cause masked storage completion failure", mode, err)
				}
				if mode != "wrapped" && !errors.Is(err, fault) {
					t.Fatal("storage completion cause lost", mode, err)
				}
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatal("access diagnostics disclosed private details")
			}
			stored, present, err := store.Load(t.Context(), record.ID())
			if err != nil || !present || !stored.AccessedAt().Equal(record.AccessedAt()) {
				t.Fatal("failed policy mutated session", err)
			}
		})
	}
}
