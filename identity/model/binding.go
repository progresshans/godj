// Package model binds a generated account type to the selected Schema IR role
// and the host's complete relation graph. It does not provision, authenticate,
// authorize or transition a persisted account domain.
package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// Binding keeps the account type, role and project graph under one immutable
// owner. The zero value is unbound. A valid binding still requires runtime
// verification of the stored credential domain before account I/O.
type Binding[M any] struct{ state *bindingState[M] }

type bindingState[M any] struct {
	role             ir.AccountRole
	roleFingerprint  [sha256.Size]byte
	credentialFields map[string]ir.Field
	project          orm.ProjectBinding
	model            orm.BoundModel[M]
	objects          orm.Manager[M]
}

// Bind is normally called by a generated project BindAccount function. The
// complete host schema includes profile relations and external apps, not just
// the three models named by the account role. Descriptor binding checks the
// actual selected model rather than accepting a same-shaped foreign Go type.
func Bind[M any](role ir.AccountRole, descriptor orm.RelationObjectDescriptor[M], schemas ...ir.Schema) (Binding[M], error) {
	typeOf := reflect.TypeOf(descriptor)
	if typeOf == nil || typeOf.Kind() != reflect.Struct || typeOf.Name() == "" || typeOf.Size() != 0 {
		return Binding[M]{}, errors.New("identity model binding: descriptor must be a named zero-state value")
	}
	if _, ok := descriptor.(orm.ValueAssignmentDescriptor[M]); !ok {
		return Binding[M]{}, errors.New("identity model binding: descriptor cannot prepare model writes")
	}
	if err := projectspec.ValidateProjectResources(schemas, &role); err != nil {
		return Binding[M]{}, fmt.Errorf("identity model binding: %w", err)
	}
	normalized, err := ir.NormalizeAccountRole(role, schemas...)
	if err != nil {
		return Binding[M]{}, fmt.Errorf("identity model binding: %w", err)
	}
	project, err := orm.BindProject(schemas...)
	if err != nil {
		return Binding[M]{}, fmt.Errorf("identity model binding: %w", err)
	}
	account, err := orm.BindModel(project, normalized.Model, descriptor)
	if err != nil {
		return Binding[M]{}, fmt.Errorf("identity model binding: %w", err)
	}
	fingerprint, err := normalizedRoleFingerprint(normalized)
	if err != nil {
		return Binding[M]{}, fmt.Errorf("identity model binding: %w", err)
	}
	metadata, exists := project.Model(normalized.Model)
	if !exists {
		return Binding[M]{}, errors.New("identity model binding: account model is missing from the project")
	}
	fields := make(map[string]ir.Field, 3)
	for _, field := range metadata.Fields {
		if field.Name == normalized.PrincipalID || field.Name == normalized.Login || field.Name == normalized.Password {
			fields[field.Name] = field.Clone()
		}
	}
	return Binding[M]{state: &bindingState[M]{
		role: normalized, project: project, model: account, objects: orm.NewManager[M](descriptor),
		roleFingerprint: fingerprint, credentialFields: fields,
	}}, nil
}

func (binding Binding[M]) Role() (ir.AccountRole, error) {
	if binding.state == nil {
		return ir.AccountRole{}, errors.New("identity model binding: unbound account")
	}
	return binding.state.role.Clone(), nil
}

func (binding Binding[M]) Project() (orm.ProjectBinding, error) {
	if binding.state == nil {
		return orm.ProjectBinding{}, errors.New("identity model binding: unbound account")
	}
	return binding.state.project, nil
}

func (binding Binding[M]) Model() (orm.BoundModel[M], error) {
	if binding.state == nil {
		return orm.BoundModel[M]{}, errors.New("identity model binding: unbound account")
	}
	return binding.state.model, nil
}

// Objects exposes the selected model's ordinary typed ORM. It has the same
// server-side authority as other ORM managers; possession is not a credential
// and does not replace authorization or the identity runtime's write fence.
func (binding Binding[M]) Objects() (orm.Manager[M], error) {
	if binding.state == nil {
		return orm.Manager[M]{}, errors.New("identity model binding: unbound account")
	}
	return binding.state.objects, nil
}
