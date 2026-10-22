package consumer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/inventory"
	"github.com/progresshans/godj/query"
)

var clientChecks = []string{
	"malformed_credentials", "invalid_credentials", "prior_permission", "model_defaults", "typed_write", "response_projection",
	"request_projection", "normalized_input", "invalid_length", "canceled_request",
}

type clientReceipt struct {
	Checks  []string         `json:"checks"`
	Rows    map[string]int64 `json:"rows"`
	Evolved *bool            `json:"evolved"`
	Race    *bool            `json:"race"`
}

type clientEvidence struct {
	Version          string            `json:"version"`
	Vendor           string            `json:"vendor"`
	DocumentSHA256   string            `json:"document_sha256"`
	ExecutableSHA256 string            `json:"executable_sha256"`
	ReceiptSHA256    string            `json:"receipt_sha256"`
	SourceFiles      map[string]string `json:"source_files"`
	BuildFlags       string            `json:"build_flags"`
	HTTPRequests     int64             `json:"http_requests"`
	Client           clientReceipt     `json:"client"`
}

func verifyGeneratedClient(t *testing.T, backend resourceBackend) {
	a := newAssembly(t, backend, "generated-client")
	application, document, err := a.httpApplication()
	check(t, err)
	before, err := inventory.AssetObjects.Using(backend).OrderBy(inventory.AssetFields.ID.Asc()).All(t.Context())
	check(t, err)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		application.ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	root := filepath.Join(t.TempDir(), "client")
	check(t, copyClientTree(filepath.Join("..", "client_template"), root))
	locks := map[string][]byte{}
	for _, name := range []string{"go.mod", "go.sum", "ogen.yml"} {
		locks[name], err = os.ReadFile(filepath.Join(root, name))
		check(t, err)
	}
	check(t, os.WriteFile(filepath.Join(root, "schema.json"), document.Bytes(), 0o600))
	clientCommand(t, root, "generate", false, nil, "go", "tool", "ogen", "-loglevel", "warn", "-config", "ogen.yml", "-target", "inventoryapi", "-package", "inventoryapi", "schema.json")
	dependencies := clientCommand(t, root, "dependencies", false, nil, "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	for _, name := range strings.Fields(string(dependencies)) {
		if name == "github.com/progresshans/godj" || strings.HasPrefix(name, "github.com/progresshans/godj/") {
			t.Fatal("generated HTTP client acquired a GoDj dependency")
		}
	}
	executable := filepath.Join(t.TempDir(), "client")
	clientCommand(t, root, "build", clientRace, nil, "go", "build", "-o", executable, ".")
	for name, expected := range locks {
		actual, err := os.ReadFile(filepath.Join(root, name))
		check(t, err)
		if !bytes.Equal(expected, actual) {
			t.Fatal("generated client changed its locked input", name)
		}
	}
	input, err := json.Marshal(struct {
		URL string `json:"url"`
	}{URL: server.URL})
	check(t, err)
	raw := clientCommand(t, root, "execute", clientRace, input, executable)
	var result clientReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	check(t, decoder.Decode(&result))
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		t.Fatal("generated client returned trailing data")
	}
	got, wanted := slices.Clone(result.Checks), slices.Clone(clientChecks)
	slices.Sort(got)
	slices.Sort(wanted)
	if !slices.Equal(got, wanted) || result.Evolved == nil || *result.Evolved != evolved || result.Race == nil || *result.Race != clientRace || requests.Load() != 6 {
		t.Fatal("generated client omitted or duplicated required work, changed mode or retried", result, requests.Load())
	}
	verifyClientDatabase(t, a, before, result)
	version := "baseline"
	if evolved {
		version = "changed"
	}
	path := strings.Split(t.Name(), "/")
	if len(path) != 3 || path[0] != "TestModelResourceAssembly" || path[1] != "sqlite" && path[1] != "postgres" || path[2] != "generated_client_change" {
		t.Fatal("unexpected generated client execution coordinate")
	}
	files, err := clientSourceFiles(root)
	check(t, err)
	binary, err := os.ReadFile(executable)
	check(t, err)
	evidence := clientEvidence{Version: version, Vendor: path[1], DocumentSHA256: clientDigest(document.Bytes()), ExecutableSHA256: clientDigest(binary),
		ReceiptSHA256: clientDigest(raw), SourceFiles: files, BuildFlags: clientFlags(clientRace), HTTPRequests: requests.Load(), Client: result}
	encoded, err := json.Marshal(evidence)
	check(t, err)
	if directory := os.Getenv("GODJ_MODEL_RESOURCE_CLIENT_EVIDENCE"); directory != "" {
		destination := filepath.Join(directory, version, path[1])
		check(t, copyClientTree(root, destination))
		check(t, os.WriteFile(filepath.Join(destination, "client-receipt.json"), raw, 0o600))
		check(t, os.WriteFile(filepath.Join(destination, "evidence.json"), append(encoded, '\n'), 0o600))
	}
	t.Logf("model resource generated client evidence: %s", encoded)
}

