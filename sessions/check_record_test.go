package sessions

import (
	"errors"
	"testing"
	"time"
)

func TestManagerCheckRecordHonorsOwnerLimitsWithoutSources(t *testing.T) {
	store, err := NewMemoryStore(8)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := NewManager(store, Config{})
	if err != nil {
		t.Fatal(err)
	}
	one, err := creator.Create(t.Context(), map[string]string{"one": "value"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := one.WithValue("two", "value")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewManager(store, Config{Limits: Limits{MaxValues: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner.sources.clock = func() time.Time { t.Fatal("record validation sampled clock"); return time.Time{} }
	if err := owner.CheckRecord(one); err != nil {
		t.Fatal(err)
	}
	for _, record := range []Record{{}, two} {
		if err := owner.CheckRecord(record); !errors.Is(err, &Error{Code: CodeInvalidRecord}) {
			t.Fatal("manager accepted an invalid or oversized record", err)
		}
	}
	var absent *Manager
	if err := absent.CheckRecord(one); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatal(err)
	}
}
