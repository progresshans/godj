package systemstate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
)

const transferredCredentialDigest = "sha256:eb4cdcd8f0419b8be2e1d67452037d27eba80ef0e08e8ecc9f5cbd686d4077a5"
const transferredPassword = "!godj-identity-transferred"

// IdentityTransition is a durable, immutable ownership receipt. Password
// material and its source fingerprint have no public accessor or JSON form.
// The receipt remains meaningful if the adopted user is changed or deleted.
type IdentityTransition struct{ state *identityTransitionState }

type identityTransitionState struct {
	kind             string
	sourceID         int64
	principalID      string
	userID           int64
	staff, superuser bool
	at               time.Time
	fingerprint      string
}

func (r IdentityTransition) value() identityTransitionState {
	if r.state == nil {
		return identityTransitionState{}
	}
	return *r.state
}

func (IdentityTransition) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.IdentityTransition{redacted}"))
}

func (IdentityTransition) String() string        { return "systemstate.IdentityTransition{redacted}" }
func (IdentityTransition) GoString() string      { return "systemstate.IdentityTransition{redacted}" }
func (r IdentityTransition) Kind() string        { return r.value().kind }
func (r IdentityTransition) PrincipalID() string { return r.value().principalID }
func (r IdentityTransition) UserID() int64       { return r.value().userID }
func (r IdentityTransition) At() time.Time       { return r.value().at }

// AdoptOperatorConfig selects roles explicitly; they are never inferred from
// an old permission name. DateJoined records the adoption instant because the
// old credential has no joined timestamp. A zero value uses the current UTC
// instant. Expected is the exact old persisted credential policy.
type AdoptOperatorConfig struct {
	Expected         RuntimeConfig
	Staff, Superuser bool
	DateJoined       time.Time
}

func (AdoptOperatorConfig) String() string   { return "systemstate.AdoptOperatorConfig{redacted}" }
func (AdoptOperatorConfig) GoString() string { return "systemstate.AdoptOperatorConfig{redacted}" }

// AdoptOperator moves one verified operator into the identity app in one
// coordinated transaction. Existing session/audit rows remain byte-for-byte
// unchanged. The old row becomes a non-authenticating tombstone, so even an
// older binary cannot provision/authenticate it again. No schema migration,
// password derivation, retry, policy widening or implicit adoption occurs.
// On any error, including unknown commit, no receipt is published; inspect the
// durable transition explicitly before deciding what happened.
func AdoptOperator(ctx context.Context, backend Backend, config AdoptOperatorConfig) (result IdentityTransition, resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
		if resultErr != nil {
			result = IdentityTransition{}
		}
	}()
	if err := validateSystemStateCall(ctx, backend); err != nil {
		return result, err
	}
	policy, err := validateCredentialPolicy(config.Expected.CredentialPolicy)
	if err != nil {
		return result, err
	}
	at, err := transitionInstant(config.DateJoined)
	if err != nil {
		return result, err
	}
	inspection := &Runtime{backend: backend}
	sessionStore, err := newDurableSessionStore(inspection, config.Expected.SessionLimits, config.Expected.MaxSessions)
	if err != nil {
		return result, err
	}
	auditCapacity, err := normalizeAuditCapacity(config.Expected.AuditCapacity)
	if err != nil {
		return result, err
	}
	if err := requireIdentityMigrations(ctx, backend); err != nil {
		return result, err
	}
	var callbackState operatorErrorSnapshot
	var callbackFailure error
	calls := 0
	err = backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || isNilInterface(session) {
			callbackFailure = identityCallbackFailure()
			return callbackFailure
		}
		failure := func() error {
			receipts, err := readIdentityTransitions(ctx, session)
			if err != nil {
				return err
			}
			if len(receipts) != 0 {
				return identityInitializedError()
			}
			if _, err := inspectSessionTable(ctx, session, sessionStore.limits, sessionStore.maxRecords); err != nil {
				return err
			}
			if _, err := inspectAuditTable(ctx, session, auditCapacity); err != nil {
				return err
			}
			rows, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return &Error{Code: CodeCredentialAbsent, Field: "credential", Detail: "there is no operator to adopt"}
			}
			if len(rows) != 1 {
				return credentialCardinalityError()
			}
			source := rows[0]
			credential, err := validateStoredCredential(source, policy)
			if err != nil {
				return err
			}
			// The source profile is checked without deriving or exposing a password.
			created, err := createIdentityUser(ctx, session, source.principalID, source.username, source.encodedPassword, source.active, config.Staff, config.Superuser, credential.Principal().Permissions(), at)
			if err != nil {
				return err
			}
			candidate := IdentityTransition{state: &identityTransitionState{kind: "operator", sourceID: source.id, principalID: source.principalID, userID: created.ID, staff: config.Staff, superuser: config.Superuser, at: at, fingerprint: credentialFingerprint(source)}}
			if err := writeIdentityTransition(ctx, session, candidate); err != nil {
				return err
			}
			empty, err := encodePermissions(nil)
			if err != nil {
				return err
			}
			affected, err := session.Update(ctx, query.NewUpdatePlan(credentialTableName, []query.Assignment{
				query.NewAssignment(credentialEncodedPasswordRef, query.String(transferredPassword)),
				query.NewAssignment(credentialActiveRef, query.Boolean(false)),
				query.NewAssignment(credentialPermissionsRef, query.String(empty)),
				query.NewAssignment(credentialDefinitionDigestRef, query.String(transferredCredentialDigest)),
			}, credentialIDRef, query.Integer(source.id)))
			if err != nil {
				return persistenceFailure("retire adopted operator credential", err)
			}
			if affected != 1 {
				return cardinalityFailure("credential", "adoption did not retire exactly one source row")
			}
			result = candidate
			return ctx.Err()
		}()
		callbackFailure = failure
		callbackState = snapshotOperatorError(failure)
		return failure
	})
	err = errors.Join(err, callbackFailure)
	if err != nil {
		return IdentityTransition{}, redactOperatorAtomicFailure(err, callbackState)
	}
	if calls != 1 || result.UserID() == 0 {
		return IdentityTransition{}, identityCallbackFailure()
	}
	return result, nil
}

