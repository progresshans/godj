package systemstate

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// AppendAudit writes one already-validated semantic event through the borrowed
// Article transaction. It deliberately does not acquire Runtime's gate or open
// a nested transaction: callers inject Runtime as the Article backend, so the
// surrounding Runtime.Atomic already owns both until commit or rollback. The
// callback must continue using this borrowed Session rather than recursively
// invoking another Runtime or session-store transaction.
func (runtime *Runtime) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	if err := runtime.validBackendCall(ctx); err != nil {
		return err
	}
	if isNilInterface(session) {
		return &Error{Code: CodeInvalidInput, Field: "session", Detail: "borrowed audit session is nil"}
	}
	changedFields, err := encodeAuditChangedFields(event.ChangedFields())
	if err != nil {
		return err
	}
	// Revalidate the exported snapshot so a zero or manually corrupted value
	// cannot cross the persistence boundary even though PreparedEvent fields are
	// currently private.
	if _, err := admin.PrepareEvent(
		event.ActorID(),
		event.Model(),
		event.ObjectID(),
		event.Action(),
		event.ChangedFields(),
		event.DisplayLabel(),
	); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "audit_event", Detail: "prepared audit event is invalid", Cause: err}
	}
	identifier, err := session.Insert(ctx, query.NewInsertPlanReturningKey(
		auditTableName,
		[]query.Assignment{
			query.NewAssignment(auditActorIDRef, query.String(event.ActorID())),
			query.NewAssignment(auditModelRef, query.String(event.Model())),
			query.NewAssignment(auditObjectIDRef, query.String(strconv.FormatInt(event.ObjectID(), 10))),
			query.NewAssignment(auditActionRef, query.String(string(event.Action()))),
			query.NewAssignment(auditChangedFieldsRef, query.String(changedFields)),
			query.NewAssignment(auditDisplayLabelRef, query.String(event.DisplayLabel())),
		},
		auditIDRef,
	))
	if err != nil {
		return persistenceFailure("append audit", err)
	}
	if identifier <= 0 {
		return cardinalityFailure("audit", "audit insert returned an invalid sequence")
	}
	return pruneAuditRows(ctx, session, runtime.auditCapacity)
}

// AuditHistory returns at most the newest limit matching events in ascending
// durable sequence order. Runtime's gate makes the read a consistent bounded
// transaction relative to cooperative append/prune writers.
func (runtime *Runtime) AuditHistory(
	ctx context.Context,
	model string,
	objectID int64,
	limit int,
) ([]admin.AuditEntry, error) {
	if err := runtime.validBackendCall(ctx); err != nil {
		return nil, err
	}
	if limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, &Error{Code: CodeInvalidInput, Field: "limit", Detail: "audit history limit is outside the current profile"}
	}
	if _, err := admin.PrepareEvent("systemstate-history", model, objectID, admin.ActionChange, nil, ""); err != nil {
		return nil, &Error{Code: CodeInvalidInput, Field: "history", Detail: "audit history identity is invalid", Cause: err}
	}
	var result []admin.AuditEntry
	err := runtime.withAtomic(ctx, func(session db.Session) error {
		rows, err := queryAuditRows(ctx, session, model, objectID, limit, query.Descending)
		if err != nil {
			return err
		}
		result = make([]admin.AuditEntry, len(rows))
		for index := range rows {
			entry, err := rows[index].entry()
			if err != nil {
				return err
			}
			result[len(rows)-1-index] = entry
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type persistedAuditRow struct {
	id            int64
	actorID       string
	model         string
	objectID      string
	action        string
	changedFields string
	displayLabel  string
}

func (row persistedAuditRow) entry() (admin.AuditEntry, error) {
	objectID, err := strconv.ParseInt(row.objectID, 10, 64)
	if row.id <= 0 || err != nil || objectID <= 0 || strconv.FormatInt(objectID, 10) != row.objectID ||
		len(row.actorID) > auditActorIDMaxLength || len(row.model) > auditModelMaxLength ||
		len(row.objectID) > auditObjectIDMaxLength || len(row.action) > auditActionMaxLength ||
		len(row.changedFields) > auditChangedFieldsMaxLength || len(row.displayLabel) > auditDisplayLabelMaxLength {
		return admin.AuditEntry{}, &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "stored audit row is outside the current profile", Cause: err}
	}
	changedFields, err := decodeAuditChangedFields(row.changedFields)
	if err != nil {
		return admin.AuditEntry{}, err
	}
	event, err := admin.PrepareEvent(
		row.actorID,
		row.model,
		objectID,
		admin.Action(row.action),
		changedFields,
		row.displayLabel,
	)
	if err != nil {
		return admin.AuditEntry{}, &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "stored audit semantics are invalid", Cause: err}
	}
	return admin.AuditEntry{
		Sequence:      uint64(row.id),
		ActorID:       event.ActorID(),
		Model:         event.Model(),
		ObjectID:      event.ObjectID(),
		Action:        event.Action(),
		ChangedFields: event.ChangedFields(),
		DisplayLabel:  event.DisplayLabel(),
	}, nil
}

func inspectAuditTable(ctx context.Context, queryer db.Queryer, capacity int) (bool, error) {
	plan := query.NewPlan(auditTableName, auditFieldRefs).
		WithOrderings(query.NewOrdering(auditIDRef, query.Ascending))
	plan, err := plan.WithLimit(capacity + 1)
	if err != nil {
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: err}
	}
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: err}
	}
	if result == nil {
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: errors.New("backend returned nil rows")}
	}

	count := 0
	var previous int64
	for result.Next() {
		if err := ctx.Err(); err != nil {
			_ = result.Close()
			return false, err
		}
		var row persistedAuditRow
		if err := result.Scan(
			&row.id,
			&row.actorID,
			&row.model,
			&row.objectID,
			&row.action,
			&row.changedFields,
			&row.displayLabel,
		); err != nil {
			_ = result.Close()
			return false, &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "stored audit row cannot be decoded", Cause: err}
		}
		count++
		if count > capacity {
			_ = result.Close()
			return false, cardinalityFailure("audit", "stored audit history exceeds configured capacity")
		}
		if row.id <= previous {
			_ = result.Close()
			return false, cardinalityFailure("audit", "stored audit sequences are not strictly increasing")
		}
		if _, err := row.entry(); err != nil {
			_ = result.Close()
			return false, err
		}
		previous = row.id
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return false, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: errors.Join(iterationErr, closeErr)}
	}
	return count != 0, nil
}

