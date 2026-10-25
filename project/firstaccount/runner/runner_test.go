package runner_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	projectcommand "github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/project/firstaccount/runner"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings/database"
)

const operation = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const password = "First-account-private-password!"

var privateFault = errors.New("private backend/profile/password diagnostic")

type probeBackend struct {
	reads, writes, closes atomic.Int64
	closeMode             string
}

type readOnlyBackend struct{ closes *atomic.Int64 }

func (*readOnlyBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	return privateFault
}
func (backend *readOnlyBackend) Close() error { backend.closes.Add(1); return nil }

func (backend *probeBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	backend.reads.Add(1)
	return privateFault
}
func (backend *probeBackend) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	backend.writes.Add(1)
	return privateFault
}
func (backend *probeBackend) Close() error {
	backend.closes.Add(1)
	switch backend.closeMode {
	case "panic":
		panic(privateFault)
	case "goexit":
		runtime.Goexit()
	case "error":
		return privateFault
	}
	return nil
}

func commandFixture(t *testing.T, open func(context.Context) (firstaccount.Backend, error)) firstaccount.Command {
	t.Helper()
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := project.NewFirstAccountDefinition(hasher, accountmodel.FirstAccountConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "first_name"}, MaxPasswordBytes: 64}, EvidenceSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	command, err := firstaccount.New(definition, open)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func requestFor(t *testing.T, command firstaccount.Command) firstaccountwire.Request {
	t.Helper()
	description, err := command.Description()
	if err != nil {
		t.Fatal(err)
	}
	request := firstaccountwire.Request{Fingerprint: description.Fingerprint, OperationID: operation, Password: []byte(password)}
	for _, field := range description.Fields {
		value := "Private profile"
		if field.Name == description.Login {
			value = "first-runner"
		}
		request.Values = append(request.Values, firstaccount.Value{Name: field.Name, Present: true, Text: []byte(value)})
	}
	return request
}

func runRequest(t *testing.T, command firstaccount.Command, ctx context.Context, request firstaccountwire.Request) firstaccountwire.Response {
	t.Helper()
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(document)
	var output bytes.Buffer
	handled, err := runner.Serve(ctx, command, []string{firstaccountwire.ProvisionArgument}, bytes.NewReader(document), &output)
	if !handled || err != nil {
		t.Fatal("private invocation failed", err)
	}
	response, err := firstaccountwire.DecodeResponse(output.Bytes(), request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), password) || strings.Contains(output.String(), "private") || strings.Contains(output.String(), "first-runner") {
		t.Fatal("response exposed submitted data or causes")
	}
	return response
}

type forbiddenReader struct{ calls atomic.Int64 }

func (reader *forbiddenReader) Read([]byte) (int, error) { reader.calls.Add(1); panic(privateFault) }

