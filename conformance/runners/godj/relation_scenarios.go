package godj

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/relationdeleteproduct"
	"github.com/progresshans/godj/conformance/relationobjectproduct"
	"github.com/progresshans/godj/conformance/relationprefetchproduct"
	"github.com/progresshans/godj/conformance/relationproduct"
	"github.com/progresshans/godj/conformance/relationqueryproduct"
	"github.com/progresshans/godj/conformance/relationreverseproduct"
	"github.com/progresshans/godj/conformance/relationselectproduct"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func relationScenarioHandler(scenario string) (scenarioHandler, bool) {
	switch scenario {
	case "django.relation.cross_app_metadata":
		return relationCrossAppMetadata, true
	case "django.relation.unsaved_related_target":
		return relationUnsavedRelatedTarget, true
	case "django.relation.forward_lazy_cache":
		return relationForwardLazyCache, true
	case "django.relation.forward_lookup_join_reuse":
		return relationForwardLookupJoinReuse, true
	case "django.relation.reverse_accessor_and_lookup":
		return relationReverseAccessorAndLookup, true
	case "django.relation.nullable_access_and_isnull":
		return relationNullableAccessAndIsNull, true
	case "django.relation.protect_delete":
		return relationProtectDelete, true
	case "django.relation.set_null_delete":
		return relationSetNullDelete, true
	case "django.relation.required_select_related":
		return relationRequiredSelectRelated, true
	case "django.relation.nullable_select_related":
		return relationNullableSelectRelated, true
	case "django.relation.invalid_reverse_select_related":
		return relationInvalidReverseSelectRelated, true
	case "django.relation.reverse_prefetch":
		return relationReversePrefetch, true
	default:
		return nil, false
	}
}

