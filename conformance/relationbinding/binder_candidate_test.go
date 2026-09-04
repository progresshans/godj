package relationbinding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// Everything in this directory is a test-only feasibility candidate. None of
// these names or wire shapes are proposed public API.

type modelKey struct {
	App   string `json:"app"`
	Model string `json:"model"`
}

func (k modelKey) String() string {
	return k.App + "." + k.Model
}

var symbolicPart = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func canonicalModelKey(app, model string) (modelKey, error) {
	key := modelKey{
		App:   strings.ToLower(strings.TrimSpace(app)),
		Model: strings.ToLower(strings.TrimSpace(model)),
	}
	if !symbolicPart.MatchString(key.App) || !symbolicPart.MatchString(key.Model) {
		return modelKey{}, fmt.Errorf("invalid_model_key: %q.%q", app, model)
	}
	return key, nil
}

type deletePolicy string

const (
	deleteProtect deletePolicy = "protect"
	deleteSetNull deletePolicy = "set_null"
)

type relationDeclaration struct {
	Field     string
	Column    string
	Target    modelKey
	Nullable  bool
	Delete    deletePolicy
	Reverse   string
	Generated bool
}

type modelDescriptor struct {
	Key       modelKey
	Fields    []string
	Relations []relationDeclaration
}

type relationIdentity struct {
	Source modelKey `json:"source"`
	Field  string   `json:"field"`
}

func (i relationIdentity) String() string {
	return i.Source.String() + "." + i.Field
}

type boundRelation struct {
	Identity    relationIdentity `json:"identity"`
	Column      string           `json:"column"`
	Target      modelKey         `json:"target"`
	Nullable    bool             `json:"nullable"`
	Delete      deletePolicy     `json:"delete"`
	ReverseName string           `json:"reverse_name"`
}

type reverseBinding struct {
	Owner       modelKey `json:"owner"`
	Name        string   `json:"name"`
	Source      modelKey `json:"source"`
	SourceField string   `json:"source_field"`
	Cardinality string   `json:"cardinality"`
}

type bindingError struct {
	Code     string
	Model    modelKey
	Field    string
	Target   modelKey
	Reverse  string
	Conflict string
}

func (e *bindingError) Error() string {
	if e == nil {
		return "relation binding error"
	}
	return fmt.Sprintf("%s model=%s field=%s target=%s reverse=%s conflict=%s", e.Code, e.Model, e.Field, e.Target, e.Reverse, e.Conflict)
}

type bindingSet struct {
	models    []boundModel
	forward   []boundRelation
	reverse   []reverseBinding
	canonical []byte
	digest    string
}

func (s bindingSet) Models() []modelKey {
	keys := make([]modelKey, len(s.models))
	for i := range s.models {
		keys[i] = s.models[i].Key
	}
	return keys
}

type boundModel struct {
	Key    modelKey `json:"key"`
	Fields []string `json:"fields"`
}

func cloneBoundModels(models []boundModel) []boundModel {
	cloned := make([]boundModel, len(models))
	for i := range models {
		cloned[i] = boundModel{Key: models[i].Key, Fields: append([]string(nil), models[i].Fields...)}
	}
	return cloned
}

func (s bindingSet) modelFields(key modelKey) ([]string, bool) {
	position := sort.Search(len(s.models), func(i int) bool {
		return s.models[i].Key.String() >= key.String()
	})
	if position == len(s.models) || s.models[position].Key != key {
		return nil, false
	}
	return append([]string(nil), s.models[position].Fields...), true
}

func (s bindingSet) Forward() []boundRelation {
	return append([]boundRelation(nil), s.forward...)
}

func (s bindingSet) Reverse() []reverseBinding {
	return append([]reverseBinding(nil), s.reverse...)
}

func (s bindingSet) CanonicalBytes() []byte {
	return append([]byte(nil), s.canonical...)
}

func (s bindingSet) Digest() string {
	return s.digest
}

type bindingWire struct {
	Models  []boundModel     `json:"models"`
	Forward []boundRelation  `json:"forward"`
	Reverse []reverseBinding `json:"reverse"`
}

