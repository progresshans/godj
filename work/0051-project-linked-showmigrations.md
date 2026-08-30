---
id: GDJ-0051
status: active
updated: 2026-08-30
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "39a5ce5f319c690508cd258f80082bd5f5a31216"
depends_on: ["GDJ-0014", "GDJ-0015", "GDJ-0018", "GDJ-0022", "GDJ-0038", "GDJ-0049", "GDJ-0050"]
contracts: ["MIG-111..MIG-118", "Q-010", "Q-012"]
allowed_paths:
  - "cmd/godj/**"
  - "project/**"
  - "migrations/**"
  - "db/sqlite/**"
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
  - "conformance/projectshowmigrationsproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0015-recorder-backed-applied-state.md"
  - "docs/adr/0053-project-linked-read-only-migration-status.md"
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
  - "work/0051-project-linked-showmigrations.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0051 — Project-linked Showmigrations

## 사용자에게 보이는 결과

사용자는 migration 생성·적용 전에 별도 write 없이 loaded catalog와 현재 database recorder가 보는 상태를 확인합니다.

```text
godj showmigrations
godj showmigrations --project ./godj.toml
```

첫 범위는 Django의 기본 list 경험을 따르는 deterministic plain-text status입니다.

```text
authors
 [X] 0001_author
blog
 [ ] 0001_article
```

## 목표

- Existing project selector와 declaration package를 재사용해 exact 두 public argv를 제공합니다.
- Complete definition catalog를 backend open 전에 strict load합니다.
- Project-owned backend에서 revision-fenced read-only session을 한 번 열고 applied-history snapshot을 정확히 한 번 읽은 뒤
  session과 acquired backend를 각각 정확히 한 번 닫습니다.
- Core graph가 known history를 검증하고 app별 known migration을 dependency-valid deterministic order로 열거합니다. 전체 app
  출력 순서는 global dependency order를 주장하지 않습니다.
- Known applied/unapplied는 `[X]`/`[ ]`, definition이 없는 recorded identity는 `[?]`로 명시해 숨기지 않습니다.
- Known과 unknown row가 모두 없는 전체 empty result는 GoDj-owned `(no migrations)` 한 줄로 게시합니다.
- Schema, recorder, revision metadata와 application table mutation이 0임을 SQLite/PostgreSQL에서 검증합니다.
- 별도 current-only strict private protocol, bounded result와 secret-free failure taxonomy를 둡니다.
- Pinned Django 6.1의 list semantics와 GoDj의 unknown-history fail-visible 결정을 MIG-111..118로 게시합니다.

## 비목표

- app filter, `--list`, `--plan`, verbosity와 applied timestamp
- replacement/squash title, transitional `[-]` recorder state와 zero-migration app별 `(no migrations)` heading
- `sqlmigrate`, SQL rendering/capture와 backend dry-run compiler
- named/zero target, reverse 실행, `--fake`, `--check`와 repair/adoption
- multi-DB alias/router, distributed snapshot 또는 status 이후 writer까지 유지되는 lock
- Definition/Schema IR/ProjectState/generated ABI 변경
- destructive/rename/alter/custom/data operation과 migration batching/merge
- Form/Admin/API 두 번째 모델 generalization, JWT/OAuth, Realtime
- Q-019 commit-unknown retained-resource 정책

## 결정할 경계

### Core inspection

- Phase B에서 raw `AppliedState` 조회를 새로 게시하지 않고, `Planner.Statuses`가 actual applied snapshot을 받아 fresh
  `[]MigrationStatusEntry`를 반환하며 loader-authorized `LoadedDefinitionSet.Statuses`가 이를 감싸는 경계로 결정·구현했습니다.
  Phase A의 남은 authority/reference lock은 이 API의 관찰 의미를 검증하며 API 형태를 다시 결정하는 단계가 아닙니다.
- Listing은 actual applied snapshot에 대해 `Planner.CheckHistory`를 먼저 실행합니다. Known child가 applied이고 known parent가
  빠진 history는 stdout 0으로 fail-closed합니다.
