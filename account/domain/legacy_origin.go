package domain

import (
	"context"
	"errors"
	"time"

	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
)

// LegacyIdentityOrigin describes the exact historical systemstate inventory
// adopted by the FIRST selected-account owner. It contains only counts and
// digests, never credentials, session payloads or historical display values.
// The handoff owner verifies the actual rows, current authority, retirement and
// unchanged identities in the same transaction. This evidence grants nothing.
type LegacyIdentityOrigin struct {
	SchemaSHA256   string `json:"schema_sha256"`
	ProjectSHA256  string `json:"project_sha256"`
	OwnerSHA256    string `json:"owner_sha256"`
	AccountsSHA256 string `json:"accounts_sha256"`
	CatalogSHA256  string `json:"catalog_sha256"`
	SessionsSHA256 string `json:"sessions_sha256"`
	SessionRecords int    `json:"session_records"`
	AuditSHA256    string `json:"audit_sha256"`
	AuditEvents    int    `json:"audit_events"`
}

func normalizeLegacyOrigin(origin *LegacyIdentityOrigin, rows []Mapping, catalog *CatalogMappings, collections []CollectionIdentity) (*LegacyIdentityOrigin, error) {
	if origin == nil {
		return nil, nil
	}
	if catalog == nil || len(collections) != 0 || origin.SessionRecords < 0 || origin.SessionRecords > 1<<16 || origin.AuditEvents < 0 || origin.AuditEvents > 1_000_000 {
		return nil, ErrInvalid
	}
	for _, value := range []string{origin.SchemaSHA256, origin.ProjectSHA256, origin.OwnerSHA256, origin.AccountsSHA256, origin.CatalogSHA256, origin.SessionsSHA256, origin.AuditSHA256} {
		if _, valid := digest(value); !valid {
			return nil, ErrInvalid
		}
	}
	// This operation adopts existing identities. Creating or remapping them
	// belongs to a later explicit typed model transition with its own receipt.
	for _, namespace := range [][]Mapping{rows, catalog.Groups, catalog.Permissions} {
		for _, row := range namespace {
			if row.SourceID != row.TargetID || row.SourceRevision != row.TargetRevision {
				return nil, ErrInvalid
			}
		}
	}
	owned := *origin
	return &owned, nil
}

// InitializeAdoptedIn records a reviewed historical identity handoff. It owns
// neither authorization nor legacy retirement. Those effects, the unchanged
// rows and this receipt MUST share one coordinated transaction. Errors require
// rollback and no receipt is externally valid before that owner commits.
func InitializeAdoptedIn(ctx context.Context, transaction db.Session, change Change, at time.Time, inventory MappingInventory) (Receipt, error) {
	if err := validateWrite(ctx, transaction, change, at); err != nil {
		return Receipt{}, err
	}
	if inventory.Legacy == nil {
		return Receipt{}, ErrInvalid
	}
	prepared, err := prepareMappingSet(inventory)
	if err != nil {
		return Receipt{}, err
	}
	_, err = InspectIn(ctx, transaction)
	if err == nil {
		return Receipt{}, ErrStale
	}
	if err != ErrUninitialized {
		return Receipt{}, err
	}
	return appendMappedIn(ctx, transaction, Receipt{}, change, at, &prepared)
}

// InitialLegacyOriginIn reads the immutable first receipt and its complete
// canonical correspondence. Later model transitions do not replace its origin.
// It does not inspect legacy tables or authorize adoption, startup or history.
func InitialLegacyOriginIn(ctx context.Context, reader db.Queryer) (Receipt, LegacyIdentityOrigin, bool, error) {
	var zero LegacyIdentityOrigin
	validator, err := lookupScope(ctx, reader)
	if err != nil {
		return Receipt{}, zero, false, err
	}
	selected, err := models.TransitionObjects.Using(reader).Filter(models.TransitionFields.Sequence.Exact(1)).OrderBy(models.TransitionFields.ID.Asc()).Limit(2)
	if err != nil {
		return Receipt{}, zero, false, failure(err)
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return Receipt{}, zero, false, failure(err)
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return Receipt{}, zero, false, failure(err)
	}
	if len(rows) == 0 {
		return Receipt{}, zero, false, nil
	}
	if len(rows) != 1 {
		return Receipt{}, zero, false, ErrCorrupt
	}
	receipt, err := fromRow(rows[0])
	if err != nil {
		return Receipt{}, zero, false, err
	}
	inventory, err := lookupMappingSetIn(ctx, reader, receipt)
	if err != nil {
		return Receipt{}, zero, false, err
	}
	if inventory.legacy == nil {
		return receipt, zero, false, nil
	}
	return receipt, *inventory.legacy, true, nil
}