func bindProject(input []modelDescriptor) (bindingSet, error) {
	// Snapshot every caller-owned slice before global validation begins.
	models := make([]modelDescriptor, len(input))
	for i := range input {
		models[i] = modelDescriptor{
			Key:       input[i].Key,
			Fields:    append([]string(nil), input[i].Fields...),
			Relations: append([]relationDeclaration(nil), input[i].Relations...),
		}
		sort.Strings(models[i].Fields)
		sort.SliceStable(models[i].Relations, func(left, right int) bool {
			return models[i].Relations[left].Field < models[i].Relations[right].Field
		})
	}
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Key.String() < models[j].Key.String()
	})

	registry := make(map[modelKey]modelDescriptor, len(models))
	orderedModels := make([]boundModel, 0, len(models))
	for _, model := range models {
		if !symbolicPart.MatchString(model.Key.App) || !symbolicPart.MatchString(model.Key.Model) {
			return bindingSet{}, &bindingError{Code: "invalid_model_identity", Model: model.Key}
		}
		if _, exists := registry[model.Key]; exists {
			return bindingSet{}, &bindingError{Code: "duplicate_model_identity", Model: model.Key}
		}
		registry[model.Key] = model
		fields := append([]string(nil), model.Fields...)
		sort.Strings(fields)
		orderedModels = append(orderedModels, boundModel{Key: model.Key, Fields: fields})
	}

	type relationCandidate struct {
		source modelKey
		decl   relationDeclaration
	}
	candidates := make([]relationCandidate, 0)
	for _, model := range models {
		fieldNames := make(map[string]struct{}, len(model.Fields)+len(model.Relations))
		columnNames := make(map[string]string, len(model.Fields)+len(model.Relations))
		for _, field := range model.Fields {
			if !symbolicPart.MatchString(field) {
				return bindingSet{}, &bindingError{Code: "invalid_source_field", Model: model.Key, Field: field}
			}
			if _, duplicate := fieldNames[field]; duplicate {
				return bindingSet{}, &bindingError{Code: "duplicate_source_field", Model: model.Key, Field: field}
			}
			fieldNames[field] = struct{}{}
			columnNames[field] = field
		}
		for _, relation := range model.Relations {
			if !symbolicPart.MatchString(relation.Field) || !symbolicPart.MatchString(relation.Column) {
				return bindingSet{}, &bindingError{Code: "invalid_source_relation", Model: model.Key, Field: relation.Field, Target: relation.Target}
			}
			if relation.Generated {
				return bindingSet{}, &bindingError{Code: "relation_not_source_owned", Model: model.Key, Field: relation.Field, Target: relation.Target}
			}
			if _, duplicate := fieldNames[relation.Field]; duplicate {
				return bindingSet{}, &bindingError{Code: "duplicate_source_field", Model: model.Key, Field: relation.Field}
			}
			if conflict, duplicate := columnNames[relation.Column]; duplicate {
				return bindingSet{}, &bindingError{Code: "duplicate_source_column", Model: model.Key, Field: relation.Field, Conflict: conflict}
			}
			if !symbolicPart.MatchString(relation.Target.App) || !symbolicPart.MatchString(relation.Target.Model) {
				return bindingSet{}, &bindingError{Code: "invalid_target_identity", Model: model.Key, Field: relation.Field, Target: relation.Target}
			}
			if relation.Reverse == "" || !symbolicPart.MatchString(relation.Reverse) {
				return bindingSet{}, &bindingError{Code: "invalid_reverse_name", Model: model.Key, Field: relation.Field, Target: relation.Target, Reverse: relation.Reverse}
			}
			if relation.Delete != deleteProtect && relation.Delete != deleteSetNull {
				return bindingSet{}, &bindingError{Code: "unsupported_delete_policy", Model: model.Key, Field: relation.Field, Target: relation.Target}
			}
			if relation.Delete == deleteSetNull && !relation.Nullable {
				return bindingSet{}, &bindingError{Code: "set_null_requires_nullable", Model: model.Key, Field: relation.Field, Target: relation.Target}
			}
			fieldNames[relation.Field] = struct{}{}
			columnNames[relation.Column] = relation.Field
			candidates = append(candidates, relationCandidate{source: model.Key, decl: relation})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := relationIdentity{Source: candidates[i].source, Field: candidates[i].decl.Field}.String()
		right := relationIdentity{Source: candidates[j].source, Field: candidates[j].decl.Field}.String()
		return left < right
	})

	forward := make([]boundRelation, 0, len(candidates))
	reverse := make([]reverseBinding, 0, len(candidates))
	reverseNamespaces := make(map[string]relationIdentity, len(candidates))
	for _, candidate := range candidates {
		identity := relationIdentity{Source: candidate.source, Field: candidate.decl.Field}
		target, exists := registry[candidate.decl.Target]
		if !exists {
			return bindingSet{}, &bindingError{Code: "unresolved_target", Model: candidate.source, Field: candidate.decl.Field, Target: candidate.decl.Target}
		}
		for _, targetField := range target.Fields {
			if targetField == candidate.decl.Reverse {
				return bindingSet{}, &bindingError{Code: "reverse_name_collision", Model: candidate.source, Field: candidate.decl.Field, Target: candidate.decl.Target, Reverse: candidate.decl.Reverse, Conflict: target.Key.String() + "." + targetField}
			}
		}
		for _, targetRelation := range target.Relations {
			if targetRelation.Field == candidate.decl.Reverse {
				return bindingSet{}, &bindingError{Code: "reverse_name_collision", Model: candidate.source, Field: candidate.decl.Field, Target: candidate.decl.Target, Reverse: candidate.decl.Reverse, Conflict: target.Key.String() + "." + targetRelation.Field}
			}
		}
		namespace := candidate.decl.Target.String() + "." + candidate.decl.Reverse
		if existing, collision := reverseNamespaces[namespace]; collision {
			return bindingSet{}, &bindingError{Code: "reverse_name_collision", Model: candidate.source, Field: candidate.decl.Field, Target: candidate.decl.Target, Reverse: candidate.decl.Reverse, Conflict: existing.String()}
		}
		reverseNamespaces[namespace] = identity
		forward = append(forward, boundRelation{
			Identity:    identity,
			Column:      candidate.decl.Column,
			Target:      candidate.decl.Target,
			Nullable:    candidate.decl.Nullable,
			Delete:      candidate.decl.Delete,
			ReverseName: candidate.decl.Reverse,
		})
		reverse = append(reverse, reverseBinding{
			Owner:       candidate.decl.Target,
			Name:        candidate.decl.Reverse,
			Source:      candidate.source,
			SourceField: candidate.decl.Field,
			Cardinality: "one_to_many",
		})
	}
	sort.Slice(reverse, func(i, j int) bool {
		left := reverse[i].Owner.String() + "." + reverse[i].Name
		right := reverse[j].Owner.String() + "." + reverse[j].Name
		return left < right
	})

	wire := bindingWire{Models: orderedModels, Forward: forward, Reverse: reverse}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return bindingSet{}, fmt.Errorf("serialize binding candidate: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return bindingSet{
		models:    cloneBoundModels(orderedModels),
		forward:   append([]boundRelation(nil), forward...),
		reverse:   append([]reverseBinding(nil), reverse...),
		canonical: append([]byte(nil), canonical...),
		digest:    hex.EncodeToString(digest[:]),
	}, nil
}

type bindingSnapshot struct {
	set bindingSet
}

type bindingPublisher struct {
	current atomic.Pointer[bindingSnapshot]
}

func (p *bindingPublisher) Publish(models []modelDescriptor) error {
	set, err := bindProject(models)
	if err != nil {
		return err
	}
	p.current.Store(&bindingSnapshot{set: set})
	return nil
}

func (p *bindingPublisher) Snapshot() (bindingSet, bool) {
	snapshot := p.current.Load()
	if snapshot == nil {
		return bindingSet{}, false
	}
	// Return a fresh value boundary even though successful snapshots are never
	// mutated after their single atomic publication.
	return bindingSet{
		models:    cloneBoundModels(snapshot.set.models),
		forward:   snapshot.set.Forward(),
		reverse:   snapshot.set.Reverse(),
		canonical: snapshot.set.CanonicalBytes(),
		digest:    snapshot.set.Digest(),
	}, true
}
