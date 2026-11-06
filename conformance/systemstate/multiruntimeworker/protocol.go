// Package multiruntimeworker provides the distinct-process system-state
// coordination sentinel used by the GoDj conformance runner.
//
// Database and bootstrap material crosses a process boundary only through an
// inherited anonymous descriptor. Standard output contains one bounded,
// secret-free response and standard error contains, at most, a fixed failure
// sentence emitted by the command wrapper.
package multiruntimeworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	wireFormatVersion  = 1
	maximumConfigBytes = 64 << 10
	maximumOutputBytes = 16 << 10

	bootstrapUsername  = "godj-multiruntime-admin"
	bootstrapPrincipal = "godj-multiruntime-principal"
	auditModel         = "godj_conformance.multiruntime"
	holderActor        = "godj-multiruntime-holder"
	contenderActor     = "godj-multiruntime-contender"
)

// BackendKind is the closed database profile understood by the worker.
type BackendKind string

const (
	BackendSQLite   BackendKind = "sqlite"
	BackendPostgres BackendKind = "postgresql"
)

type databaseConfigState struct {
	kind             BackendKind
	sqlitePath       string
	sqliteDataSource string
	postgresURL      string
	postgresSchema   string
}

// DatabaseConfig is an opaque, redacted database target. Keeping the actual
// strings behind a private pointer prevents invalid fmt verbs from traversing
// its fields.
type DatabaseConfig struct{ state *databaseConfigState }

// NewSQLiteDatabase identifies one absolute SQLite file. The worker adds the
// bounded busy timeout required for two distinct writers to wait on one
// BEGIN IMMEDIATE fence.
func NewSQLiteDatabase(path string) (DatabaseConfig, error) {
	if path == "" || len(path) > 4096 || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return DatabaseConfig{}, newError(CodeInvalidConfig)
	}
	clean := filepath.Clean(path)
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(clean)}
	query := location.Query()
	query.Set("mode", "rwc")
	query.Set("_busy_timeout", "15000")
	location.RawQuery = query.Encode()
	return DatabaseConfig{state: &databaseConfigState{
		kind:             BackendSQLite,
		sqlitePath:       clean,
		sqliteDataSource: location.String(),
	}}, nil
}

// NewPostgresDatabase identifies one already-created PostgreSQL schema. The
// backend performs the authoritative URL, schema, and PostgreSQL 17 profile
// validation when the scenario starts.
func NewPostgresDatabase(databaseURL, schema string) (DatabaseConfig, error) {
	if databaseURL == "" || len(databaseURL) > 8192 || strings.ContainsRune(databaseURL, 0) ||
		len(schema) < 8 || len(schema) > 63 || strings.ContainsRune(schema, 0) {
		return DatabaseConfig{}, newError(CodeInvalidConfig)
	}
	return DatabaseConfig{state: &databaseConfigState{
		kind:           BackendPostgres,
		postgresURL:    databaseURL,
		postgresSchema: schema,
	}}, nil
}

func (DatabaseConfig) String() string   { return "multiruntimeworker.DatabaseConfig{redacted}" }
func (DatabaseConfig) GoString() string { return "multiruntimeworker.DatabaseConfig{redacted}" }

// Format keeps even deliberately unsuitable diagnostic verbs from walking the
// opaque value. The pointer verb reports only the address of the private state.
func (config DatabaseConfig) Format(state fmt.State, verb rune) {
	if verb == 'p' {
		_, _ = fmt.Fprintf(state, "%p", config.state)
		return
	}
	_, _ = io.WriteString(state, config.String())
}

// MarshalJSON never publishes database connection material.
func (DatabaseConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal("multiruntimeworker.DatabaseConfig{redacted}")
}

func (config DatabaseConfig) valid() bool {
	if config.state == nil {
		return false
	}
	switch config.state.kind {
	case BackendSQLite:
		return config.state.sqlitePath != "" && config.state.sqliteDataSource != "" &&
			config.state.postgresURL == "" && config.state.postgresSchema == ""
	case BackendPostgres:
		return config.state.sqlitePath == "" && config.state.sqliteDataSource == "" &&
			config.state.postgresURL != "" && config.state.postgresSchema != ""
	default:
		return false
	}
}

type workerRole string

const (
	roleHolder    workerRole = "holder"
	roleContender workerRole = "contender"
	roleProbe     workerRole = "probe"
)

// descriptorEvent and descriptorControl form a fixed single-byte protocol.
// No variable text or caller value can enter these synchronization pipes.
type descriptorEvent byte

const (
	eventReady     descriptorEvent = 0x31
	eventAttempted descriptorEvent = 0x32
	eventAcquired  descriptorEvent = 0x33
	eventWaiting   descriptorEvent = 0x34
)

