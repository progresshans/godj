package lifecyclefence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type durableShape struct {
	metadataPresent bool
	epoch           string
	revision        int64
	historyHash     string
	tables          []string
	records         []string
}

func TestSpikeBoundaryIsTestOnly(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	directory := filepath.Dir(source)
	if err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return fmt.Errorf("spike contains non-test Go source %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	readme, err := os.ReadFile(filepath.Join(directory, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"test-only conformance",
		"제품 schema/API로 고정된 것이 아닙니다",
		"fingerprint만으로는 충분하지 않습니다",
		"Stale로 위장하지 않고",
		"semantic re-read/replan/step retry는 없습니다",
		"non-cooperating ABA",
	} {
		if !bytes.Contains(readme, []byte(required)) {
			t.Errorf("README is missing boundary marker %q", required)
		}
	}
}

func TestFenceRejectsStaleBeforeFirstWriteWithoutMutation(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/stale-before-first.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	writerA := openSpikeDatabase(t, path, 1000, true)
	writerB := openSpikeDatabase(t, path, 1000, true)

	initial := mustSnapshot(t, ctx, reader)
	if _, err := runFencedStep(ctx, writerB, initial, "competitor", stepApply, "", nil); err != nil {
		t.Fatalf("competing commit: %v", err)
	}
	beforeStale := readDurableShape(t, reader)
	if _, err := runFencedStep(ctx, writerA, initial, "stale", stepApply, "", nil); !errors.Is(err, errFenceStale) {
		t.Fatalf("stale step error = %v, want %v", err, errFenceStale)
	} else if errors.Is(err, errFenceContended) {
		t.Fatalf("stale token was misclassified as contention: %v", err)
	}
	afterStale := readDurableShape(t, reader)
	if !reflect.DeepEqual(afterStale, beforeStale) {
		t.Fatalf("stale rejection mutated durable state:\n before=%+v\n  after=%+v", beforeStale, afterStale)
	}
	t.Logf("FENCE stale-before-first rejected with zero domain mutation: %+v", afterStale)
}

func TestUninitializedBootstrapRejectsStaleLegacySnapshotBeforeMetadataOrDomainMutation(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/uninitialized.sqlite"
	reader := openSpikeDatabase(t, path, 1000, false)
	legacyWriter := openSpikeDatabase(t, path, 1000, false)
	fencedWriter := openSpikeDatabase(t, path, 1000, true)

	stale := mustSnapshot(t, ctx, reader)
	if stale.token.initialized || len(stale.identities) != 0 {
		t.Fatalf("fresh legacy snapshot = %+v, want uninitialized empty", stale)
	}
	insertLegacyRecord(t, legacyWriter, migrationIdentity{app: "legacy", name: "0001"})
	beforeStale := readDurableShape(t, reader)
	if beforeStale.metadataPresent {
		t.Fatal("legacy competitor unexpectedly initialized fence metadata")
	}

	if _, err := runFencedStep(
		ctx,
		fencedWriter,
		stale,
		"stale_bootstrap",
		stepApply,
		"losing-bootstrap-epoch",
		nil,
	); !errors.Is(err, errFenceStale) {
		t.Fatalf("stale bootstrap error = %v, want %v", err, errFenceStale)
	}
	afterStale := readDurableShape(t, reader)
	if !reflect.DeepEqual(afterStale, beforeStale) {
		t.Fatalf("stale bootstrap mutated metadata/domain/recorder:\n before=%+v\n  after=%+v", beforeStale, afterStale)
	}
	if afterStale.metadataPresent || contains(afterStale.tables, "step_stale_bootstrap") {
		t.Fatalf("stale bootstrap left candidate metadata or DDL: %+v", afterStale)
	}

	fresh := mustSnapshot(t, ctx, reader)
	if fresh.token.initialized || !reflect.DeepEqual(formatHistory(fresh.identities), []string{"legacy.0001"}) {
		t.Fatalf("fresh legacy snapshot = %+v", fresh)
	}
	if _, err := runFencedStep(
		ctx,
		fencedWriter,
		fresh,
		"after_adoption",
		stepApply,
		"adopted-epoch",
		nil,
	); err != nil {
		t.Fatalf("fresh explicit adoption: %v", err)
	}
	shape := readDurableShape(t, reader)
	wantRecords := []string{"legacy.0001", "spike.after_adoption"}
	if !shape.metadataPresent || shape.epoch != "adopted-epoch" || shape.revision != 1 ||
		!reflect.DeepEqual(shape.tables, []string{"step_after_adoption"}) ||
		!reflect.DeepEqual(shape.records, wantRecords) {
		t.Fatalf("adopted durable shape = %+v, want records=%v", shape, wantRecords)
	}
	t.Logf("BOOTSTRAP stale adoption mutated nothing; fresh adoption committed: %+v", shape)
}

