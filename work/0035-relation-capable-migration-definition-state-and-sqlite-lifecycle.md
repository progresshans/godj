---
id: GDJ-0035
status: active
updated: 2026-08-20
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
  - "conformance/fixtures/godj-migration-relation-deviation-expected.json"
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
  - "conformance/cmd/godjcheck/deviation_policy.go"
  - "conformance/cmd/godjcheck/main.go"
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
  - "migrations/internal/definitionhandoff/**"
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
  - "docs/DEVIATIONS.md"
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

이 문서는 그 목표를 검증하기 위한 contract-first 활성 packet입니다. 아래 format/state/DDL shape는 Phase C
test-only proof와 Proposed decision-freeze docs head의 별도 local/hosted proof를 거쳐
[ADR-0034](../docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)의 bounded
decision으로 **Accepted**됐습니다. 이는 제품 구현, product contract `passing`, backend support 또는 제품
**Verified**를 자동으로 뜻하지 않습니다. 현재 checkout은 후속 Phase D1/D2/D3a/D3b bounded slices,
D4 existing-path restart, D4d sealed same-target nullable ForeignKey Add와 D4e empty-source required ForeignKey
Add, D4f bounded ForeignKey Remove-by-remake를 구현·검증했습니다. Populated required Add/reapply,
arbitrary/general remake, general restart와 MIG product adapter/status 전환은 아직 완료하지 않았습니다.

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
- Phase C test-only decision-proof head `7d36502f104daa62b39744b5705478acc19a7ead`, tree
  `d9e8a6b7bec59828ba0bd2b1864cbba3d9f9396d`는 exact 8개 `_test.go`만 수정했고
  [EVID-090](../docs/status/TEST_EVIDENCE.md#evid-20260819-090--gdj-0035-phase-c-test-only-decision-proof-exact-head-hosted-ci) /
  [run 32174259324](https://github.com/progresshans/godj/actions/runs/32174259324)의 고유 attempt-1 exact
  26/26 jobs·342/342 steps, annotations 0과 audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Product/ADR
  상태는 불변입니다.
- Phase D1 product head `42aa9a90db01c548923b443a82ffb8682d4ce9c0`과 inventory correction head
  `f22a4983a200570902daaa921a8e96d144c95d07`은 definition profile/codec/digest와 module-private handoff를
  구현했습니다. [EVID-093](../docs/status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification) /
  [run 32195313382](https://github.com/progresshans/godj/actions/runs/32195313382)의 exact 26/26 jobs·342/342
  steps와 audit P0..P3=0을 통과했습니다.
- Phase D2 product head `ec8877e08b0b196787ef161eb65f6987493e0ba0`과 correction head
  `80776b5b82effd7cf9892839400b6c6624aef845`는 private relation state/reconstructor/readiness를
  구현했습니다. EVID-093/[run 32205324145](https://github.com/progresshans/godj/actions/runs/32205324145)의
  exact 26/26·342/342와 audit P0..P3=0을 통과했습니다. 그 당시 valid relation graph의 D3b 전
  pre-session `relation_migration` Unsupported 경계는 EVID-093의 historical checkout 사실로 보존합니다.
- Phase D3a product head `2eafde10656a7f819fe5685c8ddf7d63a09f839a`과 correction head
  `ce58c5e1975e9e21d9c3ee6ed901302d5ce31bc7`는 exact optional backend API와 direct SQLite
  relation-bearing Create/Delete port를 구현했습니다. EVID-093/
  [run 32218003207](https://github.com/progresshans/godj/actions/runs/32218003207)의 exact 26/26·342/342와
  audit P0..P3=0을 통과했습니다. 이는 direct optional port만의 증거이며 core support가 아닙니다.
- Phase D3b product head `74c2b7241aca3448f999d84e625fc9233434d977`과 inventory correction head
  `167ef0335fcdbcafadecaacf301e6a33671d2ee3`는 normal loaded relation core를 exact-one history/actual
  Planner/whole-plan dry validation/conditional capability와 D3a port에 연결했습니다.
  [EVID-094](../docs/status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification) /
  [run 32231149900](https://github.com/progresshans/godj/actions/runs/32231149900)의 exact 26/26·342/342와
  audit P0..P3=0을 통과했습니다. 이는 normal loaded Create/Delete apply/unapply/reapply만의 증거이며
  Add/Remove/remake와 file restart를 포함하지 않습니다.
- Phase D4 restart verification head `424ec4d80684c07e8d961d858909e394ac8de9a9`, tree
  `6f43ae7b902ceaa82d32ea719431c9dd8fabf96a`는 exact one `_test.go`만 바꾸고 existing product path를
  disposable file-backed SQLite에서 close/reopen마다 fresh Backend/loaded mixed set으로 다시 실행했습니다.
  [EVID-095](../docs/status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification) /
  [run 32248885053](https://github.com/progresshans/godj/actions/runs/32248885053)의 exact 26/26·342/342를
  통과했습니다. Product source/API/workflow와 inventory lock은 불변입니다.
- D4b/D4c를 기록한 EVID-096 exact-six documentation head
  `62df9b2ca3bb397ec826d07b2840408544231845`는 unique
  [run 32260744096](https://github.com/progresshans/godj/actions/runs/32260744096)의 exact
  26/26·342/342와 audit P0..P3=0을 통과했습니다.
- D4d product `3950d98f10544ed18821c1af7960eb1696384eb4`와 inventory lock
  `28b141e023d5e851e25e6560fc21a463982bf1be` 뒤 첫 run `32267789056`은 macOS Intel race의
  wall-clock assertion P1로 25/26 jobs에서 실패했습니다. Deterministic scan-count fix
  `dd8336296afec1c05f739817c7ab77bdb63a2535`는 distinct
  [run 32271361724](https://github.com/progresshans/godj/actions/runs/32271361724)의 exact
  26/26·342/342와 audit P0..P3=0을 통과했습니다. EVID-097은 이 bounded D4d 결과를 기록합니다.
- EVID-097 documentation head `c59669c6fd436b243e96eaf72256535454b705ed`는 unique
  [run 32278555810](https://github.com/progresshans/godj/actions/runs/32278555810)의 exact
  26/26·342/342와 audit P0..P3=0에서 별도로 닫혔습니다.
- D4e product `7c07805918dd680bfd5f85440d71aa14825972b6`와 inventory lock
  `1d86f6e921ec57403980423b83efc17a248a3864`는
  [EVID-098](../docs/status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
  [run 32282269755](https://github.com/progresshans/godj/actions/runs/32282269755)의 exact
  26/26·342/342와 audit P0..P3=0을 통과했습니다. EVID-098은 bounded empty-source D4e 결과를 기록합니다.
- EVID-098 documentation head `85f92704ded6b9d6bd7da32b3fcff12fe747f74b`는 unique CI #94
  [run 32288383027](https://github.com/progresshans/godj/actions/runs/32288383027)의 exact
  26/26·342/342와 audit P0..P3=0에서 별도로 닫혔습니다.
- D4f product `4982e27437b575cf202b55e7ce8c01fd56a94c9c`와 inventory lock
  `9d5b894643f3394974c91a1127534b219840e0a1`은
  [EVID-099](../docs/status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
  unique CI #95 [run 32294983953](https://github.com/progresshans/godj/actions/runs/32294983953)의 exact
  26/26·342/342와 audit P0..P3=0을 통과했습니다. EVID-099은 bounded D4f 결과를 기록합니다.
- D4g Phase 0 observer-only head `b80f06a5a0699dc08278e841087150fe2b232ce2`, tree
  `d8f56993f6a85e84e9a5f3745d577e29d8a1fa20`는 MIG-075..086을 registry에 등록하지 않고 locked-only
  `CharacterizeMigrationRelation`으로 actual typed facts를 수집했습니다. Unique CI #97
  [run 32310167590](https://github.com/progresshans/godj/actions/runs/32310167590)은 이 exact observer-only head에서
  success였습니다. 서로 다른 fresh process의 repo-external O_EXCL capture 두 개는 각각 624,739 bytes,
  SHA-256 `0679a54035605ab9e8b94dec2b9729e4b699c6a96cf20dc694282dec528dffb3`로 exact 동일했습니다.
  Frozen inventory는 845 tests/86,738 bytes/SHA-256
  `9bb0ef63e521749b256bbce1348c9e71bd7628e01306abe00dc546352ab733f3`입니다. Normal `Generate`, status와
  registry는 불변이고 MIG-075..086은 모두 `oracle_locked`/unregistered입니다. CI #97은 later comparison,
  deviation acceptance 또는 status transition을 증명하지 않습니다.
- 이 문서만 `active`이고 `ready`는 0입니다. Draft PR #1은 open/draft/unmerged이며 사용자 요청 전 merge하지 않습니다.

## 보존해야 하는 legacy 불변 조건

1. Existing definition profile `(definition_format=1, loader_abi=1, operation_codec=1, schema_ir=2)`는
   byte-compatible하게 계속 허용합니다.
2. Legacy-only definition set의 canonical domain/digest v1과 old canonical bytes는 그대로 유지합니다.
3. `migrations.StateFormatVersion=1` scalar state와 기존 CreateModel/AddField lifecycle 의미를 보존합니다.
4. Existing migration manifests/oracles/static fixtures, Q-010/Q-012 `Partial`, Q-013 `Partial`, Q-017/Q-019
   P1/open과 all relation product status를 그대로 둡니다.
5. Unsupported relation data를 legacy tuple/state로 조용히 낮추거나 current generated model에서 복구하지 않습니다.

## Accepted frozen architecture

### Definition profile와 mixed set

- Relation profile의 Accepted exact tuple은 `(1,2,2,3)`입니다. Existing `migrations/definition` public
  constants/constructors는 보존하고 같은 package에 additive `RelationLoaderABIVersion=2`,
  `RelationOperationCodecVersion=2`, `RelationSchemaIRVersion=3`을 둡니다.
  `definition_format=1` envelope는 유지하되
  loader ABI v2가 per-document profile dispatch와 mixed set을, operation codec v2가 relation field arm을,
  Schema IR v3가 normalized ForeignKey 의미를 소유합니다.
- Existing public `definition.Load` 하나는 legacy `(1,1,1,2)`와 relation `(1,2,2,3)` document를 한 batch에서
  받고 각 document를 선언 profile로만 decode한 뒤 combined set을 actual `migrations.Planner` 하나로 검증합니다.
  중간 hybrid tuple은 새 hybrid code 없이 coordinate별 existing incompatibility로 pre-publication 실패합니다.
- Legacy-only set은 digest domain v1을 보존합니다. Relation document가 하나라도 있는 relation-only/mixed set은
  domain v2를 사용하고 각 canonical definition에 profile을 포함해 동일 migration 의미라도 profile 차이가
  digest에 반영하도록 결정했습니다. Old definition을 rewrite하지 않습니다.
- Public signature나 entrypoint를 추가하지 않고 implemented `migrations/internal/definitionhandoff.Handoff`를
  module-private immutable carrier로 사용합니다. `definition.Load`는 tuple/decode와 combined actual Planner가
  모두 성공한 뒤 exact per-migration profile, cloned `SourceID`/`Producer`, canonical definition seal, set v1/v2
  digest와 sorted full-graph seal을 carrier에 넣습니다. Raw document bytes나 caller alias는 보존하지 않습니다.
- `definition.Set`의 unexported handoff field는 relation-only/mixed set에만 채우고 legacy-only/empty set은 zero를
  유지합니다. Public `Load`/`Set`/`Definitions`/`Digest`/`Sources`/`Set.Migrate`/`Executor.Migrate` signatures와
  새 public entrypoint 수는 바뀌지 않습니다. Internal package의 exported `Handoff`/`WithContext` identifier는
  Go `internal` import boundary 안의 module-private 이름이며 consumer API가 아닙니다.

### Historical state와 preflight

- `migrations.StateFormatVersion=1` constants/constructors는 scalar-only로 유지하고 같은 package의 additive
  `RelationStateFormatVersion=2`가 IR v3 relation-bearing historical state를 소유합니다. Promotion/demotion helper는
  unexported이고 whole migration-step 경계에서만 실행합니다.
- Relation wire arm은 symbolic target app/model, cardinality/reverse/on_delete를 소유하고 `target_field`는 없습니다.
  Operation 시점 historical target model의 exact one non-nullable AutoField PK를 backend intent로 파생합니다.
- Preflight는 (1) request/resource/carrier/profile/digest/graph/chronology/readiness static zero-I/O,
  (2) existing fenced session의 exact-one history로 actual Planner를 실행한 후 전체 plan dry validation,
  (3) SQLite transaction physical의 세 stage입니다. Relation capability는 2번이 성공한 후 every
  begin/mutation 전에만 검증하고 scalar-only/no-op actual plan은 relation call 0입니다. 하나의
  relation step이라도 unsupported면 scalar prefix를 begin/commit하지 않습니다. Target table creator가
  source보다 앞선 dependency ancestry에 없거나 actual plan/history가 일치하지 않으면 fail-closed합니다.
- Cross-app dependency는 explicit migration identity로 표현합니다. Implicit discovery/order나 current runtime
  binding으로 historical dependency를 보충하지 않습니다.
- Relation-only/mixed `Set.Migrate`는 nonnil context에 fresh-cloned carrier를
  `definitionhandoff.WithContext`의 typed unexported key로 붙이고 existing `Executor.Migrate`를 정확히 한 번
  호출합니다. Executor는 context nil/cancellation/deadline/value precedence 뒤, backend/session/DB I/O 전에
  caller-visible definition clone과 carrier의 exact profile/provenance/digest/definition/full-graph seal을 동기
  검증하고 retain하지 않습니다. 이 검증 경계만 private loaded-state reconstructor와 prepared lifecycle handoff를
  mint합니다.
- Relation support는 구현 뒤에도 normal loaded-set `definition.Load` → `definition.Set.Migrate` →
  `Executor.Migrate` 경로만 소유합니다. Carrier 없는 relation-bearing raw `Executor.Migrate`, `Set.Definitions`
  copy, direct `Executor.Apply`/`Executor.Unapply`/`ExecutePlan`은 legacy `BeginMigration` 전에
  `CategoryCapability`/`CodeUnsupported` feature `relation_migration`으로 실패합니다. Raw legacy
  `Executor.Migrate`와 public `NewStateReconstructor` scalar behavior는 보존하고 public reconstructor의 raw
  relation input은 계속 `CategoryState`/`CodeInvalidState`입니다.

### SQLite lifecycle와 physical policy

- Existing scalar fenced port를 깨지 않고 implemented `migrations/backend/relation.go`의 additive
  `RelationRevisionFencedBackend`/`RelationRevisionFencedSession`, exact four capabilities와 public
  intent/operation/target/kind shape를 사용합니다. Begin은 existing `RevisionFencedTransaction` 하나를 반환하고
  legacy begin fallback은 없습니다.
- SQLite connection은 migration transaction이 사용하는 exact physical connection에서
  transaction `BEGIN` 전에 `PRAGMA foreign_keys=1`을 확인합니다. 다른 pooled connection의 PRAGMA 결과를
  재사용하지 않습니다.
- `PROTECT`와 `SET_NULL` 모두 physical FK action은 `ON DELETE NO ACTION`; application-level delete policy와
  database constraint action을 동일시하지 않습니다.
- Relation-bearing CreateModel, empty/non-empty table의 nullable ForeignKey AddField, empty table required AddField,
  reverse remove, unapply, reapply와 file reopen restart shape를 검증합니다. Populated table required AddField는
  backfill/default policy 없이 pre-DDL structured error로 거부합니다.
- SQLite FK removal은 bounded table remake 후보를 사용합니다. Exact declared table/column/PK/FK shape,
  user rows와 `sqlite_sequence`를 보존합니다. Remake source를 참조하는 inbound FK, source의 non-PK
  index, touched/control table을 소유하거나 참조하는 trigger/view, relevant table의 generated/hidden
  column이나 unsupported option, namespace/temp/control collision은 임의 재생성 없이 capability error로
  거부하지만 unrelated harmless schema object는 허용합니다.
- Exact order는 `PRAGMA foreign_keys=1` → `BEGIN IMMEDIATE` → physical preflight → expected
  epoch/revision/history claim → DDL/remake → `foreign_key_check` → recorder/successor revision →
  `CommitFenced` once입니다. Physical preflight는 DB I/O이며 static zero-I/O preflight와 별도입니다.
- Precommit DDL/recorder/revision fault는 same-transaction rollback과 original cause를 보존합니다. Commit은
  success/definite-failure/unknown-outcome 세 결과를 그대로 전달하고 automatic retry를 하지 않습니다.
- D3a 당시 SQLite capability는 CreateModel FK만 true였습니다. D4d 이후 tuple은
  `{CreateModelForeignKeys:true, AddNullableForeignKey:true,
  AddRequiredForeignKeyToEmptyTable:false, RemoveForeignKeyByTableRemake:false}`였고 D4e 이후 current tuple은
  `{CreateModelForeignKeys:true, AddNullableForeignKey:true,
  AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:false}`였으며 D4f 이후 current tuple은
  `{CreateModelForeignKeys:true, AddNullableForeignKey:true,
  AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:true}`입니다. Relation Add/Remove는 public
  changed-field target 하나를 유지하면서 pre-existing source relation이 모두 같은 symbolic target이고 sealed
  target model이 relation-free인 경우에만 private full target list를 파생합니다. Source model당 step의 nullable/
  required relation mutation을 합쳐 하나입니다. Required Add는 no-default/non-PK/`PROTECT`와 empty source만 허용하며
  existing source emptiness는 pinned BEGIN 뒤 claim 전에 확인하고 same-intent created source는 statically
  empty입니다. Complete relation intent의 zero-target scalar Add/Remove도 같은 transaction에서
  수행할 수 있습니다. `BeginFencedMigration`/`BeginRelationFencedMigration`, global PRAGMA/catalog/
  physical-preflight/claim failure는 step-level `NoOperation`과 existing typed class를, SchemaEditor/final-FK
  failure는 exact operation을 소유합니다.
- D4f backward/unapply 구현은 original exact appended no-default/non-PK ForeignKey만 허용합니다. Nullable은
  `PROTECT` 또는 `SET_NULL`, required는 `PROTECT`여야 하며 public target은 changed field exact 하나입니다.
  Frozen direct E2E fixture는 nullable `PROTECT`와 required `PROTECT`만 검증했으며 dedicated nullable
  `SET_NULL` D4f E2E proof는 주장하지 않습니다. Core dry pass와
  execution rematerialization은 same-symbolic-target relation closure, relation-free exact non-null AutoField target과
  max-one mutation을 pre-capability에 재검증합니다. SQLite는 unique field-order/prefix authority만 받아
  `BEGIN IMMEDIATE` 뒤 claim 전에 remake-source inbound/non-PK-index, touched/control trigger/view, relevant
  generated/hidden/option, sequence와 namespace/temp/control hazards를 fail-closed하지만 unrelated harmless
  object는 허용하고, 같은 fenced transaction에서 deterministic temp, retained-column PK-order copy, row-count/
  sequence/final canonical/FK 검증, recorder/revision과 one commit을 수행합니다.

Phase A의 pinned Django 관찰은 이 GoDj 후보와 다른 경계를 보였습니다. SQLite `AddField` schema-editor가
끝난 뒤 recorder fault를 주입하면 schema는 commit되고 migration record만 없었으며, pre-DDL fault만
완전 rollback되었습니다. MIG-085는 이 Django-observed boundary와 GoDj same-transaction proposal을 분리해
고정하며 두 동작이 같다고 주장하지 않습니다.

## MIG-075..086 Accepted decision/reference matrix

| ID | Accepted decision/reference observation | 현재 분류 |
|---|---|---|
| MIG-075 | Legacy tuple/codec/state/digest ABI byte preservation | `oracle_locked` / reference-only |
| MIG-076 | Exact relation profile, coordinate mismatch와 hybrid tuple rejection | `oracle_locked` / Accepted-decision reference |
| MIG-077 | Relation-only/mixed set canonical order, per-document profile와 digest-v2 semantics | `oracle_locked` / Accepted-decision reference |
| MIG-078 | Scalar v1 ↔ relation v2 explicit promote/demote와 alias-free state | `oracle_locked` / Accepted-decision reference |
| MIG-079 | No wire `target_field`, historical AutoField derivation and static/history-plan/physical three-stage preflight | `oracle_locked` / Accepted-decision reference |
| MIG-080 | Relation-bearing CreateModel apply/unapply/reapply | `oracle_locked` / Django observed |
| MIG-081 | Populated-table nullable success, empty-table required support와 populated-table required rejection | `oracle_locked` / observed + Accepted-decision separation |
| MIG-082 | FK removal table remake의 row/order/sequence preservation | `oracle_locked` / Django observed |
| MIG-083 | Exact pinned connection FK-on과 physical `NO ACTION` | `oracle_locked` / observed + Accepted-decision separation |
| MIG-084 | File-backed close/reopen restart와 applied-state reconstruction | `oracle_locked` / Django observed; bounded product-path scenario Verified, actual MIG adapter/general restart blocked |
| MIG-085 | Pre-DDL full rollback, recorder-fault committed-schema boundary, GoDj atomic proposal | `oracle_locked` / observed + Accepted-decision separation |
| MIG-086 | Commit success/definite failure/unknown outcome, no automatic retry | `oracle_locked` / Accepted-decision reference |

이 12개 ID만 현재 고정합니다. 이 표는 Phase A의 historical observation/proposal과 later Accepted decision을
함께 보여 주며, later decision을 Phase A artifact가 관찰한 사실로 소급하지 않습니다. Checked-in
Phase A manifest/oracle/static fixture/checksum payload의 provenance는 historical `kind=proposal`, decision ID
`GDJ-0035`, `derived=false`로 불변입니다. 각 artifact는 7,792/125,248/1,846/1,245 bytes이며 exact SHA-256은
EVID-085에 기록했습니다.

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

- 상태: **decision Accepted in a separate documentation head; Proposed decision-freeze head hosted-verified by
  EVID-091; acceptance head `7cdc6d6...` exact-hosted-verified by EVID-092/run `32187094845`; bounded
  D1/D2/D3a product sub-slices and D3b core integration are implemented/verified; D4 bounded restart scenario is
  verified; D4b docs and D4c test-only taxonomy heads are hosted-verified; EVID-096 docs proof → bounded
  AddNullable → later capability heads → actual adapter remains open**.
- [x] Phase A/B 결과로 version values, one-loader/digest/state/wire/preflight, exact optional
      backend/error/SQLite order의 behavior를 test-only head `7d36502...`에서 검증하고 EVID-089/090으로 고정;
      이 Accepted decision이 additive public constant/port/type names를 선택하며 test-only proof는 product
      package에서 그 names를 export하지 않음
- [x] Existing `Set.Migrate` metadata-loss 경계를 닫는 module-private `definitionhandoff.Handoff`/context carrier를
      public signature/entrypoint 변경 없이 Accepted decision으로 선택; Phase C test-only head는 이 later
      internal product bridge를 구현하거나 검증하지 않음
- [x] Candidate helper/type/error detail, golden/hash/private catalog는 noncanonical로 분리하고 product
      `StateReconstructor`/SQLite optional port/restart를 blocker로 명시
- [x] Proposed decision-freeze documentation head `5bdf013...`의 unique exact-head hosted CI와 independent audit —
      EVID-091/run `32183309328`
- [x] 그 hosted success를 별도 acceptance documentation head에 기록하고 ADR status를 Accepted로 전환
- [x] Acceptance documentation head `7cdc6d6...`의 unique exact-head hosted CI와 independent audit —
      EVID-092/run `32187094845`; 26/26 jobs·342/342 steps, audit P0..P3=0, EVID-091 재사용 0

### D. Bounded implementation

- [x] **D1**: existing legacy profile/digest/lifecycle bytes와 behavior를 보존하며 relation profile/codec/digest v2와
  `migrations/internal/definitionhandoff` immutable carrier, relation/mixed `Set` private field, context handoff,
  pre-backend Executor seal validation을 구현; exact product `42aa9a9`, inventory correction `f22a498`,
  EVID-093/run `32195313382`
- [x] **D2**: additive `RelationStateFormatVersion=2`, validated carrier-only private historical state/
  reconstructor/readiness, whole-step promotion/demotion, exact creator ancestry/target derivation과 bounded
  planner/replay를 구현; public raw reconstructor/direct entry는 여전히 fail-closed; exact product `ec8877e`,
  correction `80776b5`, EVID-093/run `32205324145`
- [x] **D3a**: Accepted additive backend API와 direct optional SQLite relation-bearing Create/Delete port를
  구현; exact pinned connection, one fenced transaction, physical preflight, `NO ACTION`, final FK check,
  direct-session fault/rollback을 검증; Add/Remove/remake caps false이고 core hookup은 없음; exact product `2eafde1`,
  correction `ce58c5e`, EVID-093/run `32218003207`
- [x] **D3b**: static request/resource/carrier/profile/digest/graph/chronology/readiness → exact-one fenced
  history → fresh actual Planner → whole-plan dry validation → actual-plan relation capability 순서를 core
  `Executor.Migrate`에 새 public API 없이 연결; scalar/no-op relation calls 0, unsupported relation step 시
  scalar partial commit 0, step-global `NoOperation`/operation exact error ownership과 normal loaded SQLite
  Create/Delete apply/unapply/reapply를 검증; product `74c2b72`, correction `167ef03`, EVID-094/run
  `32231149900`
- [x] **D4a**: exact one-test-file head `424ec4d...`에서 disposable SQLite file close/reopen, fresh Backend와
  source-order-permuted mixed `Load`, exact epoch/revision/fingerprint/full history, actual DAG/private state,
  Latest no-op → target child-first unapply → second-restart reapply와 unsupported Add/Remove snapshot 불변을
  bounded captured schema/rows/history/token/FK observation으로 검증; EVID-095/run `32248885053`
- [x] **D4b**: exact 18-document D4 bounded-restart completion-documentation head `84588f9...`의 고유
      hosted proof; EVID-096/run `32252834752`, 26/26·342/342, audit P0..P3=0
- [x] **D4c**: product change 없는 real `definition.Load`→`Set.Migrate`→SQLite test-only taxonomy proof;
      Begin/PRAGMA-set/catalog/claim-busy는 `NoOperation`, final-FK는 operation 1 `AddField`, recorder는
      `NoOperation`; exact one test head `e4fbc7b...`, EVID-096/run `32256113658`, 26/26·342/342,
      audit P0..P3=0; product/API/workflow/capability/status/inventory change 0
- [x] **D4d**: sealed same-target loaded universe의 `AddNullableForeignKey`를 product `3950d98...`, inventory
      lock `28b141e...`, deterministic resource-scan fix `dd83362...`로 구현; public changed-target-only/private
      ordered expansion, native ALTER/mixed canonical SQL, populated rows/sequence, reopen, fault/no-retry와
      resource bounds를 EVID-097/run `32271361724`에서 검증; first run `32267789056` P1도 보존
- [x] **D4e**: `AddRequiredForeignKeyToEmptyTable`을 product `7c07805...`, inventory lock `1d86f6e...`로 구현;
      no-default/non-PK/required `PROTECT`, public changed-target-only/private same-target expansion, pinned-BEGIN
      pre-claim emptiness, same-intent static-empty, native NOT NULL ALTER, populated rejection, fault/reopen을
      EVID-098/run `32282269755`의 D4e final head에서 검증. 별도 run `32278555810`은 prior EVID-097 docs head
      `c59669c...`만 닫았으며 D4e product proof로 재사용하지 않음
- [x] **D4f**: `RemoveForeignKeyByTableRemake`를 product `4982e27...`, inventory lock `9d5b894...`로 구현;
      exact backward authority/public target1/same-target relation-free AutoField/max-one mutation, unique SQLite
      candidate, pre-claim relevant-shape hazards, deterministic temp/PK-order copy/row+sequence preservation,
      fault ownership/rollback/no-retry와 nullable reopen/reapply를 EVID-099/CI #95 run `32294983953`에서 검증.
      별도 CI #94/run `32288383027`은 prior EVID-098 docs head `85f9270...`만 닫았으며 D4f proof로 재사용하지 않음
- [x] **D4g Phase 0 observer-only characterization**: expected fixture/oracle을 읽지 않는 exact head
      `b80f06a...`에서 MIG-075..086 actual typed facts를 locked-only/unregistered 경로로 두 번 결정적으로 수집;
      capture each 624,739 bytes/SHA-256 `0679a540...dffb3`, inventory
      845/86,738/`9bb0ef63...ab733f3`, unique CI #97/run `32310167590` success. Normal `Generate`, status,
      registry와 12개 `oracle_locked` 분류는 불변
- [ ] **D4g explicit comparison/status subphases**: immutable actual을 explicit comparison한 strict 결과는
      0/12 contracts, 0/30 declared dimensions입니다. Actual의 generic `{case,outcomes}`/`{snapshots}`/
      `{loads,trace}` envelope와 contract별 oracle projection 차이가 먼저 드러났으므로 이를 12개 product semantic
      failure로 해석하지 않습니다. P1 수정 항목은 MIG-076 relation case의 required author dependency 누락,
      public `*migrations.PlanningError` typed classification 누락, MIG-080..085 raw SQL statement count/kind actual
      metric 미계측입니다. Projection/metric 보완 뒤 passing 검토 후보는 MIG-075/080/082/084이고, Proposed
      `DEV-0003` 검토 후보는 MIG-076..079/081/083/085/086입니다. `DEV-0003`은 아직 Accepted가 아니며
      deviation이나 status flip을 승인하지 않습니다. Exact order는 observer fixes → contract projections + actual
      metrics → deterministic recapture → sparse DEV review → status/registry transition입니다.

### E. 검증과 evidence heads

- [x] Exact 16-document activation head의 고유 hosted CI와 independent audit — EVID-084/run `31618469072`
- [x] Phase A reference-only artifact/local gates — EVID-085; 20/20 focused, 216/216 exact, 725/725 Go roster
- [x] Phase A exact committed head의 고유 hosted CI와 independent audit — EVID-086/run `31625898551`;
      26/26 jobs·326/326 steps, annotations 0, four-coordinate 725/725/0, audit P0..P3=0
- [x] Local normal/race/CGO-disabled/vet/shuffle-20, SQLite/file restart/fault, protocol, no-rewrite와 root CI gates — EVID-087
- [x] Phase B exact implementation head의 고유 hosted four-coordinate CI와 independent audit — EVID-088/run
      `31653237691`; 26/26 jobs·342/342 steps, annotations 0, four SQLite coordinates 75/75/0, audit P0..P3=0
- [x] Phase C exact 8-test-only decision proof local/hosted gates — EVID-089/090/run `32174259324`; exact
      26/26 jobs·342/342 steps, annotations 0, audit P0..P3=0
- [x] Proposed decision-freeze documentation head를 EVID-091의 고유 exact-head CI/evidence로 검증하고 Phase C
      test-only run을 재사용하지 않음
- [x] Acceptance documentation head를 EVID-092의 별도 고유 exact-head CI/evidence로 검증하고 EVID-091을 재사용하지 않음
- [x] Phase D1/D2/D3a product/correction head를 서로 다른 exact-head hosted CI로 검증 —
      EVID-093/runs `32195313382`, `32205324145`, `32218003207`
- [x] D3b core product/correction head local/hosted verification — EVID-094/run `32231149900`; exact
      26/26 jobs·342/342 steps, audit P0..P3=0
- [x] D4 bounded restart verification head local/hosted proof — EVID-095/run `32248885053`; exact one `_test.go`,
      product source/API/workflow/inventory unchanged, 26/26 jobs·342/342 steps
- [x] D4b bounded-restart completion-documentation head `84588f9...`의 고유 exact-head hosted CI —
      EVID-096/run `32252834752`
- [x] D4c taxonomy test-only head `e4fbc7b...`의 local/unique exact-head hosted CI —
      EVID-096/run `32256113658`
- [x] EVID-096 exact-six documentation head `62df9b2...`의 고유 hosted CI — run `32260744096`
- [x] Bounded `AddNullableForeignKey` final head `dd83362...`의 local/unique exact-head hosted CI —
      EVID-097/run `32271361724`; exact 26/26 jobs·342/342 steps, audit P0..P3=0
- [x] EVID-097 docs head `c59669c...`와 bounded `AddRequiredForeignKeyToEmptyTable` final head `1d86f6e...`의
      각 고유 exact-head hosted CI — EVID-098/runs `32278555810`/`32282269755`; 각 exact 26/26·342/342,
      audit P0..P3=0
- [x] EVID-098 docs head `85f9270...`와 bounded `RemoveForeignKeyByTableRemake` final head `9d5b894...`의
      각 고유 exact-head hosted CI — EVID-099/CI #94/#95 runs `32288383027`/`32294983953`; 각 exact
      26/26·342/342, audit P0..P3=0
- [x] D4g Phase 0 oracle-blind observer-only head `b80f06a...`와 unique CI #97/run `32310167590` success;
      deterministic capture 624,739 bytes/`0679a540...dffb3`, inventory
      845/86,738/`9bb0ef63...ab733f3`, normal Generate/status/registry 불변
- [ ] observer fixes → contract projections + actual metrics → deterministic recapture → sparse DEV review →
      status/registry transition → overall completion-documentation → terminal evidence/status를 서로 다른
      exact-head CI로 검증; Proposed `DEV-0003`을 아직 Accepted로 쓰지 않음
- [x] CURRENT/MATRIX/TEST_EVIDENCE/work를 실제 local 상태에 맞춰 갱신; ADR-0034 bounded design을 별도 head에서 Accepted로 전환

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

Phase A/B/C와 acceptance evidence는 EVID-085..092에 분리돼 있습니다. 이후 D1
`42aa9a9`/`f22a498`, D2 `ec8877e`/`80776b5`, D3a `2eafde1`/`ce58c5e`가 각각 구현됐고
EVID-093의 고유 hosted run에서 검증됐습니다. D3b `74c2b72`/`167ef03`도 EVID-094/run
`32231149900`에서 normal loaded core integration을 구현·검증했습니다. D4a exact one-test-file head
`424ec4d...`도 EVID-095/run `32248885053`에서 file close/reopen마다 fresh loaded set/backend를 사용한
full/branch/full captured-snapshot restart를 검증했습니다. D4b `84588f9...`는 EVID-096/run
`32252834752`에서 exact18 docs head를, D4c `e4fbc7b...`는 EVID-096/run `32256113658`에서
six-case loaded SQLite taxonomy를 각 고유 hosted proof로 검증했습니다. EVID-096 exact-six docs head
`62df9b2...`도 run `32260744096`에서 별도로 닫혔습니다. D4d product `3950d98...`, inventory lock
`28b141e...`와 deterministic scan fix `dd83362...`는 EVID-097/run `32271361724`에서 sealed/resolvable
same-target loaded universe의 `AddNullableForeignKey`를 구현·검증했습니다. Exact capability는
`{true,true,false,false}`였습니다. EVID-097 docs head `c59669c...`는 run `32278555810`에서 별도로 닫혔고,
D4e product `7c07805...`/inventory lock `1d86f6e...`는 EVID-098/run `32282269755`에서 bounded
`AddRequiredForeignKeyToEmptyTable`을 구현·검증했습니다. Exact capability는 `{true,true,true,false}`입니다.
EVID-098 docs head `85f9270...`는 CI #94/run `32288383027`에서 별도로 닫혔고 D4f product
`4982e27...`/inventory lock `9d5b894...`는 EVID-099/CI #95 run `32294983953`에서 bounded
`RemoveForeignKeyByTableRemake`를 구현·검증했습니다. Exact capability는 `{true,true,true,true}`입니다. 다음
Phase 0 observer-only head `b80f06a...`는 unique CI #97/run `32310167590`에서 success였고, locked-only actual
capture 두 개는 각각 624,739 bytes/SHA-256 `0679a540...dffb3`로 exact 동일했습니다. Frozen inventory는
845/86,738/`9bb0ef63...ab733f3`이고 normal Generate/status/registry는 불변입니다. Explicit comparison의 strict
결과는 generic actual projection 때문에 0/12 contracts, 0/30 dimensions이며 이를 곧바로 product semantic
failure로 해석하지 않습니다. 다음 정확한 작업은 MIG-076 dependency와 `PlanningError` classification을 고치고
raw SQL metric을 실제 계측한 뒤 contract projection, deterministic recapture를 순서대로 수행하는 것입니다.
그 뒤 MIG-075/080/082/084 passing 후보와 MIG-076..079/081/083/085/086 Proposed `DEV-0003` 후보를 sparse
DEV review에서 분리합니다. `DEV-0003`은 아직 Accepted가 아니며, review 뒤에만 status/registry를 전환하고
overall completion, terminal 순서로 닫습니다.

## 결과와 인수인계

현재 D1 definition/handoff, D2 private historical state/readiness, D3a direct SQLite Create/Delete optional
port와 D3b normal loaded core integration은 각각 Implemented이고 EVID-093/094의 local/hosted 환경에서
Verified입니다. Normal `Load`→`Set.Migrate` relation-bearing CreateModel은 SQLite에서
apply/unapply/reapply합니다. D4a는 이 기존 product path의 bounded captured-snapshot file restart scenario를
EVID-095 환경에서 Verified했습니다. D4b docs head와 D4c test-only six-case taxonomy head는 EVID-096의
각 hosted 환경에서 Verified했고 exact-six docs head 자체도 run `32260744096`에서 검증됐습니다. D4d
`3950d98...`/`28b141e...`/`dd83362...`는 EVID-097 환경에서 bounded nullable Add를 Implemented/Verified했습니다.
D4e `7c07805...`/`1d86f6e...`는 EVID-098 환경에서 bounded empty-source required Add를 Implemented/Verified했습니다.
D4f `4982e27...`/`9d5b894...`는 EVID-099 환경에서 bounded ForeignKey Remove-by-remake를
Implemented/Verified했습니다. 현재 capability는 `{true,true,true,true}`입니다. Populated required Add/reapply,
arbitrary/general remake, general restart/actual adapter는 미지원입니다. Reference는 exact
13/139/156=`122+5+12 locked`, product contract는
12/127=`122+5+0`으로 불변이고 MIG-075..086은 여전히 `oracle_locked`입니다. D4g Phase 0 exact head
`b80f06a...`/CI #97 run `32310167590`은 observer-only characterization만 증명합니다. Generic projection의
strict 0/12 결과, 세 P1 observer/metric 수정 항목과 Proposed `DEV-0003` 후보 분류는 later status나 deviation을
Accepted로 만들지 않았고 normal Generate/registry도 바꾸지 않았습니다.

EVID-093의 D1/D2/D3a runs는 각 correction head만 증명하고 EVID-094/run `32231149900`은 D3b correction
head `167ef03...`만 증명합니다. EVID-095/run `32248885053`은 D4a test-only head `424ec4d...`만,
run `32252834752`는 D4b docs head `84588f9...`만, run `32256113658`은 D4c test-only head
`e4fbc7b...`만 증명합니다. Run `32260744096`은 EVID-096 docs head `62df9b2...`만, first D4d run
`32267789056`은 P1 실패 head `28b141e...`만, run `32271361724`는 fixed D4d head `dd83362...`만
증명합니다. Runs `32278555810`/`32282269755`는 각각 EVID-097 docs head `c59669c...`/D4e final head
`1d86f6e...`만 증명합니다. Runs `32288383027`/`32294983953`는 각각 EVID-098 docs head `85f9270...`/D4f
final head `9d5b894...`만 증명합니다. 이 EVID-099 documentation mirror, later D4g observer/status/deviation
decision, completion/terminal 또는 MIG status 전환을 재귀적으로 증명하지 않습니다. Public raw
`NewStateReconstructor`의 relation input은 계속 `CategoryState`/`CodeInvalidState`고 carrier-less raw relation
execution은 `CategoryCapability`/`CodeUnsupported`입니다.
CI #97/run `32310167590`도 exact D4g Phase 0 head `b80f06a...`만 증명하며 explicit comparison 결과,
Proposed `DEV-0003`, later sparse deviation review/status/registry transition 또는 completion/terminal proof로
재사용하지 않습니다.
Allowed path 이름을 바꿔야 하면 source를 만들기 전에 이 frontmatter를 먼저 수정하고 통합 담당자가 scope를
다시 승인합니다.
