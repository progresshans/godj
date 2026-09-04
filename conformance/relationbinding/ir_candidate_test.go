package relationbinding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const candidateRelationIRVersion = 3

type scalarArm struct {
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type relationArm struct {
	StorageType string       `json:"storage_type"`
	Target      modelKey     `json:"target"`
	Nullable    bool         `json:"nullable"`
	Reverse     string       `json:"reverse"`
	Delete      deletePolicy `json:"delete"`
}

type unionField struct {
	Name     string       `json:"name"`
	Column   string       `json:"column"`
	Scalar   *scalarArm   `json:"scalar,omitempty"`
	Relation *relationArm `json:"relation,omitempty"`
}

type unionModel struct {
	Key    modelKey     `json:"key"`
	Fields []unionField `json:"fields"`
}

type unionSchemaVNext struct {
	FormatVersion int          `json:"format_version"`
	Models        []unionModel `json:"models"`
}

type storageField struct {
	Name     string `json:"name"`
	Column   string `json:"column"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type splitRelation struct {
	Name         string       `json:"name"`
	StorageField string       `json:"storage_field"`
	Target       modelKey     `json:"target"`
	Nullable     bool         `json:"nullable"`
	Reverse      string       `json:"reverse"`
	Delete       deletePolicy `json:"delete"`
}

type splitModel struct {
	Key       modelKey        `json:"key"`
	Storage   []storageField  `json:"storage"`
	Relations []splitRelation `json:"relations"`
}

type splitSchemaVNext struct {
	FormatVersion int          `json:"format_version"`
	Models        []splitModel `json:"models"`
}

type normalizedCandidate[T any] struct {
	Value     T
	Canonical []byte
	Digest    string
}

func normalizeUnionSchema(input unionSchemaVNext) (normalizedCandidate[unionSchemaVNext], error) {
	if input.FormatVersion != candidateRelationIRVersion {
		return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("unsupported_schema_ir_version: %d", input.FormatVersion)
	}
	normalized := unionSchemaVNext{FormatVersion: candidateRelationIRVersion, Models: make([]unionModel, len(input.Models))}
	for i, model := range input.Models {
		normalized.Models[i] = unionModel{Key: model.Key, Fields: make([]unionField, len(model.Fields))}
		for j, field := range model.Fields {
			normalized.Models[i].Fields[j] = cloneUnionField(field)
		}
		sort.Slice(normalized.Models[i].Fields, func(left, right int) bool {
			return normalized.Models[i].Fields[left].Name < normalized.Models[i].Fields[right].Name
		})
	}
	sort.Slice(normalized.Models, func(i, j int) bool {
		return normalized.Models[i].Key.String() < normalized.Models[j].Key.String()
	})

	descriptors := make([]modelDescriptor, 0, len(normalized.Models))
	for _, model := range normalized.Models {
		descriptor := modelDescriptor{Key: model.Key}
		fieldNames := make(map[string]struct{}, len(model.Fields))
		columnNames := make(map[string]struct{}, len(model.Fields))
		for _, field := range model.Fields {
			if !symbolicPart.MatchString(field.Name) || !symbolicPart.MatchString(field.Column) {
				return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("invalid_field: %s.%s", model.Key, field.Name)
			}
			if _, duplicate := fieldNames[field.Name]; duplicate {
				return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("duplicate_field: %s.%s", model.Key, field.Name)
			}
			if _, duplicate := columnNames[field.Column]; duplicate {
				return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("duplicate_column: %s.%s", model.Key, field.Column)
			}
			fieldNames[field.Name] = struct{}{}
			columnNames[field.Column] = struct{}{}
			if (field.Scalar == nil) == (field.Relation == nil) {
				return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("field_union_requires_exactly_one_arm: %s.%s", model.Key, field.Name)
			}
			if field.Scalar != nil {
				if !validStorageType(field.Scalar.Type) {
					return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("unsupported_scalar_type: %s.%s", model.Key, field.Name)
				}
				descriptor.Fields = append(descriptor.Fields, field.Name)
				continue
			}
			if field.Relation.StorageType != "int64" {
				return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("unsupported_relation_storage: %s.%s", model.Key, field.Name)
			}
			descriptor.Relations = append(descriptor.Relations, relationDeclaration{
				Field: field.Name, Column: field.Column, Target: field.Relation.Target,
				Nullable: field.Relation.Nullable, Delete: field.Relation.Delete, Reverse: field.Relation.Reverse,
			})
		}
		descriptors = append(descriptors, descriptor)
	}
	if _, err := bindProject(descriptors); err != nil {
		return normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("bind union schema: %w", err)
	}
	return canonicalCandidate(normalized)
}

func normalizeSplitSchema(input splitSchemaVNext) (normalizedCandidate[splitSchemaVNext], normalizedCandidate[unionSchemaVNext], error) {
	if input.FormatVersion != candidateRelationIRVersion {
		return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("unsupported_schema_ir_version: %d", input.FormatVersion)
	}
	normalized := splitSchemaVNext{FormatVersion: candidateRelationIRVersion, Models: make([]splitModel, len(input.Models))}
	projection := unionSchemaVNext{FormatVersion: candidateRelationIRVersion, Models: make([]unionModel, len(input.Models))}
	for i, model := range input.Models {
		normalized.Models[i] = splitModel{
			Key: model.Key, Storage: append([]storageField(nil), model.Storage...), Relations: append([]splitRelation(nil), model.Relations...),
		}
		sort.Slice(normalized.Models[i].Storage, func(left, right int) bool {
			return normalized.Models[i].Storage[left].Name < normalized.Models[i].Storage[right].Name
		})
		sort.Slice(normalized.Models[i].Relations, func(left, right int) bool {
			return normalized.Models[i].Relations[left].Name < normalized.Models[i].Relations[right].Name
		})
	}
	sort.Slice(normalized.Models, func(i, j int) bool {
		return normalized.Models[i].Key.String() < normalized.Models[j].Key.String()
	})

	projection.Models = make([]unionModel, len(normalized.Models))
	for i, model := range normalized.Models {
		storageByName := make(map[string]storageField, len(model.Storage))
		relationStorage := make(map[string]string, len(model.Relations))
		for _, storage := range model.Storage {
			if !symbolicPart.MatchString(storage.Name) || !symbolicPart.MatchString(storage.Column) || !validStorageType(storage.Type) {
				return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("invalid_storage_field: %s.%s", model.Key, storage.Name)
			}
			if _, duplicate := storageByName[storage.Name]; duplicate {
				return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("duplicate_storage_field: %s.%s", model.Key, storage.Name)
			}
			storageByName[storage.Name] = storage
		}
		for _, relation := range model.Relations {
			storage, exists := storageByName[relation.StorageField]
			if !exists {
				return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("missing_relation_storage: %s.%s", model.Key, relation.Name)
			}
			if storage.Type != "int64" || storage.Nullable != relation.Nullable {
				return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("relation_storage_mismatch: %s.%s", model.Key, relation.Name)
			}
			if prior, duplicate := relationStorage[relation.StorageField]; duplicate {
				return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("relation_storage_reused: %s.%s conflicts=%s", model.Key, relation.Name, prior)
			}
			relationStorage[relation.StorageField] = relation.Name
			projection.Models[i].Fields = append(projection.Models[i].Fields, unionField{
				Name: relation.Name, Column: storage.Column,
				Relation: &relationArm{StorageType: storage.Type, Target: relation.Target, Nullable: relation.Nullable, Reverse: relation.Reverse, Delete: relation.Delete},
			})
		}
		projection.Models[i].Key = model.Key
		for _, storage := range model.Storage {
			if _, consumed := relationStorage[storage.Name]; consumed {
				continue
			}
			storageCopy := storage
			projection.Models[i].Fields = append(projection.Models[i].Fields, unionField{
				Name: storageCopy.Name, Column: storageCopy.Column,
				Scalar: &scalarArm{Type: storageCopy.Type, Nullable: storageCopy.Nullable},
			})
		}
	}
	unionNormalized, err := normalizeUnionSchema(projection)
	if err != nil {
		return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, fmt.Errorf("project split schema: %w", err)
	}
	splitNormalized, err := canonicalCandidate(normalized)
	if err != nil {
		return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, err
	}
	return splitNormalized, unionNormalized, nil
}

func validStorageType(kind string) bool {
	return kind == "int64" || kind == "string" || kind == "bool"
}

func cloneUnionField(field unionField) unionField {
	cloned := unionField{Name: field.Name, Column: field.Column}
	if field.Scalar != nil {
		value := *field.Scalar
		cloned.Scalar = &value
	}
	if field.Relation != nil {
		value := *field.Relation
		cloned.Relation = &value
	}
	return cloned
}

func canonicalCandidate[T any](value T) (normalizedCandidate[T], error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return normalizedCandidate[T]{}, fmt.Errorf("serialize candidate: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return normalizedCandidate[T]{Value: value, Canonical: canonical, Digest: hex.EncodeToString(digest[:])}, nil
}

func decodeUnionVNext(document []byte) (normalizedCandidate[unionSchemaVNext], error) {
	var schema unionSchemaVNext
	if err := decodeClosedJSON(document, &schema); err != nil {
		return normalizedCandidate[unionSchemaVNext]{}, err
	}
	return normalizeUnionSchema(schema)
}

func decodeSplitVNext(document []byte) (normalizedCandidate[splitSchemaVNext], normalizedCandidate[unionSchemaVNext], error) {
	var schema splitSchemaVNext
	if err := decodeClosedJSON(document, &schema); err != nil {
		return normalizedCandidate[splitSchemaVNext]{}, normalizedCandidate[unionSchemaVNext]{}, err
	}
	return normalizeSplitSchema(schema)
}

func decodeClosedJSON(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode closed candidate: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode closed candidate: trailing value")
		}
		return fmt.Errorf("decode closed candidate trailing value: %w", err)
	}
	return nil
}

type scalarFieldV2 struct {
	Name     string `json:"name"`
	Column   string `json:"column"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type scalarModelV2 struct {
	Key    modelKey        `json:"key"`
	Fields []scalarFieldV2 `json:"fields"`
}

type scalarSchemaV2 struct {
	FormatVersion int             `json:"format_version"`
	Models        []scalarModelV2 `json:"models"`
}

func decodeScalarV2(document []byte) (scalarSchemaV2, error) {
	var schema scalarSchemaV2
	if err := decodeClosedJSON(document, &schema); err != nil {
		return scalarSchemaV2{}, err
	}
	if schema.FormatVersion != 2 {
		return scalarSchemaV2{}, fmt.Errorf("unsupported_schema_ir_version: %d", schema.FormatVersion)
	}
	for _, model := range schema.Models {
		for _, field := range model.Fields {
			if field.Type != "auto" && field.Type != "char" && field.Type != "boolean" {
				return scalarSchemaV2{}, fmt.Errorf("unsupported_v2_scalar_type: %s", field.Type)
			}
		}
	}
	return schema, nil
}

type compatibilityTupleV1 struct {
	DefinitionFormat int `json:"definition_format"`
	LoaderABI        int `json:"loader_abi"`
	OperationCodec   int `json:"operation_codec"`
	SchemaIR         int `json:"schema_ir"`
}

type migrationOperationV1 struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type migrationDocumentV1 struct {
	Compatibility compatibilityTupleV1   `json:"compatibility"`
	Operations    []migrationOperationV1 `json:"operations"`
}

func decodeExistingMigrationV1(document []byte) error {
	var migration migrationDocumentV1
	if err := decodeClosedJSON(document, &migration); err != nil {
		return err
	}
	want := compatibilityTupleV1{DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 1, SchemaIR: 2}
	if migration.Compatibility != want {
		return fmt.Errorf("unsupported_compatibility_tuple: %#v", migration.Compatibility)
	}
	for index, operation := range migration.Operations {
		if operation.Type != "create_model" {
			return fmt.Errorf("unsupported_operation_codec_v1: %s", operation.Type)
		}
		if _, err := decodeScalarV2(operation.Schema); err != nil {
			return fmt.Errorf("operation %d rejects relation payload: %w", index, err)
		}
	}
	return nil
}

type layoutEvidence struct {
	Name                       string
	PhysicalColumnOwners       int
	CrossRecordInvariantCount  int
	CanonicalSemanticRoundTrip bool
}

func recommendIRLayout(union, split layoutEvidence) string {
	if union.CanonicalSemanticRoundTrip && split.CanonicalSemanticRoundTrip &&
		union.PhysicalColumnOwners < split.PhysicalColumnOwners &&
		union.CrossRecordInvariantCount < split.CrossRecordInvariantCount {
		return union.Name
	}
	return "no_candidate"
}
