package admin

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
)

func TestAuditLogValueCopySharesConcurrentState(t *testing.T) {
	const count = 128
	log, err := NewAuditLog(count)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	copied := *log
	if copied.state != log.state {
		t.Fatal("copied AuditLog does not share its state")
	}

	events := make([]PreparedEvent, count)
	for index := range events {
		events[index], err = PrepareEvent(
			"operator",
			"godj_conformance.article",
			int64(index+1),
			ActionChange,
			[]string{"title"},
			"Article",
		)
		if err != nil {
			t.Fatalf("PrepareEvent(%d) error = %v", index, err)
		}
	}

	start := make(chan struct{})
	results := make(chan bool, count)
	var writers sync.WaitGroup
	for index, event := range events {
		writers.Add(1)
		target := log
		if index%2 != 0 {
			target = &copied
		}
		go func(target *AuditLog, event PreparedEvent) {
			defer writers.Done()
			<-start
			_, ok := target.Append(event)
			results <- ok
		}(target, event)
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		<-start
		for index := 0; index < count; index++ {
			_ = log.Len()
			_ = copied.All()
		}
	}()
	close(start)
	writers.Wait()
	close(results)
	<-readerDone
	for ok := range results {
		if !ok {
			t.Fatal("Append() through a copied AuditLog returned ok=false")
		}
	}

	if log.Len() != count || copied.Len() != count {
		t.Fatalf("shared lengths = %d and %d, want %d", log.Len(), copied.Len(), count)
	}
	entries := copied.All()
	for index, entry := range entries {
		if entry.Sequence != uint64(index+1) {
			t.Fatalf("entry[%d].Sequence = %d, want %d", index, entry.Sequence, index+1)
		}
	}
}

func TestAuditLogForObjectLimitedReturnsNewestAscendingAndValidatesRequest(t *testing.T) {
	log, err := NewAuditLog(8)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	objects := []int64{1, 2, 1, 1, 2, 1}
	actions := []Action{ActionAdd, ActionAdd, ActionChange, ActionPublish, ActionDelete, ActionDelete}
	for index := range objects {
		event, eventErr := PrepareEvent(
			"operator",
			"godj_conformance.article",
			objects[index],
			actions[index],
			[]string{"title"},
			"Article",
		)
		if eventErr != nil {
			t.Fatalf("PrepareEvent(%d) error = %v", index, eventErr)
		}
		if _, ok := log.Append(event); !ok {
			t.Fatalf("Append(%d) ok = false", index)
		}
	}

	entries, err := log.ForObjectLimited(context.Background(), "godj_conformance.article", 1, 2)
	if err != nil {
		t.Fatalf("ForObjectLimited() error = %v", err)
	}
	if got := []uint64{entries[0].Sequence, entries[1].Sequence}; !reflect.DeepEqual(got, []uint64{4, 6}) {
		t.Fatalf("ForObjectLimited() sequences = %v, want [4 6]", got)
	}
	entries[0].ChangedFields[0] = "forged"
	again, err := log.ForObjectLimited(context.Background(), "godj_conformance.article", 1, 2)
	if err != nil || !reflect.DeepEqual(again[0].ChangedFields, []string{"title"}) {
		t.Fatalf("ForObjectLimited() aliases stored entries: %#v, %v", again, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := log.ForObjectLimited(canceled, "godj_conformance.article", 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ForObjectLimited() error = %v", err)
	}
	invalid := []struct {
		name   string
		ctx    context.Context
		model  string
		id     int64
		limit  int
		target *AuditLog
	}{
		{name: "nil context", model: "godj_conformance.article", id: 1, limit: 1, target: log},
		{name: "invalid model", ctx: context.Background(), model: "bad/model", id: 1, limit: 1, target: log},
		{name: "invalid object", ctx: context.Background(), model: "godj_conformance.article", id: 0, limit: 1, target: log},
		{name: "zero limit", ctx: context.Background(), model: "godj_conformance.article", id: 1, target: log},
		{name: "over limit", ctx: context.Background(), model: "godj_conformance.article", id: 1, limit: MaximumHistoryEntries + 1, target: log},
		{name: "nil log", ctx: context.Background(), model: "godj_conformance.article", id: 1, limit: 1},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.target.ForObjectLimited(test.ctx, test.model, test.id, test.limit); err == nil {
				t.Fatal("ForObjectLimited() error = nil")
			}
		})
	}
}

