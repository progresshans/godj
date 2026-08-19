package definitionhandoff

import "fmt"

// These internal limits mirror the loader's public resource envelope. Keeping
// them here lets both sides of the private handoff reject caller-amplified
// values before any deep clone or canonicalization allocation.
const (
	MaxDefinitions          = 2_048
	MaxSourceIDBytes        = 1_024
	MaxDefinitionBytes      = 1 << 20
	MaxDefinitionSetBytes   = 16 << 20
	MaxDefinitionNodes      = 262_144
	MaxDependencies         = 2_047
	MaxOperations           = 2_048
	MaxFieldsPerCreateModel = 2_048
)

type resourceBudget struct {
	nodes        uint64
	bytes        uint64
	nodeOverflow bool
	byteOverflow bool
	best         *resourceViolation
}

type resourceViolation struct {
	app    string
	name   string
	path   string
	reason string
}

func (v *resourceViolation) Error() string {
	if v == nil {
		return "definition handoff resource limit exceeded"
	}
	identity := ""
	if v.app != "" || v.name != "" {
		identity = v.app + "." + v.name
	}
	return fmt.Sprintf("definition handoff resource limit exceeded: %s %s %s", identity, v.path, v.reason)
}

// ValidateDefinitionResources scans caller-visible neutral definitions without
// cloning them. Aggregate exhaustion has deterministic precedence over a
// location-bearing violation.
func ValidateDefinitionResources(definitions []Definition) error {
	if len(definitions) > MaxDefinitions {
		return &resourceViolation{path: "definitions", reason: "definition_count"}
	}
	budget := resourceBudget{}
	consumeNodes(&budget, uint64(len(definitions)))
	for index := range definitions {
		if budget.nodeOverflow {
			break
		}
		scanDefinition(&budget, definitions[index])
	}
	return budget.failure()
}

func validateRecordResources(records []Record) error {
	if len(records) > MaxDefinitions {
		return &resourceViolation{path: "records", reason: "definition_count"}
	}
	budget := resourceBudget{}
	consumeNodes(&budget, uint64(len(records)))
	for index := range records {
		if budget.nodeOverflow {
			break
		}
		record := records[index]
		app, name := record.Definition.App, record.Definition.Name
		definitionStart := budget.bytes
		if len(record.SourceID) > MaxSourceIDBytes {
			considerViolation(&budget, &resourceViolation{app: app, name: name, path: "source_id", reason: "source_id_bytes"})
		}
		consumeString(&budget, app, name, "producer.name", record.Producer.Name)
		consumeString(&budget, app, name, "producer.version", record.Producer.Version)
		scanDefinition(&budget, record.Definition)
		if !budget.byteOverflow && budget.bytes >= definitionStart && budget.bytes-definitionStart > MaxDefinitionBytes {
			considerViolation(&budget, &resourceViolation{app: app, name: name, path: "definition", reason: "definition_bytes"})
		}
	}
	return budget.failure()
}

func scanDefinition(budget *resourceBudget, definition Definition) {
	if budget.nodeOverflow {
		return
	}
	app, name := definition.App, definition.Name
	definitionStart := budget.bytes
	consumeString(budget, app, name, "app", definition.App)
	consumeString(budget, app, name, "name", definition.Name)
	if len(definition.Dependencies) > MaxDependencies {
		considerViolation(budget, &resourceViolation{app: app, name: name, path: "dependencies", reason: "dependency_count"})
	} else {
		consumeNodes(budget, uint64(len(definition.Dependencies)))
		if budget.nodeOverflow {
			return
		}
		for index := range definition.Dependencies {
			dependency := definition.Dependencies[index]
			consumeString(budget, app, name, fmt.Sprintf("dependencies[%d].app", index), dependency.App)
			consumeString(budget, app, name, fmt.Sprintf("dependencies[%d].name", index), dependency.Name)
		}
	}
	if len(definition.Operations) > MaxOperations {
		considerViolation(budget, &resourceViolation{app: app, name: name, path: "operations", reason: "operation_count"})
	} else {
		consumeNodes(budget, uint64(len(definition.Operations)))
		if budget.nodeOverflow {
			return
		}
		for index := range definition.Operations {
			if budget.nodeOverflow {
				return
			}
			scanOperation(budget, app, name, index, definition.Operations[index])
		}
	}
	if !budget.byteOverflow && budget.bytes >= definitionStart && budget.bytes-definitionStart > MaxDefinitionBytes {
		considerViolation(budget, &resourceViolation{app: app, name: name, path: "definition", reason: "definition_bytes"})
	}
}

