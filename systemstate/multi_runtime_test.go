package systemstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/sessions"
)

func TestTwoRuntimeSQLiteConcurrentProvisionPublishesExactlyOneOperator(t *testing.T) {
	tests := []struct {
		name              string
		contenderPassword string
	}{
		{
			name:              "identical supplied material",
			contenderPassword: "multi-runtime-bootstrap-password",
		},
		{
			name:              "different supplied password",
			contenderPassword: "multi-runtime-bootstrap-mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			holder, contender := openMultiRuntimeSQLiteBackends(t, ctx, "bootstrap.sqlite3")
			explicitlyMigrateSystemState(t, ctx, holder.Backend)
			barrier := armMultiRuntimeBarrier(t, holder, contender)

			holderConfig := runtimeTestConfig(t, "multi-runtime-bootstrap-password")
			contenderConfig := runtimeTestConfig(t, test.contenderPassword)
			holderHasher := newOperatorHasherSpy(t, 10_000)
			contenderHasher := newOperatorHasherSpy(t, 10_000)
			holderConfig.PasswordHasher = holderHasher
			contenderConfig.PasswordHasher = contenderHasher
			holderProvision := holderConfig.provisionConfig(t)
			contenderProvision := contenderConfig.provisionConfig(t)
			holderResult := make(chan error, 1)
			contenderResult := make(chan error, 1)
			go func() {
				holderResult <- ProvisionOperator(ctx, holder, holderProvision)
			}()

			barrier.waitHolderEntered(t)
			go func() {
				contenderResult <- ProvisionOperator(ctx, contender, contenderProvision)
			}()
			barrier.assertContenderCallbackBlocked(t)
			barrier.release()

			gotHolder := waitMultiRuntimeResult(t, holderResult)
			gotContender := waitMultiRuntimeResult(t, contenderResult)
			disarmMultiRuntimeBarrier(holder, contender)
			barrier.assertCallbackCounts(t, 1, 1)

			if gotHolder != nil {
				t.Fatalf("holder ProvisionOperator() = %v, want nil", gotHolder)
			}
			if !errors.Is(gotContender, &Error{Code: CodeCredentialAlreadyExists, Field: "credential"}) {
				t.Fatalf("contender ProvisionOperator() = %#v, want credential_already_exists", gotContender)
			}
			if holderHasher.hashCalls.Load() != 1 || contenderHasher.hashCalls.Load() != 1 ||
				holderHasher.verifyCalls.Load() != 0 || contenderHasher.verifyCalls.Load() != 0 {
				t.Fatalf(
					"concurrent provision hasher calls = holder %d/%d contender %d/%d, want hash 1/verify 0 each",
					holderHasher.hashCalls.Load(), holderHasher.verifyCalls.Load(),
					contenderHasher.hashCalls.Load(), contenderHasher.verifyCalls.Load(),
				)
			}

			credentials, err := readCredentialRows(ctx, holder.Backend)
			if err != nil || len(credentials) != 1 {
				t.Fatalf("durable credential rows = (%d, %v), want (1, nil)", len(credentials), err)
			}
		})
	}
}

