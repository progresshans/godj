//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
)

type showMigrationsScriptedBackend struct {
	stages   []ProcessStage
	commands []Command
	build    ProcessResult
	runner   ProcessResult
}

func (backend *showMigrationsScriptedBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	command Command,
) ProcessResult {
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	if stage == BuildStage {
		return backend.build
	}
	return backend.runner
}

func TestRunShowMigrationsUsesSeparateProtocolAndCanonicalText(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 2)
	result := showmigrationsprotocol.Result{Rows: []showmigrationsprotocol.Row{
		{App: "authors", Name: "0001_author", Status: showmigrationsprotocol.StatusApplied},
		{App: "blog", Name: "0001_article", Status: showmigrationsprotocol.StatusUnapplied},
		{App: "legacy", Name: "0009_missing", Status: showmigrationsprotocol.StatusUnknown},
	}}
	wire, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{OK: true, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	backend := &showMigrationsScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1,
			Stdout:       append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := RunShowMigrations(ShowMigrationsInvocation{
		Context: context.Background(), CWD: fixture.cwd, Args: []string{"showmigrations"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || report.HasShowMigrationsFailure || !report.HasShowMigrationsResult ||
		report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 ||
		report.DirectChildReaps != 2 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 ||
		report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 ||
		report.ResidualTemp != 0 || !report.RawDiagnosticsDiscarded {
		t.Fatalf("showmigrations success report = %+v", report)
	}
	want := "authors\n [X] 0001_author\nblog\n [ ] 0001_article\nlegacy\n [?] 0009_missing\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("public output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !reflect.DeepEqual(backend.stages, []ProcessStage{BuildStage, ShowMigrationsRunnerStage}) || len(backend.commands) != 2 {
		t.Fatalf("stages=%v commands=%d", backend.stages, len(backend.commands))
	}
	runner := backend.commands[1]
	if len(runner.Argv) != 2 || runner.Argv[1] != showmigrationsprotocol.PrivateArgument ||
		runner.Dir != fixture.project || !bytes.Equal(runner.Stdin, showmigrationsprotocol.RequestDocument()) {
		t.Fatalf("runner command = %+v", runner)
	}
	if filepath.Dir(runner.Argv[0]) == fixture.project {
		t.Fatalf("runner binary was not isolated: %+v", runner)
	}
}

func TestRenderShowMigrationsEscapesKnownAndUnknownIdentitiesInjectively(t *testing.T) {
	t.Parallel()

	result := ShowMigrationsResult{Rows: []showmigrationsprotocol.Row{
		{App: "actual\napp", Name: "known\tname", Status: showmigrationsprotocol.StatusApplied},
		{App: "actual\napp", Name: "unknown\x1bname", Status: showmigrationsprotocol.StatusUnknown},
		{App: `literal\napp`, Name: `literal\tname`, Status: showmigrationsprotocol.StatusUnapplied},
		{App: "unknown\rapp", Name: "missing\x7fname", Status: showmigrationsprotocol.StatusUnknown},
		{App: "안전앱", Name: "이름_日本語", Status: showmigrationsprotocol.StatusApplied},
	}}
	got, err := renderShowMigrations(result)
	if err != nil {
		t.Fatalf("renderShowMigrations() error = %v", err)
	}
	want := "actual\\napp\n" +
		" [X] known\\tname\n" +
		" [?] unknown\\x1bname\n" +
		"literal\\\\napp\n" +
		" [ ] literal\\\\tname\n" +
		"unknown\\rapp\n" +
		" [?] missing\\x7fname\n" +
		"안전앱\n" +
		" [X] 이름_日本語\n"
	if string(got) != want {
		t.Fatalf("renderShowMigrations() = %q, want %q", got, want)
	}
	if strings.ContainsRune(string(got), '\x1b') || strings.ContainsRune(string(got), '\t') ||
		strings.ContainsRune(string(got), '\r') || strings.ContainsRune(string(got), '\x7f') ||
		strings.Count(string(got), "\n") != 9 {
		t.Fatalf("renderShowMigrations() retained a raw injected control: %q", got)
	}
}

func TestEscapeShowMigrationsIdentityIsGraphicAndRoundTrips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "safe ascii", input: "alpha_0001-name", want: "alpha_0001-name"},
		{name: "safe unicode", input: "도서관_日本語", want: "도서관_日本語"},
		{name: "quote and backslash", input: `quote"slash\`, want: `quote\"slash\\`},
		{
			name:  "control and format",
			input: "line\ncarriage\rtab\tnul\x00esc\x1bdel\x7fc1\u0085format\u200b",
			want:  `line\ncarriage\rtab\tnul\x00esc\x1bdel\x7fc1\u0085format\u200b`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := escapeShowMigrationsIdentity(test.input)
			if got != test.want {
				t.Fatalf("escapeShowMigrationsIdentity(%q) = %q, want %q", test.input, got, test.want)
			}
			decoded, err := strconv.Unquote(`"` + got + `"`)
			if err != nil || decoded != test.input {
				t.Fatalf("escaped identity did not round-trip: got=%q decoded=%q err=%v", got, decoded, err)
			}
			for _, character := range got {
				if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
					t.Fatalf("escaped identity %q contains raw control/format rune %U", got, character)
				}
			}
		})
	}

	actualNewline := escapeShowMigrationsIdentity("alpha\nname")
	literalSequence := escapeShowMigrationsIdentity(`alpha\nname`)
	if actualNewline == literalSequence || actualNewline != `alpha\nname` || literalSequence != `alpha\\nname` {
		t.Fatalf("newline collision: actual=%q literal=%q", actualNewline, literalSequence)
	}
}

