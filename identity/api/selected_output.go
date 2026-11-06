package identityapi

import (
	"math"
	"net/http"
	"strconv"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

type selectedRow[M any] struct {
	ID, Revision   int64
	Fields         orm.ModelView[M]
	PasswordUsable bool
}

type selectedPage[M any] struct {
	Items                []selectedRow[M]
	Count, Offset, Limit int64
}

func selectedOutputLimits() serializers.Limits {
	return serializers.Limits{MaxStringBytes: 4096, MaxValues: 1 << 16}
}

func selectedRowShape[M any](manager orm.Manager[M], fields []string, account bool) (output.Shape[selectedRow[M]], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return output.Shape[selectedRow[M]]{}, err
	}
	fieldShape := output.Object[orm.ModelView[M]]()
	if len(fields) != 0 {
		spec, err := serializers.FromModel(metadata, selectedModelFields(metadata, fields, true)...)
		if err != nil {
			return output.Shape[selectedRow[M]]{}, err
		}
		fieldShape = output.ModelView(manager, spec)
	}
	properties := []output.Property[selectedRow[M]]{
		output.Field("id", output.Int64(), func(row selectedRow[M]) int64 { return row.ID }),
		output.Field("revision", output.Int64Range(1, math.MaxInt64), func(row selectedRow[M]) int64 { return row.Revision }),
		output.Field("fields", fieldShape, func(row selectedRow[M]) orm.ModelView[M] { return row.Fields }),
	}
	if account {
		properties = append(properties, output.Field("password_usable", output.Boolean(), func(row selectedRow[M]) bool { return row.PasswordUsable }))
	}
	return output.Object(properties...), nil
}

func selectedOutputs[M any](builder *selectedBuilder, name string, manager orm.Manager[M], read, list []string, account bool) (output.Output[selectedRow[M]], output.Output[selectedPage[M]], error) {
	var detail output.Output[selectedRow[M]]
	var page output.Output[selectedPage[M]]
	detailShape, err := selectedRowShape(manager, read, account)
	if err != nil {
		return detail, page, err
	}
	summaryShape, err := selectedRowShape(manager, list, account)
	if err != nil {
		return detail, page, err
	}
	detail, err = output.New(output.Named(name, detailShape), selectedOutputLimits())
	if err != nil {
		return detail, page, err
	}
	summary := output.Named(name+"Summary", summaryShape)
	page, err = output.New(output.Named(name+"Page", output.Object(
		output.Field("items", output.Array(summary, 0, 100), func(page selectedPage[M]) []selectedRow[M] { return page.Items }),
		output.Field("count", output.Int64Range(0, math.MaxInt64), func(page selectedPage[M]) int64 { return page.Count }),
		output.Field("offset", output.Int64Range(0, math.MaxInt32), func(page selectedPage[M]) int64 { return page.Offset }),
		output.Field("limit", output.Int64Range(1, 100), func(page selectedPage[M]) int64 { return page.Limit }),
	)), selectedOutputLimits())
	if err != nil {
		return detail, page, err
	}
	components, err := output.Components(detail.Declaration(), page.Declaration())
	if err != nil {
		return detail, page, err
	}
	builder.schemas = append(builder.schemas, components...)
	return detail, page, nil
}

func selectedHistoryOutput() (output.Output[[]accountaudit.Entry], error) {
	return output.New(output.Array(output.Object(
		output.Field("sequence", output.Int64Range(1, math.MaxInt64), func(entry accountaudit.Entry) int64 { return entry.Sequence() }),
		output.Field("action", output.String(), func(entry accountaudit.Entry) string { return string(entry.Action()) }),
		output.Field("changed_fields", output.Array(output.String(), 0, admin.MaximumChangedFields), func(entry accountaudit.Entry) []string { return entry.ChangedFields() }),
		output.Field("at", output.DateTime(), func(entry accountaudit.Entry) time.Time { return entry.At() }),
	), 0, admin.MaximumHistoryEntries), selectedOutputLimits())
}

func selectedMutationSchema() (openapi.Schema, error) {
	id, err := openapi.IntegerRange(math.MinInt64, math.MaxInt64)
	if err != nil {
		return openapi.Schema{}, err
	}
	revision, err := openapi.IntegerRange(1, math.MaxInt64)
	if err != nil {
		return openapi.Schema{}, err
	}
	return openapi.Object(openapi.Property{Name: "id", Schema: id, Required: true}, openapi.Property{Name: "revision", Schema: revision, Required: true})
}

// The lifecycle owners have already validated these two bounded integers and
// confirmed completion. This closed receipt has no getters, further I/O or
// request-context work that could turn a committed mutation into a failure.
// It never projects a model or assumes the actor can still read the target.
func selectedMutationResponse(status int, id, revision int64) (web.Response, error) {
	if revision <= 0 || status != http.StatusOK && status != http.StatusCreated {
		return web.Response{}, selectedConfigError("mutation_receipt", nil)
	}
	value := []byte(`{"id":`)
	value = strconv.AppendInt(value, id, 10)
	value = append(value, `,"revision":`...)
	value = strconv.AppendInt(value, revision, 10)
	value = append(value, '}')
	return web.NewResponse(status, http.Header{"Content-Type": {api.JSONContentType}, "Revision": {strconv.FormatInt(revision, 10)}}, value)
}

func selectedRevisionResponse(response web.Response, revision int64, err error) (web.Response, error) {
	if err != nil {
		return web.Response{}, err
	}
	header := response.Header()
	header.Set("Revision", strconv.FormatInt(revision, 10))
	return response.WithHeaders(header)
}
