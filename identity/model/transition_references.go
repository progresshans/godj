package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const (
	MaximumTransitionReferenceModels = 64
	MaximumTransitionReferenceRows   = 65536
)

// TransitionReferenceFields names a staged pair of real host FK/OneToOne
// fields. Source points to the old account model and is retained; Target
// points to the new account model. Schema expansion/contraction is an explicit
// migration, not an implicit change to either field's declared target.
type TransitionReferenceFields struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// TransitionReferenceConfig owns one complete host model inventory. Every
// non-NULL source FK must be covered by the account mapping. A target FK must
// be NULL or already equal the explicitly mapped target; unrelated values
// are never overwritten. All pairs in a row share one optional revision bump
// and one audit event. MaxRows defaults to 4096 and fails closed.
//
// Validate runs when actual target keys are available, before host writes and
// again during final readback. It receives detached original/final rows and
// the admitted source actor. It must not retain/mutate the borrowed reader,
// re-enter the backend or publish effects, and must be safe for concurrent use.
type TransitionReferenceConfig[H any] struct {
	Fields                []TransitionReferenceFields
	RevisionField         string
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	MaxRows               int
	Validate              func(context.Context, db.Queryer, auth.Principal, H, H) (validation.Errors, error)
}

// TransitionReference binds a typed host descriptor to both account roles and
// the same complete host source. Construction performs no I/O or callback.
type TransitionReference[S, T any] struct{ state *transitionReferenceState }

func (TransitionReference[S, T]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionReference{redacted}"))
}

type transitionReferenceState struct {
	source            string
	oldRole, nextRole [sha256.Size]byte
	model             ir.ModelIdentity
	required          []auth.Permission
	observe           func(context.Context, db.Queryer, string, map[int64]bool, map[int64]int64) (transitionReferenceObservation, error)
}

// TransitionReferenceManifest describes the reviewed host inventory without
// exporting row values. Its state digest is salted by the operation ID.
type TransitionReferenceManifest struct {
	Model         ir.ModelIdentity            `json:"model"`
	Fields        []TransitionReferenceFields `json:"fields"`
	RevisionField string                      `json:"revision_field,omitempty"`
	Rows          int                         `json:"rows"`
	StateSHA256   string                      `json:"state_sha256"`
}

type transitionReferenceObservation struct {
	manifest TransitionReferenceManifest
	bytes    int
	apply    func(context.Context, db.Session, auth.Principal, []AccountMapping, time.Time) (transitionReferenceApplied, error)
}

type transitionReferenceApplied struct {
	changed int
	verify  func(context.Context, db.Queryer) error
}

type transitionReferenceRow[H any] struct {
	row    H
	id     int64
	values map[string]query.Value
}