func TestRenderShowMigrationsEscapesLeadingSpaceAppHeading(t *testing.T) {
	t.Parallel()

	result := ShowMigrationsResult{Rows: []showmigrationsprotocol.Row{
		{App: " [X] forged", Name: "0001_known", Status: showmigrationsprotocol.StatusApplied},
		{App: `\x20[X] forged`, Name: "0001_missing", Status: showmigrationsprotocol.StatusUnknown},
		{App: "alpha", Name: "0001_safe", Status: showmigrationsprotocol.StatusUnapplied},
	}}
	got, err := renderShowMigrations(result)
	if err != nil {
		t.Fatalf("renderShowMigrations() error = %v", err)
	}
	want := "\\x20[X] forged\n" +
		" [X] 0001_known\n" +
		"\\\\x20[X] forged\n" +
		" [?] 0001_missing\n" +
		"alpha\n" +
		" [ ] 0001_safe\n"
	if string(got) != want {
		t.Fatalf("renderShowMigrations() = %q, want %q", got, want)
	}
	if strings.HasPrefix(string(got), " [X] forged\n") {
		t.Fatalf("leading-space app rendered as a forged row: %q", got)
	}

	actualLeadingSpace := escapeShowMigrationsApp(" alpha")
	literalSequence := escapeShowMigrationsApp(`\x20alpha`)
	if actualLeadingSpace == literalSequence || actualLeadingSpace != `\x20alpha` || literalSequence != `\\x20alpha` {
		t.Fatalf("leading-space collision: actual=%q literal=%q", actualLeadingSpace, literalSequence)
	}
	if got := escapeShowMigrationsApp("ordinary_app"); got != "ordinary_app" {
		t.Fatalf("ordinary app escaped to %q", got)
	}
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "non-breaking space", input: "\u00a0alpha", want: `\u00a0alpha`},
		{name: "thin space", input: "\u2009alpha", want: `\u2009alpha`},
		{name: "ideographic space", input: "\u3000alpha", want: `\u3000alpha`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := escapeShowMigrationsApp(test.input)
			if got != test.want {
				t.Fatalf("escapeShowMigrationsApp(%q) = %q, want %q", test.input, got, test.want)
			}
			literal := escapeShowMigrationsApp(test.want)
			if got == literal {
				t.Fatalf("leading Unicode space collided with literal escape: actual=%q literal=%q", got, literal)
			}
			decoded, err := strconv.Unquote(`"` + got + `"`)
			if err != nil || decoded != test.input {
				t.Fatalf("leading Unicode space did not round-trip: got=%q decoded=%q err=%v", got, decoded, err)
			}
		})
	}
}

