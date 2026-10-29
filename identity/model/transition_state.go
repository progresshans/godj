package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"maps"
	"math"
	"slices"
	"strconv"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

// This is a bounded operator mapping, not an unbounded account export. The
// byte budget covers the exact scalar/profile/authority snapshot being pinned.
const MaximumTransitionStateBytes = 64 << 20

type transitionRow[M any] struct {
	row          M
	id, revision int64
	values       map[string]query.Value
	memberships  accountMemberships
	profile      map[string][]int64
}
type transitionPair[S, T any] struct {
	old  transitionRow[S]
	next transitionRow[T]
}
type transitionObservation[S, T any] struct {
	ownership  domain.Receipt
	actor      auth.Principal
	pairs      []transitionPair[S, T]
	digest     string
	rejection  error
	references []transitionReferenceObservation
	catalog    transitionCatalogObservation
	roleLinks  []transitionRoleLinkInventory
	inserted   transitionInsertions
}

func expectedTransitionRefusal(err error) bool {
	if classified, ok := err.(*Error); ok && classified.Unwrap() == nil && (classified.Code == CodePermission || classified.Code == CodeConflict || classified.Code == CodeInvalidInput || classified.Code == CodeNotFresh) {
		return true
	}
	_, rejected := validation.Rejected(err)
	return rejected && errors.Unwrap(err) == nil
}

func transitionBoolean(values map[string]query.Value, role ir.AccountBoolean) bool {
	value, _ := credentialBoolean(values, role) // The complete credential was validated by readTransitionRows.
	return value
}

func (owner *Transition[S, T]) observe(ctx context.Context, scope db.Queryer, actor auth.Principal, operation string, mappings []AccountMapping, catalog *CatalogMappings) (transitionObservation[S, T], error) {
	var zero transitionObservation[S, T]
	old, next := owner.old.state.credentials, owner.next.state.credentials
	ownership, current, err := owner.inspectAuthority(ctx, scope, actor)
	if err != nil {
		return zero, err
	}
	observedCatalog, err := owner.observeCatalog(ctx, scope, operation, catalog)
	if err != nil {
		return zero, err
	}
	before, err := readTransitionRows(ctx, scope, old, owner.oldMemberships, owner.config.MaxAccounts, ownership.Generation())
	if err != nil {
		return zero, err
	}
	after, err := readTransitionRows(ctx, scope, next, owner.nextMemberships, owner.config.MaxAccounts, ownership.Generation())
	if err != nil {
		return zero, err
	}
	if len(before) != len(mappings) || len(after) != len(mappings) {
		return zero, managementError(CodeConflict, nil)
	}
	result := transitionObservation[S, T]{ownership: ownership, actor: current, catalog: observedCatalog}
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.account-transition-state.v2"))
	digest.part([]byte(operation))
	digest.part([]byte(current.ID()))
	if err := appendTransitionCatalogDigest(digest, observedCatalog); err != nil {
		return zero, err
	}
	result.roleLinks, err = owner.observeTransitionAccountLinks(ctx, scope, digest, before, after, observedCatalog)
	if err != nil {
		return zero, err
	}
	for _, mapping := range mappings {
		a, oldPresent := before[mapping.SourceID]
		b, nextPresent := after[mapping.TargetID]
		if !oldPresent || !nextPresent || a.revision != mapping.SourceRevision || b.revision != mapping.TargetRevision {
			return zero, managementError(CodeConflict, nil)
		}
		if err := owner.validatePair(ctx, scope, operation, a, b, observedCatalog); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, a); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, b); err != nil {
			return zero, err
		}
		result.pairs = append(result.pairs, transitionPair[S, T]{a, b})
	}
	if digest.err != nil {
		return zero, digest.err
	}
	if len(owner.config.References) > 0 {
		sources, targets := make(map[int64]bool, len(mappings)), make(map[int64]int64, len(mappings))
		for _, mapping := range mappings {
			sources[mapping.SourceID], targets[mapping.SourceID] = true, mapping.TargetID
		}
		result.references, err = owner.observeReferences(ctx, scope, operation, referenceMappings(sources, targets, observedCatalog), digest)
		if err != nil {
			return zero, err
		}
	}
	result.digest = hex.EncodeToString(digest.hash.Sum(nil))
	return result, validateAccountSessionScope(ctx, scope)
}

