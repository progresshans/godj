package model

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// CatalogRecord is selected data and observed operation metadata, never an
// authorization or revision lock. The original model row is not retained.
type CatalogRecord[M any] struct{ state *catalogRecordState[M] }
type catalogRecordState[M any] struct {
	view         orm.ModelView[M]
	id, revision int64
}

func (CatalogRecord[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CatalogRecord{redacted}"))
}
func (record CatalogRecord[M]) ID() int64 {
	if record.state == nil {
		return 0
	}
	return record.state.id
}
func (record CatalogRecord[M]) Revision() int64 {
	if record.state == nil {
		return 0
	}
	return record.state.revision
}
func (record CatalogRecord[M]) View() orm.ModelView[M] {
	if record.state == nil {
		return orm.ModelView[M]{}
	}
	return record.state.view
}

type CatalogPage[M any] struct {
	Items []CatalogRecord[M]
	Total int64
}

func (resource CatalogResource[M]) Get(ctx context.Context, actor auth.Principal, id int64) (CatalogRecord[M], error) {
	return catalogSnapshot(ctx, resource, actor, func(work context.Context, scope db.Queryer, _ auth.CredentialDomain) (CatalogRecord[M], bool, error) {
		row, present, err := resource.state.resource.Get(work, scope, id)
		if err != nil || !present {
			return CatalogRecord[M]{}, present, err
		}
		value, err := resource.state.record(work, scope, row, true)
		return value, true, err
	})
}

// List owns one snapshot for current authority, total and ordered scalar page.
// It checks scalar integrity without per-row membership loads; detail reads
// own the complete current collection, including when output omits it.
func (resource CatalogResource[M]) List(ctx context.Context, actor auth.Principal, offset, limit int) (CatalogPage[M], error) {
	if offset < 0 || limit < 1 || limit > MaximumCatalogPageSize {
		return CatalogPage[M]{}, managementError(CodeInvalidInput, nil)
	}
	return catalogSnapshot(ctx, resource, actor, func(work context.Context, scope db.Queryer, _ auth.CredentialDomain) (CatalogPage[M], bool, error) {
		page, err := resource.state.resource.List(work, scope, crud.ListOptions[M]{Offset: offset, Limit: limit})
		if err != nil {
			return CatalogPage[M]{}, false, err
		}
		expected := page.Total - int64(offset)
		if expected < 0 {
			expected = 0
		}
		if expected > int64(limit) {
			expected = int64(limit)
		}
		if page.Total < 0 || int64(len(page.Items)) != expected {
			return CatalogPage[M]{}, false, managementError(CodePersistence, nil)
		}
		result := CatalogPage[M]{Items: make([]CatalogRecord[M], len(page.Items)), Total: page.Total}
		for index, row := range page.Items {
			result.Items[index], err = resource.state.record(work, scope, row, false)
			if err != nil {
				return CatalogPage[M]{}, false, err
			}
			if index > 0 && result.Items[index-1].ID() >= result.Items[index].ID() {
				return CatalogPage[M]{}, false, managementError(CodePersistence, nil)
			}
		}
		return result, true, nil
	})
}

// History is limited to the current account domain and an existing valid
// target. An old domain's same model/key never becomes current history.
func (resource CatalogResource[M]) History(ctx context.Context, actor auth.Principal, id int64, limit int) ([]accountaudit.Entry, error) {
	if limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, managementError(CodeInvalidInput, nil)
	}
	return catalogSnapshot(ctx, resource, actor, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) ([]accountaudit.Entry, bool, error) {
		row, present, err := resource.state.resource.Get(work, scope, id)
		if err != nil || !present {
			return nil, present, err
		}
		if _, err := resource.state.record(work, scope, row, true); err != nil {
			return nil, false, err
		}
		entries, err := accountaudit.HistoryIn(work, scope, namespace, resource.state.modelName(), id, limit)
		return entries, true, err
	})
}

func (state *catalogModelState[M]) modelName() string {
	return state.identity.AppLabel + "." + state.identity.ModelName
}

func (state *catalogModelState[M]) record(ctx context.Context, scope db.Queryer, row M, detail bool) (CatalogRecord[M], error) {
	_, id, revision, err := state.inspect(row, true)
	if err != nil {
		return CatalogRecord[M]{}, err
	}
	view, err := state.resource.Manager().View(row, state.listFields...)
	if err != nil {
		return CatalogRecord[M]{}, err
	}
	if detail && state.readKeys != nil {
		keys, err := state.readKeys(ctx, scope, id)
		if err != nil {
			return CatalogRecord[M]{}, err
		}
		if state.readCollection {
			view, err = view.WithCollections(map[string][]int64{state.collection: keys})
			if err != nil {
				return CatalogRecord[M]{}, err
			}
		}
	}
	return CatalogRecord[M]{state: &catalogRecordState[M]{view: view, id: id, revision: revision}}, nil
}

func (state *catalogModelState[M]) inspect(row M, stored bool) (map[string]query.Value, int64, int64, error) {
	values, err := state.resource.Manager().ModelValues(row)
	if err != nil {
		return nil, 0, 0, err
	}
	id, present := values[state.keyName].Integer()
	revision, validRevision := values[state.revision].Integer()
	if stored && (!present || !validRevision || revision <= 0) {
		return nil, 0, 0, managementError(CodePersistence, nil)
	}
	if err := state.checkScalars(values); err != nil {
		if stored {
			return nil, 0, 0, managementError(CodePersistence, err)
		}
		return nil, 0, 0, err
	}
	return values, id, revision, nil
}

func (state *catalogModelState[M]) checkScalars(values map[string]query.Value) error {
	for _, field := range state.model.Fields {
		value, present := values[field.Name]
		if !present {
			continue
		}
		if text, textual := value.String(); textual {
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
				return profileInputError(field.Name, "invalid")
			}
			if field.MaxLength > 0 && utf8.RuneCountInString(text) > field.MaxLength {
				return profileInputError(field.Name, "max_length")
			}
		}
		if field.Name == state.name {
			if text, valid := value.String(); !valid || text == "" {
				return profileInputError(field.Name, "required")
			}
		}
		if field.Name == state.code && state.code != "" {
			text, valid := value.String()
			if _, err := auth.NewPermission(text); !valid || err != nil {
				return profileInputError(field.Name, "invalid")
			}
		}
	}
	return nil
}

func (resource CatalogResource[M]) validCall(ctx context.Context, actor auth.Principal) error {
	if resource.state == nil || resource.state.shared == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return managementError(CodePermission, nil)
	}
	return nil
}

func catalogSnapshot[M, T any](ctx context.Context, resource CatalogResource[M], actor auth.Principal, read func(context.Context, db.Queryer, auth.CredentialDomain) (T, bool, error)) (T, error) {
	var zero T
	if err := resource.validCall(ctx, actor); err != nil {
		return zero, err
	}
	state := resource.state
	return managementSnapshot(ctx, state.shared.reader, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
		current, allowed, err := state.shared.authorize(work, scope, actor, state.permissions.view)
		if err == nil && !allowed {
			current, allowed, err = state.shared.authorize(work, scope, actor, state.permissions.change)
		}
		return current, allowed, err
	}, read)
}
