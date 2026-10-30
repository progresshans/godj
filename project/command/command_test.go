package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/apps"
)

func testStreams() Streams {
	return Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
}

func TestRegistryRejectsAmbiguityBeforeAnyCallback(t *testing.T) {
	installed, err := apps.New([]apps.Config{{Name: "example.com/inventory", Label: "inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	definition := Definition{Name: "count", AppLabel: "inventory", Summary: "Count rows", Handle: func(context.Context, Values, Streams) error { t.Fatal("invalid registry invoked command"); return nil }}
	for _, mutate := range []func(*Definition){
		func(d *Definition) { d.Name = "migrate" }, func(d *Definition) { d.Name = "__private" },
		func(d *Definition) { d.AppLabel = "missing" }, func(d *Definition) { d.Summary = "unsafe\noutput" },
		func(d *Definition) { d.Handle = nil },
		func(d *Definition) { d.Options = []Option{{Name: "help", Help: "conflict", Kind: Boolean}} },
		func(d *Definition) {
			d.Arguments = []Argument{{Name: "x", Help: "optional", Kind: String}, {Name: "y", Help: "required", Kind: String, Required: true}}
		},
	} {
		d := definition
		mutate(&d)
		if _, err := New(Config{Apps: installed, Commands: []Definition{d}}); err == nil {
			t.Fatal("invalid definition accepted")
		}
	}
	if _, err := New(Config{Apps: installed, Commands: []Definition{definition, definition}}); err == nil {
		t.Fatal("duplicate command accepted")
	}
	if _, err := New(Config{Commands: []Definition{definition}}); err == nil {
		t.Fatal("uninstalled app accepted")
	}
}

func TestCommandParsesAllArgumentsBeforeCheckOrResourceOpen(t *testing.T) {
	checks, opens, calls, closes := 0, 0, 0, 0
	definition := Definition{Name: "record", Summary: "Store an entry",
		Arguments: []Argument{{Name: "id", Help: "row id", Kind: Integer, Required: true}, {Name: "names", Help: "names", Kind: String, Many: true}},
		Options:   []Option{{Name: "active", Help: "active", Kind: Boolean}, {Name: "title", Help: "title", Kind: String, Required: true}},
		Handle: Using(func(context.Context) (Resource[int], error) {
			opens++
			return Resource[int]{Value: 42, Close: func(context.Context) error { closes++; return nil }}, nil
		},
			func(_ context.Context, resource int, values Values, _ Streams) error {
				calls++
				id, ok := values.Integer("id")
				active, provided := values.Boolean("active")
				title, present := values.String("title")
				if resource != 42 || !ok || id != -7 || !provided || active || !present || title != "secret-value" || !reflect.DeepEqual(values.All("names"), []string{"one", "--literal"}) {
					t.Fatal("parsed values differ")
				}
				if strings.Contains(fmt.Sprintf("%+v", values), "secret-value") {
					t.Fatal("values leaked during formatting")
				}
				copy := values.All("names")
				copy[0] = "changed"
				if values.All("names")[0] != "one" {
					t.Fatal("value slice aliases invocation")
				}
				return nil
			}),
	}
	r, err := New(Config{Commands: []Definition{definition}, Check: func(context.Context) error { checks++; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"1"}, {"bad", "--title=x"}, {"01", "--title=x"}, {"9223372036854775808", "--title=x"}, {"1", "--title=x", "--title=y"}, {"1", "--title=x", "--active=1"}, {"1", "--title=x", "--unknown=private"}, {"1", "--title"}, {"1", "--title=\xff"}, {"1", "--title=" + strings.Repeat("x", MaxArgumentBytes+1)}} {
		result, err := r.Execute(t.Context(), "record", args, testStreams())
		if err == nil || result.Executed || checks != 0 || opens != 0 || calls != 0 || closes != 0 {
			t.Fatal("invalid argument reached callback")
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("raw value exposed")
		}
	}
	argv := []string{"-7", "one", "--title", "secret-value", "--active=false", "--", "--literal"}
	before := append([]string(nil), argv...)
	result, err := r.Execute(t.Context(), "record", argv, testStreams())
	if err != nil || !result.Executed || !result.Completed || checks != 1 || opens != 1 || calls != 1 || closes != 1 || !reflect.DeepEqual(argv, before) {
		t.Fatal(result, err, checks, opens, calls, closes)
	}
}

func TestRegistrySnapshotsAndHelpNeverOpenResources(t *testing.T) {
	definitions := []Definition{{Name: "inspect", Summary: "Inspect rows", Options: []Option{{Name: "limit", Help: "row limit", Kind: Integer}}, Handle: func(context.Context, Values, Streams) error { t.Fatal("help executed a handler"); return nil }}}
	r, err := New(Config{Commands: definitions, Check: func(context.Context) error { t.Fatal("help ran checks"); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Name = "changed"
	definitions[0].Options[0].Name = "changed"
	descriptions := r.Descriptions()
	descriptions[0].Options[0].Name = "changed"
	for _, args := range [][]string{{"list"}, {"help", "inspect"}, {"run", "inspect", "--help"}} {
		var output bytes.Buffer
		streams := testStreams()
		streams.Stdout = &output
		if err := r.Serve(t.Context(), args, streams); err != nil || strings.Contains(output.String(), "changed") || !strings.Contains(output.String(), "inspect") {
			t.Fatal(output.String(), err)
		}
	}
	if got := r.Descriptions(); got[0].Handle != nil || got[0].Options[0].Name != "limit" {
		t.Fatal("descriptions retained handler or mutable option")
	}
}

func TestCommandResourceClosesAfterFailurePanicAndGoexit(t *testing.T) {
	secret := errors.New("private-database-url")
	for _, mode := range []string{"error", "panic", "goexit", "open-error", "close-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var order []string
			r, err := New(Config{Commands: []Definition{{Name: "owned", Summary: "Own a resource", Handle: Using(
				func(context.Context) (Resource[int], error) {
					order = append(order, "open")
					resource := Resource[int]{Value: 42, Close: func(cleanup context.Context) error {
						if cleanup.Err() != nil {
							t.Error("cleanup inherited cancellation")
						}
						order = append(order, "close")
						if mode == "close-error" {
							return secret
						}
						return nil
					}}
					if mode == "open-error" {
						return resource, secret
					}
					return resource, nil
				}, func(context.Context, int, Values, Streams) error {
					order = append(order, "run")
					defer func() { order = append(order, "defer") }()
					switch mode {
					case "error":
						return secret
					case "panic":
						panic(secret)
					case "goexit":
						runtime.Goexit()
					case "cancel":
						cancel()
						return ctx.Err()
					}
					return nil
				})}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.Execute(ctx, "owned", nil, testStreams())
			want := []string{"open", "run", "defer", "close"}
			if mode == "open-error" {
				want = []string{"open", "close"}
			}
			if err == nil || !result.Executed || result.Completed || !reflect.DeepEqual(order, want) || strings.Contains(fmt.Sprintf("%+v", err), "private") || strings.Contains(Diagnostic(err), "private") {
				t.Fatal(result, err, order)
			}
			if (mode == "error" || mode == "open-error" || mode == "close-error") && !errors.Is(err, secret) {
				t.Fatal("cause lost")
			}
		})
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, nil }

func TestIgnoredOutputFailureDoesNotRepeatCompletedMutation(t *testing.T) {
	writes := 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, err := New(Config{Commands: []Definition{{Name: "write", Summary: "Write once", Handle: func(_ context.Context, _ Values, output Streams) error {
		writes++
		_, _ = io.WriteString(output.Stdout, "done")
		cancel()
		return nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	streams := testStreams()
	streams.Stdout = brokenOutput{}
	result, err := r.Execute(ctx, "write", nil, streams)
	if err == nil || writes != 1 || !result.Executed || !result.Completed || !result.OutputFailed || Diagnostic(err) != "command_error/output_failed" {
		t.Fatal(result, err, writes)
	}
}

func TestRegistryConcurrentInvocationsOwnValuesAndResources(t *testing.T) {
	var output bytes.Buffer
	var mu sync.Mutex
	opens, closes := 0, 0
	r, err := New(Config{Commands: []Definition{{Name: "concurrent", Summary: "Use independent resources", Arguments: []Argument{{Name: "number", Help: "number", Kind: Integer, Required: true}}, Handle: Using(
		func(context.Context) (Resource[*int], error) {
			value := new(int)
			mu.Lock()
			opens++
			mu.Unlock()
			return Resource[*int]{Value: value, Close: func(context.Context) error { mu.Lock(); closes++; mu.Unlock(); return nil }}, nil
		},
		func(_ context.Context, resource *int, values Values, _ Streams) error {
			number, _ := values.Integer("number")
			*resource = int(number)
			mu.Lock()
			fmt.Fprintln(&output, *resource)
			mu.Unlock()
			return nil
		}),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Execute(t.Context(), "concurrent", []string{fmt.Sprint(i)}, testStreams()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if opens != 24 || closes != 24 || len(strings.Fields(output.String())) != 24 {
		t.Fatal(opens, closes, output.String())
	}
}

func TestAdmissionFailuresAndCancellationNeverEnterHandler(t *testing.T) {
	for _, mode := range []string{"nil-input", "nil-output", "unknown", "check-error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checks, calls := 0, 0
			r, err := New(Config{Check: func(context.Context) error {
				checks++
				if mode == "check-error" {
					return errors.New("private-check-detail")
				}
				return nil
			}, Commands: []Definition{{Name: "work", Summary: "Do work", Handle: func(context.Context, Values, Streams) error { calls++; return nil }}}})
			if err != nil {
				t.Fatal(err)
			}
			streams, name := testStreams(), "work"
			switch mode {
			case "nil-input":
				streams.Stdin = nil
			case "nil-output":
				streams.Stdout = nil
			case "unknown":
				name = "other"
			case "canceled":
				cancel()
			}
			result, err := r.Execute(ctx, name, nil, streams)
			if err == nil || result.Executed || calls != 0 || mode != "check-error" && checks != 0 || strings.Contains(Diagnostic(err), "private") {
				t.Fatal(result, err, checks, calls)
			}
		})
	}
}
