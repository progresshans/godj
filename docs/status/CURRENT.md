# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `b721bb6b81ba9a950558c288dcb1a78efd7ff9ab`
  (`test: lock migration execution contracts`)
- 기준 제품 commit:
  `31d264ad7c85a23b511a7549d698c1c3b0577e92`
  (`feat: implement immutable migration planner`)
- 활성 작업 baseline: `b721bb6b81ba9a950558c288dcb1a78efd7ff9ab`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: Migration plan execution orchestrator와 atomic-reverse 정책 구현 전 설계
- 완료 작업: [GDJ-0011 Migration Plan Execution Compatibility Contracts](../../work/0011-migration-plan-execution-compatibility-contracts.md)
- 활성 작업: [GDJ-0012 Migration Plan Execution Orchestrator](../../work/0012-migration-plan-execution-orchestrator.md)
- 다음 ready 작업: 없음 — GDJ-0012 결과로 ADR-0014/DEV-0001 승인 여부 결정

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
  MIG-005..016을 실행합니다. 여러 `PlanStep`을 실행하는 `ExecutePlan`은 아직 없습니다.
- SQLite table rebuild가 필요한 default AddField, indexed/CHECK/generated/view/trigger/FK
  dependency drop은 silent fallback 없이 구조화된 capability error입니다.

### 호환 계약과 machine artifact

- Protocol v2에는 여섯 ordered reference set이 있습니다. M1 read/metadata 11개, M2
  write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개, migration
  planning 12개, migration plan execution 10개로 총 67개입니다.
- 제품 adapter가 있는 기존 다섯 set의 57개만 Django oracle과 semantic 0-diff인
  `passing`입니다. 총 67개를 제품 통과로 표현하지 않습니다.
- Sixth set MIG-017..026은 manifest 10 `oracle_locked`, Django oracle 10 `observed`, static
  fixture 10 `not_implemented`입니다. Product `godjcheck`는 이 manifest를 exit 2/no actual
  output으로 fail-closed합니다.
- 여섯 set의 contract ID/scenario는 전역으로 유일하고 모든 30개 ordered cross-binding이
  validation에서 거부됩니다.
- Migration-execution manifest는 8,720 bytes, SHA-256
  `f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d`입니다.
- Migration-execution Django oracle은 47,119 bytes, SHA-256
  `641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`입니다.
- Migration-execution static fixture는 1,685 bytes, SHA-256
  `6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`입니다.
- 두 독립 random-hashseed process와 checked-in migration-execution oracle은
  byte-identical합니다.
- External execution metrics는 connection summary와 compact ordered steps만 포함합니다.
  Raw render/operation/recorder/transaction event는 runner 내부 live assertion이며 external
  compatibility surface가 아닙니다.
- Historical `ProjectState` before/after는 MIG-019에만 포함하고, MIG-023/024 recorder
  sentinel은 `fault_point=before_record_write`입니다.
- MIG-024의 exact 결과는 A3 unapply와 A2 schema reverse가 commit된 뒤 A2 recorder 삭제
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

## Proposed 결정 — 아직 확정 아님

- [ADR-0014](../adr/0014-migration-plan-execution-atomic-reverse.md)는 최소
  `Executor.ExecutePlan(ctx, before, definitions, plan)`과 full zero-I/O preflight,
  migration별 기존 `Apply`/`Unapply` commit, first-failure last durable state를 제안합니다.
- [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)은
  Django `schema_then_record` 대신 GoDj 기존 same-transaction reverse를 유지하는 후보입니다.
- 이 제안은 backend/SQLite transaction interface를 보존하고 MIG-024형 schema/history
  partial commit을 막지만 Django reference 네 계약과 다릅니다.
- 구현·승인·검증 뒤 기대 분류는 MIG-017/019/021/023/025/026 `passing`,
  MIG-018/020/022/024 `deviation`이며 최종 후보 합계는 `63 passing + 4 deviation`입니다.
  현재 상태는 여전히 `57 passing + 10 oracle_locked`입니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 열려 있습니다.

1. ADR-0014/DEV-0001 atomic reverse 후보의 구현 결과와 승인 여부
2. Proposed `ExecutePlan` API와 `CategoryExecution`의
   `invalid_execution_plan`/`mixed_directions` error taxonomy
3. Q-011: request/transaction/hook 범위의 goroutine·lifetime 정책
4. Q-010: public CLI와 project library/generator version protocol
5. Q-012 후속: migration file/loader, recorder read, data callback, lock와 crash recovery
6. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. `migrations/executor.go`와 test를 읽어 existing
   Apply/Unapply/state preflight/rollback cause seam을 inventory합니다.
2. Empty plan backend/definition zero-touch와 invalid/duplicate/missing/mixed full-preflight
   zero-I/O test를 먼저 작성합니다.
3. Per-step 기존 Apply/Unapply를 사용해 ordered execution, first-failure stop과 last durable
   `ProjectState`를 구현합니다.
4. Pre-canceled, step 사이, in-flight cancellation/rollback을 Go-native test로 검증합니다.
5. Reference oracle/comparator를 바꾸지 않고 live product observation과 별도 atomic-reverse
   `godj-migration-execution-deviation-expected.json`을 연결합니다.
6. 실제 결과를 검토한 뒤에만 ADR-0014/DEV-0001과 contract 상태를 갱신합니다.

## 작업 재개 체크포인트

- 활성 baseline: `main@b721bb6b81ba9a950558c288dcb1a78efd7ff9ab`
- Machine artifact commit은 GDJ-0011 contract-only이며 제품 execution adapter는 없음
- 활성 work: [GDJ-0012](../../work/0012-migration-plan-execution-orchestrator.md)
- 금지 경로: `migrations/backend/backend.go`, `db/sqlite/migration_backend.go`, Planner 관련
  source, Django runner/execution oracle/static fixture, 기존 다섯 manifest/oracle/static fixture
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 false green: Django backward phase/DB state를 GoDj expected에 맞게 rewrite하거나
  Proposed deviation을 승인·검증 전 `passing`으로 세는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
