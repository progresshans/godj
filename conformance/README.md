# GoDj Compatibility Lab

## GDJ-0036/0037 current migration/generated ABI boundary

GoDj는 아직 첫 외부 alpha 전이므로 개발 중 migration/generated ABI를 영구 legacy로 지원하지 않습니다.
[GDJ-0036](../work/0036-pre-release-compatibility-reset.md)과
[ADR-0035](../docs/adr/0035-pre-release-current-only-format-and-generated-publication.md)에 따른 현재 lab 경계는
다음과 같습니다.

- Schema IR, Migration Definition wire/digest와 `ProjectState`는 각각 current version 1 하나입니다.
- `definition.Load`는 opaque `migrations.LoadedDefinitionSet`을 반환하고 complete product lifecycle은
  `Executor.Migrate(ctx, loaded, request)` 하나입니다. Raw scalar execution은 `DirectExecutor`가 소유하며
  relation-bearing raw input은 fail-closed합니다.
- Backend는 mandatory `MigrationCapabilities`와
  `BeginMigration(HistoryTransition, MigrationIntent)` 하나를 사용합니다.
- MIG-057..064 observation은 single `format_version`, current digest와
  `format`/`execution`/`lifecycle`/`session_open_calls` vocabulary로 재기준화했습니다. MIG-065..074도 같은
  current definition-set digest/provenance를 사용하며 기존 contract ID와 `passing` status를 유지합니다.
- Definition manifest/oracle은 5,151/29,654 bytes와 SHA-256 `b5bc2612...`/`61401746...`, project-check
  manifest/oracle은 5,085/19,971 bytes와 `e689b370...`/`8bbf10c0...`입니다. 두 NI fixture는 status-only라
  기존 1,574/1,729 bytes를 유지합니다.
- Current codegen main ABI는 relation model descriptor/write metadata를 직접 생성합니다. App-local
  relation-query generated file과 facade-private write model은 없고 cross-app query/binding/facade는
  project-owned입니다. GDJ-0036 당시 단계별 roster `8 / 9 / 11 / 12 / 13`은 현재 GDJ-0037 whole-project
  adoption에서 Article exact 12, relationdelete exact 16 source와 별도 canonical manifest로 수렴했습니다.
- GDJ-0035 Phase-B의 legacy tuple/profile/promotion publication sequence는 retire했습니다. MIG-075..086의
  checked-in manifest/oracle은 ADR-0035 current-only 진단 reference로 재기준화했으며 계속
  `oracle_locked`/unregistered입니다. 이전 artifact bytes는 Git history와 EVID에만 남고, current reference
  aggregate에는 이 locked set을 포함하지만 product aggregate에는 포함하지 않습니다.

GDJ-0036 corrected exact head는 EVID-103 hosted matrix를 통과했습니다. GDJ-0037의 generation bundle/manifest,
Article 12/relationdelete 16 adoption과 relation behavior는 EVID-104 local gates 뒤 exact correction head
`d4643068...`의 [EVID-105](../docs/status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) /
CI #103에서 26/26 jobs·326/326 steps로 hosted-verified됐습니다. GDJ-0037은 completed지만 Q-010은 `Partial`,
Q-017은 P1/open이고 PostgreSQL/Web 또는 MIG-075..086 제품 전환을 뜻하지 않습니다.

## Historical progression

이 디렉터리는 Django reference profile, contract manifest, normalized observation,
comparator, M0 codegen bootstrap spike와 GoDj observation adapter를 보관합니다.
GDJ-0003은 같은 exact profile에 write/migration 전용 두 번째 contract set을 추가했고,
GDJ-0004는 그 set을 실제 제품 package에 연결했습니다.
GDJ-0005는 mutable Save lifecycle 전용 세 번째 reference set을 추가했습니다.
GDJ-0007은 QuerySet evaluation/cache 전용 네 번째 reference set을 추가했습니다.
GDJ-0008은 네 번째 set을 실제 제품 adapter에 연결해 `passing`으로 전환했습니다.
GDJ-0009은 migration dependency/applied-state planning 전용 다섯 번째 reference set을
추가했고, GDJ-0010은 immutable public Planner adapter를 연결해 `passing`으로
전환했습니다.
GDJ-0011은 multi-migration plan execution 전용 여섯 번째 reference set을 추가했습니다.
GDJ-0012는 여섯 번째 GoDj live adapter와 fail-closed DEV-0001 expectation을 연결해
여섯 exact `passing`과 네 verified `deviation`으로 전환했습니다.
GDJ-0013은 durable recorder read와 fresh restart planning 전용 일곱 번째 reference set을
추가했습니다. GDJ-0014는 이 set을 read-only recorder와 fresh file-backed backend를 쓰는
GoDj live adapter에 연결해 10 `passing`으로 전환했습니다.
GDJ-0015는 loaded migration definition의 historical `ProjectState` reconstruction 전용
여덟 번째 reference set을 추가했습니다. GDJ-0016은 immutable public reconstructor와
read-only recorder-backed GoDj live adapter를 연결해 이 set을 10 `passing`으로
전환했습니다.
GDJ-0017은 fresh/target/failure/restart migration lifecycle 전용 아홉 번째 reference set을
추가했습니다. GDJ-0017 완료 당시 이 10개는 `oracle_locked`였고 제품 adapter는 없었습니다.
별도 `lifecyclefence` package는 revision fence의 test-only feasibility를 검증할 뿐 제품 API나
backend 구현이 아닙니다.