func (owner *Transition[S, T]) inspectAuthority(ctx context.Context, scope db.Queryer, actor auth.Principal) (domain.Receipt, auth.Principal, error) {
	old := owner.old.state.credentials
	current, allowed, err := old.AuthorizeIn(ctx, scope, actor, owner.config.Authorizer, owner.required[0], owner.required[1:]...)
	if err != nil {
		return domain.Receipt{}, auth.Principal{}, err
	}
	if !allowed {
		return domain.Receipt{}, auth.Principal{}, managementError(CodePermission, nil)
	}
	ownership, err := domain.InspectIn(ctx, scope)
	if err != nil {
		return domain.Receipt{}, auth.Principal{}, err
	}
	role, _ := old.state.binding.RoleFingerprint()
	if ownership.RoleFingerprint() != role {
		return domain.Receipt{}, auth.Principal{}, domain.ErrMismatch
	}
	if _, err := accountaudit.InspectIn(ctx, scope); err != nil {
		return domain.Receipt{}, auth.Principal{}, err
	}
	if _, err := owner.old.state.database.InspectIn(ctx, scope); err != nil {
		return domain.Receipt{}, auth.Principal{}, err
	}
	if _, err := owner.next.state.database.InspectIn(ctx, scope); err != nil {
		return domain.Receipt{}, auth.Principal{}, err
	}

	return ownership, current, nil
}

func (owner *Transition[S, T]) validatePair(ctx context.Context, scope db.Queryer, operation string, a transitionRow[S], b transitionRow[T], catalog transitionCatalogObservation) error {
	old, next := owner.old.state.credentials, owner.next.state.credentials
	left, right := old.state.binding.state.role, next.state.binding.state.role
	mapped, err := catalog.memberships(a.memberships)
	if err != nil {
		return err
	}
	if !a.values[left.PrincipalID].Equal(b.values[right.PrincipalID]) || !a.values[left.Password].Equal(b.values[right.Password]) ||
		!slices.Equal(mapped.groups, b.memberships.groups) || !slices.Equal(mapped.direct, b.memberships.direct) || !slices.Equal(mapped.grants, b.memberships.grants) {
		return managementError(CodeConflict, nil)
	}
	for _, equal := range []struct{ a, b bool }{
		{transitionBoolean(a.values, left.Active), transitionBoolean(b.values, right.Active)},
		{transitionBoolean(a.values, left.Staff), transitionBoolean(b.values, right.Staff)},
		{transitionBoolean(a.values, left.Superuser), transitionBoolean(b.values, right.Superuser)},
	} {
		if equal.a != equal.b {
			return managementError(CodeConflict, nil)
		}
	}
	// Validate the proposed tombstone before touching a session or row.
	retired, err := transitionRetirement(old.state.binding, a, operation)
	if err != nil {
		return err
	}
	retiredRow, err := retired.Model()
	if err != nil {
		return err
	}
	retiredValues, err := old.state.binding.state.objects.ModelValues(retiredRow)
	if err != nil {
		return err
	}
	if err := validateTransitionRow(ctx, scope, old.state.binding.state.objects, retiredRow, retiredValues); err != nil {
		return err
	}
	// The staged target must already satisfy its selected write policy.
	// A switch must never silently normalize or change reviewed data.
	canonical := maps.Clone(b.values)
	metadata, _ := next.state.binding.state.objects.Metadata()
	if err := normalizeAccountInput(metadata, right, canonical); err != nil {
		return err
	}
	if !maps.Equal(canonical, b.values) {
		return managementError(CodeConflict, nil)
	}
	if owner.config.Validate != nil {
		oldCopy, err := old.state.binding.state.objects.ApplyValues(a.row, map[string]query.Value{left.Password: query.String("")})
		if err != nil {
			return err
		}
		newCopy, err := next.state.binding.state.objects.ApplyValues(b.row, map[string]query.Value{right.Password: query.String("")})
		if err != nil {
			return err
		}
		selected := transitionMembershipCopy(b.memberships, b.profile)
		selected.state.pendingGroups, selected.state.pendingPermissions = catalog.pendingGroups, catalog.pendingPermissions
		diagnostics, err := owner.config.Validate(ctx, scope, oldCopy, newCopy, transitionMembershipCopy(a.memberships, a.profile), selected)
		if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
			return err
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
	}

	return validateAccountSessionScope(ctx, scope)
}

