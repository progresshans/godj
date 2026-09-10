package admin_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/admin"
)

func TestAuditLogKeepsStableClonedSequenceAndEvictsOldest(t *testing.T) {
	log, err := admin.NewAuditLog(2)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	first := mustEvent(t, "operator", 1, admin.ActionAdd, []string{"title"})
	second := mustEvent(t, "operator", 1, admin.ActionChange, []string{"published"})
	third := mustEvent(t, "operator", 2, admin.ActionDelete, nil)
	for _, event := range []admin.PreparedEvent{first, second, third} {
		if _, ok := log.Append(event); !ok {
			t.Fatal("Append() ok = false")
		}
	}
	entries := log.All()
	if len(entries) != 2 || entries[0].Sequence != 2 || entries[1].Sequence != 3 {
		t.Fatalf("All() = %#v", entries)
	}
	entries[0].ChangedFields[0] = "forged"
	again := log.ForObject("godj_conformance.article", 1)
	if len(again) != 1 || fmt.Sprint(again[0].ChangedFields) != "[published]" {
		t.Fatalf("ForObject() = %#v", again)
	}
}

func TestPrepareEventRejectsInvalidOrSecretShapedFields(t *testing.T) {
	tests := []struct {
		name   string
		actor  string
		model  string
		id     int64
		action admin.Action
		fields []string
		label  string
	}{
		{name: "empty actor", model: "app.article", id: 1, action: admin.ActionAdd},
		{name: "control actor", actor: "user\nsecret", model: "app.article", id: 1, action: admin.ActionAdd},
		{name: "invalid UTF-8 actor", actor: string([]byte{0xff}), model: "app.article", id: 1, action: admin.ActionAdd},
		{name: "invalid model", actor: "user", model: "app/article", id: 1, action: admin.ActionAdd},
		{name: "invalid id", actor: "user", model: "app.article", action: admin.ActionAdd},
		{name: "invalid action", actor: "user", model: "app.article", id: 1, action: "unknown"},
		{name: "duplicate field", actor: "user", model: "app.article", id: 1, action: admin.ActionChange, fields: []string{"title", "title"}},
		{name: "invalid label", actor: "user", model: "app.article", id: 1, action: admin.ActionAdd, label: "bad\x01label"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := admin.PrepareEvent(test.actor, test.model, test.id, test.action, test.fields, test.label); err == nil {
				t.Fatal("PrepareEvent() error = nil")
			}
		})
	}
}

func TestPreparedEventTemplateAttachesConfirmedCreateKey(t *testing.T) {
	template, err := admin.PrepareEventTemplate(
		"operator",
		"godj_conformance.article",
		admin.ActionAdd,
		[]string{"title", "published", "summary"},
		"Article",
	)
	if err != nil {
		t.Fatalf("PrepareEventTemplate() error = %v", err)
	}
	if _, ok := template.ForObject(0); ok {
		t.Fatal("ForObject(0) ok = true")
	}
	event, ok := template.ForObject(42)
	if !ok {
		t.Fatal("ForObject(42) ok = false")
	}
	log, err := admin.NewAuditLog(1)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	entry, ok := log.Append(event)
	if !ok || entry.ObjectID != 42 || entry.Action != admin.ActionAdd {
		t.Fatalf("Append() = %#v, %v", entry, ok)
	}
}

func TestPreparedEventGettersExposeDetachedValidatedSemanticSnapshot(t *testing.T) {
	changed := []string{"title", "summary"}
	event, err := admin.PrepareEvent(
		"operator",
		"godj_conformance.article",
		42,
		admin.ActionChange,
		changed,
		"Article",
	)
	if err != nil {
		t.Fatalf("PrepareEvent() error = %v", err)
	}
	changed[0] = "forged_input"
	gotChanged := event.ChangedFields()
	gotChanged[0] = "forged_output"
	if event.ActorID() != "operator" || event.Model() != "godj_conformance.article" ||
		event.ObjectID() != 42 || event.Action() != admin.ActionChange ||
		fmt.Sprint(event.ChangedFields()) != "[title summary]" || event.DisplayLabel() != "Article" {
		t.Fatalf("PreparedEvent getters = actor %q model %q object %d action %q changed %v label %q",
			event.ActorID(), event.Model(), event.ObjectID(), event.Action(), event.ChangedFields(), event.DisplayLabel())
	}
}

func TestPreparedEventAcceptsBoundedMultibyteDisplayLabels(t *testing.T) {
	label := strings.Repeat("한", 200)
	event, err := admin.PrepareEvent("actor", "articles.article", 1, admin.ActionAdd, nil, label)
	if err != nil {
		t.Fatalf("PrepareEvent() error = %v", err)
	}
	log, err := admin.NewAuditLog(1)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	entry, ok := log.Append(event)
	if !ok || entry.DisplayLabel != label {
		t.Fatalf("Append() = %#v, %v", entry, ok)
	}
	if _, err := admin.PrepareEvent("actor", "articles.article", 1, admin.ActionAdd, nil, strings.Repeat("😀", 257)); err == nil {
		t.Fatal("oversized multibyte display label accepted")
	}
}

func TestAuditLogConcurrentAppendHasUniqueMonotonicProcessSequence(t *testing.T) {
	const count = 100
	log, err := admin.NewAuditLog(count)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	events := make([]admin.PreparedEvent, count)
	for index := range events {
		events[index] = mustEvent(t, "operator", int64(index+1), admin.ActionPublish, []string{"published"})
	}
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(event admin.PreparedEvent) {
			defer wait.Done()
			if _, ok := log.Append(event); !ok {
				t.Error("Append() ok = false")
			}
		}(events[index])
	}
	wait.Wait()
	entries := log.All()
	if len(entries) != count {
		t.Fatalf("All() length = %d, want %d", len(entries), count)
	}
	for index, entry := range entries {
		if entry.Sequence != uint64(index+1) {
			t.Fatalf("entry[%d].Sequence = %d", index, entry.Sequence)
		}
	}
}

func mustEvent(t *testing.T, actor string, objectID int64, action admin.Action, changed []string) admin.PreparedEvent {
	t.Helper()
	event, err := admin.PrepareEvent(actor, "godj_conformance.article", objectID, action, changed, "Article")
	if err != nil {
		t.Fatalf("PrepareEvent() error = %v", err)
	}
	return event
}
