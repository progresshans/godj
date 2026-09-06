# 현재 구현 범위

코드가 존재하는 범위와 검증 환경을 구분한다. 아래 기능은 모두 bounded subset이며 Django 전체 구현률을 뜻하지 않는다.
현재 개발 정리 작업의 변경은 [CURRENT](CURRENT.md), 실제 명령·source·환경별 결과는 [TEST_EVIDENCE](TEST_EVIDENCE.md)가 소유한다.

## 기능

| 영역 | 구현된 범위 | 주요 제한·다음 경계 | 코드 |
|---|---|---|---|
| Schema/IR | normalized schema, current scalar/FK 의미, immutable snapshot | 모든 Django field·custom field 아님 | [schema](../../schema/) |
| 생성 | ProjectSpec, typed model/FieldSet/descriptor와 project relation binding, 후보 compile/publication/recovery | Linux/macOS local filesystem 중심 | [codegen](../../codegen/) |
| Query | typed/dynamic 공통 AST, Boolean composition, scalar comparison, same-model F, projection·Count/Max | annotation/grouping/subquery/window/bulk/locking은 별도 | [orm](../../orm/), [query](../../query/) |
| 평가 | lazy query, full-result cache, cloning, iterator와 cancellation | 임의 model/callback의 goroutine 안전성을 포함하지 않음 | [ORM cache](../CONCURRENCY.md#queryset-평가) |
| 관계 | AutoField-target FK, 자기·상호 참조 선언/생성, lazy/forward/reverse, prefetch/eager, assignment/cache, PROTECT/SET_NULL | eager First/Count·임의 깊이/다중 관계 탐색·일반 순환 관계 동작·ManyToMany/OneToOne 미지원 | [관계 소유권](../CONCURRENCY.md#관계-객체) |
| Migration | strict current definition, historical replay, immutable planner, revision fence, durable prefix, bounded reverse | arbitrary custom/data operation·general schema repair·fake/squash 미지원 | [migrations](../../migrations/) |
| Migration CLI | project check, migrate/plan/target, showmigrations, bounded makemigrations, forward sqlmigrate | writer는 지원하는 difference만 작성 | [project](../../project/), [CLI](../../cmd/godj/) |
| SQLite/PostgreSQL | current AST/CRUD/migration/system-state paths | 각 backend capability와 물리 schema precondition 적용 | [Backend Matrix](../BACKEND_MATRIX.md) |
| SQLite quarantine | retained handle 최대 1, 이후 새 I/O 거부, explicit Close | GDJ-0057 통합 source의 backend/platform/mode 검증 완료 | [lifecycle](../../db/sqlite/relation_transaction.go) |
| Web | request context, routing/reverse, middleware, bounded HTTP errors, server lifecycle | arbitrary converters/realtime/production deployment toolkit 미지원 | [web](../../web/) |
| Template/Form | closed safe template value, escaping, validation, model field allowlist projection | 선택한 scalar 입력만 편집; arbitrary callable/attribute 실행 없음 | [templates](../../templates/), [forms](../../forms/) |
| Admin | registry, permission, CRUD/history/action composition, read-only 등록 | 선택한 model field만 편집; 모든 relation UI의 일반화를 뜻하지 않음 | [admin](../../admin/) |
| JSON API | model-derived allowlist serializer, bounded parser, PUT/PATCH, pagination/filter, authentication profile | OpenAPI/browsable API/일반 viewset 자동화 미지원 | [api](../../api/), [serializers](../../serializers/) |
| Auth/session | password hashing, Session/CSRF, injected strict Bearer verifier, rotation/logout | token issuer/JWT/OAuth/OIDC/password reset·multi-user lifecycle 별도 | [auth](../../auth/), [sessions](../../sessions/) |
| Durable system state | explicit provision/open, permission CAS와 session 폐기, cooperative application transaction | 비협력 writer·자동 policy/key 전파 미지원 | [systemstate](../../systemstate/) |
| 개발 예제 | Article 및 Category–Ticket Helpdesk의 Form/Admin/API·관계·기존 DB 권한 갱신 | SQLite와 PostgreSQL normal/race/CGO-disabled 검증 완료; 별도 Helpdesk Go module 설치 검증은 아님 | [examples](../../examples/) |

## 계약과 증거

Machine contract/provenance/status는 [conformance/contracts](../../conformance/contracts/)가 소유한다.
Reference-only MIG-075..086은 미등록 진단 reference이며 제품 passing으로 세지 않는다.
Django와 다른 결과는 [DEVIATIONS](../DEVIATIONS.md)에 제한된 차이로 기록한다.

현재 전체 통합 검증 source는 GDJ-0057의 `0b8235c`다. 과거 결과와 합쳐 PASS로 표시하지 않으며,
Quick feedback, 관련 backend 검증과 전체 platform 검증은 각각 자신의 범위만 증명한다.

Realtime, MySQL/MariaDB/Oracle, GIS/i18n/contrib와 넓은 ORM/Form/Admin/API는 [장기 범위](../CAPABILITY_CATALOG.md)이며
현재 지원 항목이 아니다.
