# 핵심 미결정 사항

- 상태: Active register
- 마지막 검토: 2026-08-25

이 문서의 항목은 초안 예시를 확정 API로 오해하지 않도록 관리합니다. 결정이 나면 개별 ADR로 옮기고 여기에는 결과 링크만 남깁니다.

| ID | 우선순위 | 결정 시점 | 질문 |
|---|---|---|---|
| Q-006 | Resolved | GDJ-0006 | ADR-0011의 Manager Save, concrete typed option/field mask와 generated explicit-key helper로 MOD-008..019 Verified |
| Q-007 | Resolved | GDJ-0008 | ADR-0012 ownership/API와 QRY-011..021 제품 adapter가 Verified; 총 45개 contract passing |
| Q-010 | Partial | GDJ-0037/GDJ-0042 completed / broader generation handshake | Current definition/loaded lifecycle과 ProjectSpec, global generate/check, project-wide manifest/publication 및 optional project-linked runserver WEB-011..020은 exact-head hosted-verified; installed runner/library/generator semver와 general upgrader/repair UX는 open |
| Q-011 | Partial | GDJ-0039..GDJ-0041 completed / M4-M5+ | Hosted-verified cache/projection/Boolean baseline, typed Integer/String range, sealed same-model/same-kind F, bounded Article advanced filter와 QRY-034..053 20/20 passing까지 완료; transaction/async/background ownership은 open |
| Q-012 | Partial | GDJ-0038 completed / broader migration 후속 | Current loaded lifecycle/unified ABI와 bounded PostgreSQL schema/recorder/revision/restart는 hosted-verified; public migrate/writer/upgrade/custom operation/general crash recovery는 open |
| Q-013 | Partial | GDJ-0038 completed / broader relation·backend 후속 | Bounded SQLite FK와 generated PostgreSQL required/nullable relation flow는 hosted-verified; broader relation/backend와 PostgreSQL REL-007/008 delete는 open |
| Q-014 | Resolved | GDJ-0043 / ADR-0043 Accepted | Closed value DTL subset만 읽고 arbitrary Go attribute/callable/reflection은 노출하지 않음; WEB-022/027 차이는 Verified DEV-0003 |
| Q-015 | Resolved | GDJ-0043 / ADR-0044 Accepted | Admin DOM byte parity 대신 Article semantic flow를 보존하고 process-lifetime system state와 one-model breadth를 명시 |
| Q-016 | Partial | GDJ-0044 completed / M8 전 | API는 DRF 3.18.0 + Django 6.1 + CPython 3.14.3 exact profile과 JSON/SessionAuthentication/PageNumber/closed Router bounded 18-contract slice를 Accepted/hosted-verified; Channels/Realtime profile과 broader API는 open |
| Q-017 | P1 | GDJ-0038/GDJ-0042 completed / raw-model and general upgrade | Project publication, ADR-0038 Web-only explicit DTO representation과 generated-aware runserver usability WEB-011..020은 hosted-verified; general raw-model UX/capability/namespace와 reverse/general upgrade는 open |
| Q-018 | P2 | 공개 배포 전 | Django trademark와 비공식 프로젝트임을 어떤 이름·고지 정책으로 다루는가 |
| Q-019 | P1 | GDJ-0035는 no-retry 보존 / retained-resource 정책은 별도 후속 | Unknown-outcome retained connection을 Backend.Close 전까지 무제한 보유할 것인가, bounded quarantine/reconciliation을 도입할 것인가 |
| Q-020 | P1 | GDJ-0045 source-local publication / hosted acceptance·multi-process 전 | Durable framework system state의 schema·migration·runtime topology를 어떻게 소유할 것인가; current checkout은 one-runtime/sequential-restart 답을 구현했지만 required PostgreSQL/hosted와 broader multi-runtime 답은 남음 |
| Q-021 | P1 | Durable system state 이후 / public API auth 전 | First-party cookie, BFF와 Bearer API profile을 어떻게 분리하고 JWT/opaque access token, rotating refresh token, key rotation/revocation을 공통 Principal/Permission 경계에 연결할 것인가 |

## GDJ-0043에서 해결한 질문

- Q-014: Accepted ADR-0043은 immutable closed Value/Engine, bounded DTL subset과 context-aware escaping을 채택하고
  arbitrary Go method/function, attribute fallback, reflection과 application dictionary callback을 기본 template 권한에서
  제외합니다. Django lookup/callable 차이는 WEB-022/027의 Verified DEV-0003 selector에만 한정합니다.
- Q-015: Accepted ADR-0044는 exact Django Admin DOM/CSS보다 login, permission, CSRF, list/search, validated CRUD,
  history와 selected publish의 observable semantic flow를 보존합니다. User/session/audit는 process-lifetime, registry는 Article
  한 모델이며 durable system state와 M6 전체 완료는 후속 범위입니다.