- Graph에 없는 valid applied identity는 existing lifecycle처럼 history 자체를 invalid로 만들지 않지만, 출력에서 숨기지 않습니다.
  Unknown row는 known topology에 끼워 넣지 않고 같은 app의 known rows 뒤에서 name 순으로 표시합니다.
- App group은 known/unknown app 합집합의 label 순이고, app 안 known row는 dependency-valid canonical order입니다. Unknown-only
  app도 heading과 name-sorted `[?]` tail을 표시합니다. 이 app grouping은 global cross-app dependency order가 아닙니다.
- 이 값은 한 시점의 read-only snapshot입니다. 반환 뒤 concurrent migrator에 대한 freshness authority가 아닙니다.

### Project/backend ownership

```text
global argv validation
→ retained project selection
→ private workspace build
→ showmigrations-only private request
→ copied static + discovered file source load exactly once
→ OpenMigrationBackend exactly once
→ RevisionFencedBackend.OpenRevisionFencedSession exactly once
→ RevisionFencedSession.ReadAppliedMigrations exactly once
→ graph/history/listing validation
→ revision session Close exactly once
→ backend.Close exactly once
→ bounded structured response
→ global deterministic text rendering
```

- `project.MigrationBackend`의 existing `RevisionFencedBackend` 경계를 그대로 사용합니다. Public interface method를 추가하지
  않으며 pre-release source-breaking widening도 만들지 않습니다.
- Read-only command는 revision-fenced session을 열지만 migration transaction은 시작하지 않습니다. Missing recorder는 canonical
  empty history이며 recorder/control table을 만들지 않습니다.
- PostgreSQL은 existing repeatable-read snapshot, SQLite는 existing atomic revision/control snapshot을 재사용합니다. 두 backend
  모두 current control shape, orphan recorder, fingerprint와 2,048-record bound를 fail-closed하게 확인합니다.
- Definition load failure는 backend open 0입니다. Read/history/listing failure는 sanitized category/code만 public stderr에 게시합니다.
- Revision-fence adoption-required, stale, contended와 integrity는 기존 migration taxonomy의 capability, conflict,
  transaction과 history category/code를 각각 보존합니다. 알 수 없는 fence kind만 integrity로 fail-closed합니다.
- Backend close failure는 successful listing을 폐기하고 nonzero closed failure가 됩니다. Raw driver cause, DSN, SQL, source bytes와
  project secret은 wire/stdout/stderr/artifact에 포함하지 않습니다.
- Show runner는 16 MiB stdout cap과 2초 post-exit process-group grace를 사용합니다. Direct child가 종료돼도 descendant가
  response pipe나 같은 process group을 유지하면 bounded cleanup 뒤 group을 제거하며 무기한 drain을 허용하지 않습니다.

### Private response and public output

- Existing check/migrate/makemigrations protocol bytes는 바꾸지 않고 별도 showmigrations protocol v1을 추가합니다.
- Response row는 raw UTF-8 app/name/status만 포함하며 status는 `applied`, `unapplied`, `unknown`의 closed enum입니다.
- Definition과 history는 각각 current 2,048-record bound를 지키고, combined row/UTF-8/response byte 상한을 protocol에서 다시
  검증합니다. Protocol은 app grouping, duplicate와 unknown-tail order를 검증하고, linked core가 known dependency order를
  소유합니다. Truncated, duplicate/unknown JSON key와 trailing bytes는 거부합니다.
- Global CLI는 child가 미리 만든 terminal text를 신뢰하지 않고 validated rows를 canonical text로 한 번 렌더링합니다. 모든
  app/name은 Go graphic escape body로 injective하게 표시하고, app heading의 첫 rune가 Unicode whitespace이면 강제 hex escape해
  row prefix와 시각적으로 혼동되지 않게 합니다. Common ASCII/Unicode identifier는 그대로 보입니다.
- 성공은 검증·렌더링된 newline-terminated canonical payload를 stdout에 정확히 한 번 write 시도합니다. Final publication 전의
  논리적 실패는 stdout write 0과 `category/code\n` stderr 한 번 write 시도입니다. Terminal stdout writer 자체가 short/error를
  반환하면 이미 기록된 prefix는 회수할 수 없으며 retry나 두 번째 stderr publication 없이 internal exit 3으로 보고합니다.

