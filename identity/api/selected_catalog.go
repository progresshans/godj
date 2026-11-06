package identityapi

import (
	"context"
	"net/http"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func addSelectedCatalog[M any](builder *selectedBuilder, owner accountmodel.CatalogResource[M], name, title string) error {
	description, err := owner.Description()
	if err != nil {
		return err
	}
	objects := owner.Manager()
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	spec, err := serializers.FromModel(metadata, selectedModelFields(metadata, owner.WriteFields(), false)...)
	if err != nil {
		return err
	}
	body, err := selectedModelBody(objects, spec)
	if err != nil {
		return err
	}
	schema, err := body.Schema(serializers.ModeFull)
	if err != nil {
		return err
	}
	p := description.Permissions
	policies, err := selectedPolicies(p.View, p.Add, p.Change, p.Delete, p.Additional, false)
	if err != nil {
		return err
	}
	project := func(row accountmodel.CatalogRecord[M]) selectedRow[M] {
		return selectedRow[M]{ID: row.ID(), Revision: row.Revision(), Fields: row.View()}
	}
	return addSelectedResource(builder, selectedResource[M]{name: name, title: title, manager: objects,
		readFields: owner.ReadFields(), listFields: owner.ListFields(), policies: policies, createSchema: schema, edit: body,
		create: func(request *web.Request, actor auth.Principal) (web.Response, error) {
			input, response, handled, err := parseSelectedBody(request, body, serializers.ModeFull)
			if handled || err != nil {
				return response, err
			}
			result, err := owner.Create(request.Context(), actor, input.Fields)
			if err != nil {
				return selectedFailure(err)
			}
			return selectedMutationResponse(http.StatusCreated, result.ID, result.Revision)
		},
		get: func(ctx context.Context, actor auth.Principal, id int64) (selectedRow[M], error) {
			row, err := owner.Get(ctx, actor, id)
			return project(row), err
		},
		list: func(ctx context.Context, actor auth.Principal, offset, limit int) (selectedPage[M], error) {
			page, err := owner.List(ctx, actor, offset, limit)
			result := selectedPage[M]{Count: page.Total, Items: make([]selectedRow[M], len(page.Items))}
			for index, row := range page.Items {
				result.Items[index] = project(row)
			}
			return result, err
		},
		update: func(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (int64, int64, error) {
			result, err := owner.Update(ctx, actor, id, revision, input)
			return result.ID, result.Revision, err
		},
		remove: func(ctx context.Context, actor auth.Principal, id, revision int64) (int64, int64, error) {
			result, err := owner.Delete(ctx, actor, id, revision)
			return result.ID, result.Revision, err
		},
		history: owner.History,
	})
}
