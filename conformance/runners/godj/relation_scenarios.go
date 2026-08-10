package godj

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/relationproduct"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func relationScenarioHandler(scenario string) (scenarioHandler, bool) {
	if scenario == "django.relation.cross_app_metadata" {
		return relationCrossAppMetadata, true
	}
	return nil, false
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
