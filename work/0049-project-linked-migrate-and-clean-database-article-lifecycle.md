---
id: GDJ-0049
status: active
updated: 2026-08-29
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "3fdb1c774b1c04d7db800f35ce9a7d714b1d973f"
depends_on: ["GDJ-0036", "GDJ-0038", "GDJ-0042", "GDJ-0045", "GDJ-0046", "GDJ-0048"]
contracts: ["MIG-087..MIG-098", "Q-010", "Q-012", "Q-019"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "cmd/godj/**"
  - "project/**"
  - "internal/projectcheck/**"
  - "internal/compiletest/**"
  - "examples/article/**"
  - "conformance/contracts/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/fixtures/**"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/**"
  - "conformance/projectcheck/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/internal/protocol/**"
  - "conformance/projectmigrateproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/runserverproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "docs/adr/0035-pre-release-current-only-format-and-generated-publication.md"
  - "docs/adr/0051-project-linked-explicit-migrate.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0049-project-linked-migrate-and-clean-database-article-lifecycle.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0049 — Project-linked Migrate and Clean-database Article Lifecycle

## 사용자에게 보이는 결과

GoDj project root에서 명시적으로 migration을 적용한 뒤 같은 database를 `runserver`로 열 수 있습니다.

```text
godj migrations check
godj generate --check
godj migrate
godj runserver
```

첫 범위의 공개 argv는 exact 두 형식입니다.

```text
godj migrate
godj migrate --project ./godj.toml
```

`migrate`는 loaded definitions의 latest leaves까지 수렴합니다. 두 번째 fresh-process invocation은 성공적인 no-op이고,
`runserver`는 계속 generate 또는 migrate를 암묵적으로 수행하지 않습니다.

## 목표

- Existing project descriptor의 declaration `package`를 project-owned migration runner로 재사용합니다.
- Global CLI는 DB URL/driver/secret을 파싱하거나 출력하지 않고 project child가 backend configuration과 open을 소유합니다.
- Public `project.Config`에 copied embedded definition source와 lazy backend opener를 additive하게 연결합니다.
- Strict migrate 전용 private protocol에서 definition load 1회 → backend open 1회 →
  `Executor.Migrate(..., LatestLifecycleRequest())` 1회 → backend close 1회를 보장합니다.
- Invalid definition은 backend open 전에, graph/state/capability/history 오류는 migration begin 또는 schema mutation 전에
  기존 core taxonomy로 fail-closed합니다.
- Commit unknown, rollback/session/backend cleanup failure를 안전한 성공 또는 재시도 가능 오류로 축약하지 않습니다.
- SIGINT/SIGTERM에서 core의 순차 rollback/session-close cleanup 두 구간과 outer close/response margin을 포함한 15초 grace 동안
  child 종료를 관찰하고, 필요한 경우에만 force-kill합니다.
- Article의 system-state와 domain migration을 clean SQLite file 및 clean PostgreSQL schema에서 적용하고,
  Admin/API CRUD와 별도 process restart까지 검증합니다.
- 새 oracle-blind MIG-087..098 decision/product contracts를 current-only lifecycle 위에 게시합니다.

## 이번 packet에서 고정하는 경계

### Public project API

Phase A에서 동결한 public shape는 다음처럼 additive합니다. Exact 이름과 compile surface는 public contract test가 고정합니다.

```go
type MigrationBackend interface {
    migrationbackend.RevisionFencedBackend
    Close() error
}

type Config struct {
    MigrationDefinitionRoots   []string
    MigrationDefinitionSources []definition.Source
    LoadProjectSpec            func(context.Context) (codegen.ProjectSpec, error)
    OpenMigrationBackend       func(context.Context) (MigrationBackend, error)
}
```

- Config 진입에서 roots와 source ID/document를 deep-copy합니다.
- `MigrationDefinitionSources`는 `systemstate.InitialDefinitionSource()`를 project별 JSON 복제 없이 주입합니다.
- Static sources는 existing `migrations check`와 새 `migrate` 양쪽의 동일 catalog에 포함되어 source/definition/digest가
  일치합니다. Check는 이 sources를 pure load하되 backend opener는 호출하지 않아 계속 DB-free입니다.
- `OpenMigrationBackend`는 callback입니다. Existing `migrations check`, generate와 runserver는 이를 호출하지 않습니다.
- Opener가 non-nil backend와 error를 함께 반환하면 runner가 부분 획득한 backend를 정확히 한 번 닫고 open failure를 primary로
  유지합니다. Raw opener cause는 게시하지 않고 `migration_backend_error/backend_open_failed`로 닫습니다. Nil/typed-nil backend는
  닫지 않으며 nil error와 함께 반환되면 `migration_backend_error/invalid_backend`입니다.
- `migrate_package` descriptor field를 추가하지 않습니다. Existing `[project].package`가 migration config를 소유합니다.

### Global/private orchestration

```text
global argv validation
→ retained project selection
→ private workspace build
→ separate migrate private argument/protocol
→ copied static + discovered file source snapshot
→ definition.Load exactly once
→ OpenMigrationBackend exactly once
→ Executor.Migrate(LatestLifecycleRequest) exactly once
→ acquired non-nil backend.Close exactly once
→ bounded sanitized response
```

- 기존 project-check private protocol bytes와 DB-free invariant는 바꾸지 않습니다.
- Migrate protocol은 version 1의 closed request/response이며 duplicate/unknown/trailing/non-UTF8/oversize/short-write를 거부합니다.
- Definition set summary는 source/definition count와 digest처럼 이미 계산 가능한 bounded 값만 반환합니다.
  “이번에 적용한 migration 수”를 추측하거나 이를 위해 추가 history query를 만들지 않습니다.
- Global process는 DSN, SQL, definition document와 raw cause를 argv, wire, stdout/stderr 또는 artifact에 게시하지 않습니다.
  Project build toolchain은 기존처럼 ambient child env를 받는 trusted local boundary입니다.

### Transaction, cleanup and exit ownership

- 전체 catalog가 하나의 transaction은 아닙니다. Existing lifecycle대로 migration step마다 fenced transaction 하나입니다.
- Later step failure/cancellation은 이미 committed된 prefix를 되돌리지 않습니다. Fresh invocation은 durable prefix에서 재개합니다.
- Commit unknown은 rollback/retry하지 않고 즉시 closed terminal failure입니다.
- Commit committed + cleanup error, backend close failure, response/write 또는 workspace cleanup failure는 DB가 이미 전진했을 수
  있으므로 nonzero 뒤 사용자가 fresh `godj migrate`로 reconciliation합니다. CLI는 자동 retry하지 않습니다.
- Joined error tree는 최소 `commit_outcome_unknown` → non-nil `migrations.Error.RollbackCause` →
  `commit_cleanup_failed` → `session_close_failed` → primary category/code 순서로 분류합니다.
- Rollback failure는 primary operation code로 축약하지 않고 CLI 전용 closed
  `migration_transaction_error/rollback_failed`로 게시합니다.
- Outer backend close failure는 기존 opener/core failure를 덮어쓰지 않고 bounded `cleanup_failed` observation으로 함께 보존합니다.
  선행 failure가 없을 때만 closed `migration_backend_error/backend_close_failed`가 primary가 됩니다.
- Existing short-process 2초 unconditional kill은 write-capable migrate에 재사용하지 않습니다. Child exit-aware grace는 core의
  rollback 5초와 deferred session close 5초를 합한 상한보다 엄격히 길어야 하며 acceptance target은 outer close/response를 위한
  5초 margin을 더한 15초입니다. Grace 만료 뒤 force-kill은 misbehaving project opener/closer의 ultimate bound입니다.
- Migrate private invocation의 `project.Run`이 SIGINT/SIGTERM을 cancellation context로 변환하고 등록을 반환 시 해제합니다.
  Canonical runner가 `context.Background()`를 전달해도 OS signal이 core cleanup을 건너뛰는 기본 종료로 처리되지 않습니다.

### Article ownership

- Stable project migration root는 `examples/article/migrations`입니다.
- Article project runner는 file definition과 `systemstate.InitialDefinitionSource()`를 조합합니다.
- SQLite/PostgreSQL environment parsing과 backend open은 site와 runner가 공유하는 외부 import 가능한 Article-owned
  `examples/article/databaseconfig` package가 소유하며 오류에는 environment key만 포함하고 값은 포함하지 않습니다.
- PostgreSQL의 clean database는 test harness가 준비한 empty application schema입니다. Server/database/schema provisioning과
  production credential distribution은 이 command의 책임이 아닙니다.

## 계약 계획

Activation 시점의 MIG-087..098 exact 12 `planned, not run`은 Phase A artifact publication으로 모두 `oracle_locked`가
됐습니다. 실제 product adapter가 같은 observation을 만족한 뒤에만 `passing`으로 전환합니다. Expected deviation은 0입니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-087 | `fresh_latest` | 빈 SQLite DB가 exact latest schema/history/digest로 수렴 |
| MIG-088 | `applied_prefix_tail` | durable prefix를 보존하고 remaining tail만 실행 |
| MIG-089 | `fully_applied_fresh_noop` | 두 번째 fresh invocation은 write/history duplication 없는 success |
| MIG-090 | `definition_preflight_before_backend` | invalid/current-unknown definition은 backend open 0 |
| MIG-091 | `inconsistent_history_preflight` | inconsistent history는 migration begin/schema mutation 0 |
| MIG-092 | `capability_preflight_before_begin` | unsupported required capability는 begin/record/schema mutation 0 |
| MIG-093 | `middle_failure_durable_prefix` | earlier commit만 durable, current rollback, tail 미실행 |
| MIG-094 | `fresh_resume_after_failure` | fresh invocation이 failure point부터 latest로 수렴 |
| MIG-095 | `commit_outcome_unknown` | structured unknown, retry/rollback/success publication 0 |
| MIG-096 | `concurrent_latest_fenced` | two-child execution에서 duplicate/corrupt history 0, fresh reconciliation 가능 |
| MIG-097 | `backend_configuration_secret_boundary` | missing/invalid config는 closed code이고 secret occurrence 0 |
| MIG-098 | `interrupt_rollback_cleanup` | interrupt 후 rollback/session/backend close, direct child reap와 residue 0 |

이 contract set은 GoDj global/project orchestration decision입니다. 새 Django execution probe를 만들지 않고 independent
oracle-blind decision artifact를 사용합니다. Retired MIG-075..086 profile/digest/state fixture나 runner를 재사용·부활시키지 않습니다.

## 비목표

- Migration writer/autodetector, `makemigrations`, rename detection, custom/data operations
- Named/zero/reverse target, `--plan`, `--fake`, `--check`, adoption, multi-DB alias/router
- Schema IR, Migration Definition/State format, planner/executor/backend 또는 generated ABI 변경
- `runserver`의 implicit generate/migrate/retry와 file-watching reload
- General Unique/Constraint/IntegerField 추가, broader relation DDL, MySQL/production deployment
- Q-019 retained-resource lifetime 정책 변경; current no-retry/close ownership만 보존
- JWT/OAuth/OpenAPI/Realtime/GIS 또는 production secret manager

## 구현 단계

### Phase A — decision contracts and public compile boundary

- [x] Proposed ADR-0051과 exact public/private/error/cancellation 경계를 활성화
- [x] MIG-087..098 independent decision manifest/oracle/payload-free NI를 `oracle_locked`로 게시
- [x] Existing project-check protocol byte identity와 public Config compile contract를 추가
- [x] Retired `migration-relation` manifest, NI fixture, oracle와 Django/GoDj runner exact 11-file bytes가 바뀌지 않는 no-diff gate 추가:
  `conformance/contracts/migration-relation-manifest.json`,
  `conformance/fixtures/godj-migration-relation-not-implemented.json`,
  `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json`,
  `conformance/runners/django/migration_relation_scenarios.py`,
  `conformance/runners/django/tests/test_migration_relation_scenarios.py`,
  `conformance/runners/django/migration_relation_fixture/__init__.py`,
  `conformance/runners/django/migration_relation_fixture/apps.py`,
  `conformance/runners/django/migration_relation_fixture/migrations/__init__.py`,
  `conformance/runners/django/migration_relation_fixture/migrations/0001_initial.py`,
  `conformance/runners/django/migration_relation_fixture/migrations/0002_nullable_relation.py`,
  `conformance/runners/godj/migration_relation_scenarios.go`

### Phase B — linked migrate kernel and global owner

- [x] copied source discovery/load-before-open와 one-open/one-migrate/one-close kernel 구현
- [x] joined rollback/cleanup/unknown precedence와 bounded secret-free response 구현
- [x] exact argv, deleted-CWD pre-discovery rejection와 strict private protocol 구현
- [x] 15초 child-exit-aware interrupt grace, signal-to-context owner, direct reap와 conditional force-kill 구현

### Phase C — Article clean SQLite vertical

- [x] Article stable migration root와 shared externally importable `examples/article/databaseconfig`를 project runner/site에 연결
- [x] fresh → latest, pre-applied prefix → tail과 second fresh-process byte-identical no-op 검증
- [x] externally held SQLite write lock 아래 actual child 두 개의 closed contention과 fresh reconciliation 검증
  (partial MIG-096 observation이며 child-vs-child winner/fence 증명은 아님)
- [x] explicit migrate 뒤 unauthenticated Article read와 두 fresh runserver process 사이 row persistence 검증
- [x] runserver-before-migrate 500과 migration mutation 0을 보존
- [x] middle failure의 durable prefix와 fresh-process resume 검증
- [x] 실제 child-vs-child overlap/winner를 증명하는 full MIG-096 검증
- [x] explicit migrate 뒤 authenticated Admin/API CRUD와 distinct-process restart durability 검증

### Phase D — PostgreSQL and product publication

- [x] digest-pinned PostgreSQL 17.10 clean-schema latest/no-op/contention/restart 검증
- [x] stdout/stderr/wire/artifact credential canary occurrence 0과 required skip 0 검증
- [x] MIG-087..098 product actual을 등록하고 exact `passing`으로 전환

### Phase E — frozen milestone

- [x] affected normal/race/CGO0/vet와 generated drift를 checkpoint마다 통과
- [x] nested product gate 격리와 conformance-validation 45분 budget 교정을 final source 전에 고정
- [x] frozen source에서 required PostgreSQL, full `make ci`, Linux/386, repository-external archive와 independent audit 한 번 수행
- [x] 첫 exact submitted-head `8841319...` / CI run `33124180742`의 23 success, 1 package-timeout failure,
  3 job-timeout cancellation을 제품 assertion 실패와 구분해 감사
- [x] Hosted correction source에서 PostgreSQL exact required selector, conformance normal/race/CGO0/artifact 분리,
  relation 좌표별 mode 분리와 coverage 기반 workflow lock을 구현하고 source-bound PostgreSQL attestation을 재캡처
- [x] Local refreeze에서 드러난 canceled runner metric test의 child-ready/parent-capture 경쟁을 test-only capture
  acknowledgment로 교정하고 focused normal/race 반복을 통과
- [ ] corrected exact submitted-head hosted matrix 뒤 ADR/status/evidence를 terminal bytes에 맞게 갱신

## 완료 조건

- 사용자가 clean SQLite/PostgreSQL project에 명시적 `godj migrate`를 실행하고 Article Admin/API를 재시작 뒤에도 사용할 수 있습니다.
- Invalid definition은 DB open 전 실패하고 history/capability 오류는 schema mutation 전에 실패합니다.
- No-op, durable-prefix resume, concurrent fence와 commit unknown/no-retry가 이름 있는 tests/contracts로 고정됩니다.
- Rollback/session/backend close와 child process cleanup이 실패 또는 interruption 뒤에도 과장된 success를 게시하지 않습니다.
- DB credential은 global argv/protocol/output/artifact에 나타나지 않습니다.
- Existing `migrations check`, generate, runserver와 project-check protocol behavior는 회귀하지 않습니다.
- MIG-075..086과 Migration core/IR/format bytes는 바뀌지 않습니다.

## 인수인계

- Phase A reference commit은 `2248c982dfafbbd86d04ee2a901c77f4b3ac6d11`, tree
  `8a86d18a87387a03f8a7747d00fb930e7aaab9b8`이고 MIG-087..098을 reference-only `oracle_locked`로 게시했습니다.
- Phase B와 partial SQLite source checkpoint는 `d31a8b8ca0cc4503aed13616fe654c6c613e2441`, tree
  `c2572a63100ebb91aa52a185cb7470e71d380f32`입니다. Public/linked/global migrate, process cleanup arbitration,
  Article stable root/shared config와 black-box latest/no-op/partial contention/read restart를 포함합니다.
  전체 범위와 실제 명령/non-claim은
  [EVID-143](../docs/status/TEST_EVIDENCE.md#evid-20260828-143--gdj-0049-activation-hosted-and-phase-ab-sqlite-source-checkpoint)에
  기록했습니다.
- SQLite lifecycle source `d160ea4...`, PostgreSQL lifecycle source `e3cee0d...`, product publication source
  `c5af15e...`와 source-bound attestation publication `dc3861f...`에서 Phase C/D 및 frozen local gate를 닫았습니다.
  MIG-087..098은 registered product `passing`이고 current aggregate는 reference 23/261/506=
  `230 passing + 19 deviation + 12 oracle_locked`, product 22/249=`230 passing + 19 deviation`입니다. 남은 locked
  range는 diagnostic MIG-075..086뿐입니다.
- [EVID-144](../docs/status/TEST_EVIDENCE.md#evid-20260828-144--gdj-0049-frozen-local-final-and-source-bound-postgresql-publication)는
  required PostgreSQL 17.10, affected normal/race/CGO0/vet/generate, relation 1,091/1,091/0, full `make ci`,
  Linux/386, 1,126-file repository-external archive와 independent source/security audit를 기록합니다.
- 첫 exact submitted-head `8841319...`의 CI run `33124180742`는 27 executions 중 23 success, 1 failure,
  3 cancelled로 끝났습니다. PostgreSQL failure는 전체 `projectmigrateproduct` package를 잘못 선택해 SQLite product까지
  실행한 900.016초 package timeout이고, 두 relation-product cancellation은 20분 job cap, conformance cancellation은
  직렬 `make ci`의 45분 job cap입니다. 수집된 로그의 assertion/panic 실패 표식은 0이지만 중단된 test는 미검증이며
  green으로 재사용하지 않습니다.
- PostgreSQL normal/race/CGO0는 exact required 20-test selector와 exact run/pass/no-skip inventory로 분리했고,
  Hosted `make ci`는 artifact-contract-386과 portable normal+vet/race/CGO0로, relation은 네 좌표×세 mode로
  분리했습니다. Relation test 15분/job 20분, PostgreSQL 세 mode job 25분을 적용하며 exact job count 대신 모든
  top-level job의 `required-ci` dependency coverage를 protocol lock으로 검증합니다. Corrected workflow/Makefile source의
  PostgreSQL attestation은 digest-pinned Linux/amd64 Go 1.26.5와 PostgreSQL 17.10에서 두 번 byte-identical하게
  재캡처했습니다. CI correction은 `3bff0097b8708445d38b85659c3a34ad681f2078`, canceled runner test-only
  synchronization correction은 `caecf5115b0902d37dd3c525902d26edcc82b69c`입니다. 첫 local refreeze `make ci`는
  기존 test ready 신호가 parent capture를 보장하지 않아 한 번 실패했고, 해당 race 교정 뒤 focused normal 100회와
  race 20회를 통과했습니다. 전체 `make ci`를 CI 완벽화 목적으로 반복하지 않았으며 [EVID-145](../docs/status/TEST_EVIDENCE.md#evid-20260829-145--gdj-0049-hosted-timeout-correction-and-local-test-synchronization)에
  실패와 non-claim을 남겼습니다. 현재 정확한 다음 작업은 corrected exact head를 제출하는 것입니다.
  그 전까지 ADR-0051은 Proposed, GDJ-0049는 active이고 terminal 승격이나 다음 packet 활성화를 하지 않습니다.
- 같은 public `project.Config`, global CLI dispatch, contract manifest/registry와 CURRENT는 통합 담당 한 명만 수정합니다.
- Full hosted/evidence cycle은 subtask마다 반복하지 않고 final frozen source에서 한 번 수행합니다.