func NewTransitionReference[S, T, H any](source Binding[S], target Binding[T], model ir.ModelIdentity, descriptor orm.RelationObjectDescriptor[H], config TransitionReferenceConfig[H]) (TransitionReference[S, T], error) {
	var zero TransitionReference[S, T]
	if source.state == nil || target.state == nil || !zeroModelDescriptor(descriptor) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if _, writable := descriptor.(orm.ValueAssignmentDescriptor[H]); !writable {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	project, old, next := source.state.project, source.state.role, target.state.role
	left, err := project.SourceSHA256()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	right, err := target.state.project.SourceSHA256()
	if err != nil || left != right || old.Model == next.Model {
		return zero, managementError(CodeInvalidConfig, err)
	}
	for _, critical := range []ir.ModelIdentity{old.Model, next.Model, old.Catalog.Group, old.Catalog.Permission, next.Catalog.Group, next.Catalog.Permission} {
		if model == critical {
			return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "account and catalog references require their identity lifecycle owner"}
		}
	}
	if _, err := orm.BindModel(project, model, descriptor); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	objects := orm.NewManager[H](descriptor)
	metadata, err := objects.Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	key, err := objects.PrimaryKeyField()
	if err != nil || key.Kind != ir.FieldAuto {
		return zero, managementError(CodeInvalidConfig, err)
	}
	fields, err := normalizeTransitionReferenceFields(model, config.Fields, config.RevisionField)
	if err != nil {
		return zero, err
	}
	for _, pair := range fields {
		for _, ref := range []struct {
			name   string
			target ir.ModelIdentity
		}{{pair.Source, old.Model}, {pair.Target, next.Model}} {
			field, found := namedField(metadata, ref.name)
			if !found || field.PrimaryKey || field.Kind != ir.FieldForeignKey || field.Relation == nil || !field.Relation.Cardinality.SingleValued() || field.Relation.Target != ref.target {
				return zero, managementError(CodeInvalidConfig, nil)
			}
		}
		for _, relation := range project.ManyToManyRelations() {
			critical := relation.Source == old.Model || relation.Source == next.Model ||
				relation.Source == old.Catalog.Group && relation.Field == old.Catalog.GroupPermissions ||
				relation.Source == next.Catalog.Group && relation.Field == next.Catalog.GroupPermissions
			if critical && relation.Through.Model == model && (relation.Through.SourceField == pair.Target || relation.Through.TargetField == pair.Target) {
				return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "account membership remapping requires its identity lifecycle owner"}
			}
		}
	}
	if config.RevisionField != "" {
		field, found := namedField(metadata, config.RevisionField)
		if !found || field.PrimaryKey || field.Kind != ir.FieldInteger || field.Nullable {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	required, err := auth.RequiredPermissions(config.Permission, config.AdditionalPermissions...)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	if config.MaxRows == 0 {
		config.MaxRows = 4096
	}
	if config.MaxRows < 1 || config.MaxRows > MaximumTransitionReferenceRows {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	config.Fields, config.AdditionalPermissions = fields, slices.Clone(config.AdditionalPermissions)
	state := &transitionReferenceState{source: left, oldRole: source.state.roleFingerprint, nextRole: target.state.roleFingerprint, model: model, required: required}
	state.observe = func(ctx context.Context, scope db.Queryer, operation string, sources map[int64]bool, targets map[int64]int64) (transitionReferenceObservation, error) {
		rows, budget, err := readTransitionReferenceRows(ctx, scope, objects, config.MaxRows, operation)
		if err != nil {
			return transitionReferenceObservation{}, err
		}
		for _, row := range rows {
			if _, err := transitionReferenceUpdates(row.values, config, sources, targets); err != nil {
				return transitionReferenceObservation{}, err
			}
		}
		manifest := TransitionReferenceManifest{Model: model, Fields: slices.Clone(fields), RevisionField: config.RevisionField, Rows: len(rows), StateSHA256: hex.EncodeToString(budget.hash.Sum(nil))}
		return transitionReferenceObservation{manifest: manifest, bytes: budget.size,
			apply: func(ctx context.Context, scope db.Session, actor auth.Principal, mappings []AccountMapping, at time.Time) (transitionReferenceApplied, error) {
				if err := verifyTransitionReferenceRows(ctx, scope, objects, config.MaxRows, operation, rows); err != nil {
					return transitionReferenceApplied{}, err
				}
				actual := make(map[int64]int64, len(mappings))
				for _, mapping := range mappings {
					actual[mapping.SourceID] = mapping.TargetID
				}
				return applyTransitionReferenceRows(ctx, scope, actor, objects, model, config, operation, rows, sources, actual, at)
			}}, nil
	}
	return TransitionReference[S, T]{state: state}, nil
}

func normalizeTransitionReferenceFields(model ir.ModelIdentity, fields []TransitionReferenceFields, revision string) ([]TransitionReferenceFields, error) {
	if len(fields) == 0 || len(fields) >= admin.MaximumChangedFields || len(model.AppLabel) > admin.MaximumModelBytes || len(model.ModelName) > admin.MaximumModelBytes {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	owned := slices.Clone(fields)
	slices.SortFunc(owned, func(a, b TransitionReferenceFields) int {
		if result := strings.Compare(a.Source, b.Source); result != 0 {
			return result
		}
		return strings.Compare(a.Target, b.Target)
	})
	targets := make(map[string]bool, len(owned))
	for _, pair := range owned {
		if pair.Source == pair.Target || pair.Source == revision || pair.Target == revision || targets[pair.Target] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if _, err := admin.PrepareEventTemplate("transition-reference", model.AppLabel+"."+model.ModelName, admin.ActionChange, []string{pair.Source, pair.Target}, ""); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		targets[pair.Target] = true
	}
	for _, pair := range owned {
		if targets[pair.Source] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	if revision != "" {
		if _, err := admin.PrepareEventTemplate("transition-reference", model.AppLabel+"."+model.ModelName, admin.ActionChange, []string{revision}, ""); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	return owned, nil
}

func transitionReferenceUpdates[H any](values map[string]query.Value, config TransitionReferenceConfig[H], sources map[int64]bool, targets map[int64]int64) (map[string]query.Value, error) {
	updates := make(map[string]query.Value)
	pending := false
	for _, pair := range config.Fields {
		left, leftPresent := values[pair.Source]
		right, rightPresent := values[pair.Target]
		if !leftPresent || !rightPresent {
			return nil, managementError(CodePersistence, nil)
		}
		if left.IsNull() {
			if !right.IsNull() {
				return nil, managementError(CodeConflict, nil)
			}
			continue
		}
		id, integer := left.Integer()
		if !integer || !sources[id] {
			return nil, managementError(CodeConflict, nil)
		}
		if targets == nil {
			if !right.IsNull() {
				return nil, managementError(CodeConflict, nil)
			}
			pending = true
			continue // Creation has not allocated target keys yet.
		}
		mapped, present := targets[id]
		if !present {
			return nil, managementError(CodeConflict, nil)
		}
		wanted := query.Integer(mapped)
		if !right.IsNull() && !right.Equal(wanted) {
			return nil, managementError(CodeConflict, nil)
		}
		if !right.Equal(wanted) {
			updates[pair.Target] = wanted
		}
	}
	if config.RevisionField != "" {
		revision, integer := values[config.RevisionField].Integer()
		if !integer || revision < 1 {
			return nil, managementError(CodeConflict, nil)
		}
		if len(updates) > 0 || pending {
			if revision == math.MaxInt64 {
				return nil, managementError(CodeConflict, nil)
			}
			if !pending {
				updates[config.RevisionField] = query.Integer(revision + 1)
			}
		}
	}
	return updates, nil
}

func readTransitionReferenceRows[H any](ctx context.Context, scope db.Queryer, objects orm.Manager[H], maximum int, operation string) ([]transitionReferenceRow[H], *transitionDigestWriter, error) {
	key, err := objects.PrimaryKeyField()
	if err != nil {
		return nil, nil, err
	}
	field := orm.NewAutoField[H](key)
	budget := &transitionDigestWriter{hash: sha256.New()}
	budget.part([]byte("godj.account-transition-references.v1"))
	budget.part([]byte(operation))
	var result []transitionReferenceRow[H]
	var last int64
	started := false
	for {
		selected := objects.Using(scope).OrderBy(field.Asc())
		if started {
			selected = selected.Filter(field.GreaterThan(last))
		}
		bounded, err := selected.Limit(min(catalogOwnerBatchSize, maximum-len(result)+1))
		if err != nil {
			return nil, nil, err
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) == 0 {
			return result, budget, validateAccountSessionScope(ctx, scope)
		}
		for _, row := range rows {
			if len(result) >= maximum {
				return nil, nil, managementError(CodeInvalidInput, nil)
			}
			values, err := objects.ModelValues(row)
			if err != nil {
				return nil, nil, err
			}
			keyValue, present, err := objects.PrimaryKey(row)
			id, integer := keyValue.Integer()
			if err != nil || !present || !integer || started && id <= last {
				return nil, nil, managementError(CodePersistence, err)
			}
			if err := validateTransitionRow(ctx, scope, objects, row, values); err != nil {
				return nil, nil, err
			}
			if err := writeTransitionRow(budget, transitionRow[H]{values: values}); err != nil {
				return nil, nil, err
			}
			result = append(result, transitionReferenceRow[H]{row: row, id: id, values: values})
			last, started = id, true
		}
	}
}

func verifyTransitionReferenceRows[H any](ctx context.Context, scope db.Queryer, objects orm.Manager[H], maximum int, operation string, expected []transitionReferenceRow[H]) error {
	actual, _, err := readTransitionReferenceRows(ctx, scope, objects, maximum, operation)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return managementError(CodeConflict, nil)
	}
	for i, row := range actual {
		if row.id != expected[i].id || !maps.Equal(row.values, expected[i].values) {
			return managementError(CodeConflict, nil)
		}
	}
	return nil
}

func applyTransitionReferenceRows[H any](ctx context.Context, scope db.Session, actor auth.Principal, objects orm.Manager[H], model ir.ModelIdentity, config TransitionReferenceConfig[H], operation string, rows []transitionReferenceRow[H], sources map[int64]bool, targets map[int64]int64, at time.Time) (transitionReferenceApplied, error) {
	var zero transitionReferenceApplied
	final := make([]transitionReferenceRow[H], len(rows))
	prepared := make([]orm.PreparedModel[H], len(rows))
	check := func(ctx context.Context, scope db.Queryer, before, after H) error {
		if config.Validate == nil {
			return validateAccountSessionScope(ctx, scope)
		}
		left, err := objects.ApplyValues(before, nil)
		if err != nil {
			return err
		}
		right, err := objects.ApplyValues(after, nil)
		if err != nil {
			return err
		}
		diagnostics, err := config.Validate(ctx, scope, actor, left, right)
		if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
			return err
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
		return nil
	}
	for i, row := range rows {
		updates, err := transitionReferenceUpdates(row.values, config, sources, targets)
		if err != nil {
			return zero, err
		}
		prepared[i], err = objects.PrepareValues(&row.row, updates)
		if err != nil {
			return zero, err
		}
		candidate, err := prepared[i].Model()
		if err != nil {
			return zero, err
		}
		values, err := objects.ModelValues(candidate)
		if err != nil {
			return zero, err
		}
		if err := validateTransitionRow(ctx, scope, objects, candidate, values); err != nil {
			return zero, err
		}
		if err := check(ctx, scope, row.row, candidate); err != nil {
			return zero, err
		}
		final[i] = transitionReferenceRow[H]{row: candidate, id: row.id, values: values}
	}
	changed := 0
	for i, candidate := range prepared {
		fields := slices.DeleteFunc(candidate.ChangedFields(), func(name string) bool { return name == config.RevisionField })
		if len(fields) == 0 {
			continue
		}
		if err := candidate.Save(ctx, scope, &final[i].row); err != nil {
			return zero, err
		}
		event, err := accountaudit.Prepare(actor, model.AppLabel+"."+model.ModelName, final[i].id, admin.ActionChange, fields, at)
		if err != nil {
			return zero, err
		}
		if _, err := accountaudit.AppendIn(ctx, scope, event); err != nil {
			return zero, err
		}
		changed++
	}
	verify := func(ctx context.Context, scope db.Queryer) error {
		if err := verifyTransitionReferenceRows(ctx, scope, objects, config.MaxRows, operation, final); err != nil {
			return err
		}
		for i, row := range final {
			if err := check(ctx, scope, rows[i].row, row.row); err != nil {
				return err
			}
		}
		return nil
	}
	if err := verify(ctx, scope); err != nil {
		return zero, err
	}
	return transitionReferenceApplied{changed: changed, verify: verify}, nil
}

func transitionReferenceManifests(observations []transitionReferenceObservation) []TransitionReferenceManifest {
	if len(observations) == 0 {
		return nil
	}
	result := make([]TransitionReferenceManifest, len(observations))
	for i, observation := range observations {
		result[i] = observation.manifest
		result[i].Fields = slices.Clone(result[i].Fields)
	}
	return result
}

func compareTransitionReferenceIdentity(a, b ir.ModelIdentity) int {
	return strings.Compare(a.AppLabel+"."+a.ModelName, b.AppLabel+"."+b.ModelName)
}

func cloneTransitionReferenceManifests(input []TransitionReferenceManifest) []TransitionReferenceManifest {
	result := slices.Clone(input)
	for i := range result {
		result[i].Fields = slices.Clone(result[i].Fields)
	}
	return result
}

func sameTransitionReferenceManifests(a, b []TransitionReferenceManifest) bool {
	return slices.EqualFunc(a, b, func(a, b TransitionReferenceManifest) bool {
		return a.Model == b.Model && a.RevisionField == b.RevisionField && a.Rows == b.Rows && a.StateSHA256 == b.StateSHA256 && slices.Equal(a.Fields, b.Fields)
	})
}

func (owner *Transition[S, T]) observeReferences(ctx context.Context, scope db.Queryer, operation string, sources map[int64]bool, targets map[int64]int64, digest *transitionDigestWriter) ([]transitionReferenceObservation, error) {
	var result []transitionReferenceObservation
	for _, reference := range owner.config.References {
		observation, err := reference.state.observe(ctx, scope, operation, sources, targets)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(observation.manifest)
		if err != nil {
			return nil, managementError(CodeInvalidInput, err)
		}
		// Count the full observed rows toward the shared state budget even
		// though this parent digest only needs their salted digest and schema.
		if observation.bytes > MaximumTransitionStateBytes-digest.size {
			return nil, managementError(CodeInvalidInput, nil)
		}
		digest.size += observation.bytes
		digest.part(encoded)
		if digest.err != nil {
			return nil, digest.err
		}
		result = append(result, observation)
	}
	return result, nil
}

func validateTransitionReferenceManifests(input []TransitionReferenceManifest) error {
	if len(input) > MaximumTransitionReferenceModels {
		return managementError(CodeInvalidInput, nil)
	}
	last := ""
	for _, item := range input {
		fields, err := normalizeTransitionReferenceFields(item.Model, item.Fields, item.RevisionField)
		if err != nil || !slices.Equal(fields, item.Fields) || item.Rows < 0 || item.Rows > MaximumTransitionReferenceRows || !transitionDigest(item.StateSHA256) {
			return managementError(CodeInvalidInput, err)
		}
		identity := item.Model.AppLabel + "." + item.Model.ModelName
		if identity <= last {
			return managementError(CodeInvalidInput, nil)
		}
		last = identity
	}
	return nil
}
