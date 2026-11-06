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
	"github.com/progresshans/godj/schema/ir"
)

type transitionRoleLinkInventory struct {
	membership deletionMembership
	rows       []deletionLink
}

func readTransitionRoleLinks(ctx context.Context, scope db.Queryer, membership deletionMembership, digest *transitionDigestWriter) ([]deletionLink, error) {
	if err := writeTransitionRoleLinkHeader(digest, membership); err != nil {
		return nil, err
	}
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
			if err := writeTransitionRoleLink(digest, row); err != nil {
				return nil, err
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

func writeTransitionRoleLinkHeader(digest *transitionDigestWriter, membership deletionMembership) error {
	for _, value := range []string{membership.owner.AppLabel, membership.owner.ModelName, membership.field, membership.through.AppLabel, membership.through.ModelName, membership.key.Name(), membership.source.Name(), membership.target.Name()} {
		digest.part([]byte(value))
	}
	return digest.err
}

func writeTransitionRoleLink(digest *transitionDigestWriter, row deletionLink) error {
	data, err := json.Marshal(struct {
		Key, Source, Target      int64
		SourceValid, TargetValid bool
	}{row.key, row.source, row.target, row.sourceValid, row.targetValid})
	if err != nil {
		return err
	}
	digest.part(data)
	return digest.err
}

func writeTransitionRoleLinkInventory(digest *transitionDigestWriter, inventory transitionRoleLinkInventory) error {
	if err := writeTransitionRoleLinkHeader(digest, inventory.membership); err != nil {
		return err
	}
	for _, row := range inventory.rows {
		if err := writeTransitionRoleLink(digest, row); err != nil {
			return err
		}
	}
	digest.part([]byte(strconv.Itoa(len(inventory.rows))))
	return digest.err
}

// Account role and profile views from both namespaces pin the same physical
// storage independently, including rows whose endpoint in one view is NULL.
func (owner *Transition[S, T]) observeTransitionAccountLinks(ctx context.Context, scope db.Queryer, digest *transitionDigestWriter, before map[int64]transitionRow[S], after map[int64]transitionRow[T], catalog transitionCatalogObservation) ([]transitionRoleLinkInventory, error) {
	var result []transitionRoleLinkInventory
	collect := func(role ir.AccountRole, memberships []deletionMembership, exists func(int64) bool, inventory transitionCatalogInventory) error {
		critical := 0
		for _, membership := range memberships {
			if membership.owner != role.Model {
				continue
			}
			rows, err := readTransitionRoleLinks(ctx, scope, membership, digest)
			if err != nil {
				return err
			}
			var targets map[int64]transitionCatalogRow
			if membership.field == role.Groups {
				targets = inventory.groups
				critical++
			}
			if membership.field == role.Permissions {
				targets = inventory.permissions
				critical++
			}
			for _, row := range rows {
				if row.sourceValid && !exists(row.source) {
					return managementError(CodeConflict, nil)
				}
				if !membership.profile && row.targetValid {
					if _, present := targets[row.target]; !present {
						return managementError(CodeConflict, nil)
					}
				}
			}
			result = append(result, transitionRoleLinkInventory{membership: membership, rows: rows})
		}
		if critical != 2 {
			return managementError(CodeInvalidConfig, nil)
		}
		return nil
	}
	if err := collect(owner.old.state.credentials.state.binding.state.role, owner.oldMemberships, func(id int64) bool { _, ok := before[id]; return ok }, catalog.before); err != nil {
		return nil, err
	}
	if err := collect(owner.next.state.credentials.state.binding.state.role, owner.nextMemberships, func(id int64) bool { _, ok := after[id]; return ok }, catalog.after); err != nil {
		return nil, err
	}
	return result, nil
}

func verifyTransitionAccountLinks(ctx context.Context, scope db.Queryer, inventories []transitionRoleLinkInventory) error {
	digest := &transitionDigestWriter{hash: sha256.New()}
	for _, inventory := range inventories {
		rows, err := readTransitionRoleLinks(ctx, scope, inventory.membership, digest)
		if err != nil {
			return err
		}
		if !slices.Equal(rows, inventory.rows) {
			return managementError(CodeConflict, nil)
		}
	}
	return nil
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
	if len(before) != len(current.pairs) || creating && len(after) != len(current.pairs) || !creating && len(after) != len(current.originalTargets) {
		return managementError(CodeConflict, nil)
	}
	for i, pair := range current.pairs {
		if !sameTransitionRow(before[pair.old.id], pair.old) {
			return managementError(CodeConflict, nil)
		}
		if creating {
			// These identities have been allocated, while requested memberships
			// are still pending. Pin every scalar plus the independently projected
			// complete physical link inventory below, not the intended logical set.
			actual, present := after[pair.next.id]
			if !present || !maps.Equal(actual.values, pair.next.values) {
				return managementError(CodeConflict, nil)
			}
		} else {
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
	if err := verifyTransitionAccountLinks(ctx, scope, current.roleLinks); err != nil {
		return err
	}
	return current.inserted.verify(ctx)
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