func TestTwoRuntimeSQLiteConcurrentSessionCreatePreservesGlobalCapacity(t *testing.T) {
	ctx := context.Background()
	holderRuntime, contenderRuntime, holderBackend, contenderBackend := openTwoSystemStateRuntimes(t, ctx, 1)
	holderStore := holderRuntime.SessionStore()
	contenderStore := contenderRuntime.SessionStore()
	base := time.Date(2026, time.August, 26, 6, 0, 0, 0, time.UTC)
	expiredRecord := multiRuntimeSessionRecord(
		t,
		"O",
		base.Add(-2*time.Hour),
		base.Add(-2*time.Hour),
		base.Add(-time.Hour),
		base.Add(-90*time.Minute),
	)
	if created, err := holderStore.Create(ctx, expiredRecord); err != nil || !created {
		t.Fatalf("Create(expired capacity fixture) = (%v, %v), want (true, nil)", created, err)
	}
	holderRecord := multiRuntimeSessionRecord(t, "A", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	contenderRecord := multiRuntimeSessionRecord(t, "B", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

	type createResult struct {
		created bool
		err     error
	}
	holderResult := make(chan createResult, 1)
	contenderResult := make(chan createResult, 1)
	go func() {
		created, err := holderStore.Create(ctx, holderRecord)
		holderResult <- createResult{created: created, err: err}
	}()
	barrier.waitHolderEntered(t)
	go func() {
		created, err := contenderStore.Create(ctx, contenderRecord)
		contenderResult <- createResult{created: created, err: err}
	}()
	barrier.assertContenderCallbackBlocked(t)
	barrier.release()

	gotHolder := waitMultiRuntimeResult(t, holderResult)
	gotContender := waitMultiRuntimeResult(t, contenderResult)
	disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
	barrier.assertCallbackCounts(t, 1, 1)
	if gotHolder.err != nil || !gotHolder.created {
		t.Fatalf("holder Create() = (%v, %v), want (true, nil)", gotHolder.created, gotHolder.err)
	}
	if gotContender.created || !errors.Is(gotContender.err, &sessions.Error{Code: sessions.CodeStoreFull}) {
		t.Fatalf("contender Create() = (%v, %#v), want (false, store_full)", gotContender.created, gotContender.err)
	}

	rows, err := listSessionRows(ctx, holderBackend.Backend, 2)
	if err != nil || len(rows) != 1 {
		t.Fatalf("durable session rows = (%d, %v), want (1, nil)", len(rows), err)
	}
	if _, found, err := holderStore.Load(ctx, holderRecord.ID()); err != nil || !found {
		t.Fatalf("Load(holder winner) = (found %v, error %v), want (true, nil)", found, err)
	}
	if _, found, err := contenderStore.Load(ctx, contenderRecord.ID()); err != nil || found {
		t.Fatalf("Load(contender loser) = (found %v, error %v), want (false, nil)", found, err)
	}
	if _, found, err := holderStore.Load(ctx, expiredRecord.ID()); err != nil || found {
		t.Fatalf("Load(globally reaped row) = (found %v, error %v), want (false, nil)", found, err)
	}
}

func TestTwoRuntimeSQLiteConcurrentSameSessionCreatePublishesOneDigest(t *testing.T) {
	ctx := context.Background()
	holderRuntime, contenderRuntime, holderBackend, contenderBackend := openTwoSystemStateRuntimes(t, ctx, 2)
	holderStore := holderRuntime.SessionStore()
	contenderStore := contenderRuntime.SessionStore()
	base := time.Date(2026, time.August, 26, 6, 30, 0, 0, time.UTC)
	record := multiRuntimeSessionRecord(t, "P", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

	type createResult struct {
		created bool
		err     error
	}
	holderResult := make(chan createResult, 1)
	contenderResult := make(chan createResult, 1)
	go func() {
		created, err := holderStore.Create(ctx, record)
		holderResult <- createResult{created: created, err: err}
	}()
	barrier.waitHolderEntered(t)
	go func() {
		created, err := contenderStore.Create(ctx, record)
		contenderResult <- createResult{created: created, err: err}
	}()
	barrier.assertContenderCallbackBlocked(t)
	barrier.release()

	gotHolder := waitMultiRuntimeResult(t, holderResult)
	gotContender := waitMultiRuntimeResult(t, contenderResult)
	disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
	barrier.assertCallbackCounts(t, 1, 1)
	if gotHolder.err != nil || !gotHolder.created {
		t.Fatalf("holder Create(same digest) = (%v, %v), want (true, nil)", gotHolder.created, gotHolder.err)
	}
	if gotContender.err != nil || gotContender.created {
		t.Fatalf("contender Create(same digest) = (%v, %v), want (false, nil)", gotContender.created, gotContender.err)
	}

	rows, err := listSessionRows(ctx, holderBackend.Backend, 3)
	if err != nil || len(rows) != 1 {
		t.Fatalf("same-digest durable rows = (%d, %v), want (1, nil)", len(rows), err)
	}
	wantDigest, err := sessionDigest(record.ID())
	if err != nil || rows[0].digest != wantDigest {
		t.Fatalf("same-digest row = (%q, %v), want (%q, nil)", rows[0].digest, err, wantDigest)
	}
}

func TestTwoRuntimeSQLiteOutOfOrderTouchPreservesNewestState(t *testing.T) {
	ctx := context.Background()
	holderRuntime, contenderRuntime, holderBackend, contenderBackend := openTwoSystemStateRuntimes(t, ctx, 8)
	holderStore := holderRuntime.SessionStore()
	contenderStore := contenderRuntime.SessionStore()
	base := time.Date(2026, time.August, 26, 7, 0, 0, 0, time.UTC)
	record := multiRuntimeSessionRecord(t, "C", base, base, base.Add(2*time.Hour), base.Add(30*time.Minute))
	if created, err := holderStore.Create(ctx, record); err != nil || !created {
		t.Fatalf("Create(touch fixture) = (%v, %v), want (true, nil)", created, err)
	}

	newestAccess := base.Add(20 * time.Minute)
	newestIdle := base.Add(50 * time.Minute)
	staleAccess := base.Add(10 * time.Minute)
	staleIdle := base.Add(40 * time.Minute)
	barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

	type touchResult struct {
		record sessions.Record
		found  bool
		err    error
	}
	holderResult := make(chan touchResult, 1)
	contenderResult := make(chan touchResult, 1)
	go func() {
		touched, found, err := holderStore.Touch(ctx, record.ID(), newestAccess, newestIdle)
		holderResult <- touchResult{record: touched, found: found, err: err}
	}()
	barrier.waitHolderEntered(t)
	go func() {
		touched, found, err := contenderStore.Touch(ctx, record.ID(), staleAccess, staleIdle)
		contenderResult <- touchResult{record: touched, found: found, err: err}
	}()
	barrier.assertContenderCallbackBlocked(t)
	barrier.release()

	gotHolder := waitMultiRuntimeResult(t, holderResult)
	gotContender := waitMultiRuntimeResult(t, contenderResult)
	disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
	barrier.assertCallbackCounts(t, 1, 1)
	for name, result := range map[string]touchResult{"newest": gotHolder, "stale": gotContender} {
		if result.err != nil || !result.found {
			t.Fatalf("%s Touch() = (found %v, error %v), want (true, nil)", name, result.found, result.err)
		}
		if !result.record.AccessedAt().Equal(newestAccess) || !result.record.IdleExpiresAt().Equal(newestIdle) {
			t.Fatalf("%s Touch() timestamps = (%v, %v), want (%v, %v)", name, result.record.AccessedAt(), result.record.IdleExpiresAt(), newestAccess, newestIdle)
		}
	}

	stored, found, err := holderStore.Load(ctx, record.ID())
	if err != nil || !found || !stored.AccessedAt().Equal(newestAccess) || !stored.IdleExpiresAt().Equal(newestIdle) {
		t.Fatalf("stored touched session = (%v, found %v, error %v), want newest state", stored.Snapshot(), found, err)
	}
}

func TestTwoRuntimeSQLiteRotateTouchAndLogoutLanesAreLinearized(t *testing.T) {
	ctx := context.Background()
	holderRuntime, contenderRuntime, holderBackend, contenderBackend := openTwoSystemStateRuntimes(t, ctx, 16)
	holderStore := holderRuntime.SessionStore()
	contenderStore := contenderRuntime.SessionStore()
	base := time.Date(2026, time.August, 26, 8, 0, 0, 0, time.UTC)

	t.Run("exactly one concurrent rotation publishes", func(t *testing.T) {
		old := multiRuntimeSessionRecord(t, "D", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
		if created, err := holderStore.Create(ctx, old); err != nil || !created {
			t.Fatalf("Create(rotation fixture) = (%v, %v)", created, err)
		}
		holderReplacement := multiRuntimeReplacement(t, old, "E", base.Add(5*time.Minute), base.Add(65*time.Minute))
		contenderReplacement := multiRuntimeReplacement(t, old, "F", base.Add(6*time.Minute), base.Add(66*time.Minute))
		barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

		type rotateResult struct {
			record  sessions.Record
			rotated bool
			err     error
		}
		holderResult := make(chan rotateResult, 1)
		contenderResult := make(chan rotateResult, 1)
		go func() {
			published, rotated, err := holderStore.Rotate(ctx, old.ID(), holderReplacement)
			holderResult <- rotateResult{record: published, rotated: rotated, err: err}
		}()
		barrier.waitHolderEntered(t)
		go func() {
			published, rotated, err := contenderStore.Rotate(ctx, old.ID(), contenderReplacement)
			contenderResult <- rotateResult{record: published, rotated: rotated, err: err}
		}()
		barrier.assertContenderCallbackBlocked(t)
		barrier.release()

		gotHolder := waitMultiRuntimeResult(t, holderResult)
		gotContender := waitMultiRuntimeResult(t, contenderResult)
		disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
		barrier.assertCallbackCounts(t, 1, 1)
		if gotHolder.err != nil || !gotHolder.rotated || !reflect.DeepEqual(gotHolder.record.Snapshot(), holderReplacement.Snapshot()) {
			t.Fatalf("holder Rotate() = (%v, %v, %v), want holder replacement/true/nil", gotHolder.record.Snapshot(), gotHolder.rotated, gotHolder.err)
		}
		if gotContender.err != nil || gotContender.rotated || gotContender.record.ID().Valid() {
			t.Fatalf("contender Rotate() = (%v, %v, %v), want zero/false/nil", gotContender.record.Snapshot(), gotContender.rotated, gotContender.err)
		}
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, old.ID(), false)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, holderReplacement.ID(), true)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, contenderReplacement.ID(), false)
	})

	t.Run("logout first denies later rotation without resurrection", func(t *testing.T) {
		old := multiRuntimeSessionRecord(t, "G", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
		if created, err := holderStore.Create(ctx, old); err != nil || !created {
			t.Fatalf("Create(logout-first fixture) = (%v, %v)", created, err)
		}
		replacement := multiRuntimeReplacement(t, old, "H", base.Add(5*time.Minute), base.Add(65*time.Minute))
		barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

		holderResult := make(chan error, 1)
		type rotateResult struct {
			record  sessions.Record
			rotated bool
			err     error
		}
		contenderResult := make(chan rotateResult, 1)
		go func() { holderResult <- holderStore.Delete(ctx, old.ID()) }()
		barrier.waitHolderEntered(t)
		go func() {
			published, rotated, err := contenderStore.Rotate(ctx, old.ID(), replacement)
			contenderResult <- rotateResult{record: published, rotated: rotated, err: err}
		}()
		barrier.assertContenderCallbackBlocked(t)
		barrier.release()

		if err := waitMultiRuntimeResult(t, holderResult); err != nil {
			t.Fatalf("logout-first Delete() error = %v", err)
		}
		gotContender := waitMultiRuntimeResult(t, contenderResult)
		disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
		barrier.assertCallbackCounts(t, 1, 1)
		if gotContender.err != nil || gotContender.rotated || gotContender.record.ID().Valid() {
			t.Fatalf("later Rotate() = (%v, %v, %v), want zero/false/nil", gotContender.record.Snapshot(), gotContender.rotated, gotContender.err)
		}
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, old.ID(), false)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, replacement.ID(), false)
	})

	t.Run("rotation first preserves replacement after old ID logout", func(t *testing.T) {
		old := multiRuntimeSessionRecord(t, "I", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
		if created, err := holderStore.Create(ctx, old); err != nil || !created {
			t.Fatalf("Create(rotate-first fixture) = (%v, %v)", created, err)
		}
		replacement := multiRuntimeReplacement(t, old, "J", base.Add(5*time.Minute), base.Add(65*time.Minute))
		barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

		type rotateResult struct {
			record  sessions.Record
			rotated bool
			err     error
		}
		holderResult := make(chan rotateResult, 1)
		contenderResult := make(chan error, 1)
		go func() {
			published, rotated, err := holderStore.Rotate(ctx, old.ID(), replacement)
			holderResult <- rotateResult{record: published, rotated: rotated, err: err}
		}()
		barrier.waitHolderEntered(t)
		go func() { contenderResult <- contenderStore.Delete(ctx, old.ID()) }()
		barrier.assertContenderCallbackBlocked(t)
		barrier.release()

		gotHolder := waitMultiRuntimeResult(t, holderResult)
		if err := waitMultiRuntimeResult(t, contenderResult); err != nil {
			t.Fatalf("old-ID Delete() after Rotate() error = %v", err)
		}
		disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
		barrier.assertCallbackCounts(t, 1, 1)
		if gotHolder.err != nil || !gotHolder.rotated || !reflect.DeepEqual(gotHolder.record.Snapshot(), replacement.Snapshot()) {
			t.Fatalf("rotate-first Rotate() = (%v, %v, %v), want replacement/true/nil", gotHolder.record.Snapshot(), gotHolder.rotated, gotHolder.err)
		}
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, old.ID(), false)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, replacement.ID(), true)
	})

	t.Run("touch first is inherited by the later replacement", func(t *testing.T) {
		old := multiRuntimeSessionRecord(t, "K", base, base, base.Add(2*time.Hour), base.Add(30*time.Minute))
		if created, err := holderStore.Create(ctx, old); err != nil || !created {
			t.Fatalf("Create(touch-first fixture) = (%v, %v)", created, err)
		}
		newestAccess := base.Add(20 * time.Minute)
		newestIdle := base.Add(50 * time.Minute)
		replacement := multiRuntimeReplacement(t, old, "L", base.Add(10*time.Minute), base.Add(40*time.Minute))
		barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

		type touchResult struct {
			found bool
			err   error
		}
		type rotateResult struct {
			record  sessions.Record
			rotated bool
			err     error
		}
		holderResult := make(chan touchResult, 1)
		contenderResult := make(chan rotateResult, 1)
		go func() {
			_, found, err := holderStore.Touch(ctx, old.ID(), newestAccess, newestIdle)
			holderResult <- touchResult{found: found, err: err}
		}()
		barrier.waitHolderEntered(t)
		go func() {
			published, rotated, err := contenderStore.Rotate(ctx, old.ID(), replacement)
			contenderResult <- rotateResult{record: published, rotated: rotated, err: err}
		}()
		barrier.assertContenderCallbackBlocked(t)
		barrier.release()

		gotHolder := waitMultiRuntimeResult(t, holderResult)
		gotContender := waitMultiRuntimeResult(t, contenderResult)
		disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
		barrier.assertCallbackCounts(t, 1, 1)
		if gotHolder.err != nil || !gotHolder.found {
			t.Fatalf("touch-first Touch() = (found %v, error %v)", gotHolder.found, gotHolder.err)
		}
		if gotContender.err != nil || !gotContender.rotated {
			t.Fatalf("later Rotate() = (%v, %v)", gotContender.rotated, gotContender.err)
		}
		if !gotContender.record.AccessedAt().Equal(newestAccess) || !gotContender.record.IdleExpiresAt().Equal(newestIdle) {
			t.Fatalf("replacement timestamps = (%v, %v), want touched (%v, %v)", gotContender.record.AccessedAt(), gotContender.record.IdleExpiresAt(), newestAccess, newestIdle)
		}
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, old.ID(), false)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, replacement.ID(), true)
	})

	t.Run("rotation first makes stale old ID touch not found", func(t *testing.T) {
		old := multiRuntimeSessionRecord(t, "M", base, base, base.Add(2*time.Hour), base.Add(time.Hour))
		if created, err := holderStore.Create(ctx, old); err != nil || !created {
			t.Fatalf("Create(rotate-touch fixture) = (%v, %v)", created, err)
		}
		replacement := multiRuntimeReplacement(t, old, "N", base.Add(5*time.Minute), base.Add(65*time.Minute))
		barrier := armMultiRuntimeBarrier(t, holderBackend, contenderBackend)

		type rotateResult struct {
			rotated bool
			err     error
		}
		type touchResult struct {
			record sessions.Record
			found  bool
			err    error
		}
		holderResult := make(chan rotateResult, 1)
		contenderResult := make(chan touchResult, 1)
		go func() {
			_, rotated, err := holderStore.Rotate(ctx, old.ID(), replacement)
			holderResult <- rotateResult{rotated: rotated, err: err}
		}()
		barrier.waitHolderEntered(t)
		go func() {
			touched, found, err := contenderStore.Touch(ctx, old.ID(), base.Add(time.Minute), base.Add(61*time.Minute))
			contenderResult <- touchResult{record: touched, found: found, err: err}
		}()
		barrier.assertContenderCallbackBlocked(t)
		barrier.release()

		gotHolder := waitMultiRuntimeResult(t, holderResult)
		gotContender := waitMultiRuntimeResult(t, contenderResult)
		disarmMultiRuntimeBarrier(holderBackend, contenderBackend)
		barrier.assertCallbackCounts(t, 1, 1)
		if gotHolder.err != nil || !gotHolder.rotated {
			t.Fatalf("rotate-first Rotate() = (%v, %v)", gotHolder.rotated, gotHolder.err)
		}
		if gotContender.err != nil || gotContender.found || gotContender.record.ID().Valid() {
			t.Fatalf("stale Touch() = (%v, found %v, error %v), want zero/false/nil", gotContender.record.Snapshot(), gotContender.found, gotContender.err)
		}
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, old.ID(), false)
		assertMultiRuntimeSessionPresence(t, ctx, holderStore, replacement.ID(), true)
	})
}

