// Package systemstatehandoff owns the retirement marker shared by historical
// systemstate handoff and selected-account startup. It grants no authority and
// neither creates a domain nor interprets historical credentials as accounts.
package systemstatehandoff

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/query"
)

const CredentialTable = "godj_system_credential"
const passwordPrefix = "!godj-selected-account:"

var ErrInvalid = errors.New("systemstate handoff: invalid retirement evidence")

func Password(operation string) (string, error) {
	if len(operation) != 64 || strings.ToLower(operation) != operation {
		return "", ErrInvalid
	}
	decoded, err := hex.DecodeString(operation)
	if err != nil || len(decoded) != 32 || operation == strings.Repeat("0", 64) {
		return "", ErrInvalid
	}
	return passwordPrefix + operation, nil
}

func RetiredOperation(password string) (string, bool) {
	operation, found := strings.CutPrefix(password, passwordPrefix)
	if !found {
		return "", false
	}
	canonical, err := Password(operation)
	return operation, err == nil && canonical == password
}

// RequireRetired verifies exactly one inactive, unusable historical marker
// bound to the first selected-account operation. Read scopes and rowsets remain
// with their original owner; an error never constitutes proof of retirement.
func RequireRetired(ctx context.Context, reader db.Queryer, operation string) error {
	password, err := Password(operation)
	if err != nil || nilValue(ctx) || nilValue(reader) {
		return ErrInvalid
	}
	validator, ok := reader.(db.SessionValidator)
	if !ok || nilValue(validator) {
		return ErrInvalid
	}
	switch reader.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return ErrInvalid
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return err
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	fields := []query.FieldRef{id,
		query.NewFieldRef("encoded_password", "encoded_password", query.FieldString, false),
		query.NewFieldRef("active", "active", query.FieldBoolean, false),
		query.NewFieldRef("permissions", "permissions", query.FieldString, false),
		query.NewFieldRef("definition_digest", "definition_digest", query.FieldString, false)}
	plan, err := query.NewPlan(CredentialTable, fields).WithOrderings(query.NewOrdering(id, query.Ascending)).WithLimit(2)
	if err != nil {
		return err
	}
	count := 0
	err = rowset.Scan(ctx, reader, plan, func(err error) error { return err }, func(rows db.Rows) (bool, error) {
		var key int64
		var encoded, permissions, digest string
		var active bool
		if err := rows.Scan(&key, &encoded, &active, &permissions, &digest); err != nil {
			return false, err
		}
		count++
		// v1.AAA is the historical canonical uint16 zero-permission payload.
		if count != 1 || key <= 0 || encoded != password || active || permissions != "v1.AAA" || digest != "sha256:"+operation {
			return false, ErrInvalid
		}
		return true, nil
	})
	if err := errors.Join(err, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return err
	}
	if count != 1 {
		return ErrInvalid
	}
	return nil
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
