# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
  (`feat: execute migration plans with atomic reverse`)
- 기준 제품 commit:
  `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
  (`feat: execute migration plans with atomic reverse`)
- 활성 작업 baseline: `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: Recorder-backed restart planning 호환 계약 설계·probe
- 완료 작업: [GDJ-0012 Migration Plan Execution Orchestrator](../../work/0012-migration-plan-execution-orchestrator.md)
- 활성 작업: [GDJ-0013 Recorder-backed Restart Planning Compatibility Contracts](../../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)
- 다음 ready 작업: 없음 — GDJ-0013 exact 계약 뒤 recorder-read 제품 단면 설계

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

### 호환 계약과 machine artifact

- Protocol v2에는 여섯 ordered reference set이 있습니다. M1 read/metadata 11개, M2
  write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개, migration
  planning 12개, migration plan execution 10개로 총 67개입니다.
- 여섯 제품 set의 분류는 63 `passing` + 4 approved `deviation`입니다. 총 67개를 Django와
  exact 일치하는 제품 통과로 표현하지 않습니다.
- MIG-017/019/021/023/025/026은 `passing`, MIG-018/020/022/024는 DEV-0001
  `deviation`입니다. Django oracle 10 `observed`와 static fixture 10 `not_implemented`는
  유지하며 static comparison은 ordered mismatch 10개입니다.
- 여섯 set의 contract ID/scenario는 전역으로 유일하고 모든 30개 ordered cross-binding이
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
- 최종 분류는 MIG-017/019/021/023/025/026 `passing`,
  MIG-018/020/022/024 `deviation`, 합계 `63 passing + 4 deviation`입니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 열려 있습니다.

1. Q-012 후속: recorder read와 restart planning exact 의미(GDJ-0013), migration file/loader,
   data callback, lock와 crash recovery
2. Q-011: request/transaction/hook 범위의 goroutine·lifetime 정책
3. Q-010: public CLI와 project library/generator version protocol
4. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. Pinned Django 6.1의 recorder `has_table/applied_migrations`, loader consistency와 executor
   applied-state plan 경계를 확인합니다.
2. Disposable SQLite에서 MIG-027 recorder-table-absent read의 before/after inventory와
   non-SELECT 0을 두 번 probe합니다.
3. MIG-028..036을 fresh recorder/executor instance로 독립 실행하고 payload를 정규화합니다.
4. Seventh manifest/oracle/static fixture와 seven-set 42 cross-binding gate를 연결합니다.
5. Product recorder-read API는 reference lock 뒤 별도 work/ADR에서만 설계합니다.

## 작업 재개 체크포인트

- 활성 baseline: `main@3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
- 제품/machine commit은 GDJ-0012 `ExecutePlan` + DEV-0001 검증을 포함
- 활성 work: [GDJ-0013](../../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)
- 금지 제품 경로: `migrations/**`, `migrations/backend/**`, `db/sqlite/**`,
  `conformance/runners/godj/**`, `cmd/godj/**`
- 기존 여섯 manifest/oracle/static/deviation artifact의 의미와 bytes는 변경하지 않음
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 false green: setup recorder/executor cache를 fresh restart로 오인하거나 read가
  recorder table을 생성해도 empty applied result만 보고 통과시키는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
