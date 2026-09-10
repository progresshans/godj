package multiruntimeworker

import (
	"context"
	"strconv"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

var (
	inspectionID               = query.NewFieldRef("id", "id", query.FieldInteger, false)
	credentialInspectionFields = []query.FieldRef{
		inspectionID,
		query.NewFieldRef("principal_id", "principal_id", query.FieldString, false),
		query.NewFieldRef("username", "username", query.FieldString, false),
		query.NewFieldRef("encoded_password", "encoded_password", query.FieldString, false),
		query.NewFieldRef("active", "active", query.FieldBoolean, false),
		query.NewFieldRef("permissions", "permissions", query.FieldString, false),
		query.NewFieldRef("definition_digest", "definition_digest", query.FieldString, false),
	}
	sessionInspectionFields = []query.FieldRef{
		inspectionID,
		query.NewFieldRef("digest", "digest", query.FieldString, false),
		query.NewFieldRef("payload", "payload", query.FieldString, false),
	}
	auditInspectionFields = []query.FieldRef{
		inspectionID,
		query.NewFieldRef("actor_id", "actor_id", query.FieldString, false),
		query.NewFieldRef("model", "model", query.FieldString, false),
		query.NewFieldRef("object_id", "object_id", query.FieldString, false),
		query.NewFieldRef("action", "action", query.FieldString, false),
		query.NewFieldRef("changed_fields", "changed_fields", query.FieldString, false),
		query.NewFieldRef("display_label", "display_label", query.FieldString, false),
	}
)

// inspectDurableSecretOccurrences queries framework state through the same
// backend-neutral Query AST used by product code. It checks every bounded text
// payload in the credential, session, and audit tables without emitting the
// values or retaining them in the child response.
func inspectDurableSecretOccurrences(
	ctx context.Context,
	runtime *systemstate.Runtime,
	config wireConfig,
) (int, error) {
	secrets := []string{config.Password, config.SQLiteDataSource, config.PostgresURL, config.PostgresSchema}
	count := 0
	credentialPlan, err := query.NewPlan("godj_system_credential", credentialInspectionFields).WithLimit(2)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	credentialRows, err := runtime.Query(ctx, credentialPlan)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	if err := consumeRows(credentialRows, func(rows db.Rows) error {
		var id int64
		var principal, username, encoded, permissions, digest string
		var active bool
		if err := rows.Scan(&id, &principal, &username, &encoded, &active, &permissions, &digest); err != nil {
			return err
		}
		count += countStrings(
			[]string{strconv.FormatInt(id, 10), principal, username, encoded, strconv.FormatBool(active), permissions, digest},
			secrets,
		)
		return nil
	}); err != nil {
		return 0, newError(CodePersistence)
	}

	sessionPlan, err := query.NewPlan("godj_system_session", sessionInspectionFields).WithLimit(4097)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	sessionRows, err := runtime.Query(ctx, sessionPlan)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	if err := consumeRows(sessionRows, func(rows db.Rows) error {
		var id int64
		var digest, payload string
		if err := rows.Scan(&id, &digest, &payload); err != nil {
			return err
		}
		count += countStrings([]string{strconv.FormatInt(id, 10), digest, payload}, secrets)
		return nil
	}); err != nil {
		return 0, newError(CodePersistence)
	}

	auditPlan, err := query.NewPlan("godj_system_audit", auditInspectionFields).WithLimit(1025)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	auditRows, err := runtime.Query(ctx, auditPlan)
	if err != nil {
		return 0, newError(CodePersistence)
	}
	if err := consumeRows(auditRows, func(rows db.Rows) error {
		var id int64
		var actor, model, objectID, action, changedFields, displayLabel string
		if err := rows.Scan(&id, &actor, &model, &objectID, &action, &changedFields, &displayLabel); err != nil {
			return err
		}
		count += countStrings(
			[]string{strconv.FormatInt(id, 10), actor, model, objectID, action, changedFields, displayLabel},
			secrets,
		)
		return nil
	}); err != nil {
		return 0, newError(CodePersistence)
	}
	return count, nil
}

func consumeRows(rows db.Rows, consume func(db.Rows) error) error {
	if rows == nil || consume == nil {
		return newError(CodePersistence)
	}
	for rows.Next() {
		if err := consume(rows); err != nil {
			_ = rows.Close()
			return newError(CodePersistence)
		}
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		return newError(CodePersistence)
	}
	return nil
}

func countStrings(values, secrets []string) int {
	count := 0
	for _, value := range values {
		for _, secret := range secrets {
			if secret != "" {
				count += strings.Count(value, secret)
			}
		}
	}
	return count
}
