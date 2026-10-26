//go:build darwin || linux

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/accounts"
	"github.com/progresshans/godj/examples/article/internal/commands"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/settings"
)

func TestArticleCommandsProvisionSelectedAccountAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "article.sqlite3")
	installCommandStorage(t, path)
	registry, frame, operation := firstCommandFrame(t, path)
	defer clear(frame)
	var output bytes.Buffer
	if err := registry.Serve(t.Context(), []string{firstaccountwire.ProvisionArgument}, command.Streams{Stdin: bytes.NewReader(frame), Stdout: &output}); err != nil {
		t.Fatal(err)
	}
	result, err := firstaccountwire.DecodeResponse(output.Bytes(), operation)
	if err != nil || result.Outcome != firstaccountwire.Created || result.Code != "" || result.CleanupFailed {
		t.Fatal("selected command outcome", result, err)
	}
	if strings.Contains(output.String(), "Quartz-river-compass-73!") {
		t.Fatal("private response disclosed password")
	}
	verifyCommandAccount(t, path, operation)
}

func TestArticleCommandsMainPreservesCreatedOutcomeOnBrokenStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "article.sqlite3")
	installCommandStorage(t, path)
	_, frame, operation := firstCommandFrame(t, path)
	defer clear(frame)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "article-commands")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/commands")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build command program: %v; diagnostic bytes=%d", err, len(output))
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), binary, firstaccountwire.ProvisionArgument)
	child.Dir, child.Env = root, commandEnvironment(path)
	child.Stdin, child.Stdout = bytes.NewReader(frame), writer
	var stderr bytes.Buffer
	child.Stderr = &stderr
	err = child.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != firstaccountwire.KnownCreatedResponseFailureExitCode || !exited.ProcessState.Exited() || strings.Contains(stderr.String(), "Quartz-river-compass-73!") {
		t.Fatal("private stdout loss did not retain known-created exit", err)
	}
	verifyCommandAccount(t, path, operation)
}

func firstCommandFrame(t *testing.T, path string) (command.Registry, []byte, string) {
	t.Helper()
	environment, err := settings.NewEnvironment(commandEnvironment(path))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := commands.Registry(environment)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := registry.Serve(t.Context(), []string{firstaccountwire.DescribeArgument}, command.Streams{Stdout: &output}); err != nil {
		t.Fatal(err)
	}
	description, err := firstaccountwire.DecodeDescription(output.Bytes())
	if err != nil || description.Description == nil {
		t.Fatal("first account description", err)
	}
	operation := strings.Repeat("7", 64)
	request := firstaccountwire.Request{Fingerprint: description.Description.Fingerprint, OperationID: operation, Password: []byte("Quartz-river-compass-73!"), Values: []firstaccount.Value{{Name: "username", Present: true, Text: []byte("selected-admin")}, {Name: "email"}}}
	defer request.Clear()
	frame, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return registry, frame, operation
}

func installCommandStorage(t *testing.T, path string) {
	t.Helper()
	backend, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(modeldef.MigrationSources()...)
	if err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	if err := errors.Join(err, backend.Close()); err != nil {
		t.Fatal(err)
	}
}

func verifyCommandAccount(t *testing.T, path, operation string) {
	t.Helper()
	backend, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	runtime, err := accounts.New(t.Context(), backend)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := runtime.InspectStartup(t.Context())
	if err != nil || !inspection.Ready() {
		t.Fatal("reopened ownership", err)
	}
	credential, err := runtime.Authenticator().Authenticate(t.Context(), "selected-admin", "Quartz-river-compass-73!")
	actor := credential.Principal()
	if err != nil || !actor.Authenticated() || !actor.Staff() || !actor.Superuser() {
		t.Fatal("reopened selected credential", err)
	}
	receipt, err := domain.Lookup(t.Context(), backend, operation)
	if err != nil || receipt.OperationID() != inspection.Ownership().OperationID() {
		t.Fatal("operation receipt reconciliation", err)
	}
}

func commandEnvironment(path string) []string {
	var result []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key != databaseconfig.SQLiteDatabaseEnv && key != databaseconfig.PostgresURLEnv && key != databaseconfig.PostgresSchemaEnv {
			result = append(result, item)
		}
	}
	return append(result, databaseconfig.SQLiteDatabaseEnv+"="+path)
}
