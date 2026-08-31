---
id: GDJ-0054
status: active
updated: 2026-08-31
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "1fbffa3b5ba8248dc5aa141212a7dad563827a7b"
depends_on: ["GDJ-0036", "GDJ-0038", "GDJ-0049", "GDJ-0050", "GDJ-0052", "GDJ-0053"]
contracts: ["MIG-129..MIG-138", "Q-010", "Q-012"]
allowed_paths:
  - "cmd/godj/**"
  - "project/**"
  - "migrations/**"
  - "db/sqlite/**"
  - "db/postgres/**"
  - "internal/projectcheck/**"
  - "internal/compiletest/**"
  - "examples/article/**"
  - "conformance/contracts/**"
  - "conformance/fixtures/**"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/projectsqlmigrateproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0055-project-linked-deterministic-migration-sql-projection.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/DEVIATIONS.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0054-project-linked-deterministic-sqlmigrate.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0054 — Project-linked Deterministic sqlmigrate

## 사용자에게 보이는 결과

정확히 지정한 migration 하나의 current forward SQL을 database를 열지 않고 출력합니다.

```text
godj sqlmigrate APP EXACT_NAME
godj sqlmigrate APP EXACT_NAME --project ./godj.toml
```

`zero`는 이 명령에서 reserved app-zero target이 아니라 literal migration name입니다. 출력은 statement body마다 정확히
`;\n`을 붙인 canonical SQL이고 empty migration은 stdout zero bytes입니다.

## 목표

- Exact 두 public argv만 허용하고 invalid/permuted/app-only/latest/reverse 형태는 project discovery, source build와 renderer
  접근 전에 거부합니다.
- Complete migration catalog를 current strict loader로 전부 읽고 graph, chronology와 identity를 검증한 뒤 exact
  `{app,name}`만 조회합니다. Prefix와 case folding은 지원하지 않습니다.
- Target의 dependency-before historical `ProjectState`를 순수하게 재구성하고, target definition의 ordered operations를
  deep-clone한 exactly-one forward intent로 materialize합니다.
- Migration identity와 forward direction을 잃지 않는 backend-neutral request를 SQLite/PostgreSQL SQL renderer에 전달합니다.
- Built-in renderer는 current `CreateModel`/`AddField` execution compiler의 identifier/type/default/relation authority를
  공유하되 database opener, session, applied history, recorder, revision fence, transaction과 schema editor는 호출하지 않습니다.
- 모든 statement와 aggregate output을 resource/UTF-8/canonical-shape 관점에서 완전히 검증한 뒤 stdout에 한 번만 쓰기를
  시도합니다. Logical failure는 SQL zero bytes와 stable redacted category/code만 게시합니다.
- SQLite와 PostgreSQL current profile, repository-external public project, fresh process, repeat/parallel determinism과
  cleanup/redaction/no-DB trace를 product observation으로 검증합니다.

## 비목표

- `Executor.Plan`, live applied history, database schema/catalog/profile/data/cardinality와 실제 migration 실행 가능성 검사
- latest/prefix/app-only/app-zero/multiple target, reverse SQL, `--backwards`, transaction wrapper와 color/verbosity option
- custom/data/executable/`RunSQL`/raw SQL operation, destructive/rename/alter writer 확대
- multi-database alias, MySQL/Windows, arbitrary third-party backend discovery
- database URL/credential/handle를 renderer configuration에 전달하거나 global CLI가 secret을 해석하는 구조
- Schema IR, Query AST, Migration Definition/ProjectState wire/version/digest, generated role ABI/golden 변경
- execution `MigrationCapabilities`, `RevisionFencedBackend`, `SchemaEditor` 또는 recorder semantics 변경
- custom renderer가 database/network/filesystem I/O를 하지 않는다는 강제, opener/renderer coherence의 framework 증명
- 출력 write의 OS-level atomicity. Terminal short write/error는 prefix를 노출할 수 있고 자동 retry하지 않습니다.

## 결정할 경계

### Exact argv와 precedence

다음 두 형태만 허용합니다.

```text
godj sqlmigrate APP EXACT_NAME
godj sqlmigrate APP EXACT_NAME --project PATH
```

