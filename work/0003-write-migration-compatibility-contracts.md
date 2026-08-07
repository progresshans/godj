---
id: GDJ-0003
status: ready
updated: 2026-08-08
baseline_branch: ""
baseline_commit: ""
depends_on: ["GDJ-0002", "ADR-0005"]
contracts: ["WRT-001..TBD", "MIG-001..TBD"]
allowed_paths: ["Makefile", "pyproject.toml", "uv.lock", "conformance/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Write lifecycle와 Migration 호환 계약 확장

## 사용자에게 보이는 결과

Django 6.1에서 model insert/update/delete, transaction rollback과 최소 schema migration의
외부 동작을 exact profile로 관찰하고, 다음 제품 수직 단면이 false green 없이 구현할
수 있는 두 번째 locked contract set을 얻습니다.

## 목표 흐름

```text
M2 질문과 위험 목록
→ 독립 Django write/migration scenarios
→ typed normalized observations
→ 별도 ordered manifest/oracle
→ mutation-based comparator gate
→ product 구현용 정확한 red baseline
```

## 제한 범위

- 한 `Article` model의 explicit value insert와 auto primary key
- nullable `NULL`, empty string, omitted/default의 write 의미
- 한 row의 제한된 update와 delete
- transaction commit/rollback과 실패 후 DB state
- `CreateModel`, nullable `AddField`, reversible operation의 schema/project state
- SQLite exact reference profile
- 8~12개의 작고 독립적인 새 계약

## 비목표

- GoDj product write/migration code 구현
- 모든 `save()` option 또는 bulk operation
- relation/cascade
- data migration callback ABI 전체
- migration merge/squash/optimizer
- PostgreSQL DDL
- 기존 11개 M1 manifest에 계약을 억지로 추가하는 것

## 시작 전 결정 spike

1. 기존 profile을 공유하는 두 번째 manifest/oracle을 protocol/CLI가 명확히 다루는지
2. Q-006의 omitted/explicit NULL/update-field 의미를 어떤 observable로 나눌지
3. Q-012 migration state, operation, recorder, transaction 의미 중 첫 implementation에
   반드시 필요한 최소 계약
4. Django migration executor 공개 동작을 관찰하되 Python 내부 object graph를 복제하지
   않는 scenario 경계
5. 각 scenario의 database state와 schema state normalization 형식

## 구현 순서

1. 현재 Django 6.1 reference에서 후보 동작을 disposable SQLite DB로 조사합니다.
2. 계약 ID, phase, 비교 차원, 독립 fixture와 provenance를 먼저 작성합니다.
3. 두 번째 manifest를 기존 profile에 연결하고 8~12개 bound를 지킵니다.
4. Django adapter와 normalizer를 작성해 byte-deterministic oracle을 생성합니다.
5. result/error/DB state/schema state/transaction mutation이 comparator에서 실패하는지
   확인합니다.
6. GoDj actual은 명시적 `not_implemented` red baseline으로 추가합니다.
7. 다음 product work item의 exact scope와 완료 gate를 oracle 결과로 작성합니다.

## 완료 gate

- [ ] 8~12개 WRT/MIG contract와 provenance가 strict validation 통과
- [ ] exact Django profile에서 deterministic oracle 생성·byte check 통과
- [ ] portable test가 exact 재현을 거짓 주장하지 않음
- [ ] write/schema/transaction mutation이 false green 없이 mismatch
- [ ] GoDj 미구현 actual이 모든 새 계약에서 명시적으로 red/not_implemented
- [ ] Django-derived material 분류와 license review 완료
- [ ] Q-006/Q-012에서 product 구현 전에 필요한 결정을 Proposed ADR 또는 좁은 질문으로 기록
- [ ] 다음 product vertical-slice work item을 `ready`로 작성

## 재개 시 첫 작업

GDJ-0002 commit을 baseline으로 기록한 뒤, 후보 8~12개를 바로 manifest에 고정하지 말고
Django runtime probe로 결과·오류·transaction/schema state가 안정적으로 관찰되는지
먼저 확인합니다. 기존 M1 manifest의 11개와 두 번째 contract set의 상태를 섞지
않습니다.
