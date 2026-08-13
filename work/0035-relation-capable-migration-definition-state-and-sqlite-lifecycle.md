---
id: GDJ-0035
status: active
updated: 2026-08-13
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "0bb8c969d0658f50f40d916996f027e7393bce14"
depends_on: ["GDJ-0034"]
contracts: ["MIG-075..MIG-086", "Q-010", "Q-012", "Q-013"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "conformance/README.md"
  - "conformance/migrationrelation/**"
  - "conformance/contracts/migration-relation-manifest.json"
  - "conformance/fixtures/godj-migration-relation-not-implemented.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/runners/django/migration_relation_scenarios.py"
  - "conformance/runners/django/runner.py"
  - "conformance/runners/django/scenarios.py"
  - "conformance/runners/django/tests/test_migration_relation_scenarios.py"
  - "conformance/runners/django/tests/test_runner_safety.py"
  - "conformance/runners/django/tests/test_scenarios.py"
  - "conformance/runners/django/migration_relation_fixture/__init__.py"
  - "conformance/runners/django/migration_relation_fixture/apps.py"
  - "conformance/runners/django/migration_relation_fixture/migrations/__init__.py"
  - "conformance/runners/django/migration_relation_fixture/migrations/0001_initial.py"
  - "conformance/runners/django/migration_relation_fixture/migrations/0002_nullable_relation.py"
  - "conformance/runners/godj/migration_relation_scenarios.go"
  - "conformance/runners/godj/runner.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/internal/protocol/migration_definition_source_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/migration_relation_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/migrationrelationproduct/observer.go"
  - "conformance/migrationrelationproduct/product_test.go"
  - "migrations/definition/codec.go"
  - "migrations/definition/definition.go"
  - "migrations/definition/definition_test.go"
  - "migrations/definition/digest.go"
  - "migrations/definition/digest_test.go"
  - "migrations/definition/error.go"
  - "migrations/definition/external_test.go"
  - "migrations/definition/ir.go"
  - "migrations/definition/json.go"
  - "migrations/definition/limits.go"
  - "migrations/definition/load.go"
  - "migrations/definition/profile.go"
  - "migrations/definition/profile_test.go"
  - "migrations/definition/relation.go"
  - "migrations/definition/relation_test.go"
  - "migrations/definition/resource_limits_test.go"
  - "migrations/backend/backend.go"
  - "migrations/backend/lifecycle.go"
  - "migrations/backend/relation.go"
  - "migrations/backend/relation_test.go"
  - "migrations/execution.go"
  - "migrations/execution_test.go"
  - "migrations/executor.go"
  - "migrations/executor_test.go"
  - "migrations/external_test.go"
  - "migrations/lifecycle.go"
  - "migrations/lifecycle_test.go"
  - "migrations/operation.go"
  - "migrations/operation_test.go"
  - "migrations/planner.go"
  - "migrations/planner_graph.go"
  - "migrations/planner_test.go"
  - "migrations/reconstructor.go"
  - "migrations/reconstructor_test.go"
  - "migrations/state.go"
  - "migrations/state_test.go"
  - "db/sqlite/backend.go"
  - "db/sqlite/backend_internal_test.go"
  - "db/sqlite/integration_test.go"
  - "db/sqlite/migration_backend.go"
  - "db/sqlite/migration_backend_test.go"
  - "db/sqlite/migration_history.go"
  - "db/sqlite/migration_history_test.go"
  - "db/sqlite/migration_lifecycle.go"
  - "db/sqlite/migration_lifecycle_test.go"
  - "db/sqlite/migration_relation.go"
  - "db/sqlite/migration_relation_test.go"
  - "db/sqlite/migration_remake.go"
  - "db/sqlite/migration_remake_test.go"
  - "db/sqlite/migration_sql.go"
  - "db/sqlite/migration_sql_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/migration_relation_external_consumer.go.txt"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/LICENSING.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0034-typed-generated-select-related-cause-preservation.md"
  - "work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Relation-capable Migration Definition, State, and SQLite Lifecycle

## 사용자에게 보이는 단일 결과

완료 후 사용자는 기존 scalar migration definition을 다시 쓰지 않은 채 relation-capable definition을 같은
project batch에 함께 두고, AutoField 대상 `ForeignKey`가 포함된 historical state를 SQLite에서
apply/unapply/reapply/restart할 수 있습니다. `PROTECT`와 `SET_NULL`은 GoDj가 application mutation에서
집행하고 SQLite physical constraint는 두 정책 모두 `NO ACTION`으로 유지합니다.

이 문서는 그 목표를 검증하기 위한 contract-first 활성 packet입니다. 아래 format/state/DDL shape와
[ADR-0034](../docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)는 아직
**Proposed candidate**이며 구현·검증·Accepted 결정이 아닙니다.

## 기준 상태와 activation 경계

- Clean baseline은 GDJ-0034 terminal head
  `0bb8c969d0658f50f40d916996f027e7393bce14`, tree
  `341deb1da8d864f21252a6e3846745af36c1551e`입니다.
- [EVID-083](../docs/status/TEST_EVIDENCE.md#evid-20260812-083--gdj-0034-terminal-exact-head-ci-and-clean-baseline) /
  [run 31613170021](https://github.com/progresshans/godj/actions/runs/31613170021)은 이 exact baseline을
  26/26 jobs·326/326 steps success와 independent audit P0/P1/P2/P3=`0/0/0/0`으로 검증했습니다.
- EVID-083은 GDJ-0034 terminal closure와 GDJ-0035의 clean baseline만 증명합니다. Exact 16-document
  activation head `52f9bcb7fedb2333a4c5e6f0e016aec15381c806`, tree
  `58acca30d8e42f4fa8a76886a03c3e2e58dcc258`는
  [EVID-084](../docs/status/TEST_EVIDENCE.md#evid-20260812-084--gdj-0035-activation-documentation-head-exact-26-job-ci) /
  [run 31618469072](https://github.com/progresshans/godj/actions/runs/31618469072)의 고유 exact
  26/26 jobs·326/326 steps와 audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Baseline run을 activation
  proof로 재사용하지 않았습니다.
- 현재 제품 분류는 exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`, relation
  12/12로 불변입니다. Existing 12 set/127 contract/132 ordered cross-binding 제품 기준도 불변입니다.
- 이 activation은 source, workflow, manifest, oracle, fixture, checksum 또는 product status를 바꾸지 않습니다.
  이후 Phase A는 [EVID-085](../docs/status/TEST_EVIDENCE.md#evid-20260813-085--gdj-0035-phase-a-reference-only-artifacts-and-local-validation)에서
  exact 13 reference sets/139 contracts/156 ordered cross-bindings와 새 12 `oracle_locked`를 로컬에서 고정했습니다.
  Exact committed head `84e16bf193fc2079cd87788249e6e4a694f2402c`, tree
  `e6e3a749ee82c0162556aec99b53772b1fe08cc9`는 별도
  [EVID-086](../docs/status/TEST_EVIDENCE.md#evid-20260813-086--gdj-0035-phase-a-github-hosted-reference-only-exact-head-ci) /
  [run 31625898551](https://github.com/progresshans/godj/actions/runs/31625898551)의 고유 attempt-1 exact
  26/26 jobs·326/326 steps와 audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Product는 12 sets/127
  contracts=`122+5+0`으로 계속 불변이고 Phase A는 hosted-verified됐습니다.
- Phase B exact implementation head `c2ecb292dca2daa8d48e9a11fbf49a3f5c4b8a6a`, tree
  `c114812fb89bffdf8e97be1779fd603209700205`는
  [EVID-088](../docs/status/TEST_EVIDENCE.md#evid-20260813-088--gdj-0035-phase-b-github-hosted-no-product-feasibility-exact-head-ci) /
  [run 31653237691](https://github.com/progresshans/godj/actions/runs/31653237691)의 고유 attempt-1 exact
  26/26 jobs·342/342 steps와 independent hosted audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Product/ADR
  상태는 불변이고 Phase B는 no-product feasibility 범위에서 hosted-verified됐습니다.
- 이 문서만 `active`이고 `ready`는 0입니다. Draft PR #1은 open/draft/unmerged이며 사용자 요청 전 merge하지 않습니다.

## 보존해야 하는 legacy 불변 조건

1. Existing definition profile `(definition_format=1, loader_abi=1, operation_codec=1, schema_ir=2)`는
   byte-compatible하게 계속 허용합니다.
2. Legacy-only definition set의 canonical domain/digest v1과 old canonical bytes는 그대로 유지합니다.
3. `migrations.StateFormatVersion=1` scalar state와 기존 CreateModel/AddField lifecycle 의미를 보존합니다.
4. Existing migration manifests/oracles/static fixtures, Q-010/Q-012 `Partial`, Q-013 `Partial`, Q-017/Q-019
   P1/open과 all relation product status를 그대로 둡니다.
5. Unsupported relation data를 legacy tuple/state로 조용히 낮추거나 current generated model에서 복구하지 않습니다.

## Proposed candidate architecture

### Definition profile와 mixed set

- Relation profile 후보는 exact tuple `(1,2,2,3)`입니다. `definition_format=1` envelope는 유지하되
  loader ABI v2가 per-document profile dispatch와 mixed set을, operation codec v2가 relation field arm을,
  Schema IR v3가 normalized ForeignKey 의미를 소유합니다.
- Loader는 legacy `(1,1,1,2)`와 relation `(1,2,2,3)` document를 한 batch에서 받되 각 document를 선언된
  profile로만 decode합니다. 중간 hybrid tuple은 coordinate별 incompatibility로 pre-publication 실패합니다.
- Legacy-only set은 digest domain v1을 보존합니다. Relation document가 하나라도 있는 relation-only/mixed set은
  domain v2를 사용하고 각 canonical definition에 profile을 포함해 동일 migration 의미라도 profile 차이가
  digest에 반영되도록 하는 후보입니다. Old definition을 rewrite하지 않습니다.

### Historical state와 preflight

- `StateFormatVersion=1`은 scalar-only로 유지하고 새 `RelationStateFormatVersion=2`가 IR v3 relation-bearing
  historical state를 소유하는 후보입니다.
- Explicit promotion은 scalar v1을 relation state v2로 losslessly 옮기고, demotion은 relation arm이 하나라도
  있으면 거부하며 scalar-only v2만 v1로 losslessly 되돌립니다.
- 전체 plan의 pure preflight는 DB I/O 전에 source/target app·model, target AutoField PK, declared
  table/column/reverse namespace, `SET_NULL` nullability와 creator-ancestry/dependency를 검증합니다. Target table을
  만드는 migration이 source보다 앞서 dependency ancestry에 있지 않으면 fail-closed합니다.
- Cross-app dependency는 explicit migration identity로 표현합니다. Implicit discovery/order나 current runtime
  binding으로 historical dependency를 보충하지 않습니다.

### SQLite lifecycle와 physical policy

- Existing scalar `migrations/backend.SchemaEditor`를 깨지 않고 optional relation editor capability를 additive하게
  검출하는 후보입니다. Pure relation/state/plan validation과 capability selection은 pinned connection/session과
  transaction `BEGIN` 전에 끝나야 합니다.
- SQLite connection은 migration transaction이 사용하는 exact physical connection에서
  transaction `BEGIN` 전에 `PRAGMA foreign_keys=1`을 확인합니다. 다른 pooled connection의 PRAGMA 결과를
  재사용하지 않습니다.
- `PROTECT`와 `SET_NULL` 모두 physical FK action은 `ON DELETE NO ACTION`; application-level delete policy와
  database constraint action을 동일시하지 않습니다.
- Relation-bearing CreateModel, empty/non-empty table의 nullable ForeignKey AddField, reverse remove, unapply,
  reapply와 file reopen restart를 검증합니다. Populated table에 required ForeignKey AddField는 backfill/default
  policy 없이 pre-DDL structured error로 거부합니다.
- SQLite FK removal은 bounded table remake 후보를 사용합니다. Exact declared table/column/PK/FK shape,
  user rows와 `sqlite_sequence`를 보존하고 inbound FK, index/trigger/view 또는 packet 밖 schema object를 만나면
  임의 재생성 없이 capability error로 거부합니다.
- Exact pinned connection의 physical preflight는 `sqlite_schema`, `foreign_key_list`, index/trigger/view와
  inbound FK를 읽는 DB I/O이며, `BEGIN IMMEDIATE` 뒤 첫 DDL/recorder write 전에 끝나야 합니다. 이 검사는
  pure zero-I/O preflight와 별도입니다.
- Precommit DDL/recorder/revision fault는 same-transaction rollback과 original cause를 보존합니다. Commit은
  success/definite-failure/unknown-outcome 세 결과를 그대로 전달하고 automatic retry를 하지 않습니다.

Phase A의 pinned Django 관찰은 이 GoDj 후보와 다른 경계를 보였습니다. SQLite `AddField` schema-editor가
끝난 뒤 recorder fault를 주입하면 schema는 commit되고 migration record만 없었으며, pre-DDL fault만
완전 rollback되었습니다. MIG-085는 이 Django-observed boundary와 GoDj same-transaction proposal을 분리해
고정하며 두 동작이 같다고 주장하지 않습니다.

## MIG-075..086 locked Phase A reference contracts

| ID | 관찰 경계 | Phase A reference 상태 |
|---|---|---|
| MIG-075 | Legacy tuple/codec/state/digest ABI byte preservation | `oracle_locked` / reference-only |
| MIG-076 | Exact relation profile, coordinate mismatch와 hybrid tuple rejection | `oracle_locked` / proposal reference |
| MIG-077 | Relation-only/mixed set canonical order, per-document profile와 digest-v2 semantics | `oracle_locked` / proposal reference |
| MIG-078 | Scalar v1 ↔ relation v2 explicit promote/demote와 alias-free state | `oracle_locked` / proposal reference |
| MIG-079 | Target AutoField/table/reverse/creator-ancestry structural preflight, DB I/O 0 | `oracle_locked` / proposal reference |
| MIG-080 | Relation-bearing CreateModel apply/unapply/reapply | `oracle_locked` / Django observed |
| MIG-081 | Populated-table nullable AddField success와 required AddField rejection | `oracle_locked` / observed + proposal separation |
| MIG-082 | FK removal table remake의 row/order/sequence preservation | `oracle_locked` / Django observed |
| MIG-083 | Exact pinned connection FK-on과 physical `NO ACTION` | `oracle_locked` / observed + proposal separation |
| MIG-084 | File-backed close/reopen restart와 applied-state reconstruction | `oracle_locked` / Django observed |
| MIG-085 | Pre-DDL full rollback, recorder-fault committed-schema boundary, GoDj atomic proposal | `oracle_locked` / observed + proposal separation |
| MIG-086 | Commit success/definite failure/unknown outcome, no automatic retry | `oracle_locked` / proposal reference |

이 12개 ID만 현재 고정합니다. Manifest/oracle/static fixture/checksum은 각각 7,792/125,248/
1,846/1,245 bytes이며 exact SHA-256은 EVID-085에 기록했습니다.

## 실행 단계

### A. Django-first reference와 immutable artifacts

- 상태: **completed and hosted-verified**. EVID-085 local proof와 EVID-086 unique exact-head hosted proof를 가집니다.
- [x] Pinned Django 6.1/SQLite exact profile에서 MIG-075..086의 외부 state/constraint/rows/sequence/failure를 관찰
- [x] Independent manifest, reference oracle, ordered static not-implemented fixture와 provenance를 생성
- [x] Existing artifacts, `SHA256SUMS`, 12/127/132 product/reference baseline와 product `122+5+0`의 불변을 검증
- [x] Exact artifact byte/count/hash와 test inventory를 final Phase-A bytes에서 측정
- [x] GoDj-owned unaccepted candidate payload는 provenance `kind=proposal`, decision ID `GDJ-0035`,
      `derived=false`로 분리하고 Django BSD source/test reference는 실제 관찰한 부분에만 기록

### B. No-product feasibility

- 상태: **completed and hosted-verified**. Exact 14개 `_test.go`의 isolated candidate만 추가했고 product
  source와 Accepted decision은 만들지 않았습니다. EVID-087 local proof와 EVID-088 unique exact-head hosted
  proof를 가집니다.
- [x] Tuple/profile dispatch, mixed digest, state promote/demote와 deterministic preflight candidate를 product 밖에서 검증
- [x] `conformance/migrationrelation/**`에서 SQLite pinned-connection FK-on, `NO ACTION`, nullable AddField와
  bounded remake를 product 밖 isolated spike로 검증
- [x] Precommit/commit-three-outcome/restart fault matrix와 no-retry invariant를 검증

### C. Decision freeze

- [ ] Phase A/B 결과로 ADR-0034 candidate를 수정하고 public/optional backend/error/digest/state shape를 동결
- [ ] P0/P1/P2/P3 audit 뒤에만 ADR 상태를 Accepted로 바꾸는 별도 decision head를 만듦

### D. Bounded implementation

- [ ] Existing legacy profile/digest/state/lifecycle bytes와 behavior를 보존하며 relation profile/state/codec 구현
- [ ] Pure structural/capability zero-I/O preflight 뒤 pinned physical preflight와 additive relation editor,
  SQLite lifecycle/remake 구현
- [ ] Actual GoDj adapter가 expected fixture replay 없이 MIG-075..086 observation을 생성

### E. 검증과 evidence heads

- [x] Exact 16-document activation head의 고유 hosted CI와 independent audit — EVID-084/run `31618469072`
- [x] Phase A reference-only artifact/local gates — EVID-085; 20/20 focused, 216/216 exact, 725/725 Go roster
- [x] Phase A exact committed head의 고유 hosted CI와 independent audit — EVID-086/run `31625898551`;
      26/26 jobs·326/326 steps, annotations 0, four-coordinate 725/725/0, audit P0..P3=0
- [x] Local normal/race/CGO-disabled/vet/shuffle-20, SQLite/file restart/fault, protocol, no-rewrite와 root CI gates — EVID-087
- [x] Phase B exact implementation head의 고유 hosted four-coordinate CI와 independent audit — EVID-088/run
      `31653237691`; 26/26 jobs·342/342 steps, annotations 0, four SQLite coordinates 75/75/0, audit P0..P3=0
- [ ] Phase D product implementation, completion-documentation, terminal evidence/status를 서로 다른 exact-head hosted CI로 검증
- [x] CURRENT/MATRIX/TEST_EVIDENCE/work를 실제 local 상태에 맞춰 갱신; ADR-0034는 의도적으로 Proposed 유지

## 명시적 비목표와 금지 경계

- Migration writer/autodetector, `makemigrations`, CLI upgrade/downgrade/repair 또는 definition cache
- Data/custom/RunSQL operation, executable callback, arbitrary backfill/default generation
- Self/cyclic ForeignKey, inbound FK remake, `to_field`, non-AutoField/composite PK, OneToOne/ManyToMany
- Arbitrary index/trigger/view/generated column preservation 또는 general-purpose SQLite schema rewriter
- Existing relation query/ORM/codegen/public facade 변경; `schema/**`, `codegen/**`, `orm/**`, `query/**` 수정
- `go.mod`, `go.sum`, existing relation manifest/oracle/static fixture/checksum 변경
- PostgreSQL/MySQL/Windows와 broader non-SQLite migration backend
- Q-010/Q-012/Q-013 close, Q-017 facade/general upgrade 또는 Q-019 retained-connection policy 결정
- Draft PR merge

## 위험과 rollback

- Profile/digest drift는 old migration identity를 바꿀 수 있으므로 legacy-only byte lock 실패 시 구현을 진행하지 않습니다.
- Table remake는 row/sequence 또는 undeclared schema object를 잃을 수 있으므로 bounded recognized shape 밖은 fail-closed합니다.
- FK PRAGMA는 connection-local이므로 exact pinned connection 증거가 없으면 capability를 주장하지 않습니다.
- Unknown commit outcome을 retry하면 duplicate schema/record mutation 위험이 있으므로 original outcome을 보존합니다.
- Activation은 Markdown-only입니다. 되돌릴 때 exact 16 activation paths만 원복하며 baseline source/product는 건드리지 않습니다.

## 다음 정확한 작업

Phase A reference-only artifact/local gate는 EVID-085에서 고정했고 exact committed head `84e16bf...`는
EVID-086/run `31625898551`의 고유 exact 26/26 jobs·326/326 steps와 audit P0..P3=0을 통과했습니다. Phase B의
product 밖 tuple/profile dispatch, mixed digest, state promotion/preflight와 isolated SQLite pinned-connection/
remake/fault feasibility는 exact 14개 `_test.go`에 구현됐고 exact head `c2ecb292...`는 EVID-088/run
`31653237691`의 고유 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다. Phase B는 completed and
hosted-verified입니다. 다음 정확한 작업은 Phase C에서 ADR-0034와 actual product integration shape를 동결하는
것입니다. Product source 구현과 ADR Accepted 전환은 별도 decision head 전까지 금지합니다.

## 결과와 인수인계

현재 검증된 결과는 activation hosted proof, Phase A local/hosted reference proof와 EVID-087/088의 Phase B
local/hosted no-product feasibility proof입니다. Exact 14 test files는 693,557 bytes/`ca579837...09e5`, top-level
inventory는 75/75/0·9,736 bytes·`48e7beb1...92ec`이며 final local normal/race/CGO0/vet/shuffle20/protocol,
pinned exact Python 216/216+13 oracle checks+13 checksums, root `make ci`와 두 independent local audit P0..P3=0을
통과했습니다. Exact Phase B head는 unique hosted 26/26 jobs·342/342 steps, four SQLite coordinates 각각
75/75/0과 independent hosted audit P0..P3=0도 통과했습니다.
Reference는 exact 13 set/139 contract/156 ordered cross-binding=`122 passing + 5 deviation + 12 oracle_locked`지만
product는 12/127=`122 passing + 5 deviation + 0 oracle_locked`로 불변입니다. Product source/artifact/manifest/
oracle/NI/Makefile은 바뀌지 않았고 새 digest/state/DDL product behavior는 없으며 ADR-0034는 Proposed입니다.
Actual SQLite product optional relation port와 actual `StateReconstructor` relation state는 Phase C blocker입니다.
Candidate-local restart는 product epoch/DAG/reconstructor evidence가 아닙니다. EVID-088 append/status tree 자체는
tested Phase B head보다 늦으므로 재귀적으로 hosted-verified되지 않았습니다. Allowed path 이름을 바꿔야 하면 source를
만들기 전에 이 frontmatter를 먼저 수정하고 통합 담당자가 scope를 다시 승인합니다.
