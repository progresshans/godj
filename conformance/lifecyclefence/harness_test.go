package lifecyclefence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	spikeMetadataTable = "godj_migration_fence_spike"
	spikeRecorderTable = "godj_migrations"
)

var (
	errFenceStale       = errors.New("stale migration snapshot")
	errFenceContended   = errors.New("migration fence contended")
	errFenceIntegrity   = errors.New("migration fence integrity failure")
	errFenceUnsupported = errors.New("migration fence unsupported")
)

type migrationIdentity struct {
	app  string
	name string
}

type spikeToken struct {
	initialized bool
	epoch       string
	revision    int64
	historyHash [sha256.Size]byte
}

type historySnapshot struct {
	token      spikeToken
	identities []migrationIdentity
}

type fenceMetadata struct {
	epoch       string
	revision    int64
	historyHash [sha256.Size]byte
}

type stepDirection uint8

const (
	stepApply stepDirection = iota + 1
	stepUnapply
)

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readAtomicSnapshot(ctx context.Context, database *sql.DB) (historySnapshot, error) {
	if ctx == nil {
		return historySnapshot{}, errors.New("snapshot context is nil")
	}
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return historySnapshot{}, classifyFenceIO("begin history snapshot", err)
	}
	defer func() { _ = transaction.Rollback() }()

	identities, err := readCanonicalHistory(ctx, transaction)
	if err != nil {
		return historySnapshot{}, err
	}
	metadata, ready, err := readFenceMetadata(ctx, transaction)
	if err != nil {
		return historySnapshot{}, err
	}
	historyHash := fingerprintHistory(identities)
	snapshot := historySnapshot{identities: identities}
	if ready {
		if metadata.historyHash != historyHash {
			return historySnapshot{}, fmt.Errorf(
				"%w: stored history fingerprint does not match recorder identities",
				errFenceIntegrity,
			)
		}
		snapshot.token = spikeToken{
			initialized: true,
			epoch:       metadata.epoch,
			revision:    metadata.revision,
			historyHash: historyHash,
		}
	} else {
		snapshot.token = spikeToken{historyHash: historyHash}
	}
	if err := transaction.Commit(); err != nil {
		return historySnapshot{}, classifyFenceIO("commit read-only history snapshot", err)
	}
	return snapshot, nil
}

