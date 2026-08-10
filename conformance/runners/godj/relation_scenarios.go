package godj

import (
	"context"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/relationproduct"
	"github.com/progresshans/godj/conformance/relationqueryproduct"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func relationScenarioHandler(scenario string) (scenarioHandler, bool) {
	switch scenario {
	case "django.relation.cross_app_metadata":
		return relationCrossAppMetadata, true
	case "django.relation.forward_lookup_join_reuse":
		return relationForwardLookupJoinReuse, true
	default:
		return nil, false
	}
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
