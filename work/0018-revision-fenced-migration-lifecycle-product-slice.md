---
id: GDJ-0018
status: completed
updated: 2026-08-09
baseline_branch: "main"
baseline_commit: "d0df905996fc1d80065ac696a2bff4bc3ddb4b2e"
depends_on: ["GDJ-0017"]
contracts: ["MIG-047..MIG-056", "Q-012", "DEV-0002"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "migrations/backend/lifecycle.go", "migrations/lifecycle.go", "migrations/lifecycle_test.go", "migrations/executor.go", "migrations/executor_test.go", "migrations/execution.go", "migrations/execution_test.go", "migrations/planner_graph.go", "migrations/planner_test.go", "migrations/external_test.go", "db/sqlite/backend_internal_test.go", "db/sqlite/migration_lifecycle.go", "db/sqlite/migration_lifecycle_test.go", "db/sqlite/migration_backend.go", "db/sqlite/migration_backend_test.go", "db/sqlite/migration_sql.go", "db/sqlite/migration_sql_test.go", "internal/compiletest/compile_test.go", "internal/compiletest/testdata/migration_external_consumer.go.txt", "conformance/contracts/migration-lifecycle-manifest.json", "conformance/fixtures/godj-migration-lifecycle-deviation-expected.json", "conformance/runners/django/tests/test_migration_lifecycle_scenarios.py", "conformance/runners/godj/migration_lifecycle_scenarios.go", "conformance/runners/godj/runner.go", "conformance/runners/godj/runner_test.go", "conformance/cmd/godjcheck/deviation_policy.go", "conformance/cmd/godjcheck/main.go", "conformance/cmd/godjcheck/main_test.go", "conformance/internal/protocol/deviation_test.go", "conformance/internal/protocol/migration_lifecycle_artifacts_test.go", "conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/README.md", "docs/ARCHITECTURE.md", "docs/COMPATIBILITY.md", "docs/DEVIATIONS.md", "docs/OPEN_QUESTIONS.md", "docs/ROADMAP.md", "docs/TESTING.md", "docs/adr/0018-revision-fenced-migration-lifecycle-product-shape.md", "docs/adr/README.md", "docs/status/CURRENT.md", "docs/status/IMPLEMENTATION_MATRIX.md", "docs/status/TEST_EVIDENCE.md", "work/0018-revision-fenced-migration-lifecycle-product-slice.md", "work/README.md"]
integration_owner: "one primary agent"
---

# Revision-Fenced Migration Lifecycle Product Slice

## 사용자에게 보이는 결과

이미 메모리에 load된 migration definition과 명시적 latest/target request를 `Executor`에 전달하면,
GoDj가 durable history를 읽고 검사한 뒤 historical `ProjectState` 재구성, plan과 migration별
실행을 하나의 revision-fenced lifecycle로 수행합니다. Fresh/prefix/no-op, named
forward/reverse, app zero, unknown legacy, inconsistent history, 중간 실패와 file reopen resume를
MIG-047..056 exact reference와 비교합니다.

이 제품 단면은 migration file을 찾거나 CLI command를 제공하지 않습니다. Caller가
version-compatible loaded `[]Migration`을 전달하는 경계까지만 구현합니다.

## 목표

- Zero value가 invalid인 explicit latest/targeted `LifecycleRequest`
- 실제 `Executor.Backend`를 재사용하는 `Executor.Migrate(ctx, definitions, request)` 구현·검증
- Definition validation → atomic history snapshot → history check → state reconstruction → plan →
  per-step fenced execution의 한 public orchestration
- Existing `Planner`, `StateReconstructor`, `Executor` state/plan/transaction kernel 재사용
- 별도 optional revision-fenced backend/session port와 legacy external fake source compatibility
- SQLite persistent epoch + revision + canonical history fingerprint metadata v1
- 각 step 첫 schema/recorder write 전 stale validation과 schema/recorder/successor 원자 commit
- Fresh bootstrap, existing recorder adoption-required와 legacy writer fail-closed 경계
- Conflict/contention/capability/integrity의 구조화된 오류와 semantic retry 0
- Empty-table default-bearing `AddField`의 logical default 보존과 physical default 부재
- MIG-047..056 live GoDj adapter와 locked Django oracle의 9 exact match + MIG-052
  DEV-0002 sparse product expectation
- 기존 `83 passing + 4 deviation + 10 oracle_locked`를 `92 passing + 5 deviation`으로 전환

## 비목표