func transitionMembershipCopy(membership accountMemberships, profile map[string][]int64) AccountMemberships {
	owned := make(map[string][]int64, len(profile))
	for name, keys := range profile {
		owned[name] = slices.Clone(keys)
	}
	return AccountMemberships{state: &accountMembershipState{groups: slices.Clone(membership.groups), permissions: slices.Clone(membership.direct), profile: owned}}
}

func readTransitionRows[M any](ctx context.Context, scope db.Queryer, store *CredentialStore[M], memberships []deletionMembership, maximum int, generation [sha256.Size]byte) (map[int64]transitionRow[M], error) {
	objects, role := store.state.binding.state.objects, store.state.binding.state.role
	result := make(map[int64]transitionRow[M])
	budget := &transitionDigestWriter{hash: sha256.New()}
	var last int64
	started := false
	for {
		source := objects.Using(scope).OrderBy(store.state.key.Asc())
		if started {
			source = source.Filter(store.state.key.GreaterThan(last))
		}
		bounded, err := source.Limit(min(catalogOwnerBatchSize, maximum-len(result)+1))
		if err != nil {
			return nil, err
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return result, nil
		}
		for _, row := range rows {
			if len(result) >= maximum {
				return nil, managementError(CodeInvalidInput, nil)
			}
			values, err := objects.ModelValues(row)
			if err != nil {
				return nil, err
			}
			key, present, err := objects.PrimaryKey(row)
			id, valid := key.Integer()
			revision, revisionValid := values[role.Revision].Integer()
			if err != nil || !present || !valid || !revisionValid || revision <= 0 || started && id <= last {
				return nil, invalidAccountCredential()
			}
			stored, err := store.state.relations.memberships(ctx, scope, id)
			if err != nil {
				return nil, err
			}
			// The join reader supplies effective grants, while raw declared links
			// independently reject dangling membership endpoints rather than
			// silently losing them in that join.
			for _, membership := range memberships {
				if membership.owner != role.Model || membership.profile {
					continue
				}
				actual, err := membership.readKeys(ctx, scope, id)
				if err != nil {
					return nil, err
				}
				expected := stored.direct
				if membership.field == role.Groups {
					expected = stored.groups
				}
				if !slices.Equal(actual, expected) {
					return nil, invalidAccountCredential()
				}
			}
			if _, err := store.state.binding.PrepareCredential(generation, row, stored.grants); err != nil {
				return nil, err
			}
			if err := validateTransitionRow(ctx, scope, objects, row, values); err != nil {
				return nil, err
			}
			profile, err := readProfileMemberships(ctx, scope, memberships, id, nil)
			if err != nil {
				return nil, err
			}
			observed := transitionRow[M]{row: row, id: id, revision: revision, values: values, memberships: stored, profile: profile}
			if err := writeTransitionRow(budget, observed); err != nil {
				return nil, err
			}
			result[id] = observed
			last, started = id, true
		}
	}
}

func validateTransitionRow[M any](ctx context.Context, scope db.Queryer, objects orm.Manager[M], row M, values map[string]query.Value) error {
	return validateTransitionValues(ctx, scope, objects, row, values, false)
}