type multiRuntimeSQLiteBackend struct {
	*sqlite.Backend
	holder  bool
	barrier atomic.Pointer[multiRuntimeCallbackBarrier]
}

func (backend *multiRuntimeSQLiteBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	barrier := backend.barrier.Load()
	if barrier == nil {
		return backend.Backend.CoordinatedAtomic(ctx, callback)
	}
	if backend.holder {
		return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
			barrier.holderCallbackCalls.Add(1)
			barrier.holderEnteredOnce.Do(func() { close(barrier.holderEntered) })
			<-barrier.releaseHolder
			return callback(session)
		})
	}

	barrier.contenderAttemptCalls.Add(1)
	barrier.contenderAttemptedOnce.Do(func() { close(barrier.contenderAttempted) })
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		barrier.contenderCallbackCalls.Add(1)
		barrier.contenderEnteredOnce.Do(func() { close(barrier.contenderEntered) })
		return callback(session)
	})
}

type multiRuntimeCallbackBarrier struct {
	holderEntered      chan struct{}
	releaseHolder      chan struct{}
	contenderAttempted chan struct{}
	contenderEntered   chan struct{}

	holderEnteredOnce      sync.Once
	releaseHolderOnce      sync.Once
	contenderAttemptedOnce sync.Once
	contenderEnteredOnce   sync.Once

	holderCallbackCalls    atomic.Int32
	contenderAttemptCalls  atomic.Int32
	contenderCallbackCalls atomic.Int32
}

