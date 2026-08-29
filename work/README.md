# Work items

이 디렉터리는 여러 턴과 여러 사람이 재개할 수 있는 실행 단위의 정본입니다. 설계 문서를 복사하지 않고 관련 문서와 ADR을 링크합니다.

## 상태

```text
proposed → ready → active → completed
                    └→ blocked
                    └→ superseded
```

- `proposed`: 순서·범위 검토 전
- `ready`: 선행 조건과 완료 기준이 정해짐
- `active`: 현재 checkout에서 진행 중
- `blocked`: blocker와 해제 조건이 명시됨
- `completed`: 실제 결과와 검증 증거까지 기록됨
- `superseded`: 다른 work item으로 대체됨

한 시점에 통합 작업은 하나만 `active`로 둡니다. 독립 subtask를 병렬 실행할 때는 수정 예정 경로와 통합 소유자를 분명히 기록합니다.

## 목록

| ID | 상태 | 작업 |
|---|---|---|
| [GDJ-0000](0000-documentation-foundation.md) | completed | 문서·결정·인수인계 기반 수립 |
| [GDJ-0001](0001-compatibility-lab.md) | completed | Django 6.1 Compatibility Lab |
| [GDJ-0002](0002-model-to-query-walking-skeleton.md) | completed | 첫 Model-to-Query 수직 단면 |
| [GDJ-0003](0003-write-migration-compatibility-contracts.md) | completed | Write/Migration 호환 계약 확장 |
| [GDJ-0004](0004-write-migration-walking-skeleton.md) | completed | Write/Migration 첫 제품 수직 단면 |
| [GDJ-0005](0005-save-lifecycle-compatibility-contracts.md) | completed | Save lifecycle 호환 계약 확장 |
| [GDJ-0006](0006-save-lifecycle-product-slice.md) | completed | Save lifecycle 제품 수직 단면 |
| [GDJ-0007](0007-queryset-evaluation-cache-compatibility-contracts.md) | completed | QuerySet evaluation/cache 호환 계약 |
| [GDJ-0008](0008-queryset-evaluation-cache-product-slice.md) | completed | QuerySet evaluation/cache 제품 수직 단면 |
| [GDJ-0009](0009-migration-planning-compatibility-contracts.md) | completed | Migration planning 호환 계약 확장 |
| [GDJ-0010](0010-immutable-migration-planner-product-slice.md) | completed | Immutable migration graph/applied-state planner 제품 단면 |
| [GDJ-0011](0011-migration-plan-execution-compatibility-contracts.md) | completed | Multi-migration plan execution 호환 계약 |
| [GDJ-0012](0012-migration-plan-execution-orchestrator.md) | completed | Migration plan 실행 orchestrator와 atomic-reverse 결정 |
| [GDJ-0013](0013-recorder-backed-restart-planning-compatibility-contracts.md) | completed | Recorder-backed restart planning 호환 계약 |
| [GDJ-0014](0014-recorder-backed-restart-planning-product-slice.md) | completed | Recorder-backed restart planning 제품 단면 |
| [GDJ-0015](0015-historical-project-state-reconstruction-compatibility-contracts.md) | completed | Historical ProjectState reconstruction 호환 계약 |
| [GDJ-0016](0016-historical-project-state-reconstruction-product-slice.md) | completed | Historical ProjectState reconstruction 제품 단면 |
| [GDJ-0017](0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md) | completed | Migration lifecycle 호환 계약과 revision-fence spike |
| [GDJ-0018](0018-revision-fenced-migration-lifecycle-product-slice.md) | completed | Revision-fenced migration lifecycle 제품 단면 |
| [GDJ-0019](0019-migration-definition-source-compatibility-contracts.md) | completed | Migration definition source/versioned-loader 호환 계약 |
| [GDJ-0020](0020-migration-definition-loader-product-slice.md) | completed | Migration definition loader 제품 단면 |
| [GDJ-0021](0021-migration-project-check-compatibility-contracts.md) | completed | Project-linked migration check 호환/결정 계약 |
| [GDJ-0022](0022-migration-project-check-product-slice.md) | completed | Project-linked migration check 제품 단면 |
| [GDJ-0023](0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md) | completed | ForeignKey 관계 호환 계약과 cross-app binding feasibility |
| [GDJ-0024](0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md) | completed | AutoField ForeignKey IR v3, atomic binding과 REL-001 제품 metadata |
| [GDJ-0025](0025-forward-foreign-key-predicate-product-slice.md) | completed | REL-004 forward ForeignKey predicate와 SQLite reusable INNER JOIN |
| [GDJ-0026](0026-forward-foreign-key-object-cache-and-nullability-product-slice.md) | completed | REL-003/006 forward object cache, nullable access와 SQLite isnull trim |
| [GDJ-0027](0027-reverse-foreign-key-accessor-and-lookup-product-slice.md) | completed | REL-005 reverse ForeignKey accessor와 exact lookup |
| [GDJ-0028](0028-reverse-foreign-key-prefetch-product-slice.md) | completed | REL-012 reverse ForeignKey one-batch prefetch와 atomic warm related set |
| [GDJ-0029](0029-one-hop-forward-select-related-product-slice.md) | completed | REL-009/010/011 one-hop forward required/nullable `select_related`와 reverse-path rejection |
| [GDJ-0030](0030-project-bound-protect-and-set-null-delete.md) | completed | REL-007/008 project-bound PROTECT/SET_NULL SQLite low-level delete |
| [GDJ-0031](0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md) | completed | Relation-aware project facade와 generated upgrade의 test-only compile usability |
| [GDJ-0032](0032-production-forward-project-facade-and-additive-first-publication.md) | completed | Production forward project facade와 additive single-companion first publication |
| [GDJ-0033](0033-forward-foreign-key-assignment-save-and-cache-ownership.md) | completed | REL-002 forward ForeignKey assignment/save/cache ownership |
| [GDJ-0034](0034-typed-generated-select-related-cause-preservation.md) | completed | Typed generated `select_related` resolve/bind original cause 보존 |
| [GDJ-0035](0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md) | superseded | Relation-capable migration D4d~D4f와 D4g Phase 0; compatibility topology/publication은 GDJ-0036으로 대체 |
| [GDJ-0036](0036-pre-release-compatibility-reset.md) | completed | Current-only Schema/Definition/State, explicit loaded set, unified backend와 generated ABI reset |
| [GDJ-0037](0037-project-schema-generated-bundle-and-recoverable-publication.md) | completed | Project Schema bundle, whole-candidate compile과 recoverable coordinated publication |
| [GDJ-0038](0038-postgresql-and-minimal-web-vertical-slices.md) | completed | PostgreSQL current backend와 최소 Web/Article HTTP 병렬 수직 단면 |
| [GDJ-0039](0039-typed-projection-scalar-aggregate-and-stable-pagination.md) | completed | Typed projection, scalar aggregate와 stable pagination Article 수직 단면 |
| [GDJ-0040](0040-composable-typed-boolean-predicates-and-article-search.md) | completed | Composable typed Boolean predicate와 Article 검색 수직 단면 |
| [GDJ-0041](0041-typed-scalar-comparisons-field-references-and-article-filtering.md) | completed | Typed scalar comparison, field reference와 Article advanced filtering |
| [GDJ-0042](0042-project-linked-runserver-and-article-development-loop.md) | completed | Project-linked `runserver`와 Article 개발 루프 |
| [GDJ-0043](0043-safe-template-validation-session-auth-and-article-admin.md) | completed | Safe template, validation, session/auth와 Article Admin 수직 단면 |
| [GDJ-0044](0044-session-authenticated-article-json-api-and-parameterized-routing.md) | completed | Session-authenticated Article JSON API와 closed parameterized routing |
| [GDJ-0045](0045-durable-single-runtime-system-state-and-article-restart.md) | completed | Durable single-runtime system state와 restart-preserving Article Admin/API |
| [GDJ-0046](0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md) | completed | Database-coordinated cooperative multi-runtime system state와 shared CSRF key ring |
| [GDJ-0047](0047-api-authentication-profiles-and-bearer-article-api.md) | completed | First-party/BFF/Bearer API authentication profile과 strict injected verifier |
| [GDJ-0048](0048-canonical-application-model-facade-and-current-generated-abi.md) | completed | Canonical embedded application model facade와 current generated ABI v3 |
| [GDJ-0049](0049-project-linked-migrate-and-clean-database-article-lifecycle.md) | completed | Project-linked explicit `migrate`와 clean-database Article lifecycle |
| [GDJ-0050](0050-project-linked-deterministic-makemigrations.md) | active | Project-linked deterministic `makemigrations`와 additive schema autodetection |