- `APP`과 `EXACT_NAME`은 non-empty valid UTF-8이고 existing migration identity bounds를 만족해야 합니다.
- `zero`를 포함한 모든 valid name은 literal exact lookup입니다. Prefix, case folding과 reserved zero 의미가 없습니다.
- Invalid argv는 CWD/project descriptor/source/build/renderer/backend를 관찰하지 않습니다.
- Valid argv 뒤에는 complete source load/graph/chronology/target lookup이 renderer availability/configuration보다 먼저입니다.
  Constructor가 early error를 반환해 이 precedence를 뒤집지 않게 합니다.

### 순수 materialization과 identity-bearing forward request

Root migration core의 후보 public 경계는 다음 의미를 가져야 합니다.

```go
type ForwardMigrationSQLRequest struct {
    App    string
    Name   string
    Intent MigrationIntent
}

type MigrationSQLRenderer interface {
    RenderForwardMigrationSQL(context.Context, ForwardMigrationSQLRequest) ([]string, error)
}

func RenderMigrationSQL(
    context.Context,
    LoadedDefinitionSet,
    MigrationKey,
    backend.MigrationSQLRenderer,
) ([]string, error)
```

- Exact exported 이름과 package 위치는 Phase B source `f51ab733...`에서 이 형태로 고정했습니다. Activation 당시의 sketch가
  구현됐다는 주장은 아니며 실제 source/evidence가 현재 authority입니다.
- Request는 zero-invalid이며 exact target identity, forward-only direction과 deep-cloned ordered intent를 함께 소유합니다.
- `MigrationIntent`만 전달하면 SQLite/PostgreSQL static validators가 필요로 하는 migration identity와 direction을 잃으므로
  금지합니다.
- History-owned `AppliedMigration`/`HistoryTransition`을 renderer public API로 재사용하지 않습니다. Built-in private adapter가
  shared static validator를 호출하기 위해 apply transition을 내부에서 구성하는 것은 허용하되 SQL 계약으로 노출하지 않습니다.
- Private validator sharing이 PostgreSQL recorder row에만 필요한 255-byte identity limit을 pure renderer identity에 유출하지
  않게 합니다. Loader-owned current migration identity bounds가 authority입니다.
- Root는 loaded snapshot/graph/chronology/target-before reconstruction과 exactly-one request를 소유하고 renderer는 backend-specific
  static representability와 SQL projection만 소유합니다.
- Renderer nil/typed-nil/configuration은 complete load, graph/chronology, exact target와 historical materialization 뒤에 검사하며
  valid renderer method는 정확히 한 번만 호출합니다. Root는 request와 반환된 모든 statement를 detached clone으로 소유합니다.

### Execution capability와 compiler 공유

- Existing `MigrationCapabilities`는 schema mutation과 revision-fenced history가 같은 atomic execution unit이라는 의미를
  포함하므로 SQL rendering capability로 재사용하지 않습니다.
- `Executor.Plan`, `validateLoadedMigrationCapabilities`, backend opener/session/history/transaction/editor도 호출하지 않습니다.
- Renderer는 unsupported current operation을 existing backend capability taxonomy로 fail-closed할 수 있지만 raw custom cause를
  root/public wire에 보존하지 않습니다.
- Built-in renderer는 execution compiler helper를 추출·공유해 identifier quoting, field type, default와 relation projection이
  execution SQL에서 drift하지 않게 합니다. SQL collector를 mutation `SchemaEditor`에 추가하지 않습니다.
- Current packet은 declarative state만으로 SQL body가 결정되는 CreateModel과 AddField에 한정합니다. Required AddField SQL을
  projection하더라도 live table-empty/cardinality preflight를 통과했다거나 적용 가능하다고 주장하지 않으며 execute는 fresh
  live preflight를 다시 수행합니다. Physical catalog에서 remake plan을 만들어야 하는 SQLite ForeignKey RemoveField는 offline
  SQL로 흉내 내지 않고 stable capability failure/SQL zero bytes로 닫습니다.

### Project configuration과 backend coherence

- `project.Config`에는 direct immutable `MigrationSQLRenderer`가 추가되는 방향을 검토합니다. Opener/factory나 database handle은
  사용하지 않습니다.
