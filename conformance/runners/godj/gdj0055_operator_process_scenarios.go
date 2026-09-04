//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/conformance/internal/protocol"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/term"
)

const gdj0055OwnedHelperSource = `package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"
)

const magic = "GODJCSU1"
const maximum = 1292

type evidence struct {
	PID int ` + "`json:\"pid\"`" + `
	DescendantPID int ` + "`json:\"descendant_pid\"`" + `
	RequestBytes int ` + "`json:\"request_bytes\"`" + `
	RequestValid bool ` + "`json:\"request_valid\"`" + `
	ArgvSecretOccurrences int ` + "`json:\"argv_secret_occurrences\"`" + `
	EnvironmentSecretOccurrences int ` + "`json:\"environment_secret_occurrences\"`" + `
}

func writeEvidence(value evidence) {
	path := os.Getenv("GDJ0055_EVIDENCE_PATH")
	if path == "" { return }
	document, _ := json.Marshal(value)
	_ = os.WriteFile(path, document, 0600)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "descendant" {
		signal.Ignore(os.Interrupt)
		for { time.Sleep(time.Hour) }
	}
	document, _ := io.ReadAll(io.LimitReader(os.Stdin, maximum+1))
	valid := len(document) >= 12 && len(document) <= maximum && string(document[:8]) == magic
	if valid {
		username := int(binary.BigEndian.Uint16(document[8:10]))
		password := int(binary.BigEndian.Uint16(document[10:12]))
		valid = username >= 1 && username <= 256 && password >= 1 && password <= 1024 && len(document) == 12+username+password
	}
	if !valid { os.Exit(41) }
	passwordLength := int(binary.BigEndian.Uint16(document[10:12]))
	password := document[len(document)-passwordLength:]
	argvOccurrences := 0
	for _, value := range os.Args { argvOccurrences += bytes.Count([]byte(value), password) }
	environmentOccurrences := 0
	for _, value := range os.Environ() { environmentOccurrences += bytes.Count([]byte(value), password) }
	observed := evidence{PID: os.Getpid(), RequestBytes: len(document), RequestValid: valid, ArgvSecretOccurrences: argvOccurrences, EnvironmentSecretOccurrences: environmentOccurrences}
	if os.Getenv("GDJ0055_HELPER_MODE") == "overflow" {
		signal.Ignore(os.Interrupt)
		child := exec.Command(os.Args[0], "descendant")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		child.Env = os.Environ()
		if err := child.Start(); err != nil { os.Exit(42) }
		observed.DescendantPID = child.Process.Pid
		writeEvidence(observed)
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4097))
		_, _ = os.Stderr.Write(document[len(document)-passwordLength:])
		for { time.Sleep(time.Hour) }
	}
	writeEvidence(observed)
	_, _ = io.WriteString(os.Stdout, "{\"protocol_version\":1,\"status\":\"ok\",\"result\":{\"created\":true}}\n")
	_ = strconv.IntSize
}
`

