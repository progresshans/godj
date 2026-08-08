# 의도적 호환 차이 원장

- 상태: Active ledger
- 마지막 갱신: 2026-08-08
- 현재 승인된 deviation: DEV-0001 한 건 / contract 네 개

이 문서는 Django reference contract와 다른 GoDj 동작을 의도적으로 수용한 경우의 정본입니다. 단순 mismatch, 미구현, bug, 환경 drift를 deviation으로 바꾸어 테스트를 녹색으로 만들면 안 됩니다.

## 승인 절차

1. Differential mismatch를 `GoDj bug`, `scenario bug`, `Django bug`, `environment drift`, `deviation candidate`로 먼저 분류합니다.
2. Candidate에는 사용자에게 보이는 차이, 대안, migration 비용, backend 영향, 보안/성능 영향과 contract ID를 기록합니다.
3. public behavior, data, error, transaction 의미를 바꾸면 Proposed ADR을 연결합니다.
4. review와 필요한 사용자 결정을 거쳐 상태를 `Accepted`로 바꿉니다.
5. manifest의 contract 상태를 `deviation`으로 바꾸고 GoDj 고유 expected test를 추가합니다.
6. 대체되면 원래 기록을 지우지 않고 `Superseded`와 새 ID를 연결합니다.

## 상태

- `Proposed`: 차이를 검토 중이며 compatibility pass로 계산하지 않음
- `Accepted`: 의도적인 제품 결정으로 승인됨
- `Implemented`: 현재 checkout에 다른 동작이 구현됨
- `Verified`: deviation 전용 expected test가 기록된 환경에서 통과함
- `Superseded`: 새 deviation 또는 원래 Django 동작으로 대체됨

## 기록 형식

```markdown
## DEV-NNNN — 제목

- Status: Proposed
- Date:
- Contracts:
- Reference profile/backend:
- Related ADR/work/evidence:

### Django의 관찰 가능 동작
### GoDj에서 제안/채택한 동작
### 이유와 고려한 대안
### 사용자·데이터·migration 영향
### backend/concurrency/security 영향
### 구현과 검증 조건
### 복귀 또는 supersede 조건
```

## 원장

## DEV-0001 — 역방향 migration의 schema와 recorder를 같은 transaction으로 처리

- Status: Verified
- Date: 2026-08-08
- Contracts: MIG-018, MIG-020, MIG-022, MIG-024
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3
- Related ADR/work/evidence:
  [ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md),
  [GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md),
  [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md),
  [EVID-20260808-010](status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts),
  [EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)

### Django의 관찰 가능 동작

Django 6.1의 backward migration은 schema transaction을 먼저 commit한 뒤 recorder row를
삭제합니다. MIG-018/020/022의 정상 또는 앞선 성공 backward step은 compact metrics에서
`transaction_model=schema_then_record`입니다. MIG-024는 A3 unapply를 완료한 뒤 A2 schema
reverse까지 commit하고, A2 recorder의 실제 write 전에 주입한 fault로 삭제가 실패합니다.
최종 schema는 A1만 남고 recorder rows는 A1/A2이며 phase는 `commit`입니다.

### GoDj에서 채택한 동작

기존 `Executor.Unapply`처럼 reverse schema operation과 recorder 삭제를 같은 transaction에서
처리합니다. Recorder 실패면 실패 migration의 schema와 recorder를 함께 rollback하고 앞선
migration commit만 보존합니다. 정상 backward 결과는 같지만 transaction model은
`schema_and_record`입니다. MIG-024형 실패에서는 A2 schema도 남고 A2 recorder도 남으므로
Django의 DB state와 phase가 달라집니다.

MIG-024의 구체적 product expectation은 phase `rollback`, A3 unapply만 durable commit,
최종 schema/records 모두 A1/A2입니다. A2 step은 `status=rolled_back`,
`schema_outcome=rolled_back`, `recorder_outcome=retained`,
`transaction_model=schema_and_record`, `fault_point=before_record_write`입니다.

### 이유와 고려한 대안

채택 이유는 schema와 applied history가 durable하게 불일치하는 실패 모드를 기본 제품
동작으로 만들지 않고, MIG-001..004에서 이미 검증한 한 migration 원자성을 유지하기
위함입니다. Django 경계를 그대로 구현하는 안, plan 전체를 한 transaction으로 묶는 안과
backend별 선택을 검토합니다. 상세 trade-off는 ADR-0014에 기록합니다.

### 사용자·데이터·migration 영향

정상 reverse 뒤 최종 schema/record는 동일합니다. 다만 recorder 장애가 발생하면 Django
운영자는 schema/history reconciliation이 필요할 수 있지만 GoDj는 실패 migration을 함께
rollback합니다. Django DB와 GoDj recorder를 직접 교환하거나 장애 복구 절차를 공유하는
경우 이 차이를 진단 출력과 문서에서 명시해야 합니다.

### backend/concurrency/security 영향

SQLite의 기존 pinned transaction과 backend interface를 유지할 수 있습니다. 다른 backend의
DDL transaction capability는 아직 검증하지 않았으므로 이 결정이 모든 backend에 자동으로
적용된다고 주장하지 않습니다. Process lock/crash recovery는 별도 Q-012 범위입니다.

### 구현과 검증 조건

- GDJ-0012의 `ExecutePlan`이 기존 same-transaction `Unapply`를 step primitive로 재사용
- MIG-018/020/022의 `transaction_model` 차이와 MIG-024의 DB state/phase 차이를 live
  product observation에서 재현
- Reference oracle/Django phase나 core comparator를 변경하지 않는 별도
  `godj-migration-execution-deviation-expected.json`
- Existing 57 differential, full/race/CGO=0/vet와 cancellation/failure gate 통과
- Manifest는 이 결정이 적용되는 네 계약만 `deviation`으로 분류하고 정확히 하나의
  `decision=DEV-0001`, `derived=false` provenance를 가짐
- Sparse product expectation의 등록되지 않은 selector/status/provenance 변경은 actual을
  쓰기 전에 exit 2로 실패

제품 구현 commit `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`에서 네 계약을
전용 expected와 live SQLite actual로 검증했습니다. GDJ-0012 완료 당시 제품 분류는
`63 passing + 4 deviation`이었으며 67 exact passing으로 표현하지 않았습니다. 이후
recorder-restart 제품 10개와 historical-state reconstruction 제품 10개가 추가된 현재 분류는
`83 passing + 4 deviation`이고, DEV-0001 네 계약은 그대로 유지됩니다. 검증 명령과 artifact hash는
[EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록하며 현재 aggregate는
[EVID-20260808-015](status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.

### 복귀 또는 supersede 조건

Django와의 exact backward transaction 호환이 schema/history 원자성보다 우선이라는 근거가
생기거나, backend별 recovery protocol이 partial commit을 안전하게 복구하면 새 ADR로 이
결정을 Rejected/Superseded할 수 있습니다.
