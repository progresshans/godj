package godj

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	systemstateworker "github.com/progresshans/godj/conformance/systemstate/worker"
)

const (
	systemStateWorkerActionInitialize     = systemstateworker.ActionInitialize
	systemStateWorkerActionAuthenticate   = systemstateworker.ActionAuthenticate
	systemStateWorkerActionLogin          = systemstateworker.ActionLogin
	systemStateWorkerActionSessionProbe   = systemstateworker.ActionSessionProbe
	systemStateWorkerActionLogout         = systemstateworker.ActionLogout
	systemStateWorkerActionOldCookieProbe = systemstateworker.ActionOldCookieProbe
	systemStateWorkerActionCSRFSetup      = systemstateworker.ActionCSRFSetup
	systemStateWorkerActionCookieProbe    = systemstateworker.ActionCookieProbe
	systemStateWorkerActionCSRFStale      = systemstateworker.ActionCSRFStale
	systemStateWorkerActionCSRFFresh      = systemstateworker.ActionCSRFFresh
	systemStateWorkerActionAuditFault     = systemstateworker.ActionAuditFault
	systemStateWorkerActionHistoryWrite   = systemstateworker.ActionHistoryWrite
	systemStateWorkerActionHistoryRead    = systemstateworker.ActionHistoryRead

	systemStateWorkerMaximumOutputBytes = 1 << 20
)

var systemStateWorkerSafeCode = regexp.MustCompile(`^[a-z0-9_]+$`)

type systemStateWorkerCookies = systemstateworker.CookieBundle
type systemStateWorkerRequest = systemstateworker.Request
type systemStateWorkerResponse = systemstateworker.Response
type systemStateWorkerSecretBundle = systemstateworker.SecretBundle
type systemStateWorkerAuditEvent = systemstateworker.AuditEvent

type systemStateWorkerHarness struct {
	directory      string
	binaryPath     string
	databasePath   string
	repositoryRoot string
	username       string
	password       string
	sensitive      []string
	pids           map[int]struct{}
}

func newSystemStateWorkerHarness(ctx context.Context) (*systemStateWorkerHarness, error) {
	directory, err := os.MkdirTemp("", "godj-system-state-worker-")
	if err != nil {
		return nil, fmt.Errorf("create system-state worker directory: %w", err)
	}
	repositoryRoot, err := systemStateRepositoryRoot()
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	harness := &systemStateWorkerHarness{
		directory:      directory,
		binaryPath:     filepath.Join(directory, "system-state-worker"),
		databasePath:   filepath.Join(directory, "system-state.sqlite3"),
		repositoryRoot: repositoryRoot,
		username:       "system-state-admin",
		pids:           make(map[int]struct{}),
	}
	passwordBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, passwordBytes); err != nil {
		harness.cleanup()
		return nil, errors.New("generate system-state worker credential")
	}
	harness.password = "gdj-" + base64.RawURLEncoding.EncodeToString(passwordBytes)
	harness.sensitive = []string{harness.password}
	if _, err := os.Stat(filepath.Join(harness.repositoryRoot, "go.mod")); err != nil {
		harness.cleanup()
		return nil, errors.New("locate system-state worker repository root")
	}

	buildContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		buildContext,
		"go", "build", "-o", harness.binaryPath,
		"./conformance/systemstate/worker/cmd",
	)
	command.Dir = harness.repositoryRoot
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	buildErr := command.Run()
	if systemStateRawSecretOccurrences(
		append(stdout.Bytes(), stderr.Bytes()...),
		harness.sensitive,
	) != 0 {
		harness.cleanup()
		return nil, errors.New("system-state worker build diagnostic exposed a secret")
	}
	if buildErr != nil {
		harness.cleanup()
		return nil, fmt.Errorf(
			"build system-state worker failed: stdout=%d stderr=%d",
			stdout.Len(), stderr.Len(),
		)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		harness.cleanup()
		return nil, fmt.Errorf(
			"system-state worker build emitted diagnostics: stdout=%d stderr=%d",
			stdout.Len(), stderr.Len(),
		)
	}
	return harness, nil
}

func (harness *systemStateWorkerHarness) cleanup() {
	if harness == nil || harness.directory == "" {
		return
	}
	base := filepath.Base(harness.directory)
	if strings.HasPrefix(base, "godj-system-state-worker-") && filepath.Dir(harness.directory) != harness.directory {
		_ = os.RemoveAll(harness.directory)
	}
	harness.directory = ""
}