func TestUninitializedBootstrapSerializesSameTokenAcrossTwoConnections(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/bootstrap-contenders.sqlite"
	reader := openSpikeDatabase(t, path, 1000, false)
	left := openSpikeDatabase(t, path, 50, true)
	right := openSpikeDatabase(t, path, 50, true)
	initial := mustSnapshot(t, ctx, reader)
	if initial.token.initialized {
		t.Fatal("fresh database unexpectedly initialized")
	}

	type result struct {
		snapshot historySnapshot
		err      error
	}
	claimed := make(chan struct{}, 1)
	release := make(chan struct{})
	results := make(chan result, 2)
	hook := func() {
		select {
		case claimed <- struct{}{}:
		default:
		}
		<-release
	}
	for _, writer := range []*sql.DB{left, right} {
		writer := writer
		go func() {
			snapshot, err := runFencedStep(
				ctx,
				writer,
				initial,
				"bootstrap_current",
				stepApply,
				"bootstrap-epoch",
				hook,
			)
			results <- result{snapshot: snapshot, err: err}
		}()
	}
	select {
	case <-claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("neither bootstrap contender claimed metadata")
	}
	loser := <-results
	if !errors.Is(loser.err, errFenceContended) || errors.Is(loser.err, errFenceStale) {
		t.Fatalf("bootstrap loser = %v, want contention while winner holds reservation", loser.err)
	}
	close(release)
	winner := <-results
	if winner.err != nil {
		t.Fatalf("bootstrap winner: %v", winner.err)
	}
	shape := readDurableShape(t, reader)
	if !shape.metadataPresent || shape.epoch != "bootstrap-epoch" || shape.revision != 1 ||
		!reflect.DeepEqual(shape.tables, []string{"step_bootstrap_current"}) ||
		!reflect.DeepEqual(shape.records, []string{"spike.bootstrap_current"}) {
		t.Fatalf("bootstrap contenders durable shape = %+v", shape)
	}
	beforeRetry := shape
	if _, err := runFencedStep(
		ctx,
		left,
		initial,
		"bootstrap_current",
		stepApply,
		"another-epoch",
		nil,
	); !errors.Is(err, errFenceStale) {
		t.Fatalf("old uninitialized token after bootstrap = %v, want stale", err)
	}
	if afterRetry := readDurableShape(t, reader); !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf("stale bootstrap retry mutated durable shape: before=%+v after=%+v", beforeRetry, afterRetry)
	}
}

func TestFenceRejectsCompetingCommitBetweenSteps(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/between-steps.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	planWriter := openSpikeDatabase(t, path, 1000, true)
	competitorWriter := openSpikeDatabase(t, path, 1000, true)

	initial := mustSnapshot(t, ctx, reader)
	afterFirst, err := runFencedStep(ctx, planWriter, initial, "plan_first", stepApply, "", nil)
	if err != nil {
		t.Fatalf("first plan step: %v", err)
	}
	if _, err := runFencedStep(ctx, competitorWriter, afterFirst, "competitor", stepApply, "", nil); err != nil {
		t.Fatalf("competing commit: %v", err)
	}
	if _, err := runFencedStep(ctx, planWriter, afterFirst, "plan_second", stepApply, "", nil); !errors.Is(err, errFenceStale) {
		t.Fatalf("second plan step error = %v, want %v", err, errFenceStale)
	}

	shape := readDurableShape(t, reader)
	wantTables := []string{"step_competitor", "step_plan_first"}
	wantRecords := []string{"spike.competitor", "spike.plan_first"}
	if shape.revision != 2 || !reflect.DeepEqual(shape.tables, wantTables) ||
		!reflect.DeepEqual(shape.records, wantRecords) {
		t.Fatalf("durable shape = %+v, want revision=2 tables=%v records=%v", shape, wantTables, wantRecords)
	}
	t.Logf("FENCE previous plan step stayed durable and stale next/tail did not start: %+v", shape)
}