## Django reference와 contract 계획

- Exact authority: Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  `django/core/management/commands/showmigrations.py::Command.show_list`.
- Django에서 가져오는 의미: app label별 grouping, `[X]`/`[ ]`와 app 내부 root-to-leaf list semantics.
- MIG-112..115는 portable `result`만 reference comparison 대상으로 둡니다. Django in-memory loader/recorder의
  fresh-instance counter는 Python authority test와 source audit에만 보존하고 oracle `db_state`/`metrics`로 게시하지 않으므로
  actual product가 그 구현 세부를 흉내 내어 통과할 수 없습니다.
  Durable no-mutation과 fresh-process proof는 SQLite/PostgreSQL product test가 실제 database/process snapshot으로 별도 강제합니다.
- GoDj-owned 의미: global empty `(no migrations)`, deterministic incomparable tie-break, strict project selection/private protocol,
  unknown/unknown-only app `[?]`, known inconsistent history fail-closed, point-in-time revision snapshot ownership과
  resource/redaction limits.
- Django `show_list`는 unknown recorder row를 숨기며 `check_consistent_history`를 호출하지 않습니다. 또한 Django recorder의
  반복 read는 snapshot authority가 아니므로 `[?]`, fail-closed history와 exact one revision-fenced read는 GoDj decision으로
  분리합니다.
- Django replacement/squash의 `[-]` 상태와 migrated app별 empty heading은 current Definition/catalog가 해당 identity를
  표현하지 않으므로 비범위입니다. Incomparable sibling의 exact 순서도 Django parity가 아니라 GoDj canonical tie-break입니다.
- SQL 문자열, ANSI style, verbosity timestamp와 Django internal loader 객체 identity는 비교하지 않습니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-111 | `empty_catalog` | GoDj-owned 성공, stdout `(no migrations)\n`, backend/session open-read-close 각 1, mutation 0 |
| MIG-112 | `fresh_unapplied` | app/name canonical list와 known rows `[ ]` |
| MIG-113 | `applied_prefix` | prefix `[X]`, tail `[ ]`, dependency order와 mutation 0 |
| MIG-114 | `fully_applied_restart` | fresh process에서도 모든 known row `[X]`, output byte-identical |
| MIG-115 | `cross_app_branch_order` | app grouping은 label 순, 각 app 내부는 dependency-valid이며 global topology를 주장하지 않음 |
| MIG-116 | `unknown_record_visible` | valid unknown/unknown-only app identity를 `[?]`로 표시하고 known rows를 보존 |
| MIG-117 | `inconsistent_known_history` | stdout 0, structured history failure, schema/recorder/revision mutation 0 |
| MIG-118 | `project_boundary` | invalid argv/load는 open 0; success는 outer/session open 1, read 1, session/outer close 1; partial acquire/read/close/cancel도 exact cleanup/redaction |

Activation에서는 위 계약이 `planned, not run`이었습니다. Phase A는 reference artifact만 `oracle_locked`로 고정했으며
product adapter가 같은 observation을 통과하기 전에는 `passing`으로 올리지 않습니다. Q-010/Q-012는 이 packet이 완료돼도
semver/upgrade, target/reverse, destructive/custom/data 범위 때문에 `Partial`로 유지합니다.

## 단계

- [x] Phase A — Django/GoDj authority audit와 MIG-111..118 reference-only artifact lock
- [x] Phase B — strict private protocol, linked read-only runner와 global command unit/race/CGO0
- [x] Phase C — SQLite actual fresh/prefix/full/unknown/inconsistent/no-mutation와 external project process flow
- [ ] Phase D — PostgreSQL 17.10 normal/race/CGO0, product actual registration과 independent audit
- [ ] Phase E — affected/full milestone gates, source-bound attestation, exact submitted-head Hosted와 terminal docs

## 현재 checkpoint

