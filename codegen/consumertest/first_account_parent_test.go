//go:build darwin || linux

package codegen_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/version"
	"golang.org/x/term"
)

//go:embed testdata/firstaccountparent/runtime.go.tmpl
var firstAccountParentRuntime string

// Keep these as independent CI roots. Each builds a real declaration runner
// and generated-aware runtime, then exercises the parent's actual OS owners.
func TestGeneratedFirstAccountParentDefault(t *testing.T) {
	assertGeneratedFirstAccountParent(t, false)
}

func TestGeneratedFirstAccountParentCustom(t *testing.T) {
	assertGeneratedFirstAccountParent(t, true)
}

func writeFirstAccountParentModule(t *testing.T, custom bool) string {
	t.Helper()
	spec := firstAccountConsumerSpec(t, accountLoginVariant{custom: custom})
	extra, err := schema.Build(schema.Definition{AppLabel: "parent_file_fields", Models: []schema.Model{{Name: "fields", GoName: "Fields", Fields: []schema.Field{
		schema.FileField("document", "Document"),
		schema.ImageField("portrait", "Portrait", schema.ImageDimensions("portrait_width", "portrait_height")),
		schema.IntegerField("portrait_width", "PortraitWidth", schema.Nullable(), schema.Editable(false)),
		schema.IntegerField("portrait_height", "PortraitHeight", schema.Nullable(), schema.Editable(false)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range spec.Apps {
		for j := range spec.Apps[i].Schema.Models {
			model := &spec.Apps[i].Schema.Models[j]
			if (ir.ModelIdentity{AppLabel: spec.Apps[i].Schema.AppLabel, ModelName: model.Name}) == spec.Account.Model {
				for _, field := range extra.Models[0].Fields {
					if !field.PrimaryKey {
						model.Fields = append(model.Fields, field.Clone())
					}
				}
			}
		}
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	writeGeneratedTestFile(t, root, codegen.GeneratedManifestPath, bundle.Manifest())
	document, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "modeldef/project.go", []byte(fmt.Sprintf(`package modeldef
import (
	"context"
	"encoding/json"
	"github.com/progresshans/godj/codegen"
)
func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	var spec codegen.ProjectSpec
	if err := ctx.Err(); err != nil { return spec, err }
	err := json.Unmarshal([]byte(%q), &spec)
	return spec, err
}
`, document)))
	writeGeneratedTestFile(t, root, "cmd/projectrunner/main.go", []byte(`package main
import (
	"context"
	"os"
	"example.com/godj-project-bundle/modeldef"
	"github.com/progresshans/godj/project"
)
func main() {
	err := project.Run(context.Background(), project.Config{LoadProjectSpec: modeldef.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil { os.Exit(project.RunnerExitCode(err)) }
}
`))
	parameters := struct{ ModelImport, ModelType, Profile, ProvisionArgument string }{
		ModelImport: "example.com/godj-project-bundle/models", ModelType: "User", Profile: strconv.Quote("first_name"),
		ProvisionArgument: strconv.Quote(firstaccountprotocol.ProvisionArgument),
	}
	if custom {
		parameters.ModelImport, parameters.ModelType, parameters.Profile = "example.com/godj-project-bundle/people", "Member", strconv.Quote("region")
	}
	renderer, err := template.New("runtime").Parse(firstAccountParentRuntime)
	if err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	if err := renderer.Execute(&source, parameters); err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "cmd/commands/main.go", source.Bytes())
	writeGeneratedTestFile(t, root, "godj.toml", []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\ncommands_package = \"./cmd/commands\"\n"))
	if output, err := generatedGoCommand(t.Context(), root, "mod", "tidy").CombinedOutput(); err != nil {
		t.Fatalf("parent module dependencies: %v\n%s", err, output)
	}
	return root
}

func assertGeneratedFirstAccountParent(t *testing.T, custom bool) {
	t.Helper()
	root := writeFirstAccountParentModule(t, custom)
	for _, test := range []struct {
		name, mode, status, code string
		exit                     int
		cleanup, created, tty    bool
	}{
		{"foreign_source", "foreign", "not_created", "first_account_source_mismatch", 3, false, false, false},
		{"invalid_profile", "", "not_created", "invalid_input", 2, false, false, true},
		{"create_and_reopen", "", "created", "", 0, false, true, true},
		{"not_fresh", "", "not_created", "account_domain_not_fresh", 1, false, true, true},
		{"unknown_after_commit", "unknown", "unknown", "outcome_unknown", 3, false, true, true},
		{"created_cleanup", "cleanup", "created", "created_cleanup_failed", 3, true, true, true},
		{"lost_response", "response_loss", "created", "created_output_failed", 3, false, true, true},
		{"invalid_image", "", "not_created", "invalid_input", 2, false, false, true},
		{"partial_publication", "file_partial", "not_created", "persistence_failed", 3, false, false, true},
		{"file_report_failed", "file_report", "created", "created_file_report_failed", 3, false, true, true},
		{"file_report_response_loss", "file_report_response_loss", "created", "created_file_report_failed", 3, false, true, true},
		{"file_report_cleanup", "file_report_cleanup", "created", "created_cleanup_failed", 3, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			tracePath := filepath.Join(state, "runtime-events")
			uploadSource := filepath.Join(state, "local-input")
			if err := os.Mkdir(uploadSource, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(state, "published"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(uploadSource, "private-document.txt"), []byte("parent private document"), 0600); err != nil {
				t.Fatal(err)
			}
			var portrait bytes.Buffer
			if err := png.Encode(&portrait, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
				t.Fatal(err)
			}
			if test.name == "invalid_image" {
				portrait.Reset()
				portrait.WriteString("private invalid image")
			}
			if err := os.WriteFile(filepath.Join(uploadSource, "private-photo.png"), portrait.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			descriptionPath := filepath.Join(state, "description.json")
			environment := append(generatedTestEnvironment(os.Environ()),
				"GODJ_PARENT_DATABASE="+filepath.Join(state, "db.sqlite"), "GODJ_PARENT_TRACE="+tracePath,
				"GODJ_PARENT_STORAGE="+filepath.Join(state, "published"), "GODJ_PARENT_FILE_REPORTS="+state,
				"GODJ_PARENT_UPLOAD_SOURCE="+uploadSource,
				"GODJ_PARENT_DESCRIPTION="+descriptionPath, "GODJ_PARENT_MODE=")
			run := func(arguments ...string) {
				t.Helper()
				command := generatedGoCommand(t.Context(), root, append([]string{"run", "./cmd/commands"}, arguments...)...)
				command.Env = append([]string(nil), environment...)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("independent runtime %v: %v\n%s", arguments, err, output)
				}
			}
			run("init")
			run("inspect", "fresh", "-")
			assertFirstAccountParentTrace(t, tracePath, "")
			document, err := os.ReadFile(descriptionPath)
			if err != nil {
				t.Fatal(err)
			}
			var description firstaccount.Description
			if err := json.Unmarshal(document, &description); err != nil || firstaccount.ValidateDescription(description) != nil {
				t.Fatal("native declaration is invalid")
			}
			var firstOperation string
			if test.name == "not_fresh" {
				created := invokeGeneratedFirstAccountParent(t, root, environment, description, true, false, custom)
				if created.ExitCode != 0 || created.Result.Status != "created" {
					t.Fatalf("nonfresh control was not created: %+v", created)
				}
				firstOperation = created.Result.OperationID
				run("inspect", "created", firstOperation)
				assertFirstAccountParentTrace(t, tracePath, "open\nclose\n")
			}
			environment[len(environment)-1] = "GODJ_PARENT_MODE=" + test.mode
			report := invokeGeneratedFirstAccountParent(t, root, environment, description, test.tty, test.name == "invalid_profile", custom)
			if report.Result.Status != test.status || report.Result.Code != test.code || report.ExitCode != test.exit || report.Result.CleanupFailed != test.cleanup || report.Result.FileReportFailed != strings.HasPrefix(test.mode, "file_report") {
				t.Fatalf("native parent outcome = %+v", report)
			}
			if report.DescriptionCalls != 1 || report.CompatibilityChecks != 2 || report.RuntimeBuildCalls != 1 || report.BuildCalls != 2 ||
				report.DirectChildReaps != report.BuildCalls+report.CompatibilityChecks+report.RunnerCalls || report.PublicationFailed ||
				report.TempCreated != report.TempCleanupAttempts || report.ResidualTemp != 0 || report.CleanupFailed != 0 {
				t.Fatalf("native command lifetime = %+v", report)
			}
			if test.tty {
				if !firstaccountwire.ValidDigest(report.Result.OperationID) || report.Terminal.Checks != 1 || report.Terminal.RestoreAttempts != 1 ||
					report.SensitiveRequestWriteAttempts != 1 || report.SensitiveRequestBytesWritten == 0 || report.Terminal.FileReads != 2 || report.Terminal.FileBytes != len("parent private document")+portrait.Len() {
					t.Fatalf("native private handoff = %+v", report)
				}
			} else if report.Terminal.Checks != 0 || report.SensitiveRequestWriteAttempts != 0 || report.Result.OperationID != "" {
				t.Fatalf("foreign source reached input: %+v", report)
			}
			wantTrace := ""
			if test.created || test.name == "partial_publication" {
				wantTrace = "open\nclose\n"
			}
			if test.name == "not_fresh" {
				wantTrace += "open\nclose\n"
				if report.Result.OperationID == firstOperation {
					t.Fatal("separate invocations shared an operation identity")
				}
			}
			assertFirstAccountParentTrace(t, tracePath, wantTrace)
			fileReportPath := filepath.Join(state, report.Result.OperationID+".json")
			fileDocument, fileErr := os.ReadFile(fileReportPath)
			attempted := test.created && test.name != "not_fresh" || test.name == "partial_publication"
			if attempted {
				var observed struct {
					Operation       string `json:"operation"`
					Created, Failed bool
					Files           []struct {
						Field, Name, Outcome string
						Size                 int64
						Failed               bool
					}
				}
				if fileErr != nil || json.Unmarshal(fileDocument, &observed) != nil || observed.Operation != report.Result.OperationID || len(observed.Files) != 2 || observed.Created != (test.created && test.name != "unknown_after_commit") || observed.Failed != (test.name == "unknown_after_commit" || test.name == "partial_publication") {
					t.Fatal("native process lost publication report", fileErr)
				}
				for _, file := range observed.Files {
					if file.Outcome == "not_published" {
						if test.name != "partial_publication" || file.Field != "portrait" || !file.Failed {
							t.Fatal("unexpected rejected publication")
						}
						continue
					}
					content, err := os.ReadFile(filepath.Join(state, "published", filepath.FromSlash(file.Name)))
					if err != nil {
						t.Fatal(err)
					}
					want := []byte("parent private document")
					if file.Field == "portrait" {
						want = portrait.Bytes()
					}
					if !bytes.Equal(content, want) || file.Size != int64(len(content)) || file.Outcome != "published" {
						t.Fatal("process publication lost exact file bytes")
					}
				}
			} else if !errors.Is(fileErr, os.ErrNotExist) {
				t.Fatal("unattempted request published a file report", fileErr)
			}
			// Inspect through a fresh process and backend. Fault probes affect the
			// actual provisioning owner only, never this durable-state observation.
			environment[len(environment)-1] = "GODJ_PARENT_MODE="
			if test.created {
				operation := report.Result.OperationID
				if firstOperation != "" {
					operation = firstOperation
				}
				run("inspect", "created", operation)
			} else {
				run("inspect", "fresh", "-")
			}
			assertFirstAccountParentTrace(t, tracePath, wantTrace)
		})
	}
}

func assertFirstAccountParentTrace(t *testing.T, path, want string) {
	t.Helper()
	document, err := os.ReadFile(path)
	if want == "" && errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || string(document) != want {
		t.Fatalf("actual runtime opens/closes = %q, %v; want %q", document, err, want)
	}
}

type firstAccountParentOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{}
}

func (output *firstAccountParentOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.buffer.Len()+len(value) > 64<<10 {
		return 0, errors.New("unexpected parent output size")
	}
	n, err := output.buffer.Write(value)
	select {
	case output.ready <- struct{}{}:
	default:
	}
	return n, err
}

func (output *firstAccountParentOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.String()
}

func invokeGeneratedFirstAccountParent(t *testing.T, root string, environment []string, description firstaccount.Description,
	withTTY, invalidProfile, custom bool,
) projectcheck.FirstAccountReport {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	info := compatibility.Current()
	// The caller is this package's in-process development launcher. Both child
	// ABI probes remain real compiled executables with their actual build info.
	info.Framework = version.Info{Module: version.Module, Origin: version.Development}
	stdout := &firstAccountParentOutput{ready: make(chan struct{}, 1)}
	stderr := &firstAccountParentOutput{ready: make(chan struct{}, 1)}
	var master, slave *os.File
	var before *term.State
	if withTTY {
		var err error
		master, slave, err = pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer master.Close()
		defer slave.Close()
		before, err = term.GetState(int(slave.Fd()))
		if err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	var report projectcheck.FirstAccountReport
	go func() {
		report = projectcheck.RunFirstAccount(projectcheck.FirstAccountInvocation{
			Context: ctx, CWD: root, Args: []string{"createsuperuser"}, Environment: environment, Stdin: slave, Stdout: stdout, Stderr: stderr,
			Backend: projectcheck.NativeBackend{Framework: info},
		})
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(40 * time.Second):
			t.Error("native parent did not join its process/TTY owner after cancellation")
		}
	}()
	wait := func(prompt string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Minute)
		defer timer.Stop()
		for !strings.Contains(stderr.String(), prompt) {
			select {
			case <-stderr.ready:
			case <-done:
				t.Fatalf("native parent ended before %q: %+v; output=%q", prompt, report, stderr.String())
			case <-timer.C:
				t.Fatalf("native parent did not reach %q", prompt)
			case <-ctx.Done():
				t.Fatal("parent consumer canceled before input")
			}
		}
	}
	write := func(value string) {
		t.Helper()
		if n, err := io.WriteString(master, value+"\n"); err != nil || n != len(value)+1 {
			t.Fatalf("private PTY input write: %v", err)
		}
	}
	const rawLogin = "  ｐａｒｅｎｔ@example.test  "
	const rawPassword = "parent-private-password"
	if withTTY {
		for _, field := range description.Fields {
			label := strconv.Quote(field.Label)
			omit := !custom && !invalidProfile && field.Name != description.Login && !field.Upload
			if field.HasDefault || !field.Required {
				prompt := "Omit " + label + "? [Y/n]: "
				if field.HasDefault {
					prompt = "Use model default for " + label + "? [Y/n]: "
				}
				wait(prompt)
				if omit {
					write("")
					continue
				}
				write("n")
			}
			if field.Upload {
				wait(label + " (file path): ")
				directory := ""
				for _, value := range environment {
					if strings.HasPrefix(value, "GODJ_PARENT_UPLOAD_SOURCE=") {
						directory = strings.TrimPrefix(value, "GODJ_PARENT_UPLOAD_SOURCE=")
					}
				}
				filename := "private-document.txt"
				if field.Name == "portrait" {
					filename = "private-photo.png"
				}
				write(filepath.Join(directory, filename))
				continue
			}
			wait(label + ": ")
			value := "  NZ  "
			if custom {
				// Static choices use exact declared text; unlike a CharField,
				// a ChoiceField does not strip an input into a valid choice.
				value = "NZ"
			}
			if field.Name == description.Login {
				value = rawLogin
			} else if invalidProfile {
				value = strings.Repeat("x", 151)
				if custom {
					value = "  NZ  "
				}
			}
			write(value)
		}
		wait("Password: ")
		write(rawPassword)
		wait("Password (again): ")
		write(rawPassword)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		t.Fatal("native first-account owner did not finish")
	}
	if withTTY {
		after, err := term.GetState(int(slave.Fd()))
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("native parent did not restore the terminal")
		}
	}
	public, err := json.Marshal(report.Result)
	if err != nil {
		t.Fatal(err)
	}
	want := string(public) + "\n"
	if report.UserStdoutWrites+report.UserStderrWrites != 1 ||
		report.ExitCode == 0 && stdout.String() != want ||
		report.ExitCode != 0 && (stdout.String() != "" || !strings.HasSuffix(stderr.String(), want)) {
		t.Fatalf("parent did not publish its sole closed result: %+v", report)
	}
	for _, secret := range []string{rawLogin, "parent@example.test", rawPassword, "private cleanup failure", "private-document.txt", "private-photo.png", "parent private document"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatal("parent published private input or child diagnostic")
		}
	}
	return report
}