type descriptorControl byte

const (
	controlStart   descriptorControl = 0x41
	controlRelease descriptorControl = 0x42
)

type wireConfig struct {
	FormatVersion int         `json:"format_version"`
	Role          workerRole  `json:"role"`
	Backend       BackendKind `json:"backend"`

	SQLiteDataSource string `json:"sqlite_data_source"`
	PostgresURL      string `json:"postgres_url"`
	PostgresSchema   string `json:"postgres_schema"`

	Username  string `json:"username"`
	Password  string `json:"password"`
	Principal string `json:"principal"`
	ObjectID  int64  `json:"object_id"`
}

func (wireConfig) String() string   { return "multiruntimeworker.wireConfig{redacted}" }
func (wireConfig) GoString() string { return "multiruntimeworker.wireConfig{redacted}" }

func newWireConfig(database DatabaseConfig, role workerRole, password string, objectID int64) (wireConfig, error) {
	if !database.valid() || !validRole(role) || password == "" || len(password) > 1024 ||
		strings.ContainsRune(password, 0) || objectID <= 0 {
		return wireConfig{}, newError(CodeInvalidConfig)
	}
	return wireConfig{
		FormatVersion:    wireFormatVersion,
		Role:             role,
		Backend:          database.state.kind,
		SQLiteDataSource: database.state.sqliteDataSource,
		PostgresURL:      database.state.postgresURL,
		PostgresSchema:   database.state.postgresSchema,
		Username:         bootstrapUsername,
		Password:         password,
		Principal:        bootstrapPrincipal,
		ObjectID:         objectID,
	}, nil
}

func decodeWireConfig(reader io.Reader) (wireConfig, error) {
	if reader == nil {
		return wireConfig{}, newError(CodeProtocol)
	}
	document, err := io.ReadAll(io.LimitReader(reader, maximumConfigBytes+1))
	if err != nil || len(document) > maximumConfigBytes || !utf8.Valid(document) {
		return wireConfig{}, newError(CodeInvalidConfig)
	}
	if !uniqueTopLevelKeys(document) {
		return wireConfig{}, newError(CodeInvalidConfig)
	}
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.DisallowUnknownFields()
	var config wireConfig
	if err := decoder.Decode(&config); err != nil {
		return wireConfig{}, newError(CodeInvalidConfig)
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return wireConfig{}, newError(CodeInvalidConfig)
	}
	if err := validateWireConfig(config); err != nil {
		return wireConfig{}, err
	}
	return config, nil
}

func validateWireConfig(config wireConfig) error {
	if config.FormatVersion != wireFormatVersion || !validRole(config.Role) || config.ObjectID <= 0 ||
		config.Username != bootstrapUsername || config.Principal != bootstrapPrincipal ||
		config.Password == "" || len(config.Password) > 1024 || strings.ContainsRune(config.Password, 0) {
		return newError(CodeInvalidConfig)
	}
	switch config.Backend {
	case BackendSQLite:
		if config.SQLiteDataSource == "" || len(config.SQLiteDataSource) > 8192 ||
			strings.ContainsRune(config.SQLiteDataSource, 0) || config.PostgresURL != "" || config.PostgresSchema != "" {
			return newError(CodeInvalidConfig)
		}
	case BackendPostgres:
		if config.SQLiteDataSource != "" || config.PostgresURL == "" || len(config.PostgresURL) > 8192 ||
			strings.ContainsRune(config.PostgresURL, 0) || config.PostgresSchema == "" ||
			len(config.PostgresSchema) < 8 || len(config.PostgresSchema) > 63 || strings.ContainsRune(config.PostgresSchema, 0) {
			return newError(CodeInvalidConfig)
		}
	default:
		return newError(CodeInvalidConfig)
	}
	return nil
}

func validRole(role workerRole) bool {
	switch role {
	case roleHolder, roleContender, roleProbe:
		return true
	default:
		return false
	}
}

// ErrorCode is a closed, secret-free harness failure class.
type ErrorCode string

const (
	CodeInvalidConfig ErrorCode = "invalid_config"
	CodeProtocol      ErrorCode = "protocol_failure"
	CodeProcess       ErrorCode = "process_failure"
	CodeDatabase      ErrorCode = "database_failure"
	CodeMigration     ErrorCode = "migration_failure"
	CodeRuntime       ErrorCode = "runtime_failure"
	CodeCoordination  ErrorCode = "coordination_failure"
	CodePersistence   ErrorCode = "persistence_failure"
	CodeContext       ErrorCode = "context_failure"
	CodeUnsupported   ErrorCode = "unsupported_platform"
)

