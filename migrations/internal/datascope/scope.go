// Package datascope carries a native session between a fenced backend and
// migrations without making a raw connection available to application code.
package datascope

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/schema/ir"
)

type Session interface {
	db.Session
	db.SessionValidator
}

type Scope struct {
	session Session
	models  map[ir.ModelIdentity]ir.Model
}

func New(session Session, models []migrationgraph.MigrationModel) Scope {
	scope := Scope{session: session, models: make(map[ir.ModelIdentity]ir.Model, len(models))}
	for _, model := range models {
		scope.models[model.Identity()] = model.Model.Clone()
	}
	return scope
}

func Model(ctx context.Context, scope Scope, app, name string) (Session, ir.Model, error) {
	if scope.session == nil {
		return nil, ir.Model{}, errors.New("historical models are not initialized")
	}
	if err := scope.session.ValidateSession(ctx); err != nil {
		return nil, ir.Model{}, err
	}
	model, ok := scope.models[ir.ModelIdentity{AppLabel: app, ModelName: name}]
	if !ok {
		return nil, ir.Model{}, fmt.Errorf("model %s.%s is outside this historical operation", app, name)
	}
	return scope.session, model, nil
}