func scanOperation(budget *resourceBudget, app, name string, index int, operation Operation) {
	if budget.nodeOverflow {
		return
	}
	prefix := fmt.Sprintf("operations[%d]", index)
	consumeString(budget, app, name, prefix+".kind", operation.Kind)
	consumeString(budget, app, name, prefix+".app_label", operation.AppLabel)
	consumeString(budget, app, name, prefix+".model_name", operation.ModelName)
	if operation.HasModel {
		consumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		consumeString(budget, app, name, prefix+".model.name", operation.Model.Name)
		consumeString(budget, app, name, prefix+".model.go_name", operation.Model.GoName)
		consumeString(budget, app, name, prefix+".model.db_table", operation.Model.DBTable)
		if len(operation.Model.Fields) > MaxFieldsPerCreateModel {
			considerViolation(budget, &resourceViolation{app: app, name: name, path: prefix + ".model.fields", reason: "field_count"})
		} else {
			consumeNodes(budget, uint64(len(operation.Model.Fields)))
			if budget.nodeOverflow {
				return
			}
			for fieldIndex := range operation.Model.Fields {
				if budget.nodeOverflow {
					return
				}
				scanField(budget, app, name, fmt.Sprintf("%s.model.fields[%d]", prefix, fieldIndex), operation.Model.Fields[fieldIndex])
			}
		}
	}
	if operation.HasField {
		consumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		scanField(budget, app, name, prefix+".field", operation.Field)
	}
}

func scanField(budget *resourceBudget, app, name, path string, field Field) {
	if budget.nodeOverflow {
		return
	}
	consumeString(budget, app, name, path+".name", field.Name)
	consumeString(budget, app, name, path+".go_name", field.GoName)
	consumeString(budget, app, name, path+".column", field.Column)
	consumeString(budget, app, name, path+".kind", field.Kind)
	if field.Default.Present {
		consumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		consumeString(budget, app, name, path+".default.kind", field.Default.Kind)
		consumePayload(budget, app, name, path+".default.string", field.Default.String)
	}
	if field.Relation.Present {
		consumeNodes(budget, 3)
		if budget.nodeOverflow {
			return
		}
		consumeString(budget, app, name, path+".relation.target.app", field.Relation.TargetApp)
		consumeString(budget, app, name, path+".relation.target.model", field.Relation.TargetModel)
		consumeString(budget, app, name, path+".relation.cardinality", field.Relation.Cardinality)
		consumeString(budget, app, name, path+".relation.reverse.name", field.Relation.ReverseName)
		consumeString(budget, app, name, path+".relation.on_delete", field.Relation.OnDelete)
	}
}

func consumeString(budget *resourceBudget, _, _, _, value string) {
	consumeBytes(budget, uint64(len(value)))
}

func consumePayload(budget *resourceBudget, app, name, path, value string) {
	if len(value) > MaxDefinitionBytes {
		considerViolation(budget, &resourceViolation{app: app, name: name, path: path, reason: "payload_bytes"})
	}
	consumeBytes(budget, uint64(len(value)))
}

func consumeNodes(budget *resourceBudget, count uint64) {
	if budget.nodeOverflow || count > uint64(MaxDefinitionNodes)-budget.nodes {
		budget.nodeOverflow = true
		return
	}
	budget.nodes += count
}

func consumeBytes(budget *resourceBudget, count uint64) {
	if budget.byteOverflow || count > uint64(MaxDefinitionSetBytes)-budget.bytes {
		budget.byteOverflow = true
		return
	}
	budget.bytes += count
}

func considerViolation(budget *resourceBudget, candidate *resourceViolation) {
	if candidate == nil || (budget.best != nil && !resourceViolationLess(candidate, budget.best)) {
		return
	}
	budget.best = candidate
}

func resourceViolationLess(left, right *resourceViolation) bool {
	if left.app != right.app {
		return left.app < right.app
	}
	if left.name != right.name {
		return left.name < right.name
	}
	if left.path != right.path {
		return left.path < right.path
	}
	return left.reason < right.reason
}

func (budget *resourceBudget) failure() error {
	if budget.nodeOverflow || budget.nodes > MaxDefinitionNodes {
		return &resourceViolation{path: "definitions", reason: "aggregate_nodes"}
	}
	if budget.byteOverflow || budget.bytes > MaxDefinitionSetBytes {
		return &resourceViolation{path: "definitions", reason: "aggregate_bytes"}
	}
	if budget.best != nil {
		return budget.best
	}
	return nil
}
