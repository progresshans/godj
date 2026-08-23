# Database Backend Matrix

- 상태: 장기 목표 Accepted, SQLite 제한 단면 Verified, PostgreSQL DB-PG-001..010 bounded slice Verified
- 마지막 검토: 2026-08-24

이 표는 지원 주장표가 아니라 **계획과 검증 범위**입니다. `Planned`는 동작한다는 뜻이 아닙니다.

## 제품별 계획

| Backend | 도입 단계 | 현재 상태 | 초기 역할 |
|---|---|---|---|
| SQLite | M0 reference / M1-M2 GoDj | 제한 단면 Verified; GDJ-0042 runserver `Implemented`/local actual pass, exact-head hosted pending | read/write, transaction, 최소 migration conformance |
| PostgreSQL | M3 | DB-PG-001..010 bounded Implemented/Verified; GDJ-0042 PostgreSQL 17 runserver `Implemented`/local actual pass, exact-head hosted pending; broader support open | relation, locking, production-oriented semantics |
| MySQL | M9 | Not started | backend conformance |
| MariaDB | M9 | Not started | MySQL과 차이를 별도 capability로 검증 |
| Oracle | M9 | Not started | 별도 driver/CI/licensing 운영 검토 필요 |
| PostGIS | M10 | Not started | GIS 첫 production spatial target 후보 |
| SpatiaLite | M10 | Not started | SQLite spatial conformance 후보 |
| MySQL Spatial | M10 | Not started | 지원 범위 capability 기반 |
| Oracle Spatial | M10 | Not started | optional official module 가능성 검토 |

## Capability 축

각 backend는 단순 boolean 하나가 아니라 최소 다음 capability를 선언하고 conformance test와 연결합니다.

```text
RETURNING
partial / expression indexes
deferrable constraints
SELECT FOR UPDATE / NOWAIT / SKIP LOCKED
JSON / array / generated columns
window functions
DDL transaction
savepoint
rename column/index/constraint
upsert and conflict target
timezone and precision behavior
collation/case-insensitive lookup
spatial fields/lookups/aggregates
```

지원하지 않는 기능은 compile/runtime에서 구조화된 `NotSupported` 계열 오류로 드러나야 합니다. backend가 기능을 조용히 무시하거나 의미가 다른 SQL로 바꾸면 안 됩니다.

## 버전 정책

각 milestone 시작 시 DB server/client/library 버전을 정확히 pin하고 [SOURCES.md](SOURCES.md)와 CI matrix에 기록합니다. 로컬 macOS의 SQLite 버전은 개발 환경 관찰일 뿐 compatibility 약속이 아닙니다.

GDJ-0038의 local PostgreSQL 17.5 profile은 exact
`170005|UTF8|UTF8|c|<null>|C|C|UTC|on|on|read committed|off|off|on|on|origin`이고 hosted PostgreSQL 17.10
target은 첫 필드만 `170010`입니다. 이는 server version, server/client encoding, locale provider/provider locale/
collation/ctype, timezone, standard strings, synchronous commit, default transaction isolation/read-only/deferrable,
fsync, full-page writes와 replication role의 16-field lock입니다. Final PostgreSQL 17.10 profile과 bounded actual
product는 EVID-108/run `32626539049`에서 검증됐지만 이 profile은 broader/production support 약속이 아닙니다.

GDJ-0040 source `86d6b169...`는 SQLite와 PostgreSQL의 model/projection/direct·derived aggregate 경로가 하나의
authoritative Boolean where tree를 재귀적으로 compile하도록 전환했습니다. 두 backend 모두 explicit grouping과
deterministic DFS argument order를 사용하고 nullable exact/`icontains`/IN의 Django complement guard를 odd NOT
parity에만 적용합니다. Existing relation predicate는 root conjunction에서만 허용하며 OR/NOT descendant는
structured unsupported로 I/O 전에 닫습니다.

