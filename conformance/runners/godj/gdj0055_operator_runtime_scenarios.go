//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/siteappconformance"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type gdj0055EventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *gdj0055EventLog) append(event string) {
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *gdj0055EventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

type gdj0055LinkedBackend struct {
	systemstate.Backend
	events       *gdj0055EventLog
	closeCalls   atomic.Int64
	closeFailure error
}

func (backend *gdj0055LinkedBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	if backend.events != nil {
		backend.events.append("provision")
	}
	return backend.Backend.CoordinatedAtomic(ctx, callback)
}

func (backend *gdj0055LinkedBackend) Close() error {
	backend.closeCalls.Add(1)
	if backend.events != nil {
		backend.events.append("backend_close")
	}
	return backend.closeFailure
}

type gdj0055EventWriter struct {
	bytes.Buffer
	events *gdj0055EventLog
	err    error
	short  bool
}

func (writer *gdj0055EventWriter) Write(document []byte) (int, error) {
	if writer.events != nil {
		writer.events.append("response_publication")
	}
	if writer.short && len(document) != 0 {
		written, _ := writer.Buffer.Write(document[:len(document)-1])
		return written, nil
	}
	if writer.err != nil {
		return 0, writer.err
	}
	return writer.Buffer.Write(document)
}

func gdj0055ProjectProvisionOwnership(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	fixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()

	policy, err := fixture.config.credentialPolicy()
	if err != nil {
		return protocol.Observation{}, err
	}
	events := &gdj0055EventLog{}
	backend := &gdj0055LinkedBackend{Backend: fixture.observed, events: events}
	var openCalls atomic.Int64
	var linkedReport linked.CreatesuperuserReport
	globalReport, err := productcheck.RunCreatesuperuserOwnershipConformance(
		productcheck.CreatesuperuserOwnershipConformanceInput{
			Context:  ctx,
			Username: []byte(fixture.config.Username),
			Password: []byte(fixture.config.Password),
			AfterArgumentValidation: func() {
				events.append("request_validation")
			},
			AfterProjectSelection: func() {
				events.append("project_selection")
			},
			BeforePublicPublication: func() {
				events.append("response_publication")
			},
			ExecutePrivate: func(ctx context.Context, request []byte) ([]byte, error) {
				writer := &gdj0055EventWriter{}
				var runErr error
				linkedReport, runErr = linked.RunCreatesuperuser(ctx, linked.CreatesuperuserConfig{
					OpenSystemStateBackend: func(context.Context) (linked.SystemStateBackend, error) {
						openCalls.Add(1)
						events.append("backend_open")
						return backend, nil
					},
					CredentialPolicy: policy,
				}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(request), writer)
				if runErr != nil {
					return nil, runErr
				}
				return append([]byte(nil), writer.Bytes()...), nil
			},
		},
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("run global and linked createsuperuser: %w", err)
	}
	if !globalReport.HasCreatesuperuserResult || !globalReport.KnownCreated || globalReport.BuildCalls != 1 ||
		globalReport.RunnerCalls != 1 || globalReport.UserStdoutWrites != 1 ||
		!linkedReport.KnownCreated || linkedReport.BackendOpenCalls != 1 || linkedReport.ProvisionCalls != 1 ||
		linkedReport.BackendCloseCalls != 1 || linkedReport.RunnerResponseWrites != 1 {
		return protocol.Observation{}, fmt.Errorf("global/linked provisioning observation drifted: global=%+v linked=%+v", globalReport, linkedReport)
	}
	wantOrdering := []string{
		"request_validation", "project_selection", "backend_open", "provision", "backend_close", "response_publication",
	}
	if got := events.snapshot(); !reflect.DeepEqual(got, wantOrdering) {
		return protocol.Observation{}, fmt.Errorf("linked provisioning ordering=%v, want %v", got, wantOrdering)
	}

	var rejectedOpens atomic.Int64
	var rejectedOutput bytes.Buffer
	rejected, rejectedErr := linked.RunCreatesuperuser(ctx, linked.CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (linked.SystemStateBackend, error) {
			rejectedOpens.Add(1)
			return nil, errors.New("must not open")
		},
		CredentialPolicy: policy,
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader([]byte("invalid")), &rejectedOutput)
	if rejectedErr != nil || rejectedOpens.Load() != 0 || rejected.BackendOpenCalls != 0 || rejected.ProvisionCalls != 0 {
		return protocol.Observation{}, fmt.Errorf("invalid request crossed backend boundary: report=%+v error=%v opens=%d", rejected, rejectedErr, rejectedOpens.Load())
	}

	missingDirectory, err := newGDJ0055UnmigratedBackend(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer missingDirectory.cleanup()
	missingDirectory.observed.resetDML()
	missingErr := systemStateProvisionOperator(ctx, missingDirectory.observed, fixture.config)
	if !errors.Is(missingErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) || missingDirectory.writes() != 0 || missingDirectory.observed.atomicCalls.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf("missing migration gate: error=%v writes=%d atomics=%d", missingErr, missingDirectory.writes(), missingDirectory.observed.atomicCalls.Load())
	}

	readiness, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer readiness.cleanup()
	if _, err := readiness.raw.ExecContext(ctx, `DROP TABLE "godj_system_audit"`); err != nil {
		return protocol.Observation{}, err
	}
	readiness.resetDML()
	readinessErr := systemStateProvisionOperator(ctx, readiness.observed, readiness.config)
	if !errors.Is(readinessErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) || readiness.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("readiness gate: error=%v writes=%d", readinessErr, readiness.writes())
	}

	ordering := make([]protocol.Value, len(wantOrdering))
	for index, event := range wantOrdering {
		ordering[index] = protocol.String(event)
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"gates": protocol.List(
				protocol.String("exact_system_migration"),
				protocol.String("system_state_readiness"),
			),
			"ordering":                        protocol.List(ordering...),
			"project_owns_backend_and_policy": protocol.Boolean(openCalls.Load() == 1),
		}),
		protocol.Object(map[string]protocol.Value{
			"mutation_without_exact_migration": systemStateInt64(missingDirectory.writes()),
			"open_before_validation":           systemStateInt64(rejectedOpens.Load()),
			"provision_before_readiness":       systemStateInt64(readiness.writes()),
		}),
		protocol.Object(map[string]protocol.Value{
			"backend_closes":  systemStateInt64(backend.closeCalls.Load()),
			"backend_opens":   systemStateInt64(openCalls.Load()),
			"provision_calls": systemStateInt(linkedReport.ProvisionCalls),
		}),
	)
}

