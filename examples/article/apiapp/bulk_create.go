package apiapp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bulkCreateEndpoint(authentication api.Authentication, item endpoint.Input[articleInput]) (endpoint.Endpoint, error) {
	list := endpoint.JSONListBody(item, input.ListConfig{
		MinItems: 1, MaxItems: articleapp.BulkCreateMaximum,
		Parser:     api.ParserConfig{MaxBodyBytes: articleapp.BulkCreateMaximum * maximumJSONBodyBytes, JSONLimits: serializers.Limits{MaxDepth: maximumJSONDepth, MaxStringBytes: maximumJSONStringByte, MaxValues: 1 << 16}},
		ItemLimits: serializers.Limits{MaxDocumentBytes: maximumJSONBodyBytes, MaxDepth: maximumJSONDepth, MaxStringBytes: maximumJSONStringByte},
	}, fmt.Sprintf("An array of 1 to %d ArticleCreate objects; at most %d total bytes and %d bytes per compact item. Single-article defaults, text and null policies apply to each item.", articleapp.BulkCreateMaximum, articleapp.BulkCreateMaximum*maximumJSONBodyBytes, maximumJSONBodyBytes), func(_ context.Context, value articleInput) (validation.Errors, error) {
		return value.textDiagnostics(), nil
	})
	return endpoint.New(authentication, endpoint.Config[[]articleInput, []articlemodels.Article]{
		Route: web.Route{Name: Namespace + ":article-bulk-create", Method: http.MethodPost, Path: BulkCreatePath}, Summary: "Create several Articles",
		Description: "Authentication, required CSRF and add permission precede parsing. All rows and unique slugs are checked before native bulk INSERTs. Every batch, stored-row reload, complete output and mutation hook share one transaction. Results retain input order. Failure or an uncertain commit publishes no partial result and does not retry. Query parameters are rejected. Field errors carry the original zero-based index; a concurrent unique conflict is a whole-request rejection. This atomic bulk policy belongs to the Article application.",
		Admission:   endpoint.All(articleapp.ArticleAddPermission), Input: endpoint.NoQuery(list), Output: a.bulkResponse,
		ErrorLimits: serializers.Limits{MaxValues: 1 << 16, MaxArrayItems: 1 << 14}, SummarizeValidationErrors: true,
		Success: []endpoint.Status{{Code: http.StatusCreated, Description: "All created Articles in input order."}},
		Errors: []endpoint.Status{
			{Code: 400, Description: "Invalid count, item or unique slug; no Article is created."},
			{Code: 413, Description: "The complete array exceeds its request byte limit."},
			{Code: 415, Description: "Exactly one supported application/json Content-Type is required."},
		},
		Handle: func(request *web.Request, _ auth.Principal, values []articleInput) (output.Prepared[[]articlemodels.Article], error) {
			inputs := make([]articleapp.Input, len(values))
			for index, value := range values {
				inputs[index] = value.full(nil)
			}
			prepared, err := articleapp.BulkCreateAndPrepare(request.Context(), a.repository, inputs, func(ctx context.Context, rows []articleapp.Article) (output.Prepared[[]articlemodels.Article], error) {
				models := make([]articlemodels.Article, len(rows))
				for index, row := range rows {
					models[index] = articleapp.ModelSnapshot(row)
				}
				return a.bulkResponse.Prepare(ctx, http.StatusCreated, models)
			})
			if err != nil {
				return output.Prepared[[]articlemodels.Article]{}, typedArticleFailure(err)
			}
			return prepared, nil
		},
	})
}
