# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `6e018e00bd9178858db597400ac9d3f98a66acf6`
  (`test: lock migration lifecycle contracts`)
- 기준 제품 commit:
  `3b0e68d6717a9612debc9cb93d03ab0f98005860`
  (`feat: reconstruct historical migration state`)
- GDJ-0018 activation baseline commit:
  `d0df905996fc1d80065ac696a2bff4bc3ddb4b2e`
  (`docs: complete migration lifecycle contracts`)
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: GDJ-0018 Revision-fenced migration lifecycle 제품 단면 active
- 최근 완료 작업:
  [GDJ-0017 Migration Lifecycle Compatibility Contracts and Revision-Fence Spike](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- 활성 작업:
  [GDJ-0018 Revision-Fenced Migration Lifecycle Product Slice](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)
- 다음 ready 작업: 없음 — GDJ-0018 완료 뒤 GDJ-0019 source/versioned-loader contract를 별도 activation

## 현재 checkout에서 확인된 사실

### 제품 구현

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2, deterministic codegen, generic Manager/QuerySet,
  typed/dynamic Query AST, SQLite query/write와 Save lifecycle 제품 단면이 구현됐습니다.
- Migration core는 versioned `ProjectState`, typed `CreateModel`/`AddField`, immutable identity
  graph와 `AppliedState`, zero-I/O `Planner`, preflighted `Executor.ExecutePlan`을 제공합니다.
- SQLite migration backend는 supported DDL과 same-transaction editor/recorder를 제공하며
  unsupported rebuild/dependency 범위는 구조화된 capability error로 거부합니다.
- 별도 `AppliedMigrationReader`, core `LoadAppliedState`와 `Planner.CheckHistory`가 durable
  recorder identity를 validated `AppliedState`로 읽고 known history를 plan 전에 검사합니다.
- Accepted [ADR-0016](../adr/0016-historical-project-state-reconstruction.md)의
  `StateReconstructor`는 loaded migration definition을 deep-copy하고 existing Planner graph/order
  kernel로 explicit empty/latest/before/after/applied `ProjectState`를 pure replay합니다.
- Zero `StateRequest`는 invalid이고 zero `StateReconstructor`는 empty graph와 같은 안전한
  immutable value입니다. Constructor input, built-in operation/nested IR과 반환 state는
  alias되지 않으며 repeated/concurrent reconstruction은 deterministic하고 race-safe합니다.
- 제품에는 아직 migration lifecycle coordinator, atomic history revision, revision-capable
  backend port, public migrate CLI 또는 MIG-047..056 GoDj adapter가 없습니다.
- Active [GDJ-0018](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)은
  already-loaded definition과 실제 `Executor.Backend`를 사용한
  `Executor.Migrate(ctx, definitions, request)` 후보를 검증합니다. [Proposed ADR-0018](../adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은
  exact-one snapshot/mandatory Close를 가진 connection-free revision session, dedicated fenced
  transaction/commit durability, SQLite metadata v1, adoption/error와 empty-table default-bearing
  AddField 경계를 제안하며 아직 Accepted/Implemented 상태가 아닙니다.

### 호환 계약과 machine artifact

- Protocol v2에는 9 ordered reference set, 97 contract가 있습니다: M1 read/metadata 11개,
  write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개, migration planning
  12개, plan execution 10개, recorder restart 10개, historical-state reconstruction 10개,
  migration lifecycle 10개입니다.
- 제품 adapter set은 앞의 8개뿐이며 현재 제품 분류는 `83 passing + 4 deviation`입니다.
  MIG-018/020/022/024만
  [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
  verified deviation입니다. MIG-047..056은 별도 `10 oracle_locked`이고 제품 passing이 아닙니다.
- 9 set의 contract ID/scenario는 전역으로 유일하고 36 set pair의 양방향 72 ordered
  cross-binding이 모두 validation에서 거부됩니다.
- Lifecycle manifest는 13,680 bytes, SHA-256
  `23a9e919edff932ae781f0768aeaf7f184fe392ec53598fa18524cf50d979a8e`입니다.
- Locked Django lifecycle oracle은 98,436 bytes, SHA-256
  `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, static fixture는
  1,681 bytes, SHA-256
  `b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`입니다.
  `SHA256SUMS` 파일은 SHA-256
  `520db274a63ed9d192e6ae0a3db224154a84676462e7fd8e49f80f64673c1a90`입니다.
- 두 독립 random-hashseed exact process output과 checked-in lifecycle oracle은
  byte-identical했습니다. Static comparison은 의도한 exit 1과 MIG-047..056 ordered status
  mismatch 10개를 유지합니다. Product binary는 lifecycle manifest를 exit 2/no actual로
  fail-closed하고 `godj-conformance`는 8 adapter만 실행합니다.
- 기존 8개 manifest/oracle/static/deviation payload와 기존 checksum entry는 변경하지
  않았습니다.

### MIG-047..056 exact lifecycle

- MIG-047 fresh latest는 A1→A2→A3→B1, MIG-048 applied A1 prefix latest는 A2→A3→B1,
  MIG-049 fully applied latest는 no-op입니다.
- MIG-050 named A2 forward는 dependency closure A1→A2까지만 commit합니다. MIG-051 named A1
  reverse는 A3←A2←만 실행하고 A1과 unrelated B1은 보존합니다.
- MIG-052 Django app zero target은 B1←A3←A2←A1← 순입니다. Existing GoDj Planner의 Accepted
  canonical order는 A3←A2←B1←A1이며 B1/A3은 incomparable sibling입니다. Final
  state/schema/history는 같고 ordered `result.plan`/`metrics.steps`만 DEV-0002 sparse deviation
  expectation 후보입니다.
  MIG-053은 graph에 없는 unknown legacy recorder identity를 보존하면서 known tail을 commit합니다.
- MIG-054는 public command orchestration이 explicit
  `check_consistent_history → migration_plan → migrate` 순서를 소유합니다. Known inconsistent
  history는 `plan_invoked=false`, transaction/DDL/write 0으로 거부됩니다.
- MIG-055 middle A2 failure는 A1만 durable하고 A2를 rollback하며 A3/B1을 시작하지 않습니다.
  MIG-056은 같은 temporary file database를 close/reopen한 fresh connection/loader/executor가
  A1을 반복하지 않고 A2→A3→B1로 resume합니다. Process restart로 과장하지 않습니다.
- Phase는 MIG-047..053/056 `commit`, MIG-054 `evaluation`, MIG-055 `rollback`입니다.
  MIG-051/052의 physical reverse transaction topology는 payload에서 제외하고 기존
  DEV-0001/ADR-0014가 소유합니다.
- Success comparison은 result/DB state/metrics, MIG-054/055는 error/DB state/metrics입니다.
  Protocol observation은 result와 error를 동시에 갖지 않습니다.
- Exact runner는 private executor helper를 직접 호출하지 않고 public orchestration만
  사용합니다. Contract ID/oracle/static dispatch 금지, live target/definition/seed/legacy/fault
  propagation과 semantic mutation gate가 false green을 거부합니다.

### Revision-fence feasibility spike

- `conformance/lifecyclefence/current_gap_test.go`만 현재 제품 package를 실제로 조립해 snapshot
  이후 first step 전과 step 사이 competing recorder commit을 제품이 받아들이는 gap을
  재현합니다. Fence 알고리즘과 fault/concurrency harness는 제품 lifecycle interface와
  독립된 test-only `database/sql` + SQLite 후보입니다.
- Accepted [ADR-0017](../adr/0017-revision-fenced-migration-lifecycle.md)은 identities와 freshness
  token을 같은 snapshot에서 읽고, 각 migration transaction 안에서 첫 DDL/recorder write 전에
  expected revision을 검증하며, schema/recorder와 successor revision을 원자적으로 commit하는
  안전성 방향을 채택합니다.
- SQLite spike는 pinned connection의 `BEGIN IMMEDIATE`, persistent epoch + monotonic revision과
  conditional metadata update를 주 fence로 사용합니다. SHA-256 history fingerprint는 snapshot
  결속과 direct non-ABA drift를 검출하는 보조 gate입니다.
- Fingerprint-only fence는 apply/unapply 뒤 identity set이 복원되는 ABA를 구분하지 못해
  기각했습니다. Safety는 모든 cooperating migration writer가 fence를 사용할 때 완전합니다.
  Pre-cutover non-cooperating completed ABA는 감지할 수 없으므로 existing database adoption에는
  exclusive cutover와 초기 snapshot 검증이 필요합니다.
- Stale-before-first-write, competing commit between steps, same-token two-connection/process
  single winner, uninitialized adoption, absent/empty/ABA, fault/cancellation rollback과 subsequent
  success가 통과했습니다. `BUSY`/`LOCKED`는 stale이 아닌 contention이고 semantic retry는 0입니다.
- Unsupported optional capability는 기존 reader/backend로 fallback하지 않고 fail-closed합니다.
  Existing public port만 구현한 external fake는 그대로 compile됩니다.
- Spike의 metadata table/column, token encoding, candidate coordinator와 helper 이름은 제품
  schema/API가 아닙니다. Live schema drift, completed non-cooperating ABA, fairness, lease,
  distributed lock와 crash repair도 보장하지 않습니다.
- GDJ-0018 activation은 spike source를 제품으로 이동하지 않습니다. Locked lifecycle
  oracle/static/SHA256SUMS와 `conformance/lifecyclefence/**`는 명시적 금지 경로이고, 제품
  구현은 새 backend/session port와 SQLite product package에서 독립적으로 검증합니다.

### 검증 증거

- GDJ-0016의 immutable reconstructor와 `83 passing + 4 deviation` 제품 baseline은
  [EVID-20260808-015](TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
  기록했습니다.
- GDJ-0017의 exact lifecycle 10개, 9-set protocol, product fail-closed와 revision-fence spike는
  [EVID-20260808-016](TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)에
  기록했습니다.
- Machine artifact commit 기준 `make check`, uncached full Go/race/CGO=0/vet, exact lifecycle
  13 tests, lifecyclefence count=20/race와 two-process count=100 gate가 통과했습니다.
- GDJ-0018 activation은 문서/frontmatter/local-link/diff gate만 실행합니다. 제품 source와
  manifest status는 아직 baseline 그대로이므로 목표인 `92 passing + 5 deviation`으로 표현하지
  않습니다.
- GitHub Actions workflow는 push하지 않아 hosted 실행 증거가 없습니다. 외부 Django checkout은
  수정하지 않았습니다.

## 확정된 결정

- Exact Python/Django/SQLite/platform/locale profile과 protocol v2 fail-closed binding을
  유지합니다.
- [ADR-0014](../adr/0014-migration-plan-execution-atomic-reverse.md)에 따라 plan은
  migration별 commit되고 실패 뒤 last durable state를 보존합니다. Lifecycle 전체 outer
  transaction은 사용하지 않습니다.
- [ADR-0015](../adr/0015-recorder-backed-applied-state.md)의 snapshot read와
  [ADR-0016](../adr/0016-historical-project-state-reconstruction.md)의 pure replay 경계를
  유지합니다.
- [ADR-0017](../adr/0017-revision-fenced-migration-lifecycle.md)에 따라 후속 제품 lifecycle은
  atomic history snapshot, per-step in-transaction first-write fence와 atomic successor binding을
  제공하고 unsupported backend에서 fail-closed해야 합니다. Conflict 자동 retry는 없습니다.
- ADR-0017의 Accepted 상태는 safety 방향 채택이며 제품 API/구현 완료가 아닙니다. MIG-047..056
  또한 `oracle_locked`이며 product `passing`으로 계산하지 않습니다.
- GDJ-0018은 migration source/loader/CLI보다 already-loaded definition lifecycle을 먼저
  구현합니다. 실제 Executor가 backend를 소유하므로 별도 backend 인자가 없는
  `Executor.Migrate(ctx, definitions, request)`를 Proposed 우선 후보로 검증합니다.
- Latest와 targeted request는 tagged value로 구분하고 zero request는 invalid입니다. Optional
  fence capability가 없으면 legacy execution으로 fallback하지 않습니다.
- Fresh database의 recorder/metadata가 모두 absent일 때만 첫 nonempty fenced step bootstrap을
  허용합니다. Recorder가 존재하면 empty여도 별도 exclusive adoption이 필요하고 GDJ-0018에는
  public adoption API를 만들지 않습니다.
- Exact A2를 위해 default-bearing SQLite AddField는 empty table에서만 logical default를
  보존하고 physical persistent default 없이 허용하는 후보를 검증합니다. Nonempty table은
  계속 capability error입니다.
- Accepted ADR-0013의 canonical ascending planner policy를 유지합니다. MIG-052는 manifest에서
  exact-one DEV-0002 decision provenance를 가진 `deviation`, 나머지 lifecycle 9개는 `passing`
  후보입니다. Oracle을 고치지 않고 여섯 ordered object path만 product expectation으로 교체합니다.
- Fenced path는 legacy `Transaction` 대신 `RevisionFencedTransaction`과
  rolled-back/committed/unknown commit durability를 사용합니다. Unknown/zero outcome은
  `commit_outcome_unknown`과 마지막 confirmed pre-step state를 반환하며 자동 retry하지 않습니다.
- Committed 뒤 cleanup 실패는 post-step state + `commit_cleanup_failed`, primary 없는 session
  terminal cleanup 실패는 last confirmed state + `session_close_failed`로 구분합니다.
- Session은 정확히 한 atomic history snapshot만 읽고 call 사이 connection을 pin하지 않지만
  immutable records/token과 ready/active/poisoned/closed state를 소유합니다. Mandatory `Close`가
  abandoned transaction cleanup/discard를 보장합니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 항목은 GDJ-0018 구현 spike와 fault/compile evidence로
ADR-0018을 Accepted하기 전에 확정합니다.

1. Backend raw fence failure와 public category/code mapping의 최소 source-compatible shape
2. SQLite metadata table/column identifier, corrupt/partial object diagnostics와 overflow 거부
3. `CommitOutcome`에 durability 외 cleanup detail을 더 노출할 필요가 있는지

Existing database public adoption/repair, copy/restore epoch, crash reconciliation, Q-012
loader/source/data callback/CLI와 Q-010/Q-011/Q-013은 후속 범위이며 GDJ-0018 blocker가 아닙니다.

## 다음 정확한 작업

`migrations/backend/lifecycle.go`와 `migrations/lifecycle.go`에 compile-only request,
connection-free session, dedicated fenced transaction/commit outcome shape를 먼저 만들고 existing
external fake source compatibility를 확인합니다. 이어 fake session에서 exact-one snapshot,
mandatory Close, invalid zero/latest/target/history와 rolled-back/committed/unknown state gate를
통과한 뒤 SQLite literal `BEGIN IMMEDIATE` fence와 AddField 경계를 구현합니다. Manifest는 actual
evidence 전에 바꾸지 않고, 완료 시 MIG-052 status+DEV-0002 provenance와 나머지 9 status만
전환합니다.

## 작업 재개 체크포인트

- 최신 machine artifact: `main@6e018e00bd9178858db597400ac9d3f98a66acf6`
- 제품 baseline: `main@3b0e68d6717a9612debc9cb93d03ab0f98005860`
- 최근 완료 work: [GDJ-0017](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- activation baseline: `main@d0df905996fc1d80065ac696a2bff4bc3ddb4b2e`
- active work: [GDJ-0018](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)
- ready work: 없음
- GDJ-0018 제품 구현 완료 전 보존할 분류: 8 product set `83 passing + 4 deviation` + lifecycle
  `10 oracle_locked`; 9 reference set/97 contract/72 ordered cross-binding
- GDJ-0018 검증 완료 목표: 9 product set `92 passing + 5 deviation`; MIG-052만 DEV-0002
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 현재 가장 위험한 과장: Proposed API/metadata나 exact lifecycle oracle/test-only fence spike를
  Accepted product adapter, public CLI, distributed lock 또는 crash recovery로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
