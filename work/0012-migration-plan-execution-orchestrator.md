---
id: GDJ-0012
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "b721bb6b81ba9a950558c288dcb1a78efd7ff9ab"
depends_on: ["GDJ-0011"]
contracts: ["MIG-017..MIG-026", "Q-012", "DEV-0001"]
allowed_paths: ["migrations/executor.go", "migrations/executor_test.go", "migrations/execution.go", "migrations/execution_test.go", "migrations/external_test.go", "db/sqlite/migration_backend.go", "db/sqlite/migration_backend_test.go", "conformance/contracts/migration-execution-manifest.json", "conformance/runners/godj/**", "conformance/runners/django/tests/test_migration_execution_scenarios.py", "conformance/cmd/godjcheck/**", "conformance/internal/protocol/**", "conformance/fixtures/godj-migration-execution-deviation-expected.json", "Makefile", ".github/workflows/ci.yml", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Migration Plan Execution Orchestrator와 Atomic-Reverse 결정

## 사용자에게 보이는 결과

Planner가 만든 여러 migration step을 GoDj가 순서대로 실행하고, 각 migration을 독립적으로
commit하며, 첫 실패 뒤 마지막 durable `ProjectState`와 오류를 반환할 수 있게 합니다.
역방향 schema와 recorder는 같은 transaction에서 처리하는 GoDj의 더 강한 원자성을
Django reference와 다른 동작으로 숨기지 않고 별도 expected/deviation gate로 검증합니다.

## 목표

- 최소 `Executor.ExecutePlan` orchestration과 full zero-I/O preflight 구현
- empty plan의 backend/definition zero-touch no-op
- invalid/duplicate/missing/mixed plan을 첫 transaction 전에 구조화된 오류로 거부
- forward/backward plan 순서, migration별 commit과 first-failure stop 구현
- 실패 시 마지막 durable commit에 해당하는 `ProjectState` 반환
- 기존 same-transaction `Apply`/`Unapply`와 backend/SQLite interface 보존
- 빈 SQLite table에 대한 no-default non-null, non-PK `AddField`와 non-PK reverse `RemoveField`의
  좁은 capability 지원; row가 있으면 table rebuild 없이 capability error 유지
- pre-step, step 사이, in-flight context cancellation/rollback 검증
- MIG-017..026 product adapter와 atomic-reverse expected fixture/harness 구축
- ADR-0014와 DEV-0001을 구현 증거에 따라 승인하고 전용 expected로 검증
- 기존 57 product differential과 six-set false-green gate 보존

## 비목표

- Django backward `schema_then_record` partial-commit을 제품에 그대로 구현
- `migrations/backend/backend.go` 또는 SQLite migration transaction interface 변경
- row가 있는 table의 non-null/default AddField backfill이나 table rebuild
- Planner/graph/applied-state 정책 변경
- Django runtime scenario/normalizer, execution oracle/static fixture 또는 기존 다섯 artifact
  변경. Final manifest status/decision을 검증하는 기존 Python assertion만 갱신 가능
- Execution manifest의 Django reference phase/comparison dimension 변경
- public migration file/source encoding, loader, CLI와 code generator
- recorder read/list, startup state reconstruction, data migration callback ABI
- non-atomic migration/operation, fake, squash/replacement/merge/optimizer
- process lock, crash recovery/repair, multi-DB/router와 backend 확대

## 선행 조건과 기준 상태

- Baseline machine commit:
  `main@b721bb6b81ba9a950558c288dcb1a78efd7ff9ab`
- Product baseline commit:
  `31d264ad7c85a23b511a7549d698c1c3b0577e92`
- [GDJ-0011](0011-migration-plan-execution-compatibility-contracts.md)은 MIG-017..026
  10개를 `oracle_locked`로 고정했고 제품 adapter는 fail-closed합니다.
- 기존 다섯 GoDj adapter의 57개는 semantic 0-diff입니다.
- [ADR-0010](../docs/adr/0010-m2-migration-state-and-executor-boundary.md)의 한 migration
  same-transaction editor/recorder와
  [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)의 immutable plan을 보존합니다.
- [ADR-0014](../docs/adr/0014-migration-plan-execution-atomic-reverse.md)와
  [DEV-0001](../docs/DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)은
  baseline 당시 Proposed였으며 구현 또는 compatibility pass를 뜻하지 않았습니다.

## Django Reference / Contract

Reference profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다.

- MIG-017: 3-step linear forward commit
- MIG-018: dependent-first full backward commit
- MIG-019: applied prefix 뒤 tail과 step별 historical state
- MIG-020: branch rollback과 unrelated applied branch 보존
- MIG-021/022: forward/backward operation failure, 앞선 commit과 이후 미실행
- MIG-023: before-write forward recorder failure와 실패 step rollback
- MIG-024: before-write backward recorder failure 뒤 schema A1, records A1/A2
- MIG-025: mixed direction을 domain step 전에 거부
- MIG-026: empty plan no-op

Reference external metrics는 connection summary와 compact ordered steps만 포함합니다. Raw
render/operation/recorder/transaction event는 Django runner 내부 assertion입니다. Historical
before/after는 MIG-019에만 있고, MIG-023/024는 `fault_point=before_record_write`를
명시합니다.

## 구현된 설계

검증된 공개 API는 다음과 같습니다.

```go
func (e Executor) ExecutePlan(
    ctx context.Context,
    before ProjectState,
    definitions []Migration,
    plan []PlanStep,
) (ProjectState, error)
```

### Preflight

- Context가 nil/canceled인지 먼저 확인합니다. 유효한 context의 empty plan은 definition과
  backend를 보지 않고 `before`와 같은 logical state를 반환합니다.
- Non-empty plan은 context, migration key/direction, definition key/duplicate, step duplicate,
  누락 definition, mixed direction과 모든 state transition을 검사합니다.
- 전 step의 state simulation이 성공하기 전에는 transaction/DDL/recorder I/O가 0입니다.
- 오류는 `CategoryExecution` + `invalid_execution_plan`/`mixed_directions`입니다.

### 실행

- Plan 순서를 바꾸거나 dependency를 다시 계산하지 않습니다.
- Dependency/history consistency는 Planner 책임이며 ExecutePlan은 graph를 다시 검증하거나
  계획하지 않습니다.
- 각 step은 기존 `Apply` 또는 `Unapply`를 호출해 migration별 transaction으로 commit합니다.
- 성공한 step 뒤에만 working `ProjectState`를 갱신합니다.
- 첫 실패에서 즉시 중단하고 뒤 transaction을 시작하지 않으며 마지막 durable state를
  반환합니다.
- Reverse schema와 recorder는 같은 transaction을 유지합니다. Backend/SQLite transaction
  interface는 변경하지 않습니다.
- Locked A2가 빈 table에 no-default non-null Boolean field를 추가하므로 SQLite implementation은 같은
  transaction에서 row 존재를 먼저 확인합니다. Empty면 persistent DB default 없이 column을
  추가하고, row가 하나라도 있거나 IR default가 있으면 기존 capability error를 유지합니다. Reverse DROP COLUMN은
  non-PK field의 nullability를 이유로 거부하지 않되 기존 dependency preflight를 보존합니다.

### Cancellation

- Pre-canceled context는 preflight/backend I/O 전에 `context.Canceled`를 반환합니다.
- Step 사이 cancellation은 다음 begin 전에 멈추고 앞선 commit/state를 보존합니다.
- In-flight cancellation은 취소와 분리된 cleanup context로 rollback을 끝내고 primary와
  rollback cause를 보존해야 합니다.
- 마지막 step commit 뒤에야 cancellation을 관찰하면 완료를 성공으로 취급합니다.

### Compatibility/deviation harness

Reference manifest의 Django phase/comparison dimension과 oracle을 GoDj에 맞게 바꾸지 않고
core comparator를 느슨하게 하지 않습니다. 제품 adapter는 live API 결과를 observation으로
만들고, 별도 `godj-migration-execution-deviation-expected.json`과 fail-closed harness가
atomic-reverse 차이를 검증합니다. Execution manifest는 deviation 네 개에만
`decision=DEV-0001`, `derived=false` provenance를 둡니다.

Product expectation 비교는 reference oracle을 원 manifest로 먼저 strict
validation하고, `passing` 항목이 Django observation과 같은지, `deviation` 항목은 지정된
차이만 갖는지 확인한 뒤 effective product phase로 live GoDj actual과 exact 비교합니다.
Deviation이라는 이유로 field 비교를 skip하지 않고, 누락/추가 차이는 exit 2로
fail-closed합니다.

MIG-017/019/021/023/025/026은 `passing`, MIG-018/020/022/024는 `deviation`입니다.
현재 제품 분류는 `63 passing + 4 deviation`입니다.

## 구현 단계

1. Existing Executor/Error/ProjectState/PlanStep API와 transaction fault-injection seam을
   inventory하고 API/error candidate를 compile test로 좁힙니다.
2. Empty no-op와 full plan/state preflight를 unit test-first로 구현합니다.
3. Existing `Apply`/`Unapply`를 사용한 ordered per-step orchestration, last durable state와
   first-failure stop을 구현합니다.
4. Empty-table non-null AddField/RemoveField capability와 nonempty fail-closed를 검증합니다.
5. Pre/between/in-flight cancellation과 rollback error precedence를 fault-injection으로
   검증합니다.
6. MIG-017..026 GoDj live adapter와 별도 product expected fixture를 연결합니다.
7. Reference 10개 비교 결과를 6 same + 4 candidate deviation으로 정확히 분류하고
   comparator/static/fail-closed gate를 유지합니다.
8. ADR-0014와 DEV-0001을 검토해 승인 여부를 결정하고, 승인된 경우에만 manifest/status를
   `passing`/`deviation`으로 갱신합니다.
9. Full/race/CGO=0/vet, 기존 57 differential, documentation/status/evidence gate를 실행합니다.

## 완료 조건

- [x] 유효한 context의 empty plan이 backend/definition zero-touch로 같은 logical state를 반환
- [x] Non-empty full preflight failure에서 transaction/DDL/recorder I/O 0
- [x] invalid/duplicate/missing/mixed plan error category/code와 timing 검증
- [x] Forward/backward ordered execution과 migration별 commit 검증
- [x] 첫 실패 뒤 last durable `ProjectState`, 실패 step rollback, 이후 미실행 검증
- [x] Pre/between/in-flight cancellation과 rollback cause 검증
- [x] Backend/SQLite interface와 Planner source 변경 없음
- [x] Empty-table no-default non-null AddField/RemoveField는 동작하고 nonempty/default AddField는 zero-change capability error
- [x] Product adapter가 live Executor 결과를 사용하고 unknown scenario가 fail-closed
- [x] Reference oracle/static fixture bytes와 기존 다섯 artifact checksum 유지
- [x] Core comparator 완화 없이 6 exact + 4 reviewed deviation 결과 식별
- [x] MIG-024 product expectation은 A3만 commit, A2 schema/record retained, phase rollback을 고정
- [x] ADR-0014 Accepted와 DEV-0001 Verified 근거 기록
- [x] 기존 57 product differential, full/vet/race/CGO=0 통과
- [x] CURRENT/matrix/evidence/work가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0011 sixth reference set과 transaction difference 확인
- [x] ADR-0014/DEV-0001 Proposed와 bounded product work 활성화
- [x] Existing API/fault seam inventory와 test-first preflight
- [x] ExecutePlan implementation
- [x] Product observation/deviation harness
- [x] Full verification과 상태 승인

## 수정 파일

- `migrations/execution.go`, `executor.go`와 test: full plan preflight, ordered execution,
  cancellation와 external API/error gate
- `db/sqlite/migration_backend.go`와 test: empty-table non-null AddField와 non-PK reverse
  capability, nonempty/default/dependency fail-closed
- `conformance/runners/godj/**`: live SQLite execution observation과 trace validation
- `conformance/internal/protocol/**`, `conformance/cmd/godjcheck/**`: sparse deviation
  expectation, code-owned policy와 fail-closed comparison
- `conformance/contracts/migration-execution-manifest.json`: 6 passing + 4 deviation status와
  DEV-0001 provenance
- `conformance/fixtures/godj-migration-execution-deviation-expected.json`: 승인된 차이만 담는
  sparse product expectation
- `conformance/runners/django/tests/test_migration_execution_scenarios.py`: final manifest의
  6 passing + 4 DEV-0001 provenance assertion만 갱신; Django scenario와 locked oracle은 불변
- `Makefile`: sixth product conformance command

## 결정된 사항

- 2026-08-08: Reference oracle은 Django의 실제 backward `schema_then_record`를 축소 없이
  보존합니다.
- 2026-08-08: GoDj same-transaction reverse를 ADR-0014로 Accepted하고 DEV-0001 네 계약을
  Verified deviation으로 기록합니다.
- 2026-08-08: File/CLI/recorder read/lock를 끌어오지 않고 in-memory definitions/plan의
  최소 orchestrator만 구현합니다.

## 미결정/Blocker

외부 blocker는 없습니다. Recorder read/restart planning, public migration file/CLI,
data callback, process lock와 crash recovery는 후속 Q-012 범위입니다.

## 테스트 증거

- Evidence ID:
  [EVID-20260808-011](../docs/status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)
- Baseline: [EVID-20260808-010](../docs/status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)
- Product commit: `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
- `make check`, uncached full Go/race/CGO=0/vet, focused repetition, exact Python 79,
  67-contract conformance와 두 독립 actual 결정성이 통과했습니다.
- Not run: GitHub-hosted workflow — push하지 않았습니다.

## 위험과 rollback

- Preflight 중 일부 step을 실행하면 invalid tail이 앞선 DB를 바꾸는 false atomicity가 됩니다.
- Django backward phase를 제품에 맞게 rewrite하면 compatibility difference를 잃습니다.
- Expected fixture를 core comparator 예외/skip으로 구현하면 실제 mismatch까지 녹색이 될 수 있습니다.
- Cancellation cleanup이 같은 canceled context를 쓰면 rollback failure가 primary error를
  가릴 수 있습니다.
- Rollback은 GDJ-0012의 Executor/adapter/expected/docs 변경만 되돌리고 locked reference와
  기존 one-migration/backend bytes를 보존합니다.

## 다음 정확한 작업

[GDJ-0013](0013-recorder-backed-restart-planning-compatibility-contracts.md)에서 recorder table
없음/empty/record/unrecord와 fresh executor의 applied-prefix tail 계획을 exact Django
reference로 먼저 고정합니다. 제품 recorder-read API는 그 다음 work로 분리합니다.

## 결과와 인수인계

GDJ-0011의 exact reference를 입력으로 제품 commit
`3bcd25ce557cfddc2d73652f9154b6db0fd0b065`을 만들었습니다. Reference oracle과 static
fixture bytes를 유지하면서 6 exact `passing`과 4 DEV-0001 `deviation`을 live SQLite
actual로 검증했습니다. 다음 담당자는 Q-012 전체 완료를 주장하지 말고 GDJ-0013의
recorder-backed restart contract부터 진행해야 합니다.
