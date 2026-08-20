package definition

import (
	"fmt"
	"sort"

	"github.com/progresshans/godj/migrations"
)

const CategorySource = "migration_definition_source_error"

type ErrorCode string

const (
	CodeInvalidSource                ErrorCode = "invalid_definition_source"
	CodeInvalidDocument              ErrorCode = "invalid_definition_document"
	CodeDefinitionFormatIncompatible ErrorCode = "definition_format_incompatible"
	CodeUnsupportedOperation         ErrorCode = "unsupported_definition_operation"
	CodeInvalidOperation             ErrorCode = "invalid_definition_operation"
	CodeInvalidIR                    ErrorCode = "invalid_definition_ir"
)

type GraphSource struct {
	Migration migrations.MigrationKey
	SourceID  string
}

type FailureContext struct {
	Stage          string
	SourceID       string
	JSONPointer    string
	App            string
	Name           string
	OperationIndex int
	Reason         string
	Limit          string
	Maximum        uint64
	Actual         uint64

	graphSources []GraphSource
}

// GraphSources returns a fresh copy of the graph-to-source diagnostic mapping.
func (context FailureContext) GraphSources() []GraphSource {
	return cloneGraphSources(context.graphSources)
}

type LoadReport struct {
	DocumentsReceived       int
	HeadersValidated        int
	OperationsDecoded       int
	PlannerConstruction     int
	DefinitionsPublished    int
	DefinitionSetsPublished int

	failure    FailureContext
	hasFailure bool
}

// Failure returns a fresh value snapshot of the failure recorded for this
// load. Successful reports return false.
func (report LoadReport) Failure() (FailureContext, bool) {
	if !report.hasFailure {
		return FailureContext{}, false
	}
	return cloneFailureContext(report.failure), true
}

type Error struct {
	Category string
	Code     ErrorCode

	context FailureContext
}

func (e *Error) Error() string {
	if e == nil {
		return "migration definition source error"
	}
	return fmt.Sprintf(
		"%s/%s stage=%s source=%q pointer=%q reason=%s",
		e.Category,
		e.Code,
		e.context.Stage,
		e.context.SourceID,
		e.context.JSONPointer,
		e.context.Reason,
	)
}

// Context returns a fresh value snapshot of this error's diagnostics.
func (e *Error) Context() FailureContext {
	if e == nil {
		return FailureContext{}
	}
	return cloneFailureContext(e.context)
}

type failureCandidate struct {
	code    ErrorCode
	context FailureContext
}

func sourceFailure(sourceID, reason string) failureCandidate {
	return failureCandidate{
		code: CodeInvalidSource,
		context: FailureContext{
			Stage:          "source",
			SourceID:       sourceID,
			OperationIndex: -1,
			Reason:         reason,
		},
	}
}

func documentFailure(sourceID, pointer, reason string) failureCandidate {
	return failureCandidate{
		code: CodeInvalidDocument,
		context: FailureContext{
			Stage:          "document",
			SourceID:       sourceID,
			JSONPointer:    pointer,
			OperationIndex: -1,
			Reason:         reason,
		},
	}
}

func formatFailure(sourceID string) failureCandidate {
	return failureCandidate{
		code: CodeDefinitionFormatIncompatible,
		context: FailureContext{
			Stage:          "format",
			SourceID:       sourceID,
			JSONPointer:    "/format_version",
			OperationIndex: -1,
			Reason:         "format_version",
		},
	}
}

func semanticFailure(
	code ErrorCode,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	reason string,
) failureCandidate {
	return failureCandidate{
		code: code,
		context: FailureContext{
			Stage:          "semantic",
			SourceID:       sourceID,
			JSONPointer:    pointer,
			App:            app,
			Name:           name,
			OperationIndex: operationIndex,
			Reason:         reason,
		},
	}
}

func resourceFailure(
	code ErrorCode,
	stage string,
	sourceID string,
	pointer string,
	limit string,
	maximum uint64,
	actual uint64,
	operationIndex int,
) failureCandidate {
	return failureCandidate{
		code: code,
		context: FailureContext{
			Stage:          stage,
			SourceID:       sourceID,
			JSONPointer:    pointer,
			OperationIndex: operationIndex,
			Reason:         resourceLimitReason,
			Limit:          limit,
			Maximum:        maximum,
			Actual:         actual,
		},
	}
}

func newSourceError(candidate failureCandidate) *Error {
	return &Error{
		Category: CategorySource,
		Code:     candidate.code,
		context:  cloneFailureContext(candidate.context),
	}
}

func withFailure(report LoadReport, context FailureContext) LoadReport {
	report.failure = cloneFailureContext(context)
	report.hasFailure = true
	return report
}

func cloneFailureContext(context FailureContext) FailureContext {
	cloned := context
	cloned.graphSources = canonicalGraphSources(context.graphSources)
	return cloned
}