func TestFenceSerializesSameTokenAcrossTwoConnections(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/two-connections.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	left := openSpikeDatabase(t, path, 50, true)
	right := openSpikeDatabase(t, path, 50, true)
	initial := mustSnapshot(t, ctx, reader)

	type result struct {
		snapshot historySnapshot
		err      error
	}
	claimed := make(chan struct{}, 1)
	release := make(chan struct{})
	results := make(chan result, 2)
	hook := func() {
		select {
		case claimed <- struct{}{}:
		default:
		}
		<-release
	}
	for _, writer := range []*sql.DB{left, right} {
		writer := writer
		go func() {
			snapshot, err := runFencedStep(ctx, writer, initial, "same_current", stepApply, "", hook)
			results <- result{snapshot: snapshot, err: err}
		}()
	}

	select {
	case <-claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("neither connection claimed fence")
	}
	var rejected result
	select {
	case rejected = <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("losing connection did not return while winner held fence")
	}
	if !errors.Is(rejected.err, errFenceContended) || errors.Is(rejected.err, errFenceStale) {
		t.Fatalf("losing connection error = %v, want contention distinct from stale", rejected.err)
	}
	close(release)
	winner := <-results
	if winner.err != nil {
		t.Fatalf("winning connection: %v", winner.err)
	}
	shape := readDurableShape(t, reader)
	if shape.revision != 1 || !reflect.DeepEqual(shape.tables, []string{"step_same_current"}) ||
		!reflect.DeepEqual(shape.records, []string{"spike.same_current"}) {
		t.Fatalf("durable shape = %+v, want exactly one committed current step", shape)
	}
	t.Logf("FENCE same-token connections produced one commit and one BUSY result: %+v", shape)
}

