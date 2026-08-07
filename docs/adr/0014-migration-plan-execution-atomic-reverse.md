# ADR-0014: Migration plan 실행은 migration별 commit과 원자적 reverse를 사용한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0011, GDJ-0012, MIG-017..MIG-026, Q-012, DEV-0001
- 대체하는 ADR: 없음

## 맥락

[ADR-0010](0010-m2-migration-state-and-executor-boundary.md)은 한 migration의 schema
operation과 recorder 갱신을 같은 backend transaction에서 처리하는 `Executor.Apply`와
`Executor.Unapply`를 채택했습니다. [ADR-0013](0013-immutable-migration-planner.md)은
dependency/applied-state를 기반으로 불변 `[]PlanStep`을 만드는 zero-I/O Planner를
추가했지만 여러 step을 실제로 실행하는 orchestrator는 정하지 않았습니다.

GDJ-0011은 Django 6.1에서 여러 migration 실행의 외부 동작을 MIG-017..026으로
고정했습니다. Forward는 schema와 recorder가 같은 transaction에서 commit되지만 Django의
backward 경로는 schema transaction을 먼저 끝낸 뒤 recorder row를 지웁니다. 따라서
정상 backward인 MIG-018/020/022의 compact observation은 `schema_then_record`이고,
MIG-024의 before-write recorder fault에서는 A2 schema reverse가 이미 commit된 뒤 A2
recorder row가 남습니다. 최종 상태는 schema A1, recorder A1/A2이며 phase도 `commit`입니다.

GoDj의 현재 `Unapply`는 schema reverse와 recorder 삭제를 같은 transaction으로 묶습니다.
이를 Django와 똑같게 바꾸면 recorder 장애가 schema/history 불일치를 durable state로
남길 수 있고, 기존 MIG-001..004에서 검증한 원자적 reverse 의미와 backend interface까지
바꾸게 됩니다. 반대로 기존 원칙을 유지하면 Django exact contract 네 개와 관찰 가능한
차이가 생기므로 명시적 deviation 절차가 필요합니다.

## 결정 기준

- 전체 plan을 하나의 transaction으로 오해하지 않고 migration별 durable commit을 보존
- 실행 시작 전 plan/definition/state 오류를 검출해 partial execution을 막음
- 첫 실패에서 멈추고 마지막으로 commit된 `ProjectState`를 정확히 반환
- schema와 recorder 사이의 durable 불일치를 GoDj 기본 실패 모드로 만들지 않음
- 기존 `migrations/backend`와 SQLite transaction interface를 불필요하게 변경하지 않음
- Django와 다른 transaction/phase/DB state를 comparator 완화 없이 드러냄
- cancellation 시 이미 commit된 migration과 아직 시작하지 않은 migration을 구분

## 고려한 선택지

### Django backward transaction 경계를 그대로 구현

정상/실패 관찰은 MIG-018/020/022/024와 일치하지만 reverse schema commit 뒤 recorder
삭제 실패가 applied history와 schema를 어긋나게 합니다. 기존 same-transaction
`Unapply` 계약과 backend 구현을 바꾸고 별도 복구 정책을 먼저 정해야 합니다.

### Plan 전체를 한 transaction으로 실행

중간 실패 때 전 step을 되돌려 일관성은 단순해지지만 Django의 migration별 commit과
MIG-021/022/023의 앞선 durable step을 위반합니다. 장시간 plan의 lock과 backend DDL
transaction 차이도 한 번에 끌어옵니다.

### Migration별 실행을 유지하고 reverse도 같은 transaction으로 처리

기존 `Apply`/`Unapply`를 step primitive로 재사용합니다. 앞선 migration은 commit되고 실패한
migration만 rollback됩니다. Django backward transaction model과 달라지는 네 contract는
별도 GoDj expected observation과 DEV-0001로 정직하게 검증합니다.

## 결정

GDJ-0012는 다음 최소 API를 구현하고 외부 compile/API test와 actual adapter로
검증했습니다.

```go
func (e Executor) ExecutePlan(
    ctx context.Context,
    before ProjectState,
    definitions []Migration,
    plan []PlanStep,
) (ProjectState, error)
```

- Context가 nil/canceled인지 먼저 확인합니다. 유효한 context의 empty plan은 backend 접근과
  definition 검증 전에 `before`와 같은 logical state를 반환합니다.
- Non-empty plan은 transaction을 열기 전에 plan, definition과 `ProjectState` 전체를
  preflight합니다. 잘못된 key/direction, duplicate definition/step, 누락 definition,
  state transition 불가와 mixed direction을 거부합니다.
