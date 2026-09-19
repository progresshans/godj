---
id: GDJ-0084
status: active
updated: 2026-09-19
baseline_commit: "9b3e8b53f8ef7cf6b5d60990b76c20c459309243"
integration_owner: "root"
---

# Historical relation graph와 순환 migration

## 목표

자기참조·상호참조 모델을 수작업 SQL 없이 historical definition과 revision-fenced migration으로 생성·변경·되돌린다.
GDJ-0082/0083의 query 소비자에서 사용한 FK-enforced DDL을 실제 migration 경로로 대체할 기반이다.
완성 목표는 헌장·기능 카탈로그 전체이며, 이 작업의 완료는 아래 migration 범위만 뜻한다.

## 구현 경계

- 한 operation의 relation-bearing source와 도달 가능한 historical target을 app/model identity로 한 번씩 보관한다.
  유한 graph를 반복 탐색하며 self/cycle을 표현한다. 누락·충돌·도달 불가능한 snapshot, 잘못된 target key와 reverse 이름을
  거부하고 전체 graph의 resource를 복제·확장 전에 제한한다. Backend와 SQL renderer는 같은 metadata authority를 사용한다.
- Migration dependency graph는 계속 DAG이며 target creator의 실제 선행성을 검사한다. 자기 자신은 같은 CreateModel에서
  visible하다. 서로 다른 모델의 forward reference를 임의로 허용하지 않으며 Create/Add 순서로 이미 존재하는 target을 참조한다.
- Before/After와 실제 physical schema, 전체 FK graph·revision·recorder·durable prefix를 함께 검증한다.
  하나의 source에 여러 relation 추가, nested target과 cross-app back edge를 허용할 만큼 authority를 확장한다.
- SQLite remake는 실제 참조 값·행·sequence를 보존해야 한다. `defer_foreign_keys`만으로는 충분하지 않다.
  Pinned connection의 설정·transaction·최종 foreign_key_check·commit/rollback·설정 복원/connection 폐기를 하나의 owner가
  책임지도록 구현한다. PostgreSQL도 실제 constraint와 관련 table lock 아래 동일한 역사 경계를 검증한다.
- Loaded definition → reconstruct → plan/SQL → 실제 양 DB apply/unapply/reopen와 generated 소비자를 연결한다.
  기존 fork/stale/process·취소·fault·물리 drift 음성 검증을 유지하며 새 self/cycle 실패 경로를 추가한다.
- 새 모델의 순환 관계를 자동으로 나누는 일반 autodetector는 field/model 순서 의미까지 별도 설계가 필요하다.
  이번에는 표현 가능한 역사 순서의 lifecycle을 먼저 완성하며 자동 계획의 남은 제약을 숨기지 않는다.

## 현재와 다음

GDJ-0083 Hosted ORM 검증을 완료했다. 별도 작업 사본에서 제약과 SQLite 물리 동작을 조사하고 공통 graph authority 구현을 시작했다.
현재 제품은 self Create/Add와 관계 cycle을 constructor에서 거부하며 Add/Remove intent는 한 개의 scalar-only target만 허용한다.
Graph authority와 backend 검증을 함께 정리한 뒤 독립 관찰·통합 검증을 수행한다. 아직 새 기능의 runtime PASS 주장은 없다.

설계 근거: SQLite [FK와 DROP 동작](https://www.sqlite.org/foreignkeys.html#fk_schemacommands),
[table 재구성 절차](https://www.sqlite.org/lang_altertable.html#otheralter),
[defer_foreign_keys](https://www.sqlite.org/pragma.html#pragma_defer_foreign_keys).
실제 실행 결과는 통합 checkpoint 이후 TEST_EVIDENCE에 기록한다.
