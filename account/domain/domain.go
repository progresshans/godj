// Package domain stores the account ownership selected by a project. Its
// append-only history has no foreign key to a particular account model.
// Authentication and management must check it in the same coherent database
// scope as the account. These records are metadata, never bearer credentials.
package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
)

var (
	ErrInvalid       = errors.New("account domain: invalid input or scope")
	ErrUninitialized = errors.New("account domain: ownership is not initialized")
	ErrMismatch      = errors.New("account domain: selected role does not match stored ownership")
	ErrStale         = errors.New("account domain: ownership changed or operation was already applied")
	ErrCorrupt       = errors.New("account domain: stored ownership history is invalid")
)

// Change holds one fresh operation ID and ownership generation. Prepare it
// before entering the write fence; retain its OperationID for reconciliation.
// A change is not authorization and does not prove that a mapping was executed.
type Change struct{ state *changeState }

type changeState struct {
	operationID string
	role        string
	generation  [sha256.Size]byte
	evidence    string
}

// Prepare identifies the exact account role and the host's canonical transition
// evidence (for example a bootstrap declaration or a reviewed mapping plan).
// Both arguments are nonzero lowercase SHA-256 hex strings. This function does
// no database I/O, mapping, authentication or permission checks.
func Prepare(ctx context.Context, roleFingerprint, evidenceSHA256 string) (Change, error) {
	return prepare(ctx, "", roleFingerprint, evidenceSHA256)
}

// PrepareOperation retains a caller-created operation ID for process-boundary
// reconciliation. It still creates a fresh generation and performs no lookup,
// retry or adoption. Reusing an operation does not recover its earlier Change;
// after an uncertain write, reconcile the exact receipt and fence first.
func PrepareOperation(ctx context.Context, operationID, roleFingerprint, evidenceSHA256 string) (Change, error) {
	if _, ok := digest(operationID); !ok {
		return Change{}, ErrInvalid
	}
	return prepare(ctx, operationID, roleFingerprint, evidenceSHA256)
}

func prepare(ctx context.Context, operationID, roleFingerprint, evidenceSHA256 string) (Change, error) {
	if err := checkContext(ctx); err != nil {
		return Change{}, err
	}
	if _, ok := digest(roleFingerprint); !ok {
		return Change{}, ErrInvalid
	}
	if _, ok := digest(evidenceSHA256); !ok {
		return Change{}, ErrInvalid
	}
	var entropy [2 * sha256.Size]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return Change{}, failure(err)
	}
	var generation [sha256.Size]byte
	copy(generation[:], entropy[sha256.Size:])
	operation := hex.EncodeToString(entropy[:sha256.Size])
	if operationID != "" {
		operation = operationID
	}
	if generation == ([sha256.Size]byte{}) {
		return Change{}, ErrInvalid
	}
	if _, ok := digest(operation); !ok {
		return Change{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Change{}, err
	}
	return Change{state: &changeState{operationID: operation, role: roleFingerprint, generation: generation, evidence: evidenceSHA256}}, nil
}

func (change Change) OperationID() string {
	if change.state == nil {
		return ""
	}
	return change.state.operationID
}

// Generation is public ownership metadata. Before commit it is only a
// candidate namespace, never current ownership or authentication authority.
func (change Change) Generation() [sha256.Size]byte {
	if change.state == nil {
		return [sha256.Size]byte{}
	}
	return change.state.generation
}

// Receipt is a detached observation. Receipts returned inside a borrowed write
// scope are provisional until its owner confirms commit. Historical receipts
// are not current authority; RequireIn always reads current stored ownership.
type Receipt struct{ state *receiptState }

type receiptState struct {
	id            int64
	sequence      int64
	previousID    int64
	change        changeState
	at            time.Time
	mappingCount  int64
	mappingSHA256 string
}

func (receipt Receipt) OperationID() string {
	if receipt.state == nil {
		return ""
	}
	return receipt.state.change.operationID
}
func (receipt Receipt) RoleFingerprint() string {
	if receipt.state == nil {
		return ""
	}
	return receipt.state.change.role
}
func (receipt Receipt) Generation() [sha256.Size]byte {
	if receipt.state == nil {
		return [sha256.Size]byte{}
	}
	return receipt.state.change.generation
}
func (receipt Receipt) EvidenceSHA256() string {
	if receipt.state == nil {
		return ""
	}
	return receipt.state.change.evidence
}
func (receipt Receipt) Sequence() int64 {
	if receipt.state == nil {
		return 0
	}
	return receipt.state.sequence
}
func (receipt Receipt) At() time.Time {
	if receipt.state == nil {
		return time.Time{}
	}
	return receipt.state.at
}

