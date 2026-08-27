# 장기 기능 카탈로그

- 상태: 제품 범위 Accepted, 구현 상태는 [Implementation Matrix](status/IMPLEMENTATION_MATRIX.md) 기준
- 마지막 검토: 2026-08-27

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

Current bounded implementation은 QRY-001..053 중 scalar exact/ASCII `icontains`/`isnull`/IN leaf,
Integer/String literal `gt`/`gte`/`lt`/`lte`, sealed same-model/same-kind `orm.F` field RHS, model-safe typed
`And`/`Or`/`Not`, canonical Filter AND, order/limit/offset/distinct, typed projection과 Count/Max를 하나의 immutable
where/result plan에 연결합니다. Condition RHS는 private literal/list/field union이고 source/kind validation이
terminal I/O 전에 닫습니다. SQLite와 PostgreSQL recursive compiler, nullable LHS/RHS odd-NOT truth table와
Article q/published/exclude/range/field-match exactly-two-query 흐름 중 GDJ-0040 경계는
[EVID-112](status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)와
[final local EVID-113](status/TEST_EVIDENCE.md#evid-20260823-113--gdj-0040-frozen-source-final-local-gates),
[hosted EVID-115](status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion)에서
affected/final local/hosted-verified됐습니다. GDJ-0041 확장도 frozen source `7f2bb223...`의 local-final과
[hosted EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion)에서
QRY-034..053 20/20·신규 10/10 zero-diff 및 PostgreSQL 17.10 actual까지 검증됐습니다. 이 문단은 아래 장기 목록의
Q/F arithmetic/functions, Django `range` lookup/custom lookup, annotation/grouping/having, subquery/window,
bulk/locking, relation/cross-model F 또는 relation OR/NOT이 구현됐다는 뜻이 아닙니다.

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
Writer/upgrade와 public migrate CLI는 포함하지 않습니다. 아래 library-level loaded execution은 별도 제품 경계입니다.

GDJ-0036 current lifecycle에서 `definition.Load`의 결과는 opaque `migrations.LoadedDefinitionSet`이며 public
DB-aware entry는 `Executor.Migrate(ctx, loaded, request)` 하나입니다. Historical reconstruction은 scalar와
ForeignKey를 같은 Schema IR/ProjectState로 표현합니다. Raw scalar execution은 별도 `DirectExecutor`가 맡고
relation-bearing raw input은 loader authority 없이 실행하지 않습니다. 이는 public migrate CLI, writer 또는
autodetector가 구현됐다는 뜻이 아닙니다.

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

Completed [GDJ-0045](../work/0045-durable-single-runtime-system-state-and-article-restart.md)는 이 목록 전체가 아니라
single-runtime system state의 첫 durable 단면입니다. Accepted
[ADR-0047](adr/0047-explicit-single-runtime-system-state.md)에 따라 current migration으로 admin credential/session/audit 세
table을 명시 적용하고 clean restart 뒤 Article Admin/API authentication, logout과 Admin audit history를 복구하는 구현이
hosted-verified됐습니다. SYS-001..012 global adapter는 11 `passing` + SYS-009 Verified DEV-0008
`deviation`이고 SQLite distinct-process actual과 EVID-129의 PostgreSQL 17.10 required 16/16·skip 0이 통과했습니다.
GDJ-0045 자체의 범위는 DB/schema당 one live runtime, sequential restart, digest-only bearer storage와 Article/audit
same-transaction이었습니다. 이 historical boundary와 SYS-001..012/DEV-0008 evidence는 그대로 보존됩니다.

Completed [GDJ-0046](../work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md)와 Accepted
[ADR-0048](adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)은 같은 DB/schema를 사용하는 cooperative
Runtime을 backend coordinated transaction으로 선형화하고 deployment-shared active/validation CSRF key ring을 주입하는 bounded
hardening입니다. Ordinary transaction을 바꾸지 않는 additive `db.CoordinatedAtomic`, fenced `systemstate.Runtime`/Article writer,
same-process two-Runtime HTTP handoff와 실제 two-process SQLite/PostgreSQL barrier/restart를 구현했습니다. SYS-013..020은
product `passing`이며 current source에 묶인 checked PostgreSQL 17.10 live attestation, local full/386/external archive와 exact hosted
matrix가 EVID-133/134에서 검증됐습니다. Zero config는 process-local key와 SYS-009/DEV-0008을 유지합니다.

이것은 cooperative framework writer와 identical normalized deployment policy의 범위입니다. User/group/password lifecycle,
general Unique/Integer/CAS IR, non-cooperative writer, distributed session/coordination, session-family revocation, automatic key
distribution/provider, JWT/OAuth와 production deployment는 별도 후속입니다.

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

Completed GDJ-0044는 API half의 exact reference를 DRF 3.18.0 + Django 6.1 + CPython 3.14.3으로 고정했습니다.
Accepted ADR-0045/0046 아래 bounded slice는 closed `<int64:name>` route/reverse와 JSON-only Article
list/create/retrieve/PUT/PATCH/delete, SessionAuthentication-style 403+CSRF, per-method permission, fixed PageNumber
pagination과 bounded search/filter/order를 구현했습니다. Exact 18 contracts는 `13 passing + 5 deviation`이며
WEB-028/029는 Verified DEV-0006, API-001/003/010은 Verified DEV-0007입니다. 구현·검증 상태는
[Implementation Matrix](status/IMPLEMENTATION_MATRIX.md)와
[EVID-126](status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion)이 정본입니다.
OpenAPI, browsable API, token auth, nested/bulk serializer와 M7 전체 완료는 포함하지 않습니다.

Completed [GDJ-0047](../work/0047-api-authentication-profiles-and-bearer-article-api.md)과 Accepted
[ADR-0049](adr/0049-first-party-bff-and-bearer-api-authentication.md)은 기존 first-party session+CSRF profile을 보존하면서
strict `Authorization: Bearer` resource-server adapter가 같은 `auth.Principal`/Permission과 Article handlers를 재사용하는 bounded
수직 단면입니다. Common authentication boundary, Session adapter refactor, opaque redacted Bearer token/injected verifier와
profile-neutral Article API가 local product publication에 도달했습니다. AUT-009/010/011/014/016/API-011/012 일곱 개는
`passing`, AUT-012/013/015 세 개는 일곱 result selector의 Verified DEV-0009 `deviation`이며 exact actual comparison
10/10과 SQLite Article Bearer E2E가 통과했습니다. Corrected source `14e47c9b...`의 digest-pinned PostgreSQL 17.10
Article Bearer E2E normal/race/CGO0 및 two-process source-bound attestation도 통과했습니다. Initial hosted run
`33044776835`는 26 jobs success 뒤 macOS Intel product job 하나가 30분 outer timeout으로 취소됐습니다. EVID-137의
Intel-only correction과 corrected full/386/1,077-file archive refreeze도 통과했습니다. Corrected head `5f97fa8...` / tree
`2b53c031...`의 [EVID-138](status/TEST_EVIDENCE.md#evid-20260827-138--gdj-0047-corrected-exact-head-hosted-completion) /
CI #155 run `33049861740`은 27/27 jobs·360/360 steps success로 이 bounded capability를 terminal Verified했습니다.
JWT/opaque token issuance, refresh family, OAuth/OIDC,
signing key provider와 production BFF는 이 packet의 지원 주장이 아닙니다.

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
`31419940399`가 26/26 hosted gate를 통과해 GDJ-0027 완료 시점 product는 exact `12 adapter sets/127 contracts = 115
passing + 5 deviation + 7 oracle_locked`, relation REL-001/003/004/005/006 5/12입니다. Reverse
prefetch/eager/write/delete/DDL/migration과 broader backend는 계속 미지원입니다.

Completed GDJ-0028/Accepted ADR-0028은 REL-012-only reverse prefetch를 bounded product slice로 구현했습니다.
Existing owner query 1회 뒤 generated project prefetch surface가 distinct ordered owner keys로
source FK `IN` batch 1회를 실행하고, 검증·grouping 전체 성공 뒤 owner-order warm `RelatedSet`을 publish하는
경계입니다. Empty input은 batch I/O 0, distinct key 1..999는 exactly one batch, 1000은 pre-I/O structured
failure이며 chunking/custom Prefetch/filter/order 소비는 제공하지 않습니다. Exact implementation head
`4858ab88...`의 run `31432551159`가 26/26 hosted gate를 통과해 GDJ-0028 completion product는 exact
`116 passing + 5 deviation + 6 oracle_locked`, relation REL-001/003/004/005/006/012 6/12입니다. REL-009..011
eager, write/delete/DDL/migration, broader relation/backend는 계속 미지원입니다.

Completed GDJ-0029/Accepted ADR-0029는 locked REL-009/010/011을 함께 구현·검증한 bounded slice입니다. Additive
app projection companions, singular immutable one-hop projection AST, existing object factory에 붙는 All-only eager
bridge와 project typed/dynamic dispatch가 required `author` INNER JOIN, nullable `reviewer` LEFT OUTER JOIN,
reverse `posts` pre-I/O rejection을 같은 resolver/runtime/compiler 경계로 묶습니다. Existing
descriptor/QuerySet/object/reverse/prefetch와 generated bytes는 frozen입니다. 이 bounded bridge는 canonical
`project.Using(backend)` application facade를 동결하지 않으며 그 UX는 Q-013/Q-017에서 open입니다. Baseline
EVID-054/run `31436881856`은 그 baseline의 exact
`116 passing + 5 deviation + 6 oracle_locked`, relation 6/12만 증명하고 activation/API/implementation proof로
재사용하지 않았습니다. Implementation head `c02aab67...`의 EVID-056/run `31470292759`가 exact 26/26 hosted
gate를 통과해 그 implementation head의 product는 `119 + 5 + 3`, relation 9/12였습니다. Canonical facade와 broader surface는
Q-013/Q-017에서 계속 open입니다.

Completed GDJ-0030/Accepted ADR-0030은 REL-007 PROTECT와 REL-008 SET_NULL을 함께 여는 SQLite-only low-level
delete slice입니다. Additive immutable `RelationSetNullPlan`, constructible typed protected error,
`db.RelationAtomic.AtomicRelation`, project-bound deleter와 declared-universe incoming-policy fingerprint, per-connection
FK-on + pinned `BEGIN IMMEDIATE`, no-retry and exact thirteen-file generated union을 한 packet으로 검증합니다.
Generated surface는 `zz_godj_relation_delete.go`의 `RelationDeleters`/`BindRelationDeleters`이고 canonical facade는
아닙니다. Direct/generated deleter target은 supported incoming edge가 하나 이상이어야 하며, literal COMMIT error만
stable `commit_outcome_unknown`, relation session mutator entry 뒤 rollback+discard confirmation이 모두 실패한 경우만 stable
`transaction_outcome_unknown`으로 분류하고 raw begin/transaction cleanup-discard를 검증합니다. Session은 모든
Mutator/RelationMutator 호출 직전에 mutation-possible을 표시하며 이 deleter의 첫 entry는 SET_NULL/target DELETE입니다. 모든 incoming
edge의 metadata-matching physical SQLite FK는 supported schema precondition이며 runtime DDL 보장이 아닙니다.
Implementation head `c3803acb...`의 EVID-061/run `31510689383`이 exact 26/26·326/326 hosted gate와
four-coordinate 687/687/0 inventory를 통과한 GDJ-0030 completion classification은
`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12였습니다. REL-002,
canonical facade, cache invalidation, recursive/bulk/CASCADE delete, DDL/migration과 non-SQLite는 그 packet에
포함하지 않았습니다.

다음 GDJ-0033/0034 단락은 GDJ-0036 reset 이전 checkout의 historical implementation evidence입니다.

Completed GDJ-0033/Accepted ADR-0033은 bounded Gate 0 facade에 exact `New`/`Save`/`With*`/clear API,
project-private write descriptor, explicit PK-presence, pending-only reconciliation, corrected canonical three-phase
preflight와 per-edge COW cache를 추가했습니다. Implementation head `be6f3d4e...`의 EVID-076/run `31586910749`은
exact 26/26 jobs·326/326 steps와 four-coordinate 715/715/0 inventory를 통과했고 그 checkout의 bounded product는
`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12입니다. 이는 SQLite/AutoField forward assignment/save
capability만 뜻합니다. Q-013 `Partial`과 Q-017 P1/open, reverse/general facade, relation-capable migration,
coordinated generated upgrade와 non-SQLite backend는 그대로 남습니다.

Completed GDJ-0034는 기존 ADR-0029 경계 안에서 typed generated `select_related` resolve/bind cause-loss P2를
별도로 수정했습니다. 당시 private stored configuration error와 context-first terminal pre-I/O 반환을 generator v2와
두 checked-in companion에 결정적으로 반영했고, exact implementation head `3099bd62...`의 EVID-081/run
`31605477297`이 26/26 jobs·326/326 steps를 통과했습니다. 새 capability, contract, public API 또는 backend 지원을
추가하지 않았으므로 product 분류는 exact `122 passing + 5 deviation + 0 oracle_locked`, relation 12/12 그대로입니다.

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

## Current implementation mirror: pre-release compatibility reset

[GDJ-0036](../work/0036-pre-release-compatibility-reset.md)과 Accepted
[ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)가 현재 설계를 소유합니다.

- Schema IR, Definition wire/digest와 `ProjectState`는 각각 current version 1 하나입니다.
- Loader는 `format_version` 하나를 strict decode하고 opaque `LoadedDefinitionSet`을 게시합니다. Lifecycle은
  `Executor.Migrate`로만 들어가며 context carrier나 raw definition slice authority가 없습니다.
- Backend는 mandatory `MigrationCapabilities`와 ordered `MigrationIntent`를 받는 session
  `BeginMigration` 하나를 사용합니다. `DirectExecutor`는 raw scalar 전용입니다.
- Public `StateReconstructor`의 최종 계약은 scalar/relation을 같은 current historical state로 재생하는 것입니다.
- Current main codegen은 relation model의 descriptor/write metadata를 직접 만들고, app relation-query file과
  facade-private write model은 제거했습니다. Project-owned cross-app query/binding/facade 책임은 유지합니다.
- MIG-057..074는 current format/result vocabulary로 재기준화됐습니다. GDJ-0035 Phase-B의 legacy
  tuple/profile/promotion publication 계획과 당시 artifact bytes는 retire되어 Git/EVID에만 남습니다. 현재
  checked-in MIG-075..086 manifest/oracle은 ADR-0035 current-only 진단 reference이며 reference aggregate에
  포함되지만 계속 `oracle_locked`/unregistered라 product publication/status 입력은 아닙니다.

이 reset은 corrected exact head의 EVID-103 hosted matrix까지 완료됐지만 public release compatibility 정책을
뜻하지 않습니다.

## Current implementation mirror: project-linked runserver

[GDJ-0042](../work/0042-project-linked-runserver-and-article-development-loop.md)와 Accepted
[ADR-0042](adr/0042-project-linked-runserver-and-article-development-loop.md)가 전역 CLI에서 generated-aware Article
development server를 실행하는 completed bounded 수직 단면을 소유합니다. Source checkpoint
`810149fd90ecf0b3a9cb7b4b98344476082ce769`에서 제품 코드는 `Implemented`이고 SQLite와 PostgreSQL 17
actual development-loop local gate를 통과했습니다. Documentation checkpoint `47b0eb8...`의 EVID-120에서 final
full/386/803-file repository-external archive와 세 independent audit도 통과했습니다. First submitted
`46a57aa...` run은 26 success/1 timeout이었고 correction `2b49938...`의 EVID-121 local refreeze 뒤 submitted
`2bfdbd5...`의 EVID-122/run `32659704239`가 exact 27/27 jobs·358/358 steps로 통과했습니다. 따라서
WEB-011..020 bounded 단면은 hosted `Verified`입니다.

- Descriptor format 1의 `runserver_package`는 declaration `package` 뒤의 optional strict field입니다. 없어도
  migration/generate descriptor는 유효하고 `runserver`만 `runserver_not_configured`로 닫힙니다.
- Global `godj runserver [--project ...] [--addr ...]`는 current bundle을 read-only preflight한 뒤 별도 runtime
  package를 no-shell로 build/run합니다. 현재 address는 exact IPv4 loopback와 canonical port만 허용하며,
  implicit generate publication, migrate, repair, retry, watch/reload를 수행하지 않습니다.
- Article example은 `GODJ_ARTICLE_SQLITE_DATABASE` 하나 또는
  `GODJ_ARTICLE_POSTGRES_URL`/`GODJ_ARTICLE_POSTGRES_SCHEMA` exact pair 하나를 상호 배타적으로
  선택합니다. 이는 example-owned runtime environment이며 framework-wide public database settings가
  아닙니다.
- SQLite actual은 pre-migrated file DB, global child Article advanced HTTP response, repeated same-port start/stop,
  durable row/history, stale bundle no-start/no-write와 clean process/temp 경계를 로컬에서 검증합니다.
- PostgreSQL 17 actual은 isolated schema에서 같은 global child Article advanced HTTP response와 child 종료 후
  row/history reopen durability를 로컬에서 검증합니다. 새 runserver 테스트 자체는 query count나
  PostgreSQL service/container restart를 증명하지 않습니다.

이 단면은 development-only loopback server입니다. MySQL, Windows process semantics, non-loopback/TLS,
production readiness, general reload 오케스트레이션은 지원 주장에서 제외합니다.

## Current implementation mirror: parameterized Article JSON API

[GDJ-0044](../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md)와 Accepted
[ADR-0045](adr/0045-closed-parameterized-routing-and-reverse.md)/
[ADR-0046](adr/0046-json-serializer-and-session-authenticated-article-api.md)이 첫 API 경계를 소유합니다.

- `web.Route.Path`는 closed `<int64:name>` segment를 받고 `web.Int64Argument`,
  `Application.ReverseWith`/`Request.ReverseWith`, `Request.Int64Parameter`가 typed route/reverse를 게시합니다.
- `serializers`는 reflection-free declared-field validation, `api`는 bounded exactly-one JSON object와 stable
  error/page envelope, `api/sessionauth`는 explicit principal/permission/CSRF 경계를 소유합니다.
- Article adapter는 deterministic list/filter/page와 create/detail/PUT/PATCH/delete를 current SQLite/PostgreSQL
  repository에 연결합니다. Invalid/auth/permission/CSRF failure의 Article mutation은 0입니다.
- Opt-in global Article site는 기존 Admin login session으로 API safe GET에서 fresh masked CSRF token을 받고 unsafe
  mutation을 수행합니다. Portable 네 좌표 required 16/16·skip 0과 PostgreSQL required 15/15·skip 0은
  EVID-126/CI #142에서 검증됐습니다.

Durable user/session/audit, arbitrary converter, generated ModelSerializer, OpenAPI/browsable API/token auth,
Channels/Realtime와 production serving은 이 mirror의 capability가 아닙니다.

## Current implementation mirror: Bearer Article API

[GDJ-0047](../work/0047-api-authentication-profiles-and-bearer-article-api.md)의 local product-publication checkpoint는
common `api.Authentication`, updated Session adapter와 strict injected `api/bearerauth`를 같은 Article handlers에 연결합니다.
Exactly-one bounded Authorization header, fixed 401/400/403 challenge, cookie/query/body no-fallback, Bearer CSRF bypass,
deny-overlay permission과 secret-free diagnostics를 구현했습니다. Exact actual 10/10과 SQLite Article CRUD/denial E2E는
통과했고 PostgreSQL 17.10 Article flow normal/race/CGO0과 checked two-process attestation도 통과했습니다. Initial
hosted run `33044776835`는 macOS Intel product job의 30분 outer timeout으로 terminal proof가 되지 못했습니다.
EVID-137의 Intel-only 45분 correction, source-bound attestation recapture와 corrected full `make ci`/Linux 386/
1,077-file external archive 뒤 EVID-138/CI #155 run `33049861740`의 exact 27/27 jobs·360/360 steps가 통과했으므로
이 bounded mirror는 Accepted/Verified입니다.
Concrete JWT/opaque issuer/verifier implementation, refresh token, OAuth/OIDC, key lifecycle와 production BFF는 없습니다.

## Current implementation mirror: project generated bundle

[GDJ-0037](../work/0037-project-schema-generated-bundle-and-recoverable-publication.md)과 Accepted
[ADR-0036](adr/0036-project-schema-generated-bundle-and-recoverable-publication.md)이 project generation 하위 경계를
소유합니다.

- Declaration-only `ProjectSpec`은 immutable bundle, format-1 manifest와 exact app `4n`/project 8 roster를 만듭니다.
- Global `godj generate`/`--check`는 sealed project root, compile-only whole candidate와 recoverable publisher를
  사용합니다. Article은 12, relationdelete는 16 source입니다.
- `--check`는 selected project tree/Git을 변경하지 않습니다. Precommit ordinary failure는 exact prior,
  postcommit publisher cleanup은 exact next를 유지하며 outer workspace cleanup failure는 별도 closed process
  outcome입니다.
- Darwin/Linux local filesystem implementation은 affected 및 final full/386/repository-external source-clean-copy local
  gates 뒤 exact correction head `d4643068...`의
  [EVID-105](status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) / CI #103
  26/26 jobs·326/326 steps에서 hosted-verified됐고 GDJ-0037은 completed입니다. 이 완료는 Q-010/Q-017의 남은
  semver·raw-model·general upgrade나 PostgreSQL/Web capability를 구현했다는 뜻이 아닙니다.

## Active capability: canonical application model facade

[GDJ-0048](../work/0048-canonical-application-model-facade-and-current-generated-abi.md)과 Proposed
[ADR-0050](adr/0050-canonical-embedded-application-model-facade.md)은 Q-017의 application-facing current ABI를 locally
implemented했고 final verification을 진행 중입니다.

- Current checkout은 facade v3의 private raw-model alias embedding으로 scalar와 app-owned ordinary method를 promotion하고 existing
  project-owned relation origin/object/cache/presence/self state를 유지합니다.
- Existing `Save`, `With*`, `Clear*`, relation accessor와 deep-clone `Unwrap` signatures는 유지합니다. Direct FK mutation은
  accessor/With*/Clear*/Unwrap/Save 전에 canonical snapshot/full rebuild/all-success publication으로 changed edge만 reconcile합니다.
- Generated/promoted field와 handwritten production method namespace collision은 whole-candidate compile에만 맡기지 않고
  target mutation 전 bounded AST audit로 fail-closed합니다.
- Wrapper direct JSON marshal/unmarshal은 fail-closed이고, `Unwrap`은 DTO 구축용 raw deep-clone escape이며 supported
  Web JSON/template representation은 app-owned DTO입니다. Exported promoted field를 `html/template`에서 type-level로 숨긴다고
  주장하지 않으며 wrapper direct template use는 unsupported/noncontractual입니다.
- Facade v3는 existing app 4/project 8/bundle-seal 1 role roster와 format-1 manifest를 재사용해 Article exact 12/snapshot
  `f0043e499...`와 relationdelete exact 16/snapshot `81534d390...`을 whole bundle로 게시했습니다.

이 구현은 affected normal/race/CGO0/vet, external compile, v2 hybrid rejection, actual-package namespace/recovery, SQLite,
focused PostgreSQL 별도-process 흐름과 독립 2회 source-bound PostgreSQL attestation을 통과했습니다. EVID-140에서 required
PostgreSQL exact 18/18·skip 0, full `make ci`, Linux/386, 1,088-file external archive와 independent audit도 통과했습니다. Exact
hosted matrix 전이므로 terminal verified로는 아직 승격하지 않습니다. Reverse/general manager, installed-version negotiation과
first-alpha 이후 upgrader도 이번 active packet의 capability가 아닙니다.

### Historical GDJ-0035 design and evidence snapshot

GDJ-0035 당시 relation-migration decision 상태는 `Accepted/Partially Implemented`였습니다. Existing migration 제품의
legacy tuple `(1,1,1,2)`, digest v1, scalar state v1과 SQLite scalar lifecycle 보존은 당시 설계 기준이었습니다.
다음 항목은 [Accepted ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)와
[active work](../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)가 Phase C에서
채택한 bounded decision surface입니다. 구현 범위는 아래 D1/D2/D3a/D3b/D4d/D4e/D4f 한계를 따릅니다.

- MIG-075: legacy profile/digest/state ABI preservation
- MIG-076..078: exact relation profile, one-loader mixed digest v2, whole-step relation state promotion/demotion
- MIG-079: wire `target_field` 없는 historical AutoField derivation과 static/history-plan/physical three-stage preflight
- MIG-080..084: existing-fence optional port, four capabilities, SQLite relation CreateModel/AddField/remake/physical FK/restart
- MIG-085..086: precommit faults, commit three outcomes and no retry

Product aggregate는 계속 exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`, relation
12/12입니다. Phase C exact 8-test-only head `7d36502...`는 EVID-089/090 local/hosted gates를 통과했습니다.
그 proof는 later product support를 구현하지 않았습니다.
Test-only helper/hash/private catalog는 noncanonical입니다. Proposed docs-freeze head `5bdf013...`는
EVID-091/run `32183309328`에서 별도 local/hosted 검증됐고 그 성공을 근거로 bounded design만 Accepted됐습니다.
Acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`에서 별도 hosted-verified됐으며 product state는
바뀌지 않았습니다. Later D1 definition/handoff, D2 private state/readiness와 D3a direct optional
SQLite Create/Delete port는
[EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)에서
각 bounded Implemented/Verified됐습니다. D3b는
[EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)에서
normal loaded relation-bearing Create/Delete core apply/unapply/reapply와 actual-plan preflight를
Implemented/Verified했습니다. D4 exact test-only head `424ec4d...`는
[EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)에서
기존 제품 경로의 bounded captured-snapshot close/reopen 시나리오를 Verified했습니다. EVID-096 docs head
`62df9b2...`는 run `32260744096`에서 고유하게 검증됐고, D4d final head
`dd83362...`는
[EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`에서 sealed same-target loaded universe의 nullable ForeignKey Add를 Implemented/Verified했습니다.
그 D4d head의 exact capability는 `{true,true,false,false}`였습니다. Public Add intent는 changed target 하나만 소유하고 SQLite가
그 sealed snapshot을 같은 symbolic target의 pre-existing source ForeignKey에만 privately 확장합니다. Native
`ALTER TABLE ... ADD COLUMN ... INTEGER NULL REFERENCES ... ON DELETE NO ACTION`, populated-row NULL 보존,
canonical mixed declaration, reopen/fault/resource 경계를 검증했습니다. 이어 EVID-097 docs head `c59669c...`는 run `32278555810`에서
닫혔고 D4e final head `1d86f6e...`는
[EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`에서 bounded empty-source required Add를 Implemented/Verified했습니다. Exact capability는
당시 `{true,true,true,false}`였습니다. Required field는 no-default/non-PK/`PROTECT`이고 existing source emptiness는 pinned
`BEGIN IMMEDIATE` 뒤 claim 전에 확인합니다. Same-intent created source는 statically empty이며 populated source와
Remove/remake는 그 head에서 fail-closed였습니다. EVID-098 docs head `85f9270...`는 CI #94/run
`32288383027`에서 별도로 닫혔고, D4f product `4982e27...`와 final inventory head `9d5b894...`는
[EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95/run `32294983953`에서 bounded `RemoveForeignKeyByTableRemake`를 Implemented/Verified했습니다. Exact
capability는 `{true,true,true,true}`입니다. 구현은 exact appended nullable `PROTECT` 또는 `SET_NULL`,
required `PROTECT` reverse only를 허용합니다. Frozen D4f direct E2E fixture는 nullable `PROTECT`와
required `PROTECT`만 검증했으며 dedicated nullable `SET_NULL` D4f E2E proof는 주장하지 않습니다.
Same-target relation-free AutoField authority, max one relation mutation/source/step, closed relevant physical shape,
row/PK/value/sequence preservation과 rollback/no-retry를 검증했습니다. Arbitrary/general remake, general restart와
actual MIG adapter는 아직 없습니다.
이 snapshot에서 MIG-075..086은 `oracle_locked`였습니다. GDJ-0036 current publication에는 포함하지 않습니다.