// Error deliberately retains only a closed code. Database, process, and JSON
// causes can contain connection material and are never wrapped across this
// conformance boundary.
type Error struct{ Code ErrorCode }

func (err *Error) Error() string {
	if err == nil || !validErrorCode(err.Code) {
		return "system-state multi-runtime worker failed"
	}
	return "system-state multi-runtime worker failed: " + string(err.Code)
}

// Is compares only the closed failure code.
func (err *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && err != nil && other != nil && err.Code == other.Code
}

func newError(code ErrorCode) error { return &Error{Code: code} }

func validErrorCode(code ErrorCode) bool {
	switch code {
	case CodeInvalidConfig, CodeProtocol, CodeProcess, CodeDatabase, CodeMigration,
		CodeRuntime, CodeCoordination, CodePersistence, CodeContext, CodeUnsupported:
		return true
	default:
		return false
	}
}

type workerResponse struct {
	FormatVersion int         `json:"format_version"`
	OK            bool        `json:"ok"`
	ErrorCode     ErrorCode   `json:"error_code"`
	Role          workerRole  `json:"role"`
	Backend       BackendKind `json:"backend"`
	PID           int         `json:"pid"`

	Opened                   bool `json:"opened"`
	CallbackInvocations      int  `json:"callback_invocations"`
	EventAppended            bool `json:"event_appended"`
	HistoryCount             int  `json:"history_count"`
	HolderEvents             int  `json:"holder_events"`
	ContenderEvents          int  `json:"contender_events"`
	UnexpectedEvents         int  `json:"unexpected_events"`
	StrictlyIncreasing       bool `json:"strictly_increasing"`
	DurableSecretOccurrences int  `json:"durable_secret_occurrences"`
}

func (workerResponse) String() string   { return "multiruntimeworker.workerResponse{safe-observation}" }
func (workerResponse) GoString() string { return "multiruntimeworker.workerResponse{safe-observation}" }

func decodeWorkerResponse(reader io.Reader) (workerResponse, error) {
	if reader == nil {
		return workerResponse{}, newError(CodeProtocol)
	}
	document, err := io.ReadAll(io.LimitReader(reader, maximumOutputBytes+1))
	if err != nil || len(document) > maximumOutputBytes || !utf8.Valid(document) {
		return workerResponse{}, newError(CodeProtocol)
	}
	if !uniqueTopLevelKeys(document) {
		return workerResponse{}, newError(CodeProtocol)
	}
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.DisallowUnknownFields()
	var response workerResponse
	if err := decoder.Decode(&response); err != nil {
		return workerResponse{}, newError(CodeProtocol)
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return workerResponse{}, newError(CodeProtocol)
	}
	identityValid := validRole(response.Role) && (response.Backend == BackendSQLite || response.Backend == BackendPostgres)
	identityUnavailable := response.Role == "" && response.Backend == "" && !response.OK &&
		(response.ErrorCode == CodeInvalidConfig || response.ErrorCode == CodeProtocol)
	if response.FormatVersion != wireFormatVersion || (!identityValid && !identityUnavailable) || response.PID <= 0 ||
		response.CallbackInvocations < 0 || response.CallbackInvocations > 1 || response.HistoryCount < 0 ||
		response.HolderEvents < 0 || response.ContenderEvents < 0 || response.UnexpectedEvents < 0 ||
		response.DurableSecretOccurrences < 0 ||
		(response.OK && response.ErrorCode != "") || (!response.OK && !validErrorCode(response.ErrorCode)) {
		return workerResponse{}, newError(CodeProtocol)
	}
	return response, nil
}

func uniqueTopLevelKeys(document []byte) bool {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	end, err := decoder.Token()
	return err == nil && end == json.Delim('}')
}

// Facts is the normalized, secret-free observation consumed by SYS-020. A
// completed scenario can report negative semantic facts without turning them
// into a transport error; protocol/database failures still fail closed.
type Facts struct {
	WriterProcesses              int  `json:"writer_processes"`
	SameSchema                   bool `json:"same_schema"`
	BarrierLinearized            bool `json:"barrier_linearized"`
	HolderCallbackInvocations    int  `json:"holder_callback_invocations"`
	ContenderCallbackInvocations int  `json:"contender_callback_invocations"`
	RestartPreserved             bool `json:"restart_preserved"`
	DurableEvents                int  `json:"durable_events"`
	Divergence                   bool `json:"divergence"`
	Loss                         bool `json:"loss"`
	Drift                        bool `json:"drift"`
	SecretOccurrences            int  `json:"secret_occurrences"`
}

func (Facts) String() string   { return "multiruntimeworker.Facts{safe-observation}" }
func (Facts) GoString() string { return "multiruntimeworker.Facts{safe-observation}" }