현재 활성 항목과 다음 ready 항목은 [docs/status/CURRENT.md](../docs/status/CURRENT.md)와 일치해야 합니다.
현재 active/ready packet은 1/0입니다. Active
[GDJ-0050](0050-project-linked-deterministic-makemigrations.md)은 normalized `ProjectSpec`과 latest historical
`ProjectState`를 current `CreateModel`/`AddField` 범위에서 diff하고 deterministic Definition format 1,
DB-free `godj makemigrations [--dry-run|--check]`, one-request schema/catalog snapshot과 recoverable publication을
연결합니다. Proposed
[ADR-0052](../docs/adr/0052-project-linked-deterministic-makemigrations.md)는 managed filesystem app과 read-only
programmatic app을 구분하고, exactly-one writer root, app-prefixed flat roster, source CAS/no-overwrite와
dependency-valid durable prefix/fresh resume recovery를 검증 경계로 둡니다. Phase A pure detector/current encoder와
reference-only MIG-099..110 lock에 이어 Phase B strict private v1 protocol, exact public read-only modes와 independent
build-input/catalog CAS가 local-scoped verified됐습니다. Pending normal은 Phase C 전 `publication_unavailable`; filesystem
publication/recovery와 product adapter는 미구현입니다. Current reference/product aggregate는
24/273/552=`230+19+24 locked`, 22/249=`230+19`입니다. Completed
[GDJ-0049](0049-project-linked-migrate-and-clean-database-article-lifecycle.md)은 existing current-only loader/executor와
project runner를 explicit `godj migrate`에 연결했습니다. Accepted
[ADR-0051](../docs/adr/0051-project-linked-explicit-migrate.md)은 project-owned backend opener/secret boundary,
load-before-open, latest-only/no-retry, rollback/unknown classification과 cleanup보다 긴 interrupt grace를 고정합니다.
Behavioral publication `c5af15e...`, source-bound attestation `dc3861f...`와 local-final documentation head
`8841319...`에서 middle failure/resume, full child-vs-child MIG-096, authenticated Admin/API distinct-process restart,
clean SQLite/PostgreSQL 17.10과 product publication을 닫았습니다. MIG-087..098은 exact 12 registered `passing`이고
GDJ-0049 completion 시점 reference/product는 23/261/506=`230+19+12 locked`, 22/249=`230+19`였습니다. First
local-final submitted-head run `33124180742`는 exact 27 checks 중
23 success, broad PostgreSQL package 15분 timeout 1 failure, relation-product 20분 ceiling 두 건과
conformance-validation 45분 ceiling 한 건의 cancellation으로 종료됐습니다. 수집된 로그의 assertion/panic 실패 표식은
0이지만 중단 lane은 미검증입니다. Exact required PostgreSQL selector, relation/conformance mode/workload 분리,
per-mode budget과 source-bound attestation/lock recapture를 적용한 corrected exact-head hosted matrix를 준비했습니다.
Local refreeze의 첫 `make ci`는 canceled runner metric test의 child-ready/parent-capture 경쟁에서 실패했으며,
test-only capture acknowledgment 교정 뒤 focused normal/race 반복은 통과했습니다. 전체 root gate 재실행은 하지
않았고 이 non-claim은 EVID-145에 남깁니다.
Intel race 전용 bounded timeout correction과 source-bound attestation refresh 뒤 submitted `a909692...`, tree
`b82bb5b...`의 [EVID-146](../docs/status/TEST_EVIDENCE.md#evid-20260829-146--gdj-0049-corrected-exact-head-hosted-completion) /
CI #164 run `33247166995`가 exact 41/41 jobs·464/464 steps와 PostgreSQL mode별 20/20·skip 0으로 terminal
acceptance를 닫았습니다. 다음 packet은 아직 활성화하지 않았습니다. Completed
[GDJ-0048](0048-canonical-application-model-facade-and-current-generated-abi.md)은 activation head `1070ec3...`에서
Q-017의 raw-model UX/namespace/relation-state 경계를 current project facade ABI로 좁혔습니다. Accepted
[ADR-0050](../docs/adr/0050-canonical-embedded-application-model-facade.md)은 private alias embedding, existing
`Save`/`With*`/relation/`Unwrap`, direct FK reconciliation, fail-closed method namespace와 explicit DTO representation을
결정 경계로 고정합니다. Source checkpoint/독립 attestation은 EVID-139, required PostgreSQL 18/18·full
`make ci`·Linux/386·1,088-file archive/audit의 첫 local final은 EVID-140에 보존합니다. Submitted `632904b...`의
CI #158은 네 relation-product inventory lock만 stale해 실패했고 underlying tests와 나머지 23 jobs는 통과했습니다.
Correction `6db4236...`/attestation publication `a2c067c...`의 EVID-141에서 current 1,073/1,073/0 inventory,
source-bound PostgreSQL 18/18과 full/386/1,088-file archive/audit refreeze가 다시 통과했습니다. Submitted
`17966e2...`, tree `90c46d7...`의 EVID-142/CI #159는 exact 27/27 jobs·360/360 steps, failure/cancel/skip/annotation
0으로 corrected hosted acceptance를 닫았습니다.
Schema IR/Migration/Backend와
JWT/OpenAPI/Realtime은 범위 밖입니다. Completed
[GDJ-0047](0047-api-authentication-profiles-and-bearer-article-api.md)은 corrected behavioral source `14e47c9b...`, tree
`1b2c9c74...`에서 common Session/Bearer authentication boundary, strict injected verifier, profile-neutral Article API,
AUT-009..016/API-011..012와 SQLite/PostgreSQL required flow를 게시했습니다. GDJ-0047 completion reference는
23 sets/261 contracts/506 ordered bindings=`218 passing + 19 deviation + 24 oracle_locked`, product는
21 adapters/237 contracts=`218 passing + 19 deviation`입니다. AUT-012/013/015만 Verified DEV-0009이고
[ADR-0049](../docs/adr/0049-first-party-bff-and-bearer-api-authentication.md)는 Accepted, Q-021은 `Partial`입니다.
[EVID-135](../docs/status/TEST_EVIDENCE.md#evid-20260827-135--gdj-0047-bearer-authentication-product-and-postgresql-source-checkpoint)의
source/backend checkpoint와 initial [EVID-136](../docs/status/TEST_EVIDENCE.md#evid-20260827-136--gdj-0047-frozen-local-final-gates)의
full/386/archive가 통과했습니다. First hosted run `33044776835`는 26 jobs success 뒤 macOS Intel product job 한 건이
30분 outer timeout으로 취소됐습니다. [EVID-137](../docs/status/TEST_EVIDENCE.md#evid-20260827-137--gdj-0047-first-exact-head-timeout-and-corrected-local-refreeze)의
Intel-only correction, attestation recapture와 corrected full/386/1,077-file external archive/final audit가 통과했습니다.
Corrected submitted head `5f97fa8...`, tree `2b53c031...`의
[EVID-138](../docs/status/TEST_EVIDENCE.md#evid-20260827-138--gdj-0047-corrected-exact-head-hosted-completion) / CI #155 run
`33049861740`은 exact 27/27 jobs·360/360 steps success, failure/cancel/skip/annotation 0으로 통과했습니다.
최근 terminal completion은 GDJ-0049이고 현재 활성 통합 작업은 GDJ-0050입니다. Draft PR #1은
OPEN/DRAFT/unmerged입니다. GDJ-0047 terminal docs descendant CI #156의 유일한 SQLite test-select flake는
baseline `3882902...`에서 barrier handshake로 교정됐고 activation head `1070ec3...`의 CI #157/run `33063990270`이
exact 27/27 jobs·360/360 steps로 corrected descendant를 확인했습니다.
[GDJ-0035](0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)는 D4d~D4f/D4g Phase 0
증거를 보존한 채 superseded됐습니다. GDJ-0036은 corrected exact head `57ddff3...`의 CI #101/run
`32347190714` exact 26/26 success로 completed됐습니다. GDJ-0037은 implementation head `9258a084...`의 첫 CI
#102에서 stale relation inventory lock 네 건이 실패한 뒤 correction head `d4643068...`의
[EVID-105](../docs/status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) /
CI #103 exact 26/26 jobs·326/326 steps success로 completed/hosted-verified됐습니다. 그 docs descendant
`681b0713...`도 CI #104/run `32444841140`의 exact 26/26 jobs·326/326 steps를 통과했습니다. GDJ-0038 final
correction head `187638f9...`는
[EVID-108](../docs/status/TEST_EVIDENCE.md#evid-20260823-108--gdj-0038-postgresql-1710-exact-head-hosted-completion) /
CI run `32626539049`에서 exact PostgreSQL 17.10 profile, required actual 12/12·skip 0와 restart resume를 포함한
27/27 jobs·341/341 steps·failure/skip 0으로 통과했습니다. DB-PG-001..010과 WEB-001..010 bounded slice는
Verified, GDJ-0038은 completed입니다. GDJ-0039 submitted head `253455d...`도
[EVID-110](../docs/status/TEST_EVIDENCE.md#evid-20260823-110--gdj-0039-exact-head-hosted-completion) / run
`32634741186`에서 exact 27/27 jobs·341/341 steps로 통과해 QRY-022..033 bounded slice를 Verified하고
completed됐습니다. GDJ-0040 corrected submitted head `136e825...`도
[EVID-115](../docs/status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion) /
run `32642341459`에서 exact 27/27 jobs·341/341 steps와 네 플랫폼 950/950/0 inventory를 통과해
QRY-034..043을 Verified하고 completed됐습니다. GDJ-0041 source
`7f2bb2232afa7d71bea56d8910a52a045ec11faa` / tree `221467b95b712dfed199b12f5a14ed17d987a7ac`은
Accepted ADR-0041의 typed range와 sealed same-model field reference, SQLite/PostgreSQL compiler와 Article advanced
filter를 구현했습니다. QRY-044..053 oracle-blind actual은 10/10 zero-diff이고 GDJ-0041 completion checkpoint reference는
15/171/210=`154 passing + 5 deviation + 12 oracle_locked`, product는 14/159=`154 passing + 5 deviation`입니다.
Submitted head `e97a4e3...`의 [EVID-118](../docs/status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) /
CI #115 run `32647746430`은 exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual/restart를
통과해 QRY-044..053을 Verified하고 GDJ-0041을 completed로 닫았습니다.
GDJ-0042는 terminal docs baseline `052de65...`에서 active로 전환했고 당시 Proposed ADR-0042와 WEB-011..020을
고정했습니다. Product `23b1936...`, PostgreSQL/CI `60da43b...`, clean-cache correction `6101140...`,
clean-checkout fixture correction `2a61376...`과 backend-close ownership correction `810149f...`은 optional
project-linked runtime package, read-only current-bundle preflight, loopback-only global `runserver`, long-lived child
drain/reap와 SQLite/PostgreSQL Article actual을 locally implemented/passing으로 전환했습니다. Final
full/386/803-file external archive와 세 audit는 documentation checkpoint `47b0eb8...`의 EVID-120에서 통과했고,
first submitted `46a57aa...` run은 26 success/1 timeout이었습니다. Correction `2b49938...`의 EVID-121 local
refreeze 뒤 submitted `2bfdbd5...`의 EVID-122/run `32659704239`가 exact 27/27 jobs·358/358 steps와
failure/cancel/skip/annotation 0으로 통과했습니다. ADR-0042는 Accepted, WEB-011..020은 bounded hosted
`Verified`, GDJ-0042는 completed입니다.
GDJ-0034 terminal head `0bb8c969...`는 EVID-083/run `31613170021`의 고유 exact 26/26 jobs·326/326 steps과
audit P0..P3=0을 통과했습니다. 그 당시 product는 `122 passing + 5 deviation + 0 oracle_locked`, relation
12/12이며 Q-010/Q-012/Q-013은 Partial, Q-017/Q-019는 P1/open입니다. GDJ-0035는 MIG-075..086
exact 12 reference-only contracts를 고정했고 [ADR-0034](../docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)는
bounded design에 한해 Accepted입니다. Activation head `52f9bcb7...`는 EVID-084/run `31618469072`, Phase A committed head
`84e16bf...`는 EVID-086/run `31625898551`의 서로 다른 unique exact-head 26/26 jobs·326/326 steps와
audit P0..P3=0을 통과했습니다. Reference는 exact 13/139/156=`122+5+12 locked`, product는 exact
12/127=`122+5+0`으로 불변입니다. Phase A는 hosted-verified됐고 Phase B exact 14개 `_test.go` no-product
candidate도 completed and hosted-verified됐습니다. EVID-087에서 exact 693,557 bytes/`ca579837...09e5`, inventory
75/75/0·9,736 bytes·`48e7beb1...92ec`, root/exact/focused gates와 두 independent audit P0..P3=0을 고정했습니다.
Exact Phase B head `c2ecb292...`는 EVID-088/run `31653237691`의 고유 attempt-1 exact 26/26 jobs·342/342
steps와 hosted audit P0..P3=0을 통과했습니다. Phase C exact 8-test-only decision proof head `7d36502...`도
EVID-089/090/run `32174259324`의 고유 exact 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다.
One-loader/profile/digest, whole-step state, wire target-key derivation, three-stage preflight, additive existing-fence
port/four capabilities와 SQLite order를 ADR-0034에 동결했습니다. Proposed docs-freeze head `5bdf013...`는
EVID-091/run `32183309328`의 고유 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했고 이 별도 head에서
bounded design을 Accepted로 전환했습니다. Acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`의
고유 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다. Later D1 definition/handoff
`42aa9a9...`/`f22a498...`, D2 private state/readiness `ec8877e...`/`80776b5...`, D3a direct optional
SQLite Create/Delete `2eafde1...`/`ce58c5e...`는 EVID-093/runs `32195313382`, `32205324145`,
`32218003207`에서 각 bounded Implemented/Verified됐습니다. D3b loaded relation core
`74c2b72...`/`167ef03...`도 EVID-094/run `32231149900`에서 normal loaded SQLite Create/Delete
apply/unapply/reapply, exact-one history/actual-plan preflight와 scalar/no-op/unsupported-tail 경계를
Implemented/Verified했습니다. D4 exact one-test-file head `424ec4d...`도 EVID-095/run `32248885053`에서
fresh loaded set/backend와 captured schema/rows/history/token/FK snapshot을 사용한 bounded file-backed
full/branch/full restart를 Verified했습니다. Product source/API/workflow/inventory는 불변이고
그 당시 Add/Remove/remake caps는 false였습니다. D4b exact18 docs head `84588f9...`는 EVID-096/run
`32252834752`, D4c exact one-test-file taxonomy head `e4fbc7b...`는 EVID-096/run `32256113658`의 각 unique
26/26·342/342·audit P0..P3=0을 통과했습니다. D4c는 Begin/PRAGMA-set/catalog/claim-busy
`NoOperation`, final-FK operation 1 `AddField`, recorder `NoOperation`의 bounded forward proof이며 product/API/
workflow/capability/status/inventory를 바꾸지 않습니다. EVID-096 exact-six docs head `62df9b2...`는 run
`32260744096`에서 고유하게 닫혔습니다. D4d product `3950d98...`/inventory lock `28b141e...`의 첫 hosted
run `32267789056`은 macOS Intel race의 wall-clock assertion P1로 25/26에서 실패했고, deterministic visit-count
fix `dd83362...` 뒤 distinct run `32271361724`가 exact 26/26·342/342·audit P0..P3=0을 통과했습니다.
EVID-097은 sealed/resolvable same-target snapshot에 한정한 nullable ForeignKey Add, native ALTER, populated row/
sequence, canonical mixed SQL, reopen/fault/resource proof를 기록합니다. 그 D4d head의 capability는
`{true,true,false,false}`였고
EVID-097 docs head `c59669c...`는 run `32278555810`에서 별도로 닫혔습니다. D4e product `7c07805...`/inventory
lock `1d86f6e...`는 EVID-098/run `32282269755`에서 empty-source required `PROTECT` Add, native NOT NULL ALTER,
pre-claim emptiness, fault/reopen 경계를 exact 26/26·342/342·audit P0..P3=0으로 검증했습니다. Capability는
당시 `{true,true,true,false}`였습니다. EVID-098 docs head `85f9270...`는 CI #94/run `32288383027`에서
별도로 닫혔고 D4f product `4982e27...`/inventory lock `9d5b894...`는 EVID-099/CI #95 run
`32294983953`에서 exact appended nullable `PROTECT` 또는 `SET_NULL`, required `PROTECT` reverse를 허용하는
bounded table remake를 구현했습니다. Frozen direct E2E fixture는 nullable `PROTECT`와 required `PROTECT`만
검증했으며 dedicated nullable `SET_NULL` D4f E2E proof는 주장하지 않습니다. Row/sequence preservation,
fault ownership/rollback/no-retry와 reopen/reapply를 exact 26/26·342/342·audit P0..P3=0으로 검증했습니다.
Capability는 `{true,true,true,true}`입니다. 다음은 oracle-blind D4g observer
characterization이며 MIG-075..086은 계속 locked입니다. Omitted allowed path와 DEV/deviation 필요 여부를
explicit decision하기 전에는 status/deviation을 바꾸지 않습니다. Arbitrary/general target/remake universe는
주장하지 않습니다.
당시 Active는 0, ready는 0이었고 Draft PR은 merge하지 않습니다.

위 GDJ-0034/0035 단락은 reset 이전 증거 계보입니다. 현재 format/API/publication 정본은 GDJ-0036과
[ADR-0035](../docs/adr/0035-pre-release-current-only-format-and-generated-publication.md)이며, legacy tuple/context
handoff/optional backend/generated-byte 보존을 현재 계약으로 재활성화하지 않습니다.

GDJ-0024 final evidence/status head `5bf143575e9b703117a328c1fc5b7eb5823fbfd6`은 run
`31351169780`의 exact 26/26 jobs·326/326 recorded steps를 통과해 EVID-038에 기록됐습니다. GDJ-0025는
이 clean tested baseline에서 REL-004-only query/join 수직 단면과 Proposed ADR-0025를 활성화했습니다.
Activation commit `cf8cb589575836cb1393079ce04ff06fc549800a`는
[run 31354040515](https://github.com/progresshans/godj/actions/runs/31354040515)의 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. Frozen implementation은 local `make ci`, relation inventory
492/492/0·49,902 bytes·SHA-256 `05064a7f...82eb`, twelve adapters, bounded Linux/386 compile과 four
independent audits P0/P1/P2/P3=0을 통과해
[EVID-20260810-039](../docs/status/TEST_EVIDENCE.md#evid-20260810-039--gdj-0025-rel-004-forward-predicate-pre-hosted-local-validation)에
기록했습니다. Implementation commit `98db55a30ff71a2f2f70722cb569a046208a5403`은
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)의 exact 26/26 jobs·326/326
recorded steps를 통과했고
[EVID-040](../docs/status/TEST_EVIDENCE.md#evid-20260810-040--gdj-0025-github-hosted-exact-26-job-implementation-head-ci)에
기록했습니다. Product는 REL-001/004 actual 2/12, exact
`112 passing + 5 deviation + 10 oracle_locked`입니다. Work는 completed, ADR-0025는 required one-hop
exact/SQLite INNER JOIN에 한해 Accepted이고 Q-013은 `Partial`입니다. Completion-documentation commit
`7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26 jobs·326/326
recorded steps를 통과해
[EVID-041](../docs/status/TEST_EVIDENCE.md#evid-20260810-041--gdj-0025-github-hosted-completion-documentation-head-exact-26-job-ci)에
기록했습니다. 그 EVID-041/final-status commit `bffc52844de87a2791959ea1e8f99c60dd13d1aa`도 별도
[run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)의 exact 26/26 jobs·326/326
steps를 통과해 [EVID-042](../docs/status/TEST_EVIDENCE.md#evid-20260810-042--gdj-0025-final-exact-head-ci-and-gdj-0026-activation-baseline)에
기록했습니다. GDJ-0026은 이 tested baseline에서 REL-003/006 object/cache/nullability slice와 ADR-0026을
활성화했습니다. Exact 15-file activation commit
`aad4f7ff0d77a1abe16ebddd01782e78c335395f`은
[run 31364944816](https://github.com/progresshans/godj/actions/runs/31364944816)의 exact 26/26 jobs·326/326
steps를 통과했습니다. Implementation은 sealed object cache, additive object companion/
project bridge, nullable relation `source_key` AST와 SQLite JOIN-0 trim을 구현했고 local exact
`114 passing + 5 deviation + 8 oracle_locked`, relation actual REL-001/003/004/006 4/12와 inventory
533/533/0·54,076 bytes·SHA-256 `6d2958b6...7aee`를 통과해
[EVID-043](../docs/status/TEST_EVIDENCE.md#evid-20260810-043--gdj-0026-rel-003006-object-cache-and-nullability-pre-hosted-local-validation)에
기록했습니다. Implementation commit `5be46141d943800a3c621975e3e5070f6d01eaf9`의
[run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755)은 exact 26/26 jobs·326/326
recorded steps, relation-product four-coordinate 533/533/0 inventory와 actual Ubuntu Linux/386 exact package
set을 통과했습니다. EVID-044와 hosted audit P0/P1/P2/P3=0을 근거로 work는 completed, ADR-0026은 bounded
slice에 한해 Accepted입니다. Completion-documentation commit
`7f92fcf036d03a5004953d9857a10291f4603efb`의 별도
[run 31372360481](https://github.com/progresshans/godj/actions/runs/31372360481)도 exact 26/26·326/326과 hosted
audit P0/P1/P2/P3=0을 통과해 EVID-045에 기록했습니다. Q-013은 `Partial`이며 이 EVID-045를 포함한 exact
8-file final evidence/status patch는 아래 EVID-046/run `31374150640`에서 별도로 닫혔습니다.
그 final-status commit `9ba1d0ee4cb96c265269000700beb5889fef2206`은 별도
[run 31374150640](https://github.com/progresshans/godj/actions/runs/31374150640)의 exact 26/26·326/326을
통과해 EVID-046에 기록했습니다. GDJ-0027은 이 clean tested baseline에서 REL-005-only reverse query/object
split, project-only generator와 SQLite reverse INNER JOIN을 ADR-0027로 활성화했습니다. Activation은
activation commit `9dbc2fd2ab3201e8968f65b31db8eedf3f9a845a`의
[run 31414060387](https://github.com/progresshans/godj/actions/runs/31414060387)에서 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. 이 run의 four relation-product 533/533/0 inventory는 activation baseline만
증명하며 implementation에 재사용하지 않습니다. Pre-hosted implementation은 local normal gate에서
exact `115 passing + 5 deviation + 7 oracle_locked`, relation actual REL-001/003/004/005/006 5/12와
569/569/0·57,738 bytes·SHA-256 `739bb6fc...c2d7`을 통과했고 runtime/codegen/final integration audits는
P0/P1/P2/P3=0입니다. 상세 분리는
[EVID-047](../docs/status/TEST_EVIDENCE.md#evid-20260811-047--gdj-0027-rel-005-reverse-accessor-and-lookup-pre-hosted-local-validation)에
기록했습니다. Implementation commit `7db684159ecfebbcbe1dc0673928e899ab8b0835`의
[run 31419940399](https://github.com/progresshans/godj/actions/runs/31419940399)은 exact 26/26 jobs·326/326
recorded steps, four-coordinate 569/569/0 inventory, actual Ubuntu Linux/386, exact Darwin/Python과 hosted audit
P0/P1/P2/P3=0을 통과했습니다. EVID-048을 근거로 work는 completed, ADR-0027은 bounded REL-005 slice에 한해
Accepted입니다. Completion-documentation commit `7998a8351c7668d53b9263bc9a381a815c6c9eb6`의 별도
[run 31422614250](https://github.com/progresshans/godj/actions/runs/31422614250)도 exact 26/26·326/326과 hosted
audit P0/P1/P2/P3=0을 통과해 EVID-049에 기록했습니다. Q-013은 `Partial`입니다. EVID-049를 포함한 terminal
7-file evidence/status head `e9dc361f983f1c02af1f63737a1f282998d5a533`은 이후 별도
  [run 31424055711](https://github.com/progresshans/godj/actions/runs/31424055711)의 exact 26/26·326/326과 hosted
  audit P0/P1/P2/P3=0을 통과해 EVID-050에 기록됐습니다. GDJ-0028은 이 clean tested baseline에서 REL-012-only
  reverse FK IN batch, atomic warm `RelatedSet` publication과 Proposed ADR-0028을 활성화했습니다. Activation commit
  `3ae4a2cecacd31a8cc72fd46ea288568e0071421`의
  [run 31429245980](https://github.com/progresshans/godj/actions/runs/31429245980)은 exact 26/26·326/326과 hosted
  audit P0/P1/P2/P3=0을 통과했습니다. Uncommitted exact 39-path implementation은 root `make ci`, local exact
  `116 passing + 5 deviation + 6 oracle_locked`, relation 6/12, 594/594/0·60,237 bytes·SHA-256
  `98a0a37b...8c47e`와 independent audits P0/P1/P2/P3=0을 통과해 EVID-051에 별도로 기록했습니다.
  Implementation commit `4858ab88b82647793cd463e9f348e43d3f5e4bb7`의
  [run 31432551159](https://github.com/progresshans/godj/actions/runs/31432551159)은 exact 26/26·326/326,
  four-coordinate 594/594/0 inventory, actual Ubuntu Linux/386, exact Darwin/Python과 hosted audit
  P0/P1/P2/P3=0을 통과했습니다. EVID-052를 근거로 work는 completed, ADR-0028은 bounded REL-012 SQLite
  slice에 한해 Accepted입니다. Completion-documentation commit
  `9dc4eb1312791ae74b384afbbfdbfef89aaf55bb`의 별도
  [run 31435136950](https://github.com/progresshans/godj/actions/runs/31435136950)도 exact 26/26·326/326과 hosted
  audit P0/P1/P2/P3=0을 통과해 EVID-053에 기록했습니다. EVID-053을 포함한 terminal 7-file evidence/status
  기록은 documentation-only이며 completion run을 그 later patch의 recursive proof로 재사용하지 않습니다.
  그 terminal evidence/status commit `5c0efef12560203d720e4c2dd7bda50c0324a228`의 별도
  [run 31436881856](https://github.com/progresshans/godj/actions/runs/31436881856)은 exact 26/26·326/326과 hosted
  audit P0/P1/P2/P3=0을 통과해 EVID-054에 기록했습니다. GDJ-0029는 이 clean tested baseline에서
  REL-009/010/011 indivisible one-hop forward eager projection과 Proposed ADR-0029를 활성화했습니다. Activation
  commit `0a1da373a443527e48a154ca6ccc7284e5e80dc0`의
  [run 31465198903](https://github.com/progresshans/godj/actions/runs/31465198903)은 exact 26/26·326/326과 hosted
  audit P0/P1/P2/P3=0을 통과했습니다. Pre-commit exact 49-entry implementation은 root `make ci`, exact
  630/630/0·63,928 bytes·SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`와
  local `119 + 5 + 3`, relation 9/12를 통과했습니다. Independent pre-commit audit에서 forged
  source-key/projection provenance P1을 발견·재현·수정했고 post-fix runtime/codegen/integration/remediation
  audits는 모두 P0/P1/P2/P3=0입니다. Implementation commit
  `c02aab672db5175d7a0886688efb5cc684c67744`의
  [run 31470292759](https://github.com/progresshans/godj/actions/runs/31470292759)은 exact 26/26·326/326,
  four-coordinate 630/630/0 inventory, actual Ubuntu Linux/386, exact Darwin/Python과 hosted audit
  P0/P1/P2/P3=0을 통과했습니다. EVID-056을 근거로 product는 current `119 + 5 + 3`, relation 9/12,
  work는 completed, ADR-0029는 bounded engine slice에 한해 Accepted입니다. Q-013은 `Partial`, Q-017은
  P1/open입니다. Completion-documentation commit `fb9985e20c92f71eaca7bac81bc61466369e0ebd`의 별도
  [run 31482242288](https://github.com/progresshans/godj/actions/runs/31482242288)도 exact 26/26·326/326,
  four-coordinate 630/630/0 inventory와 hosted audit P0/P1/P2/P3=0을 통과해 EVID-057에 기록했습니다.
  EVID-057을 포함한 terminal exact seven-file commit `d0396c76d016c0f0335b484fbad56c70b80cf6d4`도 별도
  [run 31484369693](https://github.com/progresshans/godj/actions/runs/31484369693)의 exact 26/26·326/326,
  four-coordinate 630/630/0 inventory와 source diff 0을 통과해 EVID-058에 기록했습니다. GDJ-0030은 이 clean
  tested baseline에서 REL-007/008 indivisible low-level delete와 Proposed ADR-0030을 활성화했고 EVID-058은
  activation/implementation proof로 재사용하지 않았습니다. Corrected activation `48472a1c...`의 EVID-060/run
  `31503631942` 뒤 implementation head `c3803acba1929921f23e4751679dc21d4bba9c0f`의 EVID-061/run
  `31510689383`이 exact 26/26·326/326, four-coordinate 687/687/0·69,597 bytes·SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`, Ubuntu `make ci`, actual Linux/386,
  exact Darwin/four Python과 independent P0/P1/P2/P3=0을 통과했습니다. Product는 current
  `121 + 5 + 1`, relation 11/12, REL-002 locked이고 work는 completed, ADR-0030은 bounded slice에 한해
  Accepted입니다. Exact 15-file completion-documentation head `635e9c38...`의 EVID-062/run `31514159835`도
  별도 exact 26/26·326/326, unchanged four-coordinate 687/687/0 inventory와 independent P0/P1/P2/P3=0을
  통과했습니다. EVID-062를 포함한 later exact seven-file terminal evidence/status patch는 그 시점에
  `not run/pending`이었고 completion run을 재사용하지 않았습니다. 그 terminal head
  `ceff9e5...`은 별도 EVID-063/run `31516174741`의 exact 26/26·326/326과 independent audit P0..P3=0을 통과해
  GDJ-0031 baseline이 됐고 Draft PR은 merge하지 않았습니다. EVID-063은 later activation tree의 proof로
  재사용하지 않습니다. 이후 GDJ-0031 activation EVID-064와 compile implementation EVID-065가 각각 별도 exact
  26/26·326/326 hosted gate를 통과해 work는 completed, ADR-0031은 test-only feasibility 방법에 한해
  Accepted됐습니다. Completion-documentation head `e9b2c0e...`도 EVID-066/run `31531470440`의 별도 exact
  26/26·326/326을 통과했습니다. Exact seven-file terminal head `3d661251...`도 EVID-067/run
  `31533890720`의 별도 exact 26/26·326/326을 통과했고 completion run을 재사용하지 않았습니다. 그 terminal
  head 시점에 Q-013은 Partial, Q-017은 P1/open이고 GDJ-0032가 active였습니다.
  이후 GDJ-0032 activation EVID-068와 production facade implementation EVID-069가 각각 별도 exact
  26/26·326/326 hosted gate를 통과했습니다. Completion-documentation head `6089e214...`도 EVID-070/run
  `31544273477`의 별도 exact 26/26·326/326을 통과했습니다. EVID-070을 추가한 exact seven-file terminal head
  `8748bb49...`도 EVID-071/run `31563615648`의 별도 exact 26/26·326/326을 통과했고 completion run을
  재사용하지 않았습니다. EVID-071은 later GDJ-0033 activation proof로 재사용하지 않습니다.
GDJ-0024는 baseline `50578ddc...`의 EVID-034 exact 22/22를 GDJ-0023 final evidence로 닫고,
mixed v2 target/v3 relation source companion, atomic `orm.BindProject`와 REL-001 metadata만 구현할 exact
boundary를 활성화했습니다. Activation commit `758cd093...`은 run `31344980929`의 exact 22/22·273/273
steps를 통과했습니다. Implementation commit `05e6e218...`은 IR v3/companion/bridge/binder/migration
fail-closed와 REL-001 metadata actual, 11 ordered not-implemented, exact-26 workflow를 구현했고 run
`31348285559`의 exact 26/26 jobs·326/326 recorded steps를 통과했습니다. Work는 completed, ADR-0024는
bounded metadata architecture에 한해 Accepted이고 Q-013은 `Partial`입니다. GDJ-0022는
Accepted ADR-0021/MIG-065..074를 independent product global CLI/public project runner/flat discovery로
구현해 10 contract를 `passing`으로 전환했고, final evidence head까지 exact 18-job hosted acceptance를
완료했습니다. GDJ-0023은 Q-013을 추측으로 제품화하지 않고 pinned Django 6.1 ForeignKey 외부 동작
REL-001..012와 test-only cross-app binding/import-cycle/shared-AST feasibility를 고정했습니다.
GDJ-0020은
`codex/revision-fenced-migration-lifecycle@6172d843a4bb234592cafc176a8d1191933b141c`의 제품 구현과
Draft PR #1 exact-head CI를 근거로 완료됐고,
[ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 Accepted입니다.

완료된 GDJ-0019는 explicit caller-provided strict data document, compatibility tuple `(1,1,1,2)`,
fully normalized IR v2, `CreateModel`/non-PK `char`·`boolean` `AddField` codec, atomic/deterministic load와 existing
`Executor.Migrate` reference handoff를 MIG-057..064로 잠그는 contract-only 작업입니다. 완료
결과는 기존 9 product set의 `92 passing + 5 deviation`을 보존하면서 8 `oracle_locked`를 더한
10 reference set/105 contract와 90 ordered cross-binding rejection입니다. Completed GDJ-0020은
열 번째 product adapter를 구현해 105 product contract의 `100 passing + 5 deviation`을
local/exact-head hosted gate에서 검증했습니다. Filesystem/module discovery, CLI, writer/upgrade/cache는
여전히 제품 미구현 범위입니다. Completed GDJ-0021은 그중 가장 작은 DB-free
`godj migrations check` 경험의 project selection/build/runner/flat discovery를 MIG-065..074의
contract-only decision reference로 검증했습니다. 결과는 11 reference set/115 contract/110 ordered
cross-binding과 새 10 `oracle_locked`이며, 제품은 계속 10 adapter/105 contract의
`100 passing + 5 deviation`입니다. [ADR-0021](../docs/adr/0021-project-linked-migration-check.md)은
local/10-job hosted evidence를 근거로 Accepted이지만 전역 CLI나 project package가 구현됐다는 뜻이
아닙니다. Q-010/Q-012는 `Partial`을 유지합니다. Status 7 + general 9의 exact 16-file
completion-documentation commit `34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도 exact 10-job
hosted CI를 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 run `31322959993`에서 exact 10 jobs를 통과했습니다.
GDJ-0022는 exact 11 adapter/115 contract의 `110 passing + 5 deviation`과 Accepted
[ADR-0022](../docs/adr/0022-project-runtime-and-global-migration-check.md)를 구현했습니다. Initial head
`06858dd6aafeb20449bc4fbfa9aeac78c7a794ce`의 run `31329231255`는 네 Python leg 모두 테스트 전 uv
exact-string assertion에서 실패해 취소했고, metadata suffix를 허용한 fix head
`3dfeff2a881a3313883729943519896798d92afc`의 run `31329294154`는 existing 10 + product 4 + Python
compatibility 4인 exact 18/18을 성공했습니다. EVID-028/status commit
`68b408add3b050d0938ccebc6c83200499f57b2a`의 run `31330601427`은 16 success/2 macOS product normal
failure였고, helper/harness와 production wait reconciliation을 고친 final stabilization head
`385382efffd1872ae7fb427192bab27b95dc57e2`의 run `31332208055`는 exact 18/18을 다시 성공했습니다.
EVID-029/status commit `1f161f311daa775e6a386ec0df568ff85d681f15`도 별도 run `31333420261`의
exact 18/18을 통과했고 EVID-030에 기록했습니다. GDJ-0023 activation commit
`d5d00d9e803c637a78961ed6f7dac0b415ce7901`도 제공된 verified run `31335315454`의 기존 exact 18/18을
통과했습니다. Phase A REL-001..012 reference와 Phase B test-only relationbinding은 local 구현/검증,
두 independent final audit P0/P1/P2/P3 finding 0과 implementation head `b56ccf5`의
[run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743) exact 22/22까지
통과했습니다. Work는 completed, ADR-0023은 Accepted, Q-013은 `Partial`이며 relation product adapter는
0입니다. Completion-documentation head `31784ae1`도 별도
[run 31339409336](https://github.com/progresshans/godj/actions/runs/31339409336)의 exact 22/22와
[EVID-20260810-033](../docs/status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)으로
검증했습니다. 이어 final evidence/status head `50578ddc4756452b2a9a0d2afd75711a35b76d8a`의
[run 31340170361](https://github.com/progresshans/godj/actions/runs/31340170361)도 exact 22/22와 273/273
steps를 성공해
[EVID-20260810-034](../docs/status/TEST_EVIDENCE.md#evid-20260810-034--gdj-0023-final-evidence-documentation-exact-head-ci-and-gdj-0024-activation-baseline)에
기록했습니다. 이 tested clean baseline 뒤 GDJ-0024 activation 문서 diff 자체의 exact-head hosted CI는
activation commit `758cd093...` / run `31344980929`의 exact 22/22로 해소됐습니다. GDJ-0024 local
implementation/audit와 그 증거는
[EVID-20260810-035](../docs/status/TEST_EVIDENCE.md#evid-20260810-035--gdj-0024-rel-001-metadata-product-pre-hosted-local-validation)에
기록했습니다. Implementation exact-head exact-26 hosted acceptance는
[EVID-20260810-036](../docs/status/TEST_EVIDENCE.md#evid-20260810-036--gdj-0024-github-hosted-exact-26-job-implementation-head-ci)에
기록했습니다. GDJ-0024 완료 당시 product는 12 adapter/127 contract=
`111 passing + 5 deviation + 11 oracle_locked`, REL-001
actual 1/12이며 broader relation query/write/delete/DDL/migration과 non-SQLite backend support는 아닙니다.

## 운영 규칙

- 작업 시작 전에 baseline branch/commit과 dirty files를 기록합니다.
- 목표, 비목표, contract ID, 수정 허용 경로, 완료 조건이 없으면 구현을 시작하지 않습니다.
- 공개 API 또는 장기 결정이 바뀌면 ADR을 먼저 또는 같은 변경에서 갱신합니다.
- 체크리스트만 완료로 바꾸지 말고 실제 변경 파일과 evidence ID를 기록합니다.
- 중단 시 실패한 정확한 명령과 다음에 실행할 명령을 적습니다.
- completed 항목은 결과와 남은 제한을 보존합니다. 나중 상태로 덮어쓰지 않습니다.

새 항목은 [TEMPLATE.md](TEMPLATE.md)를 사용합니다.
