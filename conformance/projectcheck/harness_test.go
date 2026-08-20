//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
)

const (
	maxDescriptorBytes  = 64 << 10
	maxAncestors        = 128
	maxRoots            = 256
	maxDirectoryEntries = 65_536
	maxSources          = 2_048
	maxSourceIDBytes    = 1_024
	maxDocumentBytes    = 1 << 20
	maxBatchBytes       = 16 << 20
	maxRequestBytes     = 64 << 10
	maxResponseBytes    = 64 << 10
	maxDiagnosticBytes  = 1 << 20

	supportedDescriptorVersion = uint64(1)
	supportedProtocolVersion   = uint64(1)
	emptySetDigest             = "sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494"
)

type limits struct {
	descriptorBytes int
	ancestors       int
	roots           int
	entries         uint64
	sources         int
	sourceIDBytes   int
	documentBytes   uint64
	batchBytes      uint64
	requestBytes    int
	responseBytes   int
	diagnosticBytes int
}

func contractLimits() limits {
	return limits{
		descriptorBytes: maxDescriptorBytes,
		ancestors:       maxAncestors,
		roots:           maxRoots,
		entries:         maxDirectoryEntries,
		sources:         maxSources,
		sourceIDBytes:   maxSourceIDBytes,
		documentBytes:   maxDocumentBytes,
		batchBytes:      maxBatchBytes,
		requestBytes:    maxRequestBytes,
		responseBytes:   maxResponseBytes,
		diagnosticBytes: maxDiagnosticBytes,
	}
}

type failure struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	ExitCode int    `json:"-"`
}

func (f *failure) Error() string {
	if f == nil {
		return "project check failure"
	}
	return f.Category + "/" + f.Code
}

func fail(category, code string) *failure {
	exit := exitFor(category, code)
	if exit < 0 {
		return &failure{Category: "migration_project_internal_error", Code: "project_internal_error", ExitCode: 3}
	}
	return &failure{Category: category, Code: code, ExitCode: exit}
}

func exitFor(category, code string) int {
	switch category {
	case "migration_project_command_error":
		if code == "invalid_arguments" {
			return 2
		}
	case "migration_project_selection_error":
		switch code {
		case "project_not_found", "project_search_limit_exceeded", "invalid_project_descriptor", "project_descriptor_incompatible":
			return 2
		case "project_selection_failed":
			return 3
		}
	case "migration_project_build_error":
		if code == "project_temporary_storage_failed" || code == "project_build_failed" {
			return 3
		}
	case "migration_project_protocol_error":
		switch code {
		case "invalid_project_runner_request", "project_runner_failed", "project_protocol_incompatible", "invalid_project_runner_response":
			return 3
		}
	case "migration_project_process_error":
		switch code {
		case "project_canceled", "project_cleanup_failed":
			return 3
		case "project_interrupted":
			return 130
		}
	case "migration_definition_discovery_error":
		switch code {
		case "invalid_project_source_config", "invalid_source_root":
			return 2
		case "invalid_source_entry", "unsafe_source_entry", "source_catalog_limit_exceeded":
			return 1
		case "source_discovery_failed", "source_read_failed":
			return 3
		}
	case "migration_definition_source_error":
		switch code {
		case "invalid_definition_source", "invalid_definition_document", "definition_format_incompatible", "unsupported_definition_operation", "invalid_definition_operation", "invalid_definition_ir":
			return 1
		}
	case "migration_graph_error":
		switch code {
		case "invalid_node", "duplicate_node", "invalid_dependency", "duplicate_dependency", "dependency_not_found", "dependency_cycle":
			return 1
		}
	case "migration_project_internal_error":
		if code == "project_internal_error" {
			return 3
		}
	}
	return -1
}

type failureContext struct {
	Stage          string   `json:"stage"`
	SourceID       string   `json:"source_id"`
	JSONPointer    string   `json:"json_pointer"`
	App            string   `json:"app"`
	Name           string   `json:"name"`
	OperationIndex int      `json:"operation_index"`
	Reason         string   `json:"reason"`
	Limit          string   `json:"limit"`
	Maximum        uint64   `json:"maximum"`
	Actual         uint64   `json:"actual"`
	GraphSources   []string `json:"graph_sources"`
}

// oracleMetrics deliberately has the exact 24 fields frozen by ADR-0021.
// Feasibility-only process and resource observations live in a separate type.
type oracleMetrics struct {
	BuildCalls                   int             `json:"build_calls"`
	RunnerCalls                  int             `json:"runner_calls"`
	RunnerResponseWrites         int             `json:"runner_response_writes"`
	SourceReads                  int             `json:"source_reads"`
	LoadCalls                    int             `json:"load_calls"`
	DocumentsReceived            int             `json:"documents_received"`
	HeadersValidated             int             `json:"headers_validated"`
	OperationsDecoded            int             `json:"operations_decoded"`
	PlannerConstruction          int             `json:"planner_construction"`
	DefinitionsPublished         int             `json:"definitions_published"`
	DefinitionSetsPublished      int             `json:"definition_sets_published"`
	DirectPlannerCalls           int             `json:"direct_planner_calls"`
	GoDjDBCalls                  int             `json:"godj_db_calls"`
	RevisionLifecycleCalls       int             `json:"revision_lifecycle_calls"`
	UserStdoutWrites             int             `json:"user_stdout_writes"`
	UserStderrWrites             int             `json:"user_stderr_writes"`
	PartialStdoutWrites          int             `json:"partial_stdout_writes"`
	ExitCode                     int             `json:"exit_code"`
	CommandDispatches            int             `json:"command_dispatches"`
	AncestorDirectoriesInspected int             `json:"ancestor_directories_inspected"`
	DescriptorReads              int             `json:"descriptor_reads"`
	RootsOpened                  int             `json:"roots_opened"`
	DirectoryEntriesSeen         int             `json:"directory_entries_seen"`
	Failure                      *failureContext `json:"failure"`
}