[EVID-112](status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)는
SQLite actual QRY-034..043 10/10 zero-diff와 PostgreSQL 17.5 compiler/normal/race Article actual을 기록합니다.
[EVID-115](status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion)은
PostgreSQL 17.10 required 12/12, Boolean/Article actual, restart와 전체 exact 27/27 matrix를 통과했습니다. 이는
collation/Unicode 일반화, relation OR/NOT, broader PostgreSQL support나 production readiness를 추가하지 않습니다.

GDJ-0041 frozen source `7f2bb223...`는 두 compiler에 Integer/String literal range와 same-source field RHS를
추가했습니다. SQLite/PostgreSQL 모두 field RHS를 quote된 identifier로 내리고 placeholder/argument를 소비하지 않으며,
nullable LHS/RHS odd-`NOT` guard와 duplicate guard 제거, RHS source/kind/union fail-closed를 같은 규칙으로
검증합니다. SQLite actual과 PostgreSQL compiler/backend-independent Article tests는 invalid request DB I/O 0과 성공
projection+aggregate 정확히 두 query를 통과했습니다. QRY-034..053 SQLite actual 20/20과 신규 10/10도
zero-diff입니다. 로컬 PostgreSQL URL은 미설정이었지만 submitted head `e97a4e3...`의
[EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion)은 exact PostgreSQL
17.10 required 12/12, normal/race/CGO-disabled, Article actual과 restart를 통과했습니다. 따라서 이 bounded
GDJ-0041 backend 경계는 hosted `Verified`입니다.

현재 SQLite 검증은 `AutoField`, `CharField`, `BooleanField`, nullable CharField의 제한된
read/write, scalar `CreateModel`/nullable no-default `AddField`, normal loaded AutoField-target ForeignKey
Create/Delete apply/unapply/reapply 및 sealed same-target universe의 nullable ForeignKey Add, empty-source required
ForeignKey Add와 bounded ForeignKey reverse/remove table remake 단면입니다. File
reopen 검증은 captured schema/rows/history/token/FK snapshot에 한하며 raw database-file bytes나 general restart를
뜻하지 않습니다. Required relation Add는 no-default/non-PK/`PROTECT`와 empty source에 한해 지원하며 populated
source는 구조화된 capability error로 거부합니다. Relation Remove 구현은 exact appended nullable
`PROTECT` 또는 `SET_NULL`, required `PROTECT` ForeignKey와 closed remake eligibility를 모두 만족할 때만
지원합니다. Frozen D4f direct E2E fixture는 nullable `PROTECT`와 required `PROTECT`만 다루며
dedicated nullable `SET_NULL` D4f E2E coverage는 주장하지 않습니다.
Backend별 verified 상태는 기능 contract와
[status/IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md)에서 관리합니다. 이
문서에는 실제 통과하지 않은 체크 표시를 추가하지 않습니다.

## GDJ-0042 project-linked Article development loop

Source checkpoint `810149fd90ecf0b3a9cb7b4b98344476082ce769`은 optional descriptor
`runserver_package`와 global loopback `godj runserver`를 구현했습니다. Article runtime backend는
`GODJ_ARTICLE_SQLITE_DATABASE` 하나 또는
`GODJ_ARTICLE_POSTGRES_URL`/`GODJ_ARTICLE_POSTGRES_SCHEMA` exact pair를 상호 배타적으로
선택합니다. Global CLI는 이 값을 DB public configuration으로 해석하지 않고 runtime environment로
전달하며, URL을 child argv나 framework diagnostic에 복제하지 않습니다.

- SQLite actual product gate는 pre-migrated file DB와 global child를 사용해 Article advanced HTTP response,
  repeated same-port start/stop, row/history durability, project-tree no-write와 process/temp cleanup을 로컬에서
  검증했습니다.
- PostgreSQL 17 actual product gate는 isolated schema에서 같은 global child response와 child 종료 후 exact
  Article row/history reopen durability를 로컬에서 검증했습니다. 새 runserver test는 query-count
  instrumentation이나 PostgreSQL service/container stop/start를 실행하지 않으므로, 기존 query/restart
  gate와 별도 증거로 유지합니다.
