package systemstate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/systemstatehandoff"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
)

type handoffObservation struct {
	origin   domain.LegacyIdentityOrigin
	ownerID  int64
	accounts []domain.Mapping
	catalog  domain.CatalogMappings
}

func sameHandoffObservation(value handoffObservation, manifest IdentityHandoffManifest) bool {
	return value.origin == manifest.Origin && slices.Equal(value.accounts, manifest.Accounts) && slices.Equal(value.catalog.Groups, manifest.Catalog.Groups) && slices.Equal(value.catalog.Permissions, manifest.Catalog.Permissions)
}

type handoffDigest struct {
	hash   hash.Hash
	budget *int64
	err    error
}

func newHandoffDigest(kind string, budget *int64) *handoffDigest {
	writer := &handoffDigest{hash: sha256.New(), budget: budget}
	writer.part([]byte("godj.systemstate.account-handoff." + kind + ".v1\x00"))
	return writer
}
func (writer *handoffDigest) part(value []byte) {
	if writer.err != nil {
		return
	}
	if writer.budget != nil {
		if int64(len(value)) > int64(accountmodel.MaximumTransitionStateBytes)-*writer.budget-8 {
			writer.err = handoffError(CodeCardinality, nil)
			return
		}
		*writer.budget += int64(len(value)) + 8
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.hash.Write(length[:])
	_, _ = writer.hash.Write(value)
}
func (writer *handoffDigest) value(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return handoffError(CodeCorruptState, err)
	}
	writer.part(encoded)
	return writer.err
}
func (writer *handoffDigest) sum() string { return hex.EncodeToString(writer.hash.Sum(nil)) }

// Archives are streamed, not retained or copied into a new session namespace.
// Their original capacity and per-row byte/encoding checks remain authoritative;
// the typed identity preparation budget does not truncate a large valid archive.
func (owner *IdentityHandoff) observeArchive(ctx context.Context, scope db.Queryer, retired string) (domain.LegacyIdentityOrigin, int64, error) {
	var zero domain.LegacyIdentityOrigin
	if retired != "" {
		if err := systemstatehandoff.RequireRetired(ctx, scope, retired); err != nil {
			return zero, 0, err
		}
	}
	receipts, err := readIdentityTransitions(ctx, scope)
	if err != nil {
		return zero, 0, err
	}
	credentials, err := readCredentialRows(ctx, scope)
	if err != nil {
		return zero, 0, err
	}
	if retired != "" && len(credentials) == 1 {
		// RequireRetired checked the exact new marker first. Reconstruct ONLY
		// its two intentionally replaced fields for the frozen source digest.
		credentials[0].encodedPassword, credentials[0].definitionDigest = transferredPassword, transferredCredentialDigest
	}
	receipt, err := validateIdentityOwnership(receipts, credentials)
	if err != nil {
		return zero, 0, err
	}
	credential := credentials[0]
	if auth.ValidateUsername(credential.username) != nil || !utf8.ValidString(credential.username) || strings.ContainsRune(credential.username, 0) || utf8.RuneCountInString(credential.username) > credentialUsernameMaxLength || len(credential.username) > auth.MaximumUsernameBytes {
		return zero, 0, handoffError(CodeCorruptState, nil)
	}
	state := receipt.value()
	ownerDigest := newHandoffDigest("owner", nil)
	if err := ownerDigest.value([]any{credential.id, credential.principalID, credential.username, credential.encodedPassword, credential.active, credential.permissions, credential.definitionDigest,
		state.id, state.kind, state.sourceID, state.principalID, state.userID, state.staff, state.superuser, state.at.UTC(), state.fingerprint}); err != nil {
		return zero, 0, err
	}
	count, err := scanSessionInventory(ctx, scope, owner.config.MaxSessions+1)
	if err != nil {
		return zero, 0, err
	}
	if count > owner.config.MaxSessions {
		return zero, 0, handoffError(CodeCardinality, nil)
	}
	sessionsDigest := newHandoffDigest("sessions", nil)
	actual, err := scanSessionPayloads(ctx, scope, owner.config.SessionLimits, owner.config.MaxSessions+1, func(row persistedSessionRow, _ sessions.Record) error {
		return sessionsDigest.value([]any{row.id, row.digest, row.payload})
	})
	if err != nil {
		return zero, 0, err
	}
	if actual != count {
		return zero, 0, handoffError(CodeHandoffConflict, nil)
	}
	auditDigest := newHandoffDigest("audit", nil)
	audits, err := scanAuditInventory(ctx, scope, owner.config.AuditCapacity, func(row persistedAuditRow) error {
		return auditDigest.value([]any{row.id, row.actorID, row.model, row.objectID, row.action, row.changedFields, row.displayLabel})
	})
	if err != nil {
		return zero, 0, err
	}
	return domain.LegacyIdentityOrigin{SchemaSHA256: owner.schema, ProjectSHA256: owner.source, OwnerSHA256: ownerDigest.sum(), SessionsSHA256: sessionsDigest.sum(), SessionRecords: count, AuditSHA256: auditDigest.sum(), AuditEvents: audits}, credential.id, nil
}