func cloneGraphSources(sources []GraphSource) []GraphSource {
	cloned := make([]GraphSource, len(sources))
	copy(cloned, sources)
	return cloned
}

func canonicalGraphSources(sources []GraphSource) []GraphSource {
	canonical := cloneGraphSources(sources)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].Migration.App != canonical[right].Migration.App {
			return canonical[left].Migration.App < canonical[right].Migration.App
		}
		if canonical[left].Migration.Name != canonical[right].Migration.Name {
			return canonical[left].Migration.Name < canonical[right].Migration.Name
		}
		return canonical[left].SourceID < canonical[right].SourceID
	})

	write := 0
	for _, source := range canonical {
		if write != 0 && canonical[write-1] == source {
			continue
		}
		canonical[write] = source
		write++
	}
	return canonical[:write]
}

func sortFailureCandidates(candidates []failureCandidate) {
	sort.SliceStable(candidates, func(left, right int) bool {
		return lessFailureCandidate(candidates[left], candidates[right])
	})
}

func lessFailureCandidate(left, right failureCandidate) bool {
	leftStage := failureStageRank(left.context.Stage)
	rightStage := failureStageRank(right.context.Stage)
	if leftStage != rightStage {
		return leftStage < rightStage
	}

	leftResource := left.context.Reason == resourceLimitReason
	rightResource := right.context.Reason == resourceLimitReason
	if leftResource != rightResource {
		return leftResource
	}
	if leftResource {
		leftLimit := failureLimitRank(left.context.Limit)
		rightLimit := failureLimitRank(right.context.Limit)
		if leftLimit != rightLimit {
			return leftLimit < rightLimit
		}
	} else if left.context.Stage == "source" || left.context.Stage == "format" {
		leftReason := failureReasonRank(left.context.Stage, left.context.Reason)
		rightReason := failureReasonRank(right.context.Stage, right.context.Reason)
		if leftReason != rightReason {
			return leftReason < rightReason
		}
	}

	if left.context.SourceID != right.context.SourceID {
		return left.context.SourceID < right.context.SourceID
	}
	if left.context.JSONPointer != right.context.JSONPointer {
		return left.context.JSONPointer < right.context.JSONPointer
	}
	if !leftResource {
		leftReason := failureReasonRank(left.context.Stage, left.context.Reason)
		rightReason := failureReasonRank(right.context.Stage, right.context.Reason)
		if leftReason != rightReason {
			return leftReason < rightReason
		}
	}
	if left.context.App != right.context.App {
		return left.context.App < right.context.App
	}
	if left.context.Name != right.context.Name {
		return left.context.Name < right.context.Name
	}
	if left.context.OperationIndex != right.context.OperationIndex {
		return left.context.OperationIndex < right.context.OperationIndex
	}
	if left.code != right.code {
		return left.code < right.code
	}
	if left.context.Limit != right.context.Limit {
		return left.context.Limit < right.context.Limit
	}
	if left.context.Maximum != right.context.Maximum {
		return left.context.Maximum < right.context.Maximum
	}
	return left.context.Actual < right.context.Actual
}

func failureStageRank(stage string) int {
	switch stage {
	case "source":
		return 0
	case "document":
		return 1
	case "format":
		return 2
	case "semantic":
		return 3
	case "graph":
		return 4
	default:
		return 5
	}
}

func failureLimitRank(limit string) int {
	switch limit {
	case limitSourceCount:
		return 0
	case limitSourceIDBytes:
		return 1
	case limitDocumentBytes:
		return 2
	case limitBatchBytes:
		return 3
	case limitJSONDepth:
		return 4
	case limitDocumentJSONValues:
		return 5
	case limitJSONValues:
		return 6
	case limitDependenciesPerMigration:
		return 7
	case limitOperationsPerMigration:
		return 8
	case limitFieldsPerCreateModel:
		return 9
	default:
		return 10
	}
}

func failureReasonRank(stage, reason string) int {
	switch stage {
	case "source":
		switch reason {
		case "empty_source_id":
			return 0
		case "invalid_source_id_utf8":
			return 1
		case "duplicate_source_id":
			return 2
		}
	case "document":
		switch reason {
		case "invalid_utf8":
			return 0
		case "syntax":
			return 1
		case "duplicate_key":
			return 2
		case "lone_surrogate":
			return 3
		case "unknown_field":
			return 4
		case "missing_field":
			return 5
		case "wrong_type":
			return 6
		case "out_of_range":
			return 7
		case "trailing_value":
			return 8
		}
	case "format":
		switch reason {
		case "format_version":
			return 0
		}
	case "semantic":
		switch reason {
		case "unsupported_operation":
			return 0
		case "invalid_operation":
			return 1
		case "invalid_ir":
			return 2
		case "wrong_type":
			return 3
		case "out_of_range":
			return 4
		}
	}
	return 100
}
