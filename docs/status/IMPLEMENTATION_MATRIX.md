# 현재 구현 범위

코드가 존재하는 범위와 검증 환경을 구분한다. 아래 기능은 모두 bounded subset이며 Django 전체 구현률을 뜻하지 않는다.
현재 작업 상태는 [CURRENT](CURRENT.md), 실제 명령·source·환경별 결과는 [TEST_EVIDENCE](TEST_EVIDENCE.md)가 소유한다.

## 기능

| 영역 | 구현된 범위 | 주요 제한·다음 경계 | 코드 |
|---|---|---|---|
| Schema/IR | Auto·signed int64 Integer(nullable/default)·Char·Text·DateTime·Boolean·FK, string/int64 choices, normalized immutable snapshot | 넓은 field/constraint·custom field 미지원 | [schema](../../schema/), [정수 의미](../adr/0059-signed-integer-field-and-model-growth.md), [Text 의미](../adr/0060-text-field-and-form-widget-semantics.md), [DateTime 의미](../adr/0061-datetime-field-and-canonical-instant-values.md) |
| 생성 | ProjectSpec, typed model/FieldSet/descriptor와 project relation binding, 후보 compile/publication/recovery | Linux/macOS local filesystem 중심 | [codegen](../../codegen/) |
| Query | typed/dynamic 공통 AST, Boolean composition, scalar comparison·IN, 검증 뒤 빈 조회 SQL 생략, same-model F, projection·Count/Min/Max·관계 filter Count | reverse/multi-hop relation·tuple/subquery IN·annotation/grouping/window/bulk/locking은 별도 | [orm](../../orm/), [query](../../query/), [IN 의미](../adr/0062-scalar-membership-and-empty-query-execution.md) |
| 평가 | lazy query, full-result cache, cloning, iterator와 cancellation | 임의 model/callback의 goroutine 안전성을 포함하지 않음 | [ORM cache](../CONCURRENCY.md#queryset-평가) |
| 관계 | AutoField-target FK, 자기·상호 참조 선언/생성, lazy/forward/reverse, nullable/required forward의 scalar lookup과 AND/OR/NOT, prefetch/eager All·First·Count, assignment/cache, PROTECT/SET_NULL | eager First는 명시적 정렬 필요; reverse non-exact/OR/NOT·relation F·임의 깊이/다중 관계 탐색·일반 순환 관계 동작·ManyToMany/OneToOne 미지원 | [관계 소유권](../CONCURRENCY.md#관계-객체) |
| Migration | strict current definition, historical replay, immutable planner, revision fence, durable prefix, choices-only AlterField와 bounded reverse | arbitrary custom/data operation·general schema repair·fake/squash 미지원 | [migrations](../../migrations/) |
| Migration CLI | project check, migrate/plan/target, showmigrations, bounded makemigrations, forward sqlmigrate | writer는 지원하는 difference만 작성 | [project](../../project/), [CLI](../../cmd/godj/) |
| SQLite/PostgreSQL | current AST/CRUD/migration/system-state paths | 각 backend capability와 물리 schema precondition 적용 | [Backend Matrix](../BACKEND_MATRIX.md) |
| SQLite quarantine | retained handle 최대 1, 이후 새 I/O 거부, explicit Close | GDJ-0057 통합 source의 backend/platform/mode 검증 완료 | [lifecycle](../../db/sqlite/relation_transaction.go) |
| Web | request context, routing/reverse, middleware, bounded HTTP errors, server lifecycle | arbitrary converters/realtime/production deployment toolkit 미지원 | [web](../../web/) |
| Template/Form | closed safe template value, escaping, validation, model field allowlist projection, Select choices·Textarea·UTC DateTimeInput과 명시적 빈 입력 정책 | 선택한 scalar 입력만 편집; arbitrary callable/attribute 실행 없음 | [templates](../../templates/), [forms](../../forms/) |
| Admin | registry, permission, CRUD/history/action composition, read-only 등록, choices 표시명과 기존 값 보존 | 선택한 model field만 편집; 모든 relation UI의 일반화를 뜻하지 않음 | [admin](../../admin/) |
| JSON API | model-derived allowlist serializer·choice 입력 enum, bounded parser, PUT/PATCH, pagination/filter, authentication profile, operation·모델 기반 OpenAPI 3.1·named local schema, Article 게시·Helpdesk composition, 고정 ogen Go client 회귀 | browsable API·배포형/다언어 SDK·일반 viewset 자동화 미지원; schema는 runtime parser·인가 검증의 대체가 아님 | [api](../../api/), [OpenAPI](../../api/openapi/), [serializers](../../serializers/) |
| Auth/session | password hashing, Session/CSRF, injected strict Bearer verifier, rotation/logout | token issuer/JWT/OAuth/OIDC/password reset·multi-user lifecycle 별도 | [auth](../../auth/), [sessions](../../sessions/) |
| Durable system state | explicit provision/open, permission CAS와 session 폐기, cooperative application transaction | 비협력 writer·자동 policy/key 전파 미지원 | [systemstate](../../systemstate/) |
| 개발 예제 | Article 및 Category–Ticket Helpdesk의 Form/Admin/API·관계·기존 DB 권한 갱신, 단일 JOIN 상세, priority·resolution·due_at 추가와 priority choices/표시명 변경 migration | 전체 범용 Helpdesk 기능이나 별도 모듈 배포 검증 아님; 환경별 실행은 TEST_EVIDENCE 참조 | [examples](../../examples/) |

## 계약과 증거

Machine contract/provenance/status는 [conformance/contracts](../../conformance/contracts/)가 소유한다.
Reference-only MIG-075..086은 미등록 진단 reference이며 제품 passing으로 세지 않는다.
Django와 다른 결과는 [DEVIATIONS](../DEVIATIONS.md)에 제한된 차이로 기록한다.

GDJ-0057의 `0b8235c`는 전체 통합, GDJ-0058의 `aca9115`는 관련 ORM scope를 검증했다.
과거 결과와 합쳐 PASS로 표시하지 않으며 Quick feedback, 관련 backend 검증과 전체 platform 검증은 각각 자신의 범위만 증명한다.

Realtime, MySQL/MariaDB/Oracle, GIS/i18n/contrib와 넓은 ORM/Form/Admin/API는 [장기 범위](../CAPABILITY_CATALOG.md)이며
현재 지원 항목이 아니다.
