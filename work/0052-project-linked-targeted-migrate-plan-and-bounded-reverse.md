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

- Exact 여덟 public argv family만 수용하고 invalid/permuted/app-only/prefix target은 project discovery와 backend I/O 전에
  거부합니다.
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
- Request/response는 duplicate/unknown keys, trailing bytes, noncanonical numbers, invalid UTF-8, invalid enum과 mode/result mismatch를
  거부합니다.
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

- Exact authority: Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, migrate command/loader/executor graph
  semantics. Django에서 exact named target, zero target, plan presentation과 reverse dependency 의미를 관찰합니다.
- GoDj-owned: exact argv grammar, current-only private protocol, structured JSON plan, known-app zero target, one fenced history snapshot,
  preview-not-authority, commit-unknown/no-retry, close/publication/resource/redaction 경계.
- SQL 문자열과 Django human-readable operation description은 비교하지 않습니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-119 | `target_argv_and_pre_io_rejection` | exact 여덟 argv, invalid/permuted/app-only/prefix는 discovery/build/open 0 |
| MIG-120 | `named_forward_closure` | target과 missing ancestors만 canonical forward, unrelated branch 보존 |
| MIG-121 | `named_reverse_descendants` | target/direct cross-app child retained, same-app descendants와 그 descendants의 cross-app dependents reverse |
| MIG-122 | `app_zero_cross_app_dependents` | known app roots와 cross-app dependent reverse, unrelated applied branch 보존; DEV-0002 order 재사용 |
| MIG-123 | `target_noop_and_legacy_zero` | applied leaf/no-op, plain unknown `ZeroTarget` empty 보존, public known-zero unknown은 target-not-found, Begin 0 |
| MIG-124 | `plan_exact_and_no_mutation` | exact rows/empty JSON, Begin 0, schema/recorder/revision/application mutation 0 |
| MIG-125 | `preview_drift_fresh_execute` | preview 뒤 history drift, execute가 fresh snapshot/replan하며 preview token을 받지 않음 |
| MIG-126 | `reverse_middle_failure_resume` | committed reverse prefix만 durable, fresh process resume가 remaining reverse를 정확히 실행 |
| MIG-127 | `reverse_commit_outcomes` | unknown no-retry, rolled-back retryable state, committed cleanup preserves committed history |
| MIG-128 | `project_protocol_and_ownership` | v2 strict wire, load-before-open, one-open/one-mode/one-close, cancel/partial output/redaction/resource bounds |

Activation에서는 MIG-119..128 모두 `planned, not run`입니다. Phase A reference artifact를 먼저 `oracle_locked`로 고정하고
actual adapter가 같은 observation을 통과하기 전에는 `passing`으로 올리지 않습니다. Q-010/Q-012/Q-019는 이 packet이
완료돼도 general upgrade/repair/destructive operation과 retained-resource policy 때문에 `Partial`/open을 유지합니다.

## 단계

- [ ] Phase A — Django/GoDj authority audit와 MIG-119..128 reference-only artifact lock
- [ ] Phase B — known-app target, shared lifecycle preparation, public Plan과 strict private v2/global argv
- [ ] Phase C — repository-external SQLite named/zero/plan/reverse failure-resume product flow
- [ ] Phase D — PostgreSQL 17.10 normal/race/CGO0, oracle-blind product registration과 independent audit
- [ ] Phase E — affected/full milestone gates, source-bound attestation, exact submitted-head Hosted와 terminal docs

## 현재 checkpoint

- Baseline `1d37272f4062365416536d4459a5294df4b06d03`, tree
  `97e091d1b3f2d4fee82c285d60fea25de0f3c41d`은 clean GDJ-0051 terminal documentation head입니다.
- GDJ-0051/ADR-0053/MIG-111..118은 completed/Accepted/hosted-verified입니다.
- Existing Planner and loaded Executor already implement library-level caller-ordered multi-target named/zero execution, reverse dependency closure,
  dry validation, per-step fenced transaction, rollback and commit-unknown semantics. This packet publishes a narrower exact-one-target CLI
  and refactors shared preparation without changing backend schema or transaction interfaces.
- Phase A reference lock and Phase B product implementation are not yet executed. No migration protocol v2 or public `--plan` support exists
  at activation.
