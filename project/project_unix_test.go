//go:build darwin || linux

package project

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

func TestPublicFacadeMatchesLinkedEntrypointAndOwnsInputs(t *testing.T) {
	root := enterProjectRoot(t)

	for _, request := range [][]byte{
		protocol.RequestDocument(),
		[]byte(`{"protocol_version":2,"command":"migrations.check"}`),
		[]byte(`{"protocol_version":1,"command":"other"}`),
	} {
		var publicOutput bytes.Buffer
		publicErr := Run(
			context.Background(),
			Config{MigrationDefinitionRoots: []string{"migrations"}},
			[]string{protocol.PrivateArgument},
			bytes.NewReader(request),
			&publicOutput,
		)
		var linkedOutput bytes.Buffer
		_, linkedErr := linked.Run(
			context.Background(),
			linked.Config{ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"}},
			[]string{protocol.PrivateArgument},
			bytes.NewReader(request),
			&linkedOutput,
		)
		if !bytes.Equal(publicOutput.Bytes(), linkedOutput.Bytes()) || !sameError(publicErr, linkedErr) {
			t.Fatalf("public/linked mismatch for %q: public=%q/%v linked=%q/%v", request, publicOutput.Bytes(), publicErr, linkedOutput.Bytes(), linkedErr)
		}
	}

	roots := []string{"migrations"}
	argv := []string{protocol.PrivateArgument}
	reader := newBlockingReader(protocol.RequestDocument())
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{MigrationDefinitionRoots: roots}, argv, reader, &output)
	}()
	<-reader.started
	roots[0] = "mutated"
	argv[0] = "mutated"
	close(reader.release)
	if err := <-done; err != nil {
		t.Fatalf("mutated public Run = %v", err)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (protocol.Failure{}) || !response.OK {
		t.Fatalf("mutated public response = %+v, %+v, %v", response, failure, failed)
	}
}

func TestPublicFacadePropagatesWriterCancellationAndPrivateArgErrors(t *testing.T) {
	enterProjectRoot(t)
	sentinel := errors.New("writer failed")
	err := Run(
		context.Background(),
		Config{MigrationDefinitionRoots: []string{"migrations"}},
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		errorWriter{err: sentinel},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("writer error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(canceled, Config{}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run = %v", err)
	}
	if err := Run(context.Background(), Config{}, []string{"migrations", "check"}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("public command argv unexpectedly accepted")
	}
	if err := Run(nil, Config{}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context unexpectedly accepted")
	}
}

func TestPublicRequestPrecedesDeletedWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		_ = os.Chdir(original)
		t.Skipf("platform does not allow removing the current directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	var output bytes.Buffer
	err = Run(
		context.Background(),
		Config{},
		[]string{protocol.PrivateArgument},
		bytes.NewReader([]byte(`{"protocol_version":2,"command":"migrations.check"}`)),
		&output,
	)
	if err != nil {
		t.Fatalf("invalid request with deleted cwd = %v", err)
	}
	response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
	if parseFailed || parseFailure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeProjectProtocolIncompatible {
		t.Fatalf("invalid request precedence = %+v, %+v, %v", response, parseFailure, parseFailed)
	}

	output.Reset()
	err = Run(
		context.Background(),
		Config{},
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		&output,
	)
	if err != nil {
		t.Fatalf("valid request with deleted cwd = %v", err)
	}
	response, parseFailure, parseFailed = protocol.ParseResponse(output.Bytes(), true)
	if parseFailed || parseFailure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeSourceDiscoveryFailed {
		t.Fatalf("deleted cwd classification = %+v, %+v, %v", response, parseFailure, parseFailed)
	}
}

func TestPublicFacadeIsRaceFreeWithinFixedWorkingDirectory(t *testing.T) {
	enterProjectRoot(t)
	const calls = 20
	var wait sync.WaitGroup
	failures := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var output bytes.Buffer
			err := Run(
				context.Background(),
				Config{MigrationDefinitionRoots: []string{"migrations"}},
				[]string{protocol.PrivateArgument},
				bytes.NewReader(protocol.RequestDocument()),
				&output,
			)
			response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
			if err != nil || parseFailed || parseFailure != (protocol.Failure{}) || !response.OK {
				failures <- errors.New("concurrent public invocation failed")
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestPublicSurfaceAndDelegationAreExact(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "project_unix.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	exports := make([]string, 0)
	linkedCalls := 0
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if ast.IsExported(value.Name.Name) {
				exports = append(exports, value.Name.Name)
			}
		case *ast.FuncDecl:
			if value.Recv == nil && ast.IsExported(value.Name.Name) {
				exports = append(exports, value.Name.Name)
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				break
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == "linked" {
				linkedCalls++
			}
		}
		return true
	})
	sort.Strings(exports)
	if !reflect.DeepEqual(exports, []string{"Config", "Run"}) {
		t.Fatalf("project exports = %v", exports)
	}
	if linkedCalls != 1 {
		t.Fatalf("linked.Run callsites in public facade = %d, want 1", linkedCalls)
	}
	source, err := os.ReadFile("project_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("os.Exit"), []byte("os.Args"), []byte("os.Stdin"), []byte("os.Stdout"), []byte("conformance")} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("public facade contains forbidden fragment %q", forbidden)
		}
	}
}

func enterProjectRoot(t *testing.T) string {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	return root
}

func sameError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}

type blockingReader struct {
	document []byte
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newBlockingReader(document []byte) *blockingReader {
	return &blockingReader{
		document: append([]byte(nil), document...),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (reader *blockingReader) Read(buffer []byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.release
	if len(reader.document) == 0 {
		return 0, io.EOF
	}
	read := copy(buffer, reader.document)
	reader.document = reader.document[read:]
	return read, nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