func newGDJ0055UnmigratedBackend(ctx context.Context) (*gdj0055StateFixture, error) {
	directory, err := newGDJ0055StateFixtureDirectory()
	if err != nil {
		return nil, err
	}
	fixture := &gdj0055StateFixture{directory: directory, config: systemStateFixtureConfig(0x56)}
	fixture.dsn = "file:" + directory + "/unmigrated.sqlite3?mode=rwc&_busy_timeout=5000&_pragma=foreign_keys(1)"
	raw, err := sqlite.Open(ctx, fixture.dsn)
	if err != nil {
		fixture.cleanup()
		return nil, err
	}
	fixture.raw = raw
	fixture.observed = &systemStateObservedBackend{Backend: raw}
	return fixture, nil
}

func newGDJ0055StateFixtureDirectory() (string, error) {
	return os.MkdirTemp("", "godj-gdj0055-system-state-")
}

func gdj0055OperatorProvisionCardinality(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	empty, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer empty.cleanup()
	empty.resetDML()
	if err := systemStateProvisionOperator(ctx, empty.observed, empty.config); err != nil {
		return protocol.Observation{}, fmt.Errorf("empty provision: %w", err)
	}
	emptyWrites := empty.writes()

	concurrent, err := gdj0046RunConcurrentBootstrap(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	if concurrent.credentialRows != 1 || concurrent.bootstrapWinners != 1 || concurrent.coordinationRetries != 0 {
		return protocol.Observation{}, fmt.Errorf("concurrent provisioning facts=%+v", concurrent)
	}

	beforeExisting, rows, err := gdj0046ReadCredentialSnapshot(ctx, empty.raw)
	if err != nil || rows != 1 {
		return protocol.Observation{}, fmt.Errorf("read existing credential rows=%d: %w", rows, err)
	}
	existingHasher := &gdj0055ObservedHasher{PasswordHasher: empty.config.PasswordHasher}
	existingConfig := empty.config
	existingConfig.Password = "must-not-be-compared"
	existingConfig.PasswordHasher = existingHasher
	empty.resetDML()
	existingErr := systemStateProvisionOperator(ctx, empty.observed, existingConfig)
	existingWrites := empty.writes()
	afterExisting, rows, snapshotErr := gdj0046ReadCredentialSnapshot(ctx, empty.raw)
	if snapshotErr != nil || rows != 1 || beforeExisting != afterExisting ||
		!errors.Is(existingErr, &systemstate.Error{Code: systemstate.CodeCredentialAlreadyExists}) ||
		existingWrites != 0 || existingHasher.verifyCalls.Load() != 0 || existingHasher.hashCalls.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf("existing provision mutated or compared secret: error=%v rows=%d writes=%d hash=%d verify=%d", existingErr, rows, existingWrites, existingHasher.hashCalls.Load(), existingHasher.verifyCalls.Load())
	}

	duplicate, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer duplicate.cleanup()
	if err := systemStateProvisionOperator(ctx, duplicate.observed, duplicate.config); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := duplicate.raw.ExecContext(ctx, `INSERT INTO "godj_system_credential" ("id", "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest") SELECT 2, "principal_id" || '-duplicate', "username" || '-duplicate', "encoded_password", "active", "permissions", "definition_digest" FROM "godj_system_credential" WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	duplicate.resetDML()
	duplicateErr := systemStateProvisionOperator(ctx, duplicate.observed, duplicate.config)
	duplicateWrites := duplicate.writes()
	if !errors.Is(duplicateErr, &systemstate.Error{Code: systemstate.CodeCardinality}) || duplicateWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("duplicate provision error=%v writes=%d", duplicateErr, duplicateWrites)
	}

	corrupt, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer corrupt.cleanup()
	if err := systemStateProvisionOperator(ctx, corrupt.observed, corrupt.config); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := corrupt.raw.ExecContext(ctx, `UPDATE "godj_system_credential" SET "encoded_password" = 'v9.invalid' WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	corrupt.resetDML()
	corruptErr := systemStateProvisionOperator(ctx, corrupt.observed, corrupt.config)
	corruptWrites := corrupt.writes()
	if !errors.Is(corruptErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) || corruptWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("corrupt provision error=%v writes=%d", corruptErr, corruptWrites)
	}

	mismatch, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer mismatch.cleanup()
	if err := systemStateProvisionOperator(ctx, mismatch.observed, mismatch.config); err != nil {
		return protocol.Observation{}, err
	}
	mismatchConfig := mismatch.config
	mismatchConfig.PrincipalID += "-mismatch"
	mismatch.resetDML()
	mismatchErr := systemStateProvisionOperator(ctx, mismatch.observed, mismatchConfig)
	mismatchWrites := mismatch.writes()
	if !errors.Is(mismatchErr, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) || mismatchWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("policy mismatch provision error=%v writes=%d", mismatchErr, mismatchWrites)
	}

	cases := protocol.List(
		gdj0055ProvisionCase("empty", "created", emptyWrites),
		protocol.Object(map[string]protocol.Value{
			"case":          protocol.String("concurrent_empty"),
			"loser_outcome": protocol.String(string(systemstate.CodeCredentialAlreadyExists)),
			"outcome":       protocol.String("exactly_one_winner"),
			"writes":        systemStateInt64(concurrent.bootstrapWinners),
		}),
		gdj0055ProvisionCase("already_one", string(systemstate.CodeCredentialAlreadyExists), existingWrites),
		gdj0055ProvisionCase("cardinality_two_or_more", string(systemstate.CodeCardinality), duplicateWrites),
		gdj0055ProvisionCase("malformed_or_profile_invalid", string(systemstate.CodeCorruptState), corruptWrites),
		gdj0055ProvisionCase("policy_mismatch", string(systemstate.CodeCredentialPolicyMismatch), mismatchWrites),
	)
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"cases":                    cases,
			"existing_secret_compared": protocol.Boolean(existingHasher.verifyCalls.Load() != 0),
		}),
		protocol.Object(map[string]protocol.Value{
			"credential_rows_after_winner": systemStateInt(concurrent.credentialRows),
			"existing_rows_deleted":        systemStateInt64(empty.observed.deletes.Load()),
			"existing_rows_updated":        systemStateInt64(empty.observed.updates.Load()),
		}),
		protocol.Object(map[string]protocol.Value{
			"automatic_retries":  systemStateInt64(concurrent.coordinationRetries),
			"concurrent_winners": systemStateInt64(concurrent.bootstrapWinners),
			"loser_mutations":    systemStateInt(0),
		}),
	)
}

