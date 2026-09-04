package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const maximumRequestBytes = 1 << 20

const (
	errorInvalidRequest    = "invalid_request"
	errorUnsupportedAction = "unsupported_action"
	errorDatabase          = "database_failure"
	errorMigration         = "migration_failure"
	errorApplication       = "application_failure"
	errorHTTP              = "http_failure"
	errorPersistence       = "persistence_failure"
	errorProtocol          = "protocol_failure"
)

type safeError struct{ code string }

func (err *safeError) Error() string {
	if err == nil || err.code == "" {
		return "system-state worker failed"
	}
	return "system-state worker failed: " + err.code
}

func fail(code string) error { return &safeError{code: code} }

func errorCode(err error) string {
	var classified *safeError
	if errors.As(err, &classified) && classified.code != "" {
		return classified.code
	}
	return errorProtocol
}

// Run decodes one request, executes it, writes secret output first to the
// caller-provided descriptor writer, and then writes the safe observation to
// stdout. Execution errors are returned only after both JSON envelopes have
// been attempted; their text is a closed code and never wraps a cause.
func Run(ctx context.Context, stdin io.Reader, stdout, secretWriter io.Writer) error {
	if ctx == nil || stdin == nil || stdout == nil || secretWriter == nil {
		return fail(errorProtocol)
	}
	request, err := decodeRequest(stdin)
	response := Response{PID: os.Getpid()}
	secret := SecretBundle{}
	if err == nil {
		response.Action = request.Action
		response, secret, err = Execute(ctx, request)
	}
	if err != nil {
		response.OK = false
		response.ErrorCode = errorCode(err)
		response.PID = os.Getpid()
		secret = SecretBundle{}
	}
	if encodeErr := json.NewEncoder(secretWriter).Encode(secret); encodeErr != nil {
		return fail(errorProtocol)
	}
	if encodeErr := json.NewEncoder(stdout).Encode(response); encodeErr != nil {
		return fail(errorProtocol)
	}
	return err
}

func decodeRequest(reader io.Reader) (Request, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fail(errorInvalidRequest)
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Request{}, fail(errorInvalidRequest)
	}
	if err := validateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func validateRequest(request Request) error {
	if !validAction(request.Action) {
		return fail(errorUnsupportedAction)
	}
	if strings.TrimSpace(request.Database) == "" || len(request.Database) > 4096 ||
		strings.ContainsRune(request.Database, 0) {
		return fail(errorInvalidRequest)
	}
	if strings.TrimSpace(request.Username) == "" || len(request.Username) > 256 ||
		strings.ContainsRune(request.Username, 0) || request.Username != strings.TrimSpace(request.Username) {
		return fail(errorInvalidRequest)
	}
	if request.Password == "" || len(request.Password) > 4096 || strings.ContainsRune(request.Password, 0) {
		return fail(errorInvalidRequest)
	}
	if len(request.Cookies.Session) > 4096 || len(request.Cookies.CSRF) > 4096 || len(request.Token) > 4096 ||
		len(request.RepositoryRoot) > 4096 || strings.ContainsRune(request.RepositoryRoot, 0) {
		return fail(errorInvalidRequest)
	}
	if request.Action == ActionHistoryRead && request.ObjectID <= 0 {
		return fail(errorInvalidRequest)
	}
	return nil
}

func validAction(action Action) bool {
	switch action {
	case ActionInitialize, ActionAuthenticate, ActionLogin, ActionSessionProbe,
		ActionLogout, ActionOldCookieProbe, ActionCSRFSetup, ActionCookieProbe,
		ActionCSRFStale, ActionCSRFFresh, ActionAuditFault, ActionHistoryWrite,
		ActionHistoryRead:
		return true
	default:
		return false
	}
}

// Execute performs one already-decoded action. Callers embedding the worker
// package must preserve the same stdout/FD-3 separation enforced by Run.
func Execute(ctx context.Context, request Request) (Response, SecretBundle, error) {
	if ctx == nil || ctx.Err() != nil {
		return Response{}, SecretBundle{}, fail(errorInvalidRequest)
	}
	if err := validateRequest(request); err != nil {
		return Response{}, SecretBundle{}, err
	}
	response := Response{OK: true, Action: request.Action, PID: os.Getpid()}
	var secret SecretBundle
	var err error
	switch request.Action {
	case ActionInitialize:
		response, secret, err = initialize(ctx, request, response)
	case ActionAuthenticate:
		response, secret, err = authenticate(ctx, request, response)
	case ActionLogin:
		response, secret, err = login(ctx, request, response)
	case ActionSessionProbe, ActionCookieProbe:
		response, secret, err = sessionProbe(ctx, request, response)
	case ActionLogout:
		response, secret, err = logout(ctx, request, response)
	case ActionOldCookieProbe:
		response, secret, err = oldCookieProbe(ctx, request, response)
	case ActionCSRFSetup:
		response, secret, err = csrfIssueAndMutate(ctx, request, response, "CSRF setup")
	case ActionCSRFStale:
		response, secret, err = csrfStale(ctx, request, response)
	case ActionCSRFFresh:
		response, secret, err = csrfIssueAndMutate(ctx, request, response, "CSRF fresh")
	case ActionAuditFault:
		response, secret, err = auditFault(ctx, request, response)
	case ActionHistoryWrite:
		response, secret, err = historyWrite(ctx, request, response)
	case ActionHistoryRead:
		response, secret, err = historyRead(ctx, request, response)
	default:
		err = fail(errorUnsupportedAction)
	}
	if err != nil {
		response.OK = false
		response.ErrorCode = errorCode(err)
		return response, SecretBundle{}, err
	}
	return response, secret, nil
}
