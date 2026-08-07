---
id: GDJ-0003
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "c03e8e350d12a548e2f8c8a51f19972354240d04"
depends_on: ["GDJ-0002", "ADR-0005"]
contracts: ["MOD-001..MOD-007", "MIG-001..MIG-004"]
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
→ product 구현용 locked oracle + explicit not-implemented baseline
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
6. GoDj actual은 명시적 `not_implemented` non-passing baseline으로 추가합니다.
7. 다음 product work item의 exact scope와 완료 gate를 oracle 결과로 작성합니다.

## 완료 gate

- [x] MOD 7개와 MIG 4개 contract/provenance가 strict validation 통과
- [x] exact Django profile에서 두 oracle의 deterministic generation·byte check 통과
- [x] portable test가 exact 재현을 거짓 주장하지 않고 exact-only 3개를 명시적으로 skip
- [x] write/schema/transaction/phase mutation과 실제 cross-set 교환이 false green 없이 실패
- [x] GoDj 미구현 actual이 11개 계약에서 명시적 `not_implemented` mismatch
- [x] Django-derived material 분류와 license review 완료
- [x] Q-006/Q-012의 다음 결정을 Proposed ADR-0009/0010으로 기록
- [x] 다음 product vertical-slice [GDJ-0004](0004-write-migration-walking-skeleton.md)를 `ready`로 작성

## 완료 결과

- `MOD-001..007`은 create, partial update, delete, transaction commit/rollback을
  결과와 DB state로 고정합니다. MOD-007은 기존 행의 변경과 신규 행 삽입이 모두
  복원되는지 관찰해 단순 보상 삭제 false green을 막습니다.
- `MIG-001..004`는 CreateModel, nullable AddField, reverse, recorder와 atomic failure 뒤
  schema inventory/connection recovery를 고정합니다.
- protocol v2가 manifest의 expected phase를 profile·ordered ID·payload dimension과 함께
  suite에 결속합니다. M1 행동 계약은 유지되며 v1 artifact는 명시적으로 거부됩니다.
- Django runner는 외부에서 구성된 settings/database를 fail closed하고, manifest별
  default oracle을 안전하게 선택합니다.
- M2 제품 adapter는 아직 없으므로 11개 계약은 `oracle_locked`입니다. Static
  `not_implemented` fixture의 11개 mismatch는 `red`가 아니라 false-green 방지 증거입니다.

## 변경 파일 범위

- `conformance/contracts`, `profiles`, `oracles`, `fixtures`: 두 번째 ordered set과 protocol v2 artifact
- `conformance/runners/django`: 독립 write/migration fixture, 격리된 runner와 safety regression
- `conformance/internal/protocol`, `conformance/cmd`: phase/set/payload/checksum binding과 mutation gate
- `docs/adr/0009-*`, `0010-*`: write change state와 migration executor 경계 제안
- `Makefile`, compatibility/testing/licensing 문서와 `NOTICE.md`: 재생·CI·provenance 정책

## 결정

- contract namespace는 `MOD-001..007`, `MIG-001..004`입니다.
- create의 omitted와 explicit NULL은 현재 nullable/no-default fixture에서 SQL NULL로
  수렴하지만 empty string은 구분됩니다. Update의 unchanged와 explicit NULL은 서로 다른
  동작이므로 GDJ-0004에서 tri-state API를 compile spike합니다.
- 성공한 migration reverse는 transaction rollback phase가 아니라 `commit` phase입니다.
- migration failure operation 실행 증거는 runner 내부 assertion으로만 검증하고 GoDj가
  Django fixture 전용 sentinel을 출력하도록 계약하지 않습니다.
- public migration file/CLI, data callback ABI와 lock 정책은 이번 범위에서 확정하지 않습니다.

## 검증 증거

- 계약/runner 구현 commit: `62c73c4cea161d970a95066b281d042a2583753d`
- Scenario cleanup baseline 회귀 commit: `3e7c87839265e1b07b6d69f59f52e596623b1eb5`
- Evidence: [EVID-20260808-002](../docs/status/TEST_EVIDENCE.md#evid-20260808-002--gdj-0003-write-migration-compatibility-contracts)
- M1 v2 oracle SHA-256: `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`
- M2 oracle SHA-256: `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`
- `make check`, 전체 `CGO_ENABLED=0 go test ./...`, checksum, exact regeneration과
  expected 11-mismatch baseline을 해당 commit에서 확인했습니다.

## 알려진 제한과 다음 작업

- GoDj write/migration 제품 코드는 아직 구현되지 않았고 M2 계약은 `passing`이 아닙니다.
- ADR-0009/0010은 compile/runtime spike 전 `Proposed` 상태입니다.
- 다음 작업은 GDJ-0004를 clean checkout에서 활성화하고 두 write API 후보와 migration
  package dependency/transaction 경계를 먼저 검증하는 것입니다.

## 시작 기록 (historical)

- 시작 checkout: `main@c03e8e350d12a548e2f8c8a51f19972354240d04`
- 시작 worktree: clean
- 외부 blocker: 없음
- 병렬 조사 범위: write lifecycle probe, migration/schema probe, 두 번째 contract set을
  위한 protocol 경로 일반화 검증