const gdj0055ProvisionHelperSource = `package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/systemstate"
)

const magic = "GODJCSU1"
const maximum = 1292

type evidence struct {
	PID int ` + "`json:\"pid\"`" + `
	DescendantPID int ` + "`json:\"descendant_pid\"`" + `
	RequestBytes int ` + "`json:\"request_bytes\"`" + `
	RequestValid bool ` + "`json:\"request_valid\"`" + `
	ArgvSecretOccurrences int ` + "`json:\"argv_secret_occurrences\"`" + `
	EnvironmentSecretOccurrences int ` + "`json:\"environment_secret_occurrences\"`" + `
	ProvisionAttempts int ` + "`json:\"provision_attempts\"`" + `
	BackendOpens int ` + "`json:\"backend_opens\"`" + `
	ProvisionCalls int ` + "`json:\"provision_calls\"`" + `
	BackendCloses int ` + "`json:\"backend_closes\"`" + `
	RunnerResponseWrites int ` + "`json:\"runner_response_writes\"`" + `
}

func writeEvidence(value evidence) {
	path := os.Getenv("GDJ0055_EVIDENCE_PATH")
	if path == "" { return }
	document, _ := json.Marshal(value)
	_ = os.WriteFile(path, document, 0600)
}

func main() {
	document, _ := io.ReadAll(io.LimitReader(os.Stdin, maximum+1))
	valid := len(document) >= 12 && len(document) <= maximum && string(document[:8]) == magic
	usernameLength, passwordLength := 0, 0
	if valid {
		usernameLength = int(binary.BigEndian.Uint16(document[8:10]))
		passwordLength = int(binary.BigEndian.Uint16(document[10:12]))
		valid = usernameLength >= 1 && usernameLength <= 256 && passwordLength >= 1 && passwordLength <= 1024 && len(document) == 12+usernameLength+passwordLength
	}
	if !valid { os.Exit(41) }
	username := document[12:12+usernameLength]
	password := document[12+usernameLength:]
	observed := evidence{PID: os.Getpid(), RequestBytes: len(document), RequestValid: true}
	for _, value := range os.Args { observed.ArgvSecretOccurrences += bytes.Count([]byte(value), password) }
	for _, value := range os.Environ() { observed.EnvironmentSecretOccurrences += bytes.Count([]byte(value), password) }

	ctx := context.Background()
	backend, err := sqlite.Open(ctx, os.Getenv("GDJ0055_PROVISION_DSN"))
	if err != nil { writeEvidence(observed); os.Exit(42) }
	observed.BackendOpens++
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "gdj0055-workspace-operator", Active: true})
	if err != nil { _ = backend.Close(); observed.BackendCloses++; writeEvidence(observed); os.Exit(43) }
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil { _ = backend.Close(); observed.BackendCloses++; writeEvidence(observed); os.Exit(44) }
	observed.ProvisionAttempts++
	observed.ProvisionCalls++
	err = systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{
		Username: string(username), Password: string(password),
		CredentialPolicy: systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher},
	})
	closeErr := backend.Close()
	observed.BackendCloses++
	clear(document)
	username = nil
	password = nil
	if err != nil || closeErr != nil { writeEvidence(observed); os.Exit(45) }
	observed.RunnerResponseWrites++
	writeEvidence(observed)
	_, _ = io.WriteString(os.Stdout, "{\"protocol_version\":1,\"status\":\"ok\",\"result\":{\"created\":true}}\n")
}
`

type gdj0055ProcessEvidence struct {
	PID                          int  `json:"pid"`
	DescendantPID                int  `json:"descendant_pid"`
	RequestBytes                 int  `json:"request_bytes"`
	RequestValid                 bool `json:"request_valid"`
	ArgvSecretOccurrences        int  `json:"argv_secret_occurrences"`
	EnvironmentSecretOccurrences int  `json:"environment_secret_occurrences"`
	ProvisionAttempts            int  `json:"provision_attempts"`
	BackendOpens                 int  `json:"backend_opens"`
	ProvisionCalls               int  `json:"provision_calls"`
	BackendCloses                int  `json:"backend_closes"`
	RunnerResponseWrites         int  `json:"runner_response_writes"`
}

type gdj0055ProcessHarness struct {
	directory    string
	project      string
	tempBase     string
	helper       string
	evidencePath string
	environment  []string
	username     string
	password     string
	confirmation string
}

func newGDJ0055ProcessHarness(ctx context.Context, mode string) (*gdj0055ProcessHarness, error) {
	return newGDJ0055ProcessHarnessWithSource(ctx, mode, gdj0055OwnedHelperSource)
}

