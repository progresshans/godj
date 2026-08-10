# 장기 기능 카탈로그

- 상태: 제품 범위 Accepted, 구현 상태는 [Implementation Matrix](status/IMPLEMENTATION_MATRIX.md) 기준
- 마지막 검토: 2026-08-10

이 문서는 큰 프로젝트에서 기능 범위를 잃지 않기 위한 카탈로그입니다. 목록에 있다는 사실은 구현, 지원, API 안정성을 뜻하지 않습니다. 각 영역은 해당 milestone에서 contract와 work item으로 더 작게 분해합니다.

## Core와 앱 시스템

- Settings와 environment configuration
- Project와 App
- installed app 목록과 deterministic app registry
- app configuration과 lifecycle
- system checks
- URL routing, namespace, reverse
- request/response와 exception handling
- middleware chain
- signal/event와 hook ordering
- management command와 custom command
- development server와 reload orchestration
- logging
- cache, storage, email, file upload의 extension interface
- static/media 처리

## Models와 Schema

- schema-first Go DSL
- versioned, normalized Schema IR
- model/field options와 metadata
- generated model type, FieldSet, Descriptor, Codec binding
- model identity와 new/loaded/dirty state
- custom model methods
- custom field와 validator/default registry
- model inheritance에 해당하는 Go다운 확장 전략
- app label, table name, ordering, indexes, constraints
- database introspection과 `inspectdb` 계열 도구

초기 field 후보:

- Auto/BigAuto/SmallAuto 계열
- integer/positive integer/float/decimal
- boolean
- char/text/slug/email/URL/UUID
- date/time/datetime/duration
- binary/JSON
- file/image
- enum/choices
- ForeignKey, OneToOne, ManyToMany
- backend 전용 array/range/generated/spatial field

정확한 field 목록과 semantics는 한 번에 구현하지 않고 contract group으로 확장합니다.

## ORM과 QuerySet

- `Manager[M]`, `QuerySet[M]`, `Predicate[M]`, `Field[M,V]` 계열 generic core
- 지연 평가와 chain 후 원본 불변성
- result cache와 iterator semantics
- typed field predicate와 dynamic Django-style lookup
- `exact`, comparison, contains/startswith/endswith, `in`, `range`, `isnull`, date transform 등 lookup
- Q/F expression
- order, limit/offset, distinct
- values/values-list에 해당하는 dynamic row
- typed projection을 위한 top-level generic operation
- annotation, aggregate, grouping, having
- subquery, exists, conditional expression, database function
- create/get/get-or-create/update-or-create
- bulk create/update/delete
- select/update/delete
- select-related와 prefetch-related
- row locking과 backend capability
- raw SQL escape hatch와 안전한 parameter binding
- custom lookup/expression/compiler extension

## Query AST와 실행

- model/select/join/where/group/having/order/limit/offset/distinct/lock/annotation 표현
- typed/dynamic path의 같은 normalized AST
- AST validation과 immutable transform
- backend compiler와 parameter binding
- row scan/value encode
- context cancellation과 resource cleanup
- connection pool과 transaction affinity
- structured error taxonomy

## Django 6.1 profile-specific backlog

