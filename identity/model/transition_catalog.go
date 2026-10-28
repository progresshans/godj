package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const MaximumTransitionCatalogRows = domain.MaximumMappings

// CatalogMappings explicitly pairs every source and staged target group and
// permission, including rows not currently assigned to any account. Codes and
// group grants must retain their meanings. Keys and revisions are evidence;
// neither equal keys nor equal labels imply a mapping between different models.
type CatalogMappings = domain.CatalogMappings

type transitionCatalogRow struct {
	revision    int64
	code        auth.Permission
	permissions []int64
}

type transitionCatalogInventory struct {
	groups, permissions map[int64]transitionCatalogRow
}

type transitionCatalogObservation struct {
	operation, digest string
	bytes             int
	mapping           *CatalogMappings
	groups, direct    map[int64]int64
}

// Catalog storage is read with the real typed descriptors already validated by
// CredentialStore. Only the declared through key/FK projection is dynamic, as
// in the account lifecycle owner. No special default-User adapter is involved.
func bindTransitionCatalog[M, G, P any](binding Binding[M], groups orm.Manager[G], permissions orm.Manager[P]) (func(context.Context, db.Queryer, int, *transitionDigestWriter) (transitionCatalogInventory, error), error) {
	role := binding.state.role
	memberships, err := bindDeletionMemberships(binding.state.project, role)
	if err != nil {
		return nil, err
	}
	var links deletionMembership
	for _, membership := range memberships {
		if membership.owner == role.Catalog.Group && membership.field == role.Catalog.GroupPermissions {
			links = membership
		}
	}
	if links.table == "" {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return func(ctx context.Context, scope db.Queryer, maximum int, digest *transitionDigestWriter) (transitionCatalogInventory, error) {
		var zero transitionCatalogInventory
		grants, err := readTransitionCatalogRows(ctx, scope, permissions, role.Catalog.PermissionRevision, role.Catalog.PermissionCode, maximum, digest)
		if err != nil {
			return zero, err
		}
		owners, err := readTransitionCatalogRows(ctx, scope, groups, role.Catalog.GroupRevision, "", maximum, digest)
		if err != nil {
			return zero, err
		}
		result := transitionCatalogInventory{groups: owners, permissions: grants}
		if err := readTransitionCatalogLinks(ctx, scope, links, result, digest); err != nil {
			return zero, err
		}
		return result, validateAccountSessionScope(ctx, scope)
	}, nil
}

func readTransitionCatalogRows[M any](ctx context.Context, scope db.Queryer, objects orm.Manager[M], revisionName, codeName string, maximum int, digest *transitionDigestWriter) (map[int64]transitionCatalogRow, error) {
	key, err := objects.PrimaryKeyField()
	if err != nil {
		return nil, err
	}
	field := orm.NewAutoField[M](key)
	result := make(map[int64]transitionCatalogRow)
	codes := make(map[auth.Permission]bool)
	digest.part([]byte("catalog-model"))
	var last int64
	started := false
	for {
		selected := objects.Using(scope).OrderBy(field.Asc())
		if started {
			selected = selected.Filter(field.GreaterThan(last))
		}
		bounded, err := selected.Limit(min(catalogOwnerBatchSize, maximum-len(result)+1))
		if err != nil {
			return nil, err
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return nil, err
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
			id, integer := key.Integer()
			revision, revisionValid := values[revisionName].Integer()
			if err != nil || !present || !integer || !revisionValid || revision <= 0 || started && id <= last {
				return nil, invalidAccountCredential()
			}
			if err := validateTransitionRow(ctx, scope, objects, row, values); err != nil {
				return nil, err
			}
			observed := transitionCatalogRow{revision: revision}
			if codeName != "" {
				code, text := values[codeName].String()
				permission, err := auth.NewPermission(code)
				if !text || err != nil || string(permission) != code || codes[permission] {
					return nil, invalidAccountCredential()
				}
				codes[permission], observed.code = true, permission
			}
			if err := writeTransitionValues(digest, values); err != nil {
				return nil, err
			}
			result[id] = observed
			last, started = id, true
		}
		if len(rows) < catalogOwnerBatchSize {
			digest.part([]byte(strconv.Itoa(len(result))))
			return result, digest.err
		}
	}
}

func readTransitionCatalogLinks(ctx context.Context, scope db.Queryer, membership deletionMembership, inventory transitionCatalogInventory, digest *transitionDigestWriter) error {
	sets := make(map[int64]map[int64]bool)
	var last int64
	started := false
	digest.part([]byte("catalog-links"))
	for {
		condition := query.NewCondition(membership.key, query.LookupIsNull, query.Boolean(false))
		if started {
			condition = query.NewCondition(membership.key, query.LookupGreaterThan, query.Integer(last))
		}
		rows, err := membership.read(ctx, scope, catalogOwnerBatchSize, condition)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if started && row.key <= last {
				return managementError(CodePersistence, nil)
			}
			_, sourcePresent := inventory.groups[row.source]
			_, targetPresent := inventory.permissions[row.target]
			if row.sourceValid && !sourcePresent || row.targetValid && !targetPresent {
				return managementError(CodePersistence, nil)
			}
			// Pin physical endpoints too: duplicate pairs and nullable links keep
			// their declared meaning, while orphan or late rewritten links fail.
			data, err := json.Marshal(struct {
				Key, Source, Target      int64
				SourceValid, TargetValid bool
			}{row.key, row.source, row.target, row.sourceValid, row.targetValid})
			if err != nil {
				return err
			}
			digest.part(data)
			if digest.err != nil {
				return digest.err
			}
			if row.sourceValid && row.targetValid {
				if sets[row.source] == nil {
					sets[row.source] = make(map[int64]bool)
				}
				sets[row.source][row.target] = true
				if len(sets[row.source]) > auth.MaximumPermissions {
					return invalidAccountCredential()
				}
			}
			last, started = row.key, true
		}
		if len(rows) < catalogOwnerBatchSize {
			for id, row := range inventory.groups {
				row.permissions = slices.Sorted(maps.Keys(sets[id]))
				inventory.groups[id] = row
			}
			return digest.err
		}
	}
}