func newGDJ0055ProcessHarnessWithSource(ctx context.Context, mode, helperSource string) (*gdj0055ProcessHarness, error) {
	directory, err := os.MkdirTemp("", "godj-gdj0055-process-")
	if err != nil {
		return nil, err
	}
	harness := &gdj0055ProcessHarness{
		directory:    directory,
		project:      filepath.Join(directory, "project"),
		tempBase:     filepath.Join(directory, "workspaces"),
		helper:       filepath.Join(directory, "owned-helper"),
		evidencePath: filepath.Join(directory, "evidence.json"),
		username:     "gdj0055-terminal-operator",
		password:     "gdj0055-terminal-password",
		confirmation: "gdj0055-terminal-password",
	}
	for _, path := range []string{harness.project, harness.tempBase, filepath.Join(directory, "home"), filepath.Join(directory, "config"), filepath.Join(directory, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			harness.cleanup()
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(harness.project, "godj.toml"), []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"), 0o600); err != nil {
		harness.cleanup()
		return nil, err
	}
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte(helperSource), 0o600); err != nil {
		harness.cleanup()
		return nil, err
	}
	buildContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	moduleCacheCommand := exec.CommandContext(buildContext, "go", "env", "GOMODCACHE")
	moduleCacheCommand.Env = append([]string(nil), os.Environ()...)
	moduleCacheOutput, err := moduleCacheCommand.Output()
	if err != nil {
		harness.cleanup()
		return nil, fmt.Errorf("resolve GDJ-0055 Go module cache: %w", err)
	}
	moduleCache := strings.TrimSpace(string(moduleCacheOutput))
	if !filepath.IsAbs(moduleCache) {
		harness.cleanup()
		return nil, errors.New("resolve GDJ-0055 Go module cache: path is not absolute")
	}
	command := exec.CommandContext(buildContext, "go", "build", "-buildvcs=false", "-trimpath", "-o", harness.helper, source)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(directory, "home"), "GOCACHE=" + filepath.Join(directory, "cache"), "GOMODCACHE=" + moduleCache, "GOENV=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		harness.cleanup()
		return nil, fmt.Errorf("build GDJ-0055 process helper: %w (stdout=%d stderr=%d)", err, stdout.Len(), stderr.Len())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		harness.cleanup()
		return nil, fmt.Errorf("GDJ-0055 process helper build emitted output stdout=%d stderr=%d", stdout.Len(), stderr.Len())
	}
	harness.environment = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(directory, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(directory, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(directory, "cache"),
		"TMPDIR=" + harness.tempBase,
		"GDJ0055_HELPER_MODE=" + mode,
		"GDJ0055_EVIDENCE_PATH=" + harness.evidencePath,
		"LC_ALL=C",
		"TZ=UTC",
	}
	return harness, nil
}

func (harness *gdj0055ProcessHarness) cleanup() {
	if harness == nil || harness.directory == "" {
		return
	}
	_ = os.Chmod(harness.tempBase, 0o700)
	if strings.HasPrefix(filepath.Base(harness.directory), "godj-gdj0055-process-") {
		_ = os.RemoveAll(harness.directory)
	}
	harness.directory = ""
}

type gdj0055BuildBackend struct {
	helper               string
	workspaceParent      string
	failWorkspaceCleanup bool
	buildCalls           int
	commands             []productcheck.Command
	promptSource         *gdj0055SafeBuffer
	promptBeforeBuild    bool
}

func (backend *gdj0055BuildBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	backend.commands = append(backend.commands, command)
	if stage != productcheck.BuildStage || len(command.Argv) != 7 || command.Argv[0] != "go" || command.Argv[4] != "-o" {
		failure := productcheck.Failure{Category: "migration_project_build_error", Code: "project_build_failed"}
		return productcheck.ProcessResult{Failure: &failure, ExitCode: 1}
	}
	backend.buildCalls++
	if backend.promptSource != nil {
		backend.promptBeforeBuild = backend.promptSource.contains("Username: ") ||
			backend.promptSource.contains("Password: ") || backend.promptSource.contains("Password (again): ")
	}
	document, err := os.ReadFile(backend.helper)
	if err == nil {
		err = os.WriteFile(command.Argv[5], document, 0o700)
	}
	clear(document)
	if err == nil {
		err = os.Chmod(command.Argv[5], 0o700)
	}
	if err != nil {
		failure := productcheck.Failure{Category: "migration_project_build_error", Code: "project_build_failed"}
		return productcheck.ProcessResult{Failure: &failure, ExitCode: 1}
	}
	if backend.failWorkspaceCleanup {
		_ = os.Chmod(backend.workspaceParent, 0o500)
	}
	return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
}