func TestRunShowMigrationsEmptyAndExactArguments(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{
		OK: true, Result: showmigrationsprotocol.Result{},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &showMigrationsScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunShowMigrations(ShowMigrationsInvocation{
		Context: context.Background(), CWD: filepath.Dir(fixture.project),
		Args:        []string{"showmigrations", "--project", filepath.Join(fixture.project, descriptorName)},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || stdout.String() != "(no migrations)\n" || stderr.Len() != 0 {
		t.Fatalf("empty explicit listing = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}

	for _, arguments := range [][]string{
		{}, {"showmigrations", "--project"}, {"showmigrations", "--project", ""},
		{"showmigrations", "--project", "godj.toml", "extra"}, {"showmigrations", "--plan"},
	} {
		arguments := append([]string(nil), arguments...)
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			var invalidOut, invalidErr bytes.Buffer
			invalid := RunShowMigrations(ShowMigrationsInvocation{
				Context: context.Background(), CWD: filepath.Join(t.TempDir(), "absent"), Args: arguments,
				Stdout: &invalidOut, Stderr: &invalidErr,
				Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
					t.Fatal("backend called after invalid showmigrations arguments")
					return ProcessResult{}
				}),
			})
			if invalid.ExitCode != 2 || !invalid.HasShowMigrationsFailure ||
				invalid.ShowMigrationsFailure.Category != showmigrationsprotocol.CategoryCommand ||
				invalid.ShowMigrationsFailure.Code != showmigrationsprotocol.CodeInvalidArguments ||
				invalid.AncestorDirectoriesInspected != 0 || invalid.BuildCalls != 0 || invalid.RunnerCalls != 0 ||
				invalidOut.Len() != 0 || invalidErr.String() != showmigrationsprotocol.CategoryCommand+"/"+showmigrationsprotocol.CodeInvalidArguments+"\n" {
				t.Fatalf("invalid args %q = %+v stdout=%q stderr=%q", arguments, invalid, invalidOut.String(), invalidErr.String())
			}
		})
	}
}

func TestRunShowMigrationsCancellationBoundaries(t *testing.T) {
	t.Run("pre-canceled before selection", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout, stderr bytes.Buffer
		report := RunShowMigrations(ShowMigrationsInvocation{
			Context: ctx,
			CWD:     filepath.Join(t.TempDir(), "absent"),
			Args:    []string{"showmigrations"},
			Stdout:  &stdout,
			Stderr:  &stderr,
			Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
				t.Fatal("backend called after pre-acquisition cancellation")
				return ProcessResult{}
			}),
		})
		if report.ExitCode != 3 || !report.HasShowMigrationsFailure ||
			report.ShowMigrationsFailure != (ShowMigrationsFailure{
				Category: showmigrationsprotocol.CategoryProcess,
				Code:     showmigrationsprotocol.CodeProjectCanceled,
			}) || report.BuildCalls != 0 || report.RunnerCalls != 0 || stdout.Len() != 0 ||
			stderr.String() != showmigrationsprotocol.CategoryProcess+"/"+showmigrationsprotocol.CodeProjectCanceled+"\n" {
			t.Fatalf("pre-canceled report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
		}
	})

	t.Run("closed snapshot precedes later cancellation", func(t *testing.T) {
		fixture := newGlobalFixture(t, 0)
		wire, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{OK: true})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := backendFunc(func(_ context.Context, _ <-chan struct{}, stage ProcessStage, _ Command) ProcessResult {
			switch stage {
			case BuildStage:
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
			case ShowMigrationsRunnerStage:
				cancel()
				return ProcessResult{
					Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
					StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
				}
			default:
				t.Fatalf("unexpected process stage %d", stage)
				return ProcessResult{}
			}
		})
		var stdout, stderr bytes.Buffer
		report := RunShowMigrations(ShowMigrationsInvocation{
			Context: ctx, CWD: fixture.project, Args: []string{"showmigrations"},
			Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
		})
		if !errors.Is(ctx.Err(), context.Canceled) || report.ExitCode != 0 || !report.HasShowMigrationsResult ||
			report.HasShowMigrationsFailure || report.BuildCalls != 1 || report.RunnerCalls != 1 ||
			stdout.String() != "(no migrations)\n" || stderr.Len() != 0 {
			t.Fatalf("completed snapshot report=%+v stdout=%q stderr=%q ctx=%v", report, stdout.String(), stderr.String(), ctx.Err())
		}
	})
}

func TestRunShowMigrationsFailurePrecedenceAndAtomicPublication(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	logical := showmigrationsprotocol.Failure{
		Category: showmigrationsprotocol.CategoryHistory,
		Code:     "inconsistent_applied_history",
	}
	wire, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{Failure: logical})
	if err != nil {
		t.Fatal(err)
	}
	backend := &showMigrationsScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunShowMigrations(ShowMigrationsInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"showmigrations"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 1 || !report.HasShowMigrationsFailure || report.ShowMigrationsFailure != logical ||
		stdout.Len() != 0 || stderr.String() != logical.Category+"/"+logical.Code+"\n" {
		t.Fatalf("logical failure = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}

	successWire, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{
		OK: true, Result: showmigrationsprotocol.Result{Rows: []showmigrationsprotocol.Row{{
			App: "alpha", Name: "0001", Status: showmigrationsprotocol.StatusApplied,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend = &showMigrationsScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: successWire,
			StdoutScalar: StreamScalar{RetainedBytes: len(successWire)}},
	}
	short := &shortWriter{}
	shortReport := RunShowMigrations(ShowMigrationsInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"showmigrations"},
		Environment: fixture.environment, Stdout: short, Stderr: &bytes.Buffer{}, Backend: backend,
	})
	if shortReport.ExitCode != 3 || !shortReport.HasShowMigrationsFailure ||
		shortReport.HasShowMigrationsResult || shortReport.ShowMigrationsFailure != showMigrationsInternalFailure() ||
		shortReport.UserStdoutWrites != 1 || shortReport.PartialStdoutWrites != 1 {
		t.Fatalf("short publication = %+v", shortReport)
	}
}