func verifyClientDatabase(t *testing.T, a assembly, before []inventory.Asset, receipt clientReceipt) {
	t.Helper()
	after, err := inventory.AssetObjects.Using(a.backend).OrderBy(inventory.AssetFields.ID.Asc()).All(t.Context())
	check(t, err)
	expected := map[string]string{"default": "client-def", "value": "client-value"}
	if !evolved {
		expected["old"] = "client-old"
	}
	if len(receipt.Rows) != len(expected) || len(after) != len(before)+len(expected) {
		t.Fatal("generated client wrote unexpected rows")
	}
	byID := map[int64]inventory.Asset{}
	for _, value := range after {
		byID[value.ID] = value
	}
	for _, value := range before {
		if !reflect.DeepEqual(value, byID[value.ID]) {
			t.Fatal("generated client changed a preexisting row")
		}
	}
	seen := map[int64]bool{}
	for label, code := range expected {
		id := receipt.Rows[label]
		value, found := byID[id]
		if !found || id <= 0 || seen[id] || value.Code != code || value.WorkspaceID != a.workspace.ID || value.Stamp != "server" || value.Enabled != (label != "value") {
			t.Fatal("generated client result does not match the current database", label)
		}
		seen[id] = true
		if label == "value" {
			if value.Note == nil || *value.Note != "client note" {
				t.Fatal("generated input lost the field excluded from changed responses")
			}
		} else if value.Note != nil {
			t.Fatal("generated omitted input changed nullable storage")
		}
		values, err := inventory.AssetObjects.ModelValues(value)
		check(t, err)
		priority := int64(5)
		if label == "value" {
			priority = 9
		}
		if actual, present := values["priority"]; present != evolved || evolved && !actual.Equal(query.Integer(priority)) {
			t.Fatal("generated client new field/default did not reach storage")
		}
	}
}

func clientFlags(race bool) string {
	flags := "-mod=readonly -trimpath"
	if race {
		flags += " -race"
	}
	return flags
}

func clientEnvironment(race bool) []string {
	settings := map[string]string{"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GONOPROXY": "none", "GONOSUMDB": "none", "GOPRIVATE": "", "GOFLAGS": clientFlags(race), "GODEBUG": "", "GORACE": ""}
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replace := settings[name]; !replace && !strings.HasPrefix(name, "GODJ_") {
			environment = append(environment, entry)
		}
	}
	var names []string
	for name := range settings {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		environment = append(environment, name+"="+settings[name])
	}
	return environment
}

type clientCapture struct {
	buffer    bytes.Buffer
	truncated bool
}

func (capture *clientCapture) Len() int      { return capture.buffer.Len() }
func (capture *clientCapture) Bytes() []byte { return capture.buffer.Bytes() }

func (capture *clientCapture) Write(value []byte) (int, error) {
	count := len(value)
	if remaining := 1<<20 - capture.Len(); len(value) > remaining {
		value, capture.truncated = value[:remaining], true
	}
	_, _ = capture.buffer.Write(value)
	return count, nil
}

func clientCommand(t *testing.T, root, stage string, race bool, input []byte, name string, arguments ...string) []byte {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	if outer, present := t.Deadline(); present && outer.Add(-10*time.Second).Before(deadline) {
		deadline = outer.Add(-10 * time.Second)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir, command.Env, command.WaitDelay = root, clientEnvironment(race), 5*time.Second
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr clientCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.truncated || stderr.truncated {
		t.Fatal("generated client command output truncated", stage)
	}
	if err != nil || stage == "execute" && stderr.Len() != 0 {
		if stage == "execute" {
			// Keep only known fixture stages on the diagnostic's first line.
			// The outer compiler summary deliberately redacts arbitrary child
			// output, including continuation lines, URLs and response bodies.
			failure := strings.TrimSpace(string(stderr.Bytes()))
			switch failure {
			case "fixture input", "fixture framing", "malformed client", "malformed credentials",
				"invalid credentials client", "invalid credentials", "prior client", "prior permission response",
				"changed permission", "baseline permission", "writer client", "model defaults", "typed model write",
				"normalized input", "response field change", "request field change", "invalid model length", "canceled request":
			default:
				failure = "unrecognized client failure"
			}
			t.Fatalf("generated client execute: %v; check=%s", err, failure)
		}
		t.Fatalf("generated client %s: %v\n%s\n%s", stage, err, stdout.Bytes(), stderr.Bytes())
	}
	return bytes.Clone(stdout.Bytes())
}

func clientDigest(value []byte) string { return fmt.Sprintf("%x", sha256.Sum256(value)) }

func clientSourceFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return errors.New("generated client source is not regular")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = clientDigest(data)
		return nil
	})
	return files, err
}

func copyClientTree(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("generated client template is not regular")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}
