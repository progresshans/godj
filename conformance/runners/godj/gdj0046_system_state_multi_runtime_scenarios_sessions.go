package godj

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

type gdj0046PersistedSession struct {
	digest  string
	payload string
}

func gdj0046ReadSessionStorage(
	ctx context.Context,
	backend db.Queryer,
	limit int,
) ([]gdj0046PersistedSession, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	digest := query.NewFieldRef("digest", "digest", query.FieldString, false)
	payload := query.NewFieldRef("payload", "payload", query.FieldString, false)
	plan, err := query.NewPlan(systemStateSessionTable, []query.FieldRef{id, digest, payload}).WithLimit(limit)
	if err != nil {
		return nil, err
	}
	plan = plan.WithOrderings(query.NewOrdering(id, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := make([]gdj0046PersistedSession, 0, limit)
	for rows.Next() {
		var ignored int64
		var row gdj0046PersistedSession
		if err := rows.Scan(&ignored, &row.digest, &row.payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, row)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}

func gdj0046DuplicateDigests(rows []gdj0046PersistedSession) int {
	seen := make(map[string]int, len(rows))
	duplicates := 0
	for _, row := range rows {
		seen[row.digest]++
		if seen[row.digest] > 1 {
			duplicates++
		}
	}
	return duplicates
}

func gdj0046StorageStrings(rows []gdj0046PersistedSession) []string {
	result := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		result = append(result, row.digest, row.payload)
	}
	return result
}

func gdj0046SessionRecord(
	seed byte,
	createdAt, accessedAt, absoluteExpiresAt, idleExpiresAt time.Time,
) (sessions.Record, error) {
	return systemStateSessionRecord(
		seed,
		map[string]string{"owner": fmt.Sprintf("gdj0046-%02x", seed)},
		createdAt,
		accessedAt,
		absoluteExpiresAt,
		idleExpiresAt,
	)
}

func gdj0046Replacement(
	old sessions.Record,
	seed byte,
	accessedAt, idleExpiresAt time.Time,
) (sessions.Record, error) {
	return gdj0046SessionRecord(
		seed,
		old.CreatedAt(),
		accessedAt,
		old.AbsoluteExpiresAt(),
		idleExpiresAt,
	)
}

func gdj0046SessionFound(ctx context.Context, store sessions.Store, id sessions.ID) (bool, error) {
	_, found, err := store.Load(ctx, id)
	return found, err
}

type gdj0046CreateResult struct {
	created bool
	err     error
}

func systemStateConcurrentSessionCapacity(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	base := time.Now().UTC().Round(0)
	capacity, err := newGDJ0046RuntimePair(ctx, 1, 8, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer capacity.cleanup()
	holderStore := capacity.holder.SessionStore()
	contenderStore := capacity.contender.SessionStore()
	expired, err := gdj0046SessionRecord(
		0x41,
		base.Add(-2*time.Hour),
		base.Add(-2*time.Hour),
		base.Add(-time.Hour),
		base.Add(-90*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, expired); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed global capacity row: created=%v err=%w", created, err)
	}
	holderRecord, err := gdj0046SessionRecord(0x42, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderRecord, err := gdj0046SessionRecord(0x43, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	capacity.backends.resetDML()
	barrier := capacity.backends.arm()
	holderResult := make(chan gdj0046CreateResult, 1)
	contenderResult := make(chan gdj0046CreateResult, 1)
	go func() {
		created, err := holderStore.Create(ctx, holderRecord)
		holderResult <- gdj0046CreateResult{created: created, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "capacity holder callback"); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		created, err := contenderStore.Create(ctx, contenderRecord)
		contenderResult <- gdj0046CreateResult{created: created, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, capacity.backends, barrier); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	barrier.release()
	holderCreated, err := gdj0046WaitResult(ctx, holderResult, "capacity holder result")
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderCreated, err := gdj0046WaitResult(ctx, contenderResult, "capacity contender result")
	if err != nil {
		return protocol.Observation{}, err
	}
	capacity.backends.disarm()
	capacityRows, err := gdj0046ReadSessionStorage(ctx, capacity.holder, 4)
	if err != nil {
		return protocol.Observation{}, err
	}
	expiredFound, err := gdj0046SessionFound(ctx, holderStore, expired.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	holderFound, err := gdj0046SessionFound(ctx, holderStore, holderRecord.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderFound, err := gdj0046SessionFound(ctx, contenderStore, contenderRecord.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	if holderCreated.err != nil || !holderCreated.created ||
		contenderCreated.created || !errors.Is(contenderCreated.err, &sessions.Error{Code: sessions.CodeStoreFull}) ||
		expiredFound || !holderFound || contenderFound || len(capacityRows) != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"global capacity facts drifted: holder=%+v contender=%+v rows=%d present=%v/%v/%v",
			holderCreated,
			contenderCreated,
			len(capacityRows),
			expiredFound,
			holderFound,
			contenderFound,
		)
	}

	// A separate same-digest race proves the lack of a duplicate publication.
	unique, err := newGDJ0046RuntimePair(ctx, 2, 8, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer unique.cleanup()
	same, err := gdj0046SessionRecord(0x44, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	uniqueBarrier := unique.backends.arm()
	uniqueHolder := make(chan gdj0046CreateResult, 1)
	uniqueContender := make(chan gdj0046CreateResult, 1)
	go func() {
		created, err := unique.holder.SessionStore().Create(ctx, same)
		uniqueHolder <- gdj0046CreateResult{created: created, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, uniqueBarrier.holderEntered, "same-digest holder callback"); err != nil {
		uniqueBarrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		created, err := unique.contender.SessionStore().Create(ctx, same)
		uniqueContender <- gdj0046CreateResult{created: created, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, unique.backends, uniqueBarrier); err != nil {
		uniqueBarrier.release()
		return protocol.Observation{}, err
	}
	uniqueBarrier.release()
	uniqueHolderResult, err := gdj0046WaitResult(ctx, uniqueHolder, "same-digest holder result")
	if err != nil {
		return protocol.Observation{}, err
	}
	uniqueContenderResult, err := gdj0046WaitResult(ctx, uniqueContender, "same-digest contender result")
	if err != nil {
		return protocol.Observation{}, err
	}
	unique.backends.disarm()
	uniqueRows, err := gdj0046ReadSessionStorage(ctx, unique.holder, 4)
	if err != nil {
		return protocol.Observation{}, err
	}
	if uniqueHolderResult.err != nil || !uniqueHolderResult.created ||
		uniqueContenderResult.err != nil || uniqueContenderResult.created || len(uniqueRows) != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"same-digest facts drifted: holder=%+v contender=%+v rows=%d",
			uniqueHolderResult,
			uniqueContenderResult,
			len(uniqueRows),
		)
	}

	// Reaping is bounded to the single slot needed by an incoming record.
	bounded, err := newGDJ0046RuntimePair(ctx, 3, 8, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer bounded.cleanup()
	oldest, err := gdj0046SessionRecord(
		0x45,
		base.Add(-3*time.Hour),
		base.Add(-3*time.Hour),
		base.Add(-2*time.Hour),
		base.Add(-150*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	secondExpired, err := gdj0046SessionRecord(
		0x46,
		base.Add(-2*time.Hour),
		base.Add(-2*time.Hour),
		base.Add(-time.Hour),
		base.Add(-90*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	live, err := gdj0046SessionRecord(0x47, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	for _, record := range []sessions.Record{oldest, secondExpired, live} {
		if created, err := bounded.holder.SessionStore().Create(ctx, record); err != nil || !created {
			return protocol.Observation{}, fmt.Errorf("seed bounded reap row: created=%v err=%w", created, err)
		}
	}
	incoming, err := gdj0046SessionRecord(0x48, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := bounded.contender.SessionStore().Create(ctx, incoming); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("bounded reap create: created=%v err=%w", created, err)
	}
	oldestFound, err := gdj0046SessionFound(ctx, bounded.holder.SessionStore(), oldest.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	secondExpiredFound, err := gdj0046SessionFound(ctx, bounded.holder.SessionStore(), secondExpired.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	boundedRows, err := gdj0046ReadSessionStorage(ctx, bounded.holder, 5)
	if err != nil {
		return protocol.Observation{}, err
	}
	unboundedReap := oldestFound || !secondExpiredFound || len(boundedRows) != 3
	if unboundedReap {
		return protocol.Observation{}, fmt.Errorf(
			"bounded reap facts drifted: oldest=%v second=%v rows=%d",
			oldestFound,
			secondExpiredFound,
			len(boundedRows),
		)
	}

	duplicates := gdj0046DuplicateDigests(capacityRows) +
		gdj0046DuplicateDigests(uniqueRows) + gdj0046DuplicateDigests(boundedRows)
	overshoots := 0
	for _, state := range []struct{ rows, capacity int }{
		{len(capacityRows), 1},
		{len(uniqueRows), 2},
		{len(boundedRows), 3},
	} {
		if state.rows > state.capacity {
			overshoots += state.rows - state.capacity
		}
	}
	result := protocol.Object(map[string]protocol.Value{
		"capacity_overshoot": protocol.Boolean(overshoots != 0),
		"concurrent_create":  protocol.String("linearized"),
		"digest_collision":   protocol.Boolean(duplicates != 0),
		"reap_scope":         protocol.String("global"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"capacity_bound_preserved": protocol.Boolean(overshoots == 0),
		"duplicate_digests":        systemStateInt(duplicates),
		"unbounded_reap":           protocol.Boolean(unboundedReap),
	})
	storage := append(gdj0046StorageStrings(capacityRows), gdj0046StorageStrings(uniqueRows)...)
	storage = append(storage, gdj0046StorageStrings(boundedRows)...)
	secrets := []string{
		expired.ID().Encoded(), holderRecord.ID().Encoded(), contenderRecord.ID().Encoded(), same.ID().Encoded(),
		oldest.ID().Encoded(), secondExpired.ID().Encoded(), live.ID().Encoded(), incoming.ID().Encoded(),
	}
	rawBearers, err := systemStateSecretOccurrences([]protocol.Value{result, dbState}, storage, secrets...)
	if err != nil {
		return protocol.Observation{}, err
	}
	coordinationRetries := barrier.callbackRetries() + uniqueBarrier.callbackRetries()
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"capacity_overshoots":  systemStateInt(overshoots),
		"coordination_retries": systemStateInt64(coordinationRetries),
		"raw_bearers_observed": systemStateInt64(rawBearers),
	}))
}

type gdj0046TouchResult struct {
	record sessions.Record
	found  bool
	err    error
}

func systemStateConcurrentTouchMonotonicity(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	pair, err := newGDJ0046RuntimePair(ctx, 8, 8, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer pair.cleanup()
	base := time.Now().UTC().Round(0)
	record, err := gdj0046SessionRecord(0x51, base, base, base.Add(2*time.Hour), base.Add(30*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := pair.holder.SessionStore().Create(ctx, record); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed touch row: created=%v err=%w", created, err)
	}
	newestAccess := base.Add(20 * time.Minute)
	newestIdle := base.Add(50 * time.Minute)
	staleAccess := base.Add(10 * time.Minute)
	staleIdle := base.Add(40 * time.Minute)
	pair.backends.resetDML()
	barrier := pair.backends.arm()
	holderResult := make(chan gdj0046TouchResult, 1)
	contenderResult := make(chan gdj0046TouchResult, 1)
	go func() {
		touched, found, err := pair.holder.SessionStore().Touch(ctx, record.ID(), newestAccess, newestIdle)
		holderResult <- gdj0046TouchResult{record: touched, found: found, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "touch holder callback"); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		touched, found, err := pair.contender.SessionStore().Touch(ctx, record.ID(), staleAccess, staleIdle)
		contenderResult <- gdj0046TouchResult{record: touched, found: found, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, barrier); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	barrier.release()
	holder, err := gdj0046WaitResult(ctx, holderResult, "touch holder result")
	if err != nil {
		return protocol.Observation{}, err
	}
	contender, err := gdj0046WaitResult(ctx, contenderResult, "touch contender result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	stored, found, err := pair.holder.SessionStore().Load(ctx, record.ID())
	if err != nil || !found {
		return protocol.Observation{}, fmt.Errorf("load touched row: found=%v err=%w", found, err)
	}
	accessedRegressions := 0
	idleRegressions := 0
	for _, candidate := range []sessions.Record{holder.record, contender.record, stored} {
		if candidate.AccessedAt().Before(newestAccess) {
			accessedRegressions++
		}
		if candidate.IdleExpiresAt().Before(newestIdle) {
			idleRegressions++
		}
	}
	if holder.err != nil || contender.err != nil || !holder.found || !contender.found ||
		accessedRegressions != 0 || idleRegressions != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"touch facts drifted: holder=%v/%v contender=%v/%v regressions=%d/%d",
			holder.found,
			holder.err,
			contender.found,
			contender.err,
			accessedRegressions,
			idleRegressions,
		)
	}
	liveRows, err := systemStateCountRows(ctx, pair.holder, systemStateSessionTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	touchWinners := pair.backends.holder.updates.Load() + pair.backends.contender.updates.Load()
	result := protocol.Object(map[string]protocol.Value{
		"accessed_at_monotonic": protocol.Boolean(accessedRegressions == 0),
		"idle_expiry_monotonic": protocol.Boolean(idleRegressions == 0),
		"out_of_order_touch":    protocol.String("newest_state_preserved"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"accessed_at_regressions": systemStateInt(accessedRegressions),
		"idle_expiry_regressions": systemStateInt(idleRegressions),
		"live_rows":               systemStateInt(liveRows),
	})
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"coordination_retries": systemStateInt64(barrier.callbackRetries()),
		"stale_overwrites":     systemStateInt(accessedRegressions + idleRegressions),
		"touch_winners":        systemStateInt64(touchWinners),
	}))
}

type gdj0046RotateResult struct {
	record  sessions.Record
	rotated bool
	err     error
}

func systemStateConcurrentSessionRotation(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	pair, err := newGDJ0046RuntimePair(ctx, 16, 8, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer pair.cleanup()
	base := time.Now().UTC().Round(0)
	holderStore := pair.holder.SessionStore()
	contenderStore := pair.contender.SessionStore()
	automaticRetries := int64(0)

	// Exactly one of two replacement records can be published.
	old, err := gdj0046SessionRecord(0x61, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, old); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed rotation row: created=%v err=%w", created, err)
	}
	holderReplacement, err := gdj0046Replacement(old, 0x62, base.Add(5*time.Minute), base.Add(65*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderReplacement, err := gdj0046Replacement(old, 0x63, base.Add(6*time.Minute), base.Add(66*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	barrier := pair.backends.arm()
	holderResult := make(chan gdj0046RotateResult, 1)
	contenderResult := make(chan gdj0046RotateResult, 1)
	go func() {
		published, rotated, err := holderStore.Rotate(ctx, old.ID(), holderReplacement)
		holderResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "rotation holder callback"); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		published, rotated, err := contenderStore.Rotate(ctx, old.ID(), contenderReplacement)
		contenderResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, barrier); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	barrier.release()
	firstHolder, err := gdj0046WaitResult(ctx, holderResult, "rotation holder result")
	if err != nil {
		return protocol.Observation{}, err
	}
	firstContender, err := gdj0046WaitResult(ctx, contenderResult, "rotation contender result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	automaticRetries += barrier.callbackRetries()
	rotationWinners := systemStateBoolInt(firstHolder.rotated) + systemStateBoolInt(firstContender.rotated)
	oldFound, err := gdj0046SessionFound(ctx, holderStore, old.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	holderReplacementFound, err := gdj0046SessionFound(ctx, holderStore, holderReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderReplacementFound, err := gdj0046SessionFound(ctx, holderStore, contenderReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	replacementRows := systemStateBoolInt(holderReplacementFound) + systemStateBoolInt(contenderReplacementFound)
	duplicateReplacements := replacementRows - 1
	if duplicateReplacements < 0 {
		duplicateReplacements = 0
	}
	if firstHolder.err != nil || firstContender.err != nil || rotationWinners != 1 || oldFound || replacementRows != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"rotation publication drifted: holder=%+v contender=%+v old=%v replacements=%d",
			firstHolder,
			firstContender,
			oldFound,
			replacementRows,
		)
	}

	// Logout first denies the later rotate.
	logoutOld, err := gdj0046SessionRecord(0x64, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, logoutOld); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed logout-first row: created=%v err=%w", created, err)
	}
	logoutReplacement, err := gdj0046Replacement(logoutOld, 0x65, base.Add(5*time.Minute), base.Add(65*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutBarrier := pair.backends.arm()
	deleteResult := make(chan error, 1)
	logoutRotateResult := make(chan gdj0046RotateResult, 1)
	go func() { deleteResult <- holderStore.Delete(ctx, logoutOld.ID()) }()
	if err := gdj0046WaitSignal(ctx, logoutBarrier.holderEntered, "logout-first holder callback"); err != nil {
		logoutBarrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		published, rotated, err := contenderStore.Rotate(ctx, logoutOld.ID(), logoutReplacement)
		logoutRotateResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, logoutBarrier); err != nil {
		logoutBarrier.release()
		return protocol.Observation{}, err
	}
	logoutBarrier.release()
	logoutDeleteErr, err := gdj0046WaitResult(ctx, deleteResult, "logout-first delete result")
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutRotate, err := gdj0046WaitResult(ctx, logoutRotateResult, "logout-first rotate result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	automaticRetries += logoutBarrier.callbackRetries()
	logoutOldFound, err := gdj0046SessionFound(ctx, holderStore, logoutOld.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutReplacementFound, err := gdj0046SessionFound(ctx, holderStore, logoutReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutFirstDenied := logoutDeleteErr == nil && logoutRotate.err == nil && !logoutRotate.rotated &&
		!logoutOldFound && !logoutReplacementFound
	if !logoutFirstDenied {
		return protocol.Observation{}, fmt.Errorf(
			"logout-first facts drifted: delete=%v rotate=%+v present=%v/%v",
			logoutDeleteErr,
			logoutRotate,
			logoutOldFound,
			logoutReplacementFound,
		)
	}

	// Rotate first preserves the replacement after a stale old-ID logout.
	rotateFirstOld, err := gdj0046SessionRecord(0x66, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, rotateFirstOld); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed rotate-first logout row: created=%v err=%w", created, err)
	}
	rotateFirstReplacement, err := gdj0046Replacement(rotateFirstOld, 0x67, base.Add(5*time.Minute), base.Add(65*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	rotateFirstBarrier := pair.backends.arm()
	rotateFirstResult := make(chan gdj0046RotateResult, 1)
	staleDeleteResult := make(chan error, 1)
	go func() {
		published, rotated, err := holderStore.Rotate(ctx, rotateFirstOld.ID(), rotateFirstReplacement)
		rotateFirstResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, rotateFirstBarrier.holderEntered, "rotate-first holder callback"); err != nil {
		rotateFirstBarrier.release()
		return protocol.Observation{}, err
	}
	go func() { staleDeleteResult <- contenderStore.Delete(ctx, rotateFirstOld.ID()) }()
	if err := gdj0046AssertBlocked(ctx, pair.backends, rotateFirstBarrier); err != nil {
		rotateFirstBarrier.release()
		return protocol.Observation{}, err
	}
	rotateFirstBarrier.release()
	rotateFirst, err := gdj0046WaitResult(ctx, rotateFirstResult, "rotate-first result")
	if err != nil {
		return protocol.Observation{}, err
	}
	staleDeleteErr, err := gdj0046WaitResult(ctx, staleDeleteResult, "stale delete result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	automaticRetries += rotateFirstBarrier.callbackRetries()
	rotateFirstOldFound, err := gdj0046SessionFound(ctx, holderStore, rotateFirstOld.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	rotateFirstReplacementFound, err := gdj0046SessionFound(ctx, holderStore, rotateFirstReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	rotateFirstPreserved := rotateFirst.err == nil && rotateFirst.rotated && staleDeleteErr == nil &&
		!rotateFirstOldFound && rotateFirstReplacementFound
	if !rotateFirstPreserved {
		return protocol.Observation{}, fmt.Errorf(
			"rotate-first logout facts drifted: rotate=%+v delete=%v present=%v/%v",
			rotateFirst,
			staleDeleteErr,
			rotateFirstOldFound,
			rotateFirstReplacementFound,
		)
	}

	// A winning touch is inherited by the later replacement.
	touchOld, err := gdj0046SessionRecord(0x68, base, base, base.Add(2*time.Hour), base.Add(30*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, touchOld); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed touch-first row: created=%v err=%w", created, err)
	}
	newestAccess := base.Add(20 * time.Minute)
	newestIdle := base.Add(50 * time.Minute)
	touchReplacement, err := gdj0046Replacement(touchOld, 0x69, base.Add(10*time.Minute), base.Add(40*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	touchBarrier := pair.backends.arm()
	touchResult := make(chan gdj0046TouchResult, 1)
	touchRotateResult := make(chan gdj0046RotateResult, 1)
	go func() {
		record, found, err := holderStore.Touch(ctx, touchOld.ID(), newestAccess, newestIdle)
		touchResult <- gdj0046TouchResult{record: record, found: found, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, touchBarrier.holderEntered, "touch-first holder callback"); err != nil {
		touchBarrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		published, rotated, err := contenderStore.Rotate(ctx, touchOld.ID(), touchReplacement)
		touchRotateResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, touchBarrier); err != nil {
		touchBarrier.release()
		return protocol.Observation{}, err
	}
	touchBarrier.release()
	touched, err := gdj0046WaitResult(ctx, touchResult, "touch-first touch result")
	if err != nil {
		return protocol.Observation{}, err
	}
	touchRotated, err := gdj0046WaitResult(ctx, touchRotateResult, "touch-first rotate result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	automaticRetries += touchBarrier.callbackRetries()
	touchOldFound, err := gdj0046SessionFound(ctx, holderStore, touchOld.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	touchReplacementFound, err := gdj0046SessionFound(ctx, holderStore, touchReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	touchFirstPreserved := touched.err == nil && touched.found && touchRotated.err == nil && touchRotated.rotated &&
		touchRotated.record.AccessedAt().Equal(newestAccess) && touchRotated.record.IdleExpiresAt().Equal(newestIdle) &&
		!touchOldFound && touchReplacementFound
	if !touchFirstPreserved {
		return protocol.Observation{}, fmt.Errorf(
			"touch-first rotation facts drifted: touch=%+v rotate=%+v present=%v/%v",
			touched,
			touchRotated,
			touchOldFound,
			touchReplacementFound,
		)
	}

	// Rotate first makes a stale old-ID touch a read-only not-found result.
	staleOld, err := gdj0046SessionRecord(0x6a, base, base, base.Add(2*time.Hour), base.Add(time.Hour))
	if err != nil {
		return protocol.Observation{}, err
	}
	if created, err := holderStore.Create(ctx, staleOld); err != nil || !created {
		return protocol.Observation{}, fmt.Errorf("seed rotate-first touch row: created=%v err=%w", created, err)
	}
	staleReplacement, err := gdj0046Replacement(staleOld, 0x6b, base.Add(5*time.Minute), base.Add(65*time.Minute))
	if err != nil {
		return protocol.Observation{}, err
	}
	staleBarrier := pair.backends.arm()
	staleRotateResult := make(chan gdj0046RotateResult, 1)
	staleTouchResult := make(chan gdj0046TouchResult, 1)
	go func() {
		published, rotated, err := holderStore.Rotate(ctx, staleOld.ID(), staleReplacement)
		staleRotateResult <- gdj0046RotateResult{record: published, rotated: rotated, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, staleBarrier.holderEntered, "stale-touch rotate callback"); err != nil {
		staleBarrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		record, found, err := contenderStore.Touch(
			ctx,
			staleOld.ID(),
			base.Add(time.Minute),
			base.Add(61*time.Minute),
		)
		staleTouchResult <- gdj0046TouchResult{record: record, found: found, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, staleBarrier); err != nil {
		staleBarrier.release()
		return protocol.Observation{}, err
	}
	staleBarrier.release()
	staleRotated, err := gdj0046WaitResult(ctx, staleRotateResult, "stale-touch rotate result")
	if err != nil {
		return protocol.Observation{}, err
	}
	staleTouched, err := gdj0046WaitResult(ctx, staleTouchResult, "stale-touch result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	automaticRetries += staleBarrier.callbackRetries()
	staleOldFound, err := gdj0046SessionFound(ctx, holderStore, staleOld.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	staleReplacementFound, err := gdj0046SessionFound(ctx, holderStore, staleReplacement.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	staleNotFound := staleRotated.err == nil && staleRotated.rotated && staleTouched.err == nil &&
		!staleTouched.found && !staleTouched.record.ID().Valid() && !staleOldFound && staleReplacementFound
	if !staleNotFound {
		return protocol.Observation{}, fmt.Errorf(
			"rotate-first stale-touch facts drifted: rotate=%+v touch=%+v present=%v/%v",
			staleRotated,
			staleTouched,
			staleOldFound,
			staleReplacementFound,
		)
	}

	resurrectionWrites := systemStateBoolInt(logoutOldFound) +
		systemStateBoolInt(logoutReplacementFound) + systemStateBoolInt(staleOldFound)
	result := protocol.Object(map[string]protocol.Value{
		"logout_first":               protocol.String("later_rotate_denied"),
		"old_bearer_resurrected":     protocol.Boolean(resurrectionWrites != 0),
		"rotate_first_old_id_logout": protocol.String("replacement_preserved"),
		"rotate_first_stale_old_id_touch": protocol.Object(map[string]protocol.Value{
			"old_bearer_resurrected": protocol.Boolean(staleOldFound),
			"outcome":                protocol.String("not_found"),
		}),
		"rotation_publication": protocol.String("exactly_one_winner"),
		"touch_first_then_rotate": protocol.Object(map[string]protocol.Value{
			"old_rows":         systemStateInt(systemStateBoolInt(touchOldFound)),
			"replacement_rows": systemStateInt(systemStateBoolInt(touchReplacementFound)),
		}),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"duplicate_replacements":  systemStateInt(duplicateReplacements),
		"old_rows_after_rotation": systemStateInt(systemStateBoolInt(oldFound)),
		"replacement_rows":        systemStateInt(replacementRows),
	})
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries":   systemStateInt64(automaticRetries),
		"resurrection_writes": systemStateInt(resurrectionWrites),
		"rotation_winners":    systemStateInt(rotationWinners),
	}))
}
