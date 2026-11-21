package identityapi

import (
	"context"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func addSelectedAccount[M any](builder *selectedBuilder, owner *accountmodel.AccountManagement[M]) error {
	description, err := owner.Description()
	if err != nil {
		return err
	}
	objects := owner.Manager()
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	reader, creator, editor := owner.Reader(), owner.Creator(), owner.Editor()
	createSpec, err := owner.InputSpec(admin.ActionAdd, selectedModelFields(metadata, creator.Fields(), false)...)
	if err != nil {
		return err
	}
	editSpec, err := owner.InputSpec(admin.ActionChange, selectedModelFields(metadata, editor.Fields(), false)...)
	if err != nil {
		return err
	}
	credential, err := selectedCredentialBody(owner.Passwords().MaxPasswordBytes())
	if err != nil {
		return err
	}
	create, err := selectedCreationBody(objects, createSpec, credential)
	if err != nil {
		return err
	}
	createSchema, err := create.Schema(serializers.ModeFull)
	if err != nil {
		return err
	}
	edit, err := selectedModelBody(objects, editSpec)
	if err != nil {
		return err
	}
	p := description.Permissions
	policies, err := selectedPolicies(p.View, p.Add, p.Change, p.Delete, p.Additional, true)
	if err != nil {
		return err
	}
	project := func(row accountmodel.Profile[M]) selectedRow[M] {
		return selectedRow[M]{ID: row.ID(), Revision: row.Revision(), Fields: row.View(), PasswordUsable: row.PasswordUsable()}
	}
	resource := selectedResource[M]{name: "accounts", title: "Account", manager: objects, account: true,
		readFields: reader.Fields(), listFields: reader.ListFields(), policies: policies, createSchema: createSchema, edit: edit,
		create: func(request *web.Request, actor auth.Principal) (web.Response, error) {
			input, response, handled, err := parseSelectedBody(request, create, serializers.ModeFull)
			if handled || err != nil {
				return response, err
			}
			password, present := input.Credential.Password.Get()
			if !present {
				return web.Response{}, selectedConfigError("credential", nil)
			}
			var prepared accountmodel.PreparedCreation[M]
			if password == nil {
				prepared, err = creator.PrepareUnusable(request.Context(), actor, input.Fields)
			} else {
				prepared, err = creator.Prepare(request.Context(), actor, input.Fields, *password)
			}
			if err != nil {
				return selectedFailure(err)
			}
			result, err := creator.Commit(request.Context(), actor, prepared)
			if err != nil {
				return selectedFailure(err)
			}
			return selectedMutationResponse(http.StatusCreated, result.ID, result.Revision)
		},
		get: func(ctx context.Context, actor auth.Principal, id int64) (selectedRow[M], error) {
			row, err := reader.Get(ctx, actor, id)
			return project(row), err
		},
		list: func(ctx context.Context, actor auth.Principal, offset, limit int) (selectedPage[M], error) {
			page, err := reader.List(ctx, actor, offset, limit)
			result := selectedPage[M]{Count: page.Total, Items: make([]selectedRow[M], len(page.Items))}
			for index, row := range page.Items {
				result.Items[index] = project(row)
			}
			return result, err
		},
		update: func(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (int64, int64, error) {
			result, err := editor.Update(ctx, actor, id, revision, input)
			return result.ID, result.Revision, err
		},
		remove: func(ctx context.Context, actor auth.Principal, id, revision int64) (int64, int64, error) {
			result, err := owner.Deleter().Delete(ctx, actor, id, revision)
			return result.ID, result.Revision, err
		},
		history: reader.History,
	}
	if err := addSelectedResource(builder, resource); err != nil {
		return err
	}
	return addSelectedPassword(builder, owner.Passwords(), policies["change"], credential)
}

func addSelectedPassword[M any](builder *selectedBuilder, owner *accountmodel.PasswordEditor[M], policy api.PermissionPolicy, credential apiinput.Body[selectedCredentialInput]) error {
	type command struct{ Credential selectedCredentialInput }
	body, err := apiinput.Object(selectedParserConfig(), apiinput.ObjectField("credential", credential,
		func(input *command, value selectedCredentialInput) { input.Credential = value }))
	if err != nil {
		return err
	}
	schema, err := body.Schema(serializers.ModeFull)
	if err != nil {
		return err
	}
	ref, err := builder.schema("AccountPassword", schema)
	if err != nil {
		return err
	}
	mutation, err := selectedMutationSchema()
	if err != nil {
		return err
	}
	responses, err := selectedResponses(200, mutation, true, 400, 403, 404, 412, 413, 415, 428, 503)
	if err != nil {
		return err
	}
	operation := builder.operation("accounts", "password", "POST", "<str:id>/password/", responses)
	operation.Description = revisionPolicy
	operation.Parameters = []openapi.Parameter{builder.revision.Parameter()}
	operation.RequestBody = &openapi.RequestBody{Schema: ref, Required: true, Description: "A nonempty, untrimmed password, or explicit null to make password login unusable. Both replace the credential and revoke current-domain target sessions."}
	return builder.add(operation, policy, func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, valid := selectedID(request, false)
		if !valid {
			return invalidRequest()
		}
		revision, response, handled, err := builder.condition(request)
		if handled || err != nil {
			return response, err
		}
		input, response, handled, err := parseSelectedBody(request, body, serializers.ModeFull)
		if handled || err != nil {
			return response, err
		}
		password, present := input.Credential.Password.Get()
		if !present {
			return web.Response{}, selectedConfigError("credential", nil)
		}
		var result accountmodel.PasswordEditResult
		if password == nil {
			result, err = owner.SetUnusablePassword(request.Context(), actor, id, revision)
		} else {
			result, err = owner.SetPassword(request.Context(), actor, id, revision, *password)
		}
		if err != nil {
			return selectedFailure(err)
		}
		return selectedMutationResponse(http.StatusOK, result.ID, result.Revision)
	})
}