func TestBusyIsNotStale(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/busy.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	holder := openSpikeDatabase(t, path, 1000, true)
	contender := openSpikeDatabase(t, path, 25, true)
	initial := mustSnapshot(t, ctx, reader)

	lock, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = runFencedStep(ctx, contender, initial, "busy", stepApply, "", nil)
	elapsed := time.Since(started)
	if !errors.Is(err, errFenceContended) || errors.Is(err, errFenceStale) {
		t.Fatalf("busy error = %v, want contention only", err)
	}
	if shape := readDurableShape(t, reader); shape.revision != 0 || len(shape.tables) != 0 || len(shape.records) != 0 {
		t.Fatalf("busy attempt mutated durable state: %+v", shape)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := runFencedStep(ctx, contender, initial, "after_busy", stepApply, "", nil); err != nil {
		t.Fatalf("explicit later attempt after resource release: %v", err)
	}
	t.Logf("BUSY returned once without stale classification or retry in %s", elapsed)
}

func TestFenceRollbackAndCancellationReleaseResources(t *testing.T) {
	t.Run("after_revision_claim", func(t *testing.T) {
		ctx := context.Background()
		path := t.TempDir() + "/error.sqlite"
		initializeReadyDatabase(t, path, "ready-epoch", nil)
		reader := openSpikeDatabase(t, path, 1000, false)
		writer := openSpikeDatabase(t, path, 1000, true)
		initial := mustSnapshot(t, ctx, reader)

		forced := errors.New("forced post-claim error")
		if _, err := runFencedMutation(
			ctx,
			writer,
			initial,
			initial.identities,
			"",
			func(*sql.Tx) error { return forced },
			nil,
		); !errors.Is(err, forced) {
			t.Fatalf("forced error = %v, want %v", err, forced)
		}
		assertEmptyReadyShape(t, reader)
		if _, err := runFencedStep(ctx, writer, initial, "after_error", stepApply, "", nil); err != nil {
			t.Fatalf("later success after error rollback: %v", err)
		}
	})

	t.Run("cancellation_after_domain_ddl", func(t *testing.T) {
		path := t.TempDir() + "/cancel.sqlite"
		initializeReadyDatabase(t, path, "ready-epoch", nil)
		reader := openSpikeDatabase(t, path, 1000, false)
		writer := openSpikeDatabase(t, path, 1000, true)
		initial := mustSnapshot(t, context.Background(), reader)
		successor, err := transitionHistory(
			initial.identities,
			migrationIdentity{app: "spike", name: "cancel_after_ddl"},
			stepApply,
		)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		if _, err := runFencedMutation(
			ctx,
			writer,
			initial,
			successor,
			"",
			func(transaction *sql.Tx) error {
				if _, err := transaction.ExecContext(ctx, recorderSchemaSQL()); err != nil {
					return err
				}
				if _, err := transaction.ExecContext(
					ctx,
					`CREATE TABLE "step_cancel_after_ddl" ("id" INTEGER PRIMARY KEY)`,
				); err != nil {
					return err
				}
				cancel()
				return nil
			},
			nil,
		); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v, want context.Canceled", err)
		}
		assertEmptyReadyShape(t, reader)
		if _, err := runFencedStep(
			context.Background(),
			writer,
			initial,
			"after_cancel",
			stepApply,
			"",
			nil,
		); err != nil {
			t.Fatalf("later success after cancellation rollback: %v", err)
		}
	})

	t.Run("error_after_recorder_mutation", func(t *testing.T) {
		ctx := context.Background()
		path := t.TempDir() + "/recorder-error.sqlite"
		initializeReadyDatabase(t, path, "ready-epoch", nil)
		reader := openSpikeDatabase(t, path, 1000, false)
		writer := openSpikeDatabase(t, path, 1000, true)
		initial := mustSnapshot(t, ctx, reader)
		identity := migrationIdentity{app: "spike", name: "recorder_fault"}
		successor, err := transitionHistory(initial.identities, identity, stepApply)
		if err != nil {
			t.Fatal(err)
		}
		forced := errors.New("forced error after recorder mutation")
		if _, err := runFencedMutation(
			ctx,
			writer,
			initial,
			successor,
			"",
			func(transaction *sql.Tx) error {
				if _, err := transaction.ExecContext(ctx, recorderSchemaSQL()); err != nil {
					return err
				}
				if _, err := transaction.ExecContext(
					ctx,
					`CREATE TABLE "step_recorder_fault" ("id" INTEGER PRIMARY KEY)`,
				); err != nil {
					return err
				}
				if _, err := transaction.ExecContext(
					ctx,
					`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
					identity.app,
					identity.name,
				); err != nil {
					return err
				}
				return forced
			},
			nil,
		); !errors.Is(err, forced) {
			t.Fatalf("post-recorder error = %v, want %v", err, forced)
		}
		assertEmptyReadyShape(t, reader)
		if _, err := runFencedStep(ctx, writer, initial, "recorder_fault", stepApply, "", nil); err != nil {
			t.Fatalf("later same-token success after recorder rollback: %v", err)
		}
	})

	t.Run("final_successor_fingerprint_mismatch", func(t *testing.T) {
		ctx := context.Background()
		path := t.TempDir() + "/successor-mismatch.sqlite"
		initializeReadyDatabase(t, path, "ready-epoch", nil)
		reader := openSpikeDatabase(t, path, 1000, false)
		writer := openSpikeDatabase(t, path, 1000, true)
		initial := mustSnapshot(t, ctx, reader)
		expectedIdentity := migrationIdentity{app: "spike", name: "expected_successor"}
		successor, err := transitionHistory(initial.identities, expectedIdentity, stepApply)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runFencedMutation(
			ctx,
			writer,
			initial,
			successor,
			"",
			func(transaction *sql.Tx) error {
				if _, err := transaction.ExecContext(ctx, recorderSchemaSQL()); err != nil {
					return err
				}
				if _, err := transaction.ExecContext(
					ctx,
					`CREATE TABLE "step_expected_successor" ("id" INTEGER PRIMARY KEY)`,
				); err != nil {
					return err
				}
				_, err := transaction.ExecContext(
					ctx,
					`INSERT INTO "godj_migrations" ("app", "name") VALUES ('spike', 'unexpected_successor')`,
				)
				return err
			},
			nil,
		); !errors.Is(err, errFenceIntegrity) {
			t.Fatalf("successor mismatch error = %v, want %v", err, errFenceIntegrity)
		}
		assertEmptyReadyShape(t, reader)
		if _, err := runFencedStep(ctx, writer, initial, "expected_successor", stepApply, "", nil); err != nil {
			t.Fatalf("later same-token success after mismatch rollback: %v", err)
		}
	})
}

type codedSQLiteError struct {
	code int
}

func (err codedSQLiteError) Error() string { return fmt.Sprintf("synthetic SQLite code %d", err.code) }
func (err codedSQLiteError) Code() int     { return err.code }

func TestBusyLockedNormalizationCoversEveryFenceSQLStage(t *testing.T) {
	stages := []string{
		"begin immediate",
		"metadata query/scan/iterate/close",
		"history query/scan/iterate/close",
		"domain DDL",
		"recorder mutation",
		"final successor verification",
		"commit",
	}
	for _, stage := range stages {
		stage := stage
		for _, code := range []int{5, 6, 5 | (1 << 8), 6 | (1 << 8)} {
			code := code
			t.Run(stage+"/"+strconv.Itoa(code), func(t *testing.T) {
				err := classifyFenceIO(stage, codedSQLiteError{code: code})
				if !errors.Is(err, errFenceContended) || errors.Is(err, errFenceStale) ||
					errors.Is(err, errFenceIntegrity) {
					t.Fatalf("normalized error = %v, want contention only", err)
				}
				integrityPathErr := classifyFenceIntegrityIO(stage, codedSQLiteError{code: code})
				if !errors.Is(integrityPathErr, errFenceContended) ||
					errors.Is(integrityPathErr, errFenceIntegrity) {
					t.Fatalf("integrity-path normalized error = %v, want contention only", integrityPathErr)
				}
			})
		}
	}

	ctx := context.Background()
	path := t.TempDir() + "/domain-busy-normalization.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	writer := openSpikeDatabase(t, path, 1000, true)
	initial := mustSnapshot(t, ctx, reader)
	if _, err := runFencedMutation(
		ctx,
		writer,
		initial,
		initial.identities,
		"",
		func(*sql.Tx) error { return codedSQLiteError{code: 6} },
		nil,
	); !errors.Is(err, errFenceContended) || errors.Is(err, errFenceIntegrity) {
		t.Fatalf("domain synthetic lock error = %v, want contention", err)
	}
	assertEmptyReadyShape(t, reader)
}

func TestFingerprintDetectsDirectDriftButRevisionIsRequiredForABA(t *testing.T) {
	ctx := context.Background()

	absentPath := t.TempDir() + "/absent.sqlite"
	emptyPath := t.TempDir() + "/empty.sqlite"
	absent := openSpikeDatabase(t, absentPath, 1000, false)
	empty := openSpikeDatabase(t, emptyPath, 1000, false)
	if _, err := empty.ExecContext(ctx, recorderSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	absentSnapshot := mustSnapshot(t, ctx, absent)
	emptySnapshot := mustSnapshot(t, ctx, empty)
	if absentSnapshot.token.historyHash != emptySnapshot.token.historyHash {
		t.Fatal("absent and empty recorder histories have different canonical fingerprints")
	}

	driftPath := t.TempDir() + "/drift.sqlite"
	initializeReadyDatabase(t, driftPath, "ready-epoch", nil)
	driftReader := openSpikeDatabase(t, driftPath, 1000, false)
	driftWriter := openSpikeDatabase(t, driftPath, 1000, true)
	directWriter := openSpikeDatabase(t, driftPath, 1000, false)
	beforeDrift := mustSnapshot(t, ctx, driftReader)
	insertLegacyRecord(t, directWriter, migrationIdentity{app: "direct", name: "0001"})
	if _, err := readAtomicSnapshot(ctx, driftReader); !errors.Is(err, errFenceIntegrity) {
		t.Fatalf("snapshot after direct drift error = %v, want integrity failure", err)
	}
	shapeBeforeRejectedStep := readDurableShape(t, driftReader)
	if _, err := runFencedStep(
		ctx,
		driftWriter,
		beforeDrift,
		"after_drift",
		stepApply,
		"",
		nil,
	); !errors.Is(err, errFenceIntegrity) {
		t.Fatalf("fenced step after direct drift error = %v, want integrity failure", err)
	}
	if shape := readDurableShape(t, driftReader); !reflect.DeepEqual(shape, shapeBeforeRejectedStep) {
		t.Fatalf("integrity rejection mutated drifted database:\n before=%+v\n  after=%+v", shapeBeforeRejectedStep, shape)
	}

	abaPath := t.TempDir() + "/aba.sqlite"
	initializeReadyDatabase(t, abaPath, "ready-epoch", nil)
	abaReader := openSpikeDatabase(t, abaPath, 1000, false)
	abaWriter := openSpikeDatabase(t, abaPath, 1000, true)
	initial := mustSnapshot(t, ctx, abaReader)
	afterApply, err := runFencedStep(ctx, abaWriter, initial, "aba", stepApply, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runFencedStep(ctx, abaWriter, afterApply, "aba", stepUnapply, "", nil); err != nil {
		t.Fatal(err)
	}
	afterABA := mustSnapshot(t, ctx, abaReader)
	if initial.token.historyHash != afterABA.token.historyHash {
		t.Fatal("apply/unapply did not restore recorder fingerprint")
	}
	if initial.token.revision == afterABA.token.revision || afterABA.token.revision != 2 {
		t.Fatalf("revision did not distinguish ABA: before=%d after=%d", initial.token.revision, afterABA.token.revision)
	}
	t.Logf("FINGERPRINT absent=empty and ABA restored hash, while revision advanced 0->%d", afterABA.token.revision)
}

func TestFenceSerializesSameTokenAcrossTwoProcesses(t *testing.T) {
	if os.Getenv("GDJ0017_FENCE_HELPER") == "1" {
		runFenceProcessHelper(t)
		return
	}

	path := t.TempDir() + "/two-processes.sqlite"
	syncDirectory := filepath.Dir(path)
	initializeReadyDatabase(t, path, "process-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	initial := mustSnapshot(t, context.Background(), reader)

	type child struct {
		command *exec.Cmd
		stdin   *os.File
		output  bytes.Buffer
	}
	children := make([]child, 2)
	for index := range children {
		command := exec.Command(os.Args[0], "-test.run=^TestFenceSerializesSameTokenAcrossTwoProcesses$")
		command.Env = append(
			os.Environ(),
			"GDJ0017_FENCE_HELPER=1",
			"GDJ0017_FENCE_DB="+path,
			"GDJ0017_FENCE_EPOCH="+initial.token.epoch,
			"GDJ0017_FENCE_REVISION="+strconv.FormatInt(initial.token.revision, 10),
			"GDJ0017_FENCE_HASH="+hex.EncodeToString(initial.token.historyHash[:]),
			"GDJ0017_FENCE_SYNC_DIR="+syncDirectory,
			"GDJ0017_FENCE_CHILD="+strconv.Itoa(index),
		)
		readPipe, writePipe, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stdin = readPipe
		command.Stdout = &children[index].output
		command.Stderr = &children[index].output
		children[index].command = command
		children[index].stdin = writePipe
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				_ = command.Process.Kill()
			}
		})
		if err := readPipe.Close(); err != nil {
			t.Fatal(err)
		}
	}
	waitForPaths(t, []string{
		filepath.Join(syncDirectory, "ready-0"),
		filepath.Join(syncDirectory, "ready-1"),
	}, 5*time.Second)
	for index := range children {
		if _, err := children[index].stdin.Write([]byte("start\n")); err != nil {
			t.Fatal(err)
		}
		if err := children[index].stdin.Close(); err != nil {
			t.Fatal(err)
		}
	}
	waitForAnyPath(t, []string{
		filepath.Join(syncDirectory, "claimed-0"),
		filepath.Join(syncDirectory, "claimed-1"),
	}, 5*time.Second)

	type childResult struct {
		index int
		err   error
	}
	waitResults := make(chan childResult, 2)
	for index := range children {
		index := index
		go func() { waitResults <- childResult{index: index, err: children[index].command.Wait()} }()
	}
	var first childResult
	select {
	case first = <-waitResults:
	case <-time.After(5 * time.Second):
		t.Fatal("losing process did not return while winner held claim")
	}
	if first.err != nil || !strings.Contains(children[first.index].output.String(), "GDJ0017_RESULT=contended") {
		t.Fatalf(
			"first process result index=%d err=%v output=%q, want BUSY/LOCKED contention",
			first.index,
			first.err,
			children[first.index].output.String(),
		)
	}
	if err := os.WriteFile(filepath.Join(syncDirectory, "release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var second childResult
	select {
	case second = <-waitResults:
	case <-time.After(5 * time.Second):
		t.Fatal("winning process did not return after release")
	}
	if second.err != nil || !strings.Contains(children[second.index].output.String(), "GDJ0017_RESULT=success") {
		t.Fatalf(
			"second process result index=%d err=%v outputs=%q / %q",
			second.index,
			second.err,
			children[0].output.String(),
			children[1].output.String(),
		)
	}
	shape := readDurableShape(t, reader)
	if shape.revision != 1 || !reflect.DeepEqual(shape.tables, []string{"step_process_current"}) ||
		!reflect.DeepEqual(shape.records, []string{"spike.process_current"}) {
		t.Fatalf("two-process durable shape = %+v, want exactly one step", shape)
	}
	t.Logf("FENCE two processes produced one success and one distinct rejection: %+v", shape)
}

func runFenceProcessHelper(t *testing.T) {
	revision, err := strconv.ParseInt(os.Getenv("GDJ0017_FENCE_REVISION"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	rawHash, err := hex.DecodeString(os.Getenv("GDJ0017_FENCE_HASH"))
	if err != nil || len(rawHash) != sha256.Size {
		t.Fatalf("decode helper hash: bytes=%d err=%v", len(rawHash), err)
	}
	var historyHash [sha256.Size]byte
	copy(historyHash[:], rawHash)
	initial := historySnapshot{token: spikeToken{
		initialized: true,
		epoch:       os.Getenv("GDJ0017_FENCE_EPOCH"),
		revision:    revision,
		historyHash: historyHash,
	}, identities: []migrationIdentity{}}

	var signal string
	syncDirectory := os.Getenv("GDJ0017_FENCE_SYNC_DIR")
	childID := os.Getenv("GDJ0017_FENCE_CHILD")
	if err := os.WriteFile(filepath.Join(syncDirectory, "ready-"+childID), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fscan(os.Stdin, &signal); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", sqliteDSN(os.Getenv("GDJ0017_FENCE_DB"), 50, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	_, err = runFencedStep(
		context.Background(),
		database,
		initial,
		"process_current",
		stepApply,
		"",
		func() {
			if err := os.WriteFile(
				filepath.Join(syncDirectory, "claimed-"+childID),
				[]byte("claimed\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			waitForPaths(t, []string{filepath.Join(syncDirectory, "release")}, 5*time.Second)
		},
	)
	switch {
	case err == nil:
		fmt.Println("GDJ0017_RESULT=success")
	case errors.Is(err, errFenceContended):
		fmt.Println("GDJ0017_RESULT=contended")
	case errors.Is(err, errFenceStale):
		fmt.Println("GDJ0017_RESULT=stale")
	default:
		t.Fatalf("unexpected helper result: %v", err)
	}
}

func waitForPaths(t *testing.T, paths []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		allPresent := true
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				allPresent = false
			}
		}
		if allPresent {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for paths %v", paths)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForAnyPath(t *testing.T, paths []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for one of %v", paths)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func mustSnapshot(t *testing.T, ctx context.Context, database *sql.DB) historySnapshot {
	t.Helper()
	snapshot, err := readAtomicSnapshot(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func insertLegacyRecord(t *testing.T, database *sql.DB, identity migrationIdentity) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, recorderSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
		identity.app,
		identity.name,
	); err != nil {
		t.Fatal(err)
	}
}

func assertEmptyReadyShape(t *testing.T, database *sql.DB) {
	t.Helper()
	shape := readDurableShape(t, database)
	if !shape.metadataPresent || shape.revision != 0 || len(shape.tables) != 0 || len(shape.records) != 0 {
		t.Fatalf("rollback left durable mutation: %+v", shape)
	}
}

func readDurableShape(t *testing.T, database *sql.DB) durableShape {
	t.Helper()
	ctx := context.Background()
	identities, err := readCanonicalHistory(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ready, err := readFenceMetadata(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	shape := durableShape{
		metadataPresent: ready,
		records:         formatHistory(identities),
	}
	if ready {
		shape.epoch = metadata.epoch
		shape.revision = metadata.revision
		shape.historyHash = hex.EncodeToString(metadata.historyHash[:])
	}
	rows, err := database.QueryContext(
		ctx,
		`SELECT "name" FROM "sqlite_schema" `+
			`WHERE "type" = 'table' AND "name" LIKE 'step_%' ORDER BY "name"`,
	)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		shape.tables = append(shape.tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return shape
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