func (harness *systemStateWorkerHarness) run(
	ctx context.Context,
	action systemstateworker.Action,
	cookies systemStateWorkerCookies,
	token string,
	objectID int64,
) (systemStateWorkerResponse, systemStateWorkerSecretBundle, error) {
	request := systemStateWorkerRequest{
		Action:         action,
		Database:       harness.databasePath,
		Username:       harness.username,
		Password:       harness.password,
		Cookies:        cookies,
		Token:          token,
		RepositoryRoot: harness.repositoryRoot,
		ObjectID:       objectID,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("encode system-state worker request")
	}
	secretReader, secretWriter, err := os.Pipe()
	if err != nil {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("create system-state worker secret pipe")
	}
	defer secretReader.Close()

	phaseContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(phaseContext, harness.binaryPath)
	command.Dir = harness.repositoryRoot
	command.Env = []string{
		"LC_ALL=C",
		"TZ=UTC",
		"TMPDIR=" + harness.directory,
	}
	command.Stdin = bytes.NewReader(requestBytes)
	command.ExtraFiles = []*os.File{secretWriter}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		_ = secretWriter.Close()
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("start system-state worker child")
	}
	childPID := command.Process.Pid
	_ = secretWriter.Close()
	secretResult := make(chan []byte, 1)
	secretReadError := make(chan error, 1)
	go func() {
		payload, readErr := io.ReadAll(io.LimitReader(secretReader, systemStateWorkerMaximumOutputBytes+1))
		secretResult <- payload
		secretReadError <- readErr
	}()
	waitErr := command.Wait()
	secretBytes := <-secretResult
	readErr := <-secretReadError
	if readErr != nil || len(secretBytes) > systemStateWorkerMaximumOutputBytes {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("read system-state worker secret channel")
	}
	if stdout.Len() > systemStateWorkerMaximumOutputBytes || stderr.Len() > systemStateWorkerMaximumOutputBytes {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("system-state worker diagnostic exceeded its bound")
	}

	var secrets systemStateWorkerSecretBundle
	secretDecodeErr := systemStateDecodeOneJSON(secretBytes, &secrets)
	if secretDecodeErr == nil {
		harness.recordSecrets(
			secrets.Cookies.Session,
			secrets.Cookies.CSRF,
			secrets.Token,
		)
	}
	diagnostics := append(append([]byte(nil), stdout.Bytes()...), stderr.Bytes()...)
	if systemStateRawSecretOccurrences(diagnostics, harness.sensitive) != 0 {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("system-state worker diagnostic exposed a secret")
	}
	if waitErr != nil {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
			"system-state worker action %s failed: stdout=%d stderr=%d",
			action, stdout.Len(), stderr.Len(),
		)
	}
	if secretDecodeErr != nil {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("decode system-state worker secret channel")
	}
	if stderr.Len() != 0 {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
			"system-state worker action %s emitted stderr bytes=%d",
			action, stderr.Len(),
		)
	}

	var response systemStateWorkerResponse
	if err := systemStateDecodeOneJSON(stdout.Bytes(), &response); err != nil {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
			"decode system-state worker action %s response",
			action,
		)
	}
	if !response.OK {
		if !systemStateWorkerSafeCode.MatchString(response.ErrorCode) {
			return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
				"system-state worker action %s failed with an unsafe code",
				action,
			)
		}
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
			"system-state worker action %s failed with code %s",
			action, response.ErrorCode,
		)
	}
	if response.ErrorCode != "" || response.Action != action || response.PID != childPID || childPID == os.Getpid() {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, fmt.Errorf(
			"system-state worker action %s returned an invalid safe envelope",
			action,
		)
	}
	if _, duplicate := harness.pids[childPID]; duplicate {
		return systemStateWorkerResponse{}, systemStateWorkerSecretBundle{}, errors.New("system-state worker reused a phase PID")
	}
	harness.pids[childPID] = struct{}{}
	return response, secrets, nil
}

func systemStateDecodeOneJSON(payload []byte, destination any) error {
	if len(payload) == 0 {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON channel contains trailing data")
	}
	return nil
}

func (harness *systemStateWorkerHarness) recordSecrets(values ...string) {
	for _, value := range values {
		if value == "" {
			continue
		}
		harness.sensitive = append(harness.sensitive, value)
	}
}

func systemStateRawSecretOccurrences(payload []byte, secrets []string) int64 {
	var count int64
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		count += int64(bytes.Count(payload, []byte(secret)))
	}
	return count
}

