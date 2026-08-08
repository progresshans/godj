package migrations

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
)

func TestLoadAppliedStateCopiesAndValidatesReaderRecords(t *testing.T) {
	t.Parallel()

	t.Run("copies successful reader result", func(t *testing.T) {
		records := []backend.AppliedMigration{{App: alpha1.App, Name: alpha1.Name}}
		reader := &fakeAppliedMigrationReader{records: records}
		applied, err := LoadAppliedState(context.Background(), reader)
		if err != nil {
			t.Fatalf("LoadAppliedState() error = %v", err)
		}
		records[0] = backend.AppliedMigration{App: alpha2.App, Name: alpha2.Name}

		if _, exists := applied.keys[alpha1]; !exists {
			t.Fatalf("applied keys = %v, want copied key %v", applied.keys, alpha1)
		}
		if _, exists := applied.keys[alpha2]; exists {
			t.Fatalf("applied keys retained caller mutation %v", applied.keys)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		applied, err := LoadAppliedState(context.Background(), &fakeAppliedMigrationReader{})
		if err != nil {
			t.Fatalf("LoadAppliedState() error = %v", err)
		}
		if len(applied.keys) != 0 {
			t.Fatalf("applied keys = %v, want empty", applied.keys)
		}
	})

	t.Run("invalid identity uses planning taxonomy", func(t *testing.T) {
		reader := &fakeAppliedMigrationReader{records: []backend.AppliedMigration{
			{App: "z", Name: ""},
			{App: "", Name: "a"},
		}}
		_, err := LoadAppliedState(context.Background(), reader)
		assertPlanningError(t, err, CategoryHistory, CodeInvalidAppliedState, MigrationKey{Name: "a"}, MigrationKey{})
	})

	t.Run("duplicate identity uses planning taxonomy", func(t *testing.T) {
		reader := &fakeAppliedMigrationReader{records: []backend.AppliedMigration{
			{App: alpha1.App, Name: alpha1.Name},
			{App: alpha1.App, Name: alpha1.Name},
		}}
		_, err := LoadAppliedState(context.Background(), reader)
		assertPlanningError(t, err, CategoryHistory, CodeDuplicateApplied, alpha1, MigrationKey{})
	})

	t.Run("unknown identity is preserved", func(t *testing.T) {
		unknown := MigrationKey{App: "legacy", Name: "0009_removed"}
		reader := &fakeAppliedMigrationReader{records: []backend.AppliedMigration{{App: unknown.App, Name: unknown.Name}}}
		applied, err := LoadAppliedState(context.Background(), reader)
		if err != nil {
			t.Fatalf("LoadAppliedState() error = %v", err)
		}
		if _, exists := applied.keys[unknown]; !exists {
			t.Fatalf("applied keys = %v, want unknown key %v preserved", applied.keys, unknown)
		}
	})
}

func TestLoadAppliedStateReaderAndContextFailures(t *testing.T) {
	t.Parallel()

	t.Run("nil context", func(t *testing.T) {
		reader := &fakeAppliedMigrationReader{}
		_, err := LoadAppliedState(nil, reader)
		assertRecorderReadError(t, err, nil)
		if got := reader.callCount(); got != 0 {
			t.Fatalf("reader calls = %d, want 0", got)
		}
	})

	t.Run("nil reader", func(t *testing.T) {
		_, err := LoadAppliedState(context.Background(), nil)
		assertRecorderReadError(t, err, nil)
	})

	t.Run("typed nil reader", func(t *testing.T) {
		var reader *fakeAppliedMigrationReader
		_, err := LoadAppliedState(context.Background(), reader)
		assertRecorderReadError(t, err, nil)
	})

	t.Run("pre-canceled context does not call reader", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := &fakeAppliedMigrationReader{}
		_, err := LoadAppliedState(ctx, reader)
		assertRecorderReadError(t, err, context.Canceled)
		if got := reader.callCount(); got != 0 {
			t.Fatalf("reader calls = %d, want 0", got)
		}
	})

	t.Run("reader failure preserves cause", func(t *testing.T) {
		cause := errors.New("read sentinel")
		reader := &fakeAppliedMigrationReader{
			records: []backend.AppliedMigration{{App: alpha1.App, Name: alpha1.Name}},
			err:     cause,
		}
		_, err := LoadAppliedState(context.Background(), reader)
		assertRecorderReadError(t, err, cause)
	})

	t.Run("in-flight cancellation preserves cause", func(t *testing.T) {
		entered := make(chan struct{})
		reader := &fakeAppliedMigrationReader{read: func(ctx context.Context) ([]backend.AppliedMigration, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := LoadAppliedState(ctx, reader)
			result <- err
		}()
		<-entered
		cancel()
		assertRecorderReadError(t, <-result, context.Canceled)
	})

	t.Run("late cancellation does not discard successful snapshot", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		reader := &fakeAppliedMigrationReader{read: func(context.Context) ([]backend.AppliedMigration, error) {
			cancel()
			return []backend.AppliedMigration{{App: alpha1.App, Name: alpha1.Name}}, nil
		}}
		applied, err := LoadAppliedState(ctx, reader)
		if err != nil {
			t.Fatalf("LoadAppliedState() error = %v", err)
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("context error = %v, want canceled", ctx.Err())
		}
		if _, exists := applied.keys[alpha1]; !exists {
			t.Fatalf("applied keys = %v, want %v", applied.keys, alpha1)
		}
	})
}

