# GoDj 로드맵

- 상태: Accepted direction
- 현재 active batch: [GDJ-0048](../work/0048-canonical-application-model-facade-and-current-generated-abi.md)은
  Q-017의 application-facing generated model을 current ABI v3로 재기준화합니다. Proposed
  [ADR-0050](adr/0050-canonical-embedded-application-model-facade.md)은 raw scalar/app method promotion과 existing
  project-owned relation state를 결합하고, direct FK reconciliation, fail-closed method namespace, pointer/copy safety와
  explicit raw `Unwrap`/DTO-only Web representation을 한 whole-bundle 배치로 검증합니다. Reverse/general manager와 post-alpha upgrader는
  계속 open입니다. Activation head `1070ec3...`의 CI #157은 corrected baseline을 exact 27/27로 확인했고, 현재 checkout은
  facade v3 product와 source-bound PostgreSQL gate에 이어 EVID-140 첫 local final을 통과했습니다. CI #158의 네
  relation-product failure는 stale inventory lock으로 한정됐고, EVID-141 corrected source에서 current 1,073/1,073/0,
  required PostgreSQL 18/18, full `make ci`, Linux/386, 1,088-file external archive/audit를 다시 통과했으며 corrected
  exact submitted hosted 검증만 남았습니다.
- 현재 단계: [GDJ-0039](../work/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)은 EVID-110에서
  QRY-022..033을 hosted-verified하고 completed됐습니다. 후속
  [GDJ-0040](../work/0040-composable-typed-boolean-predicates-and-article-search.md)과
  [GDJ-0041](../work/0041-typed-scalar-comparisons-field-references-and-article-filtering.md)도 completed됐습니다.
  [GDJ-0042](../work/0042-project-linked-runserver-and-article-development-loop.md)는
  [Accepted ADR-0042](adr/0042-project-linked-runserver-and-article-development-loop.md)의 WEB-011..020 optional
  runtime package, read-only bundle preflight, loopback-only global `runserver`와 Article actual child 개발 루프를
  source `810149f...`에서 locally implemented했습니다. SQLite와 digest-pinned PostgreSQL 17.10 actual 및
  affected gates는 통과했습니다. Initial `47b0eb8...` local final 뒤 first submitted `46a57aa...` run은 26 success와
  macOS Intel 20-minute timeout 하나로 끝났습니다. Correction `2b49938...`의 EVID-121에서 30-minute budget/locks와
  full/386/803-file archive/audit refreeze가 통과했고 submitted `2bfdbd5...`의 EVID-122/run `32659704239` exact
  27/27 jobs·358/358 steps가 bounded WEB-011..020을 hosted-verify하고 work를 completed로 닫았습니다.
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md)은 safe template/Form과
  process-lifetime session/auth/CSRF/Article Admin을 한 wide vertical flow로 구현했고, frozen source
  `8bcfa213...`의 EVID-123 local final 뒤 submitted `5eda0a4...`의
  [EVID-124](status/TEST_EVIDENCE.md#evid-20260824-124--gdj-0043-exact-head-hosted-completion) / CI #134가 exact
  27/27 jobs·358/358 steps, 네 플랫폼 993/993/0과 PostgreSQL required 14/14·skip 0을 통과했습니다.
  ADR-0043/0044는 Accepted, exact 30 contracts는 `25 passing + 5 Verified deviations`, work는 completed입니다.
  Durable session/user/audit와 M5/M6 전체 완료는 계속 제외합니다. 후속
  [GDJ-0044](../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md)는 DRF 3.18.0 isolated
  reference, closed int64 route와 session-authenticated Article JSON CRUD를 구현했습니다. Exact source
  `d9c1971...`의 EVID-125/126과 CI #142는 18 contracts=`13 passing + 5 deviation`, 27/27 jobs·359/359 steps,
  네 relation coordinate 1,017/1,017/0, portable runserver required 16/16·skip 0와 PostgreSQL required
  15/15·skip 0을 검증했습니다. ADR-0045/0046은 Accepted이고 GDJ-0044는 completed입니다. 이 terminal
  경계 뒤 [GDJ-0045](../work/0045-durable-single-runtime-system-state-and-article-restart.md)가 completed됐습니다.
  그 GDJ-0045 checkpoint product는 SYS-001..012를 global registry/Makefile/`godjcheck`에 게시했고 exact 11 `passing` + SYS-009
  DEV-0008 `deviation`, product 20/219=`203+16`입니다. Local actual
  A/B는 12,944 bytes/SHA-256 `f30ac1a4...d41c3a6`로 byte-identical하고 SQLite distinct-process restart가
  통과했습니다. Exact submitted `e673b3a...`의 EVID-129/CI #146은 PostgreSQL required 16/16·skip 0과
  필수 GitHub Actions 27/27 jobs·359/359 steps를 통과해 ADR-0047 Accepted, DEV-0008 Verified와 work completion을
  닫았습니다. 후속 [GDJ-0046](../work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md)은
  Accepted [ADR-0048](adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)의 additive SQLite/PostgreSQL
  `db.CoordinatedAtomic`, fenced system-state/Article writers, explicit shared CSRF key ring과 two-process backend restart를
  구현했습니다. Corrected frozen source `29d62469...`의 EVID-133 local full/386/1,055-file external archive 뒤
  EVID-134/CI #153 exact hosted matrix가 terminal acceptance를 닫았습니다. SYS-013..020은 모두 product `passing`이고
  그 terminal reference는 21/239/420=`211+16+12 locked`, product는 20/227=`211+16`이며 remaining locked range는
  MIG-075..086뿐입니다. General Unique/Integer/CAS IR, non-cooperative writer, session-family revocation, JWT/OAuth와
  production topology는 이 packet에 포함하지 않습니다.
  Completed [GDJ-0047](../work/0047-api-authentication-profiles-and-bearer-article-api.md)은 common
  authentication boundary와 strict injected Bearer resource-server profile을 게시했습니다. AUT-009..016/API-011..012
  actual 10/10, SQLite와 digest-pinned PostgreSQL 17.10 Article Bearer E2E 및 two-process attestation이 통과했고
  분류는 일곱 `passing` + AUT-012/013/015 Verified DEV-0009 `deviation` 세 개입니다. Current reference는
  22/249/462=`218+19+12 locked`, product는 21/237=`218+19`입니다. Initial run `33044776835`는 26 jobs success 뒤
  macOS Intel product job 한 건이 30분 outer timeout으로 취소됐습니다. EVID-137의 Intel-only 45분 correction과 corrected
  full/386/archive refreeze 뒤 EVID-138/CI #155 run `33049861740`이 exact 27/27 jobs·360/360 steps success로
  통과했으므로 ADR-0049는 Accepted, GDJ-0047은 completed/Verified입니다. JWT/opaque issuance, refresh,
  OAuth/OIDC와 production BFF도 제외합니다.
  GDJ-0040 Phase A
  `fe4996f...`/EVID-111은 독립 Django QRY-034..043 reference를 고정했고, Phase B/C product
  `86d6b169...`/actual `0ec6f385...`는 immutable typed Boolean tree, SQLite/PostgreSQL recursive compiler와
  bounded Article 검색을 구현했습니다. QRY-034..043은 10/10 `passing`; reference는
  15/161/210=`144+5+12 locked`, product는 14/149=`144+5`입니다. First hosted run `32641160967`은 stale
  916-test workflow lock에서만 네 relation-product 좌표가 실패했습니다. Correction `73b912d...`는 current
  950/950/0 inventory를 잠갔고 EVID-114에서 full/386/775-file source-clean-copy를 새 bytes로 다시 통과했습니다.
  Corrected submitted head `136e825...`는 EVID-115/run `32642341459`의 exact 27/27 jobs·341/341 steps와
  네 플랫폼 950/950/0, PostgreSQL 17.10/QRY-034..043 actual을 통과해 completed/hosted-verified됐습니다.
  GDJ-0041 current source `7f2bb223...`은 Integer/String range, sealed same-model/same-kind `orm.F`, RHS
  union/source validation, nullable RHS odd-`NOT`, SQLite/PostgreSQL identifier RHS와 bounded Article advanced
  exactly-two-query filter를 구현하고 local-final gates를 통과했습니다. QRY-034..053은 20/20, 신규
  QRY-044..053은 10/10 zero-diff이고 reference는 15/171/210=`154+5+12 locked`, product는
  14/159=`154+5+0`입니다. Submitted head `e97a4e3...`는
  [EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) / run
  `32647746430`의 exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual을
  통과해 GDJ-0041을 completed/hosted-verified로 닫았습니다.
  Q-010/Q-011/Q-012/Q-013은 `Partial`, Q-017은 GDJ-0048에서 active입니다.
- 직전 GDJ-0035 evidence snapshot: [GDJ-0035](../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)는
  당시 유일한 active contract-first packet이었습니다. Completed [GDJ-0034](../work/0034-typed-generated-select-related-cause-preservation.md)의
  terminal head `0bb8c969...`는 EVID-083/run `31613170021`의 고유 exact 26/26 jobs·326/326 steps와 audit
  P0..P3=0을 통과했습니다. Product/Q 상태는 불변입니다. GDJ-0035는 MIG-075..086 exact 12
  planned contracts와 당시 Proposed ADR-0034를 활성화했으며 source/workflow/artifact/product 변경은 0입니다.
  Exact 16-document activation head `52f9bcb7...`는 EVID-084/run `31618469072`의 고유 exact
  26/26 jobs·326/326 steps와 audit P0..P3=0을 통과했고 EVID-083을 activation proof로 재사용하지 않았습니다.
  Phase A reference-only artifacts는 EVID-085에서 로컬 고정했고 exact committed head `84e16bf...`는
  EVID-086/run `31625898551`의 고유 attempt-1 exact 26/26 jobs·326/326 steps와 hosted audit
  P0..P3=0을 통과했습니다. Reference는 exact 13 set/139 contract/156 ordered cross-binding=
  `122 passing + 5 deviation + 12 oracle_locked`이고 product는 불변입니다. Phase A는 hosted-verified됐습니다.
  Phase B exact 14개 `_test.go` no-product feasibility head `c2ecb292...`도 EVID-088/run `31653237691`의
  고유 attempt-1 exact 26/26 jobs·342/342 steps와 hosted audit P0..P3=0을 통과했습니다. Phase B는
  hosted-verified됐습니다. Phase C exact 8-test-only decision proof head `7d36502...`는 EVID-089/090과
  unique run `32174259324`의 exact 26/26 jobs·342/342 steps, audit P0..P3=0을 통과했습니다. Relation
  Proposed docs-freeze head `5bdf013...`는 EVID-091/run `32183309328`의 별도 local/hosted gates와 audit
  P0..P3=0을 통과했고 그 성공을 근거로 이 별도 documentation head에서 ADR-0034 bounded design을 Accepted로
  전환했습니다. Acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`의 고유 exact 26/26 jobs·342/342
  steps와 audit P0..P3=0을 통과했습니다. 그 후 Phase D1 definition/handoff, D2 private state/readiness,
  D3a direct optional SQLite Create/Delete port가 [EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)의
  분리된 product/correction heads와 runs `32195313382`, `32205324145`, `32218003207`에서 각각
  Implemented/Verified됐습니다. D3b normal loaded relation core product `74c2b72...`와 inventory correction
  `167ef03...`도 [EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification) /
  run `32231149900`의 exact 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다. D4 exact
  one-test-file verification head `424ec4d...`도
  [EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification) /
  run `32248885053`의 exact 26/26·342/342를 통과해 기존 product path의 bounded captured-snapshot restart를
  Verified했습니다. D4b exact 18-document head `84588f9...`는 run `32252834752`의 unique exact
  26/26·342/342와 audit P0..P3=0을 통과했고, D4c exact one-test-file head `e4fbc7b...`도
  [EVID-096](status/TEST_EVIDENCE.md#evid-20260819-096--gdj-0035-d4b-documentation-and-d4c-loaded-relation-error-taxonomy-verification) /
  run `32256113658`의 unique exact 26/26·342/342와 audit P0..P3=0에서 six-case loaded SQLite taxonomy를
  검증했습니다. EVID-096 exact-six docs head `62df9b2...`도 run `32260744096`의 고유 exact
  26/26·342/342와 audit P0..P3=0에서 닫혔습니다. D4d product `3950d98...`, inventory lock `28b141e...` 뒤
  첫 hosted run `32267789056`은 macOS Intel race test의 wall-clock assertion P1로 25/26 jobs에서 실패했고,
  deterministic resource-scan count fix `dd83362...`를 적용했습니다. 이 final head는
  [EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
  run `32271361724`의 고유 exact 26/26·342/342와 audit P0..P3=0에서 sealed same-target nullable ForeignKey Add를
  검증했습니다. EVID-097 docs head `c59669c...`는 run `32278555810`에서 별도로 닫혔고 D4e product
  `7c07805...`/inventory `1d86f6e...`는
  [EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
  run `32282269755`의 고유 exact 26/26·342/342와 audit P0..P3=0에서 empty-source required ForeignKey Add를
  검증했습니다. EVID-098 docs head `85f9270...`는 CI #94/run `32288383027`에서 별도로 닫혔고 D4f product
  `4982e27...`/inventory lock `9d5b894...`는
  [EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
  CI #95/run `32294983953`의 고유 exact 26/26·342/342와 audit P0..P3=0에서 bounded ForeignKey
  Remove-by-remake를 검증했습니다. 그 기준점의 다음 단계는 D4g observer-only characterization이었으나
  GDJ-0036 activation에서 publication 순서를 중단했습니다.
- 현재 checkout 제품 기준: 21 adapters/237 contracts의 `218 passing + 19 deviation + 0 oracle_locked`; relation
  REL-001..012 12/12, query expression QRY-034..053 20/20, GDJ-0043 exact 30=`25 passing + 5 Verified
  deviations`와 GDJ-0044 exact 18=`13 passing + 5 Verified deviations`가 exact-head hosted-verified됐고,
  GDJ-0045 exact 12=`11 passing + 1 Verified deviation`과 GDJ-0046 SYS-013..020 exact 8 passing도
  exact-head hosted-verified됐습니다. GDJ-0047 exact 10=`7 passing + 3 Verified deviations`는 product publication,
  SQLite/PostgreSQL required와 corrected final local full/386/archive 뒤 EVID-138/CI #155 exact hosted
  27/27 jobs·360/360 steps를 통과했습니다.
- 마지막 검토: 2026-08-27

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

GDJ-0003은 write/schema/transaction reference 계약을 별도 set으로 잠갔고,
[GDJ-0004](../work/0004-write-migration-walking-skeleton.md)는 generated write API,
SQLite transaction과 최소 ProjectState/Executor/editor/recorder 제품 단면으로
MOD-001..007과 MIG-001..004를 통과했습니다.

상태: M2 전체는 아직 완료되지 않았습니다. Mutable instance `Save()`,
loaded/new/force/explicit PK와 rollback의 외부 의미는
[GDJ-0005](../work/0005-save-lifecycle-compatibility-contracts.md)에서 MOD-008..019의
12개 reference 계약으로 고정했고,
[GDJ-0006](../work/0006-save-lifecycle-product-slice.md)은 typed Save option/field mask,
explicit key와 SQLite error 경계를 구현해 12개 모두 통과했습니다. 그 시점에는 public
migration file, autodetector, graph, lock과 crash recovery가 이후 별도 work/ADR 범위였습니다.

M1부터 열린 Q-007을 먼저 닫기 위해
[GDJ-0007](../work/0007-queryset-evaluation-cache-compatibility-contracts.md)은 QuerySet
evaluation/cache의 QRY-011..021 exact reference 계약을 네 번째 set으로 고정했습니다.
당시 기존 제품 34개는 계속 `passing`이었고 새 11개는 `oracle_locked`였습니다.
[GDJ-0008](../work/0008-queryset-evaluation-cache-product-slice.md)은 Go-native value-copy/
concurrency ownership과 terminal API를 ADR-0012로 결정하고 실제 adapter를 연결해
QRY-011..021을 모두 `passing`으로 올렸습니다. GDJ-0008 완료 당시 검증된 manifest contract는 총
45개이며, 이는 M2 migration이나 M4 QuerySet breadth 전체 완료를 뜻하지 않습니다.

[GDJ-0009](../work/0009-migration-planning-compatibility-contracts.md)은 기존
MIG-001..004 executor/editor 제품 경계를 바꾸지 않고 MIG-005..016으로 dependency graph,
applied state, multi-target forward/backward plan과 잘못된 graph/history의 외부 의미를
다섯 번째 exact set에 고정했습니다. 기존 45개 제품 contract는 계속 `passing`이고 새
12개는 `oracle_locked`이므로 총 57개를 제품 통과로 표현하지 않습니다.

[GDJ-0010](../work/0010-immutable-migration-planner-product-slice.md)은
[ADR-0013](adr/0013-immutable-migration-planner.md)의 immutable identity graph와 별도
AppliedState를 backend-neutral zero-I/O Planner로 구현하고 fifth-set actual adapter를
연결했습니다. MIG-005..016도 `passing`이 되어 GDJ-0010 완료 당시 다섯 제품 set의 검증 범위는 총
57개였습니다. 이 planning adapter의 zero-I/O는 실제 DB probe가 아니라 pure structural
경계로 검증합니다.

[GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md)은
MIG-017..026으로 여러 migration의 migration별 transaction, 중간 실패의 durable/rollback
경계, 이후 단계 중단, ProjectState progression, mixed preflight와 empty no-op를 여섯 번째
exact set에 고정했습니다. 총 reference contract는 67개지만 새 10개는
`oracle_locked`이고 제품 `passing`은 기존 57개뿐입니다.

완료된 [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md)는 최소
`ExecutePlan`과 full zero-I/O preflight, migration별 기존 Apply/Unapply 실행, first-failure
last durable state를 구현했습니다. Django backward의 `schema_then_record`와 달리 schema와
recorder를 같은 transaction으로 유지하는 결정은
[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
Accepted/Verified 상태입니다. GDJ-0012 완료 당시 제품 분류는
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0013](../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)은
recorder table 없음/empty/record/unrecord, fresh executor의 applied-prefix tail plan,
unknown legacy row와 explicit inconsistent-history preflight, 중간 실패 뒤 재계획을
MIG-027..036의 일곱 번째 exact set으로 고정했습니다. Reference 총계는 77개지만 새 10개는
`oracle_locked`이고 GDJ-0013 완료 당시 제품 상태는 계속
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0014](../work/0014-recorder-backed-restart-planning-product-slice.md)는 Accepted
[ADR-0015](adr/0015-recorder-backed-applied-state.md)의 별도 raw read port,
`LoadAppliedState`, explicit `Planner.CheckHistory`와 SQLite read-only reader를 구현했습니다.
Fresh file-backed restart를 포함한 MIG-027..036이 10 `passing`으로 전환되어 GDJ-0014
완료 당시 제품 분류는 `73 passing + 4 deviation`이었습니다. Read/check/plan은
`ExecutePlan`과 한 API가 아니며
snapshot과 실행 사이 lock을 보장하지 않습니다.

완료된
[GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
loaded migration definition으로 explicit empty, first/middle before·after, cross-app
dependency, multiple target/shared dependency, omitted-target latest leaves와 applied-prefix/
unrelated-known startup `ProjectState`, unknown legacy identity의 schema-state 제외 의미를
MIG-037..046의 여덟 번째 exact set으로 고정했습니다. 새 10개는 `oracle_locked`이고
기존 일곱 product set은 `73 passing + 4 deviation`이므로, reference 87개 전체를 제품
통과로 표현하지 않습니다. 여덟 set의 contract/scenario는 전역으로 유일하고 56개 ordered
cross-binding이 거부됩니다.

완료된
[GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)과 Accepted
[ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 loaded-definition replay를
별도 immutable reconstructor로 구현했습니다. Explicit empty/latest/before/after/applied tagged
request, Planner graph kernel 공유, definition/operation deep-copy와 structured error를 검증했고
MIG-037..046은 10 `passing`, GDJ-0016 완료 당시 제품 분류는
`83 passing + 4 deviation`입니다. Public
migration file/source loader, CLI, data callback과 lifecycle lock/crash recovery는 계속 후속
범위입니다.

완료된
[GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
source loader/CLI보다 lifecycle 경계를 먼저 계약화합니다. Fresh/applied-prefix/no-op에서
latest, named forward/reverse, app zero target, unknown legacy, inconsistent known history와
중간 실패/restart를 MIG-047..056의 아홉 번째 exact set으로 잠갔습니다. 제품 source와 GoDj
adapter를 만들지 않았으므로 GDJ-0017 완료 당시 결과는 기존 `83 passing + 4 deviation`과 새
`10 oracle_locked`를 구분한 9 set/97 contract였습니다. 72개 ordered cross-binding은 모두
validation에서 거부됐습니다.

별도 [Accepted ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)은 recorder identities와
opaque freshness revision을 같은 snapshot으로 읽고 expected token을 각 migration transaction의
첫 DDL/write 전에 검증합니다. `conformance/lifecyclefence/**` spike는 persistent epoch와
monotonic revision 후보로 stale-before-write, step 사이 경쟁, simultaneous contender, fault
rollback과 resource release를 입증했지만 제품 storage/encoding을 고정하지 않았습니다. Outer
transaction과 automatic retry 없이 ADR-0014의 migration별 durable commit을 보존합니다.
GDJ-0017 시점에는 제품 source/API가 없었으며 loader/operation codec, project-binary/CLI, data
callback, exclusive cutover와 crash repair/lease는 후속 범위로 남았습니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 already-loaded
`[]Migration`과 실제 `Executor.Backend`를 사용하는 tagged latest/targeted
`Executor.Migrate`를 제품화했습니다. Existing port를 widen하지 않는 backend-owned opaque
revision session은 exact-one snapshot, mandatory Close와 call 사이 connection-free lifetime을
가지며, dedicated fenced transaction은 rolled-back/committed/unknown durability를 구분합니다.
SQLite metadata v1의 epoch/revision/fingerprint와 pinned `BEGIN IMMEDIATE`가 schema, recorder와
successor revision을 한 transaction에 결속합니다. Unsupported fallback과 existing-recorder
자동 adoption은 없고, rolled-back/unknown 뒤 SQLite session은 poison/no-retry입니다.

Accepted ADR-0013의 canonical ascending planner policy도 유지했습니다. Lifecycle 9개는 exact
`passing`, MIG-052의 `result.plan[0..2]`/`metrics.steps[0..2]` 여섯 path만 DEV-0002 sparse
expectation으로 검증해 GDJ-0018 완료 당시 9 product set은
`92 passing + 5 deviation`이었습니다. 기존 DEV-0001과 locked lifecycle
oracle/static/SHA256SUMS, completed `conformance/lifecyclefence/**`는 변경하지 않았습니다.
Default-bearing SQLite `AddField`는 empty table에서 logical default를 보존하고 physical
persistent default 없이 적용하며 nonempty table은 계속 unsupported입니다.

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)은 explicit
caller-provided definition document, strict data-only JSON v1, compatibility tuple `(1,1,1,2)`,
fully normalized Schema IR v2, closed `CreateModel`/non-PK `char`·`boolean` `AddField` codec와 atomic
deterministic load를
MIG-057..064로 잠갔습니다. [Accepted ADR-0019](adr/0019-versioned-migration-definition-source.md)는
Django의 Python migration file ABI를 복제하지 않고 named identity/dependency/ordered operation
의미를 Go data format으로 재설계합니다.

GDJ-0019는 당시 기존 9 product adapter의 `92 passing + 5 deviation`을 그대로 보존하면서
8 `oracle_locked`를 더해 10 reference set/105 unique contract와 90 ordered cross-binding
rejection을 검증했습니다. Exact tuple, strict JSON, canonical digest, atomic loader snapshot과
failure precedence는 ADR-0019 decision provenance이고 Django에서 실제 관찰한 공통 동작은
MIG-057/MIG-064의 별도 provenance로 구분합니다.

`conformance/definitionload/**`의 test-only proof는 실제 `migrations.NewPlanner`와
`Executor.Migrate` handoff를 실행하지만 제품 source나 importable package가 아닙니다. Product
loader와 열 번째 GoDj adapter는 GDJ-0019 당시 구현하지 않았습니다. MIG-064도 당시에는 existing
`Executor.Migrate`에 대한 oracle reference handoff shape이지 Go product loader 지원이
아니었습니다.

[완료된 GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)은 baseline
`eecc75f7507414ad6043a090c97b84080ab0fb8b`에서 activation되어 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)의 새 leaf package
`migrations/definition`, explicit `Source{SourceID,Document}`와
`Load(...Source) (Set, LoadReport, error)`, zero-value empty set, immutable failure/report와 existing
`Executor.Migrate` handoff를 구현했습니다. Raw source/JSON과 semantic fan-out은 source 2,048,
SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, JSON depth 64, document values 65,536,
batch values 262,144, dependencies 2,047, operations 2,048, `CreateModel` fields 2,048의 exact
10 cap으로 bounded하고 Schema IR wire coordinate는 literal 2 + compile drift gate로 잠급니다.

Loader-owned atomic snapshot은 raw document를 보존하지 않고 accessor/report/error mapping마다
fresh deep copy를 반환합니다. Strict scanner는 closed JSON, any-depth duplicate,
UTF-8/surrogate/numeric lexeme와 canonical RFC 6901 failure order를 bounded lazy path로 처리합니다.
Source-owned 9-code/resource-limit context와 raw `*migrations.PlanningError`/lifecycle error의
ownership을 분리하고, `Set.Migrate`는 existing lifecycle에 exactly-once로 위임합니다.

MIG-057..064의 열 번째 actual adapter는 Django parity가 아닌 decision-reference 8개를
`passing`으로 전환했습니다. GDJ-0020 완료 당시 분류는 exact 10 adapter/105 contract의
`100 passing + 5 deviation`이며 90 ordered cross-binding이었습니다. Status-only manifest 외
기존 reference oracle, static fixture, `SHA256SUMS`와 test-only candidate는 변경/승격하지
않았습니다.

제품 commit `6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)의 Ubuntu 24.04와
macOS 15 arm64 job에서 통과했고 Ubuntu는 실제 Linux/386 focused runtime을 검증했습니다.
Completion-documentation commit `a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 별도
[run 31310002784](https://github.com/progresshans/godj/actions/runs/31310002784)의 두 job에서
통과했습니다. EVID-023 append/status 교정 baseline commit
`53729103651bfc34acc5fe07fb4376d5dd78c204`도 별도
[run 31310606332](https://github.com/progresshans/godj/actions/runs/31310606332)의 Ubuntu/macOS 두
job에서 통과했습니다.

GDJ-0020 이후에도 CLI/project orchestration은 별도 결정입니다. Directory/file/module/remote
discovery, global CLI/library/generator semver handshake, writer/upgrade/cache, executable/custom/
data/raw-SQL operation, adoption/repair command, crash recovery와 non-SQLite backend는 이 product
loader slice의 지원 범위가 아닙니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)은 다음 제품
구현보다 먼저 가장 작은 project-aware check 경험을 contract-only/test-only로 잠갔습니다. Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)의 `godj migrations check`는 nearest exact
`godj.toml` 또는 explicit descriptor file을 선택하고, global side가 private project runner를
shell 없이 `-mod=readonly`, `GOWORK=off`, `GOTOOLCHAIN=local`로 build/run하며, linked side가
project-relative flat `*.godj.json` roots를 no-follow로 탐색해 actual `definition.Load`에 정확히 한
번 넘기는 의미를 고정합니다. Public product CLI/API는 이번 work에서 만들지 않았습니다.

MIG-065..074는 Django `migrate --check` parity가 아닌
`decision/ADR-0021/derived=false` independent reference입니다. Marker/descriptor, strict runner
protocol v1, source ordering/symlink safety, build/protocol fault, public exit `0/1/2/3/130`, process
cancel/reap와 11개 parsed/accepted input·catalog·wire/retained-output cap을 검증합니다. 결과는
11 reference set/115 unique contract/110 ordered cross-binding과 새 10 `oracle_locked`입니다. Product는 계속 10 adapter/105
contract의 `100 passing + 5 deviation`이고 새 product adapter나 actual output은 없습니다.

Implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 기존
`ubuntu-24.04` x64 full과 `macos-15` arm64 exact 2개, exact labels `ubuntu-22.04`,
`ubuntu-24.04-arm`, `macos-15-intel`, `macos-26`의 project-check 4개와 같은 좌표의 SQLite 4개,
**`2 + 4 + 4 = 10` required job executions**을 모두 통과했습니다. 두 matrix는 Go 1.26.5,
`fail-fast: false`, leg별 20분 timeout, expected GOOS/GOARCH, normal/race/CGO-disabled/vet,
no `continue-on-error`와 final clean worktree를 검증했습니다. Exact 16-file
completion-documentation commit `34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 별도 run `31322959993`의 같은 exact 10 job을
통과했습니다.

Windows는 native process/path contract 전에는 지원 runner를 만들지 않습니다. 이 historical CI 확장 시점의 actual
backend는 SQLite뿐이었으므로 PostgreSQL/MySQL service-only job을 false green으로 금지했습니다. Current GDJ-0038
PostgreSQL job도 같은 원칙에 따라
digest-pinned service image, health check, UTC timezone과 C locale 또는 명시적으로 승인된 collation,
actual query/write/transaction/schema/migration/recorder/revision-lifecycle 및 durable restart/
persistence contract를 먼저 실행해야 합니다. Expected contract 수와 executed 수가 같고 `skipped=0`,
`continue-on-error` 없음, final clean worktree도 필수이며 adjacent versions는 별도 non-required
scheduled matrix로 확장합니다.

GDJ-0021에서 “DB-free”는 GoDj-owned DB/recorder/lifecycle call 0만 뜻합니다. Linked project binary의
임의 user package `init()` side effect까지 차단한다고 주장하지 않습니다. Recursive/module/embed/
remote discovery, Windows, persistent runner cache, full CLI/library/generator handshake, direct
project command dispatcher, writer/upgrade와 DB-aware migration execution은 GDJ-0022 뒤에도
남깁니다. GDJ-0021의 Accepted/Verified 상태는 reference-only/test-only contract에 한정하며 제품
명령 구현으로 승격하지 않습니다.

Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 그 다음 product 단면을 구현했습니다.
Exact 두 global argv, public `project.Config`/`project.Run`, independent internal global/linked/protocol
kernel과 flat discovery가 MIG-065..074를 actual product adapter에서 10 `passing`으로 전환했습니다.
Test-only proof는 byte-preserved independent gate로 남고 product code가 import하지 않습니다. 현재
문단의 GDJ-0022 완료 시점 분류는 11 adapters/115 contracts=`110 passing + 5 deviation`이었습니다.
Completed GDJ-0025의 REL-001 metadata와 REL-004 required predicate actual까지 포함한 GDJ-0025 완료 시점 분류는
상단의 12/127=`112 passing + 5 deviation + 10 oracle_locked`입니다.

Hosted gate는 existing full/exact 2 + test-only proof 4 + actual SQLite 4를 보존하고 Linux/macOS
x64/arm64 actual product CLI 4와 exact Python 3.12.13/3.13.15/3.14.3/3.14.7 compatibility 4를
별도 추가한 exact 18 required executions입니다. Portable/compatibility는 uv 0.12.3, embedded profile을
재현하는 historical exact darwin oracle만 uv 0.10.12를 사용합니다. Initial fix-head run `31329294154`의
exact 18/18과 앞선 four-Python pre-test assertion failure/cancel은 EVID-028에 보존했습니다.
EVID-028/status head run `31330601427`은 16 success/2 macOS product normal failure였고, final process
synchronization head `385382efffd1872ae7fb427192bab27b95dc57e2`의 run `31332208055`는 exact 18/18
성공했습니다. Failure/fix/job/checkout 증거는 EVID-029에 기록했고, EVID-029/status commit
`1f161f311daa775e6a386ec0df568ff85d681f15`도 run `31333420261` exact 18/18을 통과해 EVID-030에
기록했습니다. GDJ-0023 implementation head `b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`도
[run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743)의 exact 22/22를
통과했습니다. Completion-documentation head `31784ae1e8261ad0698921b93803aa35e9b63f93`도 별도
[run 31339409336](https://github.com/progresshans/godj/actions/runs/31339409336)의 exact 22/22와
[EVID-20260810-033](status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)으로
검증했습니다. Final evidence/status head `50578ddc4756452b2a9a0d2afd75711a35b76d8a`도
[run 31340170361](https://github.com/progresshans/godj/actions/runs/31340170361)의 exact 22/22와 273/273
steps를 성공해 EVID-034에 기록했습니다. 그 뒤 GDJ-0024 activation docs 자체의 commit/push와 exact-head
CI는 activation run `31344980929` exact 22/22로 해소됐습니다. GDJ-0024 implementation head
`05e6e218db16e17ce13f7b504a01c603041e4a2a`도
[run 31348285559](https://github.com/progresshans/godj/actions/runs/31348285559)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-036에 기록했습니다. Completion-documentation head
`e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`도 별도
[run 31349791188](https://github.com/progresshans/godj/actions/runs/31349791188)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-037에 기록했습니다. Final evidence/status head
`5bf143575e9b703117a328c1fc5b7eb5823fbfd6`도 run `31351169780`의 exact 26/26 jobs·326/326 steps를
성공해 EVID-038에 기록했습니다. 이 clean tested head가 GDJ-0025 activation baseline이었고 activation
commit `cf8cb589...`도 run `31354040515` exact 26/26을 통과했습니다. GDJ-0025 implementation head
`98db55a30ff71a2f2f70722cb569a046208a5403`은
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-040에 기록했습니다. Completion-documentation head
`7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도 별도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-041에 기록했습니다. EVID-041/final-status head
`bffc52844de87a2791959ea1e8f99c60dd13d1aa`도 별도 run `31359958949`의 exact 26/26·326/326을
성공해 EVID-042에 기록했습니다. 이 clean tested head가 GDJ-0026 activation baseline이며 activation
documentation commit `aad4f7ff...`도 별도 run `31364944816`의 exact 26/26·326/326을 통과했습니다.
GDJ-0026 implementation head `5be46141d943800a3c621975e3e5070f6d01eaf9`은
[run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-044에 기록했습니다. Completion-documentation head
`7f92fcf036d03a5004953d9857a10291f4603efb`도 별도
[run 31372360481](https://github.com/progresshans/godj/actions/runs/31372360481)의 exact 26/26 jobs와 326/326
recorded steps를 성공해 EVID-045에 기록했습니다. 이 EVID-045를 포함한 exact 8-file final evidence/status
patch는 당시 그 뒤의 별도 diff였고, 이후 exact final-status head `9ba1d0ee...`의 run `31374150640`이
26/26·326/326을 성공해 EVID-046에서 닫았습니다. Run `31372360481`은 그 later patch의 증거로 재사용하지
않았습니다.
이는 당시 PostgreSQL/MySQL service-only job 추가가 아니었습니다. M3의 첫 PostgreSQL required job은 이후
GDJ-0038 final head `187638f9...`에서 query/write/transaction/schema/migration/recorder/revision lifecycle,
durable restart와 exact pass/no-skip inventory를 함께 갖춰 EVID-108 hosted gate를 통과했습니다. MySQL은
M9 actual adapter까지 같은 원칙을 따릅니다.

## M3 — Relations + PostgreSQL

[GDJ-0038](../work/0038-postgresql-and-minimal-web-vertical-slices.md)과 Accepted
[ADR-0037](adr/0037-postgresql-current-contract-backend.md)이 첫 actual PostgreSQL current-contract backend를
활성화했습니다. Source commit `cb90f7a...`은 query/write/Atomic뿐 아니라 explicit-schema DDL/catalog,
recorder/revision, close/reopen, process contention와 actual server restart까지 구현했습니다.
[EVID-107](status/TEST_EVIDENCE.md#evid-20260823-107--gdj-0038-postgresql-migration-and-web-integration-source-frozen-local-checkpoint)은
PostgreSQL 17.5 exact 16-field profile, required actual 12/12·skip 0, generated Article/relation, durable restart,
full/386/source-clean-copy와 audit P0..P3=0을 기록합니다. PostgreSQL REL-007/008 project-aware delete는 이 packet의
비목표입니다. Final correction head `187638f9...`는
[EVID-108](status/TEST_EVIDENCE.md#evid-20260823-108--gdj-0038-postgresql-1710-exact-head-hosted-completion) /
run `32626539049`에서 PostgreSQL 17.10과 exact 27/27 jobs·341/341 steps를 통과해 DB-PG-001..010 bounded
slice를 `Verified`했습니다. Broader/production backend support는 여전히 open입니다.

- Completed [GDJ-0023](../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md)은
  ForeignKey 외부 동작 REL-001..012와 Q-013 cross-app binding/import-cycle/shared-AST feasibility를
  contract-only/test-only로 고정했습니다. Schema IR v2나 제품 API는 변경하지 않았습니다.
- [ADR-0023](adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 symbolic/atomic binding,
  import-cycle-free project bridge, shared immutable relation AST와 explicit vNext field-union relation arm
  방향을 Accepted했습니다.
- Completed [GDJ-0024](../work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md)와
  Accepted [ADR-0024](adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)는 exact
  `RelationFormatVersion=3` ForeignKey arm/DSL, mixed v2 target/v3 source additive companion, atomic
  `orm.BindProject`와 REL-001 metadata-only product subset을 동결합니다. REL-002..012는 oracle-locked로
  유지하며 GDJ-0024 completion aggregate는 product
  `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation 1/12입니다. Existing exact 22에
  relation-product 4 legs를 더한 exact 26은 implementation run `31348285559`와 별도
  completion-documentation run `31349791188`에서 모두 통과했습니다.
  OneToOne/query/eager/write/delete/DDL/migration codec와 PostgreSQL actual backend는 뒤의
  bounded pair로 계속 분리합니다.
- Completed [GDJ-0025](../work/0025-forward-foreign-key-predicate-product-slice.md)와 Accepted
  [ADR-0025](adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)는 required AutoField-target
  `author__name`/`author__id` exact predicate를 additive query companion, project-bound shared relation path와
  SQLite reusable INNER JOIN으로 연결했습니다. REL-004만 `passing`으로 전환해 completed aggregate는
  `112 passing + 5 deviation + 10 oracle_locked`, relation REL-001/004 2/12입니다. Loader/cache, nullable
  `isnull`, reverse/eager/write/delete/DDL/migration과 PostgreSQL은 명시적 비목표입니다. Implementation run
  `31357283530`과 completion-documentation run `31358640776`은 모두 exact 26/26·326/326을 통과했습니다.
- Completed [GDJ-0026](../work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)과 Accepted
  [ADR-0026](adr/0026-forward-foreign-key-object-cache-and-nullability.md)은 REL-003 required freshly-loaded
  instance cache와 REL-006 nullable access/source-key isnull을 함께 구현한 exact bounded packet입니다.
  Additive sealed descriptor/object companion, opaque pointer wrapper, QuerySet target-PK limit-2 cache와 SQLite
  root-FK JOIN-0 trim만 소유합니다. Completed aggregate는 `114 passing + 5 deviation + 8 oracle_locked`,
  relation REL-001/003/004/006 4/12입니다. Implementation run `31370313755`과 completion-documentation run
  `31372360481`은 각각 exact 26/26·326/326을 통과했습니다. Existing project-query v1과 old relation
  product bytes, reverse/eager/write/delete/DDL/migration/PostgreSQL은 forbidden/deferred입니다.
- Completed [GDJ-0027](../work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md)과 Accepted
  [ADR-0027](adr/0027-reverse-foreign-key-accessor-and-lookup.md)은 REL-005-only reverse target predicate와
  owner related-set accessor를 bounded packet으로 구현·검증했습니다. Query-only binding과 PK-capability object
  binding, declaration-centric reverse hop, project-only split query/object generator와 SQLite direction-aware
  INNER JOIN이 exact 범위입니다. Implementation head `7db68415...`의 run `31419940399`가 exact 26/26을
  통과했고 exact 15-file completion-documentation head `7998a835...`도 별도 run `31422614250`의 exact
  26/26·326/326을 통과했습니다. Aggregate는 `115 + 5 + 7`, relation 5/12입니다. Baseline/activation/
  implementation run은 각각 later head의 증거로 재사용하지 않습니다. EVID-049 terminal status 기록은
  documentation-only이며 그 기록 자체를 증명하기 위한 재귀 evidence를 만들지 않습니다.
  REL-012 prefetch와 REL-009..011 eager, write/delete/DDL/migration/non-SQLite는 deferred입니다.
- Completed [GDJ-0028](../work/0028-reverse-foreign-key-prefetch-product-slice.md)과 Accepted
  [ADR-0028](adr/0028-reverse-foreign-key-prefetch.md)은 REL-012-only reverse batch/warm-cache slice입니다.
  Existing owner QuerySet의 primary query와 new `ReversePrefetch.Load` batch query를 분리하고, immutable
  `LookupIn`, distinct key ordering/cap, sealed source-FK grouping과 all-success-only ready `RelatedSet`
  publication을 project-only generated companion으로 연결합니다. Implementation head `4858ab88...`의 run
  `31432551159`가 exact 26/26·326/326을 통과했고 exact 15-file completion-documentation head
  `9dc4eb13...`도 별도 run `31435136950`의 exact 26/26·326/326을 통과했습니다. 그 completion head의 product는
  `116 + 5 + 6`, relation 6/12였습니다. Baseline/activation/implementation run은 각각 later head의 증거로
  재사용하지 않았고 EVID-053 terminal status 기록은 documentation-only이며 자기 자신을 재귀 증명하지 않습니다.
  Existing reverse generator/nine-file output은 byte-locked하며 custom Prefetch/filter/order, REL-009..011 eager
  projection, write/delete/DDL/migration과 non-SQLite는 별도 bounded work로 남깁니다. Baseline/activation run은
  이 implementation proof로 재사용하지 않았습니다.
- Completed [GDJ-0029](../work/0029-one-hop-forward-select-related-product-slice.md)과 Accepted
  [ADR-0029](adr/0029-one-hop-forward-select-related.md)는 REL-009/010/011 indivisible one-hop forward eager
  milestone을 구현·검증했습니다. Existing descriptor/QuerySet bytes를 바꾸지 않는 app projection companions,
  singular `RelationProjection`, All-only eager runtime, required INNER/nullable LEFT OUTER SQLite compiler와
  same-resolver reverse rejection이 exact 경계입니다. Implementation head `c02aab67...`의
  EVID-056/run `31470292759`와 exact 15-file completion-documentation head `fb9985e2...`의
  EVID-057/run `31482242288`이 각각 별도 exact 26/26·326/326과 four-coordinate 630/630/0 inventory를
  통과했습니다. 그 completion head 당시 aggregate는 `119 + 5 + 3`, relation 9/12였습니다. EVID-057을 포함한 exact seven-file
  terminal 기록은 later documentation-only patch이며 completion run을 그 exact-head proof로 재사용하지
  않습니다. 그 terminal head `d0396c76...`은 별도 EVID-058/run `31484369693`의 exact
  26/26·326/326과 source diff 0을 통과해 GDJ-0030 clean baseline이 됐습니다.
  Multiple/nested/reverse eager, canonical facade, write/delete/DDL/migration/non-SQLite는 deferred입니다.
- Completed [GDJ-0030](../work/0030-project-bound-protect-and-set-null-delete.md)과 Accepted
  [ADR-0030](adr/0030-project-bound-protect-and-set-null-delete.md)은 REL-007/008 indivisible SQLite low-level
  delete packet을 구현·검증했습니다. Declared-universe incoming-policy fingerprint, constructible typed protected error,
  all-PROTECT global distinct pre-scan, canonical SET_NULL→target exact-one DELETE와 per-owned-connection FK-on + pinned
  `AtomicRelation`/no-retry transaction을 검증합니다. Exact generated surface는 `RelationDeleters` aggregate 하나이고
  omitted undeclared app 완전성, missing/mismatched physical FK repair 또는 FK-off writer 차단은 주장하지 않습니다.
  Existing one-row Delete/old interfaces와
  select-related twelve-file product는 frozen입니다. Implementation head `c3803acb...`의 EVID-061/run
  `31510689383`이 exact 26/26·326/326과 four-coordinate 687/687/0 inventory를 통과해 current는
  `121 + 5 + 1`, relation 11/12입니다. REL-002,
  canonical facade/cache invalidation, recursive/CASCADE delete, DDL/migration과 non-SQLite는 deferred입니다.
  Exact 15-file completion-documentation head `635e9c38...`도 별도 EVID-062/run `31514159835`의 exact
  26/26·326/326과 unchanged four-coordinate 687/687/0 inventory를 통과했습니다. 그다음 exact seven-file terminal
  head `ceff9e5...`는 EVID-063/run `31516174741`에서 별도 exact 26/26·326/326을 통과해 GDJ-0031의
  clean baseline이 됐습니다. EVID-063은 later activation documentation/implementation proof로 재사용하지
  않습니다.
- Canonical relation API freeze 전 GDJ-0031/Q-017 compile-usability gate를 별도로 통과했습니다. Physical exact
  16과 generated exact 13을 보존한 internal compile overlay의 logical exact 17 view에서 one-time query binding,
  exact `OrderBy(...).First(ctx)`, private wrapper의 `Model()` unwrap와 lazy/eager 동일 source pointer/accessor를
  검증했습니다. 이 test-only feasibility는 Accepted지만 `project.Using`을 포함한 모든 이름은 noncanonical이고
  reverse/REL-002/write/cache/session lifetime과 production generated upgrade는 별도 후속입니다. GDJ-0029의
  projection/runtime/compiler와 generated object-factory bridge는 low-level 기반이며 그 자체가 최종 application
  UX는 아닙니다. Exact 11-path completion-documentation head `e9b2c0e...`도 EVID-066/run `31531470440`의
  별도 exact 26/26·326/326을 통과했습니다. EVID-066을 추가한 exact seven-file terminal head `3d661251...`도
  EVID-067/run `31533890720`의 별도 exact 26/26·326/326을 통과했고 completion run을 그 proof로 재사용하지
  않았습니다.
- Completed GDJ-0032는 terminal head `3d661251...`의 EVID-067/run `31533890720`을 clean baseline으로 production
  forward facade를 처음 게시했습니다. Activation EVID-068/run `31537726792`와 implementation EVID-069를 분리했고,
  existing generated exact 13은 byte-identical subset으로 유지하면서 새 project companion 한 파일만 additive
  publish했습니다. Project-local capability는 Queryer+Mutator이고 RelationAtomic은 제외하며, 정적 Queryer-only
  입력은 거부합니다. 모든 declared model의 query root와 project-owned pointer wrapper, required Author/nullable
  Reviewer가 같은 target wrapper type을 쓰는 경계, common selector와 stored eager state를 검증했습니다. Reverse
  manager, stable target pointer identity, downstream target cache, REL-002, general generated upgrade/CLI는 deferred입니다.
  ADR-0032는 이 bounded Gate 0에 한해 Accepted입니다. Exact eleven-file completion-documentation head
  `6089e214...`도 EVID-070/run `31544273477`의 별도 exact 26/26 jobs·326/326 recorded steps와 unchanged
  product gates를 통과했습니다. EVID-070을 추가한 exact seven-file terminal head `8748bb49...`은 다시
  EVID-071/run `31563615648`의 별도 exact 26/26·326/326을 통과해 GDJ-0033 baseline이 됐습니다.
- Completed GDJ-0033은 REL-002만 열었습니다. Activation `a4a627a...`/EVID-072와 decision head
  `9d728610...`/EVID-074는 각각 별도 exact 26/26·326/326을 통과했고 Phase A/B/C는
  Django observable, exact public `New`/`Save`/`With*`/clear API, project-private write descriptor, pending-only
  reconciliation, corrected canonical three-phase preflight와 per-edge COW cache를 Accepted했습니다. Exact 23-path
  bounded implementation은 local normal/race/CGO0/vet/Linux386, full `./...`, measured inventory와 independent
  P0..P3=0을 통과했습니다. Exact head `be6f3d4e...`의 EVID-076/run `31586910749`도 unique exact
  26/26 jobs·326/326 steps와 four-coordinate 715/715/0 inventory를 통과해 `122 + 5 + 0`, relation 12/12를
  hosted-verified했습니다. EVID-076을 포함하는 exact 15-document completion head `81f4aacb...`도 EVID-077/run
  `31590911735`의 별도 exact 26/26·326/326과 audit P0..P3=0을 통과했습니다. Global identity와
  cross-materialization pointer identity는 비목표입니다. EVID-077을 포함하는 exact seven-document terminal head
  `db5c11f6...`도 EVID-078/run `31593500615`의 고유 exact 26/26·326/326과 audit P0..P3=0을 통과했습니다.
- Completed GDJ-0034는 typed generated `select_related` builder가 resolver/binder cause를 zero query 뒤 generic
  invalid-plan으로 축약하던 gap만 고쳤습니다. Private stored configuration error, 기존 context precedence 뒤
  terminal pre-I/O exact-cause 반환, generator v2와 두 checked-in companion의 deterministic regeneration을 exact
  12-path source boundary에 구현했습니다. EVID-080의 local normal/race/CGO0/vet/Linux386와 exact implementation head
  `3099bd62...`의 EVID-081/run `31605477297` 26/26 jobs·326/326 steps 및 independent P0..P3=0을 통과했습니다.
  새 Q/ADR 또는 public API를 추가하지 않았고 dynamic path, 정상 query/result/cache 의미와 REL-009/010/011 product
  status는 그대로입니다. EVID-081을 포함하는 exact 13-document completion head `45cfccd...`도 EVID-082/run
  `31609500811`의 별도 exact 26/26·326/326과 audit P0..P3=0을 통과했습니다. EVID-082를 포함한 exact
  six-document terminal head `0bb8c969...`는 EVID-083/run `31613170021`의 고유 exact 26/26·326/326과
  audit P0..P3=0을 통과해 clean GDJ-0035 baseline이 됐습니다. Completion run을 terminal proof로
  재사용하지 않았습니다.
- Current PROTECT는 exact protected identities와 count payload를 위해 모든 protected row를 materialize합니다.
  Production-scale 전 bounded-memory/stream/cap stress gate가 필요합니다. Public `ProtectedError` payload meaning을
  바꾸지 않는 최적화는 별도 work로 다루고, payload가 바뀔 때만 새 ADR을 요구합니다.
- Q-017의 facade input provenance는 coordinated multi-file upgrade/general `--check` 전에 prerequisite generator
  versions와 output digests까지 project snapshot으로 잠가야 합니다. REL-002 single-companion replacement의 필수
  선행 조건으로 과장하지 않습니다.
- Q-019는 GoDj SQLite unknown-outcome connection이 `Backend.Close`까지 retained set에 누적될 수 있는 resource
  policy를 별도 P1 work로 추적합니다. Django 의미가 아니라 `database/sql`/GoDj lifecycle 결정이며 behavior가
  바뀌면 새 ADR이 ADR-0030을 명시적으로 amend/supersede해야 합니다.
- GDJ-0034 terminal 뒤 relation-capable migration은 GDJ-0035 별도 contract-first vertical packet으로만 엽니다.
  Existing scalar migration
  tuple `(1,1,1,2)`의 의미를 byte/semantic preserve하고 relation 의미로 silent reinterpretation하지 않으며, vNext
  `ProjectState`, operation codec, historical reconstructor, SQLite FK DDL, app dependency, apply/unapply와 restart를 한
  slice에서 함께 증명합니다. 이는 REL-002의 선행 의존이 아니며 별도 activation/ADR 없이 현재 migration format을
  넓히지 않습니다.
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

Q-007의 result cache/terminal semantics는 이후 projection/aggregate/relation loader가
같은 평가 상태를 재사용하기 전에 GDJ-0007/0008의 선행 단면으로 완료했습니다. 이는
M4 전체 breadth가 구현됐다는 뜻이 아닙니다.

[GDJ-0039](../work/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)과 Accepted
[ADR-0039](adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)은 M4의 첫 broad read slice를
구현합니다. Article filter → distinct/stable order → offset/limit → typed DTO projection → Count/Max report를
SQLite/PostgreSQL에서 같은 AST로 실행하고 QRY-022..033 실제 SQLite adapter는 12/12 zero-diff입니다. Final
source `695916c8...`과 submitted head `253455d...`는
[EVID-109](status/TEST_EVIDENCE.md#evid-20260823-109--gdj-0039-typed-query-breadth-source-frozen-local-checkpoint) /
[EVID-110](status/TEST_EVIDENCE.md#evid-20260823-110--gdj-0039-exact-head-hosted-completion)의 local·hosted gate를
통과해 bounded `Verified`/completed입니다.

[GDJ-0040](../work/0040-composable-typed-boolean-predicates-and-article-search.md)과 Accepted
[ADR-0040](adr/0040-composable-typed-boolean-predicates-and-article-search.md)은 다음 read slice로 scalar typed
`And`/`Or`/`Not`, nullable NOT truth table, predicate reuse, projection/aggregate composition과 Article bounded search를
QRY-034..043으로 엽니다. Contract-first Phase A는 `fe4996f...`/EVID-111에서 exact 10 reference를 동결했고,
Phase B/C `86d6b169...`/`0ec6f385...`는 product/actual을 구현해 10/10 `passing`과 zero-diff로 전환했습니다.
[EVID-112](status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)의
affected/local PostgreSQL/audit와
[EVID-113](status/TEST_EVIDENCE.md#evid-20260823-113--gdj-0040-frozen-source-final-local-gates)의 initial final local 뒤
first hosted run은 stale inventory lock에서만 실패했습니다.
[EVID-114](status/TEST_EVIDENCE.md#evid-20260823-114--gdj-0040-first-hosted-inventory-lock-failure-and-corrected-local-refreeze)의
correction `73b912d...`는 950/950/0과 full/386/source-clean-copy를 다시 통과했습니다. Corrected submitted
`136e825...`의 [EVID-115](status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion) /
run `32642341459`은 exact 27/27 jobs·341/341 steps로 이 bounded slice를 completed/hosted-verified로 닫았습니다.
Relation leaf under OR/NOT, F arithmetic/relation F, bulk, locking, annotation/subquery/window와 related projection은 제외하므로
M4 전체는 계속 완료되지 않았습니다.

[GDJ-0041](../work/0041-typed-scalar-comparisons-field-references-and-article-filtering.md)과 Accepted
[ADR-0041](adr/0041-typed-scalar-comparisons-and-field-references.md)은 그 다음 넓은 read slice로 Integer/String range,
sealed same-model/same-kind `orm.F`, private RHS union/source validation과 nullable RHS odd-`NOT`을 기존 Boolean tree,
projection/aggregate, SQLite/PostgreSQL identifier compiler와 Article exactly-two-query 흐름에 합류시켰습니다.
Frozen source `7f2bb223...`의 local-final과 QRY-034..053 20/20·신규 QRY-044..053 10/10 zero-diff는
통과했습니다. Submitted head `e97a4e3...`의
[EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) / run
`32647746430`도 exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual을 통과해
이 bounded slice를 completed/hosted-verified로 닫았습니다. Arithmetic/function/annotation, relation/cross-model F,
bulk/locking/subquery/window는 계속 후속입니다.

## M5 — Web Core

[GDJ-0038](../work/0038-postgresql-and-minimal-web-vertical-slices.md)과 Accepted
[ADR-0038](adr/0038-minimal-web-core-request-lifetime-and-representation.md)이 최소 Web/Article HTTP 수직
단면을 PostgreSQL lane과 병렬 활성화했습니다. Declaration runner는 보존하고 별도 Article site binary, immutable
startup state, static router, request-local generated facade와 explicit DTO/template만 구현합니다. Global
`godj runserver`, DTL/Form/Auth/Admin/API는 이 packet의 비목표입니다. 이 bounded Web slice는
[EVID-106](status/TEST_EVIDENCE.md#evid-20260821-106--gdj-0038-phase-a-b-and-c-local-checkpoint)의 SQLite loopback과
[EVID-107](status/TEST_EVIDENCE.md#evid-20260823-107--gdj-0038-postgresql-migration-and-web-integration-source-frozen-local-checkpoint)의
Article PostgreSQL migration/generated CRUD/HTTP actual을 통과했고 final
[EVID-108](status/TEST_EVIDENCE.md#evid-20260823-108--gdj-0038-postgresql-1710-exact-head-hosted-completion) /
run `32626539049`에서 bounded `Verified`/completed로 전환됐습니다. GDJ-0039은 이 request-local DTO 경계를
유지한 채 검색/리포트 query breadth를 넓혀 EVID-110으로 completed됐습니다. GDJ-0040은 public Web
Core를 넓히지 않고 기존 Article handler의 bounded q/published/exclude parsing과 request-local 두 DB query 계약에
scalar Boolean 검색을 연결했고 EVID-112에서 SQLite/PostgreSQL local E2E를 통과했습니다.
GDJ-0041은 같은 handler에 `min_id`/`max_id`/`title_matches_summary`를 추가해 invalid request DB I/O 0과 성공
projection+aggregate 두 query를 유지했고 EVID-118/run `32647746430`의 exact-head PostgreSQL 17.10 actual까지
통과해 completed/hosted-verified됐습니다.

[GDJ-0042](../work/0042-project-linked-runserver-and-article-development-loop.md)는 M5의 completed 수직 단면입니다.
Accepted [ADR-0042](adr/0042-project-linked-runserver-and-article-development-loop.md)에 따라 declaration runner와
generated-aware runtime package를 분리한 채 current bundle을 read-only 확인하고 global `godj runserver`가
loopback Article server를 build/start/drain/reap하도록 WEB-011..020을 구현했습니다. SQLite/PostgreSQL Article actual과
portable/required pass-no-skip lock은 EVID-122/run `32659704239`에서 exact hosted-verified됐습니다. Auto-generate/migrate/reload,
public DB settings와 production server는 계속 제외합니다.

[GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md)은 M5 template 요청과 M6의 첫
Form/Auth/Admin을 하나의 completed wide vertical batch로 연결합니다. Accepted
[ADR-0043](adr/0043-safe-template-and-model-form-validation.md)과
[ADR-0044](adr/0044-session-auth-csrf-and-bounded-article-admin.md)는 closed-value DTL, IR-derived form,
process-lifetime session/auth/CSRF와 Article list/search/add/change/delete/history/publish 흐름을 고정합니다. Exact
30 contracts는 `25 passing + 5 Verified deviations`이고 EVID-124/CI #134에서 hosted-verified됐습니다. M5/M6
completion, durable system state, production deployment와 broader Form/Admin breadth는 계속 주장하지 않습니다.

[GDJ-0044](../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md)는 M7의 첫 completed wide
vertical batch입니다. Accepted [ADR-0045](adr/0045-closed-parameterized-routing-and-reverse.md)와
[ADR-0046](adr/0046-json-serializer-and-session-authenticated-article-api.md)은 existing static Web/Admin을 보존하면서
closed integer parameter route/reverse, reflection-free JSON serializer와 session-authenticated Article
list/create/detail/PUT/PATCH/delete를 연결합니다. Reference는 별도 DRF 3.18.0 + Django 6.1 + CPython 3.14.3
profile/lock을 사용해 기존 Django-only oracle bytes를 보존합니다. WEB-028..035/API-001..010은 exact
`13 passing + 5 deviation`으로 EVID-126/CI #142에서 hosted-verified됐습니다. OpenAPI, browsable API, token auth,
Channels와 M7/M8 completion을 주장하지 않습니다.

[GDJ-0045](../work/0045-durable-single-runtime-system-state-and-article-restart.md)는 M6의 completed wide batch입니다.
Accepted [ADR-0047](adr/0047-explicit-single-runtime-system-state.md)의 current Auto/Char/Boolean
`godj_system` credential/session/audit migration, durable Store/bootstrap, Article-audit same-transaction과
single-runtime clean restart 구현은 hosted-verified됐습니다. SYS-001..012는 global product adapter에서
11 `passing` + SYS-009 Verified DEV-0008 `deviation`으로 비교되고 SQLite distinct-process actual과 EVID-129의
PostgreSQL required 16/16·skip 0 및 exact 27/27 matrix가 통과했습니다. Multi-process/distributed topology,
DB unique IR, persistent/shared CSRF key와 production readiness는 계속 포함하지 않습니다.

[GDJ-0046](../work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md)은 이 one-runtime
경계를 대체하지 않고 확장한 completed M6 hardening batch입니다. Accepted
[ADR-0048](adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)은 SQLite `BEGIN IMMEDIATE`,
PostgreSQL transaction advisory lock과 injected active/validation CSRF key ring으로 cooperative two-Runtime/two-process를
검증합니다. SYS-013..020은 모두 product `passing`으로 전환됐고 GDJ-0046 terminal reference는
21/239/420=`211+16+12 locked`, product는 20/227=`211+16`입니다. Same-process HTTP handoff, distinct-process
SQLite/PostgreSQL barrier/restart와 source-bound live attestation은 EVID-133/134의 local/hosted gates를 통과했습니다.

[GDJ-0047](../work/0047-api-authentication-profiles-and-bearer-article-api.md)은 M7의 completed Bearer
resource-server batch입니다. Accepted [ADR-0049](adr/0049-first-party-bff-and-bearer-api-authentication.md)의 common
`api.Authentication`, Session/Bearer profile isolation, opaque redacted token/injected verifier와 profile-neutral Article handlers를
구현했습니다. Exact AUT-009..016/API-011..012 actual 10/10과 SQLite E2E는 local 통과했고, 일곱 passing과
AUT-012/013/015 Verified DEV-0009 세 deviation으로 게시됐습니다. Corrected current source
`14e47c9ba18a698cae52f7167c53148cd552f175` / tree `1b2c9c742bf66cc65e105e961a3dcfc02fa2c404`의
digest-pinned PostgreSQL 17.10 Article Bearer E2E normal/race/CGO0과 two-process source-bound attestation도
통과했습니다. Initial exact submitted run `33044776835`는 26 jobs success와 macOS Intel product job 한 건의 30분
timeout/cancellation으로 끝났습니다. EVID-137에서 해당 좌표만 45분으로 교정하고 current attestation을 recapture한 뒤
final full `make ci`, Linux/386와 1,077-file external archive를 다시 통과했습니다. Corrected head `5f97fa8...` /
tree `2b53c031...`의 EVID-138/CI #155 run `33049861740`은 exact 27/27 jobs·360/360 steps success로
ADR/work/deviation을 terminal Accepted/completed/Verified로 닫았습니다.

- settings, app registry, system check
- routing/reverse, middleware, request/response, error handling
- view와 template 한 요청 수직 단면
- development server와 management command

## M6 — Forms, Auth, Admin

- common validation core와 Form/Serializer 경계
- Form, ModelForm, CSRF, session, auth, permission
- 한 모델의 Admin list/search/edit/history/action 수직 단면
- static/messages와 접근성·보안 gate
- explicit single-runtime durable credential/session/audit와 restart-preserving Article flow — GDJ-0045 completed/hosted-verified
- cooperative multi-runtime system-state coordination과 shared CSRF key ring — GDJ-0046 completed/hosted-verified

## M7 — API

- API reference profile 확정 — GDJ-0044에서 DRF 3.18.0 exact isolated profile을 Accepted/Verified
- serializer, JSON parser/renderer, session authentication/permission — GDJ-0044 bounded slice completed
- bounded Article list/create/detail/PUT/PATCH/delete와 parameter Router, pagination/filter/order — GDJ-0044 completed
- first-party session/BFF와 strict injected Bearer resource-server profile — GDJ-0047 SQLite/PostgreSQL required와
  corrected final local full/386/archive, EVID-138 exact hosted 통과; ADR-0049 Accepted,
  JWT/opaque issuance·refresh·OAuth/OIDC·production BFF 제외
- OpenAPI와 browsable API — 별도 후속 결정, 현재 제외

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
- 매 변경은 affected compile/test, phase checkpoint는 관련 normal/race/CGO0, full local/386/hosted matrix는 final
  frozen milestone에서 한 번 실행합니다. 문서-only evidence append 때문에 product matrix를 재귀 반복하지 않습니다.

## Retired GDJ-0035 sequence

아래 순서는 GDJ-0036 activation 전의 exact-head/evidence 계획을 보존합니다. D4g observer와 Phase E
publication은 실행하지 않으며 current 구현 순서는
[GDJ-0036](../work/0036-pre-release-compatibility-reset.md)의 A~D checkpoint를 따릅니다.

GDJ-0034 terminal head `0bb8c969...`는 EVID-083/run `31613170021`의 exact 26/26 jobs·326/326 steps와
audit P0..P3=0을 통과했습니다. 이 clean baseline에서
[GDJ-0035](../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)를 유일한 active
packet으로 활성화했고 ready는 0입니다. EVID-083은 activation proof가 아니며 exact 16-document
activation head `52f9bcb7...`는
[EVID-084](status/TEST_EVIDENCE.md#evid-20260812-084--gdj-0035-activation-documentation-head-exact-26-job-ci) /
[run 31618469072](https://github.com/progresshans/godj/actions/runs/31618469072)의 고유 exact
26/26 jobs·326/326 steps와 audit P0..P3=0을 통과했습니다. 이 증거는 activation만 검증합니다.
[EVID-085](status/TEST_EVIDENCE.md#evid-20260813-085--gdj-0035-phase-a-reference-only-artifacts-and-local-validation)는
별도 dirty working tree에서 Phase A artifact/local gates를 고정했습니다. Exact committed head `84e16bf...`는 별도
[EVID-086](status/TEST_EVIDENCE.md#evid-20260813-086--gdj-0035-phase-a-github-hosted-reference-only-exact-head-ci) /
[run 31625898551](https://github.com/progresshans/godj/actions/runs/31625898551)의 고유 attempt-1
26/26 jobs·326/326 steps와 audit P0..P3=0을 통과했습니다.

1. Phase A (**hosted-verified**): MIG-075..086 exact 12 independent reference/proposal artifacts와 provenance,
   exact 13/139/156 aggregate를 고정했고 unique exact-head CI를 통과했습니다.
2. Phase B (**completed and hosted-verified**): exact 14개 `_test.go` 안에서만 tuple/mixed digest/state/
   preflight/SQLite remake/fault feasibility를 검증했습니다. Exact implementation head `c2ecb292...`, tree
   `c114812f...`는 [EVID-088](status/TEST_EVIDENCE.md#evid-20260813-088--gdj-0035-phase-b-github-hosted-no-product-feasibility-exact-head-ci) /
   [run 31653237691](https://github.com/progresshans/godj/actions/runs/31653237691)의 고유 attempt-1 exact
   26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다. Four SQLite coordinates는 각각
   75/75/0, 9,736 bytes, SHA-256 `48e7beb1994c099a0f550da54d0abdcd5bc08157b74a9db22ae3dd42d42592ec`를 재현했습니다.
3. Phase C decision (**Accepted; acceptance head hosted-verified by EVID-092**): exact 8개 test-only file에서
   numeric version tuple/state behavior, one-loader/per-document dispatch/one Planner, digest v2, whole-step state transition,
   wire `target_field` 제거, three-stage preflight, candidate existing-fence behavior/four capabilities와 SQLite order를
   동결했습니다. Accepted decision은 additive public constant/port/type names를 선택하며 test-only proof는
   product package에서 그 names를 export하지 않았습니다. Exact head `7d36502...`, tree `d9e8a6b7...`는
   [EVID-090](status/TEST_EVIDENCE.md#evid-20260819-090--gdj-0035-phase-c-test-only-decision-proof-exact-head-hosted-ci) /
   [run 32174259324](https://github.com/progresshans/godj/actions/runs/32174259324)의 unique exact
   26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다. Proposed docs-freeze head `5bdf013...`도
   EVID-091/run `32183309328`의 고유 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했고, 이 별도 head에서
   ADR-0034를 Accepted로 전환했습니다. Acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`의 고유
   26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다.
4. Phase D1/D2/D3a (**bounded slices implemented and hosted-verified**): D1 `42aa9a9...`/`f22a498...`,
   D2 `ec8877e...`/`80776b5...`, D3a `2eafde1...`/`ce58c5e...`는 각 EVID-093 hosted run의
   exact 26/26 jobs·342/342 steps·audit P0..P3=0을 통과했습니다. D3a는 direct relation-bearing
   Create/Delete만 지원하고 Add/Remove/remake caps는 false입니다.
5. Phase D3b (**implemented and hosted-verified**): product `74c2b72...`, correction `167ef03...`가 static
   request/resource/carrier/profile/digest/graph/chronology/readiness → exact-one fenced history → actual
   Planner → whole-plan dry validation → actual-plan relation capability를 core `Executor.Migrate`에 새 public
   API 없이 연결했습니다. Scalar/no-op relation call 0과 unsupported mixed plan의 scalar partial commit 0,
   normal loaded SQLite Create/Delete apply/unapply/reapply를 EVID-094/run `32231149900`에서 검증했습니다.
6. Phase D4a (**bounded restart scenario verified**): exact one-test-file head `424ec4d...`가 disposable
   file-backed SQLite를 process-scope별로 close/reopen하고 fresh loaded mixed set/Backend로 latest no-op,
   target child-first unapply와 second-restart reapply를 재구성했습니다. EVID-095/run `32248885053`은 이
   captured schema/rows/history/token/FK snapshot scenario만 검증하며 product source/API/workflow는 불변입니다.
7. Phase D4b (**hosted-verified**): exact 18-document bounded-restart completion-documentation head `84588f9...`는
   EVID-096/run `32252834752`의 unique exact-head hosted CI와 independent audit를 통과했습니다.
8. Phase D4c (**test-only taxonomy hosted-verified**): exact one-test-file head `e4fbc7b...`는 real
   `definition.Load`→`Set.Migrate`→SQLite 경로에서 Begin/PRAGMA-set/catalog/claim-busy failure는
   `NoOperation`, final-FK failure는 operation 1 `AddField`, recorder failure는 `NoOperation`임을
   EVID-096/run `32256113658`에서 검증했습니다. Product/API/workflow/capability/status/inventory는 불변입니다.
9. Phase D4d (**implemented and hosted-verified**): EVID-096 docs head `62df9b2...`/run `32260744096`을
   별도로 닫은 뒤 product `3950d98...`, inventory lock `28b141e...`, deterministic scan fix `dd83362...`로
   sealed/resolvable same-target loaded universe의 `AddNullableForeignKey`를 구현했습니다. Public intent는 changed
   target 하나만 유지하고 private expansion, native inline ALTER, mixed canonical SQL, populated rows/sequence,
   reopen, fault rollback/no-retry와 resource bounds를 검증했습니다. First run `32267789056`의 P1은
   wall-clock assertion을 deterministic visit counts로 바꿔 제거했고 distinct run `32271361724`는 exact
   26/26·342/342와 audit P0..P3=0을 통과했습니다. Capability는 `{true,true,false,false}`입니다.
10. Phase D4e (**implemented and hosted-verified; capability 2**): EVID-097 documentation head `c59669c...`를
    run `32278555810`에서 별도로 닫은 뒤 product `7c07805...`와 inventory lock `1d86f6e...`로 exact
    no-default/non-PK/required `PROTECT` ForeignKey를 empty source에 native NOT NULL ALTER로 추가했습니다.
    Existing source emptiness는 pinned `BEGIN IMMEDIATE` 뒤 claim 전에 확인하고 same-intent created source는
    statically empty로 처리합니다. Run `32282269755`는 exact 26/26·342/342와 audit P0..P3=0을 통과했습니다.
    Capability는 `{true,true,true,false}`입니다.
11. Phase D4f (**implemented and hosted-verified; capability 3**): EVID-098 documentation head `85f9270...`를
    CI #94/run `32288383027`에서 별도로 닫은 뒤 product `4982e27...`와 inventory lock `9d5b894...`로
    exact appended nullable `PROTECT` 또는 `SET_NULL`, required `PROTECT` ForeignKey의 backward/unapply를 bounded
    table remake로 구현했습니다. Frozen direct E2E fixture는 nullable `PROTECT`와 required `PROTECT`만
    검증했으며 dedicated nullable `SET_NULL` D4f E2E proof는 주장하지 않습니다. Same-target relation-free
    AutoField authority, max-one relation mutation/source/step, pre-claim
    relevant-shape rejection, deterministic temp, retained-column PK-order copy, row/sequence preservation,
    error ownership/rollback/no-retry와 reopen/reapply를 검증했습니다. CI #95/run `32294983953`은 exact
    26/26·342/342와 audit P0..P3=0을 통과했습니다. Capability는 `{true,true,true,true}`입니다.
12. Phase D4g (**retired before publication**): 당시 첫 작업 후보는 expected fixture/oracle을 보지 않는 observer-only
    characterization으로 actual GoDj observation을 수집하고 MIG-075..086 12개를 모두 `oracle_locked`로
    유지하는 것입니다. 현재 allowed paths에 없는 `conformance/cmd/godjcheck/main.go` 추가와 DEV/deviation
    경로 필요 여부는 별도 explicit scope/decision gate에서 먼저 결정하며 deviation을 묵시적으로 승인하지 않습니다.
13. Phase E (**retired**): D4g characterization 뒤의 completion/terminal publication 계획이었으나
    GDJ-0036 current-only reset으로 대체됐습니다.

당시 candidate relation tuple은 `(1,2,2,3)`, locked IDs는 MIG-075..086뿐이었습니다. Final Phase-A artifact는
manifest/oracle/NI/checksum 7,792/125,248/1,846/1,245 bytes로 측정했습니다. Reference는 exact
13/139/156=`122+5+12 locked`이며 product는 계속 exact 12/127=
`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12입니다. Writer/autodetector/CLI,
self/cyclic/inbound/general schema remake, non-AutoField/non-SQLite, Q-017/Q-019는 이 sequence 밖입니다. Phase B/C
proof는 later D1/D2/D3a/D3b를 증명하지 않고, candidate-local restart도 D4 product-path proof가 아닙니다.
EVID-093의 D1/D2/D3a는 D3b core support를, EVID-094의 D3b는 D4 restart를, EVID-095의 bounded D4 scenario는
Add/Remove/remake, raw database-file equality, `sqlite_sequence`, general restart나 actual MIG adapter를
증명하지 않습니다. EVID-099은 bounded D4f product만 증명하며 actual adapter/status/deviation 결정을
증명하지 않습니다. 당시 relation support는 normal
`definition.Load`/`Set.Migrate`/`Executor.Migrate` 경로만 소유했으며 direct legacy execution은
relation-bearing input을 capability error로 거부했습니다. Current entry는 이 문서 상단의
`definition.Load`→opaque `LoadedDefinitionSet`→`Executor.Migrate` 경계입니다.