func (harness *systemStateWorkerHarness) artifactSecretOccurrences() (int64, error) {
	entries, err := os.ReadDir(harness.directory)
	if err != nil {
		return 0, errors.New("read system-state worker artifact directory")
	}
	var count int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(harness.directory, entry.Name()))
		if err != nil {
			return 0, errors.New("read system-state worker artifact")
		}
		count += systemStateRawSecretOccurrences(payload, harness.sensitive)
	}
	return count, nil
}

func (harness *systemStateWorkerHarness) distinctProcesses(want int) (int, error) {
	observed := len(harness.pids)
	if observed != want {
		return observed, fmt.Errorf(
			"system-state worker distinct processes=%d, want %d contract-relevant phases",
			observed, want,
		)
	}
	return observed, nil
}

func (harness *systemStateWorkerHarness) initialize(
	ctx context.Context,
) (systemStateWorkerResponse, error) {
	response, secrets, err := harness.run(
		ctx,
		systemStateWorkerActionInitialize,
		systemStateWorkerCookies{},
		"",
		0,
	)
	if err != nil {
		return systemStateWorkerResponse{}, err
	}
	if !response.MigrationApplied || !response.Ready || response.CredentialRows != 1 ||
		secrets.Cookies.Session != "" || secrets.Cookies.CSRF != "" || secrets.Token != "" {
		return systemStateWorkerResponse{}, errors.New("system-state worker initialization did not observe an explicit ready migration")
	}
	return response, nil
}

func systemStateSameCookies(left, right systemStateWorkerCookies) bool {
	return left.Session != "" && left.CSRF != "" &&
		left.Session == right.Session && left.CSRF == right.CSRF
}

func systemStateCredentialPermissionRestartDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	initialized, err := harness.initialize(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	authenticated, _, err := harness.run(ctx, systemStateWorkerActionAuthenticate, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	distinct, err := harness.distinctProcesses(2)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"active":        protocol.Boolean(authenticated.Active),
		"authenticated": protocol.Boolean(authenticated.Authenticated),
		"permission":    protocol.Boolean(authenticated.Permission),
		"restart":       protocol.Boolean(initialized.Ready && authenticated.Ready),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"admin_rows": systemStateInt(authenticated.CredentialRows),
		"user_rows":  systemStateInt(authenticated.CredentialRows),
	})
	artifactOccurrences, err := harness.artifactSecretOccurrences()
	if err != nil {
		return protocol.Observation{}, err
	}
	observationOccurrences, err := authSessionSecretOccurrences(
		[]protocol.Value{result, dbState},
		harness.sensitive...,
	)
	if err != nil {
		return protocol.Observation{}, errors.New("scan credential-restart normalized values")
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes":       systemStateInt(distinct),
		"secret_values_serialized": systemStateInt64(artifactOccurrences + observationOccurrences),
	}))
}

func systemStateRotatedSessionRestartDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	if _, err := harness.initialize(ctx); err != nil {
		return protocol.Observation{}, err
	}
	login, loginSecrets, err := harness.run(ctx, systemStateWorkerActionLogin, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	probe, probeSecrets, err := harness.run(ctx, systemStateWorkerActionSessionProbe, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	distinct, err := harness.distinctProcesses(3)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"admin_status":        systemStateInt(probe.AdminStatus),
		"api_status":          systemStateInt(probe.APIStatus),
		"authenticated":       protocol.Boolean(probe.Authenticated),
		"login_status":        systemStateInt(login.LoginStatus),
		"old_session_removed": protocol.Boolean(login.OldSessionRemoved),
		"permission":          protocol.Boolean(probe.Permission),
		"rotated":             protocol.Boolean(login.Rotated),
		"same_cookie_handoff": protocol.Boolean(systemStateSameCookies(loginSecrets.Cookies, probeSecrets.Cookies)),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"session_rows_after_restart": systemStateInt(probe.SessionRows),
	})
	if occurrences, err := harness.artifactSecretOccurrences(); err != nil {
		return protocol.Observation{}, err
	} else if occurrences != 0 {
		return protocol.Observation{}, errors.New("rotated-session durable artifacts exposed a raw secret")
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes":       systemStateInt(distinct),
		"session_rows_after_login": systemStateInt(login.SessionRows),
	}))
}

func systemStateLogoutRestartDenialDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	if _, err := harness.initialize(ctx); err != nil {
		return protocol.Observation{}, err
	}
	_, loginSecrets, err := harness.run(ctx, systemStateWorkerActionLogin, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	loggedOut, _, err := harness.run(ctx, systemStateWorkerActionLogout, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	probe, _, err := harness.run(ctx, systemStateWorkerActionOldCookieProbe, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	if probe.APIStatus == 403 && probe.APIErrorCode != "not_authenticated" {
		return protocol.Observation{}, errors.New("old-cookie API denial did not return the safe not_authenticated JSON code")
	}
	distinct, err := harness.distinctProcesses(4)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"admin_status":             systemStateInt(probe.AdminStatus),
		"api_status":               systemStateInt(probe.APIStatus),
		"old_cookie_authenticated": protocol.Boolean(probe.Authenticated),
		"old_session_removed":      protocol.Boolean(loggedOut.OldSessionRemoved),
		"resurrected":              protocol.Boolean(probe.Resurrected),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"session_rows_after_logout": systemStateInt(probe.SessionRows),
	})
	if occurrences, err := harness.artifactSecretOccurrences(); err != nil {
		return protocol.Observation{}, err
	} else if occurrences != 0 {
		return protocol.Observation{}, errors.New("logout-restart durable artifacts exposed a raw secret")
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes":  systemStateInt(distinct),
		"resurrection_writes": systemStateInt64(probe.ResurrectionWrites),
	}))
}

func systemStateCSRFRestartDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	if _, err := harness.initialize(ctx); err != nil {
		return protocol.Observation{}, err
	}
	_, loginSecrets, err := harness.run(ctx, systemStateWorkerActionLogin, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	setup, setupSecrets, err := harness.run(ctx, systemStateWorkerActionCSRFSetup, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	preProbe, preProbeSecrets, err := harness.run(ctx, systemStateWorkerActionCookieProbe, setupSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	stale, staleSecrets, err := harness.run(ctx, systemStateWorkerActionCSRFStale, preProbeSecrets.Cookies, setupSecrets.Token, 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	fresh, freshSecrets, err := harness.run(ctx, systemStateWorkerActionCSRFFresh, staleSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	finalProbe, finalProbeSecrets, err := harness.run(ctx, systemStateWorkerActionSessionProbe, freshSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	distinct, err := harness.distinctProcesses(7)
	if err != nil {
		return protocol.Observation{}, err
	}
	sameCookie := systemStateSameCookies(loginSecrets.Cookies, setupSecrets.Cookies) &&
		systemStateSameCookies(setupSecrets.Cookies, preProbeSecrets.Cookies) &&
		systemStateSameCookies(preProbeSecrets.Cookies, staleSecrets.Cookies) &&
		systemStateSameCookies(staleSecrets.Cookies, freshSecrets.Cookies) &&
		systemStateSameCookies(freshSecrets.Cookies, finalProbeSecrets.Cookies)
	if !preProbe.Authenticated || !preProbe.Permission || !finalProbe.Authenticated || !finalProbe.Permission {
		return protocol.Observation{}, errors.New("CSRF restart cookie handoff lost durable authentication")
	}
	if preProbe.ArticleRows != setup.ArticleRowsAfter || stale.ArticleRowsBefore != preProbe.ArticleRows ||
		stale.ArticleRowsAfter != stale.ArticleRowsBefore || fresh.ArticleRowsBefore != stale.ArticleRowsAfter ||
		finalProbe.ArticleRows != fresh.ArticleRowsAfter {
		return protocol.Observation{}, errors.New("CSRF restart child phases did not observe one continuous durable Article state")
	}
	result := protocol.Object(map[string]protocol.Value{
		"fresh": protocol.Object(map[string]protocol.Value{
			"accepted": protocol.Boolean(fresh.MutationStatus == 201),
			"status":   systemStateInt(fresh.MutationStatus),
		}),
		"pre_restart": protocol.Object(map[string]protocol.Value{
			"accepted": protocol.Boolean(stale.MutationStatus == 201),
			"status":   systemStateInt(stale.MutationStatus),
		}),
		"same_cookie_handoff": protocol.Boolean(sameCookie),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"fresh":       protocol.Object(map[string]protocol.Value{"article_delta": systemStateInt(fresh.ArticleDelta)}),
		"pre_restart": protocol.Object(map[string]protocol.Value{"article_delta": systemStateInt(stale.ArticleDelta)}),
	})
	artifactOccurrences, err := harness.artifactSecretOccurrences()
	if err != nil {
		return protocol.Observation{}, err
	}
	observationOccurrences, err := authSessionSecretOccurrences(
		[]protocol.Value{result, dbState},
		harness.sensitive...,
	)
	if err != nil {
		return protocol.Observation{}, errors.New("scan CSRF-restart normalized values")
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes":          systemStateInt(distinct),
		"fresh_mutations":             systemStateInt(fresh.ArticleDelta),
		"pre_restart_mutations":       systemStateInt(stale.ArticleDelta),
		"pre_restart_setup_mutations": systemStateInt(setup.ArticleDelta),
		"secret_values_serialized":    systemStateInt64(artifactOccurrences + observationOccurrences),
	}))
}

func systemStateAdminAuditFaultRollbackDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	if _, err := harness.initialize(ctx); err != nil {
		return protocol.Observation{}, err
	}
	_, loginSecrets, err := harness.run(ctx, systemStateWorkerActionLogin, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	fault, _, err := harness.run(ctx, systemStateWorkerActionAuditFault, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	distinct, err := harness.distinctProcesses(3)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"article_rolled_back": protocol.Boolean(fault.ArticleDelta == 0 && fault.RolledBack),
		"audit_rolled_back":   protocol.Boolean(fault.AuditDelta == 0 && fault.RolledBack),
		"status":              systemStateInt(fault.Status),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"article_delta": systemStateInt(fault.ArticleDelta),
		"audit_delta":   systemStateInt(fault.AuditDelta),
	})
	if occurrences, err := harness.artifactSecretOccurrences(); err != nil {
		return protocol.Observation{}, err
	} else if occurrences != 0 {
		return protocol.Observation{}, errors.New("audit-fault durable artifacts exposed a raw secret")
	}
	faults := 0
	if fault.FaultInjected {
		faults = 1
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes": systemStateInt(distinct),
		"faults_injected":    systemStateInt(faults),
	}))
}