- Initial `47b0eb8...` local final 뒤 first submitted run은 26 success와 macOS Intel 20-minute timeout 하나로
  끝났습니다. Correction `2b49938...`의 EVID-121에서 30-minute budget/lock과 final full/386/803-file archive/audit
  refreeze가 통과했습니다. 두 actual gate와 corrected local final은 pass이지만 corrected exact-head hosted run은
  pending입니다. 따라서 이 단면을 hosted `Verified`로 표시하지 않습니다.

Runserver는 current generated bundle을 read-only preflight하며 auto-generate, auto-migrate, reload를 수행하지
않습니다. 현재 지원 주장은 IPv4 loopback development server와 SQLite/PostgreSQL 17로 한정되며
MySQL, Windows process semantics, non-loopback/TLS와 production readiness를 포함하지 않습니다.

## 현재 migration backend ABI

Current migration backend ABI는 scalar와 relation lifecycle을 같은 mandatory port로 통합합니다.

- `RevisionFencedBackend`는 `MigrationCapabilities()`와 `OpenRevisionFencedSession(ctx)`을 제공합니다.
- Session은 `BeginMigration(ctx, HistoryTransition, MigrationIntent)` 하나만 제공합니다. Scalar step도 같은
  ordered intent를 사용하며 relation target이 없을 수 있습니다.
- Capability는 operation 선택 전에 whole-plan을 검증하기 위한 지원 범위이고, SQLite의 FK-on, catalog,
  physical preflight와 remake는 sealed intent를 실행하는 backend 내부 단계입니다.
- `Executor.Migrate`만 opaque `LoadedDefinitionSet`의 complete lifecycle을 실행합니다. 별도
  `DirectExecutor`는 raw scalar transaction 경계이며 relation input을 fail-closed합니다.

이 ABI와 current-format 회귀는 GDJ-0036 exact hosted gate를 통과했습니다. 아래 hosted run은 GDJ-0035 당시 dual
optional-port 구현의 역사적 증거이며 현재 PostgreSQL support 주장이 아닙니다.

## GDJ-0038 PostgreSQL bounded verified slice

Source commit `cb90f7a69d70c131ccf8868fb83efcf7bd7c2548`은 위 mandatory ABI를 PostgreSQL current profile에
구현합니다. 모든 user/control object는 explicit schema와 closed catalog profile 아래에 있고 schema DDL, exact recorder
transition과 revision advance는 하나의 pinned fenced transaction에 속합니다. Generated CRUD/relation, loaded
apply/unapply/reapply, contention, close/reopen와 actual server stop/start resume가 같은 local product gate에서 실행됩니다.