func TestRegisteredHistoryBoundsRequestAndRejectsMalformedResults(t *testing.T) {
	t.Run("request limit", func(t *testing.T) {
		config := validRegistryConfig(t)
		var received HistoryRequest
		config.History = func(_ context.Context, id int64, request HistoryRequest) ([]AuditEntry, error) {
			received = request
			return []AuditEntry{validHardeningHistoryEntry(1, id)}, nil
		}
		model := buildHardeningModel(t, config)
		entries, err := model.history(context.Background(), mustPrincipal(t), 1)
		if err != nil || len(entries) != 1 {
			t.Fatalf("history() = %#v, %v", entries, err)
		}
		if received.Limit != MaximumHistoryEntries {
			t.Fatalf("HistoryRequest.Limit = %d, want %d", received.Limit, MaximumHistoryEntries)
		}
	})

	tooMany := make([]AuditEntry, MaximumHistoryEntries+1)
	for index := range tooMany {
		tooMany[index] = validHardeningHistoryEntry(uint64(index+1), 1)
	}
	tests := []struct {
		name    string
		entries []AuditEntry
		code    string
	}{
		{name: "over cap", entries: tooMany, code: "limit_exceeded"},
		{name: "malformed actor", entries: []AuditEntry{func() AuditEntry {
			entry := validHardeningHistoryEntry(1, 1)
			entry.ActorID = "operator\nforged"
			return entry
		}()}, code: "invalid"},
		{name: "malformed action", entries: []AuditEntry{func() AuditEntry {
			entry := validHardeningHistoryEntry(1, 1)
			entry.Action = Action("unknown")
			return entry
		}()}, code: "invalid"},
		{name: "malformed changed field", entries: []AuditEntry{func() AuditEntry {
			entry := validHardeningHistoryEntry(1, 1)
			entry.ChangedFields = []string{"not_editable"}
			return entry
		}()}, code: "invalid"},
		{name: "malformed display", entries: []AuditEntry{func() AuditEntry {
			entry := validHardeningHistoryEntry(1, 1)
			entry.DisplayLabel = "Article\x00secret"
			return entry
		}()}, code: "invalid"},
		{name: "sequence beyond signed template integer", entries: []AuditEntry{func() AuditEntry {
			entry := validHardeningHistoryEntry(uint64(math.MaxInt64)+1, 1)
			return entry
		}()}, code: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			config.History = func(context.Context, int64, HistoryRequest) ([]AuditEntry, error) {
				return append([]AuditEntry(nil), test.entries...), nil
			}
			model := buildHardeningModel(t, config)
			if _, err := model.history(context.Background(), mustPrincipal(t), 1); errorCode(err) != test.code {
				t.Fatalf("history() error = %v, code %q, want %q", err, errorCode(err), test.code)
			}
		})
	}
}

func TestRegisteredMutationSuccessWithInvalidPostconditionRequiresReconciliation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ModelConfig[registryArticle], *bool)
		run    func(registeredModel, forms.Form) error
	}{
		{
			name: "create",
			mutate: func(config *ModelConfig[registryArticle], called *bool) {
				config.Create = func(context.Context, auth.Principal, forms.Values) (registryArticle, error) {
					*called = true
					return registryArticle{title: "Created"}, nil
				}
			},
			run: func(model registeredModel, form forms.Form) error {
				_, err := model.create(context.Background(), mustPrincipal(t), form)
				return err
			},
		},
		{
			name: "update",
			mutate: func(config *ModelConfig[registryArticle], called *bool) {
				config.Update = func(context.Context, auth.Principal, int64, forms.Values) (registryArticle, []string, error) {
					*called = true
					return registryArticle{id: 2, title: "Updated"}, []string{"title"}, nil
				}
			},
			run: func(model registeredModel, form forms.Form) error {
				_, _, err := model.update(context.Background(), mustPrincipal(t), 1, form)
				return err
			},
		},
		{
			name: "delete",
			mutate: func(config *ModelConfig[registryArticle], called *bool) {
				config.Delete = func(context.Context, auth.Principal, int64) (registryArticle, error) {
					*called = true
					return registryArticle{id: 2, title: "Deleted"}, nil
				}
			},
			run: func(model registeredModel, _ forms.Form) error {
				_, err := model.delete(context.Background(), mustPrincipal(t), 1)
				return err
			},
		},
		{
			name: "action",
			mutate: func(config *ModelConfig[registryArticle], called *bool) {
				config.Actions[0].Run = func(context.Context, auth.Principal, []int64) (ActionResult, error) {
					*called = true
					return ActionResult{MatchedIDs: []int64{2}}, nil
				}
			},
			run: func(model registeredModel, _ forms.Form) error {
				_, err := model.actions[0].run(context.Background(), mustPrincipal(t), []int64{1})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			called := false
			test.mutate(&config, &called)
			model := buildHardeningModel(t, config)
			form := bindHardeningForm(t, model, "Updated")
			err := test.run(model, form)
			if !called {
				t.Fatal("callback was not called")
			}
			if !errors.Is(err, ErrReconciliationRequired) {
				t.Fatalf("operation error = %v, want errors.Is(ErrReconciliationRequired)", err)
			}
		})
	}
}