func createIdentityUser(ctx context.Context, session db.Session, id, username, encoded string, active, staff, superuser bool, permissions []auth.Permission, at time.Time) (models.User, error) {
	// Creation deliberately relies on the native unique constraints as well as
	// the coordinated fence. A colliding non-cooperative writer is not adopted.
	user, err := models.UserObjects.Create(ctx, session, models.NewUserCreate(id, username, encoded, at).WithActive(active).WithStaff(staff).WithSuperuser(superuser))
	if err != nil {
		return models.User{}, persistenceFailure("create identity user", err)
	}
	for _, code := range permissions {
		permission, found, err := models.PermissionObjects.Using(session).Filter(models.PermissionFields.Code.Exact(string(code))).OrderBy(models.PermissionFields.ID.Asc()).First(ctx)
		if err != nil {
			return models.User{}, persistenceFailure("read identity permission", err)
		}
		if !found {
			permission, err = models.PermissionObjects.Create(ctx, session, models.NewPermissionCreate(string(code), string(code)))
			if err != nil {
				return models.User{}, persistenceFailure("create identity permission", err)
			}
		}
		if _, err := models.UserPermissionsLinkObjects.Create(ctx, session, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
			return models.User{}, persistenceFailure("grant adopted identity permission", err)
		}
	}
	return user, nil
}

func transitionInstant(at time.Time) (time.Time, error) {
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC().Truncate(time.Microsecond)
	if at.Year() < 1 || at.Year() > 9999 {
		return time.Time{}, &Error{Code: CodeInvalidConfig, Field: "date_joined", Detail: "identity transition instant is outside the supported range"}
	}
	return at, nil
}