func relationUnsavedRelatedTarget(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationdeleteproduct.ObserveUnsavedRelatedTarget(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-002 product: %w", err)
	}
	var queryError *query.Error
	if !errors.As(observed.Err, &queryError) {
		return protocol.Observation{}, fmt.Errorf("REL-002 error = %v, want *query.Error", observed.Err)
	}
	statementKinds := make([]protocol.Value, len(observed.Metrics.StatementKinds))
	for index, kind := range observed.Metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	messageIsContract := false
	databaseState := relationDeleteDatabaseStateValue(observed.After)
	metrics := protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(observed.Metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(),
		"inner_join_count":      protocol.Integer("0"),
		"left_outer_join_count": protocol.Integer("0"),
		"row_delta": protocol.Object(map[string]protocol.Value{
			"authors": protocol.Integer(strconv.Itoa(len(observed.After.Authors) - len(observed.Before.Authors))),
			"posts":   protocol.Integer(strconv.Itoa(len(observed.After.Posts) - len(observed.Before.Posts))),
		}),
	})
	return protocol.Observation{
		ID:     contract.ID,
		Status: protocol.StatusObserved,
		Phase:  contract.Phase,
		Error: &protocol.ObservedError{
			Category:          queryError.Category,
			Code:              queryError.Code,
			Message:           observed.Err.Error(),
			MessageIsContract: &messageIsContract,
		},
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationProtectDelete(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationdeleteproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-007/008 delete product: %w", err)
	}
	var protected *query.ProtectedForeignKeyError
	if !errors.As(observed.Protect.Err, &protected) {
		return protocol.Observation{}, fmt.Errorf("REL-007 error = %v, want *query.ProtectedForeignKeyError", observed.Protect.Err)
	}
	var queryError *query.Error
	if !errors.As(observed.Protect.Err, &queryError) {
		return protocol.Observation{}, fmt.Errorf("REL-007 error = %v, want wrapped *query.Error", observed.Protect.Err)
	}
	messageIsContract := false
	databaseState := relationDeleteDatabaseStateValue(observed.Protect.After)
	metrics := protocol.Object(map[string]protocol.Value{
		"update_statement_count": protocol.Integer(strconv.FormatInt(observed.Protect.Metrics.RelationSetNullCount, 10)),
		"delete_statement_count": protocol.Integer(strconv.FormatInt(observed.Protect.Metrics.DeleteCount, 10)),
		"protected_source_rows":  protocol.Integer(strconv.FormatInt(protected.ProtectedSourceRows(), 10)),
	})
	return protocol.Observation{
		ID:     contract.ID,
		Status: protocol.StatusObserved,
		Phase:  contract.Phase,
		Error: &protocol.ObservedError{
			Category:          queryError.Category,
			Code:              queryError.Code,
			Message:           observed.Protect.Err.Error(),
			MessageIsContract: &messageIsContract,
		},
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationSetNullDelete(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationdeleteproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-007/008 delete product: %w", err)
	}
	if observed.SetNull.Err != nil {
		return protocol.Observation{}, fmt.Errorf("REL-008 delete failed: %w", observed.SetNull.Err)
	}
	mutationOrder := make([]protocol.Value, len(observed.SetNull.Metrics.MutationOrder))
	for index, kind := range observed.SetNull.Metrics.MutationOrder {
		mutationOrder[index] = protocol.String(kind)
	}
	mutationRows := make([]protocol.Value, len(observed.SetNull.Metrics.MutationRows))
	for index, row := range observed.SetNull.Metrics.MutationRows {
		mutationRows[index] = protocol.Object(map[string]protocol.Value{
			"kind":          protocol.String(row.Kind),
			"affected_rows": protocol.Integer(strconv.FormatInt(row.AffectedRows, 10)),
		})
	}
	affectedSourceRows := sumRelationDeleteRows(observed.SetNull.Metrics.RelationSetNullRows)
	deletedTargetRows := sumRelationDeleteRows(observed.SetNull.Metrics.DeleteRows)
	result := protocol.Object(map[string]protocol.Value{
		"deleted_total":  protocol.Integer(strconv.FormatInt(observed.SetNull.Returned, 10)),
		"target_deleted": protocol.Integer(strconv.FormatInt(observed.SetNull.Returned, 10)),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"transaction_count":      protocol.Integer(strconv.FormatInt(observed.SetNull.Metrics.TransactionCount, 10)),
		"mutation_order":         protocol.List(mutationOrder...),
		"mutation_rows":          protocol.List(mutationRows...),
		"update_statement_count": protocol.Integer(strconv.FormatInt(observed.SetNull.Metrics.RelationSetNullCount, 10)),
		"delete_statement_count": protocol.Integer(strconv.FormatInt(observed.SetNull.Metrics.DeleteCount, 10)),
		"affected_source_rows":   protocol.Integer(strconv.FormatInt(affectedSourceRows, 10)),
		"deleted_target_rows":    protocol.Integer(strconv.FormatInt(deletedTargetRows, 10)),
	})
	databaseState := relationDeleteDatabaseStateValue(observed.SetNull.After)
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func sumRelationDeleteRows(rows []int64) int64 {
	var total int64
	for _, row := range rows {
		total += row
	}
	return total
}

func relationRequiredSelectRelated(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationselectproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-009/010/011 product: %w", err)
	}
	result := protocol.Object(map[string]protocol.Value{
		"plain": relationSelectedRowsValue(observed.Required.Plain),
		"eager": relationSelectedRowsValue(observed.Required.Eager),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"plain": relationSelectQueryMetricsValue(observed.Required.PlainMetrics),
		"eager": relationSelectQueryMetricsValue(observed.Required.EagerMetrics),
	})
	databaseState := relationSelectDatabaseStateValue(observed.DBState)
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationNullableSelectRelated(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationselectproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-009/010/011 product: %w", err)
	}
	result := protocol.Object(map[string]protocol.Value{
		"rows": relationSelectedRowsValue(observed.Nullable.Rows),
	})
	metrics := relationSelectQueryMetricsValue(observed.Nullable.Metrics)
	databaseState := relationSelectDatabaseStateValue(observed.DBState)
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationInvalidReverseSelectRelated(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationselectproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-009/010/011 product: %w", err)
	}
	var queryError *query.Error
	if !errors.As(observed.Invalid.Err, &queryError) {
		return protocol.Observation{}, fmt.Errorf("REL-011 error = %v, want *query.Error", observed.Invalid.Err)
	}
	messageIsContract := false
	metrics := relationSelectInvalidMetricsValue(observed.Invalid.Metrics)
	databaseState := relationSelectDatabaseStateValue(observed.DBState)
	return protocol.Observation{
		ID:     contract.ID,
		Status: protocol.StatusObserved,
		Phase:  contract.Phase,
		Error: &protocol.ObservedError{
			Category:          queryError.Category,
			Code:              queryError.Code,
			Message:           queryError.Error(),
			MessageIsContract: &messageIsContract,
		},
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationForwardLazyCache(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationobjectproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-003 product: %w", err)
	}
	cold := relationObjectAuthorValue(observed.Forward.Cold)
	warm := relationObjectAuthorValue(observed.Forward.Warm)
	steps := make([]protocol.Value, len(observed.Forward.Steps))
	for index, step := range observed.Forward.Steps {
		value := relationObjectQueryMetricsValue(step.Metrics)
		fields := map[string]protocol.Value{
			"name": protocol.String(step.Name),
		}
		for _, field := range value.Fields {
			fields[field.Name] = field.Value
		}
		steps[index] = protocol.Object(fields)
	}
	result := protocol.Object(map[string]protocol.Value{
		"cold": cold,
		"warm": warm,
	})
	databaseState := relationObjectDatabaseStateValue(observed.DBState)
	metrics := protocol.Object(map[string]protocol.Value{
		"steps": protocol.List(steps...),
	})
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationNullableAccessAndIsNull(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationobjectproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-006 product: %w", err)
	}
	reviewer := protocol.Null()
	if observed.Nullable.Reviewer != nil {
		reviewer = relationObjectAuthorValue(*observed.Nullable.Reviewer)
	}
	identifiers := make([]protocol.Value, len(observed.Nullable.IsNullPostIDs))
	for index, identifier := range observed.Nullable.IsNullPostIDs {
		identifiers[index] = relationPrimaryKey(identifier)
	}
	result := protocol.Object(map[string]protocol.Value{
		"reviewer":        reviewer,
		"isnull_post_ids": protocol.List(identifiers...),
	})
	databaseState := relationObjectDatabaseStateValue(observed.DBState)
	metrics := protocol.Object(map[string]protocol.Value{
		"null_access":         relationObjectQueryMetricsValue(observed.Nullable.NullAccess),
		"isnull_construction": relationObjectQueryMetricsValue(observed.Nullable.IsNullConstruction),
		"isnull_evaluation":   relationObjectQueryMetricsValue(observed.Nullable.IsNullEvaluation),
	})
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationCrossAppMetadata(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}
	binding, err := relationproduct.Binding()
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("bind generated REL-001 project: %w", err)
	}
	return relationMetadataObservation(contract, binding)
}

func relationForwardLookupJoinReuse(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationqueryproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-004 product: %w", err)
	}
	cases := make([]protocol.Value, len(observed.Cases))
	metrics := make([]protocol.Value, len(observed.Cases))
	for index, observedCase := range observed.Cases {
		identifiers := make([]protocol.Value, len(observedCase.PostIDs))
		for identifierIndex, identifier := range observedCase.PostIDs {
			identifiers[identifierIndex] = relationPrimaryKey(identifier)
		}
		cases[index] = protocol.Object(map[string]protocol.Value{
			"name":     protocol.String(observedCase.Name),
			"post_ids": protocol.List(identifiers...),
		})
		metrics[index] = protocol.Object(map[string]protocol.Value{
			"name":         protocol.String(observedCase.Name),
			"construction": relationQueryMetricsValue(observedCase.Construction),
			"evaluation":   relationQueryMetricsValue(observedCase.Evaluation),
		})
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(cases...),
	})
	databaseState := relationDatabaseStateValue(observed.DBState)
	metricsValue := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(metrics...),
	})
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metricsValue,
	}, nil
}

