package multiruntimeworker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

const contenderWaitingProofWindow = 500 * time.Millisecond

type scenarioBackend interface {
	systemstate.Backend
	migrationbackend.RevisionFencedBackend
	Close() error
}

// RunWorker runs one child role using only explicit streams. configReader is
// the inherited private configuration descriptor; eventWriter and
// controlReader are fixed-byte anonymous synchronization pipes.
func RunWorker(
	ctx context.Context,
	configReader io.Reader,
	eventWriter io.Writer,
	controlReader io.Reader,
	stdout io.Writer,
) error {
	response := workerResponse{FormatVersion: wireFormatVersion, PID: os.Getpid()}
	config, err := decodeWireConfig(configReader)
	if err == nil {
		response.Role = config.Role
		response.Backend = config.Backend
		response, err = executeWorker(ctx, config, eventWriter, controlReader, response)
	}
	if err != nil {
		response.OK = false
		response.ErrorCode = errorCode(err)
		response.PID = os.Getpid()
	}
	if stdout == nil || json.NewEncoder(stdout).Encode(response) != nil {
		return newError(CodeProtocol)
	}
	return err
}

func executeWorker(
	ctx context.Context,
	config wireConfig,
	eventWriter io.Writer,
	controlReader io.Reader,
	response workerResponse,
) (workerResponse, error) {
	if ctx == nil || ctx.Err() != nil {
		return response, newError(CodeContext)
	}
	if eventWriter == nil || controlReader == nil {
		return response, newError(CodeProtocol)
	}
	backend, err := openScenarioBackend(ctx, config)
	if err != nil {
		return response, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()

	runtimeConfig, err := openExistingConfig(config)
	if err != nil {
		return response, newError(CodeRuntime)
	}
	runtime, err := systemstate.OpenExisting(ctx, backend, runtimeConfig)
	if err != nil {
		return response, newError(CodeRuntime)
	}
	response.Opened = true
	if config.Role == roleProbe {
		response, err = probeHistory(ctx, runtime, config, response)
	} else {
		response, err = executeWriter(ctx, runtime, config, eventWriter, controlReader, response)
	}
	if err != nil {
		return response, err
	}
	if err := backend.Close(); err != nil {
		return response, newError(CodeDatabase)
	}
	closed = true
	response.OK = true
	return response, nil
}

func executeWriter(
	ctx context.Context,
	runtime *systemstate.Runtime,
	config wireConfig,
	eventWriter io.Writer,
	controlReader io.Reader,
	response workerResponse,
) (workerResponse, error) {
	if err := emitEvent(eventWriter, eventReady); err != nil {
		return response, err
	}
	if err := expectControl(controlReader, controlStart); err != nil {
		return response, err
	}
	if config.Role == roleContender {
		if err := emitEvent(eventWriter, eventAttempted); err != nil {
			return response, err
		}
	}

	event, err := scenarioAuditEvent(config)
	if err != nil {
		return response, newError(CodePersistence)
	}
	callbackEntered := make(chan struct{})
	waitingProof := make(chan error, 1)
	if config.Role == roleContender {
		timer := time.NewTimer(contenderWaitingProofWindow)
		defer timer.Stop()
		go func() {
			select {
			case <-callbackEntered:
				waitingProof <- nil
			case <-timer.C:
				waitingProof <- emitEvent(eventWriter, eventWaiting)
			}
		}()
	}
	err = runtime.Atomic(ctx, func(session db.Session) error {
		response.CallbackInvocations++
		close(callbackEntered)
		if err := emitEvent(eventWriter, eventAcquired); err != nil {
			return err
		}
		if config.Role == roleHolder {
			if err := expectControl(controlReader, controlRelease); err != nil {
				return err
			}
		}
		if err := runtime.AppendAudit(ctx, session, event); err != nil {
			return err
		}
		response.EventAppended = true
		return nil
	})
	if config.Role == roleContender {
		if proofErr := <-waitingProof; proofErr != nil {
			return response, proofErr
		}
	}
	if err != nil {
		// Runtime/backend own rollback, commit-unknown classification, and the
		// exact once/no-retry contract. The worker deliberately makes no second
		// attempt and publishes no cause text.
		return response, newError(CodeCoordination)
	}
	return response, nil
}

func scenarioAuditEvent(config wireConfig) (admin.PreparedEvent, error) {
	actor := holderActor
	action := admin.ActionAdd
	label := "distinct holder publication"
	if config.Role == roleContender {
		actor = contenderActor
		action = admin.ActionChange
		label = "distinct contender publication"
	}
	return admin.PrepareEvent(actor, auditModel, config.ObjectID, action, []string{"coordination"}, label)
}

func probeHistory(
	ctx context.Context,
	runtime *systemstate.Runtime,
	config wireConfig,
	response workerResponse,
) (workerResponse, error) {
	history, err := runtime.AuditHistory(ctx, auditModel, config.ObjectID, 4)
	if err != nil {
		return response, newError(CodePersistence)
	}
	response.HistoryCount = len(history)
	response.StrictlyIncreasing = true
	var previous uint64
	for _, entry := range history {
		if entry.Sequence <= previous {
			response.StrictlyIncreasing = false
		}
		previous = entry.Sequence
		switch {
		case entry.ActorID == holderActor && entry.Action == admin.ActionAdd:
			response.HolderEvents++
		case entry.ActorID == contenderActor && entry.Action == admin.ActionChange:
			response.ContenderEvents++
		default:
			response.UnexpectedEvents++
		}
	}
	secretOccurrences, err := inspectDurableSecretOccurrences(ctx, runtime, config)
	if err != nil {
		return response, err
	}
	response.DurableSecretOccurrences = secretOccurrences
	return response, nil
}

func emitEvent(writer io.Writer, event descriptorEvent) error {
	if writer == nil {
		return newError(CodeProtocol)
	}
	written, err := writer.Write([]byte{byte(event)})
	if err != nil || written != 1 {
		return newError(CodeProtocol)
	}
	return nil
}

func expectControl(reader io.Reader, want descriptorControl) error {
	if reader == nil {
		return newError(CodeProtocol)
	}
	var value [1]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil || descriptorControl(value[0]) != want {
		return newError(CodeProtocol)
	}
	return nil
}

func openScenarioBackend(ctx context.Context, config wireConfig) (scenarioBackend, error) {
	switch config.Backend {
	case BackendSQLite:
		backend, err := sqlite.Open(ctx, config.SQLiteDataSource)
		if err != nil {
			return nil, newError(CodeDatabase)
		}
		return backend, nil
	case BackendPostgres:
		backend, err := postgres.Open(ctx, postgres.Config{URL: config.PostgresURL, Schema: config.PostgresSchema})
		if err != nil {
			return nil, newError(CodeDatabase)
		}
		return backend, nil
	default:
		return nil, newError(CodeInvalidConfig)
	}
}

func prepareScenarioDatabase(ctx context.Context, config wireConfig) error {
	backend, err := openScenarioBackend(ctx, config)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()
	loaded, _, err := migrationdefinition.Load(systemstate.InitialDefinitionSource())
	if err != nil {
		return newError(CodeMigration)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	); err != nil {
		return newError(CodeMigration)
	}
	provision, err := provisionOperatorConfig(config)
	if err != nil {
		return newError(CodeRuntime)
	}
	if err := systemstate.ProvisionOperator(ctx, backend, provision); err != nil {
		return newError(CodeRuntime)
	}
	if err := backend.Close(); err != nil {
		return newError(CodeDatabase)
	}
	closed = true
	return nil
}

func credentialPolicy(config wireConfig) (systemstate.CredentialPolicy, error) {
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     config.Principal,
		Active: true,
	})
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	return systemstate.CredentialPolicy{
		Principal:      principal,
		PasswordHasher: hasher,
	}, nil
}

func provisionOperatorConfig(config wireConfig) (systemstate.ProvisionOperatorConfig, error) {
	policy, err := credentialPolicy(config)
	if err != nil {
		return systemstate.ProvisionOperatorConfig{}, err
	}
	return systemstate.ProvisionOperatorConfig{
		Username:         config.Username,
		Password:         config.Password,
		CredentialPolicy: policy,
	}, nil
}

func openExistingConfig(config wireConfig) (systemstate.RuntimeConfig, error) {
	policy, err := credentialPolicy(config)
	if err != nil {
		return systemstate.RuntimeConfig{}, err
	}
	return systemstate.RuntimeConfig{CredentialPolicy: policy}, nil
}

func errorCode(err error) ErrorCode {
	var classified *Error
	if errors.As(err, &classified) && classified != nil && validErrorCode(classified.Code) {
		return classified.Code
	}
	return CodeProtocol
}