[EVID-107](status/TEST_EVIDENCE.md#evid-20260823-107--gdj-0038-postgresql-migration-and-web-integration-source-frozen-local-checkpoint)은
exact 16-field local profile, required actual 12/12·skip 0, race/CGO0/full/386/source-clean-copy와 audit P0..P3=0을
기록합니다. Docker restart published-port/sequence assertion correction head `187638f9...`는
[EVID-108](status/TEST_EVIDENCE.md#evid-20260823-108--gdj-0038-postgresql-1710-exact-head-hosted-completion) /
run `32626539049`에서 PostgreSQL 17.10 exact profile, required actual 12/12·skip 0와 durable restart를 포함해
27/27 jobs·341/341 steps로 통과했습니다. 따라서 DB-PG-001..010만 `Verified`이며 production readiness,
REL-007/008, adoption/repair/retry 또는 broader PostgreSQL support는 주장하지 않습니다.

## Historical GDJ-0035 backend evidence

GDJ-0035 Phase C는 relation migration에 필요한 optional backend 경계를
[Accepted ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)의 bounded
design으로 채택했습니다. Proposed docs-freeze head `5bdf013...`의 local/hosted proof는 EVID-091에 기록했고,
acceptance head `7cdc6d6...`도 EVID-092/run `32187094845`의 고유 exact-head hosted gate를 통과했습니다.
Exact four capability는 relation-bearing CreateModel, nullable ForeignKey AddField, empty-table required
ForeignKey AddField와 bounded remake remove입니다. Optional port는 existing revision-fenced backend/session을
embed하고 existing `RevisionFencedTransaction`을 그대로 반환합니다. SQLite transaction order는 exact connection
`PRAGMA foreign_keys=1` → `BEGIN IMMEDIATE` → physical preflight → revision/history claim → DDL/remake →
`foreign_key_check` → recorder/successor revision → one commit입니다.

Phase D3a product `2eafde1...`과 inventory correction `ce58c5e...`는 additive optional API와 direct SQLite
relation-bearing Create/Delete port를 구현했고 [EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification) /
run `32218003207`에서 exact 26/26 jobs·342/342 steps·audit P0..P3=0으로 검증됐습니다.
D3b product `74c2b72...`과 inventory correction `167ef03...`은 normal loaded `definition.Load`→
`Set.Migrate` core가 exact-one fenced history, fresh actual Planner, whole-plan dry validation과 conditional
relation capability를 거쳐 SQLite Create/Delete를 apply/unapply/reapply하도록 연결했고
[EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification) /
run `32231149900`에서 exact 26/26·342/342와 audit P0..P3=0으로 검증됐습니다.
D4 test-only verification head `424ec4d...`는 새 backend와 fresh mixed `Load`를 사용한 close/reopen,
Latest no-op, target child-first unapply와 second-restart reapply를
[EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification) /
run `32248885053`에서 검증했습니다. Product source/API/workflow와 inventory lock은 바뀌지 않았습니다.
EVID-096 documentation head `62df9b2...`는 run `32260744096`에서 고유하게 닫혔습니다. D4d product
`3950d98...`, inventory lock `28b141e...`와 deterministic resource-scan fix `dd83362...`는
[EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`의 exact 26/26 jobs·342/342 steps와 audit P0..P3=0에서 검증됐습니다. 그 뒤 EVID-097
documentation head `c59669c...`는 run `32278555810`에서 별도로 닫혔습니다. D4e product `7c07805...`와
inventory lock `1d86f6e...`는
[EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`의 exact 26/26 jobs·342/342 steps와 audit P0..P3=0에서 검증됐습니다. 현재 SQLite capability는
당시 exact `{CreateModelForeignKeys:true, AddNullableForeignKey:true,
AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:false}`였습니다. EVID-098 docs head
`85f9270...`는 CI #94/run `32288383027`에서 별도로 닫혔고, D4f product `4982e27...`와 inventory lock
`9d5b894...`는 [EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95/run `32294983953`의 exact 26/26 jobs·342/342 steps와 audit P0..P3=0에서 검증됐습니다. 현재 SQLite
capability는 exact `{CreateModelForeignKeys:true, AddNullableForeignKey:true,
AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:true}`입니다. Complete relation intent의
zero-target scalar Add/Remove는 실행할 수 있지만 relation Add/Remove support로 세지 않습니다. Nullable Add는
public changed-field target 하나를 유지하고, pre-existing source ForeignKey가 모두 그 exact symbolic target을
가리키며 target snapshot에 relation이 없을 때만 private full target list를 파생합니다. Migration step마다 source
model당 nullable/required relation Add를 합쳐 하나만 허용합니다. Required Add는 empty existing source를 pinned
transaction에서 claim 전에 확인하거나 same-intent created source를 statically empty로 증명하며 native NOT NULL
FK를 추가합니다. D4f Remove는 deterministic temporary table, explicit retained-column PK-order copy, exact row
count와 `sqlite_sequence`, final canonical/FK 검증을 같은 fenced transaction에서 수행합니다. Remake source
inbound FK/non-PK index, touched/control trigger/view, relevant generated/hidden/option, malformed sequence와
namespace/temp/control collision은 pre-claim 거부하지만 unrelated harmless object는 허용합니다. Populated required
Add/reapply, arbitrary/general remake와 general
restart는 계속 미지원이며
GDJ-0035의 MIG-075..086 reference-only `oracle_locked` 분류는 당시 증거입니다. GDJ-0036에서는 그
Phase-B publication을 retire했고 current product error ownership만 회귀 기준으로 유지합니다. Current
`BeginMigration`의 global PRAGMA/catalog/physical-preflight/claim failure는 step-level `NoOperation`과 existing
typed class를, SchemaEditor/final-FK failure는 exact operation을 소유합니다.
