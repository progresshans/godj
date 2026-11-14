package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
)

type opaqueStorageFailure []string

func (opaqueStorageFailure) Error() string { return "private domain storage failure" }

type storageFailureScope struct {
	db.Session
	fault error
}

func (scope *storageFailureScope) ValidateSession(context.Context) error { return scope.fault }

type storageFailureOwner struct{ calls int }

func (owner *storageFailureOwner) CoordinatedAtomic(ctx context.Context, run func(db.Session) error) error {
	owner.calls++
	return run(&storageFailureScope{})
}

func TestAccountDomainStorageFailureKeepsOpaqueCauseThroughTransaction(t *testing.T) {
	fault := opaqueStorageFailure{"private domain cause"}
	value, err := domain.RequireIn(t.Context(), &storageFailureScope{fault: fault}, strings.Repeat("a", 64))
	if err == nil || value.Sequence() != 0 {
		t.Fatal("failed domain scope published ownership")
	}
	var observed opaqueStorageFailure
	if !errors.As(err, &observed) || !reflect.DeepEqual(observed, fault) {
		t.Fatal("domain failure lost its opaque backend cause")
	}
	// The prepared-result owner must be able to recognize the very error that
	// its callback returned, regardless of the cause's dynamic comparability.
	owner := &storageFailureOwner{}
	result, returned := db.CoordinatedAtomicResult(t.Context(), owner, func(context.Context, db.Session) (int, error) { return 17, err })
	if result != 0 || owner.calls != 1 || !errors.Is(returned, err) {
		t.Fatal("domain failure broke confirmed callback error ownership")
	}
	encoded, marshalErr := json.Marshal(returned)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	diagnostic := fmt.Sprintf("%v %+v %#v", returned, returned, returned) + string(encoded)
	if strings.Contains(diagnostic, "private domain") {
		t.Fatal("domain storage failure disclosed backend data")
	}
}