func relationReverseAccessorAndLookup(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationreverseproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-005 product: %w", err)
	}
	accessorIDs := make([]protocol.Value, len(observed.AccessorPostIDs))
	for index, identifier := range observed.AccessorPostIDs {
		accessorIDs[index] = relationPrimaryKey(identifier)
	}
	lookupIDs := make([]protocol.Value, len(observed.LookupAuthorIDs))
	for index, identifier := range observed.LookupAuthorIDs {
		lookupIDs[index] = relationPrimaryKey(identifier)
	}
	result := protocol.Object(map[string]protocol.Value{
		"accessor_post_ids": protocol.List(accessorIDs...),
		"lookup_author_ids": protocol.List(lookupIDs...),
	})
	databaseState := relationReverseDatabaseStateValue(observed.DBState)
	metrics := protocol.Object(map[string]protocol.Value{
		"accessor": relationReverseQueryMetricsValue(observed.Accessor),
		"lookup":   relationReverseQueryMetricsValue(observed.Lookup),
	})
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationReversePrefetch(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	observed, err := relationprefetchproduct.Observe(ctx)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("observe generated REL-012 product: %w", err)
	}
	authors := make([]protocol.Value, len(observed.Authors))
	for index, author := range observed.Authors {
		posts := make([]protocol.Value, len(author.PostIDs))
		for postIndex, identifier := range author.PostIDs {
			posts[postIndex] = relationPrimaryKey(identifier)
		}
		authors[index] = protocol.List(
			relationPrimaryKey(author.AuthorID),
			protocol.List(posts...),
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
	})
	databaseState := relationPrefetchDatabaseStateValue(observed.DBState)
	metrics := relationPrefetchQueryMetricsValue(observed.Metrics)
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func relationQueryMetricsValue(metrics relationqueryproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(joinKinds...),
		"inner_join_count":      protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count": protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
	})
}