func newMultiRuntimeCallbackBarrier() *multiRuntimeCallbackBarrier {
	return &multiRuntimeCallbackBarrier{
		holderEntered:      make(chan struct{}),
		releaseHolder:      make(chan struct{}),
		contenderAttempted: make(chan struct{}),
		contenderEntered:   make(chan struct{}),
	}
}

func armMultiRuntimeBarrier(
	t *testing.T,
	holder *multiRuntimeSQLiteBackend,
	contender *multiRuntimeSQLiteBackend,
) *multiRuntimeCallbackBarrier {
	t.Helper()
	barrier := newMultiRuntimeCallbackBarrier()
	holder.barrier.Store(barrier)
	contender.barrier.Store(barrier)
	t.Cleanup(barrier.release)
	return barrier
}

func disarmMultiRuntimeBarrier(backends ...*multiRuntimeSQLiteBackend) {
	for _, backend := range backends {
		backend.barrier.Store(nil)
	}
}

func (barrier *multiRuntimeCallbackBarrier) waitHolderEntered(t *testing.T) {
	t.Helper()
	waitMultiRuntimeSignal(t, barrier.holderEntered, "holder coordinated callback entry")
}

func (barrier *multiRuntimeCallbackBarrier) assertContenderCallbackBlocked(t *testing.T) {
	t.Helper()
	waitMultiRuntimeSignal(t, barrier.contenderAttempted, "contender coordinated transaction attempt")
	select {
	case <-barrier.contenderEntered:
		t.Fatal("contender callback entered before holder coordinated transaction committed")
	case <-time.After(100 * time.Millisecond):
	}
}