func systemStateAuditHistoryRestartDistinctProcess(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	harness, err := newSystemStateWorkerHarness(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer harness.cleanup()
	if _, err := harness.initialize(ctx); err != nil {
		return protocol.Observation{}, err
	}
	_, loginSecrets, err := harness.run(ctx, systemStateWorkerActionLogin, systemStateWorkerCookies{}, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	written, _, err := harness.run(ctx, systemStateWorkerActionHistoryWrite, loginSecrets.Cookies, "", 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	history, _, err := harness.run(ctx, systemStateWorkerActionHistoryRead, systemStateWorkerCookies{}, "", written.ObjectID)
	if err != nil {
		return protocol.Observation{}, err
	}
	distinct, err := harness.distinctProcesses(4)
	if err != nil {
		return protocol.Observation{}, err
	}
	allEvents := systemStateWorkerAuditValues(history.AuditEvents)
	newestEvents := systemStateWorkerAuditValues(history.NewestEvents)
	strictlyIncreasing := systemStateWorkerAuditStrictlyIncreasing(history.AuditEvents)
	result := protocol.Object(map[string]protocol.Value{
		"all_events":          protocol.List(allEvents...),
		"contiguous_required": protocol.Boolean(!history.AcceptsNonContiguous),
		"newest_bounded":      protocol.List(newestEvents...),
		"strictly_increasing": protocol.Boolean(strictlyIncreasing),
		"survived_restart":    protocol.Boolean(len(history.AuditEvents) == 3),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"article_rows": systemStateInt(history.ArticleRows),
		"audit_rows":   systemStateInt(history.AuditRows),
	})
	if occurrences, err := harness.artifactSecretOccurrences(); err != nil {
		return protocol.Observation{}, err
	} else if occurrences != 0 {
		return protocol.Observation{}, errors.New("audit-history durable artifacts exposed a raw secret")
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes": systemStateInt(distinct),
		"history_limit":      systemStateInt(len(history.NewestEvents)),
		"write_statuses": protocol.List(
			systemStateInt(written.AddStatus),
			systemStateInt(written.ChangeStatus),
			systemStateInt(written.DeleteStatus),
		),
	}))
}

func systemStateWorkerAuditValues(entries []systemStateWorkerAuditEvent) []protocol.Value {
	values := make([]protocol.Value, len(entries))
	for index, entry := range entries {
		values[index] = protocol.Object(map[string]protocol.Value{
			"action":   protocol.String(entry.Action),
			"sequence": protocol.Integer(strconv.FormatUint(entry.Sequence, 10)),
		})
	}
	return values
}

func systemStateWorkerAuditStrictlyIncreasing(entries []systemStateWorkerAuditEvent) bool {
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Sequence >= entries[index].Sequence {
			return false
		}
	}
	return true
}
