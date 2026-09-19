//go:build !race

package compiletest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

type fixtureCompileState struct {
	output                         strings.Builder
	started, finished, buildFailed bool
}

func parseCompileEvents(output []byte, packages []string) ([]compileResult, error) {
	states := make(map[string]*fixtureCompileState, len(packages))
	for _, name := range packages {
		if name == "" || states[name] != nil {
			return nil, fmt.Errorf("invalid or duplicate fixture package %q", name)
		}
		states[name] = &fixtureCompileState{}
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var event struct{ Action, ImportPath, Package, Test, FailedBuild, Output string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode compiler event: %w", err)
		}
		if event.Action == "build-output" || event.Action == "build-fail" {
			state := states[event.ImportPath]
			if state == nil {
				if event.Action == "build-output" {
					continue
				}
				return nil, fmt.Errorf("dependency build failed: %q", event.ImportPath)
			}
			if state.finished || event.Package != "" || event.Test != "" {
				return nil, fmt.Errorf("invalid build event for %q", event.ImportPath)
			}
			if event.Action == "build-fail" {
				if state.buildFailed {
					return nil, fmt.Errorf("duplicate build failure for %q", event.ImportPath)
				}
				state.buildFailed = true
			}
			state.output.WriteString(event.Output)
			continue
		}
		state := states[event.Package]
		if state == nil || event.Test != "" || event.ImportPath != "" || state.finished {
			return nil, fmt.Errorf("unexpected compiler event %q for %q", event.Action, event.Package)
		}
		if event.Action == "start" {
			if state.started {
				return nil, fmt.Errorf("duplicate package start for %q", event.Package)
			}
			state.started = true
			continue
		}
		if !state.started {
			return nil, fmt.Errorf("missing package start for %q", event.Package)
		}
		switch event.Action {
		case "output":
			state.output.WriteString(event.Output)
		case "skip":
			// The staged consumer.go has no test files. This terminal event
			// proves successful compilation; no runtime test skip is accepted.
			want := "?   \t" + event.Package + "\t[no test files]\n"
			if state.buildFailed || event.FailedBuild != "" || !strings.HasSuffix(state.output.String(), want) {
				return nil, fmt.Errorf("unproven compile-only success for %q", event.Package)
			}
			state.finished = true
		case "fail":
			if !state.buildFailed || event.FailedBuild != event.Package {
				return nil, fmt.Errorf("fixture %q did not fail its own build", event.Package)
			}
			state.finished = true
		default:
			return nil, fmt.Errorf("unexpected compiler action %q", event.Action)
		}
	}
	results := make([]compileResult, len(packages))
	for index, name := range packages {
		state := states[name]
		if !state.started || !state.finished {
			return nil, fmt.Errorf("missing terminal result for %q", name)
		}
		results[index].output = state.output.String()
		if state.buildFailed {
			results[index].err = fmt.Errorf("fixture %s did not compile", name)
		}
	}
	return results, nil
}

func TestCompileEventsRequireEveryOwnBuildAndTerminalResult(t *testing.T) {
	events := []map[string]string{
		{"ImportPath": "bad", "Action": "build-output", "Output": "bad diagnostic\n"},
		{"ImportPath": "bad", "Action": "build-fail"},
		{"Package": "good", "Action": "start"},
		{"Package": "good", "Action": "output", "Output": "?   \tgood\t[no test files]\n"},
		{"Package": "good", "Action": "skip"},
		{"Package": "bad", "Action": "start"},
		{"Package": "bad", "Action": "fail", "FailedBuild": "bad"},
	}
	encode := func(events []map[string]string) []byte {
		var buffer bytes.Buffer
		for _, event := range events {
			if err := json.NewEncoder(&buffer).Encode(event); err != nil {
				t.Fatal(err)
			}
		}
		return buffer.Bytes()
	}
	document := encode(events)
	results, err := parseCompileEvents(document, []string{"good", "bad"})
	if err != nil || results[0].err != nil || results[1].err == nil || strings.Contains(results[0].output, "bad diagnostic") || !strings.Contains(results[1].output, "bad diagnostic") {
		t.Fatalf("separate fixture results = %#v, %v", results, err)
	}
	// Every required event is necessary, including the no-test-files proof.
	for index := range events {
		modified := append([]map[string]string(nil), events[:index]...)
		modified = append(modified, events[index+1:]...)
		if index == 0 {
			continue
		} // Diagnostics are asserted by each fixture's caller.
		if _, err := parseCompileEvents(encode(modified), []string{"good", "bad"}); err == nil {
			t.Fatalf("missing event %d was accepted", index)
		}
	}
	for _, suffix := range []string{
		`{"Package":"good","Action":"skip"}`,
		`{"ImportPath":"dependency","Action":"build-fail"}`,
		`{"Package":"other","Action":"start"}`,
		`{"Package":"good","Action":"run","Test":"SkippedRuntimeTest"}`,
		`{"Action":`,
	} {
		if _, err := parseCompileEvents(append(append([]byte(nil), document...), suffix...), []string{"good", "bad"}); err == nil {
			t.Fatalf("invalid trailing event accepted: %s", suffix)
		}
	}
	events[len(events)-1]["FailedBuild"] = "dependency"
	if _, err := parseCompileEvents(encode(events), []string{"good", "bad"}); err == nil {
		t.Fatal("dependency failure was accepted as a fixture's own build failure")
	}
}