func (barrier *multiRuntimeCallbackBarrier) release() {
	barrier.releaseHolderOnce.Do(func() { close(barrier.releaseHolder) })
}

func (barrier *multiRuntimeCallbackBarrier) assertCallbackCounts(t *testing.T, holder, contender int32) {
	t.Helper()
	if got := barrier.holderCallbackCalls.Load(); got != holder {
		t.Fatalf("holder coordinated callback calls = %d, want %d", got, holder)
	}
	if got := barrier.contenderAttemptCalls.Load(); got != 1 {
		t.Fatalf("contender coordinated attempts = %d, want 1", got)
	}
	if got := barrier.contenderCallbackCalls.Load(); got != contender {
		t.Fatalf("contender coordinated callback calls = %d, want %d", got, contender)
	}
}

func openTwoSystemStateRuntimes(
	t *testing.T,
	ctx context.Context,
	maxSessions int,
) (*Runtime, *Runtime, *multiRuntimeSQLiteBackend, *multiRuntimeSQLiteBackend) {
	t.Helper()
	holder, contender := openMultiRuntimeSQLiteBackends(t, ctx, "system-state.sqlite3")
	explicitlyMigrateSystemState(t, ctx, holder.Backend)
	config := runtimeTestConfig(t, "multi-runtime-system-state-password")
	config.MaxSessions = maxSessions
	if err := ProvisionOperator(ctx, holder, config.provisionConfig(t)); err != nil {
		t.Fatalf("ProvisionOperator(holder Runtime): %v", err)
	}
	holderRuntime, err := OpenExisting(ctx, holder, config.runtimeConfig(t))
	if err != nil {
		t.Fatalf("OpenExisting(holder Runtime): %v", err)
	}
	contenderConfig := runtimeTestConfig(t, "unused-contender-password")
	contenderConfig.MaxSessions = maxSessions
	contenderRuntime, err := OpenExisting(ctx, contender, contenderConfig.runtimeConfig(t))
	if err != nil {
		t.Fatalf("OpenExisting(contender Runtime): %v", err)
	}
	return holderRuntime, contenderRuntime, holder, contender
}

