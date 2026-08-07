---
id: GDJ-0005
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "de099f31738c1df0dcc4c6ffd609d0fb4f0d4683"
depends_on: ["GDJ-0004"]
contracts: ["MOD-008..MOD-017"]
allowed_paths: ["Makefile", "NOTICE.md", ".github/workflows/ci.yml", "conformance/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Save Lifecycle Compatibility Contracts

## 사용자에게 보이는 결과

Django의 mutable model instance `save()` 동작을 추측으로 Go API에 옮기기 전에,
new/loaded instance, `update_fields`, force flag, explicit PK와 transaction rollback의
관찰 가능한 의미를 exact profile에서 10개 작은 계약으로 고정합니다.

완료 시 이 contract set은 byte-deterministic Django oracle과 explicit GoDj
`not_implemented` baseline을 가지며, 후속 제품 work가 instance `Save()`를 도입할지
기존 generated Manager API로 의미를 매핑할지 증거를 바탕으로 결정할 수 있어야 합니다.

## 계약 후보

| ID | 잠글 동작 |
|---|---|
| MOD-008 | 새 instance save가 INSERT하고 auto PK를 채움 |
| MOD-009 | 저장된 instance의 기본 save가 concrete field를 갱신하는 범위 |
| MOD-010 | `update_fields`가 지정 field만 쓰고 나머지 DB 값을 보존 |
| MOD-011 | 빈 `update_fields`의 no-op과 query count |
| MOD-012 | 존재하지 않거나 비-concrete field의 `update_fields` 오류 시점 |
| MOD-013 | `force_insert`의 새 행 성공과 기존 key 충돌 의미 |
| MOD-014 | `force_update`의 PK 없음/행 없음 실패 의미 |
| MOD-015 | `force_insert`와 `force_update` 동시 지정 오류 |
| MOD-016 | explicit PK가 있는 instance의 update-then-insert 분기 |
| MOD-017 | atomic rollback 뒤 instance PK와 DB state의 차이 |

ID와 payload dimension은 probe 결과가 이 동작을 독립적으로 구분함을 확인한 뒤
manifest에 고정합니다. 한 계약이 여러 의미를 숨기면 ID를 재배치하되 총 8~12개 bound를
지킵니다.

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

- [ ] exact profile runtime probe가 후보별 안정 payload를 두 번 동일하게 생성
- [ ] 8~12개 contract ID, phase, payload와 provenance가 manifest에 고정
- [ ] Django oracle이 두 번 byte-identical하고 checksum이 기록됨
- [ ] explicit GoDj `not_implemented`가 정확히 contract 수만큼 mismatch
- [ ] 세 contract set의 cross-set/profile/order/phase 결속이 false green을 거부
- [ ] portable/exact Python, protocol Go test와 full/race/CGO=0 gate 통과
- [ ] CURRENT/matrix/evidence/work가 같은 checkout과 상태를 가리킴

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
