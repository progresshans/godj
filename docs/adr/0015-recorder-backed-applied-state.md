# ADR-0015: Recorder-backed applied state는 별도 read port와 explicit history check를 사용한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0013, GDJ-0014, MIG-027..MIG-036, Q-012
- 대체하는 ADR: 없음

## 맥락

[ADR-0013](0013-immutable-migration-planner.md)의 `Planner`는 caller가 제공한
`AppliedState`만 사용해 zero-I/O plan을 계산합니다. [ADR-0014](0014-migration-plan-execution-atomic-reverse.md)의
`Executor.ExecutePlan`은 이미 만들어진 `ProjectState`, migration definition과 plan을 실행하지만,
durable recorder를 읽어 `AppliedState`를 구성하는 제품 경계는 없습니다.

GDJ-0013은 Django 6.1의 recorder table absent/empty, record/unrecord 뒤 fresh read, database
격리, applied-prefix tail, fully-applied empty plan, unknown legacy row, inconsistent known history와
중간 실패 뒤 restart를 MIG-027..036으로 고정했습니다. 이 reference를 제품에 연결하려면
read I/O, raw identity 검증과 graph history validation의 소유권을 정해야 합니다.

Recorder read와 plan execution을 하나의 API로 묶을 수는 없습니다. Recorder key만으로
historical `ProjectState`를 일반적으로 재구성할 수 없고, read와 execution 사이를 보호하는
lock 또는 revision token도 아직 없습니다. 편의 API가 이 경계를 숨기면 stale plan을 원자적
restart처럼 오해하게 됩니다.

## 결정 기준

- 기존 `db/sqlite → migrations/backend ← migrations` 의존 방향 보존
- `AtomicBackend`/`Transaction`과 read-only history port의 source compatibility
- Recorder table이 없을 때 write 없이 empty snapshot을 반환
- Raw DB identity와 validated `MigrationKey` 의미 분리
- Unknown legacy row 보존과 known inconsistent history 거부
- Context/error cause 전달과 structured error taxonomy
- Fresh backend, multi-DB isolation과 deterministic plan 검증 가능성
- Read snapshot과 execution 사이 TOCTOU를 보장으로 과장하지 않음

## 고려한 선택지

### `migrations`가 `[]MigrationKey` reader interface를 직접 소유

API는 단순하지만 SQLite backend가 top-level `migrations` package를 import하게 되어
`db/sqlite → migrations → migrations/backend` 결합이 생깁니다. 향후 core/backend 편의 계층을
추가할 때 cycle 가능성이 커지고 raw transport가 이미 semantic key인 것처럼 보입니다.

### `migrations/backend`가 raw transport와 별도 reader port를 소유

`AppliedMigration{App, Name}` transport와 `AppliedMigrationReader`만 backend-neutral package에
두고 core가 이를 `MigrationKey`로 변환해 `NewAppliedState`로 검증합니다. 작은 DTO 중복은
생기지만 기존 package 방향과 transaction interface를 보존합니다.

### Recorder read부터 execution까지 하나의 restart API로 제공

사용 표면은 짧아지지만 recorder identity만으로 `ProjectState`를 복원할 수 없고 concurrent
writer를 막는 lock도 없습니다. `Migrate`, `PlanAndExecute`, `RestartExecutor`를 지금 공개하면
불완전한 lifecycle을 안정 API로 고정합니다.

## 결정

GDJ-0014는 다음 read boundary를 채택했습니다.

```go
// package migrations/backend
type AppliedMigration struct {
    App  string
    Name string
}

type AppliedMigrationReader interface {
    ReadAppliedMigrations(context.Context) ([]AppliedMigration, error)
}
```

```go
// package migrations
func LoadAppliedState(
    ctx context.Context,
    reader backend.AppliedMigrationReader,
) (AppliedState, error)

func (p Planner) CheckHistory(applied AppliedState) error
```

- `AppliedMigrationReader`는 `Recorder`, `Transaction` 또는 `AtomicBackend`에 embed하지 않습니다.
- Core는 모든 raw record를 `MigrationKey`로 복사한 뒤 `NewAppliedState`를 통과시킵니다.
- Empty app/name과 duplicate는 기존 `invalid_applied_state`/`duplicate_applied`입니다.
- Graph에 없는 정상 key는 보존하고, known child의 known parent 누락은
  `CheckHistory`의 `inconsistent_applied_history`입니다.
- `Planner.Plan`의 기존 history validation도 유지합니다. Explicit `CheckHistory`는 startup
  preflight가 target validation/planning보다 먼저 일어났음을 표현합니다.
- `AppliedState.Keys()`는 planning에 필요하지 않으므로 이번 단면에서는 추가하지 않습니다.
  Listing/showmigrations 소비자가 생기면 정렬된 clone accessor를 별도 결정합니다.

제품 호출 순서는 다음처럼 명시적으로 분리합니다.

```go
applied, err := migrations.LoadAppliedState(ctx, reader)
if err != nil {
    return err
}
if err := planner.CheckHistory(applied); err != nil {
    return err
}
plan, err := planner.Plan(applied, targets...)
```

