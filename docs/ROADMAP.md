# GoDj 로드맵

- 상태: Accepted direction
- 현재 단계: M1 완료, M2 contract 확장 준비
- 마지막 검토: 2026-08-08

로드맵은 계층별 골격을 오래 만든 뒤 마지막에 연결하는 방식이 아니라, **호환 계약을 통과하는 수직 단면**을 넓혀 갑니다.

## 공통 완료 gate

각 milestone은 다음을 충족해야 완료됩니다.

- M0는 범위 내 reference contract가 모두 `oracle_locked`, M1 이후는 대상 contract가
  모두 `passing` 또는 승인된 `deviation`
- 미지원 기능을 조용히 무시하는 경로가 없음
- external consumer 관점의 compile test가 통과함
- 오류와 실패/rollback 경로가 검증됨
- 실제 명령과 checkout이 test evidence에 기록됨
- CURRENT, work 문서, implementation matrix가 같은 상태를 가리킴
- 새 장기 결정은 Accepted ADR 또는 명시적 Proposed 상태를 가짐

## M0 — Compatibility Lab

목표: 구현 전에 기준 행동과 비교 도구가 거짓 양성 없이 작동함을 증명합니다.

- Django 6.1, Python, SQLite, timezone, locale exact profile lock
- 8~12개 초기 contract와 upstream provenance
- Django scenario runner와 deterministic oracle
- normalizer와 comparator unit/property tests
- GoDj 미구현 상태를 명시적으로 표현하는 runner/protocol
- codegen bootstrap 실패 사례를 재현하는 작은 architecture spike
- CI에서 manifest validation과 Django reference suite 실행

상태: GDJ-0001에서 exact darwin/arm64 oracle과 portable CI validation을 구현하고
로컬 gate를 통과했습니다. 일반 hosted CI는 다른 platform에서 exact oracle을
재생성했다고 주장하지 않습니다.

상세 범위: [GDJ-0001](../work/0001-compatibility-lab.md)

## M1 — Model-to-Query Walking Skeleton

목표: 한 모델의 의미가 선언부터 실제 SQLite 결과까지 흐릅니다.

```text
Schema DSL → normalized IR → deterministic codegen
→ Manager[Article] / QuerySet[Article]
→ typed + dynamic lookup → same AST
→ SQLite compiler/executor → differential result
```

범위는 `AutoField`, `CharField`, `BooleanField`, 필요한 최소 nullable field, exact/ASCII icontains/AND/order/limit/isnull로 제한합니다. migration engine 대신 test-only schema provisioner를 허용할 수 있습니다. public API는 이 단면의 compile usability와 contract 통과 후 확정합니다.

상태: GDJ-0002에서 이 범위의 Schema-to-SQLite 수직 단면과 11개 Django differential
계약을 통과했습니다. 범위 밖 ORM 기능이나 Django 전체 호환을 뜻하지 않습니다.

## M2 — Write Lifecycle + Migration

- create/insert, loaded/new/dirty state, save/update/delete
- transaction과 context cancellation
- project/model/historical state
- CreateModel, Add/Alter/RemoveField
- migration recorder, graph, lock, forward/backward, failure rollback

첫 단계는 [GDJ-0003](../work/0003-write-migration-compatibility-contracts.md)에서
write/schema/transaction reference 계약을 별도 contract set으로 잠그는 것입니다.
제품 구현은 이 결과로 범위를 좁힌 다음 work item에서 시작합니다.

## M3 — Relations + PostgreSQL

- ForeignKey, OneToOne, reverse relation
- cascade와 database-level delete 선택
- `select_related`, `prefetch_related`
- 앱 간 관계/import 전략 검증
- SQLite와 PostgreSQL conformance

## M4 — QuerySet Breadth

- Q/F expression, aggregate, annotation
- projection, subquery, window function
- bulk operation, locking, custom lookup/field extension
- result cache와 iterator semantics 확정

## M5 — Web Core

- settings, app registry, system check
- routing/reverse, middleware, request/response, error handling
- view와 template 한 요청 수직 단면
- development server와 management command

## M6 — Forms, Auth, Admin

- common validation core와 Form/Serializer 경계
- Form, ModelForm, CSRF, session, auth, permission
- 한 모델의 Admin list/search/edit/history/action 수직 단면
- static/messages와 접근성·보안 gate

## M7 — API

- API reference profile 확정
- serializer, parser/renderer, authentication/permission
- APIView/ViewSet/Router, pagination/filter/order
- OpenAPI와 browsable API

## M8 — Realtime

- Realtime reference profile 확정
- WebSocket/SSE consumer와 protocol router
- auth/session middleware, group, channel layer
- in-memory와 Redis backend, backpressure/lifecycle

## M9 — Backend Expansion

- MySQL, MariaDB, Oracle
- multi-DB와 database router
- capability-driven conformance와 explicit unsupported paths

## M10 — Advanced + 1.0

- GIS, i18n, FormSet, advanced Admin, contrib
- security audit, performance baseline, migration stability
- compatibility matrix와 Django DB migration tools
- generated code/schema/migration upgrade policy
- API freeze, tutorial, release engineering

## 작업 분할 원칙

- 한 work item은 사용자에게 보이는 하나의 결과와 실행 가능한 완료 조건을 가집니다.
- 한 단계에서 모든 Field/API를 만들지 않고 다음 수직 단면에 필요한 최소 폭만 구현합니다.
- 조사 spike와 production implementation을 구분합니다.
- 관계 없는 package나 같은 공개 API를 병렬 에이전트에 나누지 않습니다.
- 긴 milestone은 contract group별 work item으로 쪼개되 milestone gate는 하나의 통합 담당자가 닫습니다.