type gdj0055SafeBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

func newGDJ0055SafeBuffer() *gdj0055SafeBuffer {
	return &gdj0055SafeBuffer{written: make(chan struct{}, 32)}
}

func (buffer *gdj0055SafeBuffer) Write(document []byte) (int, error) {
	buffer.mu.Lock()
	written, err := buffer.buffer.Write(document)
	buffer.mu.Unlock()
	select {
	case buffer.written <- struct{}{}:
	default:
	}
	return written, err
}

func (buffer *gdj0055SafeBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *gdj0055SafeBuffer) contains(marker string) bool {
	return bytes.Contains(buffer.Bytes(), []byte(marker))
}

func gdj0055WaitPrompt(ctx context.Context, buffer *gdj0055SafeBuffer, marker string) error {
	for !buffer.contains(marker) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for prompt %q: %w", marker, ctx.Err())
		case <-buffer.written:
		}
	}
	return nil
}

type gdj0055TerminalRun struct {
	report        productcheck.CreatesuperuserReport
	stdout        []byte
	stderr        []byte
	stateRestored bool
	backend       *gdj0055BuildBackend
	terminalBytes []byte
}

func (harness *gdj0055ProcessHarness) runTerminal(
	ctx context.Context,
	confirmation string,
	interruptAtPassword bool,
	failWorkspaceCleanup bool,
) (gdj0055TerminalRun, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return gdj0055TerminalRun{}, err
	}
	defer master.Close()
	defer slave.Close()
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		return gdj0055TerminalRun{}, err
	}
	stdout := newGDJ0055SafeBuffer()
	stderr := newGDJ0055SafeBuffer()
	interrupt := make(chan struct{})
	backend := &gdj0055BuildBackend{
		helper: harness.helper, workspaceParent: harness.tempBase,
		failWorkspaceCleanup: failWorkspaceCleanup, promptSource: stderr,
	}
	result := make(chan productcheck.CreatesuperuserReport, 1)
	go func() {
		result <- productcheck.RunCreatesuperuser(productcheck.CreatesuperuserInvocation{
			Context: ctx, CWD: harness.project, Args: []string{"createsuperuser"},
			Environment: append([]string(nil), harness.environment...), Stdin: slave,
			Stdout: stdout, Stderr: stderr, Interrupt: interrupt, Backend: backend,
		})
	}()
	if err := gdj0055WaitPrompt(ctx, stderr, "Username: "); err != nil {
		close(interrupt)
		return gdj0055TerminalRun{}, err
	}
	if _, err := io.WriteString(master, harness.username+"\n"); err != nil {
		close(interrupt)
		return gdj0055TerminalRun{}, err
	}
	if err := gdj0055WaitPrompt(ctx, stderr, "Password: "); err != nil {
		close(interrupt)
		return gdj0055TerminalRun{}, err
	}
	if interruptAtPassword {
		close(interrupt)
	} else {
		if _, err := io.WriteString(master, harness.password+"\n"); err != nil {
			close(interrupt)
			return gdj0055TerminalRun{}, err
		}
		if err := gdj0055WaitPrompt(ctx, stderr, "Password (again): "); err != nil {
			close(interrupt)
			return gdj0055TerminalRun{}, err
		}
		if _, err := io.WriteString(master, confirmation+"\n"); err != nil {
			close(interrupt)
			return gdj0055TerminalRun{}, err
		}
	}
	var report productcheck.CreatesuperuserReport
	select {
	case report = <-result:
	case <-ctx.Done():
		select {
		case <-interrupt:
		default:
			close(interrupt)
		}
		return gdj0055TerminalRun{}, ctx.Err()
	}
	after, stateErr := term.GetState(int(slave.Fd()))
	if stateErr != nil {
		return gdj0055TerminalRun{}, fmt.Errorf("read restored terminal state: %w", stateErr)
	}
	terminalBytes, terminalErr := gdj0055ReadAvailablePTY(master)
	if terminalErr != nil {
		return gdj0055TerminalRun{}, terminalErr
	}
	return gdj0055TerminalRun{
		report: report, stdout: stdout.Bytes(), stderr: stderr.Bytes(),
		stateRestored: reflect.DeepEqual(before, after), backend: backend,
		terminalBytes: terminalBytes,
	}, nil
}

