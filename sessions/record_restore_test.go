package sessions_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestRestoreRecordRoundTripIsDetachedAndCanonical(t *testing.T) {
	t.Parallel()

	id := mustSessionID(t)
	location := time.FixedZone("restore-test", 9*60*60)
	created := time.Date(2026, time.August, 24, 12, 0, 0, 123, location)
	snapshot := sessions.RecordSnapshot{
		ID:                id,
		Values:            map[string]string{"principal": "admin", "csrf": "redacted-value"},
		CreatedAt:         created,
		AccessedAt:        created.Add(time.Minute),
		AbsoluteExpiresAt: created.Add(24 * time.Hour),
		IdleExpiresAt:     created.Add(31 * time.Minute),
	}

	record, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
	if err != nil {
		t.Fatalf("RestoreRecord(): %v", err)
	}
	snapshot.Values["principal"] = "mutated"
	if value, _ := record.Value("principal"); value != "admin" {
		t.Fatalf("record principal = %q, want detached admin", value)
	}
	if got := record.CreatedAt(); got.Location() != time.UTC || !got.Equal(created) {
		t.Fatalf("CreatedAt() = %v (%v), want canonical UTC %v", got, got.Location(), created.UTC())
	}

	published := record.Snapshot()
	if published.ID != id || !published.CreatedAt.Equal(created) || published.CreatedAt.Location() != time.UTC {
		t.Fatalf("Snapshot() identity/time = (%v,%v), want detached canonical values", published.ID, published.CreatedAt)
	}
	published.Values["principal"] = "changed-again"
	if value, _ := record.Value("principal"); value != "admin" {
		t.Fatalf("record changed through published snapshot: %q", value)
	}
	if got := record.Snapshot().Values["principal"]; got != "admin" {
		t.Fatalf("second Snapshot().Values[principal] = %q, want admin", got)
	}
	if got := snapshot.String(); got != "sessions.RecordSnapshot{redacted}" || strings.Contains(got, "admin") {
		t.Fatalf("RecordSnapshot.String() = %q, want redacted", got)
	}
}

func TestRestoreRecordRejectsInvalidPersistentState(t *testing.T) {
	t.Parallel()

	id := mustSessionID(t)
	created := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
	valid := sessions.RecordSnapshot{
		ID:                id,
		Values:            map[string]string{"principal": "admin"},
		CreatedAt:         created,
		AccessedAt:        created,
		AbsoluteExpiresAt: created.Add(time.Hour),
		IdleExpiresAt:     created.Add(30 * time.Minute),
	}

	tests := []struct {
		name   string
		mutate func(*sessions.RecordSnapshot)
	}{
		{name: "zero id", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.ID = sessions.ID{} }},
		{name: "zero created", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.CreatedAt = time.Time{} }},
		{name: "access before create", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.AccessedAt = created.Add(-time.Second) }},
		{name: "absolute not after create", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.AbsoluteExpiresAt = created }},
		{name: "idle after absolute", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.IdleExpiresAt = created.Add(2 * time.Hour) }},
		{name: "idle not after access", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.IdleExpiresAt = created }},
		{name: "malformed value", mutate: func(snapshot *sessions.RecordSnapshot) { snapshot.Values = map[string]string{"bad\x00key": "value"} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			snapshot.Values = map[string]string{"principal": "admin"}
			test.mutate(&snapshot)
			record, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
			if err == nil || record.ID().Valid() {
				t.Fatalf("RestoreRecord() = (%v,%v), want zero/error", record, err)
			}
			var classified *sessions.Error
			if !errors.As(err, &classified) || classified.Code != sessions.CodeInvalidRecord || classified.Field != "snapshot" {
				t.Fatalf("RestoreRecord() error = %#v, want invalid_record/snapshot", err)
			}
		})
	}
}

func TestRestoreRecordAppliesExplicitLimitsBeforePublication(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
	snapshot := sessions.RecordSnapshot{
		ID:                mustSessionID(t),
		Values:            map[string]string{"principal": "admin"},
		CreatedAt:         created,
		AccessedAt:        created,
		AbsoluteExpiresAt: created.Add(time.Hour),
		IdleExpiresAt:     created.Add(30 * time.Minute),
	}

	if record, err := sessions.RestoreRecord(snapshot, sessions.Limits{MaxValueBytes: 4}); err == nil || record.ID().Valid() {
		t.Fatalf("RestoreRecord(oversize) = (%v,%v), want zero/error", record, err)
	} else {
		var classified *sessions.Error
		if !errors.As(err, &classified) || classified.Code != sessions.CodeInvalidRecord {
			t.Fatalf("RestoreRecord(oversize) error = %#v, want invalid_record", err)
		}
	}

	if record, err := sessions.RestoreRecord(snapshot, sessions.Limits{MaxValues: -1}); err == nil || record.ID().Valid() {
		t.Fatalf("RestoreRecord(invalid limits) = (%v,%v), want zero/error", record, err)
	} else {
		var classified *sessions.Error
		if !errors.As(err, &classified) || classified.Code != sessions.CodeInvalidConfig {
			t.Fatalf("RestoreRecord(invalid limits) error = %#v, want invalid_config", err)
		}
	}

	tooManyValues := make(map[string]string, sessions.DefaultLimits().MaxValues+1)
	for index := 0; index <= sessions.DefaultLimits().MaxValues; index++ {
		tooManyValues[fmt.Sprintf("key-%02d", index)] = "value"
	}
	snapshot.Values = tooManyValues
	if record, err := sessions.RestoreRecord(snapshot, sessions.Limits{}); err == nil || record.ID().Valid() {
		t.Fatalf("RestoreRecord(too many values) = (%v,%v), want zero/error", record, err)
	} else {
		var classified *sessions.Error
		if !errors.As(err, &classified) || classified.Code != sessions.CodeInvalidRecord {
			t.Fatalf("RestoreRecord(too many values) error = %#v, want invalid_record", err)
		}
	}
	if len(tooManyValues) != sessions.DefaultLimits().MaxValues+1 {
		t.Fatal("RestoreRecord mutated adapter-owned oversized values")
	}
}

func mustSessionID(t *testing.T) sessions.ID {
	t.Helper()
	id, err := sessions.ParseID(strings.Repeat("A", 43))
	if err != nil {
		t.Fatalf("ParseID(): %v", err)
	}
	return id
}