- Exact submitted head `5eda0a4...`의
  [EVID-124](status/TEST_EVIDENCE.md#evid-20260824-124--gdj-0043-exact-head-hosted-completion) / CI #134가
  27/27 jobs·358/358 steps로 이 bounded answer를 hosted-verify했습니다.

## GDJ-0044에서 부분 결정한 질문

- Q-016 API half: DRF 3.18.0 tag `11875a38...`, Django 6.1과 CPython 3.14.3을 isolated exact reference로
  선택했습니다. 첫 범위는 JSON parser/renderer, SessionAuthentication-style 403+CSRF, per-method permission,
  fixed PageNumber pagination, SimpleRouter trailing slash와 Article list/create/detail/PUT/PATCH/delete입니다.
- Accepted ADR-0045/0046 아래 exact 18 contracts는 `13 passing + 5 deviation`으로
  [EVID-126](status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion)에서 hosted-verified됐습니다.
  WEB-028/029는 Verified DEV-0006, API-001/003/010은 Verified DEV-0007입니다.
- OpenAPI, browsable API, token auth, nested/bulk serializer와 Channels/Realtime reference는 이번 completed packet에서
  결정하지 않았으므로 Q-016 전체는 계속 `Partial`입니다.

## GDJ-0045에서 검증할 질문

- Q-020의 첫 답은 Proposed [ADR-0047](adr/0047-explicit-single-runtime-system-state.md)입니다. Current
  Auto/Char/Boolean IR의 explicit `godj_system.0001_initial`, DB/schema당 one live runtime, process mutex와 transaction의
  0/1 cardinality 검사, raw bearer 대신 digest, current-only bounded codec과 Article/Admin-audit same-transaction을 검증합니다.
- Restart는 이전 process의 listener/runtime/backend handle이 모두 종료된 sequential restart입니다. DB unique/index IR,
  concurrent multi-process, distributed session, direct SQL writer, online repair와 production topology는 답하지 않으므로 Q-020은
  이 packet이 완료돼도 broader scope에 대해 `Partial`로 남습니다.
- One-runtime은 `Open`이 lease/fence로 강제하지 않는 operator precondition입니다. Future multi-runtime 답은 credential singleton,
  session digest uniqueness, row-lock/conditional monotonic touch, shared capacity/reap/audit-prune, Article read-modify-write와
  purpose-separated CSRF/Admin notice deployment key ring을 함께 다뤄야 합니다.
- SYS-001..012 exact artifact와 global GoDj adapter는 source-local로 게시됐습니다. Current classification은
  SYS-001..008/010..012 `passing` + SYS-009 `deviation`, reference 21/231/420=`203+16+12 locked`, product
  20/219=`203+16`이며 MIG-075..086만 locked/unregistered입니다. Local actual A/B는 12,944 bytes/SHA-256
  `f30ac1a4...d41c3a6`로 byte-identical하고 SQLite distinct-process가 통과했습니다.
- SYS-009의 process-local CSRF-key 정책은 계속 [Proposed DEV-0008](DEVIATIONS.md#dev-0008--restart-뒤-process-local-csrf-key로-stale-masked-token을-거부)입니다.
  Required PostgreSQL distinct-process와 final hosted matrix 전에는 ADR Accepted, DEV Verified, GDJ-0045 completed 또는
  Q-020 `Partial` terminal 전환을 주장하지 않습니다.

## Public API authentication에서 검증할 질문

- Q-021은 Session을 JWT로 대체하지 않습니다. First-party Web은 HttpOnly durable session cookie+CSRF를 기본으로 두고, BFF는
  browser token custody를 서버로 제한하며, 독립 client용 Bearer는 JWT 또는 opaque access token을 별도 adapter로 검토합니다.
- Refresh token을 채택하면 opaque CSPRNG bearer, digest-only storage, family/generation rotation, revocation/reuse detection과
  transaction ownership을 함께 결정합니다. Shared `auth.Principal`/Permission deny-overlay를 재사용하고 token 전용 permission
  체계를 만들지 않습니다.
- 구현이나 ADR은 GDJ-0045 acceptance 뒤 별도 packet에서 결정합니다. 현재 `api` core와 `api/sessionauth`를 재작성하지 않습니다.

## M0에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-001 | 선언 package와 generated target을 import graph에서 분리 — [ADR-0006](adr/0006-codegen-input-package-boundary.md) |
| Q-002 | exact runtime fingerprint와 `uv.lock` hash를 profile에서 검증 — [Compatibility Lab](../conformance/README.md) |
| Q-003 | M0에서 strict JSON protocol v1과 explicit adapter를 채택한 뒤, GDJ-0003에서 contract phase를 결속하는 v2로 명시 승격 — [protocol](../conformance/internal/protocol) |
| Q-004 | 독립 시나리오와 upstream 파생물을 구분하고 Django BSD 고지를 보수적으로 포함 — [Licensing](LICENSING.md) |

## M1에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-005 | `orm` 소유 generic interface + generated zero-state concrete descriptor, generation/compile 시점 freeze — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-008 | ordered input을 `ParseDynamic`에서 즉시 typed predicate 또는 error로 변환 — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-009 | consumer-owned interface와 external module compile + `go list` dependency gate — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |

## M2에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-006 | generated create/patch와 nullable change state는 ADR-0009, mutable instance Save/typed mask/explicit key orchestration은 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md); MOD-001..019 verified |
| Q-012 core | preflighted ProjectState/Operation/Executor와 한 transaction의 SQLite editor/recorder — [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md) |

## GDJ-0036 current answer boundary

Accepted [ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)는 아래 질문의
현재 기준을 바꿨지만 전체를 닫지는 않습니다.

- Q-010/Q-012: Definition wire/digest와 ProjectState는 current version 1 하나입니다. `definition.Load`가
  opaque `LoadedDefinitionSet`을 만들고 `Executor.Migrate(ctx, loaded, request)`가 complete lifecycle을
  소유합니다. `DirectExecutor`는 raw scalar 실행만 소유합니다.
- Q-012/Q-013: Backend는 mandatory capability와 ordered `MigrationIntent`를 받는 `BeginMigration` 하나를
  사용합니다. Public `StateReconstructor`의 최종 계약도 scalar/relation을 같은 current state로 재생합니다.
- Q-013/Q-017: Main generator가 relation model descriptor/write metadata를 직접 생성합니다. App-local
  relation-query file과 facade-private write model은 current ABI에서 제거됐고 project-owned cross-app
  binding/query/facade는 유지됩니다.
- Q-017: ProjectSpec, manifest, whole-project candidate compile과 recoverable publish는 GDJ-0037 exact correction
  head에서 hosted-verified됐습니다. 물리 companion roster를 first-alpha 이후 장기 public ABI로 보장하는 기간,
  general upgrader/repair와 raw-model/facade UX는 여전히 open입니다.

GDJ-0035 Phase-B의 legacy tuple/profile/promotion product publication sequence는 retire됐습니다. 그 옛
artifact bytes와 evidence는 당시 의사결정의 역사 기록입니다. 현재 checked-in MIG-075..086 artifact는
ADR-0035 current-only 진단 reference로 재기준화되어 reference aggregate에는 포함되지만, 계속
`oracle_locked`/unregistered라 위 질문의 제품 status 입력은 아닙니다. GDJ-0036의 corrected exact head는
[EVID-103](status/TEST_EVIDENCE.md#evid-20260820-103--gdj-0036-corrected-exact-head-hosted-completion)에서 hosted
검증됐습니다. GDJ-0037의 publication 하위 경계는 affected 및 final full/386/repository-external source-clean-copy
local gates와 exact correction head `d4643068...`의
[EVID-105](status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) / CI #103
26/26 jobs·326/326 steps를 통과해 completed/hosted-verified됐습니다. Q-010은 `Partial`, Q-017은 P1/open입니다.

Completed [GDJ-0038](../work/0038-postgresql-and-minimal-web-vertical-slices.md)은 Accepted
[ADR-0037](adr/0037-postgresql-current-contract-backend.md)의 PostgreSQL current backend와
[ADR-0038](adr/0038-minimal-web-core-request-lifetime-and-representation.md)의 synchronous request/explicit DTO
하위 경계를 병렬 구현했습니다. Exact source commit `cb90f7a...`의 Phase D/E local proof는
[EVID-107](status/TEST_EVIDENCE.md#evid-20260823-107--gdj-0038-postgresql-migration-and-web-integration-source-frozen-local-checkpoint)의
PostgreSQL schema/recorder/revision/restart, generated Article/relation, required 12/12·skip 0과 full local gates를
통과했습니다. Final correction head `187638f9...`는
[EVID-108](status/TEST_EVIDENCE.md#evid-20260823-108--gdj-0038-postgresql-1710-exact-head-hosted-completion) /
run `32626539049`에서 PostgreSQL 17.10 actual을 포함한 27/27 jobs·341/341 steps로 통과해 bounded
DB-PG-001..010/WEB-001..010을 `Verified`하고 work를 completed로 닫았습니다. 이 결과도 Q-011/Q-012/Q-013을
해결하거나 Q-017 general raw-model UX를 닫지 않습니다.

Completed [GDJ-0039](../work/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)은 Accepted
[ADR-0039](adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)에 따라 Q-011/M4의 첫 넓은 read
slice를 구현했습니다. Final source `695916c8...`은 source/result shape 분리, typed DTO projection, scalar
Count/Max, distinct/offset을 SQLite/PostgreSQL Article 검색·리포트 흐름으로 묶고
[Local EVID-109](status/TEST_EVIDENCE.md#evid-20260823-109--gdj-0039-typed-query-breadth-source-frozen-local-checkpoint)의
full/386/source-clean-copy와 audit을 통과했습니다. Submitted head `253455d...`는
[Hosted EVID-110](status/TEST_EVIDENCE.md#evid-20260823-110--gdj-0039-exact-head-hosted-completion) / run
`32634741186`에서 exact 27/27 jobs·341/341 steps로 통과해 QRY-022..033을 `Verified`하고 work를
completed로 닫았습니다.

Completed [GDJ-0040](../work/0040-composable-typed-boolean-predicates-and-article-search.md)은 Accepted
[ADR-0040](adr/0040-composable-typed-boolean-predicates-and-article-search.md)의 하나의 immutable typed Boolean predicate tree와
bounded Article search를 QRY-034..043으로 구현한 packet입니다. Phase A `fe4996f...`는 신규 reference-only
set을 고정했고 Phase B/C product `86d6b169...`/actual `0ec6f385...`는 하나의 capped tree, typed connector,
SQLite/PostgreSQL compiler와 Article q/exclude flow를 구현했습니다. QRY-034..043은 10/10 `passing`, current
reference는 15/161/210=`144+5+12 locked`, product는 14/149=`144+5`입니다.
[EVID-112](status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)는
affected/local checkpoint입니다. Correction `73b912d...` 뒤 submitted `136e825...`의
[EVID-115](status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion) / run
`32642341459` exact 27/27 jobs·341/341 steps가 QRY-034..043을 hosted-verify하고 work를 completed로 닫았습니다.
Q/F 전체, relation under OR/NOT,
bulk/locking, annotation/subquery/window, related projection, transaction container와 async/background ownership은
별도 후속입니다.

Completed [GDJ-0041](../work/0041-typed-scalar-comparisons-field-references-and-article-filtering.md)과 Accepted
[ADR-0041](adr/0041-typed-scalar-comparisons-and-field-references.md)은 Q-011의 다음 bounded read 답변입니다.
Integer/String `gt`/`gte`/`lt`/`lte`, sealed same-model/same-kind `orm.F`, private RHS union/source validation,
nullable RHS odd-`NOT`, SQLite/PostgreSQL identifier RHS와 Article advanced exactly-two-query flow를 구현했습니다.
Current QRY-034..053은 20/20, 신규 QRY-044..053은 10/10 zero-diff이고 reference는
15/171/210=`154+5+12 locked`, product는 14/159=`154+5+0`입니다. Frozen source `7f2bb223...`의 local-final과
submitted head `e97a4e3...`의
[EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) / run
`32647746430` exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual을 통과해
work를 completed/hosted-verified로 닫았습니다. Q-011은 아래 broader scope 때문에 계속 `Partial`입니다.
Arithmetic/function/annotation, relation/cross-model F, transaction container와 async/background ownership은 open입니다.

Completed [GDJ-0042](../work/0042-project-linked-runserver-and-article-development-loop.md)와 Accepted
[ADR-0042](adr/0042-project-linked-runserver-and-article-development-loop.md)는 Q-010의 direct project command와
Q-017의 generated runtime usability를 WEB-011..020으로 좁혀 구현했습니다. Optional descriptor capability,
current-bundle read-only preflight, loopback-only `godj runserver`, long-lived child lifecycle과 actual
SQLite/PostgreSQL Article flow가 source `810149f...`에서 locally implemented/actual-passing입니다. Documentation
checkpoint `47b0eb8...`의 initial local final 뒤 first hosted run은 26 success/1 timeout이었습니다. Correction
`2b49938...`의 EVID-121 full/386/803-file archive/audit refreeze 뒤 submitted `2bfdbd5...`의 EVID-122/run
`32659704239` exact 27/27 jobs·358/358 steps가 WEB-011..020을 hosted-verify했습니다. Installed
semver/upgrader/general raw-model UX를 결정하지
않으므로 두 질문의 상태는 그대로입니다.

## Q-001 — Codegen bootstrap — Resolved

초안의 임시 runner import 방식은 schema package가 오래된 generated type 때문에
compile되지 않으면 동작하지 않습니다. 실행 spike에서 rename/delete, stale 사용자
메서드, last-good output 보존을 검증하고 선언/generated package 분리를
채택했습니다. Project runner 형태는 Q-010에서 계속 다룹니다.

## Q-006 — Nullable와 변경 추적

M1 read model은 nullable CharField에 `*string`을 사용해 `nil`, `ptr("")`, 일반 값을
구분합니다. MOD-002~004 oracle과 GDJ-0004 구현을 거쳐 generated immutable
create/patch builder, `Change[T]`/`NullableChange[T]`와 Manager write API를
[ADR-0009](adr/0009-m2-explicit-write-change-state.md)에서 채택·검증했습니다. Django가
기본 `save()`에서 실제 dirty tracking을 하는지 추측하지 않고, instance save/new/loaded,
`update_fields`, force flag, explicit PK와 rollback 의미를
[GDJ-0005](../work/0005-save-lifecycle-compatibility-contracts.md)의 MOD-008..019로
고정했습니다. Fully loaded default save는 field 전체를 쓰며 dirty-only가 아닙니다.
Go에서는 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md)에 따라 concrete
`SaveOption[M]`, sealed `WritableField[M]`, generated explicit-key constructor와
`Manager[M].Save`를 사용합니다. Generated instance method는 model field `Save`와의
충돌 때문에 만들지 않으며, MOD-008..019가 이 경계로 모두 통과했습니다.

## Q-007 — QuerySet cache

불변 plan과 instance result cache를 분리해야 합니다.
[GDJ-0007](../work/0007-queryset-evaluation-cache-compatibility-contracts.md)에서 chain,
반복/빈/실패 평가, iterator/Count/Exists/fresh clone의 Django 외부 동작을 먼저
QRY-011..021의 exact 계약으로 고정했습니다. 당시 oracle은 `oracle_locked`이고 제품은
intentionally uncached였습니다. Value-copy QuerySet의 cache ownership, cached result alias,
동시 `All`, waiter cancellation과 Go terminal 표면은 그 결과를 입력으로
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에서 Accepted했습니다.
[GDJ-0008](../work/0008-queryset-evaluation-cache-product-slice.md)은 이를 제품 unit/compile/
race/differential test로 구현해 QRY-011..021 모두를 `passing`으로 검증했습니다. 따라서
Q-007은 해결됐습니다. Q-011은 QuerySet evaluation subset만 해결됐고 request,
transaction-bound session과 hook의 goroutine ownership은 후속 단계에 남습니다.

## Q-010 — CLI와 project/library version handshake

현재 definition handshake는 persisted `format_version=1` 하나입니다. 아래 GDJ-0019/0020 tuple과 ABI
설명은 당시 evidence snapshot이며 current reader/upgrade 약속이 아닙니다. Full CLI/project/library semver와
repair가 남아 있으므로 Q-010은 계속 `Partial`입니다.

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)와
[Accepted ADR-0019](adr/0019-versioned-migration-definition-source.md)은 caller-provided migration
definition document와 consumer 사이의 exact tuple
`(definition format 1, loader ABI 1, operation codec 1, Schema IR 2)`를 MIG-060 `environment`
contract로 검증합니다. 이 handshake는 operation decode/construction 전에 fail-closed하는 source
ABI이며 global `godj` CLI, project library와 generator의 semver resolution이 아닙니다.

GDJ-0019는 file/module discovery, project build, generated Go runner, CLI exit code나 upgrade
command를 결정하지 않았습니다. 완료된
[GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)은 explicit caller bytes의
bounded product loader만 구현·검증했습니다. File/module/FS discovery나 CLI를 붙이지 않으며,
CLI/project binary가 어떤 version 정보를 교환하고 mismatch/old generator/stale output을 어떻게
복구할지는 별도 work/ADR로 결정해야 합니다. 따라서 Q-010은 계속 Partial입니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)과 Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)은 full handshake보다 작은 DB-free
`godj migrations check` contract를 test-only로 검증했습니다. Proof는 exact `godj.toml` descriptor v1의
project package를 private binary로 build하고 strict runner protocol v1에서
`migrations.check`를 요청합니다. Descriptor/runner wire version mismatch와 public exit,
`-mod=readonly`/`GOWORK=off`/`GOTOOLCHAIN=local`/`GOENV=off`, private TMP/cache/HOME/XDG/telemetry와
handled SIGINT cancel/reap는 MIG-065..074의 decision reference 범위입니다. Runner wire는
category/code pair만 전달하고 loader file/pointer detail은 test-only이며 user diagnostic protocol
확장은 후속입니다. 11 cap은 parsed/retained 경계일 뿐 build/runner CPU·시간·disk/network sandbox가
아닙니다.

Implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`의 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)은 existing
`ubuntu-24.04` x64 full/`macos-15` arm64 exact 2개, `ubuntu-22.04` x64/
`ubuntu-24.04-arm` arm64/`macos-15-intel` x64/`macos-26` arm64 project-check 4개와 동일 좌표의
SQLite normal/race/CGO-disabled/vet 4개, exact `2 + 4 + 4 = 10` job을 모두 통과했습니다. 각 matrix
leg는 expected GOOS/GOARCH, `fail-fast: false`, no `continue-on-error`와 final tracked-diff/
porcelain-empty clean worktree gate를 만족했습니다. Exact 16-file completion-documentation commit
`34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 별도 run `31322959993`의 같은 exact 10 job을
통과했습니다. GDJ-0022 workflow는 product 4와 exact Python
3.12.13/3.13.15/3.14.3/3.14.7 compatibility 4를 더한 exact 18 required execution으로 확장됐고 fix head
run `31329294154`에서 18/18 성공했습니다. Initial run의 네 Python pre-test uv assertion failure/cancel과
fix는 EVID-028에 기록했습니다. Portable/compatibility는 uv
0.12.3, historical exact darwin oracle만 embedded profile의 uv 0.10.12를 사용합니다. Windows
native contract가 없고 GDJ-0022 완료 당시 actual backend는 SQLite뿐이었으므로 Windows green skip과 PostgreSQL/MySQL
service-only CI는 support evidence로 만들지
않습니다. Future backend는 digest-pinned service image, health check, UTC timezone과 C locale 또는
명시적으로 승인된 collation, actual query/write/transaction/schema/migration/recorder/
revision-lifecycle 및 durable restart/persistence contract를 먼저 required job으로 검증합니다.
Expected contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도
필수이며 adjacent versions는 scheduled/non-required로 분리합니다.

이 GDJ-0021 증거만으로는 public global CLI/project package API, production project binary entrypoint,
persistent runner cache, generator/library semver resolution이나 stale output repair를 확정하지 않았습니다.
SIGTERM/other fatal signal, crash stale-temp scavenging과 broken stdout/stderr sink delivery도 아직
결정하지 않습니다.

Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 Q-010 중 exact 두 global argv와
public project-linked entrypoint를 제품화했습니다. Exact API는 explicit
`project.Config{MigrationDefinitionRoots: ...}`와
`project.Run(ctx, config, argv, stdin, stdout) error` 두 export이며 global mutable registration과 public
protocol/report는 만들지 않았습니다. Exact 18 hosted acceptance는 완료됐지만 full library/generator
semver, stale repair와 installed runner lifecycle은 계속 open이므로 Q-010은 이 work 뒤에도
Partial입니다.

## Q-012 — Migration format과 실행 수명주기

현재 complete lifecycle entry는 `Executor.Migrate(ctx, LoadedDefinitionSet, request)`이며 raw scalar
`Apply`/`Unapply`/`ExecutePlan`은 `DirectExecutor`로 분리됐습니다. Historical state는 current relation-aware
`ProjectState` 하나로 표현하고 backend begin도 하나로 통합했습니다. 아래 단계별 API는 당시 checkout의
evidence를 설명합니다.

MIG-001~004와 GDJ-0004 제품 구현을 거쳐 state/operation/executor/schema editor/
recorder core를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에서
채택·검증했습니다.
[GDJ-0009](../work/0009-migration-planning-compatibility-contracts.md)는 제품 graph를 먼저
만들지 않고 MIG-005..016으로 dependency/applied-state 기반 forward/backward plan,
multi-target 중복 제거와 잘못된 graph/history 오류를 contract-only로 고정했습니다.

[ADR-0013](adr/0013-immutable-migration-planner.md)은 `ProjectState`와 applied history를
분리하고 immutable identity graph의 zero-I/O Planner를 사용하기로 결정했습니다.
[GDJ-0010](../work/0010-immutable-migration-planner-product-slice.md)은 이 경계와 actual
adapter를 구현해 MIG-005..016을 모두 `passing`으로 검증했습니다. MIG-012는 caller target
order와 dependency precedence만 잠그며, incomparable sibling의 Django private DFS
tie-break는 호환 계약이 아닌 Go deterministic 정책입니다.

[GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md)은
multi-migration execution과 partial commit/failure stop 의미를 MIG-017..026의 여섯 번째
exact set으로 잠갔습니다. Django backward의 `schema_then_record` 때문에 정상 backward
세 계약의 transaction model과 recorder failure 한 계약의 DB state/phase가 GoDj 기존
same-transaction reverse와 다릅니다.

완료된 [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md)는 full zero-I/O
preflight, migration별 existing Apply/Unapply commit과 last durable state를 가진 최소
`ExecutePlan`을 구현했습니다. Same-transaction reverse는
[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
Accepted/Verified 결정이며 GDJ-0012 완료 당시 제품 상태는
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0013](../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)은
새 process/executor가 durable recorder에서 applied identity를 읽고 남은 plan을 계산하는
의미를 MIG-027..036의 10 `oracle_locked` 계약으로 고정했습니다. Absent read는 table을
만들지 않고, unknown legacy row는 보존하며, known inconsistent history는 explicit
migrate-style preflight에서 plan 전에 거부합니다.

완료된 [GDJ-0014](../work/0014-recorder-backed-restart-planning-product-slice.md)와 Accepted
[ADR-0015](adr/0015-recorder-backed-applied-state.md)는 transaction write interface와 분리된
raw recorder read port, core `LoadAppliedState`와 `Planner.CheckHistory`, SQLite read-only
reader를 제품화했습니다. Fresh file-backed restart를 포함한 MIG-027..036은 10
`passing`이며 GDJ-0014 완료 당시 제품 분류는 `73 passing + 4 deviation`이었습니다.
Recorder key만으로
`ProjectState`를 재구성할 수 없고 read/execution 사이 lock도 없으므로 public
restart/migrate convenience API는 아직 만들지 않습니다.

완료된
[GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
MIG-037..046으로 historical `ProjectState` reconstruction의 외부 의미를 여덟 번째 exact
set에 고정했습니다. Explicit empty와 omitted latest는 다른 request mode이고, target
before/after는 dependency closure의 포함 위치를 구분합니다. Applied projection은 unrelated
known branch를 포함하되 unknown legacy identity를 schema로 만들지 않습니다. 새 10개는
`oracle_locked`이고 GDJ-0015 완료 당시 제품 adapter는 fail-closed했습니다.

[ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 Accepted이며, 완료된
[GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)은 explicit
request API, immutable definition ownership/clone, Planner graph kernel 공유와 structured error를
구현했습니다. MIG-037..046은 10 `passing`, GDJ-0016 완료 당시 전체 제품 분류는
`83 passing + 4 deviation`입니다.
Applied adapter는 real SQLite recorder snapshot을 쓰지만 reconstructor core는 backend I/O가
없는 pure replay 경계입니다.

Explicit data-only source encoding과 bounded loader는 GDJ-0019/0020에서 결정·구현했습니다.
Source discovery/listing, writer/upgrade, data/custom/raw-SQL operation ABI, graph merge/squash/
optimizer, multi-process lock와 crash recovery는 여전히 결정하지 않았으며 public CLI 전에 별도
ADR과 contract가 필요합니다. Recorder read/planning, historical-state와 explicit-source loader
제품 subset을 완료해도 Q-012 전체 해결을 뜻하지 않습니다.

완료된
[GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
loader/CLI보다 lifecycle의 observable meaning과 stale-snapshot safety feasibility를 먼저
분리합니다. MIG-047..056은 fresh/prefix/no-op latest, named forward/reverse, app zero target,
unknown legacy, explicit inconsistent-history preflight, middle failure와 fresh durable restart의
아홉 번째 exact set으로 고정했습니다. MIG-054의 preflight 소유자는
`MigrationExecutor.migrate()` 자체가 아니라 public command orchestration의
`loader.check_consistent_history(connection) → target/plan → migrate` 순서이며
`plan_invoked=false`와 transaction/DDL/write 0을 관찰합니다. MIG-056은 `:memory:` 재사용이
아니라 temporary file database close/reopen과 fresh connection/loader/executor를 요구합니다.
Backward MIG-051/052는 abstract step outcome과 final schema/recorder만 비교하고 physical
transaction topology는 기존 DEV-0001/ADR-0014에 남깁니다.

[Accepted ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)은 recorder identities와
opaque freshness revision을 한 snapshot으로 읽고 각 migration transaction 안에서 첫 DDL/write
전에 expected token을 검증합니다. SQLite spike는 persistent epoch와 monotonic revision을
후보로 검증했고 fingerprint는 direct non-ABA recorder drift를 잡는 보조 gate로만 사용했지만,
GDJ-0017 완료 당시 제품 storage와 token encoding은 open이었습니다. Outer transaction 없이
migration별 durable commit을 유지하고 stale conflict는 current step mutation 0/last-durable
state로 fail-closed하며 semantic auto retry를 하지 않습니다. Spike가 optional capability와
unsupported fallback 금지를 검증했지만 당시 public coordinator/backend API와 final error
taxonomy는 후속 제품 work로 남겼습니다.

GDJ-0017이 끝났어도 file/source encoding, operation codec/version, data callback, public
coordinator/CLI, lease/fairness, process-kill crash reconciliation과 non-SQLite backend는 여전히
Q-012 후속입니다.

완료된
[GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 Q-012 중 already-loaded
definition lifecycle을 닫았습니다. Public `Executor.Migrate(ctx, definitions, request)`는 explicit
latest/targeted tagged request와 Executor-owned optional backend session을 사용합니다. Session은
identities와 private epoch/revision/fingerprint를 exact-one atomic snapshot에 결속하고 call 사이
connection을 pin하지 않으며 mandatory Close/state machine을 가집니다. 각 SQLite step은 pinned
connection의 `BEGIN IMMEDIATE` 안에서 first-write fence, schema, recorder와 successor token을
commit합니다. Unsupported backend fallback과 existing recorder 자동 adoption은 없습니다.

Dedicated fenced transaction은 rolled-back/committed/unknown durability를 구분합니다.
`CommitRolledBack`은 confirmed state/token을 advance하지 않고 SQLite session을 poison하며,
unknown과 함께 semantic retry를 금지합니다. Accepted ADR-0013의 canonical ascending plan도
그대로여서 MIG-052의 final state/schema/history는 exact match이고 ordered plan/steps 여섯 path만
DEV-0002입니다. Lifecycle 9 `passing` + 1 `deviation`을 더한 GDJ-0018 완료 당시 제품 분류는
`92 passing + 5 deviation`이었습니다.

Exact A2의 empty-table `BooleanField(default=false)`는 logical state에 default를 보존하면서
physical persistent default 없이 추가합니다. Nonempty table backfill/rebuild는 계속
unsupported입니다.

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)은 Q-012 중
explicit caller-provided source/versioned-loader 의미만 MIG-057..064의 contract로 분리했습니다.
Strict data-only JSON v1, tuple `(1,1,1,2)`, fully normalized IR v2,
`CreateModel`/non-PK `char`·`boolean` `AddField` closed codec, atomic load, canonical digest/error와 existing
`Executor.Migrate` reference handoff를 Accepted ADR-0019 decision oracle로 고정했습니다. 이는
Django Python file ABI exact compatibility가 아니라 Go redesign이며, 새 8개는
GDJ-0019 완료 당시 `oracle_locked`였습니다. MIG-064도 당시에는 Go handoff 구현이나 제품 loader
지원을 뜻하지 않았습니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)은 Accepted ADR-0020의
`migrations/definition` explicit `Source`/`Load`/zero `Set`/immutable report를 구현했습니다.
Exact cap은 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, JSON depth 64,
document values 65,536, batch values 262,144, dependencies 2,047, operations 2,048,
`CreateModel` fields 2,048입니다. Strict scanner의 closed JSON/RFC 6901 ordering, loader-owned
snapshot/deep copy와 source-owned 9-code/resource context를 검증하고, raw Planner/lifecycle error를
wrap/reclassify/retry하지 않습니다. Literal Schema IR 2는 two-way compile drift gate로 잠급니다.

열 번째 actual adapter가 MIG-057..064 decision-reference 8개를 `passing`으로 전환해 GDJ-0020 당시 제품
분류는 10 adapter/105 contract의 `100 passing + 5 deviation`이었습니다. Product commit
`6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1 run 31309152526의 Ubuntu/macOS
두 job과 실제 Linux/386 focused runtime을 통과했습니다. Completion-documentation commit
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 별도 run 31310002784의 Ubuntu/macOS 두 job에서
통과했고, EVID-023 append/status 교정 baseline
`53729103651bfc34acc5fe07fb4376d5dd78c204`도 별도 run 31310606332의 Ubuntu/macOS 두 job에서
통과했습니다. GDJ-0021 implementation head
`84ddf109c04acd72992b816aa72140c6e748e5f0`도 별도
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)의 exact 10 job에서
통과했습니다.

File/directory/module/remote discovery, public CLI, writer/upgrade/cache, executable/custom/data/raw-SQL operation,
global CLI/project handshake, adoption/repair command, copy/restore epoch와 crash/unknown-commit reconciliation은
계속 Q-012/Q-010의 open 범위입니다. PostgreSQL은 GDJ-0038 bounded current lifecycle만 hosted-verified이고
REL-007/008/general recovery 및 MySQL 등 다른 backend는 여전히 open입니다.

GDJ-0021은 위 open 범위 중 project-relative flat file discovery와 check-only process boundary만
MIG-065..074로 분리해 완료했습니다. Accepted contract는 linked project code가 clean root list를 제공하고
case-sensitive `*.godj.json` immediate regular files를 no-follow/byte order로 읽어 actual
`definition.Load`를 정확히 한 번 호출하는 의미입니다. 새 10개는 GDJ-0021 완료 당시
`oracle_locked`였고 제품 10 adapter/105 contract의 `100 passing + 5 deviation`은 바뀌지 않았습니다.

이 check의 DB-free 주장은 GoDj-owned DB/recorder/lifecycle call 0으로 제한됩니다. User `init()` side
effect, recursive/module/embed/remote discovery, writer/upgrade, DB/applied-history check와 actual migrate
execution은 해결하지 않습니다. 따라서 GDJ-0021이 완료됐어도 Q-012/Q-010 전체는 Partial입니다.

Completed GDJ-0022는 이 reference를 independent product global/linked/protocol
kernel과 actual adapter로 구현했습니다. Flat filesystem discovery는 included dependency지만
writer/upgrade와 DB-aware lifecycle은 계속 제외합니다. GDJ-0022 완료 당시 제품 분류는 11 adapter/115
contract의 `110 passing + 5 deviation`이었고 exact 18 hosted acceptance도 완료됐습니다. Completed
GDJ-0025의 REL-001 metadata와 REL-004 required predicate actual까지 포함한 GDJ-0025 완료 시점 aggregate는
12 adapter/127 contract의 `112 passing + 5 deviation + 10 oracle_locked`입니다.
이 GDJ-0022 완료 시점에는 PostgreSQL/MySQL job을 actual backend contract 전까지 만들지 않았습니다. 현재
PostgreSQL required job은 GDJ-0038/EVID-108에서 hosted-verified됐고 MySQL 정책은 불변입니다.

## Q-013 — 관계 API

Current Schema IR은 scalar와 ForeignKey를 version 1 하나로 표현합니다. Relation main model도 scalar와 같은
descriptor/write ABI를 생성하며 app-local relation-query companion을 요구하지 않습니다. 아래 v2/v3 additive
설명은 당시 증거이고 broader relation API가 남아 있어 Q-013은 계속 `Partial`입니다.

초안의 `RelationField[Post]`에는 target type이 없지만 `PostFields.Author.Name`을 사용합니다. symbolic relation binding, target descriptor, generated loader, reverse relation과 import cycle을 한 설계로 검증해야 합니다.

Completed [GDJ-0023](../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md)과
Accepted [ADR-0023](adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 이 질문의 architecture
부분을 contract/test-only evidence로 결정했습니다. Go package/type pointer가 아닌 stable symbolic target
`(app, model)` identity와 source model/field-owned relation declaration, atomic all-app project binder의
target/reverse resolution, generated source package 사이 target-package direct import 금지와 project bridge
ownership, typed/dynamic relation path의 같은 immutable AST 수렴과 unresolved/collision의 pre-I/O
fail-closed를 채택합니다.

Completed [GDJ-0024](../work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md)와
Accepted [ADR-0024](adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)는 metadata-only
product shape를 더 좁혀 구현·검증했습니다. `ir.FormatVersion=2`/existing bytes를 보존하고 exact
`RelationFormatVersion=3` ForeignKey arm과 DSL, mixed v2 target/v3 source app의 additive
`GoDjRelationSchema` companion/project bridge, one-schema-per-app atomic `orm.BindProject`와 reachable
structured errors를 사용합니다. Existing migration tuple `(1,1,1,2)`는 relation을 계속 거부합니다.
REL-001만 제품 metadata actual로 만들고 REL-002..012는 oracle-locked/not-implemented로 유지합니다.

Completed [GDJ-0025](../work/0025-forward-foreign-key-predicate-product-slice.md)와 Accepted
[ADR-0025](adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)는 Q-013 중 required one-hop exact
predicate, shared immutable path와 SQLite reusable INNER JOIN만 구현·검증했습니다. REL-001/004 actual 2/12와
exact implementation-head 26/26 hosted acceptance를 통과했지만 model object wrapper/loader/cache,
nullable/reverse typed/query surface, `select_related`/`prefetch_related`, write/delete/DDL/migration codec와
broader generator ABI는 아직 결정/구현하지 않았습니다. 이 open breadth 때문에 Q-013은 `Resolved`가 아니라
`Partial`입니다. REL-003 object cache와 REL-006 nullable access/isnull은 의도적으로 별도 decision으로
남깁니다.

Completed [GDJ-0026](../work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)과 Accepted
[ADR-0026](adr/0026-forward-foreign-key-object-cache-and-nullability.md)은 그 두 bounded boundary를 함께 동결합니다.
Original descriptor interface를 retain하지 않는 sealed immutable snapshot/storage, project-owned opaque pointer
wrapper와 QuerySet-backed target PK limit-2 cache, nullable local NULL fast path, `source_key` relation terminal
scope와 SQLite JOIN-0 trim을 선택했습니다. Existing project-query v1은 바꾸지 않고 새 object aggregate가
typed/dynamic reviewer isnull을 소유합니다. Exact implementation head `5be46141...`의 run `31370313755`은
26/26 jobs·326/326 recorded steps를 통과했습니다. Reverse, eager/prefetch, write/delete/DDL/migration과
broader target/backend가 열려 있어 Q-013은 계속 `Partial`입니다.

Completed [GDJ-0027](../work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md)과 Accepted
[ADR-0027](adr/0027-reverse-foreign-key-accessor-and-lookup.md)은 Q-013 중 REL-005-only reverse exact lookup와
owner-instance related-set surface를 고정·검증했습니다. Query-only `BindReverse`와 PK-capability
`BindReverseObject`를 분리하고, declaration-centric reverse path 및 project-only query/object aggregates를
사용합니다. Exact implementation head `7db68415...`의 run `31419940399`는 26/26 hosted gate를 통과했고
REL-005를 `passing`으로 전환했습니다.
REL-012 prefetch/IN batching/warm publication, REL-009..011 eager path, write/delete/DDL/migration과 broader
backend가 계속 열려 있으므로 Q-013은 `Partial`입니다.

Completed [GDJ-0028](../work/0028-reverse-foreign-key-prefetch-product-slice.md)과 Accepted
[ADR-0028](adr/0028-reverse-foreign-key-prefetch.md)은 그중 REL-012-only two-stage reverse prefetch를 별도로
동결·검증했습니다. Existing QuerySet이 primary owner query를 소유하고 additive immutable IN condition,
`ReversePrefetch.Load`, sealed source-FK grouping과 private ready `RelatedSet` publication이 batch stage를
소유합니다. Generated project-only companion은 concrete owner/source wrapper를 input order로 반환하되 모든
row 검증 뒤에만 `.Posts().All()` cache를 공개합니다. Exact implementation head `4858ab88...`의 run
`31432551159`는 26/26 hosted gate를 통과해 product를 exact `116 + 5 + 6`, relation 6/12로 전환했습니다.
Custom Prefetch, filter/order 소비, eager REL-009..011, write/delete/DDL/migration과 broader backend가 남아
Q-013은 계속 `Partial`입니다.

Completed [GDJ-0029](../work/0029-one-hop-forward-select-related-product-slice.md)과 Accepted
[ADR-0029](adr/0029-one-hop-forward-select-related.md)는 REL-009/010/011을 required INNER, nullable LEFT OUTER,
reverse multi-valued pre-I/O rejection의 indivisible packet으로 구현·검증했습니다. App-local projection scanner,
singular immutable relation projection, existing object factory에 붙는 All-only eager bridge와 shared
typed/dynamic resolver가 exact implementation head `c02aab67...`의 run `31470292759`에서 26/26 hosted gate를
통과해 product는 exact `119 + 5 + 3`, relation 9/12입니다.
Multiple/nested/no-argument/reverse eager, OneToOne/ManyToMany, write/delete/DDL/migration과 broader backend는
결정하지 않았으므로 Q-013은 계속 `Partial`입니다.

Completed [GDJ-0030](../work/0030-project-bound-protect-and-set-null-delete.md)과 Accepted
[ADR-0030](adr/0030-project-bound-protect-and-set-null-delete.md)은 Q-013 중 REL-007/008 low-level target delete만
분리해 구현·검증했습니다. Canonical `Bind()`와 같은 authoritative declared project universe의 incoming policy fingerprint,
generated v2 scalar/AutoField target deleter, all-PROTECT pre-scan과 canonical SET_NULL→DELETE, SQLite pinned
`AtomicRelation` transaction을 검증합니다. Standalone generator는 raw slice에서 완전히 빠진 undeclared app을
감지한다고 주장하지 않습니다. Generated aggregate binder는 full runtime `Bind()`의 canonical unique incoming-target
identity set과 emitted target set을 exact 비교하고 각 target fingerprint를 검증해 stale/partial companion mismatch를
pre-I/O 거부합니다. Binding/delete companion이 모두 undeclared source보다 stale한 경우는 runtime에서 발견한다고
주장하지 않으며 authoritative generation/check가 precondition입니다. Existing
incoming edge와 일치하는 physical SQLite FK는 supported schema precondition이며 relation DDL/runtime repair는
범위 밖입니다. Existing one-row Delete와
public DB interfaces는 바꾸지 않고 별도 relation ports/generator를 추가합니다. REL-002 assignment/cache
invalidation, canonical project facade, queryset/recursive/CASCADE delete, global cache invalidation, migration/DDL과
non-SQLite는 결정하지 않으므로 Q-013은 계속 `Partial`입니다. 이 packet의 저수준 `BindRelationDeleter`는 Q-017의
최종 application API 답이 아닙니다.

Implementation head `c3803acb...`의 EVID-061/run `31510689383`이 exact 26/26·326/326 hosted gate와
four-coordinate 687/687/0 inventory를 통과했을 때 GDJ-0030 completion product는 exact `121 + 5 + 1`, relation
11/12였습니다. 당시 REL-002만 relation `oracle_locked`로 남았고 canonical facade와 broader
mutation/cache/delete/backend 표면은 open이었습니다.

Django 6.1의 관계 의미는 기본 reference입니다. Raw FK와 관계 accessor 분리, 미조회/NULL/loaded 구분, 첫 접근
cache, eager/prefetch의 동일 cache warming, reverse manager와 조회 origin DB 유지가 기준입니다. GoDj는 Python
descriptor/runtime registry/예외 구현을 복제하지 않고 explicit `context.Context`/`error`, backend/session binding,
Go 값 복사 규칙과 codegen/project bridge로 번역합니다.

Completed GDJ-0032는 `project.Using(backend)`가 relation-aware project model을 반환하고 lazy/eager 모두
`Author(ctx)` 같은 accessor를 공유하는 bounded Gate 0 facade를 first-publish했습니다. Gate 0 이름은 그 bounded
surface에서 canonical입니다. Completed
[GDJ-0033](../work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)과 Accepted
[ADR-0033](adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)은 REL-002 assignment/save/cache
ownership의 exact bounded API를 구현·검증했습니다. Exact implementation head `be6f3d4e...`의 EVID-076/run
`31586910749`은 26/26 jobs·326/326 steps를 통과했고 product는 `122 passing + 5 deviation + 0 oracle_locked`,
relation 12/12입니다. Reverse chaining, general write facade와 upgrade policy는 Q-017에 남습니다.

GDJ-0033의 Django observable semantics는 이미 정해져 있습니다. Relation assignment는 raw FK와 accessor cache를
함께 갱신하고, raw FK change는 stale cache를 지우며, no-PK assigned target은 pre-I/O
`model_state_error/unsaved_related_object`입니다. Manual key-present target은 DB FK가 판단하고, assignment 뒤 같은
target이 key를 얻으면 source save preparation이 이를 reconcile하며, nullable clear는 raw NULL/cache absent입니다.

Go translation은 original source를 보존하는 fresh derived wrapper, exact assigned target pointer, scalar/cache/pending
분리, same target wrapper의 in-place Save, pending-only key snapshot/reconciliation, corrected canonical three-phase preflight와
per-edge COW cache로 Accepted했습니다. Exact public names는 `New`, `Save`, `WithAuthor`/`WithReviewer`, ID helpers와
`ClearReviewer`입니다. 별도 materialization 사이 target identity/global identity map이나 rollback memory rewind는
목표가 아닙니다. 이 bounded decision/code/verification은 REL-002 `passing`만 뜻하며 Q-013의 broader relation/backend나
Q-017의 raw-model UX/capability/namespace/reverse/general upgrade를 닫지 않습니다. Typed generated
`select_related` cause-loss P2는 별도 GDJ-0034에서 private stored error와 deterministic generator v2로 수정되어
EVID-081/run `31605477297`에서 hosted-verified됐지만, 이 좁은 remediation도 Q-013/Q-017 상태나 general coordinated
generated upgrade를 닫지 않습니다. Relation-capable migration은 계속 별도 packet입니다.

## Q-017 — 공개 API와 generated upgrade

GDJ-0036 current ABI는 relation main descriptor/write generation을 통합하고 facade-private write model과
app-local relation-query file을 제거했습니다. Project-owned cross-app surface는 유지합니다. 이 reset은 첫 alpha
전 재기준화이며 project-wide coordinated publication/repair나 final raw-model UX를 닫지 않습니다.

GDJ-0036의 corrected exact head hosted completion 뒤
[GDJ-0037](../work/0037-project-schema-generated-bundle-and-recoverable-publication.md)은 exact correction head
`d4643068...`의 [EVID-105](status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) /
CI #103에서 completed/hosted-verified됐고
[ADR-0036](adr/0036-project-schema-generated-bundle-and-recoverable-publication.md)은 Accepted입니다. ProjectSpec/immutable
bundle, manifest, whole-candidate compile, read-only check와 recoverable publication 하위 경계는 닫혔지만 raw-model
embedding/unwrap/sidecar, capability/namespace와 broader facade/general upgrade UX는 계속 open입니다.

### Historical pre-reset Gate 0와 additive publication evidence

GDJ-0030의 bounded REL-007/008 low-level engine 뒤
[GDJ-0031](../work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md)은 completed이고
[ADR-0031](adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md)은 현재 ADR-0035에 의해
Superseded됐습니다. 다만 test-only feasibility 방법에 한해 당시 Accepted였던 증거는 보존합니다. Relation-delete
physical exact 16 fixture 위에 internal compiletest의 virtual project
source 한 개만 overlay한 logical exact 17 view가 실제 external/root compile gate를 통과했습니다. Production
codegen/generated output과 product manifest를 바꾸지 않았던 pre-reset proof이므로 현재 publication ABI의 증거로
재사용하지 않습니다.

Terminal head `3d661251...`은 EVID-067/run `31533890720`의 별도 exact 26/26·326/326을 통과해
[GDJ-0032](../work/0032-production-forward-project-facade-and-additive-first-publication.md)와
[ADR-0032](adr/0032-production-forward-project-facade-and-additive-first-publication.md)의 clean activation
baseline이 됐습니다. GDJ-0032는 existing generated exact 13을 byte-preserve하고 project-only companion 한 파일만
additive first-publish했습니다. Activation EVID-068과 implementation EVID-069는 서로 다른 exact historical head를
증명합니다. ADR-0032의 additive byte-preservation rule은 현재 ADR-0035에 의해 Superseded됐고, bounded Gate 0의
동작 증거만 보존합니다.

이번 spike에서 검증한 범위는 다음 forward read-only compile 후보입니다.

- Project facade의 exact name과 one-time backend/session binding
- Exact `post, found, err := ...OrderBy(...).First(ctx)`와 error-returning `Limit`의 Go ergonomics
- Raw app model과 relation-aware source model의 역할, private wrapper에서 explicit `Model()` unwrap
- Lazy/eager의 동일 source pointer type과 `Author(ctx)` accessor
- Filter/OrderBy/Limit 뒤에도 facade type과 relation state가 유지되는지
- `db.RelationSession → db.Queryer` structural assignability와 explicit origin 전달
- Future generated identifier/collision/deprecation/upgrade 정책을 결정하기 전에 필요한 compile gates

GDJ-0032가 구현·고정한 product 경계는 다음과 같습니다.

- Project-local capability는 `db.Queryer + db.Mutator`이고 `db.RelationAtomic`은 제외합니다. 정적 `db.Queryer`
  입력은 negative입니다.
- 모든 declared model에 project-owned pointer wrapper/query root가 있으며 forward accessor는 target query root와
  같은 target wrapper type을 반환합니다.
- Required Author는 target wrapper + error, nullable Reviewer는 target wrapper + present + error shape를 사용합니다.
- Low-level `Object`와 application wrapper namespace는 disjoint이고 raw model은 explicit unwrap으로만 접근합니다.
- Author/Reviewer는 common selector와 concrete eager evaluation state를 공유하며 eager `All`마다 cache를 새로
  만들지 않습니다.
- Session-origin은 callback 안에서만 지원하지만 warm cache 때문에 callback 이후 deterministic failure를 계약하지
  않습니다.

Reverse manager/chaining, 별도 materialization 간 stable target wrapper pointer identity, downstream target cache,
delete facade, wrapper JSON과 사용자 model method는 GDJ-0032의 비목표입니다. REL-002의 bounded write API는
ADR-0033에서 결정했고 GDJ-0033/EVID-076에서 product로 구현·검증했습니다. 그 결과를 reverse/general facade,
coordinated generated upgrade, relation-capable migration 또는 non-SQLite support로 확대하지 않습니다.

저수준 `Bind*`, `From`, `ForwardSelect*` API나 EVID-065의 compile success만으로 최종 사용자 API가 확정됐다고
보지 않습니다. GDJ-0032는 `Backend`, `Using`, `Models`, singular `AuthorsAuthor`/`BlogPost` roots와 wrappers,
`BlogPostRelationSelector(s)`, `BlogPostEagerQuery`, `Unwrap`을 이 bounded facade의 canonical Gate 0 surface로
결정했습니다. 이 결정은 reverse/write manager나 전체 ORM naming policy를 확정하지 않습니다.

### Current post-reset Q-017 open boundary

Django에서 scalar field, user-defined model method와 relation access는 한 logical model 경험입니다. GoDj는 lazy I/O의
explicit `context.Context`/`error`를 유지하지만, bounded Gate 0의 explicit `Unwrap`만을 전체 장기 model UX로 자동
동결하지 않습니다. Broader reverse/general facade를 열기 전에 embedding/promotion, explicit unwrap, project sidecar를
external compile prototype으로 비교해 raw fields/user methods, namespace collision, copy/JSON과 relation state가 함께
유지되는 방식을 Q-017에서 결정합니다. 이는 Accepted Gate 0/ADR-0033 bounded write 경계를 재개방하지 않습니다.

Binder-first, original binder-error precedence, valid binding 뒤 nil/typed-nil backend의
`backend_error/invalid_plan`과 I/O 0은 stable contract이고 detail message만 noncontractual입니다. Current generator는
ProjectSpec, project snapshot/ABI manifest, 전체 candidate compile, sealed-root check와 journaled publication/recovery를
제공합니다. Renderer rename/deprecation, installed version negotiation과 general repair/upgrader UX는 제공하지 않으므로
broader generated upgrade 질문은 Q-017에 남습니다.

GDJ-0033이 좁게 Accepted한 forward relation assignment/save의 observable behavior와 binder/cache ownership은
유지합니다. 당시 existing exact 13 보존과 single project-facade companion publication rule은 ADR-0035 current main
ABI reset으로 Superseded됐습니다. Project-wide current bundle publication은 GDJ-0037에 구현됐지만 broader capability
split, namespace, raw-model UX와 general upgrade는 여전히 open입니다.

Current manifest는 normalized Schema/layout, 13-role generator ABI와 exact output digest 전체를 project snapshot 아래
증명합니다. 남은 provenance/upgrade 질문은 installed library/runner/generator version negotiation, renderer
rename/deprecation과 first-alpha 이후 compatibility 기간입니다. Bounded REL-002 behavior의 무조건적 선행 blocker로
과장하지 않습니다.

## Q-019 — SQLite unknown-outcome retained connection resource policy

Accepted [ADR-0030](adr/0030-project-bound-protect-and-set-null-delete.md)은 rollback/discard 확인이 모두 실패한
unknown-outcome physical connection을 pool에 돌려주지 않고 backend-private retained set에 보관한 뒤
`Backend.Close`에서 seal/drain합니다. 이는 raw transaction reuse를 막는 안전한 current behavior지만, backend가 오래
살고 unknown-outcome fault가 반복되면 retained connection/lock/resource가 Close 전까지 계속 증가할 수 있습니다.

Q-019는 다음 선택을 별도 P1 work에서 결정합니다.

- Current unbounded retention을 명시적 operational contract로 유지할지
- Backend-level cap/quarantine/health degradation을 추가할지
- External reconciliation token 또는 forced backend shutdown을 요구할지
- Retained resource/lock을 어떤 metric과 error surface로 노출할지

GDJ-0033은 `db/**`를 수정하지 않고 이 질문에 답하지 않습니다. Behavior를 바꾸려면 ADR-0030을 조용히 다시 쓰지
않고 새 ADR/work에서 명시적으로 amend 또는 supersede해야 합니다. 단순 문서/telemetry clarification이 아니라
connection reuse, transaction outcome 또는 public error 의미가 달라지면 exact failure-injection과 long-lived resource
growth gate가 필요합니다.

새 작업이 이 표의 질문에 의존하면 추측으로 확정하지 말고 작업 문서에 명시하고 필요한 ADR/prototype을 먼저 만듭니다.

## Historical GDJ-0035 Accepted decision impact

이 절의 dual profile/state/context handoff/optional backend와 Phase D4g 순서는 GDJ-0036 이전 기록입니다.
[ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)가 현재 제품 계약을 대체했고
MIG-075..086 publication은 retire됐습니다.

[GDJ-0035](../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)는 Q-010/Q-012/Q-013에
의존하지만 세 질문을 닫지 않습니다. Q-010/Q-012/Q-013은 계속 `Partial`, Q-017/Q-019는
P1/open입니다.

- Q-010: exact global check/public project runner는 있지만 writer/autodetector/upgrade·generator/library semver은 open입니다.
- Q-012: relation tuple/state/codec/SQLite lifecycle boundary를 MIG-075..086과 Accepted ADR-0034에 동결했지만 custom/data operation,
  DB-aware public migrate command, repair/crash policy와 non-SQLite는 open입니다.
- Q-013: AutoField-target ForeignKey migration은 broader relation/backend 질문의 bounded Accepted decision일 뿐이며
  OneToOne/ManyToMany/`to_field`/self/cyclic/inbound/non-SQLite를 닫지 않습니다.
- Q-017: relation facade/general generated upgrade를 바꾸지 않습니다.
- Q-019: unknown commit outcome의 no-retry error meaning만 보존하고 retained connection cap/reconciliation은 결정하지 않습니다.

[ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)는 bounded design에 한해
Accepted입니다. Phase A/B와 Phase C exact 8-test-only decision proof는 EVID-085..090에서 local/hosted 검증됐고,
Proposed decision-freeze docs head `5bdf013...`는 EVID-091/run `32183309328`에서 별도로 검증됐습니다. Exact
relation constants, one-loader dispatch, digest v2, whole-step state transition, wire `target_field` 제거,
three-stage preflight, additive existing-fence port/four capabilities와 SQLite order는 Accepted design으로
동결됐습니다. Later D1 definition/handoff, D2 private state/readiness와 D3a direct optional SQLite
Create/Delete port는 [EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)의
분리된 product/correction head에서 구현·검증됐습니다. D3b normal loaded core integration은
[EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)의
별도 `74c2b72...`/`167ef03...` head에서 구현·검증됐습니다. Normal loaded relation-bearing CreateModel은
apply/unapply/reapply합니다. D4 exact one-test-file head `424ec4d...`는
[EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)에서
fresh backend/loaded set과 structured durable snapshot을 사용한 bounded close/reopen scenario만 Verified했습니다.
EVID-096 docs head `62df9b2...`는 run `32260744096`에서 고유하게 닫혔습니다. D4d final head `dd83362...`는
[EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`에서 public changed-target-only/private sealed same-target expansion과 native nullable
ForeignKey Add를 구현·검증했습니다. Capability는 `{true,true,false,false}`이고 required Add/Remove-remake,
general restart/actual adapter는 당시 미지원이었으며 Q 상태도 불변입니다. EVID-097 documentation head `c59669c...`는
run `32278555810`에서 고유하게 닫혔고 D4e final head `1d86f6e...`는
[EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`에서 empty-source required `PROTECT` Add를 구현·검증했습니다. Capability는
당시 `{true,true,true,false}`였고 populated required Add/Remove-remake, general restart/actual adapter는
미지원이었으며 Q 상태도 불변입니다. EVID-098 docs head `85f9270...`는 CI #94/run `32288383027`에서 닫혔고
D4f final head `9d5b894...`는
[EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95/run `32294983953`에서 bounded Remove-by-remake를 구현·검증했습니다. Capability는
`{true,true,true,true}`이고 MIG/Q 상태는 불변입니다. Populated required Add/reapply, arbitrary/general remake,
general restart와 actual adapter는 미지원입니다.
Acceptance docs head `7cdc6d6...`는 EVID-092/run `32187094845`의 unique exact-head hosted gate를 통과했으며
EVID-091을 재사용하지 않았습니다. EVID-093은 각 D1/D2/D3a bounded slice만 증명하며
EVID-094는 D3b product/correction head만, EVID-095는 D4 verification head만 증명합니다. EVID-097/098/099도
MIG contract 분류나 Q 상태를 바꾸지 않습니다.