func gdj0055ReadAvailablePTY(master *os.File) (result []byte, resultErr error) {
	if master == nil {
		return nil, errors.New("read GDJ-0055 PTY: nil master")
	}
	fd := int(master.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, fmt.Errorf("read GDJ-0055 PTY: enable nonblocking mode: %w", err)
	}
	defer func() {
		if err := syscall.SetNonblock(fd, false); err != nil && resultErr == nil {
			result = nil
			resultErr = fmt.Errorf("read GDJ-0055 PTY: restore blocking mode: %w", err)
		}
	}()
	result = make([]byte, 0, 256)
	buffer := make([]byte, 256)
	for {
		// os.File.Read waits in Go's poller after EAGAIN on Linux. Read the
		// cached descriptor directly so an empty non-blocking PTY drain returns.
		read, err := syscall.Read(fd, buffer)
		if read > 0 {
			result = append(result, buffer[:read]...)
		}
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				return result, nil
			}
			return nil, fmt.Errorf("read GDJ-0055 PTY: %w", err)
		}
		if read == 0 {
			return result, nil
		}
	}
}

func (harness *gdj0055ProcessHarness) readEvidence() (gdj0055ProcessEvidence, error) {
	document, err := os.ReadFile(harness.evidencePath)
	if err != nil {
		return gdj0055ProcessEvidence{}, err
	}
	var evidence gdj0055ProcessEvidence
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return gdj0055ProcessEvidence{}, err
	}
	return evidence, nil
}

func (harness *gdj0055ProcessHarness) secretOccurrences(extra ...[]byte) (int64, error) {
	var count int64
	secrets := [][]byte{[]byte(harness.password), []byte(harness.confirmation)}
	for _, document := range extra {
		for _, secret := range secrets {
			count += int64(bytes.Count(document, secret))
		}
	}
	err := filepath.WalkDir(harness.directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == harness.helper {
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		defer clear(document)
		for _, secret := range secrets {
			count += int64(bytes.Count(document, secret))
		}
		return nil
	})
	return count, err
}

