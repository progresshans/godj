package model

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type transitionRoleLinkInventory struct {
	membership deletionMembership
	rows       []deletionLink
}

func readTransitionRoleLinks(ctx context.Context, scope db.Queryer, membership deletionMembership, digest *transitionDigestWriter) ([]deletionLink, error) {
	digest.part([]byte(membership.field))
	var result []deletionLink
	var last int64
	started := false
	for {
		condition := query.NewCondition(membership.key, query.LookupIsNull, query.Boolean(false))
		if started {
			condition = query.NewCondition(membership.key, query.LookupGreaterThan, query.Integer(last))
		}
		rows, err := membership.read(ctx, scope, catalogOwnerBatchSize, condition)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if started && row.key <= last {
				return nil, managementError(CodePersistence, nil)
			}
			data, err := json.Marshal(struct {
				Key, Source, Target      int64
				SourceValid, TargetValid bool
			}{row.key, row.source, row.target, row.sourceValid, row.targetValid})
			if err != nil {
				return nil, err
			}
			digest.part(data)
			if digest.err != nil {
				return nil, digest.err
			}
			result = append(result, row)
			last, started = row.key, true
		}
		if len(rows) < catalogOwnerBatchSize {
			digest.part([]byte(strconv.Itoa(len(result))))
			return result, digest.err
		}
	}
}

func (owner *Transition[S, T]) observeCatalogAccountLinks(ctx context.Context, scope db.Queryer, digest *transitionDigestWriter, accounts map[int64]transitionRow[T], catalog transitionCatalogInventory) ([]transitionRoleLinkInventory, error) {
	role := owner.next.state.credentials.state.binding.state.role
	var result []transitionRoleLinkInventory
	for _, membership := range owner.nextMemberships {
		if membership.owner != role.Model || membership.field != role.Groups && membership.field != role.Permissions {
			continue
		}
		rows, err := readTransitionRoleLinks(ctx, scope, membership, digest)
		if err != nil {
			return nil, err
		}
		targets := catalog.groups
		if membership.field == role.Permissions {
			targets = catalog.permissions
		}
		for _, row := range rows {
			_, accountPresent := accounts[row.source]
			_, targetPresent := targets[row.target]
			if row.sourceValid && !accountPresent || row.targetValid && !targetPresent {
				return nil, managementError(CodeConflict, nil)
			}
		}
		result = append(result, transitionRoleLinkInventory{membership: membership, rows: rows})
	}
	if len(result) != 2 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return result, nil
}

func (owner *Transition[S, T]) verifyCatalogAccountLinks(ctx context.Context, scope db.Queryer, current catalogTransitionObservation[S, T], unchanged bool) error {
	role := owner.next.state.credentials.state.binding.state.role
	digest := &transitionDigestWriter{hash: sha256.New()}
	for _, inventory := range current.roleLinks {
		actual, err := readTransitionRoleLinks(ctx, scope, inventory.membership, digest)
		if err != nil {
			return err
		}
		if unchanged {
			if !slices.Equal(actual, inventory.rows) {
				return managementError(CodeConflict, nil)
			}
			continue
		}
		retained := make(map[int64]deletionLink, len(inventory.rows))
		existing := make(map[transitionCatalogPair]bool)
		for _, row := range inventory.rows {
			retained[row.key] = row
			if row.sourceValid && row.targetValid {
				existing[transitionCatalogPair{row.source, row.target}] = true
			}
		}
		added := make(map[transitionCatalogPair]bool)
		for _, account := range current.pairs {
			keys := account.next.memberships.groups
			if inventory.membership.field == role.Permissions {
				keys = account.next.memberships.direct
			}
			for _, key := range keys {
				pair := transitionCatalogPair{account.next.id, key}
				if !existing[pair] {
					added[pair] = true
				}
			}
		}
		for _, row := range actual {
			if before, present := retained[row.key]; present {
				if before != row {
					return managementError(CodeConflict, nil)
				}
				delete(retained, row.key)
				continue
			}
			pair := transitionCatalogPair{row.source, row.target}
			if !row.sourceValid || !row.targetValid || !added[pair] {
				return managementError(CodeConflict, nil)
			}
			delete(added, pair)
		}
		if len(retained) != 0 || len(added) != 0 {
			return managementError(CodeConflict, nil)
		}
	}
	return checkTransitionReadbackBudget(current.transitionObservation, digest.size)
}

func (owner *Transition[S, T]) verifyCatalogAccountBaseline(ctx context.Context, scope db.Queryer, current catalogTransitionObservation[S, T], creating bool) error {
	before, err := readTransitionRows(ctx, scope, owner.old.state.credentials, owner.oldMemberships, owner.config.MaxAccounts, current.ownership.Generation())
	if err != nil {
		return err
	}
	after, err := readTransitionRows(ctx, scope, owner.next.state.credentials, owner.nextMemberships, owner.config.MaxAccounts, current.ownership.Generation())
	if err != nil {
		return err
	}
	if len(before) != len(current.pairs) || creating && len(after) != 0 || !creating && len(after) != len(current.originalTargets) {
		return managementError(CodeConflict, nil)
	}
	for i, pair := range current.pairs {
		if !sameTransitionRow(before[pair.old.id], pair.old) {
			return managementError(CodeConflict, nil)
		}
		if !creating {
			expected := current.originalTargets[i]
			// A shared Group's new grants can change target effective authority
			// before its account revision is advanced. No account row or physical
			// assignment may change during the catalog's own writes.
			expected.memberships.grants, err = catalogMembershipGrants(current.catalog.after, expected.memberships)
			if err != nil {
				return err
			}
			if !sameTransitionRow(after[expected.id], expected) {
				return managementError(CodeConflict, nil)
			}
		}
	}
	return owner.verifyCatalogAccountLinks(ctx, scope, current, true)
}

func catalogMembershipGrants(catalog transitionCatalogInventory, membership accountMemberships) ([]auth.Permission, error) {
	keys := slices.Clone(membership.direct)
	for _, id := range membership.groups {
		group, present := catalog.groups[id]
		if !present {
			return nil, managementError(CodeConflict, nil)
		}
		keys = append(keys, group.permissions...)
	}
	grants := make(map[auth.Permission]bool)
	for _, key := range keys {
		permission, present := catalog.permissions[key]
		if !present {
			return nil, managementError(CodeConflict, nil)
		}
		grants[permission.code] = true
		if len(grants) > auth.MaximumPermissions {
			return nil, invalidAccountCredential()
		}
	}
	return slices.Sorted(maps.Keys(grants)), nil
}