- SQLite built-in은 credential-free zero configuration입니다. PostgreSQL built-in은 immutable schema name만 받으며
  URL/credential/connection을 받지 않습니다.
- SQLite constructor 후보는 `sqlite.NewMigrationSQLRenderer() backend.MigrationSQLRenderer`, PostgreSQL은
  `postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "..."}) backend.MigrationSQLRenderer`입니다.
  PostgreSQL constructor는 early error로 loader/target precedence를 뒤집지 않고 valid/invalid immutable value를 반환하며 invalid
  raw schema text를 error에 보존하지 않습니다.
- Typed-nil renderer는 method call 전에 fail-closed합니다.
- Supported built-in project는 one normalized project-owned backend selection에서 opener와 renderer를 함께 파생해야 합니다.
  Custom project가 둘을 서로 다른 backend/profile로 구성하지 않았다는 것은 framework가 증명하지 않으며 project owner의
  책임입니다.
- `project.Config` exported field 추가는 pre-release current-only source break가 될 수 있으므로 ADR-0055에 명시하고 external
  keyed/unkeyed literal compile 영향을 검증합니다.
- Article integration은 environment/profile을 request마다 두 번 읽지 않고 one frozen immutable selection/result에서 opener와
  renderer를 함께 구성합니다. `sqlmigrate`는 저장된 opener error를 관찰하거나 opener를 호출하지 않습니다.

### Statement와 output publication

- Renderer result hard ceiling 후보는 statement body 2,048개, aggregate body 16 MiB입니다. Root가 resource scan을 semantic
  scan보다 먼저 수행하고 exact limit/one-over를 검증합니다.
- `len(statements)`는 exact `len(intent.Operations)`와 같아야 합니다. Empty intent는 non-nil empty result와 public zero bytes입니다.
- 각 body는 valid UTF-8, non-empty, semicolon 없음, leading/trailing ASCII whitespace 없음이며 internal LF 외 Unicode control
  rune을 포함하지 않습니다. Root는 각 body를 복사합니다.
- Global owner만 각 body에 정확히 `;\n`을 붙입니다. Private response도 독립된 cap/canonical validation을 거칩니다.
- Private JSON wire의 worst-case escaping은 existing identity/output bound와 함께 최대 101 MiB 안에 들어야 하며 exact-limit과
  one-byte-over tests로 산술을 고정합니다.
- 전체 logical output을 private buffer에서 검증한 뒤 stdout에 정확히 한 번 write를 시도합니다. Zero bytes는 `Write`를
  호출하지 않습니다. Short write/error 뒤 retry나 두 번째 stderr publication은 없으며 이미 노출된 prefix를 회수할 수 있다고
  주장하지 않습니다.

### Error, cancellation과 DB-free의 정확한 의미

- Stable SQL-specific category/code 후보는 renderer unavailable, render failed, invalid rendered SQL과 rendered SQL resource limit입니다.
- Root SQL error taxonomy는 Phase B에서 stable category/code와 no-unwrap shape로 고정했습니다. Public exit mapping은 existing
  command family와 일치하도록 invalid argv/identity `2`, renderer unavailable/render failed/invalid rendered SQL `3`,
  capability unsupported/resource limit `1`을 제안하며 Phase C private/global protocol publication 전에 exact test로 닫습니다.
- Raw renderer error, partial statement, definition/source, URL/credential와 child stderr는 public error, `Unwrap`, protocol,
  log/artifact에 넣지 않습니다. Existing `migrations.Error`가 cause를 노출한다면 safe sentinel로 새로 매핑합니다.
- Root/built-in은 entry, operation 사이, renderer return과 output scan에서 context cancellation을 확인합니다. Custom renderer를
  goroutine으로 강제 중단하거나 cooperative cancellation 이상을 주장하지 않습니다.
- “DB-free”는 framework와 built-in renderer가 backend opener/session/history/recorder/transaction/schema editor를 호출하지 않고
  credential/handle을 보유하지 않는다는 뜻입니다. Project selection/build/source load, Go module cache/network/auth, user `init()`와
  custom renderer I/O까지 offline/sandboxed라고 뜻하지 않습니다.
- 출력 SQL은 applied state, physical schema/catalog, server profile, live data/cardinality, execution transaction/recorder atomicity나
  실제 성공을 증명하지 않습니다.

