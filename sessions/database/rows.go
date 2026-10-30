package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/sessions/database/models"
)

const digestDomain = "godj.sessions.database.v1\x00"

func bearerDigest(namespace string, id sessions.ID) (string, error) {
	if !id.Valid() {
		return "", failure(sessions.CodeInvalidInput, nil)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(digestDomain))
	_, _ = hash.Write([]byte(namespace))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(id.Encoded()))
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range []byte(value) {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (store *Store) lookup(ctx context.Context, reader db.Queryer, digest string) (models.Session, bool, error) {
	source := models.SessionObjects.Using(reader).Filter(models.SessionFields.Namespace.Exact(store.state.namespace), models.SessionFields.Digest.Exact(digest)).OrderBy(models.SessionFields.ID.Asc())
	bounded, err := source.Limit(2)
	if err != nil {
		return models.Session{}, false, err
	}
	rows, err := readRows(ctx, reader, bounded.Plan(), 2)
	if err != nil {
		return models.Session{}, false, err
	}
	if len(rows) == 0 {
		return models.Session{}, false, nil
	}
	if len(rows) != 1 || rows[0].ID <= 0 || rows[0].Namespace != store.state.namespace || rows[0].Digest != digest || !validDigest(rows[0].Digest) || len(rows[0].Payload) > sessionwire.MaximumPayloadBytes {
		return models.Session{}, false, failure(sessions.CodeInvalidRecord, nil)
	}
	return rows[0], true, nil
}

type inventory struct{ total, owned int }
type inventoryRow struct {
	id                int64
	namespace, digest string
}

// Only identity and namespace/digest are selected. Digest ordering exposes a
// duplicate using one previous value in the bounded stream; no unbounded
// payload array or set of every bearer digest is retained.
func (store *Store) inventory(ctx context.Context, reader db.Queryer) (inventory, error) {
	return store.state.database.inventory(ctx, reader, store.state.namespace)
}

func (database *databaseState) inventory(ctx context.Context, reader db.Queryer, namespace string) (inventory, error) {
	var result inventory
	var previous string
	projection := orm.Project3(models.SessionFields.ID, models.SessionFields.Namespace, models.SessionFields.Digest,
		func(id int64, namespace, digest string) inventoryRow { return inventoryRow{id, namespace, digest} })
	selected, err := orm.SelectQuery(models.SessionObjects.Using(reader).OrderBy(models.SessionFields.Digest.Asc(), models.SessionFields.ID.Asc()), projection)
	if err != nil {
		return inventory{}, err
	}
	selected, err = selected.Limit(database.maxRecords + 1)
	if err != nil {
		return inventory{}, err
	}
	err = rowset.Scan(ctx, reader, selected.Plan(), rowFailure, func(rows db.Rows) (bool, error) {
		var row inventoryRow
		if err := rows.Scan(&row.id, &row.namespace, &row.digest); err != nil {
			return false, failure(sessions.CodeInvalidRecord, err)
		}
		if row.id <= 0 || !validNamespace(row.namespace) || !validDigest(row.digest) || previous != "" && strings.Compare(previous, row.digest) >= 0 {
			return false, failure(sessions.CodeInvalidRecord, nil)
		}
		previous = row.digest
		result.total++
		if row.namespace == namespace {
			result.owned++
		}
		if result.total > database.maxRecords {
			return false, failure(sessions.CodeStoreFull, nil)
		}
		return true, nil
	})
	if err != nil {
		return inventory{}, err
	}
	return result, nil
}

// The first keyset page has no lower bound so a corrupt nonpositive storage
// key is rejected instead of skipped. Each rowset is closed before visiting
// rows, permitting same-scope mutation and rollback after a later-page error.
func (store *Store) payloads(ctx context.Context, reader db.Queryer, ownNamespace bool, visit func(models.Session, sessionwire.Metadata) error) (int, error) {
	namespace := ""
	if ownNamespace {
		namespace = store.state.namespace
	}
	return store.state.database.payloads(ctx, reader, namespace, visit)
}

func (database *databaseState) payloads(ctx context.Context, reader db.Queryer, namespace string, visit func(models.Session, sessionwire.Metadata) error) (int, error) {
	count := 0
	var previous int64
	for {
		source := models.SessionObjects.Using(reader).OrderBy(models.SessionFields.ID.Asc())
		if namespace != "" {
			source = source.Filter(models.SessionFields.Namespace.Exact(namespace))
		}
		if count > 0 {
			source = source.Filter(models.SessionFields.ID.GreaterThan(previous))
		}
		bounded, err := source.Limit(ownerBatchSize)
		if err != nil {
			return 0, err
		}
		rows, err := readRows(ctx, reader, bounded.Plan(), ownerBatchSize)
		if err != nil {
			return 0, err
		}
		if len(rows) > ownerBatchSize {
			return 0, failure(sessions.CodeInvalidRecord, nil)
		}
		for _, row := range rows {
			if row.ID <= previous || !validNamespace(row.Namespace) || !validDigest(row.Digest) || namespace != "" && row.Namespace != namespace {
				return 0, failure(sessions.CodeInvalidRecord, nil)
			}
			count++
			if count > database.maxRecords {
				return 0, failure(sessions.CodeStoreFull, nil)
			}
			metadata, err := sessionwire.Inspect(row.Payload, database.limits)
			if err != nil {
				return 0, err
			}
			if visit != nil {
				if err := visit(row, metadata); err != nil {
					return 0, err
				}
			}
			previous = row.ID
		}
		if len(rows) < ownerBatchSize {
			return count, nil
		}
	}
}

func deleteRow(ctx context.Context, scope db.Session, row models.Session) error {
	count, err := models.SessionObjects.Delete(ctx, scope, &row)
	if err != nil {
		return err
	}
	if count != 1 {
		return failure(sessions.CodeInvalidRecord, nil)
	}
	return nil
}

func (store *Store) ensureCapacity(ctx context.Context, scope db.Session, at time.Time) error {
	inventory, err := store.inventory(ctx, scope)
	if err != nil {
		return err
	}
	if inventory.total < store.state.database.maxRecords {
		return nil
	}
	var victim models.Session
	count, err := store.payloads(ctx, scope, false, func(row models.Session, metadata sessionwire.Metadata) error {
		if victim.ID == 0 && metadata.Expired(at) {
			victim = row
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count != inventory.total {
		return failure(sessions.CodeInvalidRecord, nil)
	}
	if victim.ID == 0 {
		return failure(sessions.CodeStoreFull, nil)
	}
	return deleteRow(ctx, scope, victim)
}

// Inspect validates the complete database inventory in one stable snapshot,
// including foreign namespaces. It neither provisions nor cleans up records.
func (store *Store) Inspect(ctx context.Context) (int, error) {
	return readStore(ctx, store, func(work context.Context, reader db.Queryer) (int, error) {
		return store.state.database.inspect(work, reader)
	})
}

func (database *databaseState) inspect(ctx context.Context, reader db.Queryer) (int, error) {
	inventory, err := database.inventory(ctx, reader, "")
	if err != nil {
		return 0, err
	}
	count, err := database.payloads(ctx, reader, "", nil)
	if err != nil {
		return 0, err
	}
	if count != inventory.total {
		return 0, failure(sessions.CodeInvalidRecord, nil)
	}
	return count, nil
}

// Validate each scanned payload before retaining it in the bounded page. A
// malformed oversized row cannot accumulate a whole page of unbounded strings.
func readRows(ctx context.Context, reader db.Queryer, plan query.Plan, maximum int) ([]models.Session, error) {
	var result []models.Session
	err := rowset.Scan(ctx, reader, plan, rowFailure, func(rows db.Rows) (bool, error) {
		row, err := (models.SessionDescriptor{}).Scan(rows)
		if err != nil {
			return false, failure(sessions.CodeInvalidRecord, err)
		}
		if row.ID <= 0 || !validNamespace(row.Namespace) || !validDigest(row.Digest) || len(row.Payload) > sessionwire.MaximumPayloadBytes || len(result) >= maximum {
			return false, failure(sessions.CodeInvalidRecord, nil)
		}
		result = append(result, row)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func rowFailure(err error) error { return failure(sessions.CodeStoreFailure, err) }
