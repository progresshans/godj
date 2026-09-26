# 현재 구현 범위

코드가 존재하는 범위와 검증 환경을 구분한다. 아래 기능은 모두 bounded subset이며 Django 전체 구현률을 뜻하지 않는다.
현재 작업 상태는 [CURRENT](CURRENT.md), 실제 명령·source·환경별 결과는 [TEST_EVIDENCE](TEST_EVIDENCE.md)가 소유한다.

## 기능

| 영역 | 구현된 범위 | 주요 제한·다음 경계 | 코드 |
|---|---|---|---|
| Schema/IR | Auto·signed int64 Integer(nullable/default)·Char·Text·Date·DateTime·Time·Duration·Float(binary64/nullable/default)·Decimal(exact coefficient/exponent·precision/scale)·UUID(128-bit/nullable/default)·JSON(exact token·SQL/JSON null·nullable/default)·Boolean(nullable/default)·FK/OneToOne, string/int64 choices, column Unique와 named model unique 선언·정규화·생성 metadata, normalized immutable snapshot; columnless ManyToMany 선언·자동 storage projection·generated binding | 양 DB column UNIQUE·ORM 사전 검증·Helpdesk Form/Admin/API/client 연결 지원; named model constraint는 선언·CreateModel/Add/Remove 이력·자동 계획과 양 DB native 적용·모든 key catalog·전체 candidate의 ORM 사전 검증까지 반영, Category Label의 Form/Admin/API/client 연결 구현, GDJ-0097의 소비자·전체 통합 검증 완료; conditional/expression constraint·relation-as-PK·custom field 미지원 | [schema](../../schema/), [정수 의미](../adr/0059-signed-integer-field-and-model-growth.md), [Text 의미](../adr/0060-text-field-and-form-widget-semantics.md), [DateTime 의미](../adr/0061-datetime-field-and-canonical-instant-values.md), [Date 의미](../adr/0065-calendar-date-field-and-input-boundaries.md), [Time 의미](../adr/0066-clock-time-field-and-precision-boundaries.md), [Duration 의미](../adr/0067-duration-model-range-and-number-input.md), [Float 의미](../adr/0068-binary64-field-and-finite-json-boundaries.md), [Decimal 의미](../adr/0069-exact-decimal-values-and-storage.md), [UUID 의미](../adr/0070-uuid-model-values-and-storage.md), [JSON 의미](../adr/0071-json-values-and-native-storage-boundaries.md) |
| 생성 | ProjectSpec, typed model/FieldSet/descriptor와 project relation binding, 후보 compile/publication/recovery | Linux/macOS local filesystem 중심 | [codegen](../../codegen/) |
| Query | typed/dynamic 공통 AST, Boolean composition, scalar comparison·IN, 검증 뒤 빈 조회 SQL 생략, same-model F, projection·Count/Min/Max·관계 filter Count와 root DTO, forward 대상의 11종 scalar·JSON 문서/경로 DTO 선택·정렬 | JSON은 exact/IN/F exact·isnull·projection과 명시적 key/index 경로의 exact/IN/isnull, PostgreSQL contains/contained_by와 양 DB has_key/has_keys/has_any_keys(root/path·forward); root/forward JSON path projection; literal JSON gt/gte/lt/lte·ASC/DESC와 literal 문자열 IContains(root/path·forward); SQLite containment·path compound RHS 대소 비교·JSON Min/Max·ordered F·reverse value/path projection·정렬은 미지원; 넓은 reverse relation·tuple/subquery IN·annotation/grouping/window/bulk/locking은 별도 | [orm](../../orm/), [query](../../query/), [IN 의미](../adr/0062-scalar-membership-and-empty-query-execution.md) |
| 평가 | lazy query, full-result cache, cloning, iterator와 cancellation | 임의 model/callback의 goroutine 안전성을 포함하지 않음 | [ORM cache](../CONCURRENCY.md#queryset-평가) |
| 관계 | AutoField-target FK, 자기·상호 참조 선언/생성, lazy/forward/reverse, 최대 64 hop forward의 scalar lookup·FK isnull과 AND/OR/NOT, prefetch/eager All·First·Count와 여러 direct·nested forward selected 관계·하위 cache·다른 filter JOIN 조합, assignment/cache, CASCADE/PROTECT/SET_NULL과 recursive delete·transitive fingerprint; OneToOne의 명시적 cardinality·단일 reverse 객체/prefetch·직접 scalar 조건과 isnull/AND/OR/NOT·typed/dynamic facade의 forward/reverse 혼합 eager tree, 명시적 reverse Set·required/nullable Clear·unsaved 저장과 outgoing FK를 가진 target의 incoming 정책 삭제; ManyToMany root manager의 add/remove/clear/set·nullable/nonunique through·self symmetry; 같은 typed/dynamic AST의 mixed 관계 조건·scalar lookup·AND/OR/NOT·isnull·IN·filter별 multiplicity·Distinct·Count; ManyToMany 양방향 direct/nested/filtered prefetch·typed/path selection·독립 model cache; 단일 FK/역방향 OneToOne prefetch·target Filter/OrderBy/Distinct/eager·root eager 조합과 부모 cache 재사용·조회 부재와 해제 할당 구분; reverse FK collection·ManyToMany target eager·파생 query 설정 보존·named collection snapshot과 owner별 slice | eager First는 명시적 정렬 필요; relation F·collection value projection/ordering·무제한 깊이·일반 순환 관계 동작·prefetch 설정 query의 streaming 미지원; OneToOne의 ServiceReport migration·Form/Admin/API/client 구현, GDJ-0096의 소비자 통합 milestone 완료 | [관계 소유권](../CONCURRENCY.md#관계-객체), [일대일 의미](../adr/0073-one-to-one-cardinality-and-reverse-objects.md) |
| Migration | strict current definition, historical replay, immutable planner, revision fence, durable prefix, choices·Decimal precision·Unique·관계 cardinality/reverse namespace/delete policy AlterField와 bounded reverse; historical self/cyclic graph·여러 FK Add/Remove·transitive target; self/later/cyclic 자동 계획·선언 순서와 prefix 재개; 명시적 through의 ManyToMany metadata migration과 자동 intermediary의 Create/Add/Remove/Rename·reverse·자동 계획·SQL projection, retained link PK/sequence 보존 | Unique의 historical wire/digest·자동 변경 계획과 양 DB DDL/catalog·실패/재시도·SQLite remake의 index 보존은 지원; named constraint의 CreateModel/AddConstraint/RemoveConstraint·역방향·자동 계획·순환 member 지연과 양 DB native ownership·원자적 실패/재시도·SQLite remake 보존을 지원; required FK 추가는 빈 table에서만 적용; 넓은 schema 변경·backfill은 후속 | [migrations](../../migrations/) |
| Migration CLI | project check, migrate/plan/target, showmigrations, bounded makemigrations, forward sqlmigrate | writer는 지원하는 difference만 작성 | [project](../../project/), [CLI](../../cmd/godj/) |
| SQLite/PostgreSQL | current AST/CRUD/migration/system-state paths; SQLite 새 physical connection의 FK 활성화/readback | 각 backend capability와 물리 schema precondition 적용; JSON TEXT/CHECK·native JSONB의 모델·query/관계·historical add/reverse와 Form/Admin/API·독립 client 연결; UUID 모델·query/집계·historical add/reverse와 Form/Admin/API·독립 client 연결; Decimal은 SQLite BLOB과 PostgreSQL NUMERIC의 exact 저장·Form/API 비용 소비자까지 연결; Float의 SQLite NaN 거부와 signed-zero 정규화, PostgreSQL native 특수값 의미 구분 | [Backend Matrix](../BACKEND_MATRIX.md) |
| SQLite quarantine | retained handle 최대 1, 이후 새 I/O 거부, explicit Close | GDJ-0057 통합 source의 backend/platform/mode 검증 완료 | [lifecycle](../../db/sqlite/relation_transaction.go) |
| Web | request context, routing/reverse, middleware, bounded HTTP errors, server lifecycle | arbitrary converters/realtime/production deployment toolkit 미지원 | [web](../../web/) |
| Template/Form | closed safe template value, escaping, validation, model field allowlist projection, Select choices·NullBooleanSelect·Textarea·DateInput·TimeInput·NumberInput(Float step=any·Decimal 선언 scale)·UTC DateTimeInput과 명시적 빈 입력 정책 | Decimal 원문 precision·scale 검사와 fixed-scale 초기값; UUID 별칭 입력·canonical 초기값·값 기준 변경 감지; JSON 원문·빈 값·exact 숫자 기반 변경 감지; bound Form.WithErrors의 post-clean 진단·cleaned data 소유권; 선택한 scalar와 명시적 int64 ModelChoice snapshot 입력만 편집; arbitrary callable/attribute 실행 없음 | [templates](../../templates/), [forms](../../forms/) |
| Admin | registry, permission, CRUD/history/action composition, read-only 등록, choices 표시명과 기존 값 보존·nullable Boolean의 세 상태·날짜·시간·Float·exact Decimal·UUID·JSON 편집과 Char/Text/JSON 검색 | 확인된 입력 거부의 field/non-field 재표시와 원문 보존; 선택한 model field만 편집; RelatedChoices target 권한·요청 snapshot·저장 재검증과 PROTECT 화면 지원; 모든 relation UI의 일반화를 뜻하지 않음 | [admin](../../admin/) |
| JSON API | model-derived allowlist serializer·choice 입력 enum, bounded parser·exact numeric token·Decimal fixed-scale·UUID canonical 문자열·선언된 JSONField의 임의 JSON 값과 입력/응답 nullability, PUT/PATCH, pagination/filter, authentication profile, operation·모델 기반 OpenAPI 3.1·named local schema, Article 게시·Helpdesk composition, 고정 ogen Go client 회귀 | browsable API·배포형/다언어 SDK·일반 viewset 자동화 미지원; schema는 runtime parser·인가 검증의 대체가 아님 | [api](../../api/), [OpenAPI](../../api/openapi/), [serializers](../../serializers/) |
| Auth/session | password hashing, Session/CSRF, injected strict Bearer verifier, rotation/logout | token issuer/JWT/OAuth/OIDC/password reset·multi-user lifecycle 별도 | [auth](../../auth/), [sessions](../../sessions/) |
| Durable system state | explicit provision/open, permission CAS와 session 폐기, cooperative application/relation transaction | 비협력 writer·자동 policy/key 전파 미지원 | [systemstate](../../systemstate/) |
| 개발 예제 | Article 및 Category–Ticket Helpdesk의 Form/Admin/API·관계·기존 DB 권한 갱신, 단일 JOIN 상세, priority·resolution·due_at·reviewed·service_on·service_at·elapsed·effort·expected_cost·external_reference·external_payload 추가와 priority choices/표시명 변경 및 expected_cost 한도 확장 migration, nullable Boolean·Date·Time·Duration·Float·Decimal·UUID·JSON의 Form/Admin·PUT/PATCH와 external_payload 전체/source 경로 검색 | 전체 범용 Helpdesk 기능이나 별도 모듈 배포 검증 아님; 환경별 실행은 TEST_EVIDENCE 참조 | [examples](../../examples/) |

## 계약과 증거

GDJ-0098의 CASCADE 선언·생성 metadata/project wire·historical 정책 변경·자동 계획과 durable prefix 재개를 구현했다.
양 DB의 deferred native FK·정책 변경/역방향·catalog drift와 SQLite remake의 행·sequence·다른 제약 보존도 구현했다.
공통 ORM의 recursive collector·transitive generated fingerprint도 구현했다. 중복 경로·순환은 model+PK로 한 번 처리하며,
도달한 모든 PROTECT 검사 뒤 SET_NULL·exact-key 삭제를 같은 transaction에서 실행한다. TicketLabel의 migration·scoped Form/Admin/API/OpenAPI/client와 두 endpoint의 권한·Category·pair uniqueness를 연결했다.
Label/Ticket 삭제의 링크 CASCADE·ServiceReport PROTECT를 소비자에서 검증했으며, 복수 API 권한·검색 없는 Admin도 지원한다.
Source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의 Hosted 전체 통합을 완료했다. 이는 GDJ-0099 이전 source의 결과다.
GDJ-0099는 독립 ManyToMany 기준과 명시한 non-null tuple의 native conflict insert를 연결했다.
Columnless 선언·normalized IR·자동 storage projection·generated metadata/descriptor·binding과 bounded project wire를 구현했다.
명시적 through의 historical Add/Remove/Rename·reverse·자동 계획과 양 DB의 metadata migration을 연결했다.
기존 nullable/nonunique/payload 연결의 행·sequence·catalog를 보존하며 선언 변경과 retained binding에 capability 검사를 적용한다.
자동 intermediary의 Create/Add/Remove/Rename·reverse와 raw CreateModel의 columnless 선언도 연결했다.
연결 PK·sequence·물리 identity를 유지하고 managed constraint 이름을 변경하며, storage 참조의 생성/제거 순서와 전체 transient inventory를 검증한다.
공통 root collection runtime·generated forward/reverse manager의 add/remove/clear/set와 같은 AST의 기본 컬렉션 조회·명시적 Distinct를 연결했다.
Nullable/nonunique through·retained ID/payload·self symmetry, incoming 정책·취소·unknown outcome과 cache 소유권을 처리한다.
통합 model facade의 collection 접근자·명시적 UsingSession/InSession과 native session lifetime 검사를 연결했다.
Outer transaction 소유권과 callback 종료 후 cache/model/view 사용 거부를 유지한다.
일반 mixed 관계 조건의 filter scope·부정 EXISTS·manager core filter를 연결했다.
ManyToMany direct/nested/filtered prefetch와 단일 관계 prefetch·root eager의 직접 조합을 구현했다.
Owner별 slice의 named snapshot과 runtime/generated API·전체 owner 집합 조회를 연결했다.
Custom single target query와 빌린 session의 native batch 실행 기반을 연결했다. Root 연결 소유권·prefetch materialized streaming,
Ticket 소비자와 GDJ-0099 Hosted 전체 통합은 남아 있다.
실행한 환경과 source는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

Machine contract/provenance/status는 [conformance/contracts](../../conformance/contracts/)가 소유한다.
Reference-only MIG-075..086은 미등록 진단 reference이며 제품 passing으로 세지 않는다.
Django와 다른 결과는 [DEVIATIONS](../DEVIATIONS.md)에 제한된 차이로 기록한다.

GDJ-0057의 `0b8235c`는 전체 통합, GDJ-0058의 `aca9115`는 관련 ORM scope를 검증했다.
과거 결과와 합쳐 PASS로 표시하지 않으며 Quick feedback, 관련 backend 검증과 전체 platform 검증은 각각 자신의 범위만 증명한다.

Realtime, MySQL/MariaDB/Oracle, GIS/i18n/contrib와 넓은 ORM/Form/Admin/API는 [장기 범위](../CAPABILITY_CATALOG.md)이며
현재 지원 항목이 아니다.
