---
id: GDJ-0021
status: completed
updated: 2026-08-10
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "53729103651bfc34acc5fe07fb4376d5dd78c204"
depends_on: ["GDJ-0020"]
contracts: ["MIG-065..MIG-074", "Q-010", "Q-012"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "conformance/README.md"
  - "conformance/contracts/migration-project-check-manifest.json"
  - "conformance/fixtures/godj-migration-project-check-not-implemented.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/runners/django/runner.py"
  - "conformance/runners/django/migration_project_check_scenarios.py"
  - "conformance/runners/django/tests/test_migration_project_check_scenarios.py"
  - "conformance/runners/django/tests/test_runner_safety.py"
  - "conformance/runners/django/tests/test_scenarios.py"
  - "conformance/projectcheck/**"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/migration_definition_source_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/LICENSING.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/adr/0021-project-linked-migration-check.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0021-migration-project-check-compatibility-contracts.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Migration Project Check Compatibility Contracts

## 사용자에게 보이는 결과

이 contract가 목표로 하는 **향후 제품 사용자 경험 후보**는 프로젝트 안이나 그 하위 디렉터리에서
`godj migrations check`를 실행하거나 `godj migrations check --project <descriptor-file>`로 프로젝트를
명시해, 데이터베이스를 열거나 migration을 실행하지 않고 migration definition catalog가 안전하게
탐색·해석되는지 확인하는 것입니다. 제품화된다면 성공 시 definition/source count와 canonical
definition-set digest를 받고 catalog 문제와 project/config/build/transport 문제를 서로 다른 종료
코드로 구분합니다.

이번 GDJ-0021은 이 경험의 **compatibility/decision contract와 test-only feasibility 경계만**
만듭니다. 전역 `godj` 제품 CLI, project package/API, production project binary command와 실제
filesystem discovery 지원을 구현하지 않습니다. 완료 후에도 제품 상태는 10 adapter/105 contract의
`100 passing + 5 deviation`에 머물며, 새 MIG-065..074는 `oracle_locked`입니다.

## 목표

- MIG-065..074의 열한 번째 manifest/oracle/static fixture와 decision provenance 구축
- exact `godj.toml` project selection, strict descriptor v1과 invalid-nearest fail-closed 의미 고정
- global CLI build/orchestration과 project-linked runner의 역할 및 closed protocol v1 고정
- project-relative flat source discovery, no-follow safety와 deterministic order 고정
- `definition.Load` exactly-once handoff와 DB/lifecycle call 0의 check-only 의미 고정
- public exit `0/1/2/3/130`, runner transport exit 0과 structured failure 전달 규칙 고정
- descriptor/catalog/wire acceptance와 diagnostic retention의 11개 inclusive cap 및 combined-fault
  precedence 고정
- no-shell/private output/`-mod=readonly`/workspace·toolchain policy/cancel·reap 경계 고정
- Existing 2 jobs와 4-leg project-check + 4-leg SQLite matrix의 exact 10 hosted job execution 검증
- 11 reference set, 115 unique contract와 110 ordered cross-binding rejection 검증
- 기존 10 product adapter/105 contract와 `100 passing + 5 deviation` 보존
- 같은 Draft PR [#1](https://github.com/progresshans/godj/pull/1)에만 후속 commit을 쌓고 새 PR을
  만들지 않음

## 비목표와 금지 경계

- `cmd/godj/**`, product `project/**`, `migrations/**`, `migrations/backend/**`, `db/**` 변경
- `conformance/runners/godj/**` actual product adapter 또는 열한 번째 product adapter 추가
- 전역 `godj` 제품 CLI, project-linked production binary, public Go package/type/function 확정
- MIG-065..074를 `passing`/`deviation`으로 바꾸거나 구현됐다고 표현
- 기존 10 manifest/oracle/static/deviation payload 또는 `100 passing + 5 deviation` 변경
- persistent runner build cache, installed project binary lifecycle와 direct production command 표면
- generator/library/CLI 전체 semver negotiation, stale generated code repair와 upgrade command
- recursive/module/embed/remote/watch/glob discovery, Go source registration과 plugin/reflection
- Windows path/process behavior, network/module fetch guarantee와 cross-compiled runner 실행
- writer, format/codec v2+, executable/custom/data/raw-SQL operation과 historical app registry
- DB/applied-history/schema-drift 검사, migration plan/execute, adoption/repair와 crash reconciliation
- PostgreSQL/MySQL/non-SQLite, multi-DB router와 distributed coordination
- Product adapter/contract 없이 PostgreSQL/MySQL service만 띄우는 green-skip hosted job
- 프로젝트의 임의 사용자 `init()` side effect가 없다는 주장
- build/runner wall time, CPU/RSS/address space, goroutine/thread/process count, private binary/HOME/cache/
  temp bytes·inode, module/network transfer와 post-cap pipe drain bytes/time의 hard bound
- sandbox/rlimit/cgroup/quota/hard timeout, Unix SIGTERM/SIGHUP/other fatal signal/SIGKILL 또는
  parent/host crash/power loss 뒤 structured exit·cleanup과 stale-temp scavenging
- caller가 닫거나 고장 낸 stdout/stderr sink에 대한 atomic delivery, rollback 또는 portable exit
- Explicit NETRC/Git/SSH/askpass/helper/agent environment와 OS-account home lookup의 exhaustive isolation,
  module-auth confidentiality 또는 network/auth success
- Static validation 뒤 same-user concurrent temp-base path rebind/rename를 막는 retained-handle fencing

제품 package/API 타입명, root 제공 interface와 CLI internal struct는 GDJ-0022 이후까지 가설입니다.
Contract test-only code가 후보 타입을 사용하더라도 public API나 production package로 승격하지
않습니다.

## 선행 조건과 기준 상태

- Baseline은
  `codex/revision-fenced-migration-lifecycle@53729103651bfc34acc5fe07fb4376d5dd78c204`
  (`docs: record hosted loader completion validation`)입니다. 이 exact baseline은 같은 Draft PR #1의
  [run 31310606332](https://github.com/progresshans/godj/actions/runs/31310606332)에서 Ubuntu/macOS 두
  job이 통과했습니다. 아직 commit되지 않은 GDJ-0021 activation diff의 hosted CI로 재사용하지
  않습니다.
- [GDJ-0020](0020-migration-definition-loader-product-slice.md)과 Accepted
  [ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 explicit caller bytes를
  pure `migrations/definition.Load`로 검증하는 bounded product loader를 완료했습니다.
- 현재 protocol v2는 10 reference set/105 unique contract/90 ordered cross-binding입니다. 10 product
  adapter가 같은 105 contract를 실행하며 분류는 `100 passing + 5 deviation`입니다.
- Exact reference profile은 Django 6.1 tag/commit
  `fe0a859f537d4238cf49fca39073513206f83122`, CPython 3.14.3, SQLite 3.50.4,
  UTC/C locale, macOS arm64입니다.
- Baseline checkout은 clean입니다. 이후 다른 agent나 사용자가 만든 범위 밖 변경은 보존하고,
  stable 문서와 artifact는 한 integration owner만 통합합니다.
- [ADR-0004](../docs/adr/0004-cli-and-project-binary.md)는 global CLI와 project-linked binary의 역할
  분리만 Accepted했습니다. Descriptor, entrypoint, protocol과 exit 의미는 activation 당시 Proposed
  [ADR-0021](../docs/adr/0021-project-linked-migration-check.md) 및 이번 contract로 검증했고, local/
  hosted evidence를 근거로 ADR-0021을 Accepted했습니다. 이는 제품 CLI/API 구현을 뜻하지 않습니다.

## Reference / Provenance

MIG-065..074는 Django result parity가 아닙니다. Django의 `migrate --check`는 DB-aware pending
migration 검사이고, `makemigrations --check`는 model drift 검사이며,
`MigrationLoader.load_disk()`는 Python module import를 사용합니다. 어느 것도 GoDj의
`godj.toml`, JSON flat discovery, runner protocol이나 exit code의 근거로 사용하지 않습니다.

열 개 contract 모두 GoDj의 project-check orchestration 결정을 검증하는 독립 scenario이며 manifest
provenance는 exact `decision/ADR-0021/derived=false`, scenario namespace는
`godj.migration.project_check.*`입니다. 기존 Django-named oracle directory와 runner/profile
namespace를 재사용하는 이유는 protocol v2의 locked reference corpus, checksum과 exact-profile
gate를 한 곳에서 유지하기 위해서일 뿐, Django에서 파생됐다는 뜻이 아닙니다. 새 독립 작성물과
분류는 `NOTICE.md`, `docs/LICENSING.md`, `docs/SOURCES.md`에도 기록합니다.

## Contract set

| ID | Exact scenario slug | Phase | Comparison | Exact base fixture와 outcome |
|---|---|---|---|---|
| MIG-065 | `godj.migration.project_check.nested_project_success` | `environment` | `result`, `metrics` | Nested cwd의 nearer project는 ADR-0019 one-`CreateModel` golden이고 outer project는 empty. Nearest만 선택해 `source_count=1`, `definition_count=1`, digest `sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc`, exit 0 |
| MIG-066 | `godj.migration.project_check.explicit_project_override` | `environment` | `result`, `metrics` | Nearer implicit project는 empty지만 exact trailing `--project <descriptor-file>`이 one-model project를 선택하고 ancestor probe 0. Counts 1/1, 같은 `07e61f8d...` digest, exit 0 |
| MIG-067 | `godj.migration.project_check.empty_catalog` | `construction` | `result`, `metrics` | One existing empty root, source read 0, actual `Load` 1. `source_count=0`, `definition_count=0`, canonical digest `sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`, exit 0 |
| MIG-068 | `godj.migration.project_check.canonical_filesystem_order` | `construction` | `result`, `metrics` | MIG-057 exact two-source/three-operation fixture를 두 roots에 배치. Configured root와 directory enumeration permutation 각각이 같은 SourceID byte order, counts 2/2, digest `sha256:5a73e03d3448f3f19f7646eed67f4e312610f4389f2e3e537c379e725f0b106d`, exit 0 |
| MIG-069 | `godj.migration.project_check.unsafe_source_entry` | `construction` | `error`, `metrics` | Base matching symlink `link.godj.json`은 target을 열지 않고 `migration_definition_discovery_error/unsafe_source_entry`, exit 1. Matching directory/other non-regular는 mutation gate |
| MIG-070 | `godj.migration.project_check.project_not_found` | `environment` | `error`, `metrics` | Base는 filesystem root까지 exact marker 없음: `migration_project_selection_error/project_not_found`, exit 2. 128번째 뒤 parent가 남는 variant는 `project_search_limit_exceeded`, further probe/build/runner 0 |
| MIG-071 | `godj.migration.project_check.project_protocol_incompatible` | `environment` | `error`, `metrics` | Runner transport exit 0, one-value+EOF otherwise-valid success envelope에서 exact `protocol_version` lexeme `2`; global은 dispatch/Load 0, `migration_project_protocol_error/project_protocol_incompatible`, exit 3 |
| MIG-072 | `godj.migration.project_check.project_build_failure_atomic` | `environment` | `error`, `metrics` | Selected `./cmd/broken` main package의 Go syntax error로 build 1회 실패. `migration_project_build_error/project_build_failed`, runner/read/Load 0, private output 미게시·project tree 불변, exit 3 |
| MIG-073 | `godj.migration.project_check.definition_load_failure` | `construction` | `error`, `metrics` | One safe candidate는 MIG-061 accepted duplicate `/migration/name` document. Read 1/actual `Load` 1 뒤 raw `migration_definition_source_error/invalid_definition_document`, failure `stage=document`, pointer `/migration/name`, reason `duplicate_key`, partial result 0, exit 1 |
| MIG-074 | `godj.migration.project_check.invalid_runner_response` | `environment` | `error`, `metrics` | Runner transport exit 0의 base stdout은 duplicate top-level `status`. `migration_project_protocol_error/invalid_project_runner_response`, success/partial result 0, exit 3. Malformed/trailing/non-UTF-8/oversized는 mutation gate |

MIG-071 exact mismatching runner stdout은 다음 bytes 뒤 EOF입니다. Version framing/coordinate만 valid
unsupported value이고 나머지는 supported success shape입니다.

```json
{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"}}
```

MIG-074 exact duplicate base stdout은 다음 bytes 뒤 EOF입니다.

```json
{"protocol_version":1,"status":"ok","status":"error","result":{"source_count":0,"definition_count":0,"definition_set_digest":"sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"}}
```

각 ID는 top-level `result` 또는 `error` 하나만 갖습니다. MIG-065..068은 success result,
MIG-069..074는 error입니다. Base case에 여러 fault를 섞지 않습니다. Exact common metric shape와
base integer value는 다음과 같습니다. `documents_received`부터 `definition_sets_published`까지는 actual
linked `LoadReport`, 나머지 call/publication counter는 instrumented test-only boundary가 유일한
source입니다. MIG-073의 stage/pointer/reason을 포함한 detailed loader/planning context와 모든 counter는
test-only oracle observation이며 runner wire/public API에 없고 global 분류에 사용하지 않습니다.
Feasibility gate는 linked actual context 보존과 end-to-end wire가 detail을 strip하면서 category/code
pair만 보존하는지를 별도로 검증합니다.

Machine oracle의 `metrics` exact field set은 다음 24개뿐이며 unknown/extra field를 거부합니다:
`build_calls`, `runner_calls`, `runner_response_writes`, `source_reads`, `load_calls`,
`documents_received`, `headers_validated`, `operations_decoded`, `planner_construction`,
`definitions_published`, `definition_sets_published`, `direct_planner_calls`, `godj_db_calls`,
`revision_lifecycle_calls`, `user_stdout_writes`, `user_stderr_writes`, `partial_stdout_writes`,
`exit_code`, `command_dispatches`, `ancestor_directories_inspected`, `descriptor_reads`, `roots_opened`,
`directory_entries_seen`, `failure`. 아래 표의 17개 열은 앞의 exact snake_case key 순서와 일치합니다.
`failure`는 모든 base row에 존재하고 MIG-073만 아래 full object이며 success와 Load 0 row는 exact null입니다.

| ID | build | runner | runner response writes | source reads | Load | documents received | headers validated | operations decoded | Load-owned planner | definitions published | sets published | direct orchestration planner | GoDj DB | revision lifecycle | user stdout | user stderr | partial stdout |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| MIG-065 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | 1 | 0 | 0 |
| MIG-066 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | 1 | 0 | 0 |
| MIG-067 | 1 | 1 | 1 | 0 | 1 | 0 | 0 | 0 | 1 | 0 | 1 | 0 | 0 | 0 | 1 | 0 | 0 |
| MIG-068 | 1 | 1 | 1 | 2 | 1 | 2 | 2 | 3 | 1 | 2 | 1 | 0 | 0 | 0 | 1 | 0 | 0 |
| MIG-069 | 1 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| MIG-070 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| MIG-071 | 1 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| MIG-072 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| MIG-073 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| MIG-074 | 1 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |

All base rows also record `exit_code`, `command_dispatches`, `ancestor_directories_inspected`,
`descriptor_reads`, `roots_opened` and `directory_entries_seen`. Fixture-specific exact values are asserted
beside the scenario: MIG-065 dispatch 1/ancestors 4/descriptor 1/root 1/entries 1; MIG-066
1/0/1/1/1; MIG-067 1/1/1/1/0; MIG-068 1/1/1/2/3; MIG-069 1/1/1/1/1;
MIG-070 0/4/0/0/0; MIG-071 0/1/1/0/0; MIG-072 0/1/1/0/0; MIG-073
1/1/1/1/1; MIG-074 0/1/1/0/0. Exit은 위 outcome의 exact 값입니다. Permutation/mutation
variant는 해당 base counter shape와 달라지는 필드만 별도 observation으로 명시합니다.
Graph-stage loader mutation은 `Load=1`, `Load-owned planner=1`이고 direct orchestration planner/DB/
revision lifecycle은 0입니다.

Temp/diagnostic/process scalar는 이 24-field machine oracle에는 존재하지 않고 feasibility harness가
별도로 소유합니다. Base feasibility 값은 MIG-070의
`temp_created/temp_cleanup_attempts/cleanup_failed/residual_temp=0/0/0/0`, 나머지 ID는
`1/1/0/0`; 모든 base는 `group_sigint_attempts=0`, `group_sigkill_attempts=0`,
`direct_child_reaps=build_calls+runner_calls`입니다. Join 뒤 raw diagnostic buffer field는 absent입니다.
Stream별 `retained_bytes`/`truncated`는 host compiler output에 의존하므로 machine oracle에는 없고
harness-only observation입니다. Cancellation, injected cleanup failure와 oversize/cap mutation은 각 test가
관련 feasibility scalar를 exact 값으로 고정합니다.

MIG-068 aggregate `directory_entries_seen` exact 값 3은 candidate 두 개와 ignored noise 한 개를
합한 값입니다(roots opened는 2). Exact discovery fixture mapping은 다음과 같습니다.

| ID | Semantic roots / immediate raw entries | Resulting SourceID handoff |
|---|---|---|
| MIG-065 | selected nearer root `migrations`; `0001_initial.godj.json` | `migrations/0001_initial.godj.json` |
| MIG-066 | explicit project root `migrations`; `0001_initial.godj.json` | `migrations/0001_initial.godj.json` |
| MIG-067 | one existing root `migrations`; empty | empty source slice |
| MIG-068 | configured base roots `["migrations/z","migrations/a"]`; `migrations/z` has `0001_initial.godj.json` and ignored `notes.txt`; `migrations/a` has `0002_fields.godj.json` | sorted `migrations/a/0002_fields.godj.json`, `migrations/z/0001_initial.godj.json` |
| MIG-069 | root `migrations`; matching symlink `link.godj.json` | no Source handoff; unsafe logical path `migrations/link.godj.json` |
| MIG-073 | root `migrations`; `broken.godj.json` containing MIG-061 duplicate name | `migrations/broken.godj.json` |

MIG-068은 configured root order와 `migrations/z`의 candidate/noise enumeration order를 각각 뒤집어
같은 sorted SourceIDs/counts/digest를 관찰합니다. MIG-073 actual `FailureContext`는 exact
`stage="document"`, `source_id="migrations/broken.godj.json"`, `json_pointer="/migration/name"`,
`reason="duplicate_key"`, empty `app`/`name`/`limit`, `operation_index=-1`, `maximum=0`, `actual=0`, empty
`graph_sources`입니다. MIG-065..068 success report의 failure는 absent이고 Load 0인 MIG-069..072/074에는
LoadReport/failure context가 없습니다.

User output 전 normal primary error, success 또는 cancellation outcome을 하나 선택하고 cleanup을
수행합니다. Cleanup 성공 뒤 success는 user stdout에 정확히 한 번, error는 user stdout 0을 유지하고
bounded stderr human message로 정확히 한 번 publish합니다. Error renderer는 category/code에서 만든
한 줄이고 raw path/document/diagnostic을 넣지 않습니다. Message bytes는 contract가 아니고
category/code와 counter가 의미를 고정합니다. Cleanup failure는 선택된 success/`project_interrupted`/
`project_canceled`만 `project_cleanup_failed`로 대체합니다. 이미 선택된 non-cancel primary
(selection/build/runner/catalog/internal 등)는 그대로 한 번 publish하고 cleanup failure는 metric에만
남깁니다. `project_internal_error`는 undefined invariant가 처음 발생한 primary일 때만 선택합니다.
Runner stdout response write는 별도 counter이며 public user stdout과 합치지 않습니다.

## Project selection과 closed descriptor v1

- Accepted contract의 exact public argv grammar는 executable 뒤 exact `migrations check` 또는
  `migrations check --project <descriptor-file>` 두 개뿐입니다. `--project=` form, repeated/missing/
  empty value, unknown/extra argument, flag/subcommand reorder와 flag-before-subcommand는
  `migration_project_command_error/invalid_arguments`이며 project selection 전에 exit 2입니다.
- Marker 이름은 raw UTF-8 bytes가 case-sensitive exact `godj.toml`이어야 합니다. Retained parent
  directory handle에서 entries를 열거해 raw name equality를 먼저 확인한 뒤 그 exact entry를
  no-follow open합니다. 따라서 case-insensitive macOS에서 `GODJ.TOML`만 존재해도 marker로
  인정하지 않으며 wrong-case-only fixture를 Ubuntu/macOS gate에서 검증합니다.
- Exact marker는 `Lstat`/no-follow에서 regular file이어야 합니다. Symlink/directory/non-regular marker는
  `migration_project_selection_error/invalid_project_descriptor`이고 더 바깥 marker로 fallback하지
  않습니다.
- `godj migrations check`는 invocation cwd를 한 번 physical absolute directory로 resolve하고 이를 1로
  세어 physical parent 방향으로 최대 128개 directory를 검사합니다. 128번째까지 marker가 없고
  filesystem root에 도달하면 `project_not_found`; 128번째의 parent가 더 남아 있으면 그 parent를
  검사하지 않고 `project_search_limit_exceeded`입니다. 둘 다 exit 2, 후속 build/runner 0입니다.
- Normal marker absence는 retained ancestor를 성공적으로 enumerate하고 raw exact `godj.toml` entry가
  없음을 관찰한 경우뿐입니다. Cwd physical resolution 또는 retained ancestor open/enumeration/stat의
  ENOENT/ESTALE/ENOTDIR/permission/I/O와 traversal 중 disappearance/race는 errno와 무관하게
  `migration_project_selection_error/project_selection_failed`, exit 3이며 outer fallback/build/runner
  0입니다.
- Raw exact entry의 initial `Lstat`가 stable symlink/directory/non-regular를 관찰하거나 selected stable
  identity의 bytes가 oversized/lexical/shape-invalid면 `invalid_project_descriptor`, exit 2이며 outer
  fallback하지 않습니다. Initial regular entry를 선택한 뒤 no-follow open/Fstat/bounded read/post-read
  same-file Lstat 중 disappearance, identity change, symlink swap 또는 permission/I/O가 발생하면 errno와
  무관하게 `migration_project_selection_error/project_selection_failed`, exit 3이고 outer
  fallback/build/runner 0입니다. Observed identity/file-type/disappearance race와 stable invalid descriptor를
  같은 fault에서 섞으면 selection failure가 우선합니다. Stable opened inode의 concurrent in-place
  write/truncate는 atomic snapshot을 보장하지 않습니다. Bounded read가 invalid/oversized면
  `invalid_project_descriptor`, 우연히 valid면 success할 수 있으며 producer atomic replace 또는 external
  synchronization이 필요합니다.
- `--project <descriptor-file>`의 값은 **descriptor file 자체**입니다. Directory, package path나
  project root가 아닙니다. Relative value는 invocation cwd 기준으로 resolve/clean하고 absolute value도
  허용하되 basename은 exact `godj.toml`이어야 합니다. Descriptor parent를 physical absolute
  directory로 resolve한 뒤 retained parent handle의 raw exact-name equality와 implicit marker와 같은
  Lstat/no-follow/same-file 규칙을 적용합니다. 명시 선택은 ancestor discovery를 전혀 실행하지 않습니다.
  Explicit parent/entry initial probe에서 supplied path가 처음부터 absent/non-directory이거나 final entry가
  stable symlink/non-regular임이 확인되면 `invalid_project_descriptor`, exit 2입니다. Initial regular exact
  entry를 선택한 뒤 disappearance/identity/symlink race나 initial parent probe의 permission/ESTALE/other
  I/O는 `project_selection_failed`, exit 3입니다.
- Physical descriptor parent가 project root이자 build/runner working directory입니다.
- Descriptor는 dependency 없는 **GoDj canonical descriptor-v1 TOML-shaped subset**입니다. Full TOML
  1.0 compatibility를 주장하지 않습니다. Maximum 64 KiB, BOM/non-ASCII semantic syntax를 거부하고
  newline mode는 document 전체가 LF 또는 CRLF 하나여야 하며 mixed/bare CR를 거부합니다. EOF는
  exact one line ending 뒤여야 합니다.
- Physical line은 ASCII SP/TAB만인 blank, optional ASCII SP/TAB 뒤 `#`와 printable ASCII/TAB comment,
  또는 아래 semantic line 중 하나입니다. Inline comment, escape, single/multiline string은 없습니다.
  Blank/comment를 제외한 semantic line은 exact order로 한 번씩만 나타나며 `format_version` 뒤
  `[project]`, 그 뒤 `package`입니다. Key/`=` 주변에는 ASCII SP/TAB만 허용합니다.

```text
format_version [SP/TAB]* = [SP/TAB]* <canonical-version>
[SP/TAB]* [project] [SP/TAB]*
package [SP/TAB]* = [SP/TAB]* "<simple-package>"
```

Canonical example은 다음과 같습니다.

```toml
format_version = 1

[project]
package = "./cmd/mysite"
```

- Parser는 document 전체 lexical/framing과 closed shape를 먼저 검증합니다. Duplicate/unknown/missing/
  reordered semantic line, invalid comment/string/whitespace와 package grammar가 하나라도 있으면 version
  값도 틀렸더라도 `invalid_project_descriptor`입니다. Shape가 완전히 valid한 뒤 canonical decimal
  version lexeme `0|[1-9][0-9]*`와 range 0..65,535를 검사합니다. Sign/leading zero/overflow는 invalid
  descriptor이고 supported value는 exact 1입니다. 다른 canonical in-range integer는
  `migration_project_selection_error/project_descriptor_incompatible`입니다.
- `<simple-package>`는 escape 없는 printable ASCII이고 `project.package`는
  literal leading `./`을 먼저 분리하고 그 뒤 non-empty slash-form remainder를 검증합니다. Remainder는
  `path.Clean(remainder)==remainder`, 모든 segment가 non-empty이고 `.`/`..`/`...`가 아니며 glob meta,
  backslash, NUL이 없습니다. 따라서 `./cmd/mysite`는 valid지만 `cmd/mysite`, `./`, `./cmd/../site`,
  `./cmd//site`, `./...`는 invalid입니다. Shell fragment가 아니라 `go build`의 단일 argv입니다.
- Descriptor/root/source path 비교와 diagnostic order는 Unicode normalization이나 locale을 쓰지
  않는 raw UTF-8 byte order입니다.

## Global build와 project-linked runner protocol v1

Global orchestration은 project root에서 shell 없이 다음 exact argv로 build합니다.

```text
go build -mod=readonly -o <private-0700-temp-dir>/godj-project-runner <project.package>
```

`<project.package>`는 descriptor의 단일 검증된 argv입니다. Child env를 바꾸기 전에 caller의 non-empty
original `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`을 capture합니다. 각 configured protected root는
existing directory로 physical resolve해야 하며 resolve/stat할 수 없으면
`project_temporary_storage_failed`입니다. Temp base는 ambient `TMPDIR`, 없으면 platform default를
physical resolve/Lstat해 existing real directory인지 확인하고 project root 및 각 protected root와
같거나 그 descendant면 거부합니다. Symlink final base, invalid/unreadable/project/HOME/XDG-inside base도
`project_temporary_storage_failed`입니다. 따라서 `TMPDIR=$HOME/tmp`와 XDG subtree mutation은 build 전
실패합니다. Default `os.MkdirTemp("", ...)`를 쓰지 않고 검증된 base를 명시해 그 아래
invocation-private `0700` root를 만든 뒤 같은 outside-project/protected-roots 조건을 다시 확인합니다.

이 containment와 project/HOME/XDG non-mutation claim은 static final-base symlink/physical containment
validation 뒤 같은 사용자가 temp-base path를 concurrently rebind/rename하지 않는 base fixture에
한정합니다. Base validation과 private-root creation/build 사이의 path-resolution race를 retained directory
handle로 fence하지 않으므로 그 race에서 write redirection을 막는다고 주장하지 않으며 product hardening은
후속입니다.

Child environment는 caller environment의 `TMPDIR`, `GOWORK`, `GOTOOLCHAIN`, `GOFLAGS`, `GOENV`,
`GOCACHE`, `GOCACHEPROG`, `GOMODCACHE`, `GOTMPDIR`, `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`,
`TEST_TELEMETRY_DIR`를 제거합니다. 그 뒤 exact `GOWORK=off`, `GOTOOLCHAIN=local`, `GOENV=off`, empty
`GOFLAGS`, empty `GOCACHEPROG`와 private root 아래 exact `TMPDIR=<private>/tmp`, `GOTMPDIR=<private>/gotmp`,
`GOCACHE=<private>/gocache`, `GOMODCACHE=<private>/gomodcache`, `HOME=<private>/home`,
`XDG_CONFIG_HOME=<private>/xdg-config`, `XDG_CACHE_HOME=<private>/xdg-cache`,
`TEST_TELEMETRY_DIR=<private>/telemetry`를 넣습니다. 각 child directory도 `0700`이고 만들 수 없으면
build 전에 fail-closed합니다. `GOTELEMETRY=off` 환경변수를 유효한 policy switch라고 주장하거나
의존하지 않습니다. 따라서 Go tool-controlled workspace/cache/temp/telemetry/`go env -w` write는 project
tree와 caller HOME/XDG tree 밖의 private root로 향하고 automatic toolchain download나 injected build
flag에 의존하지 않으며 `go.mod`/`go.sum`을 고치지 않습니다. Private HOME/XDG는 Go의 default
HOME/UserConfigDir/default-netrc lookup만 private tree로 redirect합니다. Ambient `NETRC`,
`GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM`/`GIT_CONFIG_COUNT`, `GIT_SSH`/`GIT_SSH_COMMAND`, askpass/helper,
`SSH_AUTH_SOCK`, `GOAUTH` command, `CC`/`CXX`/cgo tool과 external ssh의 OS-account home 등 explicit
override/helper/read는 exhaustive scrub/isolation하지 않습니다. Module network/auth success와 credential
confidentiality를 보장하지 않고 build failure로
닫습니다. Binary를 persistent path에 publish하지 않으며 normal
return/caller context cancellation/handled Unix SIGINT에서 private temp removal을 정확히 한 번
best-effort attempt합니다. Cleanup 성공은 `RemoveAll` 성공과 retained parent의 post-check absence를
모두 뜻하고 그 observation에서만 residue 0을 보장합니다. Persistent runner/module/build cache,
cleanup retry와 crash/escaped-descendant 뒤 stale-temp scavenging 정책은 후속 범위입니다.

성공한 binary는 project root cwd에서 shell 없이 exact private argv token 하나로 실행합니다.

```text
<private-binary> __godj_project_runner_v1
```

stdin request는 UTF-8 JSON one value + EOF인 closed protocol입니다.

```json
{"protocol_version":1,"command":"migrations.check"}
```

Success stdout도 one JSON value + EOF이며 정확히 다음 closed shape입니다.

```json
{"protocol_version":1,"status":"ok","result":{"source_count":2,"definition_count":2,"definition_set_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
```

Failure stdout은 정확히 다음 closed shape입니다.

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invalid_definition_document"}}
```

- Request/success/failure 모두 duplicate/unknown/missing field, wrong type, trailing value/garbage와
  non-UTF-8을 거부합니다. Integer decoder는 raw lexeme를 보존합니다. Canonical integer는 exact
  `0|[1-9][0-9]*`이며 sign, leading zero, decimal과 exponent를 거부합니다. `protocol_version` range는
  0..65,535, success `source_count`/`definition_count` range는 각각 0..2,048입니다. Digest는
  `sha256:`와 lowercase hex 64자입니다. Failure category/code는 아래 closed pair만 허용하며 unknown
  pair는 `invalid_project_runner_response`입니다.
- Request parser precedence는 byte cap/UTF-8/one-value+EOF/duplicate framing → required
  `protocol_version` presence/type/canonical lexeme/range → supported value → exact command/schema입니다.
  Global sender의 canonical request version lexeme는 exact `1`입니다. Canonical in-range value가 1이
  아니면 command dispatch/Load 0의 `project_protocol_incompatible`입니다. Byte/framing/duplicate/UTF-8,
  wrong/missing/out-of-range/noncanonical coordinate와 remaining command/schema fault는
  `invalid_project_runner_request`입니다. Linked request parser는 두 pair만 exact
  `protocol_version:1`, `status:"error"` response envelope의 logical error로 쓰고 transport exit 0을
  사용합니다. Canonical closed objects는 각각 다음과 같습니다.

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_project_protocol_error","code":"invalid_project_runner_request"}}
```

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_project_protocol_error","code":"project_protocol_incompatible"}}
```
- Response parser precedence는 transport exit → byte cap/UTF-8/one-value+EOF/duplicate framing → required
  `protocol_version` presence/type/canonical lexeme/range → supported value → remaining status/result/error
  closed schema(unknown fields와 individual count/digest grammar 포함) → success cross-field invariants →
  logical outcome입니다. 따라서 duplicate `protocol_version`과 value mismatch가 함께 있으면 duplicate
  framing의 `invalid_project_runner_response`; otherwise-valid exact version 2는 MIG-071의
  `project_protocol_incompatible`입니다. Count lexeme/range/digest grammar 오류도
  `invalid_project_runner_response`입니다. Success는 `source_count == definition_count`여야 하고 둘 다
  0이면 digest가 exact
  `sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`, count가 양수면 이 empty digest가
  아니어야 합니다. Cross-field 위반도 `invalid_project_runner_response`, exit 3이고 combined malformed
  field는 앞선 schema 오류가 우선합니다. Raw document가 wire에 없으므로 그 밖의 valid-looking nonempty
  64-hex digest truth는 global이 recompute하지 않고 linked response를 trust하는 protocol v1 제한입니다.
- Runner는 valid success 또는 logical failure response를 썼다면 transport exit 0을 사용합니다.
  Nonzero exit, signal, response 없이 exit는 `project_runner_failed` transport error/exit 3입니다.
- Valid logical failure의 known category/code **pair만** process boundary에서 reclassify하지 않습니다.
  Detailed loader/planning error context는 linked test instrumentation에만 남고 wire/global error에는
  없습니다. User-visible file/pointer diagnostics 부재는 protocol v1의 의도적 제한입니다.
- stdout protocol에는 raw document, decoded definition, source inventory/path와 user data를 넣지
  않습니다. Build stdout/build stderr/runner stderr raw diagnostic prefix도 result/error와 public stream에
  넣지 않습니다. Cancel path는 capture stop → parent pipe close → drainer join 뒤, normal path는 EOF
  drainer join 뒤 test-only `retained_bytes`/`truncated` scalar를 확정하고 raw bytes를 public publication
  전에 discard합니다. Public stderr는 category/code one-line뿐이며
  user-visible raw diagnostic exposure는 protocol v1 비목표입니다.

### Closed error taxonomy와 exit mapping

Global/project-check가 내보낼 수 있는 exact top-level category/code와 public exit은 다음 closed
table입니다. Logical runner failure response는 linked request parser의 exact protocol pair 두 개,
discovery와 definition pass-through owner만 허용합니다.

| Owner | Category | Allowed code | Public exit |
|---|---|---|---:|
| command | `migration_project_command_error` | `invalid_arguments` | 2 |
| selection/descriptor | `migration_project_selection_error` | `project_not_found`, `project_search_limit_exceeded`, `invalid_project_descriptor`, `project_descriptor_incompatible` | 2 |
| selection filesystem | `migration_project_selection_error` | `project_selection_failed` | 3 |
| build | `migration_project_build_error` | `project_temporary_storage_failed`, `project_build_failed` | 3 |
| runner/protocol | `migration_project_protocol_error` | `invalid_project_runner_request`, `project_runner_failed`, `project_protocol_incompatible`, `invalid_project_runner_response` | 3 |
| process | `migration_project_process_error` | `project_canceled`, `project_cleanup_failed` | 3 |
| process | `migration_project_process_error` | `project_interrupted` | 130 |
| linked config/discovery | `migration_definition_discovery_error` | `invalid_project_source_config`, `invalid_source_root` | 2 |
| linked catalog | `migration_definition_discovery_error` | `invalid_source_entry`, `unsafe_source_entry`, `source_catalog_limit_exceeded` | 1 |
| linked filesystem | `migration_definition_discovery_error` | `source_discovery_failed`, `source_read_failed` | 3 |
| product loader pass-through | `migration_definition_source_error` | ADR-0020의 exact 9 source code | 1 |
| product graph pass-through | `migration_graph_error` | `invalid_node`, `duplicate_node`, `invalid_dependency`, `duplicate_dependency`, `dependency_not_found`, `dependency_cycle` | 1 |
| internal | `migration_project_internal_error` | `project_internal_error` | 3 |

Global-owned failure는 table의 category/code로 생성합니다. Runner logical failure response에서 허용하는
owner는 linked request parser가 생성한 exact
`migration_project_protocol_error/invalid_project_runner_request` 또는
`migration_project_protocol_error/project_protocol_incompatible`, exact
`migration_definition_discovery_error`의 위 closed code와
`migration_definition_source_error` 9 code/`migration_graph_error` 6 code pass-through뿐이며 known
category/code pair만 reclassify 없이 보존합니다. 다른 global-owned pair를 runner가 보내거나 unknown
category/code 조합이면 logical
error가 아니라 closed response schema 위반이므로
`migration_project_protocol_error/invalid_project_runner_response`, exit 3입니다.
Request-version error pair는 linked parser가 response envelope version 1 안에 쓴 logical error이고,
MIG-071처럼 runner response envelope coordinate 자체가 version 2인 경우의
`project_protocol_incompatible`는 global response parser가 생성하므로 서로 다른 observation입니다.
Root count/alias/grammar는 `invalid_project_source_config`; initial absent/static non-directory/symlink
component는 `invalid_source_root`; selected component open/Fstat race·permission/I/O와 retained root
traversal/readdir permission·I/O는 `source_discovery_failed`;
candidate read는 `source_read_failed`; entry/source/SourceID/document/batch cap은
`source_catalog_limit_exceeded`입니다. Private temp/cache creation은 `project_temporary_storage_failed`;
pipe close/drainer join/temp removal은 `project_cleanup_failed`; closed stage에 속하지 않는 invariant
failure는 `migration_project_internal_error/project_internal_error`입니다. 선택된 non-cancel primary가
있으면 그 error가 cleanup failure보다 우선하고 cleanup failure는 metric에 남깁니다. 예정 success/
`project_interrupted`/`project_canceled`만 cleanup failure가 final error로 대체합니다. Cleanup 성공 뒤
success만 public stdout 한 번, error는 stdout 0/bounded stderr 한 번이며 runner stdout response와
별개입니다.

## Flat source discovery와 DB-free 경계

Project-linked code는 ordered semantic root list를 제공합니다. 이를 제공할 public Go type/API는
이번 work에서 정하지 않고 test-only candidate로만 검증합니다.

- Root Go string은 config preflight에서 valid UTF-8이어야 하고 그 뒤 project-root-relative clean slash
  path grammar를 검사합니다. Invalid UTF-8, absolute, empty, NUL/backslash, `..` escape와 clean-path
  alias는 filesystem/SourceID/Load 전에 `invalid_project_source_config`, exit 2입니다. Configured order는
  의미가 없고 unique roots를 UTF-8 byte order로 정렬합니다. `.` root는 project root 자체를 뜻합니다.
- Root는 existing real directory여야 합니다. Project root directory handle에서 각 intermediate/final
  component마다 retained parent handle의 raw entry name이 configured component bytes와 exact 같은지
  먼저 확인합니다. Initial exact-name absence/wrong-case 또는 stable initial Lstat non-directory/symlink는
  `invalid_source_root`, exit 2입니다. Initial real-directory entry를 선택한 뒤 no-follow open/Fstat의
  ENOENT, identity/non-directory/symlink swap, disappearance 또는 permission/I/O는
  `source_discovery_failed`, exit 3입니다. Final retained handle 자체로 entries를 열거하고 target을 따라가지
  않습니다. Wrong-case/static-invalid와 post-selection race mutation을 Ubuntu/macOS gate에 두며 precheck
  path를 나중에 이름으로 다시 여는 방식은 허용하지 않습니다.
- Sorted semantic roots의 directory-handle traversal/preflight를 모두 먼저 완료한 뒤 canonical root path
  order로 retained final handle을 bounded `ReadDir` chunk로 enumerate합니다. 한 call이 entries와 non-EOF
  error를 함께 반환하면 entries를 세기 전에 `source_discovery_failed`, exit 3이 우선합니다. Successful
  entries는 matching 여부와 무관하게 checked-add하고 65,537번째를 관찰하는 즉시
  `source_catalog_limit_exceeded`, exit 1로 멈춥니다. Later roots/ReadDir은 실행하지 않아 그 미관찰 I/O가
  override하지 않습니다. Cap 이하로 모든 immediate entries를 끝낸 뒤에만 retained names를 raw full
  SourceID path order로 정렬해 candidate stage로 갑니다. Recursion하지 않습니다.
- Raw case-sensitive suffix `*.godj.json`만 candidate입니다. Nonmatching regular file과 ordinary
  subdirectory는 ignore합니다. Matching symlink, directory, socket/device/FIFO 등 non-regular entry는
  `unsafe_source_entry`이며 target을 열지 않습니다. Nonmatching symlink/non-regular entry는 ignore합니다.
- Candidate suffix match와 diagnostic selection은 Unix directory-entry raw name bytes에 적용합니다.
  Matching name의 SourceID byte cap을 먼저 검사하고 cap 안에서 UTF-8을 검증합니다. Matching invalid
  UTF-8은 SourceID를 만들거나 file을 열기 전 `invalid_source_entry`, exit 1입니다. Error metric은
  invalid string 대신 exact lowercase `path_bytes_hex`를 쓰고 raw byte order로 winner를 고릅니다.
  Nonmatching invalid-byte entry는 aggregate entry cap에만 세고 ignore합니다.
- Candidate는 retained root handle 기준으로 entry no-follow open과 opened handle `Fstat` regular 확인을
  합니다. 모든 bounded read outcome은 read bytes/error/cap breach를 provisional로 보존하고 handle을 close한
  뒤 retained root handle에서 mandatory post-read same-entry check를 먼저 분류합니다. Identity mismatch,
  non-regular 또는 symlink swap은 `unsafe_source_entry`, exit 1; disappearance 또는 post-check
  permission/I/O는 `source_read_failed`, exit 3이며 둘 다 simultaneous original read/cap outcome보다
  우선합니다. Post-check가 stable일 때만 original non-EOF read I/O를 `source_read_failed`, 그 다음
  document/batch maximum+1을 `source_catalog_limit_exceeded`로 분류하고 clean EOF는 계속합니다. Concurrent
  mutation에서 success snapshot을 보장하지 않습니다.
- Hardlink는 regular file로 취급합니다. Inode 기반 alias를 만들지 않으며 같은 definition identity면
  actual loader의 deterministic duplicate graph error가 처리합니다.
- `SourceID`는 root/entry를 합친 clean project-relative slash path입니다. Candidate를 SourceID raw
  UTF-8 byte order로 정렬한 뒤 bounded read하며 configured/enumeration order가 결과에 영향을 주지
  않습니다.
- Candidate precedence는 aggregate entry cap 뒤 각 matching full logical path의 SourceID byte cap →
  UTF-8 → no-follow regular/identity safety → aggregate source count입니다. 어느 candidate fault라도 있으면
  source-count 판정보다 먼저 실패합니다. 여러 entry는 raw full path bytes가 가장 작은 winner이고 같은
  entry의 combined fault는 위 reason order를 따릅니다.
- Zero roots와 existing empty roots 모두 source read 0, canonical empty source slice로
  `definition.Load()`를 정확히 1회 호출합니다. Nonempty success/failure도 discovery preflight가
  끝난 뒤 actual product `definition.Load`를 최대·정확히 1회 호출하고 partial definition을 내보내지
  않습니다.
- Root handle traversal 또는 directory enumeration permission/I/O는 `source_discovery_failed`입니다.
  Root/entry/SourceID 오류도 raw UTF-8 byte path order로 고릅니다.

“DB-free”는 global/linked orchestration의 direct `migrations.NewPlanner` call 0과 GoDj-owned DB
open/query, recorder read/write, `Executor.Migrate`/revision lifecycle execution 0을 뜻합니다. Actual
`definition.Load`는 decoded graph stage에서 loader-owned `migrations.NewPlanner`를 위 LoadReport처럼
정확히 한 번 호출하며 graph error category/code를 보존합니다. Project-linked Go binary를 build/run할
때 실행되는 임의 사용자 package `init()`의 외부 side effect까지 차단하거나 없다고 주장하지 않습니다.

## Resource limits와 precedence

다음 11개 parsed/accepted input·catalog·wire 또는 retained-output maximum은 inclusive입니다. 각 cap은
maximum-1/equal/+1, overflow-safe aggregate와 combined fault를 검증합니다.

| Limit | Inclusive maximum | 적용 경계 |
|---|---:|---|
| descriptor bytes | 64 KiB | descriptor framing 전 |
| ancestor directories | 128 | starting directory를 1로 계산 |
| semantic roots | 256 | runner root config preflight |
| aggregate directory entries | 65,536 | final source-root immediate entries의 noise/subdirectory 포함 |
| sources | 2,048 | matching safe candidate 수 |
| SourceID bytes | 1,024 | candidate read 전 |
| document bytes | 1 MiB | 개별 file read/copy 전 |
| batch bytes | 16 MiB | aggregate file read/copy 전 |
| request bytes | 64 KiB | runner stdin framing |
| response bytes | 64 KiB | global stdout framing |
| diagnostic retained-prefix bytes | 1 MiB | build stdout, build stderr, runner stderr 각 stream별 별도 retained cap |

Regular source document는 per-file/remaining-batch maximum+1까지만 읽습니다. 모든 outcome 뒤 handle을
close하고 mandatory post-read same-entry check를 완료합니다. Post-check safety/I/O fault가 simultaneous
read error/cap breach보다 우선하며, identity가 stable일 때만 original read I/O 뒤 document/batch cap을
분류합니다. Breach 뒤 remainder/file EOF까지 전량 읽지 않으므로 1 MiB/16 MiB cap이 실제 document I/O
bound입니다.

Request/response/diagnostic **pipe**는 maximum+1 확인 뒤 child deadlock을 피하려고 EOF까지 concurrent
drain하되 cap 밖 bytes를 보존하지 않습니다. Build stdout, build stderr와 runner stderr는 각각
독립적으로 앞의 1 MiB만 retain하고 이후를 drain하며 stream별 deterministic `truncated=true`를
표시합니다. 1 MiB는 total stream maximum이 아니라 retained-prefix cap입니다. 세 diagnostic stream을
합쳐 하나의 cap으로 계산하지 않습니다. Runner stdout은 diagnostic이 아니라 별도 64 KiB response
cap입니다. 합계는 checked/saturating addition으로 overflow가 cap 우회를 만들지 못합니다. Raw retained
prefix는 test-only byte-count/truncation 검증용이고 oracle/wire/user stdout·stderr에 게시하지 않습니다.
Cancel은 capture stop/parent pipe close/drainer join, normal completion은 EOF drainer join 뒤 scalar만
확정하고 raw bytes를 public publication 전에 폐기합니다.

이 11개는 build/runner wall time, CPU/RSS/address space, goroutine/thread/process count, binary/private
HOME/cache/temp bytes·inode, module/network transfer 또는 cap 이후 pipe drain bytes/time을 제한하지
않습니다. 64 KiB/1 MiB wire·diagnostic 값도 total pipe I/O cap이 아닙니다. Sandbox, rlimit, cgroup,
quota나 hard timeout은 두지 않으며 normal runner는 caller가 cancel/SIGINT하지 않으면 무기한 실행될
수 있습니다. Host filesystem/OS limit와 CI timeout은 외부 운영 경계입니다.

65,536 entry cap은 retained **final source-root** immediate-entry enumeration에만 적용합니다. Ancestor/
explicit descriptor parent의 raw marker-name scan과 configured root intermediate/final component parent의
raw exact-name scan은 entry를 한 개씩 stream하고 누적 name을 retain하지 않지만 aggregate entry
count/name bytes/time hard cap은 없습니다. Host filesystem의 per-entry name bound만 의존하며 이
unbounded pre-scan과 product hardening은 후속 범위입니다.

Semantic roots는 count 256 외 per-root/aggregate Go-string bytes, component count와 validation time cap이
없습니다. Existing empty root/zero-candidate catalog는 SourceID가 없어 SourceID 1,024-byte cap을
exercise하지 않습니다. Trusted linked config의 root-string hardening은 새 12번째 cap을 이번 work에
추가하지 않고 후속으로 남깁니다.

Global stage precedence는 다음과 같습니다.

```text
arguments
→ project selection
→ descriptor byte/framing/lexical + full closed shape
→ descriptor version
→ build
→ runner transport/response framing
→ protocol coordinate
→ supported protocol version
→ remaining response schema
→ logical outcome
```

Runner 내부 precedence는 다음과 같습니다.

```text
request byte/framing/duplicate
→ request protocol coordinate
→ supported request version
→ remaining request schema
→ all sorted root-handle traversal/preflight
→ canonical-root ReadDir I/O/aggregate entry cap
→ candidate SourceID byte cap/UTF-8
→ no-follow regular/identity safety
→ aggregate source count
→ sorted bounded read provisional outcome
→ mandatory close/post-read same-entry check
→ stable original-read I/O/document+batch cap classification
→ definition.Load exactly once
→ response
```

선행 stage failure에서는 후속 build/runner/read/Load/DB/lifecycle counter가 0입니다. 같은 stage의
여러 path failure는 raw UTF-8 byte path가 가장 작은 항목을, numeric breach는 minimum Actual과
동일 path order를 사용합니다.

## Exit, cancellation과 process ownership

Public `godj migrations check` exit 의미는 다음과 같습니다.

| Exit | 의미 |
|---:|---|
| 0 | Project check가 끝났고 source/definition graph가 valid |
| 1 | Check가 실행됐지만 source entry/definition/document/graph가 invalid |
| 2 | Argument, explicit/implicit project selection, descriptor 또는 project config가 invalid |
| 3 | Build, filesystem read, runner transport/protocol 또는 internal orchestration failure |
| 130 | Unix SIGINT 뒤 applicable active-child escalation/direct reap(if any)과 parent cleanup 완료 |

Build와 runner child는 invocation-owned process group으로 실행합니다. Cleanup 보장은 normal return,
caller context cancellation과 handled Unix SIGINT에 한정합니다. Context cancel이나 handled Unix
SIGINT의 primary selection은 single coordinator만 소유합니다. 각 ordered stage completion barrier에서
advance/terminal commit 직전에 handled SIGINT를 먼저, 그 다음 caller context cancellation을 확인합니다.
Terminal outcome은 한 번 atomic commit합니다. Interrupt/cancel이 먼저 commit되면 later child success/
fault는 무시하고, final success 또는 non-cancel primary error가 먼저 commit되면 later signal/cancel이
primary를 바꾸지 않습니다. Barrier order와 두 방향 race를 mutation gate로 고정합니다.

Committed interrupt/cancel은 새 dispatch/read/capture를 멈춥니다. Active unreaped child가 있을 때만
child process group에 SIGINT를 전달하고 아래 2초 escalation을 실행합니다. Child가 아직 spawn되지
않았거나 이미 reaped면 group forward/escalation을 생략하되 같은 interrupted/canceled exit와 parent/temp
cleanup을 수행합니다. Initial
SIGINT forward와 이후 escalation signal에서 ESRCH만 already-gone이며 다른 syscall error는
`migration_project_process_error/project_cleanup_failed`입니다. Exact
graceful escalation deadline은 2초이며 그 뒤 direct child 상태와 무관하게 stored owned pgid에
group SIGKILL을 attempt합니다. Group escalation attempt 뒤 parent가
소유한 pipe read handle을 강제 close하고, capture goroutine과 독립인 direct OS wait로 owned direct
child를 synchronous `Wait`/reap한 뒤 drainer를 join합니다. 이어 test-only diagnostic scalar를 확정하고
raw prefix를 zero/discard한 뒤 private temp `RemoveAll`을 한 번 attempt하고 retained parent에서 absence를
post-check합니다. Normal completion도 EOF drainer join →
scalar 확정 → raw discard → temp cleanup 순서입니다. 따라서 descendant가 pipe FD를 보유해도 parent return을
막지 않습니다. Arbitrary Unix process가 waitable해지는 시간에는
hard bound를 두지 않습니다. SIGINT는 applicable active-child escalation/direct reap(if any)과 parent
cleanup이 완료된 뒤 `migration_project_process_error/project_interrupted`, exit 130입니다. SIGINT가 아닌 caller
context cancellation은 같은 cleanup 뒤 `migration_project_process_error/project_canceled`, exit 3입니다.
Structured return 전에 owned direct child reap과 parent drainer join은 완료하지만 temp residue 0은
RemoveAll+absence post-check가 성공했을 때만 보장합니다. Removal/post-check failure는
`project_cleanup_failed`; injected base는 exact `temp_cleanup_attempts=1`, `cleanup_failed=1`,
`residual_temp=1` test-only metric이며 raw temp path를 output하지 않습니다. Residual temp가 남을 수 있고
automatic retry/scavenging하지 않습니다. Same-pgid 또는
pgid를 이탈한 non-child descendant의 return-time disappearance/reap은 sandbox/subreaper가 없는 이번
contract에서 보장하지 않습니다. Cleanup failure는 선택된 success/interrupt/cancel outcome만
`project_cleanup_failed`/3으로 대체합니다. Non-cancel primary selection/build/runner/catalog/internal
failure가 이미 있으면 그 primary code를 보존하고 cleanup failure는 metric에 남깁니다.

SIGTERM/SIGHUP/other fatal signal/SIGKILL, parent/host crash와 power loss에서는 structured public exit,
group escalation 또는 temp cleanup을 보장하지 않습니다. 이를 caller context cancellation/exit 3으로
조용히 합치지 않으며 signal-specific code(예: 143)와 stale-temp scavenging은 후속 결정입니다.

Base oracle의 stdout/stderr exactly-once와 partial stdout 0은 healthy injected/base sink에서 cleanup 뒤
single bounded write attempt에 대한 계약입니다. EPIPE/SIGPIPE/EBADF, short write, disk-full 또는
caller-closed sink에서는 category delivery, rollback, zero partial write와 portable exit을 보장하지
않습니다. Detectable sink failure는 가능한 경우 exit 3으로 끝내지만 실패한 stderr로 structured error를
전달할 수 있다고 주장하지 않습니다. Cleanup 자체는 user sink write 전에 이미 완료됩니다.

## Static fail-closed와 artifact gate

- 완료 결과는 11 reference set/115 unique contract와 110 ordered cross-binding rejection입니다.
- MIG-065..074의 manifest status는 exact 10 `oracle_locked`입니다.
- Product는 10 adapter/105 contract의 `100 passing + 5 deviation`을 그대로 유지하며 새 manifest를
  `godj-conformance` product adapter에 연결하지 않습니다.
- 새 static fixture comparison은 exit 1과 MIG-065..074 ordered mismatch 정확히 10개입니다.
- Product `godjcheck`에 새 scenario를 직접 주면 **product check CLI가 아니라 conformance tool의**
  exit 2/no actual output으로 거부합니다.
- 기존 `SHA256SUMS` 10줄은 byte-for-byte prefix로 보존하고 새 oracle checksum을 11번째 줄에만
  append합니다. `migration_definition_source_artifacts_test.go`의 기존 exact line assertion도 함께
  11-line append-only invariant로 갱신합니다.
- Oracle/manifest/static/scenario를 재생성한 뒤 두 번 실행이 byte-identical이고 `--check`와
  no-rewrite gate가 모두 통과해야 합니다.

## Hosted CI expansion gate

2026-08-09 현재 public repository에 제공되는 exact label은
[GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)와
official `actions/runner-images`에서 확인했습니다. Implementation head에서 required **10 hosted
job executions**을 달성했으며 existing workflow 하나만 수정했습니다. 새 workflow/PR을 만들지
않았습니다.

- Existing `ubuntu-24.04` x64 full job은 현재 `make ci`, Linux/386, checksum/no-rewrite를 보존하고
  `go test -count=1 ./conformance/projectcheck` focused normal gate를 추가합니다.
- Existing `macos-15` arm64 exact job은 현재 exact Python/oracle과 focused lifecycle gate를 보존하고
  `go test -count=1 ./conformance/projectcheck`를 추가합니다.
- New project-check matrix는 `strategy.fail-fast: false`, leg당 `timeout-minutes: 20`으로 아래 exact
  label/expected coordinate 네 leg입니다.
- 같은 네 label의 new SQLite matrix도 `fail-fast: false`, leg당 20분인 별도 required matrix입니다.
  현재 actual backend package인 `./migrations ./db/sqlite`만 검증합니다.

| `runs-on` label | `expected_goos` | `expected_goarch` |
|---|---|---|
| `ubuntu-22.04` | `linux` | `amd64` |
| `ubuntu-24.04-arm` | `linux` | `arm64` |
| `macos-15-intel` | `darwin` | `amd64` |
| `macos-26` | `darwin` | `arm64` |

두 matrix의 각 leg는 pinned checkout/setup-go action을 사용해 Go `1.26.5`를 설치하고 `go version`과
`go env GOOS GOARCH` fingerprint를 남긴 뒤 다음 exact assertions로 runner misrouting을 fail합니다.

```text
test "$(go env GOOS)" = "${{ matrix.expected_goos }}"
test "$(go env GOARCH)" = "${{ matrix.expected_goarch }}"
```

Project-check leg는 exact 다음 네 command를, SQLite leg는 package target만 바꾼 다음 네 command를 모두
실행합니다.

```text
go test -count=1 ./conformance/projectcheck
go test -race -count=1 ./conformance/projectcheck
CGO_ENABLED=0 go test -count=1 ./conformance/projectcheck
go vet ./conformance/projectcheck

go test -count=1 ./migrations ./db/sqlite
go test -race -count=1 ./migrations ./db/sqlite
CGO_ENABLED=0 go test -count=1 ./migrations ./db/sqlite
go vet ./migrations ./db/sqlite
```

각 project-check/SQLite matrix leg의 마지막은 exact clean-worktree gate입니다. 첫 command는 tracked
rewrite를, 둘째는 staged/unstaged/untracked residue 전체를 잡습니다.

```text
git diff --exit-code
test -z "$(git status --porcelain=v1)"
```

Matrix leg에는 `continue-on-error`를 두지 않습니다. `fail-fast: false`는 네 환경을 끝까지 관찰하기
위한 것이며 어느 required leg 실패도 전체 gate 실패입니다. `ubuntu-24.04-arm` public-preview image의
as-available 위험은 숨기지 않고 exact runner fingerprint와 실패로 관찰합니다.

CI topology static gate는 YAML top-level job definition 수 4를 job 수라고 오판하지 않습니다. Existing
non-matrix 2 + project-check matrix 4 + SQLite matrix 4를 expand해 exact 10 executions인지 계산하고,
위 label/expected coordinate, 양쪽 `fail-fast: false`, timeout 20, `continue-on-error` absence와 assertion/
command set, final clean-worktree 두 command를 함께 pin합니다.

Windows는 native signal/process-group/no-follow 계약이 없으므로 green skip job을 만들지 않고 지원을
주장하지 않습니다. PostgreSQL/MySQL actual adapter/product contract도 없으므로 service container만
띄우는 job은 금지합니다. Future backend의 첫 required job은 digest-pinned service image,
health check, UTC timezone과 C locale 또는 명시적으로 승인된 collation, actual query/write/
transaction/schema/migration/recorder/revision-lifecycle 및 durable restart/persistence contract를 모두
실행해야 합니다. Expected contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음,
final clean worktree도 필수입니다. Adjacent versions는 이후 non-required scheduled matrix로만
분리합니다. 이
implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 위 exact 10 job을
모두 통과했습니다. Exact 16-file completion-documentation commit
`34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. 현재 EVID-026 append/status 교정의 exact 8-file patch 자체의 hosted CI는
`not run/pending`입니다.

## 구현 단계

1. Activation 당시 Proposed ADR-0021과 이 work의 exact descriptor/protocol/discovery/limit/exit 경계를
   independent review로 고정합니다.
2. MIG-065..074 independent reference scenarios, manifest, static fixture, oracle와 provenance를
   작성합니다.
3. `conformance/projectcheck/**`에 test-only global/project orchestration proof를 만듭니다. Global
   orchestration helper는 product loader를 import하지 않고, linked fixture/runner만 existing
   `migrations/definition`을 import해 actual `Load`를 호출합니다. Production package가 test harness를
   import하지 않음을 compile/dependency gate로 확인하고 no-shell/no-follow/cancel/reap/11-cap behavior를
   검증합니다.
4. Protocol registry/uniqueness/cross-binding/checksum append-only tests와 `godjcheck` fail-closed
   tests를 갱신합니다.
5. Existing Ubuntu/macOS 두 job을 보존·보강하고 exact four-label project-check/SQLite matrix 두 개를
   같은 `.github/workflows/ci.yml`에 연결해 required 10 hosted job executions를 만듭니다.
6. Exact allowed-path, artifact byte pin, false-green mutation과 independent P0..P3 review를 완료합니다.
7. Contract 증거가 모두 통과한 뒤에만 work completion과 ADR Accepted/Rejected 여부를 별도
   completion 문서 변경에서 결정합니다.

## 완료 조건

- [x] MIG-065..074 manifest/oracle/static fixture가 exact 10 `oracle_locked`
- [x] 11 set/115 unique contract/110 ordered cross-binding이 protocol gate를 통과
- [x] Product 10 adapter/105 contract와 `100 passing + 5 deviation` byte/semantic 보존
- [x] Descriptor selection, runner protocol과 public exit/cancel semantics 검증
- [x] Cwd/ancestor/explicit-path disappearance taxonomy와 invalid-nearest no-fallback 검증
- [x] Valid-UTF-8 root preflight, flat no-follow discovery, deterministic ordering, post-read safety-over-cap combined fault와 `definition.Load` exactly once 검증
- [x] 11 limits의 maximum-1/equal/+1, overflow와 combined precedence 검증
- [x] Static exit 1/10 mismatch와 product `godjcheck` exit 2/no actual false-green 검증
- [x] 기존 checksum 10-line prefix 보존과 새 11번째 checksum append 검증
- [x] Exact 10-job hosted CI에서 existing full/exact 두 job과 4-leg project-check/4-leg SQLite
  coordinate assertion/normal/race/CGO-disabled/vet/clean-worktree 및 expanded-count static gate 검증
- [x] Allowed-path 34개, docs/status/evidence와 independent final audit 완료
- [x] ADR-0021을 증거에 따라 Accepted로 결정

## 진행 기록

- [x] GDJ-0021 activation work/Proposed ADR/status 범위 작성
- [x] P0 design audit에서 descriptor/protocol/discovery와 missing checksum-test allowlist 교정
- [x] Reference scenario/manifest/oracle/static artifact
- [x] Test-only project-check feasibility implementation
- [x] Protocol/artifact/false-green/CI integration
- [x] Completion docs, implementation-head hosted evidence와 ADR status 결정
- [x] Exact completion-documentation head의 별도 10-job hosted evidence 기록

## 수정 파일

Activation commit `fbc3c7cfc2fd779117944b8e2479a6a2bf17fdb5`는 이 파일,
`docs/adr/0021-project-linked-migration-check.md`, 두 index, CURRENT, ROADMAP, OPEN_QUESTIONS의 정확히
7개 문서에 한정했습니다. Implementation commit
`84ddf109c04acd72992b816aa72140c6e748e5f0`은 frontmatter의 exact 34개 allowed path 안에서 reference
artifact/runner, `conformance/projectcheck/**` test-only proof, protocol/static/checksum gate, Makefile/
workflow와 관련 stable 문서만 변경했습니다. Glob `conformance/projectcheck/**`는 test-only candidate
파일에만 적용됩니다.

Completion-documentation commit `34ae58fc2490deb8f884a0b5591520b11bae8669`는 exact 16 files입니다.
Status/completion 7개는 이 work,
`docs/adr/0021-project-linked-migration-check.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`,
`docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/README.md`이고, general 9개는
`docs/ARCHITECTURE.md`, `docs/CAPABILITY_CATALOG.md`, `docs/COMPATIBILITY.md`,
`docs/DEVELOPER_EXPERIENCE.md`, `docs/LICENSING.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`,
`docs/SOURCES.md`, `docs/TESTING.md`입니다. 모두 frontmatter allowed path 안입니다.

특히 `cmd/godj/**`, product `project/**`, `migrations/**`, `db/**`,
`conformance/runners/godj/**`는 금지 경계입니다. 범위 변경이 필요하면 구현 전에 work/ADR을 다시
검토하며 임의로 allowlist를 넓히지 않습니다.

## 결정된 사항

- 2026-08-09: GDJ-0021을 contract-only active work로 activation하고 ADR-0021은 Proposed로 둠
- 2026-08-09: Exact 34-path allowlist에 macOS CI workflow, independent provenance 문서와 기존
  definition-source checksum append assertion test를 포함
- 2026-08-09: Product support claim/API freeze 없이 `godj migrations check`의 externally observable
  decision contract만 MIG-065..074로 검증
- 2026-08-09: Same public Draft PR의 한 workflow에서 official Linux/macOS x64/arm64 labels를 사용한
  required 10-job target을 채택하고 Windows/service-only PostgreSQL·MySQL green skip은 금지
- 2026-08-10: MIG-065..074 local/exact reference와 implementation-head hosted 10-job evidence를 근거로
  ADR-0021을 Accepted. Public CLI/project API와 product adapter는 계속 미구현
- 2026-08-10: Exact 16-file completion-documentation head도 별도 hosted 10-job run에서 PASS

## 미결정/Blocker

외부 blocker는 없습니다. 다음은 의도적으로 GDJ-0022 이후까지 미결정입니다.

- Public global CLI/project package 타입과 registration API
- Production project binary의 direct command dispatcher와 installed binary lifecycle
- Persistent runner build cache와 full CLI/library/generator semver/upgrade policy
- Recursive/module/embed/remote discovery와 Windows behavior

ADR-0021은 contract artifact, test-only feasibility와 exact implementation-head hosted evidence를
근거로 Accepted입니다. Q-010/Q-012는 global semver/제품 CLI/DB-aware lifecycle 전체가 아니므로
`Partial`을 유지하고, Accepted decision contract를 implemented product command라고 표현하지 않습니다.

## 테스트 증거

- Local/contract evidence:
  [EVID-20260810-024](../docs/status/TEST_EVIDENCE.md#evid-20260810-024--gdj-0021-project-linked-migration-check-compatibility-contracts)
- Hosted implementation-head evidence:
  [EVID-20260810-025](../docs/status/TEST_EVIDENCE.md#evid-20260810-025--gdj-0021-github-hosted-10-job-implementation-head-ci)
- Hosted completion-documentation-head evidence:
  [EVID-20260810-026](../docs/status/TEST_EVIDENCE.md#evid-20260810-026--gdj-0021-github-hosted-completion-documentation-head-10-job-ci)
- Final implementation commit:
  `codex/revision-fenced-migration-lifecycle@84ddf109c04acd72992b816aa72140c6e748e5f0`
- Completion-documentation/hosted-tested commit:
  `codex/revision-fenced-migration-lifecycle@34ae58fc2490deb8f884a0b5591520b11bae8669`
- Project-check normal/race/CGO-disabled/vet/count-20, root `make ci`, exact Python 174/174,
  11-oracle/checksum/no-rewrite, static exit 1/10와 product exit 2/no actual이 모두 PASS했습니다.
- Reference는 11 set/115 contract/110 ordered cross-binding과 새 10 `oracle_locked`, product는
  불변 10 adapter/105 contract=`100 passing + 5 deviation`입니다.
- Independent contract/integration 및 filesystem/process/security final audit는 각각 P0/P1/P2/P3
  finding 0입니다.
- Draft PR #1 [run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)은
  implementation head에서 existing 2 + project-check 4 + SQLite 4의 exact 10 job을 모두
  성공시켰습니다. 네 Linux/macOS x64/arm64 coordinate의 normal/race/CGO-disabled/vet와 clean-worktree
  gate가 통과했습니다.
- Draft PR #1 [run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)은 exact
  completion-documentation head에서도 같은 10 job과 full/exact/checksum/no-rewrite gate를 모두
  성공시켰습니다.
- Not run: Windows와 PostgreSQL/MySQL/non-SQLite backend actual contract. 현재 EVID-026 append/status
  교정의 exact 8-file patch 자체의 hosted CI도 commit/push 전 `not run/pending`입니다.

## 위험과 rollback

- Test-only runner shape를 public API로 오해하면 구현 순서가 뒤집힐 수 있으므로 product path를
  금지하고 Accepted decision contract와 미구현 product를 명시적으로 구분합니다.
- Project binary는 user code를 link/run하므로 DB-free 의미를 GoDj-owned call 0으로 제한하고 arbitrary
  `init()` side effect를 숨기지 않습니다.
- Filesystem TOCTOU와 symlink escape는 path clean만으로 막지 못하므로 no-follow observation과
  fail-closed test를 완료 조건으로 둡니다.
- Child cancellation/reap가 틀리면 process/temp artifact가 남을 수 있으므로 process-group fault test
  전에는 제품화를 진행하지 않습니다.
- Rollback은 GDJ-0021 reference/test/workflow와 completion 상태를 함께 되돌리고 ADR-0021을 새
  evidence 없이 Accepted로 남기지 않는 것입니다. 제품 code와 Accepted ADR-0019/0020은 변경하지
  않습니다.

## 다음 정확한 작업

통합 담당자는 EVID-026 append/status 교정의 exact 8-file patch를 Markdown/frontmatter/link/
exact-scope 검사 뒤 같은 Draft PR #1에 commit/push하고 그 **evidence-patch exact head**의 10-job CI를
별도로 기록해야 합니다. 현재 active/ready work는 없습니다. 다음 product slice를 시작하려면 public
CLI/project API와 production runner 경계를 별도 work/ADR로 activation하고, 이 test-only proof를
제품 package로 조용히 승격하지 않습니다.

## 결과와 인수인계

GDJ-0021은 완료됐습니다. Independent MIG-065..074 artifact와 test-only proof는 exact
11 reference set/115 contract/110 cross-binding 및 새 10 `oracle_locked`를 만족했고, Draft PR #1의
implementation head 10-job matrix까지 통과했습니다. ADR-0021은 Accepted입니다.

제품은 불변 10 adapter/105 contract의 `100 passing + 5 deviation`이며 전역 `godj migrations check`,
public project package, production project runner, DB-aware migration check와 non-SQLite backend는
구현되지 않았습니다. Q-010/Q-012는 `Partial`입니다. Exact 16-file completion-documentation head의
10-job hosted CI도 별도 run으로 통과했습니다. 현재 EVID-026 append/status 교정의 exact 8-file patch
자체는 아직 `not run/pending`이며 completion-documentation-head run을 재귀 사용하지 않습니다.
