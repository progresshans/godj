# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
  (`test: lock historical project state contracts`)
- 기준 제품 commit:
  `a9ce9597551840f1be8e1f27006d427842f38081`
  (`feat: add recorder-backed restart planning`)
- 활성 작업 baseline: `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: Historical `ProjectState` reconstruction product slice
- 완료 작업: [GDJ-0015 Historical ProjectState Reconstruction Compatibility Contracts](../../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)
- 활성 작업: [GDJ-0016 Historical ProjectState Reconstruction Product Slice](../../work/0016-historical-project-state-reconstruction-product-slice.md)
- 다음 ready 작업: 없음 — GDJ-0016 완료 뒤 source loader/CLI와 lifecycle lock 범위를 재평가

## 현재 checkout에서 확인된 사실

### 제품 구현

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2가 `AutoField`, `CharField`, `BooleanField`, nullable
  CharField와 typed scalar default의 존재/zero value를 보존합니다.
- Deterministic codegen `godj-codegen-m2-v3`는 `Article`, FieldSet, descriptor,
  `CloneModel`, immutable create/patch builder, nullable deep clone, explicit-key constructor와
  Save option helper를 생성하고 schema hash/generator version/last-good drift gate를 유지합니다.
- Generic Manager/QuerySet, typed/dynamic lookup, immutable read/mutation AST와 SQLite
  compiler/executor가 구현됐습니다. QuerySet은 direct value copy가 공유하는 평가 state,
  successful full-result cache, singleflight와 `Fresh`/terminal subset을 제공합니다.
- Write 제품 단면은 one-row create/partial update/delete, callback-bound transaction,
  mutable instance Save, typed/dynamic mask, force mode와 explicit PK fallback을 제공합니다.
- Migration 제품 단면은 versioned `ProjectState`, typed `CreateModel`/nullable no-default
  `AddField`, 한 migration의 preflighted `Executor`와 같은 SQLite transaction의
  editor/recorder를 제공합니다.
- Immutable migration identity graph, 별도 `AppliedState`와 zero-I/O `Planner`가
  MIG-005..016을 실행합니다. `Executor.ExecutePlan`은 전체 plan/state를 I/O 전에
  검증하고 기존 Apply/Unapply를 migration별 transaction으로 실행하며 첫 실패에서 마지막
  durable `ProjectState`를 반환합니다.
- SQLite migration backend는 empty table의 no-default non-null non-PK AddField와 reverse
  RemoveField를 지원하고, row/default/rebuild 의존 경계는 구조화된 capability error입니다.
- SQLite table rebuild가 필요한 default AddField, indexed/CHECK/generated/view/trigger/FK
  dependency drop은 silent fallback 없이 구조화된 capability error입니다.
- Migration recorder read는 write transaction 경계와 분리된 backend-neutral
  `AppliedMigrationReader`를 사용합니다. Core `LoadAppliedState`가 raw identity를 복사·검증하고
  `Planner.CheckHistory`가 target planning 전 known dependency consistency를 명시적으로 검사합니다.
- SQLite recorder reader는 exact missing-table만 empty로 정규화하고 recorder table을 만들지
  않습니다. Fresh file backend, database isolation, record/unrecord durable visibility와
  exact-one-SELECT/no-write driver gate가 검증됐습니다.

### 호환 계약과 machine artifact

- Protocol v2에는 여덟 ordered reference set이 있습니다. M1 read/metadata 11개, M2
  write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개, migration
  planning 12개, migration plan execution 10개, recorder-backed restart planning 10개,
  historical-state reconstruction 10개로 총 87개입니다.
- 앞의 일곱 set에는 제품 adapter가 있으며 분류는 73 `passing` + 4 approved
  `deviation`입니다. 새 historical-state set은 10 `oracle_locked`이고 제품 adapter가 없습니다.
  DEV-0001과 미구현 set 때문에 총 87개를 Django exact 제품 통과로 표현하지 않습니다.
- MIG-017/019/021/023/025/026은 `passing`, MIG-018/020/022/024는 DEV-0001
  `deviation`입니다. Django oracle 10 `observed`와 static fixture 10 `not_implemented`는
  유지하며 static comparison은 ordered mismatch 10개입니다.
- 여덟 set의 contract ID/scenario는 전역으로 유일하고 모든 56개 ordered cross-binding이
  validation에서 거부됩니다.
- Migration-execution manifest는 9,120 bytes, SHA-256
  `1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9`입니다.
- Migration-execution Django oracle은 47,119 bytes, SHA-256
  `641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`입니다.
- Migration-execution static fixture는 1,685 bytes, SHA-256
  `6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`입니다.
- Migration-execution deviation expectation은 4,685 bytes, SHA-256
  `568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547`입니다.
- 두 독립 GoDj actual은 각각 47,446 bytes, SHA-256
  `f191d116cc38194e2019df358c31f752101fdacb005d9cc442b701d8d4afde4b`로 byte-identical하고
  reviewed product expectation과 10개 0-diff입니다.
- 두 독립 random-hashseed process와 checked-in migration-execution oracle은
  byte-identical합니다.
- Recorder-restart passing manifest는 10,165 bytes, SHA-256
  `79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`입니다.
- Recorder-restart Django oracle은 33,888 bytes, SHA-256
  `90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`입니다.
- Recorder-restart static fixture는 1,715 bytes, SHA-256
  `31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`입니다.
- MIG-027..036은 모두 `evaluation` phase입니다. Absent/empty recorder, record/unrecord fresh
  read, database isolation, applied-prefix/fully-applied plan, unknown/known history와
  middle-failure restart를 10 `passing`/10 `observed`/10 static `not_implemented`로
  구분합니다.
- 두 독립 random-hashseed process와 checked-in recorder-restart oracle은 33,888 bytes의
  같은 canonical bytes였습니다. 두 독립 GoDj actual은 각각 33,795 bytes, SHA-256
  `f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`로 byte-identical하고
  Django oracle과 protocol 의미상 10개 0-diff입니다.
- Historical-state reconstruction manifest는 9,257 bytes, SHA-256
  `04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`입니다.
- Historical-state reconstruction Django oracle은 89,997 bytes, SHA-256
  `bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`입니다.
- Historical-state reconstruction static fixture는 1,715 bytes, SHA-256
  `9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`입니다.
- MIG-037..046은 모두 `evaluation` phase입니다. Explicit empty/latest, first·middle
  before/after, cross-app/shared dependency와 applied-prefix/unrelated-known/unknown-legacy
  startup state를 10 `oracle_locked`/10 `observed`/10 static `not_implemented`로 구분합니다.
- 두 독립 random-hashseed process와 checked-in historical-state oracle은 89,997 bytes의
  같은 canonical bytes였습니다. 제품 adapter는 아직 없으며 actual을 합성하지 않고
  unsupported scenario에서 exit 2/no output으로 fail-closed합니다.
- External execution metrics는 connection summary와 compact ordered steps만 포함합니다.
  Raw render/operation/recorder/transaction event는 runner 내부 live assertion이며 external
  compatibility surface가 아닙니다.
- Historical `ProjectState` before/after는 MIG-019에만 포함하고, MIG-023/024 recorder
  sentinel은 `fault_point=before_record_write`입니다.
- MIG-024의 Django reference exact 결과는 A3 unapply와 A2 schema reverse가 commit된 뒤 A2 recorder 삭제
  전에 실패하여 최종 schema A1, recorder A1/A2, phase `commit`입니다.
- 기존 planning manifest/oracle/static hashes와 다섯 product adapter는 GDJ-0011에서
  변경하지 않았습니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference SQLite
  3.50.4와 별도 fingerprint로 관리합니다.

### 검증 증거

- GDJ-0010의 Planner unit/property/race, external API, full/vet/CGO=0, fifth adapter,
  57 differential과 mutation/hardcode audit는
  [EVID-20260808-009](TEST_EVIDENCE.md#evid-20260808-009--gdj-0010-immutable-migration-planner-product-slice)에
  기록했습니다.
- GDJ-0011의 two-process exact oracle, portable Python 79 pass/7 exact skips, exact Python
  79 pass, full uncached Go/race/CGO=0/vet, six-set/30 cross-binding, static ordered 10
  mismatch, product fail-closed, existing 57 differential과 mutation audit는
  [EVID-20260808-010](TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)에
  기록했습니다.
- 최종 독립 migration-execution contract 감사에서 P0–P3 finding은 없었습니다.
- GDJ-0012의 ExecutePlan/core/backend unit·race·CGO=0, six-set live product conformance,
  sparse deviation fail-closed, two-process Go actual과 독립 core/conformance 감사는
  [EVID-20260808-011](TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
  기록했습니다.
- GDJ-0013의 two-process recorder/restart oracle, portable Python 94 pass/9 exact-only skip,
  exact Python 94/94, seven-set/42 cross-binding, static ordered 10 mismatch, product
  fail-closed와 독립 contract 감사는
  [EVID-20260808-012](TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)에
  기록했습니다.
- GDJ-0014의 backend-neutral reader/core history preflight, SQLite fresh read, seven-set live
  product conformance, two-process Go actual, full/race/CGO=0/vet와 false-green mutation 감사는
  [EVID-20260808-013](TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
  기록했습니다.
- GDJ-0015의 two-process historical-state oracle, portable Python 114개 중 exact-only 11
  skip, exact Python 114/114, eight-set/56 cross-binding, static ordered 10 mismatch,
  product fail-closed와 public-path/ID-dispatch false-green 감사는
  [EVID-20260808-014](TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
  기록했습니다.
- GitHub Actions workflow는 push하지 않아 hosted 실행 증거가 없습니다. 로컬 Django
  checkout은 수정하지 않았습니다.

## 확정된 결정

### 공통 아키텍처

- Exact Python/Django/SQLite/platform/locale profile과 uv lock/hash를 사용합니다.
- `Schema DSL → IR → codegen → generic core → runtime metadata → Query AST → backend` 경계를
  유지합니다.
- Typed/dynamic query API는 같은 AST로 수렴하고 unsupported 기능은 구조화된 오류입니다.
- Generated output은 deterministic/last-good 보존 gate와 external compile test를 가집니다.

### M2 write/migration

- Generated immutable create/patch와 `Change[T]`/`NullableChange[T]`는 ADR-0009,
  mutable Save orchestration은 ADR-0011을 따릅니다.
- [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md)에 따라 한 migration의
  operation과 recorder는 같은 backend transaction에서 commit/rollback됩니다.
- [ADR-0013](../adr/0013-immutable-migration-planner.md)에 따라 graph/applied state는
  historical `ProjectState`와 분리되고 Planner는 zero-I/O pure computation입니다.
- Planning은 recorder/schema state가 같고 DDL/write/기타 non-SELECT statement가 0이어야
  합니다. 모든 SELECT까지 0이라고 주장하지 않습니다.

### Multi-migration execution reference

- MIG-017..026은 migration별 commit, first-failure stop, 앞선 durable step, 실패 step
  rollback/partial state, 이후 미실행, mixed preflight와 empty no-op를 reference로 고정합니다.
- Plan 전체가 atomic하다고 가정하지 않습니다. Forward/backward 성공과 operation/recorder
  failure는 ordered step별 schema/recorder outcome으로 구분합니다.
- Django backward 성공은 `schema_then_record`입니다. 이 transaction model은
  MIG-018/020/022에서 관찰되고 MIG-024에서는 material DB state/phase 차이로 이어집니다.
- Go `context.Context` cancellation은 Django differential ID에 합성하지 않고 GDJ-0012의
  Go-native pre/between/in-flight gate로 검증합니다.

### Multi-migration execution product

- [ADR-0014](../adr/0014-migration-plan-execution-atomic-reverse.md)는 최소
  `Executor.ExecutePlan(ctx, before, definitions, plan)`과 full zero-I/O preflight,
  migration별 기존 `Apply`/`Unapply` commit, first-failure last durable state를 채택합니다.
- [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)은
  Django `schema_then_record` 대신 GoDj 기존 same-transaction reverse를 유지하는 Verified
  결정입니다.
- 이 결정은 backend/SQLite transaction interface를 보존하고 MIG-024형 schema/history
  partial commit을 막지만 Django reference 네 계약과 다릅니다.
- Execution set 분류는 MIG-017/019/021/023/025/026 `passing`,
  MIG-018/020/022/024 `deviation`입니다. Recorder-restart 10개까지 포함한 현재 제품 총계는
  `73 passing + 4 deviation`입니다.

### Recorder-backed restart reference와 제품 경계

- MIG-027..036은 recorder table absent/empty, record/unrecord fresh read, database isolation,
  applied-prefix tail, fully-applied empty, unknown legacy, explicit inconsistent-history preflight와
  middle-failure restart를 10 `passing`으로 검증합니다.
- MIG-035는 executor가 자동 거부한다고 주장하지 않습니다. Migrate-style explicit history
  check가 planning 전에 실패하고 `plan_invoked=false`인 외부 동작입니다.
- [ADR-0015](../adr/0015-recorder-backed-applied-state.md)는 transaction write interface와
  별도인 backend raw read port, core `LoadAppliedState`와 `Planner.CheckHistory`를 Accepted로
  채택합니다.
- GDJ-0014는 `read → validate → check → plan`까지만 제품화했습니다. Historical
  `ProjectState` reconstruction, read/execution lock와 public migrate API는 보장하지 않습니다.

### Historical ProjectState reconstruction reference와 제품 경계

- MIG-037..046은 loaded migration definition의 state transition을 dependency order로
  replay한 explicit empty/latest/before/after/applied historical state를 reference로
  고정합니다. Live database schema나 current generated model은 state의 의미 소스가 아닙니다.
- Canonical observation은 lowercase model key, explicit table/column, declaration-order field
  kind/PK/null/max-length와 supported scalar default presence/type/value를 보존합니다.
- Applied startup은 private helper가 아니라 public
  `MigrationExecutor.migrate(targets=[], plan=[])`를 관찰합니다. Unknown recorder identity는
  applied observation에 남지만 가상 schema로 materialize하지 않습니다.
- Explicit empty와 omitted latest는 서로 다른 durable request 의미입니다. Product API는
  nil/empty variadic 차이가 아니라 tagged request로 구분하는 방향을
  [ADR-0016](../adr/0016-historical-project-state-reconstruction.md)과 GDJ-0016에서 검증합니다.
- GDJ-0015는 contract-only로 끝났습니다. Historical-state product adapter는 없으며 현재
  제품 분류는 계속 `73 passing + 4 deviation`, 신규 10개는 `oracle_locked`입니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 열려 있습니다.

1. Q-012 후속: historical `ProjectState` reconstructor 제품, 그 뒤 migration definition
   source/loader, data callback, lock와 crash recovery
2. Q-011: request/transaction/hook 범위의 goroutine·lifetime 정책
3. Q-010: public CLI와 project library/generator version protocol
4. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. Tagged `StateRequest`와 immutable `StateReconstructor`의 zero value, ownership, external
   package compile shape를 먼저 검증하고 ADR-0016의 Accepted 여부를 결정합니다.
2. Existing Planner graph/order kernel을 재사용해 explicit empty/latest/before/after/applied
   state를 backend I/O 없이 replay하고 definition/request/result alias와 concurrency를 검증합니다.
3. MIG-037..046 GoDj adapter를 public reconstructor 결과와 deliberately divergent live DB에서
   생성하고 manifest status 10개만 `passing`으로 전환합니다.
4. Locked oracle/static bytes와 DEV-0001을 보존하면서 two-process actual 10/0-diff,
   `83 passing + 4 deviation`, full/race/CGO=0/vet와 독립 감사를 완료합니다.

## 작업 재개 체크포인트

- 활성 baseline: `main@594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- Machine commit은 GDJ-0015의 eighth exact set을 포함하고 제품 baseline은
  `a9ce9597551840f1be8e1f27006d427842f38081`의 seven-set `73 passing + 4 deviation` 상태
- 활성 work: [GDJ-0016](../../work/0016-historical-project-state-reconstruction-product-slice.md)
- 제품 수정은 GDJ-0016 frontmatter에 한정하고 `migrations/backend/**`, `db/**`, locked
  Django oracle/static fixture와 SHA256SUMS는 수정하지 않음
- Locked 여덟 reference artifact 의미/bytes와 DEV-0001을 변경하지 않음
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 false green: latest를 global out-degree leaf로 계산하는 것, before에서 명시
  target set을 정확히 제외하지 않는 것, 모든 definition을 applied 여부와 무관하게 replay하는 것,
  live schema를 historical state로 오인하는 것과 lock 없는 read/state/plan/execute를 atomic
  lifecycle처럼 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