func gdj0055TTYSecretTransport(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	successHarness, err := newGDJ0055ProcessHarness(ctx, "success")
	if err != nil {
		return protocol.Observation{}, err
	}
	defer successHarness.cleanup()
	success, err := successHarness.runTerminal(ctx, successHarness.confirmation, false, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	evidence, err := successHarness.readEvidence()
	if err != nil {
		return protocol.Observation{}, err
	}
	if success.report.ExitCode != 0 || !success.report.HasCreatesuperuserResult || success.report.HasCreatesuperuserFailure ||
		success.report.BuildCalls != 1 || success.report.RunnerCalls != 1 || success.report.TerminalChecks != 1 ||
		success.report.TerminalRestoreAttempts != 1 || success.report.SensitiveRequestWriteAttempts != 1 ||
		!success.stateRestored || !evidence.RequestValid || evidence.PID == 0 || evidence.PID == os.Getpid() {
		return protocol.Observation{}, fmt.Errorf("successful terminal observation drifted: report=%+v restored=%v evidence=%+v", success.report, success.stateRestored, evidence)
	}
	if bytes.Contains(success.stderr, []byte(successHarness.password)) || bytes.Contains(success.stdout, []byte(successHarness.password)) ||
		bytes.Contains(success.terminalBytes, []byte(successHarness.password)) {
		return protocol.Observation{}, errors.New("successful public terminal output exposed the password")
	}

	errorHarness, err := newGDJ0055ProcessHarness(ctx, "success")
	if err != nil {
		return protocol.Observation{}, err
	}
	defer errorHarness.cleanup()
	errorRun, err := errorHarness.runTerminal(ctx, errorHarness.password+"-mismatch", false, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !errorRun.report.HasCreatesuperuserFailure || errorRun.report.CreatesuperuserFailure.Code != createsuperuserprotocol.CodePasswordMismatch ||
		errorRun.report.RunnerCalls != 0 || errorRun.report.TerminalRestoreAttempts != 1 || !errorRun.stateRestored {
		return protocol.Observation{}, fmt.Errorf("terminal mismatch observation drifted: report=%+v restored=%v", errorRun.report, errorRun.stateRestored)
	}

	interruptHarness, err := newGDJ0055ProcessHarness(ctx, "success")
	if err != nil {
		return protocol.Observation{}, err
	}
	defer interruptHarness.cleanup()
	interruptRun, err := interruptHarness.runTerminal(ctx, interruptHarness.confirmation, true, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !interruptRun.report.HasCreatesuperuserFailure || interruptRun.report.CreatesuperuserFailure.Code != createsuperuserprotocol.CodeProjectInterrupted ||
		interruptRun.report.RunnerCalls != 0 || interruptRun.report.TerminalRestoreAttempts != 1 || !interruptRun.stateRestored {
		return protocol.Observation{}, fmt.Errorf("terminal interrupt observation drifted: report=%+v restored=%v", interruptRun.report, interruptRun.stateRestored)
	}

	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		return protocol.Observation{}, err
	}
	_ = pipeWriter.Close()
	var nonTerminalOut, nonTerminalErr bytes.Buffer
	nonTerminal := productcheck.RunCreatesuperuser(productcheck.CreatesuperuserInvocation{
		Context: ctx, CWD: successHarness.project, Args: []string{"createsuperuser"}, Environment: successHarness.environment,
		Stdin: pipeReader, Stdout: &nonTerminalOut, Stderr: &nonTerminalErr,
		Backend: &gdj0055BuildBackend{helper: successHarness.helper, workspaceParent: successHarness.tempBase},
	})
	_ = pipeReader.Close()
	if !nonTerminal.HasCreatesuperuserFailure || nonTerminal.CreatesuperuserFailure.Code != createsuperuserprotocol.CodeInputNotTerminal || nonTerminal.RunnerCalls != 0 {
		return protocol.Observation{}, fmt.Errorf("non-terminal observation drifted: %+v", nonTerminal)
	}

	maxFrame, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{
		Username: bytes.Repeat([]byte("u"), createsuperuserprotocol.MaxUsernameBytes),
		Password: bytes.Repeat([]byte("p"), createsuperuserprotocol.MaxPasswordBytes),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	defer clear(maxFrame)
	encodingValid := len(maxFrame) == createsuperuserprotocol.MaxRequestBytes && string(maxFrame[:len(createsuperuserprotocol.Magic)]) == createsuperuserprotocol.Magic &&
		int(binary.BigEndian.Uint16(maxFrame[len(createsuperuserprotocol.Magic):])) == createsuperuserprotocol.MaxUsernameBytes &&
		int(binary.BigEndian.Uint16(maxFrame[len(createsuperuserprotocol.Magic)+2:])) == createsuperuserprotocol.MaxPasswordBytes
	wantRequestBytes := len(createsuperuserprotocol.Magic) + 4 + len(successHarness.username) + len(successHarness.password)
	confirmationForwarded := evidence.RequestBytes != wantRequestBytes
	if !encodingValid || confirmationForwarded || evidence.RequestBytes != success.report.SensitiveRequestBytesWritten {
		return protocol.Observation{}, fmt.Errorf("transport facts invalid encoding=%v confirmation=%v evidence=%+v report=%+v", encodingValid, confirmationForwarded, evidence, success.report)
	}
	secretOccurrences, err := successHarness.secretOccurrences(success.stdout, success.stderr, success.terminalBytes)
	if err != nil || secretOccurrences != 0 {
		return protocol.Observation{}, fmt.Errorf("secret occurrence scan=%d: %w", secretOccurrences, err)
	}

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"confirmation":                   protocol.String("required_and_parent_verified"),
			"echo_disabled_for_secret_input": protocol.Boolean(!bytes.Contains(success.terminalBytes, []byte(successHarness.password))),
			"input_mode":                     protocol.String("actual_terminal_only"),
			"terminal_restore": protocol.List(
				protocol.String("success"),
				protocol.String("error"),
				protocol.String("interrupt"),
			),
			"transport": protocol.Object(map[string]protocol.Value{
				"confirmation_forwarded": protocol.Boolean(confirmationForwarded),
				"encoding":               protocol.String("big_endian_bounded_binary"),
				"magic":                  protocol.String(createsuperuserprotocol.Magic),
				"one_shot":               protocol.Boolean(success.report.SensitiveRequestWriteAttempts == 1),
			}),
		}),
		protocol.Object(map[string]protocol.Value{
			"argv_secret_occurrences":        systemStateInt(evidence.ArgvSecretOccurrences),
			"environment_secret_occurrences": systemStateInt(evidence.EnvironmentSecretOccurrences),
			"filesystem_secret_occurrences":  systemStateInt64(secretOccurrences),
		}),
		protocol.Object(map[string]protocol.Value{
			"frame_max_bytes":                         systemStateInt(createsuperuserprotocol.MaxRequestBytes),
			"pipe_writes":                             systemStateInt(success.report.SensitiveRequestWriteAttempts),
			"secret_max_bytes":                        systemStateInt(createsuperuserprotocol.MaxPasswordBytes),
			"terminal_reads_before_project_build":     systemStateInt(systemStateBoolInt(success.backend.promptBeforeBuild)),
			"terminal_reads_before_project_selection": systemStateInt(systemStateBoolInt(success.backend.promptBeforeBuild && success.report.DescriptorReads == 0)),
			"username_max_bytes":                      systemStateInt(createsuperuserprotocol.MaxUsernameBytes),
		}),
	)
}