type feasibilityMetrics struct {
	TempCreated          int
	TempCleanupAttempts  int
	CleanupFailed        int
	ResidualTemp         int
	GroupSIGINTAttempts  int
	GroupSIGKILLAttempts int
	DirectChildReaps     int
	Diagnostics          map[string]diagnosticScalar
	RawDiagnostics       []byte
}

type checkResult struct {
	SourceCount         int    `json:"source_count"`
	DefinitionCount     int    `json:"definition_count"`
	DefinitionSetDigest string `json:"definition_set_digest"`
}

type observation struct {
	Result      *checkResult
	Failure     *failure
	Metrics     oracleMetrics
	Feasibility feasibilityMetrics
	Stdout      []byte
	Stderr      []byte
	publication publicationHarness
}

type publicationHarness struct {
	stdout io.Writer
	stderr io.Writer
	event  func(string)
}

type captureForwardWriter struct {
	target   io.Writer
	captured *[]byte
}

func (w captureForwardWriter) Write(payload []byte) (int, error) {
	if w.target == nil {
		*w.captured = append(*w.captured, payload...)
		return len(payload), nil
	}
	written, err := w.target.Write(payload)
	if written < 0 || written > len(payload) {
		return 0, io.ErrShortWrite
	}
	*w.captured = append(*w.captured, payload[:written]...)
	if written != len(payload) && err == nil {
		err = io.ErrShortWrite
	}
	return written, err
}

func (o *observation) choose(primary *failure, result *checkResult) {
	if o.Failure != nil || o.Result != nil {
		return
	}
	o.Failure = primary
	o.Result = result
}

func (o *observation) publish() {
	if o.Failure == nil && o.Result == nil {
		o.Failure = fail("migration_project_internal_error", "project_internal_error")
	}
	if o.Failure != nil {
		o.Metrics.ExitCode = o.Failure.ExitCode
		o.Metrics.UserStderrWrites++
		payload := []byte(o.Failure.Category + "/" + o.Failure.Code + "\n")
		if o.publication.event != nil {
			o.publication.event("publish.stderr")
		}
		_, _ = (captureForwardWriter{target: o.publication.stderr, captured: &o.Stderr}).Write(payload)
		return
	}
	o.Metrics.ExitCode = 0
	o.Metrics.UserStdoutWrites++
	payload, err := json.Marshal(o.Result)
	if err != nil {
		panic(err)
	}
	payload = append(payload, '\n')
	if o.publication.event != nil {
		o.publication.event("publish.stdout")
	}
	written, _ := (captureForwardWriter{target: o.publication.stdout, captured: &o.Stdout}).Write(payload)
	if written > 0 && written < len(payload) {
		o.Metrics.PartialStdoutWrites++
	}
}

type diagnosticScalar struct {
	RetainedBytes int
	Truncated     bool
}

type cappedCapture struct {
	mu        sync.Mutex
	maximum   int
	retained  bytes.Buffer
	truncated bool
}

func newCappedCapture(maximum int) *cappedCapture {
	return &cappedCapture{maximum: maximum}
}

func (c *cappedCapture) Write(payload []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := c.maximum - c.retained.Len()
	if want > len(payload) {
		want = len(payload)
	}
	if want > 0 {
		_, _ = c.retained.Write(payload[:want])
	}
	if want < len(payload) {
		c.truncated = true
	}
	return len(payload), nil
}

func (c *cappedCapture) snapshotAndDiscard() diagnosticScalar {
	c.mu.Lock()
	defer c.mu.Unlock()
	scalar := diagnosticScalar{RetainedBytes: c.retained.Len(), Truncated: c.truncated}
	zeroBytes(c.retained.Bytes())
	c.retained.Reset()
	return scalar
}

func (c *cappedCapture) takeAndDiscard() ([]byte, diagnosticScalar) {
	c.mu.Lock()
	defer c.mu.Unlock()
	scalar := diagnosticScalar{RetainedBytes: c.retained.Len(), Truncated: c.truncated}
	buffer := c.retained.Bytes()
	retained := append([]byte(nil), buffer...)
	zeroBytes(buffer)
	c.retained.Reset()
	return retained, scalar
}

func zeroBytes(payload []byte) {
	for index := range payload {
		payload[index] = 0
	}
}

func checkedAdd(current, amount, maximum uint64) (uint64, bool) {
	if amount > math.MaxUint64-current {
		return math.MaxUint64, true
	}
	updated := current + amount
	return updated, updated > maximum
}

func drainInto(ctx context.Context, source io.Reader, destination io.Writer) error {
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := source.Read(buffer)
		if count > 0 {
			if _, writeErr := destination.Write(buffer[:count]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func sortedKeys(value map[string]jsonValue) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func exactKeys(value map[string]jsonValue, expected ...string) bool {
	actual := sortedKeys(value)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	return strings.Join(actual, "\x00") == strings.Join(want, "\x00")
}

func errorPair(err error) (string, string, bool) {
	var candidate *failure
	if !errors.As(err, &candidate) || candidate == nil {
		return "", "", false
	}
	return candidate.Category, candidate.Code, true
}

func requireNoUnexpectedPrimary(primary *failure) *failure {
	if primary != nil && primary.Category == "" {
		return fail("migration_project_internal_error", "project_internal_error")
	}
	return primary
}

func joinedPair(category, code string) string {
	return fmt.Sprintf("%s/%s", category, code)
}