func (receipt Receipt) MappingCount() int {
	if receipt.state == nil {
		return 0
	}
	return int(receipt.state.mappingCount)
}
func (receipt Receipt) MappingSHA256() string {
	if receipt.state == nil {
		return ""
	}
	return receipt.state.mappingSHA256
}

// Follows compares two receipts observed in the same database scope.
// Sequence numbers alone do not identify a predecessor. This is evidence
// comparison, not a current-domain check or permission to apply a transition.
func (receipt Receipt) Follows(previous Receipt) bool {
	return receipt.state != nil && previous.state != nil && receipt.state.previousID == previous.state.id &&
		receipt.state.sequence-1 == previous.state.sequence && receipt.state.change.generation != previous.state.change.generation
}

// Read publishes only after one successful native read snapshot has closed.
// It does not hold that snapshot open for subsequent account operations.
func Read(ctx context.Context, backend db.SnapshotReader, roleFingerprint string) (Receipt, error) {
	if _, ok := digest(roleFingerprint); !ok {
		return Receipt{}, ErrInvalid
	}
	return readSnapshot(ctx, backend, func(reader db.Queryer) (Receipt, error) {
		return RequireIn(ctx, reader, roleFingerprint)
	})
}

// RequireIn verifies the selected role inside the caller's stable read snapshot
// or coordinated write scope. Callers must use that same handle for all related
// account/permission/session operations and must not cache this as authority.
func RequireIn(ctx context.Context, reader db.Queryer, roleFingerprint string) (Receipt, error) {
	if _, ok := digest(roleFingerprint); !ok {
		return Receipt{}, ErrInvalid
	}
	receipt, err := currentIn(ctx, reader)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.RoleFingerprint() != roleFingerprint {
		return Receipt{}, ErrMismatch
	}
	return receipt, nil
}

// Lookup reads an exact operation receipt, including one superseded by a later
// transition. A missing record is ErrUninitialized, not a confirmed rollback:
// after an unknown write outcome the host must also reconcile its write fence.
// This is header/predecessor metadata; a mapped operation's correspondence
// must additionally pass LookupMappingsIn in the same snapshot used by its owner.
func Lookup(ctx context.Context, backend db.SnapshotReader, operationID string) (Receipt, error) {
	if err := checkContext(ctx); err != nil {
		return Receipt{}, err
	}
	if nilValue(backend) {
		return Receipt{}, ErrInvalid
	}
	if _, ok := digest(operationID); !ok {
		return Receipt{}, ErrInvalid
	}
	type observation struct {
		receipt Receipt
		found   bool
	}
	result, err := db.ReadSnapshotResult(ctx, backend, func(work context.Context, reader db.Queryer) (observation, error) {
		receipt, found, err := LookupIn(work, reader, operationID)
		return observation{receipt, found}, err
	})
	if err != nil {
		return Receipt{}, failure(err)
	}
	if !result.found {
		return Receipt{}, ErrUninitialized
	}
	return result.receipt, nil
}

// InitializeIn records the first ownership. The host must verify an eligible
// empty/explicitly adopted account domain and perform provisioning, source
// retirement, session policy and audit in THIS coordinated transaction. This
// low-level operation does not infer any of those permissions from empty history.
func InitializeIn(ctx context.Context, transaction db.Session, change Change, at time.Time) (Receipt, error) {
	if err := validateWrite(ctx, transaction, change, at); err != nil {
		return Receipt{}, err
	}
	_, err := currentIn(ctx, transaction)
	if err == nil {
		return Receipt{}, ErrStale
	}
	// Only a successfully observed empty history permits initialization. A
	// backend error wrapping the same sentinel is still a failed observation.
	if err != ErrUninitialized {
		return Receipt{}, err
	}
	return appendIn(ctx, transaction, Receipt{}, change, at)
}