func validateTransitionValues[M any](ctx context.Context, scope db.Queryer, objects orm.Manager[M], row M, values map[string]query.Value, candidate bool) error {
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	fields := make([]serializers.ModelField, 0, len(metadata.Fields))
	for _, field := range metadata.Fields {
		if candidate && field.PrimaryKey {
			continue
		}
		selected := serializers.ModelField{Name: field.Name, ReadOnly: true}
		switch field.Kind {
		case ir.FieldChar, ir.FieldEmail, ir.FieldURL, ir.FieldSlug, ir.FieldFile, ir.FieldImage, ir.FieldText:
			selected.AllowEmpty = field.Blank
		}
		fields = append(fields, selected)
	}
	spec, err := serializers.FromModel(metadata, fields...)
	if err != nil {
		return err
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(_ M, field ir.Field) (query.Value, bool) { value, found := values[field.Name]; return value, found })
	if err != nil {
		return err
	}
	if _, err := encoder.Encode(row); err != nil {
		return err
	}
	uniqueValues, current := values, &row
	if candidate {
		// An unsaved target has no row to exclude from advisory uniqueness.
		// Its absent primary key is not a supplied uniqueness candidate either.
		uniqueValues, current = maps.Clone(values), nil
		for _, field := range metadata.Fields {
			if field.PrimaryKey {
				delete(uniqueValues, field.Name)
			}
		}
	}
	for _, check := range []func(context.Context, db.Queryer, map[string]query.Value, *M) (validation.Errors, error){objects.ValidateUniqueFields, objects.ValidateUniqueConstraints} {
		diagnostics, err := check(ctx, scope, uniqueValues, current)
		if err != nil {
			return err
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
	}
	return nil
}

func transitionRetirement[M any](binding Binding[M], row transitionRow[M], operation string) (orm.PreparedModel[M], error) {
	role := binding.state.role
	digest := sha256.Sum256([]byte("godj.account-retirement.v1\x00" + operation + "\x00" + strconv.FormatInt(row.id, 10)))
	values := map[string]query.Value{role.Password: query.String("!" + hex.EncodeToString(digest[:])), role.Revision: query.Integer(row.revision + 1)}
	for _, flag := range []string{role.Active.Field, role.Staff.Field, role.Superuser.Field} {
		if flag != "" {
			values[flag] = query.Boolean(false)
		}
	}
	if row.revision <= 0 || row.revision == math.MaxInt64 {
		return orm.PreparedModel[M]{}, managementError(CodeInvalidInput, nil)
	}
	return binding.state.objects.PrepareValues(&row.row, values)
}

func (owner *Transition[S, T]) verifyRows(ctx context.Context, scope db.Queryer, expected transitionObservation[S, T], mappings []AccountMapping) error {
	// Correspondence was checked against the reviewed source. A catalog
	// creation may intentionally advance a shared source Group revision; its
	// post-write inventory is now pinned independently of those prior revisions.
	catalog, err := owner.observeCatalogRows(ctx, scope, expected.catalog.operation)
	if err != nil {
		return err
	}
	if catalog.digest != expected.catalog.digest {
		return managementError(CodePersistence, nil)
	}
	before, err := readTransitionRows(ctx, scope, owner.old.state.credentials, owner.oldMemberships, owner.config.MaxAccounts, expected.ownership.Generation())
	if err != nil {
		return err
	}
	after, err := readTransitionRows(ctx, scope, owner.next.state.credentials, owner.nextMemberships, owner.config.MaxAccounts, expected.ownership.Generation())
	if err != nil {
		return err
	}
	if len(before) != len(mappings) || len(after) != len(mappings) {
		return managementError(CodePersistence, nil)
	}
	for _, pair := range expected.pairs {
		if !sameTransitionRow(before[pair.old.id], pair.old) || !sameTransitionRow(after[pair.next.id], pair.next) {
			return managementError(CodePersistence, nil)
		}
	}
	if err := verifyTransitionAccountLinks(ctx, scope, expected.roleLinks); err != nil {
		return err
	}
	if err := expected.inserted.verify(ctx); err != nil {
		return err
	}
	if err := checkTransitionReadbackBudget(expected, 0); err != nil {
		return err
	}
	return validateAccountSessionScope(ctx, scope)
}

// Allocation and retirement can enlarge the reviewed representation. Bound
// the complete final account/catalog/host state too, not each component alone.
func checkTransitionReadbackBudget[S, T any](expected transitionObservation[S, T], extraBytes int) error {
	if extraBytes < 0 || extraBytes > MaximumTransitionStateBytes {
		return managementError(CodeInvalidInput, nil)
	}
	budget := &transitionDigestWriter{hash: sha256.New(), size: extraBytes}
	if err := appendTransitionCatalogDigest(budget, expected.catalog); err != nil {
		return err
	}
	if expected.inserted.bytes < 0 || expected.inserted.bytes > MaximumTransitionStateBytes-budget.size {
		return managementError(CodeInvalidInput, nil)
	}
	budget.size += expected.inserted.bytes
	for _, inventory := range expected.roleLinks {
		if err := writeTransitionRoleLinkInventory(budget, inventory); err != nil {
			return err
		}
	}
	for _, pair := range expected.pairs {
		if err := writeTransitionRow(budget, pair.old); err != nil {
			return err
		}
		if err := writeTransitionRow(budget, pair.next); err != nil {
			return err
		}
	}
	for _, reference := range expected.references {
		if reference.bytes < 0 || reference.bytes > MaximumTransitionStateBytes-budget.size {
			return managementError(CodeInvalidInput, nil)
		}
		budget.size += reference.bytes
		encoded, err := json.Marshal(reference.manifest)
		if err != nil {
			return err
		}
		budget.part(encoded)
	}
	return budget.err
}

func sameTransitionRow[M any](a, b transitionRow[M]) bool {
	return a.values != nil && maps.Equal(a.values, b.values) && slices.Equal(a.memberships.groups, b.memberships.groups) &&
		slices.Equal(a.memberships.direct, b.memberships.direct) && slices.Equal(a.memberships.grants, b.memberships.grants) && maps.EqualFunc(a.profile, b.profile, slices.Equal[[]int64])
}

type transitionDigestWriter struct {
	hash hash.Hash
	size int
	err  error
}

func (writer *transitionDigestWriter) part(value []byte) {
	if writer.err != nil {
		return
	}
	if len(value) > MaximumTransitionStateBytes-writer.size-8 {
		writer.err = managementError(CodeInvalidInput, nil)
		return
	}
	writer.size += len(value) + 8
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.hash.Write(length[:])
	_, _ = writer.hash.Write(value)
}
func writeTransitionRow[M any](writer *transitionDigestWriter, row transitionRow[M]) error {
	if err := writeTransitionValues(writer, row.values); err != nil {
		return err
	}
	collections := map[string][]int64{"groups": row.memberships.groups, "permissions": row.memberships.direct}
	encoded, err := json.Marshal(struct {
		Roles   map[string][]int64
		Profile map[string][]int64
		Grants  []auth.Permission
	}{collections, row.profile, row.memberships.grants})
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	writer.part(encoded)
	return writer.err
}

func writeTransitionValues(writer *transitionDigestWriter, values map[string]query.Value) error {
	names := slices.Sorted(maps.Keys(values))
	writer.part([]byte(strconv.Itoa(len(names))))
	for _, name := range names {
		value := values[name]
		writer.part([]byte(name))
		writer.part([]byte(value.Kind()))
		if number, floating := value.Float(); floating {
			var bits [8]byte
			binary.BigEndian.PutUint64(bits[:], math.Float64bits(number))
			writer.part(bits[:])
			continue
		}
		data, err := value.DatabaseValue()
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return managementError(CodeInvalidInput, err)
		}
		writer.part(encoded)
	}
	return writer.err
}
