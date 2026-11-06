package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestWithSavepointRejectsInvalidOrUnsupportedSessionsWithoutCallback(t *testing.T) {
	t.Parallel()
	var nilSession *savepointSession
	expired := &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "expired source"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name     string
		ctx      context.Context
		session  db.Session
		callback bool
		want     error
	}{
		{"nil_context", nil, &savepointSession{}, true, &query.Error{Code: query.CodeInvalidPlan}},
		{"nil_session", context.Background(), nil, true, &query.Error{Code: query.CodeInvalidPlan}},
		{"typed_nil_session", context.Background(), nilSession, true, &query.Error{Code: query.CodeInvalidPlan}},
		{"canceled_context", canceled, &savepointSession{}, true, context.Canceled},
		{"nil_callback", context.Background(), &savepointSession{}, false, &query.Error{Code: query.CodeInvalidPlan}},
		{"no_capability", context.Background(), &plainSession{}, true, &query.Error{Code: query.CodeUnsupported}},
		{"expired", context.Background(), &savepointSession{validation: expired}, true, expired},
	} {
		t.Run(test.name, func(t *testing.T) {
			var callback func(db.Session) error
			if test.callback {
				callback = func(db.Session) error { t.Fatal("invalid savepoint invoked callback"); return nil }
			}
			if err := db.WithSavepoint(test.ctx, test.session, callback); !errors.Is(err, test.want) {
				t.Fatalf("WithSavepoint = %v", err)
			}
			if source, ok := test.session.(*savepointSession); ok && source != nil && source.calls != 0 {
				t.Fatalf("invalid source entered: %d", source.calls)
			}
		})
	}
}

func TestWithSavepointKeepsCallbackAndErrorOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := &savepointSession{}
	want := errors.New("callback failure")
	calls := 0
	err := db.WithSavepoint(ctx, source, func(child db.Session) error {
		calls++
		if child == source {
			t.Fatal("source adapter did not supply its child")
		}
		return want
	})
	if err != want || calls != 1 || source.calls != 1 {
		t.Fatalf("callback ownership = %v/%d/%d", err, calls, source.calls)
	}
}

type plainSession struct{}

func (*plainSession) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (*plainSession) Insert(context.Context, query.InsertPlan) (int64, error) {
	return 0, errors.New("unexpected insert")
}
func (*plainSession) Update(context.Context, query.UpdatePlan) (int64, error) {
	return 0, errors.New("unexpected update")
}
func (*plainSession) Delete(context.Context, query.DeletePlan) (int64, error) {
	return 0, errors.New("unexpected delete")
}

type savepointSession struct {
	plainSession
	validation error
	calls      int
}

func (source *savepointSession) ValidateSession(context.Context) error { return source.validation }
func (source *savepointSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	source.calls++
	return callback(&plainSession{})
}