// TransitionIn compares the entire expected receipt under the caller's native
// coordination fence and appends one fresh generation. Even returning to the
// same model requires a new generation. Mapping, revocation, retirement and
// audit must share this transaction; errors require rollback, never a partial
// commit or an automatic retry. It does not commit or publish an account.
func TransitionIn(ctx context.Context, transaction db.Session, expected Receipt, change Change, at time.Time) (Receipt, error) {
	return transitionIn(ctx, transaction, expected, change, at, nil)
}

// TransitionMappedIn appends ownership and its exact native key mapping in the
// same borrowed transaction. The mapping is data, never authorization. Target
// creation, source retirement, session policy and audit remain the outer
// owner's responsibility. Propagate every error; no partial receipt is valid.
func TransitionMappedIn(ctx context.Context, transaction db.Session, expected Receipt, change Change, at time.Time, mappings []Mapping) (Receipt, error) {
	if err := validateWrite(ctx, transaction, change, at); err != nil {
		return Receipt{}, err
	}
	prepared, err := prepareMappings(mappings)
	if err != nil {
		return Receipt{}, err
	}
	return transitionIn(ctx, transaction, expected, change, at, &prepared)
}

func transitionIn(ctx context.Context, transaction db.Session, expected Receipt, change Change, at time.Time, mappings *preparedMappings) (Receipt, error) {
	if err := validateWrite(ctx, transaction, change, at); err != nil {
		return Receipt{}, err
	}
	if expected.state == nil {
		return Receipt{}, ErrInvalid
	}
	current, err := currentIn(ctx, transaction)
	if err != nil {
		return Receipt{}, err
	}
	if *current.state != *expected.state || current.Sequence() == math.MaxInt64 {
		return Receipt{}, ErrStale
	}
	return appendMappedIn(ctx, transaction, current, change, at, mappings)
}

func appendIn(ctx context.Context, transaction db.Session, prior Receipt, change Change, at time.Time) (Receipt, error) {
	return appendMappedIn(ctx, transaction, prior, change, at, nil)
}

func appendMappedIn(ctx context.Context, transaction db.Session, prior Receipt, change Change, at time.Time, mappings *preparedMappings) (Receipt, error) {
	// Reusing an already committed preparation is an explicit stale error; do
	// not return the earlier receipt as if this transaction applied the mapping.
	exists, err := models.TransitionObjects.Using(transaction).Filter(models.TransitionFields.OperationID.Exact(change.OperationID())).Exists(ctx)
	if err != nil {
		return Receipt{}, failure(err)
	}
	if exists {
		return Receipt{}, ErrStale
	}
	input := models.NewTransitionCreate(change.OperationID(), prior.Sequence()+1, change.state.role,
		hex.EncodeToString(change.state.generation[:]), change.state.evidence, at.UTC().Truncate(time.Microsecond))
	if prior.state != nil {
		input = input.WithPreviousID(prior.state.id)
	}
	if mappings != nil {
		input = input.WithMappingCount(int64(len(mappings.rows))).WithMappingSHA256(mappings.digest)
	}
	row, err := models.TransitionObjects.Create(ctx, transaction, input)
	if err != nil {
		return Receipt{}, failure(err)
	}
	receipt, err := fromRow(row)
	if err != nil {
		return Receipt{}, err
	}
	if mappings != nil {
		stored, err := models.MappingObjects.Create(ctx, transaction, models.NewMappingCreate(row.ID, mappings.payload))
		if err != nil {
			return Receipt{}, failure(err)
		}
		if stored.ID <= 0 {
			return Receipt{}, ErrCorrupt
		}
		actual, err := LookupMappingsIn(ctx, transaction, receipt)
		if err != nil {
			return Receipt{}, err
		}
		if !slices.Equal(actual, mappings.rows) {
			return Receipt{}, ErrCorrupt
		}
	}
	return receipt, nil
}

