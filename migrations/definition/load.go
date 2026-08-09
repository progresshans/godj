package definition

import (
	"bytes"
	"encoding/hex"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
)

type plannerValidator func([]migrations.Migration) error

// Load synchronously snapshots and validates all explicitly supplied source
// documents, then atomically publishes one immutable-by-contract Set.
func Load(sources ...Source) (Set, LoadReport, error) {
	return loadWithPlanner(sources, validateDefinitionGraph)
}

func validateDefinitionGraph(definitions []migrations.Migration) error {
	_, err := migrations.NewPlanner(definitions...)
	return err
}

func loadWithPlanner(sources []Source, validate plannerValidator) (Set, LoadReport, error) {
	report := LoadReport{DocumentsReceived: len(sources)}
	snapshots, failure, failed := preflightAndSnapshot(sources)
	if failed {
		return failLoad(report, failure)
	}

	parsed := make([]parsedDocument, 0, len(snapshots))
	documentFailures := make([]failureCandidate, 0)
	var aggregateValues uint64
	for _, source := range snapshots {
		root, stats, framing := scanJSONDocument(source)
		if aggregateValues <= MaxJSONValues {
			updated, overflow := saturatingAdd(aggregateValues, stats.Values)
			aggregateValues = updated
			if overflow || aggregateValues > MaxJSONValues {
				documentFailures = append(documentFailures, resourceFailure(
					CodeInvalidDocument,
					"document",
					source.sourceID,
					"",
					"json_values",
					MaxJSONValues,
					MaxJSONValues+1,
					-1,
				))
			}
		}
		if !stats.Complete {
			documentFailures = append(documentFailures, framing...)
			continue
		}
		document, candidates := parseEnvelope(source, root, framing)
		if len(candidates) != 0 {
			documentFailures = append(documentFailures, candidates...)
			continue
		}
		parsed = append(parsed, document)
		report.HeadersValidated++
	}
	if len(documentFailures) != 0 {
		sortFailureCandidates(documentFailures)
		return failLoad(report, documentFailures[0])
	}

	if candidates := compatibilityCandidates(parsed); len(candidates) != 0 {
		sortFailureCandidates(candidates)
		return failLoad(report, candidates[0])
	}
	if candidates := semanticLimitCandidates(parsed); len(candidates) != 0 {
		sortFailureCandidates(candidates)
		return failLoad(report, candidates[0])
	}

	semanticFailures := make([]failureCandidate, 0)
	for _, document := range parsed {
		semanticFailures = append(semanticFailures, semanticCandidates(document)...)
	}
	if len(semanticFailures) != 0 {
		sortFailureCandidates(semanticFailures)
		return failLoad(report, semanticFailures[0])
	}

	decoded := make([]decodedDocument, 0, len(parsed))
	operationsDecoded := 0
	decodeFailures := make([]failureCandidate, 0)
	for _, document := range parsed {
		definition, operations, candidates := decodeDocument(document)
		if len(candidates) != 0 {
			decodeFailures = append(decodeFailures, candidates...)
			continue
		}
		decoded = append(decoded, definition)
		operationsDecoded += operations
	}
	if len(decodeFailures) != 0 {
		sortFailureCandidates(decodeFailures)
		return failLoad(report, decodeFailures[0])
	}
	report.OperationsDecoded = operationsDecoded

	sort.Slice(decoded, func(left, right int) bool {
		leftKey := decoded[left].migration.Key()
		rightKey := decoded[right].migration.Key()
		if leftKey.App != rightKey.App {
			return leftKey.App < rightKey.App
		}
		if leftKey.Name != rightKey.Name {
			return leftKey.Name < rightKey.Name
		}
		return bytes.Compare(decoded[left].source.rawID, decoded[right].source.rawID) < 0
	})
	definitions := make([]migrations.Migration, len(decoded))
	for index := range decoded {
		definitions[index] = cloneMigration(decoded[index].migration)
	}

	report.PlannerConstruction++
	if err := validate(definitions); err != nil {
		failure := graphFailureCandidate(err, decoded)
		return Set{}, withFailure(report, failure.context), err
	}

	digest, err := definitionSetDigest(definitions)
	if err != nil {
		return Set{}, report, err
	}
	inventory := make([]SourceInfo, len(decoded))
	for index := range decoded {
		inventory[index] = SourceInfo{
			SourceID:  decoded[index].source.sourceID,
			Producer:  decoded[index].producer,
			Migration: decoded[index].migration.Key(),
		}
	}
	sort.Slice(inventory, func(left, right int) bool {
		return bytes.Compare([]byte(inventory[left].SourceID), []byte(inventory[right].SourceID)) < 0
	})
	report.DefinitionsPublished = len(definitions)
	report.DefinitionSetsPublished = 1
	return newSet(definitions, digest, inventory), report, nil
}