func gdj0055ProvisionCase(name, outcome string, writes int64) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"case":    protocol.String(name),
		"outcome": protocol.String(outcome),
		"writes":  systemStateInt64(writes),
	})
}

type gdj0055CommitUnknownBackend struct {
	*systemStateObservedBackend
	calls atomic.Int64
}

func (backend *gdj0055CommitUnknownBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.calls.Add(1)
	if err := backend.systemStateObservedBackend.CoordinatedAtomic(ctx, callback); err != nil {
		return err
	}
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Detail: "GDJ-0055 injected commit outcome unknown"}
}

type gdj0055ProvisionOutcomeFacts struct {
	attempts             int64
	provisionCalls       int64
	backendOpens         int64
	backendCloses        int64
	buildCalls           int64
	runnerCalls          int64
	sensitiveWrites      int64
	runnerResponseWrites int64
}

func (facts *gdj0055ProvisionOutcomeFacts) provision(
	ctx context.Context,
	backend systemstate.Backend,
	config systemStateConfig,
) error {
	facts.attempts++
	facts.provisionCalls++
	return systemStateProvisionOperator(ctx, backend, config)
}

func gdj0055LinkedOutcomeFacts(report linked.CreatesuperuserReport) gdj0055ProvisionOutcomeFacts {
	return gdj0055ProvisionOutcomeFacts{
		attempts:             int64(report.ProvisionCalls),
		provisionCalls:       int64(report.ProvisionCalls),
		backendOpens:         int64(report.BackendOpenCalls),
		backendCloses:        int64(report.BackendCloseCalls),
		runnerResponseWrites: int64(report.RunnerResponseWrites),
	}
}

