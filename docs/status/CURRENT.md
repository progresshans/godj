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
- GDJ-0017 baseline commit:
  `9856fd0278162af0a5ee28dfebd4f07d93eca790`
  (`docs: complete historical state reconstruction`)
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: GDJ-0017 Migration lifecycle exact contract와 revision-fence feasibility spike 완료
- 최근 완료 작업:
  [GDJ-0017 Migration Lifecycle Compatibility Contracts and Revision-Fence Spike](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- 활성 작업: 없음
- 다음 ready 작업: 없음 — 별도 GDJ-0018 activation 전에는 제품 source를 변경하지 않음

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
- MIG-052 app zero target은 cross-app dependent-first B1←A3←A2←A1← 순으로 reverse합니다.
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

### 검증 증거

- GDJ-0016의 immutable reconstructor와 `83 passing + 4 deviation` 제품 baseline은
  [EVID-20260808-015](TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
  기록했습니다.
- GDJ-0017의 exact lifecycle 10개, 9-set protocol, product fail-closed와 revision-fence spike는
  [EVID-20260808-016](TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)에
  기록했습니다.
- Machine artifact commit 기준 `make check`, uncached full Go/race/CGO=0/vet, exact lifecycle
  13 tests, lifecyclefence count=20/race와 two-process count=100 gate가 통과했습니다.
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

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 항목은 별도 GDJ-0018 activation 전까지 미결정입니다.

1. Public lifecycle coordinator/target request 이름, zero value와 exact result shape
2. Revision-capable optional port, SQLite metadata storage/upgrade와 successor handoff interface
3. Conflict/contention/capability/uncertain-commit의 final structured error taxonomy
4. Existing database exclusive cutover, revision overflow, copy/restore epoch와 crash recovery
5. Q-012 loader/source, data callback, public CLI/project protocol과 migration format
6. Q-011/Q-010/Q-013 request lifetime, project handshake와 cross-app relation loader

## 다음 정확한 작업

현재 ready 작업은 없습니다. 별도 GDJ-0018을 activation할 때 Accepted ADR-0017의 최소 제품
단면과 수정 허용 경로를 먼저 정하고, storage/API/error/cutover 범위와 명시적 비목표를 work에
기록해야 합니다. 그 전에는 test-only spike를 제품 package로 옮기거나 MIG-047..056을
`passing`으로 전환하지 않습니다.

## 작업 재개 체크포인트

- 최신 machine artifact: `main@6e018e00bd9178858db597400ac9d3f98a66acf6`
- 제품 baseline: `main@3b0e68d6717a9612debc9cb93d03ab0f98005860`
- 최근 완료 work: [GDJ-0017](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- active work: 없음
- ready work: 없음
- 새 작업 activation 전 보존할 분류: 8 product set `83 passing + 4 deviation` + lifecycle
  `10 oracle_locked`; 9 reference set/97 contract/72 ordered cross-binding
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 현재 가장 위험한 과장: exact lifecycle oracle이나 test-only revision-fence spike를 product
  adapter, public migrate API, distributed lock 또는 crash recovery로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