func cloneCatalogMappings(input *CatalogMappings) *CatalogMappings {
	if input == nil {
		return nil
	}
	return &CatalogMappings{Groups: append([]domain.Mapping{}, input.Groups...), Permissions: append([]domain.Mapping{}, input.Permissions...)}
}

func normalizeCatalogMappings(input *CatalogMappings) (*CatalogMappings, error) {
	result := cloneCatalogMappings(input)
	if result == nil {
		return nil, nil
	}
	for _, rows := range []*[]domain.Mapping{&result.Groups, &result.Permissions} {
		if len(*rows) > MaximumTransitionCatalogRows {
			return nil, managementError(CodeInvalidInput, nil)
		}
		slices.SortFunc(*rows, func(a, b domain.Mapping) int { return compareInt64(a.SourceID, b.SourceID) })
		targets := make(map[int64]bool, len(*rows))
		for index, pair := range *rows {
			if pair.SourceRevision <= 0 || pair.TargetRevision <= 0 || index > 0 && (*rows)[index-1].SourceID == pair.SourceID || targets[pair.TargetID] {
				return nil, managementError(CodeInvalidInput, nil)
			}
			targets[pair.TargetID] = true
		}
	}
	return result, nil
}

func equalCatalogMappings(a, b *CatalogMappings) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return slices.Equal(a.Groups, b.Groups) && slices.Equal(a.Permissions, b.Permissions)
}

func (owner *Transition[S, T]) prepareCatalogMappings(input []CatalogMappings) (*CatalogMappings, error) {
	if len(input) > 1 {
		return nil, managementError(CodeInvalidInput, nil)
	}
	if len(input) == 0 {
		if owner.old.state.credentials.state.binding.state.role.Catalog != owner.next.state.credentials.state.binding.state.role.Catalog {
			return nil, managementError(CodeInvalidInput, nil)
		}
		return nil, nil
	}
	if len(input[0].Groups) > owner.config.MaxCatalogRows || len(input[0].Permissions) > owner.config.MaxCatalogRows {
		return nil, managementError(CodeInvalidInput, nil)
	}
	return normalizeCatalogMappings(&input[0])
}