// handoffRows streams exact typed rows. The callback only inspects detached
// values; it must not re-enter the reader while its rowset is live.
func handoffRows[M any](ctx context.Context, scope db.Queryer, objects orm.Manager[M], limit int, writer *handoffDigest, visit func(M) error) error {
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	key, err := objects.PrimaryKeyField()
	if err != nil {
		return err
	}
	fields := make([]serializers.ModelField, len(metadata.Fields))
	for index, field := range metadata.Fields {
		fields[index] = serializers.ModelField{Name: field.Name, ReadOnly: true}
		switch field.Kind {
		case ir.FieldChar, ir.FieldEmail, ir.FieldURL, ir.FieldSlug, ir.FieldFile, ir.FieldImage, ir.FieldText:
			fields[index].AllowEmpty = field.Blank
		}
	}
	spec, err := serializers.FromModel(metadata, fields...)
	if err != nil {
		return err
	}
	writer.part([]byte(metadata.DBTable))
	if writer.err != nil {
		return writer.err
	}
	selected, err := objects.Using(scope).OrderBy(orm.NewAutoField[M](key).Asc()).Limit(limit + 1)
	if err != nil {
		return err
	}
	count, previous := 0, int64(0)
	err = selected.Iterate(ctx, func(row M) error {
		value, present, err := objects.PrimaryKey(row)
		id, integer := value.Integer()
		if err != nil || !present || !integer || count > 0 && id <= previous || count >= limit {
			return handoffError(CodeCardinality, err)
		}
		previous, count = id, count+1
		values, err := objects.ModelValues(row)
		if err != nil || len(values) != len(metadata.Fields) {
			return handoffError(CodeCorruptState, err)
		}
		encoder, err := serializers.NewModelEncoder(spec, metadata, func(_ struct{}, field ir.Field) (query.Value, bool) {
			value, present := values[field.Name]
			return value, present
		})
		if err != nil {
			return err
		}
		if _, err := encoder.Encode(struct{}{}); err != nil {
			return err
		}
		// The fixed generated historical structs contain every scalar and no
		// password-redacting JSON override. Validate all IR text before JSON's
		// encoding so invalid UTF-8 cannot alias a replacement character.
		for _, value := range values {
			if text, isString := value.String(); isString && (!utf8.ValidString(text) || strings.ContainsRune(text, 0)) {
				return handoffError(CodeCorruptState, nil)
			}
		}
		if err := writer.value(row); err != nil {
			return err
		}
		return visit(row)
	})
	if err != nil {
		return err
	}
	return writer.err
}