func openMultiRuntimeSQLiteBackends(
	t *testing.T,
	ctx context.Context,
	filename string,
) (*multiRuntimeSQLiteBackend, *multiRuntimeSQLiteBackend) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), filename)
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=5000"
	open := func(holder bool) *multiRuntimeSQLiteBackend {
		backend, err := sqlite.Open(ctx, dataSourceName)
		if err != nil {
			t.Fatalf("sqlite.Open(%q): %v", dataSourceName, err)
		}
		wrapped := &multiRuntimeSQLiteBackend{Backend: backend, holder: holder}
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Errorf("close multi-runtime SQLite backend: %v", err)
			}
		})
		return wrapped
	}
	return open(true), open(false)
}

func multiRuntimeSessionRecord(
	t *testing.T,
	seed string,
	createdAt time.Time,
	accessedAt time.Time,
	absoluteExpiresAt time.Time,
	idleExpiresAt time.Time,
) sessions.Record {
	t.Helper()
	if len(seed) != 1 {
		t.Fatalf("multi-runtime session seed length = %d, want 1", len(seed))
	}
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte(seed), 32)))
	if err != nil {
		t.Fatalf("sessions.ParseID(multi-runtime seed): %v", err)
	}
	return mustSessionStoreRecord(
		t,
		id,
		map[string]string{"owner": "multi-runtime-" + seed},
		createdAt,
		accessedAt,
		absoluteExpiresAt,
		idleExpiresAt,
	)
}

func multiRuntimeReplacement(
	t *testing.T,
	old sessions.Record,
	seed string,
	accessedAt time.Time,
	idleExpiresAt time.Time,
) sessions.Record {
	t.Helper()
	return multiRuntimeSessionRecord(
		t,
		seed,
		old.CreatedAt(),
		accessedAt,
		old.AbsoluteExpiresAt(),
		idleExpiresAt,
	)
}

func assertMultiRuntimeSessionPresence(
	t *testing.T,
	ctx context.Context,
	store sessions.Store,
	id sessions.ID,
	want bool,
) {
	t.Helper()
	_, found, err := store.Load(ctx, id)
	if err != nil || found != want {
		t.Fatalf("Load(%s) = (found %v, error %v), want (found %v, nil)", id, found, err, want)
	}
}

func waitMultiRuntimeSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitMultiRuntimeResult[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(10 * time.Second):
		var zero T
		t.Fatal("timed out waiting for multi-runtime operation result")
		return zero
	}
}