실행이 필요한 상위 계층만 별도로 `Executor.ExecutePlan`을 호출합니다. GDJ-0014는 이 두
단계를 한 공개 API로 결합하지 않습니다.

## SQLite 의미

SQLite implementation은 recorder table을 생성하거나 migration transaction을 열지 않고
한 번의 ordered read를 수행합니다. 현재 구현은 table-qualified column을 사용하지만
SQL text 자체는 compatibility contract가 아닙니다.

```sql
SELECT "godj_migrations"."app", "godj_migrations"."name"
FROM "godj_migrations"
ORDER BY "godj_migrations"."app", "godj_migrations"."name"
```

정확히 recorder table이 없다는 driver 오류만 empty result로 정규화합니다. Malformed table,
column 누락, scan/rows/close 오류는 empty로 삼키지 않습니다. `ensureMigrationRecorder`,
`BeginMigration`과 `ExecContext`를 read path에서 호출하지 않습니다.

Database alias 문자열을 새 API에 넣지 않습니다. 각 `sqlite.Backend`가 하나의 DSN에
결속되고 상위 router가 적절한 `AppliedMigrationReader`를 선택합니다. MIG-031의 alias는
서로 다른 backend/database를 식별하는 observation label입니다.

## 오류와 context

Recorder I/O 실패는 semantic planning error와 구분합니다. Public classification은
`RecorderError{Category: migration_recorder_error, Code: read_failed}`이고 `Unwrap`으로
cause를 보존합니다.

- nil reader/context, pre/in-flight cancellation과 query/scan/rows 오류는
  `migration_recorder_error/read_failed`이며 `errors.Is`/`errors.As`로 원인을 보존합니다.
- Raw invalid/duplicate key는 기존 `PlanningError`입니다.
- Known inconsistent history와 invalid/missing target도 기존 planning taxonomy를 유지합니다.
- 오류 message와 SQLite 원문은 compatibility contract가 아닙니다.

`LoadAppliedState`는 호출 전 context를 검사하고 reader 결과를 즉시 복사합니다. 정상 read가
완료된 뒤 늦게 도착한 cancellation은 성공 snapshot을 실패로 뒤집지 않습니다. 같은 backend의
concurrent read는 race-safe해야 하지만 concurrent migration writer와의 직렬화는 보장하지
않습니다.

## 결과와 비용

- 기존 migration transaction API와 external fake를 깨뜨리지 않고 durable history read를
  추가할 수 있습니다.
- Backend DTO와 core key가 분리되어 raw 데이터가 semantic validation을 우회하지 않습니다.
- Explicit history check가 MIG-035의 pre-plan failure timing을 제품 API에 표현합니다.
- Read 결과는 한 시점의 snapshot일 뿐이며 subsequent plan/execution의 최신성을 보장하지
  않습니다.
- Listing accessor, router와 full lifecycle 편의 API는 추가 결정을 기다립니다.

## 의도적으로 결정하지 않은 것

- Recorder history에서 historical `ProjectState` 재구성
- Read/check/plan/execute를 묶는 public migrate API
- Migration file/source loader, CLI와 generator
- Multi-process lock, revision token, session binding과 concurrent executor 직렬화
- Crash repair, schema/recorder reconciliation
- Alias registry/router, PostgreSQL/MySQL/Oracle backend
- Replacement/squash/merge/fake와 data migration callback ABI

## 검증과 구현 결과

- MIG-027..036 live SQLite adapter가 locked Django oracle과 10개 semantic 0-diff
- Recorder table absent read가 table/row를 생성하지 않는 unit/integration gate
- Fresh file backend와 서로 다른 두 database 격리
- Invalid/duplicate/unknown raw record와 explicit `CheckHistory` timing
- Query/scan/rows/context 오류 cause와 typed-nil reader
- Existing `AtomicBackend`/`Transaction` fake source compatibility
- `migrations/backend`이 top-level `migrations`를 import하지 않는 dependency gate
- Concurrent read race, full/race/CGO=0/vet와 deterministic two-process actual
- Locked Django oracle/static fixture bytes, seven-set 42 cross-binding과 기존 제품 결과 보존

GDJ-0014가 이 경계를 제품 commit
`a9ce9597551840f1be8e1f27006d427842f38081`에 구현했습니다. Backend DTO/read port는
transaction interface와 분리됐고, core는 reader 반환을 복사한 뒤 기존
`NewAppliedState`로 검증합니다. SQLite는 read-only fresh file backend, absent/empty,
record/unrecord, database isolation, malformed schema, cancellation, rows lifecycle와 concurrent
read/close race를 검증했습니다.

MIG-027..036 GoDj actual은 locked Django oracle과 10-contract semantic 0-diff이고 두
actual은 33,795 bytes의 byte-identical 결과입니다. 기존 여섯 product set의
`63 passing + 4 deviation`은 회귀 없이 유지됐고 새 set이 10 `passing`이 되어
제품 분류는 `73 passing + 4 deviation`입니다. `make check`, full uncached
regular/race/`CGO_ENABLED=0`/vet, portable 94 pass/9 skip와 exact Python 94/94가
통과했으며 세부 증거는
[EVID-20260808-013](../status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
기록했습니다.