func (facts gdj0055ProvisionOutcomeFacts) retry() bool {
	return facts.attempts > 1
}

func (facts gdj0055ProvisionOutcomeFacts) retries() int64 {
	if facts.attempts <= 1 {
		return 0
	}
	return facts.attempts - 1
}

func (facts gdj0055ProvisionOutcomeFacts) exactDirectAttempt() bool {
	return facts.attempts == 1 && facts.provisionCalls == 1 && facts.backendOpens == 0 &&
		facts.backendCloses == 0 && facts.buildCalls == 0 && facts.runnerCalls == 0 &&
		facts.sensitiveWrites == 0 && facts.runnerResponseWrites == 0
}

func (facts gdj0055ProvisionOutcomeFacts) exactLinkedAttempt() bool {
	return facts.attempts == 1 && facts.provisionCalls == 1 && facts.backendOpens == 1 &&
		facts.backendCloses == 1 && facts.buildCalls == 0 && facts.runnerCalls == 0 &&
		facts.sensitiveWrites == 0 && facts.runnerResponseWrites == 1
}

func (facts gdj0055ProvisionOutcomeFacts) exactGlobalAttempt() bool {
	return facts.attempts == 1 && facts.provisionCalls == 1 && facts.backendOpens == 1 &&
		facts.backendCloses == 1 && facts.buildCalls == 1 && facts.runnerCalls == 1 &&
		facts.sensitiveWrites == 1 && facts.runnerResponseWrites == 1
}

