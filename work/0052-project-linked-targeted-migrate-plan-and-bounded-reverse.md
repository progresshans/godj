---
id: GDJ-0052
status: active
updated: 2026-08-30
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "1d37272f4062365416536d4459a5294df4b06d03"
depends_on: ["GDJ-0014", "GDJ-0018", "GDJ-0036", "GDJ-0049", "GDJ-0050", "GDJ-0051"]
contracts: ["MIG-119..MIG-128", "Q-010", "Q-012", "Q-019"]
allowed_paths:
  - "cmd/godj/**"
  - "project/**"
  - "migrations/**"
  - "db/sqlite/**"
  - "db/postgres/**"
  - "internal/projectcheck/**"
  - "internal/compiletest/**"
  - "examples/**"
  - "conformance/contracts/**"
  - "conformance/fixtures/**"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/projectmigrateproduct/**"
  - "conformance/projectmigratetargetproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0054-project-linked-targeted-migration-plan-and-reverse-safety.md"
  - "docs/adr/README.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/DEVIATIONS.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0052-project-linked-targeted-migrate-plan-and-bounded-reverse.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0052 — Project-linked Targeted Migrate, Plan and Bounded Reverse

## 사용자에게 보이는 결과

Latest-only `migrate`를 보존하면서 exact migration target과 실행 전 read-only plan을 추가합니다.

```text
godj migrate
godj migrate --project ./godj.toml
godj migrate --plan
godj migrate --plan --project ./godj.toml
godj migrate blog 0001_article
godj migrate blog 0001_article --project ./godj.toml
godj migrate blog 0001_article --plan
godj migrate blog 0001_article --plan --project ./godj.toml
godj migrate blog zero
```

`zero`는 exact lowercase reserved target name입니다. Named target은 해당 migration을 적용 상태로 남기고, 이미 적용된
target의 same-app descendants와 그 때문에 필요 없어진 applied dependents를 dependency-safe reverse order로 되돌립니다.
App-zero는 해당 app의 모든 applied roots와 cross-app applied dependents를 되돌립니다.

Plan mode는 canonical JSON 한 줄만 출력합니다.

```json
{"plan":[{"app":"blog","name":"0002_editor","direction":"backward"}]}
```

빈 plan은 `{"plan":[]}`입니다. Existing execute 성공 JSON은 byte shape를 바꾸지 않습니다.

## 목표

- Exact 여덟 public argv family만 수용하고 invalid/permuted/app-only 형태는 project discovery와 backend I/O 전에
  거부합니다. 문법상 유효한 prefix-looking 이름은 catalog를 읽기 전에는 prefix인지 알 수 없으므로 exact name으로만 조회하고,
  exact identity가 없으면 `plan/target_not_found`로 실패합니다.
- Existing `LifecycleRequest`, immutable graph, history check, revision-fenced session과 step별 transaction을 재사용해 named
  forward/reverse와 known-app zero를 public CLI에 연결합니다.
- `Executor.Plan(ctx, LoadedDefinitionSet, LifecycleRequest) ([]PlanStep, error)`를 추가해 execute와 같은 current history
  snapshot, graph validation, historical reconstruction, whole-plan dry validation과 capability preflight를 공유합니다.
- Preview는 transaction을 시작하거나 recorder/schema/application/revision을 수정하지 않습니다. Backend/session close failure는
  이미 계산한 plan을 폐기하고 실패합니다.
- Preview 결과나 token을 실행 API 입력으로 받지 않습니다. Execute는 항상 새 revision-fenced session에서 history를 다시 읽고
  fresh plan을 만들어 drift를 반영합니다.
- Existing `ZeroTarget("unknown")`의 accepted empty-plan library 계약은 보존하고, CLI의 app-zero가 unknown app을 명시적으로
  `plan/target_not_found`로 거부하도록 `KnownAppZeroTarget`을 별도 target kind로 추가합니다.
- Current-only strict private migrate protocol v2가 execute/plan과 latest/named/zero target을 명시적으로 전달하고 response mode를
  결과 union에 결합합니다. v1 dual reader나 legacy private argument는 남기지 않습니다.
- Middle reverse failure는 committed durable prefix만 남기고 fresh process resume이 새 history에서 정확히 이어집니다.
- Reverse commit outcome unknown은 자동 retry하지 않고, rollback/committed cleanup 결과와 우선순위를 기존 taxonomy로 보존합니다.

## 비목표

- App-only latest target, migration-name prefix resolution, option 순열, public CLI 여러 target과 multiple database alias
- `sqlmigrate`, SQL text/operation description/capture, interactive confirmation
- `--fake`, `--fake-initial`, `--check`, `--prune`, repair/adoption과 recorder 직접 편집
- squash/replacement/merge/optimizer, conflicting leaf selection과 ambiguous prefix UX
- destructive/rename/alter/custom/data operation writer 또는 autodetection 확대
- plan token, optimistic execution authorization, distributed snapshot/lock 유지
- MySQL/Windows, non-cooperative writer와 arbitrary direct SQL
- Form/Admin/API second-model generalization, JWT/OAuth, Realtime
- General semver/upgrader와 Q-019 retained-resource policy 변경

## 결정할 경계

### Public target and argv grammar

다음 여덟 형태만 허용합니다.

```text
godj migrate
godj migrate --project PATH
godj migrate --plan
godj migrate --plan --project PATH
godj migrate APP EXACT_NAME
godj migrate APP EXACT_NAME --project PATH
godj migrate APP EXACT_NAME --plan
godj migrate APP EXACT_NAME --plan --project PATH
```

- `APP`/`EXACT_NAME`은 non-empty valid UTF-8이며 각각 1 MiB 이하, 합계와 전체 request는 16 MiB 이하입니다.
- `EXACT_NAME == "zero"`만 app-zero입니다. 다른 모든 이름은 exact named lookup이며 prefix나 case folding을 하지 않습니다.
- `migrate APP`, `migrate --project PATH --plan`, repeated flags, `--`와 unknown flags는 `command/invalid_arguments`입니다.
- Invalid argv는 CWD/project descriptor/source/backend/process를 관찰하지 않습니다.

### One preparation kernel, two terminal modes

```text
strict loaded snapshot validation
→ immutable definition clone/reconstructor
→ one revision-fenced session
→ one applied-history read
→ history/resource validation
→ target resolution and Planner.Plan
→ historical before-state reconstruction
→ whole-plan dry materialization and capability preflight
                     ├─ plan: detached []PlanStep, no BeginMigration
                     └─ execute: rematerialize and fenced step transactions
```

- `prepareLoadedLifecycle`은 validated plan과 execution-only materialization authority를 내부 값으로 반환합니다.
- `executePreparedLifecycle`만 `BeginMigration`을 호출합니다. Public `Plan`은 step key/direction을 fresh slice로 복사합니다.
- Session close는 두 mode 모두 exact once입니다. Plan close failure는 `(nil, error)`이고 execute는 기존 state/error precedence를
  유지합니다.
- Latest request는 canonical app leaves로 정규화됩니다. Public CLI/private wire의 targeted request만 exact one target이며
  existing library `TargetedLifecycleRequest(first, rest...)`의 caller-ordered multi-target/mixed-plan 계약은 Plan/Migrate 모두
  보존합니다. Empty/no-op plan도 session/history validation과 close를 생략하지 않습니다.
- Same snapshot에서 semantic dry/capability validation을 통과하지 못한 step은 plan으로도 게시하지 않습니다. Preview는
  SQL dry-run이나 transaction-local physical schema/cardinality preflight가 아니며 실제 성공을 보장하지 않습니다.

### Target semantics

- Named unapplied target: target과 모든 missing ancestors를 forward topological order로 실행합니다.
- Named applied target: target 자체와 target에 직접 의존하는 cross-app branch는 유지하고, same-app applied descendants를
  reverse합니다. 제거되는 same-app descendant에 의존하는 cross-app applied dependents만 graph closure에 따라 함께 reverse합니다.
- Known app zero: 해당 app이 graph에 존재해야 하며 all applied roots와 cross-app dependents를 reverse합니다.
- Unknown valid recorded identities는 existing history contract대로 plan topology에 포함하지 않지만 known history inconsistency는
  target lookup보다 먼저 fail-closed합니다.
- Named unknown과 known-app zero unknown은 `CategoryPlan/CodeTargetNotFound`입니다. Existing plain `ZeroTarget` unknown-empty
  behavior는 library compatibility를 위해 보존합니다.

### Strict current-only private wire

Request examples:

```json
{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}
{"protocol_version":2,"command":"migrations.migrate","mode":"plan","target":{"kind":"named","app":"blog","name":"0001_article"}}
{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"zero","app":"blog"}}
```

- Private argv는 `__godj_project_migrate_runner_v2` 하나뿐입니다.
- Request/response는 duplicate/unknown keys, trailing bytes, noncanonical numbers, invalid UTF-8, unpaired UTF-16 surrogate
  escape, invalid enum과 mode/result mismatch를 거부합니다. Wire identity를 replacement rune으로 조용히 정규화하지 않습니다.
- Execute success result의 inner summary는 existing source/definition/digest shape를 보존합니다.
- Plan success는 최대 2,048 unique rows이며 각 row는 `{app,name,direction}` exact keys와 forward/backward closed enum을 씁니다.
- Request hard cap은 16 MiB입니다. Response/public plan hard cap은 minimal JSON escaping의 최악 6배 expansion과
  2,048-row framing을 포함하도록 101 MiB입니다. Identity 문자열은 existing 1 MiB bound와 aggregate 16 MiB budget을 다시
  검증합니다.
- Canonical encoder는 arbitrary identity를 JSON string으로 직접 minimal JSON escape하며 `%q`나 HTML-escaping default에 wire
  identity를 맡기지 않습니다. Plan keys는 loaded source identity의 unique subset이므로 16 MiB raw identity envelope의
  `6 * bytes + row framing` 최악값도 101 MiB response cap 안에 들어야 합니다.
- Load-before-open, one outer open/close, process-group cleanup, partial-output와 detail-free error taxonomy는 existing migrate owner가
  그대로 소유합니다.

## Django reference와 contract 계획

- Exact authority: Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`의 real
  `MigrationExecutor.migration_plan()` ordered plan semantics를 MIG-120..122에서만 관찰합니다.
- GoDj-owned: exact argv grammar, current-only private protocol, structured JSON plan, known-app zero target, one fenced history snapshot,
  preview-not-authority, commit-unknown/no-retry, close/publication/resource/redaction 경계.
- SQL 문자열과 Django human-readable operation description은 비교하지 않습니다.
- Django의 app-zero reference graph에서 `beta.0001`과 `alpha.0003`은 비교 불가능(incomparable)한 reverse sibling이며 exact
  관찰 순서는 `B1, A3, A2, A1`입니다. Existing DEV-0002 GoDj canonical order `A3, A2, B1, A1`과 membership 및
  dependency-safety는 같지만 byte order는 다릅니다. Reference artifact는 Django 순서를 그대로 보존하고, actual product
  publication에서는 MIG-122를 DEV-0002의 bounded sparse deviation으로 명시해 이 차이를 정렬로 숨기지 않습니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-119 | `target_argv_and_pre_io_rejection` | exact 여덟 argv, invalid/permuted/app-only는 discovery/build/open 0; valid prefix-looking token은 exact-only lookup |
| MIG-120 | `named_forward_closure` | target과 missing ancestors만 canonical forward, unrelated branch 보존 |
| MIG-121 | `named_reverse_descendants` | target/direct cross-app child retained, same-app descendants와 그 descendants의 cross-app dependents reverse |
| MIG-122 | `app_zero_cross_app_dependents` | known app roots와 cross-app dependent reverse, unrelated applied branch 보존; Django exact `B1, A3, A2, A1`, product DEV-0002 sparse order deviation |
| MIG-123 | `target_noop_and_legacy_zero` | applied leaf/no-op, plain unknown `ZeroTarget` empty 보존, public known-zero unknown은 target-not-found, Begin 0 |
| MIG-124 | `plan_exact_and_no_mutation` | exact rows/empty JSON, Begin 0, schema/recorder/revision/application mutation 0 |
| MIG-125 | `preview_drift_fresh_execute` | preview 뒤 history drift, execute가 fresh snapshot/replan하며 preview token을 받지 않음 |
| MIG-126 | `reverse_middle_failure_resume` | committed reverse prefix만 durable, fresh process resume가 remaining reverse를 정확히 실행 |
| MIG-127 | `reverse_commit_outcomes` | unknown no-retry, rolled-back retryable state, committed cleanup preserves committed history |
| MIG-128 | `project_protocol_and_ownership` | v2 strict wire, load-before-open, one-open/one-mode/one-close, cancel/partial output/redaction/resource bounds |

Activation에서는 MIG-119..128 모두 `planned, not run`이었고, Phase A source `db8fc418f4627fbe364360ae05ec5e015ad25ed4`에서
reference artifact를 `oracle_locked`로 고정했습니다. Actual adapter가 같은 observation을 통과하기 전에는 `passing`으로
올리지 않습니다. Q-010/Q-012/Q-019는 이 packet이
완료돼도 general upgrade/repair/destructive operation과 retained-resource policy 때문에 `Partial`/open을 유지합니다.

## 단계

- [x] Phase A — Django/GoDj authority audit와 MIG-119..128 reference-only artifact lock
- [x] Phase B — known-app target, shared lifecycle preparation, public Plan과 strict private v2/global argv
- [x] Phase C — repository-external SQLite named/zero/plan/reverse failure-resume product flow
- [x] Phase D — PostgreSQL 17.10 normal/race/CGO0, oracle-blind product registration과 independent audit
- [ ] Phase E — affected/full milestone gates, source-bound attestation, exact submitted-head Hosted와 terminal docs

## 현재 checkpoint

- Baseline `1d37272f4062365416536d4459a5294df4b06d03`, tree
  `97e091d1b3f2d4fee82c285d60fea25de0f3c41d`은 clean GDJ-0051 terminal documentation head입니다.
- GDJ-0051/ADR-0053/MIG-111..118은 completed/Accepted/hosted-verified입니다.
- Existing Planner and loaded Executor already implement library-level caller-ordered multi-target named/zero execution, reverse dependency closure,
  dry validation, per-step fenced transaction, rollback and commit-unknown semantics. This packet publishes a narrower exact-one-target CLI
  and refactors shared preparation without changing backend schema or transaction interfaces.
- Phase B source checkpoint `cd499462c794c4e136e94bb5abc2121b98fb722d`, tree
  `580ae7a8186e3d668c5aab670f46398bb320b721`은 `KnownAppZeroTarget`, shared loaded lifecycle preparation,
  `Executor.Plan`, strict private migrate protocol v2, exact global argv/public plan, linked project ownership과 Article read-only
  plan smoke를 구현했습니다. 기존 project-migrate product suite와 affected normal/race/CGO-disabled/vet/count-10 및
  all-package compile-only gate가 통과했습니다. 독립 감사에서 찾은 unpaired surrogate identity collapse와 response-cap
  test false-green 가능성은 같은 source checkpoint 안에서 fail-closed scanner와 production stage-policy seam으로 교정했습니다.
  자세한 증거는 EVID-161에 있습니다.
- Phase A source checkpoint `db8fc418f4627fbe364360ae05ec5e015ad25ed4`, tree
  `639a7127022f3897ddc62ab24739c34e7fe4eb56`은 MIG-119..128 manifest/NI/oracle을 reference-only
  `oracle_locked`로 고정했습니다. Manifest/NI/oracle은 6,781/1,707/43,516 bytes와 SHA-256
  `d76a42f2a0fb4daa190d03f18d18707192c8b42881b94a1462b701a9d481947b`/
  `dfefb6fd6ca27e5e70dffea002fd07d801792ba7c6a83142dab18b969617bd44`/
  `dc688e27a727270594b32291e8cff83e1bd929af0a0fcd6fcf9b1f706dba9a7f`입니다. Shared 23-line
  `SHA256SUMS`는 2,177 bytes/SHA-256
  `00bd4d0d865ace8620bc577d84fd4198b5724360727117fd4998f0772460f331`, all-scenario semantic payload는
  291 scenarios/1,015,687 bytes/SHA-256
  `b3918c9d471cacd79ad9da0774618b0df085b6db71784a884c668703807790de`입니다. Reference는
  26 sets/291 contracts/650 ordered bindings=`245 passing + 24 deviation + 22 oracle_locked`로 늘었지만 product는
  24 adapters/269 contracts=`245 passing + 24 deviation`으로 불변입니다. Exact Python 305, portable Python 305,
  four-version semantic identity, protocol/conformance/oracle/checksum과 final P0..P3=`0` audit는
  [EVID-162](../docs/status/TEST_EVIDENCE.md#evid-20260830-162--gdj-0052-phase-a-reference-only-artifact-lock)에
  기록합니다.
- Phase C source checkpoint `5b8d48fb93151f4fb4d24323f59ace259f4bffd8`, tree
  `7df990a25ad5a3daeed9589dd33f960238fdb624`는 저장소 밖 public-only Go module에서 actual global `godj`, fresh
  linked child와 real SQLite를 사용해 exact 여덟 public argv, named forward/reverse, app-zero DEV-0002 product order,
  no-op/known-zero unknown, read-only exact plan, preview 뒤 fresh-history replan, middle reverse failure와 fresh-process
  resume를 검증했습니다. MIG-128은 load-before-open, outer-close suppression, backend-open redaction과 process cleanup의
  bounded Phase-C subset만 관찰합니다. MIG-127과 strict wire/resource/cancel/partial/short-write 전체는 Phase D 비주장입니다.
  Current-byte normal/race/CGO-disabled, vet, protocol, full compile-only, Linux/386 compile-only, workflow/YAML/selector와
  final independent P0..P3=`0` audit는
  [EVID-163](../docs/status/TEST_EVIDENCE.md#evid-20260830-163--gdj-0052-phase-c-external-sqlite-targeted-migrate-checkpoint)에
  기록합니다. MIG-119..128은 계속 reference-only `oracle_locked`/unregistered이고 aggregate와 Proposed ADR-0054는
  불변입니다. 다음 정확한 작업은 Phase D PostgreSQL 17.10, oracle-blind adapter, MIG-127/full MIG-128과 DEV-0002
  sparse publication입니다.
- Phase D source checkpoint `a92efb5f09eb4dcf3094fddf84a21ff65fa604f3`, tree
  `06f90a90eb61de13c234dfc2356b6b4ed085f087`은 MIG-119..128 열 개 계약을 production API와 실제
  process/transaction seam에서만 관찰하는 oracle-blind GoDj adapter로 등록했습니다. Adapter source는 oracle, manifest,
  not-implemented/deviation fixture와 Django bytes를 읽지 않으며, MIG-119..121/MIG-123..128은 passing, MIG-122의
  incomparable reverse sibling order만 DEV-0002 sparse deviation으로 게시합니다. Reference aggregate는 26 sets/291
  contracts=`254 passing + 25 deviation + 12 oracle_locked`, product aggregate는 25 adapters/279 contracts=
  `254 passing + 25 deviation`입니다.
  - MIG-127은 commit outcome unknown을 retry/rollback/success로 추측하지 않고, confirmed rollback과 committed cleanup
    failure를 포함한 세 실제 transaction outcome에서 history, retry, rollback과 publication을 결정적으로 관찰합니다.
    MIG-128은 strict private v2의 six ownership, 17 wire rejection, five resource-limit cases와 plan invariants를 실제
    production owner/parser bound에서 관찰하며 raw cause, runner stderr와 secret value를 public result에 게시하지 않습니다.
  - 초기 독립 감사는 P2 두 건과 P3 한 건을 찾았습니다. MIG-126 fresh resume와 published step identity를 첫 process의
    실제 durable record/begin/commit/rollback 관찰로 바꾸고, MIG-128 cancellation/partial-output/redaction/resource 증거를
    실제 child와 process-group lifecycle에서 수집하도록 교정했습니다. Default owner의 capped stdout을 raw bytes 없이
    증명하기 위해 internal `MigrateReport`에 retained-byte/truncated safe scalar만 추가했으며, exact-limit success와
    one-byte overflow rejection을 함께 관찰합니다. Cooperative cancellation 외에 SIGINT를 무시하는 실제 runner도 default
    grace 뒤 SIGKILL/direct reap/process-group absence까지 실행해 attempt maxima를 report에서 계산합니다. Target deviation
    fixture는 CI의 두 reference-artifact non-rewrite allowlist에도 명시해 intended DEV-0002 update만 허용했습니다. 후속
    source/behavior 재감사는 이 보강 뒤 P0..P3=`0`이었습니다.
  - Repository-external SQLite final-byte proof인
    `go test -run '^TestProjectLinkedTargetedMigrateSQLite$' -count=1 ./conformance/projectmigratetargetproduct`는 PASS
    (`526.760s`)였고 final focused SQLite run도 PASS (`24.092s`)였습니다. Local pinned PostgreSQL 17.10/UTF8/UTC에서는
    secret-free 환경 표기로 다음 exact selector를 normal/race/CGO-disabled에서 각각 통과했습니다. Normal
    `GODJ_TEST_POSTGRES_URL='<redacted>' GODJ_REQUIRE_POSTGRES=1 go test -run '^TestGlobalTargetedMigratePostgresLifecycle$' -count=1 ./conformance/projectmigratetargetproduct`
    은 PASS (`398.553s`), race
    `GODJ_TEST_POSTGRES_URL='<redacted>' GODJ_REQUIRE_POSTGRES=1 go test -race -run '^TestGlobalTargetedMigratePostgresLifecycle$' -count=1 ./conformance/projectmigratetargetproduct`
    는 PASS (`399.569s`), CGO-disabled
    `GODJ_TEST_POSTGRES_URL='<redacted>' GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -run '^TestGlobalTargetedMigratePostgresLifecycle$' -count=1 ./conformance/projectmigratetargetproduct`
    는 PASS (`397.906s`)였습니다. Workflow는 이 최소 selector를 사용하면서 기존 PostgreSQL 좌표와 합친 exact 23
    top-level sentinel inventory, required-run/no-skip와 PostgreSQL 17.10 fingerprint를 잠급니다. 이 23개는 Phase D
    local 실행 수가 아니라 Phase E Hosted용 inventory lock입니다.
  - Exact product comparison command인
    `go run ./conformance/cmd/godjcheck -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json -manifest conformance/contracts/migration-target-plan-manifest.json -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-target-plan-oracle.json -deviation-expected conformance/fixtures/godj-migration-target-plan-deviation-expected.json`
    은 `26.607s`에
    `GoDj observations match the reviewed product expectation for 10 contracts under DEV-0002`로 통과했습니다.
    `go test ./conformance/runners/godj -count=1` (`45.732s`), MIG-128 actual-boundary normal (`24.940s`), MIG-126/128
    actual-boundary race (`26.609s`), `go test ./internal/projectcheck -count=1` (`11.031s`), focused owner race
    (`1.456s`), source/transaction-identity guards (`0.459s`)와 두 affected package의 `go vet`도 PASS였습니다.
  - `make godj-conformance`는 새 target-plan publication까지 green이었고, 이후 source bytes가 바뀌어 stale해진 기존
    system-state source-bound attestation에서 의도대로 fail-closed했습니다. 이는 Phase D product failure가 아니며 Phase E의
    exact-source attestation recapture가 pending입니다. 또한 macOS에서 `GOOS=linux GOARCH=386` test binary를 `-exec`
    없이 잘못 실행한 시도는 `exec format error`였고 이를 제품 실패로 세지 않았습니다. Corrected Linux/386 compile-only는
    `-exec=/usr/bin/true`를 사용해 PASS했으며, corrected command form과 affected-package 결과는 EVID-164에 기록합니다.
  - Phase E local-final publication `88371448e88c2b22f8d4b7acd6e67635b9b49e71`, tree
    `d29b09b5a9c0a33365a3a0f8275c805aed749659`은 첫 Hosted diagnostic의 stale attestation/relation lock과
    PostgreSQL/targeted timeout 구조를 bounded하게 교정했습니다. Source freeze `554102e...`의 independent PostgreSQL A/B는
    exact 1,134 bytes/SHA-256 `abd409ec...2b89`, binding 267 files/3,388,048 bytes/`3af400bf...573d9`로 같았고
    checked publication과 byte-identical합니다. Full `make ci`, current 118-package Linux/386 compile-only, workflow-exact
    relation 955/955/0 inventory와 1,230-file `.git`-free archive의 targeted/show/writer public flows가 통과했습니다.
    [EVID-165](../docs/status/TEST_EVIDENCE.md#evid-20260830-165--gdj-0052-first-hosted-diagnostic-ci-isolation-and-frozen-local-final)이
    exact command, hash, timeout 분류와 non-claim을 기록합니다. Corrected exact submitted-head Hosted와 terminal status
    documentation은 아직 pending이므로 GDJ-0052는 계속 active, Phase E는 unchecked이며 ADR-0054도 Proposed를 유지합니다.
  - Submitted local-final `fbde4f777af62468003291cb9480339592a9219c`의 second Hosted
    [CI #188](https://github.com/progresshans/godj/actions/runs/33314164696)은 53 jobs=`47 success + 6 failure`, 572
    steps=`549 success + 6 failure + 17 post-failure skips`, cancellation 0으로 완료됐습니다. Five primary failures는
    portable normal/race의 duplicated targeted package 15분 timeout, macOS Intel targeted normal/CGO-disabled의 30분
    package timeout과 MIG-128의 test-local 90초 guard였으며 assertion/DB/transaction/cleanup mismatch는 없었습니다.
    `538102f895043b1dc73373c0367e8d724f164ff4`는 MIG-128에만 10분 scenario guard를 적용하고,
    source freeze `7859bd79fe89c7be4b3b5a4addc376d2e8aa9527`, tree
    `e7b7ec782e510c53872df4f06843923197971577`은 Hosted portable duplication을 제거하면서 full local `make ci`가
    별도 `targeted-migrate-product` normal/race/CGO-disabled를 계속 소유하게 합니다. Dedicated 12-lane은 non-Intel
    package/job 30/40분, Intel 45/55분을 사용하며 이번 correction에는 shard를 추가하지 않았습니다.
  - Publication `7d047aef6fdf04ed576ec5628003960d4c3360ae`, tree
    `21c0bd1565194f33a296c310fc3bfd707c805fef`은 freeze의 independent PostgreSQL A/B를 exact 1,134 bytes/SHA-256
    `ac46d9ba46ef30df3420ecff6a308110fe51aeb7dcfa90000d647f22eac9e893`, binding 267 files/3,388,510 bytes/
    `d041e1f4a16dfe46c5b1a1c7f378e56cd069a0e90ba20459c8551829bb3a482f`로 다시 게시했습니다. A/B/published,
    pre/post archive와 resource/secret checks가 일치했고 focused normal/race/CGO-disabled/vet, conformance/product comparison,
    format/generate/diff gates가 통과했습니다. Corrected full local `make ci`, Linux/386, relation/archive와 exact submitted-head
    Hosted는 아직 재실행하지 않았으므로 Phase E/ADR/work 상태는 그대로입니다. 자세한 증거는
    [EVID-166](../docs/status/TEST_EVIDENCE.md#evid-20260830-166--gdj-0052-second-hosted-timing-diagnostic-and-corrected-source-refreeze)에
    기록합니다.