## Django reference와 contract 계획

- Exact reference authority는 pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`의 public
  `sqlmigrate` Command, `MigrationLoader.collect_sql`, `Migration.apply`와 collect-SQL schema editor 경계입니다. Django
  command의 terminal wrapper는 `BaseCommand.execute`가 소유하며 `MigrationExecutor`를 사용한다고 표현하지 않습니다.
- Django는 connection을 사용하고 prefix/backwards와 conditional transaction wrapper를 제공하지만 GoDj profile은 exact name,
  forward-only, built-in DB-free, wrapper-free입니다. Overlapping observation만 parity로 비교하고 나머지는 GoDj-owned decision으로
  분류합니다.
- MIG-131의 target-before state/forward operation order와 MIG-132의 portable SQLite Create/Add result semantics만 Django
  result-derived observation으로 둡니다. Identity-bearing request shape, raw SQL bytes, comments, transaction wrapper, exact-only argv,
  no-DB/output/error/resource/process boundary는 `godj.migration.sql_rendering.*` decision namespace로 분리합니다.
- Phase A가 실제 overlapping mismatch를 발견할 때만 새 deviation record/classification을 제안합니다. Existing
  `docs/DEVIATIONS.md`의 live aggregate mirror는 reference publication과 동기화할 수 있지만 activation은 deviation을
  선등록하지 않습니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-129 | `godj.migration.sql_rendering.argv_and_pre_io_rejection` | exact 두 argv; invalid/permuted/latest/reverse는 pre-project/build/renderer; `zero` literal |
| MIG-130 | `godj.migration.sql_rendering.complete_load_exact_lookup_and_request` | complete catalog/graph/chronology and exact lookup precede one identity-bearing deep-cloned request; prefix rejected, zero-value request invalid |
| MIG-131 | `django.migration.sql_rendering.forward_before_state_order` | result-only: target-before state, exactly one target migration and forward definition order |
| MIG-132 | `django.migration.sql_rendering.sqlite_create_add_semantics` | result-only: normalized SQLite CreateModel/AddField meaning/order; raw bytes/comments/wrapper excluded |
| MIG-133 | `godj.migration.sql_rendering.postgres_current_projection` | schema-qualified current projection from schema-only config; no URL/server/open |
| MIG-134 | `godj.migration.sql_rendering.canonical_deterministic_output` | repeat/parallel/fresh-process bytes, exact statement cardinality, one `;\n`, empty zero bytes |
| MIG-135 | `godj.migration.sql_rendering.database_and_history_zero_calls` | success/failure/cancel all keep opener/session/history/recorder/transaction/schema mutation at zero |
| MIG-136 | `godj.migration.sql_rendering.renderer_and_operation_fail_closed` | typed-nil, unsupported/custom/data/malformed renderer fail before logical SQL publication; reverse argv는 MIG-129 소유 |
| MIG-137 | `godj.migration.sql_rendering.resource_cleanup_redaction_and_write` | exact caps, child cleanup, stable redaction, one final write attempt and explicit short-write prefix non-claim |
| MIG-138 | `godj.migration.sql_rendering.external_project_configuration` | repository-external constructor to project.Config compile/config coherence and built-in no-DB proof; no custom-coherence/offline overclaim |

Activation에서는 MIG-129..138 모두 `planned, not run`이며 manifest/oracle/runner/product adapter가 없습니다. Product/reference
aggregate와 MIG-075..086 locked range는 바꾸지 않습니다. Q-010/Q-012는 이 bounded command가 완료돼도 broader
semver/upgrade/repair/destructive/custom/multi-DB 범위 때문에 `Partial`을 유지합니다.

Phase A source `c3de0d35b3dce4fd0a5c0dde4cc16b85923a14a0`, tree
`1af05572ed3a5f50219fe48b810262a37ca7f46f`은 MIG-129..138을 reference-only `oracle_locked`로 게시했습니다.
MIG-131/132만 pinned Django result authority이고 나머지 여덟 계약은 GoDj decision authority입니다. Manifest/NI/oracle은
각각 8,010/1,727/46,941 bytes, shared 24-line checksum은 2,279 bytes입니다. Reference는
27 sets/301 contracts/702 ordered cross-bindings=`254 passing + 25 deviation + 22 oracle_locked`, product는
25 adapters/279=`254 passing + 25 deviation`로 불변입니다. MIG-075..086과 MIG-129..138만 locked/unregistered이고
product adapter, deviation fixture 또는 `passing` 전환은 없습니다. Exact hash와 검증 증거는 EVID-173이 소유합니다.

Phase A artifact slug는 `migration-sql-rendering`이고 proposed roster는 manifest, ordered not-implemented fixture, pinned Django
oracle, independent scenario/decision runners와 protocol lock입니다. 모든 MIG-129..138을 reference-only `oracle_locked`로
추가하면 reference inventory는 산술상 27 sets/301 contracts/702 ordered cross-bindings=
`254 passing + 25 deviation + 22 oracle_locked`, product는 25 adapters/279=`254+25`로 불변입니다. Raw artifact size/hash,
semantic payload와 digest는 생성 후 independent A/B에서 측정하며 activation 수치로 추정하지 않습니다. Exact proposed oracle
filename을 current 23-line/2,177-byte `SHA256SUMS`에 append하면 checksum roster는 24 lines/2,279 bytes여야 하며 실제
publication에서 다시 측정·검증합니다. Phase A에는
deviation fixture, GoDj product adapter와 `passing` 전환을 만들지 않습니다.

## 단계

- [x] Phase A — pinned Django source/runtime authority audit와 MIG-129..138 reference-only artifact lock
- [x] Phase B — pure forward materializer, identity-bearing renderer port, shared compiler, config/error/output boundary
- [x] Phase C — strict private/global protocol과 repository-external SQLite exact CLI/no-DB/redaction product flow
- [x] Phase D — PostgreSQL current profile, oracle-blind actual/policy와 source-bound attestation publication
- [ ] Phase E — affected/full milestone, Linux/386, external archive, exact submitted-head Hosted와 terminal docs

Phase A는 focused Django/decision/runner/protocol/artifact gates만 수행하고 full product/Hosted acceptance로 과장하지 않습니다.
Makefile/workflow lock을 바꾼 뒤 Phase D source freeze에서 PostgreSQL source-bound attestation을 independent A/B로 재캡처했습니다.
Phase E에서 behavioral source, workflow 또는 lock byte가 다시 바뀌면 그 capture를 재사용하지 않고 새 freeze에서 재캡처합니다.
Phase A의 exact stale-attestation failure는 expected diagnostic으로 보존하며 다른 assertion failure와 혼동하지 않습니다.
CI #191의 exact 53-job topology는 이 packet에서 유지합니다. 장시간 child build의 중복을
측정·계층화하는 CI 최적화는 별도 bounded packet 후보이며 GDJ-0054 semantics나 activation에 섞지 않습니다.

## 현재 checkpoint

- Phase A source `c3de0d35b3dce4fd0a5c0dde4cc16b85923a14a0`, tree
  `1af05572ed3a5f50219fe48b810262a37ca7f46f`의 exact artifact와 reference aggregate는 EVID-173이 소유합니다.
- Phase B source는 `f51ab7339753508ed070a8ba6da1c917cb3ce392`, tree
  `ab71e8a09cbddb01c4d0054d955ad722af9db6d9`입니다. Public `RenderMigrationSQL`은 complete loaded catalog와 exact target의
  dependency-before state에서 detached exactly-one forward intent를 만들고 identity-bearing renderer를 정확히 한 번 호출합니다.
- SQLite/PostgreSQL immutable built-in renderer는 execution compiler와 static validation을 공유하되 opener/session/history/
  recorder/transaction/schema editor와 URL/credential/handle을 소유하지 않습니다. PostgreSQL config는 schema-only이고 invalid
  configuration은 complete-load/lookup/materialization 뒤 redacted failure로 닫힙니다.
- Root는 nil/typed-nil, context, exact statement cardinality, 2,048 statement/16 MiB aggregate cap, UTF-8/canonical body와 raw
  cause/partial SQL redaction을 검증합니다. External compile contract는 built-in/custom keyed `project.Config` assignment와 이전
  four-field unkeyed literal의 의도된 source break를 함께 고정합니다.
- Affected normal/race/CGO-disabled/vet, all-package compile-only, repeated determinism과 focused external compile이 통과했습니다.
  독립 감사에서 blocking P0/P1/P2는 없었고 loader identity, context checkpoint, sealed FK target/compiler 공유와 recorder-only
  identity limit 분리를 source/tests로 보강했습니다. Exact 명령과 non-claim은 EVID-174가 소유합니다.
- Phase C source는 `a304a73d80b0512b6594d512938874e2456d1292`, tree
  `f8df2d405152eade4f5748c2f36398ca2e87b75a`입니다. Strict private v1 wire와 exact two-form global CLI, one final
  publication owner, Article의 one-frozen-selection opener/renderer configuration, repository-external SQLite public-only project를
  구현했습니다. Complete catalog와 exact lookup은 renderer보다 먼저이고 success/failure/cancellation에서 DB opener/history/
  recorder/transaction/schema mutation은 zero입니다.
- Phase C external product는 implicit/explicit selection, exact/prefix/zero/invalid argv, malformed unrelated catalog precedence,
  deterministic repeat/parallel fresh process, partial SQL·secret·path redaction, process reap와 scratch/source immutability를 검증합니다.
  Affected normal/race/CGO-disabled/vet, all-package compile-only, Linux/386 cross-compile proxy, protocol count-10, generated drift와
  independent audit가 통과했고 exact 명령과 non-claim은 EVID-175가 소유합니다.
- Phase D product source는 `a85ade1ee6a183fee504a21505798914abf6fa43`, tree
  `211e1ad86067be8d1d1fc119b592e3e87407c064`이고 checked attestation publication descendant는
  `9603cc6568f9f41046e3a32909b3e4e7316a2914`, tree `d6fa714a04f11e02fc6e5ff5a5405d37f972227b`입니다. PostgreSQL
  schema-only current-profile projection, actual external child cancellation/reap, oracle-blind actual/policy와 compile-valid
  false-green mutations을 게시했습니다.
- MIG-129..138은 모두 registered `passing`입니다. Reference는 27 sets/301 contracts/702 ordered cross-bindings=
  `264 passing + 25 deviation + 12 oracle_locked`, product는 26 adapters/289 contracts=`264 passing + 25 deviation`이며
  MIG-075..086만 reference-only locked입니다. 새 deviation fixture는 없습니다.
- Source-bound PostgreSQL attestation A/B는 각각 1,134 bytes/SHA-256
  `ac17dbf2ac348ec11e7f28d7f2f98d424bf6313b7a6411d2cc752de085cf6cb7`로 동일했고 source binding은
  274 files/3,557,167 payload bytes/SHA-256 `5cb50f0c3b833d09a6a42edb0180fc6b6bc8800a8d6eec2596702ba38afc9dc8`입니다.
  Affected normal/race/CGO-disabled/vet, actual comparator, checksum, protocol과 independent publication/source-binding audit가
  통과했습니다. Exact 명령과 non-claim은 EVID-176이 소유합니다.
- ADR-0055는 계속 Proposed이고 GDJ-0054는 active입니다. Full `make ci`, native Linux/386, `.git`-free external archive와 exact
  submitted-head Hosted는 아직 주장하지 않습니다. 다음 정확한 작업은 existing 53-job topology를 유지한 Phase E frozen
  local/external/Hosted acceptance입니다.

## 완료 조건

- MIG-129..138 reference artifact와 oracle-blind GoDj product adapter가 같은 reviewed observation을 통과합니다.
- SQLite/PostgreSQL built-in output이 execution compiler projection과 drift하지 않고 DB/history/transaction trace가 zero입니다.
- Repository-external public project가 exact two CLI forms, config constructors, deterministic bytes, failure redaction과 child cleanup을
  통과합니다.
- Affected normal/race/CGO-disabled/vet, full frozen local milestone, Linux/386 compile-only, `.git`-free external archive,
  source-bound attestation A/B와 exact-head Hosted가 실제 current bytes에서 통과합니다.
- MIG-129..138 product `passing`은 Phase D actual 증거 뒤에만 전환합니다. ADR-0055 Accepted, work completed와 Phase E
  terminal 상태는 exact-head Hosted 증거 뒤에만 전환합니다.