- Migration file encoding, directory/module/source discovery, operation codec와 loader version protocol
- `godj migrate`, `showmigrations`, `makemigrations` 또는 project-binary handshake
- Data migration callback/plugin ABI와 historical app registry
- Existing database revision metadata를 만드는 public adoption/repair command
- Process kill 뒤 schema/recorder reconciliation, live schema drift repair와 uncertain-commit recovery
- Completed pre-cutover non-cooperating apply/unapply ABA 검출
- Long-lived lock/lease, fairness, distributed consensus와 automatic retry/backoff
- Revision overflow 자동 복구, database copy/restore epoch 재발급 정책
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict merge
- PostgreSQL, multi-DB router와 non-SQLite backend 제품 구현
- Relation/`real_apps`와 historical relation rendering
- 기존 공개 `Apply`, `Unapply`, `ExecutePlan`, `AppliedMigrationReader`, `AtomicBackend`,
  `Transaction`의 breaking change
- Locked lifecycle oracle/static/SHA256SUMS 또는 `conformance/lifecyclefence/**` 변경

## 선행 조건과 기준 상태

- Activation baseline은
  `main@d0df905996fc1d80065ac696a2bff4bc3ddb4b2e`
  (`docs: complete migration lifecycle contracts`)입니다.
- Machine artifact baseline은
  `main@6e018e00bd9178858db597400ac9d3f98a66acf6`입니다.
