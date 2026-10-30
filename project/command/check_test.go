package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
)

func TestResourceChecksRejectInvalidRegistrationWithoutCallbacks(t *testing.T) {
	installed, err := apps.New([]apps.Config{{Name: "example.com/inventory", Label: "inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	definition := ResourceCheck{Name: "database", AppLabel: "inventory", Summary: "Read the database", Run: func(context.Context) error { t.Fatal("registration ran I/O"); return nil }}
	for _, mutate := range []func(*ResourceCheck){
		func(c *ResourceCheck) { c.Name = "private/name" },
		func(c *ResourceCheck) { c.AppLabel = "missing" },
		func(c *ResourceCheck) { c.Summary = "private\noutput" },
		func(c *ResourceCheck) { c.Run = nil },
	} {
		invalid := definition
		mutate(&invalid)
		if _, err := New(Config{Apps: installed, ResourceChecks: []ResourceCheck{invalid}}); err == nil {
			t.Fatal("invalid resource check registered")
		}
	}
	for _, checks := range [][]ResourceCheck{{definition, definition}, make([]ResourceCheck, MaxCommands+1)} {
		if _, err := New(Config{Apps: installed, ResourceChecks: checks}); err == nil {
			t.Fatal("ambiguous or unbounded resource checks registered")
		}
	}
}

func TestResourceChecksAreExplicitAndSnapshotTheirInventory(t *testing.T) {
	var order []string
	checks := []ResourceCheck{
		{Name: "storage", Summary: "Read storage", Run: func(context.Context) error { order = append(order, "storage"); return nil }},
		{Name: "database", Summary: "Read database", Run: func(context.Context) error { order = append(order, "database"); return nil }},
	}
	registry, err := New(Config{
		Check: func(context.Context) error { order = append(order, "pure"); return nil }, ResourceChecks: checks,
		Commands: []Definition{{Name: "work", Summary: "Do work", Handle: func(context.Context, Values, Streams) error { order = append(order, "command"); return nil }}},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks[0] = ResourceCheck{Name: "changed"}
	inventory := registry.ResourceDescriptions()
	if len(inventory) != 2 || inventory[0].Name != "database" || inventory[1].Name != "storage" || inventory[0].Run != nil || inventory[1].Run != nil {
		t.Fatal("check inventory is unsorted or carries callbacks")
	}
	inventory[0].Name = "changed"
	var output bytes.Buffer
	streams := testStreams()
	streams.Stdout = &output
	if err := registry.Serve(t.Context(), []string{"check", "--list"}, streams); err != nil || output.String() != "database\tRead database\nstorage\tRead storage\n" || len(order) != 0 {
		t.Fatal("check discovery performed I/O or aliases declarations", output.String(), err)
	}
	if err := registry.Serve(t.Context(), []string{"check"}, streams); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), "work", nil, streams); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"pure", "pure", "command"}) {
		t.Fatal("ordinary operation ran optional I/O", order)
	}
	order = nil
	args := []string{"check", "--resource", "storage", "--resource", "database"}
	before := append([]string(nil), args...)
	if err := registry.Serve(t.Context(), args, streams); err != nil || !reflect.DeepEqual(args, before) || !reflect.DeepEqual(order, []string{"pure", "storage", "database"}) {
		t.Fatal("selected checks lost order or ownership", order, err)
	}
	order = nil
	if err := registry.CheckResources(t.Context(), "database"); err != nil || !reflect.DeepEqual(order, []string{"pure", "database"}) {
		t.Fatal("unselected resource executed", order, err)
	}
	streams.Stdout = brokenOutput{}
	if err := registry.Serve(t.Context(), []string{"check", "--list"}, streams); Diagnostic(err) != "command_error/output_failed" {
		t.Fatal("lost inventory output failure", err)
	}
}

func TestResourceCheckSelectionCompletesBeforeAnyCallback(t *testing.T) {
	calls := 0
	registry, err := New(Config{Check: func(context.Context) error { calls++; return nil }, ResourceChecks: []ResourceCheck{{Name: "database", Summary: "Read database", Run: func(context.Context) error { calls++; return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"check", "--resource"}, {"check", "--unknown", "private-value"}, {"check", "--list", "--resource", "database"},
		{"check", "--resource", "database", "--resource", "missing"}, {"check", "--resource", "database", "--resource", "database"},
		{"check", "--resource", "private/name"}, {"check", "--resource", strings.Repeat("x", 64)},
	} {
		if err := registry.Serve(t.Context(), args, testStreams()); err == nil || ExitCode(err) != 2 || calls != 0 || strings.Contains(Diagnostic(err), "private") {
			t.Fatal("invalid selection reached a callback or disclosed input", err, calls)
		}
	}
	if err := registry.CheckResources(t.Context(), make([]string, MaxCommands+1)...); ExitCode(err) != 2 || calls != 0 {
		t.Fatal("unbounded selection reached checks", err)
	}
	if err := registry.CheckResources(nil, "database"); ExitCode(err) != 2 || calls != 0 {
		t.Fatal("nil context reached checks", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := registry.CheckResources(ctx, "database"); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled selection reached checks", err)
	}
}

func TestResourceCheckFailuresStopLaterChecksAndJoinCleanup(t *testing.T) {
	secret := errors.New("credential-private-value")
	for _, mode := range []string{"pure-error", "error", "panic", "goexit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var order []string
			registry, err := New(Config{
				Check: func(context.Context) error {
					order = append(order, "pure")
					if mode == "pure-error" {
						return &CheckError{Field: "GODJ_POSTGRES_SCHEMA", Cause: secret}
					}
					return nil
				},
				ResourceChecks: []ResourceCheck{
					{Name: "database", Summary: "Read database", Run: func(context.Context) error {
						order = append(order, "open")
						defer func() { order = append(order, "close") }()
						switch mode {
						case "panic":
							panic(secret)
						case "goexit":
							runtime.Goexit()
						case "cancel":
							cancel()
							return nil
						}
						return &CheckError{Field: "database", Cause: secret}
					}},
					{Name: "storage", Summary: "Read storage", Run: func(context.Context) error { t.Fatal("failure reached later I/O"); return nil }},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = registry.CheckResources(ctx, "database", "storage")
			want := []string{"pure", "open", "close"}
			if mode == "pure-error" {
				want = []string{"pure"}
			}
			if err == nil || ExitCode(err) != 1 || !reflect.DeepEqual(order, want) || strings.Contains(fmt.Sprintf("%+v %#v %s", err, err, Diagnostic(err)), "private-value") {
				t.Fatal("failed check lost cleanup or exposed cause", order, err)
			}
			switch mode {
			case "pure-error":
				if !errors.Is(err, secret) || Diagnostic(err) != "command_error/check_failed/GODJ_POSTGRES_SCHEMA" {
					t.Fatal("pure field diagnostic lost", err)
				}
			case "error":
				if !errors.Is(err, secret) || Diagnostic(err) != "command_error/resource_check_failed/database/database" {
					t.Fatal("resource field diagnostic lost", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || Diagnostic(err) != "command_error/canceled" {
					t.Fatal("check cancellation lost", err)
				}
			}
		})
	}
	if err := (&CheckError{Field: "environment[2]", Cause: secret}); Diagnostic(err) != "command_error/check_failed/environment[2]" || !errors.Is(err, secret) {
		t.Fatal("entrypoint configuration lost its field", err)
	}
	for _, field := range []string{"private/path", "\nprivate-value", strings.Repeat("x", 129), "environment[]", "environment[private-value]", "environment[1", "environment]"} {
		failure := &CheckError{Field: field, Cause: secret}
		err := failed("check_failed", failure)
		if Diagnostic(err) != "command_error/check_failed" || strings.Contains(fmt.Sprintf("%+v %#v", failure, failure), "private-value") {
			t.Fatal("unsafe configuration location escaped", err)
		}
	}
}

func TestResourceCheckCancellationDoesNotAbandonTheCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var released sync.Once
	finish := func() { released.Do(func() { close(release) }) }
	defer finish()
	registry, err := New(Config{ResourceChecks: []ResourceCheck{{Name: "database", Summary: "Read database", Run: func(ctx context.Context) error {
		defer close(closed)
		close(started)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- registry.CheckResources(ctx, "database") }()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("check returned before its callback", err)
	case <-time.After(5 * time.Second):
		t.Fatal("check did not enter its callback")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("check abandoned its resource callback")
	case <-time.After(20 * time.Millisecond):
	}
	finish()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		select {
		case <-closed:
		default:
			t.Fatal("check returned before cleanup")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("check did not join cleanup")
	}
}

func TestResourceChecksAllowConcurrentInvocationWithoutSharedResults(t *testing.T) {
	var calls, closed atomic.Int64
	registry, err := New(Config{ResourceChecks: []ResourceCheck{{Name: "database", Summary: "Read database", Run: func(context.Context) error { calls.Add(1); defer closed.Add(1); return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 24 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := registry.CheckResources(t.Context(), "database"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 24 || closed.Load() != 24 {
		t.Fatal("resource invocation lifetime was shared", calls.Load(), closed.Load())
	}
}