- 오류 taxonomy는 `CategoryExecution` 아래 `invalid_execution_plan`과
  `mixed_directions`입니다.
- 유효한 plan은 순서대로 기존 `Apply` 또는 `Unapply`를 호출하며 각 step이 별도
  transaction으로 commit됩니다.
- 첫 오류에서 뒤 step을 시작하지 않고 마지막 durable commit에 대응하는
  `ProjectState`와 원인을 보존한 error를 반환합니다.
- Reverse에서도 schema operation과 recorder update를 같은 transaction으로 유지합니다.
  `migrations/backend`와 SQLite backend interface는 변경하지 않습니다.
- pre-canceled context는 preflight/backend I/O 전에 실패합니다. Step 사이 취소는 다음
  transaction 전에 멈추고 앞선 commit을 보존합니다. In-flight cancellation은 rollback을
  완료하고 primary/rollback cause를 보존해야 합니다.
- 마지막 step commit 뒤에야 cancellation이 관찰되면 실행 완료를 성공으로 취급합니다.

## 호환성 결과

참조 manifest의 Django phase/comparison dimension과 Django oracle은 변경하지 않습니다.
Core comparator도 contract dimension을 완화하지 않습니다. 대신 GDJ-0012는
`godj-migration-execution-deviation-expected.json`과 fail-closed product harness를 별도로
두어 동일 동작과 승인된 차이를 구분합니다. Execution manifest는 exact 일치 여섯 개를
`passing`, 승인된 차이 네 개를 `deviation`으로 기록하고 deviation에만
`decision=DEV-0001` provenance를 둡니다.

검증된 분류는 다음과 같습니다.

| 계약 | 기대 분류 | 이유 |
|---|---|---|
| MIG-017/019/021/023/025/026 | `passing` | forward, failure-stop, mixed preflight와 empty no-op 의미가 동일 |
| MIG-018/020/022 | `deviation` | 최종 결과는 같아도 backward step의 `transaction_model`이 `schema_and_record` |
| MIG-024 | `deviation` | recorder fault 때 A2 schema까지 rollback되어 DB state와 phase가 다름 |

제품 합계는 기존 57에 6 `passing`과 4 `deviation`을 더한
`63 passing + 4 deviation`입니다. 이를 67 exact passing으로 표현하지 않습니다.

MIG-024의 GoDj product expectation은 phase `rollback`, A3 unapply commit, A2 schema와
recorder retained입니다. A2 compact step은 `status=rolled_back`,
`schema_outcome=rolled_back`, `recorder_outcome=retained`,
`transaction_model=schema_and_record`, `fault_point=before_record_write`입니다. Django
reference의 phase `commit`, schema A1/records A1·A2를 이 값으로 덮어쓰지 않습니다.

## 결과와 비용

- 기존 one-migration 원자성, backend interface와 SQLite 구현을 재사용할 수 있습니다.
- Django보다 강한 schema/recorder 원자성을 제공하지만, Django DB에서 실패 뒤 보이는
  historical state와 정확히 같지 않습니다.
- 사용자는 GoDj에서 recorder 실패 뒤 수동 schema/history reconciliation이 필요한
  MIG-024형 partial state를 보지 않게 됩니다.
- 향후 Django DB와 상호 운용하거나 recorder 장애 복구 도구를 만들 때 이 차이를 문서와
  migration 진단에 노출해야 합니다.

## 의도적으로 결정하지 않는 것

- public migration file/source encoding, loader와 CLI
- recorder read/list API와 startup applied-state reconstruction
- data migration callback ABI, non-atomic migration/operation
- fake/fake-initial, replacement/squash/merge/optimizer
- multi-process lock, crash recovery와 repair command
- PostgreSQL/MySQL/Oracle의 DDL transaction 정책

## 승인과 검증 증거

다음 조건을 GDJ-0012에서 모두 검증했습니다.

- `ExecutePlan` API와 full zero-I/O preflight의 unit/external compile test
- migration별 commit, first-failure stop과 last durable `ProjectState` 검증
- pre/between/in-flight context cancellation과 rollback cause 검증
- MIG-017..026 실제 product adapter observation
- MIG-018/020/022/024용 GoDj expected fixture가 reference oracle을 바꾸지 않는지 검증
- [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리) 승인·전용 expected 검증
- full/race/CGO=0/vet, existing 57 differential과 false-green gate 통과

제품 구현 commit은 `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`이고 전체 명령,
actual hash와 독립 감사 결과는
[EVID-20260808-011](../status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록합니다.