- [GDJ-0017](0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
  MIG-047..056을 `10 oracle_locked`로 고정하고 test-only fence의 feasibility를 검증했습니다.
- [Accepted ADR-0017](../docs/adr/0017-revision-fenced-migration-lifecycle.md)은
  atomic snapshot, per-step first-write validation, atomic successor와 no-auto-retry 안전성 방향을
  채택했지만 제품 API/storage는 고정하지 않았습니다.
- 현재 제품은 8 adapter set의 `83 passing + 4 deviation`이고 lifecycle product adapter는
  없습니다. `Executor`는 이미 `Backend backend.AtomicBackend` field를 소유합니다.
- `AppliedMigrationReader`, `LoadAppliedState`, `Planner.CheckHistory`, `StateReconstructor`와
  `ExecutePlan`은 각각 존재하지만 atomic freshness session으로 묶이지 않았습니다.
- SQLite `AddField`는 default가 있으면 현재 무조건 capability error를 반환합니다. MIG-047의
  A2는 empty table에 `BooleanField(default=false)`를 추가하므로 이 좁은 safe case가 필요합니다.
- 시작 시 working tree는 clean이었고 baseline HEAD의 full SHA를 확인했습니다.

## Django Reference / Contract

Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다. Contract 의미와 phase는 GDJ-0017에서
잠근 그대로 유지합니다.

| ID | 제품 completion gate |
|---|---|
| MIG-047 | Fresh latest가 A1→A2→A3→B1을 migration별 commit하고 latest state/schema/history에 도달 |
| MIG-048 | Durable A1 prefix에서 A2→A3→B1만 실행 |
| MIG-049 | Fully applied latest가 history/schema/transaction 변화 없는 no-op |
| MIG-050 | Named A2가 dependency closure A1→A2까지만 commit |
| MIG-051 | Named A1 reverse가 A3←A2←만 실행하고 A1/B1 보존 |
| MIG-052 | Alpha zero의 final state/schema/history는 동일하되 GoDj canonical plan A3←A2←B1←A1과 Django B1←A3←A2←A1의 incomparable sibling 순서를 DEV-0002로 review |
| MIG-053 | Unknown legacy recorder row를 보존하면서 known tail commit |
| MIG-054 | Known inconsistent history를 plan 전에 transaction/DDL/write 0으로 거부 |
| MIG-055 | A1 durable, A2 schema rollback, A3/B1 unstarted와 last durable state |
| MIG-056 | 같은 file database close/reopen의 fresh backend/executor가 A2→A3→B1 resume |

MIG-047..053/056은 `commit`, MIG-054는 `evaluation`, MIG-055는 `rollback`입니다.
MIG-051/052의 physical reverse transaction topology는 계속 DEV-0001/ADR-0014가 소유합니다.
별도로 MIG-052의 B1과 A3은 서로 incomparable합니다. Accepted ADR-0013의 GoDj canonical
ascending planner policy를 Django private traversal에 맞춰 바꾸지 않고, final
state/schema/history가 같은 상태에서 `result.plan[0..2]`와 `metrics.steps[0..2]`의 순서만
DEV-0002 sparse deviation expectation으로 review합니다. Locked oracle은 수정하지 않습니다.

## 공개 API

[Accepted ADR-0018](../docs/adr/0018-revision-fenced-migration-lifecycle-product-shape.md)로
검증·채택한 API는 다음입니다.

```go
type LifecycleRequest struct { /* unexported tagged value */ }

func LatestLifecycleRequest() LifecycleRequest
func TargetedLifecycleRequest(first Target, rest ...Target) LifecycleRequest

func (e Executor) Migrate(
    ctx context.Context,
    definitions []Migration,
    request LifecycleRequest,
) (ProjectState, error)
```

- `Executor`가 이미 backend를 소유하므로 `Migrate`에 중복 backend/reader 인자를 추가하지
  않습니다. Dynamic backend가 optional fence capability를 제공하는지 fail-closed로 확인합니다.
- Zero `LifecycleRequest`는 invalid입니다. Latest와 target list 없음은 같은 표현이 아닙니다.
- Targeted request는 `first`를 요구하고 existing immutable `NamedTarget`/`ZeroTarget`을 복사합니다.
- Latest는 existing planner graph의 same-app leaf 전체를 canonical dependency order로 확장합니다.
  Cross-app child만 있는 node도 자기 app leaf라는 ADR-0016 의미를 재사용합니다. Targeted는
  caller order와 existing Planner semantics를 보존합니다.
- Definition과 nested operation/IR은 호출 초기에 snapshot/deep-copy하므로 caller mutation이 진행
  중인 lifecycle 의미를 바꾸지 않아야 합니다.
- 반환값은 성공 시 resulting state, 확정 rollback/실행 오류 시 last durable `ProjectState`입니다.
  Commit durability가 unknown이면 마지막 confirmed pre-step state와 `commit_outcome_unknown`을 반환합니다.
  Snapshot/history/reconstruction 전에 실패해 검증된 durable state가 없으면 explicit empty state를
  반환하고 error를 우선합니다.
- Empty plan은 schema/recorder/revision metadata를 만들지 않는 no-op입니다.

## Backend/session

기존 port를 widen하지 않고 별도 optional capability를 둡니다.

```go
type HistoryTransitionKind uint8

type HistoryTransition struct {
    Migration AppliedMigration
    Kind      HistoryTransitionKind
}

type RevisionFencedBackend interface {
    OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error)
}

type RevisionFencedSession interface {
    AppliedMigrationReader
    BeginFencedMigration(context.Context, HistoryTransition) (RevisionFencedTransaction, error)
    Close(context.Context) error
}

type CommitDurability uint8

const (
    CommitRolledBack CommitDurability = iota + 1
    CommitCommitted
    CommitUnknown
)

type CommitOutcome struct {
    Durability CommitDurability
}

type RevisionFencedTransaction interface {
    SchemaEditor
    Recorder
    CommitFenced(context.Context) (CommitOutcome, error)
    Rollback(context.Context) error
}
```

- Session의 `AppliedMigrationReader`는 정확히 한 번 identities와 private expected token을 같은
  atomic snapshot에서 읽습니다. `Migrate`는 별도 private conversion에서 raw record를 복사하고
  `AppliedState`로 검증합니다. Generic `LoadAppliedState`와 detached reader를 조립해 token binding을
  우회하지 않습니다.
- Snapshot call이 반환되기 전에 read connection을 닫습니다. Session은 call 사이 physical
  connection을 pin하지 않고 immutable records, opaque token과 `ready/active/poisoned/closed`
  state만 소유합니다. Public caller는 epoch/revision/fingerprint를 읽거나 증가시키지 않습니다.
- `BeginFencedMigration`은 session의 current expected token과 declared apply/unapply successor를
  사용하고 동시에 하나의 active transaction만 허용합니다. Returned fenced transaction의
  `CommitCommitted`만 session records/token을 successor로 advance합니다. `CommitRolledBack`은
  이전 token을 유지하고 `CommitUnknown`은 session을 poison해 재사용을 거부합니다.
- Fenced transaction은 recorder call이 declared transition의 identity/direction과 정확히 한 번
  일치하는지 확인하고, 누락·중복·다른 identity는 commit 전 integrity error로 거부합니다.
  Successor fingerprint는 caller가 아니라 session이 current full identities에 transition을
  적용해 계산합니다.
- `CommitDurability` zero는 invalid backend outcome입니다. `CommitFenced`는
  rolled-back/committed/unknown durability와 cause를 함께 반환해 core가 state를 추측하지 않게
  합니다. 기존 `Transaction.Commit`을 fenced commit에 재사용하지 않습니다.
- Coordinator는 session을 연 직후 deferred cleanup closure를 등록하고, 실제 cleanup 시점에
  caller cancellation과 분리된 bounded context를 만들어 `session.Close(cleanupCtx)`를 호출합니다.
  `Close`는 abandoned active transaction을
  rollback/discard하고 poisoned/closed session 재사용을 거부합니다. Cleanup 실패는 primary
  lifecycle error를 대체하지 않습니다.
- Coordinator는 existing operation preflight/state/recorder logic을 private execution kernel로
  추출하고 begin/commit strategy를 내부 주입합니다. Public `Apply`/`Unapply`/`ExecutePlan`은
  legacy `BeginMigration` + `Transaction.Commit` 경로를 보존하고, `Migrate`만
  `BeginFencedMigration` + `CommitFenced`를 사용합니다. Private helper는 public API가 아닙니다.
- Port를 구현하지 않은 backend와 zero/nil Executor backend는
  `migration_capability_error/revision_fence_unsupported`로 transaction 전 거부합니다.
- Session open/snapshot/begin/commit/rollback/close/cancellation resource가 누수되지 않음을
  SQLite connection count, race와 fault gate로 검증했습니다. Core 계약상
  `CommitRolledBack`은 token을 advance하지 않지만, SQLite는 rollback을 포함한 어느 failed step
  뒤에도 session을 poison합니다. 이로써 같은 lifecycle에서 semantic retry나 tail transaction을
  다시 여는 동작을 거부하고 fresh reopen/reconciliation을 요구합니다.

## SQLite storage와 adoption 경계

Metadata v1은 exact `godj_migration_revision` singleton table/row에 다음을 저장합니다.

- `format_version = 1`
- CSPRNG로 한 번 생성한 128-bit database epoch
- non-negative signed int64 monotonic revision
- sorted full recorder identity를 length-prefixed byte encoding으로 hash한 SHA-256 fingerprint

Canonical fingerprint에는 graph에 없는 unknown legacy row도 포함합니다. Identity string
concatenation이나 delimiter에 의존하지 않습니다. Encoding v1은 identity count와 각 app/name
UTF-8 byte length를 unsigned 64-bit big-endian으로 기록한 뒤 raw bytes를 붙입니다. Step
transaction은 current
epoch/revision/fingerprint와 expected 값의 conditional match를 첫 DDL 전에 수행하고, schema,
recorder mutation, `revision+1`과 successor fingerprint를 한 commit에 넣습니다. Overflow,
format/row/epoch/hash 불일치와 impossible recorder/metadata shape는 integrity error로 fail-closed합니다.

각 SQLite step은 새 pinned `*sql.Conn`에서 literal `BEGIN IMMEDIATE`를 실행합니다.
`database/sql.Tx`의 deferred begin mode를 fenced write에 사용하지 않습니다. Existing schema/
recorder SQL helper는 `*sql.Tx`에 고정하지 않고 필요한 `ExecContext`/`QueryContext` generic SQL
interface로 좁혀 legacy transaction과 fenced connection이 공유합니다. Rollback/close는 caller
cancellation과 분리된 bounded cleanup context를 쓰고, cleanup이 실패하면
`driver.ErrBadConn`으로 physical connection을 pool에서 discard합니다.

Adoption은 다음처럼 좁게 고정합니다.

1. Metadata와 recorder가 모두 없는 fresh database의 nonempty 첫 fenced step은 같은 transaction
   안에서 metadata/recorder/schema를 bootstrap할 수 있습니다.
2. Empty plan은 두 table을 만들지 않습니다.
3. Metadata는 없지만 recorder object가 존재하면 row가 0개여도
   `revision_fence_adoption_required`입니다. Opportunistic revision 0 추론을 금지합니다.
4. Metadata가 생긴 뒤 SQLite legacy `BeginMigration`은 fail-closed하여 direct
   `ExecutePlan`이 non-cooperating writer가 되지 못하게 합니다.
5. Existing database adoption은 old writer를 멈추고 snapshot을 검증하는 별도 exclusive cutover
   command가 필요하며 GDJ-0018에는 public adoption API를 만들지 않습니다.

Direct SQL writer와 pre-cutover completed ABA는 완전히 막을 수 없습니다. Fingerprint는 direct
non-ABA drift를 잡는 보조 integrity gate이고 persistent revision이 cooperating writer의 주
freshness fence입니다.

## 오류 경계

Public `migrations.Error`에 다음 category/code를 구현했습니다.

| Category | Code | 의미 |
|---|---|---|
| `migration_capability_error` | `revision_fence_unsupported` | backend가 optional port를 제공하지 않음 |
| `migration_capability_error` | `revision_fence_adoption_required` | existing recorder에 exclusive adoption이 필요함 |
| `migration_conflict_error` | `stale_history_revision` | expected revision이 first write 전에 stale임 |
| `migration_transaction_error` | `history_revision_contended` | SQLite BUSY/LOCKED 등 lock contention |
| `migration_transaction_error` | `commit_outcome_unknown` | commit durability를 증명할 수 없음 |
| `migration_transaction_error` | `commit_cleanup_failed` | commit은 durable하지만 connection cleanup이 실패함 |
| `migration_transaction_error` | `session_close_failed` | primary 오류 없이 session terminal cleanup이 실패함 |
| `migration_history_error` | `history_revision_integrity` | metadata/recorder/fingerprint/version/overflow 불일치 |

- Known dependency inconsistency는 기존
  `migration_history_error/inconsistent_applied_history`를 유지합니다.
- Zero/corrupt lifecycle request와 contained invalid target은 transaction 전에 existing
  plan taxonomy의 `invalid_target`으로 거부합니다. Mixed-direction plan도 existing
  `mixed_directions` preflight 의미를 유지합니다.
- Operation/recorder/begin/rollback 오류와 cause wrapping은 existing executor taxonomy를
  보존합니다. `CommitRolledBack`은 pre-step state, `CommitCommitted`는 post-step state를
  반환합니다. `CommitUnknown` 또는 zero/invalid outcome은 전용 `commit_outcome_unknown` error와 마지막
  **confirmed** durable pre-step state를 반환하고 session/tail을 중단합니다. 이 state는 실제 DB가
  rollback됐다는 주장이 아니므로 caller는 fresh reopen/reconciliation 전 자동 재실행하면 안 됩니다.
- `CommitCommitted`와 함께 close/cleanup error가 오면 current step state/token은 advance한 뒤
  `commit_cleanup_failed`를 반환하고 tail을 중단합니다. `CommitRolledBack`의 commit/rollback
  error는 pre-step state를 유지합니다.
- `BUSY`/`LOCKED`를 stale 또는 integrity로 오분류하지 않고 contention으로 정규화합니다.
- Stale/conflict/contention/capability/integrity 어느 경우에도 semantic retry는 0입니다.
- 오류 우선순위는 operation/recorder/commit durability 같은 primary lifecycle error가 먼저입니다.
  Rollback/session-close/discard failure는 `session_close_failed` 등 secondary structured error로
  감싸 `errors.Join(primary, secondary)`에 primary를 먼저 보존합니다. Primary가 없을 때 session
  close failure는 last confirmed/resulting state와 `session_close_failed`를 반환합니다. Cleanup이
  primary classification을 덮거나 `CommitUnknown`을 rolled back으로 낮추지 않습니다.

## AddField empty-table 경계

MIG-047 A2를 위해 default-bearing SQLite `AddField`는 table이 비어 있고 기존 compiler가
지원하는 field shape일 때만 허용합니다. Logical `ProjectState`에는 `default=false`를 그대로
보존하지만 physical column은 persistent SQLite default 없이 `BOOLEAN NOT NULL`로 추가합니다.
`PRAGMA table_info`의 default는 `NULL`이어야 합니다.

Row가 하나라도 있는 table의 default-bearing/non-null AddField는 one-time backfill/table rebuild가
필요하므로 계속 `migration_capability_error/unsupported_operation`입니다. 이 단면에서
table rebuild, data backfill 또는 generated insert-default 동작을 일반화하지 않습니다.

## False-green 위험과 필수 gate

- **Lifecycle 이름만 붙인 unfenced 조립**: competing commit을 snapshot 뒤/step 사이 주입하면
  current step mutation 0이어야 합니다.
- **Reader/backend 분리**: snapshot reader와 executor backend가 다른 DB를 가리키는 API를
  제공하지 않습니다. Executor 소유 backend session 하나가 둘을 결속합니다.
- **Token 노출/수동 증가**: caller가 revision을 만들거나 successor를 전달할 수 없어야 합니다.
- **Legacy bypass**: metadata 생성 후 `ExecutePlan`/`Apply`가 old `BeginMigration`으로 쓰면
  capability error여야 합니다.
- **Adoption false fresh**: existing empty recorder를 absent recorder로 오인하면 실패해야 합니다.
- **Fingerprint-only ABA**: apply/unapply로 identities가 같아져도 revision이 달라 stale이어야 합니다.
- **CAS 뒤 DDL window**: revision 검증, DDL, recorder, successor가 한 transaction이 아니면
  fault gate가 실패해야 합니다.
- **Declared transition mismatch**: 다른 recorder identity, 반대 direction, zero/multiple recorder
  call이 successor hash와 분리되어 commit되면 실패해야 합니다.
- **Hidden retry**: snapshot/session open과 각 step attempt는 정확히 한 번이어야 합니다.
- **Session lifetime 누수**: snapshot 뒤 connection pin, 동시에 둘 이상의 active transaction,
  abandoned transaction 방치, poisoned/closed session 재사용과 caller-canceled context만 사용한
  cleanup은 모두 실패해야 합니다.
- **Commit 추측**: unknown/zero durability를 rolled back이나 committed로 승격하거나 tail을
  실행하면 실패해야 합니다. Committed+cleanup-error는 post-step state를 보존해야 합니다.
- **Latest/target 혼합**: zero request, latest와 targeted empty를 같은 값으로 처리하지 않습니다.
- **History preflight 순서**: MIG-054는 plan/transaction/DDL/write 0을 개별 sentinel로 확인합니다.
- **Failure false completion**: MIG-055는 A1 durable, A2 rollback, A3/B1 unstarted이고 MIG-056은
  같은 file만 넘긴 fresh backend/executor여야 합니다.
- **Default leakage**: A2 physical schema에 persistent `DEFAULT`가 남으면 실패합니다.
- **Django sibling order 복제**: MIG-052에서 Accepted ADR-0013 canonical order를 private Django
  traversal에 맞춰 바꾸면 실패합니다. DEV-0002 expectation은 `result.plan[0..2]`와
  `metrics.steps[0..2]` 여섯 replace만 허용하고 final state/schema/history는 바꾸지 않습니다.
- **Adapter synthesis**: contract ID/oracle/static dispatch를 금지하고 request/definition/history/
  failure mutation이 actual result/error/DB state/metrics에 전파되어야 합니다.
- **Artifact drift**: lifecycle oracle, not-implemented static fixture, SHA256SUMS와
  `conformance/lifecyclefence/**`는 byte-for-byte 불변이어야 합니다.

## 구현 단계

1. ADR-0018의 public request, `Executor.Migrate`와 optional session port를 external
   compile test로 검증하고 zero/nil/copy/error ownership을 확정합니다.
2. Existing graph/reconstructor/execution kernel을 공유해 definition preflight, snapshot,
   explicit history check, reconstruction, latest/target plan과 last-durable state를 구현합니다.
3. SQLite metadata v1, atomic snapshot, literal `BEGIN IMMEDIATE` pinned connection,
   conditional first-write fence와 successor binding을 구현합니다.
4. Fresh bootstrap/existing-recorder adoption-required, legacy writer fail-closed, format/integrity/
   overflow와 BUSY/LOCKED classification을 fault/concurrency test로 검증합니다.
5. Default-bearing empty-table AddField를 physical default 없이 지원하고 nonempty 거부를 회귀
   검증합니다.
6. MIG-047..056 scenario-driven GoDj adapter를 public `Executor.Migrate`로 작성합니다. MIG-055
   fault는 public backend/session wrapper로 A2 DDL 뒤 operation error를 주입합니다.
7. Lifecycle manifest는 MIG-052만 `deviation` + exact-one `DEV-0002` decision provenance로,
   나머지 9개만 `passing`으로 전환합니다. Sparse deviation fixture와 code-owned policy dispatch를
   추가하고 unknown decision은 fail-closed합니다. DEV-0002 decision/status/evidence를
   `docs/DEVIATIONS.md`에 기록하고 locked oracle/static/SHA256SUMS를 보존합니다.
8. Two-process Go actual byte identity, 9 exact + MIG-052 reviewed expectation, static ordered 10
   mismatch와 semantic/deviation-scope mutation gate를 통과합니다.
9. Nine product set `92 passing + 5 deviation`, 97 unique IDs/scenarios와 72 ordered
   cross-binding을 검증합니다.
10. Full/race/CGO=0/vet, focused repetition/two-process/compile/source gate와 독립 P0–P3 감사를
    통과한 뒤 ADR/status/evidence를 완료 반영합니다.

## 완료 조건

- [x] Tagged latest/targeted request와 invalid zero semantics
- [x] `Executor.Migrate(ctx, definitions, request)` external compile/public ownership 확정
- [x] Atomic identities+private revision snapshot과 explicit history preflight
- [x] Existing reconstructor/planner/executor kernel 재사용과 definition deep-copy
- [x] Per-step first-write fence, atomic successor와 committed/rolled-back/unknown state 의미
- [x] Exact-one session snapshot, mandatory Close와 active/poisoned/closed lifetime/resource gate
- [x] Fresh bootstrap, empty no-op와 existing recorder adoption-required
- [x] Metadata 뒤 legacy writer fail-closed와 unsupported backend no fallback
- [x] Stale/contention/capability/integrity/commit_outcome_unknown structured error와 semantic retry 0
- [x] Two connection/process single winner, between-step conflict와 ABA/fault/cancellation gate
- [x] Empty-table default AddField logical default/physical no-default와 nonempty 거부
- [x] MIG-047..056 GoDj live adapter, two-process actual byte identity와 9 exact + DEV-0002 expectation
- [x] DEV-0002 decision/status/evidence와 six-path sparse scope가 DEVIATIONS/policy/fixture에서 일치
- [x] MIG-052 status + exact-one provenance/expectation 외 locked lifecycle oracle/static/SHA256SUMS 불변
- [x] `conformance/lifecyclefence/**` byte 불변과 existing public port fake compile 회귀
- [x] Existing `83 passing + 4 deviation` 회귀 없이 `92 passing + 5 deviation`
- [x] Nine product adapter, 97 global unique IDs/scenarios와 72 cross-binding 유지
- [x] Full Go/race/CGO=0/vet, portable/exact Python, compile/source/mutation gate
- [x] 독립 P0–P3 product/conformance/fence audit
- [x] Work/CURRENT/matrix/evidence/ADR가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0017 exact lifecycle contract와 revision-fence feasibility 완료
- [x] 실제 `Executor.Backend` ownership과 AddField/default gap 재확인
- [x] Active work allowed path와 Proposed ADR-0018 후보 작성
- [x] Activation audit에서 ADR-0013 canonical order와 MIG-052 DEV-0002 후보 분리
- [x] Dedicated fenced transaction/commit durability와 mandatory session Close 후보 반영
- [x] Public API/backend session compile 및 source compatibility gate
- [x] Core/SQLite product 구현과 concurrency/fault 검증
- [x] GoDj lifecycle adapter와 `9 passing + 1 deviation` status 전환
- [x] Full 검증·독립 감사·문서 handoff

## 수정 파일

- Core/API: `migrations/backend/lifecycle.go`, `migrations/lifecycle.go`,
  `migrations/lifecycle_test.go`, `migrations/executor.go`, `migrations/executor_test.go`,
  `migrations/execution.go`, `migrations/execution_test.go`, external compile fixture
- SQLite: `db/sqlite/migration_lifecycle.go`, `db/sqlite/migration_lifecycle_test.go`, migration
  backend/SQL helper와 회귀 test, invocation-unique repeated-test DB를 쓰는
  `db/sqlite/backend_internal_test.go`
- Conformance: lifecycle manifest/DEV-0002 fixture, Django status assertion, live GoDj adapter,
  runner tests, `godjcheck` decision policy와 protocol artifact/deviation/source/mutation gates
- Automation: `Makefile`, `.github/workflows/ci.yml`
- Handoff: 이 work, ADR-0018, CURRENT, matrix, TEST_EVIDENCE와 DEV-0002 ledger를 포함한 관련
  architecture/compatibility/testing/roadmap/index 문서

구현은 다음 commit으로 분리했습니다.

- Product: `d076bd20f5964074b7b76b44147ca59f7b3e6eb8`
- Machine/conformance: `fd49d5147beefead640f43ae6fd5c83860a17a06`
- CI workflow: `7df6e2ad97d5890610e597277653df0674e8dd52`
- Repeated SQLite test isolation:
  `9f51ad0da443d259940d44acbb8c3d095a9a257b`

## 결정된 사항

- 2026-08-08: GDJ-0018은 source loader보다 already-loaded definition lifecycle 제품을 먼저
  구현합니다.
- 2026-08-09: Actual `Executor`가 backend field를 소유하므로 중복 backend 인자 없는
  `Executor.Migrate`를 public API로 채택했습니다.
- 2026-08-08: Accepted ADR-0017의 safety invariant와 locked MIG-047..056 의미는 변경하지
  않습니다.
- 2026-08-08: Oracle/static/SHA256SUMS와 test-only lifecyclefence spike는 제품 work에서
  수정하지 않습니다.
- 2026-08-09: Accepted ADR-0013의 canonical ascending order를 보존합니다. MIG-052의
  incomparable B1/A3 sibling order만 DEV-0002 sparse expectation으로 분리하고 final
  state/schema/history는 Django와 동일해야 합니다.
- 2026-08-09: Fenced lifecycle은 legacy `Transaction`이 아니라 dedicated
  `RevisionFencedTransaction`, explicit commit durability와 mandatory session `Close`를
  사용합니다. Session은 call 사이 connection-free이고 SQLite step만 pinned connection을 소유합니다.
- 2026-08-09: `CommitRolledBack`은 core state/token을 advance하지 않습니다. SQLite는 failed
  step 뒤 session을 poison하여 같은 attempt의 retry/tail reopen을 금지합니다.
- 2026-08-09: Metadata v1 exact table은 `godj_migration_revision`이고, raw backend failure는
  source-compatible `RevisionFenceError`로만 core taxonomy에 전달합니다. `CommitOutcome`에는
  durability 외 cleanup field를 추가하지 않고 error를 독립 carrier로 유지합니다.

## 미결정/Blocker

외부 blocker는 없습니다. Backend raw carrier, metadata identifier/shape와 commit outcome carrier는
구현·compile/fault evidence로 확정했습니다. 다음은 명시적 비목표 또는 후속 제한입니다.

- Hosted GitHub Actions는 workflow만 추가됐고 branch push/PR 전이라 아직 실행하지 않음
- Public existing-database adoption/repair, copy/restore epoch 정책, overflow recovery와 crash repair
- Migration source/versioned loader/codec와 CLI
- Non-SQLite fenced backend, distributed lock/lease와 automatic retry

## 테스트 증거

- Contract baseline:
  [EVID-20260808-016](../docs/status/TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)
- Product evidence:
  [EVID-20260809-017](../docs/status/TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice)
- Final code checkout:
  `codex/revision-fenced-migration-lifecycle@9f51ad0da443d259940d44acbb8c3d095a9a257b`
- `make check` PASS; portable Python 130 tests/13 exact-only skipped, exact 130/130 PASS
- Full `CGO_ENABLED=0 go test -count=1 ./...` PASS
- `go test -count=50 -shuffle=on ./migrations`와
  `go test -count=20 -shuffle=on ./db/sqlite` PASS
- Focused migration/core race count=10, SQLite lifecycle race count=5, lifecycle adapter race count=5
  PASS
- 두 Go actual은 각각 98,304 bytes, SHA-256
  `a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical했고
  10 contract/0-diff; static fixture는 의도한 exit 1/ordered mismatch 10
- Independent product/SQLite/conformance P0–P3 audit finding 없음

## 위험과 rollback

- Public API/metadata v1의 acceptance는 EVID-017 gate에 결속합니다. 이 gate가 깨지면
  Accepted 상태와 storage compatibility를 재검토합니다.
- Metadata를 만든 뒤 legacy writer가 열리면 fence safety가 깨지므로 fail-closed gate를 필수로 둡니다.
- CAS와 DDL/recorder/successor를 분리하는 구현은 허용하지 않습니다.
- Locked oracle/static/SHA나 completed spike를 regenerate해 문제를 숨기지 않습니다.
- Migration별 commit 때문에 전체 lifecycle rollback은 불가능합니다. Error result는 last durable
  prefix를 정확히 전달해야 합니다.

## 다음 정확한 작업

GDJ-0019 migration definition source/versioned-loader compatibility contract를 별도 work/ADR로
activation합니다. File discovery, format/version handshake, deterministic load order, duplicate/
partial source failure와 loaded `[]Migration` → `Executor.Migrate` handoff를 먼저 관찰·고정합니다.
현재 active work는 없고 GDJ-0019는 planned 상태입니다.

## 결과와 인수인계

GDJ-0018은 완료됐습니다. 제품 분류는 9 adapter set, 97 contract의
`92 passing + 5 deviation`이며 lifecycle은 MIG-052만 DEV-0002입니다. Manifest는 13,735 bytes
SHA-256 `5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`, DEV-0002 fixture는
6,769 bytes SHA-256 `58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`입니다. Locked oracle
98,436 bytes와 static 1,681 bytes, `SHA256SUMS`, `conformance/lifecyclefence/**`는 불변입니다.

다음 작업은 planned GDJ-0019 migration definition source/versioned-loader contract입니다.
GDJ-0018 API는 file layout, operation codec나 CLI handshake를 고정하지 않았습니다. Hosted
GitHub Actions는 single PR을 만든 뒤 Ubuntu portable/macOS exact job으로 확인해야 하며, 현재
local evidence를 hosted pass로 표현하지 않습니다.