Compatibility manifest를 만들 때 [Django 6.1 release notes](https://docs.djangoproject.com/en/6.1/releases/6.1/)에서 새로 추가되거나 의미가 바뀐 항목도 별도 contract 후보로 추적합니다.

- deferred field의 `FETCH_ONE`, `FETCH_PEERS`, `FETCH_RAISE`와 `QuerySet.fetch_mode()` 의미
- database-level `ForeignKey.on_delete` 선택과 signal 부작용 차이
- multiple `MAILERS` 설정과 legacy email setting 전환
- Content Security Policy middleware/context processor/template nonce 통합
- `JSONNull`, UUID4/UUID7 database function
- backend별 GeneratedField 지원 변화
- deterministic ordering을 나타내는 QuerySet 의미
- Django 6.1에서 추가·변경된 Admin, Form, GIS, contrib 동작

이 목록은 release note를 그대로 구현 목록으로 복사하지 않습니다. 실제 사용자 관찰 가능성, backend, 우선순위를 분석해 contract ID를 부여합니다.

## Migration

- project state, model state, historical model
- migration graph와 dependency
- autodetector와 rename prompt/policy
- applied migration recorder와 locking
- schema editor와 introspection
- forward/backward와 fake/plan/show commands
- data migration과 현재 model type 사용 금지

현재 제품 단면은 caller가 explicit source bytes를 `migrations/definition`에 전달하는 loader와,
completed GDJ-0022/Accepted ADR-0022의 exact `godj migrations check`까지입니다.
Global CLI는 exact
`godj.toml`을 선택해 private project runner를 build/run하고 linked code가 명시한 flat roots를 no-follow로
읽어 actual loader에 exactly once 넘깁니다. MIG-065..074는 actual adapter에서 10 `passing`입니다.
Writer/upgrade/DB-aware execution은 포함하지 않습니다.

장기 operation 범위:

- Create/Delete/RenameModel
- Add/Remove/Alter/RenameField
- Add/Remove/RenameIndex
- Add/Remove/AlterConstraint
- AlterModelOptions와 table rename
- RunSQL과 Go data migration
- database/state 분리 operation

## Database backends

- SQLite
- PostgreSQL
- MySQL
- MariaDB
- Oracle
- multi-DB와 database router
- backend feature/capability matrix
- type mapper, value adapter, query compiler, schema editor, introspector
- explicit unsupported feature error

정확한 도입 순서와 상태는 [BACKEND_MATRIX.md](BACKEND_MATRIX.md)에 있습니다.

## Validation과 Forms

- 공통 validation core와 error code
- Form field와 model field mapping
- bound/unbound form
- widget과 HTML rendering
- multipart/file upload
- Form과 ModelForm
- FormSet, ModelFormSet, InlineFormSet
- cleaned data, initial data, changed data
- field/non-field error와 localization
- CSRF 통합

Form과 Serializer는 validation primitive를 공유할 수 있지만 공개 API와 lifecycle은 분리합니다.

## Template

- Django Template Language 계열 parser/runtime 호환
- variable resolution과 안전한 method exposure
- auto escaping과 safe value
- inheritance, block, include
- custom tag/filter와 library registry
- context processor
- CSRF/static/URL reverse/i18n integration
- loader, cache, template override

## Admin

- AdminSite와 model registration
- ModelAdmin options
- list display/filter/search/order/pagination/date hierarchy
- add/change/delete/history
- fieldsets, readonly fields, widgets, autocomplete
- inline models
- actions
- global/model/object permission
- custom Admin view와 template override
- Form/Auth/Session/CSRF/Messages 통합
- GeoAdmin 계열 spatial widget

흐름과 확장 개념은 보존하되 Django Admin DOM/CSS 복제는 목표가 아닙니다.

## Auth, Session, Security

- user/group/permission/content type
- password hash 문자열 호환과 upgrade
- authentication backend
- login/logout/session
- permission decorator/middleware/helper
- object permission extension point
- CSRF, secure cookie, host/origin validation
- password reset/change, token
- audit/security regression catalog

## API

- Request/Response abstraction
- serializer와 model serializer
- validation/deserialization/representation
- nested data와 partial update
- parser/renderer/content negotiation
- authentication, permission, throttling
- APIView와 generic view
- ViewSet와 model ViewSet
- router
- pagination/filtering/ordering/versioning
- exception/error response
- OpenAPI generation
- browsable API

DRF의 정확한 reference profile은 Q-016에서 정합니다.

## Realtime

- WebSocket와 SSE
- Consumer와 typed/JSON consumer
- protocol router
- auth/session middleware
- channel layer와 group
- presence
- in-memory backend
- Redis backend
- NATS 등 adapter extension
- backpressure, disconnect, shutdown, delivery semantics

Channels의 정확한 reference profile은 Q-016에서 정합니다.

## GIS

- Geometry, Point, LineString, Polygon, Multi*와 SRID
- spatial field와 value codec
- spatial lookup, distance, transform, aggregate
- GeoForm과 GeoAdmin
- PostGIS
- SpatiaLite
- MySQL Spatial
- Oracle Spatial
- backend별 지원 차이와 explicit capability

GIS는 상위 package 하나로 끝나지 않고 Schema IR, Query AST, Codec, Backend extension point를 필요로 합니다.

## i18n과 지역화

- translation catalog와 locale negotiation
- lazy/explicit translation API에 해당하는 Go 설계
- timezone-aware datetime
- 날짜, 시간, 숫자 format
- validation/Admin/template/API error localization
- locale file extraction/compile workflow

## Contrib와 인프라

- content types
- sites
- messages
- redirects
- sitemaps
- humanize
- static files
- sessions
- cache backend
- storage backend
- email/mailer abstraction
- file upload handler
- task/background work extension interface

기능 간 metadata/auth/validation 의존을 먼저 정의하고 이름만 있는 빈 package를 만들지 않습니다.

## CLI와 개발 도구

장기 사용자 명령 후보:

```text
godj version
godj startproject
godj startapp
godj generate
godj generate --check
godj makemigrations
godj migrate
godj runserver
godj createsuperuser
godj test
godj inspectdb
custom management commands
```

- global CLI와 project-aware binary
- deterministic project/app templates
- schema/code generation orchestration
- migration plan/show/apply
- dev reload/build
- version mismatch detection
- shell/completion/documentation generation 후보

향후 UX 후보 `godj migrations check`와
`godj migrations check --project <descriptor-file>`는 완료된 GDJ-0021/Accepted ADR-0021이 argument,
descriptor, protocol, failure와 exit `0/1/2/3/130` 의미를 test-only로 먼저 검증했고,
GDJ-0022/Accepted ADR-0022가 전역 CLI, exact two-export public project API와 production
project-linked runner를 독립 구현했습니다.

## Testing과 품질

- Django differential contract
- translated invariant test
- Go-native unit/integration/compile test
- codegen golden/idempotency/atomicity
- backend conformance
- race/fuzz/property test
- goroutine/connection leak test
- transaction/rollback/error-path test
- security regression catalog
- performance/allocation baseline
- dependency boundary test
- migration compatibility/upgrade test

GDJ-0021 품질 gate는 기존 제품 10 adapter/105 contract를 유지하면서 열한 번째 reference set까지
115 unique contract/110 ordered cross-binding을 검증했습니다. Historical implementation head
`84ddf109c04acd72992b816aa72140c6e748e5f0`의
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)은 기존 full/exact 2개,
Linux/macOS x64/arm64 test-only project-check 4개와 같은 좌표의 actual SQLite 4개, 총 10개 job을
모두 통과했습니다. GDJ-0022 local gate는 열한 번째 product adapter를 추가해 115 product contract의
`110 passing + 5 deviation`을 검증했습니다. Workflow는 product 4 + Python compatibility 4를 더한
exact 18 required execution으로 확장됐고 fix head run `31329294154`에서 18/18 성공했습니다. Initial
run의 네 Python pre-test uv assertion failure와 취소 및 수정 범위는 EVID-028에 기록했습니다. Actual
adapter가 없는
PostgreSQL/MySQL service-only job은 false green이므로
두지 않습니다. 첫 backend CI는 digest-pinned service image, health check, UTC timezone과 C locale
또는 명시적으로 승인된 collation, actual query/write/transaction/schema/migration/recorder/
revision-lifecycle 및 durable restart/persistence contract를 모두 실행해야 합니다. Expected contract
수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도 필수입니다.