func gdj0055ProvisionOutcomeOwnership(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	rollback, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer rollback.cleanup()
	rollback.observed.injectNextInsert(errors.New("GDJ-0055 injected insert failure"))
	rollbackFacts := gdj0055ProvisionOutcomeFacts{}
	rollbackErr := rollbackFacts.provision(ctx, rollback.observed, rollback.config)
	rollbackRows, rowErr := systemStateCountRows(ctx, rollback.raw, systemStateCredentialTable)
	if rowErr != nil || !errors.Is(rollbackErr, &systemstate.Error{Code: systemstate.CodePersistence}) || rollbackRows != 0 ||
		rollback.observed.atomicCalls.Load() != 1 || !rollbackFacts.exactDirectAttempt() {
		return protocol.Observation{}, fmt.Errorf("confirmed rollback facts: error=%v rows=%d row_error=%v atomics=%d attempts=%+v", rollbackErr, rollbackRows, rowErr, rollback.observed.atomicCalls.Load(), rollbackFacts)
	}

	unknown, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer unknown.cleanup()
	unknownBackend := &gdj0055CommitUnknownBackend{systemStateObservedBackend: unknown.observed}
	unknownFacts := gdj0055ProvisionOutcomeFacts{}
	unknownErr := unknownFacts.provision(ctx, unknownBackend, unknown.config)
	if !errors.Is(unknownErr, &systemstate.Error{Code: systemstate.CodePersistence}) ||
		!errors.Is(unknownErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || unknownBackend.calls.Load() != 1 ||
		!unknownFacts.exactDirectAttempt() {
		return protocol.Observation{}, fmt.Errorf("commit-unknown facts: error=%v calls=%d attempts=%+v", unknownErr, unknownBackend.calls.Load(), unknownFacts)
	}

	closeFixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer closeFixture.cleanup()
	closePolicy, err := closeFixture.config.credentialPolicy()
	if err != nil {
		return protocol.Observation{}, err
	}
	closeRequest, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{Username: []byte(closeFixture.config.Username), Password: []byte(closeFixture.config.Password)})
	if err != nil {
		return protocol.Observation{}, err
	}
	defer clear(closeRequest)
	closeBackend := &gdj0055LinkedBackend{Backend: closeFixture.observed, closeFailure: errors.New("GDJ-0055 close failure")}
	var closeResponse bytes.Buffer
	closeReport, closeRunErr := linked.RunCreatesuperuser(ctx, linked.CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (linked.SystemStateBackend, error) { return closeBackend, nil },
		CredentialPolicy:       closePolicy,
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(closeRequest), &closeResponse)
	parsedClose, closeFailure, closeFailed := createsuperuserprotocol.ParseResponse(closeResponse.Bytes(), closeRunErr == nil)
	closeRows, closeRowErr := systemStateCountRows(ctx, closeFixture.raw, systemStateCredentialTable)
	if closeRunErr != nil || closeRowErr != nil || closeFailed || closeFailure != (createsuperuserprotocol.Failure{}) ||
		parsedClose.OK || !closeReport.KnownCreated || !parsedClose.Failure.KnownCreated ||
		parsedClose.Failure.Code != createsuperuserprotocol.CodeBackendCloseFailed || closeRows != 1 {
		return protocol.Observation{}, fmt.Errorf("known-created close facts: report=%+v response=%+v failure=%+v error=%v rows=%d row_error=%v", closeReport, parsedClose, closeFailure, closeRunErr, closeRows, closeRowErr)
	}
	closeFacts := gdj0055LinkedOutcomeFacts(closeReport)
	if !closeFacts.exactLinkedAttempt() {
		return protocol.Observation{}, fmt.Errorf("known-created close attempt ownership=%+v", closeFacts)
	}

	outputFixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer outputFixture.cleanup()
	outputPolicy, err := outputFixture.config.credentialPolicy()
	if err != nil {
		return protocol.Observation{}, err
	}
	outputRequest, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{Username: []byte(outputFixture.config.Username), Password: []byte(outputFixture.config.Password)})
	if err != nil {
		return protocol.Observation{}, err
	}
	defer clear(outputRequest)
	outputBackend := &gdj0055LinkedBackend{Backend: outputFixture.observed}
	outputWriter := &gdj0055EventWriter{short: true}
	outputReport, outputErr := linked.RunCreatesuperuser(ctx, linked.CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (linked.SystemStateBackend, error) { return outputBackend, nil },
		CredentialPolicy:       outputPolicy,
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(outputRequest), outputWriter)
	outputRows, outputRowErr := systemStateCountRows(ctx, outputFixture.raw, systemStateCredentialTable)
	if outputErr == nil || outputRowErr != nil || !outputReport.KnownCreated || outputRows != 1 {
		return protocol.Observation{}, fmt.Errorf("known-created output facts: report=%+v error=%v rows=%d row_error=%v", outputReport, outputErr, outputRows, outputRowErr)
	}
	outputFacts := gdj0055LinkedOutcomeFacts(outputReport)
	if !outputFacts.exactLinkedAttempt() {
		return protocol.Observation{}, fmt.Errorf("known-created output attempt ownership=%+v", outputFacts)
	}

	workspaceKnown, workspaceRows, workspaceFacts, workspaceErr := gdj0055ObserveKnownCreatedWorkspaceCleanup(ctx)
	if workspaceErr != nil {
		return protocol.Observation{}, workspaceErr
	}
	if !workspaceKnown || workspaceRows != 1 || !workspaceFacts.exactGlobalAttempt() {
		return protocol.Observation{}, fmt.Errorf("known-created workspace facts known=%v rows=%d attempts=%+v", workspaceKnown, workspaceRows, workspaceFacts)
	}
	automaticRetries := rollbackFacts.retries() + unknownFacts.retries() + closeFacts.retries() +
		workspaceFacts.retries() + outputFacts.retries()

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"cases": protocol.List(
				gdj0055OutcomeCase("confirmed_rollback", "not_committed", false, false, rollbackFacts.retry()),
				protocol.Object(map[string]protocol.Value{
					"case":           protocol.String("commit_outcome_unknown"),
					"creation":       protocol.String("unknown"),
					"reconciliation": protocol.String("fresh_open_existing_or_login"),
					"retry":          protocol.Boolean(unknownFacts.retry()),
				}),
				gdj0055OutcomeCase("known_created_backend_close_failure", "preserved", true, true, closeFacts.retry()),
				gdj0055OutcomeCase("known_created_workspace_cleanup_failure", "preserved", true, true, workspaceFacts.retry()),
				gdj0055OutcomeCase("known_created_output_failure", "preserved", true, true, outputFacts.retry()),
			),
			"synthetic_success": protocol.Boolean(false),
		}),
		protocol.Object(map[string]protocol.Value{
			"commit_unknown_rows":     protocol.String("unknown"),
			"confirmed_rollback_rows": systemStateInt(rollbackRows),
			"known_created_rows":      systemStateInt(closeRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"automatic_retries":   systemStateInt64(automaticRetries),
			"creation_attempts":   systemStateInt64(rollbackFacts.attempts),
			"synthetic_successes": systemStateInt(0),
		}),
	)
}

