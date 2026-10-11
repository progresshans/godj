package identity

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

// CheckUserCreation checks selected, already-cleaned creation form fields.
// A nil username was rejected by field cleaning; a nil password was rejected
// by cleaning/confirmation or deliberately disabled. The username check uses
// the form's case-insensitive duplicate policy. Password policy observes the
// candidate username unless that name was a duplicate, matching the default
// Django UserCreationForm's model construction order.
//
// This read requires current add_user AND change_user authority, even for
// invalid/empty fields. It allocates no identity, hashes no password and writes
// nothing. It does not grant write authority: CreateUser performs its own
// preflight and final fenced checks. The caller retains no reusable snapshot.
func (manager *Manager) CheckUserCreation(ctx context.Context, actor auth.Principal, username, password *string) error {
	if err := manager.validCall(ctx, actor); err != nil {
		return err
	}
	_, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
		if err := manager.requireActor(ctx, reader, actor.ID(), AddUser, ChangeUser); err != nil {
			return struct{}{}, err
		}
		profile := Profile{Active: true}
		var failures validation.Errors
		if username != nil && *username != "" {
			exists, err := models.UserObjects.Using(reader).Filter(models.UserFields.Username.IExact(*username)).Exists(ctx)
			if err != nil {
				return struct{}{}, err
			}
			if exists {
				failures = validation.NewErrors(validation.New("username", "unique"))
			} else {
				profile.Username = *username
			}
		}
		if password != nil && *password != "" {
			if err := manager.validatePassword(ctx, *password, profile); err != nil {
				if diagnostics, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
					failures = validation.Join(failures, diagnostics)
				} else {
					return struct{}{}, err
				}
			}
		}
		if !failures.Empty() {
			return struct{}{}, validation.Reject(failures, nil)
		}
		return struct{}{}, nil
	})
	return err
}
