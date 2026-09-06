package codegen

import (
	"sort"

	"github.com/progresshans/godj/schema/ir"
)

type normalizedRelationPackage struct {
	alias      string
	prefix     string
	importPath string
	schema     ir.Schema
}

type projectRelationModel struct {
	app      normalizedRelationPackage
	identity ir.ModelIdentity
	model    ir.Model
	bind     int
}

// relationProjectPlan belongs to one generation. Every renderer and namespace
// check shares the same app/model identities and lazily prepared relation views.
// It is never retained by generated output or shared across generation calls.
type relationProjectPlan struct {
	apps       []normalizedRelationPackage
	models     []*projectRelationModel
	byIdentity map[ir.ModelIdentity]*projectRelationModel
	query      relationView[[]projectRelationQuerySource]
	object     relationView[[]projectRelationObjectSource]
	reverse    relationView[[]projectRelationReverseOwner]
	delete     relationView[[]projectRelationDeleteTarget]
}

type relationView[T any] struct {
	ready bool
	value T
	err   error
}

func (view *relationView[T]) get(build func() (T, error)) (T, error) {
	if !view.ready {
		view.value, view.err = build()
		view.ready = true
	}
	return view.value, view.err
}

func newRelationProjectPlan(apps []normalizedRelationPackage) *relationProjectPlan {
	plan := &relationProjectPlan{apps: apps, byIdentity: make(map[ir.ModelIdentity]*projectRelationModel)}
	for _, app := range apps {
		for _, model := range app.schema.Models {
			identity := ir.ModelIdentity{AppLabel: app.schema.AppLabel, ModelName: model.Name}
			candidate := &projectRelationModel{app: app, identity: identity, model: model}
			plan.models = append(plan.models, candidate)
			plan.byIdentity[identity] = candidate
		}
	}
	sort.Slice(plan.models, func(left, right int) bool {
		if plan.models[left].identity.AppLabel != plan.models[right].identity.AppLabel {
			return plan.models[left].identity.AppLabel < plan.models[right].identity.AppLabel
		}
		return plan.models[left].identity.ModelName < plan.models[right].identity.ModelName
	})
	for index, model := range plan.models {
		model.bind = index
	}
	return plan
}

func (plan *relationProjectPlan) querySurface() ([]*projectRelationModel, []projectRelationQuerySource, error) {
	sources, err := plan.query.get(func() ([]projectRelationQuerySource, error) {
		_, sources, err := buildProjectRelationQuerySurface(plan)
		return sources, err
	})
	return plan.models, sources, err
}

func (plan *relationProjectPlan) objectSurface() ([]*projectRelationModel, []projectRelationObjectSource, error) {
	sources, err := plan.object.get(func() ([]projectRelationObjectSource, error) {
		_, sources, err := buildProjectRelationObjectSurface(plan)
		return sources, err
	})
	return plan.models, sources, err
}

func (plan *relationProjectPlan) reverseSurface() ([]*projectRelationModel, []projectRelationReverseOwner, error) {
	owners, err := plan.reverse.get(func() ([]projectRelationReverseOwner, error) {
		_, owners, err := buildProjectRelationReverseSurface(plan)
		return owners, err
	})
	return plan.models, owners, err
}

func (plan *relationProjectPlan) deleteSurface() ([]projectRelationDeleteTarget, error) {
	return plan.delete.get(func() ([]projectRelationDeleteTarget, error) {
		return buildProjectRelationDeleteSurface(plan)
	})
}