func queryAuditRows(
	ctx context.Context,
	queryer db.Queryer,
	model string,
	objectID int64,
	limit int,
	direction query.Direction,
) ([]persistedAuditRow, error) {
	plan := query.NewPlan(auditTableName, auditFieldRefs).
		WithOrderings(query.NewOrdering(auditIDRef, direction))
	var err error
	plan, err = plan.WithLimit(limit)
	if err != nil {
		return nil, persistenceFailure("build audit query", err)
	}
	if model != "" {
		plan = plan.WithConditions(
			query.NewCondition(auditModelRef, query.LookupExact, query.String(model)),
			query.NewCondition(auditObjectIDRef, query.LookupExact, query.String(strconv.FormatInt(objectID, 10))),
		)
	}
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return nil, persistenceFailure("query audit", err)
	}
	if result == nil {
		return nil, persistenceFailure("query audit", errors.New("backend returned nil rows"))
	}
	rows := make([]persistedAuditRow, 0, limit)
	for result.Next() {
		var row persistedAuditRow
		if err := result.Scan(
			&row.id,
			&row.actorID,
			&row.model,
			&row.objectID,
			&row.action,
			&row.changedFields,
			&row.displayLabel,
		); err != nil {
			_ = result.Close()
			return nil, &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "stored audit row cannot be decoded", Cause: err}
		}
		rows = append(rows, row)
		if len(rows) > limit {
			_ = result.Close()
			return nil, cardinalityFailure("audit", "backend returned more rows than the bounded audit plan")
		}
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return nil, persistenceFailure("iterate audit", errors.Join(iterationErr, closeErr))
	}
	return rows, nil
}

func pruneAuditRows(ctx context.Context, session db.Session, capacity int) error {
	plan := query.NewPlan(auditTableName, []query.FieldRef{auditIDRef}).
		WithOrderings(query.NewOrdering(auditIDRef, query.Descending))
	plan, err := plan.WithLimit(capacity + 1)
	if err != nil {
		return persistenceFailure("build audit prune query", err)
	}
	rows, err := session.Query(ctx, plan)
	if err != nil {
		if rows != nil {
			_ = rows.Close()
		}
		return persistenceFailure("query audit prune", err)
	}
	if rows == nil {
		return persistenceFailure("query audit prune", errors.New("backend returned nil rows"))
	}
	identifiers := make([]int64, 0, capacity+1)
	for rows.Next() {
		var identifier int64
		if err := rows.Scan(&identifier); err != nil {
			_ = rows.Close()
			return &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "audit sequence cannot be decoded", Cause: err}
		}
		if identifier <= 0 {
			_ = rows.Close()
			return &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "audit sequence is invalid"}
		}
		identifiers = append(identifiers, identifier)
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		return persistenceFailure("iterate audit prune", errors.Join(iterationErr, closeErr))
	}
	for _, identifier := range identifiers[minimumInt(capacity, len(identifiers)):] {
		affected, err := session.Delete(ctx, query.NewDeletePlan(auditTableName, auditIDRef, query.Integer(identifier)))
		if err != nil {
			return persistenceFailure("prune audit", err)
		}
		if affected != 1 {
			return cardinalityFailure("audit", fmt.Sprintf("audit prune affected %d rows, want 1", affected))
		}
	}
	return nil
}

func minimumInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