func TestRegisteredGetAllowsSpacedExistingInitialWhileMutationUsesCleanedValue(t *testing.T) {
	const existing = "  legacy title  "
	config := validRegistryConfig(t)
	config.Get = func(context.Context, int64) (registryArticle, bool, error) {
		return registryArticle{id: 1, title: existing}, true, nil
	}
	var received string
	config.Update = func(_ context.Context, _ auth.Principal, id int64, values forms.Values) (registryArticle, []string, error) {
		received, _ = values.String("title")
		return registryArticle{id: id, title: received}, []string{"title"}, nil
	}
	model := buildHardeningModel(t, config)
	record, found, err := model.get(context.Background(), mustPrincipal(t), 1)
	if err != nil || !found {
		t.Fatalf("get() = %#v, %v, %v", record, found, err)
	}
	if title, ok := record.initial["title"].AsString(); !ok || title != existing {
		t.Fatalf("initial title = %q, %v, want %q", title, ok, existing)
	}
	bound, err := model.form.Bind(forms.NewData(map[string][]string{
		"title": {"  new title  "}, "published": {"false"}, "summary": {""},
	}), record.initial)
	if err != nil || !bound.Valid() {
		t.Fatalf("Bind() = %#v, %v", bound, err)
	}
	if _, _, err := model.update(context.Background(), mustPrincipal(t), 1, bound); err != nil {
		t.Fatalf("update() error = %v", err)
	}
	if received != "new title" {
		t.Fatalf("update callback title = %q, want cleaned strict value %q", received, "new title")
	}
}

func TestRegisterModelRejectsCSRFFieldReservedByHTTPBoundary(t *testing.T) {
	config := validRegistryConfig(t)
	for index := range config.Model.Fields {
		if config.Model.Fields[index].Name != "title" {
			continue
		}
		config.Model.Fields[index].Name = "csrfmiddlewaretoken"
		config.Model.Fields[index].GoName = "CSRFMiddlewareToken"
		config.Model.Fields[index].Column = "csrfmiddlewaretoken"
	}
	config.ListFields = []string{"id", "csrfmiddlewaretoken", "published"}
	config.SearchFields = []string{"csrfmiddlewaretoken", "summary"}
	err := RegisterModel(NewBuilder(mustApps(t)), config)
	var configErr *ConfigError
	if !errors.As(err, &configErr) || configErr.Code != "reserved" || configErr.Path != "model.form.csrfmiddlewaretoken" {
		t.Fatalf("RegisterModel() error = %#v", err)
	}
}

func buildHardeningModel(t *testing.T, config ModelConfig[registryArticle]) registeredModel {
	t.Helper()
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return registry.models[0]
}

func bindHardeningForm(t *testing.T, model registeredModel, title string) forms.Form {
	t.Helper()
	form, err := model.form.Bind(forms.NewData(map[string][]string{
		"title": {title}, "published": {"false"}, "summary": {""},
	}), nil)
	if err != nil || !form.Valid() {
		t.Fatalf("Bind() = %#v, %v", form, err)
	}
	return form
}

func validHardeningHistoryEntry(sequence uint64, objectID int64) AuditEntry {
	return AuditEntry{
		Sequence:      sequence,
		ActorID:       "operator",
		Model:         "godj_conformance.article",
		ObjectID:      objectID,
		Action:        ActionChange,
		ChangedFields: []string{"title"},
		DisplayLabel:  "Article",
	}
}