func currentIn(ctx context.Context, reader db.Queryer) (Receipt, error) {
	if err := checkContext(ctx); err != nil {
		return Receipt{}, err
	}
	if nilValue(reader) {
		return Receipt{}, ErrInvalid
	}
	rowsQuery, err := models.TransitionObjects.Using(reader).OrderBy(models.TransitionFields.Sequence.Desc()).Limit(2)
	if err != nil {
		return Receipt{}, failure(err)
	}
	rows, err := rowsQuery.All(ctx)
	if err != nil {
		return Receipt{}, failure(err)
	}
	if len(rows) == 0 {
		return Receipt{}, ErrUninitialized
	}
	receipt, err := fromRow(rows[0])
	if err != nil {
		return Receipt{}, err
	}
	if rows[0].Sequence == 1 {
		if len(rows) != 1 {
			return Receipt{}, ErrCorrupt
		}
	} else if len(rows) != 2 || validatePredecessor(rows[0], rows[1]) != nil {
		return Receipt{}, ErrCorrupt
	}
	return receipt, nil
}

func fromRow(row models.Transition) (Receipt, error) {
	generation, ok := digest(row.Generation)
	at := row.TransitionedAt.UTC()
	if !ok || row.ID <= 0 || row.Sequence <= 0 || at.IsZero() || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 {
		return Receipt{}, ErrCorrupt
	}
	for _, value := range []string{row.OperationID, row.RoleFingerprint, row.EvidenceSHA256} {
		if _, ok := digest(value); !ok {
			return Receipt{}, ErrCorrupt
		}
	}
	if row.MappingCount < 0 || row.MappingCount > MaximumMappings || row.MappingCount == 0 && row.MappingSHA256 != "" {
		return Receipt{}, ErrCorrupt
	}
	if row.MappingCount > 0 {
		if _, ok := digest(row.MappingSHA256); !ok {
			return Receipt{}, ErrCorrupt
		}
	}
	previousID := int64(0)
	if row.PreviousID != nil {
		previousID = *row.PreviousID
	}
	if row.Sequence == 1 && row.PreviousID != nil || row.Sequence > 1 && (previousID <= 0 || previousID == row.ID) {
		return Receipt{}, ErrCorrupt
	}
	return Receipt{state: &receiptState{id: row.ID, sequence: row.Sequence, previousID: previousID,
		change:       changeState{operationID: row.OperationID, role: row.RoleFingerprint, generation: generation, evidence: row.EvidenceSHA256},
		at:           at,
		mappingCount: row.MappingCount, mappingSHA256: row.MappingSHA256,
	}}, nil
}

func validatePredecessor(current, prior models.Transition) error {
	if _, err := fromRow(prior); err != nil {
		return err
	}
	if current.PreviousID == nil || *current.PreviousID != prior.ID || current.Sequence-1 != prior.Sequence || current.Generation == prior.Generation {
		return ErrCorrupt
	}
	return nil
}

func validateWrite(ctx context.Context, transaction db.Session, change Change, at time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	at = at.UTC().Truncate(time.Microsecond)
	if nilValue(transaction) || change.state == nil || at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return ErrInvalid
	}
	validator, ok := transaction.(db.SessionValidator)
	if !ok {
		return ErrInvalid
	}
	return validator.ValidateSession(ctx)
}

func readSnapshot(ctx context.Context, backend db.SnapshotReader, read func(db.Queryer) (Receipt, error)) (Receipt, error) {
	if err := checkContext(ctx); err != nil {
		return Receipt{}, err
	}
	if nilValue(backend) {
		return Receipt{}, ErrInvalid
	}
	var candidate Receipt
	var callbackErr error
	calls := 0
	err := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilValue(reader) {
			callbackErr = errors.Join(callbackErr, ErrInvalid)
			return callbackErr
		}
		candidate, callbackErr = read(reader)
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return Receipt{}, failure(err)
	}
	if calls != 1 {
		return Receipt{}, ErrInvalid
	}
	return candidate, nil
}

func digest(value string) ([sha256.Size]byte, bool) {
	var result [sha256.Size]byte
	if len(value) != sha256.Size*2 {
		return result, false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return result, false
		}
	}
	_, err := hex.Decode(result[:], []byte(value))
	return result, err == nil && result != ([sha256.Size]byte{})
}

func checkContext(ctx context.Context) error {
	if nilValue(ctx) {
		return ErrInvalid
	}
	return ctx.Err()
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}

// Keep backend diagnostics inspectable without formatting their private values.
type storageError struct{ cause error }

func (storageError) Error() string                    { return "account domain: storage operation failed" }
func (e storageError) Unwrap() error                  { return e.cause }
func (e storageError) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(e.Error())) }
func failure(err error) error                         { return storageError{cause: err} }