func gdj0055ObserveKnownCreatedWorkspaceCleanup(
	ctx context.Context,
) (bool, int, gdj0055ProvisionOutcomeFacts, error) {
	fixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, err
	}
	defer fixture.cleanup()
	harness, err := newGDJ0055ProcessHarnessWithSource(ctx, "provision", gdj0055ProvisionHelperSource)
	if err != nil {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, err
	}
	defer harness.cleanup()
	harness.environment = append(harness.environment, "GDJ0055_PROVISION_DSN="+fixture.dsn)
	run, runErr := harness.runTerminal(ctx, harness.confirmation, false, true)
	_ = os.Chmod(harness.tempBase, 0o700)
	if runErr != nil {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, runErr
	}
	wantCode := createsuperuserprotocol.CodeOperatorCreatedWorkspaceCleanupFailed
	if !run.report.KnownCreated || !run.report.HasCreatesuperuserFailure || run.report.CreatesuperuserFailure.Code != wantCode ||
		run.report.HasCreatesuperuserResult || run.report.CleanupFailed != 1 || run.report.ResidualTemp != 1 {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, fmt.Errorf("workspace cleanup report=%+v", run.report)
	}
	evidence, err := harness.readEvidence()
	if err != nil {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, err
	}
	rows, err := systemStateCountRows(ctx, fixture.raw, systemStateCredentialTable)
	if err != nil {
		return false, 0, gdj0055ProvisionOutcomeFacts{}, err
	}
	facts := gdj0055ProvisionOutcomeFacts{
		attempts:             int64(evidence.ProvisionAttempts),
		provisionCalls:       int64(evidence.ProvisionCalls),
		backendOpens:         int64(evidence.BackendOpens),
		backendCloses:        int64(evidence.BackendCloses),
		buildCalls:           int64(run.report.BuildCalls),
		runnerCalls:          int64(run.report.RunnerCalls),
		sensitiveWrites:      int64(run.report.SensitiveRequestWriteAttempts),
		runnerResponseWrites: int64(evidence.RunnerResponseWrites),
	}
	return run.report.KnownCreated, rows, facts, nil
}

