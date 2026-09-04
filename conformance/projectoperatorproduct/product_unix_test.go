//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	operatorattestation "github.com/progresshans/godj/conformance/projectoperatorproduct/attestation"
	sqlitebackend "github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

var operatorCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

type operatorProvisionResult struct {
	globalPID int
	leafPID   int
}

type operatorProvisionExecution struct {
	operatorProvisionResult
	exitCode   int
	transcript string
	truncated  bool
}

type operatorRuntimeResult struct {
	globalPID int
	leafPID   int
}

type operatorAuthenticationState struct {
	sessionCookie string
	csrfCookie    string
	maskedCSRF    string
}

type operatorHTTPResult struct {
	status      int
	contentType string
	header      http.Header
	cookies     []*http.Cookie
	body        []byte
}

type operatorArticlePage struct {
	Count    int64   `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []struct {
		ID        int64   `json:"id"`
		Title     string  `json:"title"`
		Published bool    `json:"published"`
		Summary   *string `json:"summary"`
	} `json:"results"`
}

func TestGlobalCreatesuperuserExternalSQLiteProduct(t *testing.T) {
	t.Run("durable lifecycle", func(t *testing.T) {
		operatorRunSQLiteBackendProduct(t)
	})
	t.Run("lost private response outcome ownership", func(t *testing.T) {
		operatorRunSQLiteLostResponseProduct(t)
	})
}

func operatorRunSQLiteLostResponseProduct(t *testing.T) {
	t.Helper()
	project := newOperatorExternalProject(t)
	for _, test := range []struct {
		name         string
		mode         string
		wantCategory string
		wantCode     string
	}{
		{
			name:         "known created response write failure",
			mode:         "broken-pipe",
			wantCategory: "operator_project_internal_error",
			wantCode:     "operator_created_output_failed",
		},
		{
			name:         "post-commit child abort is outcome unknown",
			mode:         "abort",
			wantCategory: "operator_project_process_error",
			wantCode:     "operator_provision_outcome_unknown",
		},
		{
			name:         "known created backend close and response failure",
			mode:         "backend-close-broken-pipe",
			wantCategory: "system_state_backend_error",
			wantCode:     "operator_created_backend_cleanup_failed",
		},
		{
			name:         "post-commit empty response is outcome unknown",
			mode:         "empty",
			wantCategory: "operator_project_process_error",
			wantCode:     "operator_provision_outcome_unknown",
		},
		{
			name:         "post-commit malformed response is outcome unknown",
			mode:         "malformed",
			wantCategory: "operator_project_process_error",
			wantCode:     "operator_provision_outcome_unknown",
		},
		{
			name:         "post-commit over-limit response is outcome unknown",
			mode:         "over-limit",
			wantCategory: "operator_project_process_error",
			wantCode:     "operator_provision_outcome_unknown",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := filepath.Join(project.state, "lost-response-"+test.mode+".sqlite3")
			migrateMarker := project.marker(t, "lost-response-migrate-"+test.mode)
			environment := project.sqliteEnvironment(t, database, migrateMarker)
			project.runMigrate(t, environment, migrateMarker, nil)
			operatorAssertSQLiteCounts(t, database, 0, 0, 0)

			password := operatorRandomPassword(t)
			defer clear(password)
			username := "lost-response-" + test.mode
			provisionMarker := project.marker(t, "lost-response-provision-"+test.mode)
			provisionEnvironment := operatorEnvironment(environment, map[string]string{
				operatorMarkerEnvironment:       provisionMarker,
				operatorResponseModeEnvironment: test.mode,
			})
			execution := project.runProvisionPTY(t, provisionEnvironment, provisionMarker, username, password)
			wantFailure := test.wantCategory + "/" + test.wantCode
			normalizedTranscript := strings.ReplaceAll(execution.transcript, "\r\n", "\n")
			wantTranscript := "Username: " + username + "\nPassword: \nPassword (again): \n" + wantFailure + "\n"
			if execution.exitCode != 3 || execution.truncated || normalizedTranscript != wantTranscript ||
				strings.Count(execution.transcript, "Username: ") != 1 ||
				strings.Count(execution.transcript, "Password: ") != 1 ||
				strings.Count(execution.transcript, "Password (again): ") != 1 ||
				strings.Count(execution.transcript, wantFailure) != 1 ||
				strings.Contains(execution.transcript, `{"status":"created"}`) {
				t.Fatalf(
					"lost response outcome = mode:%s exit:%d failure:%d created:%t bytes:%d truncated:%t",
					test.mode,
					execution.exitCode,
					strings.Count(execution.transcript, wantFailure),
					strings.Contains(execution.transcript, `{"status":"created"}`),
					len(execution.transcript),
					execution.truncated,
				)
			}
			operatorAssertSQLiteCounts(t, database, 1, 0, 0)
			operatorAssertSQLiteCredential(t, database, username, password)
			operatorAssertSQLiteCounts(t, database, 1, 0, 0)
			project.assertArtifactsExclude(t, password)
			if occurrences := operatorSQLiteRawSecretOccurrences(t, database, password); occurrences != 0 {
				t.Fatalf("lost response raw secret occurrences = %d", occurrences)
			}
		})
	}
	for _, test := range []struct {
		name             string
		stream           string
		responseMode     string
		wantFailureBytes bool
	}{
		{
			name:             "global stdout broken after durable success",
			stream:           "stdout",
			wantFailureBytes: true,
		},
		{
			name:         "global stderr broken after known-created child exit",
			stream:       "stderr",
			responseMode: "broken-pipe",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := filepath.Join(project.state, "lost-public-"+test.stream+".sqlite3")
			migrateMarker := project.marker(t, "lost-public-migrate-"+test.stream)
			environment := project.sqliteEnvironment(t, database, migrateMarker)
			project.runMigrate(t, environment, migrateMarker, nil)
			operatorAssertSQLiteCounts(t, database, 0, 0, 0)

			password := operatorRandomPassword(t)
			defer clear(password)
			username := "lost-public-" + test.stream
			provisionMarker := project.marker(t, "lost-public-provision-"+test.stream)
			provisionEnvironment := operatorEnvironment(environment, map[string]string{
				operatorMarkerEnvironment:       provisionMarker,
				operatorResponseModeEnvironment: test.responseMode,
				operatorHoldEnvironment:         "1000",
			})
			execution := project.runProvisionPublicStreamLoss(
				t, provisionEnvironment, provisionMarker, username, password, test.stream,
			)
			normalizedTranscript := strings.ReplaceAll(execution.transcript, "\r\n", "\n")
			// stdout/stderr are deliberately detached from the input PTY in this
			// branch, so the username's visible terminal echo is not copied into
			// the captured public stream. The three framework prompts/newlines are.
			wantTranscript := "Username: \nPassword: \nPassword (again): \n"
			if test.wantFailureBytes {
				wantTranscript += "operator_project_internal_error/operator_created_output_failed\n"
			}
			if execution.exitCode != 3 || execution.truncated || normalizedTranscript != wantTranscript ||
				strings.Contains(normalizedTranscript, `{"status":"created"}`) {
				t.Fatalf(
					"lost public stream outcome = stream:%s exit:%d bytes:%d want-bytes:%d truncated:%t",
					test.stream, execution.exitCode, len(normalizedTranscript), len(wantTranscript), execution.truncated,
				)
			}
			operatorAssertSQLiteCounts(t, database, 1, 0, 0)
			operatorAssertSQLiteCredential(t, database, username, password)
			operatorAssertSQLiteCounts(t, database, 1, 0, 0)
			project.assertArtifactsExclude(t, password)
			if occurrences := operatorSQLiteRawSecretOccurrences(t, database, password); occurrences != 0 {
				t.Fatalf("lost public stream raw secret occurrences = %d", occurrences)
			}
		})
	}
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
}

func operatorRunSQLiteBackendProduct(t *testing.T) operatorattestation.ObservedFacts {
	t.Helper()
	project := newOperatorExternalProject(t)
	database := filepath.Join(project.state, "operator.sqlite3")
	migrateMarker := project.marker(t, "migrate")
	environment := project.sqliteEnvironment(t, database, migrateMarker)
	migrateLeaf := project.runMigrate(t, environment, migrateMarker, nil)
	operatorAssertSQLiteCounts(t, database, 0, 0, 0)
	schemaBefore := operatorSQLiteSchemaSnapshot(t, database)

	password := operatorRandomPassword(t)
	defer clear(password)
	const username = "external-operator"
	provisionMarker := project.marker(t, "provision")
	provisionEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment: provisionMarker,
		operatorHoldEnvironment:   "1000",
	})
	operatorAssertSecretFreeEnvironment(t, provisionEnvironment, password)
	provision := project.runProvision(t, provisionEnvironment, provisionMarker, username, password)
	operatorAssertSQLiteCredential(t, database, username, password)
	project.assertArtifactsExclude(t, password)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create external operator cookie jar")
	}
	client := operatorHTTPClient(jar)
	t.Cleanup(client.CloseIdleConnections)

	var authentication operatorAuthenticationState
	runtimeAMarker := project.marker(t, "runtime-a")
	runtimeAEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment:       runtimeAMarker,
		operatorRunnerMarkerEnvironment: "",
		operatorHoldEnvironment:         "",
	})
	operatorAssertRuntimeEnvironment(t, runtimeAEnvironment, password)
	runtimeA := project.runRuntime(t, runtimeAEnvironment, runtimeAMarker, password, func(baseURL string) error {
		var phaseErr error
		authentication, phaseErr = operatorExercisePhaseA(client, jar, baseURL, username, password)
		return phaseErr
	})
	operatorAssertSQLiteCounts(t, database, 1, 1, 1)
	operatorAssertSQLiteCredential(t, database, username, password)

	runtimeBMarker := project.marker(t, "runtime-b")
	runtimeBEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment:       runtimeBMarker,
		operatorRunnerMarkerEnvironment: "",
		operatorHoldEnvironment:         "",
	})
	operatorAssertRuntimeEnvironment(t, runtimeBEnvironment, password)
	runtimeB := project.runRuntime(t, runtimeBEnvironment, runtimeBMarker, password, func(baseURL string) error {
		return operatorExercisePhaseB(client, jar, baseURL, authentication)
	})
	operatorAssertSQLiteCounts(t, database, 1, 1, 2)
	operatorAssertSQLiteCredential(t, database, username, password)

	leafPIDs := []int{provision.leafPID, runtimeA.leafPID, runtimeB.leafPID}
	if !operatorDistinctPositivePIDs(leafPIDs...) ||
		operatorContainsPID(leafPIDs, os.Getpid()) || operatorContainsPID(leafPIDs, migrateLeaf) ||
		operatorContainsPID(leafPIDs, provision.globalPID) ||
		operatorContainsPID(leafPIDs, runtimeA.globalPID) ||
		operatorContainsPID(leafPIDs, runtimeB.globalPID) {
		t.Fatalf(
			"operator product leaf identity = provision:%d runtime-a:%d runtime-b:%d migrate:%d globals:%d/%d/%d test:%d",
			provision.leafPID, runtimeA.leafPID, runtimeB.leafPID, migrateLeaf,
			provision.globalPID, runtimeA.globalPID, runtimeB.globalPID, os.Getpid(),
		)
	}
	project.assertArtifactsExclude(t, password, []byte(authentication.sessionCookie), []byte(authentication.csrfCookie))
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	rawSecretOccurrences := operatorSQLiteRawSecretOccurrences(t, database, password)
	if rawSecretOccurrences != 0 {
		t.Fatalf("external operator SQLite forbidden secret sink occurrences = %d", rawSecretOccurrences)
	}
	schemaAfter := operatorSQLiteSchemaSnapshot(t, database)
	schemaDrift := !bytes.Equal(schemaBefore, schemaAfter)
	if schemaDrift {
		t.Fatal("external operator SQLite physical schema drifted across runtime restart")
	}
	return operatorattestation.ObservedFacts{
		Backend:                             operatorattestation.BackendSQLite,
		ProvisionProcesses:                  1,
		RuntimeProcesses:                    2,
		DistinctProcesses:                   int64(len(leafPIDs)),
		ProvisionCalls:                      1,
		CredentialRows:                      1,
		Provisioned:                         true,
		AdminAuthenticated:                  true,
		APIAuthenticated:                    true,
		DistinctProcessRestart:              runtimeA.leafPID != runtimeB.leafPID,
		ProvisionProcessDistinctFromRuntime: provision.leafPID != runtimeA.leafPID && provision.leafPID != runtimeB.leafPID,
		RestartRawSecretInput:               false,
		RestartStateLoss:                    0,
		SchemaDrift:                         schemaDrift,
		RawSecretOccurrences:                rawSecretOccurrences,
	}
}

func (project *operatorExternalProject) runProvision(
	t *testing.T,
	environment []string,
	marker, username string,
	password []byte,
) operatorProvisionResult {
	t.Helper()
	execution := project.runProvisionPTY(t, environment, marker, username, password)
	transcript := execution.transcript
	if execution.exitCode != 0 || execution.truncated ||
		strings.Count(transcript, "Username: ") != 1 ||
		strings.Count(transcript, "Password: ") != 1 ||
		strings.Count(transcript, "Password (again): ") != 1 ||
		strings.Count(transcript, `{"status":"created"}`) != 1 {
		t.Fatalf(
			"external operator provision shape = exit:%d prompts:%d/%d/%d created:%d bytes:%d truncated:%t",
			execution.exitCode,
			strings.Count(transcript, "Username: "),
			strings.Count(transcript, "Password: "),
			strings.Count(transcript, "Password (again): "),
			strings.Count(transcript, `{"status":"created"}`),
			len(transcript), execution.truncated,
		)
	}
	return execution.operatorProvisionResult
}

func (project *operatorExternalProject) runProvisionPTY(
	t *testing.T,
	environment []string,
	marker, username string,
	password []byte,
) operatorProvisionExecution {
	t.Helper()
	arguments := []string{"createsuperuser", "--project", project.descriptor}
	operatorAssertSecretFreeInvocation(t, project.globalBinary, project.nested, environment, password, arguments...)

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open external operator PTY: %v", err)
	}
	defer func() { _ = master.Close() }()
	command := exec.Command(project.globalBinary, arguments...)
	command.Dir = project.nested
	command.Env = append([]string(nil), environment...)
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start external operator provision: %v", err)
	}
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close external operator PTY slave: %v", err)
	}

	output := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	drained := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(output, master)
		drained <- copyErr
	}()
	waited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = command.Wait()
		close(waited)
	}()
	finished := false
	defer func() {
		if finished {
			return
		}
		operatorStopRunningCommand(command, waited)
		_ = master.Close()
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
		}
	}()

	operatorAwaitPTYPrompt(t, output, waited, "Username: ")
	operatorWritePTYRecord(t, master, []byte(username))
	operatorAwaitPTYPrompt(t, output, waited, "Password: ")
	operatorWritePTYRecord(t, master, password)
	operatorAwaitPTYPrompt(t, output, waited, "Password (again): ")
	operatorWritePTYRecord(t, master, password)

	leafPID := operatorWaitMarkerPIDDone(t, marker, waited)
	operatorAssertLiveProcessMetadata(t, []int{command.Process.Pid, leafPID}, password)
	select {
	case <-waited:
	case <-time.After(operatorCommandTimeout):
		t.Fatal("external operator provision timed out")
	}
	finished = true
	_ = master.Close()
	select {
	case drainErr := <-drained:
		if drainErr != nil && !errors.Is(drainErr, os.ErrClosed) && !strings.Contains(drainErr.Error(), "input/output error") {
			t.Fatalf("drain external operator PTY: %v", drainErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("external operator PTY drain timed out")
	}
	if finalLeafPID := operatorReadSingleMarkerPID(t, marker); finalLeafPID != leafPID {
		t.Fatalf("external operator provision leaf changed after direct-child completion: before=%d after=%d", leafPID, finalLeafPID)
	}
	transcript := output.String()
	if bytes.Contains([]byte(transcript), password) {
		t.Fatal("external operator PTY transcript exposed the raw password")
	}
	if leafPID == command.Process.Pid || leafPID == os.Getpid() {
		t.Fatalf("external operator provision leaf identity = global:%d leaf:%d test:%d", command.Process.Pid, leafPID, os.Getpid())
	}
	operatorRequireProcessAbsent(t, command.Process.Pid)
	operatorRequireProcessAbsent(t, leafPID)
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatal("external operator provision returned a non-exit process error")
		}
		exitCode = exitError.ExitCode()
	}
	return operatorProvisionExecution{
		operatorProvisionResult: operatorProvisionResult{globalPID: command.Process.Pid, leafPID: leafPID},
		exitCode:                exitCode,
		transcript:              transcript,
		truncated:               output.Truncated(),
	}
}

func (project *operatorExternalProject) runProvisionPublicStreamLoss(
	t *testing.T,
	environment []string,
	marker, username string,
	password []byte,
	brokenStream string,
) operatorProvisionExecution {
	t.Helper()
	arguments := []string{"createsuperuser", "--project", project.descriptor}
	operatorAssertSecretFreeInvocation(t, project.globalBinary, project.nested, environment, password, arguments...)

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open public-stream-loss PTY: %v", err)
	}
	defer func() { _ = master.Close() }()
	command := exec.Command(project.globalBinary, arguments...)
	command.Dir = project.nested
	command.Env = append([]string(nil), environment...)
	command.Stdin = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	output := &operatorBoundedOutput{maximum: operatorMaximumOutput}

	var brokenReader, childWriter *os.File
	var drained chan error
	switch brokenStream {
	case "stdout":
		brokenReader, childWriter, err = os.Pipe()
		if err == nil {
			err = brokenReader.Close()
			brokenReader = nil
		}
		command.Stdout = childWriter
		command.Stderr = output
	case "stderr":
		brokenReader, childWriter, err = os.Pipe()
		command.Stdout = output
		command.Stderr = childWriter
	default:
		err = errors.New("unknown public stream loss")
	}
	if err != nil {
		_ = slave.Close()
		if brokenReader != nil {
			_ = brokenReader.Close()
		}
		if childWriter != nil {
			_ = childWriter.Close()
		}
		t.Fatal("prepare public stream loss")
	}
	if brokenStream == "stderr" {
		drained = make(chan error, 1)
		go func(reader *os.File) {
			_, copyErr := io.Copy(output, reader)
			drained <- copyErr
		}(brokenReader)
	}
	if err := command.Start(); err != nil {
		_ = slave.Close()
		if brokenReader != nil {
			_ = brokenReader.Close()
		}
		_ = childWriter.Close()
		t.Fatalf("start public-stream-loss provision: %v", err)
	}
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close public-stream-loss PTY slave: %v", err)
	}
	if err := childWriter.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close public-stream-loss child writer: %v", err)
	}

	waited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = command.Wait()
		close(waited)
	}()
	finished := false
	defer func() {
		if finished {
			return
		}
		operatorStopRunningCommand(command, waited)
		if brokenReader != nil {
			_ = brokenReader.Close()
		}
	}()

	operatorAwaitPTYPrompt(t, output, waited, "Username: ")
	operatorWritePTYRecord(t, master, []byte(username))
	operatorAwaitPTYPrompt(t, output, waited, "Password: ")
	operatorWritePTYRecord(t, master, password)
	operatorAwaitPTYPrompt(t, output, waited, "Password (again): ")
	operatorWritePTYRecord(t, master, password)

	leafPID := operatorWaitMarkerPIDDone(t, marker, waited)
	operatorAssertLiveProcessMetadata(t, []int{command.Process.Pid, leafPID}, password)
	if brokenStream == "stderr" {
		if err := brokenReader.Close(); err != nil {
			t.Fatalf("close global stderr reader: %v", err)
		}
		brokenReader = nil
	}
	select {
	case <-waited:
	case <-time.After(operatorCommandTimeout):
		t.Fatal("public-stream-loss provision timed out")
	}
	finished = true
	if drained != nil {
		select {
		case drainErr := <-drained:
			if drainErr != nil && !errors.Is(drainErr, os.ErrClosed) {
				t.Fatalf("drain public-stream-loss stderr: %v", drainErr)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("public-stream-loss stderr drain timed out")
		}
	}
	if finalLeafPID := operatorReadSingleMarkerPID(t, marker); finalLeafPID != leafPID {
		t.Fatalf("public-stream-loss leaf changed after completion: before=%d after=%d", leafPID, finalLeafPID)
	}
	if bytes.Contains(output.Bytes(), password) {
		t.Fatal("public-stream-loss output exposed the raw password")
	}
	operatorRequireProcessAbsent(t, command.Process.Pid)
	operatorRequireProcessAbsent(t, leafPID)
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatal("public-stream-loss provision returned a non-exit process error")
		}
		exitCode = exitError.ExitCode()
	}
	return operatorProvisionExecution{
		operatorProvisionResult: operatorProvisionResult{globalPID: command.Process.Pid, leafPID: leafPID},
		exitCode:                exitCode,
		transcript:              output.String(),
		truncated:               output.Truncated(),
	}
}

func operatorAwaitPTYPrompt(t *testing.T, output *operatorBoundedOutput, waited <-chan struct{}, prompt string) {
	t.Helper()
	deadline := time.Now().Add(operatorCommandTimeout)
	for {
		if strings.Contains(output.String(), prompt) {
			return
		}
		if output.Truncated() {
			t.Fatalf("external operator PTY truncated before %q", prompt)
		}
		select {
		case <-waited:
			t.Fatalf("external operator provision exited before %q", prompt)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("external operator PTY prompt %q timed out", prompt)
		}
	}
}

func operatorWritePTYRecord(t *testing.T, terminal *os.File, value []byte) {
	t.Helper()
	record := make([]byte, len(value)+1)
	copy(record, value)
	record[len(record)-1] = '\n'
	written, err := terminal.Write(record)
	clear(record)
	if err != nil || written != len(value)+1 {
		t.Fatalf("write external operator PTY record: bytes=%d error=%t", written, err != nil)
	}
}

func operatorWaitMarkerPIDDone(t *testing.T, path string, waited <-chan struct{}) int {
	t.Helper()
	deadline := time.Now().Add(operatorCommandTimeout)
	for {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return operatorReadSingleMarkerPID(t, path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect external operator marker: %v", err)
		}
		select {
		case <-waited:
			t.Fatal("external operator process exited before leaf marker")
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("external operator leaf marker timed out")
		}
	}
}

func (project *operatorExternalProject) runRuntime(
	t *testing.T,
	environment []string,
	marker string,
	password []byte,
	exercise func(string) error,
) operatorRuntimeResult {
	t.Helper()
	if exercise == nil {
		t.Fatal("external operator runtime exercise is nil")
	}
	address := operatorReserveLoopbackAddress(t)
	arguments := []string{"runserver", "--project", project.descriptor, "--addr", address}
	operatorAssertSecretFreeInvocation(t, project.globalBinary, project.nested, environment, password, arguments...)
	stdout := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	stderr := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	command := exec.Command(project.globalBinary, arguments...)
	command.Dir = project.nested
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatalf("start external operator runtime: %v", err)
	}
	waited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = command.Wait()
		close(waited)
	}()
	finished := false
	defer func() {
		if !finished {
			operatorStopRunningCommand(command, waited)
		}
	}()

	leafPID := operatorWaitRuntimeReady(t, address, marker, stdout, stderr, waited, password)
	operatorAssertLiveProcessMetadata(t, []int{command.Process.Pid, leafPID}, password)
	exerciseErr := exercise("http://" + address)
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt external operator runtime: %v", err)
	}
	select {
	case <-waited:
	case <-time.After(operatorCleanupTimeout):
		operatorStopRunningCommand(command, waited)
		t.Fatal("external operator runtime did not stop cooperatively")
	}
	finished = true
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) || (exitError.ExitCode() != 0 && exitError.ExitCode() != 130) {
			t.Fatalf("external operator runtime wait failed: %v", waitErr)
		}
	}
	if stdout.Truncated() || stderr.Truncated() || stderr.String() != "" {
		t.Fatalf("external operator runtime output shape = stdout:%d stderr:%d truncated:%t/%t", len(stdout.String()), len(stderr.String()), stdout.Truncated(), stderr.Truncated())
	}
	wantOutput := "external operator site listening on http://" + address + "\n"
	if stdout.String() != wantOutput {
		t.Fatalf("external operator runtime stdout bytes=%d, want %d", len(stdout.String()), len(wantOutput))
	}
	if bytes.Contains(stdout.Bytes(), password) || bytes.Contains(stderr.Bytes(), password) {
		t.Fatal("external operator runtime output exposed the raw password")
	}
	if exerciseErr != nil {
		t.Fatalf("exercise external operator runtime: %v", exerciseErr)
	}
	if leafPID == command.Process.Pid || leafPID == os.Getpid() {
		t.Fatalf("external operator runtime leaf identity = global:%d leaf:%d test:%d", command.Process.Pid, leafPID, os.Getpid())
	}
	operatorRequireProcessAbsent(t, command.Process.Pid)
	operatorRequireProcessAbsent(t, leafPID)
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	return operatorRuntimeResult{globalPID: command.Process.Pid, leafPID: leafPID}
}

func operatorWaitRuntimeReady(
	t *testing.T,
	address, marker string,
	stdout, stderr *operatorBoundedOutput,
	waited <-chan struct{},
	password []byte,
) int {
	t.Helper()
	want := "external operator site listening on http://" + address + "\n"
	deadline := time.Now().Add(operatorCommandTimeout)
	for {
		if stdout.Truncated() || stderr.Truncated() {
			t.Fatal("external operator runtime output exceeded its bound before readiness")
		}
		if bytes.Contains(stdout.Bytes(), password) || bytes.Contains(stderr.Bytes(), password) {
			t.Fatal("external operator runtime pre-readiness output exposed the raw password")
		}
		if strings.Contains(stdout.String(), want) {
			leaf := operatorReadSingleMarkerPID(t, marker)
			if err := operatorWaitForHTTP(address); err != nil {
				t.Fatal(err)
			}
			return leaf
		}
		select {
		case <-waited:
			t.Fatalf("external operator runtime exited before readiness: stdout=%d stderr=%d", len(stdout.String()), len(stderr.String()))
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("external operator runtime readiness timed out")
		}
	}
}

func operatorStopRunningCommand(command *exec.Cmd, waited <-chan struct{}) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	select {
	case <-waited:
		return
	case <-time.After(5 * time.Second):
	}
	groups, _ := operatorOwnedProcessGroups(command.Process.Pid)
	for _, group := range groups {
		_ = syscall.Kill(-group, syscall.SIGKILL)
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
	}
}

func operatorAssertLiveProcessMetadata(t *testing.T, pids []int, password []byte) {
	t.Helper()
	arguments := []string{"eww", "-p", strings.Trim(strings.ReplaceAll(fmt.Sprint(pids), " ", ","), "[]")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", arguments...)
	output := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil || ctx.Err() != nil || output.Truncated() {
		t.Fatalf("inspect live operator process metadata: error=%t timeout=%t truncated=%t", err != nil, ctx.Err() != nil, output.Truncated())
	}
	document := output.Bytes()
	if len(password) != 0 && bytes.Contains(document, password) {
		t.Fatal("live operator process metadata exposed the raw password")
	}
	for _, retired := range []string{operatorRetiredUsernameEnvironment, operatorRetiredPasswordEnvironment} {
		if bytes.Contains(document, []byte(retired+"=")) {
			t.Fatalf("live operator process metadata retained retired environment key %s", retired)
		}
	}
}

func operatorAssertRuntimeEnvironment(t *testing.T, environment []string, password []byte) {
	t.Helper()
	operatorAssertSecretFreeEnvironment(t, environment, password)
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values[operatorSQLiteDatabaseEnvironment] == "" &&
		(values[operatorPostgresURLEnvironment] == "" || values[operatorPostgresSchemaEnvironment] == "") {
		t.Fatal("external operator runtime environment omitted its durable database location")
	}
	if _, exists := values[operatorRetiredUsernameEnvironment]; exists {
		t.Fatal("external operator runtime environment retained retired username")
	}
	if _, exists := values[operatorRetiredPasswordEnvironment]; exists {
		t.Fatal("external operator runtime environment retained retired password")
	}
}

func operatorExercisePhaseA(
	client *http.Client,
	jar http.CookieJar,
	baseURL, username string,
	password []byte,
) (operatorAuthenticationState, error) {
	loginPage, err := operatorRequest(client, http.MethodGet, baseURL+"/admin/login/", "", "", "")
	if err != nil || loginPage.status != http.StatusOK || loginPage.contentType != "text/html; charset=utf-8" {
		return operatorAuthenticationState{}, errors.New("phase A Admin login page failed")
	}
	masked := operatorExtractCSRF(loginPage.body)
	if masked == "" {
		return operatorAuthenticationState{}, errors.New("phase A Admin login page omitted CSRF")
	}
	preLoginCookie, err := operatorUniqueCookie(loginPage.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil || !preLoginCookie.HttpOnly || preLoginCookie.Value == "" {
		return operatorAuthenticationState{}, errors.New("phase A Admin login CSRF cookie failed")
	}
	form := url.Values{
		"csrfmiddlewaretoken": {masked},
		"username":            {username},
		"password":            {string(password)},
		"next":                {"/admin/articles/"},
	}
	login, err := operatorRequest(client, http.MethodPost, baseURL+"/admin/login/", "application/x-www-form-urlencoded", form.Encode(), "")
	form.Set("password", "")
	if err != nil || login.status != http.StatusFound || login.header.Get("Location") != "/admin/articles/" {
		return operatorAuthenticationState{}, errors.New("phase A Admin authentication failed")
	}
	sessionCookie, err := operatorUniqueCookie(login.cookies, websessionauth.DefaultSessionCookieName)
	if err != nil || !sessionCookie.HttpOnly || sessionCookie.Value == "" {
		return operatorAuthenticationState{}, errors.New("phase A Admin session cookie failed")
	}
	csrfCookie, err := operatorUniqueCookie(login.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil || !csrfCookie.HttpOnly || csrfCookie.Value == "" || csrfCookie.Value == preLoginCookie.Value {
		return operatorAuthenticationState{}, errors.New("phase A Admin CSRF rotation failed")
	}
	adminPage, err := operatorRequest(client, http.MethodGet, baseURL+"/admin/articles/", "", "", "")
	if err != nil || adminPage.status != http.StatusOK || adminPage.contentType != "text/html; charset=utf-8" {
		return operatorAuthenticationState{}, errors.New("phase A authenticated Admin access failed")
	}
	listed, err := operatorRequest(client, http.MethodGet, baseURL+"/api/articles/", "", "", "")
	if err != nil || listed.status != http.StatusOK || listed.contentType != api.JSONContentType {
		return operatorAuthenticationState{}, errors.New("phase A authenticated API access failed")
	}
	page, err := operatorDecodeArticlePage(listed.body)
	if err != nil || page.Count != 0 || len(page.Results) != 0 {
		return operatorAuthenticationState{}, errors.New("phase A API did not start from durable empty Article state")
	}
	apiCSRF := listed.header.Get(websessionauth.DefaultCSRFHeader)
	if !operatorCSRFToken(apiCSRF) {
		return operatorAuthenticationState{}, errors.New("phase A API omitted a masked CSRF token")
	}
	created, err := operatorRequest(client, http.MethodPost, baseURL+"/api/articles/", api.JSONContentType, `{"title":"phase-a-durable","published":true}`, apiCSRF)
	if err != nil || created.status != http.StatusCreated || !bytes.Contains(created.body, []byte(`"title":"phase-a-durable"`)) {
		return operatorAuthenticationState{}, errors.New("phase A authenticated API create failed")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || operatorJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != sessionCookie.Value {
		return operatorAuthenticationState{}, errors.New("phase A browser did not retain the durable session cookie")
	}
	return operatorAuthenticationState{
		sessionCookie: sessionCookie.Value,
		csrfCookie:    csrfCookie.Value,
		maskedCSRF:    apiCSRF,
	}, nil
}

func operatorExercisePhaseB(
	client *http.Client,
	jar http.CookieJar,
	baseURL string,
	state operatorAuthenticationState,
) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || operatorJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != state.sessionCookie ||
		operatorJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != state.csrfCookie {
		return errors.New("phase B browser did not carry the phase A session state")
	}
	stale, err := operatorRequest(client, http.MethodPost, baseURL+"/api/articles/", api.JSONContentType, `{"title":"stale-csrf-must-not-persist"}`, state.maskedCSRF)
	if err != nil || stale.status != http.StatusForbidden {
		return errors.New("phase B stale process-local CSRF token was not rejected")
	}
	adminPage, err := operatorRequest(client, http.MethodGet, baseURL+"/admin/articles/", "", "", "")
	if err != nil || adminPage.status != http.StatusOK || adminPage.contentType != "text/html; charset=utf-8" {
		return errors.New("phase B durable-session Admin access failed")
	}
	listed, err := operatorRequest(client, http.MethodGet, baseURL+"/api/articles/", "", "", "")
	if err != nil || listed.status != http.StatusOK || listed.contentType != api.JSONContentType {
		return errors.New("phase B durable-session API access failed")
	}
	page, err := operatorDecodeArticlePage(listed.body)
	if err != nil || page.Count != 1 || len(page.Results) != 1 || page.Results[0].Title != "phase-a-durable" {
		return fmt.Errorf("phase B API durable Article shape = decode:%t count:%d results:%d first:%t", err == nil, page.Count, len(page.Results), len(page.Results) == 1 && page.Results[0].Title == "phase-a-durable")
	}
	freshCSRF := listed.header.Get(websessionauth.DefaultCSRFHeader)
	if !operatorCSRFToken(freshCSRF) || freshCSRF == state.maskedCSRF {
		return errors.New("phase B API did not publish a fresh masked CSRF token")
	}
	created, err := operatorRequest(client, http.MethodPost, baseURL+"/api/articles/", api.JSONContentType, `{"title":"phase-b-durable","published":false}`, freshCSRF)
	if err != nil || created.status != http.StatusCreated || !bytes.Contains(created.body, []byte(`"title":"phase-b-durable"`)) {
		return errors.New("phase B durable-session API create failed")
	}
	if operatorJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != state.sessionCookie {
		return errors.New("phase B durable session cookie changed")
	}
	return nil
}

func operatorRequest(client *http.Client, method, target, contentType, body, csrf string) (operatorHTTPResult, error) {
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		return operatorHTTPResult{}, errors.New("build external operator HTTP request")
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		return operatorHTTPResult{}, errors.New("execute external operator HTTP request")
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, operatorMaximumOutput+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil || len(payload) > operatorMaximumOutput {
		return operatorHTTPResult{}, errors.New("read bounded external operator HTTP response")
	}
	return operatorHTTPResult{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		header:      response.Header.Clone(),
		cookies:     response.Cookies(),
		body:        payload,
	}, nil
}

func operatorHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func operatorDecodeArticlePage(document []byte) (operatorArticlePage, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var page operatorArticlePage
	if err := decoder.Decode(&page); err != nil {
		return operatorArticlePage{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return operatorArticlePage{}, errors.New("Article page has trailing JSON")
	}
	return page, nil
}

func operatorExtractCSRF(document []byte) string {
	match := operatorCSRFPattern.FindSubmatch(document)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func operatorCSRFToken(value string) bool {
	if len(value) != 128 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func operatorUniqueCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, errors.New("duplicate authentication cookie")
		}
		copy := *cookie
		found = &copy
	}
	if found == nil {
		return nil, errors.New("authentication cookie is absent")
	}
	return found, nil
}

func operatorJarCookie(jar http.CookieJar, target *url.URL, name string) string {
	for _, cookie := range jar.Cookies(target) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func operatorReserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve external operator loopback address")
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal("release external operator loopback address")
	}
	return address
}

func operatorWaitForHTTP(address string) error {
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(8 * time.Second)
	for {
		response, err := client.Get("http://" + address + "/articles/")
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, operatorMaximumOutput+1))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("external operator HTTP readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func operatorAssertSQLiteCounts(t *testing.T, path string, credentials, sessions, articles int) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("open external operator SQLite inspection")
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	queries := []struct {
		name  string
		query string
		want  int
	}{
		{name: "credential", query: `SELECT COUNT(*) FROM "godj_system_credential"`, want: credentials},
		{name: "session", query: `SELECT COUNT(*) FROM "godj_system_session"`, want: sessions},
		{name: "Article", query: `SELECT COUNT(*) FROM "godj_conformance_article"`, want: articles},
	}
	for _, item := range queries {
		var got int
		if err := database.QueryRowContext(ctx, item.query).Scan(&got); err != nil {
			t.Fatalf("inspect external operator SQLite %s count", item.name)
		}
		if got != item.want {
			t.Fatalf("external operator SQLite %s count = %d, want %d", item.name, got, item.want)
		}
	}
}

func operatorAssertSQLiteCredential(t *testing.T, path, username string, password []byte) {
	t.Helper()
	func() {
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal("open external operator SQLite credential inspection")
		}
		database.SetMaxOpenConns(1)
		defer database.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var principal, storedUsername, encoded, permissions, digest string
		var active bool
		err = database.QueryRowContext(ctx, `SELECT "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest" FROM "godj_system_credential"`).Scan(
			&principal, &storedUsername, &encoded, &active, &permissions, &digest,
		)
		if err != nil {
			t.Fatal("read external operator SQLite credential")
		}
		if principal != "external-article-operator" || storedUsername != username || encoded == "" || !active || permissions == "" || !strings.HasPrefix(digest, "sha256:") {
			t.Fatal("external operator SQLite credential semantic shape differs")
		}
		if bytes.Equal([]byte(encoded), password) || bytes.Contains([]byte(encoded), password) {
			t.Fatal("external operator SQLite credential retained the raw password")
		}
		rows, err := database.QueryContext(ctx, `SELECT "digest", "payload" FROM "godj_system_session"`)
		if err != nil {
			t.Fatal("read external operator SQLite sessions")
		}
		defer rows.Close()
		for rows.Next() {
			var sessionDigest, payload string
			if err := rows.Scan(&sessionDigest, &payload); err != nil {
				t.Fatal("scan external operator SQLite session")
			}
			if bytes.Contains([]byte(sessionDigest+payload), password) {
				t.Fatal("external operator SQLite session retained the raw password")
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal("finish external operator SQLite session inspection")
		}
	}()

	// Reconcile through the product API on a separately opened backend instead
	// of treating row shape as proof. This is the no-retry path used after a
	// lost private response: OpenExisting must accept the exact stored policy and
	// the supplied password must authenticate against the durable hash.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	backend, err := sqlitebackend.Open(ctx, path)
	if err != nil {
		t.Fatal("open fresh external operator SQLite backend")
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()
	policy := operatorCredentialPolicy(t)
	runtime, err := systemstate.OpenExisting(ctx, backend, systemstate.RuntimeConfig{
		CredentialPolicy: policy,
		SessionLimits:    sessions.DefaultLimits(),
		MaxSessions:      64,
		AuditCapacity:    256,
	})
	if err != nil {
		t.Fatal("open existing external operator runtime")
	}
	passwordText := string(password)
	principal, err := runtime.Authenticator().Authenticate(ctx, username, passwordText)
	passwordText = ""
	if err != nil || principal.ID() != "external-article-operator" || !principal.Active() {
		t.Fatal("authenticate fresh external operator runtime")
	}
	if err := backend.Close(); err != nil {
		t.Fatal("close fresh external operator SQLite backend")
	}
	closed = true
}

func operatorCredentialPolicy(t *testing.T) systemstate.CredentialPolicy {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     "external-article-operator",
		Active: true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		t.Fatal("build external operator principal")
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		t.Fatal("build external operator password hasher")
	}
	return systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
}

func operatorRandomPassword(t *testing.T) []byte {
	t.Helper()
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		t.Fatal("generate external operator password")
	}
	password := []byte("Op-7vQ-")
	password = append(password, []byte(hex.EncodeToString(random))...)
	clear(random)
	return password
}

func operatorDistinctPositivePIDs(pids ...int) bool {
	seen := make(map[int]struct{}, len(pids))
	for _, pid := range pids {
		if pid <= 1 {
			return false
		}
		if _, exists := seen[pid]; exists {
			return false
		}
		seen[pid] = struct{}{}
	}
	return true
}

func operatorContainsPID(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