func credentialFingerprint(row credentialRow) string {
	// Byte-length framing preserves exact source bytes, including a custom
	// hasher's encoding. JSON replacement of invalid UTF-8 must not alias two
	// distinct credentials. The digest is a private receipt, not a bearer token.
	digest := sha256.New()
	_, _ = digest.Write([]byte("godj.identity.transition.v1\x00"))
	var length [8]byte
	for _, value := range []string{strconv.FormatInt(row.id, 10), row.principalID, row.username, row.encodedPassword, strconv.FormatBool(row.active), row.permissions, row.definitionDigest} {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

var transitionFields = []query.FieldRef{
	query.NewFieldRef("id", "id", query.FieldInteger, false),
	query.NewFieldRef("source_kind", "source_kind", query.FieldString, false),
	query.NewFieldRef("source_id", "source_id", query.FieldInteger, false),
	query.NewFieldRef("principal_id", "principal_id", query.FieldString, false),
	query.NewFieldRef("user_id", "user_id", query.FieldInteger, false),
	query.NewFieldRef("staff", "staff", query.FieldBoolean, false),
	query.NewFieldRef("superuser", "superuser", query.FieldBoolean, false),
	query.NewFieldRef("transitioned_at", "transitioned_at", query.FieldDateTime, false),
	query.NewFieldRef("source_fingerprint", "source_fingerprint", query.FieldString, false),
}

func readIdentityTransitions(ctx context.Context, reader db.Queryer) ([]IdentityTransition, error) {
	plan, err := query.NewPlan(identityTransitionTable, transitionFields).WithOrderings(query.NewOrdering(transitionFields[0], query.Ascending)).WithLimit(2)
	if err != nil {
		return nil, err
	}
	var result []IdentityTransition
	err = scanRows(ctx, reader, plan, schemaRowsFailure(identityTransitionTable), func(rows db.Rows) (bool, error) {
		var id int64
		row := IdentityTransition{state: &identityTransitionState{}}
		if err := rows.Scan(&id, &row.state.kind, &row.state.sourceID, &row.state.principalID, &row.state.userID, &row.state.staff, &row.state.superuser, &row.state.at, &row.state.fingerprint); err != nil {
			return false, persistenceFailure("decode identity transition", err)
		}
		if err := validateIdentityTransition(id, row); err != nil {
			return false, err
		}
		result = append(result, row)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if len(result) > 1 {
		return nil, cardinalityFailure("identity_transition", "identity transition must have at most one row")
	}
	return result, nil
}

func validateIdentityTransition(id int64, row IdentityTransition) error {
	fingerprint, err := hex.DecodeString(row.state.fingerprint)
	if id <= 0 || row.state.userID <= 0 || row.state.sourceID < 0 || (row.state.kind != "operator" && row.state.kind != "bootstrap") || row.state.kind == "operator" && row.state.sourceID == 0 || row.state.kind == "bootstrap" && row.state.sourceID != 0 || err != nil || len(fingerprint) != 32 || hex.EncodeToString(fingerprint) != row.state.fingerprint || row.state.at.IsZero() {
		return &Error{Code: CodeCorruptState, Field: "identity_transition", Detail: "stored identity transition is invalid"}
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.state.principalID}); err != nil {
		return &Error{Code: CodeCorruptState, Field: "identity_transition", Detail: "stored identity transition principal is invalid", Cause: err}
	}
	return nil
}

func writeIdentityTransition(ctx context.Context, session db.Session, row IdentityTransition) error {
	values := []query.Value{query.String(row.state.kind), query.Integer(row.state.sourceID), query.String(row.state.principalID), query.Integer(row.state.userID), query.Boolean(row.state.staff), query.Boolean(row.state.superuser), query.DateTime(row.state.at), query.String(row.state.fingerprint)}
	assignments := make([]query.Assignment, len(values))
	for index, value := range values {
		assignments[index] = query.NewAssignment(transitionFields[index+1], value)
	}
	id, err := session.Insert(ctx, query.NewInsertPlanReturningKey(identityTransitionTable, assignments, transitionFields[0]))
	if err != nil {
		return persistenceFailure("record identity transition", err)
	}
	if id <= 0 {
		return cardinalityFailure("identity_transition", "identity transition insert returned an invalid key")
	}
	return nil
}

func identityInitializedError() error {
	return &Error{Code: CodeIdentityAlreadyInitialized, Field: "identity_transition", Detail: "identity ownership has already been initialized; inspect the existing receipt"}
}

func requireIdentityMigrations(ctx context.Context, backend Backend) error {
	rows, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		return schemaRowsFailure("migration_history")(err)
	}
	system, identityCount, initial, transition := 0, 0, 0, 0
	for _, row := range rows {
		if row.App == initialMigrationApp {
			system++
			if row.Name == initialMigrationName {
				initial++
			}
			if row.Name == identityMigrationName {
				transition++
			}
		}
		if row.App == identity.InitialMigrationKey().App {
			identityCount++
			if row.Name != identity.InitialMigrationKey().Name {
				return schemaRowsFailure("migration_history")(nil)
			}
		}
	}
	if system != 2 || initial != 1 || transition != 1 || identityCount != 1 {
		return &Error{Code: CodeSchemaUnavailable, Field: "migration_history", Detail: "exact identity/system migration graph is not applied"}
	}
	return nil
}

func identityCallbackFailure() error {
	return &Error{Code: CodePersistence, Field: "identity_transition", Detail: "identity backend did not execute one successful callback"}
}