func gdj0055OutcomeCase(name, creation string, knownCreated, includeKnown, retry bool) protocol.Value {
	fields := map[string]protocol.Value{
		"case":     protocol.String(name),
		"creation": protocol.String(creation),
		"retry":    protocol.Boolean(retry),
	}
	if includeKnown {
		fields["known_created"] = protocol.Boolean(knownCreated)
	}
	return protocol.Object(fields)
}

func gdj0055OpenExistingAuthenticator(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	fixture, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	observedHasher := &gdj0055ObservedHasher{PasswordHasher: fixture.config.PasswordHasher}
	fixture.config.PasswordHasher = observedHasher
	if err := systemStateProvisionOperator(ctx, fixture.observed, fixture.config); err != nil {
		return protocol.Observation{}, err
	}
	storedBefore, rows, err := gdj0046ReadCredentialSnapshot(ctx, fixture.raw)
	if err != nil || rows != 1 {
		return protocol.Observation{}, fmt.Errorf("read provisioned credential rows=%d: %w", rows, err)
	}
	fixture.resetDML()
	observedHasher.hashCalls.Store(0)
	observedHasher.verifyCalls.Store(0)
	runtime, validErr := systemStateOpenExisting(ctx, fixture.observed, fixture.config)
	if validErr != nil || runtime == nil || runtime.Authenticator() == nil || fixture.writes() != 0 ||
		observedHasher.verifyCalls.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf("valid OpenExisting facts: runtime=%v error=%v writes=%d hash=%d verify=%d", runtime, validErr, fixture.writes(), observedHasher.hashCalls.Load(), observedHasher.verifyCalls.Load())
	}

	corruptUpdates := []struct {
		column string
		value  any
	}{
		{column: "username", value: ""},
		{column: "encoded_password", value: "v9.invalid"},
		{column: "permissions", value: "v9.invalid"},
		{column: "definition_digest", value: "sha256:invalid"},
	}
	profileHasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 20_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	profileMismatch, err := profileHasher.Hash(ctx, fixture.config.Password)
	if err != nil {
		return protocol.Observation{}, err
	}
	corruptUpdates = append(corruptUpdates, struct {
		column string
		value  any
	}{column: "encoded_password", value: profileMismatch})
	validationErrors := make([]error, 0, len(corruptUpdates)+3)
	for _, mutation := range corruptUpdates {
		if _, err := fixture.raw.ExecContext(ctx, `UPDATE "godj_system_credential" SET "`+mutation.column+`" = ? WHERE "id" = 1`, mutation.value); err != nil {
			return protocol.Observation{}, err
		}
		fixture.resetDML()
		opened, openErr := systemStateOpenExisting(ctx, fixture.observed, fixture.config)
		if opened != nil || !errors.Is(openErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) || fixture.writes() != 0 {
			return protocol.Observation{}, fmt.Errorf("OpenExisting corrupt %s: runtime=%v error=%v writes=%d", mutation.column, opened, openErr, fixture.writes())
		}
		validationErrors = append(validationErrors, openErr)
		if err := gdj0055RestoreCredential(ctx, fixture, storedBefore); err != nil {
			return protocol.Observation{}, err
		}
	}

	for _, configure := range []func(*systemStateConfig){
		func(config *systemStateConfig) { config.PrincipalID += "-mismatch" },
		func(config *systemStateConfig) { config.Active = !config.Active },
		func(config *systemStateConfig) { config.Permissions = nil },
	} {
		candidate := fixture.config
		configure(&candidate)
		fixture.resetDML()
		opened, openErr := systemStateOpenExisting(ctx, fixture.observed, candidate)
		if opened != nil || !errors.Is(openErr, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) || fixture.writes() != 0 {
			return protocol.Observation{}, fmt.Errorf("OpenExisting policy mismatch: runtime=%v error=%v writes=%d", opened, openErr, fixture.writes())
		}
		validationErrors = append(validationErrors, openErr)
	}

	storedAfter, rows, err := gdj0046ReadCredentialSnapshot(ctx, fixture.raw)
	if err != nil || rows != 1 {
		return protocol.Observation{}, fmt.Errorf("read final credential rows=%d: %w", rows, err)
	}
	credentialMismatchOccurrences := 0
	for _, validationErr := range validationErrors {
		var stateErr *systemstate.Error
		if errors.As(validationErr, &stateErr) && stateErr != nil && string(stateErr.Code) == "credential_mismatch" {
			credentialMismatchOccurrences++
		}
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"raw_secret_required": protocol.Boolean(false),
			"startup":             protocol.String("authenticator_ready_from_stored_state"),
			"validated_fields": protocol.List(
				protocol.String("username"),
				protocol.String("encoded_credential"),
				protocol.String("hash_profile"),
				protocol.String("principal"),
				protocol.String("active"),
				protocol.String("permissions"),
				protocol.String("definition_digest"),
			),
			"validation_cases": protocol.List(
				gdj0055ValidationCase("valid_stored_state", "authenticator_ready"),
				gdj0055ValidationCase("malformed_or_profile_invalid", string(systemstate.CodeCorruptState)),
				gdj0055ValidationCase("policy_mismatch", string(systemstate.CodeCredentialPolicyMismatch)),
			),
		}),
		protocol.Object(map[string]protocol.Value{
			"credential_rows":                     systemStateInt(rows),
			"open_existing_writes":                systemStateInt64(fixture.writes()),
			"stored_encoded_credential_preserved": protocol.Boolean(storedBefore.encodedPassword == storedAfter.encodedPassword),
		}),
		protocol.Object(map[string]protocol.Value{
			"authenticator_constructions":          systemStateInt(systemStateBoolInt(runtime.Authenticator() != nil)),
			"credential_mismatch_code_occurrences": systemStateInt(credentialMismatchOccurrences),
			"raw_secret_reads":                     systemStateInt64(observedHasher.verifyCalls.Load()),
			"startup_writes":                       systemStateInt64(fixture.writes()),
		}),
	)
}