func relationObjectQueryMetricsValue(metrics relationobjectproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(joinKinds...),
		"inner_join_count":      protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count": protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
	})
}

func relationReverseQueryMetricsValue(metrics relationreverseproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(joinKinds...),
		"inner_join_count":      protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count": protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
	})
}

func relationPrefetchQueryMetricsValue(metrics relationprefetchproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":                  protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":              protocol.List(statementKinds...),
		"join_kinds":                   protocol.List(joinKinds...),
		"inner_join_count":             protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count":        protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
		"primary_query_count":          protocol.Integer(strconv.FormatInt(metrics.PrimaryQueryCount, 10)),
		"batch_query_count":            protocol.Integer(strconv.FormatInt(metrics.BatchQueryCount, 10)),
		"batch_predicate_column":       protocol.String(metrics.BatchPredicateColumn),
		"batch_key_count":              protocol.Integer(strconv.FormatInt(metrics.BatchKeyCount, 10)),
		"related_access_extra_queries": protocol.Integer(strconv.FormatInt(metrics.RelatedAccessExtraQueries, 10)),
	})
}

func relationSelectQueryMetricsValue(metrics relationselectproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(joinKinds...),
		"inner_join_count":      protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count": protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
		"access_extra_queries":  protocol.Integer(strconv.FormatInt(metrics.AccessExtraQueries, 10)),
	})
}

func relationSelectInvalidMetricsValue(metrics relationselectproduct.QueryMetrics) protocol.Value {
	statementKinds := make([]protocol.Value, len(metrics.StatementKinds))
	for index, kind := range metrics.StatementKinds {
		statementKinds[index] = protocol.String(kind)
	}
	joinKinds := make([]protocol.Value, len(metrics.JoinKinds))
	for index, kind := range metrics.JoinKinds {
		joinKinds[index] = protocol.String(kind)
	}
	return protocol.Object(map[string]protocol.Value{
		"query_count":           protocol.Integer(strconv.FormatInt(metrics.QueryCount, 10)),
		"statement_kinds":       protocol.List(statementKinds...),
		"join_kinds":            protocol.List(joinKinds...),
		"inner_join_count":      protocol.Integer(strconv.FormatInt(metrics.InnerJoinCount, 10)),
		"left_outer_join_count": protocol.Integer(strconv.FormatInt(metrics.LeftOuterJoinCount, 10)),
		"mutation_count":        protocol.Integer(strconv.FormatInt(metrics.MutationCount, 10)),
	})
}

func relationSelectedRowsValue(rows []relationselectproduct.PostRelatedRow) protocol.Value {
	values := make([]protocol.Value, len(rows))
	for index, row := range rows {
		name := protocol.Null()
		if row.Name != nil {
			name = protocol.String(*row.Name)
		}
		values[index] = protocol.List(relationPrimaryKey(row.PostID), name)
	}
	return protocol.List(values...)
}

func relationDatabaseStateValue(state relationqueryproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = protocol.Object(map[string]protocol.Value{
			"id":   relationPrimaryKey(author.ID),
			"name": protocol.String(author.Name),
		})
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationObjectAuthorValue(author relationobjectproduct.AuthorRow) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"id":   relationPrimaryKey(author.ID),
		"name": protocol.String(author.Name),
	})
}