func (owner *IdentityHandoff) observe(ctx context.Context, scope db.Queryer, actorID, retired string) (handoffObservation, error) {
	var zero handoffObservation
	// Resolve current authority before scanning private global inventories.
	// The complete snapshot below rechecks the same actor and every grant.
	if retired == "" {
		if _, err := inspectIdentityTransition(ctx, scope); err != nil {
			return zero, err
		}
	} else if err := systemstatehandoff.RequireRetired(ctx, scope, retired); err != nil {
		return zero, err
	}
	if err := owner.authorizeActor(ctx, scope, actorID); err != nil {
		return zero, err
	}
	origin, ownerID, err := owner.observeArchive(ctx, scope, retired)
	if err != nil {
		return zero, err
	}
	var budget int64
	accountsDigest, catalogDigest := newHandoffDigest("accounts", &budget), newHandoffDigest("catalog", &budget)
	users := make(map[int64]models.User)
	groups := make(map[int64]models.Group)
	permissions := make(map[int64]models.Permission)
	principals, logins, names, codes := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	observation := handoffObservation{origin: origin, ownerID: ownerID, accounts: []domain.Mapping{}, catalog: domain.CatalogMappings{Groups: []domain.Mapping{}, Permissions: []domain.Mapping{}}}
	if err := handoffRows(ctx, scope, models.UserObjects, owner.config.MaxAccounts, accountsDigest, func(row models.User) error {
		if row.Revision <= 0 || principals[row.PrincipalID] || logins[row.Username] {
			return handoffError(CodeCorruptState, nil)
		}
		principals[row.PrincipalID], logins[row.Username], users[row.ID] = true, true, row
		observation.accounts = append(observation.accounts, domain.Mapping{SourceID: row.ID, TargetID: row.ID, SourceRevision: row.Revision, TargetRevision: row.Revision})
		return nil
	}); err != nil {
		return zero, err
	}
	if len(users) == 0 {
		return zero, handoffError(CodeHandoffDenied, nil)
	}
	if err := handoffRows(ctx, scope, models.GroupObjects, owner.config.MaxCatalogRows, catalogDigest, func(row models.Group) error {
		if row.Revision <= 0 || names[row.Name] {
			return handoffError(CodeCorruptState, nil)
		}
		names[row.Name], groups[row.ID] = true, row
		observation.catalog.Groups = append(observation.catalog.Groups, domain.Mapping{SourceID: row.ID, TargetID: row.ID, SourceRevision: row.Revision, TargetRevision: row.Revision})
		return nil
	}); err != nil {
		return zero, err
	}
	if err := handoffRows(ctx, scope, models.PermissionObjects, owner.config.MaxCatalogRows, catalogDigest, func(row models.Permission) error {
		if _, err := auth.NewPermission(row.Code); err != nil {
			return err
		}
		if row.Revision <= 0 || codes[row.Code] {
			return handoffError(CodeCorruptState, nil)
		}
		codes[row.Code], permissions[row.ID] = true, row
		observation.catalog.Permissions = append(observation.catalog.Permissions, domain.Mapping{SourceID: row.ID, TargetID: row.ID, SourceRevision: row.Revision, TargetRevision: row.Revision})
		return nil
	}); err != nil {
		return zero, err
	}
	grants, direct, memberships := map[int64]map[int64]bool{}, map[int64]map[int64]bool{}, map[int64]map[int64]bool{}
	add := func(index map[int64]map[int64]bool, source, target int64, maximum int) error {
		if index[source] == nil {
			index[source] = make(map[int64]bool)
		}
		if index[source][target] || len(index[source]) >= maximum {
			return handoffError(CodeCardinality, nil)
		}
		index[source][target] = true
		return nil
	}
	// The per-source bounds and shared byte budget remain independent of the
	// native FK/UNIQUE constraints; orphan/duplicate links are never adopted.
	if err := handoffRows(ctx, scope, models.GroupPermissionsLinkObjects, owner.config.MaxCatalogRows*auth.MaximumPermissions, catalogDigest, func(row models.GroupPermissionsLink) error {
		if _, found := groups[row.SourceID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		if _, found := permissions[row.TargetID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		return add(grants, row.SourceID, row.TargetID, auth.MaximumPermissions)
	}); err != nil {
		return zero, err
	}
	if err := handoffRows(ctx, scope, models.UserGroupsLinkObjects, owner.config.MaxAccounts*accountmodel.MaximumAccountGroups, accountsDigest, func(row models.UserGroupsLink) error {
		if _, found := users[row.SourceID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		if _, found := groups[row.TargetID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		return add(memberships, row.SourceID, row.TargetID, accountmodel.MaximumAccountGroups)
	}); err != nil {
		return zero, err
	}
	if err := handoffRows(ctx, scope, models.UserPermissionsLinkObjects, owner.config.MaxAccounts*auth.MaximumPermissions, accountsDigest, func(row models.UserPermissionsLink) error {
		if _, found := users[row.SourceID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		if _, found := permissions[row.TargetID]; !found {
			return handoffError(CodeCorruptState, nil)
		}
		return add(direct, row.SourceID, row.TargetID, auth.MaximumPermissions)
	}); err != nil {
		return zero, err
	}
	var actor auth.Principal
	for _, id := range slices.Sorted(maps.Keys(users)) {
		row := users[id]
		effective := maps.Clone(direct[id])
		if effective == nil {
			effective = make(map[int64]bool)
		}
		for group := range memberships[id] {
			for permission := range grants[group] {
				effective[permission] = true
			}
		}
		if len(effective) > auth.MaximumPermissions {
			return zero, handoffError(CodeCardinality, nil)
		}
		values := make([]auth.Permission, 0, len(effective))
		for permission := range effective {
			values = append(values, auth.Permission(permissions[permission].Code))
		}
		slices.Sort(values)
		if err := owner.binding.ValidateCredentialSnapshot(row, values); err != nil {
			return zero, err
		}
		if row.PrincipalID == actorID {
			actor, err = auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser, Permissions: values})
			if err != nil {
				return zero, err
			}
		}
	}
	if err := owner.authorizePrincipal(ctx, actor); err != nil {
		return zero, err
	}
	observation.origin.AccountsSHA256, observation.origin.CatalogSHA256 = accountsDigest.sum(), catalogDigest.sum()
	return observation, nil
}

func (owner *IdentityHandoff) authorizeActor(ctx context.Context, scope db.Queryer, actorID string) error {
	selected, err := models.UserObjects.Using(scope).Filter(models.UserFields.PrincipalID.Exact(actorID)).OrderBy(models.UserFields.ID.Asc()).Limit(2)
	if err != nil {
		return err
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return handoffError(CodeHandoffDenied, nil)
	}
	row := rows[0]
	relations := owner.relations.IdentityPermission
	grants, err := models.PermissionObjects.Using(scope).Filter(orm.Or(relations.Users.ID.Exact(row.ID), relations.Groups.Users().ID.Exact(row.ID))).Distinct().OrderBy(models.PermissionFields.Code.Asc()).Limit(auth.MaximumPermissions + 1)
	if err != nil {
		return err
	}
	permissions, err := grants.All(ctx)
	if err != nil {
		return err
	}
	if len(permissions) > auth.MaximumPermissions {
		return handoffError(CodeCardinality, nil)
	}
	codes := make([]auth.Permission, len(permissions))
	for index, permission := range permissions {
		if permission.Revision <= 0 {
			return handoffError(CodeCorruptState, nil)
		}
		codes[index] = auth.Permission(permission.Code)
	}
	if err := owner.binding.ValidateCredentialSnapshot(row, codes); err != nil {
		return err
	}
	current, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser, Permissions: codes})
	if err != nil {
		return err
	}
	return owner.authorizePrincipal(ctx, current)
}

func (owner *IdentityHandoff) authorizePrincipal(ctx context.Context, actor auth.Principal) error {
	if !actor.Authenticated() {
		return handoffError(CodeHandoffDenied, nil)
	}
	for _, permission := range owner.required {
		if !actor.Has(permission) {
			return handoffError(CodeHandoffDenied, nil)
		}
		allowed, err := owner.config.Authorizer.Allowed(ctx, actor, permission)
		if err != nil {
			return err
		}
		if !allowed {
			return handoffError(CodeHandoffDenied, nil)
		}
	}
	return nil
}