func gdj0055SensitiveChildCleanup(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	harness, err := newGDJ0055ProcessHarness(ctx, "overflow")
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	run, err := harness.runTerminal(ctx, harness.confirmation, false, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	evidence, err := harness.readEvidence()
	if err != nil {
		return protocol.Observation{}, err
	}
	if !run.report.HasCreatesuperuserFailure || run.report.HasCreatesuperuserResult || run.report.RunnerCalls != 1 ||
		!run.report.RunnerStdoutTruncated || run.report.DirectChildReaps != 2 ||
		run.report.GroupSIGINTAttempts < 1 || run.report.GroupSIGKILLAttempts < 1 || !run.report.RawDiagnosticsDiscarded ||
		evidence.PID == 0 || evidence.DescendantPID == 0 || !evidence.RequestValid {
		return protocol.Observation{}, fmt.Errorf("sensitive cleanup observation drifted: report=%+v evidence=%+v", run.report, evidence)
	}
	childProcesses := 0
	for _, pid := range []int{evidence.PID, evidence.DescendantPID} {
		if err := syscall.Kill(pid, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
			childProcesses++
		}
	}
	secretOccurrences, err := harness.secretOccurrences(run.stdout, run.stderr)
	if err != nil {
		return protocol.Observation{}, err
	}
	if childProcesses != 0 || secretOccurrences != 0 || len(run.stdout) != 0 || bytes.Contains(run.stderr, []byte(harness.password)) {
		return protocol.Observation{}, fmt.Errorf("cleanup residue children=%d secrets=%d stdout=%d stderr=%d", childProcesses, secretOccurrences, len(run.stdout), len(run.stderr))
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"bounded_response":             protocol.Boolean(run.report.RunnerStdoutTruncated),
			"cancellation":                 protocol.String("process_group_interrupt_then_kill"),
			"direct_child_reaped":          protocol.Boolean(run.report.DirectChildReaps-run.report.BuildCalls == 1),
			"held_pipe_descendant_cleaned": protocol.Boolean(childProcesses == 0),
			"public_diagnostics_redacted":  protocol.Boolean(run.report.RawDiagnosticsDiscarded && len(run.stdout) == 0),
		}),
		protocol.Object(map[string]protocol.Value{
			"child_processes_after_cleanup":      systemStateInt(childProcesses),
			"partial_private_response_published": protocol.Boolean(len(run.stdout) != 0),
			"secret_artifacts":                   systemStateInt64(secretOccurrences),
		}),
		protocol.Object(map[string]protocol.Value{
			"direct_child_reaps":            systemStateInt(run.report.DirectChildReaps - run.report.BuildCalls),
			"private_response_max_bytes":    systemStateInt(createsuperuserprotocol.MaxResponseBytes),
			"raw_child_stderr_publications": systemStateInt(systemStateBoolInt(bytes.Contains(run.stderr, []byte(harness.password)))),
			"secret_occurrences":            systemStateInt64(secretOccurrences),
		}),
	)
}

var _ productcheck.Backend = (*gdj0055BuildBackend)(nil)
var _ io.Writer = (*gdj0055SafeBuffer)(nil)
var _ = strconv.IntSize