func runFencedStep(
	ctx context.Context,
	database *sql.DB,
	expected historySnapshot,
	step string,
	direction stepDirection,
	bootstrapEpoch string,
	afterClaim func(),
) (historySnapshot, error) {
	identity := migrationIdentity{app: "spike", name: step}
	successor, err := transitionHistory(expected.identities, identity, direction)
	if err != nil {
		return expected, err
	}
	mutation := func(transaction *sql.Tx) error {
		table := stepTable(step)
		switch direction {
		case stepApply:
			if _, err := transaction.ExecContext(ctx, recorderSchemaSQL()); err != nil {
				return classifyFenceIO("create recorder table", err)
			}
			if _, err := transaction.ExecContext(
				ctx,
				fmt.Sprintf(`CREATE TABLE "%s" ("id" INTEGER PRIMARY KEY)`, table),
			); err != nil {
				return classifyFenceIO("apply domain DDL", err)
			}
			_, err := transaction.ExecContext(
				ctx,
				`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
				identity.app,
				identity.name,
			)
			return classifyFenceIO("record applied identity", err)
		case stepUnapply:
			if _, err := transaction.ExecContext(ctx, fmt.Sprintf(`DROP TABLE "%s"`, table)); err != nil {
				return classifyFenceIO("unapply domain DDL", err)
			}
			result, err := transaction.ExecContext(
				ctx,
				`DELETE FROM "godj_migrations" WHERE "app" = ? AND "name" = ?`,
				identity.app,
				identity.name,
			)
			if err != nil {
				return classifyFenceIO("record unapplied identity", err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return classifyFenceIO("count unapplied identity", err)
			}
			if rows != 1 {
				return fmt.Errorf("recorder delete affected %d rows", rows)
			}
			return nil
		default:
			return fmt.Errorf("invalid step direction %d", direction)
		}
	}
	return runFencedMutation(ctx, database, expected, successor, bootstrapEpoch, mutation, afterClaim)
}

func runFencedMutation(
	ctx context.Context,
	database *sql.DB,
	expected historySnapshot,
	successorIdentities []migrationIdentity,
	bootstrapEpoch string,
	mutation func(*sql.Tx) error,
	afterClaim func(),
) (historySnapshot, error) {
	if ctx == nil {
		return expected, errors.New("fenced mutation context is nil")
	}
	expectedCanonical := canonicalHistory(expected.identities)
	if expected.token.historyHash != fingerprintHistory(expectedCanonical) {
		return expected, fmt.Errorf("%w: expected token is not bound to its identities", errFenceIntegrity)
	}
	successorCanonical := canonicalHistory(successorIdentities)
	successorHash := fingerprintHistory(successorCanonical)

	// The writer handle is configured with _txlock=immediate. database/sql pins
	// this transaction to one driver connection through commit/rollback.
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return expected, classifyFenceIO("begin immediate", err)
	}
	defer func() { _ = transaction.Rollback() }()

	currentIdentities, err := readCanonicalHistory(ctx, transaction)
	if err != nil {
		return expected, err
	}
	currentHash := fingerprintHistory(currentIdentities)
	metadata, ready, err := readFenceMetadata(ctx, transaction)
	if err != nil {
		return expected, err
	}

	var successor spikeToken
	if expected.token.initialized {
		if !ready {
			return expected, fmt.Errorf("%w: initialized token but metadata is absent", errFenceStale)
		}
		if metadata.historyHash != currentHash {
			return expected, fmt.Errorf("%w: direct recorder drift from stored fingerprint", errFenceIntegrity)
		}
		if metadata.epoch != expected.token.epoch ||
			metadata.revision != expected.token.revision ||
			metadata.historyHash != expected.token.historyHash ||
			!equalHistory(currentIdentities, expectedCanonical) {
			return expected, fmt.Errorf(
				"%w: expected epoch=%q revision=%d",
				errFenceStale,
				expected.token.epoch,
				expected.token.revision,
			)
		}
		if expected.token.revision == math.MaxInt64 {
			return expected, fmt.Errorf("%w: revision exhausted", errFenceIntegrity)
		}
		result, err := transaction.ExecContext(
			ctx,
			`UPDATE "godj_migration_fence_spike" `+
				`SET "revision" = "revision" + 1, "history_fingerprint" = ? `+
				`WHERE "singleton" = 1 AND "epoch" = ? AND "revision" = ? AND "history_fingerprint" = ?`,
			successorHash[:],
			expected.token.epoch,
			expected.token.revision,
			expected.token.historyHash[:],
		)
		if err != nil {
			return expected, classifyFenceIO("claim initialized revision", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return expected, classifyFenceIO("count initialized revision claim", err)
		}
		if rows != 1 {
			return expected, fmt.Errorf("%w: initialized CAS affected %d rows", errFenceStale, rows)
		}
		successor = spikeToken{
			initialized: true,
			epoch:       expected.token.epoch,
			revision:    expected.token.revision + 1,
			historyHash: successorHash,
		}
	} else {
		if ready {
			return expected, fmt.Errorf("%w: database was initialized after snapshot", errFenceStale)
		}
		if !equalHistory(currentIdentities, expectedCanonical) || currentHash != expected.token.historyHash {
			return expected, fmt.Errorf("%w: legacy history changed before adoption", errFenceStale)
		}
		if bootstrapEpoch == "" {
			return expected, fmt.Errorf("%w: bootstrap epoch is empty", errFenceIntegrity)
		}
		if _, err := transaction.ExecContext(ctx, metadataSchemaSQL()); err != nil {
			return expected, classifyFenceIO("create candidate metadata", err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO "godj_migration_fence_spike" `+
				`("singleton", "epoch", "revision", "history_fingerprint") VALUES (1, ?, 1, ?)`,
			bootstrapEpoch,
			successorHash[:],
		); err != nil {
			return expected, classifyFenceIO("initialize candidate metadata", err)
		}
		successor = spikeToken{
			initialized: true,
			epoch:       bootstrapEpoch,
			revision:    1,
			historyHash: successorHash,
		}
	}

	if afterClaim != nil {
		afterClaim()
	}
	if err := ctx.Err(); err != nil {
		return expected, err
	}
	if mutation == nil {
		return expected, errors.New("fenced mutation is nil")
	}
	if err := mutation(transaction); err != nil {
		return expected, classifyFenceIO("domain and recorder mutation", err)
	}
	if err := ctx.Err(); err != nil {
		return expected, err
	}
	actualSuccessor, err := readCanonicalHistory(ctx, transaction)
	if err != nil {
		return expected, err
	}
	if !equalHistory(actualSuccessor, successorCanonical) || fingerprintHistory(actualSuccessor) != successorHash {
		return expected, fmt.Errorf("%w: domain recorder mutation does not match claimed successor", errFenceIntegrity)
	}
	if err := transaction.Commit(); err != nil {
		return expected, classifyFenceIO("commit", err)
	}
	return historySnapshot{token: successor, identities: successorCanonical}, nil
}

func readFenceMetadata(ctx context.Context, queryer sqlQueryer) (fenceMetadata, bool, error) {
	exists, err := tableExists(ctx, queryer, spikeMetadataTable)
	if err != nil {
		return fenceMetadata{}, false, err
	}
	if !exists {
		return fenceMetadata{}, false, nil
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT "singleton", "epoch", "revision", "history_fingerprint" `+
			`FROM "godj_migration_fence_spike" ORDER BY "singleton"`,
	)
	if err != nil {
		return fenceMetadata{}, false, classifyFenceIntegrityIO("read metadata", err)
	}
	defer func() { _ = rows.Close() }()
	var metadata fenceMetadata
	count := 0
	for rows.Next() {
		var singleton int
		var rawHash []byte
		if err := rows.Scan(&singleton, &metadata.epoch, &metadata.revision, &rawHash); err != nil {
			return fenceMetadata{}, false, classifyFenceIntegrityIO("scan metadata", err)
		}
		count++
		if singleton != 1 || metadata.epoch == "" || metadata.revision < 0 || len(rawHash) != sha256.Size {
			return fenceMetadata{}, false, fmt.Errorf("%w: malformed singleton metadata", errFenceIntegrity)
		}
		copy(metadata.historyHash[:], rawHash)
	}
	if err := rows.Err(); err != nil {
		return fenceMetadata{}, false, classifyFenceIntegrityIO("iterate metadata", err)
	}
	if err := rows.Close(); err != nil {
		return fenceMetadata{}, false, classifyFenceIntegrityIO("close metadata rows", err)
	}
	if count != 1 {
		return fenceMetadata{}, false, fmt.Errorf("%w: metadata row count is %d", errFenceIntegrity, count)
	}
	return metadata, true, nil
}

func readCanonicalHistory(ctx context.Context, queryer sqlQueryer) ([]migrationIdentity, error) {
	exists, err := tableExists(ctx, queryer, spikeRecorderTable)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []migrationIdentity{}, nil
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`,
	)
	if err != nil {
		return nil, classifyFenceIO("read recorder history", err)
	}
	defer func() { _ = rows.Close() }()
	identities := make([]migrationIdentity, 0)
	for rows.Next() {
		var identity migrationIdentity
		if err := rows.Scan(&identity.app, &identity.name); err != nil {
			return nil, classifyFenceIO("scan recorder history", err)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyFenceIO("iterate recorder history", err)
	}
	if err := rows.Close(); err != nil {
		return nil, classifyFenceIO("close recorder rows", err)
	}
	return identities, nil
}

func tableExists(ctx context.Context, queryer sqlQueryer, table string) (bool, error) {
	var exists int
	err := queryer.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?)`,
		table,
	).Scan(&exists)
	if err != nil {
		return false, classifyFenceIO("inspect table "+table, err)
	}
	return exists == 1, nil
}

func transitionHistory(
	before []migrationIdentity,
	identity migrationIdentity,
	direction stepDirection,
) ([]migrationIdentity, error) {
	result := canonicalHistory(before)
	index := sort.Search(len(result), func(index int) bool {
		return compareIdentity(result[index], identity) >= 0
	})
	exists := index < len(result) && result[index] == identity
	switch direction {
	case stepApply:
		if exists {
			return nil, fmt.Errorf("identity %s already applied", formatIdentity(identity))
		}
		result = append(result, migrationIdentity{})
		copy(result[index+1:], result[index:])
		result[index] = identity
	case stepUnapply:
		if !exists {
			return nil, fmt.Errorf("identity %s is not applied", formatIdentity(identity))
		}
		result = append(result[:index], result[index+1:]...)
	default:
		return nil, fmt.Errorf("invalid direction %d", direction)
	}
	return result, nil
}

func canonicalHistory(identities []migrationIdentity) []migrationIdentity {
	result := append([]migrationIdentity(nil), identities...)
	sort.Slice(result, func(left, right int) bool {
		return compareIdentity(result[left], result[right]) < 0
	})
	if result == nil {
		return []migrationIdentity{}
	}
	return result
}

func compareIdentity(left, right migrationIdentity) int {
	if left.app < right.app {
		return -1
	}
	if left.app > right.app {
		return 1
	}
	if left.name < right.name {
		return -1
	}
	if left.name > right.name {
		return 1
	}
	return 0
}

func equalHistory(left, right []migrationIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func fingerprintHistory(identities []migrationIdentity) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	for _, identity := range canonicalHistory(identities) {
		binary.BigEndian.PutUint64(length[:], uint64(len(identity.app)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(identity.app))
		binary.BigEndian.PutUint64(length[:], uint64(len(identity.name)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(identity.name))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func initializeReadyDatabase(t *testing.T, path, epoch string, identities []migrationIdentity) {
	t.Helper()
	database := openSpikeDatabase(t, path, 1000, false)
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, recorderSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	for _, identity := range canonicalHistory(identities) {
		if _, err := database.ExecContext(
			ctx,
			`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
			identity.app,
			identity.name,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, metadataSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	historyHash := fingerprintHistory(identities)
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO "godj_migration_fence_spike" `+
			`("singleton", "epoch", "revision", "history_fingerprint") VALUES (1, ?, 0, ?)`,
		epoch,
		historyHash[:],
	); err != nil {
		t.Fatal(err)
	}
}

func metadataSchemaSQL() string {
	return `CREATE TABLE "godj_migration_fence_spike" (` +
		`"singleton" INTEGER PRIMARY KEY CHECK ("singleton" = 1), ` +
		`"epoch" TEXT NOT NULL CHECK (length("epoch") > 0), ` +
		`"revision" INTEGER NOT NULL CHECK ("revision" >= 0), ` +
		`"history_fingerprint" BLOB NOT NULL CHECK (length("history_fingerprint") = 32))`
}

func recorderSchemaSQL() string {
	return `CREATE TABLE IF NOT EXISTS "godj_migrations" (` +
		`"app" TEXT NOT NULL, "name" TEXT NOT NULL, PRIMARY KEY ("app", "name"))`
}

func openSpikeDatabase(t *testing.T, path string, busyMilliseconds int, immediate bool) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteDSN(path, busyMilliseconds, immediate))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close spike database: %v", err)
		}
	})
	return database
}

func sqliteDSN(path string, busyMilliseconds int, immediate bool) string {
	dsn := "file:" + path + "?_busy_timeout=" + strconv.Itoa(busyMilliseconds)
	if immediate {
		dsn += "&_txlock=immediate"
	}
	return dsn
}

func classifyFenceIO(stage string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		primaryCode := sqliteError.Code() & 0xff
		if primaryCode == 5 || primaryCode == 6 {
			return fmt.Errorf("%w at %s: %v", errFenceContended, stage, err)
		}
	}
	return fmt.Errorf("fence %s: %w", stage, err)
}

func classifyFenceIntegrityIO(stage string, err error) error {
	normalized := classifyFenceIO(stage, err)
	if errors.Is(normalized, errFenceContended) {
		return normalized
	}
	return fmt.Errorf("%w: %v", errFenceIntegrity, normalized)
}

func stepTable(step string) string {
	var builder strings.Builder
	builder.WriteString("step_")
	for _, character := range step {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func formatIdentity(identity migrationIdentity) string {
	return identity.app + "." + identity.name
}

func formatHistory(identities []migrationIdentity) []string {
	formatted := make([]string, len(identities))
	for index, identity := range identities {
		formatted[index] = formatIdentity(identity)
	}
	return formatted
}