func TestDescriptionAndRejectedInputNeverAcquireBackend(t *testing.T) {
	backend := &probeBackend{}
	var opens atomic.Int64
	command := commandFixture(t, func(context.Context) (firstaccount.Backend, error) { opens.Add(1); return backend, nil })
	reader := &forbiddenReader{}
	var output bytes.Buffer
	handled, err := runner.Serve(t.Context(), command, []string{firstaccountwire.DescribeArgument}, reader, &output)
	if !handled || err != nil {
		t.Fatal(err)
	}
	description, err := firstaccountwire.DecodeDescription(output.Bytes())
	if err != nil || description.Description == nil || description.Description.Login != "username" || reader.calls.Load() != 0 {
		t.Fatal("description read sensitive input or lost actual model", err)
	}
	for _, test := range []struct {
		name   string
		change func(*firstaccountwire.Request)
	}{
		{"source_changed", func(r *firstaccountwire.Request) { r.Fingerprint = strings.Repeat("d", 64) }},
		{"missing_required", func(r *firstaccountwire.Request) { r.Values[0].Present = false; r.Values[0].Text = nil }},
		{"empty_required", func(r *firstaccountwire.Request) { r.Values[0].Text = nil }},
		{"wrong_field", func(r *firstaccountwire.Request) { r.Values[0].Name = "encoded_password" }},
		{"wrong_order", func(r *firstaccountwire.Request) { r.Values[0], r.Values[1] = r.Values[1], r.Values[0] }},
		{"declared_password_bound", func(r *firstaccountwire.Request) { r.Password = bytes.Repeat([]byte{'p'}, 65) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := requestFor(t, command)
			test.change(&request)
			response := runRequest(t, command, t.Context(), request)
			if response.Outcome != firstaccountwire.NotCreated || response.Code != firstaccountwire.InvalidInput || response.CleanupFailed {
				t.Fatal("invalid input crossed acquisition boundary", response)
			}
		})
	}
	for _, args := range [][]string{{firstaccountwire.ProvisionArgument, "password"}, {firstaccountwire.DescribeArgument, "extra"}} {
		if handled, err := runner.Serve(t.Context(), command, args, reader, &output); !handled || err == nil {
			t.Fatal("private argv accepted extra input")
		}
	}
	if handled, err := runner.Serve(t.Context(), command, []string{"run", "count-groups"}, reader, &output); handled || err != nil {
		t.Fatal("ordinary command was captured")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output.Reset()
	if handled, err := runner.Serve(ctx, command, []string{firstaccountwire.ProvisionArgument}, reader, &output); !handled || err != nil {
		t.Fatal("pre-entry cancellation failed", err)
	}
	if response, err := firstaccountwire.DecodeResponse(output.Bytes(), operation); err != nil || response.Code != firstaccountwire.Canceled || response.OperationID != "" {
		t.Fatal("pre-entry cancellation read the frame")
	}
	output.Reset()
	if _, err := runner.Serve(t.Context(), command, []string{firstaccountwire.ProvisionArgument}, strings.NewReader("malformed private frame"), &output); err != nil {
		t.Fatal(err)
	}
	if response, err := firstaccountwire.DecodeResponse(output.Bytes(), operation); err != nil || response.Code != firstaccountwire.InvalidRequest || response.OperationID != "" {
		t.Fatal("malformed frame acquired operation authority")
	}
	if opens.Load() != 0 || backend.closes.Load() != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 || reader.calls.Load() != 0 {
		t.Fatal("pure phase performed resource or input I/O")
	}
}

func TestAcquiredResourceClosesOnceOnOpenErrorOrCancellation(t *testing.T) {
	for _, mode := range []string{"error", "cancel", "close_error", "close_panic", "close_goexit", "missing_capability", "nil", "typed_nil", "open_panic", "open_goexit"} {
		t.Run(mode, func(t *testing.T) {
			backend := &probeBackend{closeMode: strings.TrimPrefix(mode, "close_")}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opens := 0
			command := commandFixture(t, func(context.Context) (firstaccount.Backend, error) {
				opens++
				switch mode {
				case "missing_capability":
					return &readOnlyBackend{closes: &backend.closes}, nil
				case "nil":
					return nil, nil
				case "typed_nil":
					var value *probeBackend
					return value, nil
				case "open_panic":
					panic(privateFault)
				case "open_goexit":
					runtime.Goexit()
				case "cancel":
					cancel()
					return backend, context.Canceled
				}
				return backend, privateFault
			})
			response := runRequest(t, command, ctx, requestFor(t, command))
			acquired := !strings.HasPrefix(mode, "open_") && mode != "nil" && mode != "typed_nil"
			if response.Outcome != firstaccountwire.NotCreated || opens != 1 || backend.reads.Load() != 0 || backend.writes.Load() != 0 || acquired && backend.closes.Load() != 1 || !acquired && backend.closes.Load() != 0 {
				t.Fatal("resource ownership was lost", response)
			}
			if strings.HasPrefix(mode, "close_") && !response.CleanupFailed {
				t.Fatal("close failure was lost")
			}
			if mode == "cancel" && response.Code != firstaccountwire.Canceled {
				t.Fatal("open cancellation was lost")
			}
		})
	}
}

type ownedBackend struct {
	database.Backend
	mode           string
	closes, writes *atomic.Int64
	cancel         context.CancelFunc
}

func (backend *ownedBackend) Close() error {
	backend.closes.Add(1)
	err := backend.Backend.Close()
	switch backend.mode {
	case "close_error":
		return errors.Join(err, privateFault)
	case "close_panic":
		panic(privateFault)
	case "close_goexit":
		runtime.Goexit()
	}
	return err
}
func (backend *ownedBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	backend.writes.Add(1)
	err := backend.Backend.(db.CoordinatedRelationAtomic).CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
		if err := write(scope); err != nil {
			return err
		}
		if backend.mode == "rollback" {
			return privateFault
		}
		return nil
	})
	if err != nil {
		return err
	}
	switch backend.mode {
	case "unknown":
		backend.cancel()
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Cause: context.Canceled}
	case "after_commit_panic":
		panic(privateFault)
	case "after_commit_goexit":
		runtime.Goexit()
	case "late_cancel":
		backend.cancel()
	}
	return nil
}