func preflightAndSnapshot(sources []Source) ([]sourceSnapshot, failureCandidate, bool) {
	if len(sources) > MaxSources {
		return nil, resourceFailure(
			CodeInvalidSource,
			"source",
			"",
			"",
			"source_count",
			MaxSources,
			uint64(len(sources)),
			-1,
		), true
	}

	var oversizedID uint64
	for index := range sources {
		actual := uint64(len(sources[index].SourceID))
		if actual > MaxSourceIDBytes && (oversizedID == 0 || actual < oversizedID) {
			oversizedID = actual
		}
	}
	if oversizedID != 0 {
		return nil, resourceFailure(
			CodeInvalidSource,
			"source",
			"",
			"",
			"source_id_bytes",
			MaxSourceIDBytes,
			oversizedID,
			-1,
		), true
	}

	sourceFailures := make([]failureCandidate, 0)
	order := make([]int, len(sources))
	for index := range sources {
		order[index] = index
		rawID := []byte(sources[index].SourceID)
		switch {
		case len(rawID) == 0:
			sourceFailures = append(sourceFailures, sourceFailure("", "empty_source_id"))
		case !utf8.Valid(rawID):
			sourceFailures = append(sourceFailures, sourceFailure("hex:"+hex.EncodeToString(rawID), "invalid_source_id_utf8"))
		}
	}
	sort.Slice(order, func(left, right int) bool {
		return bytes.Compare([]byte(sources[order[left]].SourceID), []byte(sources[order[right]].SourceID)) < 0
	})
	for index := 1; index < len(order); index++ {
		left := sources[order[index-1]].SourceID
		right := sources[order[index]].SourceID
		if left == right {
			sourceFailures = append(sourceFailures, sourceFailure(right, "duplicate_source_id"))
		}
	}
	if len(sourceFailures) != 0 {
		sortFailureCandidates(sourceFailures)
		return nil, sourceFailures[0], true
	}

	documentFailures := make([]failureCandidate, 0)
	var batchBytes uint64
	for index := range sources {
		actual := uint64(len(sources[index].Document))
		if actual > MaxDocumentBytes {
			documentFailures = append(documentFailures, resourceFailure(
				CodeInvalidDocument,
				"document",
				sources[index].SourceID,
				"",
				"document_bytes",
				MaxDocumentBytes,
				actual,
				-1,
			))
		}
		updated, _ := saturatingAdd(batchBytes, actual)
		batchBytes = updated
	}
	if len(documentFailures) != 0 {
		sortFailureCandidates(documentFailures)
		return nil, documentFailures[0], true
	}
	if batchBytes > MaxBatchBytes {
		return nil, resourceFailure(
			CodeInvalidDocument,
			"document",
			"",
			"",
			"batch_bytes",
			MaxBatchBytes,
			batchBytes,
			-1,
		), true
	}

	snapshots := make([]sourceSnapshot, len(sources))
	for index := range sources {
		snapshots[index] = sourceSnapshot{
			sourceID: sources[index].SourceID,
			rawID:    append([]byte(nil), []byte(sources[index].SourceID)...),
			document: append([]byte(nil), sources[index].Document...),
		}
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return bytes.Compare(snapshots[left].rawID, snapshots[right].rawID) < 0
	})
	return snapshots, failureCandidate{}, false
}

func graphFailureCandidate(err error, decoded []decodedDocument) failureCandidate {
	context := FailureContext{Stage: "graph", OperationIndex: -1, Reason: "planner_error"}
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) || planningError == nil {
		return failureCandidate{context: context}
	}

	context.Reason = string(planningError.Code)
	members := planningError.Members()
	selected := planningError.Node
	if selected == (migrations.MigrationKey{}) && len(members) != 0 {
		selected = members[0]
	}
	context.App = selected.App
	context.Name = selected.Name
	if planningError.Code == migrations.CodeInvalidNode || planningError.Code == migrations.CodeDuplicateNode {
		context.JSONPointer = "/migration"
	} else {
		context.JSONPointer = "/migration/dependencies"
	}

	byKey := make(map[migrations.MigrationKey][]string)
	for _, item := range decoded {
		key := item.migration.Key()
		byKey[key] = append(byKey[key], item.source.sourceID)
	}
	for key := range byKey {
		sort.Slice(byKey[key], func(left, right int) bool {
			return bytes.Compare([]byte(byKey[key][left]), []byte(byKey[key][right])) < 0
		})
	}
	if sources := byKey[selected]; len(sources) != 0 {
		if planningError.Node != (migrations.MigrationKey{}) {
			context.SourceID = sources[len(sources)-1]
		} else {
			context.SourceID = sources[0]
		}
	}

	keys := make([]migrations.MigrationKey, 0, len(members)+2)
	if planningError.Node != (migrations.MigrationKey{}) {
		keys = append(keys, planningError.Node)
	}
	if planningError.Related != (migrations.MigrationKey{}) {
		keys = append(keys, planningError.Related)
	}
	keys = append(keys, members...)
	pairs := make([]GraphSource, 0)
	seen := make(map[GraphSource]struct{})
	for _, key := range keys {
		for _, sourceID := range byKey[key] {
			pair := GraphSource{Migration: key, SourceID: sourceID}
			if _, exists := seen[pair]; exists {
				continue
			}
			seen[pair] = struct{}{}
			pairs = append(pairs, pair)
		}
	}
	sort.Slice(pairs, func(left, right int) bool {
		if pairs[left].Migration.App != pairs[right].Migration.App {
			return pairs[left].Migration.App < pairs[right].Migration.App
		}
		if pairs[left].Migration.Name != pairs[right].Migration.Name {
			return pairs[left].Migration.Name < pairs[right].Migration.Name
		}
		return bytes.Compare([]byte(pairs[left].SourceID), []byte(pairs[right].SourceID)) < 0
	})
	context.graphSources = pairs
	return failureCandidate{context: context}
}

func failLoad(report LoadReport, failure failureCandidate) (Set, LoadReport, error) {
	return Set{}, withFailure(report, failure.context), newSourceError(failure)
}