GDJ-0018은 public `Executor.Migrate`와 revision-fenced SQLite backend를 사용하는 아홉 번째
GoDj live adapter를 연결했습니다. Lifecycle 9개는 `passing`, MIG-052만 reviewed DEV-0002
`deviation`이며 GDJ-0018 완료 당시 9 product set의 분류는
`92 passing + 5 deviation`이었습니다.
GDJ-0019는 explicit migration definition source 전용 열 번째 reference set을 contract-only로
추가했습니다. GDJ-0019 완료 당시 MIG-057..064 여덟 개는 `oracle_locked`였고 제품 loader와
열 번째 GoDj adapter가 없었습니다. 따라서 reference는 10 set/105 unique contract/90 ordered
cross-binding으로 늘었지만 당시 제품 분류는 `92 passing + 5 deviation` 그대로였습니다.
GDJ-0020은 public `migrations/definition` bounded loader와 열 번째 actual adapter를 연결해
MIG-057..064를 8 `passing`으로 전환했습니다. GDJ-0020 완료 당시 제품 분류는 정확히 10 adapter/105 contract의
`100 passing + 5 deviation`입니다. Source discovery/CLI, writer/upgrade, executable/custom
operation과 non-SQLite migration backend 지원을 뜻하지 않습니다.
GDJ-0021은 database-free migration project check의 decision/compatibility contract 열 개를
Accepted ADR-0021에 묶인 열한 번째 reference set으로 추가했습니다. GDJ-0021 완료 당시
MIG-065..074는 `oracle_locked`였고 product adapter, global CLI 또는 production project-linked
runner는 없었습니다. Reference corpus는 11 set/115 unique contract/110 ordered cross-binding으로
늘었지만 제품 분류는 10 adapter/105 contract의 `100 passing + 5 deviation`을 유지했습니다.
GDJ-0022는 actual global kernel과 project-linked loader report를 결합하는 열한 번째 GoDj adapter를
연결해 MIG-065..074를 10 `passing`으로 전환했습니다. GDJ-0022 완료 당시 제품 분류는 정확히
11 adapter/115 contract의 `110 passing + 5 deviation`이며 DB-aware drift check나 non-SQLite backend 지원을
뜻하지 않습니다.
GDJ-0023은 ForeignKey relation 동작 12개를 exact Django reference로 고정한 열두 번째 set을
추가했습니다. 이 단계의 REL-001..012는 모두 `oracle_locked`였고 제품 adapter는 없었습니다.
GDJ-0024는 generated v3 relation metadata와 project binder를 관찰하는 열두 번째 product adapter를
연결해 REL-001 metadata를 `passing`으로 전환했습니다. GDJ-0025는 별도 generated relation-query product와
SQLite required one-hop `INNER JOIN`을 연결해 REL-004도 actual `passing`으로 전환했습니다. GDJ-0026은
additive generated relation-object companion/project bridge와 actual SQLite를 통해 REL-003 required lazy cache와
REL-006 nullable access/source-key `isnull`을 `passing`으로 전환했습니다. GDJ-0027은 별도 generated
reverse-relation product와 actual SQLite를 연결해 REL-005 reverse accessor/lookup도 `passing`으로 전환했습니다.
GDJ-0028은 exact ten-file generated reverse-prefetch product와 actual SQLite의 한 번짜리 root `IN` batch를 연결해
REL-012 reverse prefetch도 `passing`으로 전환했습니다. GDJ-0029는 app-local projection scanner 두 개와
project select-related companion을 더한 exact twelve-file product를 actual SQLite에 연결해 REL-009 required
INNER, REL-010 nullable LEFT OUTER와 REL-011 reverse-path pre-I/O rejection을 함께 `passing`으로 전환했습니다.
GDJ-0030은 별도 exact thirteen-file relation-delete product와 project-bound collector를 actual SQLite에 연결해
REL-007 `PROTECT`와 REL-008 `SET_NULL`을 `passing`으로 전환했습니다. GDJ-0033은 existing exact thirteen-file
generated prerequisite를 byte-for-byte 보존하면서 project facade companion 하나를 교체한 exact fourteen-file
union에서 forward assignment와 Save를 실행해 REL-002도 `passing`으로 전환했습니다. GDJ-0035 Phase A는
MIG-075..086을 13번째 reference-only set으로 고정했습니다. 그 GDJ-0035 Phase A checkout의 reference는
13 set/139 unique contract/139 unique scenario/156 ordered cross-binding이고, 제품 분류는 계속
12 adapter/127 contract의
`122 passing + 5 deviation + 0 oracle_locked`입니다. 이는 relation metadata, required predicate/object cache,
nullable local-key access/`isnull`, bounded reverse accessor/lookup, exact reverse prefetch와 one-hop forward eager
selection, forward assignment/Save 및 bounded project-bound `PROTECT`/`SET_NULL` delete 12/12의 제품 증거이며
general cascade/eager graph/DDL/migration 전체
지원을 뜻하지 않습니다.
GDJ-0035 Phase B와 Phase C는 `conformance/migrationrelation`의 test-only candidate/proof만 검증했습니다.
Exact Phase C head `7d36502...`/EVID-090은 one-loader/profile/digest/state/wire/preflight/existing-fence/SQLite
decision boundary를 동결했고 Proposed docs head `5bdf013...`/EVID-091의 별도 local/hosted 성공 뒤 ADR-0034의
bounded design이 Accepted됐습니다. Acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`의 고유
exact-head hosted gate를 통과했습니다. Later D1 definition/handoff, D2 private historical
state/readiness, D3a direct optional SQLite Create/Delete port는
[EVID-093](../docs/status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)에서
각 bounded product slice로 구현·검증됐습니다. D3b는
[EVID-094](../docs/status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)에서
normal loaded `Load`→`Set.Migrate`의 bounded relation Create/Delete apply/unapply/reapply와 actual-plan
preflight를 구현·검증했습니다. MIG-075..086 product handler/status는 그대로 `oracle_locked`이고
D4 verification head `424ec4d...`는 [EVID-095](../docs/status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)에서
기존 normal loaded 제품 경로의 bounded file-backed close/reopen 시나리오를 검증했습니다. 이후 EVID-096
documentation head `62df9b2...`는 unique run `32260744096`에서 닫혔고, D4d product `3950d98...`, inventory
lock `28b141e...`, deterministic resource-scan fix `dd83362...`를 포함한 exact head는
[EVID-097](../docs/status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`에서 bounded nullable ForeignKey Add를 검증했습니다. 이는 `migrationrelation` actual adapter나
MIG status 전환이 아닙니다. 이어서 D4d documentation head `c59669c...`는 run `32278555810`에서 닫혔고,
D4e product `7c07805...`와 inventory lock `1d86f6e...`는
[EVID-098](../docs/status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`에서 empty-source required ForeignKey Add를 검증했습니다. Exact capability는
`{true,true,true,false}`였습니다. EVID-098 documentation head `85f9270...`는 CI #94/run `32288383027`에서
별도로 닫혔고, D4f product `4982e27...`와 inventory lock `9d5b894...`는
[EVID-099](../docs/status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95/run `32294983953`에서 bounded ForeignKey reverse/unapply table remake를 검증했습니다. Exact capability는
`{true,true,true,true}`입니다. 이는 `migrationrelation` actual adapter나 MIG status 전환이 아닙니다.
제품용 Schema/ORM/SQLite/migration 구현은 루트의 `schema`, `codegen`, `query`, `orm`,
`db`, `migrations` package에 있으며 이 디렉터리는 그 동작을 oracle에 연결합니다.

## 정본과 생성물

| 경로 | 역할 |
|---|---|
| `profiles/*.json` | exact reference runtime과 dependency lock fingerprint |
| `contracts/manifest.json` | M1 read/metadata contract 11개 |
| `contracts/write-migration-manifest.json` | M2 write/transaction/migration contract 11개 |
| `contracts/save-lifecycle-manifest.json` | Save lifecycle reference contract 12개 |
| `contracts/query-cache-manifest.json` | QuerySet evaluation/cache reference contract 11개 |
| `contracts/query-breadth-manifest.json` | Typed projection/scalar aggregate/stable pagination reference contract QRY-022..033 |
| `contracts/query-expression-manifest.json` | Boolean/range/field-reference query contract QRY-034..053; current 20개 모두 `passing` |
| `contracts/template-form-manifest.json` | WEB-021..027/FRM-001..005 closed template/Form contract; 10 passing + 2 DEV-0003 |
| `contracts/auth-session-manifest.json` | AUT-001..008 session/auth/CSRF contract; 6 passing + 2 DEV-0004 |
| `contracts/article-admin-manifest.json` | ADM-001..010 bounded Article Admin contract; 9 passing + 1 DEV-0005 |
| `contracts/parameter-routing-manifest.json` | DRF-profile WEB-028..035 closed parameter route/reverse contract; 6 passing + 2 DEV-0006 |
| `contracts/article-api-manifest.json` | DRF-profile API-001..010 Article JSON API contract; 7 passing + 3 DEV-0007 |
| `contracts/api-authentication-manifest.json` | DRF/RFC/GoDj AUT-009..016/API-011..012 authentication profile contract; 7 passing + 3 Verified DEV-0009 |
| `contracts/system-state-manifest.json` | SYS-001..020 system-state contract; 19 passing + SYS-009 DEV-0008 deviation under Accepted ADR-0048 |
| `contracts/migration-planning-manifest.json` | Migration planning reference contract 12개 |
| `contracts/migration-execution-manifest.json` | Migration plan execution reference contract 10개 |
| `contracts/migration-restart-manifest.json` | Recorder-backed restart planning reference contract 10개 |
| `contracts/migration-state-reconstruction-manifest.json` | Historical ProjectState reconstruction reference contract 10개 |
| `contracts/migration-lifecycle-manifest.json` | End-to-end migration lifecycle reference contract 10개 |
| `contracts/migration-definition-source-manifest.json` | Current-format migration definition source reference contract 8개 |
| `contracts/migration-project-check-manifest.json` | Project-linked migration catalog check decision contract 10개 |
| `contracts/migration-command-manifest.json` | Project-linked explicit migrate decision contract MIG-087..098; current `passing` product publication/status 입력 |
| `contracts/migration-writer-manifest.json` | MIG-099..110 bounded migration-writer mixed-authority contract; Phase A historical은 `oracle_locked`, current는 7 passing + 5 Verified DEV-0010 deviation product publication |
| `contracts/migration-status-manifest.json` | MIG-111..118 bounded read-only migration-status mixed-authority contract; current Phase A reference-only `oracle_locked`/unregistered이며 product publication/status 입력 아님 |
| `contracts/relation-manifest.json` | ForeignKey relation reference contract 12개; 현재 12개 모두 product-required |
| `contracts/migration-relation-manifest.json` | ADR-0035 current-only MIG-075..086 diagnostic reference; `oracle_locked`/unregistered이며 product publication/status 입력 아님 |
| `profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json` | API half의 isolated exact DRF/Django/Python/SQLite runtime과 dependency lock fingerprint |
| `reference/drf` | Root Django lock을 바꾸지 않는 DRF 3.18.0 isolated `uv.lock`과 provenance |
| `runners/django` | 명시적인 Django/DRF observation과 GoDj decision-oracle scenario, type-preserving normalizer |
| `querybreadth` | QRY-022..033 전용 deterministic reference check/regeneration entrypoint |
| `queryexpression` | QRY-034..053 전용 deterministic reference check/regeneration entrypoint |
| `runners/godj` | M1 read부터 Article Bearer API, explicit migrate와 migration writer까지 제품 package를 실행하는 스물세 GoDj observation adapter와 immutable actual-handler registry |
| `relationproduct` | checked-in generated cross-app fixture, generated project bridge와 REL-001 actual observation root |
| `relationqueryproduct` | current app main/metadata와 project-owned relation-query fixture의 REL-004 actual SQLite observation root |
| `relationobjectproduct` | checked-in generated relation-object fixture와 REL-003/006 actual SQLite observation root |
| `relationreverseproduct` | current exact eight-file generated reverse-relation fixture와 REL-005 actual SQLite observation root |
| `relationprefetchproduct` | current exact nine-file generated reverse-prefetch fixture와 REL-012 actual two-query SQLite observation root |
| `relationselectproduct` | current exact eleven-file generated one-hop forward select-related fixture와 REL-009/010/011 actual SQLite observation root |
| `relationdeleteproduct` | current exact twelve-file generated prerequisite와 facade를 합친 exact thirteen-file fixture; REL-002/007/008 actual SQLite observation root |
| `oracles/**/*.json` | 정확한 provenance에 묶인 byte-deterministic expected reference observation |
| `oracles/**/SHA256SUMS` | checked-in oracle byte checksum |
| `internal/protocol` | strict decoder/validator/canonical value, all-observed comparator와 required-observed product comparator |
| `fixtures/godj*.json` | 미구현 상태가 pass되지 않는 set별 protocol fixture와 reviewed sparse deviation expectation |
| `codegenbootstrap` | Q-001 package bootstrap 실행 실험 |
| `lifecyclefence` | GDJ-0017 revision-fence test-only SQLite feasibility와 current-gap characterization |
| `definitionload` | Current format/load ownership과 opaque lifecycle authority를 검증하는 focused gate |
| `projectcheck` | GDJ-0021 descriptor/discovery/process/protocol test-only feasibility gate; product package가 아님 |
| `projectmigrateproduct` | GDJ-0049 actual global SQLite/PostgreSQL black-box E2E root; product status 입력은 별도 `runners/godj` migration-command adapter가 소유 |
| `migrationwriterproduct` | Repository-external temp Go module에서 public project runner/API만 사용하는 SQLite generate/migrate/no-op/restart actual root; product status 입력은 별도 `runners/godj` migration-writer adapter가 소유 |
| `runserverproduct` | Global `godj runserver` lifecycle, actual SQLite/PostgreSQL Article와 authenticated Admin/API required sentinels |
| `cmd/godjcheck` | GoDj observation을 생성해 provenance-locked expected reference와 비교 |

각 machine-readable manifest는 해당 contract set 실행 입력의 정본입니다. Profile ID,
ordered contract ID/position, phase와 payload dimension이 suite를 선택 manifest에 묶으며
다른 set의 oracle을 섞으면 validation이 실패합니다. 사람이 보는 진행 상태는
`docs/status/IMPLEMENTATION_MATRIX.md`가 요약하며 두 파일의 상태를 같은 변경에서
갱신합니다.

현재 wire format은 protocol v2입니다. v2는 contract manifest의 expected phase를
필수화하며 v1 profile, manifest와 observation suite를 조용히 받아들이지 않습니다.

## GDJ-0042 runserver product gate

`conformance/runserverproduct`는 Darwin/Linux에서 actual global CLI를 build하고 strict optional
`runserver_package`가 지정된 Article project를 실행합니다. 한 번 retained selection한 project에서 declaration runner를
한 번 build/run하고 current bundle을 계산한 뒤 `CheckRoot` → isolated readonly runtime build → `CheckRoot` 순서를
통과해야만 exact `<private-binary> serve --listen <loopback-address>` child를 시작합니다. Actual test는 project tree를
수정하지 않으며 global 명령이 generate publication, migration 또는 reload를 실행하지 않는 경계를 고정합니다.

SQLite sentinel은 pre-migrated file-backed DB의 exact nine rows/history를 준비하고 같은 concrete port의 repeated
start/stop, advanced Article HTTP response, durable DB state, exact declaration/runtime build 대상, stale/missing/interrupted
pre-start failure, process group과 private temp residue 0을 관찰합니다. PostgreSQL sentinel은 isolated schema에 같은 nine
rows를 준비하고 같은 HTTP/clean-interrupt/durable-state/project-tree-no-write 흐름과 secret-free global output을 관찰합니다.

Local PostgreSQL을 required로 실행할 때는 test-only connection URL을 다음처럼 전달합니다.

```bash
GODJ_TEST_POSTGRES_URL='postgresql://…' \
GODJ_REQUIRE_POSTGRES=1 \
  go test -count=1 -run '^TestGlobalRunserverArticlePostgresDevelopmentLoop$' \
  ./conformance/runserverproduct
```

`GODJ_REQUIRE_POSTGRES=1`인데 URL이 없으면 failure이고, required PostgreSQL CI에서는 skip 0 sentinel로 잠급니다. Test
harness는 test-only 변수를 runtime environment에서 제거하고 example-owned
`GODJ_ARTICLE_POSTGRES_URL`/`GODJ_ARTICLE_POSTGRES_SCHEMA` pair만 application에 전달합니다. 일반 portable 실행에서
PostgreSQL URL이 없으면 이 한 test만 명시적으로 skip하고 SQLite/process/unit gate는 계속 실행합니다.

Submitted `2bfdbd5...`의
[EVID-122](../docs/status/TEST_EVIDENCE.md#evid-20260824-122--gdj-0042-corrected-exact-head-hosted-completion) /
run `32659704239`에서 네 portable product 좌표의 SQLite/stale/forced-cleanup required sentinel 합계
12 pass·skip 0과 PostgreSQL 17.10의 기존 12+runserver required sentinel 13 pass·skip 0이 exact
27/27 jobs·358/358 steps 안에서 통과했습니다. 일반 portable PostgreSQL skip 정책 자체는 그대로입니다.

GDJ-0044는 같은 opt-in site에 authenticated Article Admin/API sentinel을 추가했습니다. Submitted source
`d9c1971...`의 [EVID-126](../docs/status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion) /
CI #142는 네 portable 좌표에서 기존 세 개와 authenticated Admin/API를 합친 required 16/16·skip 0,
PostgreSQL 17.10 job에서 Article API와 global runserver를 포함한 required 15/15·skip 0을 통과했습니다. 일반 portable
PostgreSQL skip 정책과 development-only/non-loopback exclusion은 바뀌지 않습니다.

Black-box Article HTTP는 response와 durable state 증거이며 in-process exactly-two-query instrumentation이나 Django
differential comparison을 수행하지 않습니다. `go test -race`도 harness/in-process lifecycle을 instrument할 뿐 test가 일반
`go build`로 만든 global/runtime child까지 race-instrumented됐다는 뜻은 아닙니다. No-shell process-group streaming,
SIGINT/bounded force/direct reap, output failure와 held pipe의 세부 fault injection은 `internal/projectcheck` unit/process test가
소유합니다. Windows, production serving, non-loopback/TLS와 daemonized descendant 보장은 이 package의 claim이 아닙니다.

## GDJ-0044 parameter routing and Article API gate

별도 exact profile `drf-3.18.0-django-6.1-sqlite-darwin-arm64`은 DRF 3.18.0 tag
`11875a38f483cea69d8ef2fd9ede6b96fb602ec4`, Django 6.1, CPython 3.14.3, SQLite 3.50.4와 isolated
`reference/drf/uv.lock`을 고정합니다. Existing Django-only profile/oracle/root lock은 다시 쓰지 않습니다.

`parameter-routing-manifest.json`의 WEB-028..035와 `article-api-manifest.json`의 API-001..010은 서로 독립적인
reference scenario와 oracle-blind GoDj actual을 사용합니다. Product는 exact `13 passing + 5 deviation`입니다.
WEB-028/029의 eight sparse selectors는 Verified DEV-0006, API-001/003/010의 six sparse selectors는 Verified
DEV-0007만 허용하고 root-list comparator도 selector/type/count를 fail-closed하게 검증합니다. 두 actual comparison은
각각 8/8과 10/10에서 unexpected difference 0입니다.

GDJ-0046 terminal checkout의 global reference는 21 sets/239 contracts/420 ordered bindings=
`211 passing + 16 deviation + 12 oracle_locked`, product는 20 adapters/227 eligible contracts=
`211 passing + 16 deviation`이었습니다. SYS-001..020 actual은 global registry/Makefile에 게시됐고
locked/unregistered range는 MIG-075..086뿐이었습니다. 그 terminal four-coordinate relation inventory는
1,054/1,054/0, 108,991 bytes, SHA-256
`ec137c064b8eb1f8b5db119e51d92a8034c12c0df1adf503f47efbd261081ce3`입니다.

Current Phase E final local `make ci`, Linux/386, 1,055-file external archive와 source audit는
[EVID-133](../docs/status/TEST_EVIDENCE.md#evid-20260826-133--gdj-0046-phase-e-frozen-source-and-corrected-local-final), exact
hosted gate와 PostgreSQL/live-attestation sentinels는
[EVID-134](../docs/status/TEST_EVIDENCE.md#evid-20260826-134--gdj-0046-corrected-exact-head-hosted-completion)에 기록됩니다.
OpenAPI, browsable API, Bearer/token auth, nested/bulk serializer, non-cooperative writer, distributed coordination,
Channels/Realtime와 production readiness는 이 GDJ-0046 gate가 증명하지 않았습니다.

## GDJ-0047 API authentication profile product-publication gate

`api-authentication-manifest.json`은 exact DRF/RFC/GoDj authority를 분리한 AUT-009..016/API-011..012 열 계약을
게시합니다. AUT-009/010/011/014/016/API-011/012 일곱 개는 `passing`이고 AUT-012/013/015 세 개는
Verified DEV-0009 `deviation`입니다. Sparse expectation은 invalid/inactive token detail/challenge 네 개,
permission challenge 한 개와 invalid-Bearer-with-session detail/challenge 두 개, 모두 합쳐 exact 일곱
`result` replacement만 허용합니다. DB/token-table selector는 없습니다.

Oracle/expected/deviation fixture를 읽지 않는 스물한 번째 GoDj actual adapter는 열 계약 모두를 관찰하고 local exact
comparison 10/10, unexpected difference 0을 통과했습니다. SQLite Article Bearer user-flow E2E도 통과했습니다.
Corrected current source commit `14e47c9ba18a698cae52f7167c53148cd552f175`, tree
`1b2c9c742bf66cc65e105e961a3dcfc02fa2c404`의 digest-pinned Linux/amd64 Go 1.26.5 + PostgreSQL 17.10
fingerprint에서 같은 Article Bearer flow normal/race/CGO0과 two-process attestation도 통과했습니다.

GDJ-0048 correction source에서 갱신한 checked attestation은 current behavioral source 257 files/2,942,402 payload
bytes/SHA-256 `e6798d648d1023c00375f61009428106e4a4274d502a51cbf46613a3d185ba71`에 묶인 1,134
bytes/SHA-256 `ef0e2e69ec4b79e44d85d455544164deec85b924a3ad1f872534ff1bd919d108`입니다. Attestation checksum
file은 103 bytes/SHA-256 `068274321f10e08a854085630505acc8415402bfa192417eeba48134b8acefae`입니다.
`make godj-conformance`와 current relation inventory 1,073/1,073/0, affected normal/race/CGO0/vet/generate도 같은
source에서 통과했습니다. Historical EVID-139/140은 이전 exact source에 대한 증거로 보존하고, EVID-141의 current
required PostgreSQL 18/18·skip 0, normal/race/CGO0/service restart/vet/clean과 checked capture byte comparison을
local proof로 사용합니다. EVID-142/CI #159는 네 hosted relation 좌표의 exact 1,073/1,073/0·111,158 bytes·
`be3344a3...9ee6`와 PostgreSQL required 18/18·required skip 0을 포함해 27/27 jobs·360/360 steps로 이 경계를
hosted-verify했습니다.

GDJ-0049 Phase A는 MIG-087..098 project-linked migrate exact 12를 independent oracle-blind decision manifest,
oracle과 payload-free not-implemented fixture로 처음 게시했습니다. 당시 bytes는 reference-only `oracle_locked`였고
`conformance/projectmigrateproduct`의 partial SQLite checkpoint도 product status 입력이 아니었습니다. 이 historical
characterization은 보존하되, completed Phase B-E에서 separate migration-command adapter의 actual observation이 일치해
MIG-087..098은 current product `passing`으로 전환됐습니다. Retired MIG-075..086 artifact/runner는 재사용하지 않았습니다.

별도의 GDJ-0047 completion snapshot에서 reference는 23 sets/261 contracts/506 ordered bindings=
`218 passing + 19 deviation + 24 oracle_locked`, product는 21 adapters/237 eligible contracts=
`218 passing + 19 deviation`이었습니다. Initial exact submitted-head run `33044776835`는 26 jobs success 뒤 macOS Intel
product job 하나가 30분 외곽 제한에서 취소됐고 완료된 steps와 수집 로그의 제품 assertion failure 표식은 0이었습니다.
EVID-137의 Intel-only 45분 correction과 corrected full `make ci`, Linux/386, 1,077-file external archive refreeze 뒤
corrected head `5f97fa8...`, tree `2b53c031...`의 EVID-138/CI #155 run `33049861740`이 exact
27/27 jobs·360/360 steps success, failure/cancel/skip/annotation 0으로 terminal acceptance를 닫았습니다. GDJ-0047이
고정한 GoDj-owned manifest provenance `kind=proposal`, `reference=GDJ-0047`, `derived=false`는 historical bytes이며
later acceptance로 소급 변경하지 않습니다. JWT/opaque token issuance, refresh family, OAuth/OIDC,
signing/validation key lifecycle, production BFF, OpenAPI와 browsable API는 포함하지 않습니다.

Current reference는 25 sets/281 contracts/600 ordered bindings=
`237 passing + 24 deviation + 20 oracle_locked`, product는 23 adapters/261 eligible contracts=
`237 passing + 24 deviation`입니다. 남은 locked/unregistered range는 MIG-075..086과 MIG-111..118입니다. Accepted ADR-0051과
completed GDJ-0049는 EVID-146/CI #164 run `33247166995`에서 submitted tree `b82bb5b...`의 41/41 jobs·464/464 steps,
PostgreSQL normal/race/CGO-disabled 각각 required 20/20·skip 0과 전체 relation coordinate/mode job을 통과했습니다.
GDJ-0049 Phase A의 reference-only 상태는 historical snapshot이며 현재 status로 읽지 않습니다. Writer/autodetector,
target/reverse/custom operation과 general upgrade/repair는 이 bounded acceptance에 포함되지 않습니다.

GDJ-0051 Phase A는 MIG-111..118을 별도 migration-status set으로 추가했습니다. MIG-112..115의 portable
comparison은 Django result만 소유하고 durable DB/process no-mutation을 reference runner 내부 counter로 위조하지
않습니다. Manifest/NI/oracle은 5,311/1,566/39,478 bytes와 SHA-256
`3b1c8693359cb465879a90a931f3bc778c141dafcee9fdb64b4a890e29f5e6fc`/
`0dd4dd08b13b9497ea541b7de4a85448cf6e0358b899c095c6eaafaf290f6cc6`/
`5a7a7827b37594b5084a25567fedd65152bfb05b5783cdf9e052bdc4d6d9355f`로 고정됐습니다. Shared 22-entry
`SHA256SUMS`는 2,077 bytes/SHA-256 `a6b29f8b947c9150ddc09c3cad261f423503cf3aa232cb3e2d2007d0161bd762`
이고 all-scenario semantic payload는 281 scenarios/971,815 bytes/SHA-256
`7c76a6cf1894b9877e80998a0d6731fbe4ee8df1b7813b762da22b3fc61f1784`입니다. 이 Phase A는 product adapter를
등록하지 않으며 SQLite/repository-external actual은 Phase C의 다음 검증 경계입니다.

GDJ-0050 Phase A는 MIG-099..110을 별도 migration-writer set으로 추가합니다. MIG-099/100/101/103/104/105/106은
Django 6.1 autodetector와 `makemigrations --dry-run/--check` 관찰을 authority로 삼고,
MIG-102/107/108/109/110은 GoDj deterministic current-document, fail-closed delta, private snapshot protocol,
atomic publication과 interruption recovery 결정을 authority로 삼습니다. Manifest/NI/oracle은 각각
8,864/1,876/25,980 bytes와 SHA-256 `75b3f485...5d1e`/`b27563a8...5502`/`9068d0e6...fd41`로 고정됩니다.
이 Phase A historical set은 reference `oracle_locked`였고 `godj-conformance` product adapter, MIG-075..098 상태 또는 당시
제품 22 adapters/249 contracts를 변경하지 않았습니다. Historical manifest/NI/oracle bytes는 current status publication으로
소급 rewrite하지 않습니다.

GDJ-0050 Phase D current manifest는 9,227 bytes/SHA-256 `90bce609...f1c72`, DEV-0010 sparse expectation은
7,242 bytes/SHA-256 `74617f20...4718e`입니다. MIG-099/100/101/102/108/109/110은 `passing`, MIG-103..107은
Verified DEV-0010 `deviation`입니다. Exact 열아홉 result replacement는 `PROTECT` vs `CASCADE`, digest-derived name,
flat `.godj.json` roster/canonical JSON output와 stable GoDj error taxonomy만 소유합니다. MIG-107은 Django parity가 아니라
Phase-A GoDj decision-oracle taxonomy를 production `unsupported_change`/`invalid_relation` taxonomy로 supersede한 명시적
deviation입니다. PostgreSQL 17.10 normal/race/CGO-disabled actual과 repository-external public module에 이어 Phase E
full `make ci`, Linux/386 compile-only, relation/archive와 source-bound attestation recapture도 locally 통과했습니다. Exact
submitted tree `48994a0...`는 EVID-153/CI #171 run `33280434425`의 same-head failed-job rerun 뒤 effective
41/41 jobs·464/464 steps로 corrected exact-head Hosted terminal gate를 통과했습니다.

## GDJ-0045 durable system-state reference gate

GDJ-0045가 게시한 `system-state-manifest.json`의 legacy SYS-001..012 range는 mixed authority로 고정됩니다. Django 6.1은 durable
credential/session/logout/Admin audit 관찰을, Accepted ADR-0047은 GoDj 운영 경계를 소유합니다. SYS-008의 durable
logout 의미는 Django authority이고 anonymous JSON API 403은 기존 Accepted ADR-0046 authority입니다. SYS-009의
restart 전 masked token 네 selector만 Verified DEV-0008 expected로 닫습니다. Current source manifest는
SYS-001..008/010..012 `passing`과 SYS-009 `deviation`이고, Go actual은 global registry, Makefile과
`godjcheck` fail-closed deviation policy에 게시됐습니다. Exact submitted head `e673b3a...`의
[EVID-129](../docs/status/TEST_EVIDENCE.md#evid-20260825-129--gdj-0045-corrected-exact-head-hosted-completion) /
CI #146이 필수 GitHub Actions 27/27 jobs·359/359 steps와 PostgreSQL required 16/16·skip 0을 통과해
ADR-0047 Accepted, DEV-0008 Verified와 GDJ-0045 hosted completion을 닫았습니다.

GDJ-0045의 frozen SYS-001..012 canonical manifest subsuite/oracle은 각각 6,412/13,099 bytes와 SHA-256
`40a91f1bb18bb5541f2d74270c8b64b416b9af0e63a0563988cdd7b1dd2b0bd7`/
`4b1cf9a63308c2f9ad9ac385c24e35ffec8f94546d80ed933dcf32edcb5a34bb`입니다. 당시 pretty manifest 전체는
7,730 bytes/SHA-256 `f570cadb322ce7587a70fc4cbbf69bd7d9b1641b31719c42ed00509dc807af44`였습니다.
Local global actual A/B는 각각
12,944 bytes, SHA-256 `f30ac1a42b43b037067865b37a902bc2f07de187c0bf512712bc9c058d41c3a6`로 byte-identical하고
12 contracts가 DEV-0008 reviewed product expectation과 일치했습니다. Exact profile에서 oracle을
재생성하거나 checked-in bytes를 검증하려면 historical uv 0.10.12를 명시해 다음을 사용합니다.

GDJ-0046 Phase A 당시에는 같은 set에 Proposed ADR-0048 authority의 SYS-013..020을 `oracle_locked`로 append했습니다.
그 Phase A snapshot의 manifest/NI/oracle은 각각 11,151/2,417/21,338 bytes와 SHA-256 `2dadfd5eeb66a591c1e305dfff65a10bee58ce3766b238d3ea96a968f27b427a`/
`92b05690265f6ffaa56dcc2a4e309d308c65e9b318d2557ed769c1daf89682fa`/
`6e5042b2003dc16840c63b08c708635eb08ccbaa6865c5fd8d89ad4d5542d83c`였습니다. 당시 product actual registry는 legacy
12개만 required였고 full 20-entry actual의 나머지 8개는 payload-free `not_implemented`였습니다.

GDJ-0046 Phase E publication은 Accepted ADR-0048 아래 SYS-013..020을 모두 product `passing`으로 전환했습니다. 그
manifest/oracle/root `SHA256SUMS`는 각각 11,143/21,242/1,791 bytes와 SHA-256
`b326cc3379f5792d67425005652e113c4e548c3bd0302b945659c573d336af09`/
`d83bf0c987f246a605253fea050cc82218f7b9cf744b94e150033393099c05b4`/
`e69c745711babce2f54db98bf32e2ecf6340b4419c693ea6a2642ec7cb3ebddd`입니다. 당시 checked PostgreSQL attestation은
1,134 bytes/SHA-256 `52fc003389b9131cf11a1da0deb013be18c0571503a012eb11b6cd31e04cc1ca`, sibling checksum file은
103 bytes/SHA-256 `29d08917e71083bb1aedc99d70c91fa449541a89cf08e1b74cf3b72ecf7f518a`입니다. Attestation은 current
behavioral source 250 files/2,855,113 payload bytes/SHA-256
`b0356da11869a1bfaf8573ea0734913f56529d9acfe25dd68b4aeaadcb72abb8`에 fail-closed하게 묶입니다.

```bash
LC_ALL=C TZ=UTC PYTHONDONTWRITEBYTECODE=1 uvx --from uv==0.10.12 uv run --frozen \
  python -m conformance.systemstate.reference --write
LC_ALL=C TZ=UTC PYTHONDONTWRITEBYTECODE=1 uvx --from uv==0.10.12 uv run --frozen \
  python -m conformance.systemstate.reference
LC_ALL=C TZ=UTC PYTHONDONTWRITEBYTECODE=1 uvx --from uv==0.10.12 uv run --frozen \
  python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/system-state-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json --check
go test ./conformance/internal/protocol ./conformance/runners/godj \
  ./conformance/systemstate/restart -count=1
tmpdir="$(mktemp -d)"
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/system-state-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json \
  -deviation-expected conformance/fixtures/godj-system-state-deviation-expected.json \
  -system-state-postgres-attestation \
    conformance/systemstate/attestations/postgresql-17.10-two-process-v1.json \
  -actual-output "$tmpdir/system-state-actual.json"
```

일반 uv 0.12.3은 portable test에는 사용할 수 있지만 embedded exact profile의 oracle regeneration은 version mismatch로
fail-closed합니다. GDJ-0045의 single-runtime SQLite/PostgreSQL restart evidence는 EVID-129에 보존됩니다. GDJ-0046은
anonymous-pipe barrier를 쓰는 실제 두 process SQLite actual과 checked live attestation을 결합한 PostgreSQL actual을 추가했습니다.
그 GDJ-0046 required PostgreSQL 17.10 lane은 `GODJ_TEST_POSTGRES_URL`, `GODJ_REQUIRE_POSTGRES=1`과 explicit capture path를 사용해
17/17 named pass·skip 0, checked bytes `cmp`, normal/race/CGO0/service-restart/vet/clean을 통과했습니다. Portable actual은
PostgreSQL을 live 실행했다고 주장하지 않고 strict attestation 검증 결과만 합성 입력으로 사용합니다.

## Exact profile

초기 profile은 다음 조합에만 oracle identity를 주장합니다.

```text
Django 6.1 / commit fe0a859f537d4238cf49fca39073513206f83122
CPython 3.14.3 managed by uv
SQLite 3.50.4 / locked sqlite_source_id
darwin / arm64
TIME_ZONE=UTC / USE_TZ=true / locale=C / LANGUAGE_CODE=en-us
uv 0.10.12 / uv.lock SHA-256 pinned in the profile
```

CPython 3.14.3은 Django 6.1이 지원하는 Python minor에 속하지만 2026-08-07의 최신
3.14 micro는 아닙니다. 이 profile은 “최신 공식 Python profile”이 아니라 GoDj가
실제로 재생하고 고정한 conformance reference입니다. Runner는 fingerprint나 lock이
하나라도 다르면 oracle을 쓰기 전에 실패합니다.

일반 Linux CI는 이 darwin oracle을 재생성했다고 주장하지 않습니다. Portable
normalizer/scenario test와 checked-in artifact validation만 실행합니다. 권위 있는
regeneration은 exact profile을 가진 환경에서 `make check`로 수행합니다.

GoDj SQLite backend는 Django reference와 별도 실행 환경입니다. 현재 module pin은
`modernc.org/sqlite v1.56.0`이고 내장 SQLite는 3.53.3입니다. Django reference의
SQLite 3.50.4 fingerprint를 Go backend 정보로 덮어쓰지 않으며, 차등 비교는 계약된
외부 동작을 비교합니다.

API exact regeneration은 위 Django-only profile이 아니라 별도 DRF profile과 isolated lock을 요구합니다. Exact
Darwin/arm64 job은 두 profile을 각각 strict fingerprint로 재생하고 parameter/article oracle checksum을 `--check`로
검증합니다. Portable job은 checked-in artifact와 actual comparison만 검증하며 DRF oracle을 재생성했다고 주장하지 않습니다.

## 실행

의존성을 잠긴 상태로 설치합니다.

```bash
uv sync --frozen
```

일반 CI 범위를 실행합니다.

```bash
make ci
```

Exact profile에서 oracle 재생까지 확인합니다.

```bash
make check
```

Oracle을 의도적으로 다시 만들 때만 다음을 실행하고 diff를 검토합니다.

```bash
make oracle-regenerate
git diff -- conformance/oracles
```

두 번째 write/migration oracle만 직접 확인할 수도 있습니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/write-migration-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json \
  --check
```

Save lifecycle oracle도 같은 runner에 세 번째 manifest를 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/save-lifecycle-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  --check
```

QuerySet evaluation/cache oracle은 네 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/query-cache-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  --check
```

Migration planning oracle은 다섯 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-planning-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  --check
```

Migration plan execution oracle은 여섯 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-execution-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  --check
```

Recorder-backed restart planning oracle은 일곱 번째 manifest와 전용 output을 함께 넘겨
확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-restart-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  --check
```

Historical ProjectState reconstruction oracle은 여덟 번째 manifest와 전용 output을 함께
넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  --check
```

Migration lifecycle oracle은 아홉 번째 manifest와 전용 output으로 확인합니다.

```sh
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-lifecycle-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json \
  --check
```

Migration project-check decision oracle은 열한 번째 manifest와 전용 output으로 확인합니다.

```sh
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-project-check-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json \
  --check
```

두 observation을 직접 비교할 수 있습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json \
  -actual path/to/godj-observation.json
```

현재 GoDj read 구현을 oracle과 직접 비교하려면 다음을 실행합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
```

Write/migration set도 같은 command에 두 번째 manifest와 oracle을 넘겨 직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json
```

QuerySet evaluation/cache 제품 adapter도 같은 command에 네 번째 manifest와 oracle을
넘겨 직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json
```

Migration planning 제품 adapter도 같은 command에 다섯 번째 manifest와 oracle을 넘겨
직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json
```

Static baseline은 별도 비교에서 예상 exit 1과 ordered 12 status mismatch를 계속 내야
합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  -actual conformance/fixtures/godj-migration-planning-not-implemented.json
```

Manifest에 등록되지 않은 migration-planning scenario는 `godjcheck`가 exit 2로 거부하고
actual output을 쓰지 않습니다.

Migration plan execution 제품 adapter는 locked Django oracle을 먼저 strict self-compare한
뒤 code-owned DEV-0001 selector와 sparse expectation으로 product expectation을 구성합니다.
Deviation fixture가 없거나 selector/status/provenance가 등록 정책과 다르면
`godjcheck`가 exit 2로 거부하고 actual output을 쓰지 않아야 합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-execution-deviation-expected.json
```

Static baseline은 별도 비교에서 예상 exit 1과 MIG-017..026 ordered 10 status mismatch를
계속 냅니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -actual conformance/fixtures/godj-migration-execution-not-implemented.json
```

Recorder-backed restart 제품 adapter는 일곱 번째 manifest와 locked oracle을 직접
비교합니다. Adapter는 file-backed database를 새 backend로 다시 열어 fresh read/check/plan
경계를 실행합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json
```

Static baseline은 별도 비교에서 MIG-027..036 ordered 10 status mismatch를 계속 냅니다.
Manifest에 등록되지 않은 recorder-restart scenario는 현재도 `godjcheck`가 exit 2로
거부하고 actual output을 쓰지 않습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual conformance/fixtures/godj-migration-restart-not-implemented.json
```

Historical-state static baseline도 MIG-037..046 ordered 10 status mismatch를 냅니다. 실제
`godjcheck` adapter는 public reconstructor와 live database observation으로 10개를 실행하며,
manifest에 등록되지 않은 scenario만 exit 2/no output으로 거부합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  -actual conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json
```

Revision-fenced migration lifecycle 제품 adapter는 public `Executor.Migrate`와 live SQLite
database로 MIG-047..056을 실행합니다. MIG-052의 canonical sibling order는 locked oracle을
바꾸지 않고 DEV-0002 sparse expectation으로만 대체합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-lifecycle-deviation-expected.json
```

Migration definition source 제품 adapter는 public `migrations/definition.Load`와 opaque
`LoadedDefinitionSet`을 받는 `Executor.Migrate`를 실행합니다. MIG-057..064는 Django result parity가 아닌 Accepted ADR decision
set이므로 성공 문구는 `locked reference oracle`이며, Django-derived set의 기존
`locked Django oracle` 문구와 구분합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-definition-source-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json
```

Static not-implemented fixture 비교는 MIG-057..064의 ordered status mismatch 8개와 exit 1을
계속 내야 합니다. 제품 adapter는 expected 값을 actual로 복사하지 않으며 source/header/
operation/graph mutation이 non-empty diff 또는 success/error shape rejection을 만드는지 별도
false-green test로 확인합니다.

Migration project-check 제품 adapter는 actual global kernel과 production project-linked entrypoint를
실행해 MIG-065..074를 관찰합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-project-check-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json
```

Static fixture는 계속 MIG-065..074 ordered status mismatch 정확히 10개와 exit 1을 내야 합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-project-check-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json \
  -actual conformance/fixtures/godj-migration-project-check-not-implemented.json
```

Relation product adapter는 checked-in current generated bridge로 REL-001 metadata를 관찰하고, 별도
`relationqueryproduct`의 project-owned generated query bridge와 실제 SQLite rows로 REL-004의 두 forward
predicate case를 관찰합니다. `relationobjectproduct`는 independently generated descriptor seal/object bridge와
actual SQLite source rows로 REL-003 cold/warm object cache와 REL-006 null access/JOIN-0 `isnull`을 관찰합니다.
`relationreverseproduct`는 exact eight generated files의 project-only companion, freshly loaded owner accessor와
typed/dynamic reverse lookup을 actual SQLite에서 관찰합니다. `relationprefetchproduct`는 exact eight-file
reverse union에 project-only prefetch companion 하나를 더한 exact nine-file union을 사용해 owner SELECT 1회와 root
`author_id IN` batch SELECT 1회, JOIN 0, warm access 추가 query 0을 actual SQLite에서 관찰합니다.
`relationselectproduct`는 exact eleven-file union과 actual SQLite joined row scan으로 REL-009/010의
required INNER/nullable LEFT OUTER eager access를 관찰하고, 같은 resolver로 REL-011 reverse path를 pre-I/O
거부합니다. `relationdeleteproduct`는 current generator prerequisite를 exact twelve-file union으로 결정적으로
재생성하고 project facade 한 파일을 더한 exact thirteen-file generated union을 사용합니다. REL-002는 new source에 no-PK target을
할당한 뒤 Save가 query/mutation 0, database unchanged와 `model_state_error/unsaved_related_object`로 실패하는 것을
관찰합니다. 같은 실제 `NO ACTION`/`RESTRICT` FK와 pinned `BEGIN IMMEDIATE` transaction에서 REL-007의 전 incoming
edge `PROTECT` count 2와 mutation 0, REL-008의 `UPDATE(2) -> DELETE(1)`/caller key clear도 계속 관찰합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/relation-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json
```

성공 stdout은 정확히
`GoDj observations match the locked Django oracle for 12 contracts`입니다.
Product comparator는 registry가 요구하는 12개 contract를 oracle과 byte-semantic하게 비교합니다. 기존
all-observed adapter의 strict comparator 의미는 바뀌지 않습니다. 비교가 끝나기 전에는 `-actual-output`을
만들지 않으므로 status/payload/
registry mismatch는 exit 1 또는 2, 빈 stdout, output file 없음으로 fail-closed합니다.

Checked-in static `not_implemented` fixture는 의도된 mismatch입니다. Comparator test는 result value, list
order, phase, error category/code, contractual message, DB state, metrics를 각각 변형해
false green이 생기지 않는지 검증합니다.

Write/migration set은 GDJ-0004의 제한된 제품 수직 단면에서 `passing`입니다. Generated
create/patch API, generic Manager write, SQLite transaction과 migration executor/editor/
recorder를 실행해 MOD-001..007, MIG-001..004가 oracle과 일치합니다. Checked-in
`godj-write-migration-not-implemented.json`은 구현 전 상태가 pass되지 않는다는 것을
지속적으로 확인하는 false-green fixture이며, 현재 GoDj 실행 결과를 뜻하지 않습니다.

Save lifecycle set은 GDJ-0005에서 Django exact reference로 고정했고 GDJ-0006에서 실제
generated model, `Manager.Save`와 SQLite adapter를 연결해 12개 모두 `passing`입니다.
Checked-in `godj-save-lifecycle-not-implemented.json`은 구현 전 상태가 pass되지 않는다는 ordered
12-mismatch false-green fixture로 유지하며 현재 GoDj actual을 뜻하지 않습니다.

QuerySet evaluation/cache set은 GDJ-0007에서 QRY-011..021의 exact Django 결과와
provenance를 `oracle_locked`로 먼저 고정했습니다. GDJ-0008은 generated model, generic
QuerySet과 SQLite 실제 adapter를 연결해 11개 모두 `passing`으로 전환했습니다. 네
manifest의 contract ID/scenario는 전역으로 유일하고 모든 12개 ordered cross-pair가
validation에서 거부됩니다. GDJ-0008 완료 당시 `make godj-conformance`는 M1 11개,
M2 write/migration 11개, Save 12개와 QuerySet cache 11개, 총 45개를 실행했습니다. Manifest에 등록되지 않은 임의
unknown scenario는 계속 exit 2로 fail-closed하며 actual output을 쓰지 않습니다.

두 독립 Go query-cache actual은 각각 56,283 bytes이며 SHA-256
`c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`로 서로
byte-identical합니다. Django oracle은 56,426 bytes, SHA-256
`d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`이므로 양쪽 artifact가
byte-identical한 것은 아닙니다. Result/error/DB state/metrics의 계약 의미를 comparator가
0-diff로 판정한 것입니다. Checked-in `godj-query-cache-not-implemented.json`의 ordered
11 mismatch는 현재 제품 actual이 아니라 구현 전 false-green 회귀 증거로 그대로
유지합니다. Go-native singleflight/cancellation/deep-clone/terminal gate와 전체 명령은
[EVID-20260808-007](../docs/status/TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
기록합니다.

GDJ-0039는 QRY-022..033의 typed ordered projection, projection 밖 source field filter/order,
projection/model-cache 독립, distinct, stable offset/limit, cold/warm Count, sliced Count, nullable Max와
terminal rows ownership 결과를 새 query-breadth set에 고정하고 실제 Article ORM/SQLite adapter를 연결합니다.
Manifest 12개는 모두 `passing`이며 actual adapter는 pinned Django oracle과 12/12 zero-diff입니다.
`godj-query-breadth-not-implemented.json`은 ordered 12 mismatch false-green fixture로 남습니다.

QRY-032의 reference payload는 consumer stop과 decode/iteration/close ownership을 고정합니다. Go context
cancellation은 ORM unit gate에서 별도로 검증합니다. QRY-033 payload는 SQLite 결과 anchor만 소유하며, 실제
PostgreSQL parity와 cross-model compile rejection은 각각 Article PostgreSQL E2E와 generated compile gate가
따로 소유합니다. Manifest/oracle/static fixture는 각각 11,282/41,943/1,867 bytes와 SHA-256
`04665808...`/`0236bdab...`/`f618ca12...`입니다. 현재 reference inventory는 migration-relation diagnostic을
포함해 14 sets/151 unique contracts와 182 ordered cross-bindings이고, product inventory는
13 adapters/139 contracts(134 passing + 5 reviewed deviations)입니다. Final source commit `695916c8...`의
artifact, actual adapter, generated bundle, local PostgreSQL, full/386/source-clean-copy와 independent audit 결과는
[EVID-109](../docs/status/TEST_EVIDENCE.md#evid-20260823-109--gdj-0039-typed-query-breadth-source-frozen-local-checkpoint)에
기록합니다. Submitted head `253455d...`의
[EVID-110](../docs/status/TEST_EVIDENCE.md#evid-20260823-110--gdj-0039-exact-head-hosted-completion) / run
`32634741186`는 exact 27/27 jobs·341/341 steps, QRY-022..033 actual 12/12, PostgreSQL 17.10 required actual
12/12·skip 0을 통과했습니다. 따라서 이 bounded 단면은 `Verified`, GDJ-0039은 completed입니다.
Exact profile에서 재생성하고 일반 환경에서 bytes를 확인하려면 다음을 사용합니다.

```bash
uvx --from uv==0.10.12 uv run --frozen python -m conformance.querybreadth.reference --write
uv run --frozen python -m conformance.querybreadth.reference
```

GDJ-0040 Phase A는 QRY-034..043 scalar Boolean composition set을 reference-only로 고정했습니다. 독립 Django
scenario는 exact/public `Q`와 `QuerySet` 동작만 관찰하며 checked-in expected artifact를 읽지 않습니다. 새
manifest의 10개 status는 모두 `oracle_locked`, oracle observation은 `observed`, static fixture는 ordered 10개
`not_implemented`입니다. Manifest/oracle/static fixture는 exact 8,135/41,264/1,715 bytes와 SHA-256
`8ed9ef62b568a2bf4843e3136574c3d73d5571ddd4fe7f1efad0493c7300e895`/
`8b087a394b52620b84d510d6981e77171179ac3690fda738261bf64bea00583e`/
`0df907357fcab944272eb45158189e68520e3567678c57995e05c5a0feccbffb`입니다.

Reference inventory는 exact 15 sets/161 unique contracts+scenarios/210 ordered cross-bindings의
`134 passing + 5 deviation + 22 oracle_locked`입니다. Product inventory는 query-expression adapter를
등록하지 않은 exact 13 adapters/139 contracts의 `134 passing + 5 deviation + 0 oracle_locked`로 불변입니다.
Focused scenario 7/7, exact focused runner 60/60, portable focused runner 60 tests/16 expected skips와 전체
portable Python 236 tests/21 expected skips를 통과했습니다. Exact semantic registry는 161 scenarios/
702,415 bytes/SHA-256 `aa0d321264e0ad9eed1818d1530a51d18592c16d509c51417e4bdf598655b10e`이며 reference drift와
oracle/static 두 `contractcheck`도 통과했습니다. 이 Phase A 결과는 GoDj Boolean tree/compiler/Article 제품
지원이나 QRY-034..043 `passing` 전환 증거가 아닙니다.

Exact profile에서 query-expression oracle을 재생성하고 checked-in bytes를 확인하려면 다음을 사용합니다.

```bash
uvx --from uv==0.10.12 uv run --frozen python -m conformance.queryexpression.reference --write
uv run --frozen python -m conformance.queryexpression.reference
```

GDJ-0040 Phase B/C는 위 Phase A reference bytes 중 oracle/static/checksum을 바꾸지 않고 manifest status만
10/10 `passing`으로 전환하고 oracle-blind GoDj/SQLite actual adapter를 등록했습니다. GDJ-0040 completion checkpoint manifest는
8,075 bytes/SHA-256 `e4160851da2e0820dc4f9f2e8c9e9c2d4d372cde426622b4fea5def51739ea69`입니다.
두 독립 actual은 각각 41,134 bytes/SHA-256
`20b5cf0a332d9d85394a2021fc0b1e8839f9e57994b9c278a7f8bcce8e5f918a`로 byte-identical하고 locked
oracle과 protocol difference 0입니다. Scenario registry는 정확히 10개이며 expected artifact를 읽지 않고 실제
ORM/SQLite plan, compiled SQL shape, DB state와 metrics를 관찰합니다.

GDJ-0040 completion checkpoint reference inventory는 exact 15 sets/161 unique contracts+scenarios/210 ordered cross-bindings의
`144 passing + 5 deviation + 12 oracle_locked`입니다. Product inventory는 14 adapters/149 contracts의
`144 passing + 5 deviation`이며 QRY-034..043 actual은 10/10 zero-diff입니다. Source/conformance commit과
affected local/audit 증거는
[EVID-112](../docs/status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)에
기록합니다. 이어진 [EVID-113](../docs/status/TEST_EVIDENCE.md#evid-20260823-113--gdj-0040-frozen-source-final-local-gates)은
initial full/386/775-file source-clean-copy를 통과했습니다. First hosted run의 stale 916-test workflow lock과
correction `73b912d...`의 current 950/950/0 refreeze는
[EVID-114](../docs/status/TEST_EVIDENCE.md#evid-20260823-114--gdj-0040-first-hosted-inventory-lock-failure-and-corrected-local-refreeze)에
기록합니다. Corrected submitted head `136e825...`의
[EVID-115](../docs/status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion) /
run `32642341459`은 exact 27/27 jobs·341/341 steps와 네 플랫폼 950/950/0을 통과해 Phase D를 닫았습니다.

GDJ-0041 Phase A는 같은 set에 QRY-044..053 typed range와 same-row field-reference observable을 추가했습니다.
신규 10개는 reference-only `oracle_locked`이며 QRY-034..043의 기존 observation prefix는 canonical SHA-256
`3eeadea95edffb87cac52dc9a7fca6b439bda31322fe797aa726a7909bd5483c`로 동일합니다. 그 Phase A
manifest/oracle/static fixture는 각각 16,652/87,852/2,465 bytes, SHA-256
`90adeee098285a3b6581a3d0029c22ee115351f21483f4d704101813bbe940e3`/
`4efa5c26f5f17c77e7ef65a0bbdb00cff72835c9a98642726bd61f5524e1ec6f`/
`7ab556ff1f6b77f5e1d4614d6d752cabd6f3428572558d39007e9cd15972f6c2`입니다. Exact Django 전체
239/239, reference regeneration/check와 oracle/static `contractcheck`가 통과했습니다. Reference inventory는
15 sets/171 unique contracts+scenarios/210 ordered cross-bindings의
`144 passing + 5 deviation + 22 oracle_locked`이고, product inventory는 actual 등록 전
14 sets/159 contracts의 `144 passing + 5 deviation + 10 oracle_locked`입니다. 이 Phase A는 QRY-044..053
GoDj actual이나 backend 제품 검증 증거가 아닙니다.

GDJ-0041 Phase B/C completion checkpoint source는 private literal/list/field RHS union과 source inventory validation,
Integer/String `gt`/`gte`/`lt`/`lte`, sealed same-model/same-kind `orm.F`, nullable RHS odd-`NOT` complement를
하나의 immutable Boolean tree에 구현했습니다. SQLite/PostgreSQL은 field RHS를 bind parameter가 아닌 quote된
identifier로 compile하며, bounded Article advanced filter는 invalid input DB I/O 0과 성공 시 projection+aggregate
정확히 두 query를 유지합니다. Oracle-blind GoDj/SQLite actual은 QRY-034..053 20/20, 신규 QRY-044..053 10/10
zero-diff입니다.

GDJ-0041 completion checkpoint manifest는 16,592 bytes/SHA-256
`a32365e72bff2f96d576dc2a6322c703c6f0cf7c277776f6b326eda47cf9de17`이고, Phase A oracle과 ordered NI fixture는
각각 87,852/2,465 bytes와
`4efa5c26f5f17c77e7ef65a0bbdb00cff72835c9a98642726bd61f5524e1ec6f`/
`7ab556ff1f6b77f5e1d4614d6d752cabd6f3428572558d39007e9cd15972f6c2`로 불변입니다. 두 독립 actual은
87,592 bytes/SHA-256 `c8762a8a728440e8b7c42c705aad9635f902100041c0171cdb121880b3813a7c`로 byte-identical합니다.
GDJ-0041 completion checkpoint reference inventory는 15 sets/171 unique contracts+scenarios/210 ordered cross-bindings의
`154 passing + 5 deviation + 12 oracle_locked`, product inventory는 14 adapters/159 contracts의
`154 passing + 5 deviation + 0 oracle_locked`입니다. Frozen source `7f2bb223...`의 local-final gates와 submitted
head `e97a4e3...`의 [EVID-118](../docs/status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) /
run `32647746430` exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual이 통과해
QRY-044..053은 hosted `Verified`, GDJ-0041은 completed입니다.

Migration planning set은 GDJ-0009에서 MIG-005..016의 exact Django 결과와 provenance를
`oracle_locked`로 고정했습니다. 다섯 manifest의 ID/scenario는 전역으로 유일하고 모든
20개 ordered cross-pair가 validation에서 거부됩니다. Oracle은 39,139 bytes, SHA-256
`7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474`, manifest는
10,623 bytes, SHA-256
`7e8f0d19c8f227721e7cfe4254a4f39d1313e801f1ea0a759e14c46a3dbbe876`, static fixture는
1,869 bytes, SHA-256
`a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13`입니다.
GDJ-0009 완료 당시 `make godj-conformance`는 제품 adapter가 있는 45개만 실행했습니다.
Fifth-set determinism, mutation, static mismatch와 fail-closed 증거는
[EVID-20260808-008](../docs/status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)에
기록합니다.

GDJ-0010은 `migrations.NewPlanner`, `NewAppliedState`, `Planner.Plan`과
`PlanningError`를 사용하는 실제 fifth adapter를 추가했습니다. Manifest는 10,551 bytes,
SHA-256 `f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6`이고
MIG-005..016이 모두 `passing`입니다. GDJ-0010 완료 당시 `make godj-conformance`는 다섯 제품 set의
11 + 11 + 12 + 11 + 12, 총 57개를 실행합니다.

두 독립 Go migration-planning actual은 각각 39,094 bytes, SHA-256
`eb5bf3b6f41855684582f67b3be675da42975b8fc1ed9c7085f6d35a078eac32`로 서로
byte-identical하며 Django oracle과 protocol 의미상 12개 0-diff입니다. Logical DB state와
zero metrics는 실제 DB probe가 아니라 pure structural planner capture에서 산출합니다.
Static ordered 12 mismatch, plan 하위값/dependency/error-code mutation과 adapter source
hardcode 금지 증거는
[EVID-20260808-009](../docs/status/TEST_EVIDENCE.md#evid-20260808-009--gdj-0010-immutable-migration-planner-product-slice)에
기록합니다.

Migration plan execution set은 GDJ-0011에서 MIG-017..026의 exact Django 결과와
provenance를 `oracle_locked`로 고정했습니다. 여섯 manifest의 ID/scenario는 전역으로
유일하고 모든 30개 ordered cross-pair가 validation에서 거부됩니다. Manifest는 8,720
bytes, SHA-256
`f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d`, oracle은 47,119
bytes, SHA-256
`641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`, static fixture는
1,685 bytes, SHA-256
`6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. External
metrics는 connection summary와 compact ordered steps만 포함하고, raw render/operation/
recorder/transaction trace는 runner 내부 assertion입니다. Historical before/after는
MIG-019에만 있고 MIG-023/024는 `fault_point=before_record_write`를 명시합니다. MIG-024는
최종 schema A1, records A1/A2와 `commit` phase를 고정합니다.

GDJ-0011 완료 당시 static fixture는 ordered 10 mismatch이고 product `godjcheck`는
exit 2/no output으로 fail-closed했습니다. 당시 30 cross-binding, semantic mutation,
exact regeneration과 full gate는
[EVID-20260808-010](../docs/status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)에
기록합니다.

GDJ-0012의 approved manifest는 9,120 bytes, SHA-256
`1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9`입니다. Django oracle과
static fixture bytes는 그대로 유지했고, sparse deviation expectation은 4,685 bytes,
SHA-256 `568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547`입니다.
두 독립 live Go actual은 각각 47,446 bytes, SHA-256
`f191d116cc38194e2019df358c31f752101fdacb005d9cc442b701d8d4afde4b`로 byte-identical합니다.
GDJ-0012 완료 당시 `make godj-conformance`는 여섯 제품 set을 실행했고 총 분류는 63
`passing` + 4 DEV-0001 `deviation`이었습니다. 상세 증거는
[EVID-20260808-011](../docs/status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록합니다.

Recorder-backed restart set은 GDJ-0013에서 MIG-027..036의 exact Django result와
provenance를 `oracle_locked`로 고정했습니다. 일곱 manifest의 ID/scenario는 전역으로
유일하고 모든 42개 ordered cross-pair가 validation에서 거부됩니다. Manifest는 10,225
bytes, SHA-256
`93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b`, oracle은 33,888
bytes, SHA-256
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, static fixture는
1,715 bytes, SHA-256
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. Static
fixture는 ordered 10 mismatch이고 product `godjcheck`는 exit 2/no output입니다. Recorder
presence/identity, alias, plan order/direction, unknown/known history, restart tail과
zero-mutation mutation 증거는
[EVID-20260808-012](../docs/status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)에
기록합니다. GDJ-0013 완료 당시 분류는 기존 제품 `63 passing + 4 deviation`에 새 10
`oracle_locked`를 더한 것이며 77 product passing이 아니었습니다.

GDJ-0014는 manifest status만 10 `passing`으로 전환해 10,165 bytes, SHA-256
`79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`로 만들었습니다.
Locked oracle과 static fixture는 각각 33,888 bytes/
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, 1,715 bytes/
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`로 유지됩니다. 두
독립 live Go actual은 각각 33,795 bytes, SHA-256
`f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`로
byte-identical하며 locked oracle과 semantic 0-diff입니다. GDJ-0014 완료 당시
`make godj-conformance`의 일곱 제품 set 분류는 `73 passing + 4 deviation`이고, static ordered 10 mismatch와 42
cross-binding은 계속 false-green gate입니다. 상세 증거는
[EVID-20260808-013](../docs/status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
기록합니다.

Historical ProjectState reconstruction set은 GDJ-0015에서 MIG-037..046의 exact Django
result와 provenance를 `oracle_locked`로 고정했습니다. 여덟 manifest의 ID/scenario는
전역으로 유일하고 모든 56개 ordered cross-pair가 validation에서 거부됩니다. Manifest는
9,257 bytes, SHA-256
`04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`, oracle은 89,997
bytes, SHA-256
`bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`, static fixture는
1,715 bytes, SHA-256
`9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. Logical
state는 loaded definition에서 replay하고 deliberately divergent live database는 전후
불변입니다. State의 app/model/field와 table/column/kind/primary-key/null/max-length/default,
request target/position, applied/graph와 DB/metrics mutation을 각각 검증합니다. Static
fixture는 ordered 10 mismatch이고 제품 `godjcheck`는 당시 exit 2/no output이었습니다. 기존
일곱 product set은 `73 passing + 4 deviation`이었으므로 GDJ-0015 완료 시 분류는
`73 passing + 4 deviation + 10 oracle_locked`, reference 총계는 87개였습니다. 상세 증거는
[EVID-20260808-014](../docs/status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
기록합니다.

완료된 [GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)은
Accepted [ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md)의 immutable
reconstructor와 explicit empty/latest/before/after/applied request, real SQLite recorder를
읽는 여덟 번째 adapter를 구현했습니다. Passing manifest는 9,197 bytes, SHA-256
`85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`입니다. Locked
oracle/static bytes는 유지됐고, 두 Go actual은 각각 89,867 bytes, SHA-256
`a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로
byte-identical하며 oracle과 protocol 의미상 10개 0-diff입니다. GDJ-0016 완료 당시 8 product
set의 분류는 `83 passing + 4 deviation`; 87 unique contract와 56 cross-binding gate를
유지합니다. 상세
증거는
[EVID-20260808-015](../docs/status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.

Migration lifecycle set은 GDJ-0017에서 MIG-047..056의 exact Django result와 provenance를
아홉 번째 manifest에 고정했습니다. Fresh/latest, applied prefix, fully-applied no-op,
named forward/reverse, app zero target, unknown legacy identity, explicit inconsistent-history
preflight, middle failure와 file-backed fresh restart를 다룹니다. Manifest는 13,680 bytes,
SHA-256
`23a9e919edff932ae781f0768aeaf7f184fe392ec53598fa18524cf50d979a8e`, oracle은 98,436
bytes, SHA-256
`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, static fixture는
1,681 bytes, SHA-256
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`입니다. 두 독립
random-hashseed process와 checked-in oracle은 byte-identical합니다.

아홉 set의 ID/scenario 97개는 전역으로 유일하고 72개 ordered cross-binding이 모두
거부됩니다. Static comparison은 MIG-047..056 ordered status mismatch 10개와 exit 1이고,
제품 `godjcheck`는 등록되지 않은 lifecycle scenario를 exit 2/no actual output으로
fail-closed합니다. 따라서 GDJ-0017 완료 당시 분류는 기존 `83 passing + 4 deviation`과 새
`10 oracle_locked`이며 reference 97개 전체를 제품 지원으로 표현하지 않습니다.

`lifecyclefence` spike는 현재 unfenced 조합의 first-write 전/step 사이 stale gap을 재현하고,
persistent epoch와 monotonic revision을 주 fence로, recorder identity fingerprint를 보조
integrity gate로 검증했습니다. 각 step은 pinned SQLite connection의 `BEGIN IMMEDIATE` 안에서
expected token을 확인하고 successor token, schema와 recorder를 함께 commit합니다. Conflict는
current/tail mutation 없이 last-durable `ProjectState`를 반환하고 자동 retry하지 않습니다.
Two connections/processes, bootstrap 경쟁, DDL/recorder 뒤 fault, BUSY/LOCKED 분류와 unsupported
capability fail-closed를 검증했지만 이는 제품 implementation이 아닙니다. 상세 증거는
[EVID-20260808-016](../docs/status/TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)에
기록합니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](../docs/adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 public
`Executor.Migrate`가 already-loaded definition을 exact-one opaque revision session으로 읽고,
SQLite `BEGIN IMMEDIATE` transaction에서 epoch/revision/fingerprint fence, schema, recorder와
successor token을 함께 commit하도록 제품화했습니다. Unsupported backend로 fallback하지 않고,
existing recorder의 자동 adoption도 하지 않습니다.

현재 lifecycle manifest는 13,735 bytes, SHA-256
`5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`입니다. Locked Django
oracle과 static fixture는 각각 98,436 bytes/
`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, 1,681 bytes/
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`로 유지됩니다.
DEV-0002 sparse expectation은 6,769 bytes, SHA-256
`58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`이며 MIG-052의
`result.plan[0]`, `result.plan[1]`, `result.plan[2]`, `metrics.steps[0]`, `metrics.steps[1]`,
`metrics.steps[2]`만 바꿉니다. 기존 DEV-0001 네 계약은 그대로입니다.

두 독립 Go actual은 각각 98,304 bytes, SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical하고
reviewed product expectation과 10-contract match입니다. 이때 확정된 9 product adapter의
역사적 분류는 `92 passing + 5 deviation`이며, 97 unique contract와 72 ordered
cross-binding gate를 유지합니다.

### Historical GDJ-0019..0022 artifact snapshots

다음 definition/project-check API, artifact hash와 집계는 각 완료 checkout의 증거입니다. Current
MIG-057..074 format/API/artifact는 이 문서 상단의 GDJ-0036 boundary를 따릅니다.

완료된 [GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)과 Accepted
[ADR-0019](../docs/adr/0019-versioned-migration-definition-source.md)는 caller-provided bytes,
strict data-only JSON v1, tuple `(1,1,1,2)`, closed `CreateModel`/non-PK `char`·`boolean`
`AddField`, atomic loader-owned snapshot, canonical digest와 stage-major failure precedence를
MIG-057..064로 고정했습니다. MIG-064는 public Django graph/executor의 reference-only success
observation이며 digest는 handoff observation metadata이지 executor argument가 아닙니다.

GDJ-0019 completion manifest는 5,195 bytes/SHA-256
`8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`, oracle은 29,851
bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture는
1,574 bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`입니다. 갱신된
`SHA256SUMS`는 959 bytes/
`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다. Exact Python은
164개 모두 통과했고 portable run은 149 passed/15 skipped입니다. 열 set의 105 ID/scenario와
90 ordered cross-binding도 검증했습니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 새 leaf package
`migrations/definition`에 파일 I/O 없는 `Load(...Source) (Set, LoadReport, error)`를
구현했습니다. Zero `Set`은 canonical empty set이고, source/document와 반환 accessor는
loader-owned deep copy입니다. Raw document는 set에 보존하지 않습니다. `Set.Migrate`는 fresh
definition copy와 immutable request value만 기존 `Executor.Migrate`에 정확히 한 번 넘깁니다.

Loader의 exact numeric cap은 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB,
JSON depth 64, document JSON values 65,536, batch JSON values 262,144, migration별 dependencies
2,047, operations 2,048, `CreateModel` fields 2,048입니다. Strict scanner는 invalid UTF-8/BOM,
trailing value, any-depth duplicate member, surrogate와 integer lexeme를 closed JSON 의미로
검증하고, canonical RFC 6901 failure order를 bounded lazy path comparator로 선택합니다.
Source-owned failure만 9개 `migration_definition_source_error` code로 분류하며 resource breach는
별도 code 없이 `reason=resource_limit_exceeded`와 stable limit/maximum/actual로 보고합니다.
Graph failure는 raw `*migrations.PlanningError`, lifecycle failure는 기존 raw error identity와
`errors.As` 의미를 보존합니다.

GDJ-0020 status-only manifest는 5,147 bytes/SHA-256
`688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`입니다. Oracle 29,851
bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture 1,574
bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`와 `SHA256SUMS`
959 bytes/`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`는 변경하지 않았습니다.
MIG-057..064는 decision-reference actual 8 `passing`이며 현재 10 adapter/105 contract의 제품
분류는 `100 passing + 5 deviation`; 90 ordered cross-binding gate도 유지합니다. 두 독립 product
actual은 각각 29,631 bytes/SHA-256
`a3f40f9bbee06d4edc4af0a00f40a76da259207995ac20d030101aa2ec3aec87`로 서로 byte-identical하고
locked reference oracle과 protocol difference 0입니다. Cross-runtime raw JSON byte 동일성은
계약이 아닙니다.

제품 commit `6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1의
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)에서 Ubuntu 24.04
portable job과 macOS 15 arm64 exact job이 모두 통과했습니다. Ubuntu job은
`CGO_ENABLED=0 GOARCH=386 go test -count=1 ./migrations/definition`을 실제 Linux/386 runtime에서
실행했습니다. File/directory/module/remote discovery, public migration CLI, writer/upgrade/cache,
custom/executable/data/raw-SQL operation과 PostgreSQL/MySQL 등 non-SQLite lifecycle backend는
여전히 미지원입니다.

GDJ-0021 reference artifact의 MIG-065..074는 당시 exact 10 `oracle_locked`였습니다. Manifest는
4,580 bytes/SHA-256
`0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, oracle은 19,971
bytes/`49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, static fixture는
1,729 bytes/`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`입니다. 기존
`SHA256SUMS` 10줄을 byte-identical prefix로 보존하고 11번째 oracle line만 append한 파일은
1,061 bytes/SHA-256
`74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`입니다. 이 artifact는
Django 결과 parity가 아니라 Accepted ADR-0021의 독립 GoDj decision oracle입니다.

GDJ-0022 status-only manifest는 4,520 bytes/SHA-256
`0bbf254e80fea17b52070d0589da5ddcd401ff67440062a89b4fcd3e8309c048`입니다. Oracle, static fixture와
`SHA256SUMS` bytes는 위 GDJ-0021 값에서 바꾸지 않았습니다. Actual product adapter는 injected
in-process backend를 통해 global kernel을 실행하고 runner stage에서 같은 production linked
entrypoint를 호출해 두 actual report를 결합합니다. MIG-065..074는 10 `passing`, 현재 제품 분류는
11 adapter/115 contract의 `110 passing + 5 deviation`이며 static fixture는 계속 ordered 10 mismatch를
만듭니다.

GDJ-0023 relation reference manifest는 10,842 bytes/SHA-256
`08124b420e6313e4c2c1a5be32a3bdd29d831f02f1479bc3591af6f8f7da1522`였고 REL-001..012 모두
`oracle_locked`였습니다. GDJ-0024 status-only manifest는 10,836 bytes/SHA-256
`1a844ae1f0da7226b0dd936ee5b3eb884144e4caaf829ec2f6c822ab361b4254`입니다. GDJ-0025는 REL-004만
`passing`으로 바꾼 10,830 bytes/SHA-256
`944be1b941b9217ed27c2f6d5a33662cdfafc23f0c7698cad5ebb80849b633f0` status-only manifest입니다. GDJ-0026은
REL-003/006만 추가로 `passing`으로 바꾼 10,818 bytes/SHA-256
`e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a` status-only manifest입니다.
GDJ-0027은 REL-005만 추가로 `passing`으로 바꾼 10,812 bytes/SHA-256
`640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136` status-only manifest입니다.
GDJ-0028은 REL-012만 추가로 `passing`으로 바꾼 10,806 bytes/SHA-256
`70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477` status-only manifest입니다.
GDJ-0029는 REL-009/010/011만 추가로 `passing`으로 바꾼 10,788 bytes/SHA-256
`64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141` status-only manifest입니다.
GDJ-0030은 REL-007/008만 추가로 `passing`으로 바꾼 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46` status-only manifest입니다.
GDJ-0033은 여기서 REL-002만 추가로 `passing`으로 바꾼 10,770 bytes/SHA-256
`791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b` status-only manifest입니다.
Oracle 33,792
bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, 12-line
`SHA256SUMS` 1,148 bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`는
바꾸지 않았습니다.

### GDJ-0036 current generated relation fixture shape

GDJ-0036에서는 checked-in generated fixture bytes를 current ABI로 재기준화했습니다.
별도 checked-in REL-004 fixture는 current target/source schema에서 main/metadata와 project-owned
binding/query bridge를 실제 재생성하고 manual FK-enabled
SQLite fixture의 real QuerySet 결과를 관찰했습니다. 다음 파일 수는 GDJ-0036 current ABI로 재기준화했습니다.
REL-003/006 fixture는 object companions/project
object bridge를 재생성하고 actual SQLite loader/cache/source-key Plan을 관찰합니다. REL-005 fixture는 authors
main/metadata/object, blog main/metadata/object와 project binding/reverse companion의 exact eight files를
재생성하고 accessor `[10,11]`과 lookup `[1]`을 관찰합니다. REL-012 fixture는 이 exact eight-file union에
project prefetch companion을 더한 exact nine files로 `[1:[10,11],2:[],3:[12]]`,
두 SELECT, root `author_id IN`, key count 3, JOIN 0과 warm access extra query 0을 관찰합니다.
REL-009/010/011 fixture는 current object product에 app-local projection companion 두 개와 project
select-related companion 하나를 더한 exact eleven files를 재생성합니다. Required plain/eager 4-vs-1 SELECT와
INNER 1, nullable LEFT OUTER 1, warm access extra query 0, reverse `posts` path의 query/mutation 0을 관찰합니다.
REL-007/008 fixture의 prerequisite exact twelve generated union은 current generator로 결정적으로
재생성됩니다. Project facade를 더한 exact thirteen-file union에서 REL-002 assignment/Save와 supplemental
presence/reconciliation/per-edge COW cache/rollback 경계를 검증합니다. 같은 실제 FK schema에서 `PROTECT` distinct
source count 2와 mutation 0, `SET_NULL` UPDATE 2행 뒤 target DELETE 1행 및 하나의 transaction도 계속 관찰합니다.
모든 actual은 oracle/static expected artifact를 import하지 않습니다.

다음 workflow/inventory 수치는 GDJ-0035 final checkout의 historical snapshot입니다. GDJ-0036 final frozen
inventory와 hosted topology는 통합 gate에서 새로 기록해야 합니다.

Required workflow topology는 full/exact 2 + independent project-check proof 4 + relation-binding proof 4 +
SQLite 4 + actual project-check product 4 + Python compatibility 4의 existing exact 22를 보존하고,
relation-product Linux/macOS x64/arm64 4개를 더한 exact 26 executions입니다. 각 relation-product leg는
normal/race/CGO-disabled/vet, generated fixture/compile proof, artifact no-rewrite와 clean worktree를
검증합니다. Exact top-level package inventory는 725 run/725 pass/0 skip이고, encoded inventory는 73,806
bytes/SHA-256 `2ad28eb2e36c496e760b32d13725e6c889bd323965827d41472fa4d43ad8a5d4`입니다.
Python compatibility
matrix는 Ubuntu 24.04에서 CPython 3.12.13, 3.13.15, 3.14.3, 3.14.7과 Django 6.1/asgiref
3.12.1/sqlparse 0.5.5와 uv 0.12.3을 isolated하게 고정하고 portable 216 tests/19 intentional skips 및
139 scenario payload 623,543 bytes/SHA-256
`f4f48c4c680debbe5ed7ab2b962e01e9110064b7bf3064b7c6fd9a06539018da`를 검증합니다. 이는 checked-in
exact oracle identity를 넓히지 않으며 기존 CPython 3.14.3 profile/oracle/uv.lock job이 계속 유일한
oracle regeneration 경계입니다.

## Provenance

현재 query/write/migration/Save/QuerySet evaluation-cache/migration-planning/execution/
recorder-restart/historical-state/lifecycle/definition-source scenario는 Django 코드를
번역하지 않고 GoDj 고유 fixture로 독립적으로
작성했습니다. Static migration fixture도 public `migrate` 경로를 관찰하기 위한 독립
정의입니다. Manifest의
upstream 문서/test reference는 동작 근거와 버전을 추적하기 위한 것입니다. Current MIG-057..064는
모두 Accepted ADR-0035 decision provenance를 가지며, Django behavior를 실제 관찰한
MIG-057/MIG-064만 pinned Django provenance를 별도로 가집니다. 파생물
분류와 고지 규칙은 `docs/LICENSING.md`와 `NOTICE.md`를 따릅니다.
MIG-065..074는 Accepted ADR-0021 decision provenance를 유지하고, current definition load/digest를 직접
소유하는 MIG-065..068/MIG-073은 ADR-0035 decision provenance도 가집니다. 이 set은 Django source/test를
참조하지 않습니다. Django-named exact profile과 oracle directory 재사용은 corpus 관리
경계일 뿐 Django-derived 분류가 아닙니다. REL-001..012는 Django 6.1 commit에 고정된 documentation/test
provenance를 가지지만 scenario와 GoDj product fixture는 독립 작성했으며 Django source를 번역하지
않았습니다.
QRY-022..033도 같은 pinned Django 6.1 commit의 QuerySet/aggregation documentation, public tests와 cursor
iteration boundary를 contract별로 좁게 참조하지만 scenario/oracle/Go adapter는 독립 작성했습니다. Source/result
AST, fixed-arity top-level generic builders, cache ownership과 backend error policy는 ADR-0039의 GoDj-owned
`kind=decision`, `derived=false` provenance이며 Django internal object ABI를 복제하거나 번역하지 않습니다.
QRY-034..043도 같은 pinned Django 6.1 commit의 public `Q`/lookup documentation과 public tests를 관찰 근거로
좁게 참조하지만 scenario, expected payload와 Go actual adapter는 독립 작성했습니다. Immutable Boolean tree,
typed `And`/`Or`/`Not` API, depth/node caps, backend lowering과 relation OR/NOT 제한은 ADR-0040의 GoDj-owned
`kind=decision`, `derived=false` provenance이며 Django internal object ABI를 복제하거나 번역하지 않습니다.

아래 GDJ-0035 provenance와 artifact bytes는 historical evidence입니다. 그 Phase-B legacy
tuple/profile/promotion 의미와 product publication sequence는 GDJ-0036에서 retire했습니다. 현재 checked-in
MIG-075..086 artifact는 ADR-0035 current-only 진단 reference로 대체됐고 reference aggregate에는 포함되지만,
계속 `oracle_locked`/unregistered라 product aggregate에는 포함하지 않습니다.

GDJ-0035 exact 16-document activation head는 EVID-084/run `31618469072`에서 hosted-verified됐습니다. Phase A는
[EVID-085](../docs/status/TEST_EVIDENCE.md#evid-20260813-085--gdj-0035-phase-a-reference-only-artifacts-and-local-validation)에서
manifest 7,792 bytes/`dfe021c2…569b`, oracle 125,248/`c742f91a…de27`, ordered NI 1,846/
`f9bd9c47…9e24`, 13-line checksum 1,245/`5022a230…9cf4`를 로컬에서 고정했습니다. Reference는
exact 13 set/139 contract/156 ordered cross-binding=`122 passing + 5 deviation + 12 oracle_locked`이고 product는
exact 12/127=`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12로 불변입니다. Phase A exact head
`84e16bf193fc2079cd87788249e6e4a694f2402c`는
[EVID-086](../docs/status/TEST_EVIDENCE.md#evid-20260813-086--gdj-0035-phase-a-github-hosted-reference-only-exact-head-ci)의
unique run `31625898551`에서 26/26 jobs와 326/326 steps 모두 성공해 hosted-verified됐습니다. 이 증거는
Phase B test-only 후보나 ADR-0034 수락을 재귀적으로 증명하지 않습니다. Phase B head `c2ecb292...`는
EVID-087/088, Phase C exact 8-test-only proof head `7d36502...`는 EVID-089/090/run `32174259324`의
별도 local/hosted gate를 통과했습니다. Product aggregate와 MIG-075..086 `oracle_locked` 분류는 불변입니다.
Later D1/D2/D3a product/correction heads는 EVID-093/runs `32195313382`, `32205324145`, `32218003207`에서
각각 exact 26/26 jobs·342/342 steps·audit P0..P3=0을 통과했습니다. D3b product/correction heads
`74c2b72...`/`167ef03...`도 EVID-094/run `32231149900`의 별도 exact 26/26·342/342와 audit
P0..P3=0을 통과했습니다. D4 exact one-test-file head `424ec4d...`도 EVID-095/run `32248885053`의
별도 exact 26/26·342/342를 통과했습니다. EVID-096 exact-six docs head `62df9b2...`도 run
`32260744096`의 고유 exact 26/26·342/342와 audit P0..P3=0을 통과했습니다. D4d final head
`dd83362...`는 nullable no-default non-PK ForeignKey Add를 sealed same-target loaded universe에서 구현했고
run `32271361724`의 고유 exact 26/26·342/342와 audit P0..P3=0을 통과했습니다. 이 증거는 conformance
`migrationrelation` test-only helper를 product adapter로 재분류하거나 MIG-075..086을 `passing`으로
전환하지 않습니다. D4d docs head `c59669c...`와 D4e final head `1d86f6e...`도 각각 unique runs
`32278555810`/`32282269755`의 exact 26/26·342/342와 audit P0..P3=0을 통과했습니다. D4e는 required,
no-default, non-PK, `PROTECT` ForeignKey를 empty source에 추가하는 bounded slice만 소유합니다. EVID-098 docs
head `85f9270...`와 D4f final head `9d5b894...`도 각각 unique CI #94/#95 runs
`32288383027`/`32294983953`의 exact 26/26·342/342와 audit P0..P3=0을 통과했습니다. D4f bounded
reverse/unapply remake 구현은 exact appended nullable `PROTECT` 또는 `SET_NULL`, required `PROTECT`를
허용합니다. Frozen D4f direct E2E fixture는 nullable `PROTECT`와 required `PROTECT`만 검증했으며
dedicated nullable `SET_NULL` D4f E2E proof를 주장하지 않습니다. 그 checkout에서 MIG-075..086은
`oracle_locked`였습니다.

Phase A에서 아직 Accepted되지 않았던 GoDj-owned GDJ-0035 candidate payload는 historical provenance
`kind=proposal`, decision ID `GDJ-0035`, `derived=false`로 계속 분류합니다. Pinned Django BSD source/test
reference는 실제 관찰한 부분의 provenance일 뿐이며
GoDj scenario, fixture, payload와 assertion은 독립적으로 작성하고 upstream source, fixture, comment 또는 assertion
구조를 복사·번역하지 않습니다. Later ADR-0034 acceptance는 이 payload를 `kind=decision`이나 Django parity로
소급 재분류하지 않습니다. Test-only helper/type/error detail, golden/hash와 private catalog도 product API가 아닙니다.