func nativeFixture(t *testing.T) (database.Backend, database.Config) {
	t.Helper()
	config, err := database.SQLite(filepath.Join(t.TempDir(), "first.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	backend, err := database.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	sources := append(identity.MigrationSources(), domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	return backend, config
}

type faultWriter struct {
	mode  string
	calls int
}

func (writer *faultWriter) Write(document []byte) (int, error) {
	writer.calls++
	switch writer.mode {
	case "panic":
		panic(privateFault)
	case "goexit":
		runtime.Goexit()
	case "short":
		return len(document) - 1, nil
	case "overcount":
		return len(document) + 1, nil
	}
	return 0, privateFault
}

func TestNativeOutcomeSurvivesCleanupCancellationAndPublication(t *testing.T) {
	for _, mode := range []string{"success", "close_error", "close_panic", "close_goexit", "late_cancel", "unknown", "after_commit_panic", "after_commit_goexit", "rollback", "output_error", "output_short", "output_overcount", "output_panic", "output_goexit", "close_error_output_error"} {
		t.Run(mode, func(t *testing.T) {
			backend, config := nativeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var opens, closes, writes atomic.Int64
			openMode := mode
			if mode == "close_error_output_error" {
				openMode = "close_error"
			}
			command := commandFixture(t, func(ctx context.Context) (firstaccount.Backend, error) {
				opens.Add(1)
				opened, err := database.Open(ctx, config)
				if err != nil {
					return nil, err
				}
				return &ownedBackend{Backend: opened, mode: openMode, closes: &closes, writes: &writes, cancel: cancel}, nil
			})
			request := requestFor(t, command)
			document, err := firstaccountwire.EncodeRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(document)
			var output, logs bytes.Buffer
			registry, err := projectcommand.New(projectcommand.Config{FirstAccount: command, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Check: func(context.Context) error { t.Error("ordinary command Check called"); return privateFault }})
			if err != nil {
				t.Fatal(err)
			}
			var writer io.Writer = &output
			fault := &faultWriter{mode: strings.TrimPrefix(mode, "output_")}
			outputFailed := strings.Contains(mode, "output_")
			if outputFailed {
				writer = fault
			}
			err = registry.Serve(ctx, []string{firstaccountwire.ProvisionArgument}, projectcommand.Streams{Stdin: bytes.NewReader(document), Stdout: writer, Stderr: &logs})
			if outputFailed {
				wantExit := firstaccountwire.KnownCreatedResponseFailureExitCode
				if mode == "close_error_output_error" {
					wantExit = firstaccountwire.KnownCreatedCleanupResponseFailureExitCode
				}
				if err == nil || projectcommand.ExitCode(err) != wantExit || fault.calls != 1 {
					t.Fatal("publication failure lost confirmed commit", err)
				}
				for _, text := range []string{fmt.Sprintf("%+v", err), projectcommand.Diagnostic(err)} {
					if strings.Contains(text, password) || strings.Contains(text, "private") {
						t.Fatal("error diagnostics leaked cause/input")
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				response, err := firstaccountwire.DecodeResponse(output.Bytes(), operation)
				if err != nil {
					t.Fatal(err)
				}
				want := firstaccountwire.Created
				if mode == "unknown" || strings.HasPrefix(mode, "after_commit_") {
					want = firstaccountwire.Unknown
				}
				if mode == "rollback" {
					want = firstaccountwire.NotCreated
				}
				if response.Outcome != want || response.CleanupFailed != strings.HasPrefix(mode, "close_") || response.OperationID != operation {
					t.Fatal("durable outcome changed", response)
				}
				if want == firstaccountwire.Unknown && response.Code != firstaccountwire.OutcomeUnknown || mode == "rollback" && response.Code != firstaccountwire.PersistenceFailed {
					t.Fatal("unknown/rollback classification changed", response)
				}
			}
			if opens.Load() != 1 || closes.Load() != 1 || writes.Load() != 1 || logs.Len() != 0 {
				t.Fatal("private operation retried, leaked logs or failed to close")
			}
			count, err := models.UserObjects.Using(backend).Count(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			receipt, receiptErr := domain.Lookup(t.Context(), backend, operation)
			if mode == "rollback" {
				if count != 0 || receiptErr == nil {
					t.Fatal("rolled-back attempt left row or receipt")
				}
			} else if count != 1 || receiptErr != nil || receipt.OperationID() != operation || receipt.Sequence() != 1 {
				t.Fatal("reported outcome disagrees with independently reopened DB", receiptErr)
			}
			if mode == "success" {
				response := runRequest(t, command, t.Context(), request)
				if response.Outcome != firstaccountwire.NotCreated || response.Code != firstaccountwire.NotFresh || opens.Load() != 2 || closes.Load() != 2 || writes.Load() != 1 {
					t.Fatal("existing operation was replayed/adopted", response)
				}
			}
		})
	}
}