func transitionCatalogPairs(before, after map[int64]transitionCatalogRow, pairs []domain.Mapping, sameModel bool) (map[int64]int64, error) {
	if len(before) != len(pairs) || len(after) != len(pairs) {
		return nil, managementError(CodeConflict, nil)
	}
	result := make(map[int64]int64, len(pairs))
	for _, pair := range pairs {
		left, oldPresent := before[pair.SourceID]
		right, newPresent := after[pair.TargetID]
		if !oldPresent || !newPresent || left.revision != pair.SourceRevision || right.revision != pair.TargetRevision || left.code != right.code || sameModel && pair.SourceID != pair.TargetID {
			return nil, managementError(CodeConflict, nil)
		}
		result[pair.SourceID] = pair.TargetID
	}
	return result, nil
}

func remapTransitionKeys(input []int64, targets map[int64]int64) ([]int64, error) {
	if targets == nil {
		return slices.Clone(input), nil
	}
	result := make([]int64, len(input))
	for i, key := range input {
		var present bool
		result[i], present = targets[key]
		if !present {
			return nil, managementError(CodeConflict, nil)
		}
	}
	slices.Sort(result)
	return result, nil
}

func (observed transitionCatalogObservation) memberships(input accountMemberships) (accountMemberships, error) {
	groups, err := remapTransitionKeys(input.groups, observed.groups)
	if err != nil {
		return accountMemberships{}, err
	}
	direct, err := remapTransitionKeys(input.direct, observed.direct)
	if err != nil {
		return accountMemberships{}, err
	}
	return accountMemberships{groups: groups, direct: direct, grants: slices.Clone(input.grants)}, nil
}

func (owner *Transition[S, T]) observeCatalog(ctx context.Context, scope db.Queryer, operation string, mapping *CatalogMappings) (transitionCatalogObservation, error) {
	var zero transitionCatalogObservation
	old, next := owner.old.state.credentials, owner.next.state.credentials
	left, right := old.state.binding.state.role.Catalog, next.state.binding.state.role.Catalog
	if mapping == nil && left != right {
		return zero, managementError(CodeInvalidInput, nil)
	}
	budget := &transitionDigestWriter{hash: sha256.New()}
	budget.part([]byte("godj.account-transition-catalog.v1"))
	budget.part([]byte(operation))
	for _, role := range []ir.AccountCatalog{left, right} {
		encoded, err := json.Marshal(role)
		if err != nil {
			return zero, err
		}
		budget.part(encoded)
	}
	before, err := old.state.relations.catalog(ctx, scope, owner.config.MaxCatalogRows, budget)
	if err != nil {
		return zero, err
	}
	after := before
	if left != right {
		after, err = next.state.relations.catalog(ctx, scope, owner.config.MaxCatalogRows, budget)
		if err != nil {
			return zero, err
		}
	}
	result := transitionCatalogObservation{operation: operation, mapping: cloneCatalogMappings(mapping)}
	if mapping == nil {
		// Shared catalog identities still have a complete inventory. Retain
		// explicit self maps so host catalog references cannot invent a key
		// merely because no separate catalog mapping was required.
		result.groups = make(map[int64]int64, len(before.groups))
		for key := range before.groups {
			result.groups[key] = key
		}
		result.direct = make(map[int64]int64, len(before.permissions))
		for key := range before.permissions {
			result.direct[key] = key
		}
	} else {
		result.groups, err = transitionCatalogPairs(before.groups, after.groups, mapping.Groups, left.Group == right.Group)
		if err != nil {
			return zero, err
		}
		result.direct, err = transitionCatalogPairs(before.permissions, after.permissions, mapping.Permissions, left.Permission == right.Permission)
		if err != nil {
			return zero, err
		}
		for source, target := range result.groups {
			keys, err := remapTransitionKeys(before.groups[source].permissions, result.direct)
			if err != nil || !slices.Equal(keys, after.groups[target].permissions) {
				return zero, managementError(CodeConflict, err)
			}
		}
	}
	result.digest, result.bytes = hex.EncodeToString(budget.hash.Sum(nil)), budget.size
	return result, budget.err
}

func appendTransitionCatalogDigest(parent *transitionDigestWriter, observed transitionCatalogObservation) error {
	if observed.bytes > MaximumTransitionStateBytes-parent.size {
		return managementError(CodeInvalidInput, nil)
	}
	parent.size += observed.bytes
	parent.part([]byte(observed.digest))
	return parent.err
}
