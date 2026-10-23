package model

import (
	"context"
	"fmt"
	"slices"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

const MaximumProfilePageSize = 100

// ProfileReadConfig explicitly selects readable scalar fields. Principal
// identity and encoded passwords cannot be selected. Server-owned timestamps,
// revision, role flags or the row key are not exposed automatically: they may
// be selected deliberately under this read policy and remain non-writable in
// ProfileEditor. File and collection representations have separate policies.
//
// Reads require ViewPermission or, when configured, ChangePermission, together
// with all AdditionalPermissions. Only a definite denial permits the change
// alternative; a current-authority or overlay error stops the operation.
type ProfileReadConfig struct {
	Fields                []string
	ViewPermission        auth.Permission
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
}

type ProfileReader[M any] struct{ state *profileReadState[M] }
type profileReadState[M any] struct {
	store        *CredentialStore[M]
	overlay      auth.Authorizer
	fields       []string
	view, change []auth.Permission
}

func (ProfileReader[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileReader{redacted}"))
}

func NewProfileReader[M any](store *CredentialStore[M], overlay auth.Authorizer, config ProfileReadConfig) (*ProfileReader[M], error) {
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if nilModelValue(overlay) || len(config.Fields) == 0 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	view, err := auth.RequiredPermissions(config.ViewPermission, config.AdditionalPermissions...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	var change []auth.Permission
	if config.ChangePermission != "" {
		if config.ChangePermission == config.ViewPermission {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		change, err = auth.RequiredPermissions(config.ChangePermission, config.AdditionalPermissions...)
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	metadata, err := store.state.binding.state.objects.Metadata()
	if err != nil || len(config.Fields) > len(metadata.Fields) {
		return nil, managementError(CodeInvalidConfig, err)
	}
	role := store.state.binding.state.role
	selected := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		field, found := namedField(metadata, name)
		if !found || selected[name] || name == role.Password || name == role.PrincipalID || field.Kind.IsFile() {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		selected[name] = true
	}
	return &ProfileReader[M]{state: &profileReadState[M]{store: store, overlay: overlay,
		fields: slices.Clone(config.Fields), view: view, change: change}}, nil
}

func (reader *ProfileReader[M]) Fields() []string {
	if reader == nil || reader.state == nil {
		return nil
	}
	return slices.Clone(reader.state.fields)
}

// Profile contains only explicitly selected view data and operation metadata.
// No original account row, credential or effective permission set is retained.
// It is an observation, not authority for another read or write.
type Profile[M any] struct{ state *profileData[M] }
type profileData[M any] struct {
	view     orm.ModelView[M]
	id       int64
	revision int64
}

func (Profile[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Profile{redacted}"))
}
func (profile Profile[M]) ID() int64 {
	if profile.state == nil {
		return 0
	}
	return profile.state.id
}
func (profile Profile[M]) Revision() int64 {
	if profile.state == nil {
		return 0
	}
	return profile.state.revision
}
func (profile Profile[M]) View() orm.ModelView[M] {
	if profile.state == nil {
		return orm.ModelView[M]{}
	}
	return profile.state.view
}

type ProfilePage[M any] struct {
	Items []Profile[M]
	Total int64
}

// Get owns one stable snapshot for current authorization, target integrity and
// selected profile data. Missing/denied results are published only after read
// cleanup succeeds. Every integer object key, including zero, is meaningful.
func (reader *ProfileReader[M]) Get(ctx context.Context, actor auth.Principal, id int64) (Profile[M], error) {
	return profileSnapshot(ctx, reader, actor, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) (Profile[M], bool, error) {
		row, present, err := reader.target(work, scope, id)
		if err != nil || !present {
			return Profile[M]{}, present, err
		}
		permissions, err := reader.state.store.state.permissions(work, scope, id)
		if err != nil {
			return Profile[M]{}, false, err
		}
		profile, err := reader.project(namespace, row, permissions)
		return profile, true, err
	})
}

// List returns a bounded page in stored primary-key order. Authorization,
// count and rows share one snapshot, so concurrent commits cannot mix totals
// with a different page. It validates scalar account state without loading
// target grants per row; effective permissions are never claimed by this list.
func (reader *ProfileReader[M]) List(ctx context.Context, actor auth.Principal, offset, limit int) (ProfilePage[M], error) {
	if offset < 0 || limit < 1 || limit > MaximumProfilePageSize {
		return ProfilePage[M]{}, managementError(CodeInvalidInput, nil)
	}
	return profileSnapshot(ctx, reader, actor, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) (ProfilePage[M], bool, error) {
		store := reader.state.store.state
		objects := store.binding.state.objects.Using(scope).OrderBy(store.key.Asc())
		total, err := objects.Count(work)
		if err != nil || total < 0 {
			if err == nil {
				err = invalidAccountCredential()
			}
			return ProfilePage[M]{}, false, err
		}
		page, err := objects.Offset(offset)
		if err != nil {
			return ProfilePage[M]{}, false, err
		}
		page, err = page.Limit(limit)
		if err != nil {
			return ProfilePage[M]{}, false, err
		}
		rows, err := page.All(work)
		if err != nil {
			return ProfilePage[M]{}, false, err
		}
		expected := min(int64(limit), max(int64(0), total-int64(offset)))
		if int64(len(rows)) != expected {
			return ProfilePage[M]{}, false, invalidAccountCredential()
		}
		result := ProfilePage[M]{Items: make([]Profile[M], len(rows)), Total: total}
		for index, row := range rows {
			// Nil grants here validate only scalar credential representation; this
			// temporary credential is discarded and never used as authority.
			profile, err := reader.project(namespace, row, nil)
			if err != nil || index > 0 && profile.ID() <= result.Items[index-1].ID() {
				if err == nil {
					err = invalidAccountCredential()
				}
				return ProfilePage[M]{}, false, err
			}
			result.Items[index] = profile
		}
		return result, true, nil
	})
}

// History reads only the CURRENT account domain after the same scoped view
// admission and target-integrity check as Get. An old generation's coincident
// model/object ID never supplies current history. Explicit historical-domain
// inspection belongs to a separately authorized account-transition owner.
func (reader *ProfileReader[M]) History(ctx context.Context, actor auth.Principal, id int64, limit int) ([]accountaudit.Entry, error) {
	if limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, managementError(CodeInvalidInput, nil)
	}
	return profileSnapshot(ctx, reader, actor, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) ([]accountaudit.Entry, bool, error) {
		row, present, err := reader.target(work, scope, id)
		if err != nil || !present {
			return nil, present, err
		}
		permissions, err := reader.state.store.state.permissions(work, scope, id)
		if err != nil {
			return nil, false, err
		}
		binding := reader.state.store.state.binding
		if _, err := binding.prepareCredentialForDomain(namespace, row, permissions); err != nil {
			return nil, false, err
		}
		model := binding.state.role.Model
		entries, err := accountaudit.HistoryIn(work, scope, namespace, model.AppLabel+"."+model.ModelName, id, limit)
		return entries, true, err
	})
}

func (reader *ProfileReader[M]) target(ctx context.Context, scope db.Queryer, id int64) (M, bool, error) {
	store := reader.state.store.state
	row, present, err := store.binding.state.objects.Using(scope).Filter(store.key.Exact(id)).OrderBy(store.key.Asc()).First(ctx)
	if err != nil || !present {
		return row, present, err
	}
	key, assigned, err := store.binding.state.objects.PrimaryKey(row)
	actual, integer := key.Integer()
	if err != nil || !assigned || !integer || actual != id {
		var zero M
		return zero, false, invalidAccountCredential()
	}
	return row, true, nil
}

func (reader *ProfileReader[M]) project(namespace auth.CredentialDomain, row M, permissions []auth.Permission) (Profile[M], error) {
	binding := reader.state.store.state.binding
	if _, err := binding.prepareCredentialForDomain(namespace, row, permissions); err != nil {
		return Profile[M]{}, err
	}
	values, err := binding.state.objects.ModelValues(row)
	if err != nil {
		return Profile[M]{}, err
	}
	revision, ok := values[binding.state.role.Revision].Integer()
	if !ok || revision <= 0 {
		return Profile[M]{}, invalidAccountCredential()
	}
	view, err := binding.state.objects.View(row, reader.state.fields...)
	if err != nil {
		return Profile[M]{}, err
	}
	key, present := view.PrimaryKey()
	id, integer := key.Integer()
	if !present || !integer {
		return Profile[M]{}, invalidAccountCredential()
	}
	return Profile[M]{state: &profileData[M]{view: view, id: id, revision: revision}}, nil
}

type profileReadResult[T any] struct {
	value   T
	allowed bool
	present bool
}

func profileSnapshot[M, T any](ctx context.Context, reader *ProfileReader[M], actor auth.Principal, read func(context.Context, db.Queryer, auth.CredentialDomain) (T, bool, error)) (T, error) {
	var zero T
	if reader == nil || reader.state == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return zero, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return zero, managementError(CodePermission, nil)
	}
	state := reader.state
	result, err := db.ReadSnapshotResult(ctx, state.store.state.backend, func(work context.Context, scope db.Queryer) (profileReadResult[T], error) {
		current, allowed, err := state.store.AuthorizeIn(work, scope, actor, state.overlay, state.view[0], state.view[1:]...)
		if err == nil && !allowed && len(state.change) != 0 {
			current, allowed, err = state.store.AuthorizeIn(work, scope, actor, state.overlay, state.change[0], state.change[1:]...)
		}
		if err != nil || !allowed {
			return profileReadResult[T]{}, err
		}
		namespace, _ := current.CredentialDomain()
		value, present, err := read(work, scope, namespace)
		return profileReadResult[T]{value: value, allowed: true, present: present}, err
	})
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	if !result.allowed {
		return zero, managementError(CodePermission, nil)
	}
	if !result.present {
		return zero, managementError(CodeNotFound, nil)
	}
	return result.value, nil
}