func TestLoadAppliedStateRepeatedAndConcurrentReadsAreIndependent(t *testing.T) {
	t.Parallel()

	records := []backend.AppliedMigration{
		{App: alpha1.App, Name: alpha1.Name},
		{App: alpha2.App, Name: alpha2.Name},
	}
	reader := &fakeAppliedMigrationReader{read: func(context.Context) ([]backend.AppliedMigration, error) {
		return append([]backend.AppliedMigration(nil), records...), nil
	}}
	planner := mustPlanner(t, migration(alpha1), migration(alpha2, alpha1), migration(alpha3, alpha2))

	const workers = 32
	const iterations = 50
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				applied, err := LoadAppliedState(context.Background(), reader)
				if err != nil {
					errorsChannel <- err
					return
				}
				plan, err := planner.Plan(applied, NamedTarget(alpha3))
				if err != nil {
					errorsChannel <- err
					return
				}
				if want := []PlanStep{forward(alpha3)}; !reflect.DeepEqual(plan, want) {
					errorsChannel <- errors.New("concurrent loaded state produced a different plan")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
	if got, want := reader.callCount(), workers*iterations; got != want {
		t.Fatalf("reader calls = %d, want %d", got, want)
	}
}

func TestMigrationBackendHistorySourceDoesNotImportMigrationCore(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("backend")
	if err != nil {
		t.Fatalf("read backend directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filename := filepath.Join("backend", entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", imported.Path.Value, filename, err)
			}
			if path == "github.com/progresshans/godj/migrations" {
				t.Fatalf("%s imports top-level migration core %q", filename, path)
			}
		}
	}
}

type fakeAppliedMigrationReader struct {
	mu      sync.Mutex
	records []backend.AppliedMigration
	err     error
	read    func(context.Context) ([]backend.AppliedMigration, error)
	calls   int
}

func (r *fakeAppliedMigrationReader) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	r.mu.Lock()
	r.calls++
	read := r.read
	records := r.records
	err := r.err
	r.mu.Unlock()
	if read != nil {
		return read(ctx)
	}
	return records, err
}

func (r *fakeAppliedMigrationReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func assertRecorderReadError(t *testing.T, err error, cause error) *RecorderError {
	t.Helper()
	var recorderError *RecorderError
	if !errors.As(err, &recorderError) {
		t.Fatalf("error = %#v, want *RecorderError", err)
	}
	if recorderError.Category != CategoryRecorder || recorderError.Code != CodeReadFailed {
		t.Fatalf("recorder error = %#v, want category=%s code=%s", recorderError, CategoryRecorder, CodeReadFailed)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	return recorderError
}