func relationObjectDatabaseStateValue(state relationobjectproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = relationObjectAuthorValue(author)
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationReverseDatabaseStateValue(state relationreverseproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = protocol.Object(map[string]protocol.Value{
			"id":   relationPrimaryKey(author.ID),
			"name": protocol.String(author.Name),
		})
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationPrefetchDatabaseStateValue(state relationprefetchproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = protocol.Object(map[string]protocol.Value{
			"id":   relationPrimaryKey(author.ID),
			"name": protocol.String(author.Name),
		})
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationSelectDatabaseStateValue(state relationselectproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = protocol.Object(map[string]protocol.Value{
			"id":   relationPrimaryKey(author.ID),
			"name": protocol.String(author.Name),
		})
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationDeleteDatabaseStateValue(state relationdeleteproduct.DatabaseState) protocol.Value {
	authors := make([]protocol.Value, len(state.Authors))
	for index, author := range state.Authors {
		authors[index] = protocol.Object(map[string]protocol.Value{
			"id":   relationPrimaryKey(author.ID),
			"name": protocol.String(author.Name),
		})
	}
	posts := make([]protocol.Value, len(state.Posts))
	for index, post := range state.Posts {
		reviewer := protocol.Null()
		if post.ReviewerID != nil {
			reviewer = relationPrimaryKey(*post.ReviewerID)
		}
		posts[index] = protocol.Object(map[string]protocol.Value{
			"id":          relationPrimaryKey(post.ID),
			"title":       protocol.String(post.Title),
			"author_id":   relationPrimaryKey(post.AuthorID),
			"reviewer_id": reviewer,
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"authors": protocol.List(authors...),
		"posts":   protocol.List(posts...),
	})
}

func relationPrimaryKey(identifier int64) protocol.Value {
	return protocol.PrimaryKey(protocol.Integer(strconv.FormatInt(identifier, 10)))
}

func relationMetadataObservation(contract protocol.Contract, binding orm.ProjectBinding) (protocol.Observation, error) {
	forwardMetadata := binding.ForwardRelations()
	forward := make([]protocol.Value, 0, len(forwardMetadata))
	for _, relation := range forwardMetadata {
		onDelete, err := relationDeletePolicyPresentation(relation.OnDelete)
		if err != nil {
			return protocol.Observation{}, err
		}
		forward = append(forward, protocol.Object(map[string]protocol.Value{
			"name":        protocol.String(relation.Field),
			"column":      protocol.String(relation.Column),
			"target":      relationModelIdentityValue(relation.Target),
			"nullable":    protocol.Boolean(relation.Nullable),
			"reverse":     protocol.String(relation.Reverse.Name),
			"many_to_one": protocol.Boolean(relation.Cardinality == ir.RelationManyToOne),
			"on_delete":   protocol.String(onDelete),
		}))
	}

	reverseMetadata := binding.ReverseRelations()
	reverse := make([]protocol.Value, 0, len(reverseMetadata))
	for _, relation := range reverseMetadata {
		reverse = append(reverse, protocol.Object(map[string]protocol.Value{
			"name":        protocol.String(relation.Name),
			"field":       protocol.String(relation.SourceField),
			"target":      relationModelIdentityValue(relation.Target),
			"one_to_many": protocol.Boolean(relation.Cardinality == ir.RelationOneToMany),
		}))
	}

	result := protocol.Object(map[string]protocol.Value{
		"forward": protocol.List(forward...),
		"reverse": protocol.List(reverse...),
	})
	return protocol.Observation{
		ID:     contract.ID,
		Status: protocol.StatusObserved,
		Phase:  contract.Phase,
		Result: &result,
	}, nil
}

func relationModelIdentityValue(identity ir.ModelIdentity) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"app":   protocol.String(identity.AppLabel),
		"model": protocol.String(identity.ModelName),
	})
}

func relationDeletePolicyPresentation(policy ir.DeletePolicy) (string, error) {
	switch policy {
	case ir.DeleteProtect:
		return "PROTECT", nil
	case ir.DeleteSetNull:
		return "SET_NULL", nil
	default:
		return "", fmt.Errorf("unsupported relation delete policy %q", policy)
	}
}