- Baseline `39a5ce5f319c690508cd258f80082bd5f5a31216`은 clean GDJ-0050 terminal documentation head입니다.
- GDJ-0050/ADR-0052/MIG-099..110은 completed/Accepted/hosted-verified입니다.
- Activation audit에서 core Article/blog identity special-case는 발견되지 않았습니다.
- `showmigrations`에 필요한 definition clone, planner/history validation과 SQLite/PostgreSQL read-only reader는 이미 존재합니다.
- `sqlmigrate`는 pure compile-without-execute port가 없어 이번 packet에서 분리했습니다.
- Implementation checkpoint `294e7e26b6f92f18d5bd8edad7e0a51e03243ad0`, tree
  `3a834f477f007e5820ab4a31423690965a1b560b`은 loader-authorized core status API, strict private v1 wire,
  exact global/project dispatch, one revision-session/read/close ownership, bounded SQLite snapshot, closed revision taxonomy,
  injective terminal identity escape와 bounded descendant process-group cleanup을 구현했습니다.
- [EVID-154](../docs/status/TEST_EVIDENCE.md#evid-20260830-154--gdj-0051-activation-and-phase-b-read-only-core-checkpoint)는
  affected normal/race/CGO0/vet와 최종 changed-package refreeze, 독립 에이전트 3개가 수행한 4개 집중 감사 패스의 최종
  P0..P3=`0`을 기록합니다.
- Phase A는 MIG-111..118 manifest/NI/oracle을 각각 5,311/1,566/39,478 bytes와 SHA-256
  `3b1c8693...e6fc`/`0dd4dd08...6cc6`/`5a7a7827...355f`로 고정했습니다. MIG-112..115는 portable result만 Django
  reference와 비교하고, durable no-mutation/fresh-process는 product black-box proof가 소유합니다.
- Reference aggregate는 25 sets/281 contracts/600 ordered bindings=`237 passing + 24 deviation + 20 oracle_locked`이고
  product는 23 adapters/261 contracts=`237 passing + 24 deviation`으로 불변입니다. MIG-075..086과 MIG-111..118만
  reference-only locked/unregistered입니다.
- Exact Python suite 291 tests/21 skips, semantic inventory 281/971,815 bytes/SHA-256
  `7c76a6cf...1784`, Go protocol, 50개 conformance check, oracle regeneration/checksum과 독립 P0/P1=`0` 감사를
  통과했습니다. Workflow/Makefile source 변경으로 기존 PostgreSQL source-bound attestation은 의도대로 stale하며 Phase E에서
  product source가 얼어붙은 뒤 한 번 재캡처합니다.
- Phase C checkpoint `22e5c01715ed9129d975b34a81f19b5b5f211962`, tree
  `e6fbf3154e2d6e20f13d4a7f37812fffa90c0aa0`은 repository-external public Go module에서 actual global binary,
  fresh project-runner OS process와 SQLite를 연결해 MIG-111..117의 empty/fresh/prefix/restart/cross-app/unknown/inconsistent
  결과와 database byte/schema/table/history/revision no-mutation을 검증했습니다. MIG-118 external black-box는 invalid argv,
  invalid definition과 success ownership을 직접 검증했으며, 나머지 13 fault case는 Phase D actual adapter 등록 전에 existing
  Phase B fault harness에 oracle-blind하게 연결해야 합니다.
- [EVID-156](../docs/status/TEST_EVIDENCE.md#evid-20260830-156--gdj-0051-phase-c-external-sqlite-lifecycle-checkpoint)은
  Phase C normal/race/CGO0/vet, Linux amd64/386 compile-only, selector/protocol/gofmt/diff gate와 독립 semantic/CI 감사를
  기록합니다. Portable race outer timeout은 이전 Hosted 31분 44초와 새 package race 2분 38초를 근거로 40분에서 45분으로만
  보강했습니다. Separate job 증설이나 검증 축소는 하지 않았습니다.
- 다음 exact 작업은 Phase D PostgreSQL 17.10 actual, MIG-111..118 oracle-blind product adapter와 exact 16-case MIG-118
  boundary publication입니다. Phase A reference lock이나 Phase C partial boundary를 product passing으로 오해하지 않습니다.