Completed GDJ-0023은 relation 제품 지원 전에 pinned Django 6.1 ForeignKey REL-001..012와
`conformance/relationbinding/**` compile/AST feasibility를 분리했습니다. 당시 relation product adapter가
없어 12개는 `oracle_locked`였고 public product 지원이 아니었습니다. Implementation head
`b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`은
existing exact 18에 Linux/macOS x64/arm64 test-only relation-binding proof 4개를 더한 exact 22/22를
통과했습니다. Accepted ADR-0023은 symbolic/atomic binding, project bridge, shared immutable AST와
field-union relation arm 방향을 고정했습니다. Completed GDJ-0024/Accepted ADR-0024는 exact IR v3/DSL,
mixed v2 target/v3 source additive companion, atomic binder와 REL-001 metadata-only product subset을
구현하고 exact 26 hosted gate에서 검증했습니다. Completed GDJ-0025/Accepted ADR-0025는 그 위에 REL-004
required forward exact predicate, shared immutable path와 SQLite one reusable INNER JOIN만 추가했습니다.
Completed GDJ-0026/Accepted ADR-0026은 REL-003/006을 additive sealed descriptor/object companions,
opaque pointer instance cache, nullable NULL zero-I/O access와 relation-provenance-preserving SQLite source-key
`isnull` trim으로 구현했습니다. Exact implementation head `5be46141...`의 run `31370313755`이 26/26
jobs·326/326 recorded steps를 통과해 GDJ-0026 완료 시점 product는 `12 adapter sets/127 contracts = 114 passing + 5
deviation + 8 oracle_locked`, relation REL-001/003/004/006 4/12입니다. Reverse/eager/write/delete/DDL/
migration, LEFT JOIN, broader target와 non-SQLite backend는 계속 미지원이며 PostgreSQL,
OneToOne/ManyToMany 또는 ForeignKey breadth 제품 지원의 증거가 아닙니다.

Completed GDJ-0027/Accepted ADR-0027은 REL-005-only reverse ForeignKey exact lookup과 owner related-set
accessor를 bounded product slice로 구현했습니다. Exact implementation head `7db68415...`의 run
`31419940399`가 26/26 hosted gate를 통과해 현재 product는 exact `12 adapter sets/127 contracts = 115
passing + 5 deviation + 7 oracle_locked`, relation REL-001/003/004/005/006 5/12입니다. Reverse
prefetch/eager/write/delete/DDL/migration과 broader backend는 계속 미지원입니다.

## Django 데이터 이행

- default table/column naming
- relation and join table convention
- auth/group/permission/content type
- password hashes
- existing schema introspection
- data import/export
- migration bridge 또는 별도 migration tool

이행 도구는 destructive operation 전에 plan/dry-run/backup/rollback 계약이 필요합니다.

## 패키징과 릴리스

- 하나의 제품, version, CLI, documentation 체계
- 초기 single Go module
- generated application source의 Git commit과 `generate --check`
- heavy optional dependency가 실제로 core build를 방해할 때 official module 분리
- generated code/Schema IR/migration format upgrade policy
- API freeze 전 compatibility warning
- signed release, supply-chain, security disclosure 정책

## 상태 확인

이 카탈로그의 어떤 항목도 자체적으로 완료 표시하지 않습니다. 현재 구현과 검증 여부는 [IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md), 실제 명령은 [TEST_EVIDENCE.md](status/TEST_EVIDENCE.md)를 기준으로 합니다.