func gdj0055RestoreCredential(ctx context.Context, fixture *gdj0055StateFixture, snapshot gdj0046CredentialSnapshot) error {
	_, err := fixture.raw.ExecContext(ctx, `UPDATE "godj_system_credential" SET "principal_id" = ?, "username" = ?, "encoded_password" = ?, "active" = ?, "permissions" = ?, "definition_digest" = ? WHERE "id" = ?`,
		snapshot.principalID, snapshot.username, snapshot.encodedPassword, snapshot.active, snapshot.permissions, snapshot.definitionDigest, snapshot.id)
	return err
}

func gdj0055ValidationCase(name, outcome string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"case":    protocol.String(name),
		"outcome": protocol.String(outcome),
	})
}

func gdj0055CredentialAbsentPublicOnly(
	ctx context.Context,
	contract protocol.Contract,
	_ GDJ0055Inputs,
) (protocol.Observation, error) {
	type observedCase struct {
		name       string
		outcome    string
		publicOnly bool
		writes     int64
	}
	cases := make([]observedCase, 0, 6)

	empty, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer empty.cleanup()
	empty.resetDML()
	emptyComposition, startupErr := siteappconformance.ObserveComposition(ctx, empty.observed)
	if startupErr != nil || !emptyComposition.PublicOnly() || empty.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("empty Article composition observation=%+v error=%v writes=%d", emptyComposition, startupErr, empty.writes())
	}
	cases = append(cases, observedCase{"exact_migration_and_all_system_state_empty", string(systemstate.CodeCredentialAbsent), emptyComposition.PublicOnly(), empty.writes()})

	missing, err := newGDJ0055UnmigratedBackend(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer missing.cleanup()
	missing.resetDML()
	missingComposition, startupErr := siteappconformance.ObserveComposition(ctx, missing.observed)
	if missingComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) || missing.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("missing migration Article composition=%+v error=%v writes=%d", missingComposition, startupErr, missing.writes())
	}

	wrongHistory, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer wrongHistory.cleanup()
	if _, err := wrongHistory.raw.ExecContext(ctx, `INSERT INTO "godj_migrations" ("app", "name") VALUES ('godj_system', '9999_wrong')`); err != nil {
		return protocol.Observation{}, err
	}
	wrongHistory.resetDML()
	wrongComposition, startupErr := siteappconformance.ObserveComposition(ctx, wrongHistory.observed)
	if wrongComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) || wrongHistory.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("wrong migration history Article composition=%+v error=%v writes=%d", wrongComposition, startupErr, wrongHistory.writes())
	}
	cases = append(cases, observedCase{
		"missing_or_wrong_migration",
		string(systemstate.CodeSchemaUnavailable),
		missingComposition.PublicOnly() || wrongComposition.PublicOnly(),
		missing.writes() + wrongHistory.writes(),
	})

	unavailable, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer unavailable.cleanup()
	if _, err := unavailable.raw.ExecContext(ctx, `DROP TABLE "godj_system_session"`); err != nil {
		return protocol.Observation{}, err
	}
	unavailable.resetDML()
	unavailableComposition, startupErr := siteappconformance.ObserveComposition(ctx, unavailable.observed)
	if unavailableComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) || unavailable.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("unavailable table Article composition=%+v error=%v writes=%d", unavailableComposition, startupErr, unavailable.writes())
	}
	cases = append(cases, observedCase{"unavailable_table", "startup_failure", unavailableComposition.PublicOnly(), unavailable.writes()})

	dependent, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer dependent.cleanup()
	if _, err := dependent.raw.ExecContext(ctx, `INSERT INTO "godj_system_session" ("id", "digest", "payload") VALUES (1, 'dependent', 'dependent')`); err != nil {
		return protocol.Observation{}, err
	}
	dependent.resetDML()
	dependentComposition, startupErr := siteappconformance.ObserveComposition(ctx, dependent.observed)
	if dependentComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) || dependent.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("dependent row Article composition=%+v error=%v writes=%d", dependentComposition, startupErr, dependent.writes())
	}

	dependentAudit, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer dependentAudit.cleanup()
	if _, err := dependentAudit.raw.ExecContext(ctx, `INSERT INTO "godj_system_audit" (`+
		`"actor_id", "model", "object_id", "action", "changed_fields", "display_label"`+
		`) VALUES ('operator', 'article.article', '1', 'add', 'v1.AAA', 'dependent')`); err != nil {
		return protocol.Observation{}, err
	}
	dependentAudit.resetDML()
	dependentAuditComposition, startupErr := siteappconformance.ObserveComposition(ctx, dependentAudit.observed)
	if dependentAuditComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) || dependentAudit.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("dependent audit row Article composition=%+v error=%v writes=%d", dependentAuditComposition, startupErr, dependentAudit.writes())
	}
	cases = append(cases, observedCase{
		"dependent_rows_without_credential",
		"startup_failure",
		dependentComposition.PublicOnly() || dependentAuditComposition.PublicOnly(),
		dependent.writes() + dependentAudit.writes(),
	})

	corrupt, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer corrupt.cleanup()
	if err := systemStateProvisionOperator(ctx, corrupt.observed, corrupt.config); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := corrupt.raw.ExecContext(ctx, `UPDATE "godj_system_credential" SET "encoded_password" = 'v9.invalid' WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	corrupt.resetDML()
	corruptComposition, startupErr := siteappconformance.ObserveComposition(ctx, corrupt.observed)
	if corruptComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) || corrupt.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("corrupt Article composition=%+v error=%v writes=%d", corruptComposition, startupErr, corrupt.writes())
	}
	cases = append(cases, observedCase{"corrupt_state", string(systemstate.CodeCorruptState), corruptComposition.PublicOnly(), corrupt.writes()})

	mismatch, err := newGDJ0055StateFixture(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer mismatch.cleanup()
	if err := siteappconformance.ProvisionPolicyMismatch(
		ctx,
		mismatch.observed,
		mismatch.config.Username,
		mismatch.config.Password,
	); err != nil {
		return protocol.Observation{}, err
	}
	mismatch.resetDML()
	mismatchComposition, startupErr := siteappconformance.ObserveComposition(ctx, mismatch.observed)
	if mismatchComposition.ApplicationCreated || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) || mismatch.writes() != 0 {
		return protocol.Observation{}, fmt.Errorf("mismatch Article composition=%+v error=%v writes=%d", mismatchComposition, startupErr, mismatch.writes())
	}
	cases = append(cases, observedCase{"policy_mismatch", string(systemstate.CodeCredentialPolicyMismatch), mismatchComposition.PublicOnly(), mismatch.writes()})

	values := make([]protocol.Value, len(cases))
	var writes int64
	var absentBranches, downgraded int
	for index, candidate := range cases {
		values[index] = protocol.Object(map[string]protocol.Value{
			"case":        protocol.String(candidate.name),
			"outcome":     protocol.String(candidate.outcome),
			"public_only": protocol.Boolean(candidate.publicOnly),
		})
		writes += candidate.writes
		if candidate.outcome == string(systemstate.CodeCredentialAbsent) {
			absentBranches++
		}
		if candidate.publicOnly && candidate.outcome != string(systemstate.CodeCredentialAbsent) {
			downgraded++
		}
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"cases":             protocol.List(values...),
			"failure_downgrade": protocol.Boolean(downgraded != 0),
		}),
		protocol.Object(map[string]protocol.Value{
			"downgraded_failure_cases": systemStateInt(downgraded),
			"public_only_mutations":    systemStateInt64(empty.writes()),
			"required_empty_stores": protocol.List(
				protocol.String("credential"),
				protocol.String("session"),
				protocol.String("audit"),
			),
		}),
		protocol.Object(map[string]protocol.Value{
			"credential_absent_branches": systemStateInt(absentBranches),
			"failure_downgrades":         systemStateInt(downgraded),
			"startup_writes":             systemStateInt64(writes),
		}),
	)
}

var _ io.Writer = (*gdj0055EventWriter)(nil)
