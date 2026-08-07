---
id: GDJ-0005
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "de099f31738c1df0dcc4c6ffd609d0fb4f0d4683"
depends_on: ["GDJ-0004"]
contracts: ["MOD-008..MOD-019"]
allowed_paths: ["Makefile", "NOTICE.md", ".github/workflows/ci.yml", "conformance/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Save Lifecycle Compatibility Contracts

## 사용자에게 보이는 결과

Django의 mutable model instance `save()` 동작을 추측으로 Go API에 옮기기 전에,
new/loaded instance, `update_fields`, force flag, explicit PK와 transaction rollback의
관찰 가능한 의미를 exact profile에서 12개 작은 계약으로 고정했습니다.

완료 시 이 contract set은 byte-deterministic Django oracle과 explicit GoDj
`not_implemented` baseline을 가지며, 후속 제품 work가 instance `Save()`를 도입할지
기존 generated Manager API로 의미를 매핑할지 증거를 바탕으로 결정할 수 있어야 합니다.

## 고정된 계약

| ID | 잠글 동작 |
|---|---|
| MOD-008 | 새 instance save가 INSERT하고 auto PK를 채움 |
| MOD-009 | 저장된 instance의 기본 save가 concrete field를 갱신하는 범위 |
| MOD-010 | `update_fields`가 지정 field만 쓰고 나머지 DB 값을 보존 |
| MOD-011 | 빈 `update_fields`의 no-op과 query count |
| MOD-012 | primary key를 `update_fields`에 넣었을 때의 zero-I/O 오류 |
| MOD-013 | 기존 key에 대한 `force_insert` 충돌과 행 보존 |
| MOD-014 | PK 없는 `force_update`의 zero-I/O 오류 |
| MOD-015 | 존재하지 않는 행의 `force_update`와 `NotUpdated` |
| MOD-016 | `force_insert`와 `force_update` 동시 지정 오류 |
| MOD-017 | 기존 explicit PK의 UPDATE 분기 |
| MOD-018 | 없는 explicit PK의 UPDATE 후 INSERT 분기 |
| MOD-019 | atomic rollback 뒤 instance field/PK와 DB state의 차이 |

Probe에서 서로 다른 오류와 UPDATE/INSERT 분기를 한 observation에 묶으면 protocol의
`result`/`error` 상호배타성과 오류 의미가 약해짐을 확인해 후보 10개를 위 12개로
재배치했습니다. 이 ordered set은
[`save-lifecycle-manifest.json`](../conformance/contracts/save-lifecycle-manifest.json)에
고정됐습니다.

## 목표 흐름

```text
Django 6.1 exact save scenarios
→ result / error / db_state / query metrics normalization
→ separate ordered manifest + deterministic oracle
→ explicit GoDj not_implemented suite
→ future generated API / generic core design input
```

## 제한 범위

- 기존 exact Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 profile
- 현재 Article field subset과 save 관련 공개 동작
- transaction rollback의 instance/DB state 차이
- protocol v2의 독립 third contract set
- upstream provenance와 라이선스 분류

## 비목표

- GoDj instance Save 제품 구현
- dirty map/wrapper API 확정
- hook/signal, bulk write, relation/cascade
- migration file/graph/lock 또는 PostgreSQL
- 기존 두 manifest/oracle 행동 의미 변경

## 완료 gate

- [x] exact profile runtime probe가 후보별 안정 payload를 두 번 동일하게 생성
- [x] 12개 contract ID, phase, payload와 provenance가 manifest에 고정
- [x] Django oracle이 두 번 byte-identical하고 checksum이 기록됨
- [x] explicit GoDj `not_implemented`가 정확히 12개 mismatch
- [x] 세 contract set의 cross-set/profile/order/phase 결속이 false green을 거부
- [x] portable/exact Python, protocol Go test와 full/race/CGO=0 gate 통과
- [x] CURRENT/matrix/evidence/work가 같은 checkout과 상태를 가리킴

## 시작 시 첫 작업

1. Django 6.1 public model save 문서와 관련 upstream test 경로를 확인합니다.
2. 위 10개 후보를 disposable SQLite에서 독립 실행해 result/error/DB/query state를 두 번
   canonicalize합니다.
3. Django가 dirty tracking을 한다는 가정을 두지 말고 실제 default save와
   `update_fields` 차이를 먼저 기록합니다.
4. Probe 결과로 contract 경계를 줄인 뒤 세 번째 manifest/runner/oracle을 추가합니다.

## 알려진 위험

- Django instance object는 rollback 시 메모리 값을 자동 복원하지 않을 수 있으므로 DB와
  object state를 함께 관찰해야 합니다.
- `save()`는 PK 존재 여부와 row existence에 따라 UPDATE/INSERT 경로가 달라질 수 있어
  query count를 정렬하거나 숨기면 안 됩니다.
- GoDj의 현재 generated immutable patch API를 Django mutable instance 구조와 동일하게
  만들 필요는 없지만, 관찰 가능한 차이는 후속 ADR/deviation 없이 조용히 바꾸지 않습니다.

## 실행 결과와 결정

- Exact Django 6.1/CPython 3.14.3/SQLite 3.50.4 profile에서 12개를 서로 독립적인
  disposable table로 실행했습니다.
- Fully loaded default save는 dirty field만 쓰지 않고 writable concrete non-PK field를
  모두 UPDATE합니다. Concurrent DB 값을 loaded instance의 오래된 값으로 되돌리는
  scenario로 dirty-only false green을 막았습니다.
- 명시적인 `update_fields`는 named field만 저장하며 omitted in-memory mutation은 object에
  남습니다. Empty `update_fields`는 object 값을 되돌리지 않고 SQL 0개로 끝납니다.
- `force_update`의 PK 없음과 missing row는 각각 SQL 0개의 `ValueError`, UPDATE 1개 뒤
  `Model.NotUpdated`이므로 분리했습니다.
- Explicit PK가 존재하면 UPDATE 1개, 없으면 UPDATE→INSERT 2개입니다. SQL 문자열은
  계약하지 않고 DML kind와 count만 비교합니다.
- Transaction rollback은 DB 값을 되돌리지만 persisted object의 변경값과 새 object에
  할당된 auto PK를 복원하지 않습니다. Django의 private `_state`는 payload에 넣지
  않았습니다.
- Phase는 `commit`을 persistent write 성공, `evaluation`을 no-op/validation 또는
  explicit transaction 밖의 실패, `rollback`을 명시적 transaction 복원으로 사용합니다.
- Scenario와 fixture는 upstream 코드를 복사·번역하지 않은 독립 동작 시나리오이며 각
  manifest provenance는 pinned tag의 실제 문서 heading/test symbol로 검증했습니다.

## 산출물과 검증

- Contract commit: `138581da38bfbb6ba89ea5ca82752dfd3d76df02`
- Oracle SHA-256:
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`
- Django runner: `conformance/runners/django/save_lifecycle_scenarios.py`
- Manifest/oracle/static baseline: `conformance/contracts/save-lifecycle-manifest.json`,
  `conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json`,
  `conformance/fixtures/godj-save-lifecycle-not-implemented.json`
- False-green gate는 3개 set의 전역 ID/scenario uniqueness, 6개 ordered cross-binding,
  phase/profile/order와 9개 Save payload mutation을 검사합니다.
- 최종 명령과 환경은
  [EVID-20260808-004](../docs/status/TEST_EVIDENCE.md#evid-20260808-004--gdj-0005-save-lifecycle-compatibility-contracts)에
  기록했습니다.

## 남은 제한과 다음 작업

- 이 작업은 Django reference 계약을 고정했으며 GoDj `Save` 제품 API를 구현하지
  않았습니다. 따라서 12개 상태는 `oracle_locked`이고 static actual은 의도한
  `not_implemented` mismatch입니다.
- Deferred field, signal/hook, inheritance, relation, custom PK와 bulk write는 범위 밖입니다.
- 다음 작업은 [GDJ-0006](0006-save-lifecycle-product-slice.md)에서 typed field mask,
  force mode와 explicit PK 경계를 먼저 compile/runtime spike한 뒤 generic Manager와
  SQLite adapter로 12개를 실제 통과시키는 것입니다.
