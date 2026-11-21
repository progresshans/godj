package consumer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/owners"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestUpdateOrCreateContention(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "upsert.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(1000)"
		open := func(string) (collectionBackend, error) { return sqlite.Open(t.Context(), dsn) }
		initial, err := open("bootstrap")
		check(t, err)
		migrateCollections(t, initial)
		check(t, initial.Close())
		runUpsertContention(t, open, nil)
	})
	raw := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if raw == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		admin, err := pgx.Connect(t.Context(), raw)
		check(t, err)
		schema := fmt.Sprintf("godj_upsert_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			if err != nil {
				t.Error(err)
			}
			if err := admin.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		open := func(role string) (collectionBackend, error) {
			parsed, err := url.Parse(raw)
			if err != nil {
				return nil, errors.New("invalid PostgreSQL test URL")
			}
			parameters := parsed.Query()
			parameters.Set("application_name", schema+"-"+role)
			parsed.RawQuery = parameters.Encode()
			return postgres.Open(t.Context(), postgres.Config{URL: parsed.String(), Schema: schema})
		}
		initial, err := open("bootstrap")
		check(t, err)
		migrateCollections(t, initial)
		check(t, initial.Close())
		blocked := func(ctx context.Context) error {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var observed bool
				err := admin.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_stat_activity follower JOIN pg_stat_activity leader ON leader.pid=ANY(pg_blocking_pids(follower.pid))
 WHERE follower.application_name=$1 AND leader.application_name=$2 AND follower.wait_event_type='Lock')`, schema+"-follower", schema+"-leader").Scan(&observed)
				if err != nil || observed {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}
		runUpsertContention(t, open, blocked)
	})
}

func upsertGate() (<-chan struct{}, func()) {
	gate := make(chan struct{})
	var once sync.Once
	return gate, func() { once.Do(func() { close(gate) }) }
}

func awaitUpsert(ctx context.Context, signal <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-signal:
		return nil
	}
}

type upsertRaceResult struct {
	index    int
	amount   int64
	observed *int64
	created  bool
	err      error
}

func requireUpsertBusy(t *testing.T, err error) {
	t.Helper()
	var native interface{ Code() int }
	if !errors.As(err, &native) || native.Code()&255 != 5 || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatalf("expected native SQLite busy read/write conflict, got %v", err)
	}
}

func runUpsertContention(t *testing.T, open func(string) (collectionBackend, error), blocked func(context.Context) error) {
	t.Helper()
	var sources [3]collectionBackend
	for index, role := range []string{"leader", "follower", "reader"} {
		backend, err := open(role)
		check(t, err)
		sources[index] = backend
		t.Cleanup(func() { check(t, backend.Close()) })
	}
	t.Run("existing_row", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		row, err := owners.UpsertCounterObjects.Create(ctx, sources[2], owners.NewUpsertCounterCreate("shared"))
		check(t, err)
		leaderRead, signalLeaderRead := upsertGate()
		followerRead, signalFollowerRead := upsertGate()
		followerStart, startFollower := upsertGate()
		leaderWrite, allowLeaderWrite := upsertGate()
		written, signalWritten := upsertGate()
		commit, allowCommit := upsertGate()
		probes := [2]*upsertProbe{{collectionBackend: sources[0]}, {collectionBackend: sources[1]}}
		probes[0].afterUpdate = func(ctx context.Context) error { signalWritten(); return awaitUpsert(ctx, commit) }
		var builds [2]atomic.Int64
		results := make(chan upsertRaceResult, 2)
		var workers sync.WaitGroup
		for index := range 2 {
			workers.Add(1)
			go func(index int) {
				defer workers.Done()
				if index == 1 {
					if err := awaitUpsert(ctx, followerStart); err != nil {
						results <- upsertRaceResult{index: index, err: err}
						return
					}
				}
				var observed *int64
				value, created, err := owners.UpsertCounterObjects.Using(probes[index]).Filter(owners.UpsertCounterFields.ID.Exact(row.ID)).UpdateOrCreate(ctx, nil, singlePatch[owners.UpsertCounter](func(current owners.UpsertCounter) orm.Mutation[owners.UpsertCounter] {
					builds[index].Add(1)
					before := current.Counter
					observed = &before
					if index == 0 {
						signalLeaderRead()
						if err := awaitUpsert(ctx, leaderWrite); err != nil {
							return orm.InvalidMutation[owners.UpsertCounter](err)
						}
					} else if blocked == nil {
						signalFollowerRead()
						if err := awaitUpsert(ctx, written); err != nil {
							return orm.InvalidMutation[owners.UpsertCounter](err)
						}
					}
					return (owners.UpsertCounterPatch{}.WithCounter(current.Counter + 1)).BuildPatch(current)
				}))
				results <- upsertRaceResult{index: index, amount: value.Counter, observed: observed, created: created, err: err}
			}(index)
		}
		defer func() { cancel(); startFollower(); allowLeaderWrite(); allowCommit(); workers.Wait() }()
		check(t, awaitUpsert(ctx, leaderRead))
		startFollower()
		if blocked != nil {
			check(t, blocked(ctx))
			if builds[1].Load() != 0 {
				t.Fatal("follower evaluated its patch before acquiring the row")
			}
		} else {
			check(t, awaitUpsert(ctx, followerRead))
		}
		allowLeaderWrite()
		check(t, awaitUpsert(ctx, written))
		finished := make(map[int]upsertRaceResult)
		if blocked == nil {
			select {
			case value := <-results:
				finished[value.index] = value
				requireUpsertBusy(t, value.err)
				if value.index != 1 {
					t.Fatal("wrong SQLite writer conflicted")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		visible, err := owners.UpsertCounterObjects.Using(sources[2]).Filter(owners.UpsertCounterFields.ID.Exact(row.ID)).Get(ctx)
		check(t, err)
		if visible.Counter != 0 {
			t.Fatal("uncommitted update became visible")
		}
		if blocked != nil {
			free, err := owners.UpsertCounterObjects.Create(ctx, sources[2], owners.NewUpsertCounterCreate("free"))
			check(t, err)
			err = sources[2].(db.Atomic).Atomic(ctx, func(session db.Session) error {
				_, err := owners.UpsertCounterObjects.Using(session).Filter(owners.UpsertCounterFields.ID.Exact(row.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
				return err
			})
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "55P03" {
				t.Fatal("native NOWAIT did not observe the existing upsert lock", err)
			}
			var available []string
			check(t, sources[2].(db.Atomic).Atomic(ctx, func(session db.Session) error {
				values, err := owners.UpsertCounterObjects.Using(session).SelectForUpdate(orm.RowLockOptions{SkipLocked: true}).All(ctx)
				if err != nil {
					return err
				}
				for _, value := range values {
					available = append(available, value.Name)
				}
				return nil
			}))
			compareUpsertLockOptions(t, map[string]any{"nowait_sqlstate": native.Code, "skip_locked_names": available})
			_, err = owners.UpsertCounterObjects.Delete(ctx, sources[2], &free)
			check(t, err)
		}
		allowCommit()
		for len(finished) < 2 {
			select {
			case value := <-results:
				finished[value.index] = value
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if finished[0].err != nil || finished[0].amount != 1 || finished[0].created || builds[0].Load() != 1 || builds[1].Load() != 1 {
			t.Fatal("existing-row leader or input count", finished)
		}
		want := int64(1)
		if blocked != nil {
			want = 2
			if finished[1].err != nil || finished[1].amount != 2 || finished[1].created {
				t.Fatal("PostgreSQL lost a concurrent update", finished)
			}
		}
		final, err := owners.UpsertCounterObjects.Using(sources[2]).Filter(owners.UpsertCounterFields.ID.Exact(row.ID)).Get(ctx)
		check(t, err)
		if final.Counter != want || probes[0].updates.Load() != 1 || probes[1].updates.Load() != 1 || probes[0].transactions.Load() != 1 || probes[1].transactions.Load() != 1 {
			t.Fatal("existing-row operation retried or lost its durable outcome", final)
		}
		compareUpsertContention(t, blocked != nil, 0, finished, [2]int64{}, [2]int64{builds[0].Load(), builds[1].Load()}, probes, []int64{visible.Counter}, final)
		_, err = owners.UpsertCounterObjects.Delete(ctx, sources[2], &final)
		check(t, err)
	})
	t.Run("concurrent_create", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		probes := [2]*upsertProbe{{collectionBackend: sources[0]}, {collectionBackend: sources[1]}}
		entered := make(chan int, 2)
		leaderStart, startLeader := upsertGate()
		inserted, signalInserted := upsertGate()
		commit, allowCommit := upsertGate()
		probes[0].beforeInsert = func(ctx context.Context) error { entered <- 0; return awaitUpsert(ctx, leaderStart) }
		probes[0].afterInsert = func(ctx context.Context) error { signalInserted(); return awaitUpsert(ctx, commit) }
		probes[1].beforeInsert = func(ctx context.Context) error { entered <- 1; return awaitUpsert(ctx, inserted) }
		var creates, patches [2]atomic.Int64
		results := make(chan upsertRaceResult, 2)
		var workers sync.WaitGroup
		for index := range 2 {
			workers.Add(1)
			go func(index int) {
				defer workers.Done()
				var observed *int64
				value, created, err := owners.UpsertCounterObjects.Using(probes[index]).Filter(owners.UpsertCounterFields.Name.Exact("shared")).UpdateOrCreate(ctx, singleCreate[owners.UpsertCounter](func() orm.Mutation[owners.UpsertCounter] {
					creates[index].Add(1)
					return owners.NewUpsertCounterCreate("shared").WithCounter(1).BuildCreate()
				}), singlePatch[owners.UpsertCounter](func(current owners.UpsertCounter) orm.Mutation[owners.UpsertCounter] {
					patches[index].Add(1)
					before := current.Counter
					observed = &before
					return (owners.UpsertCounterPatch{}.WithCounter(current.Counter + 1)).BuildPatch(current)
				}))
				results <- upsertRaceResult{index: index, amount: value.Counter, observed: observed, created: created, err: err}
			}(index)
		}
		defer func() { cancel(); startLeader(); allowCommit(); workers.Wait() }()
		seen := make(map[int]bool)
		for len(seen) < 2 {
			select {
			case index := <-entered:
				seen[index] = true
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		startLeader()
		check(t, awaitUpsert(ctx, inserted))
		finished := make(map[int]upsertRaceResult)
		if blocked != nil {
			check(t, blocked(ctx))
		} else {
			select {
			case value := <-results:
				finished[value.index] = value
				requireUpsertBusy(t, value.err)
				if value.index != 1 {
					t.Fatal("wrong SQLite creator conflicted")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		count, err := owners.UpsertCounterObjects.Using(sources[2]).Filter(owners.UpsertCounterFields.Name.Exact("shared")).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("uncommitted creation became visible")
		}
		allowCommit()
		for len(finished) < 2 {
			select {
			case value := <-results:
				finished[value.index] = value
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if finished[0].err != nil || !finished[0].created || finished[0].amount != 1 || creates[0].Load() != 1 || creates[1].Load() != 1 || patches[0].Load() != 0 {
			t.Fatal("creator did not publish its one durable result", finished)
		}
		want := int64(1)
		if blocked != nil {
			want = 2
			if finished[1].err != nil || finished[1].created || finished[1].amount != want || patches[1].Load() != 1 || probes[1].uniqueFailures.Load() != 1 || probes[1].reads.Load() != 2 {
				t.Fatal("unique loser did not roll back, lock the winner and patch once", finished)
			}
		} else if patches[1].Load() != 0 || probes[1].uniqueFailures.Load() != 0 || probes[1].reads.Load() != 1 {
			t.Fatal("SQLite busy was mistaken for recoverable unique creation")
		}
		for _, probe := range probes {
			if probe.inserts.Load() != 1 || probe.transactions.Load() != 1 || probe.savepoints.Load() != 1 {
				t.Fatal("creation repeated input, insertion or transaction")
			}
		}
		all, err := owners.UpsertCounterObjects.Using(sources[2]).Filter(owners.UpsertCounterFields.Name.Exact("shared")).All(ctx)
		check(t, err)
		if len(all) != 1 || all[0].Counter != want {
			t.Fatal("concurrent creation left a duplicate or wrong final value", all)
		}
		compareUpsertContention(t, blocked != nil, 1, finished, [2]int64{creates[0].Load(), creates[1].Load()}, [2]int64{patches[0].Load(), patches[1].Load()}, probes, []int64{}, all[0])
	})
}
