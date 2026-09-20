package definition

import (
	"fmt"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

const (
	documentStructuralLowerBound   = uint64(len(`{"format_version":1,"producer":{"name":"","version":""},"migration":{"app":"","name":"","dependencies":[],"operations":[]}}`) + 1)
	dependencyStructuralLowerBound = uint64(len(`{"app":"","name":""}`))

	createModelOperationStructuralLowerBound = uint64(len(`{"kind":"create_model","app_label":"","model":}`))
	addFieldOperationStructuralLowerBound    = uint64(len(`{"kind":"add_field","app_label":"","model_name":"","field":}`))
	modelStructuralLowerBound                = uint64(len(`{"name":"","go_name":"","db_table":"","fields":[]}`))
	fieldStructuralLowerBound                = uint64(len(`{"name":"","go_name":"","column":"","kind":"","primary_key":true,"nullable":true,"max_length":0,"default":null}`))

	durationDefaultStructuralLowerBound = uint64(len(`{"kind":"","duration":""}`))
	timeDefaultStructuralLowerBound     = uint64(len(`{"kind":"","time":""}`))
	dateDefaultStructuralLowerBound     = uint64(len(`{"kind":"","date":""}`))
	datetimeDefaultStructuralLowerBound = uint64(len(`{"kind":"","datetime":""}`))
	stringDefaultStructuralLowerBound   = uint64(len(`{"kind":"","string":""}`))
	booleanDefaultStructuralLowerBound  = uint64(len(`{"kind":"","boolean":true}`))
	integerDefaultStructuralLowerBound  = uint64(len(`{"kind":"","integer":0}`))
	nullStructuralLowerBound            = uint64(len(`null`))

	relationMemberStructuralLowerBound = uint64(len(`,"relation":`))
	relationStructuralLowerBound       = uint64(len(`{"target":{"app_label":"","model_name":""},"cardinality":"","reverse":{"name":"","disabled":true},"on_delete":""}`))
)

// preflightEncodingResources computes an unescaped current-wire byte lower
// bound before Encode performs any proportional copy or JSON allocation.
// Escaping can only increase the final byte count, so the post-marshal exact
// cap remains authoritative for inputs whose lower bound still fits.
func preflightEncodingResources(producer Producer, migration migrations.Migration) error {
	if len(migration.Dependencies) > MaxDependenciesPerMigration {
		return encodeFailure(
			"migration.dependencies",
			"dependencies_per_migration resource limit exceeded: got %d, maximum %d",
			len(migration.Dependencies),
			MaxDependenciesPerMigration,
		)
	}
	if len(migration.Operations) > MaxOperationsPerMigration {
		return encodeFailure(
			"migration.operations",
			"operations_per_migration resource limit exceeded: got %d, maximum %d",
			len(migration.Operations),
			MaxOperationsPerMigration,
		)
	}

	scanner := encodingSizeScanner{}
	if err := scanner.addStructural("document", documentStructuralLowerBound); err != nil {
		return err
	}
	for _, value := range []struct {
		path  string
		value string
	}{
		{path: "producer.name", value: producer.Name},
		{path: "producer.version", value: producer.Version},
		{path: "migration.app", value: migration.App},
		{path: "migration.name", value: migration.Name},
	} {
		if err := scanner.addString(value.path, value.value); err != nil {
			return err
		}
	}

	for index, dependency := range migration.Dependencies {
		path := fmt.Sprintf("migration.dependencies[%d]", index)
		if index != 0 {
			if err := scanner.addStructural(path, 1); err != nil {
				return err
			}
		}
		if err := scanner.addStructural(path, dependencyStructuralLowerBound); err != nil {
			return err
		}
		if err := scanner.addString(path+".app", dependency.App); err != nil {
			return err
		}
		if err := scanner.addString(path+".name", dependency.Name); err != nil {
			return err
		}
	}

	for index, operation := range migration.Operations {
		path := fmt.Sprintf("migration.operations[%d]", index)
		if index != 0 {
			if err := scanner.addStructural(path, 1); err != nil {
				return err
			}
		}
		switch value := operationValue(operation).(type) {
		case migrations.CreateModel:
			if err := scanner.scanCreateModel(path, index, value); err != nil {
				return err
			}
		case *migrations.CreateModel:
			return encodeFailure(path, "nil *migrations.CreateModel")
		case migrations.AddField:
			if err := scanner.scanAddField(path, value); err != nil {
				return err
			}
		case *migrations.AddField:
			return encodeFailure(path, "nil *migrations.AddField")
		case migrations.AlterField:
			if err := scanner.scanAlterField(path, value); err != nil {
				return err
			}
		case *migrations.AlterField:
			return encodeFailure(path, "nil *migrations.AlterField")
		case nil:
			return encodeFailure(path, "nil operation")
		default:
			return encodeFailure(path, "unsupported operation type %T", operation)
		}
	}
	return nil
}

type encodingSizeScanner struct {
	lowerBound uint64
}

func (scanner *encodingSizeScanner) addString(path, value string) error {
	actual := uint64(len(value))
	if actual > MaxDocumentBytes {
		return encodeDocumentSizeFailure(path, actual)
	}
	return scanner.add(path, actual)
}

func (scanner *encodingSizeScanner) addStructural(path string, size uint64) error {
	return scanner.add(path, size)
}

func (scanner *encodingSizeScanner) add(path string, size uint64) error {
	updated, overflow := saturatingAdd(scanner.lowerBound, size)
	if overflow || updated > MaxDocumentBytes {
		return encodeDocumentSizeFailure(path, updated)
	}
	scanner.lowerBound = updated
	return nil
}

func encodeDocumentSizeFailure(path string, actualLowerBound uint64) error {
	return encodeFailure(
		path,
		"document_bytes resource limit exceeded: lower bound %d, maximum %d",
		actualLowerBound,
		MaxDocumentBytes,
	)
}

func (scanner *encodingSizeScanner) scanCreateModel(path string, operationIndex int, operation migrations.CreateModel) error {
	if len(operation.Model.Fields) > MaxFieldsPerCreateModel {
		return createModelFieldsLimitFailure(operationIndex, len(operation.Model.Fields))
	}
	if err := scanner.addStructural(path, createModelOperationStructuralLowerBound); err != nil {
		return err
	}
	if err := scanner.addString(path+".app_label", operation.AppLabel); err != nil {
		return err
	}
	return scanner.scanModel(path+".model", operation.Model)
}

func (scanner *encodingSizeScanner) scanAddField(path string, operation migrations.AddField) error {
	if err := scanner.addStructural(path, addFieldOperationStructuralLowerBound); err != nil {
		return err
	}
	if err := scanner.addString(path+".app_label", operation.AppLabel); err != nil {
		return err
	}
	if err := scanner.addString(path+".model_name", operation.ModelName); err != nil {
		return err
	}
	if operation.BeforeField != "" {
		if err := scanner.addStructural(path+".before_field", uint64(len(`,"before_field":""`))); err != nil {
			return err
		}
		if err := scanner.addString(path+".before_field", operation.BeforeField); err != nil {
			return err
		}
	}
	return scanner.scanField(path+".field", operation.Field)
}

func (scanner *encodingSizeScanner) scanModel(path string, model ir.Model) error {
	if err := scanner.addStructural(path, modelStructuralLowerBound); err != nil {
		return err
	}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "name", value: model.Name},
		{name: "go_name", value: model.GoName},
		{name: "db_table", value: model.DBTable},
	} {
		if err := scanner.addString(path+"."+value.name, value.value); err != nil {
			return err
		}
	}
	for index, field := range model.Fields {
		fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
		if index != 0 {
			if err := scanner.addStructural(fieldPath, 1); err != nil {
				return err
			}
		}
		if err := scanner.scanField(fieldPath, field); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *encodingSizeScanner) scanField(path string, field ir.Field) error {
	if err := scanner.addStructural(path, fieldStructuralLowerBound); err != nil {
		return err
	}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "name", value: field.Name},
		{name: "go_name", value: field.GoName},
		{name: "column", value: field.Column},
		{name: "kind", value: string(field.Kind)},
	} {
		if err := scanner.addString(path+"."+value.name, value.value); err != nil {
			return err
		}
	}
	if field.Decimal != nil {
		if err := scanner.addStructural(path+".decimal", uint64(len(`,"decimal":{"max_digits":0,"decimal_places":0}`))); err != nil {
			return err
		}
	}
	if field.Default != nil {
		if err := scanner.scanDefault(path+".default", *field.Default); err != nil {
			return err
		}
	}
	if field.Choices != nil {
		if err := scanner.addStructural(path+".choices", uint64(len(`,"choices":[]`))); err != nil {
			return err
		}
		for index, choice := range field.Choices {
			itemPath := fmt.Sprintf("%s.choices[%d]", path, index)
			structure := uint64(len(`{"value":null,"label":""}`))
			if index > 0 {
				structure++
			}
			if err := scanner.addStructural(itemPath, structure); err != nil {
				return err
			}
			if err := scanner.addString(itemPath+".label", choice.Label); err != nil {
				return err
			}
			if err := scanner.scanDefault(itemPath+".value", choice.Value); err != nil {
				return err
			}
		}
	}
	if field.Relation != nil {
		if err := scanner.scanRelation(path+".relation", *field.Relation); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *encodingSizeScanner) scanDefault(path string, value ir.Scalar) error {
	if err := scanner.addString(path+".json", value.JSON); err != nil {
		return err
	}
	if err := scanner.addString(path+".uuid", value.UUID); err != nil {
		return err
	}
	if err := scanner.addString(path+".decimal", value.Decimal); err != nil {
		return err
	}
	if err := scanner.addString(path+".float_bits", value.FloatBits); err != nil {
		return err
	}
	if err := scanner.addString(path+".duration", value.Duration); err != nil {
		return err
	}
	if err := scanner.addString(path+".time", value.Time); err != nil {
		return err
	}
	if err := scanner.addString(path+".date", value.Date); err != nil {
		return err
	}
	if err := scanner.addString(path+".datetime", value.DateTime); err != nil {
		return err
	}
	structural := uint64(0)
	switch value.Kind {
	case ir.ScalarJSON:
		structural = uint64(len(`{"kind":"","json":""}`))
	case ir.ScalarUUID:
		structural = uint64(len(`{"kind":"","uuid":""}`))
	case ir.ScalarDecimal:
		structural = uint64(len(`{"kind":"","decimal":""}`))
	case ir.ScalarFloat:
		structural = uint64(len(`{"kind":"","float_bits":""}`))
	case ir.ScalarDuration:
		structural = durationDefaultStructuralLowerBound
	case ir.ScalarTime:
		structural = timeDefaultStructuralLowerBound
	case ir.ScalarDate:
		structural = dateDefaultStructuralLowerBound
	case ir.ScalarDateTime:
		structural = datetimeDefaultStructuralLowerBound
	case ir.ScalarString:
		structural = stringDefaultStructuralLowerBound
	case ir.ScalarBoolean:
		structural = booleanDefaultStructuralLowerBound
	case ir.ScalarInteger:
		structural = integerDefaultStructuralLowerBound
	default:
		// Invalid IR is rejected after the resource preflight. Use the smallest
		// existing default arm here so the lower bound never becomes an authority for
		// accepting unsupported scalar kinds.
		structural = nullStructuralLowerBound
	}
	if err := scanner.addStructural(path, structural-nullStructuralLowerBound); err != nil {
		return err
	}
	if err := scanner.addString(path+".kind", string(value.Kind)); err != nil {
		return err
	}
	// Scan all string payloads, even in malformed arms, before a proportional
	// snapshot. The date and datetime payloads were counted at entry.
	return scanner.addString(path+".string", value.String)
}

func (scanner *encodingSizeScanner) scanRelation(path string, relation ir.ForeignKeyRelation) error {
	if err := scanner.addStructural(path, relationMemberStructuralLowerBound+relationStructuralLowerBound); err != nil {
		return err
	}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "target.app_label", value: relation.Target.AppLabel},
		{name: "target.model_name", value: relation.Target.ModelName},
		{name: "cardinality", value: string(relation.Cardinality)},
		{name: "reverse.name", value: relation.Reverse.Name},
		{name: "on_delete", value: string(relation.OnDelete)},
	} {
		if err := scanner.addString(path+"."+value.name, value.value); err != nil {
			return err
		}
	}
	return nil
}
